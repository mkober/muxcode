# `muxcode send` Drops a Message Because Another Agent's Task Is In Flight

At 15:39 on 2026-09-08 plan sent `muxcode send edit notify "MUX-153 updated: …"`. The CLI answered
*"In-flight task for edit:notify already exists (sent 100s ago) — already tracking"* and returned.
Nothing was written — not to edit's inbox, not to the log, not to the task store. The in-flight task
it had matched was **watch's**: `1788895052-watch-a18e3100  watch→edit  notify`, sent 100 seconds
earlier. Plan's message was dropped because a different agent had an unanswered message to the same
recipient under the same action name.

The dedup guard exists so that one sender retrying its own request after a killed `--wait` does not
inject a duplicate. It keys on `(to, action)` and never looks at the sender, so every agent's
unanswered `notify` to edit silences every other agent's `notify` to edit — and `notify` is the
action every role uses for reports. The sender is told its message is tracked. It is not.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08, session `muxcode`; every fact below read by plan directly)

| | |
|---|---|
| Dropped | `plan → edit notify` at 15:39:12 |
| CLI output | `In-flight task for edit:notify already exists (sent 100s ago) — already tracking` |
| Matched task | `1788895052-watch-a18e3100  watch→edit  notify  [in-flight]` (`muxcode tasks`), sent 15:37:32 — 100s earlier, exactly the reported age |
| Written anywhere? | No — `grep` of `inbox/edit.jsonl` and `log.jsonl` for the payload: 0 rows; no task or delivery file for it |
| Recovered by | Re-sending under a made-up action name (`spec-update`), which the guard does not match — the workaround agents now rely on, so the fix must not break it |
| Same afternoon, edit's side | Edit's MUX-145 follow-up — a *different* message under its own in-flight `update-docs` to plan — was refused as a duplicate until `--force`. That is the same-sender case the guard intends, blocking a message that was not a retry (reported by edit, 15:22) |

An earlier drop the same afternoon (14:29) was the *same-sender* case — two plan notifies seconds
apart — which is the behaviour the guard intends. This one is not.

### Mechanism — verified

| Fact | How established |
|------|-----------------|
| The pre-send dedup runs for `request` sends with tracking on and no `--force`, and returns before any write | `cmd/send.go:184-189` — `if existing, found := bus.FindInFlightTask(session, to, action); found { … return }` |
| `FindInFlightTask` matches `t.To == to && t.Action == action` and nothing else | `bus/dedup.go:235-249` |
| `HasInFlightTaskForRole` has the same key | `bus/dedup.go:217` |
| The guard's stated purpose is a **same-sender** retry: "where `--wait` was killed by Bash tool timeout and the agent retries the same request" | `cmd/send.go` comment above the check |
| Tracking is on by default for requests (`Tracking task …` is printed on every plain send), so the guard applies to ordinary sends, not only `--track` | observed on every request send this session |

The sibling failure on the reply road: `bus.Send` returns `nil` after *"suppressing duplicate reply to
already-completed task"* (`bus/inbox.go:228`, deliberately before any write), and `cmd/send.go` then
prints `Sent response:<action> to <role>`. Twice today that line sent plan looking for a delivery that
never happened. Both roads share the defect: **the CLI reports success or tracking for a message it
did not send.**

### Consequences

| | |
|---|---|
| Silent message loss | Keyed on *another agent's* traffic — the sender can do nothing to predict or prevent it |
| The sender is misled | "already tracking" means someone else's task is tracked; no reply will ever wake this sender |
| The recipient is misled by omission | Edit believed plan had not moved on MUX-145 for eight minutes and sent a follow-up; plan had sent two reports |
| Collision rate is highest where it matters | `notify` is the universal report action, and edit is the universal recipient — watch, run, review and plan all notify edit |
| Workaround corrodes the bus | Senders learn to invent action names to get through, which defeats dedup entirely and makes action names meaningless |

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-010`](./MUX-010-delegation-message-hygiene.md) | The same guard's *expiry* half — a stuck in-flight task once blocked every `(to,action)` send to a role forever, fixed by `TaskExpired`. This is the *key* half: the guard also blocks across senders |
| [`MUX-154`](./MUX-154-codex-status-line-closes-tracked-tasks.md) | Same family of "the bus says one thing and did another" — that one records a success that did not happen; this one reports tracking for a message that does not exist |
| [`MUX-111`](./MUX-111-harness-reply-miscorrelation.md) | Adjacent: harness replies correlate to the wrong request. Different mechanism |

## Requirements

### Acceptance criteria

- [ ] The in-flight dedup never suppresses a send because of a task **another sender** created — the
      key includes `from`
- [ ] A suppressed send never prints a success or "tracking" line: the CLI names the suppression, says
      the message was not sent, and exits non-zero (or returns a distinct status) — on both roads,
      in-flight dedup and duplicate-reply
- [ ] Negative control: a same-sender retry of an identical in-flight request is still deduplicated
      (the `--wait`-killed-by-timeout case the guard exists for), and `--wait` still reattaches
- [ ] Negative control: `--force` still bypasses the dedup as documented
- [ ] Negative control: a genuine duplicate reply to a completed task is still suppressed — but says so
      honestly
- [ ] Negative control: distinct action names remain independently deliverable — varying the action
      is the workaround agents use today, and the fix must not turn it into a new drop

### Technical approach

Add `from` to the key in `FindInFlightTask` / `HasInFlightTaskForRole` (and their callers), so the
guard matches the retry it was written for and nothing else. Tighten further with a payload
hash, since a same-sender send of a *different* message under the same action is also not a retry —
edit hit exactly that today: its MUX-145 follow-up, a different message under its own in-flight
`update-docs`, was refused until `--force`. Then make `Send`'s suppression paths return a sentinel the CLI can distinguish from success,
and have `cmd/send.go` print the suppression and exit non-zero instead of `Sent …`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/dedup.go` | `HasInFlightTaskForRole` (`:217`), `FindInFlightTask` (`:235`) — the `(to, action)` key |
| `tools/muxcode/cmd/send.go` | The pre-send dedup (`:184-189`), `--wait` reattach (`:191-`), the `Sent …` success line |
| `tools/muxcode/bus/inbox.go` | Duplicate-reply suppression returning `nil` (`:228`, and the self-addressed variant at `:92`) |
| `tools/muxcode/bus/task.go` | `Task.From` — already recorded, just not consulted |

## Implementation

### Phase 1: Pin

- [ ] Characterization test: two senders, same `(to, action)`, the second send is dropped today;
      failure message names Phase 2
- [ ] Pin the same-sender retry case as the behaviour to keep
- [ ] Pin that a suppressed send (both roads) currently exits 0 and prints a success/tracking line

### Phase 2: Key on the sender

- [ ] `FindInFlightTask` / `HasInFlightTaskForRole` take and match `from`
- [ ] Invert the Phase 1 cross-sender pin
- [ ] Negative control: same-sender dedup and `--wait` reattachment unchanged; `--force` unchanged

### Phase 3: An honest CLI

- [ ] `Send` returns a distinguishable result for each suppression path instead of `nil`
- [ ] `cmd/send.go` prints what was suppressed and why, never `Sent …` or "already tracking" for a
      message it did not write; non-zero exit
- [ ] Negative control: a delivered send still prints `Sent …` and exits 0

### Phase 4: Integration test

- [ ] Create `scripts/test-send-dedup-sender.sh` (hermetic; scratch bus)
- [ ] Test: `watch → edit notify` in flight, then `plan → edit notify` → delivered (row in edit's
      inbox, its own task created)
- [ ] Test: `plan → edit notify` twice → second suppressed, CLI says so, exit non-zero (negative
      control — the guard cannot go inert)
- [ ] Test: reply to a completed task → suppressed and reported honestly, exit non-zero
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Notes

Filed 2026-09-08 by plan after being bitten twice in one afternoon — once by the intended same-sender
case, once by this cross-sender one — and after first reaching a wrong theory (that the dropped
message had been written to the inbox but not the log). The code settled it: the guard returns before
any write, and the matched task belonged to watch. The "Sent after suppression" idea filed earlier
today in the backlog's ideas list is folded in here as Phase 3.

## Status

**Backlog** — filed 2026-09-08. Not started.
