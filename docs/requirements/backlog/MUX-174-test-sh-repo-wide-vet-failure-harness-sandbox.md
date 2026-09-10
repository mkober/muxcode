# `./test.sh` Fails Repo-Wide When the Harness Module's Vet Cannot Resolve a Stdlib Package

**Tracking:** filed 2026-09-10 on the user's explicit request, from evidence gathered in session
`muxcode`. Related: MUX-152 (a `set -e` failure hiding later modules), MUX-153 (the no-socket rule
that put `pipeServer` in the harness in the first place).

`./test.sh` exits 1 partway through the `muxcode-llm-harness` module **when the `test` role runs it**
(that role is on codex); the same script is clean from a non-codex role — see
[Cause](#cause--sandbox-scope-discriminating-experiment-run-2026-09-10-1057):

```
harness/pipeserver_test.go:6:2: package net/http/httptest is not in std
  (/opt/homebrew/Cellar/go/1.26.2/libexec/src/net/http/httptest)
```

Because `test.sh` runs under `set -euo pipefail`, the failure takes the whole script with it. The
repo's own test gate is currently unusable: every verification in this session came from targeted
`cd tools/muxcode && go test ./...` runs delegated by hand, never from `./test.sh`.

## Context

### Observed (2026-09-10 10:14–10:15, session `muxcode`)

| Fact | Value |
|------|-------|
| Failing module | `tools/muxcode-llm-harness` |
| Failing step | `go vet ./...` |
| Toolchain | `go1.26.2 darwin/arm64` |
| `test` role provider | **codex**, `gpt-5.6-luna` (`muxcode config list`) |
| `build` role | compiled fine in the same session, same repo |

### The package is present, and the import is legitimate

Two dead ends are already closed, so the fix should not start by re-checking them:

- **The stdlib source exists.** `/opt/homebrew/Cellar/go/1.26.2/libexec/src/net/http/httptest`
  holds `httptest.go`, `recorder.go`, `server.go` and their tests, mode 644, owned by the invoking
  user. The toolchain is not missing the package.
- **The import is not a leftover to delete.** `harness/pipeserver_test.go:6` imports
  `net/http/httptest` and uses only `httptest.NewRecorder()` (`:52`), which binds no socket. The file
  exists *because* of the module's no-socket rule — its own comment reads "pipeServer is an
  in-process stand-in for `httptest.NewServer` (MUX-153)". Removing the import to clear the error
  would delete the very seam that rule requires.

### Cause — sandbox scope, **discriminating experiment run 2026-09-10 10:57**

`codexWritableRoots(role)` (`bus/provider_codex.go:113–116`) opens:

```go
if role != "build" {
    return nil
}
```

Only a non-nil return causes `BuildExecArgs` to pass `-s workspace-write` plus one `--add-dir` per
external root — the Go caches from `go env` among them. The `test` role runs on codex and therefore
inherits codex's default sandbox with no Go-cache grant, while `build` — which worked — has one.

**The discriminator has since been run, and it resolves in favour of this theory.** Edit dispatched it
deliberately at 10:56 — *"we need a NON-codex provider to run it, because the test agent's codex
sandbox is the suspected cause"*:

| Role | Provider | `./test.sh` | Harness `go vet` |
|------|----------|-------------|------------------|
| `test` (10:14) | **codex** `gpt-5.6-luna` | exit 1 | `package net/http/httptest is not in std` |
| `run` (10:57) | **Claude** (non-codex) | **exit 0, clean** | zero output — no vet errors |

The 10:57 log (`/tmp/test-sh-run.log`, 6818 lines) ends `EXIT_CODE=0` beneath `ok` lines for all six
packages, the harness module among them (`ok muxcode-llm-harness/harness 6.624s` at `:401`), and
contains **no** occurrence of `not in std`. Verified by plan from the file, and independently by
review.

Same machine, same repo, same toolchain, same working tree — only the provider differs. That rules out
the `GOFLAGS`/`GOROOT` anomaly, which would fail everywhere. **What remains open** is sandbox scope
versus a build cache poisoned *on the test agent specifically*; both are per-agent and the experiment
above does not separate them. Clearing that agent's cache and re-running is the cheap next
discriminator, and Phase 1's third item is where the answer goes.

### Why it matters more than one module

`set -e` means the harness failure hides **every module after it**. That is the shape MUX-152 already
documents — a green-looking gate that never ran ~2900 tests. A test gate that cannot be trusted to
fail loudly for the right reason is worse than no gate, because its exit code is still read as a
verdict by the hook chain.

**Scope of the breakage, corrected 10:59.** This spec's opening says the gate "is currently unusable";
the 10:57 run narrows that. The gate is unusable **on the `test` role**, which is precisely the role
the build→test→review chain routes to — so the chain's own test evidence is the part that is
compromised. Run from a non-codex role it is healthy today. Both halves matter: the defect is real and
sits on the default path, *and* a working `./test.sh` result does exist for this session's code.

### Scope boundary

In scope: making `./test.sh` run to completion for all modules, and confirming why the harness vet
fails on the `test` role. Not in scope: the no-socket rule itself (correct, keep), `pipeServer`'s
design, the harness's own tests, or widening any sandbox beyond what the diagnosis justifies.

## Requirements

### Acceptance criteria

- [x] The cause is **established by experiment**, not inferred — the non-codex-role vet run above is
      recorded here with its outcome before any fix lands
- [ ] `./test.sh` runs every module to completion on the `test` role's own provider
- [ ] A failure in one module no longer silently hides the modules after it — the script reports
      which modules ran, which passed and which never executed
- [ ] If the cause is sandbox scope, the grant is the **minimum** that fixes it, and no role gains
      write access it did not need
- [ ] A test or check pins the module list, so a module dropping out of the run is a failure rather
      than a shorter green report — with a negative control proving the check can fail

### Technical approach

Diagnose first. Then, if the sandbox theory survives:

`codexWritableRoots` currently encodes "build is special" as a role comparison. The Go caches are
needed by any role that compiles or vets Go, which is at least `build` and `test`. The narrow change
is to return the Go-cache roots for those roots-needing roles rather than for one named role; the
wider question — whether this should key off a capability ("runs Go tooling") instead of a role name
— is worth deciding once, since the next role to hit this will hit it the same way.

Independently of the cause, `test.sh` should not let one module's failure erase the rest. Collecting
per-module results and reporting a summary at the end (still exiting non-zero) turns a truncated run
into a legible one, and directly addresses the MUX-152 shape.

### Key files

| File | Purpose |
|------|---------|
| `test.sh` | `set -euo pipefail`; the per-module loop that stops early |
| `tools/muxcode/bus/provider_codex.go` | `codexWritableRoots` at `:113`; `BuildExecArgs` sandbox flags |
| `tools/muxcode-llm-harness/harness/pipeserver_test.go` | The failing file; `:6` import, `:52` `NewRecorder` |

## Implementation

### Phase 1: Diagnose

- [x] Run `go vet ./...` on `tools/muxcode-llm-harness` from a non-codex role and record the result
- [x] Run the same from the codex `test` role and record the result
- [ ] Record the discriminating outcome here, and only then choose the fix

### Phase 2: Fix the cause

- [ ] Apply the minimum change the diagnosis supports
- [ ] Add the test that pins it, with a negative control

### Phase 3: Make `test.sh` report truncation

- [ ] Collect per-module outcomes; report ran/passed/never-executed at the end
- [ ] Keep a non-zero exit on any module failure

### Phase 4: Docs

- [ ] `CLAUDE.md` build/test table: note that `./test.sh` covers both modules and how a truncated run
      now presents

### Phase 5: Integration test

- [ ] `scripts/test-test-sh-coverage.sh`: assert every module appears in the run summary, and that a
      forced failure in the first module still reports the second as never-executed rather than
      silently passing
- [ ] Include the negative control: the assertion must fail if the summary stops listing modules
- [ ] Run through the run agent and record the row here

## Notes

**Provenance.** Surfaced while delegating `./test.sh` during MUX-163 Phase 4 verification. It is the
reason no full-suite row exists for this session — a fact that also affected how MUX-163's items could
be verified, and which is recorded in that spec.

## Status

Backlog
