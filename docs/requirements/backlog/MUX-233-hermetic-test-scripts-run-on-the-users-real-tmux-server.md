# MUX-233: Seventeen Hermetic Test Scripts Create Their Sessions on the User's Real tmux Server

**Tracking:** [mkober/muxcode#239](https://github.com/mkober/muxcode/issues/239)

Seventeen `scripts/test-*.sh` that describe themselves as hermetic create their scratch tmux session
on the user's own tmux server rather than a private one, and clean up with `kill-session` only. One
script of this shape was measured and did leak: for the length of its run a scratch session sat on
the real server, its panes ran with the real `HOME`, and each run left a lifecycle log in the real
`~/.config/muxcode/logs/`. That script is fixed by
[MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md). The
seventeen here share its shape and have not been measured.

## Context

### Source and standard of evidence

Found 2026-11-17 while working MUX-226, whose Notes asked for the audit. Filed by plan on the
user's standing instruction to file defects found along the way.

| Claim | Standing |
|-------|----------|
| The seventeen call `tmux new-session` with no `TMUX_TMPDIR`, `tmux -L` or `-S`, and clean up with `kill-session` | Read file by file by the MUX-226 worker (`/tmp/mux226-phase1.md`), from a first-pass search by plan. Plan did not re-read each file |
| A script of this shape puts a session on the real server and leaves a log in the real home | **Measured** for `test-graph-node-caps.sh` before its fix (MUX-226, Results for Phase 1) |
| The seventeen leak the same way | **Not measured.** Partial evidence for two: on 2026-11-17 the real log directory held three `multiphase-test-*` logs and two `unrequested-b-*` logs, the session names `test-multi-phase-graph` and `test-unrequested-test-cycles` use. Plan saw the files; which run wrote each was not traced |
| Whether any of them also resolves `muxcode` from the installed binary instead of the one under test — MUX-226's second defect | Not looked at |

### The scripts

`test-close-spec-guard`, `test-codex-idle-delivery`, `test-codex-trust-prompt`,
`test-control-pane`, `test-durable-state-location`, `test-force-respond`, `test-gate-authority`,
`test-graph-orchestrator`, `test-multi-phase-graph`, `test-node-outcome-attribution`,
`test-plan-verify-verdict`, `test-prompt-mode`, `test-spawn-no-cross-tree-delegation`,
`test-stall-redrive-busy`, `test-status-line-task-close`, `test-unrequested-test-cycles` (two
`new-session` calls) and `test-verify-spec-refire`.

Not in scope: `test-resize-hook`, a live-session script meant to drive the real server.

**Corrected the day it was filed.** This spec first counted sixteen and excluded
`test-stall-redrive-busy` as live-session. A review of MUX-226 caught it: that script's header says
"Hermetic: a scratch bus session and a scratch tmux session", it calls a plain `tmux new-session`
and cleans up with `kill-session`, and `scripts/test-all.sh` does not list it as live-only — plan
re-read all three. `CLAUDE.md`'s entry for it says "live-session"; correct that entry with the
script. Its fix must keep MUX-212's `tmux respawn-pane` working on the private server.

### Why it matters

- A scratch session on the real server is visible to the user's own session list, hooks and
  status line for as long as the script runs, and its panes inherit the real server's environment
  — the real `HOME` included — so anything a pane-side process writes by `HOME` escapes the
  scratch directories the script set for itself.
- An interrupted run leaves the session behind: `kill-session` in an exit trap does not run on
  every interrupt, and nothing else removes it.
- Leftover lifecycle logs accumulate in the real log directory, one per run.

### The pattern to follow

MUX-226's fix, and the private-server scripts before it (`test-diagnose-dead-agent.sh` and others):
unset `TMUX`, export `TMUX_TMPDIR="$WORK/tmux"` before the first tmux call, `tmux kill-server` in
the cleanup, and `INT`/`TERM` traps that exit so the cleanup runs on interrupt.

**Two rules for that pattern, both learned on 2026-11-17 — a script that gets them wrong can kill
the user's own tmux server, which is worse than the leak this spec fixes:**

- **Isolate before any trap is armed.** MUX-228's new script armed its exit trap first; an early
  exit would have run its cleanup's `kill-server` against the caller's server. Caught in review
  before it happened.
- **Kill by socket path, not by environment:** `tmux -S "$TMUX_TMPDIR/tmux-$(id -u)/default"
  kill-server`, and only if that socket exists. A bare `tmux kill-server` depends on `TMUX_TMPDIR`
  still naming a directory that exists; when it does not, tmux falls back to the real socket
  (MUX-226's harness met exactly this). Most of the existing private-server scripts use the bare
  form; a shared helper written here should use the by-path form, and they can adopt it.

## Requirements

### Acceptance criteria

- [ ] For each of the seventeen, what it leaves on the real tmux server and in the real home is recorded — measured, not inferred from its shape — or the script is recorded as not leaking, with why
- [ ] Each script found to use the real server creates its session on a private server under its work directory and kills that server on success, failure and interrupt
- [ ] After the change, a run of each leaves nothing on the real server and nothing new in the real `~/.config/muxcode/logs/`
- [ ] Each changed script still passes with its coverage floor met
- [ ] Negative control: the check that detects a scratch session on the real server is shown to fail against an unmodified script
- [ ] The live-session script `test-resize-hook` is untouched

### Technical approach

Measure first, with the wrapper MUX-226 used (snapshot the real server's sessions, global
environment and hooks, and the real state and log directories, before and after; probe during).
Then apply the private-server pattern per script. If the same four lines are pasted seventeen times,
put them in a helper under `scripts/lib/` instead and source it. Whether MUX-226's second defect —
workers resolving the installed binary — applies to any of these is recorded in Phase 1 and fixed
here only where the script spawns workers.

### Key files

| File | Purpose |
|------|---------|
| The seventeen `scripts/test-*.sh` listed above | Session creation and cleanup |
| `scripts/test-graph-node-caps.sh` | The fixed example (MUX-226) |
| `scripts/lib/` | Home for a shared private-server helper, if one is written |
| `scripts/test-all.sh` | Runs them serially; unchanged unless the helper needs it |

## Implementation

### Phase 1: Measure

- [ ] Run each of the seventeen under the snapshot wrapper; record per script what appeared on the real server during the run and what was left after
- [ ] Record which of them spawn workers or panes that resolve `muxcode`, and from where

### Phase 2: Isolate

- [ ] Private tmux server under the work directory for every script Phase 1 shows on the real server, killed in the cleanup, with interrupt traps
- [ ] A shared helper under `scripts/lib/` if the pattern repeats; each script sources it
- [ ] Scratch bin directory first on `PATH` for the scripts Phase 1 shows resolving the installed binary

### Phase 3: Integration test

- [ ] Re-run each changed script under the snapshot wrapper: nothing on the real server during the run, nothing left after, pass counts and floors recorded here
- [ ] Negative control: the wrapper's real-server check fails against one unmodified script (from `git stash` or a scratch copy)
- [ ] `bash scripts/test-all.sh` — the full runner — passes for every changed script; record the summary
- [ ] Update `CLAUDE.md` where a script's entry describes its isolation

## Notes

- Removing the leftover `*-test-*` logs already in the real `~/.config/muxcode/logs/` is the
  user's to do or to ask for; this spec stops new ones.
- Seventeen scripts is a large change for one PR. If Phase 1 shows only some leak, the spec narrows
  to those.

## Related

- [MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) —
  the one measured and fixed instance, and where this audit was asked for
- [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) — the
  orphan reaper, which cleans up after dead sessions but not a scratch session left alive

## Status

**Backlog** — 0/15. Filed 2026-11-17. The shape is read; the leak is measured for one sibling script
and not for these.
