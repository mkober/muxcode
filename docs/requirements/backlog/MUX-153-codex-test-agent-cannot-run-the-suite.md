# A Codex Test Agent Structurally Cannot Run This Repo's Suite

`TestProcessBatch_SimpleResponse` (`tools/muxcode-llm-harness/harness/loop_test.go:23`) binds a
loopback listener via `httptest.NewServer`. Codex restricts network access by default and
`BuildExecArgs` never lifts it, so the listen panics in `newLocalListener`, the harness module fails,
and — through [`MUX-152`](./MUX-152-test-sh-hides-modules-after-first-failure.md) — the bus module
never runs. A test agent on codex **cannot validate this repository**, and nothing says so: the role
launches, accepts requests, and returns nothing usable. Today's green suite exists only because the
work was routed to the unsandboxed run agent.

This is not transient and not a model problem. It is the sandbox policy, and it is the default.

Tracking: _(no GitHub issue yet)_

## Context

### Observed (2026-09-08, session `muxcode`)

| | |
|---|---|
| Launch | `role=test cli=codex` (lifecycle 13:44:15) |
| Results produced by the test agent all session | none — its only replies were echoed TUI status lines ([`MUX-154`](./MUX-154-codex-status-line-closes-tracked-tasks.md)) |
| Force-respond ladder on `test` | all four rungs fired 14:11–14:14 with no response |
| Where the suite actually ran green | the **run** agent (Claude, no sandbox): 2901 pass / 0 fail, exit 0 |

### Mechanism — verified

| Fact | How established |
|------|-----------------|
| The test binds a real socket | `httptest.NewServer(...)`, `harness/loop_test.go:23` |
| Codex network access is restricted by default and controlled separately from the filesystem policy | `CLAUDE.md` Codex sandbox note (corrected 2026-09-08) |
| `codexWritableRoots` returns `nil` for every role except `build` | `bus/provider_codex.go:91-94` |
| So `-s workspace-write` + `--add-dir` are passed for `build` only, and **no flag anywhere lifts network** | `BuildExecArgs`, `:61-64` |
| The failing test was the first exposure, not the only one: **52 `httptest.NewServer` sites across 8 files in both modules** — harness `loop_test` 7, `ollama_test` 9, `trace_test` 3; bus `ollama_test` 12, `atlassian_test` 10, `health_test` 5, `agent_test` 3, `atlassian_attachment_test` 3 | **Verified** — `git grep -c` at `8b5c360`. Edit's correction (15:14) to this spec's first draft, which said option C "fixes this test only" — wrong by a factor of 52 |

The `CLAUDE.md` Codex note covers the **build** role's half of the sandbox story — writes outside the
workspace (`make install` into `~/.local/bin`). This is the **test** role's separate, unsolved half:
network, not filesystem. `workspace-write` does not grant it.

### Why it matters

A test agent that cannot test is worse than no test agent: the role exists in every listing, receives
the chain's `build → test` request, and produces silence — which MUX-154 then converts into "done".
The suite stays green on paper while the role meant to prove it has never run it.

### Relationship

| Spec | Relationship |
|------|--------------|
| [`MUX-042`](../completed/MUX-042-codex-cli-compatibility.md) | Made codex a provider. This is the sandbox half its compatibility claim did not cover |
| [`MUX-152`](./MUX-152-test-sh-hides-modules-after-first-failure.md) | Turns this single failing test into a run where 2900 tests never execute |
| [`MUX-154`](./MUX-154-codex-status-line-closes-tracked-tasks.md) | Turns this agent's silence into a recorded success |

## Requirements

### Acceptance criteria

- [ ] A test agent on codex either runs the whole suite green, or **refuses the role at launch** with a
      message naming the sandbox constraint — never silently accepts work it cannot do
- [ ] The decision — permit loopback network for the test role, exclude test from codex, or make the
      suite socket-free — is recorded in `CLAUDE.md`'s Codex sandbox note
- [ ] Negative control: a codex **review** agent (read-only, no network need) is unaffected
- [ ] Negative control: the test role on claude / opencode runs the suite exactly as today

### Technical approach

Three options were laid out for the user; **the user chose C** (relayed by edit, 2026-09-08 15:14):

| Option | What it does | Cost |
|--------|--------------|------|
| A — permit network for `test` | Extend the `codexWritableRoots`-style role rule so the test role launches with a network-permitting policy (identify the real codex mechanism first: a `--sandbox` level or a config key — verify before choosing) | Widens the sandbox for a role that runs arbitrary test code |
| B — refuse `test` on codex | A provider-capability check at launch: `test` + codex → `launch-refused` naming the constraint (the MUX-136 refusal path exists) | Codex cannot be the test provider at all |
| **C — make the suite socket-free (chosen)** | Replace every `httptest.NewServer` with one shared in-process helper — `newPipeServer(h http.Handler)` (`pipeserver_test.go` in each module) serves the handler through an `http.RoundTripper`, so `Client()` needs no listener and `Close()` is a no-op | **52 sites across 8 files**, not one test (this spec first said "fixes this test only" — wrong by a factor of 52). Every future socket-binding test reintroduces the fault unless the rule is enforced, so a guard test that fails on any new `httptest.NewServer` or `net.Listen` in `*_test.go` outside the helper is part of the option |

With C, the silent-accept state goes because the suite becomes runnable under the sandbox: a Codex
test agent can then validate the repo, and the role no longer accepts work it cannot do.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `BuildExecArgs` (`:47`), `codexWritableRoots` (`:91`) — the only sandbox policy muxcode sets |
| `tools/muxcode-llm-harness/harness/loop_test.go` | `httptest.NewServer` at `:23` — the first of 52 sites across 8 files (see Mechanism) |
| `tools/muxcode/bus/pipeserver_test.go`, `tools/muxcode-llm-harness/harness/pipeserver_test.go` | The shared in-process helper the conversion targets: `newPipeServer`, `Client()`, `Close()`, `roundTripFunc` |
| `tools/muxcode/bus/launch.go` | Launch path — where a refusal would sit (MUX-136 precedent) |
| `CLAUDE.md` | Codex CLI sandbox note — the build half is documented; the test half is not |

## Implementation

### Phase 1: Pin

- [ ] Reproduce under the codex sandbox: run `TestProcessBatch_SimpleResponse` via `codex exec` with
      the default policy → panics in `newLocalListener`
- [ ] Negative control: the same test unsandboxed passes
- [ ] Record the exact codex mechanism (flag or config key) that would permit loopback, or that none
      exists — the option table above depends on it

### Phase 2: Decide and implement

- [x] User chooses A, B or C — **C, socket-free suite** (relayed by edit, 2026-09-08 15:14)
- [ ] Implement C: convert all 52 `httptest.NewServer` sites across 8 files behind the shared
      `newPipeServer` helper — **converted, uncommitted, held open at edit's request until the full
      suite is confirmed green** (plan ticked this at 15:50 on the conversion alone and reverted it at
      15:57; edit's verification run is in flight). Conversion state at 15:45: only the helper's own
      four self-test sites remain (`pipeserver_test.go`, two per module), verified by grep. Production
      seams for the injected client: `OllamaConfig.HTTPClient` (`bus/ollama.go:33`, nil in
      production), `healthHTTPClient` (`bus/health.go:55`), harness `Config.HTTPClient`
      (`harness/config.go:17`). **Half-verified:** the harness module (19 sites) ran green — `go vet`
      + `go test`, rc=0 by sentinel; the bus module (33 sites) has **not** been run — the user
      declined the full-suite run — so that half is unproven until the next suite pass
- [ ] Guard: a test fails on any `httptest.NewServer` / `httptest.NewTLSServer` / `net.Listen` in a
      `*_test.go` outside the helper, so the rule cannot erode one test at a time
- [ ] Suite green under the codex default sandbox — the failure this spec opened with no longer
      reproduces
- [ ] Negative control: codex review agent launch and behaviour unchanged
- [ ] Negative control: test role on claude / opencode unchanged
- [ ] Update `CLAUDE.md`'s Codex sandbox note with the test-role half

### Phase 3: Integration test

- [ ] Create `scripts/test-codex-test-sandbox.sh` (hermetic; skips **with reason** when codex is not
      installed — a coverage floor keeps the skip from reporting green)
- [ ] Test: the full suite under `codex exec` with the default sandbox → green, no
      `newLocalListener` panic (option C makes the "or `launch-refused`" branch unnecessary — the
      suite runs, so a codex test agent can validate the repo)
- [ ] Test: the guard trips on a fixture test that binds a socket, and passes on the real suite
- [ ] Test: negative controls above hold
- [ ] Run the script and verify all checks pass

## Notes

Filed 2026-09-08 by plan from edit's handoff (`/tmp/mux-new-findings-20260908.md`); the socket
site, the `codexWritableRoots` role gate and the launch/force-respond rows were verified from this
role. The default-restricted network claim rests on the corrected `CLAUDE.md` note; Phase 1 pins it.

## Status

**In Progress — 1/19.** Filed 2026-09-08; the user chose **option C** the same afternoon (relayed by
edit at 15:14), and edit converted all 52 `httptest.NewServer` sites behind a shared
`newPipeServer` helper within the hour — uncommitted; the harness half ran green, and the full-suite
verification is in flight at 15:57 with the implementation step **held open until edit confirms
green** (CLAUDE.md now carries the no-socket-binding test rule, per edit). This spec's first draft costed C as
"fixes this test only"; the real count, verified at `HEAD` by `git grep`, is 52 sites in 8 files, and
the spec was corrected the same hour. Still filed under `backlog/` with work begun: **ready to move
to `drafts/` when the user says so** (a filesystem move, user-gated).
