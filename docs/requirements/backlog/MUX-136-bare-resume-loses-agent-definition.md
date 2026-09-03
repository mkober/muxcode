# A Bare Resume Loses the Agent Definition

The `plan` agent came back **with no agent definition** — default tools, no role restrictions, no
inbox listener — and nothing said so except a banner inside the pane. The agent kept its privileged
identity (docs owner, sole Atlassian write authority) while losing every constraint that identity
depends on, and ran that way for ~37 minutes.

The cause is a **bare `claude --resume` typed in the pane, outside the launcher** — this spec's shape of
[`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md), on `plan` rather than `edit`. Evidence and
reasoning in "Phase 1 finding" below.

> **Filed under a different name (2026-09-01 → renamed 2026-09-02).** This spec was originally titled
> *"Auto-Restart Resumes an Agent Without Its Definition"* and filed at
> `MUX-136-restart-resume-loses-agent-definition.md`, because the incident was first read as a daemon
> auto-restart. Phase 1 item 3 established otherwise; the title and filename were corrected on the
> user's instruction. The **consequences** described throughout were always accurate — only the cause
> moved. Full evidence in "Phase 1 finding" below.

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

What this changes: **Defect B's stated mechanism was real code but not the cause** — it is now closed
as *hardening* rather than as the fix for this incident. The two knock-on consequences (Phase 2 becomes
the load-bearing phase; MUX-126 is corroborated rather than contradicted) are set out once in
[Status](#status), with the retraction itself in the table below.

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

- [x] `--agent` is **never passed without `--agents`** — the name and the definition travel together
      or neither is sent, so a dropped definition can no longer degrade into a name-only launch
- [x] An agent that cannot resolve its definition **fails loudly**: an alert to `edit` plus a
      lifecycle event, never a silent fallback to default tools
- [ ] An agent never runs with fewer restrictions than its definition grants, **bounded as follows**
      (*reworded 2026-09-02 by user ruling — state the bound rather than close the window*):
      - **Launcher — absolute.** A definition that cannot be applied (resolved at no tier, or
        declaring an uncarriable nested key) is refused **before exec**. The agent does not come up.
      - **Daemon — bounded.** A bare-resumed agent that the launcher never saw runs unconstrained for
        at most **two 30 s sweeps** (`definitionCheckSecs`=30 × `definitionDebounce`=2, so ~60 s to
        flag) plus one reload, then comes back with its definition.
      - **Exception A — `edit` is unbounded by design.** `IsAgentHealthExcluded` roles are *alerted,
        never reloaded*, because MUX-126 documents the bare resume on `edit` as current practice.
      - **Exception B — after the cap, unbounded.** At `definitionReloadCap`=3 (with a 180 s cooldown
        between attempts) the watchdog gives up and alerts; its own message says the agent "is running
        unconstrained". Worst case before give-up is ~60 s + 3 reloads + 2×180 s cooldown, and
        **thereafter indefinite** until a human acts.
- [x] The restart path is verified to restore the inbox listener, so receipts resume and no
      `delivery-gap` follows a "successful" restart
- [x] `agent-recovered` is emitted only when the agent came back **with** its definition — a
      recovery event must not describe a downgraded agent
- [x] **Negative control:** a genuinely missing definition file still produces the loud failure, not
      a hang and not a default-tools launch
- [x] **Negative control:** a normal healthy restart is unchanged — no extra alerts, no added latency
      on the common path
- [x] Defect A reproduced (or explicitly recorded as not-reproducible) before any fix is attributed
      to it — the two incidents may not share a cause

**Criterion 1 checked 2026-09-02** (commit `658c305`): the guard is in `BuildExecArgs`, pinned by
`TestClaudeBuildExecArgs_NoBareAgentFlag`, and `provider_claude.go:77` is the sole Claude `--agent`
emitter. Scope caveat: it constrains what **muxcode passes**. A bare `claude --resume` in a pane —
the thing that actually caused this incident — never goes through `BuildExecArgs` at all, so this
criterion cannot cover it. Phase 2's detector is what covers that.

~~**Criterion 3 is currently going backwards, not forwards.** Phase 1 landed as-is with the reduced
JSON, so at tier 1 a definition now grants *fewer* restrictions than before…~~

**Closed 2026-09-02 in the Phase 2 pass** — the regression window lasted one commit. `BuildAgentsJSON`
now takes the parsed frontmatter and forwards an allowlist of Claude's subagent keys, so a project-tier
`tools:`/`model:`/`permissionMode:` file survives into the JSON that overrides it. Criterion 3 is no
longer regressing; it is now gated on the *refusal* half rather than on the JSON.

**Criteria 2, 5, 6, 7 checked 2026-09-02** against the Phase 2 working tree — `refuseWithoutDefinition`
(`launch.go:876`, lifecycle + event verified in the body), `definitionApplied` gating recovery
(`daemon.go:1745`), and the named pins read on disk including the two positive/negative controls
(`TestRunAgentLaunch_RefusesWithoutDefinition` with `…LaunchesWithDefinition`,
`TestCheckDefinitionlessHealthyAndDisabled`).

**Criterion 3 deliberately left unchecked**, and it is the one genuine judgement call in this pass.
The *launcher* half is met — a definition-less Claude role is refused, not started. The criterion as
written says "**never** runs with fewer restrictions", and the daemon half does not deliver that: a
bare-resumed agent runs unconstrained for up to one 30s sweep plus a two-sighting debounce (~60s, the
cost the Phase 2 decision explicitly accepts) before it is reloaded. That is a *bounded* exposure, not
an eliminated one. For a role whose entire safety model is scope restriction, the difference between
"never" and "for about a minute" is worth a human decision rather than a checkbox — either the
criterion is reworded to state the bound, or the window is closed. Flagged to the user; not resolved
here.

~~**A second, independent reason criterion 3 stays unchecked (added after Phase 3):** nested
frontmatter keys are still not carried…~~ **Closed in the fix-loop pass** — a definition declaring
`hooks:`/`mcpServers:`/`experimental:` is now *refused at launch* rather than launched reduced, which
folds that case into the launcher's absolute half above.

**Status of criterion 3 after the user ruling (2026-09-02).** The ruling — reword to state the bound,
do not close the window — is applied above, and the tick was conditioned on "the code meets the bound
as written". It does for the launcher and for the normal daemon path. It does **not** for the two
exceptions, which the ruling did not mention and which are **unbounded, not merely longer**:

| | Bound | Meets the ruling's ~60s? |
|---|---|---|
| Launcher refusal | absolute — no exec | n/a (stronger) |
| Daemon, normal path | ~60 s + one reload | **Yes** |
| `edit` (health-excluded) | alert only, never reloaded | **No — unbounded** |
| Any role past `definitionReloadCap`=3 | alert only; watchdog states it "is running unconstrained" | **No — unbounded** |

Left **unchecked** pending one clarification: whether the ruling's bound was meant to hold *with* these
two by-design exceptions written into it (in which case the criterion as reworded is met and can be
ticked), or whether an exception-free bound was intended (in which case Exception B in particular is a
gap to close — a privileged role that fails three reloads currently stays up unconstrained
indefinitely). Ticking on the narrower reading would certify a guarantee the system does not make.

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

- [x] Detect a definition-less launch (banner signature and/or a positive capability probe) and
      surface it as an alert to `edit` plus a lifecycle event
- [x] Decide and record the failure mode: refuse to come up vs. come up quarantined. A privileged
      role must not run unconstrained either way
- [x] Ensure `agent-recovered` is withheld when the definition did not apply

#### Decision (item 2, 2026-09-02): refuse at two layers — quarantine rejected

A role's whole safety model *is* its definition. A quarantined-but-running privileged agent is still
unconstrained for whoever types into it, so the definition-less instance is **refused, not isolated**.

| Layer | Mechanism | Loudness |
|-------|-----------|----------|
| **Launcher** — `refuseWithoutDefinition` (`bus/launch.go:876`) | A Claude role whose definition resolves at **no** tier is not launched on the inline fallback: error returned, no exec, no startup message seeded. Roles with no mapped file (`cfg.Agent == ""`) keep the fallback — nothing was promised for them. The pane stays at a shell prompt, so the health sweep reports `agent-down`; capped restarts hit the same refusal, then alert-only | lifecycle `launch-refused` (error) + `agent-definitionless` to edit |
| **Daemon** — `checkDefinitionless` (`daemon/definition_watchdog.go:47`, 30s sweep) | **Positive argv probe first**: `ProbeAgentDefinition` reads `#{pane_pid}` → `ps` → first `claude` descendant → argv must carry `--agent` **and** `--agents`. The startup banner is only a fallback when no process can be attributed, and **never overrides a positive probe** — an agent *discussing this bug* prints the banner text. Two-sighting debounce → same-provider reload in place (recovery, not a provider change), cap 3/role, 180s cooldown, alert-only at the cap. Roles the daemon never restarts (`edit`) are **alerted, not reloaded** | lifecycle `agent-definitionless` / `definition-reload` / `definition-reload-giveup` / `definition-restored` |

**Cost accepted:** a bare-resumed session in a daemon-managed pane loses its resumed context ~60s
later. That is the deliberate trade — an unconstrained privileged agent is the worse outcome, and the
alert names the cause. [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) is where a resume
learns to *carry* the definition; until then this is a blunt instrument by choice. Opt-out:
`MUXCODE_DEFINITION_WATCHDOG_DISABLE=1`.

**Why the probe leads and the banner follows** is the sharpest detail here: banner-matching alone
would flag any pane whose scrollback quotes the banner — including this spec's own reviewers. The
argv probe is positive evidence about the *process*, so it outranks pane text.

**Item 3 — `agent-recovered` withheld.** `checkAgentHealth`'s recovery branch now requires
`definitionApplied(role)` (`daemon/daemon.go:1745`): a Claude role counts as recovered only on
**positive** `DefinitionPresent` evidence. `DefinitionUnknown` (launcher pre-exec, probe failure)
*defers* the announcement rather than asserting recovery — the 2026-09-01 event did exactly that.
Non-Claude providers pass through (no argv contract).

### Phase 3: Fix the resolution

- [x] Bind `--agent` to `--agents` so neither can be emitted alone
- [x] Confirm no remaining path can launch a role by bare name — repo-wide, a **sole-caller** proof
      rather than a spot check
- [x] Verify the inbox listener returns after a restart (receipts resume, no `delivery-gap`)
- [x] ~~Reconcile MUX-126's path table, which records daemon restart as preserving full launch flags —
      this incident contradicts it~~ **Revised 2026-09-02:** re-read this incident in
      [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) as a **bare resume on `plan`**, and widen
      that spec's resume-aware fix beyond `edit`. Its daemon-restart row is **consistent** with the
      Phase 1 evidence — leave it alone
- [x] **Carry the full key set into the agents JSON — this is a regression the Phase 1 guard
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

> **Done early, in the Phase 2 pass (2026-09-02).** `BuildAgentsJSON(name, fm AgentFrontmatter, prompt)`
> (`bus/launch.go:407`) now forwards an allowlist with Claude's own types — `agentJSONKeys`
> (`launch.go:388`): `tools`/`disallowedTools`/`skills` → arrays (comma, inline `[a, b]`, or a YAML
> block list), `maxTurns` → int, `background` → bool, and `model`/`permissionMode`/`memory`/`effort`/
> `isolation`/`color`/`initialPrompt` → strings. Unknown **scalar** keys are dropped deliberately:
> forwarding one risks the whole launch against an unknown schema strictness. Pinned by
> `TestBuildAgentsJSON_CarriesRestrictions` and `TestClaudeConfigureLaunch_ProjectTierRestrictionsSurvive`.
>
> **Nested keys — amended in the fix-loop pass (2026-09-02).** `hooks:`, `mcpServers:` and
> `experimental:` are still **not carried**, but a definition declaring one is now **refused at launch**
> rather than launched with the key stripped. That closes the review must-fix by *refusal*, not by
> forwarding.
>
> *Verified directly here:* the allowlist, its type coercion, and the doc comment — all read in
> `launch.go:388-435`. The regression window was one commit wide (`658c305` → the Phase 2 tree).
>
> **Review false-positive worth remembering:** the reviewer re-reported this item as still open
> *after* it was fixed, because it read the **main checkout at the Phase 1 commit** while the fix sat
> in the spawn worktree awaiting harvest. Same family as the changed-files provenance problem
> [`MUX-007`](../completed/MUX-007-verify-spec-stale-review-refire.md) closed: a reviewer reading a
> different tree than the one under review reports confidently and wrongly. Check *which tree* before
> trusting a re-report.

> **Phase 3 item 1 note (2026-09-02).** The `--agent`/`--agents` binding already landed *with* the
> Phase 1 pins — a red tree cannot pass the phase's commit gate, so pin and guard shipped as one unit.
> The box is deliberately left unchecked here: this pass was scoped to Phase 1, and item 1 should be
> ticked by the verify pass that covers Phase 3 on its own evidence.

#### Phase 3 results (2026-09-02)

Two of the five items were delivered by earlier phases and are **verified, not re-implemented**.

| Item | Outcome |
|------|---------|
| 1 — bind the flags | Landed with Phase 1; nothing changed this pass |
| 2 — sole-caller proof | **New:** `TestClaudeAgentFlagSoleEmitter` (`bus/sole_emitter_test.go`) |
| 3 — listener returns | Verified three ways (below) |
| 4 — MUX-126 reconciliation | **Applied to [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md)** — third path row, scope amendment, occurrences 5 and 6 |
| 5 — full key set | Landed with Phase 2; nothing changed this pass |

**Item 2 is a repo-walking pin, not a spot check.** It resolves the module root from its own file,
walks every non-test `.go` file, and requires the literal `"--agent"` to appear only in an allowlist of
three files with a stated reason each: `bus/provider_claude.go` (**the emitter** — exactly one hit, and
that line must also contain `"--agents"`), `bus/provider_opencode.go` (OpenCode's own CLI), and
`bus/definition.go` (the probe, which reads argv). It then scans `scripts/`, `config/` (minus
`config/nvim`), `agents/`, `skills/` and the root build scripts for `--agent[ =]` and requires zero
hits. **Coverage floors** — the walk must reach the emitter, and the non-Go scan must cover ≥ 20 files —
so a mis-resolved root cannot pass vacuously. That floor is the part worth keeping: a repo-walking test
that silently walks nothing is the classic green-but-empty pass.

**Item 3, three ways.** (a) *By construction* — `TestClaudeLaunchCarriesListenerProtocol`: a
`ResolveLaunchConfig`-built Claude launch carries the bound definition **and** an
`--append-system-prompt` containing `muxcode inbox --poll --loop` (`bus/prompt.go:109`, emitted for
every hook provider); `RestartLocalAgent` relaunches through `muxcode agent launch`, so a daemon restart
always carries it. A bare `claude --resume` carries **neither** — which is precisely why receipts
stopped and `delivery-gap` fired on 2026-09-01. (b) *Kept alive* — the Stop hook
(`bus.DecideStopHook`, pinned by `TestDecideStopHook`) blocks a turn's stop and demands a relaunch
whenever a request is pending with no listener running. (c) *Live evidence* — **zero `delivery-gap`
events in the whole of 2026-09-02**, across two restart waves; receipts resumed after every launcher
restart.

**Strengthened in the fix-loop pass:** review flagged (a) as a mere prompt-string check — asserting the
launch *mentions* the command, not that the command does anything. `TestRestartLaunchRestoresListenerProtocol`
(`bus/listener_restore_test.go`) closes the loop: the launch a **restart** produces carries the bound
definition and instructs exactly `muxcode inbox --poll --loop`, and that command's polling marker
(`SetPolling`) is what the Stop hook's liveness read (`IsPolling || IsWaiting`) consults — so a live
listener is left alone, while with the marker gone and work pending `DecideStopHook` blocks with
`StopHookPollReason` naming the same command. The string and the mechanism are now tied together.
Live-pane verification remains Phase 5's.

> **Timestamp correction (verified here).** The handoff dated the live evidence at 17:40 and 19:16;
> those are **UTC printed as local**, and both were still in the future when the claim was written. The
> machine runs **UTC-4** — provable from the log itself, where the `13:27:25` row records a daemon
> `built 2026-09-02T17:24:49Z`. Real local times: waves at **13:37 / 13:40** and **15:16**. The events
> and the correlation are real; only the clock was wrong. Corrected times are what went into MUX-126.
> `edit` reached the same conclusion independently from the raw `ts` values, our messages crossing.
> The zero-`delivery-gap` claim was checked directly and holds.

**Open decision for the user — nested frontmatter keys.** `hooks:` and `mcpServers:` are still not
carried, so a project-tier definition declaring them launches without them: an AC3 gap at tier 1, for
that shape only (no shipped definition uses nested keys). Two fix shapes:

- **(a) Refuse on an uncarriable key** — `ExtractFrontmatter` records dropped nested keys and the
  launcher refuses. Small, and consistent with the Phase 2 *refuse* decision.
- **(b) Forward nested YAML into the JSON** — needs a YAML-subset parser in a stdlib-only module, and
  re-exposes the `--agents` schema-strictness question the allowlist was built to sidestep.

**Resolved as (a) in the fix-loop pass (2026-09-02) — shape decision: refuse-on-uncarriable-key.**
Consistent with the Phase 2 "refuse, not quarantine" decision and with AC3's "if the definition cannot
be applied, the agent does not come up at all". Implementation, verified in the spawn worktree:
`agentJSONNestedKeys` = `hooks`/`mcpServers`/`experimental`; `AgentFrontmatter.uncarriableKey()`
catches **both** the YAML-block and the inline `{...}` flow-map shape; a new `LaunchConfig.AgentJSONErr`
carries *why* the JSON is empty although the file resolved, so `refuseWithoutDefinition` can emit a
distinct "cannot be applied … refusing to launch with a reduced definition" message instead of the
misleading "resolved at no tier". Nested keys outside that set stay muxcode-side metadata and drop.

Option (b) remains open for the user to choose later; it is a *widening*, not a correction, and it
still depends on the `--agents` schema strictness Phase 5 has not established.

> **Provenance caveat.** That the option was *implemented* is verified. That the **user chose** it is
> **not** something this agent can establish — the choice was recorded as awaiting the user, and the
> next pass arrived with (a) built. Recorded as an implemented shape decision rather than as a
> user ruling.

### Phase 4: Investigate Defect A (corroborates MUX-126)

- [x] Reproduce the mass exit; test the daemon-upgrade hypothesis directly by running
      `muxcode upgrade-daemons` against a live session and observing the Claude-provider agents
      — *hypothesis refuted from the timeline; the proposed live run was deliberately **not** done
      because wave 2's deaths precede the upgrade, so it would buy nothing. The mass exit itself was
      not reproduced — that is recorded under the item below, which exists for exactly this case*
- [x] Explain why only daemon-managed **Claude** agents die while OpenCode agents and `edit` survive
      — *the premise was wrong and is corrected: it is **idle Claude sessions machine-wide**, and the
      discriminator is idle-vs-mid-turn plus binary, not daemon-managed-vs-not*
- [x] If not reproducible, record that explicitly rather than closing it against the 17:19 incident
- [x] Raise the lifecycle rotation cap or snapshot on `agent-down`, so the next occurrence is not
      eaten by the 1000-entry limit as the 16:42 one was — *both: cap 1000 → 5000 **and**
      `SnapshotAgentDown()` at strike 2*
- [x] Fold the findings into [`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) rather than
      duplicating them here — this phase exists to add its third and fourth occurrences, not to own
      the mass-death defect

**Phase 4 was first assessed at 1 of 5 — untouched investigation — while a commit gate was already
armed.** Recorded once, because the lesson outlives the snapshot: *a gate arriving is not evidence that
investigation happened.* The per-item state at that moment is superseded by "Phase 4 results" below.

~~**Incidental evidence that materially advances item 1.** `upgrade-daemons` ran against a live session
nine times… two of seven bursts were followed by a trio health-failure; only one tightly (30 s)…~~

**Superseded the same day — and my measurement was the weaker one.** That correlation used
`agent-health-fail` as the death proxy. Health-fail is a **lagging** indicator: it fires when the
daemon's sweep notices, not when the session exits. Reading the Claude **transcripts** (file mtimes and
last entries) gives the true instant, and for wave 2 it *inverts the causality* — see item 1 below.
Recorded because the lesson generalises: when a detector's timestamp is the only evidence, what you are
measuring is detection latency, not the event.

#### Phase 4 results (2026-09-02) — hypothesis refuted, mechanism still open

All times **UTC** (machine local = UTC−4).

**Item 1 — the daemon-upgrade hypothesis is REFUTED.** No live experiment was needed; the timeline
answers it.

| Wave | Deaths (transcripts stop) | Nearest daemon upgrade | Verdict |
|------|---------------------------|------------------------|---------|
| 1 | **17:35:49.65–.82Z** (6 sessions), again ~17:39:2xZ | 17:27:25Z before, 17:53:41Z after — none within ±8 min; the only build in the window came **after** the first death | not the upgrade |
| 2 | **19:14:16.41–.46Z** (5 sessions) | 19:14:34Z — **18 s after the deaths** (`upgrade-daemons` is the last step of `build.sh`) | not the upgrade |

Wave 2 is decisive: the upgrade cannot have caused deaths that **precede** it. The proposed live
`upgrade-daemons` experiment was therefore **not run**, and should not be — it would buy nothing.

Also refuted: that `make install` kills running muxcode processes. A hermetic test with a copy of the
real Go binary shows the process **survives** `install` over its path (new inode), and live `muxcode
console` processes predate the install. **A false positive worth not repeating:** a first hermetic test
using a copied `/bin/sleep` *did* die — that is macOS launch constraints on a copied platform binary,
not replacement semantics. The `claude` binary itself was unchanged (`autoUpdates: false`), and no
muxcode `pkill` pattern matches a Claude argv.

**Item 2 — the selectivity was mis-framed.** It is not "daemon-managed Claude agents":

- **Every idle Claude Code session on the machine died in the same ~200 ms window**, both waves —
  three tmux sessions, three repos (muxcode, is-advising-gateway, is-operations-gateway), one instant.
- **Survivors:** every OpenCode agent (a different binary), and Claude sessions that were **mid-turn**.
- **The exits are Claude-initiated clean shutdowns, not external kills.** Each dead transcript ends
  with Claude's own shutdown bookkeeping (a `cost-state` write, or its "background command … was
  stopped" notice). No crash reports, no unified-log signal events, no muxcode kills.

So the real signature is **idle Claude sessions vs everything else**, machine-wide — which is why no
per-session or muxcode-side cause was ever going to explain it.

**Item 3 — the mechanism is explicitly NOT reproduced.** What is bounded: a machine-wide, same-instant
broadcast inside Claude Code that makes *idle* sessions exit cleanly while mid-turn sessions defer.
Two candidates, neither confirmable from outside: a **bridge-side teardown/reconnect** (every session
here is bridge-attached) is strongest; a **SIGWINCH broadcast** from the `client-resized` hook's
`muxcode resize` (which refits every window in every session) is the other. `muxcode resize` logs
nothing and Claude's debug dir is empty, so discriminating them needs a deliberate experiment — one
scratch Claude session exposed in turn to `muxcode resize`, a settings touch, and a bridge disconnect.
That costs a live Claude session, so it is **the user's call**, not something to run unasked.

**Item 4 — evidence no longer rots.** The lifecycle log rotated past **both** waves within ~2 h, and
wave 1 was gone before Phase 3 finished reading it. Now: `bus/snapshot.go`'s `SnapshotAgentDown()`
writes a bundle (`lifecycle.log`, `pane.txt` 300 lines — where Claude's exit banner lives — and
`procs.txt`) under `logs/snapshots/`, bounded at 20 per session, capture failures recorded inside the
bundle rather than fatal. The daemon takes it at **strike 2** — after `agent-down` fires, *before*
strike 3's relaunch types over the pane. `MUXCODE_LIFECYCLE_LOG_MAX` default raised **1000 → 5000**.
Verified on disk: `snapshot.go:46`, `daemon.go:152/261/1779` (injectable seam + `agent-down-snapshot`
event), `lifecycle.go:67`. The daemon suite now pins `MUXCODE_LIFECYCLE_LOG_DIR` to a temp dir, so
tests can no longer touch the real logs.

**What Phase 4 still owes:** item 3's mechanism, which is outside muxcode's control and awaits the
user's decision on the discriminating experiment. Item 1 is answered in the negative and item 2 is
answered by reframing; neither needs more work.

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

**Carried in from Phase 2 as explicitly not-verified-live** — the unit tests inject `ps`/tmux output,
so these three are only ever exercised here:

- [ ] The real `ProbeAgentDefinition` against a **live pane** — the unit pins inject `ps` and tmux
      output, so the probe has never run against a real process tree
- [ ] `ps -axo command=` on **Linux** (procps accepts `command` as an alias of `args`; only macOS has
      been exercised)
- [ ] Claude's `--agents` schema strictness on **unknown keys** — the allowlist sidesteps it rather
      than establishing it, so the assumption behind "drop unknown keys" is untested

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-136-restart-resume-loses-agent-definition | 32m | 2026-09-02 17:22 |

## Status

**In Progress** — filed 2026-09-01 from two live incidents the same afternoon; **Phase 1 complete and
committed 2026-09-02** (`658c305`, 4 files). Still filed under `backlog/`; the move to `drafts/` is a
`git mv` and awaits the user.

Phase 1 landed **as-is**, carrying a one-commit tier-1 restriction loss; the **Phase 2 pass closed it
early** by teaching `BuildAgentsJSON` the full key allowlist. The regression therefore existed between
`658c305` and the Phase 2 tree and no longer does. (For the record, and because it stopped mattering
quickly: whether the defer was considered or the fix-now question simply went unanswered was never
determinable from here.)

**Phase 3 complete 2026-09-02** — items 1 and 5 verified from earlier phases, item 2 added the
repo-walking sole-emitter pin, item 3 verified the listener three ways, and item 4 was applied to
[`MUX-126`](./MUX-126-edit-resume-aware-auto-restart.md) (third path row, scope amendment beyond `edit`,
occurrences 5 and 6). **Phases 1–3 are now done; Phase 4 (Defect A) and Phase 5 (integration test)
remain**, and Phase 4 got harder rather than easier — see below.

**Phase 2 work is complete in the working tree, uncommitted** — `bus/definition.go`,
`daemon/definition_watchdog.go` and their tests are new; `launch.go`, `provider_claude.go`,
`daemon.go`, `diagnose.go`, `guard.go` modified. The decision recorded above is **refuse, not
quarantine**, at launcher and daemon, with a positive argv probe leading and the banner as fallback.

Where the risk now sits, in order:

1. **Nothing has run against a live pane.** Every probe pin injects `ps`/tmux output. The watchdog
   reloads agents on a 30s sweep, so a probe that misreads a real process tree would cycle healthy
   agents — the failure mode is noisy and self-inflicted rather than silent, but it is Phase 5 that
   establishes it does not happen.
2. **Linux is unexercised** (`ps -axo command=`), and the daemon runs wherever the user runs it.
3. **The "drop unknown keys" choice is unvalidated** — it sidesteps Claude's schema strictness rather
   than establishing it.

Criteria 1, 2, 4, 5, 6 and 7 are checked. **Criterion 3 remains open on two independent counts** — the
~60s detection window and the uncarried nested keys — and **criterion 8 belongs to Phase 4**.

**Phase 4 is now a harder problem than when it was written.** The two occurrences added to MUX-126
weaken rather than strengthen the daemon-upgrade hypothesis: across 2026-09-02, only **two of seven**
upgrade bursts were followed by an incident, and only one tightly (30s); four bursts, including two
consecutive late in the day, produced nothing. Phase 4's "run `upgrade-daemons` against a live session"
step should therefore be sized as a reproduction **expected to fail most of the time** — a single clean
run proves nothing. Its sibling instruction to raise the lifecycle rotation cap is now the more
valuable half.

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
