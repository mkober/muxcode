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
| Which script writes each path | **Measured 2026-11-24** — Phase 1 confirmed the reading below for the three scripts the runner runs; `test-browser-monitor-e2e` stays read only |

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

- [x] The writer of each of the four paths is established by a run, not by reading: for each, one script named, and the tree shown clean without it and dirty with it
- [x] After `bash scripts/test-all.sh`, `git status --porcelain` is identical to what it was before the run — no new untracked file, no modified tracked file
- [x] The same holds for each changed script run alone as `bash scripts/<name>.sh` from the repository root, on success and on a failing or interrupted run of a sample
- [x] A stand-in pane that receives typed payloads cannot write into the checkout: its working directory is under the script's work dir
- [x] Each changed script still passes with its coverage floor met; one failing for another reason keeps the same counts, check for check
- [x] The full runner itself detects a dirtied tree: a run that changes `git status --porcelain` names the paths and exits non-zero
- [x] Negative control: that check is shown to fail against an unmodified script that dirties the tree

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

- [x] From a clean tree, run each suspected script alone and record `git status --porcelain` before and after; record the result per script here
- [x] Run the full runner with a per-script status snapshot and record every script that changes the tree — including any the reading did not name
- [x] Record the list of scripts in scope for Phase 2

#### Phase 1 results (2026-11-24)

Measured by the run's implement worker with a one-off script, `/tmp/mux236-measure.sh`, at `HEAD`
`d030125`. It runs scripts one at a time from the repository root, snapshots
`git status --porcelain --untracked-files=all` with a content hash per entry before and after each,
and then restores only what that script changed, so each script starts from the same tree. The
baseline held plan's four spec edits under `docs/requirements/` and nothing else. The reports are
under `/tmp/mux236-measure-suspects/` and `/tmp/mux236-measure-all/` (volatile; what they carry is
recorded here).

**Suspects alone** (10:26):

| Script | Exit | Time | Tree after |
|--------|------|------|------------|
| `test-pane-targeting` | 0 | 36s | `?? probe]`, `?? probe2]` — both empty |
| `test-plan-diagram-render` | 0 | 5s | ` M docs/media/sample-aws.svg` |
| `test-playwright-check` | 0 | 24s | ` M test/fixtures/vite-react-app/vite-test.log` |

**Every script** (10:28–11:20):

| Result | Count | Scripts |
|--------|-------|---------|
| Dirty | 3 | The three above, the same paths |
| Clean | 80 | Every other script run |
| Skipped, live-session | 9 | `test-browser-monitor-e2e`, `test-context-pressure`, `test-diff-split`, `test-hot-reload`, `test-modal-size`, `test-multi-reload`, `test-notify-flood`, `test-resize-hook`, `test-stuck-agent` |

The 92 are every `scripts/test-*.sh` but the runner. The final tree equalled the baseline. No
writer the reading missed was found. Each of the four paths appeared in its own script's window
and in no other's, each window starting from a restored tree — which is criterion 1.

Qualifications:

- The pass is a loop over the runner's list with the runner's skip set, not `scripts/test-all.sh`
  itself. A write the runner makes on its own account would not show; none was looked for.
- `test-browser-monitor-e2e` was not run. Its `vite-test.log` line (101) stands on reading alone.
- A status snapshot does not see ignored files. A script that writes only ignored paths reads as
  clean.
- Two scripts exited 1 while clean, `test-graph-tui` and `test-prompt-mode` — the failures parked as
  MUX-210 and MUX-122. Several others finish in 0–1 s (`test-test-sh-coverage`,
  `test-watch-chain-gate`, `test-watchdog-churn`, `test-xcode-compat` among them). A clean result
  from a script that failed or skipped early says less than one from a script that ran through.
  None of them is a suspected writer.
- `sample-aws.svg` hashed differently in the two passes (`cd3cb670c6bc`, then `a4d675a476d7`): the
  renderer's output is not stable from run to run, so every run modifies the file.
- The suspects pass left `sample-aws.svg` modified — the script's protected set wrongly covered
  `docs/media/` — and a follow-up restored it from `HEAD` before the full pass.

**In scope for Phase 2:**

| Script | Path | Standing |
|--------|------|----------|
| `test-pane-targeting` | `probe]`, `probe2]` | Measured |
| `test-plan-diagram-render` | `docs/media/sample-aws.svg` | Measured |
| `test-playwright-check` | `test/fixtures/vite-react-app/vite-test.log` | Measured |
| `test-browser-monitor-e2e` | The same log, line 101 | Read, not run |

Phase 2's fourth step, any further writer, has nothing to fix.

### Phase 2: Fix the writers

- [x] `test-pane-targeting`: stand-in panes start in the work directory
- [x] `test-plan-diagram-render`: no write under `docs/media/`; the default-path assertion kept in a form that does not need one
- [x] `test-playwright-check` and `test-browser-monitor-e2e`: vite log under a work directory
- [x] Any further writer Phase 1 found — none was found, so nothing to fix

#### Phase 2 results (2026-11-24)

Four scripts changed, uncommitted.

| Script | Change |
|--------|--------|
| `test-pane-targeting` | Every `new-session`, `new-window` and `split-window` — stand-ins, intruder panes, the graph UI splits, the legacy session — takes `-c "$WORK"`. No pane-creating call is left without it. The payload is still executed by a shell; its redirect now lands in the work dir |
| `test-plan-diagram-render` | Runs a copy of `render-diagram.sh` at `$WORK/repo/scripts/`, with the fixtures copied to `$WORK/repo/docs/media/`. The renderer resolves its default output directory from its own location, so the default-path assertion is kept — against the scratch tree's `docs/media/` — and the real one is only read. The missing-`mmdc` case uses a copy too. `track_output` and its cleanup are gone; the work dir is removed on exit |
| `test-playwright-check` | Vite log in a `mktemp` directory, removed by the existing cleanup |
| `test-browser-monitor-e2e` | The same change |

Re-measured with the Phase 1 script's suspects pass at 11:26:55, after the last script edit
(11:26):

| Script | Exit | Tree | Count |
|--------|------|------|-------|
| `test-pane-targeting` | 0 | Clean | 22 passed, 0 failed |
| `test-plan-diagram-render` | 0 | Clean | 14 passed, 0 failed, 0 skipped |
| `test-playwright-check` | 0 | Clean | 22 passed, 0 failed |

The final tree equalled the baseline. The worker reports the counts unchanged from the Phase 1
logs; plan did not compare them, and the Phase 1 logs have since been overwritten by this pass.

Qualifications:

- `test-browser-monitor-e2e` was syntax-checked only. It is a live-session script and has not run
  with its change.
- These are passing runs. A failing or interrupted run (criterion 3) is Phase 4's.
- In the two vite scripts the log directory is created before the cleanup trap is armed, so an exit
  between the two leaves an empty directory under `$TMPDIR` — outside the checkout, and not this
  spec's subject.

### Phase 3: The runner's check

- [x] `scripts/test-all.sh` compares `git status --porcelain` before and after, names the changed paths and exits non-zero on a difference; skipped with a note outside a git checkout
- [x] The summary line and exit code are unchanged for a clean run

#### Phase 3 results (2026-11-24)

`scripts/test-all.sh` now snapshots the tree before and after **each** script (`tree_state`: one
line per `git status --porcelain=v1 -z --untracked-files=all` entry, with a hash of the file's
content, so rewriting an already-dirty file counts). A script that changes it gets
`changed the checkout:` and its paths under its own line; after the summary a closing
`CHANGED THE CHECKOUT: <scripts>` line names every offender and the run exits 1, even when every
script passed. Outside a git checkout it prints `tree check: skipped (not a git checkout)` and
checks nothing. The `CLAUDE.md` runner row describes this.

Checked by the worker's one-off `/tmp/mux236-runner-check.sh`, in scratch repositories with stub
scripts, run at 11:34 after the runner's last edit (11:33): **13 passed, 0 failed**
(`/tmp/mux236-runner-check2.log`, volatile). The worker's report cites the earlier 9-check run;
review then added four cases and the runner was changed to read `-z` records.

| Case | Result |
|------|--------|
| Clean run | Exit 0; output byte-identical to `HEAD`'s runner |
| Stub that adds `probe]` and appends to a tracked file | Exit 1; both paths and the script named; summary line still `passed: 1  failed: 0` |
| Rewrite of an already-dirty file | Exit 1, path named |
| Dirty baseline left alone — a non-ASCII name, a tab in a name, a staged rename | Exit 0, nothing reported |
| Rewrite of each of those three | Exit 1, path named (the rename by its destination) |
| `HEAD`'s runner on the dirtying stub | Exit 0 — the gap |
| Outside a git checkout | Exit 0, the skip note printed |

Qualifications:

- The dirtying script here is a stub. Criterion 7 — the check failing against an **unmodified real
  script** — is Phase 4's third step and stays open.
- The check attributes any change during a script's window to that script. A file another agent or
  an editor writes in the checkout while the runner is going — plan recording into this spec, for
  one — is reported against whichever script was running, and fails the run.
- Ignored files are not seen, as in Phase 1.
- The check script is a one-off under `/tmp`, not a `scripts/test-*.sh`.

### Phase 4: Integration test

- [x] Re-run each changed script the runner runs (`test-pane-targeting`, `test-plan-diagram-render`, `test-playwright-check`) alone from the repository root: tree unchanged, counts and floors recorded here
- [x] Run `test-browser-monitor-e2e` alone from the repository root with its change, in a live session: tree unchanged, count recorded here
- [x] A failing run and an interrupted run of one changed script: tree unchanged
- [x] Negative control: the runner's check flags `HEAD`'s copy of one changed script, run from a scratch checkout
- [x] `bash scripts/test-all.sh` — the full runner — tree unchanged, summary recorded; no changed script newly fails
- [x] Update `CLAUDE.md` where the runner's and the scripts' entries describe what they write

#### Phase 4 results (2026-11-24)

Run by the run agent for the implement worker; plan read the logs and the tree at `verify-spec`
(13:07, and 13:11 for the fourth script). The first step above was one box, "each changed script",
and was split at the first verification: three of the four changed scripts had run, and the fourth
became its own step, run on the next dispatch.

Every script's last edit (11:25–11:33) precedes these runs. Each case compares
`git status --porcelain=v1 -z --untracked-files=all`, with a content hash per entry, before and
after. The baseline held 10 dirty entries — this spec's uncommitted work.

**Steps 1–3** — the worker's one-off `/tmp/mux236-phase4.sh`, 12:10–12:13: **22 passed, 0 failed**
(`/tmp/mux236-phase4.log`, volatile).

| Script alone | Exit | Count | Floor | Tree |
|--------------|------|-------|-------|------|
| `test-pane-targeting` | 0 | 22 passed, 0 failed | 22, met exactly | Unchanged |
| `test-plan-diagram-render` | 0 | 14 passed, 0 failed, 0 skipped | None in the script | Unchanged |
| `test-playwright-check` | 0 | 22 passed, 0 failed | None in the script | Unchanged |
| `test-browser-monitor-e2e` | 0 | 13 passed, 0 failed, 13 total | None in the script | Unchanged |

`test-browser-monitor-e2e` ran separately at 13:09, in the live session `muxcode`, through the
worker's one-off `/tmp/mux236-bme2e.sh` (`/tmp/mux236-bme2e.log`, volatile): 10 s, its dev server
started on port 5199 — the changed line — and the tree compared unchanged, hash for hash. The
fixture's tracked `vite-test.log` was last written at 11:04, before the run, and no
`browser-monitor-e2e.*` directory is left under `$TMPDIR`. The run sent the live watch agent one
`browser-check` request, as the script is written to; watch answered that it could not run it
(no `playwright` module where it looked), which the script does not assert on.

| Case | Result | Tree |
|------|--------|------|
| Failing run: `test-plan-diagram-render`, both renderers stubbed to exit 1 | Exit 1; 9 passed, 5 failed | Unchanged |
| SIGINT to `test-pane-targeting` after 15 s | Exit 130, mid-run | Unchanged |
| SIGINT to `test-playwright-check` after 10 s | Exit 130, dev server stopped by its trap | Unchanged |
| SIGINT to `test-plan-diagram-render` after 2 s | Exit 130, during the live renders | Unchanged |
| Negative control: the current runner on `HEAD`'s (`d030125`) `test-pane-targeting`, in a scratch clone | Exit 1; the script itself passed (`passed: 1  failed: 0`); `changed the checkout:` lists `probe]` and `probe2]`; `CHANGED THE CHECKOUT: test-pane-targeting` | The clone's, dirtied as expected |
| Control: the same clone with the fixed script | Exit 0, nothing flagged | — |

**Step 4** — `bash scripts/test-all.sh`, 12:14–13:05 (`/tmp/mux236-testall.log`, volatile):
`passed: 81  failed: 2  skipped: 9`, exit 1. No `changed the checkout` block and no
`CHANGED THE CHECKOUT` line; the tree at 13:07 is the same 10 entries, none of the four paths among
them. The two failures are unchanged scripts that also exited 1 in Phase 1: `test-graph-tui`
(44 passed, 2 failed — parked as MUX-210) and `test-prompt-mode` (17 passed, 7 failed, 3 skipped —
parked as MUX-122; 5 or 4 failed on 2026-11-18, cause of the drift not looked at). The three
changed scripts the runner runs all passed in it.

**Step 5** — the `scripts/test-all.sh` row has described the tree check since Phase 3; the
`scripts/test-*.sh` row gained one sentence: nothing a script runs writes into the checkout — panes
start with `-c "$WORK"`, the renderer runs from a copy of the tree, a dev server's log goes to a
temp dir. It was written at 12:13:55, before the runner's log began (12:14:02), so the runner's
check did not see it as a change.

Qualifications:

- `test-browser-monitor-e2e` ran once, passing. It has no failing or interrupted run; criterion 3
  asks that of a sample, which the other three scripts supply. The full runner still skips it.
- Criterion 5's second clause, a script failing for another reason keeping its counts, had no
  case: no changed script failed.
- The failing run is forced by stubbed renderers, and the interrupts are SIGINT only; no SIGTERM
  or SIGKILL case.
- The full run exits 1 on the two parked failures, so "the runner exits 0 on a clean tree" is shown
  by the scratch-clone control, not by this run.
- The check script is a one-off under `/tmp`, not a `scripts/test-*.sh`.

## Related

- [MUX-235](../completed/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) — the run during which these paths were flagged; its Phase 3 results record them
- [MUX-234](../completed/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md) — the earlier full runs that showed the same four paths
- [MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — hermetic scripts and the real tmux server; its ship commit carries the two modified files

## Status

**Complete** — 22/22, closed out 2026-11-24. Filed 2026-11-19 on the user's request, relayed by edit. Activated
2026-11-24 by `55-defect-backlog-loop` run 1795531653 (user-launched), after MUX-237 merged as
PR #250. Phases 1–3 complete: three writers measured and fixed, re-measured clean; the runner's
per-script tree check in place. Phase 4 complete: the four changed scripts alone
(`test-browser-monitor-e2e` in the live session), a failing and three interrupted runs and the full
runner all left the tree unchanged, and the runner's check flagged `HEAD`'s script in a scratch
clone. Nothing is open.

What shipped: `test-pane-targeting` starts every pane in its work dir; `test-plan-diagram-render`
renders from a copy of the tree; `test-playwright-check` and `test-browser-monitor-e2e` write the
dev server's log to a temp dir; `scripts/test-all.sh` compares the tree around each script, names
what changed and exits 1. `CLAUDE.md` describes both.

Left for the user, outside this spec: `a58a187` committed test output into
`docs/media/sample-aws.svg` and `test/fixtures/vite-react-app/vite-test.log`, and reverting either
is a separate change. The full runner still exits 1 on the two parked failures (MUX-210, MUX-122).
The runner's check has no `scripts/test-*.sh` of its own — it was shown by one-off scripts under
`/tmp`.

Findings at activation: MUX-235 (`55d4ce8`) edited `test-pane-targeting.sh` after this spec was
filed — it now runs on a private tmux server — and the mechanism read above still stands there:
the sessions are created with no `-c` (lines 97 and 174) and the `PS1` lines are still at 101 and
176. The `vite-test.log` lines are still at `test-playwright-check.sh:94` and
`test-browser-monitor-e2e.sh:101`. `test-plan-diagram-render.sh` was not re-read. The tree was
clean on `main` at activation. No decision is owed before Phase 1.
