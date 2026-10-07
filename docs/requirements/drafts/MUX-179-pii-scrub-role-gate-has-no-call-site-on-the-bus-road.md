# The PII Scrub Role Gate Has No Call Site on the Bus Road

**Tracking:** [mkober/muxcode#137](https://github.com/mkober/muxcode/issues/137)

**Provenance:** filed 2026-09-11 by plan on the user's request relayed by commit (`1789143390`), from a
credential leak plan caused while diagnosing [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md).
Verified in code before filing.

`CLAUDE.md` states that `api`, `run` and `watch` output is redacted before it enters the conversation.
That holds for the **local LLM harness only**. In the bus module — every real Claude, Codex and
OpenCode agent — `IsPIISensitiveRole` is **dead code**: defined, unit-tested, and called from nowhere.
No role's tool output is scrubbed on that road.

## Context

### Observed — a real key in clear text

While identifying an orphaned listener's role, plan ran `ps eww -p 21146`, which dumps the whole
environment. `MUXCODE_OPENCODE_API_KEY` was printed in clear text into the agent's conversation and
from there into `plan-history.jsonl`. Recorded at MUX-156's third-occurrence note (committed
`e23f9ee`). The key requires rotation.

`plan` is not in the sensitive-role list — but as the mechanism below shows, membership would not
have helped.

### Mechanism — verified in code

| Module | `IsPIISensitiveRole` callers | Effect |
|--------|------------------------------|--------|
| `tools/muxcode-llm-harness/harness/` | `loop.go:133` → `executor.ScrubPII = true` | **works** — api/run/watch/runner output scrubbed |
| `tools/muxcode/bus/` | **none** (definition + `scrub_test.go:188` only) | **no role output is scrubbed at all** |

Bus-side `ScrubPII` / `ScrubPIIWithNotice` call sites, in full:

| Site | Scope |
|------|-------|
| `cmd/scrub.go:21` | the manual `muxcode pii-scrub` pipe filter — opt-in, never automatic |
| `bus/snapshot.go:91, 98, 107` | snapshot log, pane and procs |

Nothing on the PostToolUse/history path calls either. `bus/scrub.go:166` carries the same
`piiSensitiveRoles` map as the harness, so the two modules *look* identical at a glance; only the
harness wires it to anything.

### The test does not catch it

`TestIsPIISensitiveRole_Bus` (`bus/scrub_test.go:188`) passes today. It asserts the **map's contents**
— that `api`/`run`/`watch` are members and others are not — never that scrubbing occurs. It would
pass unchanged if every call site were deleted, which is exactly what has happened. A green suite has
been reporting this feature as present.

### Why it matters beyond one key

Agent tool output lands in `<role>-history.jsonl`, in lifecycle logs, and in the conversation itself.
Diagnosis is routine across every role — `ps`, `env`, `cat` of a config, `muxcode config list` — and
the roles that do it most (`plan`, `edit`, `commit`, `build`) are precisely the ones outside the map.
The documented guarantee reads as cover that is not there.

### Scope boundary

In scope: the missing bus-side call site, the map's membership, and the vacuous test. Not in scope:
the harness path (working), the snapshot path (working), and MUX-156 itself — the leak surfaced while
diagnosing it but is independent of it.

## Requirements

### Acceptance criteria

- [x] Tool output from a sensitive role is scrubbed **on the bus road**, not only in the harness (Phase 1: `ProcessBashHook` → `GetScrubbedOutput`; `TestProcessBashHook_SensitiveRoleRedactsSecret`)
- [ ] A secret in a diagnostic command's output never reaches `<role>-history.jsonl` or the conversation unredacted (**history half met** in Phase 1 — `TestProcessBashHook_SensitiveRoleRedactsSecret`, `…RedactsSecretAcrossTail`, `TestGetScrubbedOutput_RedactsBeforeClip`; the history row's `Command`/`Description` fields are still unscrubbed. **The conversation half is not**: `PostToolUse` fires after the provider has already shown the agent the raw output, so this road cannot redact what the agent sees — a different mechanism, or a narrowed promise, is for [Phase 3](#phase-3-decide-coverage) to decide)
- [x] **Negative control:** ordinary output with no secret is passed through **unchanged** — no banner, no truncation, no altered exit code (Phase 1: `TestProcessBashHook_SensitiveRoleCleanOutputUnchanged`, `TestGetScrubbedOutput_CleanMatchesGetOutput`)
- [x] A test fails if the call site is removed — the current test passes with the feature gone (Phase 1: `TestProcessBashHook_SensitiveRoleRedactsSecret` drives `ProcessBashHook` end to end and reads the history row — it fails if the `IsPIISensitiveRole` gate is removed, which the membership-only `TestIsPIISensitiveRole_Bus` never did)
- [ ] The role list is reconsidered on evidence: the leak came from `plan`, which is not a member
- [ ] `CLAUDE.md` and [`docs/agents.md`](../../agents.md) state what is actually covered, per road

### Technical approach

Find the bus-side equivalent of the harness's `loop.go:133` — the point where a role's tool output is
captured and written to history — and gate it on `IsPIISensitiveRole` there. The predicate, the
patterns and the banner already exist and are shared; only the wiring is missing.

Then decide the membership question separately, on evidence rather than intuition. Two shapes:

| Option | Trade-off |
|--------|-----------|
| Widen the list to every role | Safe by default; scrubbing cost and false-positive redaction on all output |
| Keep the list, scrub *credential patterns* everywhere | Narrower; needs the secret patterns split from the PII patterns |

The second is the better fit for this evidence: what leaked was a credential, not PII, and credentials
are cheap to match with high precision. `ScrubPIIWithNotice`'s banner requirement stands either way —
a redaction must announce itself so a placeholder is never mistaken for data.

**The load-bearing test is the negative control.** A fix that scrubs aggressively would satisfy every
"no secret in history" assertion while corrupting ordinary output, and agents reason over that output.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/scrub.go` | `piiSensitiveRoles` (166), `IsPIISensitiveRole` (175) — the dead gate |
| `tools/muxcode-llm-harness/harness/loop.go` | `:133`, the working call site to mirror |
| `tools/muxcode/bus/hook.go` | PostToolUse path — where tool output is captured |
| `tools/muxcode/bus/scrub_test.go` | `:188` `TestIsPIISensitiveRole_Bus` — the vacuous test |
| `CLAUDE.md`, `docs/agents.md` | the guarantee as currently written |

## Implementation

### Phase 1: Wire the gate

- [x] Call `IsPIISensitiveRole` on the bus-side tool-output path, mirroring `loop.go:133` (`ProcessBashHook`, `bus/hook.go`: a PII-sensitive role's output is captured through `GetScrubbedOutput`, which runs `ScrubPII` on the **whole ANSI-stripped response before both** the line tail and the `maxChars` clip — each limit can split a secret from what identifies it — then applies the limits through the one shared `clipOutput(text, maxLines, maxChars)`, then prepends the `PIIScrubNotice` banner counting every redaction. `GetOutput`'s output is unchanged. Covers `api`, `run`, `runner`, `watch` — the current `IsPIISensitiveRole` set)
- [x] Unit test: sensitive-role output containing a key is redacted with the banner (`TestProcessBashHook_SensitiveRoleRedactsSecret` — a `ps eww` environment dump carrying an API key, the shape of the original leak; the history row holds the placeholder under the `[muxcode pii-scrub:` banner; `TestGetScrubbedOutput_RedactsBeforeClip` for the straddling key)
- [x] **Negative control:** non-secret output is byte-identical after the call (`TestProcessBashHook_SensitiveRoleCleanOutputUnchanged` — no banner, no truncation, exit code and outcome untouched; `TestGetScrubbedOutput_CleanMatchesGetOutput` — tailed and clipped clean output equals `GetOutput`'s)

#### Phase 1 verification note

Verified 2026-10-07 14:15 by plan from the working tree (run `1791395671`, user-launched). First pass:
review **failed** with one must-fix — `GetScrubbedOutput` scrubbed the *tail*: `outputTail` kept the last
`maxLines` lines first, so a `password=` label on a discarded line with `SuperSecret123` on a kept line
stored the secret unredacted, the scrubber never seeing the label it matches on. Second pass, after the
`fix` worker: the scrub runs on the whole stripped response before both limits, `outputTail` is gone in
favour of one `clipOutput` shared with `GetOutput`, and `TestProcessBashHook_SensitiveRoleRedactsSecretAcrossTail`
pins the exact shape — label on the 16th-from-last line, value on the first kept one, a fixture guard
proving the unscrubbed tail would drop the label and keep the value. Test node **success** on the full
suite both passes; review 0 must-fix. The worker's own follow-ups, carried to Phase 3: the conversation
half of criterion 2 needs another mechanism (`PostToolUse` is too late); the history row's `Command` and
`Description` fields are unscrubbed; `plan`, `edit`, `commit` and `build` stay unscrubbed pending the
coverage decision — the original leak came from `plan`.

### Phase 2: Make the test non-vacuous

- [ ] Replace/augment `TestIsPIISensitiveRole_Bus` so it fails when the call site is removed
- [x] Assert the *behaviour* (output redacted) rather than the map's membership (already met by Phase 1's `TestProcessBashHook_SensitiveRoleRedactsSecret` and `…AcrossTail`, which drive `ProcessBashHook` and read the history row; the membership-only `TestIsPIISensitiveRole_Bus` is the step above's to replace or keep)

### Phase 3: Decide coverage

- [ ] Split credential patterns from PII patterns, or widen the role list — recorded with the reasoning
- [ ] Pin the chosen shape with a test for a `plan`-role diagnostic leaking a key

### Phase 4: Docs

- [ ] `CLAUDE.md` PII bullet and [`docs/agents.md`](../../agents.md): what is covered, on which road
- [ ] Note the narrow-env-read idiom recorded on MUX-156 as the safe way to read another process's role

### Phase 5: Integration test

- [ ] `scripts/test-pii-scrub-roles.sh`: a scratch agent emits a fake key; assert it is absent from `<role>-history.jsonl` and the pane
- [ ] **Negative control:** a run with no secret leaves output unchanged
- [ ] Coverage floor pinned to the exact pass count
- [ ] Run through the run agent (**foreground**, per MUX-171) and record counts here

## Notes

**Found by causing it.** Plan leaked the key itself while diagnosing MUX-156, then checked whether the
scrubber should have caught it and found the gate unwired. Recorded that way because the narrow
`ps eww … | grep -E '^(AGENT_ROLE|BUS_SESSION)='` idiom now on MUX-156 is a workaround for a missing
control, not a fix.

**Related:** [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (where the leak is
recorded); [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) (a rule with no enforcement — the
same shape, one road up).

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road | 10m | 2026-10-07 14:15 |

## Status

In Progress — moved from `backlog/` to `drafts/` and set as the active spec on the user's instruction,
2026-10-07 (next defect by rank, 1 / Tier 1).
