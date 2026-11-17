# MUX-212: `test-stall-redrive-busy.sh` Fails — a Stale Spinner in Scrollback Pins the Pane Busy

**Tracking:** [mkober/muxcode#183](https://github.com/mkober/muxcode/issues/183)

`scripts/test-stall-redrive-busy.sh` ends `14 passed, 1 failed (floor 15)`. Its "stale spinner in
scrollback" check renders a pane whose history mentions a spinner but which is at rest, sends it a
row and runs `muxcode deliver review --force`; the row never reaches the pane. The script's own
comment states what that means if the product is at fault: the busy check is reading history rather
than the pane's live tail, and force recovery is refused for as long as the line stays in the
capture window. Whether the product or the fixture is wrong is not determined.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, from the MUX-208 graph worker's report
(`/tmp/mux208-implement-report.md`), while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script. Observed by plan in the run agent's
logs; not re-run by plan. Code read at `b8bfcab` plus the uncommitted MUX-208 tree.

### Observation — verified

| Fact | Where |
|------|-------|
| `FAIL: stale spinner history pinned the pane busy — force recovery refused forever`; every other check passes, including the busy-pane refusals and their controls | `/tmp/mux208-rerun-logs/test-stall-redrive-busy.log`, `/tmp/mux208-rerun2-test-stall-redrive-busy.log` |
| Failed in both runs on 2026-11-13 (15:38 and 15:53) — "reproduced twice" in the worker's report | same logs |
| The failing sequence: `render_stale_spinner review`, `sleep 1`, `muxcode send review stale-row "a row after a stale spinner" --no-notify`, `muxcode deliver review --force`, `sleep 1`, then grep the pane for the row's text | `scripts/test-stall-redrive-busy.sh:192-206` |
| The check runs directly after the "provider awareness" section, which ran `deliver review --force` under `MUXCODE_REVIEW_CLI=codex` | `scripts/test-stall-redrive-busy.sh:180-190` |

### Why this is not MUX-208's — verified

`bus/timetrack.go`, `bus/stuck.go` and `bus/deliver.go` are unmodified in the MUX-208 working tree
(`git status --short`), and the script's MUX-208 change is its bus-directory line. The worker's
report adds that `AgentIsWorking` (`timetrack.go`, a 12-line tail) is unchanged since 2026-10-10 —
not checked by plan. The script was not run against `main`'s own binary.

### Cause — not determined

(As filed. Since determined — fixture and order; see
[Results for Phase 1](#results-for-phase-1).)

Candidates, none verified:

- **Product:** `AgentIsWorking`'s tail window still contains the stale spinner line, so the pane
  reads as working and `ForceDeliver` skips it. The 12-line tail against the fixture's rendered
  height decides this.
- **Fixture:** `render_stale_spinner` no longer pushes the spinner line out of the live tail — the
  pane the script draws is not the pane its comment describes.
- **Order dependence:** the preceding section's forced delivery left the pane, a task or a marker in
  a state that withholds the next one; the check may pass in isolation.
- **Timing:** the one-second `sleep` after the deliver is shorter than the injection's own retries.

### Blast radius

- `bash scripts/test-all.sh` exits 1 on every run.
- If the product candidate holds, [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)'s
  busy guard over-refuses: an agent at rest with a spinner in recent scrollback cannot be recovered
  by `deliver --force`, the stall watchdog or receipt-gap recovery until the line scrolls away — and
  `--force` deliberately never overrides the busy check.

## Requirements

### Acceptance criteria

- [x] The cause is recorded here, with the captured pane and what `AgentIsWorking` returned for it
- [x] A pane at rest whose scrollback mentions a spinner is delivered to by `deliver --force`
- [x] A pane with a live spinner in its tail is still refused (the MUX-171 guarantee, unchanged)
- [x] The check passes when run in isolation and in sequence — or the order dependence is removed
- [x] `bash scripts/test-stall-redrive-busy.sh` exits 0, floor met

### Technical approach

Phase 1 is a capture: the pane as `AgentIsWorking` reads it, the tail it classifies, and the
`deliver --force` output the script currently discards (`>/dev/null 2>&1`). That output alone
distinguishes a busy skip from an injection that was attempted and lost.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-stall-redrive-busy.sh:192-206` | The failing check and `render_stale_spinner` |
| `tools/muxcode/bus/timetrack.go` | `AgentIsWorking` and its tail window |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver`'s busy skip |
| `tools/muxcode/bus/stuck.go` | `PaneShowsRecoverableIdle` |

## Implementation

### Phase 1: Establish the cause

- [x] Capture the fixture pane, the tail `AgentIsWorking` classifies, and the `deliver --force`
  output; record them here
- [x] Run the check in isolation (without the provider-awareness section before it); record the
  result
- [x] Run the script against a binary built from `main`; record pass/fail
- [x] Decide: product, fixture, order or timing; record it here

### Results for Phase 1

Recorded 2026-11-17 from the worker's findings (`/tmp/mux212-phase1.md`); plan read them whole and
confirmed the three runs in the run agent's replies. Boxes are ticked at `verify-spec`, not here.

**Decision: fixture and order. Not the product, and not timing.** The busy guard is right about what
is on the screen; the stale-spinner check's pane has been corrupted by the section before it.

**The mechanism, as captured:**

| Step | What happens |
|------|--------------|
| 1 | The "provider awareness" section runs `MUXCODE_REVIEW_CLI=codex muxcode deliver review --force`. The Codex scrape road types the whole request, with its role preamble and reply instructions, into the review pane — which in this script is a bash shell — and presses Enter |
| 2 | The preamble holds an apostrophe ("may hold another role's instructions"), which opens a single quote bash never sees closed. The shell now sits at its continuation prompt, `>` |
| 3 | `render_stale_spinner`'s keystrokes arrive as continuation input, so **the frame is never drawn**. The pane's tail is the typed command line itself — `> PS1="❯ "; clear; printf "✻ Cogitating… (2m 14s · esc to interrupt)\n⏺ Read(a)…` — and then `>` |
| 4 | `AgentIsWorking` reads a 10-line live tail (`paneLiveTail` over a 12-line capture). That tail holds the typed line, and `isClaudeThinking` matches the spinner text in it — correctly, for what is on screen |
| 5 | `deliver review --force` prints `deliver: nothing delivered to review (agent is mid-turn — nothing is typed into a running tool call)` and exits 0. The script throws that output away. The lifecycle log shows `injection-refused` ("pane ends at a shell prompt: >"), `enter-withheld`, `force-deliver review: 1 messages`, `deliver-skipped-busy`; both the Codex row and the stale-spinner row are still pending in review's inbox |

**The runs.**

| Run | Result | Run task |
|-----|--------|----------|
| The script as it is, installed binary | Exit 1 — 14 passed, 1 failed (floor 15); `FAIL: stale spinner history pinned the pane busy — force recovery refused forever` | `1794952894-spawn-56d56314-c77724d6` |
| Instrumented, in sequence | The same check fails (7 passed, 1 failed in that part) | `1794952964-spawn-56d56314-fc96748a` |
| Instrumented, **the provider-awareness section removed** | The fixture draws as its comment says — the live tail is `⏺ Read(b)` … `✻ Cooked for 2m 14s`, then `❯`, the spinner outside the tail. `deliver --force` reports `woke review with 1 pending message(s)` and the check passes (6 passed, 0 failed) | the same task |
| A binary built from `main` (`e040a83`) | The same `FAIL`, 14 to 1, exit 1 | `1794953029-spawn-56d56314-203f8bb0` |

**Against this spec's four candidates:**

| Candidate | Finding |
|-----------|---------|
| Product — the busy read sees history | No. The isolated run delivers; the guard read the live tail correctly both times |
| Fixture | Yes — the pane the check draws on is not in the state it assumes |
| Order dependence | Yes — the preceding section leaves the shell at a continuation prompt. The check passes in isolation |
| Timing | No |

**One correction to the findings.** They call the typed text the "MUX-232 payload". The apostrophe
is in the Codex scrape-road role preamble (`bus/provider_codex.go:1143`), in the tree since
`01096b9` (2026-10-06) — plan looked it up. It is not from today's MUX-232 change, which fits the
script having failed on 2026-11-13 already.

**What this means for the criteria.** The second — a pane at rest whose scrollback mentions a
spinner is delivered to by `deliver --force` — already holds in the product: the isolated run shows
it. The third, MUX-171's refusal of a live spinner, is untouched. So Phase 2 takes its fixture
branch: give the check a pane in a known state, and stop discarding the `deliver` output. The
product branch — a change to `paneLiveTail` — does not arise, and **the busy guard is not
loosened**, so nothing here needs the user's approval.

**Noted on the way, not this spec's.** The script's fixture panes are real shells behind a `❯`
prompt, so any delivered payload is executed as shell input, and one holding a quote can wedge the
pane. The script's header already warns of it. A real agent pane is a TUI, not a shell, so this is a
hazard of the fixture, not of delivery.

### Phase 2: Fix

- [x] Product: bound the busy read to the live tail, with a Go test over a pane whose history holds
  a spinner and whose tail does not, and a sibling whose tail does — **not applicable**: Phase 1
  found the product already reads only the live tail; no product code changed
- [x] Fixture, order or timing: correct the script, and stop discarding the `deliver` output so the
  next failure names its cause

### Results for Phase 2

Verified and ticked 2026-11-17. Plan read the diff; the only file changed outside `docs/` is
`scripts/test-stall-redrive-busy.sh` (last edited 17:09:41).

| Change | What it does |
|--------|--------------|
| `render_stale_spinner` runs `tmux respawn-pane -k` before drawing | The check gets a fresh shell, so a pane left at a continuation prompt by the provider-awareness section can no longer swallow the frame. This removes the order dependence |
| The `deliver review --force` output is captured rather than discarded | On failure the message carries that output, and the pane's last 12 non-blank lines are printed — the next failure names its cause |

**The run, through the run agent** (task `1794953387-spawn-56d56314-894294ef`, reply at 17:10:48):
exit 0 — 15 passed, 0 failed, floor 15. It includes "an idle pane with stale spinner history is
still delivered to", in sequence after the provider-awareness section, and the busy-pane refusals.

Evidence after the last edit: `./build.sh` exit 0 at 17:11:11, `./test.sh` exit 0 at 17:11:30,
review at 17:11:57 with 0 must-fix, 0 should-fix, 0 nits.

**Criteria ticked on this:** 2 (the check now passes in sequence, as it did in isolation in Phase
1), 3 (the busy-pane refusals pass and no product code changed), 4 (order dependence removed) and 5
(exit 0, floor met). Phase 3 repeats the run, adds the negative control and the `test-all.sh` check.

### Phase 3: Integration test

- [x] `bash scripts/test-stall-redrive-busy.sh` through the run agent: every check passes, floor
  met, exit 0; record the counts here
- [x] Negative control: a live spinner in the tail is still refused
- [x] `bash scripts/test-all.sh` no longer lists `test-stall-redrive-busy` as failed

### Results for Phase 3

Verified and ticked 2026-11-17 from the worker's report in the `verify-spec` dispatch; plan read the
run agent's replies. The phase changed no code — the script was last edited at 17:09:41, in Phase 2.

| Run | Result | Run task |
|-----|--------|----------|
| `bash scripts/test-stall-redrive-busy.sh` | Exit 0 — 15 passed, 0 failed, floor 15 | `1794953387-spawn-56d56314-894294ef` |
| `bash scripts/test-all.sh stall-redrive-busy` | Exit 0 — `test-stall-redrive-busy PASS`; passed 1, failed 0, skipped 0; the script's own log again 15 of 15. **The filtered runner, one script** — not a full `test-all.sh` | `1794953564-spawn-56d56314-afad954c` |

**The negative control is inside those runs**, not a separate one: the script's own checks that a
working pane — a live spinner in its tail — is refused. They passed in both: "deliver --force
refused the working pane", "pending row withheld from the working pane", "no task-stall-redrive for
the working pane" and "working pane untouched across two check intervals", each beside a control
showing the resting pane is still delivered to.

Evidence after the last edit: `./build.sh` exit 0 at 17:14:16, `./test.sh` exit 0 at 17:14:36,
review at 17:15:09 with 0 must-fix, 0 should-fix, 0 nits.

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — the busy guard
  this script pins
- [MUX-123](../backlog/MUX-123-stall-watchdog-selective-misses.md) — the stall watchdog's misses

### Findings at activation (2026-11-17)

Activated against the tree at `e040a83`. Plan did not re-run the script; whether it still fails, and
in the same way, is Phase 1's first question.

| Spec says | Tree today |
|-----------|------------|
| `AgentIsWorking` in `bus/timetrack.go`, unchanged since 2026-10-10 per the worker's report | At `timetrack.go:237`; the file's newest commit is `0909bba` (2026-09-11), so unchanged for longer than reported |
| `bus/deliver.go` unmodified | **Changed on the day of activation** by `ba749b0` ([MUX-232](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md)): the re-drive types the whole request, and `ForceDeliver` goes through `ForceDeliverFor`. The script's failing `deliver review --force` may now print something different |
| The script | Its newest commit is `f0e5f14` (MUX-208), the bus-directory line only |

**No user decision is known to be waiting — and that is weaker than it sounds.** This spec lists
four candidate causes that plan wrote without having run anything. Its two siblings from the same
close-out went differently: [MUX-209](../completed/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md)'s
cause was among its candidates; MUX-210's was none of them, and its criteria turned out to
contradict a design decision, so it was parked after Phase 1. If Phase 1 here finds the script's
premise — or the second criterion — at odds with how the busy guard is meant to behave, that is to
be recorded and raised, not resolved by the worker. In particular `--force` deliberately never
overrides the busy check ([MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md)):
any fix that loosens that guard is the user's to approve.

## Status

**Complete** — closed 2026-11-17, uncommitted at close-out (the run's ship step commits it). The
failure was the script's, not the product's: an earlier section left the check's shell pane at a
continuation prompt, so the stale-spinner frame was never drawn and the busy guard correctly read
the typed line as a working pane. The check now respawns its pane first and keeps the `deliver`
output for the next failure. The script passes 15 of 15; no product code changed and MUX-171's busy
guard is untouched. Activated 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 34; every better-ranked row is parked). 14/14 — Phases 1–3 and all five criteria ticked 2026-11-17, one step as not applicable; awaiting close-out.

Filed 2026-11-13 on the user's instruction relayed by edit, with the cause undetermined. Phase 1
determined it on 2026-11-17: the fixture and the order of the script's sections, not the product.
Verified that day with no code changed: `./build.sh` exit 0 at 17:06:29, `./test.sh` exit 0 at
17:08:09, review at 17:08:46 with 0 must-fix, 0 should-fix and 1 nit — this paragraph still called
the cause undetermined; corrected here.
