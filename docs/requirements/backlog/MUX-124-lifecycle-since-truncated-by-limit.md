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

- [ ] `lifecycle show --since <d>` with no `--limit` returns **all** matching entries in the window
- [ ] `lifecycle show --since <d> --limit N` returns the last N, and when more matched prints
      `showing last N of M` on stderr
- [ ] Any truncated `lifecycle show` — unscoped, `--event`, `--source`, `--level` — prints the same
      stderr notice; stdout carries rows only
- [ ] An empty result means *"nothing matched"*, never *"matches existed but were cut"*
- [ ] The default limit of 50 still applies to unscoped `lifecycle show`
- [ ] **Negative control**: a genuinely empty window returns empty, and a result that fits the limit
      prints no notice
- [ ] `go vet ./...` and `go test ./...` green

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

- [ ] `FilterLifecycleLog` reports how many entries matched before the limit was applied; existing
      callers updated in the same change
- [ ] `--since` without an explicit `--limit` runs unlimited; with one, the limit holds
- [ ] `lifecycle show` prints `showing last N of M` on stderr whenever M > N
- [ ] Unit tests: a window holding more rows than 50 returns all; `--since` with `--limit 10` returns
      10 and reports the total; unscoped `show` over 60 rows returns 50 and reports 60
- [ ] **Negative control** unit tests: an empty window and a result within the limit report no
      truncation
- [ ] `docs/agent-bus.md`: the `--since` and notice behaviour

### Phase 2: Integration test

- [ ] Create `scripts/test-lifecycle-since.sh` — hermetic: muxcode built from the tree, a scratch
      lifecycle log with a known distribution across time (more than 50 rows inside the window, some
      outside), no daemon or tmux
- [ ] `--since` covering the in-window rows prints every one of them and nothing on stderr
- [ ] `--since … --limit 10` prints 10 rows and `showing last 10 of M` on stderr, M being the fixture's
      count
- [ ] Unscoped `show` prints 50 rows and the notice; stdout holds no notice text
- [ ] **Negative control**: a window with no rows prints nothing on either stream; a control build
      with the `--since` rule reverted returns 50 for the first check, so the assertion can fail
- [ ] Coverage floor; run through the run agent and record the counts here

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Unbounded output on a large log | `--since 30d` prints everything in the window | Accepted with decision (a): the user named the window; `--limit` still bounds it, with the notice |
| Changing default `show` behaviour | It is used interactively and 50 rows is a reasonable default | Explicit criterion: unscoped behaviour unchanged |
| Fixing only `--since` | `--event`/`--source`/`--level` have the same interaction | Decision (b): the notice covers every flag combination |
| A notice on stdout breaks a consumer | Scripts parse the rows | The notice goes to stderr only |

## Status

Backlog

Unparked 2026-11-24 on the user's decision (options a and b together), relayed by edit and recorded
above. Not started.
