# Deploy Verification

## Purpose

After a successful infrastructure deployment, there is no automated verification that deployed resources are healthy. The deploy-verify feature adds a hook-driven chain: when a deploy succeeds, the deploy agent automatically runs verification checks and reports results back to the edit agent.

## Requirements

- On successful deployment, the deploy agent must automatically run post-deploy verification without manual intervention
- Verification must cover three categories: AWS resource health, HTTP endpoint smoke tests, and CloudWatch alarms/logs
- Only apply-type commands (deploy, destroy, up) trigger the verify chain — read-only commands (diff, plan) must not
- Verification failures must report the specific resource and error details
- Verification results must be summarized as PASS/FAIL per check category and sent to the edit agent
- The verify behavior must reuse the deploy agent window — no additional tmux window required
- The deploy agent must have access to HTTP tools (curl, wget) for smoke tests
- Deploy messages must be auto-CC'd to the edit agent for visibility

## Acceptance criteria

- A successful `cdk deploy` triggers an automatic verify action in the deploy window
- A `cdk diff` does not trigger the verify chain
- Verification results appear in the edit agent's inbox with per-category PASS/FAIL summary
- Deploy failures notify the edit agent directly with the exit code and command
- The deploy agent can execute `curl` and `wget` commands for HTTP smoke tests
- All deploy/verify message traffic is visible to the edit agent via auto-CC

## Status

Implemented
