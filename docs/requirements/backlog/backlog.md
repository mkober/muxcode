# Requirements Backlog

Index of all pending requirement specs. Every planned feature with a written spec has a row
in the [Spec index](#spec-index) below; ideas without a spec doc yet are collected in
[Ideas without specs](#ideas-without-specs). 87 delivered specs live in
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
- Once a spec's GitHub issue exists, the spec records it as a **`**Tracking:**` line directly
  under its H1** — the issue link, plus any blocks/blocked-by relationship. Introduced
  2026-08-28 with [`MUX-116`](./MUX-116-commit-window-lazygit-diff-pane.md) /
  [`MUX-117`](../completed/MUX-117-pane-targeting-by-identity.md); the id conventions above cover issue
  *titles* but never recorded the issue *number*, so a spec gave no way back to its issue.
  Applied to new specs from that date, not retrofitted.
- `MUX-NNN` matches the `[A-Z][A-Z0-9]*-[0-9]+` key shape existing muxcode tooling expects
  (story-lifecycle `{KEY}-*.md` spec lookup, branch-time key-prefix matching, branch-name
  key extraction), so branches and specs named this way work with no code changes.
- Every spec ends with a **`## Status`** section whose value tracks the directory it lives in:
  `Backlog` → `In Progress` → `Complete`. A trailing dash-clause may qualify the value
  (`Complete — closed at 31/32, see Known gaps`); the **first token is the state**. Normalized
  across all three directories 2026-08-31: 12 backlog specs said `Draft`, 4 completed specs
  said `Implemented`, and 47 completed specs predating this convention had **no Status section
  at all** — those 47 were given a bare `Complete` retroactively, so their Status records the
  directory rather than a close-out that was never written. The one deliberate exception is
  [`MUX-005`](./MUX-005-plan-diagrams.md), parked in `backlog/` while reading `In Progress`
  because Phases 1–3 shipped; its index row states the parked status.

Why it matters: the Status field is the only per-file record of state, and a spec that
contradicts its directory is read by both humans and the close-spec guard, which evaluates
open items against the active spec.

## Spec index

### In progress

| ID | Spec | Since | State |
|----|------|-------|-------|
| MUX-007 | [`MUX-007-verify-spec-stale-review-refire.md`](../drafts/MUX-007-verify-spec-stale-review-refire.md) | 2026-08-31 | Moved to `drafts/`; [`MUX-127`](./MUX-127-review-completion-routing.md) Defect B folded in as evidence. No implementation phase started |

Its rows remain in [Defects](#defects--prioritized) (rank 2) and
[Reliability & observability](#reliability--observability) with `../drafts/` paths, rather than moving
to the completed registry — the spec is in flight, not delivered.

[`MUX-117`](../completed/MUX-117-pane-targeting-by-identity.md) closed and moved to
[`completed/`](../completed/) on 2026-08-31 — 33/33 items, all five phases, verified by
`scripts/test-pane-targeting.sh` at **22 passed, 0 failed**. Its row now lives in the
[completed registry](#completed-id-registry), and its former rows in
[Defects](#defects--prioritized) and [Reliability & observability](#reliability--observability)
were removed with the move. **[MUX-116](./MUX-116-commit-window-lazygit-diff-pane.md) is unblocked**
by it.

### Defects — prioritized

Specs describing **behaviour that is already broken**, ranked. Everything else in this index is new
capability. The ranking is by blast radius, with one rule doing most of the work: **a defect that is
silently wrong outranks one that fails loudly**, because a loud failure stops work while a silent one
corrupts it. Full summaries stay in the topic sections below; this table is the ordering.

| # | ID | Defect | Sev | Why here |
|---|----|--------|-----|----------|
| 1 | [`MUX-127`](./MUX-127-review-completion-routing.md) | Review failure routes nowhere; review success loops | High | Actively killing runs **now** — discarded 2 must-fix findings and buried the notice in 12 echoes |
| 2 | [`MUX-007`](../drafts/MUX-007-verify-spec-stale-review-refire.md) | Verify-spec stale review refire | High | **In progress** — MUX-127 Defect B folded in as evidence 2026-08-31. Filed 2026-08-13 with the sharper mechanism. **Likely merge target** |
| 3 | [`MUX-112`](./MUX-112-idle-task-rescue-closes-live-work.md) | Idle-task rescue closes tasks still running | High | Silently wrong: marks live work complete, so the result is never awaited |
| 4 | [`MUX-120`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Spawned workers never receive their seeded task | High | Blocks every graph `spawn`/`map`; CLI path still has no recovery net |
| 5 | [`MUX-124`](./MUX-124-lifecycle-since-truncated-by-limit.md) | `lifecycle show --since` answers the wrong question | High | The investigation tool lies — already caused MUX-123 to be nearly mis-filed |
| 6 | [`MUX-006`](./MUX-006-diagnose-false-clean-verdict.md) | Diagnose reports a clean verdict over a wedged agent | High | Same class as MUX-124: the diagnostic itself is the thing that misleads |
| 7 | [`MUX-123`](./MUX-123-stall-watchdog-selective-misses.md) | Stall watchdog fires routinely, still misses live stalls | High | Selective misses need a human with `deliver --force`; also now carries the false-positive direction |
| 8 | [`MUX-111`](./MUX-111-harness-reply-miscorrelation.md) | Harness reply correlates to the batch's last message | High | Silently wrong: answers are attributed to the wrong request |
| 9 | [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) | Edit resumes without its launch flags | High | Interactive agent silently loses permission mode and tool profile; **recurred twice in one day** |
| 10 | [`MUX-110`](./MUX-110-harness-startup-tool-loop-exhaustion.md) | Harness startup message exhausts the tool loop | High | Agent burns its budget before doing work |
| 11 | [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) | Daemon auto-restart is unverified | High | Restart reported without confirming the agent came back |
| 12 | [`MUX-009`](./MUX-009-response-echo-chain-retrigger.md) | Response echo re-triggers the chain | High | Same shape as MUX-127 Defect B on non-hook providers |
| 13 | [`MUX-122`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | Prompt-agent turn budget exhaustion | High | Stuck at 26/2/1 across four attempts; instrument built but never pointed at it |
| 14 | [`MUX-010`](./MUX-010-delegation-message-hygiene.md) | No force-terminate for a hung-but-alive agent | Medium | Recovery requires killing the OS process by hand |
| 15 | [`MUX-032`](./MUX-032-loop-detector-granularity.md) | Loop detector too coarse to act on | Medium | Governs whether the detector that flagged MUX-127 can do anything about it |

**Clustering worth noting.** Items 1, 2, 9 and 13 are one family — *a message or response re-entering
the pipeline that produced it* — and items 5 and 7 are another: *the diagnostic misleads about its own
subject*. Fixing either family together is likely cheaper than fixing its members one at a time.

### Reliability & observability

| ID | Spec | Priority | Summary |
|----|------|----------|---------|
| MUX-127 | [`MUX-127-review-completion-routing.md`](./MUX-127-review-completion-routing.md) | High | When a review finishes, two independent mechanisms decide what happens next — the graph executor's edges and the chain's notify gates — and on 2026-08-31 both mis-routed within the same hour, in opposite directions. **Defect A**: `review → failure` has **no live edge** in `req-code-pr`, so a review carrying **2 must-fix** findings killed run `1788195259` (`node review failed with no live edge`) and stranded `fix` as unreachable-`pending`; `build → fix` and `test → fix` both exist, so the *mechanical* failures are repairable in-loop while the *reasoned* one is fatal. Compounding it, build and test both returned `unknown` and were routed via the success edge — review was the only node in the run that asserted anything. **Defect B**: `review → success` emits `plan-verify` unconditionally, so plan writing a spec trips the analyze hook and causes plan to be asked to write a spec — **14 fires in ~50 min, 2 of them real**, `loop-detected plan type=message` twice, and fires 13–14 arriving *after* plan stopped writing (self-sustaining). Sub-defects: the changed-files list is **provenance-blind** (fire 1 named a `$TMPDIR` spawn-worktree path absent from the branch, which a trusting verifier would have checked Phase 2 off against) and plan's own writes are **indistinguishable from implementation progress**. The cheap "it names a doc, ignore it" rule is explicitly *not* the fix: **fire 11 named a doc edit yet carried a real change** — it is how Defect A was found — so suppression must key on state movement, and fire 11's movement was a *graph run transition*, not a file edit, so a working-tree fingerprint alone is insufficient. **Two root causes, separate fix sites, filed together**: fixing A does not fix B |
| MUX-126 | [`MUX-126-edit-resume-aware-auto-restart.md`](./MUX-126-edit-resume-aware-auto-restart.md) | High | An environmental event killed all four Claude agents at once; `plan`/`run`/`commit` were restarted with **full flags but fresh conversations**, while `edit` — hard-excluded from auto-restart (`agentHealthExcludedRoles`, `bus/agent_health.go:13`) — returned only via a bare `claude --resume` that restored the conversation and **none of the launch flags**. The user's interactive agent silently lost `--dangerously-skip-permissions`, `--agent`, `--allowedTools` and `--append-system-prompt`. The two recovery paths are exactly complementary — each missing what the other has — so the fix is one path that keeps both: scrape the session id from the dead pane's `Resume this session with: claude --resume <id>` line, then relaunch through `muxcode agent launch` with `--resume`. **Correcting the request's framing**: `RestartLocalAgent` (`bus/health.go:281`) never assembles flags — it send-keys `muxcode agent launch <role>`, so daemon restarts are *already* fully flagged and the real gap is that the launch command has no resume concept. **Ordering is load-bearing**: the relaunch overwrites the pane text the id must come from, so a scrape placed after the interrupt reads its own launch command and falls back to fresh *forever* — passing any test that only asserts the agent came back. Un-excluding `edit` also fans out to **three** `IsAgentHealthExcluded` call sites (daemon, `cmd/agent_health.go`, `bus/inspect.go`), not one; `webhook` stays excluded. Default ON with `MUXCODE_EDIT_AUTO_RESTART_DISABLE=1` opt-out |
| MUX-125 | [`MUX-125-usage-and-billing-modal.md`](./MUX-125-usage-and-billing-modal.md) | Medium | Modal showing usage, plan headroom and spend for Claude and OpenCode — **the numbers the provider websites show**, pulled from their APIs rather than estimated. **No endpoint is named in the spec, because none is verified**: there is no `ANTHROPIC_API_KEY` in the tree, no `~/.claude/.credentials.json` (Claude Code holds OAuth in the macOS Keychain), and no usage-endpoint reference anywhere under `~/.claude`. Anthropic's Admin API *does* exist but reports **organization API usage** — the Console's numbers, a different account object from a subscription — so using it as a stand-in would look authoritative and measure the wrong thing. OpenCode is the tractable half: `MUXCODE_OPENCODE_API_KEY` already resolves, only the endpoint is unverified. **Source order is decided**: provider API when a verified endpoint exists, labelled local estimate otherwise — resolved **per provider**, so Claude may fall back while OpenCode uses its API in the same frame, and a missing endpoint degrades rather than erroring. Phase 1 is endpoint discovery with no UI work and may legitimately conclude *none exists*, which is now a branch rather than a blocker. The Admin API is explicitly **not** an acceptable substitute — it measures a different account object. Subscription plan, limits and overage rule are likewise **not locally observable** (a transcript search for `limit|plan|quota|overage|bill` returns nothing), and *blocking* vs *billing* are opposite behaviours, so a fee must never be inferred. Fallback data, if used, is real but measures this machine's sessions — and would trip the `input_tokens`=2,466 vs `cache_read`=**518,522,752** trap if conflated |
| MUX-123 | [`MUX-123-stall-watchdog-selective-misses.md`](./MUX-123-stall-watchdog-selective-misses.md) | High | `checkStalledTasks` is **not inert** — it fired **26 times on 2026-08-28** — yet three live stalls that day (two `pr-read`, one spawn task, each idle 4–8 min) were resolved only by a human running `deliver --force`. So the question is not *why does the watchdog never fire* but **why does a working watchdog selectively miss**. Prime suspects are gate 5 (`!PaneHasIdlePrompt && !HasPendingInput`) and gate 6 (the two-sighting debounce, which at a 30 s throttle means no redrive before ~60–90 s). Strongest lead: the pane is resolved via `PaneTarget(session, role)` and one missed stall was a **spawn task**, whose role is a dynamic `spawn-<hex>` — [MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) already showed spawn roles are invisible to the other daemon loops. **This spec was nearly filed on a false premise** (*'zero redrive rows'*) produced by [MUX-124](./MUX-124-lifecycle-since-truncated-by-limit.md) |
| MUX-124 | [`MUX-124-lifecycle-since-truncated-by-limit.md`](./MUX-124-lifecycle-since-truncated-by-limit.md) | High | `lifecycle show --since 8h` returns **0** `task-stall` rows on a day with **26** — same query with `--limit 2000` returns all 26. `FilterLifecycleLog` applies `Since` correctly and then truncates to the **last 50**, so a time-scoped question is silently answered with *'the most recent 50 events'* and an empty result reads as *'this never happened'*. Worst exactly where the tool matters most: the busier the session, the less of it is visible. **Not cosmetic — it produced the false premise that nearly mis-filed [MUX-123](./MUX-123-stall-watchdog-selective-misses.md)** as an inert-watchdog bug. A truncation notice alone would have prevented it |
| MUX-122 | [`MUX-122-prompt-agent-turn-attribution-and-fix.md`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | High | Carries [MUX-115](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md)'s Phases 2–4 after it closed at 11/32 with its instrument built and never used. `scripts/test-prompt-mode.sh` has returned **26/2/1 across four attempts on four different code states**; run 10 is the informative one — probe rejections fell 4→3 while turn exhaustion *rose* 1→2 and the result did not move, so probe-burn is a contributing factor, not the sole cause. **The expensive part is already done**: MUX-115 shipped a verified per-turn tracer (`harness/trace.go`, `ae39804`) whose `TraceOutcomeRejectedProfile` gives exactly the probe-vs-other split four attempts could not observe. So this spec **opens with one traced run, not a fix** — the standing instruction is explicit, because four plausible guesses have each been implemented and each refuted by the same number. Bar for success is unchanged and unambiguous: a result *other than* 26/2/1 | 
| MUX-120 | [`MUX-120-spawn-worker-never-woken-for-seeded-task.md`](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | High | Graph `spawn`/`map` workers never start their seeded task — observed live at **4.5 min idle** until `deliver --force`. The report said *nothing wakes the fresh agent*; measurement says something narrower and more actionable: a wake **does** exist (`spawn.go`, `go func(){ time.Sleep(2*time.Second); _ = Notify(...) }()`) but it is **timer-gated rather than readiness-gated**, fires once, and discards its error — so it lands before the agent reaches `❯` and is swallowed. `LaunchSession` already does this correctly for regular agents (prompt-ready polling, re-capture confirmation, retry, lifecycle logging); spawns just don't use it. The deeper cause is that this single wake has **no recovery net**: both daemon delivery loops iterate the static `bus.KnownRoles` (`checkInboxes` daemon.go:368, `checkPollHealth` ~1851), which never contains dynamic `spawn-<hex>` roles — so the 5 s re-delivery and the 45 s receipt-gap backstop are both blind to spawns, leaving only `checkStalledTasks`, which matches the 4.5 min exactly. **Half of this landed mid-authoring** (`1356694`): `StartSpawn` now calls `wakeAfterReload`, which polls for the idle prompt, always sends on detect-or-timeout, clears stale markers, and logs a `spawn-wake` row — a good fix for the daemon-driven path. **What keeps this open is the fix's own stated fallback**: its comment says a short-lived caller that cuts the goroutine short is *"covered by the receipt-gap backstop"*, and measurement says it is **not** — `checkPollHealth` iterates the static `bus.KnownRoles`, which never contains a `spawn-<hex>` role. So the CLI spawn path strands exactly as before, with no net. Also leaves the superseded 2 s `Notify` in place at `spawn.go:216` | 
| MUX-112 | [`MUX-112-idle-task-rescue-closes-live-work.md`](./MUX-112-idle-task-rescue-closes-live-work.md) | High | `checkIdleTaskCompletion()` (`daemon.go:2912`) reads "pane at the `❯` prompt" as "agent finished", but an agent that launched background work sits at the prompt *because* the work is still running — the **`run` agent's normal mode**. Phase 2 scrapes 50 pane lines, sends them as the task's response, and `CompleteTask`s it (`:3034`); the real reply then lands on a closed task and is dropped as a duplicate, so the *correct* answer is the one discarded. 3/3 reproducible 2026-08-27. The signal already exists — `RefreshProcStatus()`/`CheckProcAlive()`, which `PreCommitCheck()` already consumes for exactly this question |
| MUX-111 | [`MUX-111-harness-reply-miscorrelation.md`](./MUX-111-harness-reply-miscorrelation.md) | High | Harness answers a whole batch with one reply correlated to `msgs[len(msgs)-1]` (`harness/loop.go:276`, `:491`) — recipient, action, and correlation id all come from whatever arrived **last**. When that is a response rather than the request, `MarkResponded` never fires, the request never gains a receipt, and the backstop re-drives it forever; a self-addressed reply echo was also observed. Fix = select the answered request, filter self-addressed messages, define multi-request behaviour |
| MUX-110 | [`MUX-110-harness-startup-tool-loop-exhaustion.md`](./MUX-110-harness-startup-tool-loop-exhaustion.md) | High | The open-ended startup message (`bus/launch.go:747`) has no completion predicate, so a 4B burns all `MaxTurns` and emits `(no response generated — tool loop exhausted)`; `isSingleShotRole()` never arms because it needs one *successful* tool execution. A prompt-shape defect, not a model-quality one — raising `MaxTurns` buys a longer loop, not a completion. **Multiplies with [MUX-111](./MUX-111-harness-reply-miscorrelation.md)**: that one makes the retry infinite, this one makes each retry cost a full budget |
| MUX-006 | [`MUX-006-diagnose-false-clean-verdict.md`](./MUX-006-diagnose-false-clean-verdict.md) | High | `diagnose` collects `IsAlive` but no detector reads it — a dead agent gets "No issues detected" exit 0; add `checkAgentDead` first in `diagnosticChecks` |
| MUX-007 | [`MUX-007-verify-spec-stale-review-refire.md`](../drafts/MUX-007-verify-spec-stale-review-refire.md) | High | `checkInboxes()` refires the reviewed-transition on any edit-inbox growth while an unconsumed review message exists — one review completion spawns unbounded `verify-spec` echoes |
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
| MUX-119 | [`MUX-119-graph-routes-edit-work-off-the-edit-agent.md`](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) | Medium | Keep the **edit agent interactively free while a graph runs** by routing graph implementation work to `auto`. Measurement reframes the obvious fix: of the five builtin nodes targeting `edit`, **four are `spawn`** and already run isolated in their own `spawn-<hex>` window and worktree — only one `send` node (`commit-pr-review-loop`) occupies the interactive agent. Redirecting `role: "edit"` nodes alone would **not** meet the goal, because two other channels wake edit and are not edit steps at all: `graph-approval` and `graph-complete` are **hardcoded** to `"edit"` (`graph_exec.go:347,690`) and auto-CC copies build/test/review traffic into its inbox. Concurrency itself is sound — mode cycling uses `swap-window`, not `swap-pane`, so both agents run continuously. Prerequisite: `auto` is **never launched at session start** (created lazily on first F2 cycle), so a graph dispatching to it today hangs until timeout; and `commitAuthorityDefault` denies auto git mutations | 
| MUX-118 | [`MUX-118-rename-edit-role-to-code.md`](./MUX-118-rename-edit-role-to-code.md) | Medium | Rename the F2 window **Edit → Code** and the "editor" vocabulary to "coder". Not cosmetic: `BusRole()` resolves the role from the tmux window name `#W`, so that one string is at once the display label, the bus address, the inbox filename, the git-mutation authority key (`commitAuthorityDefault`), and the auto-clear exclusion. The governing hazard is that `edit`/`editor` carry **three unrelated meanings** — the agent role, Claude Code's **Edit tool** (`CheckEditGuard`, the `Write\|Edit\|NotebookEdit` hook matcher), and **a text editor** (`MUXCODE_EDITOR`, *default nvim*; `sendEditorCommand`; `editLine`) — and only the first may be renamed, so a bulk substitution would produce `MUXCODE_CODER=nvim`. The target name is its own trade: ~3698 of ~4125 "code" lines are already `muxcode`/`opencode`/`codex`/`code-builder`. Carries an explicit bus-state migration phase — inboxes and receipts are keyed by role name on disk | 
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
| MUX-128 | [`MUX-128-fkey-navigation-for-spawn-windows.md`](./MUX-128-fkey-navigation-for-spawn-windows.md) | Medium | F1–F10 are fully consumed by the ten agent windows (indices 1–10) plus the `research` hold window at index 0, so **every spawned worker lands at index 11+ and is unreachable by any F-key** — structural, not situational; only `prefix + w` reaches them. Binds **F11 → first spawn window, F12 → second**. The obvious `select-window -t:11` is the wrong shape for [MUX-117](../completed/MUX-117-pane-targeting-by-identity.md)'s exact reason: spawn windows are dynamic and uncapped, so an index binding selects whichever worker happens to sit there — within one session the spawn at index 11 was already `spawn-a279185a` and then `spawn-ea720538`. **Resolve by the `spawn-` prefix, then select**, preferably behind a `muxcode window select-spawn <n>` subcommand so ordering is testable in Go rather than an untestable pipeline in `tmux.conf`. **The governing risk is environmental**: F11 is fullscreen in iTerm2/Terminal.app and most emulators, so a correct binding can be wholly inert — empirical confirmation that the key reaches tmux is an *acceptance criterion*, with F12-and-a-chord as the fallback. Open decision recorded: slots are **positional** (F11 = lowest-indexed live spawn), so a key's target shifts when an earlier worker exits — chosen over pinned slots because it needs no persisted state and always reaches a live worker. Also pairs with `WindowFKey`, just corrected to return `""` above index 10: if spawn windows gain F-keys that bound and `TestWindowFKey_ByIndexNotPosition` must change **together**, not after |
| MUX-129 | [`MUX-129-gate-waiting-announcement.md`](./MUX-129-gate-waiting-announcement.md) | Medium | A `wait_human` gate halts a run until a person approves, but **every signal it emits today is agent-facing or pull-based**: a `graph-approval` bus message to the *edit agent's inbox*, a send-keys notify to the edit *pane*, a `graph-gate-pending` log row, and the control pane switching itself to Pending Gates (MUX-108). A user who steps away from the terminal — the normal case while build→test→review runs for minutes — receives nothing, and since [MUX-121](../completed/MUX-121-multi-phase-sequential-graph.md) gives every phase its own gate, one missed announcement stalls the whole pipeline behind it. Adds a **push** signal aimed at the human: a **TTS-generated spoken line** plus a *persistent* visual indicator (transient `display-message` is missed by definition when the user is away). **The hard constraint is that audio is a global, singular, serial resource**: `BusDir()` is per-session and each session runs its own daemon, so N sessions means N independent detectors — two panes render fine side by side, two utterances overlap and neither is intelligible, and sound carries no indication of which session spoke. That forces machine-scoped serialization plus a session name in every utterance, which is why this is more than "call `say` from the gate dispatch". **No audio code exists in the tree today** (verified: zero hits for say/afplay/audio/sound/bell/speech/tts outside comments); `say` and `afplay` are present on macOS, no Linux TTS is, so the no-op backend is a first-class path. **The governing risk is environmental**: over SSH the daemon speaks on the *server* to an empty room while logging success — a log that lies is worse than silence — so detecting unreachable audio and degrading in text is an acceptance criterion. Second risk is self-inflicted: a backend chosen by environment sniffing finds `say` on every developer Mac and **the test suite starts talking**, so a silent suite is asserted, not assumed. **Both scope decisions settled by the user 2026-08-31**: "sub sessions" means **separate muxcode sessions** (so Phase 4 cross-session coordination is required, and spawn windows are covered by containment), and SSH **refuses** rather than playing server-side. The SSH call surfaced a design constraint: reachability must be checked **per announcement against currently-attached tmux clients**, never from the daemon's launch-time environment — a daemon started locally and later attached over SSH would read "no SSH vars", speak to an empty room, and log success, which is the exact false-positive the decision prevents. `tmux list-clients -F '#{client_tty}'` supplies the live truth. **Ready to start**; the three remaining opens (escalation shape, lock ownership, voice verbosity) are implementation-level with recorded leans |
| MUX-116 | [`MUX-116-commit-window-lazygit-diff-pane.md`](./MUX-116-commit-window-lazygit-diff-pane.md) | Medium | The commit console lists staged/modified/untracked files (`renderGitStatus`, `console.go:1257`) but cannot show what changed in one — it is a timer-driven poller with no input handling and no selection. Add a **lazygit** view to the commit window so the user cycles modified + untracked files and reads the selected diff, rather than reimplementing selection, cycling, and untracked-file diffing by hand. **Two hazards governed the design**: `AgentPane()` was hardcoded `"1"` so splitting pane 0 would shift the agent to index 2, breaking every delivery path and colliding with the control pane; and lazygit is a full git-write UI reachable by `tmux send-keys`, which has **no permission hook** in front of it — it must be excluded from every agent-facing path, with a test. **Dedicated pane chosen** (user, 2026-08-28) over an on-demand overlay, which made the index hazard unavoidable — so it was blocked on [MUX-117](../completed/MUX-117-pane-targeting-by-identity.md) rather than worked around. **UNBLOCKED 2026-08-31**: MUX-117 is complete, `AgentPane()` is gone, and panes resolve by `@muxcode_pane` identity — the first hazard is retired and Phase 1 may start. The **second hazard stands undiminished**: the lazygit write-UI exclusion is independent of pane targeting and still needs its test |
| MUX-113 | [`MUX-113-graph-template-delete-rename.md`](./MUX-113-graph-template-delete-rename.md) | Medium | The graph surface can `create` and `export` a template but never remove or move one, so every supersede leaves the old definition resolvable — and `project > user > builtin` means the **superseded** name keeps winning. Found live when the prompt-agent renamed a template and correctly reported it could not delete the original. Sharpest consequence: the `prompt` profile grants no `Write` by design, so a correctly-sandboxed agent has no path to undo its own `create` |
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
| MUX-101 | [`MUX-101-agent-hot-reload.md`](../completed/MUX-101-agent-hot-reload.md) | Agent hot reload — change an agent's CLI provider and model at runtime without restarting the session: `muxcode reload` (multi-role batch, `--all`, `--provider` filter), session-scoped runtime override files, reload markers suppressing health checks mid-cycle, and the provider selector modal. Row restored 2026-08-26; the spec has been in `completed/` since delivery but its registry row was missing, leaving the id claimed on disk and invisible in the index |
| MUX-102 | [`MUX-102-agent-mode.md`](../completed/MUX-102-agent-mode.md) | Agent mode (renumbered from MUX-032 to free the id for the loop-detector granularity spec) |
| MUX-108 | [`MUX-108-control-pane.md`](../completed/MUX-108-control-pane.md) | **The muxcode control pane** — a permanent full-width pane on every agent window, the standing home for global TUIs. Replaced the graph popups and the `prefix + b` graph menu group outright: one always-present surface cannot disagree with itself the way two config-gated ones can. Created **last** on each window so `AgentPane()`'s hardcoded `"1"` delivery contract holds. Pane 1's title stays CLI-managed and is uppercased at *display* via `pane-border-format` substitution — the CLI keeps content, the launcher keeps presentation. `scripts/test-control-pane.sh` (14 checks, both negative controls, a coverage floor) **found a `BUS_SESSION` server-env leak no unit test could see**. Closed 44/45 — supervision cost at 12 panes never measured |
| MUX-105 | [`MUX-105-force-respond-escalation.md`](../completed/MUX-105-force-respond-escalation.md) | Force-respond escalation + graph TUI mode cycling. The root cause was not a missing feature: `SendWakeUp`'s in-flight guard blocked the recovery injection with the very task being recovered **and returned `nil`**, so `checkPollHealth` recorded re-drives that never happened — fixed by an `ErrInjectionSkipped` sentinel, a force path across all four providers, and a 4-rung daemon ladder whose advancement is **receipt-based** (no command return ever marks a rung recovered). `f` in the remote TUI runs the same `bus.ForceDeliver` the ladder does. Also `Tab`/`Shift-Tab` surface cycling and `wait_human` gate auto-show. Unit 2014/0, `scripts/test-force-respond.sh` 15/0. Closed 56/58 — the two open items are one self-contradictory criterion of mine and its withdrawn step, left visible |
| MUX-104 | [`MUX-104-send-keys-dash-payload.md`](../completed/MUX-104-send-keys-dash-payload.md) | `tmux send-keys` parsed a leading `-` in an injected payload as a flag, so bullet-formatted wake-ups died with `invalid flag -` and were retried forever. **`-l` alone does not fix it — only `--` does**, a distinction the originating report got wrong and the integration test now pins. Three vulnerable sites, not the two first reported: `provider_opencode.go`, `notify.go` (which already carried a misleading `-l` comment), and `provider_codex.go` (a raw `exec.Command`, outside the mockable runner, so no argv test could have caught it). All now route through `TmuxSendLiteral()`, which makes the safe form the default. `scripts/test-send-keys-dash.sh` (10 checks, hermetic scratch tmux) |
| MUX-031 | [`MUX-031-graph-run-tui.md`](../completed/MUX-031-graph-run-tui.md) | Graph agent management TUIs for [MUX-014](../completed/MUX-014-graph-agent-orchestrator.md) — retired six low-value `prefix + b` popups (capability preserved on the CLI) and reallocated the slots to a run browser (list → layered DAG → node detail), a template launcher validating before it starts a run, and a cross-run `wait_human` gate queue flagging approvals that release git/Atlassian mutations. Gate approval calls `bus.ApproveGraphGate` directly with no bus-message path, and reuses the validator's own `NodeRequiresGate` predicate so the queue cannot drift from the gate rule. `scripts/test-graph-tui.sh` (42 checks, hermetic fixture run store). Closed at 58/60 with a documented *Deferred gaps* table — node detail lacks `EndedAt`/message-id/input-preview, and the removed-popup criterion's CLI half is verified only by hand |
| MUX-109 | [`MUX-109-prompt-mode-graph-control-pane.md`](../completed/MUX-109-prompt-mode-graph-control-pane.md) | **Prompt** surface on the [control pane](../completed/MUX-108-control-pane.md) plus a headless prompt-agent: name a graph to launch it, ask after runs, approve a **named** gate, compose a project-local graph (validate-before-write), or toggle to inject typed text into the window's active agent. Moved the single local-model default to `qwen3:4b`. **Closed on its [known-gaps table](../completed/MUX-109-prompt-mode-graph-control-pane.md#known-gaps-at-close-out) at 75 of 97 checkboxes** — the 22 open items are left unchecked on purpose, six *measured failing*, including the spec's own gate `scripts/test-prompt-mode.sh` at 26/2/1. The feature shipped (PR #40, `94b8521`); the specification is what stayed open. Follow-ups: [MUX-115](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md), [MUX-113](./MUX-113-graph-template-delete-rename.md). **Carried nowhere:** the 4B regression is measured FALSE — build/test/commit/watch do not complete on `qwen3:4b` with thinking on; the gateway default sidesteps it rather than repairing it |
| MUX-115 | [`MUX-115-prompt-agent-turn-budget-exhaustion.md`](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md) | Per-turn trace of the prompt-agent's harness loop — turn index, tool, arguments, outcome — with `ScrubPII` applied **before** truncation, a nil tracer that no-ops so the loop is instrumented unconditionally, per-row append so the trace survives a killed run, and `TraceOutcomeRejectedProfile` giving the probe-vs-other split four fix attempts could not observe. Gated on `MUXCODE_HARNESS_TURN_TRACE`, default off. **Closed 11/32 as accepted debt** on its [known-gaps table](../completed/MUX-115-prompt-agent-turn-budget-exhaustion.md#known-gaps-at-close-out): the instrument was built and **never pointed at the problem** — `scripts/test-prompt-mode.sh` is still at 26/2/1, both intents still fail, and no trace was ever captured from a failing run. Phases 2–4 carried to [MUX-122](./MUX-122-prompt-agent-turn-attribution-and-fix.md), which was filed *because the close-out initially named no carrier* |
| MUX-121 | [`MUX-121-multi-phase-sequential-graph.md`](../completed/MUX-121-multi-phase-sequential-graph.md) | Sequential multi-phase graph — one run walks a spec's phases **in order**: implement → build/test → review → `update-spec` → a `wait_human` the user approves → commit work **and** spec → loop, with a single final gate covering push and PR. Replaces the one-phase-per-run flow that on 2026-08-28 let three consecutive runs re-implement a completed phase, one producing a parallel incompatible implementation. Phase is **derived statelessly** (lowest phase with open items) so the frozen intent never carries phase state; `spec_phases_remaining` ends the loop with no hardcoded count; the cap is derived from the spec's phase count with a **separate loop edge** so stuck-gate retries cannot consume phase-advance budget; a `phase-progress` guard withholds a commit whose phase did not close, into a gate-and-ask. `nodeRequiresGate` **unchanged** — per-commit approval satisfies the authority rule. `scripts/test-multi-phase-graph.sh` 20/0 (floor 19). **Closed 36/37** on a [known gap](../completed/MUX-121-multi-phase-sequential-graph.md#known-gap-at-close-out): the hermetic harness uses send nodes and has no git, so it cannot observe what a commit *contains* — the first real phase commit is the proof |
| MUX-114 | [`MUX-114-close-spec-node-has-no-completion-check.md`](../completed/MUX-114-close-spec-node-has-no-completion-check.md) | Close-spec completion guard — the builtin `commit-pr-review-loop` marked specs **Complete** guarded only by *"does a pointer exist"*, never *"is the work done"*, and `commit-spec` would have pushed the false claim to the PR branch. Delivered a daemon-side `spec-complete` dispatch guard (`Node.Guard`, `SpecOpenItems()`) that blocks the send before it reaches plan; gate-text validation warning when a gate fails to name a mutation it dominates (hard-pinned for builtins, advisory for user graphs); an ungated-`deploy` warning whose deliberate trade is *positively* pinned; `scripts/test-close-spec-guard.sh` 21/0. **Closed through its own guard** — the predicate that refused [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) on 22 open items allowed this one on zero |
| MUX-103 | [`MUX-103-auto-clear-between-tasks.md`](../completed/MUX-103-auto-clear-between-tasks.md) | Daemon-injected `/clear` for episodic Claude roles after task completion — `bus/clear.go` guard matrix (idle, empty inbox, no live in-flight task, no reload marker, Claude-only, non-harness, not mode-cycled), `edit`/`auto` hard-excluded at both parse and guard, exactly-once via `auto-clear-{role}.last` marker written only on successful injection, dual completion stores (tasks + responded delivery statuses), `muxcode clear <role>` manual path; `scripts/test-auto-clear.sh` (22 checks, real scratch daemon + tmux). Post-clear delivery verified live rather than by the suite — a scratch pane has no Claude runtime and so no `Stop` hook to relaunch the inbox listener |
| MUX-117 | [`MUX-117-pane-targeting-by-identity.md`](../completed/MUX-117-pane-targeting-by-identity.md) | Panes resolve by `@muxcode_pane` identity instead of position — `AgentPane()` removed outright, all 31 enumerated hand-built target sites (Go plus three shell hooks) routed through `bus/pane.go`, throttled once-per-window `pane-fallback` for untagged legacy sessions, and loud failure — never an index guess — when a marked window's tag is missing, including the marker-write-failure case recorded on disk via `markWindowBroken`. `scripts/test-pane-targeting.sh` (22 checks, floor 22, private tmux server) whose negative control inserts a pane *before* the agent and asserts both that delivery follows the tag **and** that the interloper at the agent's old index receives nothing — an index-based fix cannot pass it. Unblocked [MUX-116](./MUX-116-commit-window-lazygit-diff-pane.md) |

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
