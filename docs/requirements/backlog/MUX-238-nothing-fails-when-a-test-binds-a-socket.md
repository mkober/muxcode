# MUX-238: Nothing Fails When a Test Binds a Socket

**Tracking:** [mkober/muxcode#252](https://github.com/mkober/muxcode/issues/252)

**Split from:** [MUX-153](../completed/MUX-153-codex-test-agent-cannot-run-the-suite.md), closed as
overtaken 2026-11-24. This is the one item of it the user kept.

`CLAUDE.md` states the rule: no test may bind a socket — use `newPipeServer`, never
`httptest.NewServer`. Nothing enforces it. MUX-153 converted all 52 `httptest.NewServer` sites on
2026-09-08 and left its guard step open; the rule has held since only because nobody broke it.

## Context

- A sandboxed agent cannot listen. A socket-bound test panics before any assertion, and `test.sh`
  stops at the first failing module, so every later module reads `never executed`.
- As of 2026-11-24 the only files under `tools/` naming `httptest.NewServer` or `net.Listen(` are
  the two helpers, `tools/muxcode/bus/pipeserver_test.go` and
  `tools/muxcode-llm-harness/harness/pipeserver_test.go` (checked by plan with `grep -rl`).
- Codex roles launch with no sandbox by default since 2026-11-02, so a new bind would pass on every
  default agent and fail only under `MUXCODE_CODEX_SANDBOX=workspace-write` — the configuration
  least often run.

## Requirements

### Acceptance criteria

- [ ] A test in each Go module fails when any `*_test.go` file other than the module's
      `pipeserver_test.go` names `httptest.NewServer`, `httptest.NewTLSServer`,
      `httptest.NewUnstartedServer` or `net.Listen`
- [ ] The failure names the file and line, and the helper to use instead
- [ ] Negative control: the guard passes on the tree as it stands
- [ ] Negative control: the guard's own scan is proven on a fixture that binds, so a scan that
      matches nothing cannot pass

### Technical approach

A source scan in a `_test.go` file per module (stdlib only: `go/parser` or a line scan), with an
allowlist of exactly the helper file. Production code is out of scope — `webhook.go` listens by
design.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/pipeserver_test.go` | The allowed helper; the guard lives beside it |
| `tools/muxcode-llm-harness/harness/pipeserver_test.go` | Same, for the harness module |
| `CLAUDE.md` | The rule's row names the guard once it exists |

## Implementation

### Phase 1: The guard

- [ ] Add the scan test to both modules, allowlisting the helper file only
- [ ] Fixture test: a temp directory holding a `_test.go` that calls `httptest.NewServer` is
      reported with file and line
- [ ] Name the guard in `CLAUDE.md`'s "No test may bind a socket" rule

### Phase 2: Integration test

- [ ] `./test.sh` passes with the guard in both modules
- [ ] In a scratch copy of the tree, add one `httptest.NewServer` call to a test file: `./test.sh`
      fails, and the summary's detail lines name the guard test
- [ ] Remove the call in the scratch copy: `./test.sh` passes again

## Status

**Backlog** — 0/10. Filed 2026-11-24 by plan on the user's decision relayed by edit, when MUX-153
closed. Not started.
