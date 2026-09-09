package bus

import (
	"strings"
	"time"
)

// Console history provenance and outcome vocabulary.
//
// A console history entry is read as evidence that work ran — by humans looking
// at a left pane and by agents scraping one. Two very different things write
// those entries:
//
//  1. The authoritative path: a PostToolUse hook or an explicit `muxcode log`
//     call made by the agent that actually ran the command. It carries a real
//     shell command and a real exit code.
//  2. The synthesized path: the bus turning a *response payload* into an entry
//     so non-hook provider panes (OpenCode, Codex) don't look empty. It carries
//     no command and no exit code — only whatever text the agent replied with,
//     which for a TUI provider is frequently a launch banner or partial
//     reasoning rather than a result.
//
// Only the first is evidence. These constants let the second be told apart and
// kept out of the pass/fail verdict entirely.
const (
	// SourceBusResponse marks an entry synthesized from a bus response payload.
	// Such an entry is never proof that a command ran, so it can never carry a
	// success verdict. An empty Source means the authoritative path — that is
	// also how entries written before provenance existed read, which is the
	// safe default: they keep the verdict they already recorded.
	SourceBusResponse = "bus-response"

	// OutcomeSuccess / OutcomeFailure / OutcomeUnknown are the outcome values.
	// OutcomeUnknown is the verdict for an entry with no real exit code: not a
	// pass, not a fail, simply not evidence either way.
	OutcomeSuccess = "success"
	OutcomeFailure = "failure"
	OutcomeUnknown = "unknown"
)

// nonResultSignatures identify shell and TUI chrome rather than the result of
// any work — agent launch banners, the macOS shell-change notice, provider
// reasoning chatter and prompt furniture. Matched case-insensitively.
//
// These are exactly the payloads observed being recorded as passing builds and
// tests for runs that never happened: a non-hook agent's completion detection
// fires while the agent is still booting, and its launch banner becomes the
// "result".
var nonResultSignatures = []string{
	"muxcode agent launch",
	"lsps are disabled",
	"the default interactive shell is now zsh",
	"chsh -s /bin/zsh",
	"for more details, please visit",
	"thought:",
	"thinking...",
	"esc to interrupt",
	"? for shortcuts",
	"bypassing permissions",
	"/ps to view",
	"/stop to close",
}

// toolEchoPrefixes lead a provider's rendered tool-call line.
var toolEchoPrefixes = []string{"└", "⎿", "•"}

// renderPrefixes open a line the provider's TUI drew: the status bullet
// and spinner glyphs, and the branch characters of a rendered tool call.
// An agent composing a reply does not begin with these.
var renderPrefixes = []string{"•", "└", "⎿", "✻", "✽", "⏵"}

// providerChromeSignatures identify a provider's own transient pane
// furniture — the status line and its slash-command hints.
//
// This is deliberately a much shorter list than nonResultSignatures,
// because the two answer different questions. Console history asks "is
// this evidence of work?", so it rejects launch banners and shell notices
// that are perfectly real messages. The send path asks "is this pane
// furniture rather than composed text?", and must not reject those: the
// launch-refusal alert reads "…relaunch: muxcode agent launch plan", a
// legitimate message that names a banner phrase and was silently swallowed
// when this path borrowed the broader list.
var providerChromeSignatures = []string{
	"esc to interrupt",
	"/ps to view",
	"/stop to close",
	"? for shortcuts",
}

// LooksLikeProviderChrome reports whether a payload is a provider's
// rendered pane furniture rather than text an agent composed.
//
// Every non-empty line must be chrome. A real message that merely quotes a
// status line survives, which is the safe direction: dropping a message
// starves whoever waits on it, while keeping one only adds noise.
//
// An empty payload is not chrome. It is useless, but it may still carry a
// correlation id, and dropping it would strand the task it answers.
func LooksLikeProviderChrome(payload string) bool {
	lines := nonEmptyLines(payload)
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		if !isProviderChromeLine(line) {
			return false
		}
	}
	return true
}

// isProviderChromeLine reports whether one line is provider furniture.
//
// Structure decides, not vocabulary. The line must OPEN with a render
// glyph the provider draws — the status bullet or a tool-call branch — and
// then either carry a status signature or end in the pane's own truncation
// ellipsis. A substring test alone matched genuine one-line replies that
// merely mention the words ("the pane sat at esc to interrupt"), and
// swallowing a real reply is the costlier mistake: it starves whoever
// waits on it, while keeping chrome only adds noise.
func isProviderChromeLine(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if !hasRenderPrefix(l) {
		return false
	}
	for _, sig := range providerChromeSignatures {
		if strings.Contains(l, sig) {
			return true
		}
	}
	return strings.HasSuffix(l, "…")
}

// hasRenderPrefix reports whether a line opens with a glyph a provider TUI
// draws rather than a character an agent types.
func hasRenderPrefix(l string) bool {
	for _, p := range renderPrefixes {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

// looksLikeChrome reports whether a single line is recognizable TUI/shell noise.
//
// Beyond the fixed signatures it recognizes one structural shape: a
// rendered tool-call line ("└ set -o pipefail; ./test.sh 2>&1 | awk '…")
// closed by the pane's own truncation ellipsis. Composed text does not end
// mid-token in a "…" the writer never typed, so the pairing of a render
// prefix with that marker identifies chrome without matching prose that
// merely happens to start with a bullet.
func looksLikeChrome(line string) bool {
	l := strings.ToLower(strings.TrimSpace(line))
	if l == "" {
		return true
	}
	for _, sig := range nonResultSignatures {
		if strings.Contains(l, sig) {
			return true
		}
	}
	if strings.HasSuffix(l, "…") {
		for _, p := range toolEchoPrefixes {
			if strings.HasPrefix(l, p) {
				return true
			}
		}
	}
	return false
}

// LooksLikeNonResult reports whether a payload is plainly not the result of any
// work and therefore must not be recorded in console history at all.
//
// The test is deliberately conservative, because the cost of the two mistakes
// is asymmetric: wrongly recording chrome fabricates evidence, while wrongly
// keeping a real result only adds an unverified row that claims nothing. So a
// payload is rejected only when it is empty, when its *first* line is chrome
// (the observed launch-banner shape), or when every line is chrome. A real
// result that merely happens to quote a banner further down is kept.
func LooksLikeNonResult(payload string) bool {
	lines := nonEmptyLines(payload)
	if len(lines) == 0 {
		return true
	}
	if looksLikeChrome(lines[0]) {
		return true
	}
	for _, line := range lines {
		if !looksLikeChrome(line) {
			return false
		}
	}
	return true
}

// SummarizePayload reduces a response payload to a single-line summary for the
// console. It takes the first line rather than a raw prefix so a summary can
// never contain an embedded newline, which would corrupt the row it renders on.
func SummarizePayload(payload string) string {
	summary := payload
	if idx := strings.Index(summary, "\n"); idx >= 0 {
		summary = summary[:idx]
	}
	summary = strings.TrimSpace(summary)
	if len(summary) > 200 {
		summary = summary[:200] + "..."
	}
	return summary
}

// NewBusResponseEntry builds a console history entry from a bus response
// payload, returning ok=false when the payload should not be recorded at all.
//
// The entry it returns cannot be rendered as a pass:
//
//   - Command is left empty and the bus action goes to Action, so an action
//     name can never be mistaken for an executed shell command.
//   - ExitCode is empty and Outcome is "unknown" — no verdict is claimed.
//   - Source marks it synthesized, so counters and renderers can segregate it.
//
// The one exception is a *detected failure*, which records a real failure
// verdict. The asymmetry is intentional: inventing a success hides broken work
// behind a green pane, while an over-eager failure is merely noisy and self-
// correcting — someone looks. Success is the direction that must never be
// guessed, so it is the only one this constructor refuses to produce.
//
// Callers must not fill in ExitCode or Outcome afterwards. Every synthesized
// path goes through here precisely so the old duplicated heuristics — which
// scanned payloads for "failed"/"error:" and defaulted to success otherwise —
// cannot grow back in one copy while being fixed in another.
func NewBusResponseEntry(action, payload string, errored bool) (HookHistoryEntry, bool) {
	payload = strings.TrimRight(payload, "\n")
	if LooksLikeNonResult(payload) {
		return HookHistoryEntry{}, false
	}

	entry := HookHistoryEntry{
		TS:     time.Now().Unix(),
		Action: action,
		Source: SourceBusResponse,
		// Explicitly verdict-free: no exit code, outcome "unknown".
		ExitCode: "",
		Outcome:  OutcomeUnknown,
		Output:   payload,
		Summary:  SummarizePayload(payload),
	}

	// Two ways a failure reaches this point. The daemon paths pass errored
	// directly from DetectTaskCompletion; the --wait path has no such flag and
	// instead sees the action the daemon substitutes on failure ("error", set
	// in checkNonHookTasks). Both must be honored or a detected failure would
	// silently downgrade to "unverified" on one path.
	if errored || action == "error" {
		entry.ExitCode = "1"
		entry.Outcome = OutcomeFailure
	}

	return entry, true
}
