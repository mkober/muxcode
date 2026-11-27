# `lifecycle show --since` Silently Answers the Wrong Question

**Tracking:** [mkober/muxcode#111](https://github.com/mkober/muxcode/issues/111)

`muxcode lifecycle show --since 8h` returns **zero** `task-stall` rows on a day that recorded
**26** of them. The rows are in the log; the filter matches them; they are then thrown away by the
default `--limit`.

This is not a cosmetic reporting bug. **It produced a materially false premise for
[MUX-123](../completed/MUX-123-stall-watchdog-selective-misses.md)** — a High-priority spec was very nearly
filed to root-cause an "inert watchdog" that had in fact fired 26 times that day. The investigator
used the tool built for the job and got a confidently wrong answer.

## Context

### The mechanism

`FilterLifecycleLog` (`bus/lifecycle.go`) applies filters, then truncates:

```go
if opts.Since > 0 && e.TS < opts.Since {
    continue
}
filtered = append(filtered, e)
...
// Apply limit (take last N entries)
if opts.Limit > 0 && len(filtered) > opts.Limit {
    filtered = filtered[len(filtered)-opts.Limit:]
}
```

`Since` is correct. The truncation is correct in isolation. Together, with the **default limit of
50**, a time-window query returns *"the most recent 50 events"* and presents it as *"everything since
8 h ago"*.

Confirmed empirically — same query, only the limit differs:

| Command | `task-stall` rows |
|---------|-------------------|
| `lifecycle show --since 8h` | **0** |
| `lifecycle show --since 8h --limit 2000` | **26** |

### Why it misleads rather than merely truncating

A limit is honest when the user asked for *N most recent*. It is dishonest when the user asked a
**time-scoped question**: the answer looks complete for the requested window, and an empty result
reads as *"this never happened"* rather than *"your window was silently narrowed"*.

The failure is worst exactly where the tool is most needed — a busy session generates enough rows
that 50 covers only the last few minutes, so the more there is to investigate, the less of it is
visible.

## Decisions (user, 2026-11-24)

Relayed by edit. Both of the first two options, together:

| # | Decision |
|---|----------|
| a | `--since` implies no limit unless `--limit` is given explicitly — the user named a window, so it is honoured |
| b | Whenever output is truncated, by any flag combination, a notice `showing last N of M` goes to **stderr** — stdout stays the rows alone, so a pipe or a script reading the rows is unaffected |

Option 3 (limit before the other filters) stays rejected. The unscoped default of 50 is unchanged.

## Requirements

### Acceptance criteria

- [x] `lifecycle show --since <d>` with no `--limit` returns **all** matching entries in the window
      — script section 1: `--since 8h` prints all 70 in-window rows, none from outside, nothing on
      stderr
- [x] `lifecycle show --since <d> --limit N` returns the last N, and when more matched prints
      `showing last N of M` on stderr — script section 2: 10 rows ending at the newest,
      `showing last 10 of 70`
- [x] Any truncated `lifecycle show` — unscoped, `--event`, `--source`, `--level` — prints the same
      stderr notice; stdout carries rows only — script section 3: unscoped (`showing last 50 of 90`),
      `--source launcher --limit 5` (`5 of 20`), `--event task-stall` at the default limit
      (`50 of 70`) and `--level warn --limit 7` (`7 of 70`), each asserting the row count, the exact
      stderr notice and no notice text on stdout. Left open at the Phase 2 verify, when only unscoped
      and `--source` were exercised; the run's criteria worker added the other two
- [x] An empty result means *"nothing matched"*, never *"matches existed but were cut"* — every cut
      prints the notice (sections 2, 3), an empty window prints nothing on either stream and exits 0
      (section 4)
- [x] The default limit of 50 still applies to unscoped `lifecycle show` — script section 3
- [x] **Negative control**: a genuinely empty window returns empty, and a result that fits the limit
      prints no notice — script section 4
- [x] `go vet ./...` and `go test ./...` green — the run's test node (`./test.sh`, both modules
      passed) after the last code change, 2026-11-27

### Technical approach

`cmd/lifecycle.go` starts from `Limit: 50` and already zeroes it for `--all` (`opts.Limit = 0`).
Track whether `--limit` was given; when `--since` is set and it was not, zero the limit the same way.

For the notice the command needs the pre-truncation count, which `FilterLifecycleLog`
(`bus/lifecycle.go`) discards at its `filtered[len(filtered)-opts.Limit:]` cut. Return the matched
total beside the entries (a second return value or a result struct) and let the command print the
notice — the library function stays free of I/O.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/lifecycle.go` | `FilterLifecycleLog` — filter-then-truncate; reports the matched total |
| `tools/muxcode/cmd/lifecycle.go` | Flag parsing, default limit, `--all`; prints the notice |
| `docs/agent-bus.md` | `muxcode lifecycle show` reference |
| `scripts/test-lifecycle-since.sh` | New |

## Implementation

### Phase 1: Fix the interaction

- [x] `FilterLifecycleLog` reports how many entries matched before the limit was applied; existing
      callers updated in the same change — as built: a new `FilterLifecycleLogCounted`
      (`bus/lifecycle.go`) returns the matched total, and `FilterLifecycleLog` wraps it, so its
      existing callers needed no change
- [x] `--since` without an explicit `--limit` runs unlimited; with one, the limit holds —
      `resolveShowLimit` (`cmd/lifecycle.go`), `TestResolveShowLimit`
- [x] `lifecycle show` prints `showing last N of M` on stderr whenever M > N — `truncationNotice`,
      `TestTruncationNotice`
- [x] Unit tests: a window holding more rows than 50 returns all; `--since` with `--limit 10` returns
      10 and reports the total; unscoped `show` over 60 rows returns 50 and reports 60 —
      `TestFilterLifecycleLogCountedReportsMatchedBeforeLimit`: 60 of 60, 10 of 60, and 50 of 65 (the
      fixture holds 65 rows unscoped)
- [x] **Negative control** unit tests: an empty window and a result within the limit report no
      truncation — `TestFilterLifecycleLogCountedNoTruncationControls`, and the `(5, 5)` and `(0, 0)`
      cases of `TestTruncationNotice`
- [x] `docs/agent-bus.md`: the `--since` and notice behaviour — the "How many rows `show` prints"
      block under `muxcode lifecycle`

Phase 1 verified 2026-11-27 against the uncommitted tree: the run's build, test (`./test.sh`, both
modules passed) and review nodes all succeeded. Also changed, beyond the steps: an unfiltered `show`
on an empty log now prints its "No lifecycle events" line on stderr, not stdout, and a filtered
query that matches nothing prints nothing.

### Phase 2: Integration test

- [x] Create `scripts/test-lifecycle-since.sh` — hermetic: muxcode built from the tree, a scratch
      lifecycle log with a known distribution across time (more than 50 rows inside the window, some
      outside), no daemon or tmux — a 90-row fixture: 70 inside an 8 h window, 20 outside; scratch
      HOME, config and `MUXCODE_LIFECYCLE_LOG_DIR`
- [x] `--since` covering the in-window rows prints every one of them and nothing on stderr — 70 rows
- [x] `--since … --limit 10` prints 10 rows and `showing last 10 of M` on stderr, M being the fixture's
      count — `showing last 10 of 70`
- [x] Unscoped `show` prints 50 rows and the notice; stdout holds no notice text —
      `showing last 50 of 90`
- [x] **Negative control**: a window with no rows prints nothing on either stream; a control build
      with the `--since` rule reverted returns 50 for the first check, so the assertion can fail —
      the control, built from a copy with `case since > 0:` removed, prints 50 and
      `showing last 50 of 70`
- [x] Coverage floor; run through the run agent and record the counts here — **27 passed, 0 failed,
      floor 26** (run agent, 2026-11-27 15:46, exit 0; the script unchanged since). Every query's
      exit status is asserted too, so a crash cannot pass as an empty result. An earlier run of the
      first version read 20 passed against a floor of 19. **Final: 36 passed, 0 failed, floor 35**
      (run agent, 2026-11-27 15:49, exit 0), after the `--event` and `--level` checks were added

Phase 2 verified 2026-11-27 from the script as it stands and the run agent's log of its last run.

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Unbounded output on a large log | `--since 30d` prints everything in the window | Accepted with decision (a): the user named the window; `--limit` still bounds it, with the notice |
| Changing default `show` behaviour | It is used interactively and 50 rows is a reasonable default | Explicit criterion: unscoped behaviour unchanged |
| Fixing only `--since` | `--event`/`--source`/`--level` have the same interaction | Decision (b): the notice covers every flag combination |
| A notice on stdout breaks a consumer | Scripts parse the rows | The notice goes to stderr only |

## Status

**Complete** — closed out 2026-11-27 at 19/19 by the `55-defect-backlog-loop` run that activated it
that day (rank 2; rank 1, MUX-215, is parked): Phases 1 and 2 and every acceptance criterion
verified, the integration script 36/36 against a floor of 35. Unparked 2026-11-24 on the user's
decision (options a and b together), relayed by edit and recorded above.
