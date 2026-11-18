# MUX-235: `tmux kill-server` Is Aliased to a Confirm Prompt, so Test Scripts Leave Their Private tmux Servers Running

**Tracking:** [mkober/muxcode#243](https://github.com/mkober/muxcode/issues/243)

Twenty-four `scripts/test-*.sh` end their private tmux server with `tmux kill-server`.
`config/tmux.conf` aliases that command to a `confirm-before` prompt, which fails with "no current
client" on a server nothing is attached to. In a script whose tmux loads that config, the cleanup's
`kill-server` does nothing and the server outlives the run. Servers of two such scripts were seen
alive hours and days after their runs.

## Context

### Source and standard of evidence

Found 2026-11-17 during Phase 3 of
[MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), whose
interrupted-run check left a private server alive after `TERM`. Filed by plan on the user's
standing instruction to file defects found along the way. Written 2026-11-18: MUX-233 recorded this
spec as filed on the 17th, but the file was not written before the session ended, and the review of
MUX-233's Phase 3 caught the missing file.

| Claim | Standing |
|-------|----------|
| `config/tmux.conf:117` sets `command-alias[0] kill-server='confirm-before -p "Kill the tmux server and ALL sessions? (y/n)" "run-shell \"kill #{pid}\""'` | Read by plan, 2026-11-18 |
| Twenty-four `scripts/test-*.sh` call `tmux kill-server` in their cleanup | A search by plan on 2026-11-18 for the text `kill-server` under `scripts/`, each hit read as a line. A search, not a reading of each script — Phase 1 establishes the list |
| The aliased command fails "no current client" on an unattached server | From the MUX-233 worker's findings (`/tmp/mux233-phase3.md`, read whole by plan on the 17th; the file did not survive the machine's restart on the 18th). Plan has not run it |
| Servers survive | **Seen by plan** in the process list at 21:35 on 2026-11-17: five `pane-id-test-*` servers (four from 2026-11-13, one from that evening) and three `version-test-*` (two from 2026-11-13, one from that evening). The machine restarted on 2026-11-18, so none is running now |
| Which of the twenty-four load the config | **Not established.** The alias applies only where the script's tmux reads `config/tmux.conf` — in practice, a script that runs with the real `HOME`. `test-pane-targeting` and `test-version` set no `HOME` (plan checked with `grep`), and theirs are the servers seen. A script with a scratch `HOME` may be unaffected |

### The scripts

Bare `tmux kill-server` in the cleanup: `test-agent-resume`, `test-auto-startup-gating`,
`test-batch-reply-correlation`, `test-diagnose-dead-agent`, `test-edit-auto-resume`,
`test-graph-foreign-dispatch`, `test-graph-node-caps` (twice), `test-graph-verify-once`,
`test-orphan-reaper`, `test-pane-targeting`, `test-redrive-reply-route`,
`test-reload-defers-dispatch`, `test-reload-hosted-role`, `test-reload-keeps-dispatch`,
`test-reply-to-unknown`, `test-response-echo`, `test-restart-definition`, `test-root-go-vet`,
`test-self-upgrade`, `test-upgrade-daemons-no-ps`, `test-version`, `test-windowless-notify`,
`test-windowless-routing`.

Through a variable or a socket path: `test-escape-absorber` (`$T kill-server`),
`test-spec-dangling-debounce` (`tmux -S "$PRIVATE_SOCK" kill-server`). Also
`scripts/probe-escape-matrix.sh`, which is not a test script.

Already aware of the alias: `test-fkey-labels`, whose cleanup comment says "Kill sessions, not
servers: the sourced tmux.conf aliases kill-server". Whether its server then exits by itself is for
Phase 1.

Not in scope: the twenty-two scripts MUX-233 moved onto `scripts/lib/private-tmux.sh`, whose
`private_tmux_kill` already signals the server's pid.

### Consequence

- One tmux server process is left per run of each affected script, until the machine restarts.
  Nothing reaps them: the orphan reaper
  ([MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md)) acts on dead
  sessions, and these sessions are alive.
- A script's cleanup that goes on to remove its work directory leaves the server running on a
  socket whose directory is gone.
- Under `set -e`, the failing `kill-server` can abort an exit trap before the rest of the cleanup
  runs — MUX-233's Phase 2 met this in four scripts, which then left bus directories in the real
  state directory.

### Not this spec

- The alias itself. It guards the user against killing every session by accident and stays.
- Ending servers already left behind. That is the user's to do or to ask for; a restart has
  cleared those seen on 2026-11-17.

## Requirements

### Acceptance criteria

- [x] The list of test scripts that end a tmux server with `kill-server` is established by reading, and for each it is recorded — measured — whether its server survives a run
- [x] Every script found to leave a server ends it through `private_tmux_kill` (`scripts/lib/private-tmux.sh`), or an equivalent that signals the server's pid found through its socket
- [x] After a run of each changed script — success, failure and interrupt on a sample — no tmux server process of that script remains
- [x] Each changed script still passes with its coverage floor met; a script failing for another reason keeps the same counts, check for check
- [x] Negative control: the check that detects a surviving server is shown to fail against an unmodified script
- [x] The check counts servers started as `tmux -L <name>` and `tmux -S <path>` as well as the default socket — MUX-233's wrapper missed the first form

### Technical approach

`private_tmux_kill [SOCKET]` already does the right thing: it reads the server's pid through the
socket (`list-sessions -F '#{pid}'`, which needs no client), sends `TERM`, waits, then `KILL`, and
never fails. Scripts that already set `TMUX_TMPDIR` themselves can source the helper and call it
with their socket path without adopting `private_tmux_init`; adopting the whole helper is the
tidier end state where a script's existing trap order allows it. The helper's two rules — isolate
before any trap, kill only a socket that exists — bind this work as they bound MUX-233.

### Key files

| File | Purpose |
|------|---------|
| The scripts listed above | Cleanup |
| `scripts/lib/private-tmux.sh` | `private_tmux_kill` |
| `config/tmux.conf` | The alias, line 117 — read only |
| `CLAUDE.md` | The integration-test entry already states the rule; update a script's entry if its description changes |

## Implementation

### Phase 1: Measure

- [x] Establish the list by reading every `scripts/test-*.sh` that starts a tmux server; record which load `config/tmux.conf` and how
- [x] Run each under a wrapper that snapshots tmux server processes — default, `-L` and `-S` forms — before and after; record which leave a server
- [x] Record what `test-fkey-labels` leaves, since it avoids `kill-server` by design

### Results for Phase 1 (2026-11-18)

Recorded from the worker's handoff, `/tmp/mux235-phase1.md`, and the wrapper's log,
`/tmp/mux235-phase1-measure.log` (run agent task `1795035646-spawn-caa28c64-155b1b20`, finished
16:24). Both are under `/tmp` and will not survive a restart; what matters is copied here. Plan
read the handoff whole and the log's result lines; plan ran nothing.

**Wrapper.** `scripts/measure-tmux-leak.sh`, new and untracked. Its census is every process whose
`argv[0]` is `tmux` — default, `-L`, `-S` and `TMUX_TMPDIR` servers — each tied to its socket
through `lsof` while the run is going, the alias probed live (`show-options -s command-alias`),
and survivors read by pid 3 s after the run ends. The log marks each leaked server
`ended-by-pid`: the wrapper ends what it finds. The run agent's sandbox refuses `setpgid`, so
each run goes through a `python3` shim that restores `SIGINT` rather than job control.

**How the config is loaded.** Only `~/.tmux.conf` sources `~/.config/muxcode/tmux.conf` (its line
294); `XDG_CONFIG_HOME` is unset and there is no `/etc` or Homebrew `tmux.conf`. A server carries
the alias only if it starts while `HOME` is the real one, or sources the config itself.

**Result: 26 scripts read and measured, 3 leak, 23 clean; all 26 exit 0 with floors met.** The
list is 26, not the 24 this spec was filed with.

| Script | Loads `tmux.conf`? | Measured |
|--------|--------------------|----------|
| `test-version` | Yes — real `HOME`, sets none | **Leak.** Alias loaded; work directory removed, so the server is left on a deleted socket |
| `test-pane-targeting` | Yes — real `HOME` | **Leak.** Alias loaded; work directory removed |
| `test-escape-absorber` | Yes — real `HOME`; `tmux -L escabs-$$` on the default `TMUX_TMPDIR` | **Leak.** Alias loaded; `\|\| true` swallows the failed `kill-server` |
| `test-fkey-labels` | Yes — `tmux source-file config/tmux.conf` into its `-f /dev/null` server | Clean. Alias loaded, but it kills sessions, and the server exits with its last session, as its comment says. Its driver `-L` server has no alias and exits too |
| The other 22 | No — scratch `HOME` before any tmux | Clean; every server seen had no alias and exited |

The other 22: `agent-resume`, `auto-startup-gating`, `batch-reply-correlation`,
`diagnose-dead-agent`, `edit-auto-resume`, `graph-foreign-dispatch`, `graph-node-caps`,
`graph-verify-once`, `orphan-reaper`, `redrive-reply-route`, `reload-defers-dispatch`,
`reload-hosted-role`, `reload-keeps-dispatch`, `reply-to-unknown`, `response-echo`,
`restart-definition`, `root-go-vet`, `self-upgrade`, `spec-dangling-debounce`,
`upgrade-daemons-no-ps`, `windowless-notify`, `windowless-routing`.

**Counts on this run** (passed / failed, floor in brackets) — the baseline Phase 3 compares
against: `agent-resume` 104/0, `auto-startup-gating` 55/0, `batch-reply-correlation` 41/0 (41),
`diagnose-dead-agent` 38/0 (38), `edit-auto-resume` 84/0, `escape-absorber` 14/0 and 1 skipped,
`fkey-labels` 19/0, `graph-foreign-dispatch` 24/0 (24), `graph-node-caps` 37/0 (37),
`graph-verify-once` 21/0 (21), `orphan-reaper` 56/0 (56), `pane-targeting` 22/0,
`redrive-reply-route` 21/0 (21), `reload-defers-dispatch` 46/0 (46), `reload-hosted-role` 13/0
(13), `reload-keeps-dispatch` 60/0 (60), `reply-to-unknown` 22/0 (22), `response-echo` 29/0 (29),
`restart-definition` 30/0, `root-go-vet` 34/0 (34), `self-upgrade` OK, `spec-dangling-debounce`
14/0 (14), `upgrade-daemons-no-ps` 26/0 (26), `version` OK, `windowless-notify` 10/0 (10),
`windowless-routing` 25/0 (25).

**Phase 2 scope, by the second criterion:** `test-version`, `test-pane-targeting`,
`test-escape-absorber`. The 22 clean scripts keep a bare `tmux kill-server` that works only
because their `HOME` is scratch; changing them is outside the criterion as written.

**Also seen.**

- Two servers leaked earlier in the day were alive at 15:58 (`pane-id-test-11218`, pid 11235;
  `version-test-83213`, pid 83922) — the same two scripts. Left running: ending them is the
  user's call, as this spec says.
- `scripts/probe-escape-matrix.sh`, not a test script, also ends its server with `kill-server`.
  Not measured.
- `probe]` and `probe2]`, empty, appeared at the repository root at 16:01:29, during this run —
  the files seen after the full runner on MUX-234. One of these 26 scripts therefore writes
  them; the log's per-script durations place 16:01:29 early in the run, but which script was
  running then was not worked out. Not this spec's defect; not to be staged.

**First review of this phase: 2 must-fix, both on the wrapper.** The run's
review node returned `EXIT=1`: `scripts/measure-tmux-leak.sh` can kill unrelated tmux servers
started during a run, and it aborts when `TMUX` is unset. The `fix` node then failed —
its worker never consumed the task after 3 redrives — and run
`1795035299-55-defect-backlog-loop-416695aa` ended `failed` with the fixes unmade. The first
finding bears on the measurement as well as on safety: the wrapper ends what it classes as
leaked, so a server it misattributes is both miscounted and killed. Plan has the review's verdict
line only, not its full text.

**Fixed and re-reviewed (17:43–17:46).** Edit retried the run from `fix`. The wrapper now marks
each run with a `MEASURE_TMUX_RUN` token and treats a server as the run's own only if its process
carries that token; any other server that appears during the run is reported `FOREIGN` and never
signalled. It runs with `TMUX` set or unset, and has a `--self-test`. Evidence:
`/tmp/mux235-fix-verify.log` (run agent, 17:45, exit 0), read whole by plan:

| Check | Result |
|-------|--------|
| `--self-test`, `TMUX` set | 4 / 0 — a fixture's leaked `-L` server flagged and ended by pid; a foreign `-L` server started during the run survived and was reported foreign |
| `--self-test`, `TMUX` unset | 4 / 0, the same four checks |
| `test-version`, `TMUX` unset | exit 0, leaked 1, foreign 0 — alias loaded, work directory gone |
| `test-pane-targeting` | exit 0, 22 / 0, leaked 1, foreign 0 |
| `test-escape-absorber` | exit 0, 14 / 0 and 1 skipped, leaked 1, foreign 0 |
| `test-fkey-labels` | exit 0, 19 / 0, leaked 0 — both its servers exited by themselves |

Review after the fix: 0 must-fix, 0 should-fix, 0 nits, `EXIT=0`. Build and test passed before it.

**What was ticked, and on what.** Phase 1's three steps and the first criterion, verified
2026-11-18. The three leaks and `test-fkey-labels` rest on the corrected wrapper. The other 22
were measured only on the first revision, at 16:00, and not re-run. That is accepted here for a
stated reason: the fault review found makes the first revision count a server that is not the
run's — it can report a leak that is not one, never hide one — and that log holds exactly three
`LEAK` lines, each naming its own script's session. Phase 3 re-runs the changed scripts on the
corrected wrapper in any case.

**Not ticked.** The negative-control criterion and the `-L` / `-S` criterion describe the check
Phase 3 uses on the fixed scripts. The self-test shows a `-L` leak caught; no leaked `-S <path>`
server has been shown caught.

### Phase 2: Fix the cleanups

- [x] Each script found leaving a server: pid-based kill through the helper, by socket path, only if the socket exists
- [x] No cleanup can abort under `set -e` before it removes its work and bus directories

### Results for Phase 2 (2026-11-18)

Three scripts changed, last edited 17:48:29–17:48:36. Plan read the diff.

| Script | Change |
|--------|--------|
| `test-version` | Its own `TMUX_TMPDIR` setup replaced by `private_tmux_init "$WORK/tmux"`, before the trap; cleanup's `tmux kill-server` replaced by `private_tmux_kill` |
| `test-pane-targeting` | The same two replacements |
| `test-escape-absorber` | Keeps its `tmux -L escabs-$$` socket — the worker's reason: the opt-in live probe inherits it. Sources the helper, records the socket path (`${TMUX_TMPDIR:-/tmp}/tmux-<uid>/escabs-$$`) before the trap, and its cleanup calls `private_tmux_kill` on that path in place of `$T kill-server … \|\| true`. Adds `INT` and `TERM` traps that exit 130 and 143, so the exit trap runs on a signal |

**The `set -e` step.** `private_tmux_kill` returns 0 on every path (`scripts/lib/private-tmux.sh`,
read by plan: no socket, no pid, already gone, and its last line) and acts only on a path that is
a socket. Of the three, only `test-escape-absorber` runs under `set -e`, and its cleanup is now
that call followed by the `rm`. `test-version` and `test-pane-targeting` run under
`set -uo pipefail`, so their cleanups cannot abort either.

**Measured after the change** — `/tmp/mux235-phase2-measure.log`, run agent task
`1795042125-spawn-caa28c64-f9fd9248`, written 17:49:57, after the last script edit:

| Script | Exit | Counts | Phase 1 counts | Servers |
|--------|------|--------|----------------|---------|
| `test-version` | 0 | OK | OK | 2 seen, 0 leaked, 0 foreign alive |
| `test-pane-targeting` | 0 | 22 / 0 | 22 / 0 | 1 seen, 0 leaked |
| `test-escape-absorber` | 0 | 14 / 0, 1 skipped | 14 / 0, 1 skipped | 1 seen, 0 leaked |

Summary line: `scripts=3 leaky=0 clean=3`. Each owned server still had the alias loaded and exited
all the same. Build and test passed; review: 0 must-fix, 0 should-fix, 0 nits.

Two things about the wrapper, neither changing a count:

- `test-version`'s second "server" is a line reading `exited foreign pid=14287
  socket=alias-unknown` with an empty command — a process that was gone before the wrapper could
  read it. Not looked into further.
- The wrapper was edited again at 17:50:07, ten seconds after this log was written — the worker
  reports a cosmetic fix, an empty start time padded. No run has been made on that revision yet;
  Phase 3's runs will be the first.

Ticked: Phase 2's two steps, and two criteria — every leaking script ends its server through
`private_tmux_kill`, and each changed script still passes with the counts it had. The spec is
8/16. **Open:** Phase 3's five steps; and the criteria for failure and interrupted runs, the
negative control, and the `-L` / `-S` census.

### Phase 3: Integration test

- [x] Re-run each changed script under the wrapper, invoked as `bash scripts/<name>.sh` from the repository root: no surviving server, counts and floors recorded here
- [x] Interrupted run on two changed scripts, `INT` and `TERM`: no surviving server, work directory gone
- [x] Negative control: the wrapper flags `HEAD`'s copy of one changed script, run from a scratch tree
- [x] `bash scripts/test-all.sh` — the full runner — and record its summary; no changed script newly fails
- [x] Update `CLAUDE.md` where a script's entry describes its cleanup

### Results for Phase 3 (2026-11-18)

Recorded at the worker's request before `verify-spec`, then checked by plan against the finished
logs at `verify-spec` (18:57), where the boxes above were ticked.
The wrapper, `scripts/measure-tmux-leak.sh`, was last edited at 18:51:51; each row says which side
of that edit its log falls on. The worker's own summary is `/tmp/mux235-phase3-results.md`.

| Check | Result | Log | Against the final wrapper |
|-------|--------|-----|---------------------------|
| Clean re-runs of the three changed scripts | `scripts=3 leaky=0 clean=3` — `test-version` OK, exit 0; `test-pane-targeting` 22 / 0; `test-escape-absorber` 14 / 0, 1 skipped; counts as in Phases 1 and 2. Each owned server had the alias loaded and exited | `/tmp/mux235-phase3.log` | Yes — re-run, log written 18:54 |
| `INT` and `TERM` on `test-pane-targeting`, 15 s in | Exit 130 and 143, `leaked=0`, work directory gone, both | same | Yes |
| `INT` and `TERM` on `test-escape-absorber`, 5 s in | Exit 130 and 143, `leaked=0`, work directory gone, both | same | Yes — log finished 18:55:29 |
| Negative control: `HEAD`'s `test-escape-absorber` from a scratch tree | Flagged — `leaked=1`, its `-L escabs` server with the alias loaded, ended by pid; summary `leaky=1`, wrapper exit 1. The script itself still reports 14 / 0, 1 skipped, exit 0 — a leak its own result never shows | same | Yes |
| `-L` / `-S` census: `--self-test`, with `TMUX` set and unset | 6 passed, 0 failed, each. A fixture leaks one `-L` and one absolute-path `-S` server: `leaked=2`, the `-S` leak reported with its socket, both ended by pid; a foreign server started during the run survived and was reported `FOREIGN` | `/tmp/mux235-phase3-fix.log`, 18:52:45 | Yes |
| Ordinary failure after the server starts — scratch copies of the current scripts with one injected line | `test-escape-absorber`, `false` after line 85 (under `set -e`): exit 1. `test-pane-targeting`, `exit 3` after line 97: exit 3. `test-version`, `exit 3` after line 163: exit 3. Each `servers_seen=1 leaked=0`, work directory gone | same | Yes |
| `bash scripts/test-all.sh` | 80 passed, 2 failed, 9 skipped (live-session) — the MUX-234 baseline. The three changed scripts pass. Failed: `test-graph-tui` (2 checks, [MUX-210](../backlog/MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md)) and `test-prompt-mode` (5 checks) | `/tmp/mux235-test-all.log`, 18:47:26 | Does not use the wrapper |
| `CLAUDE.md` | The `scripts/test-*.sh` row now describes a script that keeps its own socket passing its path to `private_tmux_kill`, and `measure-tmux-leak.sh` | working tree, one line changed | — |

Three things the table does not show:

- **A detector gap found and fixed during this phase.** The first `--self-test` with the `-S`
  fixture counted one leak of two: the fixture's relative `-S` path left a socket `lsof` did not
  resolve, and the census skipped that server. The wrapper now records an owned server whether or
  not its socket resolves. This is why the earlier clean, interrupt and negative-control runs are
  being repeated.
- **`test-prompt-mode` fails five checks here against four recorded for MUX-234.** The extra one is
  `injected payload mangled: got '' want '- dash inject probe mux163'`. This spec touches neither
  that script nor anything it runs; the cause has not been looked at.
- **The full runner recreated `probe]` and `probe2]`** at the repository root (empty files, writer
  still not traced) and left `docs/media/sample-aws.svg` and
  `test/fixtures/vite-react-app/vite-test.log` modified. None belongs in this spec's commit.

Two servers leaked before the fix are still running — pid 11235 (`pane-id-test-11218`) and pid
83922 (`version-test-83213`) — and are the user's to end.

## Related

- [MUX-233](./MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — where
  this was found, and the helper that fixes it
- [MUX-226](./MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) —
  `test-graph-node-caps`, whose fix uses the bare `kill-server`
- [MUX-184](./MUX-184-orphaned-session-processes-never-reaped.md) — the orphan reaper,
  which does not cover a live scratch server
- [MUX-147](../backlog/MUX-147-process-leak-and-memory-footprint.md) — parked; its remaining scope is the
  scripts' own cleanup

### Findings at activation (2026-11-18)

Activated against the tree at `ffd62a7`, the merge of MUX-234 (PR #245). Nothing was run.

- **The script list above is a day old and was a search, not a reading.** A search today for
  `kill-server` and `private_tmux_kill` under `scripts/` finds 51 files, which includes the
  scripts already on the helper. Phase 1's reading is what establishes the list.
- **A census script already exists.** `scripts/check-test-tmux-leak.sh`, added by MUX-233, counts
  tmux servers including the `-L` form. Whether it also counts `-S <path>` servers, as this spec's
  last criterion asks, was not checked; Phase 1's wrapper can start from it.
- **`test-restart-definition` was edited by MUX-234** (the consumer-guard opt-out). Its cleanup
  still uses the bare `kill-server` as far as this spec knows; its counts are now 30 / 0.
- **Two scripts fail in the full runner for other reasons** and keep their counts under the
  fourth criterion: `test-graph-tui` (44 / 2, MUX-210) and `test-prompt-mode` (4 failing checks),
  as recorded in [MUX-234](./MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md)
  on a full run of 80 passed, 2 failed, 9 skipped.
- **That full run left four paths in the checkout** — `docs/media/sample-aws.svg` and
  `test/fixtures/vite-react-app/vite-test.log` modified, `probe]` and `probe2]` untracked at the
  root. Which script writes them is not traced and it is not this spec's defect; Phase 3's full
  run should expect them and they are not to be staged with this spec.

**No user decision is known to be waiting.**

## Status

**Complete** — closed out 2026-11-18 by `55-defect-backlog-loop` run
`1795035299-55-defect-backlog-loop-416695aa`, which activated it the same day (Defects rank 35;
every better-ranked row was parked). 16/16 — all three phases and all six criteria verified; review
0 must-fix, 0 should-fix, 0 nits. Filed 2026-11-18. Three scripts left a server — `test-version`,
`test-pane-targeting`, `test-escape-absorber` — and now end it by pid, on success, failure, `INT`
and `TERM`. Not committed at close-out; the run's ship step follows.
