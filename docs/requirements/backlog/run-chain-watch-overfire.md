# Run Chain Watch Overfire

## Context

### Problem

The run agent's `OnSuccess` event chain (`bus/profile.go:918-941`) fires a
`watch` request after **every** successful Bash command the run agent
executes:

```go
"run": {
  OnSuccess: ChainActions{{
    SendTo:  "watch",
    Action:  "watch",
    Message: "Run succeeded (${command}) — tail logs to verify deployed services are healthy and report findings to edit",
    Type:    "request",
    Conditions: map[string]any{
      "command_not_match": "muxcode *",
    },
  }},
  ...
}
```

The only gate is `command_not_match: "muxcode *"`. The chain's message assumes
the run was a **post-deploy verification** ("tail logs to verify deployed
services are healthy"), but the run agent's remit is much broader — ad-hoc
script execution, integration tests (`scripts/test-*.sh`), AWS process
execution, and incidental read-only commands while preparing those. A plain
`cat` of a file therefore triggers a bogus tail-logs request to watch even
though nothing was deployed.

### Observed failure

The daemon's loop detection logged a `run→watch` message loop **4 times in
4m44s** — exactly at the relay-loop suppression backstop
(`MUXCODE_RELAY_SUPPRESS_THRESHOLD` default 4 per 300s window,
`bus.CountRecentRequestTuple` in `bus/guard.go`). The safety net capped the
storm; the chain trigger itself is what is wrong. This is the same
run→watch storm shape that motivated relay suppression originally (watch
stood down while run kept delegating).

Live corroboration (2026-08-18 11:34, bus history): the run agent executed a
read-only `cat` of a task output file and the chain fired
`run → watch  [request:watch] "Run succeeded (cat /private/tmp/.../tasks/….output 2>&1) — tail logs to verify deployed services are healthy…"`,
to which watch replied `"Loop at 5. Edit already notified. No new info."` —
the exact bogus-trigger-plus-loop shape this spec describes, captured while
the spec was being filed.

### Root cause

A denylist gate on a chain whose semantic trigger is an allowlist-shaped
event. "Everything except `muxcode *`" treats *any* successful command as
evidence of a deploy-verification run. Denylists of incidental commands
(`cat`, `ls`, `grep`, …) can never be closed — the trigger must instead be
scoped to the commands that actually mean "a verification run completed".

### Condition machinery (verified)

- `command_match` / `command_not_match` are implemented, validated, and
  tested condition types (`bus/conditions.go:39-40`, `:96-98`, `:246-270`;
  `bus/conditions_test.go:605`, `:630`).
- The documented condition-type list is stale: `CLAUDE.md` and the
  conditional-chains references say **8** condition types, but with
  `command_match`/`command_not_match` there are **10**.
- `ChainActions` is a slice evaluated first-match-wins
  (`ResolveChain()`, `bus/profile.go`), so an allowlist of N command shapes
  is expressible today as N actions each carrying one `command_match`
  condition — no new machinery required.
- `ChainContext` does **not** carry the triggering bus message (only git
  state, command, output, exit code) — origin-aware chains ("fire watch only
  when this run was delegated by the deploy chain") would require extending
  it. That is noted as a design option, not required for the fix.

## Requirements

### Acceptance criteria

- [ ] A successful read-only or incidental command in the run window (`cat`, `ls`, `grep`, file inspection) does NOT fire a watch request
- [ ] A post-deploy verification run (deploy-chain shapes, e.g. `aws *` invocations and `scripts/test-*.sh`) still fires the watch request
- [ ] `muxcode *` commands still never fire the chain
- [ ] No `run→watch` tuple reaches the relay-loop suppression threshold during a normal run-agent session
- [ ] Condition-type documentation lists all 10 types including `command_match`/`command_not_match` (`CLAUDE.md`, `docs/hooks.md`)
- [ ] Existing `bus/conditions_test.go` and `bus/profile.go` chain tests keep passing

### Technical approach

Invert the gate from denylist to allowlist using existing machinery:

1. Replace the single `OnSuccess` action with a first-match-wins
   `ChainActions` slice where each action carries a `command_match`
   condition for a verification-run shape (final pattern list decided at
   implementation; candidates: `aws *`, `scripts/test-*`, `*.sh *`/`*.sh`).
   Commands matching no action fire nothing — the `muxcode *` exclusion
   becomes moot but harmless to keep as documentation of intent.
2. `OnFailure`/`OnUnknown` (edit notifications) stay as they are — failure
   noise is actionable regardless of command shape.
3. Design option (out of scope here, cross-link if picked up): extend
   `ChainContext` with the triggering request's sender/action so
   deploy→run→watch fires as a true pipeline — see
   [`conditional-chains`](../completed/conditional-chains.md) for the
   condition framework this would build on.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/profile.go` | Run `EventChain` config (lines 918-941) — the fix |
| `tools/muxcode/bus/conditions.go` | `command_match`/`command_not_match` implementation (no change expected) |
| `tools/muxcode/bus/hook.go` | `ProcessBashHook()` → `ResolveChain()` path the trigger flows through |
| `tools/muxcode/bus/guard.go` | `CountRecentRequestTuple()` relay suppression (backstop, no change) |
| `CLAUDE.md`, `docs/hooks.md` | Stale "8 condition types" documentation |
| `scripts/test-run-chain-scope.sh` (new) | Integration test |

### Dependencies

| Dependency | Note |
|------------|------|
| [`conditional-chains`](../completed/conditional-chains.md) | Condition framework this fix uses |
| [`response-echo-chain-retrigger`](./response-echo-chain-retrigger.md) | Sibling chain-noise defect on non-hook agents |

## Implementation

### Phase 1: Scope the OnSuccess trigger

- [ ] Replace the run chain's denylist gate with a first-match-wins allowlist of `command_match` actions in `bus/profile.go`
- [ ] Decide and record the final allowlist patterns (verify against real run-agent traffic in `log.jsonl`)
- [ ] Unit tests: `cat`/`ls`/`grep` resolve to no action; each allowlisted shape resolves to the watch action; `muxcode *` resolves to no action
- [ ] Existing chain and condition tests pass unchanged

### Phase 2: Documentation sync

- [ ] Update `CLAUDE.md` conditional-chains constraint: 10 condition types, naming `command_match`/`command_not_match`
- [ ] Update `docs/hooks.md` condition-type reference to match
- [ ] Note the run-chain allowlist behavior in `docs/hooks.md` chain documentation

### Phase 3: Integration test

- [ ] Create `scripts/test-run-chain-scope.sh` with automated verification
- [ ] Test: simulated hook success for `cat <file>` in the run window enqueues no watch message
- [ ] Test: simulated hook success for an allowlisted verification command enqueues exactly one watch request
- [ ] Test: simulated hook success for a `muxcode *` command enqueues nothing
- [ ] Run the integration test and verify all checks pass

## Status

Draft
