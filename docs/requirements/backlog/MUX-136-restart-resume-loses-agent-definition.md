# Auto-Restart Resumes an Agent Without Its Definition

A daemon auto-restart brought the `plan` agent back **with no agent definition** — default tools, no
role restrictions, no inbox listener — and nothing said so except a banner inside the pane. The agent
kept its privileged identity (docs owner, sole Atlassian write authority) while losing every
constraint that identity depends on, and ran that way for ~37 minutes.

> **Correction (2026-09-02, Phase 1 item 3).** The trigger was **not** the auto-restart. The banner
> quoted below is emitted only by Claude's *session-resume* agent resolver, and muxcode's launcher
> never passes `--resume`, so the definition-less agent was a bare `claude --resume` typed in the
> pane outside the launcher — [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md)'s shape on
> `plan` rather than `edit`. The H1 and the filename predate that finding and now overstate the
> restart's role. Everything below about the **consequences** of a definition-less privileged agent
> stands unchanged; the **cause** is restated in "Phase 1 finding" below.

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

#### Phase 1 finding (2026-09-02) — why `AgentJSON` was empty

**It was never muxcode's launch.** Evidence from the installed Claude Code 2.1.258 binary (`strings`
of `claude.exe`; resolver `C4`, gate `QYt`):

| # | Evidence | Consequence |
|---|----------|-------------|
| 1 | The banner is emitted **only** by the session-resume agent resolver, gated on `resumedAgentSetting` — the agent name persisted in the *resumed* session — with no explicit main-thread agent. Adjacent strings: `Resumed session had agent "…" but it is no longer available. Using default behavior.` and the tail `To restore it, re-create the agent, or resume with an explicit --agent <name>.` | The banner is a **resume** signature, not a launch one |
| 2 | An explicit `--agent <name>` that fails to resolve at a fresh launch takes a different path (`' not found. Available agents: `) | A launcher failure would not have printed this banner |
| 3 | `muxcode agent launch` never passes `--resume`/`--continue` — none anywhere in Go, scripts, config, `tmux.conf`, or hooks — and at tiers 2/3 already passed `--agent` and `--agents` together. Mode cycling (F1) and `RestartLocalAgent()` both relaunch through it | A launcher-started `planner` resolves as the main-thread agent, so the banner path is **unreachable** from the launcher |
| 4 | With the installed binary, tier 3 (`resolveInstallDir()` → `~/.config/muxcode`) is the **same file** as tier 2; `~/.config/muxcode/agents/` is not on Claude's lookup list at all (it reads `.claude/agents/` and `~/.claude/agents/`) | muxcode's definitions live where Claude does not look, so a resume cannot recover them by name |

**Conclusion.** The definition-less `plan` agent of 2026-09-01 was a **resumed session** — a bare
`claude --resume`/`-c` typed in the plan pane outside the launcher — whose persisted agent name
`planner` could not be resolved from disk. `AgentJSON` was never dropped; the launcher was not in the
loop. The 2026-09-01 lifecycle log has fully rotated (oldest surviving entry is 2026-09-02), so which
hand typed the resume cannot be recovered from muxcode's records.

Independently corroborated: `edit`'s own memory for 2026-09-02 records two further Claude mass exits
that day and notes "muxcode exonerated by logs (every kill/reload path logs, none fired)" and that
**hand-resumed sessions survive** — a separate observer reaching the same launcher-not-in-the-loop
conclusion from different evidence.

What this changes:

- **Defect B's stated mechanism was real code but not the cause.** It is now closed as *hardening*
  rather than as the fix for this incident.
- **Phase 2's detector becomes the primary mitigation,** not a secondary one: a banner signature (or
  a positive capability probe) catches a bare resume regardless of who typed it, which is the only
  control that would have caught what actually happened.
- **MUX-126 is corroborated, not contradicted** — see the retraction in the table below.

### Relationship to existing specs

This is a **third** distinct way the restart story breaks, and it is not covered by either sibling:

| Spec | Path | What is lost | Would it catch this? |
|------|------|--------------|----------------------|
| [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) | `edit`, manual bare `claude --resume` | **all** launch flags | **Yes, partly** — *revised 2026-09-02*: same path (bare resume), different agent. MUX-126 scopes its fix to `edit`, so it would not have covered `plan` as written |
| [`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) | daemon restart, unverified | the agent may not come up at all | No — the agent **did** come up, alive and in-pane; only its restrictions were missing |
| **This spec** | ~~daemon restart~~ **bare `claude --resume` in the `plan` pane** (*revised 2026-09-02*) | the **definition only**, silently | — |

Two consequences worth carrying into the work:

- ~~**MUX-126's premise is contradicted.** Its table asserts daemon restart keeps launch flags in
  full; `plan` on 2026-09-01 shows that path producing a definition-less agent. Whichever spec is
  implemented first must reconcile that table rather than inherit it.~~
  **Retracted 2026-09-02 (Phase 1 item 3).** The incident was a bare resume, not a daemon restart, so
  MUX-126's "daemon restart keeps full launch flags" row is **consistent** with this evidence — the
  daemon path did not produce the downgrade. Do **not** amend that row; re-read the incident instead.
  What MUX-126 *does* need is scope: its fix is written for `edit`, and the same bare resume on
  `plan` is what happened here.
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

- [x] Unit-pin the flag pair: with `AgentName` set and `AgentJSON` empty, assert `BuildExecArgs()`
      does **not** emit a bare `--agent` — the assertion must fail against today's code, since that
      is the exact shape that shipped the downgrade
- [x] Pin that `ResolveAgentFile()` finds `planner` from the user tier while a project-dir-only
      lookup does not — the two resolutions must be shown to disagree, since Claude falls back to
      the latter once the definition is dropped
- [x] Establish **why** `AgentJSON` was empty on this restart (failed resolve, failed JSON build, or
      a resume path reconstructing `LaunchConfig` without it) — the guard above stops the silent
      degrade, but the empty JSON is a separate cause still to be found

**Result (2026-09-02).** Four pins landed, two of them required to fail first:

| Test | Pins | Red run |
|------|------|---------|
| `TestClaudeBuildExecArgs_NoBareAgentFlag` | name-only config emits no `--agent`/`--agents`; inline fallback prompt instead | **FAILED** as required — `emitted --agent at args[0]: [--agent planner]` |
| `TestClaudeConfigureLaunch_NameAndDefinitionPaired` | `ConfigureLaunch` sets name+JSON together at user **and** project tier, neither with no file | **FAILED** as required — `project tier: got AgentName="planner" AgentJSON="" — name-only launch` |
| `TestClaudeBuildExecArgs_AgentFlagsTravelTogether` | `--agent` immediately followed by `--agents`; no launch shape carries `--resume`/`-r`/`--continue`/`-c` | PASS (pin) |
| `TestResolveAgentFile_UserTierInvisibleToClaudeLookup` | `ResolveAgentFile` finds `planner` at tier 2 while Claude's own lookup finds nothing; positive control: a project-local copy is visible to both | PASS (pin) |

Red run 4 PASS / 2 FAIL exit 1 — exactly the two the phase requires. Green after the guard:
`go vet ./bus/` clean, `go test ./bus/` 1862 PASS / 0 FAIL / 1 SKIP.

Item 3's answer is above under "Phase 1 finding" — the empty `AgentJSON` had no muxcode cause,
because the launcher never produced that launch.

**How each fact was established** — the run results are relayed, the code state is not:

| Fact | How established |
|------|-----------------|
| Guard present in `BuildExecArgs` (`hasDefinition := AgentName != "" && AgentJSON != ""`), inline fallback otherwise | **Directly verified** — read `bus/provider_claude.go:66-95` |
| `ConfigureLaunch` builds `--agents` at every resolved tier | **Directly verified** — read `bus/provider_claude.go:26-45` |
| All four pins exist and assert what is claimed, with positive controls | **Directly verified** — read both test bodies |
| `provider_claude.go:77` is the **sole** Claude `--agent` emitter (OpenCode's `--agent <role>` is a different CLI) | **Directly verified** — repo-wide grep, non-test Go + scripts + config |
| No `--resume`/`--continue` on any launch path | **Directly verified** — repo-wide grep |
| `BuildAgentsJSON` forwards only `description` + `prompt`; all 18 shipped definitions carry `description:` only | **Directly verified** — read `bus/launch.go:358-376`, frontmatter scan of `agents/*.md` |
| Red-run and green-run pass/fail counts | **Relayed** by the test agent via `spawn-ea804fff`; not re-run here (plan does not run builds or tests) |
| Claude 2.1.258 binary strings, resolver `C4` / gate `QYt` | **Relayed** by `spawn-ea804fff`; not independently re-derived |

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
- [ ] ~~Reconcile MUX-126's path table, which records daemon restart as preserving full launch flags —
      this incident contradicts it~~ **Revised 2026-09-02:** re-read this incident in
      [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) as a **bare resume on `plan`**, and widen
      that spec's resume-aware fix beyond `edit`. Its daemon-restart row is **consistent** with the
      Phase 1 evidence — leave it alone
- [ ] **Carry the full key set into the agents JSON — this is a regression the Phase 1 guard
      introduced, not a pre-existing gap.** `BuildAgentsJSON()` (`bus/launch.go:358`) forwards only
      `description` + `prompt`. Before Phase 1, tier 1 (`.claude/agents/`) launched **name-only** and
      Claude read the project file itself, honouring its `tools:`/`model:`/`permissionMode:`. Phase 1
      made `ConfigureLaunch` build JSON at *every* tier, and `--agents` **overrides**
      `.claude/agents/` (precedence: managed settings → `--agents` → `.claude/agents/` →
      `~/.claude/agents/` → plugins), so a project-tier definition now has its restrictions
      **stripped by the reduced JSON**. That is acceptance criterion 3 ("never fewer restrictions
      than its definition grants") violated *by the fix itself*, at the one tier the fix newly
      touched. The JSON schema accepts the full set — `tools`, `disallowedTools`, `model`,
      `permissionMode`, `skills`, `memory`, `maxTurns`, `effort`, `background`, `isolation`, `color`,
      `initialPrompt`, plus nested `hooks`/`mcpServers`/`experimental` — so the fix is to populate it.
      Add a pin that a `tools:`-bearing project-tier definition survives into the JSON

> **Provenance (2026-09-02).** Two halves, established differently. *Directly verified here:*
> `ConfigureLaunch` is tier-agnostic — it builds JSON from whatever `ResolveAgentFile` returns,
> tier 1 included (`provider_claude.go:31-45`, `launch.go:333`) — so tier 1 did change from name-only
> to JSON. *Relayed, not verified here:* that `--agents` outranks `.claude/agents/`, and the accepted
> key list; both come from `edit`'s background check, reaching plan as a daemon pane-scrape after
> `edit` went idle without replying. The regression follows only if that precedence holds — confirm it
> before acting. No live impact on the 18 shipped definitions (all `description:`-only, and resolved
> at tiers 2/3); the exposure is a user's own project-tier file.

> **Phase 3 item 1 note (2026-09-02).** The `--agent`/`--agents` binding already landed *with* the
> Phase 1 pins — a red tree cannot pass the phase's commit gate, so pin and guard shipped as one unit.
> The box is deliberately left unchecked here: this pass was scoped to Phase 1, and item 1 should be
> ticked by the verify pass that covers Phase 3 on its own evidence.

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

**In Progress** — filed 2026-09-01 from two live incidents the same afternoon; **Phase 1 complete
2026-09-02**. Still filed under `backlog/`; the move to `drafts/` is a `git mv` and awaits the user.

Phase 1 changed the story. Defect B's mechanism is real code — `--agent <name>` was emitted
unconditionally while `--agents <definition>` was conditional — and it is now **guarded and pinned**.
But it was **not the cause of this incident**: the definition-less `plan` agent was a bare
`claude --resume` typed in the pane, outside the launcher, which muxcode never produces. The banner is
a resume signature, and the launcher passes no resume flag on any path. See "Phase 1 finding" above.

Three consequences:

- **Defect B is now hardening, not a fix.** Acceptance criterion 1 holds by construction, and
  `provider_claude.go` is the sole Claude `--agent` emitter. The guard is real work and worth keeping —
  it closes a degrade path that *could* have produced this — but it would not have prevented what
  happened.
- **Phase 2 is the load-bearing phase.** A banner-signature detector or positive capability probe is
  the only proposed control that catches a bare resume regardless of who typed it. The spec's centre of
  gravity moves from Phase 3 to Phase 2.
- **MUX-126 is corroborated, not contradicted** — the earlier claim to the contrary is struck through
  above. This is MUX-126's shape on `plan` rather than `edit`, so the reconciliation is to *widen its
  scope*, never to amend its daemon-restart row.

Distinctness from the siblings is unchanged and still holds:
[`MUX-008`](./MUX-008-unverified-daemon-auto-restart.md) covers a restart that may not come up at all
and its liveness check would pass a live, responsive, unconstrained agent.
[`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) is now the **near** sibling, not a distant
one — its fix is scoped to `edit`, and this incident is the same failure on a role whose entire safety
model is scope restriction.

~~Open question carried forward: Claude's precedence when `--agents` JSON and a project
`.claude/agents/` file define the same name is **not verified**. It is moot today (the JSON is built
from that same file)…~~

**Answered the same day, and it is the opposite of moot.** `--agents` **outranks** `.claude/agents/`
(managed settings → `--agents` → `.claude/agents/` → `~/.claude/agents/` → plugins). Because Phase 1
made tier 1 emit JSON where it previously sent a bare name, a project-tier definition carrying
`tools:`/`model:`/`permissionMode:` now loses them to the reduced JSON — **acceptance criterion 3 is
violated by the Phase 1 change itself**, at the one tier that change newly touched. No live impact on
the 18 shipped definitions; the exposure is a user's own project-tier file. Recorded as the new Phase 3
item, with its provenance split (the tier-1 behaviour change verified here, the precedence relayed).
This bears directly on the Phase 1 approval gate: it is a fix-now-or-defer call, not a documentation
detail.

Defect A is **not new** and is not owned here: MUX-126 already documents the same signature twice on
2026-08-31. The two occurrences recorded above are its third and fourth, filed to strengthen that
spec's reproduction case. One of them has no surviving evidence (lifecycle rotation), so Phase 4 is
investigation, not a fix.

Scoping note: filed as one spec on request. If the work is split, Phases 1–3 are the standalone unit —
Phase 4 belongs to MUX-126.
