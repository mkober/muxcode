# MUX-214: The Build Agent's Root `go vet` Fails Every Time and Tells Edit the Tests Failed

**Tracking:** [mkober/muxcode#189](https://github.com/mkober/muxcode/issues/189)

Before every build, the build agent's lint step runs `go vet ./...` at the repository root. The
root is not a Go module — the two modules live under `tools/` — so the command fails every time
with `directory prefix . does not contain main module`. `go vet` is a **test precheck** pattern,
and a failing precheck fires the test chain's failure edge, so edit receives "Tests FAILED (exit 1):
go vet ./... — check test window" from the *build* agent, about a run that tested nothing, while the
build itself then succeeds. On 2026-11-13 that notice reached edit at least ten times.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's request relayed by edit (`/tmp/defect-flaky-and-govet.md`), from a
finding in [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md)'s
Phase 1. The counts are plan's, read from session `muxcode`'s bus stores since the 15:06 re-init;
the agent definition and the pattern were read at `67004ed`. Which code path turns a build-pane
`go vet` failure into a test-chain notice was **not** traced by plan — Phase 1 does that.

### Observed — verified by plan

| Fact | Where |
|------|-------|
| 19 `go vet ./...` rows in `test-history.jsonl`, every one `exit_code 1`, output `pattern ./...: directory prefix . does not contain main module or its selected dependencies` | `test-history.jsonl` |
| The repository has no root `go.mod`; the modules are `tools/muxcode` and `tools/muxcode-llm-harness` | `ls` |
| 10 notices `build → edit`, type `event`, action `notify`, payload `Tests FAILED (exit 1): go vet ./... — check test window`, between 16:13:14 and 18:21:44 | `log.jsonl` |
| The build agent's own reply names it: "Build succeeded: gofmt clean; root go vet could not run because the repository root is not a Go module; ./build.sh built and installed 2 Go modules" | `log.jsonl`, `build → edit` |
| The build agent's lint table: detect `go.mod` → `go vet` → `go vet ./...`, run before the build | `agents/code-builder.md:36-43` |
| `go vet` is the one default test precheck pattern | `DefaultTestPrecheckPatterns = []string{"go*vet"}`, `bus/hook.go:398` |
| `./test.sh` runs `go vet ./...` **inside each module** and reports `failed (go vet)` per module | `test.sh` |

MUX-213's Phase 1 worker counted 20 such notices and 21 vet rows for the day; plan's counts above
are from the stores as they stood at about 19:00 and are lower. The difference is not reconciled.

### Mechanism — partly verified

_The point marked "not traced" below, and the code path from the build pane to the notice, were
both traced in Phase 1 — see [Findings for Phase 1](#findings-for-phase-1). This section is kept as
filed._

- **Verified:** the vet rows land in `test-history.jsonl` although the build agent ran them — a
  history file is chosen by command class, not by the role that ran the command (MUX-213 Findings
  for Phase 1, `bus/hook.go:1159-1214`).
- **Verified:** a test precheck feeds the test chain only when it fails (`CLAUDE.md`, hook-driven
  chains) — which is exactly this case, every time.
- **Not traced:** why the lint step runs `go vet ./...` at the root when its own table says to
  detect `go.mod` there. Either the agent detects a `go.mod` anywhere in the tree, or it runs the
  table unconditionally.

### Why it matters

- Edit is told tests failed when no test ran and the build passed. Each notice costs a turn and
  reads exactly like a real failure — the same notice MUX-213 found arriving from mid-edit runs.
- The vet the notice is about never ran, so a real vet finding in either module is not reported by
  this step at all; only `./test.sh` runs vet where it can work.
- Any repository whose Go modules are not at its root gets the same false alarm on every build.

## Requirements

### Acceptance criteria

- [x] A build in a repository with no root Go module sends edit no "Tests FAILED" notice and writes
  no failing `go vet` row, when every module vets clean — section A of the Phase 3 script, and the
  live observation recorded there: two builds by the session's own build agent after its restart,
  no failing row and no notice
- [x] `go vet` still runs before a build where it can: inside each Go module the repository has, as
  `./test.sh` does — section A; and live, the build agent's reply names the three modules it vetted
- [x] A real vet finding in a module is still reported, and names the module — section B: "Build
  FAILED (exit 1): go -C tools/broken vet ./... — check build window"
- [x] Negative control: a repository with a root `go.mod` and a real vet failure still fails the
  lint step and notifies edit — section D
- [x] The cause recorded in Phase 1 is fixed where it is — the agent definition, the precheck
  classification, or both — and the choice is recorded here — both;
  [Findings and decisions for Phase 2](#findings-and-decisions-for-phase-2)
- [x] `docs/agents.md` (the build agent's lint step) and `docs/hooks.md` (test prechecks) say where
  vet runs and which role's failure reaches edit as a test failure — Phase 2

### Technical approach

**Primary — vet per module, in the build agent's lint step.** The lint table's Go rows run where a
`go.mod` is: for each directory holding one, `go vet ./...` from that directory, matching
`./test.sh`. With no module at the root, nothing runs at the root. This removes the failing command
rather than hiding its notice.

**Secondary, only if Phase 1 shows it is needed — the notice's wording.** A precheck that fails in
the *build* pane reaches edit as "Tests FAILED … check test window". If a legitimate build-side vet
failure can still take that road after the primary fix, the notice should name the role and stage
that actually failed.

**Rejected — drop `go vet` from the test precheck patterns.** The pattern exists so a failing vet
in the test pane stops the chain; removing it trades a false alarm for a missed one.

### Key files

| File | Purpose |
|------|---------|
| `agents/code-builder.md:36-43` | The lint step and its Go rows |
| `tools/muxcode/bus/hook.go:376-398` | Build and test precheck patterns |
| `tools/muxcode/bus/hook.go:1159-1214` | History file chosen by command class |
| `tools/muxcode/cmd/hook.go` | `triggerChain` — the failure edge the precheck fires |
| `test.sh` | The per-module vet to mirror |

## Implementation

### Phase 1: Trace the false alarm

- [x] Reproduce on a scratch repository with no root `go.mod` and one module under a subdirectory:
  a build-role `go vet ./...` at the root → record the history row and the notice it produces —
  `/tmp/mux214-repro.sh`, 8 passed, 0 failed, with the inside-the-module control
- [x] Record why the lint step runs at the root — how `agents/code-builder.md`'s "detect `go.mod`"
  is being applied — as "this is a Go project", with no directory scope, under a rule forbidding `cd`
- [x] Record the code path from a build-pane precheck failure to the "Tests FAILED" notice — five
  steps, below

### Findings for Phase 1

Recorded 2026-11-13 from the implement node of run `1794608375-55-defect-backlog-loop`, from the
worker's handoff `/tmp/mux214-phase1-findings.md`. The reproduction, the transcript reading and the
notice attribution are the worker's. Plan confirmed the reproduction's result in the run agent's
history and read three of the claims in the tree — `agents/code-builder.md:28`, `ChainEvent` and
`precheckPassed` in `bus/hook.go`, and the notice text in `bus/profile.go:1178` and
`config/muxcode.json:186`; each reads as stated.

#### Reproduction

`/tmp/mux214-repro.sh` (throwaway), run by the run agent: **8 passed, 0 failed, exit 0**, task
`1794618702-spawn-100fae85-8de879f8`. A scratch repository with no root `go.mod` and one module at
`tools/mod`; the real `go vet ./...` is run, and its real exit and output are fed to the installed
`hook bash` as a Claude event with `AGENT_ROLE=build`.

| Where `go vet ./...` runs | Result |
|---------------------------|--------|
| At the root | Exit 1, `pattern ./...: directory prefix . does not contain main module or its selected dependencies`. One failing row in **`test-history.jsonl`**, written by the build agent. `build → edit` notify "Tests FAILED (exit 1): go vet ./... — check test window", and `build → analyze` "Test FAILED (exit 1): go vet ./...". The workflow goes idle → `testing` (`hook:bash:test`) → `test-failed` (`chain:test:failure`) — during a build |
| Inside `tools/mod` (control) | Exit 0, no row (a passing precheck writes nothing), no notice |

#### Why the lint step runs at the root

The build agent's Codex transcript
(`~/.codex/sessions/2026/11/13/rollout-2026-11-13T15-29-28-01a1d699-…jsonl`), for the 17:22:14
build, shows **no detection command at all**: `gofmt -l .` at 17:22:20 and `go vet ./...` at
17:22:23, both with the repository root as working directory, then `./build.sh` at 17:22:25. Its
reply: "root go vet ./... is inapplicable without a root Go module".

So the "Detect `go.mod`" column of the lint table (`agents/code-builder.md:36-47`) is applied as
"this is a Go project", not "there is a `go.mod` in this directory". The table has no scope and
names no directory, and the same definition says "**NEVER `cd` into subdirectories**" (`:28`), so
the root is the only place the agent may run a Go row. The definition gives it no road to a module
under `tools/`: in this repository it cannot follow both rules.

#### The code path to "Tests FAILED"

1. `ClassifyCommand` (`bus/hook.go:438`) returns `CmdTestPrecheck` through
   `DefaultTestPrecheckPatterns = {"go*vet"}` (`:398`, matched at `:465-466`). Classification is by
   command, never by role.
2. `ProcessBashHook` (`:1099`) moves the workflow to **`testing`** for any `CmdTest` or
   `CmdTestPrecheck` (`:1131`) — from the build pane too — and writes the row to
   `test-history.jsonl`, chosen by class (`:1159-1214`).
3. `ChainEvent(role, CmdTestPrecheck, failure)` (`:1056`) returns `"test"` whatever the role;
   `precheckPassed` (`:1080`) drops only a passing precheck.
4. `hookBash` calls `triggerChain(session, from="build", eventType="test", "failure", …)`
   (`cmd/hook.go`). The workflow guard acts on success only. The graph-ownership check suppresses
   the chain when a graph run owns build — the lifecycle log at 17:22:23 has `chain-suppressed test
   chain not fired — graph run … owns build as node build`. MUX-213's request gate applies to a test
   *success* only. So on a non-graph build it falls through to `ResolveChain("test", "failure")`.
5. The test chain's failure edge notifies edit "Tests FAILED (exit ${exit_code}): ${command} — check
   test window" (`bus/profile.go:1178`, `config/muxcode.json:186`), sent **from build**, plus the
   analyst notice; then `TransitionWorkflow(StateTestFail, "chain:test:failure")`.

#### Which builds alarm

All ten "Tests FAILED … go vet" notices in `log.jsonl` (16:13:14 to 18:21:44) follow, within 120 s,
a build request **from edit**. None follows a graph build node: fourteen daemon `node=build` requests
between 17:22 and 20:03 produced the vet failure and its row but no notice, suppressed by graph
ownership. So the alarm rate tracks edit-requested builds, while the failing rows accrue on every
build of this repository, graph or not. This also settles the count plan could not reconcile at
filing: ten notices, with more failing rows than notices.

#### What this means for Phase 2

- **Primary (as specified).** The lint step vets per module and runs nothing at a non-module root —
  and the definition's "NEVER `cd` into subdirectories" needs either an exception for it, or a form
  that needs no `cd` (`go -C <module> vet ./...`) or one the evidence guard accepts
  (`cd <module> && go vet ./...`). Which form is Phase 2's to choose and record.
- **Secondary — warranted.** A build-side precheck failure *is* mislabelled: it fires the **test**
  chain's failure edge, worded "Tests FAILED … check test window", from the build pane, and moves
  the workflow to `testing` then `test-failed` in the middle of a build. After the primary fix a
  legitimate module vet failure in the build pane would still take this road. The notice should
  name the build role and stage, and a build-pane precheck should not move the test workflow — with
  a test that fails without the change.

### Phase 2: Fix

- [x] The build agent's lint step vets each Go module from its own directory and runs nothing at a
  root that is not a module — `agents/code-builder.md`: `go -C <module dir> vet ./...` per module
  found by `git ls-files --cached --others --exclude-standard`. This is the instruction; that a
  build agent follows it is Phase 3's to show
- [x] If Phase 1 shows a build-side precheck failure is mislabelled, the notice names the failing
  role and stage; a test that fails without the change — `StageForRole`, following the base role;
  `TestProcessBashHook_BuildRoleVetIsABuildPrecheck`,
  `TestProcessBashHook_SpawnedWorkerVetFollowsBaseRole`, `TestStageForRole`
- [x] Update `docs/agents.md` and `docs/hooks.md`

### Findings and decisions for Phase 2

Recorded 2026-11-13 from the implement node of run `1794608375-55-defect-backlog-loop`, from the
worker's handoff `/tmp/mux214-phase2-docs.md`. Plan read the diff of `agents/code-builder.md` and
`StageForRole` with its call in `ProcessBashHook` (`bus/hook.go`); both match what is recorded here.
The tests and the end-to-end check are listed as the worker reported them.

#### Decision — fixed in both places (fifth acceptance criterion)

Phase 1 found two causes, and the fix addresses each where it is. The choices are the worker's.

| Where | Change |
|-------|--------|
| The agent definition (`agents/code-builder.md`, lint step) | The `go vet` row is now `go -C <module dir> vet ./...`, once per module, each its own call. A new paragraph: Go modules are found, not assumed — list them with `git ls-files -- 'go.mod' '*/go.mod'`, skipping any under `testdata/` or `vendor/`; `go -C . vet ./...` for a root module; never `go vet ./...` at a root holding no `go.mod`. `go -C` keeps the agent at the project root, so the definition's "NEVER `cd` into subdirectories" rule stands unchanged — the form Phase 1 left open. `gofmt -l .` stays: it walks directories and needs no module |
| The classification (`bus.StageForRole`, called in `ProcessBashHook` right after `ClassifyCommand`) | A test precheck run by the **build** agent is filed as a **build precheck**. A build-pane vet failure therefore writes a build-history row (not a test-history one), moves the workflow to `building` (not `testing` and `test-failed`), and fires the build chain's failure edge — "Build FAILED (exit N): <command> — check build window" — naming the build window and, through the command, the module (`go -C tools/x vet ./...`). A passing build-pane vet writes nothing and fires nothing, by the precheck rule. The test agent's own vet is unchanged: still a test precheck. This is the spec's "secondary", which Phase 1 showed is warranted |

Rejected, as in the technical approach: dropping `go vet` from the test precheck patterns.

#### Evidence

- `bus/hook_test.go`: `TestProcessBashHook_BuildRoleVetIsABuildPrecheck` — a passing build vet does
  nothing; a failing one is `CmdBuildPrecheck`, fires the build chain, writes a build-history row
  naming the module and no test-history row, and leaves the workflow at `building`; the test
  agent's failing vet is still a test precheck on the test chain (the negative control). And
  `TestStageForRole`.
- A throwaway end-to-end check on a tree-built binary, `/tmp/mux214-fixcheck.sh`, through the run
  agent: 7 passed, 0 failed, task `1794619006-spawn-100fae85-60447893`. A root vet by build: no test
  row, a build row, edit told "Build FAILED (exit 1): go vet ./... — check build window", never
  `test-failed`. `go -C tools/mod vet` with a real `Printf` misuse: edit told Build FAILED naming
  `tools/mod`. The test agent's vet failure: still "Tests FAILED".

#### Corrected after review

The first Phase 2 implementation drew one must-fix, one should-fix and a nit on the docs. The table
above describes the first version in two respects; this is what changed. Plan read the corrected
`StageForRole` and the definition's discovery command.

| Finding | What was wrong | What it is now |
|---------|----------------|----------------|
| Must-fix — spawned build workers were missed | `StageForRole` asked `WindowForRole(role)`, and a spawned worker's role is `spawn-<id>`, which resolves to itself. A spawned **build** worker running the prescribed module vet was still a test precheck: test-history row, `testing`, "Tests FAILED" | `StageForRole(session, role, t)` resolves `SpawnBaseRole(session, role)` first, so the stage follows the base role while the original role is kept for attribution and delivery. `TestProcessBashHook_SpawnedWorkerVetFollowsBaseRole`, with a spawned test worker as the control |
| Should-fix — untracked modules were skipped | `git ls-files -- 'go.mod' '*/go.mod'` lists only indexed files, so a newly created `tools/new/go.mod` — the module being written — and every module in a fresh unstaged project were silently not vetted | The discovery command is `git ls-files --cached --others --exclude-standard -- 'go.mod' '*/go.mod'`: tracked plus untracked files, ignored paths left out, `testdata/` and `vendor/` still skipped |
| Nit — plan's wording in `docs/hooks.md` | It said the test agent's vet failure is the only one that reaches edit as a test failure. `StageForRole` re-files the build role alone; `TestStageForRole` pins edit as unchanged | The doc says only the build role (spawned build workers included) is re-filed and every other role keeps the command's classification |

**Carried into Phase 3:** the integration script must cover an **untracked** module — one present
on disk and absent from the index — so the discovery command's `--others` is exercised, not just a
committed one.

#### Consequences worth knowing

- **A build agent that still runs `go vet ./...` at a module-less root still fails, and edit is
  still told** — now as "Build FAILED … check build window" rather than "Tests FAILED". The false
  alarm stops only when the agent follows the new lint instruction; the classification change makes
  the notice truthful about *where* it failed, not silent. The instruction is definition text, so
  whether a live build agent follows it is behaviour that Phase 3's script cannot show with stubs.
- **`go -C` needs Go 1.20 or later.**
- **The rule is one role wide.** Only the build agent's test prechecks are re-filed — a spawned
  build worker included, since the stage follows the base role; a vet run by any other role is
  classified as before.

### Phase 3: Integration test

- [x] Create `scripts/test-root-go-vet.sh` — hermetic: scratch repository with modules under a
  subdirectory, scratch `BUS_SESSION`, payloads fed to the real `hook bash` as the build role
- [x] A clean build cycle → no failing vet row, no notice to edit
- [x] A real vet failure inside a module → reported, naming the module
- [x] An **untracked** module — on disk, absent from the git index — is listed by the lint step's
  discovery command and vetted; an ignored one is not (added 2026-11-13 from the Phase 2 review)
- [x] Negative control: a root-module repository with a vet failure → edit is notified
- [x] Coverage floor; run through the run agent and record the counts here

Run through the run agent on 2026-11-13 20:25 (task `1794619527-spawn-100fae85-94d6ce93`, log
`/tmp/test-root-go-vet.log`): `scripts/test-root-go-vet.sh`, **26 passed, 0 failed, floor 26,
exit 0.** The script reads the discovery command out of `agents/code-builder.md` and runs it, so a
change to the definition's command is what the script exercises.

| Section | Setup | What it shows |
|---------|-------|---------------|
| A | A clean build cycle, two modules under `tools/`, no root module | Discovery lists the two modules and no root; each vets clean; the build passes; no failing vet row in build or test history; the build cycle writes no test-history row; no FAILED notice reaches edit; the passing build still chains to the test agent |
| B | A real vet finding inside one module | That module's vet fails and the clean one still passes; build-history holds the failing row naming `tools/broken`; edit is told "Build FAILED (exit 1): go -C tools/broken vet ./... — check build window"; no test-history row from the build agent, no "Tests FAILED", and the workflow never goes `test-failed` |
| C | Discovery — an untracked module, an ignored one, one under `testdata/` | The untracked module is on disk and absent from the index, and is discovered and vetted; the ignored and `testdata` modules, both carrying a finding, are not discovered and nothing fails |
| D | Negative controls | A root module is discovered as `.`, its vet fails on a real finding and edit is still notified ("Build FAILED … go -C . vet ./…"); the test agent's vet fails on the same finding and is still "Tests FAILED" |

**Hermeticity, corrected after review.** Chain configuration is read from the working directory's
`.muxcode/muxcode.json`, so the first version of the script could have been steered by whatever
directory it was launched from. It now runs every `muxcode` call from its scratch directory. This
was validated from a hostile directory whose config rewrites the FAILED messages — with a proof
step showing a hook run there does pick that config up — and the script still reported 26 passed,
0 failed, floor 26 (run-agent task `1794619721-spawn-100fae85-5ee5fa7b`).

What the script cannot show: it runs the discovery command and the per-module vet **itself**, as a
build agent following the definition would. That a live build agent follows the new lint step
rather than running `go vet ./...` at the root is behaviour, and the script drives no live agent.

**Live observation, read by plan from session `muxcode`'s stores at 20:31.** The session's own build
agent was restarted on the new definition at 20:22:13 (its `startup` row), and the run's next two
build nodes show it following the lint step:

| | Before the new definition | After it |
|---|---|---|
| Last failing `go vet ./...` row in `test-history.jsonl` | 20:18:27 (`ts 1794619107`), exit 1 — the 19th such row of the day | None since |
| The build agent's reply | "root go vet could not run because the repository root is not a Go module" | 20:26:59 and 20:29:46: "Build succeeded: gofmt clean; **go vet passed for tools/muxcode, tools/muxcode-llm-harness, and scripts/fixtures/claude-stub**; ./build.sh completed successfully" |
| Vet rows after the build | A failing test-history row per build | None in either history — a passing precheck writes no row |
| "FAILED … vet" notices from build to edit | Ten, 16:13 to 18:21 | None |

The agent discovered three modules — the two under `tools/` and a fixture module the old step never
reached. Limits: two builds, both graph nodes (whose notices graph ownership would have suppressed
anyway, though the failing *row* would still have been written), one provider, and nothing here
shows an edit-requested build since the change.

## Related

- [MUX-213](../completed/MUX-213-unrequested-test-runs-fire-review-chain-and-reviews-answer-twice.md) —
  where this was found; the other source of false FAILED notices
- [MUX-152](../backlog/MUX-152-test-sh-hides-modules-after-first-failure.md) — `test.sh` and its per-module
  reporting
- [MUX-215](../backlog/MUX-215-non-reproducible-test-sh-failures-and-lost-failing-output.md) — the failures
  that are real but cannot be named

## Status

Complete

Closed out 2026-11-13 by `55-defect-backlog-loop` run `1794608375`, which activated it the same day
(Defects rank 1). All three phases and six acceptance criteria are checked.

- **Cause** — the build agent's lint table has no scope and its definition forbids `cd`, so the
  root was the only place it could run a Go row; and a failing precheck was classified by command,
  not role, so it fired the test chain's failure edge from the build pane
  ([Findings for Phase 1](#findings-for-phase-1)).
- **Fix** — the lint step finds modules and runs `go -C <module dir> vet ./...` per module, nothing
  at a module-less root; and a test precheck run by the build role, spawned build workers included,
  is filed as a build precheck, so a failure reads "Build FAILED … check build window"
  ([Findings and decisions for Phase 2](#findings-and-decisions-for-phase-2)). Both choices are the
  run worker's.
- **Evidence** — `scripts/test-root-go-vet.sh`, 26 of 26, also from a hostile working directory;
  and a live observation: after its restart at 20:22 the session's build agent vetted per module on
  two builds, with no failing vet row and no notice since 20:18.
- **Limits that remain** — the live observation is two graph-dispatched builds on one provider; no
  edit-requested build has been seen since the change. A build agent that ignores the instruction
  and runs `go vet ./...` at a module-less root still fails and still tells edit — as a build
  failure, no longer a test failure.

As filed, 2026-11-13, on the user's request relayed by edit, ranked directly after MUX-213 and
eligible for `55-defect-backlog-loop`: the failing command, its rows and the notices were verified
by plan, and the path from the build pane to the test-chain notice was not traced.
