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
- [MUX-201](./MUX-201-non-hook-prompt-and-generated-body-drift.md) covers generated bodies drifting
  from definitions in general; this is one confirmed instance with a failing test.

## Requirements

### Acceptance criteria

- [ ] An OpenCode edit agent's generated definition contains no claim that a PreToolUse hook
  enforces delegation, and does contain the self-enforcement instruction — including for direct
  writes to `docs/**/*.md`
- [ ] A rewrite whose key matches nothing in the definition it targets is a **failure**, not a
  no-op: a Go test iterates every rewrite against the shipped definitions and fails on a miss
- [ ] Negative control: that test fails when a definition sentence is edited without its rewrite
- [ ] `bash scripts/test-opencode-edit.sh` exits 0 with every check passing
- [ ] The relationship to MUX-201 is recorded in both specs: fixed here, or folded into it

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

- [ ] Go test: every key in the rewrite table occurs in the definition file for its role; red on the
  current tree for the edit hook-guard key
- [ ] Record which commit changed `agents/code-editor.md:54`, and whether any other key has drifted

### Phase 2: Fix

- [ ] Update the edit key and its replacement to cover prohibited commands and doc writes
- [ ] Fix any other drifted key Phase 1 finds
- [ ] Record the MUX-201 relationship in both specs

### Phase 3: Integration test

- [ ] `bash scripts/test-opencode-edit.sh` through the run agent: every check passes, exit 0; record
  the counts here
- [ ] Negative control: with one definition sentence altered in a scratch copy, the Phase 1 test
  fails and names the key
- [ ] `bash scripts/test-all.sh` no longer lists `test-opencode-edit` as failed

## Related

- [MUX-208](../completed/MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) — the
  close-out that excluded this script
- [MUX-201](./MUX-201-non-hook-prompt-and-generated-body-drift.md) — the general drift between
  definitions and generated bodies
- [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) — role boundaries that rest on
  instructions alone

## Status

Backlog

Filed 2026-11-13 on the user's instruction relayed by edit. The mismatch is verified by reading both
sentences; the commit that introduced it is not traced. Not started.
