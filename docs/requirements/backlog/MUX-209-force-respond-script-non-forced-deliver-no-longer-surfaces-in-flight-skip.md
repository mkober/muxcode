# MUX-209: `test-force-respond.sh` Fails — a Non-Forced `deliver` No Longer Surfaces the In-Flight Skip

**Tracking:** [mkober/muxcode#180](https://github.com/mkober/muxcode/issues/180)

`scripts/test-force-respond.sh` ends `14 passed, 1 failed`. Its last assertion sends a tracked
request to an OpenCode-configured role, ages the in-flight task six seconds, runs a non-forced
`muxcode deliver test` and expects the output to name the skip (`skip` or `in-flight`). The command
instead attempts the injection and reports it woke the role. Either `deliver` stopped surfacing a
skip the script was written to pin, or the script's premise went stale when the delivery rules
changed — which of the two is this spec's first question.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script as a failure it did not cause.
Observed by plan in the run agent's logs; nothing here was re-run by plan. Code read at `b8bfcab`
plus the uncommitted MUX-208 tree.

### Observation — verified

| Fact | Where |
|------|-------|
| The script failed in two runs on 2026-11-13: the 15:11 `bash scripts/test-all.sh` and the 15:38 rerun | proc logs `1794600689-proc-f90797b3` and `1794602297-proc-b8c4f9fe`; `/tmp/mux208-rerun-logs/test-force-respond.log` |
| Every other check passes, including `deliver --force bypasses the in-flight skip` and `no skip message under force` on the `run` role | same log: `14 passed, 1 failed` |
| The failing assertion: `MUXCODE_TEST_CLI=opencode`, `muxcode send test skipcheck "skip surfacing payload" --no-notify --track`, `sleep 6`, `muxcode deliver test`, then `grep -qE "skip\|in-flight"` on the output | `scripts/test-force-respond.sh:191-206` |
| Actual output: `[wakeup] test injection not confirmed — text still parked after 3 retries; leaving inbox for next cycle` then `deliver: woke test with 1 pending message(s)` | same log |
| Binary under test: `v0.1.39-2-gb8bfcab-dirty`, installed 15:10 from the MUX-208 tree | `muxcode version` |

### Why this is not MUX-208's — verified

| Fact | Where |
|------|-------|
| `tools/muxcode/bus/deliver.go`, `bus/task.go` and `cmd/deliver.go` are unmodified in the MUX-208 working tree | `git status --short` |
| The script's only MUX-208 change is how it computes its scratch bus directory (`muxcode_bus_dir`, replacing the `/tmp/muxcode-bus-$SESSION` literal) | `git diff -- scripts/test-force-respond.sh` |

The script was **not** run against `main`'s own binary, so "fails the same way before MUX-208" is
inferred from the unchanged code under test, not observed.

### Cause — not determined

Candidates, none verified:

- The delivery rules changed after the script was written. `deliver.go` and `task.go` were last
  touched by `8f0284e` (PR 151 review: owed-reply redelivery, scrape-road identity), `50c5047`
  (MUX-154) and `fd03d39` (MUX-171); [MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md)
  changed how an in-flight task gates a wake to a Codex or OpenCode agent. A non-forced deliver over
  an unnotified inbox row may now be delivered by design.
- The six-second `sleep` no longer ages the task into the state the skip applies to.
- A regression: the skip is still intended and `ForceDeliver` lost it.

### Blast radius

- `bash scripts/test-all.sh` exits 1 on every run, so any spec with an "all integration scripts
  pass" criterion must carve this script out by hand, as MUX-208 did.
- If the third candidate holds, a non-forced `deliver` types a payload over an in-flight task with no
  message saying so — the "silent success" the assertion's own comment names.

## Requirements

### Acceptance criteria

- [ ] The cause is recorded here: stale script premise, or a `deliver` regression — with the commit
  that introduced the change
- [ ] If the script is stale: the assertion pins the behaviour `deliver` has today, with a negative
  control that fails when that behaviour is removed
- [ ] If `deliver` regressed: a non-forced deliver against an in-flight task surfaces the skip again,
  pinned by a Go test
- [ ] `bash scripts/test-force-respond.sh` exits 0 with every check passing and none removed without
  a replacement
- [ ] `docs/agent-bus.md`'s `muxcode deliver` entry matches the behaviour the script pins

### Technical approach

Phase 1 is a read, not a fix: run the script against a binary built from `main` to confirm the
failure predates MUX-208, then bisect the three commits above with the single failing sequence. The
fix follows whichever candidate the bisect confirms.

### Key files

| File | Purpose |
|------|---------|
| `scripts/test-force-respond.sh:191-206` | The failing sequence |
| `tools/muxcode/bus/deliver.go` | `ForceDeliver`, the skip and re-drive branches |
| `tools/muxcode/bus/task.go` | In-flight task state and its guard window |
| `tools/muxcode/cmd/deliver.go` | The messages `deliver` prints |

## Implementation

### Phase 1: Establish the cause

- [ ] Run `bash scripts/test-force-respond.sh` against a binary built from `main`; record pass/fail
  and the output here
- [ ] Bisect `8f0284e`, `50c5047`, `fd03d39` and the MUX-192 change with the failing sequence;
  record the introducing commit
- [ ] Decide: stale premise or regression; record it here

### Phase 2: Fix

- [ ] Stale premise: rewrite the assertion to pin today's behaviour, with a negative control
- [ ] Regression: restore the surfaced skip in `ForceDeliver`, with a Go test that fails without it
- [ ] Update `docs/agent-bus.md` if the documented behaviour differs

### Phase 3: Integration test

- [ ] `bash scripts/test-force-respond.sh` through the run agent: every check passes, exit 0; record
  the counts here
- [ ] The script carries a coverage floor so a skipped section cannot report green
- [ ] `bash scripts/test-all.sh` no longer lists `test-force-respond` as failed

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-105](../completed/MUX-105-force-respond-escalation.md) — the spec the script belongs to
- [MUX-192](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md),
  [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — changes to the
  delivery rules since

## Status

Backlog

Filed 2026-11-13 on the user's instruction relayed by edit. The failure is observed in two runs; the
cause is **not determined**, and that it predates MUX-208 is inferred, not observed. Not started.
