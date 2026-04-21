# Conditional chains

## Purpose

Event chains currently support only outcome-based dispatch — success goes to one target, failure to another. Real workflows need richer conditions: skip deploy verification if no infra files changed, route test failures differently based on error type, gate review on branch name patterns. This feature extends the `EventChain` system with a condition expression language that evaluates at chain resolution time, enabling smarter automation without requiring pipeline definitions or custom hook scripts.

## Context

### Current state

The chain system (`bus/profile.go`) defines three event chains:

| Event | OnSuccess | OnFailure |
|-------|-----------|-----------|
| build | send to test | send to edit |
| test | send to review | send to edit |
| deploy | send to verify | send to edit |

Each `ChainAction` is a static target — no conditions beyond the exit code outcome (success/failure/unknown). Message templates support only `${exit_code}` and `${command}` substitution.

For hook-based providers (Claude Code), chains fire from bash hooks via `ProcessBashHook()`. For non-hook providers (OpenCode, Codex), chains are injected as prompt instructions via `chainInstructionForRole()` in `SendWakeUp()`.

The subscription system (`bus/subscribe.go`) provides fan-out notifications but has no conditions beyond event+outcome matching.

### What this changes

- Adds a `conditions` field to `ChainAction` with a small expression language
- Conditions can inspect: changed files, branch name, exit code, command output, environment
- Multiple chain actions per outcome (first matching condition wins)
- Backward compatible — chains with no conditions behave exactly as today

## Requirements

### Condition expressions

- Each `ChainAction` may have a `conditions` field containing one or more condition expressions
- Conditions are evaluated as a logical AND — all must be true for the action to fire
- If a `ChainAction` has no conditions, it fires unconditionally (backward compatible)
- Condition evaluation must be fast — no shell exec, no network calls

### Supported condition types

| Condition | Field | Description |
|-----------|-------|-------------|
| `files_match` | glob pattern | At least one changed file matches the glob (e.g. `"lib/**/*.ts"`, `"cdk.json"`) |
| `files_not_match` | glob pattern | No changed file matches the glob |
| `branch_match` | regex pattern | Current branch name matches the regex (e.g. `"^feat/"`, `"^main$"`) |
| `branch_not_match` | regex pattern | Current branch name does not match |
| `env_set` | env var name | Environment variable is set and non-empty |
| `env_equals` | `{name, value}` | Environment variable equals a specific value |
| `output_contains` | substring | Command stdout/stderr contains the substring |
| `exit_code` | integer | Numeric exit code matches (more specific than success/failure) |

### Chain action lists

- `on_success`, `on_failure`, and `on_unknown` may be either a single `ChainAction` (current behavior) or an array of `ChainAction` objects
- When an array, actions are evaluated in order — first action whose conditions all pass is fired
- Only one action fires per outcome (first-match wins, not fan-out)
- An action with no conditions acts as a default/fallback — place it last in the array

### Changed files detection

- Changed files are determined by `git diff --name-only HEAD` at chain evaluation time
- File list is cached per chain evaluation (not re-computed per condition)
- If git is unavailable or fails, `files_match` conditions evaluate to false and `files_not_match` to true
- Glob matching uses `filepath.Match()` from the Go stdlib — **not** `globMatch()` from `bus/tools.go`, which treats `*` as crossing directory separators (designed for `--allowedTools` patterns). File path matching needs standard semantics where `*` matches within a single directory and `**` crosses directories. Implement a `fileGlobMatch()` helper in `bus/conditions.go` that wraps `filepath.Match()` with `**` support (split pattern on `**`, match segments recursively)

### Configuration format

Conditions are added to `ChainAction` in the profile config:

```json
{
  "event_chains": {
    "build": {
      "on_success": [
        {
          "send_to": "deploy",
          "action": "deploy",
          "message": "Build succeeded — infra files changed, run deploy diff",
          "type": "request",
          "conditions": {
            "files_match": "lib/**/*.ts",
            "branch_match": "^main$"
          }
        },
        {
          "send_to": "test",
          "action": "test",
          "message": "Build succeeded — run tests. Exit code: ${exit_code}",
          "type": "request"
        }
      ],
      "on_failure": {
        "send_to": "edit",
        "action": "notify",
        "message": "Build failed (${exit_code}): ${command}",
        "type": "request"
      }
    }
  }
}
```

### Message template expansion

- Extend `ExpandMessage()` to support additional variables:
  - `${branch}` — current branch name
  - `${changed_files}` — comma-separated list of changed files (max 10, truncated with `...`)
  - `${exit_code}` and `${command}` — already supported

### Non-hook provider support

- `chainInstructionForRole()` is currently a hardcoded switch statement (`build`→test, `test`→review) in `bus/provider.go`. It must be **rewritten** to dynamically generate instructions from `EventChains` config — iterating chain entries where the role matches `SendTo` of a prior chain or is the event source
- For simple unconditional chains (no conditions), generated instructions remain equivalent to the current hardcoded text
- For conditional chains, instructions describe the condition logic in natural language so the LLM can evaluate them (e.g. "If infra files changed (lib/**/*.ts), send to deploy; otherwise send to test")
- When an outcome has multiple actions (array), the generated instruction describes each condition and its target in order, with the unconditional fallback last
- The LLM-based evaluation is best-effort — non-hook providers cannot guarantee deterministic condition evaluation
- New function `buildChainInstruction(role string, cfg *MuxcodeConfig) string` replaces the hardcoded switch, called by `chainInstructionForRole()` which becomes a thin wrapper

### CLI interface

- `muxcode chain <event> <outcome> [--dry-run]` — existing behavior, now evaluates conditions
- `muxcode chain <event> <outcome> --dry-run --verbose` — shows condition evaluation details
- `muxcode chain <event> <outcome> --files "file1.ts,file2.ts"` — override changed files (for testing)
- `muxcode chain <event> <outcome> --branch "feat/xyz"` — override branch name (for testing)

### Subscription conditions

- Subscriptions (`bus/subscribe.go`) gain the same condition system
- A subscription can include `conditions` that must all pass (in addition to event+outcome matching) for the subscription to fire
- This allows subscribing to "test failure, but only when Python files changed"

## Acceptance criteria

- Chains with no conditions behave identically to current behavior (backward compatible)
- A chain with `files_match: "lib/**/*.ts"` only fires when at least one changed file matches
- A chain with `branch_match: "^main$"` only fires when on the main branch
- Multiple actions per outcome evaluate in order — first matching fires, rest skipped
- An unconditional action at the end of an array acts as the default fallback
- `--dry-run` shows which action would fire and why (condition evaluation results)
- `--dry-run --verbose` shows all conditions checked and their pass/fail status
- `--files` and `--branch` overrides work for testing without actually being on that branch
- Non-hook providers receive natural-language condition instructions in `SendWakeUp()` prompts
- Changed files are cached within a single chain evaluation (only one `git diff` call)
- Config validation warns on unknown condition types at load time (stderr warning, not hard error)
- `output_contains` evaluates deterministically for hook providers (via `ToolEvent.ToolResult`), best-effort for non-hook providers (LLM-evaluated), and is marked "unevaluable" in `--dry-run --verbose`
- `files_match` uses `filepath.Match` semantics (not `globMatch`) — `*` matches within directory, `**` crosses directories
- `--verbose` shows per-condition pass/fail with human-readable detail for each condition evaluated

## Key files

| File | Changes |
|------|---------|
| `bus/profile.go` | Extend `ChainAction` with `Conditions` field, add `ChainActions` type with custom JSON marshal/unmarshal, update `ResolveChain()` signature to accept `*ChainContext`, add `ValidateConfig()` |
| `bus/conditions.go` | New file — `ChainContext` struct, `EvaluateConditions()`, `ConditionResult` (per-condition pass/fail detail), `fileGlobMatch()`, `changedFiles()`, `branchName()`, per-type evaluators |
| `bus/hook.go` | Update `ProcessBashHook()` to build `ChainContext` from `ToolEvent` fields and pass to `ResolveChain()` |
| `bus/provider.go` | Rewrite `chainInstructionForRole()` to dynamically generate instructions from `EventChains` config via new `buildChainInstruction()` |
| `bus/subscribe.go` | Add `Conditions` field to `Subscription`, evaluate in `MatchSubscriptions()` |
| `cmd/chain.go` | Add `--verbose`, `--files`, `--branch`, `--output` flags; use `ConditionResult` for verbose output |
| `bus/conditions_test.go` | Unit tests for condition evaluation, `fileGlobMatch()`, `ChainContext` building |
| `bus/profile_test.go` | Tests for `ChainActions` unmarshal (single object and array forms), `ResolveChain()` with conditions |
| `bus/subscribe_test.go` | Tests for subscription conditions in `MatchSubscriptions()` |
| `cmd/chain_test.go` | Tests for `--verbose`, `--files`, `--branch` CLI flags |

## Non-goals

- **Pipeline definitions** — that's a separate backlog item; conditional chains extend the existing chain system, not replace it
- **Shell execution in conditions** — conditions must be pure data evaluation, no arbitrary shell commands
- **Condition composition (OR/NOT)** — start with AND-only; `files_not_match` and `branch_not_match` cover basic negation
- **Dynamic chain modification** — chains are resolved from config at evaluation time, not modified at runtime
- **Output capture**: `output_contains` has different fidelity across providers — hook-based providers can evaluate it deterministically via `ToolEvent.ToolResult` (stdout is available in `ProcessBashHook()`); non-hook providers evaluate it via LLM (best-effort); CLI `--dry-run` cannot evaluate it without actual output (skipped, shown as "unevaluable" in `--verbose`)

## Implementation phases

### Phase 1: Core condition engine

New types in `bus/conditions.go`:

```go
// ChainContext holds the evaluation context for condition expressions.
// Built by ProcessBashHook() from ToolEvent fields, or by the CLI from flags.
// When nil is passed to ResolveChain(), conditions are skipped (backward compatible).
type ChainContext struct {
    ChangedFiles []string // cached from git diff --name-only HEAD
    Branch       string   // current branch (git rev-parse --abbrev-ref HEAD)
    ExitCode     int      // numeric exit code from ToolEvent
    Command      string   // command string from ToolEvent.ToolInput.Command
    Output       string   // stdout/stderr from ToolEvent.ToolResult (for output_contains)
}

// ConditionResult records per-condition evaluation details for --verbose output.
type ConditionResult struct {
    Type    string // e.g. "files_match", "branch_match"
    Pattern string // the pattern or value being tested
    Passed  bool
    Detail  string // human-readable explanation (e.g. "matched: lib/constructs/foo.ts")
}
```

Updated signature: `ResolveChain(eventType, outcome string, ctx *ChainContext) *ChainAction`

Implementation steps:

- Add `Conditions map[string]any` to `ChainAction` struct
- Implement `bus/conditions.go` with `EvaluateConditions()` returning `(bool, []ConditionResult)`
- Implement `fileGlobMatch(pattern, path string) bool` — wraps `filepath.Match()` with `**` support (split on `**`, match segments recursively). Does NOT use `globMatch()` from `bus/tools.go` (wrong semantics for file paths)
- Support `files_match`, `files_not_match`, `branch_match`, `branch_not_match`
- Update `ResolveChain()` to accept `*ChainContext` and check conditions; nil context skips conditions
- Update `ProcessBashHook()` to build `ChainContext` from `ToolEvent` fields (`ExitCode`, `ToolInput.Command`, `ToolResult`) + lazy `git diff --name-only HEAD` call (cached in context)
- Add `ValidateConfig()` to `bus/profile.go` — iterates all `EventChains`, validates condition keys against known set (`files_match`, `files_not_match`, `branch_match`, `branch_not_match`, `env_set`, `env_equals`, `output_contains`, `exit_code`). Emits warning to stderr for unknown keys (not hard error — forward compatibility). Called from `LoadConfig()`
- Add `--verbose`, `--files`, `--branch`, `--output` to chain CLI; `--verbose` prints `ConditionResult` details for each condition evaluated

### Phase 2: Action arrays

New type in `bus/profile.go`:

```go
// ChainActions wraps []ChainAction with custom JSON marshal/unmarshal
// to support both single-object and array forms in config.
type ChainActions []ChainAction

func (ca *ChainActions) UnmarshalJSON(data []byte) error {
    // Try array first
    var arr []ChainAction
    if err := json.Unmarshal(data, &arr); err == nil {
        *ca = arr
        return nil
    }
    // Fall back to single object
    var single ChainAction
    if err := json.Unmarshal(data, &single); err != nil {
        return err
    }
    *ca = ChainActions{single}
    return nil
}

func (ca ChainActions) MarshalJSON() ([]byte, error) {
    // Preserve single-object format when only one action (config readability)
    if len(ca) == 1 {
        return json.Marshal(ca[0])
    }
    return json.Marshal([]ChainAction(ca))
}
```

`EventChain` fields change from `*ChainAction` to `ChainActions`:

```go
type EventChain struct {
    OnSuccess       ChainActions `json:"on_success,omitempty"`
    OnFailure       ChainActions `json:"on_failure,omitempty"`
    OnUnknown       ChainActions `json:"on_unknown,omitempty"`
    NotifyAnalyst   bool         `json:"notify_analyst"`
    NotifyAnalystOn []string     `json:"notify_analyst_on,omitempty"`
}
```

Implementation steps:

- Add `ChainActions` type with custom marshal/unmarshal
- Change `EventChain` fields from `*ChainAction` to `ChainActions`
- Update `ResolveChain()` to iterate `ChainActions` slice, evaluate conditions for each, return first match
- Update all callers of `ResolveChain()` — currently returns `*ChainAction` (nil check), now returns `*ChainAction` (first match from slice, nil if empty or no conditions pass)
- Update `DefaultConfig()` chain definitions to use `ChainActions{...}` syntax
- First-match evaluation with unconditional fallback (action with no conditions matches always)

### Phase 3: Non-hook provider instructions

`chainInstructionForRole()` is currently a hardcoded switch in `bus/provider.go` (build→test, test→review). It must be rewritten to generate instructions dynamically from config.

New function:

```go
// buildChainInstruction generates a natural-language chain instruction
// for a role by reading EventChains config. Returns "" if the role has
// no chain responsibilities.
func buildChainInstruction(role string, cfg *MuxcodeConfig) string
```


Implementation steps:

- Add `buildChainInstruction()` that iterates `cfg.EventChains` to find chains where the role is the event source (e.g. "build" role owns the "build" event chain)
- For unconditional single-action chains: generate simple instruction ("on SUCCESS, send to test")
- For conditional action arrays: generate condition descriptions in order ("if infra files changed (lib/**/*.ts), send to deploy; otherwise send to test")
- `chainInstructionForRole()` becomes a thin wrapper: calls `buildChainInstruction(role, Config())`
- Test with OpenCode and Codex agents — verify generated instructions are parseable and actionable by LLMs
- Test backward compatibility — unconditional chains produce equivalent text to the current hardcoded switch

### Phase 4: Extended conditions and subscriptions

- Add `env_set`, `env_equals`, `exit_code`, `output_contains` condition types
- Add conditions to subscription system
- Expand message templates with `${branch}` and `${changed_files}`

## Implementation readiness review

Reviewed 2026-04-20 against current codebase. Findings below.

### Accurate references (verified)

- `ChainAction` struct in `bus/profile.go` (lines 42-48): has `SendTo`, `Action`, `Message`, `Type` — matches spec
- `EventChain` struct (lines 34-40): has `OnSuccess`, `OnFailure`, `OnUnknown` as `*ChainAction` pointers — matches spec
- `ResolveChain()` in `bus/profile.go` (line 274): simple switch on outcome, returns single `*ChainAction` — matches spec's "current state"
- `ExpandMessage()` in `bus/profile.go` (line 330): supports `${exit_code}` and `${command}` only — matches spec
- `globMatch()` in `bus/tools.go` (line 220): exists and is exported within package — spec correctly references it
- `chainInstructionForRole()` in `bus/provider.go` (line 113): hardcoded `build`→test, `test`→review switch — matches spec
- `ProcessBashHook()` in `bus/hook.go` (line 511): exists — matches spec
- `Subscription` struct in `bus/subscribe.go` (line 14): has `Event`, `Outcome`, `Notify`, `Action`, `Message`, `Enabled` — matches spec
- `MatchSubscriptions()` / `FireSubscriptions()` / `ExpandSubscriptionMessage()`: all exist — matches spec
- Config loaded from `.muxcode/muxcode.json` and `~/.config/muxcode/muxcode.json` — spec's JSON config format is correct
- Chain CLI in `cmd/chain.go`: has `--exit-code`, `--command`, `--no-notify`, `--dry-run` — matches spec

### Issues found

#### 1. `globMatch()` is unexported (lowercase)

**Severity**: blocker for Phase 1

The spec says "Glob matching uses the same `globMatch()` already in `bus/tools.go`" — but `globMatch` is **unexported** (lowercase `g`). Since `bus/conditions.go` will be in the same package, this actually works fine. However, the spec should note this is an internal function, not a public API. If conditions are ever evaluated outside the `bus` package, it would need exporting.

**Action**: no code change needed, but add a note that `globMatch` is package-internal.

#### 2. `files_match` glob vs `globMatch` semantics mismatch

**Severity**: design clarification needed

`globMatch()` in `bus/tools.go` is designed for Claude Code `--allowedTools` patterns where `*` matches any character including `/`. Standard file glob patterns (like `lib/**/*.ts`) expect `**` to match directory separators and `*` to not match them. The spec uses `lib/**/*.ts` as an example, but `globMatch()` treats `*` as matching everything — so `lib/*.ts` would already match `lib/constructs/foo.ts`, which is probably not the intent.

**Action**: Phase 1 should use `filepath.Match()` or `doublestar` pattern matching for `files_match`/`files_not_match` (not `globMatch()`), or document that `**` is not needed because `*` already crosses directories. This is a design decision that should be resolved before implementation.

#### 3. `on_success` type change requires custom JSON unmarshaling

**Severity**: implementation detail missing from Phase 2

`EventChain.OnSuccess` is currently `*ChainAction` (pointer to single struct). Changing it to accept either a single object or an array requires a custom `UnmarshalJSON` method. The spec mentions "Custom JSON unmarshaling for backward-compatible parsing" in Phase 2 but doesn't specify the approach.

**Action**: add implementation detail — recommend a `ChainActions` type that wraps `[]ChainAction` with custom unmarshal that handles both single-object and array forms. Also needs custom marshal to preserve single-object format when only one action exists (for config readability).

#### 4. `chainInstructionForRole` is hardcoded — not config-driven

**Severity**: design gap

The spec says "Update `chainInstructionForRole()` to emit condition-aware natural-language instructions" (Phase 3), but the current function is a hardcoded switch statement, not driven by `EventChains` config. For conditional chains to work in non-hook providers, `chainInstructionForRole()` must be rewritten to read from `EventChains` config and generate instructions dynamically.

**Action**: Phase 3 should specify that `chainInstructionForRole()` is rewritten to iterate `EventChains` config entries for the given role, generating natural-language instructions from the chain actions and conditions. This is more complex than the spec implies — it needs to generate condition descriptions ("if infra files changed"), action targets, and fallback logic in prose.

#### 5. Missing: how `ProcessBashHook()` passes changed files context

**Severity**: implementation detail missing from Phase 1

The spec says "Update `ProcessBashHook()` to pass context (changed files, branch) to `ResolveChain()`" but doesn't specify the mechanism. Currently `ResolveChain(eventType, outcome string) *ChainAction` takes only two strings. With conditions, it needs an evaluation context.

**Action**: add to Phase 1 — define a `ChainContext` struct:

```go
type ChainContext struct {
    ChangedFiles []string  // cached git diff --name-only
    Branch       string    // current branch
    ExitCode     int       // numeric exit code
    Command      string    // command that was run
    Output       string    // stdout/stderr (for output_contains)
}
```

`ResolveChain()` signature changes to `ResolveChain(eventType, outcome string, ctx *ChainContext) *ChainAction`. When `ctx` is nil, conditions are skipped (backward compatible).

#### 6. Missing: `output_contains` availability caveat

**Severity**: non-goals section incomplete

The non-goals section says "hook-based providers don't capture stdout in the chain path" — but the spec still lists `output_contains` as a supported condition type (Phase 4). The spec should clarify:
- For hook providers: `ProcessBashHook()` receives the `ToolEvent` which includes `tool_result` (stdout). So `output_contains` COULD work for hook providers — the spec incorrectly says it can't.
- For non-hook providers: the LLM evaluates conditions, so `output_contains` is LLM-judged (best-effort).
- For CLI `--dry-run`: `output_contains` can't be evaluated without actual output.

**Action**: correct the non-goals section. `output_contains` works for hook providers (via `ToolEvent.ToolResult`), LLM-evaluated for non-hook providers, and unevaluable in dry-run.

#### 7. Missing: config validation details

**Severity**: acceptance criteria gap

The acceptance criteria says "Config validation rejects unknown condition types at load time" but there's no implementation detail for this. Currently `LoadConfig()` just does `json.Unmarshal` — unknown fields are silently ignored.

**Action**: add to Phase 1 — after unmarshal, iterate all `EventChains` entries, validate condition keys against a known set. Emit warning to stderr for unknown conditions (not hard error — forward compatibility). Add a `ValidateConfig()` function to `bus/profile.go`.

#### 8. Missing: `cmd/chain.go` changes for `--verbose`

**Severity**: implementation detail missing

The spec says add `--verbose`, `--files`, `--branch` to chain CLI, but the current `cmd/chain.go` calls `ResolveChain()` and uses the result directly. With conditions, the dry-run path needs to show each condition's evaluation result. The CLI needs access to the evaluation details, not just the final result.

**Action**: `EvaluateConditions()` should return a `ConditionResult` struct with per-condition pass/fail details, not just a boolean. The CLI's `--verbose` flag prints these details.

#### 9. Missing: test file locations

**Severity**: minor

The spec lists `bus/conditions_test.go` and `bus/profile_test.go` but doesn't mention `cmd/chain_test.go` for the CLI flag tests, or `bus/subscribe_test.go` for subscription condition tests.

**Action**: add `cmd/chain_test.go` and updates to `bus/subscribe_test.go` to the key files table.

#### 10. Missing: `hook.go` — how `ResolveChain` is called

**Severity**: implementation clarity

The spec references `bus/hook.go` but doesn't explain the call path. Currently `ProcessBashHook()` calls `ResolveChain()` via `bus/hook.go` line ~570+. The chain resolution result feeds into `Send()`. With conditions, the hook needs to build `ChainContext` from the `ToolEvent` (which has `ExitCode`, `ToolInput.Command`, `ToolResult`) and `git diff`.

**Action**: add to Phase 1 — specify that `ProcessBashHook()` builds `ChainContext` from `ToolEvent` fields + lazy `git diff` call (cached).

### Readiness assessment

| Aspect | Status | Notes |
|--------|--------|-------|
| Problem definition | ✅ Ready | Clear, well-motivated |
| Current state analysis | ✅ Ready | Accurate against codebase |
| Condition types | ✅ Ready | Well-scoped, practical |
| Configuration format | ✅ Ready | JSON example is clear, backward compatible |
| Non-goals | ✅ Fixed | `output_contains` availability corrected (issue #6) |
| Key files | ✅ Fixed | Added `cmd/chain_test.go`, `bus/subscribe_test.go` (issue #9) |
| Phase 1 | ✅ Fixed | Added `ChainContext` struct, `fileGlobMatch()`, `ValidateConfig()`, `ConditionResult` (issues #2, #5, #7, #8) |
| Phase 2 | ✅ Fixed | Added `ChainActions` type with full marshal/unmarshal code (issue #3) |
| Phase 3 | ✅ Fixed | Specified `buildChainInstruction()` rewrite of hardcoded switch (issue #4) |
| Phase 4 | ✅ Ready | Well-defined scope |
| Acceptance criteria | ✅ Fixed | Added criteria for validation behavior, `output_contains` edge cases, glob semantics, verbose output |
| Backward compatibility | ✅ Ready | Clearly specified throughout |

**Verdict**: All 10 review issues addressed. Spec is **implementation-ready**.

## Status

In Progress
