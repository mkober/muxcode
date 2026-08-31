# Harness Circuit Breaker

Three-layer stuck protection preventing runaway Ollama calls in the LLM harness.

## Requirements

- Within-turn filter rejects repeated blocked tool calls in the same turn
- Within-batch early exit after `MaxAllBlockedTurns=2` consecutive all-blocked turns
- Cross-batch cooldown after `MaxConsecutiveFailures=3` triggers a 30-second pause
- Each batch enforces a 5-minute timeout
- Circuit breaker state resets on successful batch completion

## Key files

| File | Purpose |
|------|---------|
| `harness/loop.go` | `Run()`, `processBatch()`, batch timeout, cross-batch cooldown logic |
| `harness/filter.go` | `Filter`, `Check()`, within-turn dedup, `commandHash()` |

## Status

Complete
