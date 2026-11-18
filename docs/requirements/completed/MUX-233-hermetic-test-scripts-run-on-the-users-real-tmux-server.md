# MUX-233: Hermetic Test Scripts Create Their Sessions on the User's Real tmux Server

**Tracking:** [mkober/muxcode#239](https://github.com/mkober/muxcode/issues/239)

Twenty-one `scripts/test-*.sh` that are not live-session scripts create their scratch tmux session
on the user's own tmux server rather than a private one, and clean up with `kill-session` only. One
script of this shape was measured and did leak: for the length of its run a scratch session sat on
the real server, its panes ran with the real `HOME`, and each run left a lifecycle log in the real
`~/.config/muxcode/logs/`. That script is fixed by
[MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md). The
twenty-one here share its shape and have not been measured. The count has been wrong twice — see
[The scripts](#the-scripts) — so Phase 1 establishes the list before anything is fixed.

**Phase 1 has since done so: the list is twenty-two, by reading, and twenty of them were measured
on the real server** — see [Results for Phase 1](#results-for-phase-1). "Twenty-one" below is the
text as activated; the results are the authority.

## Context

### Source and standard of evidence

Found 2026-11-17 while working MUX-226, whose Notes asked for the audit. Filed by plan on the
user's standing instruction to file defects found along the way.

| Claim | Standing |
|-------|----------|
| The twenty-one call `tmux new-session` with no `TMUX_TMPDIR`, `tmux -L` or `tmux -S`, and clean up with `kill-session` | Seventeen read file by file by the MUX-226 worker (`/tmp/mux226-phase1.md`); the four added at activation read by plan for their `new-session` and cleanup lines only. A search result, not a proof of the full set — a script that reaches the real server another way (through `muxcode launch`, say) would not be found by it |
| A script of this shape puts a session on the real server and leaves a log in the real home | **Measured** for `test-graph-node-caps.sh` before its fix (MUX-226, Results for Phase 1) |
| The twenty-one leak the same way | **Not measured at filing; since measured in Phase 1** — twenty on the real server, two leaving a log; see [Results for Phase 1](#results-for-phase-1). At filing there was partial evidence for two: on 2026-11-17 the real log directory held three `multiphase-test-*` logs and two `unrequested-b-*` logs, the session names `test-multi-phase-graph` and `test-unrequested-test-cycles` use. Plan saw the files; which run wrote each was not traced |
| Whether any of them also resolves `muxcode` from the installed binary instead of the one under test — MUX-226's second defect | Not looked at |

### The scripts

`test-auto-clear`, `test-cancel-provenance`, `test-close-spec-guard`, `test-codex-hooks` (its
session is in its opt-in live section), `test-codex-idle-delivery`, `test-codex-trust-prompt`,
`test-control-pane`, `test-durable-state-location`, `test-force-respond`, `test-gate-authority`,
`test-graph-orchestrator`, `test-graph-worker-reuse`, `test-multi-phase-graph`,
`test-node-outcome-attribution`, `test-plan-verify-verdict`, `test-prompt-mode`,
`test-spawn-no-cross-tree-delegation`, `test-stall-redrive-busy`, `test-status-line-task-close`,
`test-unrequested-test-cycles` (two `new-session` calls) and `test-verify-spec-refire`.

Not in scope: `test-resize-hook`, the one script in the search that `scripts/test-all.sh` lists as
live-only and is meant to drive the real server.

**Corrected twice on the day it was filed — sixteen, then seventeen, now twenty-one.** The second
correction, at activation: plan's first search excluded any script containing the text `-S `,
meaning to exclude `tmux -S <socket>`; that also matches `tmux capture-pane -S -50`, so four
scripts that merely capture scrollback were dropped — `test-auto-clear`, `test-cancel-provenance`,
`test-codex-hooks` and `test-graph-worker-reuse`. Searching for `tmux -S` instead names all
twenty-two, `test-resize-hook` included. The lesson is in the criteria: Phase 1 lists the scripts
by reading, and the count here is a starting point.

The first correction: this spec first counted sixteen and excluded
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

- [x] For each of the twenty-one, what it leaves on the real tmux server and in the real home is recorded — measured, not inferred from its shape — or the script is recorded as not leaking, with why
- [x] Each script found to use the real server creates its session on a private server under its work directory and kills that server on success, failure and interrupt
- [x] After the change, a run of each leaves nothing on the real server and nothing new in the real `~/.config/muxcode/logs/`
- [x] Each changed script still passes with its coverage floor met — for the four Phase 1 found failing for other reasons (`prompt-mode`, `status-line-task-close`, `plan-verify-verdict`, `codex-idle-delivery`), the same pass and fail counts as before the change, check for check (amended 2026-11-17 after Phase 1)
- [x] Negative control: the check that detects a scratch session on the real server is shown to fail against an unmodified script
- [x] The live-session script `test-resize-hook` is untouched

### Technical approach

Measure first, with the wrapper MUX-226 used (snapshot the real server's sessions, global
environment and hooks, and the real state and log directories, before and after; probe during).
Then apply the private-server pattern per script. If the same four lines are pasted twenty-one times,
put them in a helper under `scripts/lib/` instead and source it. Whether MUX-226's second defect —
workers resolving the installed binary — applies to any of these is recorded in Phase 1 and fixed
here only where the script spawns workers.

### Key files

| File | Purpose |
|------|---------|
| The twenty-one `scripts/test-*.sh` listed above | Session creation and cleanup |
| `scripts/test-graph-node-caps.sh` | The fixed example (MUX-226) |
| `scripts/lib/` | Home for a shared private-server helper, if one is written |
| `scripts/test-all.sh` | Runs them serially; unchanged unless the helper needs it |
| `scripts/check-test-tmux-leak.sh` | Added 2026-11-18: census of every tmux server process (default, `-S`, `-L`) around a run of `test-send-keys-dash`, with a positive control for the census and a negative control that reverts the probe cleanup |

## Implementation

### Phase 1: Measure

- [x] Establish the list by reading, not by search: every `scripts/test-*.sh` that starts a tmux session or a muxcode session, and for each whether it is live-only (`scripts/test-all.sh`'s list), already on a private server, or on the real one. Record the list here and correct the count above (added at activation — the count was wrong twice)
- [x] Run each of the twenty-one under the snapshot wrapper; record per script what appeared on the real server during the run and what was left after
- [x] Record which of them spawn workers or panes that resolve `muxcode`, and from where

### Results for Phase 1

Recorded 2026-11-17 from the worker's findings (`/tmp/mux233-phase1.md`), read whole by plan; the
measuring run and the re-run are in the run agent's replies (19:03:34 and 19:06:27). Boxes are
ticked at `verify-spec`, not here.

**The list, by reading: twenty-two.** The worker read every `scripts/test-*.sh` that starts a tmux
session (`tmux new-session`) or a muxcode session (`muxcode launch`).

| Group | Scripts |
|-------|---------|
| Already on a private server (26) | `agent-resume`, `auto-startup-gating`, `batch-reply-correlation`, `diagnose-dead-agent`, `edit-auto-resume`, `escape-absorber` (`-L`), `fkey-labels`, `graph-foreign-dispatch`, `graph-node-caps` (MUX-226), `graph-verify-once`, `orphan-reaper`, `pane-targeting`, `redrive-reply-route`, `reload-defers-dispatch`, `reload-hosted-role`, `reload-keeps-dispatch`, `reply-to-unknown`, `response-echo`, `restart-definition`, `root-go-vet`, `self-upgrade`, `spec-dangling-debounce` (MUX-228), `upgrade-daemons-no-ps`, `version`, `windowless-notify`, `windowless-routing` |
| Live-only, out of scope | `resize-hook` |
| **On the real server (22)** | The twenty-one listed above **plus `test-send-keys-dash`** |
| Search false hits, no session | `chain-completion`, `xcode-compat` (the word "launch") |

`test-send-keys-dash` is the third miss of plan's search, and a different kind: its probe uses a
private `-L` server, so the file contains `tmux -L` and the search excluded it — but its main
session (`:104`, read by plan) is a plain `tmux new-session` on the default server, deliberately,
with a comment that muxcode's pane targeting runs against the default socket. It needs care in
Phase 2, not the mechanical change.

**Measured.** A wrapper snapshotted the real server's sessions, global environment and hooks, and
the real state and log directories, before and after each script, and probed the real server every
0.5 s during (task `1794959247-spawn-56d56314-038b5255`, 18:47–19:03). For all twenty-two, nothing
was left afterwards in sessions, global environment, global hooks or the real state directory.

| Script | Session on the real server during the run | Left in the real `~/.config/muxcode/logs/` | Script's own result |
|--------|-------------------------------------------|--------------------------------------------|---------------------|
| `auto-clear` | Yes | None | 22 / 0 |
| `cancel-provenance` | Yes | None | 57 / 0 |
| `close-spec-guard` | Yes | None | 21 / 0 |
| `codex-hooks` | **No** — its session is only in the opt-in live section, which was not run | None | 42 / 0 hermetic |
| `codex-idle-delivery` | Yes | None | **29 / 7 — fails** |
| `codex-trust-prompt` | Yes | None | 30 / 0 |
| `control-pane` | Yes | None | 15 / 0 |
| `durable-state-location` | Yes | None | 53 / 0 |
| `force-respond` | Yes | None | 18 / 0 |
| `gate-authority` | Yes | None | 32 / 0 |
| `graph-orchestrator` | Yes | None | 60 / 0 |
| `graph-worker-reuse` | Yes | None | 93 / 0 |
| `multi-phase-graph` | Yes | **`multiphase-test-70642.log`** | 52 / 0 |
| `node-outcome-attribution` | Yes | None | 48 / 0 |
| `plan-verify-verdict` | Yes | None | **32 / 4 — fails** |
| `prompt-mode` | Yes | None | **17 / 7, 3 skipped — fails** |
| `send-keys-dash` | Yes | None | 10 / 0 |
| `spawn-no-cross-tree-delegation` | **Not observed** — a 3 s run, likely too short for the 0.5 s probe; it reads as a plain `new-session` at `:79`. Not proven absent | None | 19 / 0 |
| `stall-redrive-busy` | Yes | None | 15 / 0 |
| `status-line-task-close` | Yes | None | **14 / 5 — fails** |
| `unrequested-test-cycles` | Yes, two sessions | **`unrequested-b-19515.log`** | 55 / 0 |
| `verify-spec-refire` | Yes | None | 43 / 0 |

So: **twenty measured on the real server**, one more by reading only, and one (`codex-hooks`) not
there on a default run. **Two leave a lifecycle log in the real home** — the two this spec's
partial evidence pointed at. The first criterion's "or the script is recorded as not leaking"
applies to the rest: they put a session on the real server for the run and leave nothing after.
Panes inherit the real `HOME` wherever the script sets none into its session (`multi-phase-graph`,
`unrequested-test-cycles`, `node-outcome-attribution`, `send-keys-dash` and others); three set a
scratch `HOME` into the session (`durable-state-location`, `graph-worker-reuse`,
`cancel-provenance`).

**Which resolve `muxcode`, and from where.** Five spawn graph workers, which resolve through
`findMuxcodeBinary` (`~/.local/bin`, then `PATH`):

| Script | Resolves | Mixed builds? |
|--------|----------|---------------|
| `durable-state-location` | The tree build it installs into its scratch home's `.local/bin` and puts on the session's `PATH` | No — already right |
| `cancel-provenance`, `graph-worker-reuse` | Scratch home with no `.local/bin`, so `PATH` — the installed binary | Only under `MUXCODE_BIN` (MUX-226's second defect) |
| `multi-phase-graph`, `node-outcome-attribution` | Real home — the installed binary | Only under `MUXCODE_BIN` |

Every other real-server pane resolves the installed binary too, but those panes run stubs or
shells. `graph-orchestrator` deliberately avoids spawn.

**Four scripts fail, and isolation is not why.** Re-run standalone, same counts (task
`1794960246-spawn-56d56314-976f2eb6`). Not fixed here:

| Script | Result | Standing |
|--------|--------|----------|
| `test-prompt-mode` | 17 / 7, 3 skipped | Failing before this loop — the same seven on 2026-11-13. Its plateau is the subject of parked [MUX-122](../backlog/MUX-122-prompt-agent-turn-attribution-and-fix.md), which records a different count (26 / 2 / 1) |
| `test-status-line-task-close` | 14 / 5, floor 19 | 19 / 0 on 2026-11-13. Filed as [MUX-234](../backlog/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md) |
| `test-plan-verify-verdict` | 32 / 4 | 36 / 0 on 2026-11-13. MUX-234 |
| `test-codex-idle-delivery` | 29 / 7, floor 36 | No earlier log. MUX-234 |

The worker's candidate for the two verify-spec failures was MUX-219's graph-owned gate. Plan read
the re-run logs and it is not that: both logs carry the refusal itself — "plan has no window, inbox
listener or headless agent: no consumer for the role's inbox", and the same for edit. That is
[MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md)'s consumer guard, merged
2026-11-16, meeting a fixture with no window for the role. Phase 2 must not "fix" these while
changing the scripts' tmux setup — a script that fails before and after for another reason is
recorded as such, and its pass count is not a criterion here.

**Size.** One measuring pass is about sixteen minutes, serial. The worker's reading: Phase 2 is
mechanical once a helper exists under `scripts/lib/`, each changed script must be re-run, and a
split is not required.

Verified and ticked 2026-11-17. The phase changed no script — the tree holds no change outside
`docs/` — and the wrapper's data is on disk (`/tmp/mux233-measure/`, 265 files, per-script
snapshots). The graph's `./build.sh` exited 0 at 19:13:12 and `./test.sh` at 19:13:33; the review
at 19:13:58 returned 0 must-fix, 0 should-fix, 0 nits, after one round whose two findings were
about this spec's text and are fixed above. The three steps are ticked, and the first criterion:
what each script leaves behind is measured for all twenty-two. One qualification rides with that
tick — for `spawn-no-cross-tree-delegation` the session during the run was read from the script,
not observed. Phase 3 re-measures every changed script, so it is looked at again there.

### Phase 2: Isolate

- [x] Private tmux server under the work directory for every script Phase 1 shows on the real server, killed in the cleanup, with interrupt traps
- [x] A shared helper under `scripts/lib/` if the pattern repeats; each script sources it
- [x] Scratch bin directory first on `PATH` for the scripts Phase 1 shows resolving the installed binary

### Results for Phase 2

Recorded 2026-11-17 from the worker's findings (`/tmp/mux233-phase2.md`), read whole by plan; plan
read both helpers and checked the twenty-two scripts mechanically; the runs are in the run agent's
replies. Boxes are ticked at `verify-spec`, not here.

**Two helpers, new under `scripts/lib/`.**

| Helper | What it does |
|--------|--------------|
| `private-tmux.sh` | `private_tmux_init DIR` unsets `TMUX` and `TMUX_PANE`, exports `TMUX_TMPDIR=DIR`, records the private socket's path, and makes `INT` and `TERM` exit (130, 143) so an exit trap still runs. `private_tmux_kill` kills the server **by that socket path, only if the socket exists**, and never fails. Its header states both rules from this spec's pattern section |
| `scratch-bin.sh` | `scratch_bin_first DIR` makes `MUX` absolute, links it as `DIR/muxcode` and puts `DIR` first on `PATH` |

**Applied to all twenty-two.** Each sources `private-tmux.sh` and calls `private_tmux_init
"$WORK/tmux"` just before its `cleanup()`; its `tmux kill-session` became `private_tmux_kill`.
`test-send-keys-dash` keeps its `-L` probe server, and its "default server" comment now says
`TMUX_TMPDIR` makes the default socket private. `scratch_bin_first` is in the four that spawn
workers on the installed binary: `cancel-provenance`, `graph-worker-reuse`, `multi-phase-graph`
and `node-outcome-attribution`.

**Plan's own check of the rule that matters**, over all twenty-two files: in every one the
`private_tmux_init` line comes before the first `trap`; no `kill-session` and no bare `tmux
kill-server` remains; every one calls `private_tmux_kill`; both helpers parse. Twenty-one mention
`tmux` on a line above the init call — plan read every such line: all are `command -v tmux`
availability checks, a tool-list loop, or text inside a stub script written to disk. None is a tmux
call. A line-level check, not a trace of each script's execution.

**A correction to Phase 1.** `multi-phase-graph` and `node-outcome-attribution` *do* export a
scratch `HOME`. The real `HOME` the probe saw in their panes was the real server's global
environment, inherited by panes — which is the defect itself, not a missing export. With a scratch
home holding no `.local/bin`, `findMuxcodeBinary` falls to `PATH`, so the scratch bin covers all
four.

**Two fixes the re-measure forced** — neither was foreseen in this spec:

1. **A cleanup that aborted under `set -e`** (`control-pane`, `force-respond`, `send-keys-dash`,
   `prompt-mode`). `tmux kill-server` can exit non-zero as its server goes away; under `set -euo
   pipefail` the exit trap stopped there. The script exited 1 after printing its own success, and
   — for the scripts running with the real home — left its bus directory in the real
   `~/.local/state/muxcode/bus/`. Traced in a run of its own (reply at 19:34:50). Fixed in the
   helper, which now never fails. **What this uncovered:** before the change, those scripts' bus
   directories were being cleaned up by the user's own server — `kill-session` there fired the
   global `session-closed` hook (MUX-184), which removed them. The scripts were leaning on the
   real server for their cleanup as well as for their session.
2. **`stall-redrive-busy`'s stale-spinner check.** The delivered row arrived, but wrapped in the
   private server's narrower pane, so a single-line `grep` missed it. Its `pane_text` now captures
   with `-J`, which joins wrapped lines. The check is the one MUX-212 fixed earlier the same day.

**Runs, under the Phase 1 wrapper.**

| Run | Result |
|-----|--------|
| All twenty-two (task `1794961039-spawn-56d56314-73026981`, 19:17–19:33) | **No session on the real server during any of the twenty-two** — Phase 1 saw one for twenty of them. The two log leaks are gone |
| The six affected by the two fixes, re-run (task `1794962138-spawn-56d56314-38150dd8`, 19:35–19:41) | All clean, nothing left behind |

| Scripts | Counts |
|---------|--------|
| `auto-clear` 22, `close-spec-guard` 21, `cancel-provenance` 57, `codex-hooks` 42, `codex-trust-prompt` 30, `durable-state-location` 53, `gate-authority` 32, `graph-orchestrator` 60, `graph-worker-reuse` 93, `multi-phase-graph` 52, `node-outcome-attribution` 48, `spawn-no-cross-tree-delegation` 19, `unrequested-test-cycles` 55, `verify-spec-refire` 43 | All passed, none failed — the same as Phase 1 |
| `control-pane` 15, `force-respond` 18, `send-keys-dash` 10, `stall-redrive-busy` 15 | All passed after the two fixes |
| The four known to fail for other reasons | `plan-verify-verdict` 32 / 4, `status-line-task-close` 14 / 5, `prompt-mode` 17 / 7 with 3 skipped — the same as Phase 1. `codex-idle-delivery` 29 / 7 twice, **and 28 / 8 once**, in the full run, not reproduced |

That one 28 / 8 matters for criterion 4, which asks for the same counts check for check: one of
`codex-idle-delivery`'s checks is not stable from run to run, with or without this change. It is
noted in [MUX-234](../backlog/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md).

The worker updated `CLAUDE.md`'s entry for `test-stall-redrive-busy`. Phase 3 owns the formal
re-run, the negative control and the full runner.

#### Review fix (2026-11-17, 19:44–19:51) — five scripts could not find the helper

The review of the above returned **1 must-fix**, verdict `EXIT=1`. Five scripts sourced the helpers
through a relative `BASH_SOURCE` path *after* a top-level `cd`. Invoked the ordinary way —
`bash scripts/<name>.sh` from the repository root — the path no longer resolved, the helper was
never loaded, and the script ran with **no isolation and no cleanup**: on the real server again,
and with `kill-session` gone, leaving its session there.

**Why nothing caught it.** Phase 2's runs invoked the scripts by absolute path, which resolves from
anywhere. And plan's check above looked at where the init call sits against traps and tmux calls —
not at whether the file it sources can be found. A check of ordering, passed by scripts that never
loaded the function.

| Change | Scripts |
|--------|---------|
| The helpers are sourced beside the script's existing `lib/` sources, before the `cd` | `cancel-provenance`, `gate-authority`, `graph-worker-reuse`, `node-outcome-attribution` |
| The helper is sourced through the script's saved absolute `SCRIPT_DIR` | `prompt-mode` |
| The wrapper now invokes every script relatively, from the repository root | — |

**The run** (task `1794962817-spawn-56d56314-8f746803`, 19:47–19:51, reply at 19:51:28), invoked as
`bash scripts/<name>.sh`: no session on the real server, nothing left; `cancel-provenance` 57,
`gate-authority` 32, `graph-worker-reuse` 93, `node-outcome-attribution` 48 and `auto-clear` 22 all
passing; `prompt-mode` 17 / 7 as before.

**Plan re-checked all twenty-two for this:** in twenty-one no line beginning `cd ` precedes the
`private-tmux.sh` source line; in `prompt-mode` one does, and its source uses `$SCRIPT_DIR`. That
looks only for a `cd` at the start of a line — one inside a function or after `&&` would not be
seen. **Phase 3's re-run is the real check, and it must invoke each script the way a person does:
relatively, from the repository root.**

#### Verification (2026-11-17, 19:55)

Ticked on the tree after the must-fix; the report in the dispatch predates it. The last script was
edited at 19:44:53. After it: the relative-invocation run (reply at 19:51:28), `./build.sh` exit 0
at 19:52:21, `./test.sh` exit 0 at 19:52:41, review at 19:53:15 with 0 must-fix, 0 should-fix, 0
nits. Twenty-five files are changed outside `docs/`: the twenty-two scripts, the two helpers and
`CLAUDE.md`.

The three steps are ticked, and the sixth criterion: `scripts/test-resize-hook.sh` is unmodified.

**Left open, and why.**

- **Criterion 2** — killed "on success, failure and interrupt". Success is shown, and failure too:
  the four scripts that fail for other reasons exit 1 and leave nothing. **Interrupt is not
  shown.** The traps are in the helper and read correctly; no run was interrupted. Phase 3 had no
  step for it, so plan added one, on two scripts.
- **Criterion 3** — nothing on the real server, nothing new in the real logs. The full
  twenty-two-script re-measure showed it, but it ran before the must-fix and by absolute path; the
  run since then covered six. Phase 3's relative re-run of all of them decides it.
- **Criteria 4 and 5** are Phase 3's.

### Phase 3: Integration test

- [x] Re-run each changed script under the snapshot wrapper — **invoked relatively, as `bash scripts/<name>.sh` from the repository root** (added 2026-11-17: absolute-path runs hid five scripts that could not find their helper): nothing on the real server during the run, nothing left after, pass counts and floors recorded here
- [x] Interrupted run, on two of the changed scripts — one that spawns workers and one that does not: send `INT` partway through, and `TERM` to the other; after each, no server on its private socket, no process naming its scratch session, its work directory and bus directory gone, and the real tmux server and real `~/.config/muxcode/logs/` unchanged. The helper is shared, so two scripts stand for the rest; say so in the results (added 2026-11-17 at verification of Phase 2, for criterion 2: only the success and failure paths had been shown)
- [x] Negative control: the wrapper's real-server check fails against one unmodified script (a scratch copy taken from `git show HEAD:<path>` — not `git stash`, which a worker may not run)
- [x] `bash scripts/test-all.sh` — the full runner — and record its summary. Every changed script passes, except the four Phase 1 found failing for other reasons — `status-line-task-close`, `plan-verify-verdict` and `codex-idle-delivery` (MUX-234), and `prompt-mode` (MUX-122) — which must show the same pass and fail counts as before the change. The runner's own exit code is therefore 1 until those specs land, and that is not a failure of this step (aligned with criterion 4, 2026-11-17, after review)
- [x] Update `CLAUDE.md` where a script's entry describes its isolation

### Results for Phase 3

Recorded 2026-11-17 from the worker's findings (`/tmp/mux233-phase3.md`), read whole by plan. Plan
read `config/tmux.conf`, the helper as it now stands, and the machine's process list. Boxes are
ticked at `verify-spec`, not here.

**A fix this phase forced: `tmux kill-server` does not kill a server that loaded muxcode's config.**
The first interrupted run left `test-auto-clear`'s private server alive after `TERM`. The cleanup
*had* run `tmux -S <sock> kill-server`; it failed with "no current client".
`config/tmux.conf:117` aliases `kill-server` to a `confirm-before` prompt — a guard against the
user killing every session by accident — and a prompt needs an attached client. So in any script
that runs with the real home, and so loads that config, the private server survived **every** run
since Phase 2. The Phase 1 and 2 wrapper looked only at the real server and could not see it. It
also explains Phase 2's `set -e` aborts: that was `kill-server`'s non-zero exit.

| Change | What it is |
|--------|------------|
| `private_tmux_kill` | Reads the server's pid through the socket (`list-sessions -F '#{pid}'`, which needs no client), sends `TERM`, waits up to 2 s, then `KILL`. Never fails. Plan read it |
| The wrapper | Also snapshots running tmux server processes — a `servers=` leftover column |
| What the worker's own runs had left | 33 orphaned scratch servers of these scripts ended, and 5 leaked bus directories removed from the real `~/.local/state/muxcode/bus/` |

This overturns one thing written above: the "kill by socket path" rule in this spec's pattern
section is necessary and **not sufficient**. The command it names is the aliased one. The rule
that holds is: find the server's pid through its socket and signal the process.

**The re-run of all twenty-two, invoked relatively** (task `1794964560-spawn-56d56314-bd39294c`):
no session on the real server during any of them; nothing left in sessions, global environment,
hooks, state or logs. Eighteen pass with floors met — `auto-clear` 22, `cancel-provenance` 57,
`close-spec-guard` 21, `codex-hooks` 42, `codex-trust-prompt` 30, `control-pane` 15,
`durable-state-location` 53, `force-respond` 18, `gate-authority` 32, `graph-orchestrator` 60,
`graph-worker-reuse` 93, `multi-phase-graph` 52, `node-outcome-attribution` 48, `send-keys-dash`
10, `spawn-no-cross-tree-delegation` 19, `stall-redrive-busy` 15, `unrequested-test-cycles` 55,
`verify-spec-refire` 43. The four baselines are unchanged: `codex-idle-delivery` 29 / 7,
`plan-verify-verdict` 32 / 4, `status-line-task-close` 14 / 5, `prompt-mode` 17 / 7 with 3 skipped.

**Interrupted runs** (task `1794965558-spawn-56d56314-8adbdd89`): 12 passed, 0 failed.
`test-graph-worker-reuse`, which spawns workers, was sent `INT` once its private server held the
session with two or more windows — exit 130. `test-auto-clear`, which does not, was sent `TERM` —
exit 143. After each: no server on the private socket, no process naming the scratch session, the
work directory gone, and the real tmux sessions, state directory and logs unchanged. The helper is
shared by all twenty-two, so these two stand for the rest. The first attempt was 11 to 1 — the
surviving server that led to the fix above.

**Negative control** (task `1794965586-spawn-56d56314-0fcc0a64`): `HEAD`'s copy of
`test-close-spec-guard`, run from a scratch tree under the same wrapper, passes 21 of 21 and is
**flagged** — one session, `close-guard-test-64306`, on the real server. The changed script passes
21 of 21 and is not flagged.

**`bash scripts/test-all.sh`, the full runner** (task `1794965635-spawn-56d56314-bcb67e77`, about
34 minutes): 75 passed, 7 failed, 9 skipped as live-session; exit 1. Every changed script passes
or holds its baseline. The seven:

| Failed | Counts | Standing |
|--------|--------|----------|
| `codex-idle-delivery`, `plan-verify-verdict`, `status-line-task-close` | 29 / 7, 32 / 4, 14 / 5 | The baselines — [MUX-234](../backlog/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md) |
| `prompt-mode` | 17 / 7 | The baseline — parked MUX-122 |
| `graph-tui` | 44 / 2 | Parked [MUX-210](../backlog/MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md) |
| `fix-loop-cap` | 26 / 5 | **New to this record, not among the twenty-two.** Passed on 2026-11-13. Identical on a clean export of `HEAD` (`eaf0e69`) — on `main` already, not from this change. Added to MUX-234 |
| `restart-definition` | 28 / 2 | The same: new to this record, identical on clean `HEAD`. Added to MUX-234 |

**`CLAUDE.md`**, by the worker: a general sentence on integration tests — hermetic scripts run tmux
on a private server through `scripts/lib/private-tmux.sh`, init before any trap, kill by pid
through the socket path because `kill-server` is aliased, `scratch_bin_first` for graph workers —
and the entries for `test-cancel-provenance` and `test-graph-worker-reuse`.

#### What plan found on the machine — one claim above does not hold

The findings say the re-run left "no surviving server". Plan listed the running tmux server
processes at 21:35 and found scratch servers the wrapper's column did not count:

| Server | Count, and when started | From |
|--------|-------------------------|------|
| `tmux -L skdash-<pid> … -s probe` | Nine: two from 2026-11-13, **seven from this evening** (19:00 to 21:19, the last during the full runner) | `test-send-keys-dash` — **one of the twenty-two.** Its main session is now on the helper's private server and is cleaned up; its separate `-L` probe server is still cleaned up by `tmux -L "$SOCK" kill-server` (`:44`), the aliased command, and survives every run |
| `pane-id-test-*` | Five: four from 2026-11-13, one from 21:07 tonight | `test-pane-targeting`, already private, bare `kill-server` |
| `version-test-*` | Three: two from 2026-11-13, one from 21:23 tonight | `test-version`, the same |
| `mux226-caller` | One, 18:13 | MUX-226's named-server validation harness |
| `keep` | Two, 2026-11-16 | Origin not known |

The wrapper's pattern looks for `tmux new-session -d` and so misses a server started as
`tmux -L <name> new-session`. Two consequences:

- **`test-send-keys-dash` still leaves a tmux server behind after every run.** Not on the user's
  server, and the leak predates this spec — but it is one of the scripts this spec changed, and
  the results' "no surviving server" is not true of it. Its probe cleanup needs the same pid-based
  kill, or it is handed to MUX-235 by name.
- **The wider class is filed as
  [MUX-235](../backlog/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md):** every
  script that cleans up with `tmux kill-server` under the real home.

None of these servers was touched by plan. Ending them is the user's to do or to ask for.

#### Corrections, 2026-11-18 — after the review of the above

The review of Phase 3 returned 0 must-fix, 1 should-fix, 1 nit: two follow-ups this section claims
did not exist, and one conclusion had gone stale. The run's `fix` worker could not make them — all
three are plan's to write — and the run failed there. Plan made them the next day, after the
machine restarted.

| Claim above | What was true | Now |
|-------------|---------------|-----|
| "The wider class is filed as MUX-235" | No file was written on the 17th | Filed 2026-11-18: [MUX-235](../backlog/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md), [#243](https://github.com/mkober/muxcode/issues/243), Defects rank 37 |
| `fix-loop-cap` and `restart-definition` "Added to MUX-234" | Not added on the 17th | Added 2026-11-18, with a criterion and a Phase 1 step of their own |
| "`test-send-keys-dash` still leaves a tmux server behind after every run" — its probe cleanup at `:44` is the aliased `kill-server` | True of the file plan read at 21:35. **Stale since:** the script was changed after that reading | `:44` is now `private_tmux_kill "$TMUX_TMPDIR/tmux-$(id -u)/$SOCK"` — the pid-based kill, given the probe server's socket — followed by the default one at `:45`. The helper takes a socket argument for this. Read by plan in the tree, 2026-11-18 |

**The validating run the review asked for** (2026-11-18, run task
`1795024846-spawn-154628e4-6931603e`, the run agent's reply at 13:01; plan read the log,
`/tmp/mux233-leak.log`, and the checker). A new tracked script, `scripts/check-test-tmux-leak.sh`,
run as `bash scripts/check-test-tmux-leak.sh` from the repository root, takes a census of every
tmux server process — default, `-S` and `-L` — before and after. Exit 0; 8 passed, 0 failed:

| Check | Result |
|-------|--------|
| Positive control for the census | It sees a `tmux -L` server started for the purpose — the form the wrapper of the 17th missed |
| The fixed `test-send-keys-dash` | 10 passed, 0 failed; no `skdash` session on the real server during the run; no tmux server process left, default or `-L`; the real server's sessions and the real `~/.config/muxcode/logs/` unchanged |
| Negative control — a scratch copy with the probe cleanup put back to `tmux -L "$SOCK" kill-server` | Also passes 10 of 10, and leaves its server: pid 33300, `tmux -L skdash-33284 new-session -d -s probe`. Detected by the census, then ended by pid |

So the stale conclusion is replaced by a shown one: with the fix the script leaves no server, and
without it the same check finds one. This covers `test-send-keys-dash` alone. The checker runs that
one script; it is not the re-run of all twenty-two, which Phase 3's first step records from the
17th under a wrapper that could not see `-L` servers. Only this script among the twenty-two starts
one, by plan's search of the 17th for `tmux -L`.

**The restart also removed** every leftover server in the table above, and the evidence files this
spec cites under `/tmp` (`mux233-phase1.md` to `phase3.md`, `mux233-measure/`). The results stand as
recorded; they can no longer be re-read.

**Two empty files, `probe]` and `probe2]`, sit untracked at the repository root**, both created at
21:08 on 2026-11-17 — during the full runner. What wrote them is not known; the names suggest a
mis-quoted redirect or `-s probe` argument in a probe command. Plan did not remove them: they are
outside `docs/`. By 13:05 on 2026-11-18 both were gone from the tree — removed by someone else,
plan did not see by whom.

#### Verification (2026-11-18, 13:10)

Ticked on the tree after the resumed run's fix. After the last file change (the checker, 13:00:44):
`./build.sh` exit 0 at 13:02, `./test.sh` passed at 13:02 (2 modules, 0 failed), review at 13:03
with 0 must-fix, 0 should-fix, 0 nits. The dispatch carried no worker report; plan verified from
the results above, the run agent's replies and the tree.

The five steps are ticked, and criteria 2 to 5 — the spec is 17/17.

| Item | Rests on |
|------|----------|
| Step 1, criteria 3 and 4 | The relative re-run of all twenty-two on the 17th: no session on the real server, nothing left, eighteen passing with floors met and the four baselines unchanged |
| Step 2, criterion 2 (interrupt) | `INT` to `test-graph-worker-reuse`, `TERM` to `test-auto-clear`, 12 passed, 0 failed |
| Criterion 2 (success and failure) | The full runner ran all twenty-two, the four failing ones included, between 20:34 and 21:08; plan's process list at 21:35 held no server of any of them except `test-send-keys-dash`'s `-L` probe — fixed since, and shown today |
| Step 3, criterion 5 | `HEAD`'s `test-close-spec-guard` flagged on the real server; the changed one not |
| Step 4 | The full runner's summary: 75 passed, 7 failed, 9 skipped; every changed script passes or holds its baseline |
| Step 5 | `CLAUDE.md` carries the private-server sentence and the entries for `test-stall-redrive-busy`, `test-cancel-provenance` and `test-graph-worker-reuse` — plan read them |

**Two qualifications ride with these ticks.**

- **The shared helper was edited after the runs of the 17th.** `scripts/lib/private-tmux.sh` and
  `test-send-keys-dash.sh` were last written at 21:43; every Phase 3 run that evening ended by
  21:08. Plan's description of the helper at 21:35 matches its kill logic today, so the 21:43 edit
  is the socket argument and the probe cleanup line. Since that edit, one script of the twenty-two
  has been run — `test-send-keys-dash`, today, which calls the helper both with a socket and
  without. The other twenty-one call it without, the path that run exercised. They have not
  themselves been re-run on the final helper.
- **The evidence of the 17th can no longer be re-read.** The findings files and per-script
  snapshots were under `/tmp` and were lost in the restart. The ticks rest on what plan recorded
  here that evening, having read the findings whole, and on the run agent's replies.

`./test.sh` does not run the integration scripts, so its pass says nothing about them.

## Notes

- Removing the leftover `*-test-*` logs already in the real `~/.config/muxcode/logs/` is the
  user's to do or to ask for; this spec stops new ones.
- Twenty-one scripts is a large change for one PR. If Phase 1 shows only some leak, the spec narrows
  to those.

## Related

- [MUX-226](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) —
  the one measured and fixed instance, and where this audit was asked for
- [MUX-184](../completed/MUX-184-orphaned-session-processes-never-reaped.md) — the
  orphan reaper, which cleans up after dead sessions but not a scratch session left alive

### Findings at activation (2026-11-17)

Activated against the tree at `eaf0e69`, a few hours after filing. Nothing was run.

- **The count changed again, to twenty-one** — see [The scripts](#the-scripts). Phase 1 gained a
  first step, to establish the list by reading; the spec is 0/16, where it was filed 0/15.
- **`scripts/lib/`** holds `muxcode-bus-dir.sh`, `muxcode-version.sh` and
  `escape-chord-receiver.py` — no tmux helper yet.
- **Both pattern rules above are binding on the worker.** This work edits the cleanup of twenty-one
  scripts, and the run agent executes them inside the user's own tmux server. A cleanup that runs a
  bare `tmux kill-server` before isolation is in place kills that server — every agent session,
  this loop's included. Isolate before any trap; kill by socket path, only if the socket exists.
  Phase 3's negative control and the early-failure case MUX-228 ran are the checks that it holds.
- **Size.** Twenty-one integration scripts, several of them minutes long, each run at least twice
  under the wrapper. This is the largest spec the loop has taken today. If Phase 1 shows the work
  wants splitting, that is to be said and recorded, not pushed through.

**No user decision is known to be waiting.** GitHub issue #239 still says sixteen in its title and
body; correcting it waits for the user to ask.

## Status

**Complete** — closed out 2026-11-18 at 17/17. Activated 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 35; every better-ranked row is parked, MUX-231 at rank 34 included). All three phases and all six criteria ticked 2026-11-18. Filed 2026-11-17. Phase 1 measured them: twenty-two scripts listed by reading, twenty seen on the real tmux server during their run, two leaving a lifecycle log in the real home.

Phase 3's results are recorded and its boxes are unticked: the run failed on 2026-11-17 at its
`fix` node after the Phase 3 review (0 must-fix, 1 should-fix, 1 nit), on three docs follow-ups
only plan could write. Those are made (2026-11-18, see the corrections under Results for Phase 3).
The run was resumed on 2026-11-18 and its `fix` worker added the validating run for
`test-send-keys-dash`. Phase 3 was verified at 13:10 that day, with two qualifications recorded
under its results.
