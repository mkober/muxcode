# Demo Mode — Agent Coverage Refresh

Update the `muxcode demo` feature so its scenarios reflect the current agent
roster and the features added since the original demo-mode spec. The demo today
showcases only the `edit → build → test → review → commit` cycle and never
exercises the `plan`, `serve`, `deploy`, `run`, or `watch` windows, nor any of
the newer subsystems (conditional chains, hot reload, spec verification,
research mode, API testing, PR review, diagnostics).

## Context

### Origin
The demo feature was specified in `docs/requirements/completed/MUX-051-demo-mode.md`
(scripted scenarios with bus messages, window switching, and GIF capture). It
shipped with a single built-in scenario and has not been revisited as the agent
roster and chain system grew.

### Current state (as built)
- **CLI**: `muxcode demo <run|list>` — `cmd/demo.go`.
  - `run [SCENARIO] [--speed FACTOR] [--dry-run] [--no-switch]`
  - `list` — prints scenarios with step counts and estimated duration.
- **Engine**: `bus/demo.go`.
  - `DemoStep` actions: `select-window`, `send`, `lock`, `unlock`, `sleep`.
  - `BuiltinScenarios()` returns exactly **one** scenario:
    `build-test-review` (20 steps, ~20s at 1.0x).
  - Each `send` step calls `Send()` **and** `Notify()` against the real target
    role — i.e. it injects live bus traffic.

### Current windows / roles (drift baseline)
Default windows: `plan edit build test serve review deploy run watch commit`.

| Window | Agent file | In any demo? |
|--------|-----------|--------------|
| `plan` | `planner.md` | No |
| `edit` | `code-editor.md` | Yes |
| `build` | `code-builder.md` | Yes |
| `test` | `test-runner.md` | Yes |
| `serve` | `dev-server.md` | No |
| `review` | `code-reviewer.md` | Yes |
| `deploy` | `infra-deployer.md` | No |
| `run` | `command-runner.md` | No |
| `watch` | `log-watcher.md` | No |
| `commit` | `git-manager.md` | Yes |
| `docs` (hosted on plan) | `doc-writer.md` | No |
| `research` (F1 mode) | `code-researcher.md` | No |
| `api` (modal) | `api-tester.md` | No |
| `pr-read` (hosted on commit) | `pr-reader.md` | No |

### Problem statement
The demo no longer represents what muxcode does. Half the windows and most of
the headline features added since the original spec are invisible in any
scenario, so the demo undersells the product and can go stale silently (nothing
fails when an agent or chain is added).

## Requirements

### Acceptance criteria
- [ ] Every default window (`plan`, `serve`, `deploy`, `run`, `watch`) appears
      in at least one built-in scenario.
- [ ] `BuiltinScenarios()` returns multiple scenarios; `muxcode demo list` shows
      all of them with accurate step counts and durations.
- [ ] A scenario demonstrates the `deploy → run → watch` chain (mirroring the
      existing `build → test → review` scenario).
- [ ] At least one scenario demonstrates a newer subsystem (conditional chains,
      hot reload / provider switch, spec verification, research mode, API
      testing, or PR review) — chosen in the design phase.
- [ ] Demo traffic is clearly isolated so a live session's real agents do not
      act on demo messages (decision recorded — see open questions).
- [ ] `muxcode demo run --dry-run` works for every scenario without a tmux
      session and without sending real bus traffic.
- [ ] A `--list`/`list` entry for each scenario includes a one-line description
      and estimated duration at 1.0x.
- [ ] Docs updated: `docs/agent-bus.md` demo section and
      `docs/requirements/completed/MUX-051-demo-mode.md` cross-link.
- [ ] A guard exists so adding a new default window without demo coverage is
      detectable (test or `list` annotation — see design phase).

### Non-goals
- Recording or bundling actual GIF assets (manual, out of scope here).
- A scenario for every feature — pick a representative, high-value set.
- Visual theming changes to the TUI/console.

## Technical approach

The work splits into three concerns: (1) the **step model** may need new action
types to express newer interactions; (2) the **scenario catalog** needs new
entries; (3) **demo-traffic isolation** needs a decision so demos are safe in a
live session.

### 1. Step model (`bus/demo.go`)
Current actions cover window switching, send, lock/unlock, sleep. Newer
interactions may need:
- `notify` — wake an agent without a payload send (status-bar flash).
- `display-message` — show a narrator line in the tmux status bar.
- `mode-switch` — simulate F-key mode cycling (e.g. plan ⇄ research on F1).
- `modal-open` — simulate opening the `api` modal.
- `chain` — a convenience that emits a chain-style `event` send + notify, to
  visualize hook chains (`build→test→review`, `deploy→run→watch`).

The design phase decides which of these are actually needed vs. expressible with
existing `send`/`select-window` steps.

### 2. Scenario catalog
Proposed new built-in scenarios (final set decided in design):
- `deploy-run-watch` — `edit → deploy → run → watch` chain, mirroring the
  existing build scenario.
- `hot-reload` — switch an agent's provider/model via `muxcode reload` and show
  the agent coming back (uses the provider selector concept).
- `spec-verify` — plan agent verifies a spec after the review chain completes.
- `research-handoff` — research agent (F1 mode) finds something and hands off to
  the active F2 agent.
- `full-tour` — a longer scenario touching every window once (satisfies the
  "every window appears" criterion in a single recording).

### 3. Demo-traffic isolation (key risk)
Today a `send` step targets the real role and calls `Notify()`. In a live
session with all agents up and chains wired, this can trigger real builds,
tests, commits, or chain cascades. Options to evaluate in design:
- Route demo sends to a dedicated demo namespace / sink role instead of live
  inboxes.
- Add a `--simulate` flag (default) that prints/animates without `Send()`, with
  `--live` to opt into real traffic.
- Gate demos to require no live agents (refuse on a populated session).

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/demo.go` | `DemoStep`, `DemoScenario`, `RunDemo()`, `BuiltinScenarios()`, scenario builders, `executeStep()` |
| `tools/muxcode/cmd/demo.go` | CLI handler (`run`/`list`, flag parsing) |
| `tools/muxcode/bus/launch.go` | Source of truth for the default window list (drift baseline) |
| `docs/agent-bus.md` | `muxcode demo` CLI reference + scenario table |
| `docs/requirements/completed/MUX-051-demo-mode.md` | Original spec to cross-link |
| `tools/muxcode/bus/demo_test.go` | Unit tests for scenarios/step model (create if absent) |

## Implementation

### Phase 1: Design decisions
- [ ] Decide the final scenario set (which of the proposed scenarios ship).
- [ ] Decide the demo-traffic isolation approach (simulate vs. sink vs. gate).
- [ ] Decide which new `DemoStep` action types are required.
- [ ] Record decisions in this doc's "Open questions" section.

### Phase 2: Step model extension
- [ ] Add the agreed new action type(s) to `DemoStep` / `executeStep()`.
- [ ] Implement demo-traffic isolation per the Phase 1 decision.
- [ ] Update `printStepDetail()` for any new action types (dry-run output).
- [ ] Unit-test new actions in `bus/demo_test.go`.

### Phase 3: Scenario catalog
- [ ] Implement the agreed new scenario builders in `bus/demo.go`.
- [ ] Register them in `BuiltinScenarios()`.
- [ ] Ensure every default window is covered across the catalog.
- [ ] Verify `muxcode demo list` shows all scenarios with correct timing.

### Phase 4: Coverage guard
- [ ] Add a test asserting every default window from `launch.go` appears in at
      least one scenario (fails when a window is added without demo coverage).
- [ ] Or annotate `demo list` output with uncovered windows.

### Phase 5: Docs
- [ ] Update `docs/agent-bus.md` demo section with the new scenarios and any new
      flags.
- [ ] Cross-link from `docs/requirements/completed/MUX-051-demo-mode.md`.

### Phase 6: Integration test
- [ ] Create `scripts/test-demo-coverage.sh` (runs inside a live muxcode
      session).
- [ ] Test: `muxcode demo list` returns >1 scenario and lists each expected name.
- [ ] Test: `muxcode demo run <each scenario> --dry-run` exits 0 and prints the
      expected step count with no real bus traffic.
- [ ] Test: every default window name appears across the dry-run output of the
      catalog (coverage assertion).
- [ ] Test: a live (`--no-switch`) run of one scenario completes without
      triggering real agent chains (isolation assertion).
- [ ] Run the script and verify all checks pass.

## Open questions
- [ ] Isolation: simulate-by-default, dedicated sink role, or refuse-on-live?
- [ ] Should `full-tour` replace per-chain scenarios or complement them?
- [ ] Do hosted/modal roles (`docs`, `research`, `api`, `pr-read`) need explicit
      coverage, or is window coverage sufficient?

## Status

Backlog
