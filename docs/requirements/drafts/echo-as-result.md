# Echo As Result

Stop pane-scrape echoes from being recorded as passing command results in console history. When a `--wait` send receives a response from a non-hook provider (OpenCode/Codex), `logWaitResponseToHistory` synthesizes a history entry from the response payload and records it as a **passing command** — even when the payload is a launch banner or TUI chrome, not a result. This **fabricates GREEN**: it is arguably higher severity than the answered-row bug ([answered-row-receipt](answered-row-receipt.md)), because that one produced redundant noise while this one produces false evidence that work succeeded.

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

### Root cause

`logWaitResponseToHistory` in `cmd/send.go` (lines 466-527, called from the `--wait` response path at line 391). When a `--wait` send receives a response from a non-hook provider, it writes a console history entry synthesized from the RESPONSE PAYLOAD. Three defects compound:

1. **Success by keyword-absence** — it sets `exitCode := "0"` and only flips to `"1"` when `action == "error"` or the payload contains `"failed"` or `"error:"`. Arbitrary chat/pane text contains none of those, so ANY payload is recorded as `outcome: "success"`. Absence of evidence is recorded as evidence of success.
2. **Bus action written into `command`** — the entry is built with `Command: action`. In the console this is indistinguishable from a genuinely executed shell command, so a synthesized row renders exactly like a real `./test.sh` row.
3. **No provenance** — nothing on `HookHistoryEntry` (`bus/hook.go:432`) distinguishes "the agent self-logged a real command with a real exit code" from "send.go synthesized this from a chat reply". The console and its pass/fail counters treat both as equally authoritative.

**Upstream contributor**: for non-hook TUIs the payload that reaches `--wait` is frequently pane-scraped intermediate text (launch banner, `Thought: 242ms`, partial reasoning) rather than a real result, because completion detection for those providers is heuristic. The input to the above is often not a result at all.

**Same family, `cmd/log.go` `runLog`**: `exitCode` defaults to `"0"` (line 49) and `outcome` is derived as success unless the exit code is non-zero (lines 126-129). There is no `unknown` outcome in the model, so a log call carrying no verdict is recorded as a pass. The console reinforces this: `ConsoleEntry.IsPass()` (`bus/console.go:89`) counts an **empty** exit code as a pass.

## Requirements

### Acceptance criteria

Invariants any chosen design must satisfy:

- [ ] A fabricated pass is **impossible to render as a pass** — not merely less likely. The console pass/fail counters are consumed by humans AND by agents reading panes
- [ ] Success is never inferred from the absence of failure keywords — an entry with no real exit code can never read `outcome: "success"`
- [ ] A bus action name can never be read as an executed shell command in console history
- [ ] Every history entry's provenance is distinguishable: agent-self-logged (real command, real exit code) vs synthesized from a `--wait` response payload
- [ ] Obviously-not-a-result payloads (launch banners, `Thought:` lines, TUI chrome, prompt lines) are never recorded as a verdict
- [ ] The genuine agent-self-logged path stays intact and authoritative: `muxcode log <role> ... --command X --exit-code $code --output-file f` is the trustworthy source
- [ ] Non-hook console panes do not regress to empty — the original purpose of `logWaitResponseToHistory` (visibility) is preserved in some form

### Design directions to evaluate

Candidate directions — **no pre-commitment**; Phase 1 evaluates and records the decision:

| Direction | Sketch | Notes |
|-----------|--------|-------|
| `unknown`/`unverified` outcome | Add a third outcome to the model; synthesized entries with no real exit code get `unknown`, never `success` | Touches `HookHistoryEntry`, `runLog`, `IsPass()`, console renderers/counters |
| Provenance field | `source: agent-log \| wait-response` on each entry; render synthesized rows visibly differently; exclude from pass/fail counters or count separately | Backward-compatible additive field; counters must decide how to treat legacy rows |
| Stop writing bus action into `command` | Leave `command` empty on synthesized rows, or namespace it (e.g. `bus:review`) so it can never read as a shell command | Cheapest; fixes the "tell" but not the fabricated `success` |
| Reject non-result payloads | Signature filter (launch banner, `Thought:`, TUI chrome, prompt lines) before logging | Heuristic — reduces but cannot eliminate; complements the above |
| Activity row, not result row | `logWaitResponseToHistory` writes a dedicated "activity" row type that cannot carry a pass/fail verdict at all | Matches the function's stated purpose (stop panes looking empty); likely the cleanest invariant |

### Constraints

- Keep the genuine agent-self-logged path intact — it must stay authoritative
- Any fix must make a fabricated pass impossible to render as a pass, not merely less likely
- Console history is read by agents via `tmux capture-pane` — a rendering-only fix that leaves `outcome: "success"` in the JSONL is insufficient

### Key files

| File | Role in defect |
|------|----------------|
| `cmd/send.go` | `logWaitResponseToHistory()` (L466-527) — synthesizes the fabricated entry; called from `--wait` response path (L391) |
| `cmd/log.go` | `runLog` — `exitCode` defaults `"0"` (L49), outcome success unless non-zero (L126-129); no `unknown` outcome |
| `bus/hook.go` | `HookHistoryEntry` (L432) — no provenance field, no `unknown` outcome |
| `bus/console.go` | `ConsoleEntry.IsPass()` (L89) treats empty exit code as pass; `summaryLine()` pass/fail counters (L432-483) |

### Cross-links

Same underlying disease as the answered-row echo loop ([answered-row-receipt](answered-row-receipt.md)) and the review-echo incident: **pane text is being treated as evidence of work**. The broader effort to stop inferring state from pane scrapes is [remove-gated-pane-scrape-delivery](../backlog/remove-gated-pane-scrape-delivery.md) — this spec closes the console-history branch of that disease; that one closes the delivery branch.

## Implementation

### Phase 1: Design decision

- [ ] Evaluate: `unknown`/`unverified` outcome in the entry model (never infer success from keyword absence)
- [ ] Evaluate: provenance field (`source: agent-log | wait-response`) + distinct rendering + counter exclusion/segregation
- [ ] Evaluate: stop writing bus action into `command` (empty vs namespaced)
- [ ] Evaluate: non-result payload rejection (launch-banner/`Thought:`/TUI-chrome signatures)
- [ ] Evaluate: whether `logWaitResponseToHistory` should write a result row at all — dedicated verdict-free "activity" row shape
- [ ] Record the chosen combination in this spec's Technical approach and update Phase 2 steps to match

### Phase 2: Fix the synthesized-entry path (`cmd/send.go`)

- [ ] Implement the chosen design in `logWaitResponseToHistory` — synthesized entries can no longer carry an unearned `success`
- [ ] Remove the keyword-absence heuristic (`"failed"` / `"error:"` scan) as a success/failure oracle
- [ ] Ensure a bus action is never rendered as a shell command in console history
- [ ] Unit tests: launch-banner payload never records a pass; chat text never records a pass; real self-logged entries unaffected

### Phase 3: Fix the same family in `muxcode log` and the console model

- [ ] `cmd/log.go` `runLog`: a log call carrying no verdict is not recorded as a pass (per the chosen outcome model)
- [ ] `bus/console.go`: `IsPass()` / counters honor the chosen model — empty exit code is not silently a pass; synthesized/unverified rows excluded from or segregated in `pass`/`fail` counts
- [ ] Console renderers visually distinguish provenance (per chosen design)
- [ ] Unit tests: counter math over mixed real/synthesized/unknown histories

### Phase 4: Integration test

- [ ] Create `scripts/test-echo-as-result.sh` with end-to-end verification (requires running muxcode session)
- [ ] Test: simulate a `--wait` response whose payload is a launch banner → verify console history records no pass for it
- [ ] Test: real `muxcode log --command ./test.sh --exit-code 0` → verify it still renders as a pass (authoritative path intact)
- [ ] Test: real `muxcode log --exit-code 1` → verify it renders as a fail
- [ ] Test: console summary counters over a mixed history count only verdict-carrying entries as pass/fail
- [ ] Test: non-hook console pane is not empty after a `--wait` round-trip (visibility preserved)
- [ ] Run the script and verify all checks pass

## Status

Draft
