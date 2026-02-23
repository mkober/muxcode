# Plan: Post-deploy verification agent

## Context

After a successful `cdk deploy` (or other IaC apply), there's no automated verification that the deployed resources are healthy. This adds a hook-driven deploy → verify chain: when a deploy succeeds, the deploy agent automatically runs verification checks (AWS resource health, HTTP smoke tests, CloudWatch alarms/logs) and reports results back to the edit agent.

The verify behavior lives inside the deploy agent (reuses the deploy window) — no new tmux window is needed.

## Files modified

| File | Change |
|------|--------|
| `agents/infra-deployer.md` | Add "Post-deployment Verification" section to agent prompt |
| `config/muxcode.json` | Add deploy event chain, add `curl*` to deploy tools, add `deploy` to `auto_cc` |
| `tools/muxcode-agent-bus/bus/profile.go` | Mirror config changes in Go defaults |
| `tools/muxcode-agent-bus/bus/profile_test.go` | Update tests for new deploy chain behavior |
| `scripts/muxcode-bash-hook.sh` | Split deploy patterns — only apply commands trigger the chain |

## Step 1: Update `agents/infra-deployer.md`

Added a new section after "Deployment Workflow" that instructs the deploy agent on how to handle verify requests:

```markdown
## Post-deployment Verification

When you receive a bus message with action **verify**, run the following checks against the deployed environment. Report results back to the edit agent via the bus.

### AWS Resource Health
- Check CloudFormation stack status: `aws cloudformation describe-stacks`
- Verify Lambda function state: `aws lambda get-function --function-name <name>`
- Confirm API Gateway deployment: `aws apigateway get-rest-apis`
- Check Step Functions state machines: `aws stepfunctions describe-state-machine`
- Validate DynamoDB table status: `aws dynamodb describe-table --table-name <name>`

### HTTP Endpoint Smoke Tests
- `curl -sf <endpoint-url>` for each deployed API endpoint
- Verify response status codes and basic response structure
- Test health-check endpoints if available

### CloudWatch Alarms & Logs
- Check alarm states: `aws cloudwatch describe-alarms --state-value ALARM`
- Query recent log errors: `aws logs filter-log-events --log-group-name <group> --filter-pattern ERROR`
- Check for metric anomalies in the last 5 minutes post-deploy

### Verification Output
- Summarize results as PASS/FAIL per check category
- On any failure, include the specific resource and error details
- Send results to edit via: `muxcode-agent-bus send edit notify "<summary>"`
```

## Step 2: Update `config/muxcode.json`

### 2a. Add deploy event chain

```json
"deploy": {
  "on_success": {
    "send_to": "deploy",
    "action": "verify",
    "message": "Deployment succeeded (${command}) — verify deployed resources and report results to edit",
    "type": "request"
  },
  "on_failure": {
    "send_to": "edit",
    "action": "notify",
    "message": "Deployment FAILED (exit ${exit_code}): ${command} — check deploy window",
    "type": "event"
  },
  "on_unknown": {
    "send_to": "edit",
    "action": "notify",
    "message": "Deployment completed (exit code unknown): ${command}",
    "type": "event"
  },
  "notify_analyst": true
}
```

Key: `on_success` sends back to `deploy` (self-chain) with action `verify`. Failures go straight to edit.

### 2b. Add `curl*` to deploy tool profile

The deploy profile already has `Bash(aws *)`. Added `Bash(curl*)` and `Bash(wget*)` for HTTP smoke tests.

### 2c. Add `deploy` to `auto_cc`

```json
"auto_cc": ["build", "test", "review", "deploy"]
```

This ensures the edit agent sees deploy/verify messages.

## Step 3: Update `tools/muxcode-agent-bus/bus/profile.go`

Mirrored all Step 2 changes in the `DefaultConfig()` function:
- Added the deploy `EventChain` struct
- Added `Bash(curl*)` and `Bash(wget*)` to the deploy `ToolProfile`
- Added `"deploy"` to the `AutoCC` slice

## Step 3b: Update `tools/muxcode-agent-bus/bus/profile_test.go`

- Renamed `TestResolveChain_NoChain` to `TestResolveChain_DeploySuccess` — validates deploy chain returns `send_to:deploy`, `action:verify`, `type:request`
- Added new `TestResolveChain_NoChain` — tests a truly nonexistent chain returns nil
- Updated `TestChainNotifyAnalyst` — deploy now expects `notify_analyst=true`

## Step 4: Update `scripts/muxcode-bash-hook.sh`

### 4a. Add deploy-apply patterns

Added a new pattern variable after `DEPLOY_PATTERNS`:

```bash
DEPLOY_APPLY_PATTERNS="${MUXCODE_DEPLOY_APPLY_PATTERNS:-cdk*deploy|cdk*destroy|terraform*apply|terraform*destroy|pulumi*up|pulumi*destroy|sam*deploy|cloudformation*deploy|cloudformation*create-stack|cloudformation*update-stack}"
```

Also expanded `DEPLOY_PATTERNS` to cover more IaC tools (pulumi, sam, cloudformation).

### 4b. Add `is_deploy_apply` detection

After the existing `is_deploy` detection block, added:

```bash
# Detect deploy-apply commands (subset of deploy that triggers verify chain)
is_deploy_apply=0
if [ "$is_deploy" -eq 1 ]; then
  IFS='|' read -ra DAPATS <<< "$DEPLOY_APPLY_PATTERNS"
  for pat in "${DAPATS[@]}"; do
    case "$FIRST_CMD" in
      $pat*|bash*${pat##*/}*|sh*${pat##*/}*) is_deploy_apply=1; break ;;
    esac
  done
fi
```

### 4c. Conditional chain trigger

Changed the chain call to only fire for apply commands:

```bash
# Only trigger verify chain for actual deployments (not diff/plan)
if [ "$is_deploy_apply" -eq 1 ]; then
  muxcode-agent-bus chain deploy "$(chain_outcome)" \
    --exit-code "${EXIT_CODE:-}" --command "$COMMAND" 2>/dev/null || true
fi
```

History logging continues for ALL deploy patterns (diff, plan, deploy, etc.).

## Verification

- Build: `./build.sh` — compiles Go binary with updated defaults ✅
- Test: `go test ./...` — 117/117 tests pass ✅
- Review: 0 must-fix, 0 should-fix, 1 nit (LGTM) ✅
- Commit: `569162a` pushed to `origin/main` ✅
