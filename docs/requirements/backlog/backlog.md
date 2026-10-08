# Requirements Backlog

Index of pending requirement specs. Specs with a doc are in the [Spec index](#spec-index); ideas
without one are in [Ideas without specs](#ideas-without-specs); delivered specs live in
[`completed/`](../completed/). Lifecycle: `backlog/` → `drafts/` → `completed/`.

## GitHub tracking (MUX ids)

| Artifact | Convention | Example |
|----------|-----------|---------|
| Req doc | `MUX-NNN-<slug>.md` | `docs/requirements/backlog/MUX-017-gemini-cli-provider.md` |
| GitHub issue title | `MUX-NNN: <summary>` | `MUX-017: Gemini CLI provider` |
| Branch | `MUX-NNN-<slug>` | `MUX-017-gemini-cli-provider` |
| PR title | `MUX-NNN: <summary>` | `MUX-017: Gemini CLI provider` |

- Ids are assigned here and never reused or renumbered. Retired, never reuse: `MUX-100` (reason
  unrecorded), `MUX-106` (merged into [MUX-105](../completed/MUX-105-force-respond-escalation.md)).
- Filenames carry the `MUX-NNN-` prefix; a new spec takes the next free id.
- A spec with a GitHub issue links it in a `**Tracking:**` line under its H1.
- Every spec ends with `## Status` matching its directory: `Backlog` → `In Progress` → `Complete`;
  the first token is the state. Exception: [MUX-005](./MUX-005-plan-diagrams.md), parked while
  reading `In Progress`.

## Spec index

### In progress

| ID | Spec | Since | State |
|----|------|-------|-------|
| — | _None — MUX-203 closed 2026-10-08; the next active spec is the user's call_ | — | — |

A spec keeps its Defects and category rows until it closes; on close it moves to the
[registry](#completed-id-registry) and the defect ranks are renumbered.

**Parked** (2026-09-08) — returning one to `drafts/` is the user's call.

| Spec | At | Issue |
|------|----|-------|
| MUX-145 | 9/38 | — |
| MUX-153 | 3/19 | — |

### Defects — prioritized

**Tiers.** `0` stop the bleeding / in flight · `1` repair the instruments · `2` firing now ·
`3` spawn family · `4` phase semantics · `5` restore rebuild · `6` remainder.

| # | T | ID | Defect | Sev | Depends on |
|---|---|----|--------|-----|------------|
| 1 | 1 | [`MUX-204`](./MUX-204-claude-failing-bash-calls-never-reach-hook-bash.md) | Failing Bash calls on Claude never reach `hook bash` — only `PostToolUse` is registered | High | — |
| 2 | 1 | [`MUX-174`](./MUX-174-test-sh-repo-wide-vet-failure-harness-sandbox.md) | `./test.sh` exits 1 repo-wide on the `test` role | High | — |
| 3 | 1 | [`MUX-176`](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) | The run chain fires `success` for a call that has not finished | High | ⇄ [MUX-177](./MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) |
| 4 | 1 | [`MUX-177`](./MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) | The watch chain fires on every bash call and pastes the raw command into the message | Medium | ⇄ [MUX-176](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) |
| 5 | 1 | [`MUX-185`](./MUX-185-history-row-provenance-declared-not-proven.md) | A history row's provenance is declared, not proven | High | [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) |
| 6 | 1 | [`MUX-181`](./MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) | A graph-dispatched Atlassian write is judged on configuration, not on the gate | High | [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) · ⇄ [MUX-165](./MUX-165-gated-jira-write-declined-by-requester-rule.md) |
| 7 | 1 | [`MUX-165`](./MUX-165-gated-jira-write-declined-by-requester-rule.md) | A gate-approved Jira write is declined because plan's rule checks the messenger, not the consent | High | [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) · ⇄ [MUX-181](./MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) |
| 8 | 1 | [`MUX-166`](./MUX-166-run-builds-on-stale-base-no-freshness-gate.md) | A run builds on a stale base | High | — |
| 9 | 1 | [`MUX-168`](./MUX-168-reinit-purges-active-spec-graph-run-survives.md) | Session re-init purges the active-spec pointer while the graph run that reads it survives | High | — |
| 10 | 1 | [`MUX-170`](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) | A graph dispatch suppressed by a foreign in-flight task adopts that task as its own | High | — |
| 11 | 1 | [`MUX-172`](./MUX-172-plan-verify-gate-hardcodes-success.md) | The spec-verification gate hardcodes `success` | High | — |
| 12 | 1 | [`MUX-157`](./MUX-157-role-boundary-an-agent-can-ignore.md) | No road enforces "never author" on build/test/review | High | [MUX-159](../completed/MUX-159-codex-hooks-provider.md) |
| 13 | 1 | [`MUX-127`](./MUX-127-review-completion-routing.md) | Review failure routes nowhere; review success loops | High | — · ⇄ [MUX-009](./MUX-009-response-echo-chain-retrigger.md) |
| 14 | 1 | [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | Response echo re-triggers the chain | High | — · ⇄ [MUX-127](./MUX-127-review-completion-routing.md) |
| 15 | 1 | [`MUX-006`](./MUX-006-diagnose-false-clean-verdict.md) | Diagnose reports a clean verdict over a wedged agent | High | — |
| 16 | 1 | [`MUX-124`](./MUX-124-lifecycle-since-truncated-by-limit.md) | `lifecycle show --since` answers the wrong question | High | — |
| 17 | 1 | [`MUX-152`](./MUX-152-test-sh-hides-modules-after-first-failure.md) | `test.sh` hides every module after the first failure | High | — |
| 18 | 1 | [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | A codex test agent cannot run the suite | High | — |
| 19 | 1 | [`MUX-190`](./MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) | A codex agent drops a consumed request after a guard denial | Medium | — |
| 20 | 1 | [`MUX-189`](./MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) | A batched delivery's reply instruction names only the last request's id | Medium | — |
| 21 | 1 | [`MUX-160`](./MUX-160-tmp-go-cache-leak-unclearable-pressure.md) | The `/tmp` Go caches the disk-pressure sweep counts but cannot clear | High | — |
| 22 | 1 | [`MUX-161`](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) | `upgrade-daemons` cannot see the daemons from a codex build agent | Medium | — |
| 23 | 1 | [`MUX-162`](./MUX-162-pr-review-fix-node-600s-send-cap.md) | `commit-pr-review-loop` fix node `c` expires on the 600 s default task cap | Medium | — |
| 24 | 1 | [`MUX-137`](./MUX-137-test-bus-dir-leak.md) | PreLaunch tests leak real bus dirs into `/tmp` | Low | — |
| 25 | 1 | [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md) | Messages route to a role with no window; diagnose prescribes an impossible fix; reload reports a phantom hang | Medium | — |
| 26 | 1 | [`MUX-150`](./MUX-150-verify-spec-names-last-routed-batch.md) | `verify-spec` names the last routed batch, not the change set | Medium | — |
| 27 | 2 | [`MUX-175`](./MUX-175-response-ack-ping-pong-has-no-brake.md) | Two agents trade acknowledgements every 4–5 s and nothing brakes it | Medium | — |
| 28 | 2 | [`MUX-155`](./MUX-155-send-dedup-keys-on-target-not-sender.md) | `muxcode send` drops a message because another agent's task is in flight | High | — |
| 29 | 2 | [`MUX-180`](./MUX-180-daemon-notifies-windowless-roles-forever.md) | The daemon notifies a windowless role forever; its inbox grows unbounded | High | — |
| 30 | 2 | [`MUX-184`](./MUX-184-orphaned-session-processes-never-reaped.md) | Orphaned session processes are never reaped | High | — |
| 31 | 2 | [`MUX-156`](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) | An orphaned inbox listener consumes messages into the void | High | — |
| 32 | 2 | [`MUX-196`](./MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) | Relaunching an agent expires its in-flight graph dispatch and fails the node | High | — |
| 33 | 3 | [`MUX-135`](./MUX-135-spawn-seed-record-gc-strands-completion.md) | Delivery-record GC permanently strands a long spawn iteration | High | — |
| 34 | 3 | [`MUX-120`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Spawned workers never receive their seeded task | High | — |
| 35 | 3 | [`MUX-112`](./MUX-112-idle-task-rescue-closes-live-work.md) | Idle-task rescue closes tasks still running | High | — |
| 36 | 4 | [`MUX-130`](./MUX-130-spec-phase-parsing-semantics.md) | Spec phase parsing: two definitions of complete, matched document-wide | High | — |
| 37 | 4 | [`MUX-143`](./MUX-143-run-carries-two-phase-identities.md) | A run carries two unreconciled phase identities | High | [MUX-130](./MUX-130-spec-phase-parsing-semantics.md) |
| 38 | 5 | [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) | Daemon auto-restart is unverified | High | — |
| 39 | 5 | [`MUX-194`](./MUX-194-stale-reload-marker-cleanup-breaks-exclusive-lock.md) | Stale reload-marker cleanup can delete a live exclusive lock | Medium | — |
| 40 | 6 | [`MUX-123`](./MUX-123-stall-watchdog-selective-misses.md) | Stall watchdog fires routinely, still misses live stalls | High | — |
| 41 | 6 | [`MUX-111`](./MUX-111-harness-reply-miscorrelation.md) | Harness reply correlates to the batch's last message | High | — |
| 42 | 6 | [`MUX-110`](./MUX-110-harness-startup-tool-loop-exhaustion.md) | Harness startup message exhausts the tool loop | High | — |
| 43 | 6 | [`MUX-122`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | Prompt-agent turn budget exhaustion | High | — |
| 44 | 6 | [`MUX-032`](./MUX-032-loop-detector-granularity.md) | Loop detector too coarse to act on | Medium | — |
| 45 | 6 | [`MUX-010`](./MUX-010-delegation-message-hygiene.md) | No force-terminate for a hung-but-alive agent | Medium | — |
| 46 | 6 | [`MUX-147`](./MUX-147-process-leak-and-memory-footprint.md) | Orphaned harness processes are never reaped | Medium | — |
| 47 | 6 | [`MUX-151`](./MUX-151-display-width-runes-not-cells.md) | Display width is measured in runes, not terminal cells | Low | — |
| 48 | 6 | [`MUX-173`](./MUX-173-prompt-profile-no-discovery-affordance.md) | The `prompt` profile has no discovery affordance | Low | — |
| 49 | 6 | [`MUX-191`](./MUX-191-bus-dir-subcommand-does-not-exist-but-agents-are-told-to-use-it.md) | `muxcode bus-dir` does not exist; `log-watcher.md` and `dev-server.md` tell their agents to call it | Low | — |
| 50 | 6 | [`MUX-188`](./MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | Every spawn worker launch sends edit a stray self-addressed `startup` request | Low | — |
| 51 | 6 | [`MUX-197`](./MUX-197-guard-and-allowlist-bypassable-by-command-shape.md) | Guard rules and the local-LLM allowlist are bypassable by command shape | Medium | ⇄ [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) |

### Reliability & observability

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-204`](./MUX-204-claude-failing-bash-calls-never-reach-hook-bash.md) | Failing Bash Calls on Claude Never Reach `hook bash` — Only `PostToolUse` Is Registered | High | — |
| [`MUX-194`](./MUX-194-stale-reload-marker-cleanup-breaks-exclusive-lock.md) | Stale Reload-Marker Cleanup Can Delete a Live Exclusive Lock | Medium | — |
| [`MUX-185`](./MUX-185-history-row-provenance-declared-not-proven.md) | A History Row's Provenance Is Declared, Not Proven | High | [MUX-148](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) |
| [`MUX-184`](./MUX-184-orphaned-session-processes-never-reaped.md) | Orphaned Session Processes Are Never Reaped | High | — |
| [`MUX-188`](./MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | Every Spawn Worker Launch Sends Edit a Stray Startup Request | Low | — |
| [`MUX-174`](./MUX-174-test-sh-repo-wide-vet-failure-harness-sandbox.md) | `./test.sh` Fails Repo-Wide When the Harness Module's Vet Cannot Resolve a Stdlib Package | High | — |
| [`MUX-172`](./MUX-172-plan-verify-gate-hardcodes-success.md) | The Spec-Verification Gate Asks Its Own Question — `notifyPlanOnReview` Hardcodes `"success"` | High | — |
| [`MUX-168`](./MUX-168-reinit-purges-active-spec-graph-run-survives.md) | Session Re-Init Purges the Active Spec While the Graph Run That Reads It Survives | High | — |
| [`MUX-160`](./MUX-160-tmp-go-cache-leak-unclearable-pressure.md) | The /tmp Go Caches the Disk-Pressure Sweep Counts but Cannot Clear | High | — |
| [`MUX-161`](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) | `upgrade-daemons` Cannot See the Daemons From a Codex Build Agent | Medium | — |
| [`MUX-156`](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) | An Orphaned Inbox Listener Consumes Messages Into the Void | High | — |
| [`MUX-196`](./MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) | Relaunching an Agent Expires Its In-Flight Graph Dispatch | High | — |
| [`MUX-155`](./MUX-155-send-dedup-keys-on-target-not-sender.md) | `muxcode send` Drops a Message Because Another Agent's Task Is In Flight | High | — |
| [`MUX-152`](./MUX-152-test-sh-hides-modules-after-first-failure.md) | `test.sh` Hides Every Module After the First Failure | High | — |
| [`MUX-147`](./MUX-147-process-leak-and-memory-footprint.md) | Reap Orphaned Harness Processes and Reduce Session Memory Footprint | Medium | — |
| [`MUX-150`](./MUX-150-verify-spec-names-last-routed-batch.md) | `verify-spec` Names the Last Routed Batch, Not the Change Set | Medium | — |
| [`MUX-145`](./MUX-145-messages-routed-to-windowless-role.md) | Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix | Medium | — |
| [`MUX-143`](./MUX-143-run-carries-two-phase-identities.md) | A Run Carries Two Unreconciled Phase Identities | High | [MUX-130](./MUX-130-spec-phase-parsing-semantics.md) |
| [`MUX-137`](./MUX-137-test-bus-dir-leak.md) | PreLaunch Tests Leak Real Bus Directories Into `/tmp` | Low | — |
| [`MUX-135`](./MUX-135-spawn-seed-record-gc-strands-completion.md) | Delivery-Record GC Permanently Strands a Long Spawn Iteration | High | — |
| [`MUX-130`](./MUX-130-spec-phase-parsing-semantics.md) | Spec Phase Parsing: Two Definitions of Complete, Matched Document-Wide | High | — |
| [`MUX-127`](./MUX-127-review-completion-routing.md) | Review Completion Routes Nowhere on Failure and Loops on Success | High | — |
| [`MUX-125`](./MUX-125-usage-and-billing-modal.md) | Usage and Billing Modal | Medium | — |
| [`MUX-123`](./MUX-123-stall-watchdog-selective-misses.md) | Stall Watchdog Fires Routinely — and Still Missed Three Live Stalls | High | — |
| [`MUX-124`](./MUX-124-lifecycle-since-truncated-by-limit.md) | `lifecycle show --since` Silently Answers the Wrong Question | High | — |
| [`MUX-122`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | Prompt-Agent Turn Budget — Attribute, Then Fix | High | — |
| [`MUX-120`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Spawned Workers Never Receive Their Seeded Task | High | — |
| [`MUX-112`](./MUX-112-idle-task-rescue-closes-live-work.md) | Idle-Task Rescue Closes Tasks That Are Still Running | High | — |
| [`MUX-111`](./MUX-111-harness-reply-miscorrelation.md) | Harness Reply Correlates to the Batch's Last Message, Not the Request | High | — |
| [`MUX-110`](./MUX-110-harness-startup-tool-loop-exhaustion.md) | Harness Startup Message Exhausts the Tool Loop | High | — |
| [`MUX-006`](./MUX-006-diagnose-false-clean-verdict.md) | Diagnose False Clean Verdict | High | — |
| [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) | Unverified Daemon Auto-Restart | High | — |
| [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | Response Echo Chain Retrigger | High | — |
| [`MUX-032`](./MUX-032-loop-detector-granularity.md) | Loop-Detector Granularity | Medium | — |
| [`MUX-010`](./MUX-010-delegation-message-hygiene.md) | Agent-Freeze Auto-Recovery & Delegation Hygiene | Medium | — |
| [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) | Remove gated pane-scrape delivery machinery | Low | — |
| [`MUX-013`](./MUX-013-channels-message-transport.md) | Channels-based message transport | Medium | — |

### Workflow & automation

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-173`](./MUX-173-prompt-profile-no-discovery-affordance.md) | The Prompt Profile Denies Its Own Discovery Commands | Low | — |
| [`MUX-176`](./MUX-176-run-chain-fires-success-on-backgrounded-call.md) | The Run Chain Reported Success Three Minutes Before the Script Finished — and It Failed | High | — |
| [`MUX-177`](./MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) | The Watch Chain Fires on Every Bash Call and Pastes the Raw Command Into the Message | Medium | — |
| [`MUX-175`](./MUX-175-response-ack-ping-pong-has-no-brake.md) | Two Agents Can Acknowledge Each Other Forever — Response Traffic Has No Brake | Medium | — |
| [`MUX-170`](./MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) | A Graph Dispatch Adopts a Foreign In-Flight Task That Shares Its Action | High | — |
| [`MUX-166`](./MUX-166-run-builds-on-stale-base-no-freshness-gate.md) | A Run Builds on a Stale Base — Branch Freshness Is a Note, Not a Gate | High | — |
| [`MUX-165`](./MUX-165-gated-jira-write-declined-by-requester-rule.md) | A Gate-Approved Jira Write Is Declined Because the Rule Checks the Messenger, Not the Consent | High | [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) · ⇄ [MUX-181](./MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) |
| [`MUX-181`](./MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) | A Graph-Dispatched Atlassian Write Is Judged on Configuration, Not on the Gate | High | [MUX-144](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) · ⇄ [MUX-165](./MUX-165-gated-jira-write-declined-by-requester-rule.md) |
| [`MUX-162`](./MUX-162-pr-review-fix-node-600s-send-cap.md) | The PR-Review Fix Node Dies on the 600 s Send Cap | Medium | — |
| [`MUX-138`](./MUX-138-github-versioning-releases.md) | GitHub versioning & releases | Medium | — |
| [`MUX-011`](./MUX-011-opencode-plugin-hook-bridge.md) | OpenCode Plugin Hook Bridge | High | — |

### Agents & roles

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-157`](./MUX-157-role-boundary-an-agent-can-ignore.md) | A Role Boundary an Agent Can Ignore Is Not a Boundary | High | — |
| [`MUX-197`](./MUX-197-guard-and-allowlist-bypassable-by-command-shape.md) | Guard Rules and the Local-LLM Allowlist Are Bypassable by Command Shape | Medium | — |
| [`MUX-199`](./MUX-199-remove-f2-mode-cycling.md) | Remove F2 Mode Cycling (modal windows behind a toggle replace it) | Medium | ⇄ [MUX-146](./MUX-146-remove-research-and-auto-agents.md) |
| [`MUX-200`](./MUX-200-live-agent-test-auto-startup-behaviour.md) | Live-Agent Test of Auto Startup and Restore Behaviour | Low | [MUX-141](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) |
| [`MUX-146`](./MUX-146-remove-research-and-auto-agents.md) | Remove the Research and Auto Agents | Medium | — |
| [`MUX-119`](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) | Keep the Edit Agent Free While a Graph Runs | Medium | — |
| [`MUX-118`](./MUX-118-rename-edit-role-to-code.md) | Rename the F2 `edit` Role to `code`, and "editor" to "coder" | Medium | — |
| [`MUX-005`](./MUX-005-plan-diagrams.md) | Plan-agent Diagrams (render → store → embed across req docs, Jira, Confluence) | Medium | — |
| [`MUX-015`](./MUX-015-refactor-agent.md) | Refactor Agent (F6 review ↔ refactor mode toggle) | Medium | — |
| [`MUX-016`](./MUX-016-research-dual-provider.md) | Research Agent — Dual-Provider Split View | Medium | — |

### Integrations & providers

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-201`](./MUX-201-non-hook-prompt-and-generated-body-drift.md) | Non-Hook Prompt Text and Generated Agent Bodies Drift From the Definition | Medium | [MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) |
| [`MUX-190`](./MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) | A Codex Agent Drops a Consumed Request After a Guard Denial | Medium | — |
| [`MUX-189`](./MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) | Batch Delivery Correlates Only the Last Request's Reply | Medium | — |
| [`MUX-153`](./MUX-153-codex-test-agent-cannot-run-the-suite.md) | A Codex Test Agent Structurally Cannot Run This Repo's Suite | High | — |
| [`MUX-140`](./MUX-140-jira-issue-creation.md) | Jira Issue Creation From MuxCode | High | — |
| [`MUX-017`](./MUX-017-gemini-cli-provider.md) | Gemini CLI provider | High | — |
| [`MUX-018`](./MUX-018-opencode-diff-preview-plugin.md) | OpenCode diff preview plugin | Medium | — |
| [`MUX-019`](./MUX-019-github-user-stats.md) | GitHub user stats | Medium | — |

### UX & tooling

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-191`](./MUX-191-bus-dir-subcommand-does-not-exist-but-agents-are-told-to-use-it.md) | `muxcode bus-dir` Does Not Exist, but Agent Definitions Tell Agents to Use It | Low | — |
| [`MUX-158`](./MUX-158-api-surface-in-control-pane.md) | API Testing as a Control-Pane Surface, Served Without an LLM | Medium | — |
| [`MUX-128`](./MUX-128-fkey-navigation-for-spawn-windows.md) | F11 and F12 Navigate to Spawned Worker Windows | Medium | — |
| [`MUX-129`](./MUX-129-gate-waiting-announcement.md) | Audible and Visual Announcement of Waiting Graph Gates | Medium | — |
| [`MUX-116`](./MUX-116-commit-window-lazygit-diff-pane.md) | Lazygit Diff Pane on the Commit Window | Medium | [MUX-117](../completed/MUX-117-pane-targeting-by-identity.md) ✅ |
| [`MUX-113`](./MUX-113-graph-template-delete-rename.md) | Graph Template Delete and Rename | Medium | — |
| [`MUX-107`](./MUX-107-tui-component-kit.md) | Shared TUI Component Kit | Medium | — |
| [`MUX-151`](./MUX-151-display-width-runes-not-cells.md) | Display Width Is Measured in Runes, Not Terminal Cells | Low | — |
| [`MUX-020`](./MUX-020-cli-help-command.md) | CLI help command | Low | — |
| [`MUX-021`](./MUX-021-demo-mode-agent-coverage.md) | Demo Mode — Agent Coverage Refresh | Low | — |
| [`MUX-022`](./MUX-022-design-mode.md) | Design mode | Low | — |
| [`MUX-023`](./MUX-023-modal-cron-manager.md) | Modal: Cron Manager | Low | — |
| [`MUX-024`](./MUX-024-modal-history-viewer.md) | Modal: History Viewer | Low | — |
| [`MUX-025`](./MUX-025-modal-log-viewer.md) | Modal: Log Viewer | Low | — |
| [`MUX-026`](./MUX-026-modal-memory-browser.md) | Modal: Memory Browser | Low | — |
| [`MUX-027`](./MUX-027-modal-webhook-monitor.md) | Modal: Webhook Monitor | Low | — |

### Completed (id registry)

Delivered specs. Ids stay claimed permanently; MUX-028–MUX-099 are retroactive mints (2026-08-19).

| ID | Title |
|----|-------|
| [`MUX-001`](../completed/MUX-001-branch-time-tracking.md) | Branch Active-Time Tracking |
| [`MUX-002`](../completed/MUX-002-disk-pressure-wrong-filesystem.md) | Disk-Pressure Check Measures the Wrong Filesystem |
| [`MUX-003`](../completed/MUX-003-echo-as-result.md) | Echo As Result |
| [`MUX-004`](../completed/MUX-004-lifecycle-log-test-leak.md) | Lifecycle Log Test Leak |
| [`MUX-028`](../completed/MUX-028-agent-debug-skill.md) | Agent Debug Skill |
| [`MUX-029`](../completed/MUX-029-agent-diagnostic-command.md) | Agent diagnostic command |
| [`MUX-030`](../completed/MUX-030-agent-health-monitoring.md) | Agent Health Monitoring |
| [`MUX-033`](../completed/MUX-033-agent-spawn.md) | Agent Spawn |
| [`MUX-034`](../completed/MUX-034-agent-startup-inbox-wake.md) | Agent startup inbox wake-up |
| [`MUX-035`](../completed/MUX-035-analyze-findings-log.md) | Analyze Findings Log |
| [`MUX-036`](../completed/MUX-036-answered-row-receipt.md) | Answered-Row Receipt |
| [`MUX-037`](../completed/MUX-037-api-testing-agent.md) | API Testing Agent |
| [`MUX-038`](../completed/MUX-038-auto-session-compaction.md) | Auto Session Compaction |
| [`MUX-039`](../completed/MUX-039-bm25-memory-search.md) | BM25 Memory Search |
| [`MUX-040`](../completed/MUX-040-branch-time-tracking.md) | Branch Time Tracking |
| [`MUX-041`](../completed/MUX-041-build-test-error-extraction.md) | Build/Test Error Extraction |
| [`MUX-042`](../completed/MUX-042-codex-cli-compatibility.md) | Codex CLI compatibility |
| [`MUX-043`](../completed/MUX-043-conditional-chains.md) | Conditional chains |
| [`MUX-044`](../completed/MUX-044-confluence-update-page.md) | Confluence Page Read+Update |
| [`MUX-045`](../completed/MUX-045-context-directory.md) | Context Directory |
| [`MUX-046`](../completed/MUX-046-cron-scheduling.md) | Cron Scheduling |
| [`MUX-047`](../completed/MUX-047-cross-session-memory.md) | Cross-Session Memory |
| [`MUX-048`](../completed/MUX-048-cross-session-window-resize.md) | Cross-session window resize on client resize |
| [`MUX-049`](../completed/MUX-049-daily-memory-rotation.md) | Daily Memory Rotation |
| [`MUX-050`](../completed/MUX-050-delivery-acknowledgement.md) | Delivery Acknowledgement (receipts + agent self-poll) |
| [`MUX-051`](../completed/MUX-051-demo-mode.md) | Demo Mode |
| [`MUX-052`](../completed/MUX-052-deploy-verify.md) | Deploy Verification |
| [`MUX-053`](../completed/MUX-053-dynamic-prompts.md) | Dynamic Prompts |
| [`MUX-054`](../completed/MUX-054-edit-context-pressure.md) | Edit agent context pressure from notification storms |
| [`MUX-055`](../completed/MUX-055-event-subscription.md) | Event Subscription |
| [`MUX-056`](../completed/MUX-056-git-manager-heredoc.md) | Git Manager HEREDOC |
| [`MUX-057`](../completed/MUX-057-go-native-launcher.md) | Go native launcher |
| [`MUX-058`](../completed/MUX-058-harness-circuit-breaker.md) | Harness Circuit Breaker |
| [`MUX-059`](../completed/MUX-059-jira-pr-comment.md) | Jira PR Comment Skill |
| [`MUX-060`](../completed/MUX-060-jira-update-description.md) | Jira Description Read+Update Skill |
| [`MUX-061`](../completed/MUX-061-lifecycle-logging.md) | Lifecycle Logging |
| [`MUX-062`](../completed/MUX-062-llm-harness.md) | Local LLM Harness |
| [`MUX-063`](../completed/MUX-063-local-llm-agent.md) | Local LLM Agent for Commit Role via Ollama |
| [`MUX-064`](../completed/MUX-064-log-tailing-delegation.md) | Log Tailing Delegation |
| [`MUX-065`](../completed/MUX-065-loop-detected-self-loop-fix.md) | Loop-Detected Self-Loop Fix |
| [`MUX-066`](../completed/MUX-066-loop-detection.md) | Loop Detection |
| [`MUX-067`](../completed/MUX-067-memory-search.md) | Memory Search |
| [`MUX-068`](../completed/MUX-068-modal-auto-size.md) | Modal Auto-Size |
| [`MUX-069`](../completed/MUX-069-modal-window-manager.md) | API Agent Modal Window |
| [`MUX-070`](../completed/MUX-070-multi-agent-reload.md) | Multi-agent reload |
| [`MUX-071`](../completed/MUX-071-muxcode-go-launcher.md) | MuxCode Go Launcher |
| [`MUX-072`](../completed/MUX-072-notification-dedup-busy-agent.md) | Notification dedup and busy-agent suppression |
| [`MUX-073`](../completed/MUX-073-ollama-health-monitoring.md) | Ollama Health Monitoring |
| [`MUX-074`](../completed/MUX-074-opencode-compatibility.md) | OpenCode compatibility |
| [`MUX-075`](../completed/MUX-075-opencode-deepseek-editor.md) | OpenCode + DeepSeek V4 Pro as editor agent |
| [`MUX-076`](../completed/MUX-076-pii-scrubbing.md) | PII Scrubbing |
| [`MUX-077`](../completed/MUX-077-planner-agent.md) | Planner agent |
| [`MUX-078`](../completed/MUX-078-playwright-browser-monitoring.md) | Playwright browser monitoring |
| [`MUX-079`](../completed/MUX-079-preview-fold-fix.md) | Preview Fold Fix |
| [`MUX-080`](../completed/MUX-080-process-management.md) | Process Management |
| [`MUX-081`](../completed/MUX-081-project-aware-context.md) | Project-Aware Context |
| [`MUX-082`](../completed/MUX-082-research-mode.md) | Research mode |
| [`MUX-083`](../completed/MUX-083-review-agent-permissions.md) | Review Agent Permissions |
| [`MUX-084`](../completed/MUX-084-run-chain-watch-overfire.md) | Run Chain Watch Overfire |
| [`MUX-085`](../completed/MUX-085-runner-execution-history.md) | Runner Execution History |
| [`MUX-086`](../completed/MUX-086-session-compaction.md) | Session Compaction |
| [`MUX-087`](../completed/MUX-087-session-inspection.md) | Session Inspection |
| [`MUX-088`](../completed/MUX-088-session-reinit-purge.md) | Session Re-init Purge |
| [`MUX-089`](../completed/MUX-089-shell-to-go-migration.md) | Shell-to-Go Migration |
| [`MUX-090`](../completed/MUX-090-skills-plugin.md) | Skills Plugin System |
| [`MUX-091`](../completed/MUX-091-spawn-worktrees.md) | Spawn worktree isolation |
| [`MUX-092`](../completed/MUX-092-token-reduction.md) | Token Usage Reduction — Refactoring Plan |
| [`MUX-093`](../completed/MUX-093-tool-profiles-and-chains.md) | Tool Profiles and Event Chains |
| [`MUX-094`](../completed/MUX-094-transactional-messaging-bus.md) | Transactional messaging bus |
| [`MUX-095`](../completed/MUX-095-user-initiated-git-ops.md) | User-initiated Git Ops |
| [`MUX-096`](../completed/MUX-096-vim-diff-preview-fix.md) | Vim Diff Preview Fix |
| [`MUX-097`](../completed/MUX-097-watchdog-churn-fix.md) | Watchdog Churn Fix |
| [`MUX-098`](../completed/MUX-098-webhook-endpoint.md) | Webhook Endpoint |
| [`MUX-099`](../completed/MUX-099-workflow-state-machine.md) | Workflow state machine |
| [`MUX-014`](../completed/MUX-014-graph-agent-orchestrator.md) | Graph-Agent Orchestrator |
| [`MUX-101`](../completed/MUX-101-agent-hot-reload.md) | Agent hot reload |
| [`MUX-102`](../completed/MUX-102-agent-mode.md) | Agent mode |
| [`MUX-108`](../completed/MUX-108-control-pane.md) | The MuxCode Control Pane |
| [`MUX-105`](../completed/MUX-105-force-respond-escalation.md) | Force-Respond Escalation and Graph TUI Mode Cycling |
| [`MUX-104`](../completed/MUX-104-send-keys-dash-payload.md) | Wake-Injection Fails on Payloads Starting With a Dash |
| [`MUX-031`](../completed/MUX-031-graph-run-tui.md) | Graph Agent Management TUIs |
| [`MUX-109`](../completed/MUX-109-prompt-mode-graph-control-pane.md) | Prompt Mode and Prompt-Agent for the Graph Control Pane |
| [`MUX-115`](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md) | Prompt-Agent Turn Budget Exhaustion — Instrument Before Fixing |
| [`MUX-121`](../completed/MUX-121-multi-phase-sequential-graph.md) | Sequential Multi-Phase Graph — One Run Delivers a Whole Spec |
| [`MUX-114`](../completed/MUX-114-close-spec-node-has-no-completion-check.md) | The `close-spec` Node Marks Specs Complete Without Checking Whether They Are |
| [`MUX-103`](../completed/MUX-103-auto-clear-between-tasks.md) | Auto-Clear Between Tasks |
| [`MUX-117`](../completed/MUX-117-pane-targeting-by-identity.md) | Resolve Panes by Identity, Not by Index |
| [`MUX-149`](../completed/MUX-149-graph-unverified-hold.md) | The Unverified Hold — a Node With No Authoritative Result Parks for a Person |
| [`MUX-136`](../completed/MUX-136-bare-resume-loses-agent-definition.md) | A Bare Resume Loses the Agent Definition |
| [`MUX-134`](../completed/MUX-134-status-bar-fkey-label-diverges-from-binding.md) | Status-Bar F-Key Label Diverges From the Actual Binding |
| [`MUX-133`](../completed/MUX-133-condition-false-branch-renders-as-failure.md) | A Condition Node's False Branch Renders as a Failure |
| [`MUX-131`](../completed/MUX-131-spawn-implement-output-never-ported.md) | Graph Spawn Workers: Output Never Ported, Worker Rebuilt Every Iteration |
| [`MUX-132`](../completed/MUX-132-graph-retry-launders-gate-approval.md) | `graph retry --from` Launders a Stale Human Gate Approval |
| [`MUX-159`](../completed/MUX-159-codex-hooks-provider.md) | A Codex Hooks Provider: Put Codex Agents on the Deterministic Chain Road |
| [`MUX-163`](../completed/MUX-163-prompt-inject-escape-eats-first-char.md) | The Prompt Surface's Inject Loses Its First Character to Its Escape Prefix |
| [`MUX-169`](../completed/MUX-169-startup-self-reply-echo-loop.md) | The Startup Bootstrap's Self-Reply Rides Its Own Exemption Back Into the Sender's Inbox |
| [`MUX-164`](../completed/MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md) | A Codex Trust Prompt Reads as Idle, and the Wake-Up Types into the Shell It Leaves Behind |
| [`MUX-167`](../completed/MUX-167-spec-to-pr-commit-gate-before-phase-check.md) | spec-to-pr Asks for a Commit Approval Before It Checks the Phase Is Complete |
| [`MUX-171`](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) | The Stall Watchdog Re-Drives Into a Busy Claude Agent and Its Escape Preamble Kills the Running Tool |
| [`MUX-144`](../completed/MUX-144-wait-human-gate-openable-by-any-agent.md) | A `wait_human` Gate Is Openable by Any Agent, Unaudited |
| [`MUX-007`](../completed/MUX-007-verify-spec-stale-review-refire.md) | Verify-Spec Stale Review Refire |
| [`MUX-178`](../completed/MUX-178-spawn-node-cuts-no-worktree-port-harvest-broken.md) | A Graph Spawn Node Credits an Unanswered Seed as Success, and the Multi-Phase Fixture Still Asserts the Retired Worktree Model |
| [`MUX-179`](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md) | The PII Scrub Role Gate Has No Call Site on the Bus Road |
| [`MUX-182`](../completed/MUX-182-cancelled-run-keeps-working-provenance-unreadable.md) | A Cancelled Run Keeps Working, and Nothing Downstream Can Tell Who Launched It |
| [`MUX-183`](../completed/MUX-183-phase-commit-ready-recredits-shipped-phases.md) | `phaseCommitReady` Credits a Fresh Run With Phases It Never Shipped |
| [`MUX-186`](./MUX-186-pr-89-cancel-races-and-fail-open-cleanup-merged-unaddressed.md) | Cancel Races and Fail-Open Cleanup Merged Unaddressed in PR #89 |
| [`MUX-154`](../completed/MUX-154-codex-status-line-closes-tracked-tasks.md) | A Codex Status Line Closes Tracked Tasks as Their Answer |
| [`MUX-192`](../completed/MUX-192-stale-in-flight-task-starves-codex-delivery.md) | A Stale In-Flight Task Starves Every Wake to a Codex Agent |
| [`MUX-126`](../completed/MUX-126-edit-resume-aware-auto-restart.md) | Resume-Aware Auto-Restart for Claude Agents |
| [`MUX-193`](../completed/MUX-193-spec-to-pr-fix-loop-cap-is-per-run-not-per-phase.md) | The Spec-to-PR Fix-Loop Cap Is Per-Run, Not Per-Phase |
| [`MUX-195`](../completed/MUX-195-graph-runs-never-reuse-idle-workers.md) | Multiple Workers Are Spawned Per Graph Run — One Worker Per Run, One Per Spawning Agent |
| [`MUX-148`](../completed/MUX-148-node-outcome-reads-command-ran-as-task-done.md) | A Node Outcome Reads "a Command Ran" as "the Task Was Done" |
| [`MUX-187`](../completed/MUX-187-pr-merge-merges-over-unresolved-review-comments.md) | `110-pr-merge` Merges Over Unresolved Review Comments |
| [`MUX-198`](../completed/MUX-198-graph-requests-carry-literal-verdicts-an-echo-satisfies.md) | Builtin Graph Requests Carry Literal Verdicts an Echoed Request Satisfies |
| [`MUX-202`](../completed/MUX-202-self-upgrade-from-the-quick-menu.md) | Self-Upgrade From the Quick Menu — Check, Download, Rebuild, Restart Daemons |
| [`MUX-203`](../completed/MUX-203-sensitive-role-conversation-is-never-scrubbed.md) | A Sensitive Role's Own Conversation Is Never Scrubbed — PostToolUse Cannot Reach It |
| [`MUX-139`](../completed/MUX-139-claude-agent-auto-resume.md) | Claude Agent Auto-Resume After Mass Exit |
| [`MUX-141`](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) | Auto Agent Restarts Relaunch Autonomous Graph Runs |
| [`MUX-142`](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Spawned Worker Delegates Build/Test Into the Wrong Tree |

## Ideas without specs

Curated ideas that have no requirements doc yet. Writing the spec (and giving it the next
free MUX id) is the first step to promoting one.

### Reliability & observability

- **Codex hook-road live run** — the three live clauses [`MUX-159`](../completed/MUX-159-codex-hooks-provider.md) deferred at close; needs a real codex (`MUXCODE_CODEX_HOOKS_LIVE=1`, API spend)
- **Structured agent metrics** (Medium) — Track per-agent metrics (messages sent/received, tool calls, errors, avg response time) in `metrics.jsonl` — dashboard TUI shows metrics panel
- **File integrity validation** (Medium) — Timestamp-based change detection on file operations — detect external modifications between read and edit/write, warn agent of stale content before applying changes. Inspired by OpenCode's file integrity checks
- **Tool-call doom loop detection** (Medium) — Detect 3+ identical consecutive tool calls within a single agent turn (same tool, same args) — prompt user or abort. Complements existing message-level loop detection in `bus/guard.go`. Inspired by OpenCode's `doom_loop` permission
- **Bus audit trail** (Low) — Append-only audit log separate from `log.jsonl` capturing all bus operations (send, consume, lock, unlock, cron fire, proc start/stop) with caller identity — post-session debugging. Partially addressed by lifecycle logging (`~/.config/muxcode/logs/`)

### Performance & cost

- **Agent max steps / iteration limits** (High) — Per-role configurable maximum tool-call iterations per message — `MUXCODE_{ROLE}_MAX_STEPS` or profile field. Prevents runaway API costs from stuck agents. Harness circuit breaker handles local LLM; this extends to Claude Code agents via conversation turn counting. Inspired by OpenCode's `maxSteps` per agent
- **On-demand agent spawning** (Medium) — Convert runner, watch, and analyst from always-on to deferred launch on first message — tmux windows still created for left-pane pollers, agent process starts only when a bus message targets the role
- **Smart context pruning** (Medium) — Before hitting compaction threshold, auto-prune low-relevance memory entries (BM25-scored against recent activity) — more surgical than full session compact
- **Tiered model routing** (Medium) — Route simple/structured tasks (git status, build) to cheaper/faster models (Haiku) and complex tasks (review, analysis) to Opus — config-driven per-role model selection
- **Batch message coalescing** (Low) — When multiple messages arrive in an agent's inbox between polls, coalesce into a single prompt rather than processing sequentially — reduces context overhead and API calls

### Workflow & automation

- **Retry with backoff** (Medium) — Configurable retry policy for failed chain steps — exponential backoff, max attempts, different behavior per step
- **Workspace checkpoints** (Medium) — Snapshot working directory state before risky operations (deploy, large refactor) — allows rollback via `muxcode checkpoint restore`, leverages `git stash` or worktrees internally
- **Undo/redo for agent file changes** (Medium) — Track file snapshots before each agent Write/Edit operation — `muxcode undo [steps]` restores previous state via git stash or shadow copies. Inspired by OpenCode's `/undo` and `/redo` commands
- **Pre-commit hooks** (Low) — Beyond the current safeguard (pending inbox check), run configurable checks before commit — lint, type-check, test subset — blocks commit until all pass

### Intelligence & context

- **LSP integration for agent tools** (High) — Auto-manage LSP servers for project languages — inject diagnostics into edit/write tool results so agents see type errors and lint warnings immediately after file changes. Start with Go (`gopls`), TypeScript (`typescript-language-server`), Python (`pyright`). Auto-download LSP binaries on first use, disable via `MUXCODE_DISABLE_LSP`. Inspired by OpenCode's 30+ language LSP integration
- **Memory tagging & expiry** (Medium) — Tag memory entries with categories (bug-fix, convention, workaround) and optional TTL — auto-expire stale workarounds, improves signal-to-noise in memory search
- **Agent handoff protocol** (Medium) — Structured handoff when one agent needs another to continue its work — includes context bundle (relevant files, conversation excerpt, constraints), not just "send a message"
- **MCP protocol support** (Medium) — Model Context Protocol server integration for external resource access — databases, APIs, custom data sources. Configure MCP servers in `.muxcode/config` or `opencode.json`-compatible format. Inspired by OpenCode's MCP integration
- **Semantic memory search** (Low) — Augment BM25 with embeddings (local via Ollama embedding models) for semantic similarity — falls back to BM25 when Ollama unavailable

### UX & dashboard

- **Dashboard activity timeline** (High) — Visual timeline in TUI showing message flow between agents over time — like a sequence diagram but live — currently dashboard shows status tables but no temporal view
- **TUI theme system** (Medium) — Configurable color themes for the dashboard TUI and left-pane log scripts — built-in themes (Dracula default, Tokyo Night, Catppuccin, Nord, Gruvbox), custom themes via JSON in `~/.config/muxcode/themes/` or `.muxcode/themes/`. Inspired by OpenCode's theme system
- **Agent log viewer in TUI** (Medium) — Navigate and search `log.jsonl` from the dashboard — filter by role, action, time range — currently requires `muxcode history` CLI
- **Notification sound/bell** (Low) — Optional terminal bell or macOS notification on important events (build failure, review complete, agent-down) — configurable per-event
- **Session recording & replay** (Low) — Record all bus messages during a session for later replay/analysis — useful for demos, debugging, understanding multi-agent interactions — inverse of demo mode

### Integrations

- **GitHub Actions webhook bridge** (High) — Pre-built GitHub Actions workflow that POSTs to the webhook endpoint on PR events (opened, review submitted, CI status) — turns external events into agent actions
- **Slack/Discord notifications** (Medium) — Forward important agent events (build failure, deploy complete, review findings) to a Slack/Discord channel via webhook URL — one-way, config-driven
- **IDE status bar** (Medium) — Lightweight status indicator for VS Code / Neovim showing agent states and inbox counts — read-only, polls bus directory — for Neovim: a Lua plugin reading lock files
- **GitHub App for comment-triggered agents** (Medium) — GitHub App + Actions workflow that triggers MuxCode agents from PR/issue comments — `/muxcode fix this`, `/muxcode review`, `/muxcode explain`. Agent runs in CI runner, posts results as PR comment. Inspired by OpenCode's `/opencode` GitHub integration
- **Linear/Jira bidirectional sync** (Low) — Beyond current Jira description updates — auto-update issue status based on agent activity (e.g. move to "In Review" when review agent starts)

### Security & isolation

- **Secret scanning in commits** (High) — Pre-commit agent check scans staged diffs for patterns matching API keys, tokens, passwords — blocks commit and alerts edit. PII scrubbing (`bus/scrub.go`, `harness/scrub.go`) partially addresses this for tool output but not for commits
- **Agent sandbox levels** (Medium) — Graduated trust levels — `read-only`, `project-scoped`, `unrestricted` — new agents start at read-only and escalate based on config, more granular than current tool profiles
- **Webhook rate limiting** (Low) — Per-IP and global rate limits on the webhook endpoint — currently only has auth token + localhost binding, important if exposing via tunnel

### Developer experience

- **`muxcode init` wizard** (High) — Interactive project setup — detects project type, generates `.muxcode/config`, copies relevant agent overrides, suggests window layout
- **Agent definition linting** (Medium) — Validate agent markdown files — check frontmatter schema, verify referenced tools exist in profiles, warn about common mistakes — `muxcode agent lint`
- **Custom slash commands** (Medium) — User-defined slash commands with argument interpolation — markdown files in `.muxcode/commands/` with `$ARGUMENTS`, positional args, bash output injection, `@file` inclusion. Inspired by OpenCode's custom commands system
- **Skill marketplace** (Low) — Community-shared skills via a git-based registry — `muxcode skill install <url>` — each skill is a markdown file with frontmatter, already the right format
- **Multi-repo sessions** (Low) — Support sessions spanning multiple related repos (monorepo-like) — each repo gets its own bus directory but agents can cross-reference

## Sources

- [OpenClaw](https://openclaw.ai/) — architecture inspiration for many features
- [OpenClaw Architecture Overview](https://ppaolo.substack.com/p/openclaw-system-architecture-overview)
- [OpenCode](https://opencode.ai/) — open source AI coding agent with LSP integration, MCP protocol, multi-provider support, theme system, GitHub App, custom commands
- [OpenCode DeepWiki](https://deepwiki.com/anomalyco/opencode) — architecture analysis
