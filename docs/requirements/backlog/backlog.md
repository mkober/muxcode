# Requirements Backlog

Index of all pending requirement specs. Every planned feature with a written spec has a row
in the [Spec index](#spec-index) below; ideas without a spec doc yet are collected in
[Ideas without specs](#ideas-without-specs). 81 delivered specs live in
[`completed/`](../completed/) — each with requirements, key files, and implementation notes.

Spec lifecycle: `backlog/` (planned or parked) → `drafts/` (actively being designed or
implemented) → `completed/` (implemented and verified).

## GitHub tracking (MUX ids)

Work on this repo is tracked through GitHub issues, branches, and pull requests. Each spec
in the index carries a stable **`MUX-NNN`** id that ties the req doc to its GitHub artifacts:

| Artifact | Convention | Example |
|----------|-----------|---------|
| Req doc | `MUX-NNN-<slug>.md` | `docs/requirements/backlog/MUX-017-gemini-cli-provider.md` |
| GitHub issue title | `MUX-NNN: <summary>` | `MUX-017: Gemini CLI provider` |
| Branch | `MUX-NNN-<slug>` | `MUX-017-gemini-cli-provider` |
| PR title | `MUX-NNN: <summary>` | `MUX-017: Gemini CLI provider` |

- Ids are assigned in the Spec index below and never reused or renumbered — the index is
  the registry.
- Every spec file in `backlog/` and `completed/` carries its `MUX-NNN-` filename prefix
  (applied across both directories 2026-08-19; completed specs that predate GitHub tracking
  were retroactively minted ids MUX-028–MUX-099 in alphabetical order). New specs are named
  with their prefix from the start, using the next free id.
- `MUX-NNN` matches the `[A-Z][A-Z0-9]*-[0-9]+` key shape existing muxcode tooling expects
  (story-lifecycle `{KEY}-*.md` spec lookup, branch-time key-prefix matching, branch-name
  key extraction), so branches and specs named this way work with no code changes.

## Spec index

### In progress

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-108 | [`MUX-108-control-pane.md`](../drafts/MUX-108-control-pane.md) | High | **The muxcode control pane** — a permanent full-width pane on every agent window, the standing home for global TUIs starting with the graph UI. Default **on with per-window opt-out**; borders styled globally in `tmux.conf`. **Creation order is the contract**: always pane 2, after 0/1, because `AgentPane()` is a hardcoded `"1"` every delivery path resolves through — and at 12 windows a slip breaks every agent's delivery at once, not one. Pane 1's title stays CLI-managed (live state glyph) and is uppercased at display via `pane-border-format` substitution rather than overwritten |

### Reliability & observability

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-006 | [`MUX-006-diagnose-false-clean-verdict.md`](./MUX-006-diagnose-false-clean-verdict.md) | High | `diagnose` collects `IsAlive` but no detector reads it — a dead agent gets "No issues detected" exit 0; add `checkAgentDead` first in `diagnosticChecks` |
| MUX-007 | [`MUX-007-verify-spec-stale-review-refire.md`](./MUX-007-verify-spec-stale-review-refire.md) | High | `checkInboxes()` refires the reviewed-transition on any edit-inbox growth while an unconsumed review message exists — one review completion spawns unbounded `verify-spec` echoes |
| MUX-008 | [`MUX-008-unverified-daemon-auto-restart.md`](./MUX-008-unverified-daemon-auto-restart.md) | High | `RestartLocalAgent()` fire-and-hope relaunch: no exit wait, no launch verification — add bounded exit poll + post-relaunch verification + orphan detection |
| MUX-009 | [`MUX-009-response-echo-chain-retrigger.md`](./MUX-009-response-echo-chain-retrigger.md) | High | Non-hook `SendWakeUp` injects response payloads as prompts, re-firing chains on a delegation's own answer; never inject responses + responded-check in `HasActionableMessages` |
| MUX-032 | [`MUX-032-loop-detector-granularity.md`](./MUX-032-loop-detector-granularity.md) | Medium | `DetectMessageLoop` fires on healthy edit↔commit traffic (3 false alerts, 0 real loops in one session): ping-pong counts correlated replies, tuple pass can't distinguish unrelated delegations on the overloaded `commit` action; fix = correlated-reply exemption + normalized-content repeat requirement, with storm negative controls |
| MUX-010 | [`MUX-010-delegation-message-hygiene.md`](./MUX-010-delegation-message-hygiene.md) | Medium | Agent-freeze auto-recovery + delegation hygiene: force-terminate for hung-but-alive agents, payload/format rules enforced at the bus |
| MUX-012 | [`MUX-012-remove-gated-pane-scrape-delivery.md`](./MUX-012-remove-gated-pane-scrape-delivery.md) | Low | Physically delete the pane-scrape delivery machinery bypassed by the receipt cutover ([delivery-acknowledgement](../completed/MUX-050-delivery-acknowledgement.md)); gated on default-ON soak + backstop mis-fire fix |
| MUX-013 | [`MUX-013-channels-message-transport.md`](./MUX-013-channels-message-transport.md) | Medium | Replace file-polling inbox transport with channel-based delivery |

### Workflow & automation

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-011 | [`MUX-011-opencode-plugin-hook-bridge.md`](./MUX-011-opencode-plugin-hook-bridge.md) | High | Muxcode TS plugin on OpenCode's `tool.execute.after`/`session.idle` events shells to `muxcode hook bash` — deterministic chains for OpenCode via narrow `SupportsChainEvents()`, root enabler fix for MUX-009 storms |

### Agents & roles

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-005 | [`MUX-005-plan-diagrams.md`](./MUX-005-plan-diagrams.md) | Medium | Plan-agent diagram pipeline (render → store → embed) across req docs, Jira, and Confluence. **Parked mid-implementation**: Phases 1–3 shipped (`scripts/render-diagram.sh`, render-only integration test 14/14, Confluence + Jira attachment upload with `attach` CLI); Phases 4–6 remaining (embed across surfaces, profile/skill/docs, end-to-end test) |
| MUX-015 | [`MUX-015-refactor-agent.md`](./MUX-015-refactor-agent.md) | Medium | F6 review ↔ refactor mode toggle: a write-capable refactoring specialist paired with the read-only reviewer |
| MUX-016 | [`MUX-016-research-dual-provider.md`](./MUX-016-research-dual-provider.md) | Medium | Research window split into multiple provider panes (`research-N` bus identities) with broadcast/relay/synthesize across providers |

### Integrations & providers

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-017 | [`MUX-017-gemini-cli-provider.md`](./MUX-017-gemini-cli-provider.md) | High | `GeminiProvider` with full hook support (`BeforeTool`/`AfterTool`) — first alternative provider that can run `SupportsHooks() = true`; fixes the silent Claude fall-through in `ResolveProvider()` |
| MUX-018 | [`MUX-018-opencode-diff-preview-plugin.md`](./MUX-018-opencode-diff-preview-plugin.md) | Medium | OpenCode plugin restoring the nvim diff split preview that hook-less providers lose |
| MUX-019 | [`MUX-019-github-user-stats.md`](./MUX-019-github-user-stats.md) | Medium | Per-user GitHub contribution stats surfaced through the bus/CLI |

### UX & tooling

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-107 | [`MUX-107-tui-component-kit.md`](./MUX-107-tui-component-kit.md) | Medium | Extract the tab bar, footer, list, confirm, and empty state duplicated across `graph_ui.go`/`remote.go`/`model.go` into a shared kit with **golden-frame pinning tests written before the refactor**. Makes [`docs/tui-style.md`](../../tui-style.md) structural rather than advisory — a `List` that renders its own empty state cannot omit one, and a component taking `height` cannot ignore it (the MUX-031 defect) |
| MUX-020 | [`MUX-020-cli-help-command.md`](./MUX-020-cli-help-command.md) | Low | `muxcode help` command: discoverable, grouped CLI reference |
| MUX-021 | [`MUX-021-demo-mode-agent-coverage.md`](./MUX-021-demo-mode-agent-coverage.md) | Low | Refresh `bus/demo.go` scenarios to cover the current agent roster |
| MUX-022 | [`MUX-022-design-mode.md`](./MUX-022-design-mode.md) | Low | Design mode for UI-centric sessions |
| MUX-023 | [`MUX-023-modal-cron-manager.md`](./MUX-023-modal-cron-manager.md) | Low | Interactive cron schedule manager modal |
| MUX-024 | [`MUX-024-modal-history-viewer.md`](./MUX-024-modal-history-viewer.md) | Low | Bus history browser modal with filtering |
| MUX-025 | [`MUX-025-modal-log-viewer.md`](./MUX-025-modal-log-viewer.md) | Low | Lifecycle/log viewer modal |
| MUX-026 | [`MUX-026-modal-memory-browser.md`](./MUX-026-modal-memory-browser.md) | Low | Memory browser modal with BM25 search |
| MUX-027 | [`MUX-027-modal-webhook-monitor.md`](./MUX-027-modal-webhook-monitor.md) | Low | Live webhook request inspector modal with replay |

> `MUX-023`, `MUX-024`, and `MUX-026` each propose replacing a popup that
> [`MUX-031`](../completed/MUX-031-graph-run-tui.md) retires. **`MUX-023` and `MUX-024` only**
> carry a "Replaces existing static … menu entry" acceptance criterion, which needs rewording
> to "adds" once MUX-031 lands; `MUX-026` has no menu criterion and needs no edit. See that
> spec's *Interaction with three existing modal specs*.

### Completed (id registry)

Delivered specs and their MUX ids. Ids are never reused or renumbered — rows stay here
permanently so the id remains claimed after the spec moves to [`completed/`](../completed/).
MUX-003/004 were delivered under GitHub tracking; MUX-028–MUX-099 are retroactive mints
(alphabetical) from the 2026-08-19 prefix rollout, with the spec's title as the Delivered cell.

| ID | Spec | Delivered |
|----|------|-----------|
| MUX-001 | [`MUX-001-branch-time-tracking.md`](../completed/MUX-001-branch-time-tracking.md) | Branch active-time recording delta on the [MUX-040](../completed/MUX-040-branch-time-tracking.md) baseline: `--json` read path, verify-spec doc sink with `seed`-floor never-regress reconciliation, `scripts/test-branch-time-recording.sh` (14 checks) |
| MUX-002 | [`MUX-002-disk-pressure-wrong-filesystem.md`](../completed/MUX-002-disk-pressure-wrong-filesystem.md) | `TmpPressure()` replaces volume percent-used with absolute free-headroom + muxcode-footprint signals; adaptive alert cooldown (600s effective / 3600s ineffective) extracted as pure `shouldAlertDiskPressure` so cadence is testable without running `CleanupStale`; `scripts/test-disk-pressure.sh` (11 checks) with leak detection scoped to scratch-session log names — a whole-directory snapshot is a false-positive generator while a live daemon appends to the log dir |
| MUX-003 | [`MUX-003-echo-as-result.md`](../completed/MUX-003-echo-as-result.md) | `scripts/test-echo-as-result.sh` (20 checks) verifies synthesized bus-response rows can never render as a pass while the authoritative self-logged path still does; isolated scratch `BUS_SESSION`, no live session needed |
| MUX-004 | [`MUX-004-lifecycle-log-test-leak.md`](../completed/MUX-004-lifecycle-log-test-leak.md) | `scripts/test-lifecycle-log-leak.sh` (7 checks) runs the suite under a throwaway `HOME` so a lost `MUXCODE_LIFECYCLE_LOG_DIR` pin is caught without writing to the real install; plus `tui/main_test.go` closing the last unpinned test package |
| MUX-028 | [`MUX-028-agent-debug-skill.md`](../completed/MUX-028-agent-debug-skill.md) | Agent Debug Skill |
| MUX-029 | [`MUX-029-agent-diagnostic-command.md`](../completed/MUX-029-agent-diagnostic-command.md) | Agent diagnostic command |
| MUX-030 | [`MUX-030-agent-health-monitoring.md`](../completed/MUX-030-agent-health-monitoring.md) | Agent Health Monitoring |
| MUX-033 | [`MUX-033-agent-spawn.md`](../completed/MUX-033-agent-spawn.md) | Agent Spawn |
| MUX-034 | [`MUX-034-agent-startup-inbox-wake.md`](../completed/MUX-034-agent-startup-inbox-wake.md) | Agent startup inbox wake-up |
| MUX-035 | [`MUX-035-analyze-findings-log.md`](../completed/MUX-035-analyze-findings-log.md) | Analyze Findings Log |
| MUX-036 | [`MUX-036-answered-row-receipt.md`](../completed/MUX-036-answered-row-receipt.md) | Answered-Row Receipt |
| MUX-037 | [`MUX-037-api-testing-agent.md`](../completed/MUX-037-api-testing-agent.md) | API Testing Agent |
| MUX-038 | [`MUX-038-auto-session-compaction.md`](../completed/MUX-038-auto-session-compaction.md) | Auto Session Compaction |
| MUX-039 | [`MUX-039-bm25-memory-search.md`](../completed/MUX-039-bm25-memory-search.md) | BM25 Memory Search |
| MUX-040 | [`MUX-040-branch-time-tracking.md`](../completed/MUX-040-branch-time-tracking.md) | Branch Time Tracking — the shipped July 2026 accumulator/CLI baseline; [MUX-001](../completed/MUX-001-branch-time-tracking.md) is the later recording-sink delta on top of it |
| MUX-041 | [`MUX-041-build-test-error-extraction.md`](../completed/MUX-041-build-test-error-extraction.md) | Build/Test Error Extraction |
| MUX-042 | [`MUX-042-codex-cli-compatibility.md`](../completed/MUX-042-codex-cli-compatibility.md) | Codex CLI compatibility |
| MUX-043 | [`MUX-043-conditional-chains.md`](../completed/MUX-043-conditional-chains.md) | Conditional chains |
| MUX-044 | [`MUX-044-confluence-update-page.md`](../completed/MUX-044-confluence-update-page.md) | Confluence Page Read+Update |
| MUX-045 | [`MUX-045-context-directory.md`](../completed/MUX-045-context-directory.md) | Context Directory |
| MUX-046 | [`MUX-046-cron-scheduling.md`](../completed/MUX-046-cron-scheduling.md) | Cron Scheduling |
| MUX-047 | [`MUX-047-cross-session-memory.md`](../completed/MUX-047-cross-session-memory.md) | Cross-Session Memory |
| MUX-048 | [`MUX-048-cross-session-window-resize.md`](../completed/MUX-048-cross-session-window-resize.md) | Cross-session window resize on client resize |
| MUX-049 | [`MUX-049-daily-memory-rotation.md`](../completed/MUX-049-daily-memory-rotation.md) | Daily Memory Rotation |
| MUX-050 | [`MUX-050-delivery-acknowledgement.md`](../completed/MUX-050-delivery-acknowledgement.md) | Delivery Acknowledgement (receipts + agent self-poll) |
| MUX-051 | [`MUX-051-demo-mode.md`](../completed/MUX-051-demo-mode.md) | Demo Mode |
| MUX-052 | [`MUX-052-deploy-verify.md`](../completed/MUX-052-deploy-verify.md) | Deploy Verification |
| MUX-053 | [`MUX-053-dynamic-prompts.md`](../completed/MUX-053-dynamic-prompts.md) | Dynamic Prompts |
| MUX-054 | [`MUX-054-edit-context-pressure.md`](../completed/MUX-054-edit-context-pressure.md) | Edit agent context pressure from notification storms |
| MUX-055 | [`MUX-055-event-subscription.md`](../completed/MUX-055-event-subscription.md) | Event Subscription |
| MUX-056 | [`MUX-056-git-manager-heredoc.md`](../completed/MUX-056-git-manager-heredoc.md) | Git Manager HEREDOC |
| MUX-057 | [`MUX-057-go-native-launcher.md`](../completed/MUX-057-go-native-launcher.md) | Go native launcher |
| MUX-058 | [`MUX-058-harness-circuit-breaker.md`](../completed/MUX-058-harness-circuit-breaker.md) | Harness Circuit Breaker |
| MUX-059 | [`MUX-059-jira-pr-comment.md`](../completed/MUX-059-jira-pr-comment.md) | Jira PR Comment Skill |
| MUX-060 | [`MUX-060-jira-update-description.md`](../completed/MUX-060-jira-update-description.md) | Jira Description Read+Update Skill |
| MUX-061 | [`MUX-061-lifecycle-logging.md`](../completed/MUX-061-lifecycle-logging.md) | Lifecycle Logging |
| MUX-062 | [`MUX-062-llm-harness.md`](../completed/MUX-062-llm-harness.md) | Local LLM Harness |
| MUX-063 | [`MUX-063-local-llm-agent.md`](../completed/MUX-063-local-llm-agent.md) | Local LLM Agent for Commit Role via Ollama |
| MUX-064 | [`MUX-064-log-tailing-delegation.md`](../completed/MUX-064-log-tailing-delegation.md) | Log Tailing Delegation |
| MUX-065 | [`MUX-065-loop-detected-self-loop-fix.md`](../completed/MUX-065-loop-detected-self-loop-fix.md) | Loop-Detected Self-Loop Fix |
| MUX-066 | [`MUX-066-loop-detection.md`](../completed/MUX-066-loop-detection.md) | Loop Detection |
| MUX-067 | [`MUX-067-memory-search.md`](../completed/MUX-067-memory-search.md) | Memory Search |
| MUX-068 | [`MUX-068-modal-auto-size.md`](../completed/MUX-068-modal-auto-size.md) | Modal Auto-Size |
| MUX-069 | [`MUX-069-modal-window-manager.md`](../completed/MUX-069-modal-window-manager.md) | API Agent Modal Window |
| MUX-070 | [`MUX-070-multi-agent-reload.md`](../completed/MUX-070-multi-agent-reload.md) | Multi-agent reload |
| MUX-071 | [`MUX-071-muxcode-go-launcher.md`](../completed/MUX-071-muxcode-go-launcher.md) | MuxCode Go Launcher |
| MUX-072 | [`MUX-072-notification-dedup-busy-agent.md`](../completed/MUX-072-notification-dedup-busy-agent.md) | Notification dedup and busy-agent suppression |
| MUX-073 | [`MUX-073-ollama-health-monitoring.md`](../completed/MUX-073-ollama-health-monitoring.md) | Ollama Health Monitoring |
| MUX-074 | [`MUX-074-opencode-compatibility.md`](../completed/MUX-074-opencode-compatibility.md) | OpenCode compatibility |
| MUX-075 | [`MUX-075-opencode-deepseek-editor.md`](../completed/MUX-075-opencode-deepseek-editor.md) | OpenCode + DeepSeek V4 Pro as editor agent |
| MUX-076 | [`MUX-076-pii-scrubbing.md`](../completed/MUX-076-pii-scrubbing.md) | PII Scrubbing |
| MUX-077 | [`MUX-077-planner-agent.md`](../completed/MUX-077-planner-agent.md) | Planner agent |
| MUX-078 | [`MUX-078-playwright-browser-monitoring.md`](../completed/MUX-078-playwright-browser-monitoring.md) | Playwright browser monitoring |
| MUX-079 | [`MUX-079-preview-fold-fix.md`](../completed/MUX-079-preview-fold-fix.md) | Preview Fold Fix |
| MUX-080 | [`MUX-080-process-management.md`](../completed/MUX-080-process-management.md) | Process Management |
| MUX-081 | [`MUX-081-project-aware-context.md`](../completed/MUX-081-project-aware-context.md) | Project-Aware Context |
| MUX-082 | [`MUX-082-research-mode.md`](../completed/MUX-082-research-mode.md) | Research mode |
| MUX-083 | [`MUX-083-review-agent-permissions.md`](../completed/MUX-083-review-agent-permissions.md) | Review Agent Permissions |
| MUX-084 | [`MUX-084-run-chain-watch-overfire.md`](../completed/MUX-084-run-chain-watch-overfire.md) | Run Chain Watch Overfire |
| MUX-085 | [`MUX-085-runner-execution-history.md`](../completed/MUX-085-runner-execution-history.md) | Runner Execution History |
| MUX-086 | [`MUX-086-session-compaction.md`](../completed/MUX-086-session-compaction.md) | Session Compaction |
| MUX-087 | [`MUX-087-session-inspection.md`](../completed/MUX-087-session-inspection.md) | Session Inspection |
| MUX-088 | [`MUX-088-session-reinit-purge.md`](../completed/MUX-088-session-reinit-purge.md) | Session Re-init Purge |
| MUX-089 | [`MUX-089-shell-to-go-migration.md`](../completed/MUX-089-shell-to-go-migration.md) | Shell-to-Go Migration |
| MUX-090 | [`MUX-090-skills-plugin.md`](../completed/MUX-090-skills-plugin.md) | Skills Plugin System |
| MUX-091 | [`MUX-091-spawn-worktrees.md`](../completed/MUX-091-spawn-worktrees.md) | Spawn worktree isolation |
| MUX-092 | [`MUX-092-token-reduction.md`](../completed/MUX-092-token-reduction.md) | Token Usage Reduction — Refactoring Plan |
| MUX-093 | [`MUX-093-tool-profiles-and-chains.md`](../completed/MUX-093-tool-profiles-and-chains.md) | Tool Profiles and Event Chains |
| MUX-094 | [`MUX-094-transactional-messaging-bus.md`](../completed/MUX-094-transactional-messaging-bus.md) | Transactional messaging bus |
| MUX-095 | [`MUX-095-user-initiated-git-ops.md`](../completed/MUX-095-user-initiated-git-ops.md) | User-initiated Git Ops |
| MUX-096 | [`MUX-096-vim-diff-preview-fix.md`](../completed/MUX-096-vim-diff-preview-fix.md) | Vim Diff Preview Fix |
| MUX-097 | [`MUX-097-watchdog-churn-fix.md`](../completed/MUX-097-watchdog-churn-fix.md) | Watchdog Churn Fix |
| MUX-098 | [`MUX-098-webhook-endpoint.md`](../completed/MUX-098-webhook-endpoint.md) | Webhook Endpoint |
| MUX-099 | [`MUX-099-workflow-state-machine.md`](../completed/MUX-099-workflow-state-machine.md) | Workflow state machine |
| MUX-014 | [`MUX-014-graph-agent-orchestrator.md`](../completed/MUX-014-graph-agent-orchestrator.md) | Graph-agent orchestrator — DAG control plane over the bus: 7 node types, outcome-keyed edges, `all`/`any`/`quorum` join barriers, capped loops, `wait_human` gates, durable per-run store under `BusDir()/graphs/<run-id>/`, stateless executor tick (first tick after a daemon restart *is* the resume), 7-subcommand CLI, 5 builtin templates. `scripts/test-graph-orchestrator.sh` 29/29. **Closed at 31/32 steps and 11/15 criteria** — see "Known gaps at completion" in the spec; the open one that matters is verifying graph sends against dedup/relay-suppression/delivery-ack. The live run caught two defects every executor unit test passed over: graph sends were unreplyable (`From = "daemon"` rendered verbatim), and joins hung forever on unknown outcomes |
| MUX-102 | [`MUX-102-agent-mode.md`](../completed/MUX-102-agent-mode.md) | Agent mode (renumbered from MUX-032 to free the id for the loop-detector granularity spec) |
| MUX-105 | [`MUX-105-force-respond-escalation.md`](../completed/MUX-105-force-respond-escalation.md) | Force-respond escalation + graph TUI mode cycling. The root cause was not a missing feature: `SendWakeUp`'s in-flight guard blocked the recovery injection with the very task being recovered **and returned `nil`**, so `checkPollHealth` recorded re-drives that never happened — fixed by an `ErrInjectionSkipped` sentinel, a force path across all four providers, and a 4-rung daemon ladder whose advancement is **receipt-based** (no command return ever marks a rung recovered). `f` in the remote TUI runs the same `bus.ForceDeliver` the ladder does. Also `Tab`/`Shift-Tab` surface cycling and `wait_human` gate auto-show. Unit 2014/0, `scripts/test-force-respond.sh` 15/0. Closed 56/58 — the two open items are one self-contradictory criterion of mine and its withdrawn step, left visible |
| MUX-104 | [`MUX-104-send-keys-dash-payload.md`](../completed/MUX-104-send-keys-dash-payload.md) | `tmux send-keys` parsed a leading `-` in an injected payload as a flag, so bullet-formatted wake-ups died with `invalid flag -` and were retried forever. **`-l` alone does not fix it — only `--` does**, a distinction the originating report got wrong and the integration test now pins. Three vulnerable sites, not the two first reported: `provider_opencode.go`, `notify.go` (which already carried a misleading `-l` comment), and `provider_codex.go` (a raw `exec.Command`, outside the mockable runner, so no argv test could have caught it). All now route through `TmuxSendLiteral()`, which makes the safe form the default. `scripts/test-send-keys-dash.sh` (10 checks, hermetic scratch tmux) |
| MUX-031 | [`MUX-031-graph-run-tui.md`](../completed/MUX-031-graph-run-tui.md) | Graph agent management TUIs for [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) — retired six low-value `prefix + b` popups (capability preserved on the CLI) and reallocated the slots to a run browser (list → layered DAG → node detail), a template launcher validating before it starts a run, and a cross-run `wait_human` gate queue flagging approvals that release git/Atlassian mutations. Gate approval calls `bus.ApproveGraphGate` directly with no bus-message path, and reuses the validator's own `NodeRequiresGate` predicate so the queue cannot drift from the gate rule. `scripts/test-graph-tui.sh` (42 checks, hermetic fixture run store). Closed at 58/60 with a documented *Deferred gaps* table — node detail lacks `EndedAt`/message-id/input-preview, and the removed-popup criterion's CLI half is verified only by hand |
| MUX-103 | [`MUX-103-auto-clear-between-tasks.md`](../completed/MUX-103-auto-clear-between-tasks.md) | Daemon-injected `/clear` for episodic Claude roles after task completion — `bus/clear.go` guard matrix (idle, empty inbox, no live in-flight task, no reload marker, Claude-only, non-harness, not mode-cycled), `edit`/`auto` hard-excluded at both parse and guard, exactly-once via `auto-clear-{role}.last` marker written only on successful injection, dual completion stores (tasks + responded delivery statuses), `muxcode clear <role>` manual path; `scripts/test-auto-clear.sh` (22 checks, real scratch daemon + tmux). Post-clear delivery verified live rather than by the suite — a scratch pane has no Claude runtime and so no `Stop` hook to relaunch the inbox listener |

## Ideas without specs

Curated ideas that have no requirements doc yet. Writing the spec (and giving it the next
free MUX id) is the first step to promoting one.

### Reliability & observability

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
