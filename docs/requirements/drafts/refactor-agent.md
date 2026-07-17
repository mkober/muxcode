# Refactor Agent (F6 review ↔ refactor mode toggle)

## Context

MuxCode hosts alternate agent **modes** on shared windows: F2 toggles `edit ↔ auto`,
F1 toggles `plan ↔ research`. This feature adds a third such pair on **F6** — the
window that currently hosts `review` (window index 6). Pressing **F6** while on
window 6 cycles **review ↔ refactor**, exactly as F1 cycles plan ↔ research.

The new **`refactor`** agent is a strict, aggressive, **read-only** code reviewer
whose single job is to surface code that is **messy, verbose, wasteful, or should
otherwise be refactored** — dead code, duplication, over-abstraction, needless
complexity, inefficient patterns, poor naming, oversized functions. It emits a
prioritized, opinionated findings list with `file:line` + a concrete recommendation
and reports to the edit agent, which applies any changes.

It shares the read-only tool-profile shape of the existing `review` agent (see
[`agents/code-reviewer.md`](../../../agents/code-reviewer.md)) and the mode-role
conventions of the existing `research` agent (see
[`agents/code-researcher.md`](../../../agents/code-researcher.md)).

### Decisions already made (do not re-litigate)

- **Read-only reviewer.** Same tool-profile shape as `review`: it READS code and
  REPORTS findings to edit, which applies them. NO `Write`/`Edit`; never authors or
  edits files, including via the shell.
- **Default model: Grok 4.5 on the OpenCode CLI**, provider-prefixed ID
  **`opencode-go/grok-4.5`** (confirmed in the OpenCode catalog under the `opencode-go`
  provider). On-demand/manual, so Grok's tight rate limit (80 req/5hr) is a non-issue.
- **Not chained. Manual/on-demand only.** Never auto-fired by any event chain;
  excluded from AutoCC. Triggered by an explicit
  `muxcode send refactor refactor "review X for refactor opportunities"` from edit,
  or by switching to F6 and driving it directly.

### Related

| Reference | Role |
|-----------|------|
| [`agents/code-reviewer.md`](../../../agents/code-reviewer.md) | Read-only reviewer persona + "Scope Boundaries" to mirror |
| [`agents/code-researcher.md`](../../../agents/code-researcher.md) | Mode-role conventions: F-key documentation + "Chain exclusion" |
| `tools/muxcode/bus/mode.go` | `DefaultPlanModeCycleState` — the pattern to mirror for review |
| `tools/muxcode/bus/profile.go` | `ToolProfile["review"]`, `EventChains`, `AutoCC` |

## Requirements

### Acceptance criteria

- [ ] Pressing **F6** while on the review window cycles review ↔ refactor (swap-window).
- [ ] `muxcode mode cycle --window review`, `muxcode mode switch refactor --window review`,
  and `muxcode mode active --window review` all work.
- [ ] The refactor agent launches on the **OpenCode CLI with Grok 4.5** by default.
- [ ] refactor is **read-only**: no `Write`/`Edit`; it reports findings and never edits
  files (not even via `sed -i`/`tee`/heredoc/redirection into the project tree).
- [ ] refactor is **excluded from all event chains** and from **AutoCC** (never auto-fired).
- [ ] refactor is **manually triggerable**: `muxcode send refactor refactor "..."` reaches
  it and it replies to edit with a prioritized findings report.
- [ ] `muxcode reload refactor --cli <x> --model <y>` works (i.e. `IsKnownRole("refactor")`
  is true).
- [ ] Findings are strict/aggressive and focused on messy/verbose/wasteful/refactorable
  code, each with `file:line` + a concrete recommendation.

### Technical approach

All touchpoints below were confirmed against the working tree. Line numbers are
approximate anchors, not exact.

#### Role registration

- [ ] `tools/muxcode/bus/config.go` — `KnownRoles` (~L13): append `"refactor"`
  (else `IsKnownRole("refactor")` is false and `muxcode reload refactor` is rejected).
- [ ] `tools/muxcode/bus/config.go` — `modeRoles` map (~L505): add
  `"refactor": "refactor"` (its own hold-window name) so `WindowForRole` / `PaneTarget`
  route `muxcode send refactor ...` to the right pane. Pattern: the `research` / `auto`
  entries.

#### Agent definition

- [ ] `tools/muxcode/bus/launch.go` — `AgentFileName` (~L46): add a `"refactor"` case →
  `"code-refactorer"`.
- [ ] `tools/muxcode/bus/launch.go` — `InlineFallbackPrompt` (~L245): add a `"refactor"`
  case.
- [ ] New file `agents/code-refactorer.md` — one-line `description:` frontmatter + body.
  Model the **read-only reviewer** persona on `agents/code-reviewer.md` (esp. its
  "## Scope Boundaries": review/report, never author; delegate all fixes to edit).
  Model the **mode-role** conventions on `agents/code-researcher.md` (F-key
  documentation line + a "## Chain exclusion" section — copy both). The body encodes:
  the aggressive-refactor focus (messy / verbose / wasteful / duplicated /
  over-abstracted code), prioritized findings with `file:line` + recommendation, and
  reporting to edit.

#### Tool profile (read-only)

- [ ] `tools/muxcode/bus/profile.go` — add `ToolProfile["refactor"]` mirroring
  `"review"` (~L620): `Include: ["bus","readonly","common"]`, `CdPrefix: true`, the same
  read-only git/read tools, and **NO** `Write`/`Edit`. (`"readonly"` group =
  Read/Glob/Grep.)

#### Model + CLI defaults

- [ ] `tools/muxcode/bus/provider.go` — `roleDefaultCLI` (~L119): add `"refactor"` to
  the opencode list so it defaults to the OpenCode CLI.
- [ ] `tools/muxcode/bus/launch.go` — `RoleClaudeModelDefault` (~L206): add `"refactor"`
  to the `claude-opus-4-8` case (Claude fallback if ever run on the Claude CLI; the
  `default: return ""` branch means an unlisted role gets no model default, so an
  explicit case is needed).
- [ ] Set the default OpenCode model for `refactor` to **`opencode-go/grok-4.5`**
  (find/extend the OpenCode model-default function, e.g. `RoleOpenCodeModelDefault`).
- [ ] Confirm env overrides `MUXCODE_REFACTOR_CLI` / `MUXCODE_REFACTOR_MODEL` resolve via
  the generic `default:` branches (no per-role code expected).

#### Mode-cycle wiring (the F6 toggle)

- [ ] `tools/muxcode/bus/mode.go` — add `DefaultReviewModeCycleState()` mirroring
  `DefaultPlanModeCycleState` (~L46): agents =
  `{index 0, mode "review", role "review", holdWindow ""}`,
  `{index 1, mode "refactor", role "refactor", holdWindow "refactor"}`.
- [ ] `tools/muxcode/bus/mode.go` — extend `ReadModeCycleState`'s hardcoded fallback
  switch (~L64) to special-case `"review"` (else a resumed session with no state file
  errors "no mode cycle for window review").
- [ ] `tools/muxcode/bus/launcher.go` — `LaunchSession` (~L198): add a third seed block
  guarded on `w == "review"` that writes `DefaultReviewModeCycleState()`, mirroring the
  existing plan block.
- [ ] `config/tmux.conf` (~L4): add the F6 `if-shell` binding so pressing F6 on window
  index 6 runs `muxcode mode cycle --window review` (mirror the F1/F2 bindings). **F6
  toggle only — no `bind f` prefix shortcut** (decided).

#### Exclusion (automatic — verify, don't add)

- [ ] Verify `refactor` has **no** key in `EventChains` (`profile.go` ~L847) and never
  appears as a `SendTo` target → never auto-triggered (like `research`).
- [ ] Verify `AutoCC` (`profile.go` ~L974) keeps `refactor` **off** the list (silent by
  default).
- [ ] Confirm no `SendPolicy` entry is needed (it is not a chain step).

#### No change needed (verified)

- `provider_options.go` `WindowFKey` computes the F-key by index at runtime, so the
  swap-window mechanism keeps "F6 = whatever occupies slot 6" automatically.
- `HasConsoleView` / `IsSplitLeft` already include `"review"` (the host window); the
  hold window's console is set up by `modeCreateAgent`.

### Key files

| File | Change |
|------|--------|
| `tools/muxcode/bus/config.go` | `KnownRoles` += `"refactor"`; `modeRoles` += `"refactor": "refactor"` |
| `tools/muxcode/bus/launch.go` | `AgentFileName` + `InlineFallbackPrompt` + `RoleClaudeModelDefault` cases |
| `agents/code-refactorer.md` | New — read-only aggressive-refactor reviewer persona (F6, chain-excluded) |
| `tools/muxcode/bus/profile.go` | `ToolProfile["refactor"]` mirroring `"review"`; verify chain/AutoCC exclusion |
| `tools/muxcode/bus/provider.go` | `roleDefaultCLI` += `"refactor"` (OpenCode); OpenCode model default = `opencode-go/grok-4.5` |
| `tools/muxcode/bus/mode.go` | `DefaultReviewModeCycleState()` + `ReadModeCycleState` fallback for `"review"` |
| `tools/muxcode/bus/launcher.go` | `LaunchSession` seed block for `w == "review"` |
| `config/tmux.conf` | F6 `if-shell` binding (F6 toggle only — no `bind f` shortcut) |
| `scripts/test-refactor-mode.sh` | New — integration test (Phase 5) |

## Implementation

### Phase 1: Role registration

- [ ] `KnownRoles` += `"refactor"` in `config.go`.
- [ ] `modeRoles` += `"refactor": "refactor"` in `config.go`.
- [ ] `AgentFileName` `"refactor"` → `"code-refactorer"` in `launch.go`.
- [ ] `InlineFallbackPrompt` `"refactor"` case in `launch.go`.
- [ ] Verify `IsKnownRole("refactor")` is true and `WindowForRole("refactor")` /
  `PaneTarget` route correctly.

### Phase 2: Agent definition

- [ ] Create `agents/code-refactorer.md` with one-line `description:` frontmatter.
- [ ] Persona: strict/aggressive refactor reviewer — messy, verbose, wasteful,
  duplicated, over-abstracted, needlessly complex, poorly named, oversized code.
- [ ] Output format: prioritized findings, each with `file:line` + concrete
  recommendation; reports to edit via `muxcode send edit ... --type response`.
- [ ] Include a "## Scope Boundaries" section (mirror `code-reviewer.md`): review/report,
  never author; no file authoring via the shell; delegate all fixes to edit.
- [ ] Include a "## Chain exclusion" section (mirror `code-researcher.md`): responds only
  to direct inbox messages; not in any chain; not in AutoCC.
- [ ] Document that it lives on the **F6** window (toggled via F6 when on the review
  window).

### Phase 3: Tool profile + model/CLI defaults

- [ ] `ToolProfile["refactor"]` in `profile.go` mirroring `"review"` (read-only, no
  Write/Edit, `CdPrefix: true`, `Include: ["bus","readonly","common"]`).
- [ ] `roleDefaultCLI` += `"refactor"` (OpenCode) in `provider.go`.
- [ ] `RoleClaudeModelDefault` `"refactor"` → `claude-opus-4-8` case in `launch.go`.
- [ ] OpenCode model default for `refactor` = **`opencode-go/grok-4.5`** (extend
  `RoleOpenCodeModelDefault` or equivalent).
- [ ] Verify `MUXCODE_REFACTOR_CLI` / `MUXCODE_REFACTOR_MODEL` overrides resolve via the
  generic branches.

### Phase 4: Mode-cycle wiring (F6 toggle)

- [ ] `DefaultReviewModeCycleState()` in `mode.go` (review index 0, refactor index 1).
- [ ] `ReadModeCycleState` fallback switch special-cases `"review"` in `mode.go`.
- [ ] `LaunchSession` seed block for `w == "review"` in `launcher.go`.
- [ ] F6 `if-shell` binding in `config/tmux.conf` (mirror F1/F2). **F6 toggle only — no
  `bind f` prefix shortcut** (decided).
- [ ] Verify chain exclusion: no `EventChains` key, no `SendTo` target, off `AutoCC`.

### Phase 5: Integration test (required)

Create `scripts/test-refactor-mode.sh` (`set -euo pipefail`) exercising the feature
end-to-end against a running session. Document what requires a **live session** vs what
is asserted **offline**, with graceful skips when a session is absent.

- [ ] Create `scripts/test-refactor-mode.sh`.
- [ ] Assert `muxcode mode cycle --window review` toggles review ↔ refactor (and F6
  binding drives the same cycle).
- [ ] Assert `muxcode mode switch refactor --window review` and
  `muxcode mode active --window review` report the expected active role.
- [ ] Assert the refactor agent launches on the **OpenCode CLI with Grok 4.5** (inspect
  resolved CLI/model).
- [ ] Send `muxcode send refactor refactor "review <target> for refactor opportunities"`
  → assert a **read-only** prioritized findings report is returned to edit (no file
  writes occurred).
- [ ] Assert **no chain auto-trigger** fires from a refactor run (refactor is not a chain
  step and not in AutoCC).
- [ ] Assert `muxcode reload refactor --cli <x> --model <y>` succeeds
  (`IsKnownRole("refactor")` true).
- [ ] Run the script and verify all checks pass.

## Open items

- [x] ~~Confirm the exact provider-prefixed OpenCode model ID for Grok 4.5.~~
  **Confirmed: `opencode-go/grok-4.5`** (OpenCode catalog, `opencode-go` provider).
- [x] ~~Confirm whether a `bind f` prefix shortcut is wanted in addition to the F6
  toggle.~~ **Decided: no — F6 toggle only, no `bind f` shortcut.**

Both open items resolved.

## Status

Draft
