# The Prompt Profile Denies Its Own Discovery Commands

The `prompt` tool profile (`bus/profile.go:909–921`) lists twelve `muxcode` subcommand patterns, every
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

- `bus/profile.go:909` `"prompt"` — `Tools` holds `Bash(muxcode graph *)`, `Bash(muxcode approve *)`,
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

## Requirements

### Acceptance criteria

- [ ] A prompt-agent can run a read-only orientation command — at minimum `muxcode help` — without a
      tool-profile denial
- [ ] The `prompt` profile's `muxcode` patterns are internally consistent about the trailing space, so
      `muxcode graph` and `muxcode inbox` are treated alike rather than one admitted and one denied
- [ ] Widening the profile grants **no** new mutating capability: `commit`, `graph approve`,
      Atlassian writes and every other gated verb stay exactly as reachable as they are today
- [ ] A test pins the allowed set, and fails if a future edit admits a mutating subcommand — with a
      negative control proving the assertion can fail

### Technical approach

Add a discovery affordance to the `prompt` entry in `bus/profile.go`. The narrow form is
`Bash(muxcode help*)` plus a bare `Bash(muxcode)`; the question worth deciding is whether the profile
should instead carry a single `Bash(muxcode --help*)`-style convention shared across roles, since this
gap is unlikely to be unique to `prompt`.

Whatever the shape, the pin matters more than the widening: this profile is a **security boundary**,
and every other role's profile is one too. A test that asserts the allowed set — and carries a
negative control, so a rule that matches nothing cannot pass while the set is wrong — is the part that
keeps a later "just add one more pattern" from quietly admitting `muxcode commit`.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/profile.go` | `prompt` profile at `:909`; the `Tools` list to widen |
| `tools/muxcode-llm-harness/harness/executor.go` | `:100` raises the denial; unchanged by this spec |
| `scripts/test-prompt-mode.sh` | section 5 is where the denials surfaced |

## Implementation

### Phase 1: Widen and pin

- [ ] Decide the discovery convention (per-role `help` pattern vs a shared one) and record it here
- [ ] Add the affordance to the `prompt` profile
- [ ] Normalise the trailing-space inconsistency across that profile's `muxcode` patterns
- [ ] `profile_test.go`: assert the allowed set admits `muxcode help` and still denies a mutating
      subcommand, with a negative control that fails if the matcher stops matching

### Phase 2: Docs

- [ ] [`docs/agents.md`](../../agents.md) tool-profile section: state that a profile needs a discovery
      affordance, and why a model without one burns iterations orienting

### Phase 3: Integration test

- [ ] Extend `scripts/test-prompt-mode.sh` (or add `scripts/test-prompt-profile.sh`) with a check that
      a prompt-agent's `muxcode help` is permitted and a mutating verb is refused
- [ ] Include the negative control: the refusal assertion must fail if the deny path stops working
- [ ] Run through the run agent and record the row here

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
