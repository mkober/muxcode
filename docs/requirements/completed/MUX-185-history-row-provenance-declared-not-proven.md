# MUX-185: A History Row's Provenance Is Declared, Not Proven

**Tracking:** [mkober/muxcode#141](https://github.com/mkober/muxcode/issues/141)

`a8fa0db` (MUX-148, Decision 4) made history rows three-valued — `hook`, `self-reported`,
`bus-response` — and taught `latestAuthoritativeRow` to let an observed row outrank a self-report and
to treat anything else as not evidence. The label is **written by the writer**. An agent that appends
one line to `/tmp/muxcode-bus-<session>/<role>-history.jsonl` with `"source":"hook"` and
`"exit_code":"0"` has minted the one kind of evidence a node's verdict may rest on, and the reader
takes it. `TestRawRowWithoutHookSourceIsNotEvidence` pins the *sourceless* case; nothing pins the
*forged-label* case.

This cannot be closed inside `WriteHookHistory`, because the forgery shape does not go through the
binary at all. And it cannot be closed by the stamp MUX-148's Phase 3 names — `BusActorVerified` at
write time — because process ancestry tells a **person** from an **agent**, not a **hook** from an
**agent's shell**: `muxcode hook bash` and a forging `bash -c 'echo … >>'` sit under the same `claude`.
On a single-uid machine the hook and the forger are the same principal, with the same filesystem, the
same environment, the same place in the process tree and the same read access to any key a file could
hold. What remains is to raise the cost, add a witness the forger must also fabricate, or hold a secret
only the daemon has — and to say which, per road, and why.

## Context

### Source and standard of evidence

The residual was recorded, not closed, by plan under MUX-148's
[Decision 4](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md#decision-4--is-the-muxcode-log-writer-in-scope)
on 2026-09-14 when `a8fa0db` shipped, and left as that spec's one Phase 3 constraint that "should
stay unticked" in edit's 15:25 report — *"provenance the writer cannot author can't be fixed inside
`WriteHookHistory`, because the forgery shape is an agent appending to the JSONL without going
through the binary at all. That needs a daemon-owned writer or a keyed MAC — its own design, not an
increment on this phase."* Filed on the user's instruction, given to edit ("file the actor provenance
residual as a backlog item") and to plan directly. **Every mechanism claim below was verified by plan
against `7bcd657`** (with edit's uncommitted Phase 3 hunks in `graph_exec.go` shifting one line
number, noted where it matters). No live forgery has been observed; the nearest real occurrence is
the one `history_provenance.go:27-29` records — a commit agent self-logging exit 0 for its own
`git checkout -b` through `muxcode log` — which `a8fa0db` demoted to `self-reported`. The
hand-appended `hook` row is a **verified capability**, reproduced in Phase 1, not an incident.

### Mechanism — verified in code

| Step | Code | Behaviour |
|---|---|---|
| The label is a field on the row | `HookHistoryEntry.Source` (`bus/hook.go:614`) | `json:"source,omitempty"` — plain text in a JSONL line |
| The writer stamps it | `WriteHookHistory` (`bus/hook.go:626-628`) sets `SourceHook` when `Source` is empty; `cmd/log.go:141` sets `SourceSelfReported`; `cmd/send.go:555` and `daemon/daemon.go:3503` build `bus-response` rows through the same call | Every in-binary writer is honest; the binary is not the only writer |
| The file is world-readable, owner-writable, same uid as every agent | `OpenFile(path, O_APPEND\|O_CREATE\|O_WRONLY, 0644)` (`hook.go:639`); bus dir `0755`; `Flock(LOCK_EX\|LOCK_NB)` (`:645`) is advisory | `echo '{…"source":"hook"…}' >> <role>-history.jsonl` from any agent's shell succeeds; the lock stops nothing that does not ask for it |
| The reader believes the label | `latestAuthoritativeRow` (`bus/graph_exec.go:1849` in the working tree at filing; `:1740` at `a8fa0db`) — newest row with `Source == SourceHook` and a known outcome wins outright | A forged `hook` row outranks every genuine `self-reported` row and ties with genuine hook rows on recency |
| No guard on the write | `bus/guard.go` has no clause for `*-history.jsonl` (Decision 4's table) | Nothing at the hook layer refuses the append |
| The stamp MUX-148 proposed does not separate the two | `BusActorVerified` (`bus/config.go:167-179`) → `agentRuntimeAncestor` (`:185-212`) returns the nearest ancestor whose basename is in `agentRuntimeNames` (`:134-141`: the agent CLIs and the harness) | A hook process and a Bash-tool shell of the same agent both resolve to that agent. The stamp proves *an agent wrote this*, which is true of the forger |
| The tree already states the doctrine | `config.go:164-166`: *"This is a cost, not a boundary. Anything that can run a shell can write the approval marker it guards directly; what it removes is the cheap shortcut, leaving forgery, which the tool-profile guard is the thing that denies."* Same reading in `unverifiedHoldReleased` (`graph_exec.go:925-928`) | MUX-144 accepted exactly this residual for gate markers and named the guard as the closer. History rows have no guard |
| A witness exists on one road | `hook_codex.go:90-91`: `CodexExitCodeFromTranscript(ev.TranscriptPath, ev.ToolUseID)` — the Codex hook road already reads the real exit code from the provider's transcript by `tool_use_id` | A second artifact the forger did not write. Claude's transcript (`~/.claude/projects/<project>/<session>.jsonl`) records each tool result as a `user` record with `content[0] = {type:"tool_result", tool_use_id, content, is_error}` plus a top-level `toolUseResult` (verified on this session's file) |
| The payload carries the witness's address | `ToolEvent.ToolUseID`, `ToolEvent.TranscriptPath` (`hook.go:33-34`) | Present on the Codex road today; whether Claude's `PostToolUse` payload supplies both on the installed version is a Phase 1 measurement |

### Why the forger and the hook are one principal

| Capability | `muxcode hook bash` (genuine) | Agent shell (forger) |
|---|---|---|
| uid, filesystem | same | same |
| `BUS_SESSION`, role, bus dir | same environment | same environment |
| Nearest agent-runtime ancestor | `claude` / `codex` / `opencode` | the same process |
| Read access to a key in a file or env | yes | yes |
| Can invoke the binary | yes | yes — `muxcode log` is the honest road for exactly that |

No stamp the hook computes from what it can see is unavailable to the forger. Three things are:
a **secret held only in the daemon's memory** (never in a file or environment the agent inherits — and
`ps -E` shows own-process environments on macOS), a **second artifact under another writer's control**
(the provider transcript), and a **guard that refuses the cheap append** on roads that have a guard.
Each is a cost or a corroboration, not a boundary; the spec must say so rather than promise one.

### Where MUX-144 already drew this line

Gate approval markers had the same shape: any agent could write the file. MUX-144 made the marker
attributable (`approved_by`), authorized (`CheckGateApprovalAuthority`) and **corroborated** by a
`graph-gate-approved` lifecycle row (`gateApprovalHolds`, `graph_exec.go:901`), and accepted that a
process writing the file directly is forgery the tool-profile guard denies. The lifecycle log is the
same uid and `0644` (`~/.config/muxcode/logs/<session>.log`), so the corroboration there is also a
second artifact, not a boundary. History rows get the same treatment here — or a stronger one, since
a verdict is read on every node and a gate on a few.

### Blast radius

- One command, any role holding `Bash(*)`, mints a `hook` success for any node whose agent it can name — the row goes in the target role's file, not the writer's. The verdict then routes the node onward exactly as MUX-148's original omission did, with better paperwork.
- The reader ranks it **above** every honest self-report, so the demotion `a8fa0db` shipped is reversible by the party it demoted.
- Nothing logs it. There is no `history-row-rejected` because nothing rejects.
- Not observed live. Filed because the control it defeats is the one MUX-148 exists to install, and a control whose bypass is one line is a control on paper.

### Family

- [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) — the parent; its Phase 3 "add actor provenance" step is the pointer to this spec and stays open there.
- [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) — the same residual on gate markers, accepted with the guard as closer.
- [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) — "a role boundary an agent can ignore is not a boundary"; the guard road this spec would extend.
- [MUX-176](../completed/MUX-176-run-chain-fires-success-on-backgrounded-call.md) / [MUX-177](../completed/MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) — the same evidence class; rows written for the wrong reason rather than by the wrong hand.
- [MUX-161](../completed/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) — `BusActorVerified` is a `ps` walk; the Codex build sandbox cannot run it, so any ancestry stamp reads `unknown` there.

## Decisions (user, 2026-11-24)

The user's decisions, relayed by edit. The spec was parked on the five decisions its Phase 2 held;
all five are answered. The questions as posed are kept at the end of this spec.

| # | Question | Decision |
|---|----------|----------|
| D1 | Threat model | **(a) The one-line shortcut.** The adversary is an agent taking the shortcut its tools allow, not one that campaigns — the doctrine the tree already holds for gate markers. A separate-uid daemon stays out of scope |
| D2 | Witness, daemon secret, or both | **Witness corroboration plus the guard** — the spec's "3 + 1" — with the ancestry stamp shipped alongside as attribution only. **The daemon-held secret is deferred**, not built here |
| D3 | What the guard refuses | **(a) Writes to a history file under any bus dir**, in the cheap forms the guard can see (redirection, `tee`, `sed -i`, an interpreter given the literal path). Stated as a cost |
| D4 | What `actor` proves | **Person versus agent, and nothing more** — never hook versus shell |
| D5 | Legacy rows | **Rely on `since`; no migration.** Corrected in review 2026-11-24, the decision unchanged: `since` filters only rows older than a node's start, so a legacy row written *after* a node started — a node running across the upgrade — passes it. Such a row carries no witness fields and falls under the missing-witness rule: self-reported at best, never hook |

**What deferring the secret leaves open, so it is not read as closed:** roads with no transcript
(OpenCode, the local harness, scrape-road Codex) get no witness and no guard — a row there is
already self-reported at best, and stays so. A forger that fabricates both the row and a consistent
transcript record is outside D1's model. The secret becomes live if a forged row is ever observed on
a road the witness covers, or if a witness-less road needs a row to count as hook evidence; it is
then its own spec.

**State at the decision** (read by plan, 2026-11-24): the reader is `latestAuthoritativeRow`
(`bus/graph_exec.go:2826`), the writer `WriteHookHistory` (`bus/hook.go:980`), the existing witness
`CodexExitCodeFromTranscript` (`bus/hook_codex.go:120`); `ToolEvent` carries `ToolUseID` and
`TranscriptPath` (`bus/hook.go:42-43`); no guard file names `history.jsonl`. The bus directory is no
longer under `/tmp` — since MUX-208 it is `$XDG_STATE_HOME/muxcode/bus/muxcode-bus-<session>/` — so
the guard matches history files under the resolved bus base, and under a legacy `/tmp` one. The
Mechanism table's other line numbers are as filed at `7bcd657`.

## Requirements

### Acceptance criteria

- [x] The threat model is written down: the adversary is an agent taking the one-command shortcut, not one that campaigns; per road, each control is named a **cost**, a **corroboration** or a **boundary**, and nothing is called a boundary that a same-uid process can defeat — [The model](#the-model-2026-11-24), in this spec; carrying it into `docs/` is criterion 10's and Phase 4's
- [x] A row appended to `<role>-history.jsonl` outside the binary with `"source":"hook"` is **not** accepted as hook evidence by `latestAuthoritativeRow` on any road that has a witness — pinned by a test that writes the forged line and asserts the verdict falls through
- [x] A genuine hook row still wins outright (positive control), and a genuine `muxcode log` row is still evidence of last resort on non-hook providers (MUX-148 criterion 6) — no new holds on those roads
- [x] Every hook row carries what a reader needs to corroborate it where a witness exists — `tool_use_id` and `transcript_path` on the roads whose payload supplies them — and `actor` (`BusActorVerified`) on every row, documented as person-vs-agent provenance and nothing more
- [x] The reader corroborates a hook row against the provider transcript when the row names one: the `tool_use_id` exists, is a shell call whose command matches, and its result carries the exit code the row claims; a hook row that names **no** witness on a road that always supplies one ranks as self-reported at best, never hook
- [x] `hook guard` denies a Bash statement that writes to `*-history.jsonl` under a bus dir — redirection, `tee`, `sed -i`, an interpreter opening the path for write — logging `guard-denied`; a read (`cat`, `jq`, `tail`) is allowed — both directions tested — met 2026-11-24 by `CheckHistoryWriteGuard`, which matches the basename wherever the file is and so refuses more than "under a bus dir"
- [x] The daemon-held secret is recorded as deferred (D2), with what would make it live, in this spec and in `docs/architecture.md` — nothing is built for it here
- [x] Every rejection writes a lifecycle row naming file, row timestamp, role and reason (`history-row-rejected`), so a forgery attempt is visible where a forgery today is silent
- [x] Legacy rows — `hook`-labelled rows written before this change, without witness fields — need no migration, and are documented as such: one older than a node's start is excluded by the existing `since` filter, and one inside a node's window (the node started before the upgrade) ranks self-reported at best under the missing-witness rule, never hook
- [x] `docs/hooks.md` and `docs/architecture.md` state what a history row's provenance proves and does not prove, per road
- [x] Integration test `scripts/test-history-provenance.sh` covers the forged-row, genuine-row, guard-deny, guard-allow-read and legacy-row cases with a coverage floor

### Technical approach — options 3 + 1 chosen, with 4 as attribution; 2 deferred

| Option | Shape | Closes | Leaves open | Cost |
|---|---|---|---|---|
| 1 — witness corroboration | hook rows carry `tool_use_id` + `transcript_path`; the reader (or a verifier the reader calls) checks the transcript for that tool call and its exit code. Extends what `CodexExitCodeFromTranscript` already does on the Codex road | one-line forgery: the forger must now also fabricate a consistent transcript record under Claude Code's/Codex's file | a forger that edits both artifacts; roads with no transcript (OpenCode, local) — there the row is already at best self-reported | a transcript parse per verdict; transcript shape is the provider's, not muxcode's (the MUX-163 lesson: a bet on a constant muxcode does not own) |
| 2 — daemon-held secret | the hook hands the event to the daemon (bus request or spool file); only the daemon appends, with an HMAC under a per-session key generated at startup and held in memory; the reader verifies the MAC | a forger without the key — any agent, on every road, including ones with no transcript | an agent that reads daemon memory or restarts "the daemon" as itself; a daemon relaunch (`upgrade-daemons`, monitor) rotates the key, so rows across a relaunch need the old key or are legacy | the hook road becomes asynchronous through the daemon; key rotation; MUX-144's seal already shows a daemon-startup secret is workable |
| 3 — guard the append | `hook guard` refuses Bash statements whose write target is a bus-dir history file | the cheap shortcut on the hook roads (Claude, Codex-hook) — the closer MUX-144 named | OpenCode/local (no `PreToolUse`; `DenyTools` and the pane audit are the analogues); shell-heuristic bypasses (`$(printf …)` paths) | small; a pattern in `bus/guard.go` and tests in both directions |
| 4 — ancestry stamp alone | `actor = BusActorVerified()` on every row | person-vs-agent attribution (useful for `muxcode log` rows and audit) | **the residual itself** — hook and forger share the ancestor | trivial; ships as part of any option, never as the answer |

**Decided 2026-11-24: 3 + 1 now, 2 deferred.** The guard removes the shortcut where a guard exists,
the witness makes the remaining forgery a two-artifact job where a transcript exists, and both are
honest about the roads they do not cover. Option 2 is the only one that reaches every road, and it
is the one edit's report named; it is also the one that changes how the hook road writes, so it
waits for evidence that 1's residual is live. Option 4 ships with them as person-versus-agent
attribution (D4), never as the answer.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/history_provenance.go:26-49` | the three-valued vocabulary and its own residual note (`:44-48`) |
| `tools/muxcode/bus/hook.go:25-39,597-660,795-850` | `ToolEvent` (`ToolUseID`, `TranscriptPath`); `HookHistoryEntry`; `WriteHookHistory` (stamps `hook`, `0644`, advisory flock); the five `ProcessBashHook` write sites |
| `tools/muxcode/bus/hook_codex.go:90-91` | `CodexExitCodeFromTranscript` — the existing transcript witness |
| `tools/muxcode/cmd/log.go:135-146` | the honest self-report road, through `WriteHookHistory` |
| `tools/muxcode/cmd/send.go:555`, `tools/muxcode/daemon/daemon.go:3503` | `bus-response` writers |
| `tools/muxcode/bus/graph_exec.go` (`latestAuthoritativeRow`, `deriveSendOutcome`; `:1849`/`:1786` in the working tree at filing) | the reader; `since = st.StartedAt` |
| `tools/muxcode/bus/config.go:134-141,164-212` | `agentRuntimeNames`, the "cost, not a boundary" doctrine, `BusActorVerified`, `agentRuntimeAncestor` |
| `tools/muxcode/bus/guard.go` | no history-file clause today |
| `tools/muxcode/bus/console.go:51,101,130` | other consumers of `Source` — must keep rendering rejected/legacy rows without treating them as verdicts |
| `~/.claude/projects/<project>/<session>.jsonl` | Claude's transcript: `user` records with `content[0].tool_use_id`, `is_error`, `toolUseResult` |
| `docs/hooks.md`, `docs/architecture.md` | where provenance semantics are documented |

## Implementation

### Phase 1: Establish the boundary

- [x] Reproduce: on a scratch session, append a `"source":"hook","exit_code":"0","outcome":"success"` line to a role's history file by hand and show `latestAuthoritativeRow` returns it — a test asserting today's behaviour, so the suite stays green; Phase 4 flips its expectation and it stays as the negative control
- [x] Record whether the installed Claude Code's `PostToolUse` payload carries `tool_use_id` and `transcript_path`, and whether the transcript record for that id carries the command and the exit code (shape verified on 2026-09-14: `is_error` and `toolUseResult`; the exit code's field is to be confirmed)
- [x] Record the same for the Codex hook road (already read by `CodexExitCodeFromTranscript`) and for OpenCode and the local harness (expected: no transcript, no witness)
- [x] Record whether the process tree distinguishes a hook invocation from a Bash-tool shell (Claude's Bash tool sources a `shell-snapshots` file; hooks run under `sh -c`) — a cost at most, and a provider-internal shape
- [x] Record what `hook guard` can see of a write target — redirection, `tee`, `sed -i`, interpreter opens — and which forms it cannot
- [x] Measure the cost of a transcript lookup per verdict on a long session file — the worker's own figure (finding 6); no run-agent row backs it
- [x] Record the findings here. If the Claude payload or transcript does not carry what the witness needs (the second step), stop and report: D2 chose the witness on the expectation that it does, and a road without it is the user's to re-decide

#### Phase 1 findings (2026-11-24)

Reported by the run's implement worker (`spawn-5f179934`, run `1795547986`) and recorded by plan as
reported; plan has not re-run or re-read any of it. **The stop condition is not met** — the witness
fields are present on Claude, so the work continues to Phase 2.

**Verified by plan 2026-11-24** (`verify-spec`, run `1795547986`): the test is in the tree
(`graph_exec_test.go`, last written 14:23:47), `./test.sh` passed at 14:25 with it (2 modules, 0
failed), review returned 0 must-fix, 0 should-fix, 0 nits, and the run agent's history holds both
live `claude -p` captures — the payload keys (14:22) and the process tree (14:23). The Codex, guard
and lookup-cost findings are the worker's reading and measurement; the guard primitives it names
exist in `bus/hook.go`. Phase 1 is 7/7; no acceptance criterion is met by it.

**1. Reproduction.** `TestForgedHookRowIsTakenAsEvidence` (`tools/muxcode/bus/graph_exec_test.go`):
an honest self-reported failure written by `WriteHookHistory`, then a hand-appended
`{"exit_code":"0","outcome":"success","source":"hook"}` line; it asserts `latestAuthoritativeRow`
returns the forged hook success. It runs ungated and is green today; Phase 4 flips it. It replaces
an earlier red test that was skipped unless `MUXCODE_PIN_MUX185` was set, which asserted nothing in
a normal run.

**2. Claude** (installed 2.1.331; a live `claude -p` capture, `BUS_SESSION` unset).

| Question | Finding |
|----------|---------|
| `PostToolUse` payload keys | `cwd`, `duration_ms`, `effort`, `hook_event_name`, `permission_mode`, `prompt_id`, `session_id`, `tool_input`, `tool_name`, `tool_response`, `tool_use_id`, `transcript_path` |
| `PostToolUseFailure` | The same, with `error` and `is_interrupt` in place of `tool_response` |
| Witness fields | **Both payloads carry `tool_use_id` and `transcript_path`** |
| Transcript, the command | An assistant record `message.content[].{type:"tool_use", id, input.command}` — verbatim |
| Transcript, the result | A user record `message.content[0].{tool_use_id, is_error, content}` plus top-level `toolUseResult` |
| Exit code | **No exit-code field.** Success is `is_error:false` with `toolUseResult` `{stdout, stderr, interrupted, isImage, noOutputExpected}`, so exit 0 is implied. Failure is `is_error:true`, with `content` and the `toolUseResult` text leading `Exit code N` — a string, not a field |
| Background calls | A `run_in_background` call also reads `is_error:false` (with `backgroundTaskId`); the honest hook writes no row for it since MUX-176, so it is not a hole for the honest writer — but a hand-appended row can cite that genuine unfinished call's id, so the reader must reject it (corrected in review 2026-11-24) |

So the witness can check three things: the id exists, the command matches, and `is_error:false` ⇔
the row's exit 0, or a leading `Exit code N` equal to the row's code. This answers Phase 1's "the
exit code's field is to be confirmed": there is none, and the criterion's "its result carries the
exit code the row claims" is met by that reading, not by a field.

Corrected in review 2026-11-24: `is_error:false` alone does not prove exit 0. The witness accepts
only **completed foreground** evidence — on Claude, `is_error:false` and no `backgroundTaskId` in
`toolUseResult` and `interrupted` false (failure: `is_error:true` with a leading `Exit code N`); on
Codex, an `item_completed` record with a recorded `exit_code`. Anything background, interrupted or
otherwise not final rejects the row, with a reason naming it.

**3. Other roads.**

| Road | Witness |
|------|---------|
| Hook-road Codex | Complete: the rollout `item_completed` record for the `exec-…` id carries `command` and `exit_code` (read today by `CodexExitCodeFromTranscript`, retried 6 × 250 ms) |
| OpenCode, scrape-road Codex | None: no hook fires and muxcode reads no transcript; history rows come only from `muxcode log`, self-reported |
| Local harness (`harness/bus.go` `LogHistory`), the `muxcode agent` loop (`bus/agent.go`) | Sourceless rows — already not evidence (MUX-148) |

Consequence: **no road without a witness ever writes a genuine `hook` row.** The only `hook` rows are
`ProcessBashHook`'s, on Claude and hook-road Codex, both witness-bearing. A `hook` row with no
witness fields is therefore forged or legacy on every road, and ranking it self-reported at best
costs no genuine evidence anywhere.

**4. Process tree** (live capture, one `claude -p`). The Bash-tool shell is
`/bin/bash -c source ~/.claude/shell-snapshots/snapshot-bash-….sh …` and the `PostToolUse` hook is
`/bin/sh -c <hook command>`; both have the same claude pid as parent, and only the argv shape
differs. A forger's processes descend from the snapshot shell and cannot become a direct child of
claude, but argv is provider-internal and imitable — a cost at most, never provenance. The cheapest
forgery needs no ancestry at all: pipe a crafted payload into `muxcode hook bash` from the agent's
shell, and the binary writes an honest-looking hook row. Only the witness defeats that shape.

**5. What `hook guard` can see of a write target.**

| Visible today (`bus/hook.go`) | Not visible |
|-------------------------------|-------------|
| `>` / `>>` / `>|` redirect targets (`redirectTargets`) | `cp` / `mv` / `install` / `dd of=` onto the file — no redirect syntax; cheap to add |
| `tee` file arguments (`teeTargets`) | An interpreter `open()` (`python3 -c`), unless the path is a literal in argv |
| `sed -i` / `perl -i` (`hasInPlaceEdit`) | A computed basename (`$(printf …)`) |
| | `muxcode hook bash` fed a forged payload on stdin — it goes through the binary |

Targets are seen as written — `$B/test-history.jsonl` and `$(muxcode bus-dir)/test-history.jsonl`
keep the literal basename — so the match is on the `*-history.jsonl` basename, not a resolved
directory. `CheckBashFileWriteGuard` is gated by `HasGuardRules(role)`; the history clause must
apply to every role. The worker's recommendation for the stdin road: deny an agent's Bash call that
pipes into `muxcode hook`.

**6. Lookup cost.** The largest transcript on this machine is 70 MB, 29,722 lines. A full scan for
one `tool_use_id` took 0.33 s with `grep` and 0.36 s with a `jq` select; a Go stream with a
`bytes.Contains` prefilter (`scanTranscriptForItem`'s shape) is the same order. Tolerable per
verdict, not free: Phase 4 may cache and may read from the tail, since the id is recent.

Corrected in review 2026-11-24: the worker's "verify once per row (cache by `tool_use_id`)" would
let an accepted verdict be reused for a later forged row naming the same id with a different
command, exit code or `transcript_path`. The cache holds the **parsed witness facts**, never a row's
acceptance. Its key is the transcript's canonical identity (`transcript_path` through
`EvalSymlinks`, after the provider-root pin) plus `tool_use_id`; its value is what the transcript
says — command, `is_error` or exit code, and completion state. Every candidate row is compared
against those facts on every read; a hit saves only the scan.

**What these findings add to the phases below** (written into Phase 3's guard step by plan the same
day, when Phase 2 was recorded):

- Criterion "`hook guard` denies … under a bus dir" and D3 say *under any bus dir*; finding 5 says
  the guard can only match the basename. A `*-history.jsonl` outside a bus dir would be refused too.
- Two forgery roads the criteria do not name: `cp`/`mv`/`install`/`dd` onto the file, and a forged
  payload piped into `muxcode hook bash`. The second is defeated by the witness; whether the guard
  also denies it is not in Phase 3's step as written.

### Phase 2: Write the model down

- [x] The threat model as decided (D1), with each control classified per road — Claude, hook-road Codex, scrape-road Codex, OpenCode, the local harness — as cost, corroboration or boundary; nothing a same-uid process can defeat is called a boundary. Recorded in this spec, for Phase 4's docs step to carry into `docs/` — [The model](#the-model-2026-11-24) below
- [x] The deferred daemon secret (D2): why it waits and what would make it live — in the same section
- [x] The legacy-row rule (D5) recorded in MUX-148 as the closer of its Phase 3 pointer — its "Closed by MUX-185" paragraph, 2026-11-24

#### The model (2026-11-24)

Drafted by the run's implement worker (`spawn-5f179934`) from the decisions and the Phase 1
findings, recorded by plan. Phase 4's docs step carries it into `docs/hooks.md` and
`docs/architecture.md`.

**The adversary** is an agent taking the one-command shortcut its tools allow: appending a line, or
piping a payload into the binary. It is not one that campaigns — that fabricates a consistent second
artifact on purpose, reads daemon memory, or replaces the daemon. Everything here runs as one uid,
and nothing a same-uid process can defeat is called a boundary. **No road has a boundary.** The only
one available, a separate-uid writer, is out of scope (D1).

| Word | Means |
|------|-------|
| Cost | Removes the cheap form; a determined agent routes around it |
| Corroboration | Requires a second artifact under another writer's control — which the same uid can still write |
| Attribution | Says who, not whether |
| Detection | Makes an attempt visible; prevents nothing |

| Control | Claude | Hook-road Codex | Scrape-road Codex | OpenCode | Local harness, `muxcode agent` |
|---|---|---|---|---|---|
| Guard on history-file writes | Cost — `PreToolUse` sees the command string; it misses computed names and an interpreter's `open()` | Cost — same guard, same gaps | None — no `PreToolUse` | None — `DenyTools` and the pane audit are the analogues, not this guard | None |
| Transcript witness | Corroboration — `~/.claude/projects/…` is same-uid and appendable | Corroboration — the rollout under `$CODEX_HOME/sessions`, likewise | Not applicable — writes no `hook` rows | Not applicable — only `muxcode log` rows, self-reported | Not applicable — rows are sourceless, already not evidence |
| A `hook` row with no witness fields ranks self-reported at best | Holds — genuine rows always carry them | Holds | Holds — a `hook` row here is forged or legacy by construction (Phase 1 finding 3) | Holds, same | Holds, same |
| `actor` stamp | Attribution — person versus agent only (D4); the hook and the agent's shell share the `claude` ancestor | Attribution; reads `unknown` inside the Codex sandbox (MUX-161) | Attribution | Attribution | Attribution |
| `history-row-rejected` row | Detection | Detection | Detection | Detection | Detection |
| `since` filter (D5) | Excludes rows older than the node's start — half the legacy rule, not a control. A legacy row inside the node's window is caught by the missing-witness row above | Same | Same | Same | Same |

**The witness's address must not come from the row unchecked.** `transcript_path` is written by the
same hand as the row. A forger that writes `"transcript_path":"/tmp/fake.jsonl"` and that file has
produced two artifacts, both its own, and the witness corroborates nothing. The reader accepts a
`transcript_path` only under the provider's own transcript root — `~/.claude/projects/` for Claude,
`$CODEX_HOME/sessions` or `~/.codex/sessions` for Codex — resolved through `EvalSymlinks`. Binding
it to the role's session is tighter still: Claude's payload carries `session_id` and the transcript's
basename is `<session_id>.jsonl`. Even then, appending a record to the agent's real transcript stays
possible; that is why this is corroboration and not a boundary, and why it sits outside D1's model.
Plan added this to Phase 4 and Phase 5 as steps on 2026-11-24. It is not one of the five decisions;
it is what "witness corroboration" needs in order to be one.

**The cheapest forgery** (Phase 1 finding 4) is a crafted payload piped into `muxcode hook bash`,
which writes a row the binary believes. The guard can refuse an agent's Bash call that pipes into
`muxcode hook` — a cost. The witness defeats it on Claude and Codex, because the claimed
`tool_use_id` must exist in the agent's transcript with that command and exit code.

**The deferred daemon secret (D2).** It is the only option that reaches every road, and it waits
because it changes how the hook road writes: the hook hands the event to the daemon, which alone
appends, under a per-session HMAC key held in memory. That makes the write asynchronous, rotates the
key on every daemon relaunch (`upgrade-daemons`, the monitor), and needs a rule for rows across a
rotation. Under D1 the guard and the witness remove the shortcut on every road that can produce a
genuine `hook` row, so the secret would buy protection against an adversary the model excludes. It
is not a boundary either: the same uid can read daemon memory or replace the daemon. It becomes
live, as its own spec, when any of these happens:

- A forged row is observed on a road the witness covers
- A witness-less road (OpenCode, the harness, scrape-road Codex) needs a row to count as hook evidence
- A provider changes its transcript shape so the witness can no longer be read — the shape is the
  provider's, not muxcode's (the MUX-163 lesson) — leaving Claude or Codex rows uncorroborable

### Phase 3: Writer side

- [x] `HookHistoryEntry` gains `actor`, `tool_use_id` and `transcript_path`; `ProcessBashHook` fills them from the payload; `cmd/log.go` fills `actor` only; `actor` is documented where it is declared as person-versus-agent provenance and nothing more (D4)
- [x] `hook guard` refuses a Bash statement that writes to a history file under any bus dir — the resolved bus base and a legacy `/tmp/muxcode-bus-*` — in the cheap forms (D3), with `guard-denied`; unit tests in both directions (write refused; `cat`/`jq`/`tail` allowed; a write to an unrelated `.jsonl` allowed). From Phase 1 finding 5, added by plan 2026-11-24: the guard sees the target as written, so the match is on the `*-history.jsonl` basename and such a file outside a bus dir is refused too; the clause applies to every role, not only those `HasGuardRules` names; and the cheap forms include `cp`/`mv`/`install`/`dd of=` onto the file and a Bash call that pipes into `muxcode hook`
- [x] Unit test: `TestWriteHookHistoryStampsSource` extended to the new fields; a row written by `WriteHookHistory` with none of them is still valid on roads that have no witness

**Verified by plan 2026-11-24** (`verify-spec`, run `1795547986`): `HookHistoryEntry` carries
`actor`, `tool_use_id` and `transcript_path` (`bus/hook.go`), `ProcessBashHook`'s five write sites
share one `observed()` base, and `cmd/log.go` stamps `actor`. The guard is
`CheckHistoryWriteGuard` (new `bus/history_guard.go`), in `GuardDecisionFor`'s list for every role;
`history_guard_test.go` holds refused and allowed cases, an every-role test and a quoted
`muxcode hook bash` test; `TestWriteHookHistoryStampsSource` is extended and
`TestProcessBashHookRecordsWitness` is new. Files last written 14:41; `./build.sh` passed 14:42,
`./test.sh` 14:43; review returned 0 must-fix, 2 should-fix at 14:40 (a quoted hook invocation
missed, a read-only interpreter refused) and 0/0/0 at 14:44 after the fix. Plan read the code and
the rows; it ran nothing.

What the worker reports as not covered, by design: computed basenames, a heredoc script fed to an
interpreter, and OpenCode (no `PreToolUse`). The guard refuses more than D3 lists — `ln`, `rsync`,
`&>`, the same inside `bash -c` or `eval`, and `muxcode hook bash` in any form.

**Found at verification, open:** `NewBusResponseEntry` (`bus/history_provenance.go`), the writer of
`bus-response` rows, sets no `actor`. The step above names only `ProcessBashHook` and `cmd/log.go`,
so it is met as written; acceptance criterion 4 says "`actor` on every row" and was not. **Closed in Phase 4:** `cmd/send.go` and `daemon/daemon.go` now stamp it.

### Phase 4: Reader side

- [x] `latestAuthoritativeRow` corroborates a `hook` row that names a witness before accepting it; a `hook` row with no witness on a witness-bearing road ranks as self-reported at best; other roads unchanged. Only completed foreground evidence corroborates: on Claude `is_error:false` with no `backgroundTaskId` and `interrupted` false reads as 0, `is_error:true` as its leading `Exit code N`; on Codex an `item_completed` record with its `exit_code`; a background, interrupted or non-final call rejects the row with a reason naming it. A cache may hold the parsed witness facts, keyed on the transcript's canonical path plus `tool_use_id` — never a row's acceptance; every row is compared against the facts on every read
- [x] The reader accepts a row's `transcript_path` only under the provider's own transcript root, resolved through `EvalSymlinks`; a path elsewhere is rejected with that reason, never read — added by plan 2026-11-24 from the model above
- [x] `history-row-rejected` lifecycle row on every refusal, naming file, row ts, role and reason; Phase 1's reproduction flips in the same change
- [x] Negative controls: forged row → not evidence; genuine hook row → wins; genuine self-report on OpenCode → still last-resort evidence; legacy `hook` row before `since` → ignored as today; legacy `hook` row with no witness fields written after the node started → self-reported at best, not hook evidence; a genuine row verified (cache warm), then a forged row with the same `tool_use_id` and a different exit code, command or `transcript_path` → rejected; a genuine unchanged row read twice → accepted both times; a forged success row citing a genuine background call → rejected, beside a completed-success row → accepted
- [x] `docs/hooks.md` and `docs/architecture.md` describe what a row's provenance proves per road, and CLAUDE.md's evidence-guard bullet gains the history-file rule

**Phase 4 review, 2026-11-24** (reported by the fix node, recorded by plan before verification): the
first review returned three must-fixes and the reader changed with them. A `hook` row whose own
witness contradicts it is **no evidence at all** — it is not kept as a last resort, as the first
implementation had it. Only a row naming no witness survives, relabelled `self-reported` and ranked
below every genuine self-report. The row's outcome must agree with the corroborated exit code.
Completion must be stated, not inferred: on Claude `is_error` present, and on false no
`backgroundTaskId` key at all and `interrupted` present and false; on Codex a numeric `exit_code`.
`docs/hooks.md` and `docs/architecture.md` were corrected to this the same hour.

**Verified by plan 2026-11-24 15:08** (`verify-spec`, run `1795547986`), against the tree as it
stood after the review fixes — the worker's report predates them and still describes the first
implementation's "unconfirmed hook as last resort". Reader: `corroborateHookRow`
(`bus/history_witness.go`) and `latestAuthoritativeRowFunc` (`bus/graph_exec.go`), read by plan.
Tests present: `TestForgedHookRowIsNotEvidence` (Phase 1's reproduction, flipped),
`TestCorroborateHookRow`, `TestWitnessCacheComparesEveryRow`,
`TestWitnessSeesCallsWrittenAfterAMiss`, `TestClaudeWitnessRequiresAffirmativeCompletion`,
`TestCorroborateCodexRow`, `TestDeriveSendOutcomeIgnoresContradictedRow`,
`TestReaderNegativeControls`. Files last written 15:03:32; `./build.sh` passed after that,
`./test.sh` at 15:05. Review: 3 must-fix at 14:54, 1 must-fix at 15:03 (`interrupted: null` passed
as complete), 0/0/0 at 15:06. The Phase 3 gap is closed: `bus-response` rows now stamp `actor`
(`cmd/send.go`, and `daemon` in `daemon/daemon.go`). Plan read the code and the history rows and
ran nothing. Phase 4 is 5/5; criteria 2–5 and 7–10 are met by it, on unit tests — criterion 11, the
integration script, is Phase 5.

### Phase 5: Integration test

- [x] Create `scripts/test-history-provenance.sh` — hermetic: a scratch bus dir, a stand-in transcript file in the provider's shape, the installed `muxcode`
- [x] Forged-row control: append a `hook` success line by shell → `muxcode graph` verdict path (or a direct reader invocation) falls through; `history-row-rejected` logged
- [x] Genuine-row control: a row written by `muxcode hook bash` with a matching transcript record → accepted as hook
- [x] Mismatch control: a hook row whose `tool_use_id` exists but whose exit code disagrees with the transcript → rejected with the reason naming the disagreement
- [x] Fake-witness control: a hook row whose `transcript_path` names a file outside the provider's transcript root, holding a record that matches the row exactly → rejected, the reason naming the path — added by plan 2026-11-24
- [x] Guard controls: `hook guard` on a redirect to `test-history.jsonl` → denied; on `jq . test-history.jsonl` → allowed
- [x] Legacy control: a pre-change `hook` row older than the node's start → ignored, no rejection row (it was never read)
- [x] Legacy-in-window control: a `hook` row with no witness fields written after the node started → not hook evidence, ranks self-reported at best — added in review 2026-11-24
- [x] Warm-cache control: a genuine row accepted, then a forged row naming the same `tool_use_id` with a different exit code → rejected; the genuine row read again → still accepted — added in review 2026-11-24
- [x] Unfinished-call control: a forged success row citing a genuine `run_in_background` call's id → rejected, the reason naming the unfinished call; a completed-success row beside it → accepted — added in review 2026-11-24
- [x] Witness-less road control: a `self-reported` row on an OpenCode-configured role is still last-resort evidence, and a `hook`-labelled row there with no witness ranks no higher
- [x] Control: a muxcode built from a scratch copy with the corroboration bypassed accepts the forged row, which the forged-row control rejects
- [x] Coverage floor: every section asserts at least once; a skipped section fails the script
- [x] Run the script and record the pass/fail counts here

#### Phase 5 results (2026-11-24)

`bash scripts/test-history-provenance.sh`, run by the run agent at 15:10 (request
`1795550989-spawn-5f179934-c759d548`): **28 passed, 0 failed, floor 28, exit 0.** Read by plan from
the run agent's history row, watch's check of the log, and the log itself (`/tmp/mux-histprov.log`,
written 15:10:42 — after the script's last edit at 15:09:45; a `/tmp` file, so this table is the
record). Plan did not run it.

| Section | Checks | What passed |
|---------|-------:|-------------|
| F — forged row | 2 | The honest self-reported failure decides over a forged `hook` success; `history-row-rejected` names the missing witness |
| G — genuine row | 3 | A witnessed `hook` success outranks a newer self-reported failure; no rejection row; the row carries `tool_use_id`, `transcript_path` and `actor` |
| M — mismatch | 2 | A row its transcript contradicts decides nothing and the node holds; the rejection names the disagreement |
| W — fake witness | 2 | A matching transcript outside the provider root corroborates nothing; the rejection names the path |
| H — guard | 3 | A redirect into `build-history.jsonl` is denied and `guard-denied` logged; a `jq` read is allowed |
| L — legacy before the node | 2 | Never evidence; no rejection row, since it was never read |
| N — legacy inside the window | 2 | A witness-less `hook` row ranks below the honest self-report; the rejection names the missing witness |
| C — warm cache | 3 | The genuine row is accepted, accepted again on the same daemon over a newer forgery, and the forgery reusing its id is rejected |
| U — unfinished call | 3 | A background call's id corroborates nothing alone; the rejection names it; a completed success beside it decides |
| O — witness-less road | 2 | An OpenCode role's self-report is still last-resort evidence; a witness-less `hook` row there ranks no higher |
| Private tmux server | 1 | The scratch sessions live on it |
| X — control | 3 | A muxcode built from a copy with corroboration bypassed builds, and takes F's forged row as success — so F can see the defect |

**Verified by plan 2026-11-24 15:16** (`verify-spec`, run `1795547986`): nothing changed after the
run — the script was last edited 15:09:45 and `bus/history_witness.go` 15:07:49 (the tweak that
makes the outside-root rejection name the path), both before the 15:10 run. `./build.sh` passed at
15:11, `./test.sh` at 15:13, and review returned 0 must-fix, 0 should-fix, 0 nits at 15:14.
`CLAUDE.md`'s scripts row names the script. Phase 5 is 14/14 and criterion 11 is met.

Two differences from the steps as written above. The script builds muxcode from the tree
(`MUXCODE_HISTORY_PROVENANCE_BIN` overrides) rather than using "the installed `muxcode`". And each
verdict is the real one: a scratch daemon runs a one-node graph `build`, the script plays the build
agent and answers with no `EXIT=` sentinel, so the rows alone decide the node — not a direct reader
invocation.

## The decisions as posed

Answered by the user on 2026-11-24 — see [Decisions](#decisions-user-2026-11-24). The questions are
kept as written, for the reasoning.

### Decision 1 — threat model

An agent taking the shortcut its tools allow, or an agent that campaigns (edits two artifacts, reads
daemon memory, replaces the daemon)? The tree's own doctrine (`config.go:164-166`) chose the first
for gate markers. Choosing it here is what makes options 1 and 3 sufficient; choosing the second makes
nothing on a single uid sufficient and puts a separate-uid daemon in scope, which this spec does not
propose.

### Decision 2 — witness, daemon secret, or both

The witness covers the roads that have transcripts and costs a parse; the secret covers every road
and changes how the hook road writes. Edit's report named the secret; the evidence says the witness
plus the guard closes the observed shape. Both can ship; the order is the decision.

### Decision 3 — what the guard refuses

Writes to `*-history.jsonl` under any bus dir, or under the caller's own session only? Redirection
and `tee` are visible; an interpreter's `open()` is not unless the path is literal. The guard is a
cost; its scope should be the cheap forms, stated as such.

### Decision 4 — what `actor` proves

Person versus agent, resolved by ancestry — useful for `muxcode log` rows (a person's self-report and
an agent's are different evidence) and for audit, and **not** hook-versus-shell. Recording it as more
than that would recreate the misunderstanding this spec corrects in MUX-148's Phase 3 wording.

### Decision 5 — legacy rows

`since` already keeps a node from reading rows older than its start, so rows written before this
change are never a live node's evidence. *(As posed. Not quite true, found in review 2026-11-24: a
node started before the upgrade reads a legacy row written after its start. The missing-witness rule
covers that row; see D5 above.)* Whether the console and `muxcode lifecycle` should also
mark them is a display question, not a verdict question.

## Out of scope

- **A daemon under a separate uid, or any kernel-enforced boundary** — the only true boundary on a shared machine; not proposed.
- **MUX-148's remaining Phase 3 items** — the signal tied to the dispatched task, the spawn road, double-holds — unchanged; this spec closes one constraint of that phase.
- **The `bus-response` road** — already never evidence.
- **The lifecycle log's own forgeability** — same class (same uid, `0644`), same doctrine; a separate spec if MUX-144's corroboration is ever judged insufficient.
- **`muxcode log` as a road** — stays the honest self-report; demoting it further is MUX-148's call, not this one's.

## Status

**Complete** — 43/43, closed out 2026-11-24 by plan (`close-spec`, `55-defect-backlog-loop` run
`1795547986`). All five phases verified (7/7, 3/3, 3/3, 5/5, 14/14) and all 11 criteria met.
Uncommitted at close-out; the run's ship step commits it.

**What shipped.** A `hook` history row counts as observed evidence only when the provider's own
transcript corroborates it (`bus/history_witness.go`): the row's `tool_use_id` in a transcript under
the provider's root, the same command, affirmatively finished in the foreground, the same exit
code, an agreeing outcome. A row its witness contradicts is no evidence; a row naming no witness is
read as self-reported, below every genuine self-report. `hook guard` refuses, for every role, a
write to a `*-history.jsonl` file or a call to `muxcode hook bash` (`bus/history_guard.go`). Rows
carry `actor`, `tool_use_id` and `transcript_path`. Each refusal writes `history-row-rejected`.
Proved end to end by `scripts/test-history-provenance.sh`, 28 passed, 0 failed, floor 28, including
a control built with corroboration bypassed.

**Left for the user.** The daemon-held secret is deferred, with what would make it live recorded
under "The model". Three things went past the five decisions and were flagged to edit: the reader
pins `transcript_path` to the provider's transcript root; the guard matches the history basename
anywhere, not only under a bus dir; and it refuses more forms than D3 lists (`cp`, `mv`, `install`,
`ln`, `rsync`, `dd`, the same inside `bash -c` or `eval`, and `muxcode hook bash`). Not covered, by
design: computed basenames, a heredoc script fed to an interpreter, and roads with no `PreToolUse`
hook. Plan verified by reading code, logs and history rows, and ran nothing itself.

**History of the count:** 43/43 was reached 2026-11-24 15:16, when Phase 5 was verified. Phase 2 verified 2026-11-24 after its re-review returned 0 must-fix, 0 should-fix, 0 nits at 14:33. The count stood at 10/43 after the Phase 2 review (0 must-fix, 3 should-fix, all applied to the text 2026-11-24: the cache holds witness facts not verdicts, an unfinished call is not exit 0, a legacy row inside a node's window is self-reported at best — three Phase 5 controls added). Phase 1 complete and verified 2026-11-24 (7/7); Phase 2's three doc steps recorded by plan the same day (3/3), awaiting the run's verification; Phase 3 next. Two steps were added by plan from the Phase 2 model — the transcript-path pin (Phase 4) and its control (Phase 5) — which with the three review controls is why the total is 43, not 38. Activated 2026-11-24 by `55-defect-backlog-loop` run
`1795547986-55-defect-backlog-loop-8444bd99` (Defects rank 2; rank 1, MUX-215, is parked). Five
phases. Phase 1 ends with a stop-and-report step if Claude's hook payload lacks the witness fields
the decision assumes — that has not been measured.

Parked 2026-11-13 on five undecided questions; **unparked 2026-11-24** on the user's decisions,
relayed by edit: the shortcut threat model, witness corroboration plus the guard with the daemon
secret deferred, the guard over history files under any bus dir in the cheap forms, `actor` as
person-versus-agent only, and `since` with no migration.

Filed 2026-09-14 on the user's instruction ("file the actor provenance residual as a backlog item",
given to edit, and "file defect for this" to plan), from the residual plan recorded under MUX-148
Decision 4 when `a8fa0db` shipped and edit restated at 15:25. Every mechanism claim verified by plan
against `7bcd657`. Two findings are plan's, not the report's: the ancestry stamp MUX-148's Phase 3
names would not separate a hook from an agent's shell, since both resolve to the same runtime; and a
transcript witness already exists on the Codex road (`CodexExitCodeFromTranscript`) and Claude's
transcript records tool results by `tool_use_id`, so corroboration is an extension, not an invention.
No live forgery observed. Not started.
