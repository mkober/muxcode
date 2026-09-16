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
| **Spawn road — no derivation at all** (at `846251e`) | `spawnGroupOutcome` (`:1455`), called from `harvestRunningNode` (`:1205`) | `outcome` starts at `success`; an answered seed is a bare `continue`; it degrades only for a missing, stopped or unknown-status worker. **Content-blind** — none of the send-road machinery above is consulted. See [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all) |
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

And the **mirror**: any node whose agent's first *recognised* command fails and whose correct re-run is
*not recognised* — the node fails on the stale row, and a `fix` loop sets about repairing a suite that
is not broken ([Defect 4](#defect-4--the-mirror-a-genuine-success-recorded-as-failure)).

In this run it advanced to `close-gate` on the false signal. **Only two accidents prevented a spec
close-out on it**: the `spec-complete` guard, and the session happening to have no active spec.
Neither is a guarantee — the guard checks the spec's own completeness, not whether the upstream
node did its job.

### Defect 2 — the template puts node `d` in an impossible position

`commit-pr-review-loop` has **no commit node between `c` and `d`** (verified,
`bus/graph_templates.go:76-77` at `cc7f47d`; `:85-86` in the working tree of 2026-09-16, after a
two-node read-only `pr-precheck`/`pr-exists` was inserted ahead of `gate1` to skip the commit+PR
nodes when a PR already exists — `c`→`d` unchanged):

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

**A fix was written while this was being recorded.** Edit changed `spawnGroupOutcome` to read
`parseExitSentinel(spawnReplyPayload(…))`: a reply with no sentinel resolves `OutcomeUnknown` (held),
failure outranks unknown across a group, and `harvestRunningNode` ports on `!= failure` so a held
node's work still lands in the tree for the human to judge. It ships
`TestSpawnGroupOutcomeReadsTheReplyNotTheFactOfReplying` (`graph_exec_test.go:2292`): declined →
unknown, `EXIT=0` → success (the negative control), `EXIT=1` → failure, a sentinel-free success claim →
unknown, plus regression guards — missing → failure, stopped → failure, running → not done, and a
mixed declined+failed group fails rather than holds. **Commit history, for the record:** it was
committed as `5b32433` at 11:39 through run `1789399519`'s `phase-gate` (approved by the user) together
with plan's Phase 2 edits; at 11:42 that commit was **reset** (`reset: moving to HEAD~1`) and the spec
alone re-committed as `f942c43`, so the code and test went back to the working tree, uncommitted, to go
through the lap's build→test→review first — the run's earlier build, test and review nodes had
completed at 11:28–11:31, before the change existed. **Then removed (≈11:53):** lap 2's `test` node
failed, the run was canceled, and the change and its test were taken out of the working tree — as of
11:55 nothing in the tree implements the spawn road, and the description above is of a reverted
change. It took one side of the
[design tension](#design-tension--the-spawn-signal-and-the-sentinel-agents-omit) below without the
choice being recorded, which is moot until something is reinstated.

### Defect 4 — the mirror: a genuine success recorded as failure

**Added 2026-09-14 on the user's instruction.** Found and verified by edit against `f942c43` on
session `is-operations-gateway`, run `1789400058-spec-to-pr-c627b2f9` (PBP1-5009, Phase 1); the
classifier mechanism re-verified by plan here against `DefaultTestPatterns` and `matchPatterns`. The
spec to this point was written around false *success*; the same root cause produces a false
*failure*, and no earlier criterion covered it.

**Observed.** The `test` node shows `failed`. The suite actually passed — 21/21 suites, 468/468
tests, exit 0. The `fix` spawn then looped three times repairing a suite that was not broken, and
`loop-check`/`stuck-gate` reached ×8.

**Mechanism.** Two commands ran in the node's window:

| Command | Classified | Row written |
|---|---|---|
| `pnpm test -- --runInBand` | yes — `pnpm*test` | authoritative **failure** (exit 1: Jest read `--runInBand` as a test-name pattern, 0 matches) |
| `pnpm exec jest --runInBand` | **no** | none — and this is the run that passed |

`pnpm exec jest --runInBand` matches nothing in `DefaultTestPatterns` (`./test.sh`, `jest`,
`pnpm*test`, `pytest`, `go*test`, `cargo*test`, `vitest` — `hook.go:258`): `jest` needs
`headAtBoundary`, and the command starts with `pnpm`; `pnpm*test` clears the head but the glob then
needs the substring `test`, and `jest` is not it; the wrapper escape covers `bash`, `sh` and `npx`
only, not `pnpm exec`. So the passing run wrote no row, `latestAuthoritativeRow` returned the earlier
failure, and tier 2 outranks tier 3 — the reply said *"Tests passed … exit code 0"* as prose with no
`EXIT=` sentinel, and would have lost to the row even with one.

**Mitigation for this instance landed 12:13 — `f317a4f`.** `matchPatterns` now recognises
`pnpm exec ` and `pnpm dlx ` as runner prefixes (`runnerPrefixes`) and re-matches the nested executable
at its own boundary (`nestedRunnerCommand`), so `pnpm exec jest --runInBand` classifies as a test
command and writes its row; bare `pnpm` is deliberately excluded, because `pnpm add jest` and
`pnpm exec eslint jest.config.js` would otherwise read as test runs and a successful install could
overwrite a real suite failure. Vet clean, 2418 passed, reviewed. **It closes the instance, not the
class**: any runner the patterns do not name still writes no row, and the earlier row still wins — the
criterion and the inherited constraint stand.

**Why it belongs here and not in its own spec.** Identical root cause, in this spec's own words: the
row describes **a command**, not **the task**. A fix that only stops false successes leaves this live.

**What it constrains.** "Newest authoritative row wins" is wrong in *both* directions when the command
that carried the real verdict was never classified. Tiering or attribution alone does not fix it; the
node needs a signal tied to the **dispatched task**, not to whichever commands happened to be
recognised — recorded as an inherited Phase 3 constraint.

## Requirements

### Acceptance criteria

- [x] A node whose agent **declines** the task is not recorded as `success` — **spawn road met 15:40**; ~~**send road conditional**: met only when the declining agent's reply carries `EXIT=<non-zero>` (the conflict rule then holds); a decline with no sentinel beside an unrelated success row still records `success`. Closes with option 4 on the send road~~ **send road met 2026-09-15** (plan, working tree): option 4 landed as `rowAttributesTo` (`graph_exec.go:1875`) — a row testifies only for an action it could evidence, an untied row is not evidence, the node falls to the agent's token and otherwise holds; the 2026-09-03 shape (a tokenless decline beside an exit-0 `git commit` row on a `comment` node) is the first case of `TestDeriveSendOutcomeAttributesRowToAction` → `unknown`. **Residual, by design and pinned** (`an unmapped action still routes on its row`): an action in no attribution table — `run`, `watch`, `serve`, `api` — keeps row-decides, because its role mints a row for *any* command and is not instructed to emit a token, so refusing the row would hold every such node and fail the negative control below
- [ ] **The mirror:** a node whose work genuinely succeeded after a failed first attempt is not recorded as `failure` — see [Defect 4](#defect-4--the-mirror-a-genuine-success-recorded-as-failure) — **conditional after 15:40**: with the agent's `EXIT=0` beside the stale failure row the node now **holds** (`graph-outcome-conflict`) instead of failing — the incident's three fix laps and eight stuck-gates become one approval; with the sentinel omitted the row alone still records `failure`. `TestExecSendOutcomeHoldsOnContradiction` pins the first case — **unchanged by attribution (2026-09-15)**: the stale failure row is a test command, so it *does* testify for a `test` action, and only the agent's token can say the unrecognised re-run passed. Two ways to close, the user's call (put to them 2026-09-15): accept the residual — the test role is instructed to emit the token, omission is that agent's defect, and the conflict rule holds the moment it is emitted — or hold a failure row that has no corroborating token, which trades the mirror for a hold on every tokenless genuine failure
- [x] A node that **genuinely succeeds** is still recorded as `success` — **negative control: a fix that holds everything is not a fix** — met 2026-09-14 15:40 on both roads (tests above; live: run `1789413170`'s five completed nodes under the new daemon)
- [x] The distinction does **not** rely on parsing prose — met 15:40: the only inputs are `parseExitSentinel`'s token and the observed row; `success claimed in prose alone` → unknown is pinned on the spawn road
- [x] A node that cannot be tied to its dispatched work surfaces as a hold or failure, never as a silent success — **spawn road met 15:40** (no token → hold); ~~**send road open**: a row alone is still taken as tied, whatever command produced it (the open constraint under Phase 3)~~ **send road met 2026-09-15**: an untied row is not evidence, and with no token the node holds while `graph-outcome-untied` (warn, `graph_exec.go:1815`) names the row's outcome and command and the action it cannot testify for. Same unmapped-action residual as the first criterion
- [x] Whatever signal is chosen degrades safely for non-hook providers, which infer outcomes and cannot be assumed to emit it — met 15:40: with no observed row the conflict rule never engages, a self-report stays evidence of last resort, and an omitted token degrades to a **loud hold** (`graph-outcome-unattributed`), never a silent verdict either way
- [ ] `commit-pr-review-loop` can complete a run in which `c` made changes
- [x] A lifecycle event records any node whose outcome could not be positively established — **partial 15:40**: `graph-unverified-hold` (existing), `graph-outcome-unattributed` (tokenless spawn reply) and `graph-outcome-conflict` (disagreeing send signals) cover every case the code *recognises*; a send node whose only evidence is an untied row is not recognised as unestablished, so it records success and no event — the same gap as criterion 5 — **closed 2026-09-15**: the untied-row case is now recognised and emits `graph-outcome-untied` (`:1815`) ahead of the hold, so four events cover every case the code can reach. Verified by reading: `TestDeriveSendOutcomeAttributesRowToAction` asserts the outcome, not the event — a pin belongs in Phase 5's declining-node test
- [x] A **spawn** node whose worker **declines** is not recorded as `success` — the [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all) reproduction no longer reproduces — met 15:40: the `declined` case is the incident's own reply text ("Phase 2 is a decision phase … No code change, no spec edit") → unknown
- [x] A **spawn** node whose worker genuinely completes the work **is** still recorded as `success` — **negative control: a fix that holds every spawn node is not a fix** — met 15:40 (tests, and this run's `implement` and `fix` nodes live)
- [x] A **spawn** node whose outcome cannot be positively established emits the lifecycle event and holds — met 15:40 by reading (`graph-outcome-unattributed` + the single unknown branch); executor-level test still owed (Phase 3's last step)
- [x] The spawn-road distinction does **not** rely on parsing worker prose — met 15:40: token or nothing; prose success → unknown, pinned
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
| `tools/muxcode/bus/graph_exec.go` | Send road: `deriveSendOutcome:1678`, `parseExitSentinel:1712`, `latestAuthoritativeRow:1730` (`:1740` as of `a8fa0db` — hook > self-reported, all else not evidence); unknown-hold routing in `routeFinishedNodes:1814` via `unverifiedHoldReleased:927` (`graph-unverified-hold`). Spawn road: `harvestRunningNode:1205`, `spawnGroupOutcome:1455`, `spawnReplyPayload:1529`. Numbers as of `f942c43`; the Mechanism table's are as of `cc7f47d`. **Working tree 2026-09-15 (attribution, uncommitted):** `deriveSendOutcome:1787`, `actionEvidenceTypes:1827`, `actionEvidenceCommands:1840`, `actionsWithoutCommandEvidence:1855`, `rowAttributesTo:1875`, `parseExitSentinel:1905`, `latestAuthoritativeRow:1933` → `latestAuthoritativeRowFunc:1941`; `harvestRunningNode:1222`, `spawnGroupOutcome:1493`, `spawnReplyPayload:1621`, `routeFinishedNodes:2038`, `unverifiedHoldReleased:944`; events `graph-unverified-hold:977`, `graph-outcome-unattributed:1285`, `graph-outcome-conflict:1803`, `graph-outcome-untied:1815` |
| `tools/muxcode/bus/graph_templates.go` | `commit-pr-review-loop:72-100` — the `c`→`d` gap and the `verify-pr` token precedent. Working tree 2026-09-16: `:72-107` — `pr-precheck`/`pr-exists` `:77-78` (read-only skip of the commit+PR nodes when a PR exists), `verify-pr` token `:81-82`, `c`/`d` `:85-86`, `c`→`d` edge `:102` |
| `tools/muxcode/bus/commit_authority.go` | `checkGraphCommitDispatch:166`, `dispatchMatchesNode:208` — what a commit node inserted in Phase 4 must satisfy |
| `tools/muxcode/bus/hook.go` | `DefaultGitPatterns:288-292` (mutating git/gh only), `ClassifyCommand`, `ProcessBashHook` `case CmdUnknown:803-814` — where authoritative rows are minted; `WriteHookHistory:626` — since `a8fa0db` the one road every history row travels, stamping `SourceHook` on an undeclared entry. Working tree 2026-09-15: `DefaultGitPatterns:294` gains `git*checkout`, `git*switch`, `gh*pr*checkout` (still mutating-only; `TestGitPatternsClassifyCheckout`) so a checkout node has a row to be attributed by — its only consumer is `ProcessBashHook`'s `case CmdGit` (`:832`), so the widening mints commit-history rows and nothing else |
| `tools/muxcode/cmd/log.go` | `runLog:38`, `:135-146` — since `a8fa0db` a **self-reported** writer through `WriteHookHistory` (`Source: SourceSelfReported`, exit code still caller-chosen); before it, `:136-174` hand-rolled the row with no source at all |
| `tools/muxcode/bus/prompt.go` | `:133`, `:176-213` — the instructions that tell non-hook providers to self-log `--exit-code 0` |
| `tools/muxcode/bus/console.go` | `ConsoleEntry`, how outcome rows are read back |
| `tools/muxcode/bus/history_provenance.go` | `SourceBusResponse:33`, `SourceHook:38`, `SourceSelfReported:49` — the three-valued provenance vocabulary as of `a8fa0db`; `NewBusResponseEntry:297` — the single constructor for synthesized rows |
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

### Findings recorded from Phase 1 — 2026-09-14

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
- [x] Decide whether the unguarded `muxcode log` writer (`cmd/log.go`) is in scope here or its own spec — **in scope; decided by the user 2026-09-14 14:22**, relayed through edit. Two of the three changes "in scope" was defined to mean, plus *source* provenance in place of the process-derived kind, shipped as `a8fa0db` (14:23); what landed and the residual are recorded under [Decision 4](#decision-4--is-the-muxcode-log-writer-in-scope)
- [x] Weigh options 1–4 against the Phase 1 findings
- [x] Choose a general mechanism and, if different, a cheap immediate mitigation — **option 4 general + option 3 immediate**
- [x] Confirm the choice satisfies the "genuine success still succeeds" criterion by construction — the three-tier table below
- [x] Record the decision and rationale in this spec

### Decision recorded for Phase 2 — 2026-09-14

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

The constraints Phase 3 inherits from this decision are listed **under Phase 3** (moved 2026-09-14
12:10: `SpecPhases` drops its current phase on any heading line and re-arms only on `### Phase N`, so
under a `####` label here they were attached to no phase at all — invisible to the count —
[MUX-183](../backlog/MUX-183-phase-commit-ready-recredits-shipped-phases.md) defect 2).

### Phase 3: Implement outcome attribution

Covers **both roads** — send and spawn — since 2026-09-14 (see
[Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all)).

**Constraints Phase 3 inherits** (a bold label, not a heading — the spec parser drops its current phase
on any heading line, so a `####` here would detach the boxes below from Phase 3):

- [x] **Ship the first test of `deriveSendOutcome`** — nothing calls it today, so the precedence ordering is free to be fixed *and* free to regress unnoticed — **shipped 2026-09-14 (run `1789413170-spec-to-pr-828f8c3a`, verified by plan against the working tree 15:40):** `TestDeriveSendOutcomeSignals` (nine cases at the helper) and `TestExecSendOutcomeHoldsOnContradiction` (the outcome the executor actually records, with the uncontradicted row as its own negative control — it replaces `TestAuthoritativeRowOutranksSentinel`, whose premise the conflict rule inverts). Suite 2603 pass / 0 fail, hook-observed 15:35:31
- [ ] **Add actor provenance to `HookHistoryEntry`** — a prerequisite for option 4, not part of it — **still open after `a8fa0db`**: what landed is *source* provenance (`hook` / `self-reported` / `bus-response`), declared by the writer; actor provenance the writer cannot author (process ancestry via `BusActorVerified`) is the [Decision 4](#decision-4--is-the-muxcode-log-writer-in-scope) residual — **filed as [MUX-185](../backlog/MUX-185-history-row-provenance-declared-not-proven.md) on 2026-09-14** on the user's instruction; this step stays open here as the pointer and closes when that spec ships. MUX-185 records that the ancestry stamp alone would not close it: `agentRuntimeAncestor` resolves a `muxcode hook bash` process and a forging agent shell to the same runtime, so it separates a person from an agent, not a hook from the agent's shell
- [x] **Unit-test the `git*commit*` glob in both directions** — it fires on a `git`-headed command containing `commit`/`push` anywhere (inside "uncommitted", in `--dry-run`, in `@{push}`), and does **not** fire on a keyword-free `git status`/`git log`, nor on a non-`git`-headed command however worded (reply prose is never an input) — the re-derivation rests on it — **pinned 2026-09-14:** `TestGitPatternsFireOnTheKeywordNotTheProse` (`bus/hook_test.go`) fires on `git commit --dry-run`, `git diff --stat -- docs/uncommitted-notes.md`, `git log @{push}..HEAD`; stays quiet on `git status`, `git log --oneline -5`, `git diff --stat`, `echo 'pre-rebase cleanup done'`, `cat notes-on-commit-hooks.md`. Both directions, verified by plan in the diff
- [x] Do **not** double-hold: the new hold and the `OutcomeUnknown` hold (`:1684`) must not both fire on one node — **verified by plan by reading, 2026-09-14:** the conflict path *returns* `OutcomeUnknown` from `deriveSendOutcome` and adds no hold of its own; the one unknown branch in `routeFinishedNodes` (`:1957-1958` in the working tree) calls `unverifiedHoldReleased` once, marker-guarded. The spawn road resolves through the same branch
- [x] `hook_codex_test.go` is **not** a constraint (re-verified this run) — it calls `latestAuthoritativeRow` directly and stays green under any precedence change — confirmed 2026-09-14: untouched by the Phase 3 change and green in the 15:35:31 run
- [x] **The signal must be tied to the dispatched task, not to whichever commands happened to be recognised** — "newest authoritative row wins" is wrong in *both* directions when the command that carried the real verdict was never classified ([Defect 4](#defect-4--the-mirror-a-genuine-success-recorded-as-failure)); tiering or attribution alone does not close the mirror — **open after the Phase 3 change (plan, 2026-09-14 15:40).** The worker's report claims the new conflict rule *is* this constraint; it is not. `deriveSendOutcome` now holds when an observed row and the agent's sentinel **disagree** (`graph-outcome-conflict`), which makes disagreement loud — but it ties nothing to the dispatched task: a row **alone** still decides in both directions exactly as before, so a declining agent that emits no sentinel still reads success from an unrelated `git` row, and a mirror-shaped node whose agent omits `EXIT=0` still reads failure. The constraint closes when the row is checked against the node's action (Phase 2's option 4), not when the sentinel is checked against the row — **closed 2026-09-15 on that condition** (plan, working tree): the row is checked against the node's action by `rowAttributesTo` (`graph_exec.go:1875`), applied per candidate inside the source-ranking walk by `latestAuthoritativeRowFunc` (`:1941`). `TestDeriveSendOutcomeAttributesRowToAction` pins the 2026-09-03 shape → `unknown` with the agent's token and an unmapped action as its negative controls; `TestLatestAuthoritativeRowFuncMixedRows` pins that a newer unrelated success cannot bury an older matching failure and that rejecting an unrelated hook row leaves the matching self-report standing, with `…NilAcceptTakesAnyRow` proving the filter did the work. What attribution does **not** reach is the mirror's tokenless case — a matching-type failure row is tied to the task by construction, and only the token can say the unrecognised re-run passed — which stays with the second acceptance criterion, not here
- [x] **Provenance must fail closed on absence, not merely be unforgeable** — `latestAuthoritativeRow` (`graph_exec.go:1734`) is an *exclusion* list: it skips only `Source == SourceBusResponse` and unknown/empty outcomes, so a row carrying **no** provenance value reads as authoritative — deliberately, so pre-provenance rows keep their verdict (`history_provenance.go:26-31`). And `cmd/log.go` is the **one** history writer that bypasses `WriteHookHistory` (every other writer — `hook.go:785-840`, `cmd/send.go:525`, `daemon.go:3503` — goes through the typed `HookHistoryEntry`; `cmd/log.go:136-174` hand-rolls a map with its own append and rotate), so a field added to the struct reaches every writer except it: it bypasses by *omission*, not forgery. Whatever field Phase 3 adds, the reader must treat its absence as not-authoritative — a behaviour change for existing rows, which is why it sits inside the Decision 4 scope call. Found by run `1789402487`'s `implement` worker; verified by plan. **Implemented in `a8fa0db` (14:23), verified by plan against the commit:** `latestAuthoritativeRow` (`graph_exec.go:1740`) now accepts only `hook` (newest wins) and, failing that, `self-reported`; empty, `bus-response` and unrecognised sources are not evidence (`TestRawRowWithoutHookSourceIsNotEvidence`), and `cmd/log.go:146` writes through `WriteHookHistory`. Suite observed passing on the hook road at 14:16:00 (2425 pass) before the commit

**Steps**

- [x] Implement the chosen mechanism with unit tests — **partial, 2026-09-14 15:40 (plan).** The chosen mechanism is Phase 2's: option 4 (a row that cannot be tied to the dispatched action **holds**) as the general rule, option 3 (a per-node positive token upstream of a mutation) as the immediate mitigation. **Spawn road: implemented in substance** — the token is seeded and read, absence holds. **Send road: not the decided mechanism.** What landed is a *conflict* hold — `deriveSendOutcome` resolves `OutcomeUnknown` when the observed row and the agent's sentinel disagree — which is neither option 4 (no check of the row's command type against `n.Action`) nor option 3 (no per-node token; the `EXIT=` sentinel is option 2's signal, which the decision rejected as the general mechanism). A row alone still decides both ways, so the 2026-09-03 shape — a decline with no sentinel beside an unrelated `git` row — still routes success. **The type-to-action check is buildable now**, without waiting on [MUX-185](../backlog/MUX-185-history-row-provenance-declared-not-proven.md): the row carries `Command`, `ClassifyCommand` (`hook.go:298`) types it, and the node carries `Action` (`graph.go:42`); actor provenance only narrows *which role's* rows count, which is Phase 2's finding stated more precisely than its "cannot be built on the existing row schema" consequence. Two of five unit-test groups (the send-road ones) pin the conflict rule, not attribution — **complete 2026-09-15 (plan, working tree; code last written 2026-09-14 16:10–16:11, after `97b7288`; suite hook-observed green on it 12:08:52 and 12:09:44, 2998 pass / 0 fail / 2 skip; review 12:11 *0 must-fix, 2 should-fix, 1 nit*, the should-fixes answered by the 13:36 test edit, not yet re-run).** Send road: option 4 as `rowAttributesTo` — `actionEvidenceTypes` (`build`/`test`/`deploy` by `ClassifyCommand`, prechecks and applies included), `actionEvidenceCommands` (`commit` → `git*commit`; `checkout` → `git*checkout|switch`; `pr-checkout` → `gh*pr*checkout|…` — matched on the command because one `CmdGit` cannot tell a commit from a push), `actionsWithoutCommandEvidence` (review, update-docs, verify-spec, pr-read, pr-diff, pr-review, comment, story-read, jira-write, jira-read, issue-update, edit, spawn-task) — with option 3's token as the fall-through for that last group, which is the complementarity the Phase 2 argument predicted. An untied row is not demoted, it is not evidence: `deriveSendOutcome` takes the newest *attributable* row, then the token, then logs `graph-outcome-untied` and holds. Tests: `TestRowAttributesTo` (20 cases, both directions per action), `TestDeriveSendOutcomeAttributesRowToAction` (7), the mixed-row pair, fixtures re-anchored through `fixtureCommandFor`, and `TestExecJoinQuorumBarrier` now attributes one branch by row and one by token. Nit fixed: `worseOutcome(…, OutcomeFailure)` folded to `OutcomeFailure`. **Two residuals recorded here, not closed:** (1) unmapped actions keep row-decides (the first acceptance criterion); (2) the glob is loose — `git status | grep uncommitted` attributes to a commit node, pinned as `known-loose substring` in `TestRowAttributesTo`; tightening needs word-boundary matching across every pattern list, wider than this phase. **Consequence for Phase 4:** the send road seeds no token (only `spawnVerdictInstruction` does, `:738`) and `code-editor.md` carries no `EXIT=` line, so `commit-pr-review-loop`'s `c` (`edit:edit`, the one send node in the builtin templates whose action no row evidences *and* whose role is uninstructed) holds on every run until the template or the definition changes
- [x] **Negative control test:** a node that genuinely succeeded still routes as success — **2026-09-14:** send road — `TestDeriveSendOutcomeSignals` (agreeing signals, row alone, sentinel alone all route) and the pre-existing executor tests, green; spawn road — `answerSpawn` now answers `EXIT=0` and every pre-existing spawn executor test routes success on it. Live: this run's own `implement`, `fix`, `build`, `test` and `review` nodes all recorded `success` under a daemon carrying the change (`daemon.version`: `7bcd657-dirty` built 15:25:04 — the first-lap `build` node, run on the tree after the worker's port; the 15:33:58 rebuild differs only in test files)
- [x] Emit a lifecycle event when a node's outcome cannot be positively established — **met 15:40 (plan, from the working tree):** a spawn worker that answers without a token now reaches `OutcomeUnknown` → `graph-unverified-hold`, and `harvestRunningNode` additionally logs `graph-outcome-unattributed` (warn) naming the silent worker and appends `[no verdict token from <worker> — outcome not established]` to the node output; the send road logs `graph-outcome-conflict` on disagreement. Neither has fired live yet — both of this run's workers ended with `EXIT=0`. The untied-row case (a send node's row from unrelated commands) still emits nothing, because the code does not yet recognise it as unestablished — see the open constraint above. Earlier history: ~~verified 2026-09-14~~ **withdrawn 11:55**: the machinery below is real and stays, but with the spawn change reverted a declined spawn worker no longer reaches it, so the step is unmet on the road that matters. No new event is needed; both roads resolve "cannot be established" to `OutcomeUnknown`, and the unknown branch of `routeFinishedNodes` calls `unverifiedHoldReleased` (`:927`), which writes the pending marker, logs `graph-unverified-hold` (once — the marker guards repeats) and sends edit a `graph-approval` request. The spawn change is what makes a declined worker reach it. Deliberate exception: a node whose successors are all human gates skips the hold and the event (`successorsAllHumanGates`), because the gate is next anyway
- [x] Ensure the unknown-hold and the new path do not double-hold the same node — on the send road and the spawn road alike — **met 15:40:** both roads resolve to `OutcomeUnknown` and reach the single unknown branch of `routeFinishedNodes` (`:1957-1958`); no second hold call was added on either (verified by plan in the diff) — ~~spawn road satisfied by construction by the working-tree change (its new path *was* the existing hold)~~ **historical — reverted 11:53**; the by-construction argument holds for any reinstated change that resolves to `OutcomeUnknown` through the one hold branch in `routeFinishedNodes`, but nothing in the tree does; send road pending
- [x] Apply the Phase 2 mechanism (option 3 per-node positive token + option 4 unattributable → hold) to the **spawn** road, not the send road alone — **met 15:40:** the token is seeded into every worker task (`spawnVerdictInstruction`, appended by `graphWorkerTask` on both branches, placeholder `EXIT=<n>` so a TUI echo cannot counterfeit it — MUX-154), `spawnWorkerVerdict` reads it with `parseExitSentinel`, and absence resolves `OutcomeUnknown` → hold. The design-tension decision below names the reply sentinel as the token — ~~partial (11:50): the unattributable → hold rule was applied by the working-tree change~~ **historical — that change was reverted 11:53**; nothing in the tree applies either option on the spawn road now. Whether the reply sentinel is *the* token option 3 meant (the `PR-CONFIRMED` shape) is what the design tension below decides
- [x] `spawnGroupOutcome` establishes an answered worker's outcome **positively**; absent a positive signal the node resolves `OutcomeUnknown` (hold), never `success` — **met 15:40:** the answered branch folds `spawnWorkerVerdict` through `worseOutcome` (failure > unknown > success); `TestSpawnGroupOutcomeReadsTheReplyNotTheFactOfReplying` pins declined → unknown, `EXIT=0` → success, `EXIT=1` → failure, success-in-prose-alone → unknown. Coverage gap kept open as its own step below (review should-fix): the hold-and-port path is not exercised at the executor — ~~ticked 11:50 on the working-tree change~~ **withdrawn 11:55**: lap 2's test node failed and the change and its test were removed from the working tree (HEAD `f942c43`, no stash, `heldUnknown` absent) — nothing in the tree implements this step now; the reading stands only as a description of what the reverted change did (see Defect 3)
- [x] Preserve the existing failure semantics for missing, stopped and unknown-status workers — already correct, must not regress into holds — **met 15:40:** `TestSpawnGroupOutcomeKeepsFailureSemantics` — missing → failure, stopped → failure, running → not done; group precedence declined+failed → failure, succeeded+declined → unknown, all-succeeded → success; `unattributedWorkers` names the silent one — ~~verified 11:50~~ **withdrawn 11:55** with the reverted change; the regression guards it carried (missing → failure, stopped → failure, running → not done, mixed group → failure) are the shape to reinstate
- [x] **Spawn negative control test:** a worker that genuinely completes still routes as success — including a worker whose legitimate output is *no code change* (an investigation or decision phase, as Phases 1–2 of this spec were) — **met 15:40, with live evidence:** the `did the work` case (`EXIT=0` → success) at the function, every pre-existing spawn executor test on the `EXIT=0`-answering helper (none writes a file), and this run's own `fix` node — a worker whose worktree had *nothing to port* and whose reply ended `EXIT=0` — recorded `success` under the new daemon and dispatched `build` — ~~verified 11:50~~ **withdrawn 11:55** with the reverted change; its `EXIT=0` case was the right control, and nothing on the spawn road reads the diff, so the by-construction argument survives for whatever is reinstated
- [x] **Record the spawn-signal decision** — seeded-and-enforced sentinel, mechanical work-product signal, or explicitly accepted risk — under the design tension below, before the spawn change is treated as done — **recorded 15:40** from the worker's report (`/tmp/mux-148-phase3-report.md`, non-durable; the load-bearing content is under the design tension below): the seeded-and-enforced sentinel, with the omission risk written down and made loud
- [x] **Executor-level spawn hold test** (review should-fix, 15:37, `graph_exec_test.go:3370`): a spawn iteration with real worktree output and **no** `EXIT` token → the work ports uncommitted, the node's recorded outcome is `unknown`, the downstream send stays undispatched, `graph-outcome-unattributed` names the worker. The function-level tests stop at `spawnGroupOutcome`/`unattributedWorkers`; the hold-and-port path is verified only by reading. Review nit alongside: `worseOutcome(outcome, OutcomeFailure)` at `:1507,1520` is a constant fold — assign `OutcomeFailure` directly — **both shipped, verified 2026-09-15** (plan, working tree; `graph_port_test.go`, written 2026-09-14 15:55, green in the 12:08:52 / 12:09:44 runs): `TestExecSpawnHoldPortsWorkAndStopsThePipeline` — a worker with real worktree output and a tokenless reply: `feature.go` reaches the checkout, HEAD is unmoved, the node records `unknown`, its output names the silent worker, downstream `b` stays undispatched, and `graph-outcome-unattributed` names the worker; `TestExecSpawnHarvestLandsOutputBeforeBuild` is its negative control. The nit is folded (`outcome = OutcomeFailure` at `:1507`, `:1520`)

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

**The reverted change took the first road** (reply sentinel; none → `OutcomeUnknown`), with unit
tests but with the choice made in code rather than on the record — see Defect 3; as of 11:55 nothing
in the tree takes any road. The step above exists
so that the choice is made on the record rather than inherited from whatever landed first; recording
it may well confirm the change as written, but the omission risk, and how an omitted sentinel is made
loud, must be written down.

**Decided 2026-09-14 — the seeded-and-enforced sentinel.** Made by run `1789413170`'s `implement`
worker on the record (`/tmp/mux-148-phase3-report.md`), verified against the tree and recorded by
plan at 15:40; the user has not been asked and may overrule it. The other two roads were refused for
stated reasons: the **mechanical work-product signal** is disqualified by this spec's own history —
Phases 1–2 produced zero code changes legitimately, MUX-178 shares that signature, and a signal that
holds every correct investigation or decision worker fails "a fix that holds every spawn node is not a
fix" by construction; **explicitly accepted risk** ships nothing, and Defect 3 fires on `implement`
and `fix`, both spawn nodes in `spec-to-pr`. The omission risk is answered in two parts, and the
choice is defended as sound only with both: **(1)** the seed now asks for the token —
`spawnVerdictInstruction`, appended to every worker task by `graphWorkerTask` whether or not the
graph owns downstream roles (the seed this very node received carried no such instruction, so until
now the omission was guaranteed, not merely possible); **(2)** omission is loud — a tokenless reply
resolves `OutcomeUnknown`, which holds and raises `graph-unverified-hold` plus a `graph-approval`
request to edit, and the node additionally records `graph-outcome-unattributed` (warn) and appends
`[no verdict token from <worker> — outcome not established]` to its output, so the human reading the
hold is told which worker to ask. **Residual, accepted and to be watched:** the hold rate on spawn
nodes. If workers omit the token despite the seeded instruction, the symptom is a rise in
`graph-outcome-unattributed` rows — a distinct event name precisely so it can be counted.

### Phase 4: Fix the template gap

- [ ] Add a commit step between `c` and `d` in `commit-pr-review-loop`, or restructure so `d` has a citable sha — **and give `c` a verdict (2026-09-15):** with attribution live, `c` (`edit:edit`) has no command that could evidence it and edit emits no token (`code-editor.md` has no `EXIT=` line; send-road dispatches seed none), so it holds on every run — the restructure must seed the token in the dispatch as `spawnVerdictInstruction` does, add the instruction to the definition, or move the work to a spawn node
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
- [ ] **Test (mirror):** a node whose agent's recognised first attempt fails and whose unrecognised re-run passes is **not** recorded as `failure` — the [Defect 4](#defect-4--the-mirror-a-genuine-success-recorded-as-failure) shape
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

Phase 2's last open item. **Decided by the user 2026-09-14 14:22 — in scope**, relayed through
edit and recorded at the end of this section. The evidence below was gathered by the Phase 2 worker
(run `1789399519`, node `implement`, 2026-09-14) and verified by plan against the tree, so the
question was put with its facts attached; the table's rows 5, 9 and 10 describe the tree **before**
`a8fa0db` and are kept as the record of what the decision was made against.

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
| Bypasses `WriteHookHistory` | `cmd/log.go:136-174` — hand-rolled map, own `OpenFile`/`Flock`/`rotateHistory`; every other writer (`hook.go:785-840`, `cmd/send.go:525`, `daemon.go:3503`) uses the typed struct | A field added to `HookHistoryEntry` never reaches this writer's rows — bypass by omission |
| The reader fails **open** on absence | `latestAuthoritativeRow`, `graph_exec.go:1734` — an exclusion list; an empty `Source` is "the authoritative path" by design so legacy rows keep their verdict (`history_provenance.go:26-31`) | An absent provenance value is accepted; *unforgeable* is necessary, not sufficient |

**Why it is materially different from the glob path.** The glob path needs an accident: a
`git`-headed command that happens to contain a mutating keyword. `muxcode log` needs one deliberate
command that names the target role, picks its exit code and writes its own command text. Any agent
can mint any node's evidence.

**Bearing on the Phase 2 decision.** Option 4 as decided — attribution by command type to action —
reads fields this writer lets the caller author, so **option 4 alone does not close this hole**: a
forged row can carry a matching role and a matching command type. That is why the scope call has to be
made before Phase 3 designs the `HookHistoryEntry` schema change. If `cmd/log.go` is in scope, the new
actor provenance must be something the writer cannot set (process ancestry, as `BusActorVerified`
resolves it) rather than one more caller-supplied field — **and that is necessary, not sufficient**
(run `1789402487`'s worker, verified by plan): `cmd/log.go` bypasses the typed struct entirely, and the
reader accepts an absent value, so the field must also be *required at read time*. In scope therefore
means three changes — process-derived provenance on `HookHistoryEntry`; `cmd/log.go` routed through
`WriteHookHistory` so it cannot opt out by omission; and `latestAuthoritativeRow` failing closed on
absence, which touches rows written before the change. Own spec means Phase 3 records the residual,
this spec's acceptance is scoped to the hook road, and a one-command forgery path stays open with any
role able to mint any node's evidence. The facts are gathered either way; only the scope call is
missing.

**Decided 14:22 — in scope.** What shipped is `a8fa0db` (14:23:14, *"Rank hook-observed rows above
self-reports for verdicts"*), built by run `1789407209`'s last `implement` lap and committed through
edit → commit on the user's request after the run was canceled. Verified by plan against the commit:

| Change | Where | Effect |
|---|---|---|
| Three-valued `Source` | `history_provenance.go:33-49` — `SourceBusResponse`, `SourceHook`, `SourceSelfReported` | An observed row and a self-report are no longer peers, and an empty value is no longer "the authoritative path" |
| `WriteHookHistory` is the choke point | `hook.go:626` stamps `SourceHook` on an entry that arrives with no `Source` | The five `ProcessBashHook` sites (`hook.go:795-850`) read `hook` without touching each one; `cmd/send.go:555` and `daemon.go:3503` build through `NewBusResponseEntry` (`history_provenance.go:306`, `bus-response`); `cmd/log.go:141` sets `self-reported`. Every writer now declares itself |
| `cmd/log.go` routed through `WriteHookHistory` | `cmd/log.go:135-146` — the hand-rolled map and its own `OpenFile`/`Flock`/`rotateHistory` are gone (`rotateHistory` has no remaining reference) | Bypass by omission closed: a field on the struct reaches this writer too |
| Reader fails closed | `latestAuthoritativeRow` (`graph_exec.go:1740`): the newest `hook` row wins outright; the newest `self-reported` row is returned only when no hook row exists at or after `since`; **any other source — `bus-response`, empty/legacy, unrecognised — is not evidence** | A `muxcode log --exit-code 0` can no longer overwrite an observed failure by being newer; pre-provenance rows lose their verdict — the behaviour change the constraint predicted |
| Tests | `graph_exec_test.go`: `TestObservedRowOutranksSelfReport`, `TestSelfReportUsedWhenNothingObserved`, `TestRawRowWithoutHookSourceIsNotEvidence`, `TestWriteHookHistoryStampsSource`; `cmd/log_test.go` rewritten to the typed struct | Suite observed passing **on the hook road** before the commit: `go test ./...` rows at 14:15:47 and 14:16:00 carry `source:"hook"`, exit 0, `PASS=2425 FAIL=0 SKIP=2` |

**Why a self-report stays usable at all.** The non-hook providers have no other way to record that
work ran (acceptance criterion 6), and holding every one of their nodes is not a fix. So a
self-report is evidence *of last resort*, never an override.

**Residual — recorded, not closed.** Provenance is now *declared*, not *proven*: an agent that appends
a row to the JSONL by hand can write `"source":"hook"`, and the reader will take it.
`TestRawRowWithoutHookSourceIsNotEvidence` pins the *sourceless* case, not the *forged-label* case.
Closing it needs provenance the writer cannot author — `BusActorVerified`'s process-ancestry
resolution stamped at write time. That is the "process-derived provenance on `HookHistoryEntry`"
this decision named as the first of its three changes, and it is the one `a8fa0db` does **not**
ship; Phase 3's "add actor provenance" step therefore stays open. What landed is *source*
provenance.

**Filed as [MUX-185](../backlog/MUX-185-history-row-provenance-declared-not-proven.md), 2026-09-14
15:45**, on the user's instruction after edit restated the residual at 15:25 ("its own design, not an
increment on this phase"). With one correction, verified against `7bcd657`: the process-ancestry stamp
named above is necessary for audit and is **not** the closer — `agentRuntimeAncestor`
(`config.go:185-212`) resolves the hook process and the forging shell to the same agent runtime, so it
tells a person from an agent, not a hook from the agent's own hand. What can close the one-command
shape is a guard on the append, a transcript witness (the Codex road already reads one by
`tool_use_id`), or a daemon-held secret; MUX-185 evaluates the three under a written threat model.

~~**Observed while verifying, unexplained.** One second after the two `hook`-stamped test rows, a row
with **no `source` field at all** was appended to `test-history.jsonl` (ts `1789409761`, 14:16:01;
`command:""`, `exit_code:""`, `outcome:unknown`, a `muxcode log`-shaped summary). Under the new
reader it is not evidence either way, and its outcome is unknown regardless — but it was written by
a path that stamped nothing, after rows that did. An older installed binary's `muxcode log` is the
obvious candidate; it is not resolved, and it deserves one look before the residual above is
treated as the only gap.~~

**Retracted 14:35 — edit's correction, verified by plan with `jq`.** The 14:16:01 row is
`bus-response`, not sourceless: plan had read "no `source` field" off a line truncated at 260
characters, before the field. A field is never absent from a cut line. The whole file, parsed: **25
rows with no source — all legacy**, the newest at 13:48:03 and 13:48:13, before the first
`hook`-stamped row at 14:02:35 (which bounds the stamping binary's install; the current binary's
mtime, 14:13:49, is a later reinstall); 14 `bus-response`; 5 `hook`; 3 `self-reported`. Nothing
writes a sourceless row any more.

**The point that stands, recorded instead.** Fail-closed means those 25 legacy rows are **no longer
evidence** — a verdict the old doctrine deliberately preserved (`history_provenance.go:26-31` before
`a8fa0db`). Harmless in practice: `deriveSendOutcome` (`graph_exec.go:1687`) passes `st.StartedAt` as
`since`, so a node sees only rows written after it started. The one exposure is a node started
before the stamping binary and harvested by a daemon running `a8fa0db` or later — it would find its
own legacy rows excluded and fall through to the sentinel or the hold. No such node exists: the only
run in flight was canceled at 14:11:27. And the exposure is not live yet either way: `daemon.version`
still reads `846251e`, so the daemon that judges nodes runs the *old* reader (empty source
authoritative; `hook` and `self-reported` accepted as peers, since its exclusion list skips only
`bus-response`) until it is upgraded. The ranking and the fail-closed behaviour begin at that
upgrade, not at the commit.

**Upgraded 14:35 — live now.** The session relaunch brought the daemon up on `3f9a2cb-dirty`
(`daemon.version` rewritten 14:35:36; the binary was built 14:34:19 and installed 14:34:20; the
session's lifecycle log restarts at a `launch` row of 14:35:41), so the daemon that judges nodes
runs the new reader from that relaunch on — the ranking and the fail-closed reader have been live
since then, the 25 legacy rows excluded in practice and not only on paper, with the `since` filter
keeping that harmless. The relaunch also purged the bus dir: no graph run survived (the looping run
`1789407209-spec-to-pr-6329cbb4` is gone with it, so the MUX-183 re-asks stop by accident rather
than by fix), and the active-spec pointer was cleared — nothing points at this spec until
`muxcode spec set` is run again, and no `verify-spec` fires until it is.

*Recorded twice, merged once.* Two plan instances wrote this entry within minutes of the relaunch —
the second (this one) found the first's paragraph already in the file, under a `14:36:09` relaunch
time that matches none of the files above, and folded both into the paragraph you are reading with
only file-backed times kept. Two writers on one spec is the hazard MUX-156 names for inboxes; here it
cost a merge, not a loss.

## Out of scope

- **Whether agents should decline at all.** The decline here was correct; this spec is about the
  executor believing the wrong thing afterwards.
- **The `spec-complete` guard.** It behaved correctly and is not implicated — it simply is not a
  substitute for upstream node attribution.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-148-node-outcome-reads-command-ran-as-task-done | 6h 12m | 2026-09-15 13:41 |

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
run, so Phase 2 is 6/7 and **not complete**. Phase 3 must not treat it as settled. *(Closed 14:22 —
see the entry of that time below.)*

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
the open phase is 6/7. Flagged to edit; whether it becomes a backlog item is the user's call. Edit has since written it up as
a backlog candidate (`/tmp/mux-148-false-failure-and-phasecommit.md`, item 2, non-durable — ranking
suggested near MUX-182, false-completion family). **Filed as
[MUX-183](../backlog/MUX-183-phase-commit-ready-recredits-shipped-phases.md) at 12:10** on the user's
instruction. Verifying the filing found two more defects that touch *this* spec: `phaseHeadingRe`
counts any `### Phase N …` heading as a phase — this spec's `Phase 1 findings` and `Phase 2 decision`
sections made `completed` read 3, not 1 — and `graph retry` resets the counter. The two headings were
renamed and the constraints list moved under Phase 3 the same minute. **Corrected 12:25 on edit's
review:** the parser drops its phase on *any* heading line and re-arms only on `### Phase N`
(`spec_items.go:97-103`), so a checkbox under a `####` label attaches to no phase. The constraints list
had therefore been *invisible* — not, as first written here, counted as Phase 2's — and the move put
Phase 3's own steps behind a second H4, so **Phase 3 read complete**. Both H4s are now bold labels and
Phase 3's fifteen boxes attach to Phase 3; the scan for orphaned boxes is recorded in MUX-183.

**Phase 3 widened to the spawn road, 2026-09-14 11:40, on the user's instruction** relayed by edit
(`/tmp/mux-148-phase3-spawn-road.md`, non-durable; edit's findings re-verified by plan against
`846251e` and `graph status`). Recorded: [Defect 3](#defect-3--the-spawn-road-reads-no-evidence-at-all)
with the reference reproduction on this spec's own run, four spawn acceptance criteria, five Phase 3
steps, and the [design tension](#design-tension--the-spawn-signal-and-the-sentinel-agents-omit) Phase 3
must resolve on the record. Total 42 → 51.

**Commit churn while this was written, recorded so the history reads straight:** the spawn-road code
and test were committed as `5b32433` at 11:39 (`phase-gate`, user-approved), reset at 11:42, and the
spec alone re-committed as `f942c43` — which therefore carries an earlier version of these paragraphs
claiming the code was committed. At that moment it was not: the code and test sit in the working tree
to go through the lap's chain first. The record here and in Defect 3 is the corrected one.

**Checked off 11:50 on the user's instruction, withdrawn 11:55.** On "verify and check off work
completed by edit and worker" plan ticked four Phase 3 steps from its reading of the working-tree
spawn change and its unit test, stating that the suite had not yet been observed passing and that the
ticks would be withdrawn if it failed. It failed: lap 2 of run `1789399519` ran `implement` (67s,
`outcome=success`), `build` (14s), then **`test` failed (66s)**; the run was **canceled**, and the
spawn change and its test were **removed from the working tree** (HEAD still `f942c43`; no stash
carries them; `heldUnknown` and the test name are absent). The four ticks are struck inline with the
reason. **Spec 12/51.** The tree now carries one unrelated-looking edit — `delivery.go`
`writeDeliveryStatus` creating the delivery directory, whose comment says a missing directory made
`spawnHasResponded` read an answered worker as unanswered and `replaceLostWorkers` replace it — which
may be the root cause the failed lap hit; it belongs to no MUX-148 phase and is noted only so the next
reader knows why it is in the tree. Everything in Phase 3 is open.

**Defect 4 added 2026-09-14 12:00 on the user's instruction** relayed by edit: the same root cause
with the opposite sign — a genuine success recorded as `failure` because the passing re-run was not
classified and the earlier failing row outranked the reply. Verified reproduction on another session
(`is-operations-gateway`, run `1789400058-spec-to-pr-c627b2f9`); the classifier mechanism re-verified
by plan here. One acceptance criterion, one inherited Phase 3 constraint (the signal must be tied to
the dispatched task, not to recognised commands) and one Phase 5 mirror test added. Total 51 → 54,
still 12 checked.

**Phase 2 walked a third time, 12:19, by run `1789402487-spec-to-pr-f05fd39f`** (`implement` worker
report at `/tmp/mux-148-phase2-worker-report.md`, non-durable). The worker declined item 7 correctly,
wrote no code, re-verified every Decision 4 fact, and found that a provenance field alone would not
close the `muxcode log` road — recorded above as an inherited Phase 3 constraint (fail closed on
absence) and folded into Decision 4, each claim re-verified by plan against `e7f664b`. Nothing checked
off; spec 12/55. `e7f664b` (12:16) committed this spec's Defect 3/4 record, MUX-183 and the backlog
renumber.

**Lap 2 of the same run, 13:07 — a no-op by construction, and MUX-183's second live occurrence.** The
worker (`/tmp/mux-148-phase2-lap2-report.md`, non-durable) changed nothing and said why: the only open
item is the user's Decision 4, which no worker iteration can close, and `implement` has a single
unconditional edge, so it cannot route the run anywhere but onward. Meanwhile `phase-check` passed
again at 6/7, the user approved `phase-gate` at 13:00, and `81793df` committed this spec and
`backlog.md` — the commit agent **held back the unattributed `delivery.go` by its own judgment** and
reported that the dispatch named "Phase 1: Establish the boundary", a phase already shipped in
`7e03dc9`. Harm avoided by judgment, not by machinery. `loop-check → implement` has fired once of five,
so up to four more laps will each ask the user to approve a commit the phase does not warrant. Plan
concurs with the worker's recommendation: **cancel or hold the run, put Decision 4 to the user, and
decide `delivery.go`'s fate before any further commit node fires.** Nothing checked off; 12/55. Time
2h 40m recorded.

**Lap 3, 13:38 — a fourth run, `1789407209-spec-to-pr-6329cbb4`, started by the user at 13:33.** The
worker (`/tmp/mux-148-phase2-lap3-report.md`, non-durable) changed nothing, for the same reason, and
added the one fact that matters: `max_iterations` is per-run state, so the new run reset the loop
budget to five — the cap bounds a run's laps, never this cycle, and no number of laps closes an item
reserved for the user. Decision 4 is still unasked; `delivery.go` is still uncommitted since 11:44,
having survived two commit nodes by the commit agent's judgment alone (`a07c746`, 13:28, is edit's
`--wait` correlation fix and unrelated to this spec). Recommendation unchanged and now on its third data
point: put Decision 4 to the user, cancel or hold the run, do not approve the `phase-gate` prompt it
will raise, decide `delivery.go`. Nothing checked off; 12/55.

**Lap 4, 13:48 — no-op again; the third mis-approval happened as predicted** (13:43:27 → `f9249ba`,
docs only, `delivery.go` held back a third time). The gate's exact text is now on the record in
MUX-183 from the bus's own copies: every run was asked to *"Approve committing Phase 1: Establish the
boundary"* while its intent was Phase 2. Nothing checked off; 12/55. Time 3h 17m recorded. Left open, reasons inline: the send-road steps
(untouched), the option 3/4 application (partial), the double-hold (send road pending), and the
spawn-signal decision — the user's or Phase 3's to record; approving a commit is not recording a
design choice. No acceptance criterion is ticked: the spawn criteria are proven at unit level only,
and the daemon judging the live lap still runs `846251e` (installed binary `5b32433-dirty` since
11:40:59; `daemon.version` unchanged), so the binary that would show the reproduction gone is not
the one running.

**14:22 — Decision 4 answered: `cmd/log.go` is in scope. Phase 2 closed 7/7; spec 14/55.** Run
`6329cbb4` was canceled at 14:11:27 (`graph-run-canceled`), so the loop that re-asked the gate is
over. The user's answer came through edit, and the run's last `implement` lap had already built what
"in scope" was defined to mean: `a8fa0db` (14:23:14) carries the three-valued `Source`, the
`WriteHookHistory` choke point, `cmd/log.go` routed through it, and `latestAuthoritativeRow` failing
closed — recorded under [Decision 4](#decision-4--is-the-muxcode-log-writer-in-scope) with its
residual (a hand-appended row can still claim `hook`; process-ancestry provenance is the close and
is not in this commit) and one flag plan raised and **retracted at 14:35** on edit's correction — the
row was `bus-response`, read off a truncated line; the point that stands is that the 25 legacy
sourceless rows are no longer evidence, harmless under `since`-filtering, and not live until the
daemon (`daemon.version` still `846251e` at 14:35) is upgraded — which the 14:35:36 session relaunch
did, on `3f9a2cb`; it also cleared the active-spec pointer and dropped run `6329cbb4`. `e9e3941`
committed the orphan `delivery.go` change alone, seconds later — its fate
decided at last, after surviving three commit nodes on the commit agent's judgment. Both commits went
through edit → commit on the user's request, not through a graph gate. Beyond item 7, plan ticked
**one** Phase 3 constraint — fail closed on absence — because `a8fa0db` implements it as written and
the suite was observed passing on the hook road before the commit (14:15:47 / 14:16:00, `PASS=2425`);
the "add actor provenance" constraint is annotated and left open, since what shipped is *source*
provenance, not actor provenance. Every other Phase 3 step is untouched by this commit and stays open.

Phase 1 **disproved this spec's own account of the mechanism** (read-only `gh`/`git` mint no row for
the `commit` role) and **withdrew a constraint plan had asserted** (`hook_codex_test.go` pins the
helper, not the precedence). Both corrections are recorded inline above rather than silently edited
away. **The blocking question for Phase 2 is now: which path actually minted the 2026-09-03 row?**

This spec moved to `docs/requirements/drafts/` on 2026-09-14 (`7e03dc9`), so `muxcode spec set` no
longer warns and the automatic `verify-spec` pass fires for it. The active-spec pointer lives in the
bus directory and is purged on every session relaunch — it was re-set on 2026-09-14 11:25 after the
daemon relaunch cleared it; a restart with no pointer means review completes with no verification
dispatch, which reads as a stalled run rather than an unset pointer.

**Phase 3 verified 2026-09-14 15:40 by plan — 13/17, not complete** (spec 33/56). Graph run
`1789413170-spec-to-pr-828f8c3a`, user-started, `implement` worker report
`/tmp/mux-148-phase3-report.md` (non-durable; what matters is recorded above). Chain evidence, all
hook-observed: first `test` node **failed** 15:27:26 (2600 pass / 3 fail — `TestAuthoritativeRowOutranksSentinel`,
`TestSpawnHarvestPassesReportDownstream`, `TestExecSpawnTaskUnprefixedWithoutSendNodes`, each a
pre-existing test encoding the behaviour the change replaces); the `fix` lap rewrote them in place
(its worktree had *nothing to port*, so the edits landed in the checkout) and answered `EXIT=0`;
`build` 15:33:58 exit 0; `test` 15:35:31 exit 0, **2603 pass / 0 fail / 2 skip**; `review` 15:37:00
*0 must-fix, 1 should-fix, 1 nit*. Every code claim in the report checks against the diff. **What
was ticked:** the four inherited constraints that are tests or by-construction facts (first
`deriveSendOutcome` test; the glob in both directions; no double-hold; `hook_codex_test.go`), and
eight steps — every spawn-road step, the negative controls on both roads, the lifecycle event, and
the spawn-signal decision, now recorded under the design tension. Seven acceptance criteria met.
**What stays open, and why:** the send road did not implement the mechanism Phase 2 decided.
`deriveSendOutcome` gained a *conflict* hold — row and sentinel disagree → `unknown` — which is
neither option 4 (nothing checks the row's command type against `n.Action`) nor option 3 (no
per-node token). A row alone still decides both ways, so the 2026-09-03 shape still routes success
and the Defect 4 mirror still records failure whenever the agent omits its sentinel; the report's
claim that the conflict rule *is* the "tied to the dispatched task" constraint is not accepted. That
constraint, the "implement the chosen mechanism" step, criteria 1, 2, 5 and 8, and the actor-provenance
pointer to MUX-185 stay open — and the type-to-action check does not wait on MUX-185, as annotated on
the step. One step **added**: the executor-level spawn hold test the reviewer asked for. **Not run:**
`scripts/test-multi-phase-graph.sh` (two assertions re-anchored, not executed on any binary carrying
the change — the worker declined to run it against the stale installed binary, correctly, and nothing
ran it after the install); `scripts/test-node-outcome-attribution.sh` does not exist yet (Phase 5).
**Daemon fact, recorded not explained:** `daemon.version` reads a `7bcd657-dirty` build of
**15:25:04** — the first-lap `build` node's, produced after the worker's port, so the daemon judging
this run carries the Phase 3 code — but the 15:33:58 rebuild did not cycle it, and the lifecycle log
holds no `daemon-upgraded` row for either build. Consistent with
[MUX-161](../backlog/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) (the build role runs on
codex); how the daemon reached the 15:25 build at all is not established. No branch was named in the
dispatch, so no time was recorded this pass.

**Phase 3 verified 2026-09-15 13:45 by plan on the user's direct instruction ("verify spec against
codebase") — 16/17** (spec 39/56; acceptance 10/13). **In the tree, uncommitted, since `97b7288`:**
`graph_exec.go` and `hook.go` last written 2026-09-14 16:10–16:11 — option 4 on the send road as
`rowAttributesTo` with its three tables, `latestAuthoritativeRowFunc` carrying the acceptance test
inside the source-ranking walk, the `graph-outcome-untied` event, the constant-fold nit, and
`DefaultGitPatterns` widened to checkout/switch/`gh pr checkout`; `graph_port_test.go` (15:55) with
the executor-level spawn hold test; `graph_exec_test.go`, re-edited today at 13:36:40. **Evidence:**
the suite was hook-observed green on this code at 12:08:52 and 12:09:44
(`go test -p 1 -count=1 -v ./...`, exit 0, 2998 pass / 0 fail / 2 skip); review at 12:11 *0 must-fix,
2 should-fix, 1 nit* — the should-fixes (checkout fixtures returning `git commit`, mixed-row coverage)
are what the 13:36 edit answers (`fixtureCommandFor` split, `TestLatestAuthoritativeRowFuncMixedRows`
and its nil-accept control), and **that edit has not been observed passing**: the 13:37:17 build→test
chain wrote no test row, the codex test agent's pane reading *model at capacity*. Both new tests pass
by plan's reading of `latestAuthoritativeRowFunc`; the ticks stand on the 12:08 green plus that
reading and are withdrawn if the next run disagrees, as on 09-14. **Ticked:** criteria 1, 5 and 8,
the tied-to-task constraint on its own recorded closing condition, the implement step and the
executor-level spawn test. **Open, and why:** the actor-provenance pointer (MUX-185); criterion 2, the
mirror — attribution cannot reach it, because a matching-type failure row *is* tied to the task, so
it closes only by the user accepting the residual or by holding an uncorroborated failure row (put to
the user); Phase 4 0/4, now with a second reason — `c` holds every run until it has a token; Phase 5
0/9, `scripts/test-node-outcome-attribution.sh` absent. **Residuals recorded, not closed:** unmapped
actions keep row-decides; the `git*commit` glob still matches `uncommitted`. **Daemon:**
`daemon.version` is a `97b7288-dirty` build of 12:02:03 — this tree after the code was written, so
the daemon judging nodes carries attribution; the 13:37:12 rebuild did not cycle it
(`upgrade-daemons: ps: operation not permitted`, the build role on codex — MUX-161 again). No graph
run has exercised it live: `graphs/` holds only the five 09-14 runs. **Time:** 6h 12m recorded from
the ledger (22367s) on the user's instruction; no dispatch named the branch.
