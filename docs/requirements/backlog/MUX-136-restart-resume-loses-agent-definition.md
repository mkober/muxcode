# Auto-Restart Resumes an Agent Without Its Definition

A daemon auto-restart brought the `plan` agent back **with no agent definition** — default tools, no
role restrictions, no inbox listener — and nothing said so except a banner inside the pane. The agent
kept its privileged identity (docs owner, sole Atlassian write authority) while losing every
constraint that identity depends on, and ran that way for ~37 minutes.

Tracking: _(no GitHub issue yet)_

## Context

Two defects observed together on 2026-09-01. They are filed as one spec because the second is only
reachable through the first: the mass exit is what triggers the restart that loses the definition.

### Defect A — mass simultaneous exit of daemon-managed Claude agents (2× in 37 min)

| | |
|---|---|
| Times | 16:42:48 and 17:19:23 (`agent-down` events) |
| Died | `plan`, `run`, `commit`, `auto` — bare shell prompts, simultaneously |
| Survived | every OpenCode agent (`build`/`test`/`review`) and `edit` |

The dying set is exactly **the daemon-managed Claude Code provider agents**, both times.

The second incident sits inside a daemon upgrade window: the chain build ran `./build.sh` →
`muxcode upgrade-daemons`, logged `daemon-upgraded: Restarted daemon for muxcode on new binary`
(new pid 78391) at 17:15:48, and the first `agent-health-fail` sweep after the startup grace fired
at 17:18:53. The first incident has **no** `build.sh` in the preceding ~90 minutes, and the
lifecycle log had already rotated past it (1000-entry cap), so the evidence is gone rather than
absent. **Do not assume a single cause** — reproduce before fixing.

Auto-restart recovered the agents both times (attempts 1–2), which is what makes Defect B the
sharper problem: recovery *appeared* to work.

### Defect B — the restart resumed a Claude agent without its agent definition

From the `plan` pane after the 16:43 restart — quoted as printed, except that the absolute checkout
path is replaced by `<repo>`:

> This session was running agent 'planner', which is no longer available (no agent by that name in
> `<repo>`). Continuing with the default tools and system prompt — **the agent's tool restrictions
> no longer apply.**

`<repo>` stood for the **project directory** — the repo root the agent was launched in. Which
directory it names is the load-bearing detail: it is not any of the three tiers muxcode installs
definitions into.

**The file was never missing.** `planner.md` is present in both `agents/` (repo) and
`~/.config/muxcode/agents/`. What is absent is `.claude/agents/planner.md` — and the project dir
named in the banner is exactly where Claude Code resolves an agent **by name**.

The mechanism is a flag pair that can come apart (`bus/provider_claude.go:71-76`):

```go
if cfg.AgentName != "" {
    args = append(args, "--agent", cfg.AgentName)   // a NAME: "planner"
    if cfg.AgentJSON != "" {
        args = append(args, "--agents", cfg.AgentJSON)  // the DEFINITION — conditional
    }
}
```

`--agent` carries only the name; `--agents` carries the definition that `ResolveAgentFile()`'s 3-tier
lookup (`.claude/agents/` > `~/.config/muxcode/agents/` > repo defaults) produced. **`--agent` is not
gated on `--agents`.** So whenever `AgentJSON` comes back empty — a failed resolve, a failed JSON
build, a resume that reconstructs the config without it — the launch degrades to `--agent planner`
alone. Claude then resolves `planner` against the project dir, finds nothing, and continues with
default tools. The 3-tier lookup is bypassed not because it failed, but because its **output was
dropped while the name survived**.

That asymmetry is the defect: the name is the part that is useless without the definition, and it is
the part that is unconditional.

### Relationship to existing specs

This is a **third** distinct way the restart story breaks, and it is not covered by either sibling:

| Spec | Path | What is lost | Would it catch this? |
|------|------|--------------|----------------------|
| [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) | `edit`, manual bare `claude --resume` | **all** launch flags | No — different path; MUX-126's table records the daemon-restart path as keeping *full* launch flags |
| [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) | daemon restart, unverified | the agent may not come up at all | No — the agent **did** come up, alive and in-pane; only its restrictions were missing |
| **This spec** | daemon restart | the **definition only**, silently | — |

Two consequences worth carrying into the work:

- **MUX-126's premise is contradicted.** Its table asserts daemon restart keeps launch flags in full;
  `plan` on 2026-09-01 shows that path producing a definition-less agent. Whichever spec is
  implemented first must reconcile that table rather than inherit it.
- **MUX-008's verification would pass this agent.** A liveness-based check confirms a process exists
  in the pane; this agent existed, responded, and ran tools. Verifying *restart* is not the same as
  verifying *restoration*, so MUX-008's fix does not subsume this one.

Defect A above is likewise **not new** — MUX-126 already documents the same mass-death signature on
2026-08-31 and again at 13:42:31 that day, same dying set (Claude agents), same survivors (OpenCode,
`edit`). The two occurrences here are the **third and fourth**, and they are recorded to strengthen
MUX-126's reproduction case, not to re-specify it.

#### Blast radius observed live

- A role whose entire safety model is scope restriction (`docs/` only, no git writes, sole Atlassian
  write authority) ran ~37 min with **default tools and no restrictions**.
- The definition is what starts `muxcode inbox --poll --loop`, so no listener ran → no receipts were
  written → `delivery-gap` event at 17:21:45, and a `verify-spec` dispatch burned all three redrives
  and failed `undeliverable: plan never received the dispatch after 3 redrives`
  (run `req-code-pr-9c76e908`, `update-spec` node).
- **The role-less agent still wrote to the repo.** MUX-134's Phase 4 checkboxes appeared on disk at
  17:21:35 — after the dispatch was declared undeliverable, authored by the definition-less instance.
  A later pass had to re-verify all four ticks against the code rather than trust them, and found one
  false attribution. Work produced without the definition is not merely unrestricted, it is
  **unattributable**, and downstream agents cannot tell it apart from verified work.
- Recovery was a manual `muxcode reload plan` (17:22), which relaunched clean.

### Why it matters

A silent downgrade is worse than a failed restart. A failed restart is visible and gets fixed; this
one reported success (`Agent plan restarted successfully`), emitted `agent-recovered`, and left an
unconstrained agent wearing a privileged role's name. Every guarantee the architecture documents for
`plan` — docs-only writes, gated Atlassian authority, no git mutations — was unenforced, and the only
notice was a line of prose inside a pane nobody was reading. Same family as
[`MUX-006`](./MUX-006-diagnose-false-clean-verdict.md): **the system reported health it did not have.**

## Requirements

### Acceptance criteria

- [ ] `--agent` is **never passed without `--agents`** — the name and the definition travel together
      or neither is sent, so a dropped definition can no longer degrade into a name-only launch
- [ ] An agent that cannot resolve its definition **fails loudly**: an alert to `edit` plus a
      lifecycle event, never a silent fallback to default tools
- [ ] A restarted agent never runs with fewer restrictions than its definition grants — if the
      definition cannot be applied, the agent does not come up at all
- [ ] The restart path is verified to restore the inbox listener, so receipts resume and no
      `delivery-gap` follows a "successful" restart
- [ ] `agent-recovered` is emitted only when the agent came back **with** its definition — a
      recovery event must not describe a downgraded agent
- [ ] **Negative control:** a genuinely missing definition file still produces the loud failure, not
      a hang and not a default-tools launch
- [ ] **Negative control:** a normal healthy restart is unchanged — no extra alerts, no added latency
      on the common path
- [ ] Defect A reproduced (or explicitly recorded as not-reproducible) before any fix is attributed
      to it — the two incidents may not share a cause

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_claude.go` | `BuildExecArgs()` (:67-76) — **the defect**: `--agent <name>` is appended unconditionally, `--agents <json>` only `if cfg.AgentJSON != ""` |
| `tools/muxcode/bus/health.go` | `RestartLocalAgent()` (:281) — the restart path; types `muxcode agent launch <role>` into the pane |
| `tools/muxcode/daemon/daemon.go` | Auto-restart block (~:1780-1810), `agent-restarting`/`agent-recovered` events, fail-count reset |
| `tools/muxcode/bus/launch.go` | `ResolveAgentFile()` (:328) — the 3-tier resolution a resume bypasses; `AgentFileName()` `plan`→`planner` |
| `tools/muxcode/bus/agent_health.go` | `IsAgentAlive()`, `FormatAgentHealthAlert()` — liveness probe that passed a definition-less agent |
| `agents/planner.md`, `~/.config/muxcode/agents/planner.md` | Present in both tiers; absent from `.claude/agents/`, which is where a resume looks |

## Implementation

### Phase 1: Pin the downgrade

- [ ] Unit-pin the flag pair: with `AgentName` set and `AgentJSON` empty, assert `BuildExecArgs()`
      does **not** emit a bare `--agent` — the assertion must fail against today's code, since that
      is the exact shape that shipped the downgrade
- [ ] Pin that `ResolveAgentFile()` finds `planner` from the user tier while a project-dir-only
      lookup does not — the two resolutions must be shown to disagree, since Claude falls back to
      the latter once the definition is dropped
- [ ] Establish **why** `AgentJSON` was empty on this restart (failed resolve, failed JSON build, or
      a resume path reconstructing `LaunchConfig` without it) — the guard above stops the silent
      degrade, but the empty JSON is a separate cause still to be found

### Phase 2: Make an unresolvable definition loud

- [ ] Detect a definition-less launch (banner signature and/or a positive capability probe) and
      surface it as an alert to `edit` plus a lifecycle event
- [ ] Decide and record the failure mode: refuse to come up vs. come up quarantined. A privileged
      role must not run unconstrained either way
- [ ] Ensure `agent-recovered` is withheld when the definition did not apply

### Phase 3: Fix the resolution

- [ ] Bind `--agent` to `--agents` so neither can be emitted alone
- [ ] Confirm no remaining path can launch a role by bare name — repo-wide, a **sole-caller** proof
      rather than a spot check
- [ ] Verify the inbox listener returns after a restart (receipts resume, no `delivery-gap`)
- [ ] Reconcile [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md)'s path table, which records
      daemon restart as preserving full launch flags — this incident contradicts it

### Phase 4: Investigate Defect A (corroborates MUX-126)

- [ ] Reproduce the mass exit; test the daemon-upgrade hypothesis directly by running
      `muxcode upgrade-daemons` against a live session and observing the Claude-provider agents
- [ ] Explain why only daemon-managed **Claude** agents die while OpenCode agents and `edit` survive
- [ ] If not reproducible, record that explicitly rather than closing it against the 17:19 incident
- [ ] Raise the lifecycle rotation cap or snapshot on `agent-down`, so the next occurrence is not
      eaten by the 1000-entry limit as the 16:42 one was
- [ ] Fold the findings into [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) rather than
      duplicating them here — this phase exists to add its third and fourth occurrences, not to own
      the mass-death defect

### Phase 5: Integration test

- [ ] Create `scripts/test-restart-definition.sh` — hermetic (scratch bus + tmux session)
- [ ] Kill a Claude agent's process, let the daemon restart it, assert the agent comes back **with**
      its definition (tool restrictions enforced, listener running)
- [ ] Negative control: with the definition unresolvable, assert the loud failure fires and the agent
      does **not** come up unrestricted
- [ ] Negative control: a healthy restart emits no downgrade alert — the detector must not fire on
      the common path
- [ ] Coverage floor set to the achievable maximum so a skipped section cannot report green
- [ ] Run it and record passed/failed/exit code here

## Status

Backlog — filed 2026-09-01 from two live incidents the same afternoon.

Defect B is the reason this spec exists and has a mechanism verified in code: `--agent <name>` is
emitted unconditionally while `--agents <definition>` is conditional, so a dropped definition
degrades into a name-only launch that Claude silently resolves to default tools. It is **distinct
from both siblings** — [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) covers `edit`'s bare
`--resume` losing *all* flags, [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) covers a
restart that may not come up at all, and neither would catch a live, responsive, unconstrained agent.
It also **contradicts MUX-126's path table**, which records daemon restart as flag-preserving.

Defect A is **not new** and is not owned here: MUX-126 already documents the same signature twice on
2026-08-31. The two occurrences recorded above are its third and fourth, filed to strengthen that
spec's reproduction case. One of them has no surviving evidence (lifecycle rotation), so Phase 4 is
investigation, not a fix.

Scoping note: filed as one spec on request. If the work is split, Phases 1–3 are the standalone unit —
Phase 4 belongs to MUX-126.
