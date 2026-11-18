# MUX-226: `test-graph-node-caps.sh` Runs on the User's Real tmux Server and May Mix In the Installed Binary

**Tracking:** [mkober/muxcode#211](https://github.com/mkober/muxcode/issues/211)

A follow-up to [MUX-162](../completed/MUX-162-pr-review-fix-node-600s-send-cap.md) (PR #206), from
two Copilot review threads read after the merge. The script MUX-162 added sets a scratch `HOME` for
itself but creates its session on the user's real tmux server, and it lets spawned workers resolve
`muxcode` from `PATH` rather than from the binary under test.

## Context

### Source and standard of evidence

Both threads are unresolved on PR #206. Verified by edit against main `11c689f`; plan re-read the
script lines.

| Thread | Location | Defect |
|--------|----------|--------|
| `PRRT_kwDORVBA4863h2Aq` (4575194307) | `scripts/test-graph-node-caps.sh:200` | `tmux new-session -d -s "$BUS_SESSION"` with no private server — no `TMUX_TMPDIR`, no `-L`. The only cleanup is `kill-session` (`:77`). Panes, and whatever the scratch daemon launches in them, inherit the real server's environment, the real `HOME` included, so bus and state paths can escape the scratch `HOME` the script set for itself and survive its cleanup |
| `PRRT_kwDORVBA4863h2A4` (4575194324) | `scripts/test-graph-node-caps.sh:35–36`, `bus/spawn.go` `findMuxcodeBinary` | `findMuxcodeBinary` tries `~/.local/bin`, then `PATH`, then `os.Executable`. Under the scratch `HOME` the first hit is the first `muxcode` on `PATH` — the installed binary — so a run with `MUXCODE_BIN=<tree build>` drives spawned workers with a different build from the one under test |

Not observed causing a wrong result: the recorded 29/29 run used the installed binary throughout.
What actually leaked onto the real server or into the real state directory during that run was not
measured; Phase 1 does.

### The pattern to follow

`scripts/test-diagnose-dead-agent.sh` and the other private-server scripts export
`TMUX_TMPDIR="$WORK/tmux"` and `kill-server` on exit.

## Requirements

### Acceptance criteria

- [x] The script's tmux session lives on a private server under its work directory; no session, window or environment entry is created on the user's server
- [x] Cleanup kills that server, on success, failure and interrupt
- [x] Every `muxcode` a spawned worker or pane resolves is the binary under test: a scratch bin directory holding it is first on `PATH` for the script, its daemon and its panes
- [x] With `MUXCODE_BIN` pointing at a tree build, the script proves one build ran everywhere (negative control: the assertion fails when the scratch bin directory is left off `PATH`)
- [x] The script still passes, its coverage floor raised for the new checks

**Not a criterion — a follow-up for the user** (moved out of the list above by plan at activation,
2026-11-17; see the findings below): answering and resolving the two review threads on PR #206. It
is a GitHub write on a merged PR, the commit agent's to do and only on the user's request. Nothing
in the fix depends on it.

### Technical approach

Export `TMUX_TMPDIR="$WORK/tmux"` before the first tmux call and replace `kill-session` with
`kill-server` in the trap. Create `$WORK/bin/muxcode` as a symlink to `$MUX` and prepend `$WORK/bin`
to `PATH`, exporting it into the scratch session's environment too. Assert both: the user's server
lists no scratch session, and the worker's resolved binary equals `$MUX`.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-graph-node-caps.sh` | The session creation (200), the trap (77), the binary resolution (35–36) |
| `scripts/test-diagnose-dead-agent.sh` | The private-server pattern |
| `tools/muxcode/bus/spawn.go` | `findMuxcodeBinary` — read only; its order is not changed here |

## Implementation

### Phase 1: Isolate the script

- [x] Record what the current script leaves on the real tmux server and under the real state directory after a run
- [x] Private tmux server under `$WORK`, killed in the trap
- [x] Scratch bin directory first on `PATH`, in the script's environment and the session's

### Results for Phase 1

Recorded 2026-11-17 from the worker's findings (`/tmp/mux226-phase1.md`), read whole by plan; both
measurement runs are in the run agent's replies. Boxes are ticked at `verify-spec`, not here.

**What the unmodified script left behind** (task `1794955755-spawn-56d56314-687c9ae8`; a wrapper
snapshotted the real tmux server and the real state and log directories before and after, and
probed the server during the run; the script itself passed 29 of 29):

| When | Observed |
|------|----------|
| During the run | The session `nodecaps-test-84709` **on the user's real tmux server**, with windows `edit` and a spawn worker. Its panes inherited the real server's `HOME`. The `muxcode` a pane resolves from that server's `PATH` is the installed binary in `~/.local/bin` |
| After the run | No session, global environment key, global hook, state-directory entry or `/tmp` entry left. **One real leak: `~/.config/muxcode/logs/nodecaps-test-84709.log`** in the real home — a pane-side process with the real `HOME` ignores the script's own log directory setting |

Plan looked in the real log directory: that file is there, beside `nodecaps-test-9840.log` from an
earlier run. So the "what leaked was not measured" in this spec's Context is now answered: a
session on the real server for the length of the run, and a lifecycle log per run left for good.

**The isolation applied** to `scripts/test-graph-node-caps.sh`:

- `TMUX` unset and `TMUX_TMPDIR="$WORK/tmux"` exported before the first tmux call; cleanup runs
  `tmux kill-server`; `INT` and `TERM` traps exit 130 and 143 so the exit cleanup runs on interrupt.
- `$WORK/bin/muxcode` is a symlink to the binary under test; `$WORK/bin` is first on `PATH` for the
  script — so the private server and the daemon carry it — and is set into the session's
  environment. The scratch home has no `~/.local/bin`, so `findMuxcodeBinary` falls to `PATH`.

**Re-measured after the edit** (task `1794955830-spawn-56d56314-90945b73`): 29 passed, 0 failed,
floor 29, exit 0. No session seen on the real server; nothing left behind — sessions, environment,
hooks, state, logs and `/tmp` all unchanged.

**The audit of plan's eighteen.** By reading each file, the worker finds:

- **Two are live-session scripts**, meant to use the real server: `test-resize-hook` and
  `test-stall-redrive-busy`. Not defects.
- **Sixteen hermetic scripts** create their session on the real server and clean up with
  `kill-session` only: `test-close-spec-guard`, `test-codex-idle-delivery`,
  `test-codex-trust-prompt`, `test-control-pane`, `test-durable-state-location`,
  `test-force-respond`, `test-gate-authority`, `test-graph-orchestrator`, `test-multi-phase-graph`,
  `test-node-outcome-attribution`, `test-plan-verify-verdict`, `test-prompt-mode`,
  `test-spawn-no-cross-tree-delegation`, `test-status-line-task-close`,
  `test-unrequested-test-cycles` and `test-verify-spec-refire`.

None of the sixteen was measured the way this one was. Plan's look at the real log directory adds
some evidence for two of them: it holds three `multiphase-test-*` logs and two `unrequested-b-*`
logs. Filed separately as
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md); not fixed
here.

#### Review fixes (2026-11-17, 17:57–17:59)

The review returned 0 must-fix, **2 should-fix**.

**1. A relative `MUXCODE_BIN` broke the scratch symlink.** `$WORK/bin/muxcode` linked to `$MUX` as
given, so a relative path pointed nowhere from the work directory. `MUX` is now made absolute
before the symlink is created. Run with `MUXCODE_BIN=./muxcode` through the run agent (task
`1794956287-spawn-56d56314-e68b878a`, reply at 17:58:50): 29 passed, 0 failed, floor 29, exit 0.

**2. The audit above is wrong about one script — corrected here.** It excluded
`test-stall-redrive-busy` as a live-session script. It is not. Plan re-read it: its header says
"Hermetic: a scratch bus session and a scratch tmux session", it creates `stall-busy-test-$$` with
a plain `tmux new-session` (`:118`) and cleans up with `kill-session` (`:64`), and
`scripts/test-all.sh` does not list it among its live-only scripts. So it shares the defect:
**seventeen** hermetic scripts, not sixteen, and one live-session script (`test-resize-hook`), not
two. MUX-233 is corrected to match. The error was the worker's reading, recorded by plan without
checking; `CLAUDE.md`'s entry for the script also calls it "live-session", which may be where it
came from.

#### Verification (2026-11-17, 18:04)

Ticked on the tree after both review fixes; the report in the dispatch predates them and still
says sixteen. Plan read the script: `unset TMUX` and `TMUX_TMPDIR="$WORK/tmux"` (`:84–85`), the
symlink and `PATH` (`:87–88`), `tmux kill-server` in the cleanup (`:95`), the `EXIT`, `INT` and
`TERM` traps (`:99–101`), and `PATH` set into the session (`:229`). Last edited 17:57:54. After it:
the relative-path run at 17:58:50 (29 of 29), `./build.sh` exit 0 at 18:00:57, `./test.sh` exit 0
at 18:02:44, review at 18:03:22 with 0 must-fix, 0 should-fix, 0 nits.

The three steps are ticked, and the first criterion on the re-measure: no session on the real
server during the run, nothing left after.

**Left open, and why.** The second criterion — cleanup kills the server "on success, failure and
interrupt" — is shown for a successful run only. The traps for interrupt are in the script and read
correctly, but nothing has interrupted a run and looked. Phase 2 lists no such check, so unless
one is added the criterion stays open at close-out. Criteria 3–5 are Phase 2's.

### Phase 2: Integration test

- [x] Add checks to the script: the user's tmux server shows no scratch session during or after the run; the spawned worker's `muxcode` is `$MUX`
- [x] Negative control for the binary check, and a raised floor
- [x] Interrupted run: start the script, send it `INT` partway through, and again with `TERM`; after each, its private tmux server is gone (no server on the `$WORK/tmux` socket, no `tmux` process for it), its scratch daemon is gone, its work directory is removed, and the real tmux server and real `~/.config/muxcode/logs/` are unchanged. Also show the failure path — a run forced to fail a check exits non-zero and leaves the same nothing. Record the results here. (Added 2026-11-17 at edit's request, for criterion 2: only the success path had been shown)
- [x] Run the script with the installed binary and with a tree build (`MUXCODE_BIN`); record both pass/fail counts in this spec
- [x] Update the script's entry in `CLAUDE.md`

### Results for Phase 2

Recorded 2026-11-17 from the worker's report; both runs are in the run agent's replies. Boxes are
ticked at `verify-spec`, not here.

**The new checks** — in the script's section 2 while the run is live, and a new section 4 after it.
Floor raised from 29 to 36.

| Check | What it shows |
|-------|---------------|
| The private server holds the scratch session; the user's server does not | During the run and after it |
| The spawned worker's launcher, read from its console pane, is `$WORK/bin/muxcode` and resolves to `$MUX` | One build ran everywhere |
| Negative control | A launcher resolved with the scratch bin off `PATH` — a decoy first — is rejected by the same assertion |
| No lifecycle log in the real home | The leak Phase 1 measured is gone |

**The runs, through the run agent.**

| Binary | Result | Run task |
|--------|--------|----------|
| Installed | Exit 0 — 36 passed, 0 failed, floor 36 (reply at 18:05:52) | `1794956714-spawn-56d56314-710999b1` |
| Tree build, by a relative `MUXCODE_BIN` (`9a26c6b-dirty`, untagged, so the version precondition notes it and continues) | Exit 0 — 36 passed, 0 failed; the launcher resolved to the tree build (reply at 18:06:34) | `1794956755-spawn-56d56314-826829a9` |

The worker updated the script's entry in `CLAUDE.md`.

**The interrupted-run step** was added to this phase at 18:04, as the results above were being
produced, and was run after them.

#### The interrupt and failure paths (2026-11-17, 18:07–18:11)

A scratch harness ran `test-graph-node-caps.sh` three ways. Both attempts are in the run agent's
replies; plan read the passing run's log (`/tmp/mux226-cleanup2.log`).

| Path | How | Script's exit |
|------|-----|---------------|
| Interrupt | `INT` sent once the private server, the daemon and the fix worker were up | 130 |
| Terminate | `TERM` at the same point | 143 |
| Failure | A copy of the script run beside a doctored cap table in the docs, so one check fails — 35 passed, 1 failed, floor 36 | 1 |

After each of the three: no server answers on the private socket and the socket file is absent; no
process names the scratch session; the work directory is removed; the real tmux server's sessions
and the real `~/.config/muxcode/logs/` are unchanged. **21 passed, 0 failed** (task
`1794956968-spawn-56d56314-2d34d525`, reply at 18:10:58).

**The first attempt failed, 18 to 3, and the fault was the harness's** (task
`1794956866-spawn-56d56314-aaf8dae7`, reply at 18:09:16): its "is a server still answering" probe
set `TMUX_TMPDIR` to the already-removed work directory, and tmux then falls back to the real
socket — so the probe was answered by the user's own server. The probe now names the socket path
with `tmux -S` and checks the file is gone. Worth knowing beyond this spec: a `TMUX_TMPDIR` that no
longer exists does not isolate anything.

**What this is and is not.** The harness is a scratch script, not part of the repository: the three
paths are shown once, here, and are not a standing check in `test-graph-node-caps.sh`. The step
asked for the results to be recorded, which this does; it did not ask for a regression test.

#### Review fix (2026-11-17, 18:13–18:15) — the probe looked at the wrong server

The review of Phase 2 returned 0 must-fix, **1 should-fix**: the script's "user's server holds no
scratch session" check probed tmux's default socket, not the server the script was started from. A
user whose session runs on a named server would have had the check look somewhere else and pass
for nothing.

| Change | What it is |
|--------|------------|
| `real_tmux` | Now probes the socket the caller's `TMUX` names — saved before the script unsets it — and the default socket only when there is none, by `tmux -S` |
| New sentinel check | The caller's own session must be visible through the probe. So "no scratch session there" can no longer pass on a probe that reaches no server at all |
| Floor | 36 → 37; the worker updated `CLAUDE.md` again |

**Runs, through the run agent.**

| Run | Result | Run task |
|-----|--------|----------|
| Named server: a scratch caller server on its own socket, `TMUX` and `TMUX_PANE` pointed at it | Exit 0 — 37 passed, 0 failed; the sentinel probed that socket (reply at 18:14:31) | `1794957229-spawn-56d56314-eb96d3bb` |
| Plain | Exit 0 — 37 passed, 0 failed; the probe reached the default socket, holding the run agent's own session (reply at 18:15:11) | `1794957274-spawn-56d56314-d1e15b34` |

| Tree build, by a relative `MUXCODE_BIN` — repeated at the new floor | Exit 0 — 37 passed, 0 failed; the launcher resolved to the tree build and the sentinel reached the real default server (reply at 18:16:25) | `1794957345-spawn-56d56314-e30725b5` |

The counts recorded above this fix (36, floor 36) are from before it.

#### Verification (2026-11-17, 18:19)

Ticked on the tree after the review fix; the report in the dispatch gives the earlier 36 counts.
The script was last edited at 18:13:30. After it: the three runs at floor 37 (18:14:31, 18:15:11,
18:16:25), `./build.sh` exit 0 at 18:17:04, `./test.sh` exit 0 at 18:17:23, review at 18:18:05 with
0 must-fix, 0 should-fix, 0 nits. `CLAUDE.md`'s entry for the script names the private server and
floor 37 — plan read it.

All five steps are ticked, and criteria 2–5:

- **2**, cleanup on success, failure and interrupt — on the scratch harness's three paths. One
  thing to weigh: that harness ran at 18:10, before the 18:13 edit. The edit changed which socket
  the *probe* reads, not the cleanup or the traps, so the result stands; it was not re-run.
- **3** and **4**, one build everywhere — the launcher check and its negative control, on the
  installed binary and the tree build.
- **5**, the script passes with a raised floor — 37, from 29.

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-node-caps-isolation.md` (edit), on the user's standing instruction to file defects found along the way.
- Other scripts written this week may share the first defect; an audit of `scripts/test-*.sh` for `tmux new-session` without a private server is worth a line in Phase 1's results, and a separate spec if it finds more.

### Findings at activation (2026-11-17)

Activated against the tree at `9a26c6b`. Plan read the script and `findMuxcodeBinary`; nothing was
run.

| Spec says | Tree today |
|-----------|------------|
| `tmux new-session` with no private server at `:200`, `kill-session` at `:77` | Still so, at `:201` and `:78`; the script holds no `TMUX_TMPDIR` and no `kill-server` |
| Binary resolution at `:35–36` | At `:36–37`, unchanged in substance |
| `findMuxcodeBinary`: `~/.local/bin`, then `PATH`, then `os.Executable` | Still that order (`bus/spawn.go`, around `:1381`) |
| The script was last changed by MUX-162 | Changed once since, by `f87cb3d` (MUX-145); neither defect was touched |

**One criterion removed by plan — the only change to what "done" means here.** As filed, the sixth
criterion was "Both review threads on PR #206 are answered and resolved — the commit agent's work,
on the user's request". Plan wrote it, and it does not belong in the list: the loop cannot meet it
(no agent may write to GitHub without the user asking), so close-out would be refused on an item no
phase covers, and a worker trying to clear it would be doing exactly what the rule forbids. It is
now a follow-up note under the criteria. 0/12, where the spec was filed as 0/13. If the user wants
it kept as a gate, it goes back in and this spec waits for them.

**The Notes' audit, a first pass.** A crude search — scripts that call `tmux new-session` and
mention neither `TMUX_TMPDIR`, `tmux -L` nor `-S` — names eighteen besides this one:
`test-close-spec-guard`, `test-codex-idle-delivery`, `test-codex-trust-prompt`, `test-control-pane`,
`test-durable-state-location`, `test-force-respond`, `test-gate-authority`,
`test-graph-orchestrator`, `test-multi-phase-graph`, `test-node-outcome-attribution`,
`test-plan-verify-verdict`, `test-prompt-mode`, `test-resize-hook`,
`test-spawn-no-cross-tree-delegation`, `test-stall-redrive-busy`, `test-status-line-task-close`,
`test-unrequested-test-cycles` and `test-verify-spec-refire`. Unverified: some are live-session
scripts that are meant to use the real server, and the search can miss an isolation done another
way. This spec fixes one script; the rest are for the worker's Phase 1 note to confirm, and a
separate spec if they hold — not to be fixed here.

## Status

**Complete** — closed 2026-11-17, uncommitted at close-out (the run's ship step commits it).
`scripts/test-graph-node-caps.sh` now runs its session on a private tmux server under its work
directory, killed on exit, failure and interrupt, and puts the binary under test first on `PATH`
so spawned workers run it too. Measured before the fix, the script put a session on the user's
real tmux server and left a lifecycle log in the real home on every run; after it, nothing. It
passes 37 of 37, floor raised from 29, on the installed binary and on a tree build. Not done here,
by design: resolving the two review threads on PR #206, a follow-up for the user (see the
criteria); and the seventeen other scripts of the same shape, filed as
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md). Activated
2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 34; every better-ranked row is parked). 13/13 — Phases 1–2 and all five criteria ticked 2026-11-17, after three review fixes; awaiting close-out. Filed 2026-11-14.
