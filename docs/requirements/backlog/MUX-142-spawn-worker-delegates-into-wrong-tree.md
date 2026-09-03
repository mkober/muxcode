# A Spawned Worker Delegates Build and Test Into the Wrong Tree

A graph `spawn` node launches its worker with the **`edit` role definition**, which instructs the
agent to delegate build, test and review over the bus. The worker obeys — it has no way to know it
is a graph node whose `build`, `test` and `review` are separate downstream nodes. The delegated
agents then execute in **their own cwd, the main checkout**, while the worker's code lives in a
spawn worktree, so the results describe a tree containing none of the worker's work.

The worker is told "build clean, tests green" about a tree that does not contain a single line of
what it just wrote, and proceeds on that signal.

Tracking: _(no GitHub issue yet)_

## Context

### How this surfaced

Observed live 2026-09-02 in a **different session and a different repository** (a `PBP1-` branch,
pnpm/TypeScript), relayed to plan as a pane capture with the user's note "this keeps happening in
another session". The specific figures below marked *relayed* could not be re-derived from this
repo; the **mechanism** was then verified independently against muxcode source and is recorded
separately.

### The mechanism, and how each part was established

| Fact | How established |
|------|-----------------|
| `implement` and `fix` are `spawn` nodes with `"role": "edit"` in `spec-to-pr` | **Verified** — `bus/graph_templates.go:31`, `:34` |
| `build`, `test`, `review` are **separate `send` nodes** in the same template | **Verified** — `bus/graph_templates.go:32`, `:33` |
| A spawn launches as `cd <worktree> && AGENT_ROLE=<role> muxcode agent launch <role>` | **Verified** — `bus/spawn.go:214` |
| `Node.Role` selects the agent *definition*, so a `role: edit` spawn loads `code-editor.md` | **Verified** — `AgentFileName()` (`bus/launch.go:61`); corroborated by [MUX-119](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) |
| That definition tells the agent to delegate build → test → review | **Verified** — `agents/code-editor.md:238-243`, "As the edit agent, you are the primary orchestrator. After making code changes: 1. Delegate a build… 2. …delegate tests… 3. …request review" |
| Nothing in the spawn or graph path tells a worker *not* to delegate | **Verified** — no "do not delegate" / "never delegate" string in `code-editor.md`, `bus/spawn.go`, or `bus/graph_templates.go` |
| A bus `Message` carries **no** cwd, tree or worktree field | **Verified** — `bus/message.go:13-20`, eight fields: `id ts from to type action payload reply_to` |
| `build` and `test` agents run with `CdPrefix: true` into the **session** repo dir | **Verified** — `bus/profile.go:640-642`, `:657-659` |
| The worker's `cd <worktree> && muxcode send test …` moves only the *sending* shell | **Verified** — follows from the two rows above: the message carries no tree, the receiving agent has its own |
| Test replied "273 tests, 11 suites" — identical to the pre-phase total, with the worker's two new suites existing only in the worktree | *Relayed* (other repo, not reproducible here) |
| `./build.sh` compiled main, which contained none of the phase's code or its two new dependencies | *Relayed* |

### These are two independent defects

**Defect 1 — role-instruction collision.** The `edit` definition is written for the *interactive
orchestrator*, a role whose job is to delegate. A spawn reuses that same definition for a *worker*,
whose job is the opposite. The worker is behaving correctly with respect to its instructions; the
instructions are simply the wrong ones for the context it was launched in.

**Defect 2 — no tree context on the bus.** Even with Defect 1 fixed, *any* agent working outside the
session's main checkout that delegates gets an answer about the wrong tree, silently. There is no
field to carry the tree and no check that the answering agent shares one. This is latent for every
future spawn, `map` node, and for a user working by hand in a worktree.

Defect 2 has **already bitten this repo once**, independently of Defect 1: during MUX-136 the review
agent re-reported a frontmatter finding as still open *after* it was fixed, because review read main
while the fix sat in a spawn worktree. It was recorded in that spec as "check WHICH TREE before
trusting a re-report" — a human workaround for exactly this defect.

### What is *not* corrupted

Worth stating plainly so the severity is not overstated: the graph's own `build`/`test` nodes run
**after harvest, against main**, and those are the nodes that actually gate the phase. The phase gate
therefore stays honest. The damage is (a) duplicated build/test runs, and (b) a worker making
decisions on meaningless green.

But (b) is not cosmetic, and it reaches further than the worker:

- A worker told "tests green" may **stop fixing** a failure that is real in its own tree.
- A worker may **declare a phase complete** on that basis, and that claim is harvested.
- The harvested claim feeds `update-spec`, where **plan checks off acceptance criteria**. A false
  green can therefore be laundered into a spec tick — the exact failure mode
  [MUX-136](./MUX-136-bare-resume-loses-agent-definition.md) exists to prevent, arriving by a
  different road.

### Relationship to existing specs

| Spec | Relationship |
|------|--------------|
| [MUX-119](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) | **Qualifies its central table.** MUX-119 classes `implement`/`fix` spawns as "❌ No — isolated". They are isolated in *pane* terms, which is what that spec measures, but they are **not** isolated in delegation terms: they reach back into the shared `build`/`test` agents. The table is not wrong for its own question; it should not be read as a general isolation claim |
| [MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md) | Same family — spawn workers the graph executor mismanages |
| [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md) | Same family; both concern the spawn lifecycle rather than the graph shape |
| [MUX-007](../completed/MUX-007-verify-spec-stale-review-refire.md) | Same *class* as its changed-files problem — an agent reasoning about a tree other than the one under test |
| [MUX-118](./MUX-118-rename-edit-role-to-code.md) | Any fix that introduces a distinct worker definition should land in step with the rename, not fight it |

### Why it matters

The two flagship spawn nodes of the flagship template both hit this on every run, and the failure is
silent in both directions: the worker sees green that means nothing, and the build/test agents see a
request that looks ordinary. Nothing in a lifecycle log distinguishes it from a healthy run.

## Requirements

### Acceptance criteria

- [x] A spawned worker **does not delegate** build, test or review over the bus — it builds and tests
      in its own worktree, or reports that it cannot
- [x] The prohibition reaches every spawn of a delegating role, **not just the two nodes in
      `spec-to-pr`** — a fix that only edits template message strings does not satisfy this
- [ ] A delegated build/test that would answer about a **different tree than the requester's** either
      carries the requester's tree or **fails loudly**; it never answers silently about the wrong one
- [x] The interactive `edit` agent's orchestration behaviour is **unchanged** — it still delegates
      build → test → review from the main checkout
- [x] **Negative control:** a spawn that legitimately needs a peer agent (one whose work is not
      tree-scoped) is still able to reach it
- [x] **Negative control:** a normal non-spawn graph run is unchanged — no extra nodes, no added
      latency, no new alerts
- [ ] The harvested worker report cannot present a delegated green as its own verification — if the
      worker did not run the check in its own tree, the report says so

### Key files

| File | Purpose |
|------|---------|
| `agents/code-editor.md` | Carries the orchestration instruction the worker inherits (`:238-243`) |
| `tools/muxcode/bus/spawn.go` | `StartSpawnOwned()`, worktree creation, launch string (`:214`) |
| `tools/muxcode/bus/graph_templates.go` | `spec-to-pr` `implement`/`fix` spawn nodes (`:31`, `:34`) |
| `tools/muxcode/bus/message.go` | `Message` — would carry any tree field |
| `tools/muxcode/bus/profile.go` | `CdPrefix` for `build`/`test`, and `CheckSendPolicy()` if the prohibition is enforced at send |
| `tools/muxcode/cmd/send.go` | Natural enforcement point for a tree-mismatch check |

## What landed (Defect 1 fixed the same night)

Between this spec being filed and its first review, the edit agent implemented Defect 1 — and picked
the **mechanical** option, not the instruction-only one this spec hedged toward. Verified here by
reading the working tree (**uncommitted** at the time of writing; no test run observed by plan):

| Piece | Where | What it does |
|-------|-------|--------------|
| Ownership preamble | `bus/graph_exec.go:434` | Spawn task messages open `[graph run <id> · node <id>]` and name the roles the graph dispatches itself |
| Worker instruction | `agents/code-editor.md:246` | An **Exception** clause overriding the Orchestration Role when that preamble is present — "Do NOT delegate them… runs in whatever working directory you happen to sit in" |
| Enforcement | `bus/graph_authority.go` `CheckGraphNodeAuthority()`, called at `bus/inbox.go:173` | Refuses a send from a spawn role owned by a **still-running** run to any role that run holds as a `NodeSend` |
| Tests | `bus/graph_authority_test.go` | `RefusesOwnedRole` plus three negative controls — `AllowsUnownedRole`, `AllowsOrdinaryRoles`, `AllowsAfterRunEnds` |

Its own comment makes the argument this spec made: *"Answering an instruction failure with another
instruction leaves the same gap, so ownership is enforced where every send funnels through."* That is
enforcement option (b) below, and it is the right one — the guard is generic over spawn roles and
graph nodes, so it satisfies the "not just the two nodes in `spec-to-pr`" criterion that an
instruction-only or template-only fix would have failed.

**Defect 2 is untouched.** `bus/message.go` still carries eight fields and no tree
(`id ts from to type action payload reply_to`), and the guard deliberately constrains **only** spawn
roles owned by a running run — its own comment notes that daemon dispatches and human-driven sends
"never match". So every cross-tree path that does not involve a spawn worker delegating remains
silently wrong:

- A **review requested while work sits in a worktree** still reads main — the MUX-136 case, which had
  no spawn worker delegating at all.
- A **human working by hand in a worktree** who delegates still gets main's answer.
- Nothing yet stops a harvested report presenting a delegated green as its own verification.

This is why the spec is **not** redundant: it was filed as two separable defects, the trigger is
fixed, and the independently-reachable half is not.

## Defect 1 is fixed on the Claude road only (2026-09-03)

A live subsession surfaced that the Defect 1 fix recorded above is **provider-specific**. The
`agents/code-editor.md:246` carve-out governs Claude Code agents. Six agents run on OpenCode in the
observed session — including `review` — and the OpenCode road reaches a graph worker by a different
path that the carve-out does not fully cover.

The finding was relayed through edit from a subsession's pane captures. **Every mechanism claim below
was re-verified here against this repo's source**, and doing so corrected the diagnosis twice — once
against the subsession, once against the handoff. Both corrections are recorded so neither is
re-derived.

### Correction 1 — the build/test definitions were never "clean"

The subsession reported that `agents/code-builder.md` and `agents/test-runner.md` have *"zero
auto-forward text"*. **False.** Both still carry auto-forward instructions, in the **negative** form:

| Location | Text |
|----------|------|
| `agents/code-builder.md:34` | "The bash hook automatically chains to the test agent — do NOT send a test request yourself." |
| `agents/code-builder.md:77` | "Do NOT run tests yourself and do NOT send a test request — the bash hook auto-chains build->test on success." |
| `agents/test-runner.md:16` | "Send exactly ONE reply per request. Do NOT send additional messages to edit or review — the bash hook auto-chains test->review on success." |
| `agents/test-runner.md:21` | "Do NOT send a review request — the bash hook auto-chains test->review on success." |

The subsession grepped for the literal command `muxcode send test test`, found none, and read absence
of the positive form as absence of the instruction. Both files are **unmodified since June** (mtimes
2026-06-22 and 2026-06-10) — nothing was ever removed, and nothing needed to be. The negative
phrasing is correct for hook providers, and `adaptBodyForNonHookProvider()`
(`bus/provider_opencode.go:603-611`) rewrites each of those four sentences into a **positive** manual
send for OpenCode. A build agent on OpenCode sending a test request is the documented three-layer
degradation working as designed — **not** a leak, and not this defect.

### Correction 2 — regeneration *does* carry the carve-out; the gap is elsewhere

The handoff concluded that the replacement table "has no graph-worker rule", so regenerating the body
would not help. **The mechanism is wrong, though the conclusion survives for a different reason.**

| Fact | How established |
|------|-----------------|
| `adaptBodyForNonHookProvider` is `strings.ReplaceAll` over its keys, then `return body` | **Verified** — `bus/provider_opencode.go:626-630` |
| Body text with no matching key therefore passes through **verbatim** | **Verified** — follows from the above; the table needs no graph rule to preserve the carve-out |
| `agents/code-editor.md` carries the carve-out (1 occurrence of "races the graph") | **Verified** — `grep -c` |
| `.opencode/agents/edit.md` carries **0** — but *because it is stale*, not because the table stripped it | **Verified** — `grep -c` = 0, mtime `2026-08-18 13:43` vs source `2026-09-02 23:49`; the carve-out did not exist when that body was generated |
| That same stale body **does** carry the table's rewritten output at line 337 | **Verified** — proves the table fired on 2026-08-18 and passed the rest through |

So a regenerated `.opencode/agents/edit.md` **would** contain the carve-out. Staleness is the reason
it is missing today.

### The actual "even when fresh" gap — `bus/prompt.go`

There is a **second, independent** injection site that the replacement table never sees and
regeneration never fixes:

| Fact | How established |
|------|-----------------|
| `BuildSharedPrompt` appends a `### Manual Bus Messaging (no hook support)` section for every non-hook provider | **Verified** — `bus/prompt.go:129-131`, gated on `!provider.SupportsHooks()` |
| For `role == "edit"` it emits an **unconditional** numbered build→test→review orchestration with `--wait` | **Verified** — `bus/prompt.go:134-142` |
| This section is generated prose, **not** agent-body text — the replacement table cannot reach it | **Verified** — separate writer in a different file |
| It has **zero** graph awareness | **Verified** — `grep -c "graph" bus/prompt.go` returns `0` |
| It is present verbatim in the live generated body | **Verified** — `.opencode/agents/edit.md:427-441` |

This is the piece that survives regeneration. A **freshly generated** OpenCode edit body would carry
both:

- the carve-out from `agents/code-editor.md:246`, which is **conditional** ("When your task message
  opens with `[graph run … · node …]`… Do NOT delegate them"), and
- `bus/prompt.go`'s block, which is **unconditional** ("**After making code changes**, manually
  orchestrate the build→test→review chain") and appears *later* in the document.

Two instructions in one file that contradict each other in exactly the graph-worker case. Whether a
model reconciles them correctly is not a property this system should be relying on — and the
`CheckGraphNodeAuthority` backstop only refuses sends from a **spawn role owned by a still-running
run**, so it does not cover a non-spawn OpenCode agent that manually chains.

### Two failure modes, stated precisely

1. **Generated bodies are not invalidated when their source changes.** A body can predate a fix by
   weeks with no signal. `.opencode/agents/edit.md` is sixteen days behind its source; `build.md`,
   `test.md`, `review.md` and others regenerated on 2026-09-03 00:21 while `edit.md`, `run.md` and
   `commit.md` did not — because edit runs on Claude in this session, so its OpenCode body was never
   rewritten. These are gitignored artifacts (`.gitignore:19`), so nothing tracks the drift. This
   silently un-does **any** agent-definition fix for whichever roles happen not to have regenerated,
   and is a defect class in its own right, not specific to this spec.
2. **The shared-prompt manual-chain block has no graph carve-out.** Independent of staleness, and not
   fixed by regenerating anything.

### Added acceptance criteria

- [ ] The graph-worker carve-out reaches non-hook providers by every injection path, not only the
      agent body — specifically `BuildSharedPrompt`'s `Manual Bus Messaging` block
- [ ] A generated agent body older than its source definition is detected and surfaced (or
      regenerated), rather than running silently stale
- [ ] Negative control: a non-graph OpenCode edit agent still receives the manual-chain instruction
      unchanged

## The third tree: a `fix` node's worktree cut from a stale HEAD (2026-09-03)

### First, a correction that never reached this spec

An amendment was drafted claiming *"there is no harvest step — nothing moves a worker's code into the
checkout."* **That is wrong, and it was never written here** — the send bounced as a duplicate before
delivery, so no false claim ever entered this document. It is recorded only because the reasoning was
built on it and someone re-reading the template will otherwise re-derive it.

**A harvest does exist.** `bus/graph_port.go` — *"Spawn-output harvest — MUX-131 Defect A"* — lands
worker output:

| Fact | How established |
|------|-----------------|
| The executor harvests at iteration completion, where `spawnGroupOutcome` derives success | **Verified** — `harvestRunningNode` → `portSpawnGroup`, before `finishNode` |
| It applies the worktree's diff as a patch into the checkout's working tree, **uncommitted** | **Verified** — `graph_port.go` file doc comment, *"Landing model (spec mechanism 5, AMENDED 2026-09-01 on authority grounds)"* |
| It **deliberately never commits** — a daemon-side commit would run where `CheckCommitAuthority` is not called | **Verified** — same comment: this is the authority model being respected, not an oversight |
| `git apply` is all-or-nothing and its error names the conflicting paths | **Verified** — same comment |

**Why it was missed, and worth stating so it is not missed again:** the harvest is **not a graph
node**. It lives in the executor, between nodes. Any reading that enumerates the template's node list —
which is how this spec's own mechanism table was built — will not find it.

### The real defect

The landing is uncommitted, into the checkout. But every graph spawn worker was given a **fresh
worktree cut from `HEAD`**:

| Fact | How established |
|------|-----------------|
| Graph spawns were hardcoded to use a worktree | **Verified** — `graph_exec.go:41`, `StartSpawnOwned(..., true, ...)` before the fix below |
| Worktrees are cut with `git worktree add --detach <path> HEAD` | **Verified** — `bus/spawn.go:652` |
| The phase commit happens several nodes downstream, behind the `phase-gate` | **Verified** — `spec-to-pr` edge list |

A `fix` node therefore began work in a tree cut from a `HEAD` that **could not contain the previous
node's output** — that output had been landed *uncommitted into the checkout*, and an uncommitted
change is by definition absent from `HEAD`. The harvest was working exactly as designed; the next
worktree simply could not see what it had produced.

**Observed live 2026-09-03** *(relayed from the affected project, second occurrence there)*: a `fix`
worker found `src/api/salesforce.ts` absent from its own worktree, correctly distrusted its spawn
directory, and edited the real checkout instead. Had it trusted its worktree it would have "fixed" a
file that was not there, or written a duplicate into a tree nobody builds.

So each iteration ran **three trees**: the `implement` worktree, a `fix` worktree cut from a stale
HEAD, and the checkout where build, test, review and commit run.

### The fix that landed (this session, uncommitted)

Graph spawn workers **no longer get a worktree at all**. `graphSpawnFn` passes `useWorktree=false`, so
a worker launches with no `cd` prefix and runs in the session checkout — the one tree build, test,
review and commit already use.

| Piece | Where | Verified |
|-------|-------|----------|
| Graph spawns take no worktree | `graph_exec.go:41` — `StartSpawnOwned(..., false, ...)` | **Verified** by plan reading the working tree |
| Harvest goes cleanly inert rather than erroring | `graph_port.go:172-178` — members without a worktree "have nothing to port" | **Verified** |
| Worker reuse across loop re-entry (MUX-131 Defect B) unaffected | `graph_port.go:473-476` — `advanceSpawnWorktree` returns immediately on an empty worktree | **Verified** |
| CLI spawns can still request a worktree | `cmd/spawn.go:86` — `--no-worktree` is the opt-**out**, so worktree remains the CLI default | **Verified** |
| Full suite 2338 pass / 0 fail / 1 skip, `go vet` clean | — | **Relayed** — not witnessed by plan |

**User decision 2026-09-03:** `map` fan-out does not need isolation either, so this applies to **every**
graph spawn rather than only sequential nodes.

### What this does to Defect 2

Defect 2 ("the bus carries no tree") **narrows for graph work and is unchanged everywhere else.** With
every graph agent now sharing one tree, a graph-dispatched build/test/review can no longer answer about
a different tree than the worker used — but note *how* that was achieved: by **removing the second
tree**, not by teaching the bus what tree a message refers to. The message is still tree-less.

Still reachable, and still this spec's remaining work:

- **CLI spawns**, which default to a worktree (`--no-worktree` opts out) and whose delegated
  build/test/review still `CdPrefix` into the session checkout.
- **A human working by hand in a worktree** who delegates — no spawn involved at all. This is the
  [`MUX-136`](./MUX-136-bare-resume-loses-agent-definition.md) case, where a review re-reported a fixed
  finding against main while the fix sat in a worktree.
- **The cross-session case**, where the delegating and executing agents were never in the same tree.

The argument for a tree-aware bus is therefore narrower than when filed — it no longer rests on the
`spec-to-pr` arc — but it is not closed, and the fix above is a mitigation of the *trigger*, not of the
*defect*.

## Implementation

### Phase 1: Confirm the live shape and pick the enforcement layer

- [ ] Reproduce in **this** repo: run `spec-to-pr` to an `implement` spawn and capture the worker's
      pane, confirming it issues `muxcode send build`/`send test`
- [ ] Confirm the delegated agents answered about main (compare a worktree-only file against what the
      build/test agent reported)
- [x] Decide the enforcement layer and record the decision with its rationale — **(b) chosen and
      implemented**, see "What landed" above:
      - (a) **Instruction-only** — teach the spawn seed/definition not to delegate. Cheap, but relies
        on the model obeying, and an instruction is exactly what failed here
      - (b) **Send-side guard** — `CheckSendPolicy()`/`cmd/send.go` refuses a build/test/review send
        from a spawn worker, with a message naming the graph node that owns it. Mechanical
      - (c) **Tree-aware bus** — add tree context to `Message` and let the receiver refuse or honour
        it. Fixes Defect 2 generally; largest change
- [x] Record explicitly whether Defect 2 is in scope for this spec or split to its own — **in scope
      and now the whole of it**, since Defect 1 is fixed; splitting is still open to the user, but the
      remaining work is coherent on its own and this spec already carries its evidence

### Phase 2: Stop the worker delegating (Defect 1)

- [x] Implement the chosen enforcement so a spawn worker cannot silently delegate a tree-scoped check
- [x] Ensure it applies to **any** spawn of a delegating role, not only `spec-to-pr`'s two nodes
- [ ] Give the worker a working alternative — it must be able to build and test **in its own
      worktree** (confirm the tool profile permits this from the spawn cwd). *Still open, and the
      landed fix chose the other branch of the criterion: the preamble says "Do the work, reply to the
      requester, and stop", so the worker reports rather than verifying locally. Acceptable, but it
      means a phase is only ever verified against main after harvest — worth a deliberate ruling*
- [x] Leave the interactive `edit` path untouched; pin that with a test
- [x] Unit test: a send that would cross trees from a spawn is refused/rewritten; the same send from
      the interactive agent is allowed

### Phase 3: Make a cross-tree answer impossible to mistake (Defect 2)

- [ ] Decide and implement: carry the requester's tree on the message, or detect and refuse a
      mismatch at the receiver
- [ ] Emit a lifecycle event when a cross-tree request is refused or redirected, so this is visible
      in a log rather than inferred from a pane
- [ ] Confirm the MUX-136 review-agent case is covered — a review requested from a worktree must not
      silently review main
- [ ] Unit test with a negative control: same-tree requests are untouched

### Phase 4: Fold the finding back into the affected specs

- [ ] Add a note to [MUX-119](./MUX-119-graph-routes-edit-work-off-the-edit-agent.md) qualifying the
      "isolated" classification of `implement`/`fix` — pane-isolated, not delegation-isolated
- [ ] Cross-link from the spawn-family specs ([MUX-120](./MUX-120-spawn-worker-never-woken-for-seeded-task.md),
      [MUX-135](./MUX-135-spawn-seed-record-gc-strands-completion.md))
- [ ] Update `CLAUDE.md`'s graph-orchestration constraint if the fix changes what a spawn may do
- [ ] Record the interim workaround for users hitting this before the fix lands:
      `muxcode graph export spec-to-pr > /tmp/spec-to-pr.json`, add an explicit no-delegation
      instruction to the `implement` and `fix` messages, then
      `muxcode graph create /tmp/spec-to-pr.json --scope project` — **verified to exist**
      (`cmd/graph.go:59`, `:62`, `:116`), takes effect on the next run, and does not touch repo source

### Phase 5: Integration test

- [ ] Create `scripts/test-spawn-no-cross-tree-delegation.sh`
- [ ] A spawn worker in a worktree attempting a tree-scoped delegation is refused/redirected — assert
      the mechanism, not a log string
- [ ] **Negative control:** the interactive `edit` agent delegating build/test from the main checkout
      still succeeds — a fix that blocks both passes the first check and must fail here
- [ ] **Negative control:** a spawn's non-tree-scoped peer message still delivers
- [ ] Cross-tree refusal emits its lifecycle event exactly once per request (no re-drive storm)
- [ ] A worker that cannot verify in its own tree reports that fact rather than a green
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run it and confirm every check executes

## Status

**In Progress** — filed 2026-09-02 from a live observation relayed from another session, with the
mechanism verified independently against this repo's source. **Defect 1 was fixed the same night on
the Claude road** (see "What landed") — a 2026-09-03 subsession then established that the fix is
**provider-specific**, and that the OpenCode road is still open (see "Defect 1 is fixed on the Claude
road only"). **Defect 2 remains open** as filed.

The remaining work is therefore in three parts, not one: Defect 1's OpenCode half, Defect 2
(tree-aware bus), and the generated-body staleness class the OpenCode finding exposed. Phase 2 is
complete only for hook providers.

**Amended 2026-09-03 (see "The third tree").** Two things changed. First, a drafted claim that no
harvest existed was **wrong and never landed here** — `bus/graph_port.go` has harvested worker output
since MUX-131; it is not a graph node, which is why a node-list reading of the template missed it.
Second, the defect that *was* live is now fixed: graph spawn workers were given a fresh worktree cut
from `HEAD`, which could not contain the previous node's uncommitted harvested output, so a `fix` node
worked in a third tree. Graph spawns now take **no worktree** and run in the session checkout.

That mitigates Defect 2's **trigger** for graph work by removing the second tree — not by making the
bus tree-aware, which it still is not. Phase 3's scope is correspondingly **narrower** than when filed:
it no longer rests on the `spec-to-pr` arc, and now stands on CLI spawns (worktree remains their
default), hand-run worktrees, and the cross-session case. It remains the largest open body of work,
with Phases 4–5 following it.

A note on how this spec was nearly closed. It was reported back as *"the fix is already implemented
in code this session, so the spec is redundant."* The fix is real, well-built, and better than the
option this spec leaned toward — but "redundant" does not follow, for two reasons worth keeping:

1. It fixes **one of the two defects filed here**. The half it does not touch is the half that is
   independently reachable and has already caused a wrong answer in this repo (MUX-136's review
   re-report), with no spawn worker involved.
2. Code landing is not the same as a spec being satisfied. The acceptance criteria and the
   integration-test phase are the record of *what would have to be true* for this to be finished, and
   three criteria are still unmet. A spec deleted at the moment its trigger is fixed takes the
   unfinished half with it.

Provenance: everything in "What landed" was verified by plan **reading the working tree**. The code
was **uncommitted** at the time and plan did not observe a test run — the four unit tests are read,
not witnessed green.

Open questions for the user:

- **Split or keep?** Defect 2 is coherent on its own and could move to its own id; keeping it here
  preserves the evidence trail that produced it.
- **Rank.** Currently #7 in Defects, ranked when both halves were live and firing on every
  `spec-to-pr` run. With Defect 1 fixed (pending commit), that rank is arguably too high — but it was
  deliberately left in place rather than renumbering 14 rows twice in one session on the strength of
  uncommitted work.
- The interim template-shadow workaround is now **unnecessary** for Defect 1 and was never a fix for
  Defect 2. Recorded in Phase 4 for history only; do not apply it.

Open question for the user: the interim template-shadow workaround is per-project and lives in
`.muxcode/graphs/`, not repo source. It is available immediately and does not block the real fix —
worth applying now, or leave the defect visible until it is fixed properly?
