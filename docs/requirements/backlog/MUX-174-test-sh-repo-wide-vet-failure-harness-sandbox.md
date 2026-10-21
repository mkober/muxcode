# `./test.sh` Fails Repo-Wide When the Harness Module's Vet Cannot Resolve a Stdlib Package

**Tracking:** [mkober/muxcode#132](https://github.com/mkober/muxcode/issues/132)

**Provenance:** filed 2026-09-10 on the user's explicit request, from evidence gathered in session
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

### Discriminating outcome (recorded 2026-10-21)

**Resolved by inference: sandbox scope.** No controlled experiment separates it from a poisoned cache: the
`test` agent's cache was never cleared and re-run, and since the 2026-10-06 grant changed that agent's
sandbox, the discriminator can no longer be run under the original conditions. The inference rests on the
grant-and-pass result below — widening the `test` role's write scope to the Go caches was followed by a
full-suite pass, which sandbox scope explains without needing a poisoned entry. A poisoned entry is made
unnecessary, not disproved: the September agents' environments were not captured, and every agent writes
the shared per-user `GOCACHE`, so runs between 10:14 and 10:57 could have populated or repaired an entry.
The `role != "build"` snippet quoted above is the code as it stood on 2026-09-10; `codexWritableRoots` has
since gained a `case "test":` (see the second row).

| Question | Evidence | Outcome |
|----------|----------|---------|
| Does the failure follow the sandbox's write scope? | 2026-10-06: the Codex `test` role failed `go vet` with `package archive/tar is not in std` — a *different* stdlib package, same error, the first no build had cached ([MUX-160](MUX-160-tmp-go-cache-leak-unclearable-pressure.md) Decision 1; recorded in [MUX-202](../completed/MUX-202-self-upgrade-from-the-quick-menu.md) Phase 2). A refused write path would fail on any package not yet cached, which matches the recurrence | **Consistent** — same role, same error, new package |
| Does granting the Go caches fix it? | `codexWritableRoots` now returns `goToolchainRoots()` for `test` (`case "test":`, `bus/provider_codex.go:147`, landed 2026-10-06 as a MUX-202 Phase 2 side fix). That run's test node then returned **success on the full repository suite** (MUX-202 Phase 2 verification, 2026-10-06 16:40, run `1791315389`) | **Yes** |
| Is the grant live on the running agent? | 2026-10-21: `test` is still `codex gpt-5.6-luna`; its `launchsandbox-test.hash` stamp `417232ec…` equals `muxcode agent sandbox-fingerprint test` from the installed binary (the #170 drift watchdog); live argv carries `-s workspace-write --add-dir ~/Library/Caches/go-build --add-dir ~/go/pkg/mod` plus `network_access=true` | **Yes** |

Toolchain note: `GOROOT` is now `/opt/homebrew/Cellar/go/1.27.2/libexec` (the original failure
quoted 1.26.2); the cause is independent of the version, since the `run` agent passed on the same
toolchain the `test` agent failed on.

**Fix chosen.** The minimum change this spec's Technical approach proposed — the Go caches for
`test`, none of build's install roots — is already in the tree (MUX-160 Decision 1, test only by the
user's choice; whether `run` gets it stays open there). It is pinned by
`TestCodexBuildExecArgs_TestGetsGoCachesOnly` (`bus/provider_codex_test.go:202`), which asserts the
two cache roots present *and* the install roots (`BINDIR`, `CONFIGDIR`) absent. Phase 2 therefore
reduces to: confirm `./test.sh` on the `test` role's own provider (this graph run's test node is that
run — acceptance criterion 2), and check the pin's negative control. The role-vs-capability question
("runs Go tooling") was decided by MUX-160 Decision 1 as role-keyed; no further widening is justified
by this diagnosis.

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
- [x] `./test.sh` runs every module to completion on the `test` role's own provider — codex `test`
      agent, 2026-10-21 12:54, exit 0 (spec-to-pr run `1792601405` test node, 65s)
- [x] A failure in one module no longer silently hides the modules after it — the script reports
      which modules ran, which passed and which never executed (Phase 3; failure paths exercised by the
      worker's stub-`go` check through the run agent, 24/24, pending the Phase 5 script that pins them)
- [x] If the cause is sandbox scope, the grant is the **minimum** that fixes it, and no role gains
      write access it did not need — pinned by `TestCodexBuildExecArgs_TestGetsGoCachesOnly` (install
      roots absent) and `TestCodexBuildExecArgs_OtherRolesUnwidened` (`bus/provider_codex_test.go`)
- [x] A test or check pins the module list, so a module dropping out of the run is a failure rather
      than a shorter green report — with a negative control proving the check can fail —
      `scripts/test-test-sh-coverage.sh` sections 1 and 5 (`EXPECTED_MODULES` pin; `summary_lists_all`
      rejects a doctored summary and a dropped-out module that `test.sh` alone exits 0 on)

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
- [x] Record the discriminating outcome here, and only then choose the fix — see [Discriminating outcome](#discriminating-outcome-recorded-2026-10-21)

### Phase 2: Fix the cause

- [x] Apply the minimum change the diagnosis supports — already in the tree: `case "test":` Go-cache
      grant, `bus/provider_codex.go:147` (MUX-160 Decision 1, 2026-10-06); confirmed by the 2026-10-21
      `./test.sh` pass above
- [x] Add the test that pins it, with a negative control — `TestCodexBuildExecArgs_TestGetsGoCachesOnly`
      (`provider_codex_test.go:202`, asserts `BINDIR`/`CONFIGDIR` absent) and
      `TestCodexBuildExecArgs_OtherRolesUnwidened` (`:340`)

### Phase 3: Make `test.sh` report truncation

- [x] Collect per-module outcomes; report ran/passed/never-executed at the end — `test.sh` `=== Summary ===`
      block: one line per module plus `modules= ran= passed= failed= never-executed=` totals
- [x] Keep a non-zero exit on any module failure — exit 1 on any failure or an empty `tools/`, 0 only when
      every module passed; codex `test` agent ran it 2026-10-21 13:43: exit 0, `modules=2 ran=2 passed=2`

### Phase 4: Docs

- [x] `CLAUDE.md` build/test table: note that `./test.sh` covers both modules and how a truncated run
      now presents — row rewritten (`CLAUDE.md:47`): both modules named, the `=== Summary ===` contract
      and exit semantics described

### Phase 5: Integration test

- [x] `scripts/test-test-sh-coverage.sh`: assert every module appears in the run summary, and that a
      forced failure in the first module still reports the second as never-executed rather than
      silently passing — sections 2–4
- [x] Include the negative control: the assertion must fail if the summary stops listing modules — section 5
- [x] Run through the run agent and record the row here — row below, 23/23 at floor 23

| Run | Task | Result |
|-----|------|--------|
| 2026-10-21 14:18, `bash scripts/test-test-sh-coverage.sh` via the run agent | `1792606709-spawn-121e48a1-35ea9b52` | exit 0 — **23 passed, 0 failed (floor 23)** |

What the script covers (`scripts/test-test-sh-coverage.sh`, hermetic — runs the real `./test.sh` in place
with a stub `go` first on PATH, so nothing compiles and the suite stays the test node's job):

- Section 1 — the pinned module list (`EXPECTED_MODULES="muxcode muxcode-llm-harness"`) equals the
  `tools/*/go.mod` set, so a module added or dropped fails until the pin is updated (acceptance
  criterion 5's "pins the module list")
- Section 2 — all passing: exit 0, every pinned module `passed` and listed, the totals line last,
  `go vet` and `go test` invoked once per module
- Sections 3–4 — a forced `go vet` / `go test` failure in the first module: exit 1,
  `failed (go vet|go test)`, the second module `never executed` and not `passed`, only the first
  module's steps invoked
- Section 5 — negative control: the summary assertion rejects a doctored summary missing a module, and
  a run where a module dropped out of discovery (no `go.mod`) — which `test.sh` alone still exits 0 on,
  the shorter green report the pin exists to catch

Module order is taken from the order `test.sh` actually used, not assumed: glob collation is
locale-dependent (`muxcode-llm-harness` sorts before `muxcode` under C collation, since `-` < `/`).

Earlier Phase 3 verification of `test.sh` itself (throwaway `/tmp` script, not kept): run-agent task
`1792604464-spawn-121e48a1-eb56f14b`, 24/24 under macOS `/bin/bash` 3.2.57.

## Notes

**Provenance.** Surfaced while delegating `./test.sh` during MUX-163 Phase 4 verification. It is the
reason no full-suite row exists for this session — a fact that also affected how MUX-163's items could
be verified, and which is recorded in that spec.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-174-test-sh-repo-wide-vet-failure-harness-sandbox | 19m | 2026-10-21 14:21 |

## Status

Complete
