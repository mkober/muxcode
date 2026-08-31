# Event Subscription

JSONL-persisted subscription table enabling event fan-out to interested agents.

## Requirements

- Agents subscribe to event+outcome patterns (e.g. `build/success`, `*/failure`, `*/*`)
- Subscriptions persisted as JSONL in the bus directory
- Chain fires matching subscriptions after the primary action completes
- Message template expansion with `${event}`, `${outcome}`, `${exit_code}`, `${command}` variables
- Wildcard matching on both event and outcome fields
- Subscription messages use `SendNoCC()` to avoid redundant CC to edit

## Key files

| File | Purpose |
|------|---------|
| `bus/subscribe.go` | `AddSubscription()`, `MatchSubscriptions()`, `FireSubscriptions()`, `ExpandSubscriptionMessage()` |

## Status

Complete
