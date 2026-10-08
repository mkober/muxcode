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

- [ ] `muxcode hook bash` is registered on **`PostToolUseFailure`** (matcher `Bash`) wherever muxcode writes Claude hook settings — `config/settings.json` and the install road — alongside the existing `PostToolUse` entry
- [ ] A `PostToolUseFailure` payload resolves to a **non-zero** exit code, never `0`: the `Exit code N` line of `error` when present; `is_interrupt` → non-zero; no exit-code line → non-zero/unknown. `resolveExitCode`'s `"0"` default never applies to a failure event
- [ ] The failure event's output is taken from `error` and written to the history row through the MUX-179 scrub (`GetScrubbedOutput` for a sensitive role)
- [ ] A failing Claude Bash call now writes a history row, a console line, and fires the role's **failure** chain edge; a graph node's hook-road evidence records the failure
- [ ] **Negative control:** a `PostToolUse` success payload still resolves `0` and fires success only; a Codex event is untouched
- [ ] **Negative control:** a failure payload without the `Exit code` line is never recorded as success
- [ ] `CLAUDE.md`'s hook-driven-chains bullet and [`docs/hooks.md`](../../hooks.md) state that both events are registered and how the failure shape is read
- [ ] `bash scripts/test-hook-bash-failure.sh` passes

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

- [ ] Unit test: a `PostToolUseFailure` payload (`error: "Exit code 3\n…"`, no `tool_response`) through `ParseToolEvent` resolves to `0` today — pin red, inverted in Phase 2
- [ ] Unit test (negative control, stays green): a `PostToolUse` success payload resolves `0`

### Phase 2: Parse the failure shape

- [ ] `resolveExitCode`: on `hook_event_name == PostToolUseFailure`, read `error`'s `Exit code N`; `is_interrupt` and a missing line → non-zero per Decision 1; never `0`
- [ ] `responseText`: on a failure event, the output is `error`
- [ ] Tests: exit line present, interrupt, missing line; **negative control:** success and Codex events unchanged; invert the Phase 1 pin

### Phase 3: Register and wire

- [ ] `config/settings.json`: `PostToolUseFailure` → `muxcode hook bash`, matcher `Bash`, same `async` as the success entry (or sync if the Phase 1 probe's async finding applies — record which)
- [ ] The install/merge road writes it wherever the `PostToolUse` entry is written
- [ ] Test: `ProcessBashHook` on a failure event writes a history row with the non-zero code and fires the failure chain; **negative control:** a success event fires success only

### Phase 4: Docs

- [ ] `CLAUDE.md` hook-driven-chains bullet; [`docs/hooks.md`](../../hooks.md): both events, the failure shape, the `"0"` default's new boundary

### Phase 5: Integration test

- [ ] Create `scripts/test-hook-bash-failure.sh` — hermetic scratch bus; drive a `PostToolUseFailure` payload and a `PostToolUse` payload through `muxcode hook bash` for a Claude role; assert the failure row's non-zero exit, its console line and the failure-chain trigger; **negative control:** the success payload's row reads `0` and triggers success only; a failure payload without the exit line never reads `0`
- [ ] Coverage floor pinned to the exact pass count; run through the run agent (foreground) and record counts here

## Decisions

### Decision 1 — the exit code when the line is missing

Open. A `PostToolUseFailure` whose `error` has no `Exit code` line (the shell could not start) or whose
`is_interrupt` is set has no numeric code to record. The history row's `exit_code` is a string; the
options are a sentinel (`"unknown"`, matching the `bus-response` sentinel reply rows already use) or a
conventional non-zero (`"1"`). Either must make `OutcomeFailure`, never success; the choice is about
what `history` and the chain conditions (`exit_code`) read.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-203](../completed/MUX-203-sensitive-role-conversation-is-never-scrubbed.md) | Where this was found (Phase 1 findings, "surfaced, out of scope") |
| [MUX-179](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md) | The history scrub a failure row's output must pass through |
| [MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) | Same instrument, different fault — a success the run chain should not have fired |
| [MUX-185](./MUX-185-history-row-provenance-declared-not-proven.md) | The history row as evidence — this spec is about rows that never exist |

## Status

Backlog
