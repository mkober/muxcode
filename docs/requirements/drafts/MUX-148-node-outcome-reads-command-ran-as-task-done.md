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
| **Spawn road — no derivation at all** (at `846251e`) | `spawnGroupOutcome` (`:1455`), called from `harvestRunningNode` (`:1242`) | `outcome` starts at `success`; an answered seed is a bare `continue`; it degrades only for a missing, stopped or unknown-status worker. **Content-blind** — none of the send-road machinery above is consulted. See [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all) |
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

Any **spawn** node whose worker declines — and here no command evidence is needed at all, because the
spawn road reads none ([Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all)). In
`spec-to-pr` the `implement` and `fix` nodes are both spawn nodes: the two that do the real work.

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

### Defect 3 — the spawn road reads no evidence at all

**Added 2026-09-14 on the user's instruction to widen Phase 3 to the spawn road.** Found by edit,
re-verified by plan against `846251e` and `muxcode graph status`.

Phases 1–2 investigated the **send** road only: `latestAuthoritativeRow`, `deriveSendOutcome`,
`parseExitSentinel`, console rows. A spawn node touches none of it. `spawnGroupOutcome`
(`graph_exec.go:1455` at `846251e`) is a separate derivation with no evidence check:

```go
outcome := OutcomeSuccess
...
if e.SeedMsgID != "" && spawnHasResponded(session, e) {
    continue // iteration answered
}
```

`outcome` starts at `success` and degrades only when a worker is missing, stopped, or of unknown
status. The answered branch is a bare `continue`, so **any reply yields success** — it cannot
distinguish work done from work declined. As scoped before this addition, MUX-148 would have shipped a
send-road fix and left the defect live on the road where it actually fires: in `spec-to-pr`,
`implement` and `fix` are spawn nodes.

**Reference reproduction — on this spec's own run.** Run `1789399519-spec-to-pr-5aa52382`, node
`implement`, 2026-09-14:

| | |
|---|---|
| Worker reply | *"Phase 2 is a decision phase, 6/7, NOT closeable by an agent: item 3 (cmd/log.go scope) is reserved for the user and I did not decide it. … No code change, no spec edit."* |
| Recorded | `outcome=success took=194s` (`muxcode graph status`, read by plan) |

A correct, principled refusal recorded as success: the first acceptance criterion failing on the run
that exists to fix it. This is the strongest evidence the spec holds and is the reproduction Phase 5's
spawn test should model.

**A fix landed while this was being recorded.** `5b32433` (11:39, committed through run
`1789399519`'s `phase-gate` on the user's approval) changes `spawnGroupOutcome` to read
`parseExitSentinel(spawnReplyPayload(…))`: a reply with no sentinel resolves `OutcomeUnknown` (held),
failure outranks unknown across a group, and `harvestRunningNode` ports on `!= failure` so a held
node's work still lands in the tree for the human to judge. It ships
`TestSpawnGroupOutcomeReadsTheReplyNotTheFactOfReplying` (declined, `EXIT=0`, `EXIT=1`, sentinel-free,
plus missing/stopped/running workers and failure-outranks-unknown). **It has not been through the
build→test→review chain**: the run's build, test and review nodes completed at 11:28–11:31, before the
change existed, and the commit node commits whatever is in the tree. It takes one side of the
[design tension](#design-tension--the-spawn-signal-and-the-sentinel-agents-omit) below without the
choice being recorded; Phase 3's steps stay open until the choice is on the record and the chain has
run on this tree.

## Requirements

### Acceptance criteria

- [ ] A node whose agent **declines** the task is not recorded as `success`
- [ ] A node that **genuinely succeeds** is still recorded as `success` — **negative control: a fix that holds everything is not a fix**
- [ ] The distinction does **not** rely on parsing prose
- [ ] A node that cannot be tied to its dispatched work surfaces as a hold or failure, never as a silent success
- [ ] Whatever signal is chosen degrades safely for non-hook providers, which infer outcomes and cannot be assumed to emit it
- [ ] `commit-pr-review-loop` can complete a run in which `c` made changes
- [ ] A lifecycle event records any node whose outcome could not be positively established
- [ ] A **spawn** node whose worker **declines** is not recorded as `success` — the [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all) reproduction no longer reproduces
- [ ] A **spawn** node whose worker genuinely completes the work **is** still recorded as `success` — **negative control: a fix that holds every spawn node is not a fix**
- [ ] A **spawn** node whose outcome cannot be positively established emits the lifecycle event and holds
- [ ] The spawn-road distinction does **not** rely on parsing worker prose
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
| `tools/muxcode/bus/graph_exec.go` | Send road: `deriveSendOutcome:1705`, `parseExitSentinel:1739`, `latestAuthoritativeRow:1757`, unknown fallthrough `:1722`, unknown-hold routing `:1855`. Spawn road: `harvestRunningNode:1211`, `spawnGroupOutcome:1471`, `spawnReplyPayload:1556`. Numbers as of `5b32433`; the Mechanism table's are as of `cc7f47d` |
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

- [x] **Re-derive which path actually minted the 2026-09-03 row** — **narrowed to one reachable path, not proved** (the history is gone, so proof is unobtainable): `git*commit*` glob-matches inside the word "un**commit**ted" and `git*push*` inside "un**push**ed", both of which appear in the commit agent's recorded decline
- [x] **Decide whether `parseExitSentinel` should outrank the console row** — **no**: rejected as the general mechanism, retained as tier 3 below attribution, which preserves today's property that a real failing row outranks a claimed success
- [ ] Decide whether the unguarded `muxcode log` writer (`cmd/log.go`) is in scope here or its own spec — **still open; not put to the user** — the evidence a decision needs is gathered under [Decision 4](#decision-4--is-the-muxcode-log-writer-in-scope)
- [x] Weigh options 1–4 against the Phase 1 findings
- [x] Choose a general mechanism and, if different, a cheap immediate mitigation — **option 4 general + option 3 immediate**
- [x] Confirm the choice satisfies the "genuine success still succeeds" criterion by construction — the three-tier table below
- [x] Record the decision and rationale in this spec

### Phase 2 decision — recorded 2026-09-14

**Made by the user**, relayed through edit, as this spec reserved it. Recorded by plan, which had
refused to certify this phase earlier the same day precisely because the choice was not an agent's
to make. Source: `/tmp/mux-148-phase2-decision.md` (non-durable; the load-bearing content is here).

| Question | Decision |
|---|---|
| General mechanism | **Option 4** — a node whose authoritative row cannot be tied to the dispatched work **holds** rather than routes |
| Cheap immediate mitigation | **Option 3** — a per-node positive token for nodes immediately upstream of a mutation, reusing the shipped `verify-pr` / `PR-CONFIRMED` pattern |
| An unattributable node | **Holds for a human**, consistent with the existing unverified hold (`graph_exec.go:1684`) and recoverable |

Options 1 and 2 were **not** chosen. Option 1 is subsumed by option 4 at higher cost — both need the
missing actor provenance, and correlation then adds a command-shape heuristic the spec itself flags as
liable to drift; option 4 needs its heuristic only to be *conservative*, not *right*. Option 2 was
rejected as the general mechanism because it fixes exactly the 5 roles instructed to emit a sentinel
and leaves 9 at status quo, inherits MUX-154's counterfeit exposure, and would invert today's property
that a real failing row outranks a claimed success.

#### New finding — the authoritative row carries no actor

**Verified by plan.** `ProcessBashHook` (`hook.go:744-814`) routes rows by **command type**, not by
role: `CmdBuild` → `build-history.jsonl`, `CmdTest` → `test-history.jsonl`, `CmdDeploy` →
`deploy-history.jsonl`, `CmdGit` → `commit-history.jsonl`, and `CmdUnknown` → `{role}-history.jsonl`
for `run`/`runner`/`watch` alone. But `latestAuthoritativeRow` reads `HistoryPath(session, role)` =
`{role}-history.jsonl` (`config.go:340-342`). **For the four typed roles, the file it reads is a
command-type channel written by every role, and `HookHistoryEntry` records no actor.** A `CmdGit` row
minted by any agent is read as the `commit` node's evidence.

> **Consequence: option 4 cannot be built on the existing row schema.** Attribution needs provenance
> that is not recorded today. Phase 3 inherits an `HookHistoryEntry` schema change as a prerequisite —
> and this makes option 1 strictly more expensive than the spec's "Medium" estimate, since correlation
> would need the same change *plus* the heuristic.

#### Re-derivation of the 2026-09-03 row — a hypothesis, labelled as one

**Verified by plan by reading:** `matchPatterns` (`hook.go:415-419`) is
`headAtBoundary(cmd, patternHead(pat)) && globMatch(pat+"*", cmd)`, and `globMatch` (`tools.go:220`)
is a standard wildcard match where `*` spans any characters. So `git*commit*` matches **any command
beginning with `git` containing the substring `commit` anywhere — including inside "uncommitted"**,
and `git*push*` inside "unpushed".

The commit agent's recorded decline was: *"node c's fix (…617 insertions) is **uncommitted** and
un**push**ed"*. ~~Both mutating keywords sit in the vocabulary of the refusal itself, so any `git …`
invocation run while reaching that conclusion classifies `CmdGit`~~ — **overstated; corrected
2026-09-14 by the Phase 2 worker (run `1789399519`, node `implement`) and verified by plan.**
`ClassifyCommand` (`hook.go:298-324`) matches `stripCommandPrefix(command)` — the **command string**,
never the tool description and never the reply — and `headAtBoundary` (`hook.go:446-457`) requires it
to *begin* with `git` plus a separator. The refusal's prose therefore never reaches the classifier.
`stripCommandPrefix` (`hook.go:361`) removes only a leading `cd … &&` and env assignments, so the glob
does span the whole remaining command. The reachable path is **narrower than first recorded: a command
that starts with `git` and itself contains `commit` or `push` anywhere** — in a flag
(`git push --dry-run`, `git commit --dry-run`), a refspec (`git log @{push}..`), a trailing statement
or comment (`git status && echo "uncommitted"`). Such a command exits 0, classifies `CmdGit`, and
writes an exit-0 row into `commit-history.jsonl` — exactly the file node `d`'s outcome derivation
reads. A bare `git status` or `git log -1` cannot.

This is consistent with Phase 1's disproof (bare `gh pr view` / `git status` mint nothing) and supplies
the missing path. **It remains a hypothesis: the original history is gone and cannot be re-read.** The
`muxcode log` path stays equally reachable and equally unprovable for this incident. **Phase 3 should
unit-test the glob behaviour** — the whole re-derivation rests on it and it has never been pinned.

#### "Genuine success still succeeds" — the by-construction argument

Required by acceptance criterion 2. A genuine success has a path at every tier; a decline has one at
none:

| Tier | Signal | Genuine success | Decline |
|---|---|---|---|
| 1 | Positive token (option 3, per-node) | The agent that did the work emits it | Not emitted — nothing to fake by accident |
| 2 | **Attributable** authoritative row (option 4) | Real work mints a row of the type the node's action produces | Read-only inspection mints no row, or one whose type does not match the action |
| 3 | `EXIT=<n>` sentinel | `EXIT=0` | `EXIT=1` → failure/hold |
| — | none of the above | — | **Hold** |

**The load-bearing constraint:** attribution must be **command-type-to-action**, not actor-plus-timing.
Actor and timing alone would still admit the 2026-09-03 row — it was minted by the `commit` role after
dispatch. It is the mismatch between *"a git command ran"* and *"reply to the PR comments"* that must
fail the test.

This is also why **both** options were needed. Actions with no command that could evidence them
(`comment`, `update-docs`, `pr-read`) can never produce an attributable row, so option 4 alone would
convert them into permanent holds — and those are precisely the nodes option 3's token covers. The two
are complementary by construction, not merely additive.

#### Constraints Phase 3 inherits

- [ ] **Ship the first test of `deriveSendOutcome`** — nothing calls it today, so the precedence ordering is free to be fixed *and* free to regress unnoticed
- [ ] **Add actor provenance to `HookHistoryEntry`** — a prerequisite for option 4, not part of it
- [ ] **Unit-test the `git*commit*` glob in both directions** — it fires on a `git`-headed command containing `commit`/`push` anywhere (inside "uncommitted", in `--dry-run`, in `@{push}`), and does **not** fire on a keyword-free `git status`/`git log`, nor on a non-`git`-headed command however worded (reply prose is never an input) — the re-derivation rests on it
- [ ] Do **not** double-hold: the new hold and the `OutcomeUnknown` hold (`:1684`) must not both fire on one node
- [ ] `hook_codex_test.go` is **not** a constraint (re-verified this run) — it calls `latestAuthoritativeRow` directly and stays green under any precedence change

### Phase 3: Implement outcome attribution

Covers **both roads** — send and spawn — since 2026-09-14 (see
[Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all)).

- [ ] Implement the chosen mechanism with unit tests
- [ ] **Negative control test:** a node that genuinely succeeded still routes as success
- [ ] Emit a lifecycle event when a node's outcome cannot be positively established
- [ ] Ensure the unknown-hold and the new path do not double-hold the same node — on the send road and the spawn road alike
- [ ] Apply the Phase 2 mechanism (option 3 per-node positive token + option 4 unattributable → hold) to the **spawn** road, not the send road alone
- [ ] `spawnGroupOutcome` establishes an answered worker's outcome **positively**; absent a positive signal the node resolves `OutcomeUnknown` (hold), never `success`
- [ ] Preserve the existing failure semantics for missing, stopped and unknown-status workers — already correct, must not regress into holds
- [ ] **Spawn negative control test:** a worker that genuinely completes still routes as success — including a worker whose legitimate output is *no code change* (an investigation or decision phase, as Phases 1–2 of this spec were)
- [ ] **Record the spawn-signal decision** — seeded-and-enforced sentinel, mechanical work-product signal, or explicitly accepted risk — under the design tension below, before the spawn change is treated as done

#### Design tension — the spawn signal, and the sentinel agents omit

Recorded 2026-09-14 from edit's handoff, deliberately **not resolved here**.

The obvious mechanism is to reuse `parseExitSentinel` on the worker's reply payload: a positive
token, not prose parsing, and the send road's own vocabulary. The known risk is that **agents omit
mandated sentinels**. `agents/test-runner.md:20` already carries the `EXIT=` requirement in strong
terms — *"End every reply with the test run's literal exit code on its own line"* — and edit reports
the test agent omitted it anyway earlier on 2026-09-14, parking nodes on unverified holds. If the
spawn road requires a sentinel that workers do not reliably emit, every spawn node holds and the
negative control fails.

| Choice | What it buys | What it costs |
|---|---|---|
| Seeded-and-enforced sentinel | Positive, prose-free, one vocabulary for both roads | Holds whenever a worker forgets; needs the seed to instruct it and something to make omission loud |
| Mechanical work-product signal (worktree diff, port summary) | Cannot be forgotten or forged by prose | **Holds every correct investigation or decision worker** — Phases 1–2 of this spec produced zero code changes legitimately, and that signature is also MUX-178's |
| Explicitly accepted risk | Ships now | Must be written down as a risk, with the hold rate watched |

**`5b32433` takes the first road** (reply sentinel; none → `OutcomeUnknown`), with unit tests but with
the choice made in code rather than on the record — see Defect 3. The step above exists so that the
choice is made on the record rather than inherited from whatever landed first; recording it may well
confirm `5b32433`, but the omission risk, and how an omitted sentinel is made loud, must be written
down.

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

### Decision 4 — is the `muxcode log` writer in scope?

Phase 2's one open item. **Not decided** — reserved for the user, who has not yet been asked. The
evidence below was gathered by the Phase 2 worker (run `1789399519`, node `implement`, 2026-09-14)
and verified by plan against the tree, so the question can be put with its facts attached.

| Fact | Where | Why it matters |
|---|---|---|
| Role is a free argument | `cmd/log.go:45` (`role := args[0]`) | Any agent names the role whose file it writes |
| Writes `HistoryPath(session, role)` | `cmd/log.go:134` | The exact file `latestAuthoritativeRow` reads (`graph_exec.go:1730`) |
| Exit code is caller-supplied | `--exit-code N`, `cmd/log.go:131` | The verdict is chosen, not observed |
| **Command text is caller-supplied** | `--command CMD`, `cmd/log.go:16` | The writer authors the very field command-type attribution would read |
| Entry has no `source` | `cmd/log.go:136-143` | `latestAuthoritativeRow` skips only `SourceBusResponse`; an empty source reads as authoritative |
| `Bash(muxcode *)` is in the shared bus group | `profile.go:578` | Every role holds the permission |
| No guard coverage | `bus/guard.go` (no `log` clause) | Nothing at the hook layer refuses it |
| No default exit code | `cmd/log.go:48` | A row without `--exit-code` is `unknown` and skipped — forging takes one deliberate flag |

**Why it is materially different from the glob path.** The glob path needs an accident: a
`git`-headed command that happens to contain a mutating keyword. `muxcode log` needs one deliberate
command that names the target role, picks its exit code and writes its own command text. Any agent
can mint any node's evidence.

**Bearing on the Phase 2 decision.** Option 4 as decided — attribution by command type to action —
reads fields this writer lets the caller author, so **option 4 alone does not close this hole**: a
forged row can carry a matching role and a matching command type. That is why the scope call has to be
made before Phase 3 designs the `HookHistoryEntry` schema change. If `cmd/log.go` is in scope, the new
actor provenance must be something the writer cannot set (process ancestry, as `BusActorVerified`
resolves it) rather than one more caller-supplied field; if it is its own spec, Phase 3 records the
residual and this spec's acceptance is scoped to the hook road.

## Out of scope

- **Whether agents should decline at all.** The decline here was correct; this spec is about the
  executor believing the wrong thing afterwards.
- **The `spec-complete` guard.** It behaved correctly and is not implicated — it simply is not a
  substitute for upstream node attribution.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-148-node-outcome-reads-command-ran-as-task-done | 1h 44m | 2026-09-14 11:31 |

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

**Phase 2 decided by the user, 6/7 — one item deliberately open.** Graph run
`1789393404-spec-to-pr-ada1cfa2` first looped into Phase 2 at 10:03 and asked plan to verify it ahead
of a commit gate while it stood at 0/7, every item a decision; plan replied `EXIT=1` and checked
nothing off, because an agent cannot make these choices and then certify its own choice as the phase's
completion. **The user then made the decision and relayed it through edit**, and it is recorded above.

**`cmd/log.go` scope remains open on the user's explicit instruction** — it was not put to them this
run, so Phase 2 is 6/7 and **not complete**. Phase 3 must not treat it as settled.

**Phase 2 re-verified 2026-09-14 11:31 by graph run `1789399519-spec-to-pr-5aa52382`** (`implement`
worker report at `/tmp/mux-148-phase2-verification.md`, non-durable). All six recorded claims hold
against the tree. Two things were recorded from it, each verified by plan before recording: the
re-derivation's "vocabulary of the refusal" sentence was **overstated and is struck inline** (the
classifier never sees the reply; the path is a `git`-headed command containing the keyword), and the
evidence for the open scope call now sits under
[Decision 4](#decision-4--is-the-muxcode-log-writer-in-scope). **Nothing was checked off** — the one
open item is the user's, the worker changed no code, and the only file in the working tree was this
spec. Phase 2 stayed 6/7 (spec 12/42 at that point).

**Correction, minutes later — `phase-check` did not route to `stuck-gate`.** It passed, and
`phase-gate` is waiting on the user. `phaseCommitReady` (`graph_exec.go:511`) asks whether the spec's
*completed-phase count* is at least *this run's* commit-edge fires plus one: Phase 1 counts as complete
(6/6) and run `1789399519` has committed nothing, so `1 >= 0+1` holds — even though Phase 1 was already
committed by run `1789393404` in `7e03dc9`. The count is per-repo, the fires are per-run, so a fresh
run re-credits every phase an earlier run shipped and the gate asks a human to approve a commit while
the open phase is 6/7. Flagged to edit; whether it becomes a backlog item is the user's call.

**Phase 3 widened to the spawn road, 2026-09-14 11:40, on the user's instruction** relayed by edit
(`/tmp/mux-148-phase3-spawn-road.md`, non-durable; edit's findings re-verified by plan against
`846251e` and `graph status`). Recorded: [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all)
with the reference reproduction on this spec's own run, four spawn acceptance criteria, five Phase 3
steps, and the [design tension](#design-tension--the-spawn-signal-and-the-sentinel-agents-omit) Phase 3
must resolve on the record. Total 42 → 51, still 12 checked. **While this was being written the
spawn-road change was committed as `5b32433`** (11:39, `phase-gate` approved by the user; the commit
also carried plan's Phase 2 re-verification edits, which were complete, and none of the widening edits,
which were not). It takes the sentinel road with unit tests but no recorded choice, and it has not been
through the chain — build, test and review on this run finished before the change existed. Nothing in
Phase 3 is checked off; the next lap's `verify-spec` is where `5b32433` is verified against the spawn
steps, if the chain runs green on it.

Phase 1 **disproved this spec's own account of the mechanism** (read-only `gh`/`git` mint no row for
the `commit` role) and **withdrew a constraint plan had asserted** (`hook_codex_test.go` pins the
helper, not the precedence). Both corrections are recorded inline above rather than silently edited
away. **The blocking question for Phase 2 is now: which path actually minted the 2026-09-03 row?**

This spec moved to `docs/requirements/drafts/` on 2026-09-14 (`7e03dc9`), so `muxcode spec set` no
longer warns and the automatic `verify-spec` pass fires for it. The active-spec pointer lives in the
bus directory and is purged on every session relaunch — it was re-set on 2026-09-14 11:25 after the
daemon relaunch cleared it; a restart with no pointer means review completes with no verification
dispatch, which reads as a stalled run rather than an unset pointer.
