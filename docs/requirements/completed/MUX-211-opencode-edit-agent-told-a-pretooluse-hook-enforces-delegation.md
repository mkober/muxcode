# MUX-211: An OpenCode Edit Agent Is Told a PreToolUse Hook Enforces Its Delegation Rules

**Tracking:** [mkober/muxcode#182](https://github.com/mkober/muxcode/issues/182)

For a provider without hooks, muxcode rewrites sentences of the agent definition that describe hook
behaviour. The edit role's rewrite matches one **whole sentence** of `agents/code-editor.md` — and
that sentence has since been edited, so the match fails silently. An OpenCode edit agent's generated
definition still says a `PreToolUse` hook blocks prohibited commands before execution. No such hook
runs on OpenCode; the replacement that should tell the agent to self-enforce is never applied.
`scripts/test-opencode-edit.sh` catches it: `FAIL hook guard reference removed`.

## Context

### Source and standard of evidence

Filed 2026-11-13 on the user's instruction relayed by edit, from the MUX-208 graph worker's report
(`/tmp/mux208-implement-report.md`), while closing out
[MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md), whose "every
`scripts/test-*.sh` still passes" criterion excludes this script. The two sentences below were read
by plan; the script was not re-run by plan. Code read at `b8bfcab` plus the uncommitted MUX-208 tree.

### Mechanism — verified

| Fact | Where |
|------|-------|
| The edit-role rewrite's key is the exact sentence: "A PreToolUse hook (`muxcode hook guard`) enforces this at the tool level — prohibited commands are blocked before execution. Always delegate on the first attempt." | `tools/muxcode/bus/provider_opencode.go:619` |
| The definition now reads: "… enforces this at the tool level — prohibited commands **AND direct writes to `docs/**/*.md`** are blocked before execution. Always delegate on the first attempt." | `agents/code-editor.md:54` |
| Rewrites are applied with `strings.ReplaceAll(body, old, new)`; a key that matches nothing changes nothing and reports nothing | `provider_opencode.go:630-631` |
| The script greps the generated config for `enforces this at the tool level` and fails when it is still present | `scripts/test-opencode-edit.sh:149-154` |
| The script failed in the 15:38 and 15:53 runs on 2026-11-13, after its unrelated model check was fixed | `/tmp/mux208-rerun-logs/test-opencode-edit.log`, `/tmp/mux208-rerun2-test-opencode-edit.log` |

### Why this is not MUX-208's — verified

`agents/code-editor.md` and `bus/provider_opencode.go` are unmodified in the MUX-208 working tree
(`git status --short`). Which commit changed the definition's sentence was not traced; the doc-write
guard it mentions is `CheckDocFileGuard`.

### Blast radius

- An OpenCode edit agent (`MUXCODE_EDIT_CLI=opencode`) is told enforcement exists that does not, and
  is **not** told to self-enforce delegation — the instruction the rewrite exists to deliver.
- The same failure is latent in every other rewrite in the table (`build`, `test`, and edit's
  second entry): each keys on a whole sentence of a definition file, and any edit to that sentence
  disables it with no signal. Only this one has a script that notices.
- [MUX-201](../backlog/MUX-201-non-hook-prompt-and-generated-body-drift.md) covers generated bodies drifting
  from definitions in general; this is one confirmed instance with a failing test.

## Requirements

### Acceptance criteria

- [x] An OpenCode edit agent's generated definition contains no claim that a PreToolUse hook
  enforces delegation, and does contain the self-enforcement instruction — including for direct
  writes to `docs/**/*.md`
- [x] A rewrite whose key matches nothing in the definition it targets is a **failure**, not a
  no-op: a Go test iterates every rewrite against the shipped definitions and fails on a miss
- [x] Negative control: that test fails when a definition sentence is edited without its rewrite
- [x] `bash scripts/test-opencode-edit.sh` exits 0 with every check passing
- [x] The relationship to MUX-201 is recorded in both specs: fixed here, or folded into it

### Technical approach

The immediate fix is the key at `:619`. The durable one is the test: whole-sentence matching was
chosen deliberately (the comment at `:601-606` explains why substring rules mangle sentences), so
keep it, and pin every key against the definition files it is meant to match so drift fails the
suite instead of an agent's prompt.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_opencode.go:596-632` | The rewrite table and its application |
| `agents/code-editor.md:54` | The sentence that drifted |
| `agents/*.md` | Every definition a rewrite keys on |
| `scripts/test-opencode-edit.sh:147-160` | The check that caught it |

## Implementation

### Phase 1: Pin the drift

- [x] Go test: every key in the rewrite table occurs in the definition file for its role; red on the
  current tree for the edit hook-guard key
- [x] Record which commit changed `agents/code-editor.md:54`, and whether any other key has drifted

### Results for Phase 1

Recorded 2026-11-17 from the worker's request (`spawn-56d56314`); plan looked up the commit and
found the table and both tests in the tree. Boxes are ticked at `verify-spec`, not here.

**The commit.** `agents/code-editor.md:54` drifted in `e33b0e2` (2026-07-01, "Block edit-agent docs/
markdown writes; delegate to plan"), which added "AND direct writes to `docs/**/*.md`" to the
sentence and did not touch the rewrite key. Plan confirmed the commit changes `agents/code-editor.md`
and not `provider_opencode.go`. So an OpenCode edit agent has been told a hook enforces its
delegation since July.

**Other keys.** None has drifted, per the worker:

| Role | Keys | Still occur at |
|------|------|----------------|
| build | 2 | `agents/code-builder.md:34`, `:87` |
| test | 2 | `agents/test-runner.md:29`, `:37` |
| edit | The chain key | `agents/code-editor.md:257` |
| edit | The hook-guard key | **Does not occur** — the defect |

**The seam.** The rewrite table is hoisted to a package variable, `nonHookBodyRewrites`
(`bus/provider_opencode.go`), so a test can iterate it; behaviour is unchanged.

**Tests** (`bus/provider_opencode_test.go`):

- `TestNonHookBodyRewriteKeysMatchShippedDefinitions` — every key must occur in the shipped
  definition for its role. **Red on this tree**, for the edit hook-guard key, until Phase 2 fixes
  the key. Plan read it: it has no skip.
- `TestMissingRewriteKeysReportsAnEditedSentence` — the negative control: an intact copy reports no
  miss, and one edited sentence is reported by role and key, and only that one.

**A flaw in how this phase was written — plan's.** The first step asks for a test that is "red on
the current tree". A red test fails `./test.sh`, and this run's `test` node runs `./test.sh` after
every phase. So the phase, done exactly as written, cannot pass the run's own test step: the suite
goes green only once Phase 2's fix to the key is in. Earlier specs in this loop pinned today's
behaviour with a test that stays green and is inverted later; this one did not.

**What then happened, as that predicts.** The run's `./test.sh` failed at 16:46:20 —
`FAIL bus: TestNonHookBodyRewriteKeysMatchShippedDefinitions`, the red this step asked for, on
record in the test agent's history. The run's fix worker corrected the key at 16:46:47, which is
Phase 2's first step. The tree is no longer red, and has not been since.

Verified and ticked 2026-11-17, Phases 1 and 2 together, once all three had run after the last edit
(16:46:47): `./build.sh` exit 0 at 16:47:45, `./test.sh` exit 0 at 16:49:28, review at 16:50:04 with
0 must-fix, 0 should-fix and 1 nit — this spec still described the tree as red; corrected here. The
review approved "rewrite fix and drift tests". Criteria 2 and 3 are ticked on the two tests, and
criterion 5 on the note in both specs. Criteria 1 and 4 wait for Phase 3, which checks the generated
definition through `scripts/test-opencode-edit.sh`.

### Phase 2: Fix

- [x] Update the edit key and its replacement to cover prohibited commands and doc writes
- [x] Fix any other drifted key Phase 1 finds — **not applicable**: Phase 1 found none
- [x] Record the MUX-201 relationship in both specs

### Results for Phase 2

Recorded and ticked 2026-11-17, in the same verification as Phase 1 — see below for why the two
arrived together. Plan read the key and its replacement in `bus/provider_opencode.go`.

**The key and its replacement.** The edit hook-guard key now carries the definition's whole
sentence, "… prohibited commands AND direct writes to `docs/**/*.md` are blocked before execution
…", and its replacement tells the agent: "Your CLI does not have a PreToolUse hook to enforce this.
The permission system blocks prohibited commands, but you MUST self-enforce delegation rules: never
attempt a prohibited command and never write or edit `docs/**/*.md` yourself — delegate on the first
attempt."

**No other key needed fixing.** Phase 1 found none drifted, so the second step is ticked as not
applicable.

**The relationship to MUX-201: fixed here, not folded in.** This spec corrects its one instance and
adds the test that makes any future miss in the rewrite table fail the suite.
[MUX-201](../backlog/MUX-201-non-hook-prompt-and-generated-body-drift.md)'s own scope — the prompt
carve-out and generated bodies going stale — is untouched and stays open. The same is written into
MUX-201's Related section.


### Phase 3: Integration test

- [x] `bash scripts/test-opencode-edit.sh` through the run agent: every check passes, exit 0; record
  the counts here
- [x] Negative control: with one definition sentence altered in a scratch copy, the Phase 1 test
  fails and names the key
- [x] `bash scripts/test-all.sh` no longer lists `test-opencode-edit` as failed

### Results for Phase 3

Recorded and ticked 2026-11-17 from the worker's report in the `verify-spec` dispatch; plan read the
run agent's replies for all three runs. The phase changed no code.

| Run | Result | Run task |
|-----|--------|----------|
| `bash scripts/test-opencode-edit.sh` against the installed `v0.1.72-1-gb43b5f4-dirty` (built 16:47:41, after the key fix) | Exit 0 — 25 of 25 passed: permissions 4, deny rules 12, the rest across model and definition checks. The same count against a tree build at 16:47:22 | `1794952332-spawn-56d56314-9f8a1e56` |
| Negative control, on a scratch copy of `agents/` and `tools/muxcode` | Exit 0 — 5 of 5: the pin test passes on the intact copy; with `code-editor.md:54` altered it fails with exit 1 and names exactly one key, edit's hook-guard key | `1794952342-spawn-56d56314-2a6fb477` |
| `bash scripts/test-all.sh opencode-edit` | Exit 0 — `test-opencode-edit PASS`; passed 1, failed 0. **The filtered runner, one script** — not a full `test-all.sh` | `1794952363-spawn-56d56314-93afb7c9` |

**What the first criterion rests on.** Two checks, on the two halves of it:

- `scripts/test-opencode-edit.sh:150–158` — the generated config holds no "enforces this at the tool
  level" and does hold "MUST self-enforce delegation rules".
- `TestAdaptBodyForNonHookProvider_Edit` — the adapted body holds "never write or edit
  `docs/**/*.md` yourself". The doc-write clause is asserted by the Go test, not by the script.

Verified once all three had run after the last code edit (16:46:47): `./build.sh` exit 0 at
16:53:22, `./test.sh` exit 0 at 16:53:39, review at 16:54:07 with 0 must-fix, 0 should-fix, 0 nits.

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-201](../backlog/MUX-201-non-hook-prompt-and-generated-body-drift.md) — the general drift between
  definitions and generated bodies
- [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) — role boundaries that rest on
  instructions alone

### Findings at activation (2026-11-17)

Checked against the tree at `b43b5f4`. The defect stands.

| Spec says | Tree today |
|-----------|------------|
| The edit rewrite's key at `provider_opencode.go:619` lacks the words the definition gained | Still so; the key is now at `:611` |
| `agents/code-editor.md:54` reads "… prohibited commands AND direct writes to `docs/**/*.md` are blocked before execution …" | Unchanged, same line |

Plan did not re-run `scripts/test-opencode-edit.sh`.

(That table is the tree at activation. The key was corrected the same afternoon — see
[Results for Phase 2](#results-for-phase-2).)

Nothing here waits on a user decision. The fifth criterion asks only that the relationship to
MUX-201 be written into both specs — "fixed here, or folded into it"; this spec fixes its one
instance and adds the test, and MUX-201, the general case, stays open. Recording that in MUX-201 is
plan's to do when Phase 2 is verified.

## Status

**Complete** — activated and closed 2026-11-17 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 34 then; every better-ranked row was parked — MUX-210 at rank 33 earlier the same day, after its Phase 1). Uncommitted at close-out; the run's ship step commits it. 13/13 — Phases 1–3 complete and all five criteria met, one step as not applicable.

The edit role's hook-guard rewrite again matches the sentence in `agents/code-editor.md`, so an
OpenCode edit agent's generated definition no longer claims a `PreToolUse` hook enforces its
delegation and instead tells it to self-enforce, doc writes included. The mismatch dated from
`e33b0e2` (2026-07-01). A Go test now fails the suite whenever a rewrite key no longer occurs in its
shipped definition, with a negative control, so an edited sentence can no longer disable a rewrite
silently. `scripts/test-opencode-edit.sh` passes 25 of 25.

For a later reader: Phase 1 was written to leave a red test, which failed the run's own test step
until Phase 2's fix was pulled forward — the two phases were verified together.
[MUX-201](../backlog/MUX-201-non-hook-prompt-and-generated-body-drift.md), the general drift between
definitions and generated bodies, is not closed by this and stays open.

Filed 2026-11-13 on the user's instruction relayed by edit. As filed, the commit that introduced the
mismatch was not traced; it is `e33b0e2` (2026-07-01).
