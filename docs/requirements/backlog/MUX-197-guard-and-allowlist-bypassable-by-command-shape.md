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
[MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md)).

### Why the PR #99 approach failed

A denylist over arbitrary shell has to recognise every spelling of a program. Each PR #99 review
round found one it missed: attached `env -S` options, `$'…'` escapes, tab separators, brace and glob
program words. A real shell parser is ruled out by the stdlib-only constraint, and a partial one is
the thing that kept failing.

## Requirements

### Acceptance criteria

- [ ] Every shape in both Observed tables has a test that asserts the chosen behaviour (blocked, or
      documented as out of scope with the reason)
- [ ] Each blocked shape has a negative control: the nearest legitimate command still passes
      (`gh issue list --state open`, `git status`, `cd tools/muxcode && ls`)
- [ ] The decision on the local-LLM allowlist is recorded with its cost — a plain-only allowlist
      removes pipes, redirects and `$` from local agents — and the user has chosen it
- [ ] `docs/hooks.md` and `docs/agents.md` Known-gaps notes are updated to match what shipped
- [ ] The guard's documented scope stays "delegation nudge"; no doc claims it is a security boundary

### Technical approach

Options to weigh in Phase 1 (not a decision):

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

### Phase 1: Pin and decide

- [ ] Add table-driven tests for every Observed shape, asserting today's (bypassed) behaviour, so the
      fix is visible as a diff in expectations
- [ ] Present options A–D to the user with the cost of each; record the decision here

### Phase 2: Guard

- [ ] Implement the chosen guard option for edit and plan
- [ ] Negative controls for every refused shape

### Phase 3: Allowlist

- [ ] Implement the chosen allowlist option in `bus/tools.go` and `harness/tools.go` alike
- [ ] Negative controls: existing local-agent sequences (definition bodies) still pass

### Phase 4: Integration test

- [ ] Create `scripts/test-guard-command-shape.sh`: feed each Observed shape to `muxcode hook guard`
      as edit and as plan, and assert the chosen verdict
- [ ] Assert the negative controls pass the guard for the same roles
- [ ] Run the local-LLM executor's allowlist over both tables and assert the chosen verdict
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and verify all checks pass

## Related

- [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) — a role boundary an agent can ignore
  is not a boundary; the same class, for authoring
- [Hooks — hook guard](../../hooks.md#hook-guard-edit-guard) — the matcher, the carve-out and the
  Known-gaps note
- [Agents — tool profiles](../../agents.md#tool-profiles) — the allowlist-gap note

## Status

Backlog
