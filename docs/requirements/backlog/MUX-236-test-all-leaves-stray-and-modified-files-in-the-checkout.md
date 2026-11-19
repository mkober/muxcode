# MUX-236: `scripts/test-all.sh` Leaves Stray and Modified Files in the Checkout

**Tracking:** [mkober/muxcode#247](https://github.com/mkober/muxcode/issues/247)

A run of `bash scripts/test-all.sh` does not leave the working tree as it found it. After every run
on 2026-11-18 the checkout held two new empty files at the repository root, `probe]` and `probe2]`,
and two tracked files modified: `docs/media/sample-aws.svg` and
`test/fixtures/vite-react-app/vite-test.log`. Each had to be removed or restored by hand before a
ship, and once it was not: MUX-233's ship commit carries both tracked files.

## Context

### Source and standard of evidence

| Claim | Standing |
|-------|----------|
| The four paths appear after a full run | **Observed** — after each full run on 2026-11-18 (the MUX-234 and MUX-235 runs); `probe]` and `probe2]` also reappeared at 16:01:29 during MUX-235's 26-script measurement, which ran `test-pane-targeting` and not the full runner |
| The modified tracked files have been committed by a ship | **Read** — `git log` names `a58a187` (MUX-233, PR #244) as the last commit to touch both `docs/media/sample-aws.svg` and `test/fixtures/vite-react-app/vite-test.log`; neither is part of that spec |
| Which script writes each path | **Read in the scripts, not proven by a run** — see below. Phase 1 proves or corrects it |

Edit's evidence note for this filing is `/tmp/test-all-stray-files.md` (volatile; what it carries is
recorded here).

### The writers — as read

| Path | Script | Mechanism |
|------|--------|-----------|
| `probe]`, `probe2]` | `scripts/test-pane-targeting.sh` | Its agent stand-ins are real bash shells with `PS1="❯ "` (lines 101, 176), started by `tmux new-session` / `split-window` with no `-c`, so their working directory is wherever the script was run from — the repository root. `deliver_lands` (line 78) sends a request with action `probe` (lines 135, 202) or `probe2` (line 156) and force-delivers it. The wake payload is built as `[<from>><action>] <text>` (`tools/muxcode/bus/notify.go:170`), and the shell executes what is typed into it: `><action>]` is a redirect, to a file named `probe]` or `probe2]` |
| `docs/media/sample-aws.svg` | `scripts/test-plan-diagram-render.sh` | `render_fixture` renders each fixture to its default output path beside the source (lines 84–127). `track_output` (line 52) records an output for removal only when it did not exist before, so new images are cleaned up — but `sample-aws.svg` is tracked (the only rendered image under `docs/media/` that is), exists before the run, and is overwritten in place with nothing to restore it |
| `test/fixtures/vite-react-app/vite-test.log` | `scripts/test-playwright-check.sh:94` | `npx vite … &>"$FIXTURE_DIR/vite-test.log"` writes the dev server's output into the fixture directory, where that log is a tracked file. `scripts/test-browser-monitor-e2e.sh:101` has the same line; it is a live-session script, which the full runner skips |

The script's own header for the diagram test says the fixtures in `docs/media/` "must be left
untouched" (line 133); the tracked output is the case it missed.

### Consequence

- A ship that stages the tree commits test output. It has happened once (`a58a187`), and every ship
  since has depended on someone remembering to restore two files and delete two more.
- `docs/media/sample-aws.svg` is a published asset. Each run replaces it with whatever the local
  renderer emits.
- A graph run's `ship` step sees a dirty tree it did not make. Plan flagged the four paths at each
  `verify-spec` and `close-spec` on 2026-11-18.

### Not this spec

- tmux servers a script leaves running — [MUX-235](../completed/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md).
- Files a script leaves outside the checkout (`/tmp`, the state directory) —
  `test-durable-state-location` and `test-bus-dir-leak` cover their own cases; this spec is the
  working tree.
- Whether `vite-test.log` and `sample-aws.svg` should be tracked at all. Untracking either is one
  way to meet the criteria and is left to the implementer; reverting what `a58a187` committed to
  them is a separate, user-initiated change.

## Requirements

### Acceptance criteria

- [ ] The writer of each of the four paths is established by a run, not by reading: for each, one script named, and the tree shown clean without it and dirty with it
- [ ] After `bash scripts/test-all.sh`, `git status --porcelain` is identical to what it was before the run — no new untracked file, no modified tracked file
- [ ] The same holds for each changed script run alone as `bash scripts/<name>.sh` from the repository root, on success and on a failing or interrupted run of a sample
- [ ] A stand-in pane that receives typed payloads cannot write into the checkout: its working directory is under the script's work dir
- [ ] Each changed script still passes with its coverage floor met; one failing for another reason keeps the same counts, check for check
- [ ] The full runner itself detects a dirtied tree: a run that changes `git status --porcelain` names the paths and exits non-zero
- [ ] Negative control: that check is shown to fail against an unmodified script that dirties the tree

### Technical approach

A hermetic script writes only under its own work directory.

- `test-pane-targeting`: start the stand-in panes with `-c "$WORK"`. This removes the write into the
  checkout; the payload is still executed by a shell, which the script's design accepts.
- `test-plan-diagram-render`: render into a copy of the fixtures under a work directory, or pass an
  explicit output path, so no path under `docs/media/` is written. The assertion "output lives
  under `docs/media/`" (line 106) tests the renderer's default-path rule and needs a form that
  does not require writing there.
- `test-playwright-check` and `test-browser-monitor-e2e`: write the vite log under a work
  directory. `npx playwright install` and `node_modules` under the fixture are ignored files and
  are not this spec's concern unless Phase 1 shows otherwise.
- The runner: snapshot `git status --porcelain` before the first script and compare after the last
  — or per script, which names the offender. Outside a git checkout the check is skipped and says so.

Phase 1 may find writers the reading missed; the scope is whatever the measurement shows.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-all.sh` | The full runner; gains the tree check |
| `scripts/test-pane-targeting.sh` | Stand-in shells whose cwd is the repository root |
| `scripts/test-plan-diagram-render.sh` | Overwrites the tracked `docs/media/sample-aws.svg` |
| `scripts/test-playwright-check.sh` | Writes the tracked `vite-test.log` |
| `scripts/test-browser-monitor-e2e.sh` | The same line; live-session, skipped by the runner |
| `tools/muxcode/bus/notify.go` | Builds the `[<from>><action>] <text>` wake payload — read only, not changed |
| `CLAUDE.md` | The `scripts/test-all.sh` and `scripts/test-*.sh` rows |

## Implementation

### Phase 1: Measure

- [ ] From a clean tree, run each suspected script alone and record `git status --porcelain` before and after; record the result per script here
- [ ] Run the full runner with a per-script status snapshot and record every script that changes the tree — including any the reading did not name
- [ ] Record the list of scripts in scope for Phase 2

### Phase 2: Fix the writers

- [ ] `test-pane-targeting`: stand-in panes start in the work directory
- [ ] `test-plan-diagram-render`: no write under `docs/media/`; the default-path assertion kept in a form that does not need one
- [ ] `test-playwright-check` and `test-browser-monitor-e2e`: vite log under a work directory
- [ ] Any further writer Phase 1 found

### Phase 3: The runner's check

- [ ] `scripts/test-all.sh` compares `git status --porcelain` before and after, names the changed paths and exits non-zero on a difference; skipped with a note outside a git checkout
- [ ] The summary line and exit code are unchanged for a clean run

### Phase 4: Integration test

- [ ] Re-run each changed script alone from the repository root: tree unchanged, counts and floors recorded here
- [ ] A failing run and an interrupted run of one changed script: tree unchanged
- [ ] Negative control: the runner's check flags `HEAD`'s copy of one changed script, run from a scratch checkout
- [ ] `bash scripts/test-all.sh` — the full runner — tree unchanged, summary recorded; no changed script newly fails
- [ ] Update `CLAUDE.md` where the runner's and the scripts' entries describe what they write

## Related

- [MUX-235](../completed/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) — the run during which these paths were flagged; its Phase 3 results record them
- [MUX-234](../completed/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md) — the earlier full runs that showed the same four paths
- [MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — hermetic scripts and the real tmux server; its ship commit carries the two modified files

## Status

**Backlog** — 0/21. Filed 2026-11-19 on the user's request, relayed by edit. Writers read, not yet
measured.
