# MUX-234: Three Integration Scripts Fail — Their Fixtures Send to Roles the Consumer Guard Refuses

**Tracking:** [mkober/muxcode#242](https://github.com/mkober/muxcode/issues/242)

Three `scripts/test-*.sh` fail on every run: `test-status-line-task-close` (14 passed, 5 failed),
`test-plan-verify-verdict` (32 passed, 4 failed) and `test-codex-idle-delivery` (29 passed, 7
failed). For the first two the cause is in their own logs:
[MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md)'s consumer guard refuses a
send to a role the scratch session has no window, listener or headless agent for. The third fails
at a delivery step and its cause is not determined. `bash scripts/test-all.sh` exits 1 on every run
because of them.

## Context

### Source and standard of evidence

Found 2026-11-17 by the worker on
[MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md), which ran
twenty-two scripts under a measuring wrapper and re-ran the failures standalone with the same
counts (run task `1794960246-spawn-56d56314-976f2eb6`). Filed by plan on the user's standing
instruction to file defects found along the way. Plan read the three re-run logs
(`/tmp/mux233-rerun-test-*.log`) and did not run the scripts.

The binary was the installed `v0.1.76-1-gb0d3b15-dirty`, built from the loop's working tree, so a
regression from uncommitted work is not excluded — Phase 1 re-runs against `main`.

### Observed — verified in the logs

| Script | Result | Failing checks | In the log |
|--------|--------|----------------|------------|
| `test-status-line-task-close` | 14 / 5, floor 19 | The three "control: … task completed" checks; "genuine review reply fired verify-spec at plan"; "plan-verify row written" | `[task-detect] failed to send response for … build→edit:response: edit has no window, inbox listener or headless agent: no consumer for the role's inbox` — and the same for test and review |
| `test-plan-verify-verdict` | 32 / 4 | Section E: "passing review never fired"; no verify-spec in plan's inbox; it does not name the active spec; no plan-verify row | `[daemon] failed to send plan verification: daemon→plan:verify-spec: plan has no window, inbox listener or headless agent: no consumer for the role's inbox` |
| `test-codex-idle-delivery` | 29 / 7, floor 36 | Section 1, "stale answered task, unforced wake": the pending request is not typed into the idle pane and does not leave the inbox; no `[wakeup] skipping` line; no `wake-skipped` row; the task carries no answer and stays in flight; no `task-answered-elsewhere` row | No refusal text — the script discards the output that would show one |

Earlier results, from the worker's report: `test-status-line-task-close` 19 / 0 and
`test-plan-verify-verdict` 36 / 0 on 2026-11-13; no earlier log for `test-codex-idle-delivery`.
MUX-145 merged on 2026-11-16 (`f87cb3d`).

### Cause

**For the first two — the consumer guard, verified by the refusal in the log.** MUX-145 made
`bus.Send` refuse a message to a role nothing can read for. It added
`export MUXCODE_CONSUMER_GUARD=0` to twenty-eight scripts whose fixtures play an agent by reading
its inbox from the script
([MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) lists them). These two
are not among the twenty-eight, and neither sets the variable — plan checked with `grep`. Their
daemons send to `edit` and `plan`, which their scratch sessions have no window for.

Why MUX-145's own run did not catch them is not known: its close-out records which scripts were
run, and plan has not re-read it.

**For `test-codex-idle-delivery` — not determined.** It also does not set the variable, so the same
guard is the first candidate. But its failing step is a wake typed into a pane, not a send into an
inbox, and nothing in its log names a refusal. Other candidates: the wake gate
([MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md), which this script
was written for), and the re-drive changes of
[MUX-232](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md) and
[MUX-220](../completed/MUX-220-wake-batch-drops-a-roles-own-delegation-reply.md), both merged on
2026-11-17. If Phase 1 finds a product fault rather than a stale fixture, that is a different
defect and is to be raised, not fixed under this spec's name.

**One of its checks is also unstable.** Across four runs on 2026-11-17 the script gave 29 / 7
three times and 28 / 8 once (MUX-233's full re-measure, 19:17–19:33; not reproduced). Which check
flipped was not recorded. Phase 1 should find it: a script that fails the same way every time and
one with a flaky check are different problems.

### Two more failing scripts, cause not looked at (added 2026-11-18)

MUX-233's full runner (`bash scripts/test-all.sh`, task `1794965635-spawn-56d56314-bcb67e77`,
2026-11-17) showed two further failures, neither among the scripts MUX-233 changed:

| Script | Result | Standing |
|--------|--------|----------|
| `test-fix-loop-cap` | 26 / 5 | Passed on 2026-11-13. The same counts on a clean export of `HEAD` (`eaf0e69`), so it is on `main` and not from MUX-233's change |
| `test-restart-definition` | 28 / 2 | The same |

Both lines are from the MUX-233 worker's findings as plan recorded them in that spec on the 17th.
The findings file and the run logs were under `/tmp` and did not survive the machine's restart on
2026-11-18, so the failing checks are not named here and plan has read no log for either. Neither
sets `MUXCODE_CONSUMER_GUARD` (plan checked with `grep`, 2026-11-18), and both last passed before
the consumer guard merged — which makes the guard a candidate, nothing more. They are recorded here so the runner's failures have one home;
if Phase 1 finds a different cause, they are split out.

MUX-233 said these were added here on the 17th. They were not; its review caught that.

### Consequence

- The full integration runner cannot go green, so a new failure is easy to miss among three known
  ones.
- Two of the three are the scripts that pin the daemon's `verify-spec` gate (MUX-172) and the
  status-line task-close rule (MUX-154). While they fail at a fixture step, the checks after it do
  not run as written.

### Not this spec

`test-prompt-mode` also fails (17 / 7, 3 skipped) and did so before this loop; it is the subject of
parked [MUX-122](../backlog/MUX-122-prompt-agent-turn-attribution-and-fix.md).

## Requirements

### Acceptance criteria

- [x] The cause of each of the three failures is recorded here with the evidence — for `test-codex-idle-delivery`, determined, not assumed from the other two
- [x] `bash scripts/test-status-line-task-close.sh` exits 0, floor met
- [x] `bash scripts/test-plan-verify-verdict.sh` exits 0, floor met
- [x] `bash scripts/test-codex-idle-delivery.sh` exits 0, floor met — or its failure is shown to be a product fault and filed separately, and this criterion is ticked as handed off with a link
- [x] `test-fix-loop-cap` and `test-restart-definition`: the failing checks and the cause of each are recorded here, and each either exits 0 with its floor met or is filed separately with a link (added 2026-11-18)
- [x] No assertion is weakened or removed to get there; a check whose text changes is listed here with the reason
- [x] Negative control: with the fix reverted in a scratch copy, each script fails as it does today

### Technical approach

Give the fixture a real consumer where that is cheap — a window for the role in the scratch
session, which is what `RoleHasConsumer` looks for, and the direction MUX-230 sets. Where the
script means to read an inbox without a consumer, set `MUXCODE_CONSUMER_GUARD=0` with the same
comment the other twenty-eight carry, and add the script to MUX-230's list so that spec removes it
with the rest. The user's recorded decision in MUX-230 is that the scripts keep the switch for now,
so either road is within it. Stop discarding the `deliver` output in `test-codex-idle-delivery`, as
MUX-212 did for its sibling, so the next failure names its cause.

These three are among the scripts MUX-233 is moving onto a private tmux server. Whichever lands
second rebases onto the first; the two changes do not depend on each other.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-status-line-task-close.sh` | Sends to edit and plan from a scratch daemon |
| `scripts/test-plan-verify-verdict.sh` | Section E, the passing review that should fire `verify-spec` |
| `scripts/test-codex-idle-delivery.sh` | Section 1, the unforced wake |
| `tools/muxcode/bus/consumer.go` | `RoleHasConsumer`, `refuseWithoutConsumer` — read only |
| `docs/requirements/backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md` | The list a script joins if it takes the opt-out |

## Implementation

### Phase 1: Establish the causes

- [x] Run the three scripts against a binary built from `main`; record the counts, so a regression from uncommitted work is ruled in or out
- [x] For `test-codex-idle-delivery`, capture the `deliver` and wake output the script discards and the lifecycle rows of the failing section; record the cause
- [x] Run `test-fix-loop-cap` and `test-restart-definition` the same way; record the counts, the failing checks and what each log says (added 2026-11-18)
- [x] Record why MUX-145's own verification did not show the first two failing, if its close-out says

### Results for Phase 1

Recorded 2026-11-18 from the worker's findings (`/tmp/mux234-phase1-findings.md`), read whole by
plan. The evidence is kept outside `/tmp`, in `~/.local/state/muxcode/evidence/mux234-phase1/` —
every log, the kept `fix-loop-cap` work and bus directories, `restart-definition`'s lifecycle log
and the two runner scripts. Plan read the logs there for each count and each refusal quoted below.
Run tasks `1795026486-spawn-154628e4-5fe29676` and `1795027194-spawn-154628e4-7d82aade`. Boxes are
ticked at `verify-spec`, not here.

**The binary.** Built from the tree with `go build` into the evidence directory and put first on
`PATH`: `muxcode a58a187-dirty`. The worker reports `-dirty` is untracked files only, no tracked
source differing from `a58a187`; plan did not check that. `test-restart-definition` builds its own
from the same tree.

**All five fail on `main` with the counts of 2026-11-17**, so a regression from uncommitted work is
ruled out.

| Script | Result | Cause | In the log |
|--------|--------|-------|------------|
| `test-status-line-task-close` | 14 / 5, floor 19 | Consumer guard — edit has no consumer | `[send] REFUSED review→edit:response — edit has no consumer`, then `[task-detect] failed to send response …` for review, build and test |
| `test-plan-verify-verdict` | 32 / 4 | Consumer guard — plan has no consumer | `[send] REFUSED daemon→plan:verify-spec — plan has no consumer` |
| `test-codex-idle-delivery` | 29 / 7, floor 36 — three runs, the same seven checks each time | **Consumer guard — determined.** See below | `send-no-consumer … edit … (first: review→edit:review-complete)` |
| `test-restart-definition` | 28 / 2, floor 30 not met | Consumer guard — edit has no consumer | `[send] REFUSED daemon→edit:agent-down — edit has no consumer`; the `agent-definitionless` alert is raised and never reaches edit, so both checks on edit's inbox fail |
| `test-fix-loop-cap` | 26 / 5, twice | **Not the consumer guard — the inbox cap** ([MUX-180](../completed/MUX-180-daemon-notifies-windowless-roles-forever.md)). See below | `inbox-full … edit: 50 unconsumed messages, the cap of 50`; `inbox-full-edit.marker` in the kept bus directory |

**`test-codex-idle-delivery`, determined from an instrumented copy.** Its session has two windows,
`review` and `build`. Section 1's fixture answers a chain request with
`review→edit:review-complete`, and that send is refused: edit has no window. With no reply
recorded, the chain task is a fresh, unanswered in-flight task one second old — and the wake gate
holds the wake behind it, as designed: `[wakeup] skipping review injection — in-flight task …
exists (1s old)`, a `wake-skipped` row, nothing typed, the request still in review's inbox. All
seven failures follow from the one refused reply. **Not a product fault**: the product refused a
send to a role nothing reads for, and held a wake behind an unanswered task. The 28 / 8 seen once
on the 17th did not recur in three runs and is not explained.

**`test-fix-loop-cap`.** The script starts no tmux. Its `answer()` sends every stub agent's reply
to edit (`scripts/test-fix-loop-cap.sh:123`, read by plan), and nothing in the script reads edit's
inbox. Its five-phase run leaves edit holding 50 unconsumed messages. At the negative control's
first build the stub reads and receipts the dispatch, but its reply is refused at the cap,
`answer()` returns 1, and the script prints "negative control: build 1 never dispatched". **That
message is wrong** — the dispatch arrived; the reply did not leave. The 30-against-29 floor
mismatch is a by-product of the early exit. MUX-180 closed on 2026-11-16; the script last passed
on the 13th.

**The failing checks of the two added scripts**, from `fix-loop-cap.log` and
`restart-definition.log` in the evidence directory, read by plan:

| Script | Failing checks |
|--------|----------------|
| `test-fix-loop-cap` | "negative control: build 1 never dispatched"; "negative-control run state: running"; "no graph-loop-exhausted row naming fix->build"; "no graph-run-failed row for" the run; "coverage floor mismatch — 30 checks executed, want exactly 29". The middle three follow from the first: the negative control's run never advances past the build whose reply was refused |
| `test-restart-definition` | "edit alerted with an agent-definitionless event (timeout 10s)"; "run: edit not alerted with the refusal"; and the floor line, 28 against 30 |

**Why MUX-145's own verification did not show these.** Its close-out records unit tests, one run
task and its own `scripts/test-windowless-routing.sh`. No `bash scripts/test-all.sh` run is
recorded, nor how the twenty-eight opt-out scripts were chosen. Nothing in it would have run these
five. The same is likely true of MUX-180 and `test-fix-loop-cap`; plan has not re-read that
close-out.

**Decision, plan's, 2026-11-18: `test-fix-loop-cap` stays in this spec.** The text above said a
script with a different cause would be split out. Its cause is different in name — the cap, not
the consumer guard — and the same in kind: a fixture overtaken by a send refusal merged on
2026-11-16, with a fix in the script and none in the product. Splitting it would file a
one-step spec. If the user would rather it were separate, the criterion still allows that.

Phase 2 gains a step for each of the two added scripts; it had none. The spec is 0/20.

**Verified and ticked 2026-11-18, 13:50.** The phase changed no script — the tree holds no change
outside `docs/`. `./build.sh` exit 0 and `./test.sh` passed at 13:46; the review at 13:47 returned
0 must-fix, 0 should-fix, 0 nits. The four steps are ticked, and the first criterion: the cause of
each of the original three is recorded with its evidence, `test-codex-idle-delivery`'s determined.
The criterion added for the two further scripts stays open — their causes are recorded, but it
also asks that each exits 0 or is filed separately, which is Phase 2's. The spec is 5/20.

### Phase 2: Fix the fixtures

- [x] `test-status-line-task-close` and `test-plan-verify-verdict`: a consumer for the roles their daemons send to, or the opt-out with the script added to MUX-230's list — record which and why
- [x] `test-codex-idle-delivery`: the fix Phase 1's cause calls for, and its discarded output kept
- [x] `test-restart-definition`: a consumer for edit, or the opt-out with the script added to MUX-230's list — record which and why (added 2026-11-18 after Phase 1)
- [x] `test-fix-loop-cap`: the stub replies no longer pile up in an inbox nothing reads — drain edit, or the fix the script's own design calls for — with no cap raised or bypassed; and a refused reply is reported as a refused reply, not as "never dispatched" (added 2026-11-18 after Phase 1)
- [x] If any of the three turns out to be a product fault: stop, record it, and report it for filing — not triggered: no product fault in any of the five

### Results for Phase 2

Recorded 2026-11-18 from the worker's findings
(`~/.local/state/muxcode/evidence/mux234-phase2/findings.md`, read whole by plan). Plan read the
diff of all five scripts, the run's log and the script that produced it. Boxes are ticked at
`verify-spec`, not here.

**The run** (task `1795027827-spawn-154628e4-a2715d30`, exit 0): each script invoked as
`bash scripts/<name>.sh` from the repository root, on the tree build of `a58a187`, with
`MUXCODE_CONSUMER_GUARD` unset in the caller so that each script's own line is what is tested.

| Script | Before | After |
|--------|--------|-------|
| `test-status-line-task-close` | 14 / 5 | 19 / 0, floor 19 |
| `test-plan-verify-verdict` | 32 / 4 | 36 / 0, floor met |
| `test-codex-idle-delivery` | 29 / 7 | 36 / 0, floor 36 |
| `test-restart-definition` | 28 / 2 | 30 / 0 |
| `test-fix-loop-cap` | 26 / 5 | 30 / 0, floor met |

**Four scripts take the opt-out, and why not a window.** `test-status-line-task-close`,
`test-plan-verify-verdict`, `test-codex-idle-delivery` and `test-restart-definition` each gain one
line after `set`, worded as the others carry it: `export MUXCODE_CONSUMER_GUARD=0`, with the
MUX-145 comment. The worker's reason against a window for edit or plan: its pane would be a bare
shell, so every daemon wake into it is refused by the injection guard and logged
`injection-refused` — new rows in scripts whose checks count lifecycle rows
(`test-codex-idle-delivery` counts `wake-skipped` rows exactly). These roles are played by the
script reading their inboxes, the case the opt-out exists for, and MUX-230's recorded decision
keeps it for now. Plan has not tested the window road; the reason is the worker's.

**Added to MUX-230's list** — and the list was stale. The worker asked for twenty-eight to become
thirty-two. A search of the tree finds **thirty-eight** scripts with the line: these four, the
twenty-eight, and six new scripts written on 2026-11-16 and 17 that copied it and were on no list.
[MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) now names all
ten additions.

**What this spec does not do, said plainly:** four more scripts now run with the consumer guard
off for their whole run. That is the cost MUX-230 describes, taken knowingly; the cause is fixed
for the runner and not for the guard's coverage.

**`test-codex-idle-delivery` — discarded output kept.** A `fixture_send` helper replaces five
fixture sends that went to `>/dev/null 2>&1`; a refused one now prints `[fixture] send refused:
…`. The refusal of Section 1's reply was the whole cause and was hidden there. `show_on_fail`
prints the captured wake and deliver output of sections 1 to 3 when one of their checks failed.
Neither adds a check; the floor stays 36.

**`test-fix-loop-cap` — no cap raised or bypassed.**

- `answer()` reads edit's inbox after each reply. Edit plays no part in the script; the worker
  reports the daemon correlates replies through the bus log and `MarkResponded` at send time,
  never through edit's inbox, so draining it changes nothing the graph reads. Plan did not trace
  that in the code; the script passing 30 of 30 with its negative control is consistent with it.
- A refused reply is reported as one. `answer()` returns 1 with the role, the request id and the
  send's output; `wait_and_answer` sets the reason to "never dispatched" or "dispatched, but
  <refusal>", and every call site prints it. The two `answer` calls that were unchecked
  (implement, review) now report a refusal too. Failure path only — a passing run executes 29
  checks, as before.

**Criterion 5.** No assertion is weakened or removed, and no check's condition changed — plan read
the diff for this: no `check` line in `test-codex-idle-delivery` is among the changed lines, and
`test-fix-loop-cap`'s changes are to failure messages and to what `answer`
returns. **The one text change:** `test-fix-loop-cap`'s failure messages read "never dispatched"
for a refused reply and now say which it was.

**No product fault was found** in any of the five, so the phase's last step did not trigger.

**Not done here:** the negative control (criterion 6), the full runner and `CLAUDE.md` — Phase 3.

#### Review fix and verification (2026-11-18, 14:05)

The review at 13:58 returned 1 should-fix: `test-fix-loop-cap.sh:273`, the close-out step, still
printed "close-spec never dispatched" when the request had arrived and its reply was refused. It
now prints the same reason the other call sites do, with the run's state. A failure message only.

**Ticked on the tree after that fix, script by script against its last edit:**

| Script | Last edited | Passing run after it |
|--------|-------------|----------------------|
| `test-status-line-task-close` | 13:49:32 | 13:51:16 — 19 / 0 |
| `test-plan-verify-verdict` | 13:49:34 | 13:51:25 — 36 / 0 |
| `test-codex-idle-delivery` | 13:50:17 | 13:52:37 — 36 / 0 |
| `test-restart-definition` | 13:49:37 | 13:53:36 — 30 / 0 |
| `test-fix-loop-cap` | 13:58:50 | 14:01:38 — 30 / 0, floor met (`fix-loop-cap.fix.log`) |

After the last edit: `./build.sh` exit 0 and `./test.sh` passed at 14:02; review at 14:02 with
0 must-fix, 0 should-fix, 0 nits.

Phase 2's five steps are ticked — the last, the stop on a product fault, as not triggered: none
was found. Five criteria are ticked: the three scripts exit 0 with floors met; the two added
scripts have their causes recorded and exit 0; and no assertion is weakened, with the one kind of
text change listed above (failure messages in `test-fix-loop-cap`, line 273 included). The spec is
15/20.

**Open at that point:** Phase 3's four steps and the negative-control criterion — both since
closed, see Results for Phase 3.

### Phase 3: Integration test

- [x] Run the three scripts through the run agent: every check passes, floors met, exit 0; record the counts here
- [x] Negative control: each script, with its fix reverted in a scratch copy, fails as recorded above
- [x] `bash scripts/test-all.sh` — the full runner — and record its summary; the three no longer appear as failed
- [x] Update each script's entry in `CLAUDE.md` if its floor or setup changed — none needed, see the results

### Results for Phase 3 (2026-11-18)

One wrapper, `mux234-phase3.sh`, run by the run agent (task `1795030808-spawn-c5d6ba86-4470422e`,
reply `1795034557-run-52ed236f`, exit 0) from 14:40 to 15:42. It covers all five scripts, not the
three the steps name. It built `muxcode` from the tree (`a58a187-dirty`) and put it first on
`PATH`, with `MUXCODE_CONSUMER_GUARD` unset in its own environment. The five scripts were last
edited between 13:49 and 13:58, before the run. Evidence:
`~/.local/state/muxcode/evidence/mux234-phase3/` — `mux234-phase3.log`, `fixed/`, `negative/`,
`test-all.log`. The `fixed-*.log` and `head-*.log` files at that directory's top level are from an
earlier attempt (14:04–14:15) that ended when the session was relaunched at 14:37; they show the
same counts and are not what was ticked on.

| Script | Fixed, on the tree | HEAD copy (fix reverted) | Phase 1 count |
|--------|--------------------|--------------------------|---------------|
| `test-status-line-task-close` | 19 / 0, floor 19, exit 0 | 14 / 5, exit 1 | 14 / 5 |
| `test-plan-verify-verdict` | 36 / 0, floor 35 met, exit 0 | 32 / 4, exit 1 | 32 / 4 |
| `test-codex-idle-delivery` | 36 / 0, floor 36, exit 0 | 29 / 7, exit 1 | 29 / 7 |
| `test-restart-definition` | 30 / 0, exit 0 | 28 / 2, floor not met, exit 1 | 28 / 2 |
| `test-fix-loop-cap` | 30 / 0, floor 29 met, exit 0 | 26 / 5, exit 1 | 26 / 5 |

**Negative control.** The copy is `git archive HEAD` — `a58a187`, where the five scripts are the
unfixed ones, since nothing of this spec is committed. The wrapper compares each copy with the
tree's script and would have warned on an identical pair; it warned on none. Each fails on the
checks Phase 1 recorded, run against the same tree-built binary, so the difference is the fixture.

**Full runner.** `bash scripts/test-all.sh`: passed 80, failed 2, skipped 9 (live-session), exit 1.
All five of this spec's scripts pass in it. The two failures are not this spec's and no file of
theirs was touched:

| Script | Failing checks | Where it belongs |
|--------|----------------|------------------|
| `test-graph-tui` | 44 / 2 — "narrow pane renders the flat fallback", "failed node lists before done node (failed=9 done=8)" | The two checks [MUX-210](../backlog/MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md) is filed on; parked on the user's answer |
| `test-prompt-mode` | 4 — two render-once checks ("input line names its destination", "unreachable state renders with its reason") and two live-surface checks, after `respawn pane failed: fork failed: Device not configured` | The script [MUX-122](../backlog/MUX-122-prompt-agent-turn-attribution-and-fix.md)'s park note calls environment-dependent. Cause not looked at here; whether the two render-once failures are environment too is not established |

Their logs are kept beside the rest (`test-graph-tui.log`, `test-prompt-mode.log`).

**`CLAUDE.md`.** No change. No floor moved: `test-plan-verify-verdict` is documented at floor 35
and still runs 35 floor checks; `test-status-line-task-close` and `test-restart-definition` carry
no floor there; `test-codex-idle-delivery` and `test-fix-loop-cap` have no entry. The setup change
— four scripts exporting `MUXCODE_CONSUMER_GUARD=0` — is not documented per script for any of the
other thirty-four that do the same; MUX-230 carries the list.

**Seen during the full run, not caused by this spec's scripts as far as is known.** After it,
`git status` shows four paths this spec did not touch, all stamped 15:25–15:26, inside the
runner's window: `docs/media/sample-aws.svg` and `test/fixtures/vite-react-app/vite-test.log`
modified (one line each), and two empty untracked files at the repository root, `probe]` and
`probe2]` — the same two names seen on 2026-11-17 at 21:08. Which script writes them was not
traced. They are in the working tree a ship step would stage.

Phase 3's four steps and the negative-control criterion are ticked. The spec is 20/20.

## Related

- [MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md) — the consumer guard
- [MUX-230](../backlog/MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) — the twenty-eight scripts that opt out, and the plan to give them real consumers
- [MUX-233](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) — where these failures were found
- [MUX-209](../completed/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md), [MUX-212](../completed/MUX-212-stall-redrive-busy-script-stale-spinner-in-scrollback-pins-pane-busy.md) — two earlier scripts that failed on a stale fixture, not the product

### Findings at activation (2026-11-18)

Activated against the tree at `a58a187`, the merge of MUX-233 (PR #244). Nothing was run.

- **MUX-233 landed first.** `test-status-line-task-close`, `test-plan-verify-verdict` and
  `test-codex-idle-delivery` now start their sessions on a private tmux server
  (`private_tmux_init`, `scripts/lib/private-tmux.sh`). A fixture change here goes inside that
  setup: a window added for a role is a window on the private server, and the helper's rule —
  init before any trap — is not to be disturbed. `test-fix-loop-cap` and `test-restart-definition`
  were not among MUX-233's twenty-two; the first contains no `tmux` call at all (plan searched it) — so no role in it can have a window,
  and "give the fixture a window" is not open to it — and the second
  already ran a private server and cleans up with the aliased `tmux kill-server`
  ([MUX-235](../backlog/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md), not this
  spec).
- **None of the five sets `MUXCODE_CONSUMER_GUARD`** — plan checked with `grep` today.
- **The logs this spec was written from are gone.** `/tmp/mux233-rerun-test-*.log` did not survive
  the machine's restart on 2026-11-18. The refusals quoted above were read from them on the 17th;
  Phase 1 re-runs all five, so the evidence is made again rather than taken on trust.
- **The spec covers five scripts; its title, filename and issue #242 say three.** The two added
  ones have no cause looked at. If Phase 1 finds either fails for a reason unrelated to its
  fixture, the criterion lets it be filed separately — that is to be said and recorded, not
  folded in.
- **The binary.** The spec's note that the failures were seen on a dirty build no longer applies
  as written: the tree is clean at `a58a187`, and Phase 1's "built from `main`" is the installed
  binary once `./build.sh` has run on it.

**No user decision is known to be waiting.** The choice between a real consumer and the opt-out is
within MUX-230's recorded decision either way.

## Status

**Complete** — closed out 2026-11-18. Activated the same day by `55-defect-backlog-loop` run
`1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 35; every better-ranked row was parked).
20/20 — Phases 1–3 complete and all seven criteria met, verified 2026-11-18. Filed 2026-11-17. All five scripts pass, and each fails as before with its fix reverted. Phase 1 found: four of the five scripts
fail on the consumer guard and `test-fix-loop-cap` on the inbox cap, all five fixture faults. The
title and GitHub issue #242 still say three.
