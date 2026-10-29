# The Run Chain Reported Success Three Minutes Before the Script Finished — and It Failed

**Tracking:** [mkober/muxcode#134](https://github.com/mkober/muxcode/issues/134)

**Provenance:** filed 2026-09-10 on the user's explicit request. Same class as the 2026-09-09 precheck
incident already recorded in `CLAUDE.md` (a lone passing `go vet` fired test→review before the suite
ran), and the reason the test precheck now feeds the chain only on failure.

The run agent reported it unprompted, which is how it was caught at all:

> an automatic run-succeeded chain message fired to watch prematurely at 10:31 (before the script
> actually completed at 10:34+) — that early signal was wrong, this is the real result

`scripts/test-prompt-mode.sh` then exited **1** (26 passed / 2 failed / 1 skipped). The chain had
reported success for a run that had neither finished nor passed.

## Context

### Observed (2026-09-10, session `muxcode`)

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 10:31:39 | `workflow watching from=idle trigger=chain:run:success` | lifecycle log | **machine-written (daemon)** |
| 10:31:39 | run-history row for the `bash scripts/test-prompt-mode.sh …` call: **`exit 0`, outcome `success`** | `run-history.jsonl` | **machine-written (hook)** |
| 10:31:45 | `workflow idle from=watching trigger=chain:watch:success` | lifecycle log | **machine-written (daemon)** |
| **10:35:53** | `/tmp/test-prompt-mode-run.log` last modified | filesystem `stat` | **machine-written (fs)** |
| — | that log's final line: `EXIT_CODE=1`, above `=== 26 passed, 2 failed, 1 skipped ===` | file content | **machine-written (script)** |
| 10:33 | run agent: *"my own bash call is still running as background task `bn857kq0r`"* | bus history | agent self-report |
| 10:36 | run agent: the 10:31 signal "was wrong, this is the real result" | bus history | agent self-report |

**The defect does not depend on any agent's account.** Two independent machine sources bracket it: the
daemon logged `chain:run:success` at **10:31:39**, and the filesystem shows the script still writing at
**10:35:53**, ending `EXIT_CODE=1`. That is a **4 m 14 s** gap between the declared success and the
actual failing finish, established without reading a single agent message.

The success row and the chain edge share a timestamp to the second.

### Second occurrence (2026-09-10 15:46, session `muxcode`) — and it narrows the mechanism

Recorded by plan 2026-09-10 16:08 on edit's request (`1789070744`), verified in the primary records
rather than from the report. Same script, same defect, a wider gap this time:

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 15:46:16 | edit dispatches `bash scripts/test-prompt-mode.sh` to run (task `1789069576`) | task store | **machine-written** |
| **15:46:55** | run-history row: `bash scripts/test-prompt-mode.sh` → **`exit 0`, outcome `success`** | `run-history.jsonl` | **machine-written (hook)** |
| 15:46 | `run → watch [request:watch]` "**Run succeeded** (bash scripts/test-prompt-mode.sh)" | bus history | **machine-written (chain)** |
| 15:47, 15:48, 15:49 | `watch → edit [event:notify]` "logs look healthy after deploy" — three false all-clears | bus history | **machine-written (chain)** |
| **15:54:09** | background task output `tasks/bqxzncyob.output` final write: `=== 26 passed, 2 failed, 1 skipped ===` then `[exited with code 1]` | filesystem `stat` + content | **machine-written (fs)** |
| 15:54 | `run → edit [response:run]` "test-prompt-mode.sh exit 1" — the real result | bus history | agent self-report |

**Gap: 7 m 14 s** between the declared success (15:46:55) and the actual failing finish (15:54:09),
bracketed by two machine sources exactly as the 10:31 occurrence was.

**This occurrence eliminates candidate 3.** The recorded command is bare — `bash
scripts/test-prompt-mode.sh`, a single statement with **no compound, no trailing `cat`**. The
trailing-`cat`-supplies-the-exit-status explanation cannot account for it. The prescribed Phase 1
experiment (re-run the compound command in the foreground) was never needed: a command with no `cat`
at all reproduced the defect, which is the stronger result.

**And it corroborates candidate 1.** Backgrounding was uncorroborated at 10:31 — the claimed id
`bn857kq0r` appeared in neither `proc.jsonl` nor `spawn.jsonl`. This time the background artefact is
**on disk**: `tasks/bqxzncyob.output` holds the script's full output ending `[exited with code 1]`,
and a hook-recorded run command at 15:51:06 tails that very path. That is no longer an agent's
account. Candidates 1 (background wrapper returns at dispatch) and 2 (hook records at dispatch)
remain live; the experiment to separate them is still owed. _(Run 2026-10-22 — see
[Experiment (2026-10-22)](#experiment-2026-10-22--candidate-1-established): candidate 1 holds, candidate 2
eliminated.)_

**The countermeasure already exists — it just does not cover `run`.**
`evidenceGuardRoles = {"build", "test", "deploy"}` (`bus/evidence_guard.go:21`); `CheckEvidenceGuard`
returns nil for any other role at line 35-37. Lines 47-49 already block a backgrounded evidence
statement (`seps[i] == "&"` → `evidenceBackgroundReason`). So the exact shape that fired here is
**already refused for build, test and deploy, and permitted for `run`** — a gap in role coverage, as
the spec suspected, rather than a missing mechanism.

### Mechanism — a hypothesis, and deliberately labelled as one

_Superseded 2026-10-22: the experiment recorded in the next section established candidate 1 and
eliminated candidate 2. Kept as written, for the record of what was and was not known when filed._

**What is established:** an authoritative `exit 0` row exists for a call whose script was still
running, and the chain fired on it. The row describes something that had not finished.

**What is hypothesised:** that the call was *backgrounded*, and that a backgrounded call returns at
dispatch. This rests on the run agent's own account — *"my own bash call is still running as
background task `bn857kq0r`"* — and that account is **uncorroborated**: the id `bn857kq0r` appears in
neither `proc.jsonl` nor `spawn.jsonl`, so no ledger confirms a background task by that name existed.

This distinction was raised by edit and is worth stating plainly, because an earlier finding in this
session (MUX-173) asserted a cause from a single agent-adjacent log and had to be retracted. The
timeline here survives that standard; **the mechanism does not yet**. Phase 1 exists to settle it.

Candidate mechanisms, all consistent with the machine evidence:

| Candidate | Would explain the exit-0 row by |
|-----------|---------------------------------|
| Backgrounded call returns at dispatch | the wrapper genuinely exiting 0 once the child is launched |
| Hook records at dispatch, not completion | the PostToolUse path writing before the call ends |
| Compound statement's last command | the row taking `cat`'s exit status rather than the script's — the call was `bash … > log 2>&1; echo "EXIT_CODE=$?" >> log; cat log` |

The third deserves particular attention: it needs no backgrounding at all, and the recorded command
string genuinely ends in `cat log`, which exits 0 whatever the script did. `CheckEvidenceGuard` exists
to refuse exactly this bundled shape for build/test/deploy — **the run role is not covered by it**.
That would make this a gap in the guard's role coverage rather than a timing bug, and it is testable
immediately without reproducing any race.

### Experiment (2026-10-22) — candidate 1 established

Run through the run agent 2026-10-22 13:01 (`mux176-probe.sh`, run-history `ts 1792688491`, exit 0;
scrubbed output kept at `/tmp/mux176-probe-scrubbed.log`). One real `claude -p` (Claude Code 2.1.308,
model haiku) as `AGENT_ROLE=run` on a scratch bus against the installed muxcode (`v0.1.32-3-gecf57bb`),
with exactly `config/settings.json`'s Bash `PostToolUse` / `PostToolUseFailure` registrations
(`muxcode hook bash`, async) plus a synchronous hook that timestamped and dumped every payload. User
settings were not loaded (`--setting-sources project`), so each event reached `hook bash` once. The
live session was untouched (`TMUX`/`TMUX_PANE` unset, scratch `BUS_SESSION`). Each script stamped its
own START/END.

- Arm A: `bash bg-….sh` with `run_in_background: true` — sleeps 20 s, exits 3
- Arm B: `bash fg-….sh` in the foreground — sleeps 8 s, exits 3 (the candidate-2 control)

Times relative to the `claude -p` launch:

| Arm | Script START | Hook fired | Hook event | Script END | run-history row | Chain edge |
|-----|--------------|------------|------------|------------|-----------------|------------|
| A — backgrounded | +2.5 s | **+2.6 s** | `PostToolUse` | **+22.6 s**, exit 3 | **`exit_code "0"`, `success`** | **watch "Run succeeded (bash bg-….sh)"** ×1; edit "Run FAILED" ×0 |
| B — foreground | +4.3 s | +12.4 s | `PostToolUseFailure` (`Exit code 3`) | +12.3 s, exit 3 | `exit_code "3"`, `failure` | edit "Run FAILED (exit 3)" ×1; watch ×0 |

(The row `ts` is whole unix seconds, so it can read up to 1 s earlier than the hook stamp.)

The backgrounded call's hook payload, verbatim (`transcript_path` dropped):

```json
{
  "hook_event_name": "PostToolUse",
  "tool_name": "Bash",
  "tool_input": { "command": "bash bg-68370x286.sh", "run_in_background": true },
  "tool_response": {
    "stdout": "", "stderr": "", "interrupted": false, "isImage": false,
    "noOutputExpected": false, "backgroundTaskId": "bueqav7cd"
  },
  "tool_use_id": "toolu_01VZ8n93KntqyKR34uBzmH5C",
  "duration_ms": 374
}
```

The model was handed `Command running in background with ID: bueqav7cd. Output is being written to:
…/tasks/bueqav7cd.output`. The session outlived the script — the model later read that file, which
ended `[exited with code 3]` — yet **no further hook fired for the call: one hook event in total**.
Claude fires no hook when a background task finishes.

**Candidate 1 holds, with a precision the hypothesis lacked.** It is not a wrapper "exiting 0": Claude
completes a `run_in_background` Bash call *at dispatch* (`duration_ms: 374`) and fires `PostToolUse`
then, with a `tool_response` that carries `backgroundTaskId` and **no exit status at all**.
`(*ToolEvent).resolveExitCode` (`bus/hook.go:175`, the final `return "0"`) defaults a Claude success
payload with no code to `"0"`, so `hook bash` writes an authoritative `exit 0` / `success` row and the
run chain's `bash *.sh` allowlist (`bus/profile.go:970`) fires watch — 20 s before a script that exits 3
has finished.

**Candidate 2 is eliminated by experiment**: for a foreground call, `PostToolUseFailure` fired 0.1 s
*after* the script's END and recorded exit 3. `hook bash` records a foreground call on completion.

This matches both 2026-09-10 incidents: the agent backgrounded the script, the row appeared seconds
after dispatch, and the real result surfaced only in the background task's output file
(`tasks/bqxzncyob.output … [exited with code 1]`).

#### Fix chosen (Phase 2)

- **A background launch is not a result.** In `hook bash`, a Claude Bash event whose
  `tool_response.backgroundTaskId` is set, or whose `tool_input.run_in_background` is true, writes no
  authoritative row and fires no chain edge. Key on either signal: a call backgrounded after it started
  (Ctrl+B, auto-backgrounding) may carry the task id without the flag — unverified, so both count.
- **Lifecycle row for the withheld edge** (AC5), naming role, command and background task id, so a
  chain that did not fire is visible.
- **Keep the `"0"` default for an ordinary foreground `PostToolUse`.** A completed foreground success
  payload carries no exit code either (keys `stdout, stderr, interrupted, isImage, noOutputExpected`);
  since MUX-204 Claude sends non-zero exits to `PostToolUseFailure`. So "no exit code" is *not* the
  incompleteness signal — the background marker is. Removing the default would break the negative
  control (AC3) and silence every run chain.
- **Fail closed** (AC4) where completion cannot be read: a non-object `tool_response`, or the
  background signals in an unexpected shape → no success edge.
- **Not recoverable through hooks:** the background task's real exit code. Claude fires nothing at its
  end, so a backgrounded run's result reaches the bus only through the agent's own reply. Harvesting
  Claude's task output file was considered and rejected: its path is an internal Claude layout, not a
  hook contract.
- Considered, not chosen: extending `CheckEvidenceGuard` to refuse `run_in_background` on the run role.
  The run agent legitimately backgrounds long scripts, and the guard would not cover a call
  backgrounded after it started.

### Downstream consequence — not a stray row

Watch acted on the early signal and posted a "logs look healthy after deploy" all-clear on a **failing
run**. Anything gated on the run chain's success edge would have proceeded on it. In a graph run, that
edge is what advances a node.

### Precedent, and why the existing countermeasure does not cover this

`CLAUDE.md` already records the 2026-09-09 case where a passing `go vet` precheck fired test→review
before the suite ran. The fix there was narrow and correct for that shape: **the precheck feeds the
chain only on failure**, so a passing vet writes no row.

That countermeasure does not reach this one. The problem here is not *which command* fed the chain but
*when* its result was believed — a call that has not finished has no result to believe. The evidence
guard (`CheckEvidenceGuard`) likewise addresses a different failure: bundling statements, not
backgrounding one.

### Scope boundary

In scope: a chain edge must not fire on a call that has not completed, and the run chain specifically
must key on the script's own completion. Not in scope: the two section-5 failures inside that script
(see MUX-173's open question), the evidence guard's bundling rules, or the watch agent's behaviour —
watch acted correctly on the signal it was given.

## Requirements

### Acceptance criteria

- [x] The cause is **established by experiment** — dispatch-time recording vs background-wrapper exit
      — and recorded here before any fix lands — _2026-10-22 two-arm probe through the run agent,
      recorded under [Experiment (2026-10-22)](#experiment-2026-10-22--candidate-1-established)
      with no code change in the tree_
- [x] A backgrounded or otherwise incomplete call writes **no** authoritative success row, and fires
      no chain success edge — _by code path 2026-10-29 (`backgroundLaunch` early return precedes every
      history write; empty chain skips `triggerChain`); pinned by
      `TestProcessBashHook_UnfinishedCallWithholdsChain` (`bus/hook_test.go`) and end to end by
      `scripts/test-chain-completion.sh`_
- [x] A run that completes normally still fires its success edge exactly as it does today, pinned by a
      test — so the fix does not buy correctness by disabling the chain — _2026-10-29:
      `TestProcessBashHook_FinishedCallStillFiresChain` and the success case of
      `TestProcessBashHook_ClaudeFailureEventFiresFailureChain` (`bus/hook_test.go`)_
- [x] When a call's completion cannot be determined, the chain **fails closed** (no success edge)
      rather than assuming success — _by code path 2026-10-29: an unreadable `tool_response` with no
      exit code, or a background signal of unexpected shape, counts as unfinished; pinned by the
      fail-closed cases of `TestProcessBashHook_UnfinishedCallWithholdsChain`, with
      `TestProcessBashHook_FinishedCallStillFiresChain` as the control, and by the unreadable section
      of `scripts/test-chain-completion.sh`_
- [x] A lifecycle row records a withheld edge, so a chain that did not fire is visible rather than
      silent — _`chain-withheld` (`logChainWithheld`, `bus/hook.go`), 2026-10-29_

### Technical approach

Settle the mechanism first. If the hook records at dispatch, the fix is in the PostToolUse path: an
authoritative row must be written on completion, and a call still running writes nothing. If the
background wrapper returns 0, the fix is that a backgrounded call is not chain-eligible at all —
its exit code describes the launch, not the work.

The load-bearing test is the **negative control**: a normal completing run must still fire its
success edge. A fix that simply stops firing passes any "no premature success" assertion, and would
silently disable every run chain in the repo.

Worth considering alongside: the run chain's `OnSuccess` is a `command_match` allowlist, so it already
distinguishes verification runs from other commands. Whatever gates completion should compose with
that rather than replace it.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/hook.go` | PostToolUse classification; where the authoritative exit code is recorded |
| `tools/muxcode/bus/evidence_guard.go` | `CheckEvidenceGuard`, `parseShellStatements` — adjacent, different failure |
| `tools/muxcode/bus/profile.go` | `EventChain` / `ResolveChain`; the run chain's `OnSuccess` allowlist |
| `tools/muxcode/bus/proc.go` | background execution — the wrapper whose exit code is in question |

## Implementation

### Phase 1: Diagnose

- [x] Determine which candidate holds: backgrounded-dispatch, hook-records-at-dispatch, or the
      compound statement's trailing `cat` supplying the exit status — _candidate 1 holds: Claude
      completes a `run_in_background` call at dispatch and fires `PostToolUse` with no exit status;
      candidate 2 eliminated by the 2026-10-22 experiment (foreground hook fires after END with the
      real code); candidate 3 already eliminated by the 15:46 recurrence. See
      [Experiment (2026-10-22)](#experiment-2026-10-22--candidate-1-established)_
- [x] Check the third first — it is the cheapest: re-run the same compound command in the foreground
      and see whether the recorded row reports the script's status or `cat`'s — _settled by the
      2026-09-10 15:46 recurrence instead of by the prescribed re-run: the command there was **bare**
      (`bash scripts/test-prompt-mode.sh`, no compound, no `cat`) and still produced an `exit 0`
      success row for a script that exited 1. Candidate 3 is eliminated — it is not necessary to
      produce the defect_
- [x] Establish whether `CheckEvidenceGuard` covers the `run` role, and record the answer here —
      _**it does not.** `evidenceGuardRoles = map[string]bool{"build": true, "test": true, "deploy": true}`
      (`bus/evidence_guard.go:21`); `CheckEvidenceGuard` returns nil for every other role
      (lines 35-37). The guard already blocks a **backgrounded** evidence statement at lines 47-49,
      so the shape that fired here is refused for build/test/deploy and permitted for `run`_
- [x] Record the discriminating evidence, then choose the fix — _recorded under
      [Experiment (2026-10-22)](#experiment-2026-10-22--candidate-1-established); fix chosen: a
      Claude Bash event carrying `tool_response.backgroundTaskId` or `tool_input.run_in_background`
      writes no authoritative row and fires no edge, with a lifecycle row for the withheld edge; the
      `"0"` default stays for a completed foreground `PostToolUse`; unreadable completion fails closed_

### Phase 2: Fix

- [x] Ensure an incomplete call writes no authoritative success row and fires no success edge —
      _`(*ToolEvent).backgroundLaunch` (`bus/hook.go`): a Claude `PostToolUse` whose input has
      `run_in_background` set, or whose response names `backgroundTaskId`, makes `ProcessBashHook`
      return before every `WriteHookHistory` with no chain named, and `cmd/hook.go` skips
      `triggerChain` on an empty chain. The workflow still transitions — the work is under way_
- [x] Fail closed when completion is indeterminate — _any `run_in_background` value but absent or
      `false` (an explicit `null` fails closed), any `backgroundTaskId` value, or a `PostToolUse` with neither an object
      `tool_response` nor an exit code all read as unfinished. `PostToolUseFailure` and Codex-shaped
      events are finished by construction (the failure line, the transcript)_
- [x] Emit a lifecycle row for a withheld edge — _`logChainWithheld`: `chain-withheld` naming chain,
      role, background task id (`unknown` when the response carried none) and the role-scrubbed,
      clipped command; a call feeding no chain logs nothing_

### Phase 3: Tests

- [x] Positive: a backgrounded long-running call fires no success edge before completion —
      _`TestProcessBashHook_UnfinishedCallWithholdsChain` (`bus/hook_test.go`): the measured
      payload, the timed-out shape, a credential in the command — no chain, no row, exactly one
      `chain-withheld` row; each fixture first shown to reach the watch edge without the guard_
- [x] **Negative control:** a normal completing run still fires its success edge —
      _`TestProcessBashHook_FinishedCallStillFiresChain`: explicit `false` flag, failure event
      carrying the flag, bare `exit_code`, Codex event — row kept, edge to watch, no `chain-withheld`;
      plus `TestProcessBashHook_ClaudeFailureEventFiresFailureChain`'s success case_
- [x] Indeterminate completion fails closed — _unfinished cases: flag of unexpected type, explicit
      `null`, task id of unexpected type, no `tool_response` and no exit code, non-object
      `tool_response`. Targeted run passed 2026-10-29 (hook-fired `Test succeeded`, `ts 1793292019`)_

### Phase 4: Docs

- [x] `CLAUDE.md` hook-driven-chains bullet: extend the precheck precedent to state that an incomplete
      call is never chain evidence — _by edit 2026-10-29: "Nor is an **unfinished call** evidence…"
      after the passing-precheck precedent, naming `backgroundLaunch`, `chain-withheld`, fail-closed
      and MUX-176; the `"0"`-default sentence narrowed to a finished Claude success payload_
- [x] [`docs/hooks.md`](../../hooks.md): the same, with this incident named — _2026-10-29: an
      "Unfinished calls" paragraph in the `hook bash` section after the MUX-204 failure-event
      paragraph (incident, measured mechanism, rule, signals, fail-closed, exemptions,
      `chain-withheld` row, what hooks cannot recover, pinning tests); the `"0"` default's boundary
      sentence narrowed the same way_

### Phase 5: Integration test

- [x] `scripts/test-chain-completion.sh`: background a sleeping script, assert no success edge fires
      before it ends, and that a normal run's edge still fires — _hermetic: a scratch bus against the
      installed binary, Claude payloads fed to the real `muxcode hook bash`; sections: a backgrounded
      `./build.sh` (workflow moves to building, no row, no test request; foreground build as control),
      the 2026-10-22 measured launch on a run agent (no row, no chain message, one `chain-withheld`
      row read through `lifecycle show`), a timed-out call carrying only a task id, an unreadable
      `PostToolUse` failing closed with task `unknown`, and the foreground control (row 0, watch
      request, no `chain-withheld`). Opt-in `--live` / `MUXCODE_CHAIN_COMPLETION_LIVE=1`: one real
      `claude -p` with `config/settings.json`'s Bash hooks (`--setting-sources project`) backgrounds
      a script that sleeps 12 s and exits 3, runs a foreground control, and a waiter holds the session
      open past the script's end so a hook fired at task end would have landed_
- [x] Coverage floor so a skipped section cannot read as green — _`EXPECTED_PASS=13`, an exact
      hermetic count; live checks are counted separately, outside the floor_
- [x] Run through the run agent and record the row here — _2026-10-29 12:58, see below_

There is no run-history row to record, by design — the run agent **backgrounded its own invocation**,
and the fix withheld it. What the live session holds instead:

| Where | Evidence |
|-------|----------|
| run agent's call | `bash scripts/test-chain-completion.sh --live > /tmp/test-chain-completion-live.log 2>&1; echo "EXIT_CODE:$?"`, with `run_in_background` |
| `run-history.jsonl` | **no row** for that call, and no watch request — before Phase 2 it would have read `exit 0` / `success` and sent "Run succeeded" within a second of dispatch |
| lifecycle log | `2026-10-29 12:58:56 info hook chain-withheld run chain not fired for run — the call had not finished when its hook fired (background task b2086uwvj): bash scripts/test-chain-completion.sh --live …` |
| script result (`/tmp/test-chain-completion-live.log`) | `=== results: 13 passed, 0 failed (floor 13), 5 live ===`, exit 0 — muxcode `v0.1.32-4-gdb0452b-dirty`, Claude Code 2.1.312, model haiku |
| live lines | `claude backgrounded slow-686x17332.sh as task b7kh343qz` · `the backgrounded script ran to its end (exit 3) before the session closed` · `no run-history row and no watch request for the backgrounded call, after it ended` · `chain-withheld names the run chain and Claude's task id b7kh343qz` · `control: the foreground call recorded 0 and requested watch` |
| rerun 13:04, **foreground** (task `bx8ukm6t2`) | after review's should-fix the live check requires the end marker to exist at Claude's return *and* the foreground waiter's result, so the script's end inside the session is asserted, not assumed; `13 passed, 0 failed (floor 13), 5 live`, exit 0 |
| `run-history.jsonl`, that rerun | an **ordinary row** — `ts 1793293459`, `exit_code "0"`, `success`, source `hook` — and the run chain sent watch "Run succeeded" the same second |

That is the incident's exact shape — a backgrounded integration script on the run agent — observed
with the fix in place: the real result reached edit only through the run agent's reply and the log
file, the one road the spec says is left. The foreground rerun beside it is the production negative
control: the same script, finished when its hook fired, keeps its row and its edge.

## Notes

**Caught by a person, not a mechanism.** The premature edge produced a false all-clear that nothing in
the system contradicted; it surfaced only because the run agent volunteered that its earlier signal
had been wrong. A chain edge that can be wrong and leave no trace is the part worth fixing — the
lifecycle row for a withheld edge in Phase 2 exists for that reason.

**Observed three times, all on 2026-09-10.** 10:31 on the first run; per watch again on the rerun; and
15:46:55 with a bare (non-compound) command — the occurrence that eliminated candidate 3 and put the
background artefact on disk. The defect is reproducible enough that Phase 1's remaining question is only
*which* of candidates 1 and 2 holds, not whether the premature edge is real.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-176-run-chain-fires-success-on-backgrounded-call | 3m | 2026-10-29 13:21 |

## Status

Complete — all phases 2026-10-29 (Phase 1 on 2026-10-22); every acceptance criterion ticked. Awaiting
commit, PR and the move to `completed/`, on the user's request
