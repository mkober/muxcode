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

- [ ] The script's tmux session lives on a private server under its work directory; no session, window or environment entry is created on the user's server
- [ ] Cleanup kills that server, on success, failure and interrupt
- [ ] Every `muxcode` a spawned worker or pane resolves is the binary under test: a scratch bin directory holding it is first on `PATH` for the script, its daemon and its panes
- [ ] With `MUXCODE_BIN` pointing at a tree build, the script proves one build ran everywhere (negative control: the assertion fails when the scratch bin directory is left off `PATH`)
- [ ] The script still passes, its coverage floor raised for the new checks
- [ ] Both review threads on PR #206 are answered and resolved — the commit agent's work, on the user's request

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

- [ ] Record what the current script leaves on the real tmux server and under the real state directory after a run
- [ ] Private tmux server under `$WORK`, killed in the trap
- [ ] Scratch bin directory first on `PATH`, in the script's environment and the session's

### Phase 2: Integration test

- [ ] Add checks to the script: the user's tmux server shows no scratch session during or after the run; the spawned worker's `muxcode` is `$MUX`
- [ ] Negative control for the binary check, and a raised floor
- [ ] Run the script with the installed binary and with a tree build (`MUXCODE_BIN`); record both pass/fail counts in this spec
- [ ] Update the script's entry in `CLAUDE.md`

## Notes

- Filed 2026-11-14 by plan from `/tmp/defect-node-caps-isolation.md` (edit), on the user's standing instruction to file defects found along the way.
- Other scripts written this week may share the first defect; an audit of `scripts/test-*.sh` for `tmux new-session` without a private server is worth a line in Phase 1's results, and a separate spec if it finds more.

## Status

**Backlog** — 0/13. Filed 2026-11-14.
