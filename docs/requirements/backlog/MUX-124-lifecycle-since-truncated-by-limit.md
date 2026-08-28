# `lifecycle show --since` Silently Answers the Wrong Question

`muxcode lifecycle show --since 8h` returns **zero** `task-stall` rows on a day that recorded
**26** of them. The rows are in the log; the filter matches them; they are then thrown away by the
default `--limit`.

This is not a cosmetic reporting bug. **It produced a materially false premise for
[MUX-123](./MUX-123-stall-watchdog-selective-misses.md)** — a High-priority spec was very nearly
filed to root-cause an "inert watchdog" that had in fact fired 26 times that day. The investigator
used the tool built for the job and got a confidently wrong answer.

Tracking: _(no GitHub issue yet)_

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

## Requirements

### Acceptance criteria

- [ ] `lifecycle show --since <d>` returns **all** matching entries in the window, or states plainly
      that output was truncated and by how much
- [ ] An empty result means *"nothing matched"*, never *"matches existed but were cut"*
- [ ] The default limit still applies to unscoped `lifecycle show` — this is not a request to remove
      it
- [ ] `--since 8h` on a fixture with more than the default limit of in-window rows returns them all
      (or reports the truncation)
- [ ] **Negative control**: a genuinely empty window still returns empty, with no truncation notice
- [ ] `go vet ./...` and `go test ./...` green

### Technical approach

Options, in rough order of preference:

1. **`--since` implies unlimited** unless `--limit` is given explicitly — the user named a window;
   honour it.
2. **Emit a truncation notice** whenever `len(filtered) > Limit` — cheap, and fixes the honesty
   problem for every flag combination rather than just `--since`.
3. Apply the limit **before** other filters — rejected: it would make `--event`/`--source` equally
   misleading.

Option 2 alone would have prevented the MUX-123 false premise, because the reader would have seen
that the answer was incomplete.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/lifecycle.go` | `FilterLifecycleLog` — filter-then-truncate |
| `tools/muxcode/cmd/lifecycle.go` | Flag parsing, default limit, `--all` |

## Implementation

### Phase 1: Fix the interaction

- [ ] Implement the chosen option
- [ ] Ensure unscoped `lifecycle show` behaviour is unchanged
- [ ] Unit test: window with more in-window rows than the limit returns all, or reports truncation

### Phase 2: Regression test

- [ ] Fixture log with a known distribution across time
- [ ] Test: `--since` covering more rows than the default limit returns the full set
- [ ] Test: truncation, where it still occurs, is **visible** in the output
- [ ] **Negative control**: an empty window returns empty with no truncation notice
- [ ] **Negative control**: unscoped `show` still honours the default limit
- [ ] Run and verify

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Unbounded output on a large log | `--since 30d` could dump everything | Truncation notice rather than silent removal |
| Changing default `show` behaviour | It is used interactively and 50 rows is a reasonable default | Explicit criterion: unscoped behaviour unchanged |
| Fixing only `--since` | `--event`/`--source`/`--level` have the same interaction | Prefer the general truncation notice |

## Status

Backlog
