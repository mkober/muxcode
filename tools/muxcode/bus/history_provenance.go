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
}

// looksLikeChrome reports whether a single line is recognizable TUI/shell noise.
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
