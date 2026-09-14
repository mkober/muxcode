# MUX-185: A History Row's Provenance Is Declared, Not Proven

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
[Decision 4](../drafts/MUX-148-node-outcome-reads-command-ran-as-task-done.md#decision-4--is-the-muxcode-log-writer-in-scope)
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

- [MUX-148](../drafts/MUX-148-node-outcome-reads-command-ran-as-task-done.md) — the parent; its Phase 3 "add actor provenance" step is the pointer to this spec and stays open there.
- [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) — the same residual on gate markers, accepted with the guard as closer.
- [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) — "a role boundary an agent can ignore is not a boundary"; the guard road this spec would extend.
- [MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) / [MUX-177](./MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) — the same evidence class; rows written for the wrong reason rather than by the wrong hand.
- [MUX-161](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) — `BusActorVerified` is a `ps` walk; the Codex build sandbox cannot run it, so any ancestry stamp reads `unknown` there.

## Requirements

### Acceptance criteria

- [ ] The threat model is written down: the adversary is an agent taking the one-command shortcut, not one that campaigns; per road, each control is named a **cost**, a **corroboration** or a **boundary**, and nothing is called a boundary that a same-uid process can defeat
- [ ] A row appended to `<role>-history.jsonl` outside the binary with `"source":"hook"` is **not** accepted as hook evidence by `latestAuthoritativeRow` on any road that has a witness — pinned by a test that writes the forged line and asserts the verdict falls through
- [ ] A genuine hook row still wins outright (positive control), and a genuine `muxcode log` row is still evidence of last resort on non-hook providers (MUX-148 criterion 6) — no new holds on those roads
- [ ] Every hook row carries what a reader needs to corroborate it where a witness exists — `tool_use_id` and `transcript_path` on the roads whose payload supplies them — and `actor` (`BusActorVerified`) on every row, documented as person-vs-agent provenance and nothing more
- [ ] The reader corroborates a hook row against the provider transcript when the row names one: the `tool_use_id` exists, is a shell call whose command matches, and its result carries the exit code the row claims; a hook row that names **no** witness on a road that always supplies one ranks as self-reported at best, never hook
- [ ] `hook guard` denies a Bash statement that writes to `*-history.jsonl` under a bus dir — redirection, `tee`, `sed -i`, an interpreter opening the path for write — logging `guard-denied`; a read (`cat`, `jq`, `tail`) is allowed — both directions tested
- [ ] If the daemon-secret option is chosen: the key lives only in daemon memory, is never written to a file or exported to an environment, and a row whose MAC does not verify is ignored by the reader and logged `history-row-unverified`
- [ ] Every rejection writes a lifecycle row naming file, row timestamp, role and reason (`history-row-rejected`), so a forgery attempt is visible where a forgery today is silent
- [ ] Legacy rows — `hook`-labelled rows written before this change, without witness fields — are handled by the existing `since` filter (a node reads only rows after its start) and documented as such; no migration
- [ ] `docs/hooks.md` and `docs/architecture.md` state what a history row's provenance proves and does not prove, per road
- [ ] Integration test `scripts/test-history-provenance.sh` covers the forged-row, genuine-row, guard-deny, guard-allow-read and legacy-row cases with a coverage floor

### Technical approach — options, deliberately not yet chosen

| Option | Shape | Closes | Leaves open | Cost |
|---|---|---|---|---|
| 1 — witness corroboration | hook rows carry `tool_use_id` + `transcript_path`; the reader (or a verifier the reader calls) checks the transcript for that tool call and its exit code. Extends what `CodexExitCodeFromTranscript` already does on the Codex road | one-line forgery: the forger must now also fabricate a consistent transcript record under Claude Code's/Codex's file | a forger that edits both artifacts; roads with no transcript (OpenCode, local) — there the row is already at best self-reported | a transcript parse per verdict; transcript shape is the provider's, not muxcode's (the MUX-163 lesson: a bet on a constant muxcode does not own) |
| 2 — daemon-held secret | the hook hands the event to the daemon (bus request or spool file); only the daemon appends, with an HMAC under a per-session key generated at startup and held in memory; the reader verifies the MAC | a forger without the key — any agent, on every road, including ones with no transcript | an agent that reads daemon memory or restarts "the daemon" as itself; a daemon relaunch (`upgrade-daemons`, monitor) rotates the key, so rows across a relaunch need the old key or are legacy | the hook road becomes asynchronous through the daemon; key rotation; MUX-144's seal already shows a daemon-startup secret is workable |
| 3 — guard the append | `hook guard` refuses Bash statements whose write target is a bus-dir history file | the cheap shortcut on the hook roads (Claude, Codex-hook) — the closer MUX-144 named | OpenCode/local (no `PreToolUse`; `DenyTools` and the pane audit are the analogues); shell-heuristic bypasses (`$(printf …)` paths) | small; a pattern in `bus/guard.go` and tests in both directions |
| 4 — ancestry stamp alone | `actor = BusActorVerified()` on every row | person-vs-agent attribution (useful for `muxcode log` rows and audit) | **the residual itself** — hook and forger share the ancestor | trivial; ships as part of any option, never as the answer |

The evidence favours **3 + 1 now, 2 as its own follow-up if 1's residual is judged live**: the guard
removes the shortcut where a guard exists, the witness makes the remaining forgery a two-artifact job
where a transcript exists, and both are honest about the roads they do not cover. Option 2 is the
only one that reaches every road, and it is the one edit's report named; it is also the one that
changes how the hook road writes, so it is decided separately, not assumed.

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

- [ ] Reproduce: on a scratch session, append a `"source":"hook","exit_code":"0","outcome":"success"` line to a role's history file from an agent shell and show `latestAuthoritativeRow` returns it — a failing test, kept as the negative control
- [ ] Record whether the installed Claude Code's `PostToolUse` payload carries `tool_use_id` and `transcript_path`, and whether the transcript record for that id carries the command and the exit code (shape verified on 2026-09-14: `is_error` and `toolUseResult`; the exit code's field is to be confirmed)
- [ ] Record the same for the Codex hook road (already read by `CodexExitCodeFromTranscript`) and for OpenCode and the local harness (expected: no transcript, no witness)
- [ ] Record whether the process tree distinguishes a hook invocation from a Bash-tool shell (Claude's Bash tool sources a `shell-snapshots` file; hooks run under `sh -c`) — a cost at most, and a provider-internal shape
- [ ] Record what `hook guard` can see of a write target — redirection, `tee`, `sed -i`, interpreter opens — and which forms it cannot
- [ ] Measure the cost of a transcript lookup per verdict on a long session file
- [ ] Record the findings here before choosing an option

### Phase 2: Choose the design

- [ ] Write the threat model ([Decision 1](#decision-1--threat-model)) and classify each candidate control per road as cost, corroboration or boundary
- [ ] Decide witness, daemon secret, or both ([Decision 2](#decision-2--witness-daemon-secret-or-both)); if the daemon secret is deferred, record why and what would make it live
- [ ] Decide the guard's scope ([Decision 3](#decision-3--what-the-guard-refuses))
- [ ] Decide what `actor` on a row means and where it is read ([Decision 4](#decision-4--what-actor-proves))
- [ ] Decide the legacy-row rule ([Decision 5](#decision-5--legacy-rows)) and record it in MUX-148 as the closer of its Phase 3 pointer

### Phase 3: Writer side

- [ ] `HookHistoryEntry` gains `actor`, `tool_use_id` and `transcript_path`; `ProcessBashHook` fills them from the payload; `cmd/log.go` fills `actor` only
- [ ] `hook guard` refuses a Bash statement that writes to a bus-dir history file, with `guard-denied`; unit tests in both directions (write refused; `cat`/`jq`/`tail` allowed; a write to an unrelated `.jsonl` allowed)
- [ ] If Decision 2 chooses the daemon secret: the hook posts the entry to the daemon, the daemon appends with the MAC, the key is generated at daemon start and never persisted; tests for a bad MAC and for a relaunch
- [ ] Unit test: `TestWriteHookHistoryStampsSource` extended to the new fields; a row written by `WriteHookHistory` with none of them is still valid on roads that have no witness

### Phase 4: Reader side

- [ ] `latestAuthoritativeRow` corroborates a `hook` row that names a witness before accepting it; a `hook` row with no witness on a witness-bearing road ranks as self-reported at best; other roads unchanged
- [ ] `history-row-rejected` / `history-row-unverified` lifecycle rows on every refusal, naming file, row ts, role and reason
- [ ] Negative controls: forged row → not evidence; genuine hook row → wins; genuine self-report on OpenCode → still last-resort evidence; legacy `hook` row before `since` → ignored as today
- [ ] `docs/hooks.md` and `docs/architecture.md` describe what a row's provenance proves per road, and CLAUDE.md's evidence-guard bullet gains the history-file rule

### Phase 5: Integration test

- [ ] Create `scripts/test-history-provenance.sh` — hermetic: a scratch bus dir, a stand-in transcript file in the provider's shape, the installed `muxcode`
- [ ] Forged-row control: append a `hook` success line by shell → `muxcode graph` verdict path (or a direct reader invocation) falls through; `history-row-rejected` logged
- [ ] Genuine-row control: a row written by `muxcode hook bash` with a matching transcript record → accepted as hook
- [ ] Mismatch control: a hook row whose `tool_use_id` exists but whose exit code disagrees with the transcript → rejected with the reason naming the disagreement
- [ ] Guard controls: `hook guard` on a redirect to `test-history.jsonl` → denied; on `jq . test-history.jsonl` → allowed
- [ ] Legacy control: a pre-change `hook` row older than the node's start → ignored, no rejection row (it was never read)
- [ ] If the daemon secret shipped: a row with a bad MAC → `history-row-unverified`, ignored; a genuine daemon-written row → accepted
- [ ] Coverage floor: every section asserts at least once; a skipped section fails the script
- [ ] Run the script and record the pass/fail counts here

## Open decisions

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
change are never a live node's evidence. Whether the console and `muxcode lifecycle` should also
mark them is a display question, not a verdict question.

## Out of scope

- **A daemon under a separate uid, or any kernel-enforced boundary** — the only true boundary on a shared machine; not proposed.
- **MUX-148's remaining Phase 3 items** — the signal tied to the dispatched task, the spawn road, double-holds — unchanged; this spec closes one constraint of that phase.
- **The `bus-response` road** — already never evidence.
- **The lifecycle log's own forgeability** — same class (same uid, `0644`), same doctrine; a separate spec if MUX-144's corroboration is ever judged insufficient.
- **`muxcode log` as a road** — stays the honest self-report; demoting it further is MUX-148's call, not this one's.

## Status

Draft

Filed 2026-09-14 on the user's instruction ("file the actor provenance residual as a backlog item",
given to edit, and "file defect for this" to plan), from the residual plan recorded under MUX-148
Decision 4 when `a8fa0db` shipped and edit restated at 15:25. Every mechanism claim verified by plan
against `7bcd657`. Two findings are plan's, not the report's: the ancestry stamp MUX-148's Phase 3
names would not separate a hook from an agent's shell, since both resolve to the same runtime; and a
transcript witness already exists on the Codex road (`CodexExitCodeFromTranscript`) and Claude's
transcript records tool results by `tool_use_id`, so corroboration is an extension, not an invention.
No live forgery observed. Not started.
