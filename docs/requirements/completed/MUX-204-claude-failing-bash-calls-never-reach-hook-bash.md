# Failing Bash Calls on Claude Never Reach `hook bash` — Only `PostToolUse` Is Registered

**Tracking:** [mkober/muxcode#158](https://github.com/mkober/muxcode/issues/158)

**Provenance:** filed 2026-10-08 by plan on the user's request, through the `20-defect-to-spec` run
`1791469376` (launched by the MUX-203 Phase 1 worker). Found during
[MUX-203](../completed/MUX-203-sensitive-role-conversation-is-never-scrubbed.md) Phase 1's live hook probes
on 2026-10-07 and surfaced there as out of scope; the evidence below is the run's evidence node plus
plan's own reading of this session's history files.

muxcode registers `muxcode hook bash` for Claude on **`PostToolUse` only** — `config/settings.json`,
matcher `Bash`, `"async": true`; the installed `~/.claude/settings.json` matches; nothing in the repo
registers `PostToolUseFailure`. Claude Code 2.1.293 fires **`PostToolUseFailure`, and not
`PostToolUse`**, for a Bash call that exits non-zero. So on every Claude agent a failing command never
reaches `ProcessBashHook`: no history row, no console line, no exit-code-driven chain event, no
hook-road evidence row for a graph node. The build→test→review chains that `CLAUDE.md` describes as
"deterministic (exit codes)" can, on a Claude role, only ever see success.

## Context

### Evidence

| # | What | Where | Establishes |
|---|------|-------|-------------|
| 1 | Live probe, claude 2.1.293, headless in a scratch project with muxcode hooks inert (`BUS_SESSION` unset): settings registering **only** a sync `PostToolUse` Bash hook (muxcode's wiring) + `echo X; exit 3` → hook fired **0** times. With both events registered → only `PostToolUseFailure` fired (1) | MUX-203 Phase 1 worker, 2026-10-07 | The core claim, on the installed version |
| 2 | Claude Code hooks reference: `PostToolUse` = "after a tool call succeeds"; `PostToolUseFailure` = "after a tool call fails". The failure input carries `error` (first line `Exit code N`, then interleaved stdout+stderr, possibly middle-truncated, possibly *without* the exit-code line if the shell could not start) and `is_interrupt`, and **no `tool_response`** | `code.claude.com/docs/en/hooks` | The payload shape a fix must parse |
| 3 | `grep -n PostToolUse config/settings.json` → line 41; `~/.claude/settings.json` → line 195; `grep -rn PostToolUseFailure` on both → **zero matches** | evidence node, 2026-10-08 10:23 | Nothing registers the failure event |
| 4 | This session's history files, same window, split by provider: **Codex** roles carry non-zero exits — `test` 35 of 100 rows, `review` 10 of 79, `watch` 3 of 54; **Claude** roles carry none — `run` 0 of 100, `commit` 0 of 100, `plan` 0 of 36, `edit` 0 of 15 — while the `run` agent's own reply rows in the same file report failures (`RESULT … full=1`) | plan, `/tmp/muxcode-bus-muxcode/*-history.jsonl`, 2026-10-08 | Independent corroboration: the absence follows the provider, not the role |
| 5 | Codex 0.160.0 fires `PostToolUse` on `exit 3` (verified in the MUX-203 probes); the Codex hook road reads the real exit code from the rollout transcript by `tool_use_id` | MUX-203 findings | Codex roles are **unaffected** |

**Not established — marked unverified, not claimed:** which Claude Code version moved non-zero exits
off `PostToolUse` (no regression version is asserted); whether a hook-driven failure chain has *ever*
fired on a Claude role in this session (the lifecycle log carries no chain-fired event to grep, only
`chain-suppressed`); the specific failing-command rows the original finding cited, which have rotated
out of the 100-row history windows.

### Mechanism (verified in code)

| Fact | Where |
|------|-------|
| `hook bash` is registered on `PostToolUse` with matcher `Bash`, `async: true`, and on no other event | `config/settings.json` (installed to `~/.config/muxcode/settings.json`, merged into `~/.claude/settings.json`) |
| `ToolEvent` carries `hook_event_name`, `tool_response`, `exit_code`; `resolveExitCode` reads the top-level `exit_code`, then `tool_response.exit_code`, then the Codex transcript — and **defaults to `"0"`** when a Claude payload carries none | `bus/hook.go:26–31`, `:137–163` |
| `ProcessBashHook` classifies the command, writes the `<role>-history.jsonl` row with that exit code, and fires the chain on it | `bus/hook.go:801–896` |

The default is the trap: a `PostToolUseFailure` payload routed to `hook bash` **unchanged** carries no
`exit_code` and no `tool_response`, so `resolveExitCode` would record the failure as exit `0` and fire the
**success** chain. Registering the event without teaching the parser the failure shape turns a silent
gap into a wrong signal.

### Blast radius

- Every Claude role, for every failing Bash call: here `run`, `commit`, `deploy`, `serve`, `edit`, `plan`
  (`build`, `test`, `review`, `watch` run on Codex in this session and are unaffected — on another
  machine with Claude on those roles, the build and test chains' failure edges are dead).
- The `run` role runs every `scripts/test-*.sh`; a red integration script writes no row, so a graph node
  that depends on the run agent's hook-road evidence parks on an unverified hold or reads the agent's
  reply alone.
- A failing `cdk deploy` on the Claude `deploy` role leaves no record and fires no failure chain.
- Diagnosis: a failure with no history row is invisible to `muxcode console`, `history` and the
  evidence guard — the operator sees the agent's prose and nothing structural.
- No leak follows (no row means nothing unscrubbed, per MUX-179), but no record either.

## Requirements

### Acceptance criteria

- [x] `muxcode hook bash` is registered on **`PostToolUseFailure`** (matcher `Bash`) wherever muxcode writes Claude hook settings — `config/settings.json` and the install road — alongside the existing `PostToolUse` entry (Phase 3: the template entry, async like the success one; `install.sh`'s merge loop covers all three matcher-bearing phases)
- [x] A `PostToolUseFailure` payload resolves to a **non-zero** exit code, never `0`: the `Exit code N` line of `error` when present; `is_interrupt` → non-zero; no exit-code line → non-zero/unknown. `resolveExitCode`'s `"0"` default never applies to a failure event (Phase 2: `failureExitCode`; the no-line case is `"1"` per Decision 1; five cases pinned in `TestGetExitCode_ClaudeFailureEvent`)
- [x] The failure event's output is taken from `error` and written to the history row through the MUX-179 scrub (`GetScrubbedOutput` for a sensitive role) (Phase 2: `responseText` → `errorText()`; `TestGetOutput_ClaudeFailureEventReadsError` asserts `GetScrubbedOutput` redacts a secret in the error's output)
- [x] A failing Claude Bash call now writes a history row, a console line, and fires the role's **failure** chain edge; a graph node's hook-road evidence records the failure (Phase 5: the hermetic sections assert row, console line and chain notice through the real binary; the **live** section proves a real Claude (2.1.295) fires the async `PostToolUseFailure` hook — row `exit 3` / `failure`, `Run FAILED (exit 3)` to edit, control `0`. The hook-road evidence a graph node reads is that same history row)
- [x] **Negative control:** a `PostToolUse` success payload still resolves `0` and fires success only; a Codex event is untouched (Phase 2 for the resolution — `TestGetExitCode_ClaudeSuccessEventIsZero`; Phase 3 for the firing — the success case of `TestProcessBashHook_ClaudeFailureEventFiresFailureChain` resolves to the watch edge alone. The Codex path is untouched by either diff — `failureExitCode` is reached only on `hook_event_name == PostToolUseFailure`, which Codex never sends — and its existing tests pass)
- [x] **Negative control:** a failure payload without the `Exit code` line is never recorded as success (Phase 2: the "interrupt without exit line", "shell never started" and "exit line echoed below a missing status line" cases each resolve `"1"` and `OutcomeFailure`)
- [x] `CLAUDE.md`'s hook-driven-chains bullet and [`docs/hooks.md`](../../hooks.md) state that both events are registered and how the failure shape is read (Phase 4: both documents name `PostToolUse` and `PostToolUseFailure`, the leading `Exit code N` line of `error`, `"1"` for the no-line cases, and the `"0"` default's boundary)
- [x] `bash scripts/test-hook-bash-failure.sh` passes (run agent 2026-10-09, `--live`: 13 passed, 0 failed at floor 13, 4 live, exit 0 — twice)

### Technical approach

Register the second event where the first is registered, and teach `ParseToolEvent`/`resolveExitCode`
the failure shape before anything else: a `hook_event_name` of `PostToolUseFailure` switches exit-code
resolution to the `error` field's first line (`^Exit code (\d+)`), with `is_interrupt` and a missing line
both resolving to a non-zero sentinel (`"1"`/`"unknown"` — [Decision 1](#decision-1--the-exit-code-when-the-line-is-missing)),
and switches output extraction to `error`. `ProcessBashHook` then runs unchanged — classification,
history row, scrub, chain — on a correct exit code. The Codex road is untouched (`codexExitCode` keeps its
transcript read).

### Key files

| File | Role |
|------|------|
| `config/settings.json` | Hook registration — add the `PostToolUseFailure` entry |
| `tools/muxcode/bus/hook.go` | `ToolEvent`, `ParseToolEvent`, `resolveExitCode`, `responseText`, `ProcessBashHook` |
| `tools/muxcode/cmd/hook.go` | `hook bash` entry |
| `tools/muxcode/bus/launch.go` / install road | Wherever Claude settings are written or merged |
| `docs/hooks.md`, `CLAUDE.md` | Docs |
| `scripts/test-hook-bash-failure.sh` (new) | Integration test |

## Implementation

### Phase 1: Pin

- [x] Unit test: a `PostToolUseFailure` payload (`error: "Exit code 3\n…"`, no `tool_response`) through `ParseToolEvent` resolves to `0` today — pin red, inverted in Phase 2 (`TestGetExitCode_ClaudeFailureEventPinnedToZero`, `bus/hook_test.go`: a realistic payload — `hook_event_name`, `transcript_path`, `tool_use_id`, `error: "Exit code 3\nX"`, `is_interrupt: false`, no `tool_response` — asserts `GetExitCode() == "0"`, the defect; its comment names Phase 2 as the inversion. Test-only, no production change)
- [x] Unit test (negative control, stays green): a `PostToolUse` success payload resolves `0` (`TestGetExitCode_ClaudeSuccessEventIsZero`: a real `PostToolUse` payload with the same context fields and a `tool_response` resolves `"0"` and `HookOutcome` → `OutcomeSuccess`)

#### Phase 1 verification note

Verified 2026-10-08 19:55 by plan from the working tree (run `1791503217`, launched by the user; branch
`MUX-204-claude-failing-bash-calls-never-reach-hook-bash` created by the run). Build, test and review
nodes all success; review **0/0/0** ("defect characterization and success control match the parser").
No acceptance criterion is ticked: a pin proves the defect, and every criterion describes the fixed state.
Decision 1 (the exit code when the `Exit code` line is missing) is Phase 2's, where the parse is written.

### Phase 2: Parse the failure shape

- [x] `resolveExitCode`: on `hook_event_name == PostToolUseFailure`, read `error`'s `Exit code N`; `is_interrupt` and a missing line → non-zero per Decision 1; never `0` (`bus/hook.go`: `claudeFailureEvent` const; `failureExitCode` matches `failureExitLineRe` — `\A\s*Exit code (\d+)\b`, **anchored to the start** so an `Exit code N` echoed below a missing status line is read as output — and returns `"1"` for an interrupt, a shell that never started, or a contradictory `Exit code 0`; `resolveExitCode` routes the failure event there before every other source, so its `"0"` default never applies. `error` is kept as `json.RawMessage` (`RawError`, read by `errorText`) so a non-string `error` on any event cannot fail the parse every hook returns silently on — the guard included)
- [x] `responseText`: on a failure event, the output is `error` (`responseText` returns `errorText()` for `claudeFailureEvent`, which carries neither `tool_response` nor `tool_result`)
- [x] Tests: exit line present, interrupt, missing line; **negative control:** success and Codex events unchanged; invert the Phase 1 pin (`TestGetExitCode_ClaudeFailureEvent`, table-driven over `claudeFailurePayload`: exit line → `"3"`; interrupt without a line → `"1"`; shell never started → `"1"`; exit line echoed below a missing status line → `"1"`; contradictory `Exit code 0` → `"1"` — every case also asserted to parse as a non-zero int and to give `OutcomeFailure`; the first case is the Phase 1 pin inverted. `TestGetOutput_ClaudeFailureEventReadsError` — `GetOutput` is the error's output and `GetScrubbedOutput` redacts a secret in it. `TestParseToolEvent_NonStringErrorStillParses`. `TestGetExitCode_ClaudeSuccessEventIsZero` kept as the control; the Codex path is untouched by the diff and its existing tests pass)

#### Phase 2 verification note

Verified 2026-10-08 20:08 by plan from the working tree (run `1791503217`; Phase 1 committed as
`8defcf7`). Build, test and review nodes all success; review **0/0/0** ("failure parsing and scrubbed
output match Phase 2; success and Codex paths remain unchanged"). Criteria 2, 3 and 6 ticked on the
parser tests; criterion 5 stays open because "fires success only" is a chain claim — provable once the
event is registered (Phase 3) and the chain runs (Phase 5), not by a parser test. Decision 1 recorded
below as decided in this phase.

### Phase 3: Register and wire

- [x] `config/settings.json`: `PostToolUseFailure` → `muxcode hook bash`, matcher `Bash`, same `async` as the success entry (or sync if the Phase 1 probe's async finding applies — record which) (**async, `true`, like the success entry.** MUX-203's finding — an async hook cannot *replace* a tool result — applies to `updatedToolOutput`, which `hook bash` never emits; it records and fires, and async keeps the agent's turn unblocked)
- [x] The install/merge road writes it wherever the `PostToolUse` entry is written (`install.sh`: the two per-phase `reduce` blocks folded into one loop over `("PreToolUse", "PostToolUse", "PostToolUseFailure")` through the existing idempotent `add_hook`; a fresh install copies the template. The worker checked the `jq` merge on scratch files — adds the entry, idempotent on a second run)
- [x] Test: `ProcessBashHook` on a failure event writes a history row with the non-zero code and fires the failure chain; **negative control:** a success event fires success only (`TestProcessBashHook_ClaudeFailureEventFiresFailureChain`, `bus/hook_test.go`, read in full: a `run` agent's `bash scripts/test-demo.sh` as `PostToolUseFailure` with `error: "Exit code 3\nFAIL: …"` → `run-history.jsonl` row `exit "3"` / `OutcomeFailure`, `ResolveChain` → the **edit** edge, message containing `Run FAILED (exit 3): bash scripts/test-demo.sh`; the same command as `PostToolUse` success → row `"0"` / `OutcomeSuccess`, the **watch** edge only. `ProcessBashHook` and `hookBash` doc comments now name both events)

#### Phase 3 verification note

Verified 2026-10-09 09:40 by plan from the working tree (run `1791503217`; Phase 2 committed as
`4b9aa27`). Build, test and review nodes all success; review **0/0/0** ("failure hook registration,
installer merge and failure/success chain regression coverage are consistent"). Criteria 1 and 5 ticked.
Criterion 4 stays open: its "console line" and "graph node's hook-road evidence" are live claims, and the
worker states the async firing on Claude was **not live-probed** this phase — Phase 5's script is where
that is proven, end to end, against a real agent.

### Phase 4: Docs

- [x] `CLAUDE.md` hook-driven-chains bullet; [`docs/hooks.md`](../../hooks.md): both events, the failure shape, the `"0"` default's new boundary (`CLAUDE.md` by edit — both events registered, no `tool_response`, the leading `Exit code N` line, output from `error`, `"1"` never `"0"` and not `"unknown"` with the `Atoi` reason, the `"0"` default only for a code-less success payload, existing installs re-run `./install.sh`. `docs/hooks.md` by plan, from the worker's handoff with each fact checked in the tree — the settings JSON example gains the `PostToolUseFailure` block (still valid JSON); an *Existing installs and new hooks* note: only `install.sh` writes `~/.claude/settings.json`, the other roads copy the template, the automation choice left open; the `hook bash` section's Phase line names both events and a *failure event* paragraph carries the shape, the anchored regex, Decision 1, the scrub, `RawError`, the default's boundary, the five pinning tests and the unprobed live path; the envelope lines gain the failure shape)

#### Phase 4 verification note

Verified 2026-10-09 09:48 by plan from the working tree (run `1791503217`; Phase 3 committed as
`5ade53e`). Docs-only phase; test node success; review **0/0/0** ("docs match failure parsing and
registration, explain existing-install migration, and disclose the unprobed async path"). Criterion 7
ticked from both documents' text. Criterion 4 still open, awaiting Phase 5.

### Phase 5: Integration test

- [x] Create `scripts/test-hook-bash-failure.sh` — hermetic scratch bus; drive a `PostToolUseFailure` payload and a `PostToolUse` payload through `muxcode hook bash` for a Claude role; assert the failure row's non-zero exit, its console line and the failure-chain trigger; **negative control:** the success payload's row reads `0` and triggers success only; a failure payload without the exit line never reads `0` (227 lines, four hermetic sections — see the record below — plus an **opt-in live section** (`--live` / `MUXCODE_HOOK_BASH_FAILURE_LIVE=1`) the spec did not ask for and that proves what no hermetic section can: a real `claude -p` fires the async `PostToolUseFailure` hook)
- [x] Coverage floor pinned to the exact pass count; run through the run agent (foreground) and record counts here (`EXPECTED_PASS=13`; **13 passed, 0 failed, 4 live, exit 0**, twice — record below)

#### Phase 5 verification note

Verified 2026-10-09 10:00 by plan from the working tree (run `1791503217`; Phase 4 committed as
`90e4245`). Test node success; review **0/0/0** ("live polling now waits for both history rows and the
matching failure notice; the async assertion race is resolved"). Criteria 4 and 8 ticked — criterion 4
on the **live** section's assertions, read in full, not on the hermetic ones: the defect was a hook that
looked registered and never fired, so only a real Claude firing it could close that box. With this,
every acceptance criterion (8) and every phase step in the spec is ticked. `CLAUDE.md`'s integration
test list did not name the script; plan added the entry during this verification.

#### Phase 5 record

`scripts/test-hook-bash-failure.sh` — hermetic: a scratch bus session through the installed binary,
chain config pinned to the defaults (`MUXCODE_CONFIG_DIR` scratch), Claude payloads fed to the real
`muxcode hook bash`. Floor `EXPECTED_PASS=13`, the exact hermetic pass count.

| Section | What it proves |
|---------|----------------|
| `run` agent, failure | `PostToolUseFailure` with `Exit code 3`: `run-history` row exit `3` / `failure`, the console row `FAIL … exit 3`, edit notified `Run FAILED (exit 3)`, and **no** watch request |
| `run` agent, negative control | the same kind of call as a `PostToolUse` success: row `0` / `success`, console `OK`, the watch request, and **no** notice to edit — success fires success only |
| `run` agent, no exit line | a shell that never started records exit `1` and the failure edge, never `0` |
| `build` agent | a failing `./build.sh` records exit `2` and notifies edit with no test request; its success requests test — the chain edges hold for a second role |
| Live (`--live`) | hook entries taken from `config/settings.json` (the Bash `PostToolUse` and `PostToolUseFailure` registrations, nothing else) and passed by `--settings` to one real `claude -p` (haiku, 2.1.295) as `AGENT_ROLE=run`, which runs a script exiting 3 then one exiting 0. **Completeness gate first**: CLI exit 0, at least two tool results, at least one `is_error`. Then, polled for up to 20 s because the hook is async: the red row `exit_code "3"` / `failure`; the green control `"0"` / `success`; at least one `Run FAILED (exit 3)` notice to edit naming the red script. The user's own `~/.claude/settings.json` hooks load beside `--settings`, so a row may appear twice and every check asks for at least one |

Runs through the run agent, foreground, against the installed binary, both with `--live`:

| Run | Result | Task |
|-----|--------|------|
| First | **13 passed, 0 failed** (floor 13), **4 live**, exit 0 | `1791553765-spawn-65bbe1b7-81516558` → `1791553798-run-608fe11c` |
| After the review fix — live polling also waits for the failure notice, closing an async assertion race | **13 passed, 0 failed** (floor 13), **4 live**, exit 0 | `1791553916-spawn-65bbe1b7-09d7b442` → `1791553938-run-d5d39fdf` |

What the live run establishes: on claude 2.1.295, with exactly the hook entries muxcode ships, a Bash call
that exits non-zero **does** reach `muxcode hook bash` as `PostToolUseFailure`, writes its row with the real
code and fires the failure edge; the passing call records `0`. The Phase 1 probe (2.1.293) showed the
event fires; this shows muxcode's registration and parse receive it end to end.

## Decisions

### Decision 1 — the exit code when the line is missing

**Decided in Phase 2 (2026-10-08): `"1"`.** Made by the implement worker and accepted by review
(0/0/0); recorded by plan, who checked the reason against the code. The user may reverse it.

A `PostToolUseFailure` whose `error` has no `Exit code` line (the shell could not start) or whose
`is_interrupt` is set has no numeric code to record. The history row's `exit_code` is a string; the
options were a sentinel (`"unknown"`, matching the `bus-response` sentinel reply rows already use) or a
conventional non-zero (`"1"`). Either makes `OutcomeFailure`, never success; the choice is about what
`history` and the chain conditions (`exit_code`) read — and that is what decides it. `BuildChainContext`
(`bus/conditions.go:626`) sets `ctx.ExitCode` with `strconv.Atoi` **only when it parses**, so an
`"unknown"` sentinel would leave the context's exit code at its zero value and an `exit_code: 0`
condition would match a failure. `"1"` is also what an interrupted `tool_response` already records on
the success event. The same `"1"` covers a contradictory `Exit code 0` on a failure event, since the
event itself says the call failed. `TestGetExitCode_ClaudeFailureEvent` asserts every case parses as a
non-zero int, so the sentinel cannot creep back.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-203](../completed/MUX-203-sensitive-role-conversation-is-never-scrubbed.md) | Where this was found (Phase 1 findings, "surfaced, out of scope") |
| [MUX-179](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md) | The history scrub a failure row's output must pass through |
| [MUX-176](../backlog/MUX-176-run-chain-fires-success-on-backgrounded-call.md) | Same instrument, different fault — a success the run chain should not have fired |
| [MUX-185](../backlog/MUX-185-history-row-provenance-declared-not-proven.md) | The history row as evidence — this spec is about rows that never exist |

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-204-claude-failing-bash-calls-never-reach-hook-bash | 44m | 2026-10-09 10:00 |

## Status

Complete — closed out 2026-10-09 by run `1791503217-50-spec-to-pr` (launched by the user): all five
phases implemented and verified, every acceptance criterion (8) and phase step ticked, moved from
`drafts/` to `completed/`. Moved from `backlog/` to `drafts/` and set as the active spec on the user's
instruction 2026-10-08 (rank 1 / Tier 1), the day MUX-203 — whose Phase 1 probes surfaced this defect —
merged. Phases on `MUX-204-claude-failing-bash-calls-never-reach-hook-bash`: `8defcf7` (Phase 1),
`4b9aa27` (Phase 2), `5ade53e` (Phase 3), `90e4245` (Phase 4), `7de1356` (Phase 5).
