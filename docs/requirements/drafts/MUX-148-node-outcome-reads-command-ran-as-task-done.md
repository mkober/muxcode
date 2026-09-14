# MUX-148: A Node Outcome Reads "a Command Ran" as "the Task Was Done"

A graph `send` node whose agent **declines** the task is recorded as
**`outcome=success`** and routed onward, because outcome derivation reads the newest successful
shell command the agent ran — and an agent inspecting state in order to refuse produces
byte-for-byte the same evidence as an agent doing the work.

Observed live 2026-09-03 by the edit agent and independently flagged by the commit agent in the
same run.

## Context

### Observed

Run `1788457453-commit-pr-review-loop-9fa65bca`, template `commit-pr-review-loop`, node `d`
(`send` → `commit:comment`, *"Reply to the PR comments"*).

The commit agent **declined**:

> Cannot post PR replies yet: node c's fix (…617 insertions) is uncommitted and unpushed — this
> template has no commit node between c and d, so there's nothing to cite as a fix sha yet. Not
> committing on my own initiative under a comment action.

The executor recorded `d` as **`outcome=success`** and routed on to `close-gate`. **The PR replies
were never posted.** The commit agent noticed ~5 minutes later and posted them by hand:

> graph node d earlier marked success from my decline reply
> (unknown-outcome-falls-back-to-success gap), so it silently skipped the real reply-posting step —
> done now for real.

Corroborated independently: a `muxcode graph status` run during the incident showed
`c … outcome=unknown` and `d … outcome=success`, with `close-gate` already `done`.

### Mechanism — verified in code

| Step | Code | Behaviour |
|------|------|-----------|
| Outcome derivation | `deriveSendOutcome` (`bus/graph_exec.go:1534`) | Returns `failure` only if the response message's action is literally `error`; otherwise defers to the newest authoritative console row |
| Row selection | `latestAuthoritativeRow` (`:1586`) | Walks console history backwards, skipping rows older than dispatch, `SourceBusResponse` rows, and unknown/empty outcomes — returns the **newest row with a real exit code** |
| Sentinel fallback (**added after filing**, `c4997ed`) | `parseExitSentinel` (`:1556`) | Reads a self-reported `EXIT=<n>` from the reply body — but **only when no authoritative row was found**, so it cannot correct one |
| Consequence | — | While composing its refusal the agent ran read-only `gh`/`git` commands. Those rows carry **exit 0 → `OutcomeSuccess`**, so the provenance doctrine proved *"a command ran successfully"* and the router read it as *"the node did its job"*. ⚠️ **This row's account of *which* command minted the row was disproved by Phase 1** — read-only `gh`/`git` write no row for the `commit` role. The false green is real; its source is not yet established. See [Phase 1 findings](#the-filed-mechanism-does-not-reproduce--re-derive-the-incident-before-fixing-it) |

**The evidence an agent produces when it declines a task while inspecting state is
indistinguishable from the evidence it produces when it performs the task.** Recency plus exit
code cannot separate them.

### Re-verified against the tree, 2026-09-14

Filed 2026-09-03; re-read against `cc7f47d` before starting work. **Both defects are intact and
neither has been mitigated in passing.** Line numbers throughout this spec were stale by ~370
lines and have been corrected.

| Claim | Verdict |
|-------|---------|
| `latestAuthoritativeRow` selects on recency + exit code, skipping `SourceBusResponse` and unknown/empty | **Unchanged** — predicate identical, now `:1586` |
| Unknown-hold fires only on `OutcomeUnknown` | **Unchanged**, now `:1684` |
| Template `c` → `d` with no commit node between | **Unchanged** — `{"from": "c", "to": "d"}`, nodes now `:76-77` |
| `verify-pr` PR-CONFIRMED token precedent | **Unchanged**, now `:79-80` |
| `scripts/test-node-outcome-attribution.sh` absent | Still absent |

**One material change: `deriveSendOutcome` gained a third step.** `parseExitSentinel` (`c4997ed`,
MUX-144 Phase 2) reads a self-reported `EXIT=<n>` from the reply body. It sits **below** the
console row in precedence, which makes the defect sharper than originally stated:

> An agent that declines **and correctly self-reports `EXIT=1`** is *still* recorded
> `success`, because the read-only command it ran while deciding to decline wins first.

The agent's own verdict about its own work is already being collected and is already being
overruled by a shell exit code that was never about the task. That reframes the options below —
see the revised table.

Two constraints on Phase 3 that did not exist when this was filed:

- ~~**`hook_codex_test.go:490-531` pins `latestAuthoritativeRow`**, so a precedence change must keep
  it green.~~ **Withdrawn 2026-09-14** — this claim was wrong when written here, caught by Phase 1 and
  re-verified independently. `TestCodexHookRow_IsAuthoritativeForGraph` calls `latestAuthoritativeRow`
  **directly** and never calls `deriveSendOutcome`; only its doc comment names it. It pins the
  *helper*, not the *precedence*, and stays green under any precedence change. **It is not a
  constraint on Phase 2** — and there is no other: see [the safety-net finding](#q4--should-the-sentinel-outrank-the-console-row).
- **MUX-154 is still open** ([`backlog/MUX-154`](../backlog/MUX-154-codex-status-line-closes-tracked-tasks.md))
  and is cited in `parseExitSentinel`'s own comment: a reply that echoes its request can carry a
  counterfeit sentinel. Leaning harder on the sentinel inherits that exposure.

### Why the existing unverified hold does not catch it

The hold (`graph_exec.go:1684`) fires only on **`OutcomeUnknown`**: a node with no matching edge
and an unknown outcome is parked for approval rather than assumed successful, unless every
successor is a human gate or the hold was already released. This path produces a **confident
`success`**, so no hold is raised, no human is asked, and no lifecycle warning is emitted.

**Strictly worse than the unknown case the hold was built for** — unknown at least surfaces as
uncertain and parks for a person; this produces a **false green**.

> **Provenance correction.** This hold is commonly called "the MUX-136 hold" because it landed in
> commit `16f2027` (*"MUX-136 Verify actor identity and attribute gate approval/graph
> provenance"*). It is **not MUX-136 work**: MUX-136's spec never mentions it, and **no spec in
> `docs/requirements/` describes it at all**. Every commit on that branch carries the `MUX-136`
> prefix regardless of subject, so `git log` by prefix misattributes it. The hold is real,
> shipped, and unspecified.

### Blast radius

Any `send` node whose agent declines, partially completes, or errors **in prose** after having run
any successful shell command — which is most agents, since inspecting state is how they decide to
decline. Most dangerous **immediately upstream of a mutation**.

In this run it advanced to `close-gate` on the false signal. **Only two accidents prevented a spec
close-out on it**: the `spec-complete` guard, and the session happening to have no active spec.
Neither is a guarantee — the guard checks the spec's own completeness, not whether the upstream
node did its job.

### Defect 2 — the template puts node `d` in an impossible position

`commit-pr-review-loop` has **no commit node between `c` and `d`** (verified,
`bus/graph_templates.go:76-77`):

```
… → gate2 → c (edit: "Address the PR review comments")
          → d (commit:comment: "Reply to the PR comments")
          → close-gate → …
```

`c` makes changes; `d` is asked to reply to review comments citing a fix — but nothing has
committed `c`'s work, so **there is no sha to cite**. A run in which `c` changes anything leaves
`d` structurally unable to succeed.

**The commit agent's decline was correct behaviour.** The template, not the agent, was wrong.
Defect 1 then converted that correct refusal into a false green — the two compounded, but each is
independently reachable and either could be fixed alone.

## Requirements

### Acceptance criteria

- [ ] A node whose agent **declines** the task is not recorded as `success`
- [ ] A node that **genuinely succeeds** is still recorded as `success` — **negative control: a fix that holds everything is not a fix**
- [ ] The distinction does **not** rely on parsing prose
- [ ] A node that cannot be tied to its dispatched work surfaces as a hold or failure, never as a silent success
- [ ] Whatever signal is chosen degrades safely for non-hook providers, which infer outcomes and cannot be assumed to emit it
- [ ] `commit-pr-review-loop` can complete a run in which `c` made changes
- [ ] A lifecycle event records any node whose outcome could not be positively established
- [ ] `bash scripts/test-node-outcome-attribution.sh` passes

### Technical approach — options, deliberately not yet chosen

| # | Option | Cost | Risk |
|---|--------|------|------|
| 1 | **Correlate the authoritative row with the dispatched work** — match on command shape or action, not merely recency and exit code | Medium | Correlation heuristics can drift from what agents actually run |
| 2 | **Structured decline** — a response field the executor maps to failure or hold | Low | Only as good as agent discipline; **prose parsing is not acceptable**. **Half-built as of 2026-09-14**: `parseExitSentinel` already collects the agent's verdict, but ranks below the console row, so it never overrides a false green. Promoting it is a small diff with a large blast radius — every role that does not emit the sentinel then falls through to today's behaviour, and MUX-154 makes a counterfeit sentinel reachable |
| 3 | **Require positive evidence** for nodes whose success matters — the literal-token pattern | **Lowest** | Per-node, not general; every new node must remember to opt in |
| 4 | **Widen the hold** — a node whose authoritative row cannot be tied to the dispatched work holds rather than routes | Medium | Risks holding on legitimate successes; needs the negative control above |

**Option 3 has precedent in this very template**: `verify-pr` already demands *"Your reply MUST
contain the literal token `PR-CONFIRMED`"* and gates on it with a `pr-check` condition node
(`graph_templates.go:79-80`). **The template author already hit this problem once and solved it
locally for one node** — which is evidence both that the pattern works and that a per-node fix
does not generalise on its own.

Options 1 and 4 are the general fixes; 3 is the cheap immediate mitigation. They are not exclusive
— 3 can ship first for the mutation-adjacent nodes while 1 or 4 is built.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph_exec.go` | `deriveSendOutcome:1534`, `parseExitSentinel:1556`, `latestAuthoritativeRow:1586`, unknown-hold routing `:1684` |
| `tools/muxcode/bus/graph_templates.go` | `commit-pr-review-loop:72-100` — the `c`→`d` gap and the `verify-pr` token precedent |
| `tools/muxcode/bus/commit_authority.go` | `checkGraphCommitDispatch:166`, `dispatchMatchesNode:208` — what a commit node inserted in Phase 4 must satisfy |
| `tools/muxcode/bus/hook.go` | `DefaultGitPatterns:288-292` (mutating git/gh only), `ClassifyCommand`, `ProcessBashHook` `case CmdUnknown:803-814` — where authoritative rows are minted |
| `tools/muxcode/cmd/log.go` | `runLog:40`, `:136-143`, `:157-171` — the unguarded authoritative writer with an agent-chosen exit code |
| `tools/muxcode/bus/prompt.go` | `:133`, `:176-213` — the instructions that tell non-hook providers to self-log `--exit-code 0` |
| `tools/muxcode/bus/console.go` | `ConsoleEntry`, `SourceBusResponse`, how outcome rows are written |
| `tools/muxcode/bus/graph_run.go` | `TransitionGraphNode`, node status persistence |
| `tools/muxcode/bus/lifecycle.go` | `LogLifecycle` for the unestablished-outcome event |
| `scripts/test-graph-orchestrator.sh` | Existing graph integration harness to extend or model on |

## Implementation

### Phase 1: Establish the boundary

- [x] Enumerate every path that can set a `send` node's outcome, and what evidence each trusts — four paths, tabulated under [Mechanism](#mechanism--verified-in-code) and re-verified 2026-09-14
- [x] Determine how often a declining agent emits a successful command row (sample real console history) — **answered as far as it can be: not computable retrospectively** (history is `/tmp`-only and the incident's is gone). The underlying premise is measured instead: 80% of live authoritative success rows come from commands that cannot evidence completion
- [x] Confirm whether non-hook providers (OpenCode/Codex) can emit any positive signal at all — **both yes**: Codex from a real transcript exit code, OpenCode via `muxcode log` (which is itself unguarded)
- [x] Decide whether `deriveSendOutcome`'s `action == "error"` check can be widened without prose parsing — **yes, and the channel is already open**; the work is doctrine + tests + four guardrails, not capability
- [x] **Gather the evidence on whether `parseExitSentinel` should outrank the console row** — who emits a sentinel (5 roles instructed, 9 not), what a non-emitting role falls back to, and the MUX-154 exposure a flip inherits. **Misfiled as a *decision* when added on 2026-09-14**: this phase's own last step is "record findings *before* choosing an option", so the choice moved to Phase 2 where it belongs
- [x] Record findings here before choosing an option

### Phase 1 findings — recorded 2026-09-14

Investigated by edit, spot-verified by plan. Full report: `/tmp/mux-148-phase1-findings.md` (non-durable;
the load-bearing findings are reproduced here). **No option chosen — that is Phase 2, and it is the
user's call.**

#### The filed mechanism does not reproduce — re-derive the incident before fixing it

`DefaultGitPatterns` (`hook.go:288-292`) match **only mutating** git/gh:
`git*commit|push|merge|rebase|tag|cherry-pick`, `gh*pr*create|merge|close`, `gh*release*create`.
**Verified.** So read-only `gh pr view` / `git status` from the `commit` role write **no console row
at all** today — and this spec's filed mechanism ("*while composing its refusal the agent ran
read-only `gh`/`git` commands … carrying exit 0*") **cannot have produced the 2026-09-03 false green
for the commit role.**

Two paths that *can*, both verified in source:

1. **Prose spoofs the classifier.** `matchPatterns` globs the whole command string, so
   `git checkout main && … && echo "--- pre-rebase HEAD ---"` classifies as `CmdGit` **because the
   word "rebase" appears inside an `echo`**. An agent narrating *"cannot rebase — no sha to cite"*
   mints an authoritative row. Live and reproducible.
2. **`muxcode log` is an unguarded authoritative writer.** `runLog` (`cmd/log.go:40`) has no provider
   gate, no authority check, and writes no `source` — so the row is authoritative and its exit code
   is **chosen by the agent**. `bus/prompt.go:203,209` hardcodes `--exit-code 0` in the instructions
   given to non-hook providers. **Any agent can mint its own success verdict at will.**

> **Phase 2 must re-derive which of these actually fired on 2026-09-03, or it risks hardening a path
> that was never the one that broke.** The original history is gone (below), so this may only be
> answerable by reasoning about the commit agent's role and provider at the time.

#### The authoritative artifact is not durable — no retrospective rate is computable

`BusDir` hardcodes `/tmp/muxcode-bus-<session>` and `bus/rotation.go` archives **memory files only**;
there is no history archive. The 2026-09-03 incident's console history is **gone**, `graphs/` holds 0
runs, and `~/.config/muxcode/logs/` records no node-outcome events. **A decline rate cannot be
measured retrospectively, and none was estimated.** That non-durability is itself a finding: the
artifact the router treats as authoritative does not survive a reboot.

What the live corpus does establish (40 authoritative rows, both sessions):

| Measure | Count |
|---|---|
| Authoritative rows | 40 |
| exit 0 / `success` | **40 (100%)** |
| failures | 0 |
| produced by **non-mutating** commands (`ps`, `cat`, `muxcode …`) | **32 (80%)** |

**80% of authoritative success rows came from commands that cannot evidence task completion**, and
every one satisfies `latestAuthoritativeRow`. Mechanism confirmed independently of the sample:
`ProcessBashHook` `case CmdUnknown` (`hook.go:803-814`) writes an authoritative row for **any**
unclassified command when the role is `run`, `runner` or `watch` — **verified**. Counts are inflated
by exact-duplicate rows (2–3× for some entries); treat them as order-of-magnitude.

One anomaly **flagged, not asserted**: run-history holds hook-road rows whose command is
`muxcode inbox`, though `ClassifyCommand` returns `CmdBus` for `muxcode*` and `ProcessBashHook`
returns before writing (`hook.go:710-712`). Either a second hook road exists or the deployed
classifier differs from branch source. Unresolved — it decides whether bus commands also mint
authoritative rows.

#### Q2 — the OpenCode scrape road **can** emit a positive signal

Not via hooks (`SupportsHooks()` false, every hook entry point returns early) but via **`muxcode log`**,
which writes `Source == ""` — authoritative — with the outcome taken from `--exit-code`.
`bus/prompt.go:133,176-213` emits exactly that instruction to non-hook providers ("Always log before
sending your response message"), materialized live in `.opencode/agents/test.md:207-213`.

**So a prompt-following scrape-road agent writes an authoritative success row that `deriveSendOutcome`
consumes at step 2, before the sentinel is ever reached.** Any Phase 2 mechanism can be universal
**only if it also closes `cmd/log.go`**; otherwise it must degrade explicitly. Contrast the Codex hook
road, where authority is earned from a real transcript exit code (`CodexExitCodeFromTranscript`).

#### Q3 — the `error` channel is already wide open; widening is doctrine, not capability

One producer in Go (`daemon/daemon.go:2851-2853`, pane-scrape `errored`). **No action allowlist
anywhere** — `cmd/send.go:23` takes the action verbatim, so `muxcode send edit error "…" --type response`
works today (proven live by `test-graph-orchestrator.sh:135`). **Zero agent definitions teach it**; the
taught failure signal is universally the `EXIT=<n>` sentinel. Guardrails Phase 2 must weigh if it goes
this way: `sendResponseIsNonResult` runs *before* `deriveSendOutcome`; `wait_event` releases on **any**
message whose action equals the event name, so a user-authored `event: "error"` would be released by an
unrelated decline; `bus/webhook.go:125-163` accepts a caller-supplied `action` **and** `reply_to`, and
responses bypass the authority gates; and `NewBusResponseEntry` keys on the *request* action, so a
request named `error` writes a hard failure row into the target's history.

#### Q4 — should the sentinel outrank the console row?

**There is no safety net.** No test anywhere calls `deriveSendOutcome` — **verified independently**;
`parseExitSentinel` is unit-tested only in isolation. **The precedence ordering, which is the defect
itself, is pinned by nothing**, so Phase 2 is equally free to fix it and to regress it unnoticed.
Whatever is chosen **must ship with the first test of `deriveSendOutcome`.**

Sentinel coverage today — instructed: **build, test, review, commit, plan**. Not instructed: **run,
watch, deploy, serve, api, analyze, edit, research, docs**. Flipping precedence would help exactly the
five that emit it and leave the rest at status quo (no regression, no fix) — and the uncovered `run`
and `watch` are precisely the roles whose `CmdUnknown` rows are least meaningful. The tension, not
resolved here: today a real failing row outranks a counterfeit `EXIT=0`; after a flip it would not, so
flipping inherits MUX-154's exposure, with last-sentinel-wins as a partial mitigation, not a guarantee.

#### Doctrine inconsistency to fix either way

`agents/git-manager.md:92` and `agents/planner.md:208` both assert that a read-only / nothing-to-do
reply "runs no git command, so no hook row backs it". **True** for the commit role under current
`DefaultGitPatterns` — but **false** wherever prose spoofs a pattern or the agent self-logs. The
instructions promise an unverified hold the code may not deliver.

### Phase 2: Choose and record the fix

- [ ] **Re-derive which path actually minted the 2026-09-03 row** (prose-spoofed classifier vs self-logged) before choosing — Phase 1's blocking recommendation; the original history is gone, so this may be answerable only by reasoning
- [ ] **Decide whether `parseExitSentinel` should outrank the console row**, and what happens to a role that emits no sentinel — moved here from Phase 1, where it was misfiled as investigation
- [ ] Decide whether the unguarded `muxcode log` writer (`cmd/log.go`) is in scope here or its own spec
- [ ] Weigh options 1–4 against the Phase 1 findings
- [ ] Choose a general mechanism and, if different, a cheap immediate mitigation
- [ ] Confirm the choice satisfies the "genuine success still succeeds" criterion by construction
- [ ] Record the decision and rationale in this spec

### Phase 3: Implement outcome attribution

- [ ] Implement the chosen mechanism with unit tests
- [ ] **Negative control test:** a node that genuinely succeeded still routes as success
- [ ] Emit a lifecycle event when a node's outcome cannot be positively established
- [ ] Ensure the unknown-hold and the new path do not double-hold the same node

### Phase 4: Fix the template gap

- [ ] Add a commit step between `c` and `d` in `commit-pr-review-loop`, or restructure so `d` has a citable sha
- [ ] Confirm the new node is gated per the commit-authority rules (a commit node must be downstream of a `wait_human`) — since MUX-144 this is enforced at runtime by `checkGraphCommitDispatch`, which walks `gateTerritory` for a covering gate and re-checks its approval; an inserted node falls in **`gate2`**'s territory, so verify `gate2`'s single approval legitimately covers both `c` and the new commit node
- [ ] Verify `graph validate` still passes for the amended template
- [ ] Check the other six builtin templates for the same shape — a node asked to cite work that nothing has committed

### Phase 5: Integration test

- [ ] Create `scripts/test-node-outcome-attribution.sh` (hermetic; scratch bus, tmux session and daemon)
- [ ] Test: a scratch node whose agent **declines after running a successful read-only command** does **not** route as success
- [ ] **Negative control:** a node whose agent genuinely completes the task **does** route as success
- [ ] Test: the declining node emits the lifecycle event and holds rather than advancing
- [ ] Test: a `commit-pr-review-loop`-shaped run in which `c` changes files reaches `d` with a citable commit
- [ ] Test: no prose parsing is involved — a decline worded differently is still caught
- [ ] Coverage floor keeps a skipped section from reporting green
- [ ] Run the script and verify all checks pass

## Open decisions

### Decision 1 — general fix, cheap fix, or both?

Option 3 could ship today for mutation-adjacent nodes; options 1 and 4 take longer but cover nodes
nobody remembered to annotate. **Recommendation: both** — token-gate the nodes immediately
upstream of mutations now, and build the general correlation behind it. Shipping only option 3
leaves the class open for every future node.

### Decision 2 — what happens to a node that cannot be attributed?

Hold for a human, or fail the run? Holding matches the existing unknown behaviour and is
recoverable; failing is louder but turns any attribution gap into a broken run. **Recommendation:
hold**, consistent with `graph_exec.go:1684`.

### Decision 3 — should the unverified hold be specified retroactively?

It is shipped, load-bearing, and **described by no spec**. Whether to write it up here, in its own
spec, or in the architecture doc is a judgement call — but leaving working safety machinery
undocumented is how it gets removed by a later tidy-up.

## Out of scope

- **Whether agents should decline at all.** The decline here was correct; this spec is about the
  executor believing the wrong thing afterwards.
- **The `spec-complete` guard.** It behaved correctly and is not implicated — it simply is not a
  substitute for upstream node attribution.

## Status

In Progress

Re-verified against `cc7f47d` and started 2026-09-14 on the user's instruction. Both defects
intact; line numbers corrected throughout; the post-filing arrival of `parseExitSentinel` recorded
under [Re-verified against the tree](#re-verified-against-the-tree-2026-09-14) and folded into
option 2. Phase 1 delegated to edit.

**Phase 1 complete 2026-09-14, 6/6** (spec 6/37) — findings recorded above. No code was changed and
no option was chosen; Phase 1 is investigation by design, so there is no implementation to verify.

Phase 1's sixth step was **misfiled as a decision** when added earlier that day and has been
reworded to its evidence half, with the choice moved to Phase 2 — this phase's own final step is
"record findings *before* choosing an option", so a decision inside it contradicted the phase.
Phase 2 gained three steps as a result (re-derive the incident, the sentinel-precedence decision,
and the `cmd/log.go` scope call), which is why the total moved 34 → 37.

Phase 1 **disproved this spec's own account of the mechanism** (read-only `gh`/`git` mint no row for
the `commit` role) and **withdrew a constraint plan had asserted** (`hook_codex_test.go` pins the
helper, not the precedence). Both corrections are recorded inline above rather than silently edited
away. **The blocking question for Phase 2 is now: which path actually minted the 2026-09-03 row?**

This spec still sits in `docs/requirements/backlog/`. `muxcode spec set` warned that a spec outside
`drafts/` may not trigger post-review verification, so the automatic `verify-spec` pass will not
fire for it until it is moved — the move is the user's call.
