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
- Glob matching uses the same `globMatch()` already in `bus/tools.go`

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

- `chainInstructionForRole()` must generate condition-aware instructions when conditions are present
- For simple unconditional chains (no conditions), instructions remain as-is
- For conditional chains, instructions describe the condition logic in natural language so the LLM can evaluate them (e.g. "If infra files changed (lib/**/*.ts), send to deploy; otherwise send to test")
- The LLM-based evaluation is best-effort — non-hook providers cannot guarantee deterministic condition evaluation

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
- Config validation rejects unknown condition types at load time

## Key files

| File | Changes |
|------|---------|
| `bus/profile.go` | Extend `ChainAction` with `Conditions` field, update `ResolveChain()` to evaluate conditions, support action arrays |
| `bus/conditions.go` | New file — condition evaluation engine: `EvaluateConditions()`, `changedFiles()`, `branchName()`, per-type evaluators |
| `bus/hook.go` | Update `ProcessBashHook()` to pass context (changed files, branch) to `ResolveChain()` |
| `bus/provider.go` | Update `chainInstructionForRole()` to generate condition-aware natural-language instructions |
| `bus/subscribe.go` | Add `Conditions` field to `Subscription`, evaluate in `MatchSubscriptions()` |
| `cmd/chain.go` | Add `--verbose`, `--files`, `--branch` flags |
| `bus/conditions_test.go` | Unit tests for condition evaluation |
| `bus/profile_test.go` | Tests for array-form chain actions with conditions |

## Non-goals

- **Pipeline definitions** — that's a separate backlog item; conditional chains extend the existing chain system, not replace it
- **Shell execution in conditions** — conditions must be pure data evaluation, no arbitrary shell commands
- **Condition composition (OR/NOT)** — start with AND-only; `files_not_match` and `branch_not_match` cover basic negation
- **Dynamic chain modification** — chains are resolved from config at evaluation time, not modified at runtime
- **Output capture for hook providers** — `output_contains` works with non-hook providers (LLM-evaluated) and the harness; hook-based providers don't capture stdout in the chain path

## Implementation phases

### Phase 1: Core condition engine

- Add `Conditions` map to `ChainAction` struct
- Implement `bus/conditions.go` with `EvaluateConditions()` and per-type evaluators
- Support `files_match`, `files_not_match`, `branch_match`, `branch_not_match`
- Update `ResolveChain()` to accept evaluation context and check conditions
- Add `--verbose`, `--files`, `--branch` to chain CLI

### Phase 2: Action arrays

- Support `on_success`/`on_failure`/`on_unknown` as either single action or array
- Custom JSON unmarshaling for backward-compatible parsing
- First-match evaluation with unconditional fallback

### Phase 3: Non-hook provider instructions

- Update `chainInstructionForRole()` to emit condition-aware natural-language instructions
- Test with OpenCode and Codex agents

### Phase 4: Extended conditions and subscriptions

- Add `env_set`, `env_equals`, `exit_code`, `output_contains` condition types
- Add conditions to subscription system
- Expand message templates with `${branch}` and `${changed_files}`

## Status

Draft
