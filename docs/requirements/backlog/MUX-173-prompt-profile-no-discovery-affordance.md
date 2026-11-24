# The Prompt Profile Denies Its Own Discovery Commands

**Tracking:** [mkober/muxcode#131](https://github.com/mkober/muxcode/issues/131)

The `prompt` tool profile (`bus/profile.go`, the `"prompt"` entry — at `:1074` on 2026-11-24) lists twelve `muxcode` subcommand patterns, every
one of them requiring a subcommand argument — `Bash(muxcode graph *)`, `Bash(muxcode send *)`, and so
on. Nothing matches bare `muxcode` or `muxcode help`. A local model that opens by orienting itself,
which is what a model does when it has no memorised CLI surface, is denied twice before it starts:

```
[deepseek-v4-flash] Error: command not allowed by tool profile: muxcode
[deepseek-v4-flash] Error: command not allowed by tool profile: muxcode help
```

Filed 2026-09-10 on edit's request, out of the MUX-163 Phase 4 runs. **Scoped deliberately narrowly:
this spec covers the profile gap only.** The failure those runs were investigating is *not* claimed
here as caused by it — see [What this is not](#what-this-is-not), which exists because the first
version of this finding did make that claim and it did not survive checking.

## Context

### Observed (2026-09-10, `scripts/test-prompt-mode.sh` section 5)

| When | Run | Denials | Outcome |
|------|-----|---------|---------|
| 10:31 | run 1 | **0** | `launch intent started a run` FAILED — two bash calls, `exit=0` each, then "Final response looks like narration, requesting summary" |
| 10:45 | run 2 | **2** (`muxcode`, `muxcode help`) | `launch intent started a run` FAILED — iterations spent on denied discovery, narration at `Prompt 9/10` |

Both runs reported 26 passed / 2 failed / 1 skipped, exit 1.

### Mechanism — verified in code

- `bus/profile.go` `"prompt"` (`:909` at filing, `:1074` on 2026-11-24, list unchanged) — `Tools` holds `Bash(muxcode graph *)`, `Bash(muxcode approve *)`,
  `Bash(muxcode send *)`, `Bash(muxcode inbox*)`, `Bash(muxcode status*)`, `Bash(muxcode tasks*)`,
  `Bash(muxcode spec *)`, `Bash(muxcode workflow*)`, `Bash(muxcode history *)`,
  `Bash(muxcode memory *)`, `Bash(muxcode diagnose *)`, `Bash(muxcode lifecycle *)`, two
  `atlassian jira` read patterns, and two `Read(...)` entries.
- Each `muxcode` pattern needs its subcommand to match. Bare `muxcode` matches none; `muxcode help`
  matches none. Note the inconsistency in the existing list itself — `Bash(muxcode inbox*)` has no
  space and so admits `muxcode inbox`, while `Bash(muxcode graph *)` requires one and denies bare
  `muxcode graph`.
- The denial text comes from `tools/muxcode-llm-harness/harness/executor.go:100`.

### What this is not

The first version of this finding claimed a prompt-agent denied `muxcode` "can never start a run
however long it waits", and that the section-5 failure was therefore a profile defect rather than a
timeout. **Two facts refute it**, both from the runs above:

- The denials occur in **one run of two**, while the failure occurs in **both**. A cause present half
  the time cannot explain an effect present every time.
- The profile **allows** `Bash(muxcode graph *)`, which is the family the launch intent needs. The
  command required to pass was never denied — only the orientation calls were.

What the two runs *do* share is that the model **narrates instead of acting**: run 1 ended in
"narration, requesting summary"; run 2 ended with "I attempted to execute the task with the bash tool
as required. Here is what happened". That is the stronger candidate cause and it is **not** this
spec — it needs its own investigation, recorded below as an open question so the lead is not lost.

This section is kept rather than deleted: the retraction is the useful part, and a reader who meets
the denial in a log will otherwise re-derive the same wrong conclusion.

### Scope boundary

In scope: the `prompt` profile's missing discovery affordance, and the space-vs-no-space inconsistency
across its existing patterns. Not in scope: why section 5's live intents fail (open question below),
the `Loop detected: edit (message)` rows in both runs, other roles' profiles, or the harness's denial
mechanism itself — which behaved exactly as designed.

## Decisions (user, 2026-11-24)

Relayed by edit. Option A, the narrow form:

- Add `Bash(muxcode help*)` and a bare `Bash(muxcode)` to the **`prompt` profile only**. No convention
  shared across roles is introduced; another role's profile is changed when that role shows the gap.
- Pin the allowed set with a test that carries a negative control.
- Normalise the trailing-space spacing across the profile's `muxcode` patterns.

## Requirements

### Acceptance criteria

- [ ] A prompt-agent can run bare `muxcode` and `muxcode help` without a tool-profile denial
- [ ] The `prompt` profile's `muxcode` patterns are internally consistent about the trailing space, so
      `muxcode graph` and `muxcode inbox` are treated alike rather than one admitted and one denied
- [ ] Widening the profile grants **no** new mutating capability: `commit`, Atlassian writes and every
      other verb outside today's list stay exactly as unreachable as they are today
- [ ] No other role's profile changes
- [ ] A test pins the allowed set, and fails if a future edit admits a mutating subcommand — with a
      negative control proving the assertion can fail

### Technical approach

Add `Bash(muxcode help*)` and `Bash(muxcode)` to the `prompt` entry's `Tools` in `bus/profile.go`.
`Bash(muxcode)` must match the bare command only: the test asserts that it does not admit
`muxcode commit` or any other subcommand, since a matcher that treated it as a prefix would open the
whole CLI.

Normalising the spacing means choosing one form for the read-only subcommands. The no-space form
(`Bash(muxcode inbox*)`) admits the bare subcommand, which is what a model orienting itself types;
it also admits any subcommand sharing the prefix, so each normalised pattern is checked against the
subcommand list for a collision before it is changed, and the pin names what each admits.

The pin matters more than the widening: this profile is a **security boundary**. A test that asserts
the allowed set — and carries a negative control, so a rule that matches nothing cannot pass while the
set is wrong — is the part that keeps a later "just add one more pattern" from quietly admitting
`muxcode commit`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/profile.go` | the `"prompt"` profile's `Tools` list |
| `tools/muxcode/bus/profile_test.go` | the allowed-set pin |
| `tools/muxcode-llm-harness/harness/executor.go` | raises the denial; unchanged by this spec |
| `scripts/test-prompt-profile.sh` | new |

## Implementation

### Phase 1: Widen and pin

- [ ] Add `Bash(muxcode help*)` and `Bash(muxcode)` to the `prompt` profile
- [ ] Normalise the trailing-space inconsistency across that profile's `muxcode` patterns, each change
      checked for a prefix collision with another subcommand
- [ ] `profile_test.go`: the allowed set admits `muxcode`, `muxcode help`, and both `muxcode graph`
      and `muxcode inbox`; it denies `muxcode commit` and an Atlassian write; no other role's `Tools`
      changed
- [ ] **Negative control** in the same test: a profile copy with `Bash(muxcode commit*)` added fails
      the pin, and a matcher stub that matches nothing fails the admit assertions

### Phase 2: Docs

- [ ] [`docs/agents.md`](../../agents.md) tool-profile section: the `prompt` profile's discovery
      affordance, and why a model without one burns iterations orienting

### Phase 3: Integration test

- [ ] Create `scripts/test-prompt-profile.sh` — hermetic, muxcode and the harness built from the
      tree, no model call: each command goes through the permission check the harness executor
      applies to a prompt-agent's bash call (the road — an existing CLI entry point or a small test
      driver — is named here once Phase 1 has found it)
- [ ] `muxcode` and `muxcode help` are permitted; `muxcode commit` and `muxcode atlassian jira update`
      are refused
- [ ] **Negative control**: against a build from a copy of the tree without the two new patterns,
      `muxcode help` is refused — the permit assertion can fail
- [ ] Coverage floor; run through the run agent and record the row here

## Notes

**Open question, not part of this spec — why section 5's live intents fail.** Both runs failed
`launch intent started a run` and `create intent composed and wrote a valid definition` with the model
narrating rather than acting, in one case after two `exit=0` bash calls. The create-intent failure
reads as genuine latency (still at `Prompt 7/10 — calling gateway` at the 4-minute mark). The
launch-intent failure does not yet have an established cause. Whoever picks that up should start from
the two `live_diag` dumps in `/tmp/test-prompt-mode-run.log` and `/tmp/test-prompt-mode-run2.log`
rather than from this spec.

**Provenance.** Surfaced by the `live_diag` helper added to `test-prompt-mode.sh` during MUX-163
Phase 4 — a diagnostic written to make "latency, not capability" observable rather than assumed. It
did its job in both directions: it exposed the denials, and it also supplied the run-1 evidence that
refuted the first conclusion drawn from them.

## Status

Backlog

Unparked 2026-11-24 on the user's decision (option A, `prompt` profile only), relayed by edit and
recorded above. Not started.
