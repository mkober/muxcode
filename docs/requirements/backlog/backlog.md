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

A spec keeps its Defects and category rows until it closes; on close it moves to the
[registry](#completed-id-registry) and the defect ranks are renumbered.

**Parked** — returning one to `drafts/` is the user's call. `55-defect-backlog-loop`'s `select`
skips every spec listed here. The rule, from the user's decision of 2026-11-13 relayed by edit: a
spec whose phases hold a design decision nobody has recorded, or that ships together with an
unfinished spec, is not worked unattended. On 2026-11-24 the user went through all 34 specs then
parked — 21 unparked with their decisions recorded, MUX-010 and MUX-147 split, ten closed, two kept
here (tables below). Each parked defect keeps its Defects rank.

| Spec | At | Issue | Why parked |
|------|----|-------|------------|
| MUX-215 | 7/17 | [#190](https://github.com/mkober/muxcode/issues/190) | Flaky test not reproducible in 14 runs; reopen on a named failure. Phase 1's capture work shipped. Reviewed by the user 2026-11-24: stays parked |
| MUX-122 | 0/25 | [#109](https://github.com/mkober/muxcode/issues/109) | Its first deliverable is a traced run of `scripts/test-prompt-mode.sh`, which needs a model API key. Reviewed by the user 2026-11-24: stays parked |
| MUX-241 | 0/29 | [#255](https://github.com/mkober/muxcode/issues/255) | Parked at filing 2026-11-24 on the user's decision: the signal that tells a frozen agent from a busy one is undesigned |
| MUX-242 | 0/25 | [#116](https://github.com/mkober/muxcode/issues/116) | Parked at filing 2026-11-24 on the user's decision: measurement-first, and idle eviction is deferred. Not a Defects row |
| MUX-243 | 0/20 | [#256](https://github.com/mkober/muxcode/issues/256) | Parked at filing 2026-11-24 by plan, not on a user decision: five design questions unanswered (surface, what a halt stops, a graph-owned dispatch, who may halt, a resumed conversation) |
| MUX-244 | 1/18 | [#257](https://github.com/mkober/muxcode/issues/257) | Parked at filing 2026-11-24 by plan, not on a user decision: the check's mechanical form is undecided. Phase 1 (prose) could be worked without it |
| MUX-245 | 0/12 | [#258](https://github.com/mkober/muxcode/issues/258) | Parked at filing 2026-11-24 by plan, not on a user decision: whether the rule is enforced, where, and how authorship is known. Phase 1 (prose) could be worked without them |

MUX-223 and MUX-225 were parked at filing and unparked 2026-11-14 once the user's decisions,
relayed by edit, were recorded in each spec.

**Unparked 2026-11-15** on the user's decisions, relayed by edit and recorded in each spec with its
criteria and phases rewritten to them:

| Spec | Now | What was decided |
|------|-----|------------------|
| MUX-170 | closed 2026-11-16 at 14/14 | Keep MUX-155's deliver-both for another sender's request; a `daemon` task is a node's own by run id and node id, not payload |
| MUX-145 | closed 2026-11-16 at 49/49 | Refuse at `Send` when nothing can consume the role's inbox — no window, no listener and no headless agent; a window alone is not the test. Owns the refusal; MUX-180 owns notify, cap and workflow |
| MUX-175 | closed 2026-11-16 at 23/23 | Suppress a response that exactly matches (case, whitespace and punctuation folded; numbers kept) the previous one on the same pair and action within 300 s; no count cap; sender told `NOT SENT`; a reply to an open tracked request is exempt |
| MUX-180 | closed 2026-11-16 at 25/25 | Cap 50 per role, refuse past it, drop nothing; the workflow does not transition to a role with no consumer; divided with MUX-145 as above, kept as two specs |
| MUX-184 | closed 2026-11-16 at 52/52 | Proof by argv session name (since 2026-11-16 including the legacy `inbox-listener.sh` script path, which is reaped) or a recorded pid still naming the same process, no environment probe; automatic at every launch (`MUXCODE_ORPHAN_REAP=0`); `escabs` servers in scope; the daemon stops its children on TERM/INT, not on re-exec; MUX-147's and MUX-156's reaper phases point to it |

**Unparked 2026-11-24** on the user's decisions, relayed by edit and recorded in each spec's
"Decisions (user, 2026-11-24)" section, with criteria and phases rewritten to them. All are in
`backlog/`, none started:

| Spec | At | What was decided |
|------|----|------------------|
| [`MUX-124`](../completed/MUX-124-lifecycle-since-truncated-by-limit.md) | closed 2026-11-27 at 19/19 | `--since` implies no limit unless `--limit` is explicit; any truncation prints "showing last N of M" on stderr |
| [`MUX-173`](./MUX-173-prompt-profile-no-discovery-affordance.md) | 0/14 | Add `Bash(muxcode help*)` and bare `Bash(muxcode)` to the prompt profile only, with an allowed-set test and negative control |
| [`MUX-188`](../completed/MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | closed 2026-11-27 at 21/21 | A spawn worker gets no startup bootstrap; the launch skips it for a `spawn-*` role. Widened by the user 2026-11-27: the worker launch also skips the base role's task clearing and launch stamps |
| [`MUX-194`](../completed/MUX-194-stale-reload-marker-cleanup-breaks-exclusive-lock.md) | closed 2026-11-28 at 25/25 | The heartbeat already shipped (owner token, renewal — MUX-139). Restated to the leftover: `diagnose` reads a freshly renewed marker as a relaunch in progress; `writeReloadMarker` test-only; a lifecycle row on stale removal |
| [`MUX-143`](../completed/MUX-143-run-carries-two-phase-identities.md) | closed 2026-11-27 at 33/33 | Split in two: Defect A blesses `### Deferred` as the deferral mechanism, landing with MUX-130; Defect B warns, never refuses, when launch intent and derived phase disagree |
| [`MUX-130`](../completed/MUX-130-spec-phase-parsing-semantics.md) | closed 2026-11-27 at 55/55 | Phases are `### Phase N` under `## Implementation` only; duplicate phase numbers warn at dispatch; a deliberately open item holds the loop and the gate text says so. Phase 4 rewritten around MUX-183. Added by the user 2026-11-27: a spec with no implementation section is never commit-ready |
| [`MUX-210`](./MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md) | 4/24 | Width overflow wraps in step order with no line wider than the pane, header included; the flat fallback is pinned through height overflow |
| [`MUX-111`](../completed/MUX-111-harness-reply-miscorrelation.md) | closed 2026-11-28 at 31/31 | Each request in a batch gets its own reply; self-messages filtered. The script skips its model section without Ollama. As shipped one conversation answers the batch and the same text goes to each request; the startup bootstrap is exempt from the filter on edit's call; the model section has never run |
| [`MUX-110`](../completed/MUX-110-harness-startup-tool-loop-exhaustion.md) | closed 2026-11-28 at 26/26 | Exhaustion terminal state and its lifecycle event ship now; harness roles get a closed-form startup message, wording validated where Ollama runs. As shipped the wording has never met a model: no Ollama here, so criterion 2 and the bounded-call check were deferred to MUX-249 and criterion 7 restated, on edit's call |
| [`MUX-008`](../completed/MUX-008-unverified-daemon-auto-restart.md) | closed 2026-11-27 at 24/24 | Orphan detection dropped; the scope was verifying old-process exit and post-launch readiness inside `scrapeAndRelaunch`, a failure returning an error. Edit stays resume-only. As shipped the waits run inside the daemon's poll loop under a keepalive heartbeat |
| [`MUX-190`](../completed/MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) | closed 2026-11-27 at 22/22 | Re-drive once per task with the guard-denial text, refused on a working pane; else alert edit naming task and node. As shipped a Codex or OpenCode agent always takes the alert: the re-drive road reaches Claude only |
| [`MUX-150`](../completed/MUX-150-verify-spec-names-last-routed-batch.md) | closed 2026-11-27 at 27/27 | Gate the analyze hook by path (`docs/`); the change set is what moved since the last verification. Gated on all of `docs/` by prefix; as a consequence the analyze role hears nothing about documentation edits |
| [`MUX-151`](./MUX-151-display-width-runes-not-cells.md) | 0/29 | Ambiguous-width characters are 1 cell; the table lives in `bus` with `tui` delegating |
| [`MUX-231`](./MUX-231-rename-session-strands-a-live-session.md) | 0/29 | Detect a rename (`session-renamed` row and an edit alert with the remedy) and route the bare-name callers through one exact-match helper |
| [`MUX-160`](../completed/MUX-160-tmp-go-cache-leak-unclearable-pressure.md) | closed 2026-11-27 at 32/32 | Decision 1 settled (test already granted, no sandbox by default); age floor only; stop until the footprint changes, 30 min ceiling. Shipped the accounting fix, a `disk-pressure-unclearable` event and the back-off; the re-measure found no `/tmp` Go cache on either road, so the removal stage was not built |
| [`MUX-181`](../completed/MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) | closed 2026-11-24 at 28/28 | One verifier at the send beside `CheckCommitAuthorityForMessage`, keyed on raw sender `daemon` plus the run and node's gate approval. Owns the verifier |
| [`MUX-165`](../completed/MUX-165-gated-jira-write-declined-by-requester-rule.md) | closed 2026-11-24 at 20/20 | Same seam as MUX-181; owns plan's rule change (`agents/planner.md`), which landed with MUX-181 |
| [`MUX-157`](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) | closed 2026-11-25 at 28/28 | `validationRoles` is build, test and review only, enforced by the hook guard and OpenCode `DenyTools`; the Codex sandbox criterion dropped. Three parts split out: MUX-243, MUX-244, MUX-245 |
| [`MUX-197`](./MUX-197-guard-and-allowlist-bypassable-by-command-shape.md) | 0/22 | The user's choice: accept and document the guard's shape gaps — the guard is a delegation nudge, bus authority checks are the boundary; the local-LLM allowlist matches plain commands only |
| [`MUX-166`](../completed/MUX-166-run-builds-on-stale-base-no-freshness-gate.md) | closed 2026-11-25 at 40/40 | The user's choice: gate at 5 commits behind `origin/main` by default, `MUXCODE_FRESHNESS_MAX_BEHIND` to change it; overlap always gates. Widened on the user's decision of 2026-11-25 to templates 55, 60, 80 and 90 |
| [`MUX-185`](../completed/MUX-185-history-row-provenance-declared-not-proven.md) | closed 2026-11-24 at 43/43 | One-line shortcut threat model; witness corroboration plus guard, daemon secret deferred; history files under any bus dir; `actor` is person or agent only; rely on `since`, no migration |
| [`MUX-010`](../completed/MUX-010-delegation-message-hygiene.md) | closed 2026-11-28 at 39/39 | Split: this spec keeps the hygiene half — `--payload-file` and stdin, a `--wait` stderr heartbeat about every 30 s, a louder warning, opt-in `MUXCODE_SEND_STRICT`, `--track` documented as the default. The frozen-agent watchdog is MUX-241. As shipped the typed wakes also flatten newlines and `FormatMessage` indents continuation lines, both found by the spec's own survey; the strict-mode off values are edit's call |

Points the rewrites left for a spec's own first phase, none of them a parked-grade decision:
MUX-231 (detect by tmux hook or daemon poll; what the five remote-control callers of
`TmuxHasSession` do), MUX-150 (all of `docs/` or Markdown only), MUX-008 (what proves readiness for
a non-Claude provider), MUX-151 (whether `narrowRune` derives from the new table). MUX-130 records
that the 2026-08-31 contiguous-prefix decision was not adopted by MUX-183 and leaves it out.
MUX-181 and MUX-165 cover `issue-write` as well as the Atlassian actions, because the validator's
list (`gatedTrackerWriteActions`) does — wider than the decision's wording.

**Closed 2026-11-24** on the user's decisions, relayed by edit. Each is in `completed/` reading
`Complete — closed as overtaken`: nothing was built under it, and its open boxes record what it
asked for. Its "Decisions (user, 2026-11-24)" section says what overtook it and what was left:

| Spec | Issue | Why |
|------|-------|-----|
| [`MUX-135`](../completed/MUX-135-spawn-seed-record-gc-strands-completion.md) | closed #113 | `spawnHasResponded` falls back to the worker's reply in the session log (MUX-195). No follow-up: negative controls exist; a rotated log is untested |
| [`MUX-153`](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) | closed #120 | No test binds a socket; Codex runs unsandboxed by default. The guard continues as MUX-238. The suite never ran under the Codex sandbox |
| [`MUX-152`](../completed/MUX-152-test-sh-hides-modules-after-first-failure.md) | closed #119 | Overtaken by MUX-174; `test.sh` keeps stop-at-first-failure, by decision |
| [`MUX-191`](../completed/MUX-191-bus-dir-subcommand-does-not-exist-but-agents-are-told-to-use-it.md) | closed #145 | `muxcode bus-dir` exists since MUX-208 |
| [`MUX-032`](../completed/MUX-032-loop-detector-granularity.md) | closed #32 | The recorded false alerts no longer fire; reopen as a new spec on a recorded tuple-pass false positive |
| [`MUX-123`](../completed/MUX-123-stall-watchdog-selective-misses.md) | closed #110 | Evidence gone, watchdog reworked; a narrow spawn-role regression spec only if such a test fails |
| [`MUX-127`](../completed/MUX-127-review-completion-routing.md) | closed #112 | Review-to-fix edges are in the templates; the refire fixed by MUX-007, MUX-172, MUX-219. The `Validate()` rule continues as MUX-239 |
| [`MUX-112`](../completed/MUX-112-idle-task-rescue-closes-live-work.md) | closed #46 | Closure is advisory since MUX-182. Gating the rescue on live background work continues as MUX-240 |
| [`MUX-120`](../completed/MUX-120-spawn-worker-never-woken-for-seeded-task.md) | closed #108 | Readiness-gated wake shipped; dirty worktree preserved; graph spawns take no worktree. Decisions recorded: fix at both source and daemon; dirty worktree refuses and alerts |
| [`MUX-147`](../completed/MUX-147-process-leak-and-memory-footprint.md) | [#116](https://github.com/mkober/muxcode/issues/116) left open | Split and closed: reaper to MUX-184, script cleanup to MUX-233/235/236, no harness self-exit, idle eviction deferred. The footprint continues as MUX-242, which now carries the issue |

### Defects — prioritized

**Tiers.** `0` stop the bleeding / in flight · `1` repair the instruments · `2` firing now ·
`3` spawn family · `4` phase semantics · `5` restore rebuild · `6` remainder.

| # | T | ID | Defect | Sev | Depends on |
|---|---|----|--------|-----|------------|
| 1 | 0 | [`MUX-215`](./MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) | `./test.sh` fails, passes on rerun, and nothing recorded which test failed | High | — · ⇄ [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) |
| 2 | 6 | [`MUX-122`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | Prompt-agent turn budget exhaustion | High | — |
| 3 | 6 | [`MUX-151`](./MUX-151-display-width-runes-not-cells.md) | Display width is measured in runes, not terminal cells | Low | — |
| 4 | 6 | [`MUX-173`](./MUX-173-prompt-profile-no-discovery-affordance.md) | The `prompt` profile has no discovery affordance | Low | — |
| 5 | 6 | [`MUX-197`](./MUX-197-guard-and-allowlist-bypassable-by-command-shape.md) | Guard rules and the local-LLM allowlist are bypassable by command shape | Medium | ⇄ [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) |
| 6 | 6 | [`MUX-210`](./MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md) | `test-graph-tui.sh` fails: a narrow pane renders no flat fallback, a failed node lists after a done one | Medium | — |
| 7 | 6 | [`MUX-231`](./MUX-231-rename-session-strands-a-live-session.md) | `tmux rename-session` strands a live session: its monitor and listeners exit | Medium | — |
| 8 | 6 | [`MUX-240`](./MUX-240-idle-task-rescue-acts-on-a-role-with-live-background-work.md) | Idle-task rescue re-queues and times out a task whose role has live background work | Medium | — |
| 9 | 6 | [`MUX-239`](./MUX-239-validate-accepts-a-review-node-with-no-failure-route.md) | `Validate()` accepts a graph whose review node has no failure route | Medium | — |
| 10 | 6 | [`MUX-238`](./MUX-238-nothing-fails-when-a-test-binds-a-socket.md) | Nothing fails when a test binds a socket | Low | — |
| 11 | 6 | [`MUX-241`](./MUX-241-no-watchdog-recovers-a-frozen-but-alive-agent.md) | No watchdog recovers a frozen-but-alive agent | Medium | — |
| 12 | 6 | [`MUX-243`](./MUX-243-a-halt-that-halts.md) | A halt request does not stop an agent's work, and a reload brings it back | Medium | ⇄ [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) |
| 13 | 6 | [`MUX-244`](./MUX-244-negative-control-deletion-is-not-checked.md) | Deleting or inverting a negative control leaves the suite green | Medium | ⇄ [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) |
| 14 | 6 | [`MUX-245`](./MUX-245-a-worker-certifies-its-own-change.md) | Nothing says whose verdict counts when a worker certifies its own change | Medium | ⇄ [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) |
| 15 | 6 | [`MUX-247`](./MUX-247-bus-suite-fails-under-codex-workspace-write-sandbox.md) | The bus suite fails under Codex's opt-in `workspace-write` sandbox | Medium | — |

### Reliability & observability

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-240`](./MUX-240-idle-task-rescue-acts-on-a-role-with-live-background-work.md) | Idle-Task Rescue Re-Queues and Times Out a Task Whose Role Still Has Live Background Work | Medium | — |
| [`MUX-241`](./MUX-241-no-watchdog-recovers-a-frozen-but-alive-agent.md) | No Watchdog Recovers a Frozen-but-Alive Agent | Medium | — |
| [`MUX-242`](./MUX-242-session-memory-footprint-has-no-reclamation-path.md) | A Session's Memory Footprint Only Grows — the Largest Consumers Have No Reclamation Path | Medium | — |
| [`MUX-238`](./MUX-238-nothing-fails-when-a-test-binds-a-socket.md) | Nothing Fails When a Test Binds a Socket | Low | — |
| [`MUX-215`](./MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) | `./test.sh` Fails, Passes on Rerun, and Nothing Recorded Which Test Failed | High | ⇄ [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) |
| [`MUX-231`](./MUX-231-rename-session-strands-a-live-session.md) | `tmux rename-session` Strands a Live Session — Its Monitor and Listeners Exit | Medium | — |
| [`MUX-125`](./MUX-125-usage-and-billing-modal.md) | Usage and Billing Modal | Medium | — |
| [`MUX-122`](./MUX-122-prompt-agent-turn-attribution-and-fix.md) | Prompt-Agent Turn Budget — Attribute, Then Fix | High | — |
| [`MUX-249`](./MUX-249-verify-closed-form-startup-bounded-against-live-local-model.md) | Verify the Closed-Form Startup Is Bounded Against a Live Local Model (task; needs Ollama) | Low | [MUX-110](../completed/MUX-110-harness-startup-tool-loop-exhaustion.md) |
| [`MUX-012`](./MUX-012-remove-gated-pane-scrape-delivery.md) | Remove gated pane-scrape delivery machinery | Low | — |
| [`MUX-013`](./MUX-013-channels-message-transport.md) | Channels-based message transport | Medium | — |
| [`MUX-230`](./MUX-230-integration-scripts-opt-out-of-the-consumer-guard.md) | Integration Scripts Run With the Consumer Guard Turned Off | Medium | [MUX-145](../completed/MUX-145-messages-routed-to-windowless-role.md) |

### Workflow & automation

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-239`](./MUX-239-validate-accepts-a-review-node-with-no-failure-route.md) | `Validate()` Accepts a Graph Whose Review Node Has No Failure Route | Medium | — |
| [`MUX-246`](./MUX-246-daemon-auto-approves-gates-on-self-approval-runs.md) | The Daemon Auto-Approves Gates on Runs Whose Creator May Self-Approve | Medium | — |
| [`MUX-173`](./MUX-173-prompt-profile-no-discovery-affordance.md) | The Prompt Profile Denies Its Own Discovery Commands | Low | — |
| [`MUX-138`](./MUX-138-github-versioning-releases.md) | GitHub versioning & releases | Medium | — |
| [`MUX-011`](./MUX-011-opencode-plugin-hook-bridge.md) | OpenCode Plugin Hook Bridge | High | — |

### Agents & roles

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-243`](./MUX-243-a-halt-that-halts.md) | A Halt Request Does Not Stop an Agent's Work, and a Reload Brings It Back | Medium | — |
| [`MUX-244`](./MUX-244-negative-control-deletion-is-not-checked.md) | Deleting or Inverting a Negative Control Leaves the Suite Green and Nothing Flags It | Medium | — |
| [`MUX-245`](./MUX-245-a-worker-certifies-its-own-change.md) | Nothing Says Whose Verdict Counts When a Worker Certifies Its Own Change | Medium | — |
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
| [`MUX-247`](./MUX-247-bus-suite-fails-under-codex-workspace-write-sandbox.md) | The Bus Suite Fails Under Codex's Opt-In Workspace-Write Sandbox | Medium | — |
| [`MUX-201`](./MUX-201-non-hook-prompt-and-generated-body-drift.md) | Non-Hook Prompt Text and Generated Agent Bodies Drift From the Definition | Medium | [MUX-142](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) |
| [`MUX-140`](./MUX-140-jira-issue-creation.md) | Jira Issue Creation From MuxCode | High | — |
| [`MUX-017`](./MUX-017-gemini-cli-provider.md) | Gemini CLI provider | High | — |
| [`MUX-018`](./MUX-018-opencode-diff-preview-plugin.md) | OpenCode diff preview plugin | Medium | — |
| [`MUX-019`](./MUX-019-github-user-stats.md) | GitHub user stats | Medium | — |

### UX & tooling

| ID | Title | Priority | Depends on |
|----|-------|----------|------------|
| [`MUX-210`](./MUX-210-graph-tui-script-narrow-pane-flat-fallback-and-failed-first-order-fail.md) | `test-graph-tui.sh` Fails — a Narrow Pane Renders No Flat Fallback, and a Failed Node Lists After a Done One | Medium | — |
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
| [`MUX-204`](../completed/MUX-204-claude-failing-bash-calls-never-reach-hook-bash.md) | Failing Bash Calls on Claude Never Reach `hook bash` — Only `PostToolUse` Is Registered |
| [`MUX-207`](../completed/MUX-207-xcode-project-compatibility.md) | Xcode and Swift Projects: Agents Lack the Patterns, Sandbox Access and Tools to Build, Test and Observe Them |
| [`MUX-139`](../completed/MUX-139-claude-agent-auto-resume.md) | Claude Agent Auto-Resume After Mass Exit |
| [`MUX-141`](../completed/MUX-141-auto-agent-restart-relaunches-graph-runs.md) | Auto Agent Restarts Relaunch Autonomous Graph Runs |
| [`MUX-142`](../completed/MUX-142-spawn-worker-delegates-into-wrong-tree.md) | Spawned Worker Delegates Build/Test Into the Wrong Tree |
| [`MUX-174`](../completed/MUX-174-test-sh-repo-wide-vet-failure-harness-sandbox.md) | `./test.sh` Fails Repo-Wide When the Harness Module's Vet Cannot Resolve a Stdlib Package |
| [`MUX-176`](../completed/MUX-176-run-chain-fires-success-on-backgrounded-call.md) | The Run Chain Reported Success Three Minutes Before the Script Finished — and It Failed |
| [`MUX-177`](../completed/MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload.md) | The Watch Chain Fires on Every Bash Call and Pastes the Raw Command Into the Message |
| [`MUX-208`](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) | Durable Bus State Lives in /tmp, and macOS Deletes It After Three Idle Days |
| [`MUX-168`](../completed/MUX-168-reinit-purges-active-spec-graph-run-survives.md) | Session Re-Init Purges the Active Spec While the Graph Run That Reads It Survives |
| [`MUX-172`](../completed/MUX-172-plan-verify-gate-hardcodes-success.md) | The Spec-Verification Gate Asks Its Own Question — `notifyPlanOnReview` Hardcodes `"success"` |
| [`MUX-213`](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) | Test Runs Nobody Requested Fire the Review Chain, and Every Review Is Answered Twice |
| [`MUX-214`](../completed/MUX-214-root-go-vet-false-alarm-from-the-build-agent.md) | The Build Agent's Root `go vet` Fails Every Time and Tells Edit the Tests Failed |
| [`MUX-216`](../completed/MUX-216-test-chain-reply-target-shared-receipt-and-request-reuse.md) | Three Gaps Left by MUX-213 — Reply Target Ignored Off the Hook Road, Auto-CC Copies Share a Receipt, One Request Authorizes Many Reviews |
| [`MUX-217`](../completed/MUX-217-agent-defs-reload-races-a-graph-dispatch-and-clears-it.md) | The Agent-Defs Watchdog Reloads an Agent the Same Second a Graph Dispatches to It, and the Relaunch Clears the Dispatch |
| [`MUX-009`](../completed/MUX-009-response-echo-chain-retrigger.md) | Response Echo Chain Retrigger |
| [`MUX-006`](../completed/MUX-006-diagnose-false-clean-verdict.md) | Diagnose False Clean Verdict |
| [`MUX-189`](../completed/MUX-189-batch-delivery-correlates-only-the-last-requests-reply.md) | Batch Delivery Correlates Only the Last Request's Reply |
| [`MUX-161`](../completed/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) | `upgrade-daemons` Cannot See the Daemons From a Codex Build Agent |
| [`MUX-162`](../completed/MUX-162-pr-review-fix-node-600s-send-cap.md) | The PR-Review Fix Node Dies on the 600 s Send Cap |
| [`MUX-137`](../completed/MUX-137-test-bus-dir-leak.md) | PreLaunch Tests Leak Real Bus Directories Into `/tmp` |
| [`MUX-218`](../completed/MUX-218-build-agent-vet-failure-row-rejected-as-build-evidence.md) | A Build Agent's Failing Vet Row Is Filed as Build Evidence but Rejected as Build Evidence |
| [`MUX-221`](../completed/MUX-221-diagnose-dead-agent-test-uncontrolled-and-hosted-role-remediation.md) | The Dead-Agent Diagnose Test Is Uncontrolled, and a Hosted Role's Remediation Names a Command That Refuses |
| [`MUX-224`](../completed/MUX-224-tests-use-the-real-global-memory-dir.md) | The Test Suite Creates and Reads the User's Real Global Memory Directory |
| [`MUX-155`](../completed/MUX-155-send-dedup-keys-on-target-not-sender.md) | `muxcode send` Drops a Message Because Another Agent's Task Is In Flight |
| [`MUX-156`](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) | An Orphaned Inbox Listener Consumes Messages Into the Void |
| [`MUX-170`](../completed/MUX-170-graph-dispatch-adopts-foreign-in-flight-task.md) | A Graph Dispatch Adopts a Foreign In-Flight Task That Shares Its Action |
| [`MUX-145`](../completed/MUX-145-messages-routed-to-windowless-role.md) | Messages Route to a Role With No Window, and Diagnose Prescribes an Impossible Fix |
| [`MUX-229`](../completed/MUX-229-codex-exec-args-test-flakes-on-temp-home-cleanup.md) | `TestCodexBuildExecArgs_NoShimWithoutInstallDir` Flakes on Its Temp-HOME Cleanup |
| [`MUX-175`](../completed/MUX-175-response-ack-ping-pong-has-no-brake.md) | Two Agents Can Acknowledge Each Other Forever — Response Traffic Has No Brake |
| [`MUX-180`](../completed/MUX-180-daemon-notifies-windowless-roles-forever.md) | The Daemon Notifies a Windowless Role Forever, and Its Inbox Grows Unbounded |
| [`MUX-184`](../completed/MUX-184-orphaned-session-processes-never-reaped.md) | Orphaned Session Processes Are Never Reaped |
| [`MUX-196`](../completed/MUX-196-agent-launch-expires-in-flight-graph-dispatch.md) | Relaunching an Agent Expires Its In-Flight Graph Dispatch |
| [`MUX-220`](../completed/MUX-220-wake-batch-drops-a-roles-own-delegation-reply.md) | The Wake Batch Drops a Listenerless Role's Own Delegation Reply, and May Re-Inject an Answered Request |
| [`MUX-223`](../completed/MUX-223-project-memory-resolves-against-the-callers-cwd.md) | Project Memory Resolves Against the Caller's Working Directory, Stranding Session Summaries |
| [`MUX-225`](../completed/MUX-225-reply-to-unknown-id-accepted-silently.md) | A Reply Whose `--reply-to` Names No Message Is Accepted Silently, and the Task Waits Out Its Cap |
| [`MUX-232`](../completed/MUX-232-redrive-arrives-truncated-with-no-id-and-no-reply-route.md) | A Re-Driven Request Arrives Truncated, With No Message Id and No Reply Route |
| [`MUX-219`](../completed/MUX-219-graph-run-review-fires-verify-spec-twice.md) | A Graph Run's Review Fires `verify-spec` Twice |
| [`MUX-227`](../completed/MUX-227-reload-of-a-hosted-role-replaces-the-host-agent.md) | `muxcode reload <hosted-role>` Replaces the Host Agent Instead of Refusing |
| [`MUX-209`](../completed/MUX-209-force-respond-script-non-forced-deliver-no-longer-surfaces-in-flight-skip.md) | `test-force-respond.sh` Fails — a Non-Forced `deliver` No Longer Surfaces the In-Flight Skip |
| [`MUX-211`](../completed/MUX-211-opencode-edit-agent-told-a-pretooluse-hook-enforces-delegation.md) | An OpenCode Edit Agent Is Told a PreToolUse Hook Enforces Its Delegation Rules |
| [`MUX-212`](../completed/MUX-212-stall-redrive-busy-script-stale-spinner-in-scrollback-pins-pane-busy.md) | `test-stall-redrive-busy.sh` Fails — a Stale Spinner in Scrollback Pins the Pane Busy |
| [`MUX-222`](../completed/MUX-222-command-runner-executes-one-request-and-replies-to-all.md) | The Run Agent's Definition Executes One Request of a Batch and Replies to All of Them |
| [`MUX-226`](../completed/MUX-226-graph-node-caps-script-not-isolated-from-real-tmux-and-binary.md) | `test-graph-node-caps.sh` Runs on the User's Real tmux Server and May Mix In the Installed Binary |
| [`MUX-228`](../completed/MUX-228-spec-dangling-alerts-on-a-ship-merges-transient-gap.md) | `spec-dangling` Alerts on the Transient Gap During a Ship Merge |
| [`MUX-233`](../completed/MUX-233-hermetic-test-scripts-run-on-the-users-real-tmux-server.md) | Hermetic Test Scripts Create Their Sessions on the User's Real tmux Server |
| [`MUX-234`](../completed/MUX-234-three-integration-scripts-fail-under-the-consumer-guard.md) | Three Integration Scripts Fail — Their Fixtures Send to Roles the Consumer Guard Refuses |
| [`MUX-235`](../completed/MUX-235-kill-server-alias-leaves-test-tmux-servers-running.md) | `tmux kill-server` Is Aliased to a Confirm Prompt, so Test Scripts Leave Their Private tmux Servers Running |
| [`MUX-237`](../completed/MUX-237-blank-xdg-state-home-makes-neovim-write-state-into-the-working-directory.md) | muxcode Blanks `XDG_STATE_HOME` in Every Pane, so Neovim Writes Its State Into the Working Directory |
| [`MUX-236`](../completed/MUX-236-test-all-leaves-stray-and-modified-files-in-the-checkout.md) | `scripts/test-all.sh` Leaves Stray and Modified Files in the Checkout |
| [`MUX-032`](../completed/MUX-032-loop-detector-granularity.md) | Loop-Detector Granularity (closed as overtaken) |
| [`MUX-112`](../completed/MUX-112-idle-task-rescue-closes-live-work.md) | Idle-Task Rescue Closes Tasks That Are Still Running (closed as overtaken) |
| [`MUX-120`](../completed/MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Spawned Workers Never Receive Their Seeded Task (closed as overtaken) |
| [`MUX-123`](../completed/MUX-123-stall-watchdog-selective-misses.md) | Stall Watchdog Fires Routinely — and Still Missed Three Live Stalls (closed as overtaken) |
| [`MUX-127`](../completed/MUX-127-review-completion-routing.md) | Review Completion Routes Nowhere on Failure and Loops on Success (closed as overtaken) |
| [`MUX-135`](../completed/MUX-135-spawn-seed-record-gc-strands-completion.md) | Delivery-Record GC Permanently Strands a Long Spawn Iteration (closed as overtaken) |
| [`MUX-147`](../completed/MUX-147-process-leak-and-memory-footprint.md) | Reap Orphaned Harness Processes and Reduce Session Memory Footprint (split and closed) |
| [`MUX-152`](../completed/MUX-152-test-sh-hides-modules-after-first-failure.md) | `test.sh` Hides Every Module After the First Failure (closed as overtaken) |
| [`MUX-153`](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md) | A Codex Test Agent Structurally Cannot Run This Repo's Suite (closed as overtaken) |
| [`MUX-191`](../completed/MUX-191-bus-dir-subcommand-does-not-exist-but-agents-are-told-to-use-it.md) | `muxcode bus-dir` Does Not Exist, but Agent Definitions Tell Agents to Use It (closed as overtaken) |
| [`MUX-185`](../completed/MUX-185-history-row-provenance-declared-not-proven.md) | A History Row's Provenance Is Declared, Not Proven |
| [`MUX-181`](../completed/MUX-181-graph-atlassian-write-judged-on-configuration-not-gate.md) | A Graph-Dispatched Atlassian Write Is Judged on Configuration, Not on the Gate |
| [`MUX-165`](../completed/MUX-165-gated-jira-write-declined-by-requester-rule.md) | A Gate-Approved Jira Write Is Declined Because the Rule Checks the Messenger, Not the Consent |
| [`MUX-166`](../completed/MUX-166-run-builds-on-stale-base-no-freshness-gate.md) | A Run Builds on a Stale Base — Branch Freshness Is a Note, Not a Gate |
| [`MUX-157`](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) | A Role Boundary an Agent Can Ignore Is Not a Boundary |
| [`MUX-124`](../completed/MUX-124-lifecycle-since-truncated-by-limit.md) | `lifecycle show --since` Silently Answers the Wrong Question |
| [`MUX-188`](../completed/MUX-188-spawn-worker-launch-sends-edit-a-stray-startup-request.md) | Every Spawn Worker Launch Sends Edit a Stray Startup Request |
| [`MUX-190`](../completed/MUX-190-codex-agent-drops-a-consumed-request-after-a-guard-denial.md) | A Codex Agent Drops a Consumed Request After a Guard Denial |
| [`MUX-160`](../completed/MUX-160-tmp-go-cache-leak-unclearable-pressure.md) | The /tmp Go Caches the Disk-Pressure Sweep Counts but Cannot Clear |
| [`MUX-150`](../completed/MUX-150-verify-spec-names-last-routed-batch.md) | `verify-spec` Names the Last Routed Batch, Not the Change Set |
| [`MUX-130`](../completed/MUX-130-spec-phase-parsing-semantics.md) | Spec Phase Parsing: Two Definitions of Complete, Matched Document-Wide |
| [`MUX-143`](../completed/MUX-143-run-carries-two-phase-identities.md) | A Run Carries Two Unreconciled Phase Identities |
| [`MUX-248`](../completed/MUX-248-analyze-hook-test-writes-the-real-tmp-trigger.md) | An Analyze-Hook Test Writes the Real `/tmp` Trigger on Every Suite Run |
| [`MUX-008`](../completed/MUX-008-unverified-daemon-auto-restart.md) | Unverified Daemon Auto-Restart |
| [`MUX-194`](../completed/MUX-194-stale-reload-marker-cleanup-breaks-exclusive-lock.md) | Stale Reload-Marker Cleanup Can Delete a Live Exclusive Lock |
| [`MUX-111`](../completed/MUX-111-harness-reply-miscorrelation.md) | Harness Reply Correlates to the Batch's Last Message, Not the Request |
| [`MUX-110`](../completed/MUX-110-harness-startup-tool-loop-exhaustion.md) | Harness Startup Message Exhausts the Tool Loop |
| [`MUX-010`](../completed/MUX-010-delegation-message-hygiene.md) | Delegation Message Hygiene |

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
