# Echo As Result

Stop pane-scrape echoes from being recorded as passing command results in console history. When a response from a non-hook provider (OpenCode/Codex) completes a send, a history entry is synthesized from the response payload and recorded as a **passing command** — by `logWaitResponseToHistory` (`cmd/send.go`) on the `--wait` path AND by its daemon mirror `logTrackedTaskToHistory` (`daemon/daemon.go`) when a `--track` task completes — even when the payload is a launch banner or TUI chrome, not a result. This **fabricates GREEN**: it is arguably higher severity than the answered-row bug ([answered-row-receipt](answered-row-receipt.md)), because that one produced redundant noise while this one produces false evidence that work succeeded.

## Context

### Observed failure

Hard evidence from this session (2026-08-04):

| Console | Displayed | Reality |
|---------|-----------|---------|
| Build | `total 5  pass 5  fail 0` | Exactly ONE real build had run |
| Test | `total 1  pass 1  fail 0`, `Test completed successfully` | ZERO tests had run — the captured output was the agent's launch banner (`LSPs are disabled`, the `chsh -s /bin/zsh` notice), not a single line of go test |

Fabricated entry vs the one real entry, from `test-history.jsonl`:

```json
FAKE: {"command":"test",     "exit_code":"0","outcome":"success",
       "output":"muxcode agent launch test\nThe default interactive shell is now zsh...",
       "summary":"muxcode agent launch test"}

REAL: {"command":"./test.sh","exit_code":"0","outcome":"success",
       "output":"=== muxcode-llm-harness: go vet ===\n=== RUN TestHasMessages_EmptyFile\n--- PASS: ..."}
```

The `command` field is the tell: the real row records the shell command the agent actually ran (self-logged via `muxcode log ... --command ./test.sh --exit-code $exit_code`); the fake rows record the BUS ACTION name.

### 2026-08-11 recurrence — `--track`, and silent work loss

The same synthesis fired repeatedly on `--track` sends (not just `--wait`) on 2026-08-11. At session start, requests to `test` and `run` were answered ~15s later with the agents' shell launch banners (`muxcode agent launch test`, "LSPs are disabled", the model banner). Two false-green entries were recorded for runs that never happened:

```json
{"command":"test","exit_code":"0","outcome":"success","output":"muxcode agent launch test..."}
```

The `exit_code:"0"` / `outcome:"success"` are hardcoded defaults that only flip to failure when the payload contains `"failed"` or `"error:"` — a boot banner contains neither. Trigger appears to be OpenCode task-completion detection firing while the agent was still starting up.

**The cascade — worse than the logging defect.** Later in the same session the bug compounded: agents began receiving OTHER agents' launch noise as their own wake-up payloads and acting on it.

- The `run` agent reasoned "the pane text ... was the watch agent's response being injected as my next wake-up ... but the content is just launch noise. My inbox is empty" — and went idle WITHOUT running the script it had been asked to run.
- The `test` agent skipped a requested re-run, concluding "Everything is clean — tests pass, review is LGTM ... Nothing more to do here", based on a stale prior result.

Net effect: requested work silently does not happen while the console shows green. That is the real severity of this bug — it is not only a cosmetic logging issue.

### Provenance detector

Reliable classification of any console history entry, usable today by humans and agents — worth encoding in whatever design Phase 1 selects:

| `command` field | Provenance |
|-----------------|------------|
| A bus action name (`test`, `run`, `build`, `review`) | SYNTHESIZED from a response payload — not evidence of a run |
| A real shell command (`./test.sh`, `./build.sh`) or empty (agent self-log) | REAL |

### Root cause

`logWaitResponseToHistory` in `cmd/send.go` (lines 466-527, called from the `--wait` response path at line 391). When a `--wait` send receives a response from a non-hook provider, it writes a console history entry synthesized from the RESPONSE PAYLOAD. Three defects compound:

1. **Success by keyword-absence** — it sets `exitCode := "0"` and only flips to `"1"` when `action == "error"` or the payload contains `"failed"` or `"error:"`. Arbitrary chat/pane text contains none of those, so ANY payload is recorded as `outcome: "success"`. Absence of evidence is recorded as evidence of success.
2. **Bus action written into `command`** — the entry is built with `Command: action`. In the console this is indistinguishable from a genuinely executed shell command, so a synthesized row renders exactly like a real `./test.sh` row.
3. **No provenance** — nothing on `HookHistoryEntry` (`bus/hook.go:432`) distinguishes "the agent self-logged a real command with a real exit code" from "send.go synthesized this from a chat reply". The console and its pass/fail counters treat both as equally authoritative.

**Second path — `--track` (scope wider than first documented)**: `logTrackedTaskToHistory` (`daemon/daemon.go:2486`, called from the tracked-task completion path at :2473) is a deliberate mirror of `logWaitResponseToHistory` — same hardcoded `exitCode := "0"`, same `"failed"`/`"error:"` keyword-absence scan, same `Command: action`. Every defect above exists twice. A fix that only touches the `--wait` path in `cmd/send.go` will not close this.

**Third path (found during implementation)**: `logTaskToConsoleHistory` (`daemon/daemon.go`, called from the non-hook task-completion path and the idle-task-rescue path) carried the same hardcoded `exitCode := "0"` / `outcome := "success"` — `logTrackedTaskToHistory` was in fact a hand-copy of it. Scope: three synthesis paths, not two. See Technical approach for the resolution (single shared constructor).

**Upstream contributor**: for non-hook TUIs the payload that reaches `--wait` is frequently pane-scraped intermediate text (launch banner, `Thought: 242ms`, partial reasoning) rather than a real result, because completion detection for those providers is heuristic. The input to the above is often not a result at all.

**Same family, `cmd/log.go` `runLog`**: `exitCode` defaults to `"0"` (line 49) and `outcome` is derived as success unless the exit code is non-zero (lines 126-129). There is no `unknown` outcome in the model, so a log call carrying no verdict is recorded as a pass. The console reinforces this: `ConsoleEntry.IsPass()` (`bus/console.go:89`) counts an **empty** exit code as a pass.

## Requirements

### Acceptance criteria

Invariants any chosen design must satisfy:

- [x] A fabricated pass is **impossible to render as a pass** — not merely less likely. The console pass/fail counters are consumed by humans AND by agents reading panes
- [x] Success is never inferred from the absence of failure keywords — an entry with no real exit code can never read `outcome: "success"`
- [x] A bus action name can never be read as an executed shell command in console history
- [x] Every history entry's provenance is distinguishable: agent-self-logged (real command, real exit code) vs synthesized from a `--wait` response payload
- [x] Obviously-not-a-result payloads (launch banners, `Thought:` lines, TUI chrome, prompt lines) are never recorded as a verdict
- [x] The fix covers every synthesized-entry path — `logWaitResponseToHistory` (`--wait`, `cmd/send.go`), its daemon mirror `logTrackedTaskToHistory` (`--track`, deleted), and the third path found during implementation, `logTaskToConsoleHistory` (`daemon/daemon.go`) — all routed through one constructor
- [x] The genuine agent-self-logged path stays intact and authoritative: `muxcode log <role> ... --command X --exit-code $code --output-file f` is the trustworthy source
- [x] Non-hook console panes do not regress to empty — the original purpose of `logWaitResponseToHistory` (visibility) is preserved in some form

### Design directions to evaluate

Candidate directions — **no pre-commitment**; Phase 1 evaluates and records the decision:

| Direction | Sketch | Notes |
|-----------|--------|-------|
| `unknown`/`unverified` outcome | Add a third outcome to the model; synthesized entries with no real exit code get `unknown`, never `success` | Touches `HookHistoryEntry`, `runLog`, `IsPass()`, console renderers/counters |
| Provenance field | `source: agent-log \| wait-response` on each entry; render synthesized rows visibly differently; exclude from pass/fail counters or count separately | Backward-compatible additive field; counters must decide how to treat legacy rows |
| Stop writing bus action into `command` | Leave `command` empty on synthesized rows, or namespace it (e.g. `bus:review`) so it can never read as a shell command | Cheapest; fixes the "tell" but not the fabricated `success` |
| Reject non-result payloads | Signature filter (launch banner, `Thought:`, TUI chrome, prompt lines) before logging | Heuristic — reduces but cannot eliminate; complements the above |
| Activity row, not result row | `logWaitResponseToHistory` writes a dedicated "activity" row type that cannot carry a pass/fail verdict at all | Matches the function's stated purpose (stop panes looking empty); likely the cleanest invariant |

### Technical approach (Phase 1 decision, 2026-08-12)

Four of the five candidate directions were adopted together — they are complementary, not alternatives:

- **Activity row, not result row** — synthesized entries can no longer carry a pass/fail verdict at all. This is the core invariant.
- **`unknown` outcome** — adopted, with a correction to this spec's premise: the model ALREADY had an `unknown` outcome — `bus.HookOutcome()` has always returned `"unknown"` for an empty exit code. The defect was never a missing outcome value; it was a read/write asymmetry: the write side produced `unknown` correctly and the read side (`ConsoleEntry.IsPass()`) collapsed it into a pass. Same shape of bug as [answered-row-receipt](answered-row-receipt.md) — the write side honored an invariant the read side disagreed with.
- **Provenance field** — `source: "bus-response"` on synthesized entries; empty means the authoritative hook / self-logged path (so legacy rows keep their verdict).
- **Stop writing the bus action into `command`** — `command` is left empty and the action moves to a new `action` field, rendered as `bus:review` so it can never read as a shell command.
- **Reject non-result payloads** — adopted as a complement, deliberately conservative: rejects only when the payload is empty, its FIRST line is chrome, or EVERY line is chrome. A real result that merely quotes a banner later is kept, because losing a real verdict is the costlier error.

**Scope correction (found during implementation)**: there were THREE synthesis paths, not two. Besides `logWaitResponseToHistory` (`cmd/send.go`) and `logTrackedTaskToHistory` (`daemon/daemon.go`), a third — `logTaskToConsoleHistory` (`daemon/daemon.go`, called from the non-hook task-completion path and the idle-task-rescue path) — carried the same hardcoded `exitCode := "0"` / `outcome := "success"`. Resolution: `logTrackedTaskToHistory` was **deleted entirely** (it was a hand-copy of `logTaskToConsoleHistory`; its caller now calls the shared function), and all remaining paths construct entries through one constructor, `bus.NewBusResponseEntry()` (`bus/history_provenance.go`), so the duplication that let the same defect exist in three places cannot regrow.

**Behavior change worth flagging**: `muxcode log <role> "<summary>"` **without** `--exit-code` now records `unknown` instead of a pass. This affects documented usage for the watch agent (`muxcode log watch "summary"`), which logs observations rather than command results. This is intended — an observation is not a success — but agent docs that show `muxcode log` without `--exit-code` may deserve a note that such entries appear as unverified.

### Constraints

- Keep the genuine agent-self-logged path intact — it must stay authoritative
- Any fix must make a fabricated pass impossible to render as a pass, not merely less likely
- Console history is read by agents via `tmux capture-pane` — a rendering-only fix that leaves `outcome: "success"` in the JSONL is insufficient

### Key files

| File | Role in defect |
|------|----------------|
| `cmd/send.go` | `logWaitResponseToHistory()` (L466-527) — synthesizes the fabricated entry; called from `--wait` response path (L391) |
| `daemon/daemon.go` | `logTrackedTaskToHistory()` (L2486) — `--track` mirror of the same synthesis; fires on tracked-task completion (L2473) |
| `cmd/log.go` | `runLog` — `exitCode` defaults `"0"` (L49), outcome success unless non-zero (L126-129); no `unknown` outcome |
| `bus/hook.go` | `HookHistoryEntry` (L432) — no provenance field, no `unknown` outcome |
| `bus/console.go` | `ConsoleEntry.IsPass()` (L89) treats empty exit code as pass; `summaryLine()` pass/fail counters (L432-483) |

### Cross-links

Same underlying disease as the answered-row echo loop ([answered-row-receipt](answered-row-receipt.md)) and the review-echo incident: **pane text is being treated as evidence of work**. The broader effort to stop inferring state from pane scrapes is [remove-gated-pane-scrape-delivery](../backlog/remove-gated-pane-scrape-delivery.md) — this spec closes the console-history branch of that disease; that one closes the delivery branch.

The pre-existing test `TestConsoleEntryIsPass` (`bus/console_test.go`) had a case pinning `nil exit code -> pass` — that case encoded the defect and was flipped to `false`, with a comment pointing at the new test file (`bus/history_provenance_test.go`).

## Implementation

### Phase 1: Design decision

- [x] Evaluate: `unknown`/`unverified` outcome in the entry model (never infer success from keyword absence)
- [x] Evaluate: provenance field (`source: agent-log | wait-response`) + distinct rendering + counter exclusion/segregation
- [x] Evaluate: stop writing bus action into `command` (empty vs namespaced)
- [x] Evaluate: non-result payload rejection (launch-banner/`Thought:`/TUI-chrome signatures)
- [x] Evaluate: whether `logWaitResponseToHistory` should write a result row at all — dedicated verdict-free "activity" row shape
- [x] Record the chosen combination in this spec's Technical approach and update Phase 2 steps to match

### Phase 2: Fix the synthesized-entry paths (`cmd/send.go` + `daemon/daemon.go`)

- [x] Implement the chosen design in `logWaitResponseToHistory` — synthesized entries can no longer carry an unearned `success`
- [x] Apply the identical fix to the daemon mirror `logTrackedTaskToHistory` (`--track` path) — the mirror must not outlive the fix (resolved by deleting the mirror; its caller now uses the shared `logTaskToConsoleHistory`)
- [x] Remove the keyword-absence heuristic (`"failed"` / `"error:"` scan) as a success/failure oracle — in both paths
- [x] Ensure a bus action is never rendered as a shell command in console history
- [x] Unit tests: launch-banner payload never records a pass (via `--wait` and `--track` paths); chat text never records a pass; real self-logged entries unaffected (`bus/history_provenance_test.go`)

### Phase 3: Fix the same family in `muxcode log` and the console model

- [x] `cmd/log.go` `runLog`: a log call carrying no verdict is not recorded as a pass (per the chosen outcome model) — `exit_code` no longer defaults to `"0"`; no verdict records `unknown`
- [x] `bus/console.go`: `IsPass()` / counters honor the chosen model — `IsPass()` is strict; new `IsUnverified()` / `IsFail()` / `Label()` / `CountOutcomes()`; counters gained an `unverified` column shown only when non-zero
- [x] Console renderers visually distinguish provenance (per chosen design) — all four renderers count three ways and render unverified rows dim with a `····` marker
- [x] Unit tests: counter math over mixed real/synthesized/unknown histories

### Phase 4: Integration test

> **Outstanding.** `scripts/test-echo-as-result.sh` was not written. End-to-end verification was instead performed live against a running session (see "Observed fix behavior" below) — the scripted test remains outstanding.

- [ ] Create `scripts/test-echo-as-result.sh` with end-to-end verification (requires running muxcode session)
- [ ] Test: simulate a `--wait` response whose payload is a launch banner → verify console history records no pass for it
- [ ] Test: simulate a `--track` task completion whose payload is a launch banner → verify console history records no pass for it
- [ ] Test: real `muxcode log --command ./test.sh --exit-code 0` → verify it still renders as a pass (authoritative path intact)
- [ ] Test: real `muxcode log --exit-code 1` → verify it renders as a fail
- [ ] Test: console summary counters over a mixed history count only verdict-carrying entries as pass/fail
- [ ] Test: non-hook console pane is not empty after a `--wait` round-trip (visibility preserved)
- [ ] Run the script and verify all checks pass

### Observed fix behavior (2026-08-12 live verification)

The bug reproduced live during implementation and the fix was verified against it in the same session:

- A `--wait` send to `test` returned a payload of pure TUI chrome (`Thought: 912ms`, `LSPs are disabled`, partial reasoning). The new code **rejected it outright** — `test-history.jsonl` stayed empty. The old code would have written `command:"test", exit_code:"0", outcome:"success"`.
- Two later replies whose first line was reasoning prose were recorded as `command:"", action:"test", source:"bus-response", exit_code:"", outcome:"unknown"` — visible in the console but counted as neither pass nor fail.
- The review agent's chrome replies produced two unverified rows; under the old code the review console would have shown `clean 2`, i.e. two fabricated LGTMs.
- The authoritative path was confirmed intact: a real self-logged `./test.sh` row (`exit_code:"0"`, no `source`) still renders as a pass, and a real failing run (`exit_code:"1"`) still renders as a fail.

## Status

In Progress
