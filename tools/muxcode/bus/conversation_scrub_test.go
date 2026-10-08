package bus

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	convSecret = "hunter2secret0"
	convEmail  = "jane.doe@example.com"
)

// claudeBashPayload is a Claude PostToolUse event for a Bash call, its
// tool_response carrying a field beyond the four the docs name.
func claudeBashPayload(event, stdout, stderr string) []byte {
	data, _ := json.Marshal(map[string]any{
		"hook_event_name": event,
		"tool_name":       "Bash",
		"tool_input":      map[string]string{"command": "bash scripts/report.sh"},
		"tool_response": map[string]any{
			"stdout": stdout, "stderr": stderr, "interrupted": false, "isImage": false, "noOutputExpected": false,
		},
	})
	return data
}

// The answer's updatedToolOutput is what Claude hands the model in place of
// the result (verified live, claude 2.1.293), so it is the agent-facing text:
// redacted under the notice, every other response field kept so Claude's
// schema check still accepts it.
func TestClaudeScrubAnswer_RedactsSensitiveResult(t *testing.T) {
	payload := claudeBashPayload("PostToolUse", "author "+convEmail+"\npassword="+convSecret+"\n", "warn token=abcdefgh12345678\n")
	answer, ok := ClaudeScrubAnswer("run", payload)
	if !ok {
		t.Fatal("run: no answer for a result carrying PII and credentials")
	}
	var got struct {
		Out struct {
			Event  string         `json:"hookEventName"`
			Output map[string]any `json:"updatedToolOutput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(answer), &got); err != nil {
		t.Fatalf("answer is not JSON: %v: %s", err, answer)
	}
	stdout, _ := got.Out.Output["stdout"].(string)
	stderr, _ := got.Out.Output["stderr"].(string)
	if got.Out.Event != "PostToolUse" || !strings.HasPrefix(stdout, "[muxcode pii-scrub: 3 value(s)") {
		t.Errorf("want a PostToolUse answer with the notice heading stdout, got %s", answer)
	}
	for _, leak := range []string{convEmail, convSecret, "abcdefgh12345678"} {
		if strings.Contains(stdout+stderr, leak) {
			t.Errorf("model-facing output still holds %q: %s", leak, answer)
		}
	}
	if _, kept := got.Out.Output["noOutputExpected"]; !kept || got.Out.Output["isImage"] != false {
		t.Errorf("other response fields must survive, or Claude drops the replacement: %v", got.Out.Output)
	}
}

// Negative controls: no answer — the original reaches the model untouched —
// for a non-sensitive role, a clean result, a failure event (which Claude
// cannot replace), a Codex-shaped payload, or another tool.
func TestClaudeScrubAnswer_SilentOtherwise(t *testing.T) {
	leaky := "password=" + convSecret
	codex, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Bash", "tool_response": leaky})
	write, _ := json.Marshal(map[string]any{"hook_event_name": "PostToolUse", "tool_name": "Write", "tool_response": map[string]any{"stdout": leaky}})
	cases := []struct {
		name, role string
		payload    []byte
	}{
		{"non-sensitive role", "edit", claudeBashPayload("PostToolUse", leaky, "")},
		{"clean result", "run", claudeBashPayload("PostToolUse", "\x1b[32mok\x1b[0m build passed\n", "")},
		{"failure event", "run", claudeBashPayload("PostToolUseFailure", leaky, "")},
		{"codex payload", "run", codex},
		{"other tool", "run", write},
	}
	for _, c := range cases {
		if answer, ok := ClaudeScrubAnswer(c.role, c.payload); ok {
			t.Errorf("%s: want no answer, got %s", c.name, answer)
		}
	}
}

// The --settings value must register `muxcode hook scrub` synchronously: an
// async hook's answer is ignored (verified live), so the scrub would silently
// stop reaching the model.
func TestClaudeScrubSettings_SynchronousScrubHook(t *testing.T) {
	var doc struct {
		Hooks map[string][]struct {
			Matcher string           `json:"matcher"`
			Hooks   []map[string]any `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(ClaudeScrubSettings), &doc); err != nil {
		t.Fatalf("ClaudeScrubSettings is not JSON: %v", err)
	}
	groups := doc.Hooks["PostToolUse"]
	if len(groups) != 1 || groups[0].Matcher != "Bash" || len(groups[0].Hooks) != 1 {
		t.Fatalf("want one PostToolUse Bash group, got %+v", doc.Hooks)
	}
	h := groups[0].Hooks[0]
	if h["command"] != "muxcode hook scrub" || h["async"] != nil {
		t.Errorf("want a synchronous `muxcode hook scrub`, got %v", h)
	}
}

// TestScrubHelperProcess is not a test: runWrapped runs the test binary
// through it as the `muxcode pii-scrub --role <role>` the wrap pipes into, so
// the wrapped command meets the real scrubber.
func TestScrubHelperProcess(t *testing.T) {
	if os.Getenv("MUXCODE_SCRUB_HELPER") != "1" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	out, _ := ConversationScrub("", os.Getenv("MUXCODE_SCRUB_ROLE"), string(data))
	_, _ = os.Stdout.WriteString(out)
	os.Exit(0)
}

// runWrapped runs WrapForScrub(command, "run") under shell as Codex would,
// with a `muxcode` on PATH that is the real scrubber, and returns what Codex
// reads and records. A shell that is not installed skips the test.
func runWrapped(t *testing.T, shell, command string) (string, int) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stub := "#!/bin/sh\n[ \"$1 $2\" = 'pii-scrub --role' ] || exit 64\n" +
		"exec env MUXCODE_SCRUB_HELPER=1 MUXCODE_SCRUB_ROLE=\"$3\" '" + self + "' -test.run='^TestScrubHelperProcess$'\n"
	return runWrappedWith(t, shell, command, stub)
}

// runWrappedWith is runWrapped with stub as the `muxcode` on PATH.
func runWrappedWith(t *testing.T, shell, command, stub string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath(shell); err != nil {
		t.Skipf("%s not installed", shell)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "muxcode"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-c", WrapForScrub(command, "run"))
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return string(out), exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run wrapped under %s: %v", shell, err)
	}
	return string(out), 0
}

var wrapShells = []string{"bash", "zsh"}

// The wrapped command's output is what Codex shows the model and writes to
// its rollout: redacted, stderr included, under the notice — and the
// command's own exit status survives the pipe, so a failure still reads as one.
func TestCodexScrubWrap_ScrubsBothStreamsKeepsExitCode(t *testing.T) {
	for _, shell := range wrapShells {
		t.Run(shell, func(t *testing.T) {
			out, code := runWrapped(t, shell, "echo password="+convSecret+"; echo "+convEmail+" >&2; exit 3")
			if code != 3 {
				t.Errorf("exit code = %d, want the command's own 3", code)
			}
			if strings.Contains(out, convSecret) || strings.Contains(out, convEmail) || !strings.HasPrefix(out, "[muxcode pii-scrub: 2 value(s)") {
				t.Errorf("want both streams redacted under the notice, got %q", out)
			}
		})
	}
}

// A scrubber that fails loses the output; the wrap must say so and fail,
// never exit with the command's 0 over an empty result (Phase 3 review).
func TestCodexScrubWrap_ScrubberFailureReported(t *testing.T) {
	failing := "#!/bin/sh\ncat >/dev/null\nexit 1\n"
	for _, shell := range wrapShells {
		t.Run(shell, func(t *testing.T) {
			out, code := runWrappedWith(t, shell, "echo password="+convSecret, failing)
			if code != 125 || strings.Contains(out, convSecret) ||
				!strings.Contains(out, "[muxcode pii-scrub failed (scrubber exit 1, command exit 0)") {
				t.Errorf("failed scrubber: exit %d, output %q; want 125 and the withheld notice", code, out)
			}
		})
	}
}

// A label rule allows whitespace, newlines included, between a label and its
// value; a line-by-line scrub let this value through (Phase 3 review).
func TestCodexScrubWrap_LabelAndValueOnSeparateLines(t *testing.T) {
	out, _ := runWrapped(t, "bash", "printf 'password:\\n    "+convSecret+"\\n'")
	if strings.Contains(out, convSecret) {
		t.Errorf("value on the line after its label leaked: %q", out)
	}
}

// The wrap must not change the status of pipelines inside the command:
// `false | true` succeeds, and only pipefail set by the wrap would fail it.
func TestCodexScrubWrap_InnerPipelineStatusUnchanged(t *testing.T) {
	for _, shell := range wrapShells {
		t.Run(shell, func(t *testing.T) {
			if out, code := runWrapped(t, shell, "false | true"); code != 0 {
				t.Errorf("`false | true` exit = %d, want 0 as unwrapped: %q", code, out)
			}
		})
	}
}

// A heredoc terminator or trailing comment on the command's last line must
// still end the command: the wrap closes its group on a fresh line.
func TestCodexScrubWrap_HeredocAndTrailingComment(t *testing.T) {
	out, code := runWrapped(t, "bash", "cat <<EOF\ntoken=abcdefgh12345678\nEOF\necho done # trailing comment")
	if code != 0 || strings.Contains(out, "abcdefgh12345678") || !strings.Contains(out, "done") {
		t.Errorf("heredoc command: exit %d, output %q", code, out)
	}
}

// History, chains and command_match must read the command the agent sent,
// whether or not Codex reports the rewritten one.
func TestParseToolEvent_UnwrapsScrubWrap(t *testing.T) {
	const original = "bash scripts/test-pii-scrub-roles.sh"
	data, _ := json.Marshal(map[string]any{
		"hook_event_name": "PostToolUse", "tool_name": "Bash",
		"tool_input": map[string]string{"command": WrapForScrub(original, "watch")}, "tool_response": "ok",
	})
	ev, err := ParseToolEvent(data)
	if err != nil {
		t.Fatalf("ParseToolEvent: %v", err)
	}
	if ev.ToolInput.Command != original {
		t.Errorf("command = %q, want the original %q", ev.ToolInput.Command, original)
	}
	lookalike := "{ echo hi\n} 2>&1 | tee out.log"
	if got, ok := UnwrapScrub(lookalike); ok || got != lookalike {
		t.Errorf("a command that is not the wrap must pass through: got %q, %v", got, ok)
	}
}

func TestCodexScrubWrapAnswer(t *testing.T) {
	codex := &CodexProvider{hooks: true}
	ev := bashEvent("bash scripts/report.sh")
	answer, ok := CodexScrubWrapAnswer("", "run", codex, ev)
	var got struct {
		Out struct {
			Decision string            `json:"permissionDecision"`
			Input    map[string]string `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if !ok || json.Unmarshal([]byte(answer), &got) != nil || got.Out.Decision != "allow" || got.Out.Input["command"] != WrapForScrub(ev.ToolInput.Command, "run") {
		t.Errorf("run on codex: want allow with the wrapped command, got %q (%v)", answer, ok)
	}

	patch := &ToolEvent{ToolName: "apply_patch"}
	patch.ToolInput.Command = "*** Begin Patch"
	for name, c := range map[string]struct {
		role string
		p    Provider
		ev   *ToolEvent
	}{
		"claude provider":    {"run", &ClaudeCodeProvider{}, ev},
		"non-sensitive role": {"build", codex, ev},
		"non-bash tool":      {"run", codex, patch},
	} {
		if answer, ok := CodexScrubWrapAnswer("", c.role, c.p, c.ev); ok {
			t.Errorf("%s: want no wrap, got %s", name, answer)
		}
	}
}

// ConversationScrub is what the OpenCode plugin swaps in for a bash result:
// redacted for a sensitive role, and byte-identical otherwise so the plugin
// leaves the result alone.
func TestConversationScrub(t *testing.T) {
	leaky := "password=" + convSecret + " " + convEmail
	if out, n := ConversationScrub("", "watch", leaky); n != 2 || strings.Contains(out, convSecret) || !strings.HasPrefix(out, "[muxcode pii-scrub: 2 value(s)") {
		t.Errorf("watch: got (%d) %q", n, out)
	}
	for role, text := range map[string]string{"plan": leaky, "api": "\x1b[32mok\x1b[0m build passed"} {
		if out, n := ConversationScrub("", role, text); n != 0 || out != text {
			t.Errorf("%s: want the text back byte-for-byte, got (%d) %q", role, n, out)
		}
	}
}

// pluginHarness imports the plugin and runs its tool.execute.after on one
// result, printing the output object the hook leaves behind.
const pluginHarness = `import { pathToFileURL } from "url"
const { MuxcodeScrub } = await import(pathToFileURL(process.argv[2]).href)
const hooks = await MuxcodeScrub()
const output = { output: process.argv[3], metadata: { output: process.argv[3] } }
await hooks["tool.execute.after"]({ tool: process.argv[4] }, output)
process.stdout.write(JSON.stringify(output))
`

// runPlugin runs openCodeScrubPlugin's hook under node for a tool result,
// with stub as the only `muxcode` on PATH ("" for none) and env as the whole
// environment beside PATH. It returns the result as the model would read it,
// the TUI's copy, and the arguments the stub received.
func runPlugin(t *testing.T, stub, tool, result string, env ...string) (output, metadata, args string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if stub != "" {
		recorder := "#!/bin/sh\necho \"$*\" > '" + filepath.Join(dir, "args") + "'\n" + stub
		if err := os.WriteFile(filepath.Join(bin, "muxcode"), []byte(recorder), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plugin, harness := filepath.Join(dir, "plugin.mjs"), filepath.Join(dir, "harness.mjs")
	for path, src := range map[string]string{plugin: openCodeScrubPlugin, harness: pluginHarness} {
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, harness, plugin, result, tool)
	cmd.Env = append([]string{"PATH=" + bin + ":/bin:/usr/bin"}, env...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node harness: %v", err)
	}
	var got struct {
		Output   string `json:"output"`
		Metadata struct {
			Output string `json:"output"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("harness output %q: %v", out, err)
	}
	recorded, _ := os.ReadFile(filepath.Join(dir, "args"))
	return got.Output, got.Metadata.Output, strings.TrimSpace(string(recorded))
}

var inSession = []string{"BUS_SESSION=s", "AGENT_ROLE=watch"}

// A scrub that cannot run must withhold the result — muxcode exiting non-zero
// or missing from PATH — in the model's copy and the TUI's alike (Phase 3
// review: the plugin showed the raw text on failure).
func TestOpenCodeScrubPlugin_FailsClosed(t *testing.T) {
	leaky := "password=" + convSecret
	for name, stub := range map[string]string{"scrubber exits 1": "cat >/dev/null\nexit 1\n", "muxcode missing": ""} {
		t.Run(name, func(t *testing.T) {
			out, meta, _ := runPlugin(t, stub, "bash", leaky, inSession...)
			for _, copy := range []string{out, meta} {
				if strings.Contains(copy, convSecret) || !strings.Contains(copy, "[muxcode pii-scrub failed") {
					t.Errorf("want the result withheld, got output %q, metadata %q", out, meta)
				}
			}
		})
	}
}

// The scrubber's answer replaces the result, asked for with the agent's own
// role; an unchanged answer leaves it alone.
func TestOpenCodeScrubPlugin_ReplacesWithScrubberAnswer(t *testing.T) {
	out, meta, args := runPlugin(t, "sed 's/"+convSecret+"/[SECRET_REDACTED]/'\n", "bash", "password="+convSecret+"\n", inSession...)
	if out != "password=[SECRET_REDACTED]\n" || meta != out || args != "pii-scrub --role watch" {
		t.Errorf("got output %q, metadata %q, args %q", out, meta, args)
	}
	if out, _, _ := runPlugin(t, "cat\n", "bash", "build passed", inSession...); out != "build passed" {
		t.Errorf("an unchanged answer must leave the result alone, got %q", out)
	}
}

// Negative controls: outside a muxcode agent, or for a tool other than bash,
// the plugin does nothing — even a failing scrubber withholds nothing. With no
// role it must not call `pii-scrub --role ""`, which exits non-zero.
func TestOpenCodeScrubPlugin_InertOutsideSessionAndBash(t *testing.T) {
	failing := "cat >/dev/null\nexit 1\n"
	if out, _, args := runPlugin(t, failing, "bash", "plain output", "AGENT_ROLE=watch"); out != "plain output" || args != "" {
		t.Errorf("no session: got %q, scrubber called with %q", out, args)
	}
	if out, _, args := runPlugin(t, failing, "bash", "plain output", "BUS_SESSION=s"); out != "plain output" || args != "" {
		t.Errorf("no role: got %q, scrubber called with %q", out, args)
	}
	if out, _, args := runPlugin(t, failing, "read", "plain output", inSession...); out != "plain output" || args != "" {
		t.Errorf("read tool: got %q, scrubber called with %q", out, args)
	}
}

// The plugin file is OpenCode's half of the contract: written on every launch
// and calling the subcommand the Go side answers.
func TestWriteAgentConfig_WritesOpenCodeScrubPlugin(t *testing.T) {
	oldDir, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(oldDir)
	if err := (&OpenCodeProvider{}).WriteAgentConfig("run"); err != nil {
		t.Fatalf("WriteAgentConfig: %v", err)
	}
	data, err := os.ReadFile(openCodeScrubPluginPath)
	if err != nil {
		t.Fatalf("plugin not written: %v", err)
	}
	for _, want := range []string{`"tool.execute.after"`, `["pii-scrub", "--role", process.env.AGENT_ROLE]`, "output withheld"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("plugin missing %s", want)
		}
	}
}
