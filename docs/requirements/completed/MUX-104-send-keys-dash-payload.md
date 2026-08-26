# Wake-Injection Fails on Payloads Starting With a Dash

`tmux send-keys` parses a leading `-` in the payload as a flag, so any injected wake-up whose text begins with a dash — a bullet list, a diff line, a CLI flag quoted in a message — is rejected with `command send-keys: invalid flag -` and the message is never delivered.

## Context

### Symptom

Observed repeatedly on 2026-08-26, blocking build delivery. The daemon's injection path reports:

```
command send-keys: invalid flag -
```

The agent stays idle with an un-consumed inbox. Because the failure is in the *injection*, not the bus, the message survives on disk and the daemon retries it every cycle — producing a delivery loop that never converges for that message while other messages behind it drain normally.

### Reproduction (verified 2026-08-26)

Hermetic, no live session needed:

```bash
tmux new-session -d -s sk-probe -x 80 -y 24
t=$(tmux list-panes -t sk-probe -F '#{pane_id}' | head -1)

tmux send-keys -t "$t" '- bullet'          # command send-keys: invalid flag -
tmux send-keys -t "$t" -- '- bullet'       # OK
tmux send-keys -t "$t" -l -- '- bullet'    # OK

tmux kill-session -t sk-probe
```

### The fix is `--`, not `-l`

The originating report suggested "a `--` separator or `-l` literal flag". **`-l` alone does not fix it** — verified in the same probe:

| Invocation | Result |
|------------|--------|
| `send-keys -t T '- x'` | `invalid flag -` |
| `send-keys -l -t T '- x'` | `invalid flag -` |
| `send-keys -t T -l '- x'` | `invalid flag -` |
| `send-keys -t T -- '- x'` | **OK** |
| `send-keys -t T -l -- '- x'` | **OK** |

`-l` controls whether the payload is interpreted as *key names* (`Enter`, `C-u`); it does nothing about *flag* parsing, which happens earlier. Only `--` terminates flag parsing. A fix that adds `-l` and stops will look correct in review and still fail on the exact input that triggered the bug.

### Blast radius — three call sites, not two

> **Corrected at completion.** This section originally read "two call sites, not one" and listed
> Codex only as an open question. Codex **was** vulnerable — `provider_codex.go` ran a raw
> `exec.Command("tmux", "send-keys", "-t", target, prompt)` with neither `-l` nor `--`, making it
> the *most* exposed of the three (it did not even route through the mockable `TmuxRun`). All three
> now go through `TmuxSendLiteral`. The count in the heading was wrong for the same reason the
> original report's was: each pass found one more site than the last, because the search was
> anchored on the site that happened to fail rather than on "every injection carrying a dynamic
> payload".

| Site | Payload | Status |
|------|---------|--------|
| `bus/provider_opencode.go:213` `TmuxRun("send-keys", "-t", target, prompt)` | message text | **Vulnerable** — no `-l`, no `--`; the reported failure |
| `bus/notify.go:884` `TmuxRun("send-keys", "-t", target, "-l", text)` | message text | **Vulnerable** — has `-l`, still fails per the table above |
| `bus/provider_claude.go:345` | fixed `"You have new messages"` | Safe — literal never starts with `-` |
| `bus/clear.go`, `health.go`, `hook.go:1425-1434`, `mode.go` | fixed key names / commands | Safe unless a payload is ever threaded through |

`notify.go:884` carries a comment — *"Send text with `-l` (literal) to avoid tmux key interpretation"* — that is **true about key names and misleading about this bug**. It reads as though the line is already protected. Any fix should correct the comment, or the next reader will re-introduce the assumption.

### Why it matters more than a cosmetic escaping bug

The injection path is the delivery mechanism for non-hook providers (OpenCode, Codex), which cannot run `muxcode inbox` themselves. A payload the daemon cannot inject is a message that agent can never receive — and `confirmInjectionAndConsume()` correctly declines to consume on failure, so the message is preserved but permanently re-attempted. Bullet-formatted content is common in build and review reports, so this fires on ordinary traffic rather than on exotic input.

## Requirements

### Acceptance criteria

- [x] `bus/provider_opencode.go` injection passes the payload after a `--` separator
- [x] `bus/notify.go` injection passes the payload after a `--` separator, keeping `-l`
- [x] The misleading `-l` comment at `notify.go:883` is corrected to say `-l` governs key-name interpretation and `--` is what protects a leading dash
- [x] A payload beginning with `-` is delivered intact — the dash is not stripped and the rest of the line is not truncated
- [x] Payloads beginning with `--` (a long flag, e.g. `--render-once`) also deliver intact
- [x] Multi-line and unicode payloads are unaffected by the change
- [x] A `TmuxRun` helper (or a wrapper) makes the safe form the default, so a future call site cannot re-introduce this by writing the natural-looking version
- [x] Regression test asserts the `--` separator is present in the argv for both injection paths — argv-level, since the failure is in argument parsing and a mocked runner that only checks the payload string would pass while broken
- [x] Negative test: a payload starting with `-` round-trips through the mocked runner without error

### Technical approach

- Add `--` immediately before the payload in both call sites: `send-keys -t <target> -l -- <payload>`.
- Prefer centralising it: a `TmuxSendLiteral(target, text)` helper in `bus/tmux.go` that always emits `-l --`, with both call sites switched to it. The existing `deliver_test.go:93` already greps argv for `-l`; extend that pattern to require `--`.
- Leave the `Enter` sends alone — `Enter` is a key name and must **not** be literal, which is why the current code splits text and Enter into two calls.

### Key files

| File | Change |
|------|--------|
| `bus/provider_opencode.go` | Add `--` to the text injection (line ~213) |
| `bus/notify.go` | Add `--` to the text injection (line ~884), fix the comment above it |
| `bus/tmux.go` | New `TmuxSendLiteral()` helper making the safe form default |
| `bus/notify_test.go`, `bus/provider_opencode_test.go` | argv-level regression tests |
| `scripts/test-send-keys-dash.sh` | Integration test (see Phase 3) |

## Implementation

### Phase 1: Fix the two call sites

- [x] Add `TmuxSendLiteral()` to `bus/tmux.go` emitting `send-keys -t <target> -l -- <text>`
- [x] Switch `provider_opencode.go` text injection to it
- [x] Switch `notify.go` text injection to it
- [x] Correct the misleading `-l` comment in `notify.go`
- [x] Audit the remaining `send-keys` call sites and note in code which are payload-carrying vs fixed-string

### Phase 2: Regression tests

- [x] argv-level test: both injection paths emit `--` before the payload
- [x] Payload starting with a single `-` round-trips without error
- [x] Payload starting with `--` round-trips without error
- [x] Multi-line and unicode payloads still deliver unchanged

### Phase 3: Integration test

- [x] Create `scripts/test-send-keys-dash.sh` (hermetic — scratch tmux session, no live muxcode session needed)
- [x] Test: bare `send-keys` with a dash-leading payload fails, pinning the underlying tmux behaviour the fix defends against
- [x] Test: the muxcode injection path delivers a dash-leading payload into a scratch pane, verified by `capture-pane`
- [x] Test: a `--`-leading payload delivers intact
- [x] Test: delivery marks a receipt, so the message is consumed rather than retried forever
- [x] Run the script and verify all checks pass

## Open questions

- ~~**Does Codex's injection path share this?**~~ **Answered: yes, and worse.** `provider_codex.go:253` used a raw `exec.Command("tmux", "send-keys", "-t", target, prompt)` — no `-l`, no `--`, and not even routed through the mockable `TmuxRun`, so no argv-level test could have caught it. Now uses `TmuxSendLiteral` like the other two.
- **Should `BoundWakeUpBatch` sanitise instead?** Still open, and now less urgent. Escaping at the boundary is more robust than fixing call sites, but changes payload content; the `--` fix preserves the payload byte-for-byte. Worth revisiting only if a case appears that `--` cannot cover.

## Completion notes (2026-08-26)

Closed at **24/24**. Evidence: `scripts/test-send-keys-dash.sh` — **10 passed, 0 failed, exit 0**,
taken firsthand from the run agent rather than via relay.

**The integration test pins the diagnosis, not just the fix.** Its first two assertions are
*"bare dash payload rejected by tmux"* and *"`-l` alone still rejected — `--` is the real fix"*.
Those encode the finding that cost the most to establish: the originating report proposed `-l`,
which does not work. Without those two checks a future refactor could drop `--`, keep `-l`, and
still pass a suite that only tested the happy path.

**The helper is the durable part.** `TmuxSendLiteral()` makes the safe form the default and its
comment names the trap explicitly (*"-l alone does NOT prevent this"*), so the natural-looking
call is now the correct one. That matters more than the three individual fixes: this bug reached
production because writing `send-keys -t target payload` looks obviously right.

## Sources

- Reported by the edit agent, 2026-08-26, after repeated build-delivery failures
- Reproduction and the `-l`-is-insufficient finding verified by the plan agent in a scratch tmux session the same day
- `bus/provider_opencode.go`, `bus/notify.go`, `bus/tmux.go`, `bus/inject_verify.go`

## Provenance

Filed by the plan agent on 2026-08-26 at the edit agent's request, relaying an observed delivery failure. The originating report named `-l` as an acceptable fix; testing showed it is not, and the spec records the corrected fix.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-104-send-keys-dash-payload | 38m | 2026-08-26 11:21 |

> Recorded in one settlement at completion rather than incrementally: no active spec was set for
> this branch, so no `verify-spec` ever fired to prompt a recording. The ledger held all 37m as
> unrecorded until this pass. Had the branch been closed without someone checking, that time would
> have been lost — the same stranding trap as MUX-014, arriving from the opposite direction (no
> pointer ever set, rather than a pointer cleared too early).

## Status

Complete
