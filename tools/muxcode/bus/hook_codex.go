package bus

import (
	"bytes"
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The Codex hook dialect (MUX-159), verified against codex-cli 0.153.4 on
// 2026-09-08 (fixtures under testdata/codex-hooks/):
//
//   - PreToolUse/PostToolUse name the tool `Bash` (shell and unified exec) or
//     `apply_patch`; tool_input.command holds the shell command — or, for
//     apply_patch, the whole patch text, with absolute paths.
//   - PostToolUse tool_response is a bare string: stdout for Bash, with NO
//     exit code; for apply_patch the exec-style text "Exit code: 0\n…".
//   - The rollout transcript named by transcript_path records every tool call
//     as an item_completed event keyed by the same tool_use_id, with the real
//     exit_code and status — the only place a shell command's status lives.
//   - Stop carries stop_hook_active and last_assistant_message; answering
//     {"decision":"block","reason":…} continues the agent with reason as its
//     next prompt, and the continuation's own Stop has stop_hook_active=true.
//   - UserPromptSubmit carries prompt; hookSpecificOutput.additionalContext
//     reaches the model.

// codexShaped reports a payload in Codex's dialect: tool_response is a bare
// string where Claude sends an object.
func (ev *ToolEvent) codexShaped() bool {
	var s string
	return len(ev.ToolResponse) > 0 && json.Unmarshal(ev.ToolResponse, &s) == nil
}

// normalizeCodex folds the dialect into the shared shape: an apply_patch call
// moves its patch out of Command, and the patched paths are extracted so file
// guards and the analyze hook read FilePath for both CLIs.
func (ev *ToolEvent) normalizeCodex() {
	if ev.ToolName == "apply_patch" && ev.ToolInput.Patch == "" && ev.ToolInput.Command != "" {
		ev.ToolInput.Patch, ev.ToolInput.Command = ev.ToolInput.Command, ""
	}
	if ev.ToolInput.Patch == "" {
		return
	}
	ev.ToolInput.PatchPaths = ParsePatchPaths(ev.ToolInput.Patch)
	if ev.ToolInput.FilePath == "" && len(ev.ToolInput.PatchPaths) > 0 {
		ev.ToolInput.FilePath = ev.ToolInput.PatchPaths[0]
	}
}

var patchPathPrefixes = []string{"*** Add File: ", "*** Update File: ", "*** Delete File: ", "*** Move to: "}

// ParsePatchPaths returns every file an apply_patch payload names, in order,
// deduplicated.
func ParsePatchPaths(patch string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(patch, "\n") {
		for _, pre := range patchPathPrefixes {
			if !strings.HasPrefix(line, pre) {
				continue
			}
			p := strings.TrimSpace(strings.TrimPrefix(line, pre))
			if p != "" && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out
}

var exitCodeLineRe = regexp.MustCompile(`(?im)^\s*exit code:\s*(-?\d+)\s*$`)

// codexExitCode resolves a Codex tool call's exit code. handled is false for a
// Claude-shaped event, which keeps its own defaults. A handled event with no
// resolvable code yields "" — unknown, never a fabricated success.
//
// The "Exit code: N" line is trusted only for apply_patch, whose response is
// text codex itself composes. A Bash response is the command's own stdout, so
// a failing build that prints "Exit code: 0" would otherwise read as a pass
// and fire the success chain; without the transcript, Bash stays unknown.
func (ev *ToolEvent) codexExitCode() (code string, handled bool) {
	if !ev.codexShaped() {
		return "", false
	}
	if ev.ToolUseID != "" && ev.TranscriptPath != "" {
		if code, ok := CodexExitCodeFromTranscript(ev.TranscriptPath, ev.ToolUseID); ok {
			return code, true
		}
	}
	if ev.ToolName != "apply_patch" {
		return "", true
	}
	var text string
	_ = json.Unmarshal(ev.ToolResponse, &text)
	if m := exitCodeLineRe.FindStringSubmatch(text); m != nil {
		return m[1], true
	}
	return "", true
}

// The transcript is appended by codex around the moment the hook fires, so a
// miss is retried briefly before it counts as absent. Tests shrink both.
var (
	codexTranscriptRetries    = 6
	codexTranscriptRetryDelay = 250 * time.Millisecond
)

// CodexExitCodeFromTranscript reads the real status of the tool call toolUseID
// from the rollout transcript: item_completed.item.exit_code when recorded,
// else status completed/failed as 0/1.
func CodexExitCodeFromTranscript(path, toolUseID string) (string, bool) {
	for attempt := 0; attempt < codexTranscriptRetries; attempt++ {
		if code, ok := scanTranscriptForItem(path, toolUseID); ok {
			return code, true
		}
		time.Sleep(codexTranscriptRetryDelay)
	}
	return "", false
}

func scanTranscriptForItem(path, toolUseID string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	needle := []byte(`"id":"` + toolUseID + `"`)
	completed := []byte(`"item_completed"`)
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if !bytes.Contains(line, completed) || !bytes.Contains(line, needle) {
			continue
		}
		var rec struct {
			Payload struct {
				Item struct {
					Status   string `json:"status"`
					ExitCode *int   `json:"exit_code"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &rec) != nil {
			continue
		}
		if rec.Payload.Item.ExitCode != nil {
			return strconv.Itoa(*rec.Payload.Item.ExitCode), true
		}
		switch rec.Payload.Item.Status {
		case "completed":
			return "0", true
		case "failed":
			return "1", true
		}
		return "", false
	}
	return "", false
}

// --- Answers ---

// FormatCodexGuardDeny is the PreToolUse answer that stops a Codex tool call:
// hookSpecificOutput.permissionDecision=deny with the reason.
func FormatCodexGuardDeny(reason string) string {
	out := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// FormatGuardBlockFor emits the guard denial in the dialect the provider reads.
func FormatGuardBlockFor(p Provider, reason string) string {
	if p != nil && p.Name() == "codex" {
		return FormatCodexGuardDeny(reason)
	}
	return FormatGuardBlock(reason)
}

// FormatPromptContext is the UserPromptSubmit answer that adds context to the
// submitted prompt without replacing it.
func FormatPromptContext(context string) string {
	out := map[string]any{
		"hookSpecificOutput": map[string]string{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": context,
		},
	}
	data, _ := json.Marshal(out)
	return string(data)
}

// IsWakeSentence reports whether a submitted prompt is the fixed wake
// sentence, tolerating case and trailing punctuation.
func IsWakeSentence(prompt string) bool {
	p := strings.TrimRight(strings.TrimSpace(prompt), ".!")
	return strings.EqualFold(p, WakeSentence)
}

// --- Delivery through hooks ---

// HookDelivery is the inbox content a hook hands to its own agent.
type HookDelivery struct {
	Text     string
	Requests int
	Total    int
}

const codexDeliveryPreamble = "New bus messages. Handle every request below now, then reply exactly as its " +
	"\"To reply\" line says — run that muxcode command with your shell tool (do not print it), " +
	"one reply per request. Never wait for a human.\n\n"

const codexNothingPendingContext = "Your bus inbox is empty: an earlier hook already delivered and consumed " +
	"the messages behind this wake-up. Nothing is pending — reply briefly that there is nothing to do, then stop."

// ConsumeInboxForHook drains the role's inbox from inside the agent's own hook
// process — a true consume-ack receipt for every message (Receive) — and
// renders what was consumed. With requireRequest set, nothing is consumed
// unless an actionable request is waiting, so a response-only inbox is never
// turned into a prompt (MUX-009). Self-addressed loop artifacts and response
// payloads that are TUI chrome (MUX-154) are dropped at consume.
func ConsumeInboxForHook(session, role string, requireRequest bool) HookDelivery {
	if requireRequest && !HasActionableMessages(session, role) {
		return HookDelivery{}
	}
	msgs, err := Receive(session, role)
	if err != nil || len(msgs) == 0 {
		return HookDelivery{}
	}
	var parts []string
	d := HookDelivery{}
	for _, m := range msgs {
		if isLoopingSelfSend(m) {
			continue
		}
		if m.Type != "request" && LooksLikeNonResult(m.Payload) {
			continue
		}
		d.Total++
		if m.Type == "request" && WindowForRole(m.To) == role {
			d.Requests++
		}
		parts = append(parts, FormatMessage(m))
	}
	d.Text = strings.Join(parts, "\n")
	return d
}

// CodexStopDelivery is the Stop-hook decision for a hook-road codex agent: a
// pending request is consumed and returned as the block reason, which Codex
// feeds back as the agent's next prompt. Nothing pending, nothing said.
func CodexStopDelivery(session, role string) StopHookAction {
	d := ConsumeInboxForHook(session, role, true)
	if d.Requests == 0 {
		return StopHookAction{}
	}
	return StopHookAction{Block: true, Reason: codexDeliveryPreamble + d.Text}
}

// CodexPromptSubmitContext expands the wake sentence into the inbox contents
// as additional context. Any other prompt passes untouched (ok=false).
func CodexPromptSubmitContext(session, role, prompt string) (string, bool) {
	if !IsWakeSentence(prompt) {
		return "", false
	}
	d := ConsumeInboxForHook(session, role, false)
	if d.Total == 0 {
		return codexNothingPendingContext, true
	}
	return codexDeliveryPreamble + d.Text, true
}
