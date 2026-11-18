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
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), whose
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
  ([MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md)) acts on dead
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

- [ ] The list of test scripts that end a tmux server with `kill-server` is established by reading, and for each it is recorded — measured — whether its server survives a run
- [ ] Every script found to leave a server ends it through `private_tmux_kill` (`scripts/lib/private-tmux.sh`), or an equivalent that signals the server's pid found through its socket
- [ ] After a run of each changed script — success, failure and interrupt on a sample — no tmux server process of that script remains
- [ ] Each changed script still passes with its coverage floor met; a script failing for another reason keeps the same counts, check for check
- [ ] Negative control: the check that detects a surviving server is shown to fail against an unmodified script
- [ ] The check counts servers started as `tmux -L <name>` and `tmux -S <path>` as well as the default socket — MUX-233's wrapper missed the first form

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

- [ ] Establish the list by reading every `scripts/test-*.sh` that starts a tmux server; record which load `config/tmux.conf` and how
- [ ] Run each under a wrapper that snapshots tmux server processes — default, `-L` and `-S` forms — before and after; record which leave a server
- [ ] Record what `test-fkey-labels` leaves, since it avoids `kill-server` by design

### Phase 2: Fix the cleanups

- [ ] Each script found leaving a server: pid-based kill through the helper, by socket path, only if the socket exists
- [ ] No cleanup can abort under `set -e` before it removes its work and bus directories

### Phase 3: Integration test

- [ ] Re-run each changed script under the wrapper, invoked as `bash scripts/<name>.sh` from the repository root: no surviving server, counts and floors recorded here
- [ ] Interrupted run on two changed scripts, `INT` and `TERM`: no surviving server, work directory gone
- [ ] Negative control: the wrapper flags `HEAD`'s copy of one changed script, run from a scratch tree
- [ ] `bash scripts/test-all.sh` — the full runner — and record its summary; no changed script newly fails
- [ ] Update `CLAUDE.md` where a script's entry describes its cleanup

## Related

- [MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — where
  this was found, and the helper that fixes it
- [MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) —
  `test-graph-node-caps`, whose fix uses the bare `kill-server`
- [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) — the orphan reaper,
  which does not cover a live scratch server
- [MUX-147](./MUX-147-process-leak-and-memory-footprint.md) — parked; its remaining scope is the
  scripts' own cleanup

## Status

**Backlog** — 0/16. Filed 2026-11-18. The alias and the script list are read from the tree; which
scripts are affected is for Phase 1.
