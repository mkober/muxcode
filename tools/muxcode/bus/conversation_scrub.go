package bus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

// The conversation road (MUX-203 Phase 3): a PII-sensitive role's tool output
// is redacted before its agent's model reads it, by the mechanism each
// provider road allows (Decision 1). Claude: a synchronous PostToolUse hook
// answers with updatedToolOutput — exit 0 only, since a non-zero exit fires
// PostToolUseFailure, which cannot replace a result. Codex hook road: the
// PreToolUse guard rewrites the command so its output passes through
// `muxcode pii-scrub` before Codex records or shows it, on both outcomes.
// OpenCode: a tool.execute.after plugin. The Codex scrape road runs no hook
// and is not covered. Every road scrubs a result whole, never line by line:
// the label rules allow whitespace, newlines included, around their = or :.

// ClaudeScrubSettings is the --settings value a PII-sensitive Claude agent is
// launched with: `muxcode hook scrub` as a synchronous PostToolUse Bash hook.
// It rides the launch rather than ~/.claude/settings.json because flag
// settings add to the user's hooks instead of replacing them (verified on
// claude 2.1.293), apply on every launch with no reinstall, and spare every
// other role a synchronous hook on each Bash call.
const ClaudeScrubSettings = `{"hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"muxcode hook scrub"}]}]}}`

// ConversationScrubRole reports whether role's own conversation is scrubbed:
// a PII-sensitive role, a spawn worker judged by its base role.
func ConversationScrubRole(session, role string) bool {
	return IsPIISensitiveRole(SpawnBaseRole(session, role))
}

// ConversationScrub is text as a role's model should read it: redacted by
// ScrubForRole under the PIIScrubNotice for a PII-sensitive role when anything
// matched, and byte-for-byte unchanged otherwise — so a caller replaces a
// result only when there is something to hide.
func ConversationScrub(session, role, text string) (string, int) {
	if !ConversationScrubRole(session, role) {
		return text, 0
	}
	if scrubbed, n := ScrubForRoleWithNotice(role, text); n > 0 {
		return scrubbed, n
	}
	return text, 0
}

// ClaudeScrubAnswer is the synchronous PostToolUse answer for a Claude Bash
// result on a PII-sensitive role: the tool's own response with stdout and
// stderr redacted by ScrubForRole and the PIIScrubNotice heading stdout. The
// response is edited in place, every other field kept, because Claude drops an
// updatedToolOutput that does not match the tool's output schema and shows the
// original (verified on claude 2.1.293). ok is false — answer nothing — for
// any other role, event or tool, a Codex-shaped payload, or a clean result.
func ClaudeScrubAnswer(role string, payload []byte) (answer string, ok bool) {
	if !ConversationScrubRole(BusSession(), role) {
		return "", false
	}
	var ev struct {
		Event    string                     `json:"hook_event_name"`
		Tool     string                     `json:"tool_name"`
		Response map[string]json.RawMessage `json:"tool_response"`
	}
	if json.Unmarshal(payload, &ev) != nil || ev.Event != "PostToolUse" || ev.Tool != "Bash" || ev.Response == nil {
		return "", false
	}
	total := 0
	for _, field := range []string{"stdout", "stderr"} {
		var text string
		if json.Unmarshal(ev.Response[field], &text) != nil {
			continue
		}
		scrubbed, n := ScrubForRole(role, text)
		if n > 0 {
			total += n
			ev.Response[field], _ = json.Marshal(scrubbed)
		}
	}
	if total == 0 {
		return "", false
	}
	var stdout string
	_ = json.Unmarshal(ev.Response["stdout"], &stdout)
	ev.Response["stdout"], _ = json.Marshal(PIIScrubNotice(total) + stdout)
	data, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "PostToolUse",
			"updatedToolOutput": ev.Response,
		},
	})
	return string(data), true
}

// scrubWrapTail reads the pipeline's two statuses — bash's PIPESTATUS or
// zsh's pipestatus, whichever is set, so $1 is the command's and $2 the
// scrubber's in either shell — and exits with the command's, or with 125 and a
// notice when the scrubber failed and the output is gone. The expansions stay
// unquoted: zsh turns a quoted unset array into one empty word, which shifted
// both statuses there; unquoted, the unset one is no word in either shell.
const scrubWrapTail = `; set -- ${PIPESTATUS[@]} ${pipestatus[@]}; ` +
	`[ "$2" = 0 ] || { echo "[muxcode pii-scrub failed (scrubber exit $2, command exit $1): output withheld rather than shown unscrubbed]"; exit 125; }; ` +
	`exit "$1"`

// scrubWrapRe matches a WrapForScrub rewrite, capturing the command inside.
var scrubWrapRe = regexp.MustCompile(`(?s)^\{ (.*)\n\} 2>&1 \| muxcode pii-scrub --role [a-z0-9_-]+` + regexp.QuoteMeta(scrubWrapTail) + `$`)

// WrapForScrub rewrites command so its stdout and stderr pass through
// `muxcode pii-scrub --role role` before Codex sees them. The scrubber takes
// the whole output at once, as the history row's scrub does, because a label
// and its value can sit on different lines. The wrap exits with the command's
// own status rather than setting pipefail, which would change the status of
// every pipeline inside the command (`false | true` would fail); a failed
// scrubber is reported, never passed off as the command's success. The newline
// before the closing brace lets a trailing comment or heredoc terminator end
// the command intact. Output reaches Codex when the command ends, so one
// killed at its timeout shows nothing.
func WrapForScrub(command, role string) string {
	return "{ " + command + "\n} 2>&1 | muxcode pii-scrub --role " + role + scrubWrapTail
}

// UnwrapScrub returns the command inside a WrapForScrub rewrite, and whether
// command was one, so history, chains and command_match read what the agent
// sent rather than the wrapper.
func UnwrapScrub(command string) (string, bool) {
	if m := scrubWrapRe.FindStringSubmatch(command); m != nil {
		return m[1], true
	}
	return command, false
}

// openCodeScrubPluginPath sits beside the agent files WriteAgentConfig writes.
var openCodeScrubPluginPath = filepath.Join(".opencode", "plugin", "muxcode-scrub.ts")

// openCodeScrubPlugin replaces a bash result with `muxcode pii-scrub --role
// $AGENT_ROLE`'s answer, which decides whether the role is sensitive and
// leaves the text alone when there is nothing to hide. It is project-scoped,
// so it loads in every OpenCode process in the repo and stays inert outside a
// muxcode session. A scrub that fails — muxcode missing, a timeout, output
// past the buffer — withholds the result rather than show it unscrubbed, for
// every role, since only muxcode can say which roles are sensitive.
// metadata.output is what the TUI renders, so it is replaced too. The source is
// plain JavaScript, valid TypeScript, so a test can run it under node. Verified
// on opencode 1.18.34 with a stand-in muxcode: the model read the replaced
// output on a non-zero exit, while opencode.db keeps the streamed original.
const openCodeScrubPlugin = `// Written by muxcode before every OpenCode launch (MUX-203); edits are overwritten.
import { spawnSync } from "child_process"

export const MuxcodeScrub = async () => ({
  "tool.execute.after": async (input, output) => {
    if (input.tool !== "bash" || !process.env.BUS_SESSION || typeof output.output !== "string") return
    const r = spawnSync("muxcode", ["pii-scrub", "--role", process.env.AGENT_ROLE ?? ""], {
      input: output.output,
      encoding: "utf8",
      timeout: 30000,
      maxBuffer: 64 * 1024 * 1024,
    })
    const text = r.status === 0 && typeof r.stdout === "string"
      ? r.stdout
      : "[muxcode pii-scrub failed (" + (r.error ? r.error.message : "exit " + r.status) + "): output withheld rather than shown unscrubbed]\n"
    if (text === output.output) return
    output.output = text
    if (output.metadata && typeof output.metadata.output === "string") output.metadata.output = text
  },
})
`

// writeOpenCodeScrubPlugin writes the OpenCode road's plugin, so it always
// matches the binary that launched the agent.
func writeOpenCodeScrubPlugin() error {
	if err := os.MkdirAll(filepath.Dir(openCodeScrubPluginPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(openCodeScrubPluginPath, []byte(openCodeScrubPlugin), 0o644)
}

// CodexScrubWrapAnswer is the PreToolUse answer that runs an allowed Bash call
// wrapped, for a hook-road Codex agent of a PII-sensitive role. Codex takes an
// updatedInput only with permissionDecision allow; the allow bypasses nothing,
// since a PII-sensitive role runs `-a never` and keeps its sandbox. ok is false
// for any other provider, role or tool.
func CodexScrubWrapAnswer(session, role string, p Provider, ev *ToolEvent) (string, bool) {
	base := SpawnBaseRole(session, role)
	if p == nil || p.Name() != "codex" || ev.ToolName != "Bash" || ev.ToolInput.Command == "" || !IsPIISensitiveRole(base) {
		return "", false
	}
	data, _ := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":      "PreToolUse",
			"permissionDecision": "allow",
			"updatedInput":       map[string]string{"command": WrapForScrub(ev.ToolInput.Command, base)},
		},
	})
	return string(data), true
}
