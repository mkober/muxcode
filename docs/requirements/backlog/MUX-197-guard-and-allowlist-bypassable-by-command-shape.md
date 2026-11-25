# MUX-197: Guard Rules and the Local-LLM Allowlist Are Bypassable by Command Shape

**Tracking:** [mkober/muxcode#100](https://github.com/mkober/muxcode/issues/100)

The PreToolUse delegation guard matches a prohibited command only by **prefix of the first
statement**, after one leading `cd … &&` and any leading `VAR=value` assignments. A wrapper, a path to
the binary, a later statement, extra whitespace or an obfuscated program word all run a prohibited
command without matching a rule. The local-LLM executors' tool allowlist has the mirror-image gap: its
glob `*` spans `;`, `|`, `&&` and newlines, so an allowed prefix admits whatever follows it.

## Context

### Source and standard of evidence

Filed 2026-09-28 on the user's instruction relayed by edit (brief `/tmp/pr99-defect.md`). Found by
the PR #99 review loop (run `1790643708-80-pr-review-fix-ae14db99`; the review node failed on two
must-fix bypasses: whitespace and encoded attached `env` options). PR #99 tried a fix
(`bus/command_shape.go`: `plainCommand` plus a `mentionsProgram` denylist); every review round found a
new bypass, so the user narrowed PR #99 to its `gh issue` carve-out and filed the pre-existing class
here. Mechanism read by plan from code at `cc05693` plus the narrowed working tree.

Every shape below is **pre-existing on main** — none was introduced by the carve-out, whose own
admission test (`exceptApplies`) refuses all of them (see [Hooks](../../hooks.md#hook-guard-edit-guard)).

### Observed — guard bypasses (`checkAgainstRules`, `bus/hook.go`)

| Class | Shapes that run a prohibited command unmatched |
|-------|-----------------------------------------------|
| Wrapper | `env gh pr merge 1`, `command gh pr merge 1`, `timeout 30 gh pr merge 1`, `xargs gh pr merge < f`, `bash -c "gh pr merge 1"`, `env -S 'gh pr merge 1'`, `env -S$'\x67h\x20pr\x20merge\x201'` |
| Path to the binary | `/opt/homebrew/bin/gh pr merge 1` |
| Later statement | `ls; gh pr merge 1`, `(ls; gh pr merge 1)`, `X=$(gh pr merge 1) ls` |
| Whitespace | `gh<TAB>pr merge 1`; `git  commit -m x` misses plan's `git commit` prefix (edit's blanket `git ` prefix still catches it) |
| Obfuscated program word | `$'\x67h' pr merge 1`, `{gh,} pr merge 1`, `/usr/bin/g? pr merge 1` |

### Observed — allowlist bypasses (`isBashAllowed`, `bus/tools.go`, `harness/tools.go`)

| Pattern | Also admits |
|---------|-------------|
| `Bash(git status*)` (build, test, review profiles and others) | `git status; git push` |
| `Bash(gh issue list*)` (plan's profile, `bus/profile.go:647`) | `gh issue list; gh pr merge 1` |

### Mechanism — verified

| Fact | Where |
|------|-------|
| Delegation rules exist for **edit and plan only**; every other role has none to bypass | `guardRulesForRole`, `bus/hook.go:1006` |
| The matcher strips one `cd … &&` and leading assignments, then `strings.HasPrefix` per rule — no statement splitting, no wrapper or path handling, no whitespace normalization | `checkAgainstRules`, `bus/hook.go:1478` |
| The allowlist is a whole-line glob in which `*` matches any sequence, metacharacters included | `isBashAllowed` / `globMatch`, `bus/tools.go:203`, `harness/tools.go:196` |
| The allowlist is the only gate for the local-LLM executors — there is no PreToolUse guard on that road | `bus/executor.go:90`, `harness/executor.go:99` |
| The two modules share no code, so a fix to the allowlist belongs in both | `CLAUDE.md` (stdlib only, separate modules) |

### Blast radius

| Road | What still holds | What the bypass reaches |
|------|------------------|-------------------------|
| Bus-routed mutations | `CheckCommitAuthority` refuses a `commit` send from any role but edit; `CheckAtlassianAuthority` gates the `muxcode atlassian` writes; gate authority guards graph dispatches | — |
| Direct shell execution by edit or plan | nothing — the guard is the only check | `git push`, `gh pr merge`, a build, test or deploy the role is told to delegate |
| Local-LLM agents | nothing — the allowlist is the only check | anything appended to an allowed prefix |

The guard was designed as a **delegation nudge**, not a security boundary: it stops an agent that
reaches for the wrong command by habit. It does not stop one that works around it. The spec should
keep that scope explicit rather than promise a boundary static matching cannot deliver (compare
[MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md)).

### Why the PR #99 approach failed

A denylist over arbitrary shell has to recognise every spelling of a program. Each PR #99 review
round found one it missed: attached `env -S` options, `$'…'` escapes, tab separators, brace and glob
program words. A real shell parser is ruled out by the stdlib-only constraint, and a partial one is
the thing that kept failing.

## Decisions (user, 2026-11-24)

The user's explicit choices, relayed by edit. The spec was parked because its Phase 1 presented
options A–D and no decision was recorded; both are now made.

| # | Question | Decision |
|---|----------|----------|
| D1 | The delegation guard for edit and plan | **Option D — accept and document.** The guard keeps prefix matching and its shape gaps. It is a delegation nudge; the bus authority checks (`CheckCommitAuthority`, `CheckAtlassianAuthority`, gate authority) are the boundary. No guard matcher change ships |
| D2 | The local-LLM allowlist | **Option B — plain commands only.** `isBashAllowed` admits a line only when it is a plain command (the `exceptApplies` shape) that matches a pattern |

**Cost accepted with D1:** the direct-execution row of the blast radius stays open — an edit or plan
agent that works around the guard by command shape is not stopped by it. The docs say so.

**Cost accepted with D2:** local-LLM agents lose pipes, redirects, `;`, `&&` and `$` in Bash calls.
A definition body or harness prompt that tells a local agent to run a compound line has to be
rewritten to plain commands, in the same change.

## Requirements

### Acceptance criteria

- [ ] Every guard shape in the first Observed table has a test pinning today's behaviour (the
      prohibited command runs unmatched), named as the documented gap it is — so a later change to
      the matcher shows up as a diff in expectations, not as a surprise
- [ ] Every allowlist shape in the second Observed table is refused by `isBashAllowed` in **both**
      modules (`bus/tools.go`, `harness/tools.go`)
- [ ] Negative control for each refused allowlist shape: the nearest plain command still passes
      (`git status`, `git status --short`, `gh issue list --state open`)
- [ ] Negative control: every Bash line the local-agent definitions and harness prompts tell a
      local agent to run still passes the plain-only allowlist — found by reading
      `agents/harness/` and the harness prompt builder, each line listed in the test
- [ ] `docs/hooks.md` and `docs/agents.md`: the Known-gaps note states the guard's gaps as accepted
      (D1) and names the bus authority checks as the boundary; the Allowlist-gap note is replaced by
      the plain-only rule and what local agents lose (D2)
- [ ] The guard's documented scope stays "delegation nudge"; no doc claims it is a security boundary

### Technical approach

- **Guard (D1).** No matcher change. Phase 1's table-driven tests pin the gaps; Phase 3 documents
  them as accepted.
- **Allowlist (D2).** `isBashAllowed` first asks whether the line is plain — one statement, no
  `;`, `|`, `&`, redirect, `$`, backtick, subshell, brace or newline — and only then glob-matches
  it. The plain test is the one `exceptApplies` already applies in `bus/hook.go`; the harness is a
  separate module with no shared code, so it carries its own copy and a test asserting the two agree
  on one shared table of lines.

The options as weighed (D and B chosen):

| Option | Idea | Cost |
|--------|------|------|
| A. Allowlist-only for sensitive programs | For `git`, `gh` and deploy tools in edit/plan, admit only plain lines matching an explicit allowlist; refuse any non-plain line that names the program anywhere, or runs a wrapper, fail-closed | Refuses some legitimate compound read commands; the "names the program" test was PR #99's weakest point |
| B. Plain-only local-LLM allowlist | `isBashAllowed` admits only a plain line (the `exceptApplies` shape) that matches a pattern | Local agents lose pipes, redirects and `$`; needs the user's call |
| C. Refuse-unknown-shape in edit/plan | Any line with a metacharacter, wrapper or non-literal program word is refused outright, whatever it runs | Largest behaviour change for the orchestrator; may break the definitions' own sequences |
| D. Accept and document | Keep prefix matching, document the gaps, rely on role instructions plus the bus-level authority checks | The direct-execution row of the blast radius stays open |

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/hook.go` | `checkAgainstRules`, `exceptApplies`, `editGuardRules`, `planGuardRules` |
| `tools/muxcode/bus/tools.go` | `IsToolAllowed`, `isBashAllowed`, `globMatch` |
| `tools/muxcode-llm-harness/harness/tools.go` | The harness copy of the allowlist matcher |
| `tools/muxcode/bus/profile.go` | Role tool profiles (the allowlist patterns) |
| `tools/muxcode/bus/hook_test.go`, `bus/tools_test.go`, `harness/tools_test.go` | Tests |
| `docs/hooks.md`, `docs/agents.md` | Known-gaps notes |

## Implementation

### Phase 1: Pin

- [ ] Table-driven guard tests for every shape in the first Observed table, asserting today's
      behaviour (not matched) and labelled as accepted gaps (D1)
- [ ] Table-driven allowlist tests for both rows of the second Observed table, in both modules,
      asserting today's behaviour (admitted) — Phase 2 flips them in the same change, so the suite
      is never left red
- [ ] List every Bash line the local-agent definitions (`agents/harness/`) and the harness prompt
      builder tell a local agent to run; record here any that is not plain

### Phase 2: Plain-only allowlist (D2)

- [ ] `isBashAllowed` in `bus/tools.go` refuses a non-plain line before glob matching; Phase 1's
      allowlist expectations flip
- [ ] The same in `harness/tools.go`, with one shared table of lines asserted in both modules so the
      two copies cannot drift
- [ ] Rewrite any non-plain line found in Phase 1 to plain commands, in the same change
- [ ] Negative controls: `git status`, `git status --short` and `gh issue list --state open` pass;
      every listed definition line passes
- [ ] A refused line's tool result says why (not plain) and names the plain alternative, so a local
      model is not left retrying the same shape

### Phase 3: Docs (D1 and D2)

- [ ] `docs/hooks.md`: the Known-gaps note reads as an accepted limit — the shapes, the statement
      that the guard is a delegation nudge, and the bus authority checks as the boundary
- [ ] `docs/agents.md`: the Allowlist-gap note is replaced by the plain-only rule and its cost to
      local agents
- [ ] `CLAUDE.md`: one clause on the local-LLM allowlist being plain-only, if the harness paragraph
      is the place the next reader would look

### Phase 4: Integration test

- [ ] Create `scripts/test-guard-command-shape.sh` (hermetic, scratch bus, muxcode built from the
      tree): feed each guard shape to `muxcode hook guard` as edit and as plan and assert it is
      **allowed** — the accepted gap, pinned — with the plain prohibited command (`gh pr merge 1`,
      `git push`) denied beside it as the control showing the guard still works
- [ ] Run the local-LLM executor's allowlist over the second Observed table through the binary and
      assert each compound line is refused and each plain neighbour admitted
- [ ] Negative control: a build of a scratch copy with the plain test removed from `isBashAllowed`
      admits `git status; git push`, which the assertion above rejects
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record pass/fail counts in this spec

## Related

- [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) — a role boundary an agent can ignore
  is not a boundary; the same class, for authoring
- [Hooks — hook guard](../../hooks.md#hook-guard-edit-guard) — the matcher, the carve-out and the
  Known-gaps note
- [Agents — tool profiles](../../agents.md#tool-profiles) — the allowlist-gap note

## Notes

- The Mechanism table's line numbers are as filed (2026-09-28). On 2026-11-24 the functions stand at
  `guardRulesForRole` `bus/hook.go:1405`, `checkAgainstRules` `:1877`, `exceptApplies` `:1927`,
  `isBashAllowed` `bus/tools.go:203` and `harness/tools.go:196`; the Known-gaps notes are at
  `docs/hooks.md:137` and `docs/agents.md:607`.
- Whether any local-agent definition line is non-plain today was not checked at the decision;
  Phase 1's third step establishes it.

## Status

Backlog — unparked 2026-11-24 on the user's decisions (D1 = D, accept and document; D2 = B,
plain-only allowlist). Not started.
