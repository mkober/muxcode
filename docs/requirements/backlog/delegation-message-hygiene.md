# Agent-Freeze Auto-Recovery & Delegation Hygiene

**Primary goal:** muxcode must automatically detect and recover a **frozen agent
process** — with **no user intervention**. The daemon should watch agent health,
notice when an agent is wedged/unresponsive with an undeliverable inbox, and
kill-and-respawn it on its own. Today this requires a human to diagnose the hang,
hunt the PID, and `kill` it by hand; that must become an automatic watchdog.

**Secondary goal:** keep agents deliverable in the first place (never let a
blocking foreground `--watch`/`tail -f` wedge an agent active) and keep
delegations short, single-line, and intent-level.

Driven by a real-session incident where the commit agent silently stopped
receiving messages for ~8.5 minutes and only a manual OS `kill` recovered it.

## Context

### Origin — observed incident
In the `is-service-providers-gateway` subsession the commit agent appeared
"stuck." `muxcode diagnose commit` reported **`active-with-stale-messages`** — an
unnotified message sitting in its inbox, `IsAgentIdle: false`. Pane inspection
revealed the actual root causes, in order of impact:

1. **PRIMARY — agent wedged in active state.** A "watch the PR checks to
   completion" request had run `gh pr checks --watch` in the commit agent's pane.
   The pane reported **"1 shell still running"** and stayed **active**. The daemon
   only delivers/notifies **idle** agents (`checkIdleAgents()`), so the agent
   never consumed its inbox — 7 build/test/review auto-chain messages piled up
   unnotified for ~8.5 min. `diagnose.go` flags this as `active-with-stale-messages`
   but offers no auto-remediation.
2. **The "shell still running" was stale.** On inspection **no live `gh` process
   existed** — the "1 shell still running" indicator was a stale/zombie state, not
   a real running command. So "find and kill the hung process" was a dead end; the
   pane was wedged active without an underlying process to kill.
3. **Genuinely frozen TUI — every keystroke ignored.** Queued text (`watch PR #188
   checks…`) sat unsent in the input box. `Escape`, `Ctrl-U`, **and** `Ctrl-C`
   sent via `tmux send-keys` all had **zero effect** — a static display, i.e.
   hung, not merely busy.
4. **Every muxcode recovery path failed (they're keystroke/marker-based).**
   - `agent-health --start` only cleared the stopped marker; `--stop`/`--start`
     just toggles a marker file (`AgentStoppedPath`) and never touches the process.
   - The daemon only auto-restarts a **dead** process. A frozen-but-alive agent
     still passes `IsAgentAlive` (the `claude` CLI is running), so the daemon never
     respawned it.
   - `muxcode reload` / `GracefulStop()` recover via `tmux send-keys` (Escape →
     `/exit` → C-c fallback → a second C-c "force kill"); on a frozen pane none of
     these land either. `RestartLocalAgent()` likewise sends C-c.
5. **Resolution: kill the OS process.** The fix was to find the agent PID
   (`claude --agent git-manager`, PID 53407) and `kill -TERM` it (escalating to
   `-KILL` if needed). The now-**dead** process tripped the daemon's auto-restart,
   which respawned a fresh agent. The message survived the kill (inbox is on disk).
6. **Post-restart wake gap — respawn alone wasn't enough.** After respawn the fresh
   agent sat **idle but never ran its startup inbox check**, and the daemon's notify
   didn't fire — so the pending commit request still sat unprocessed until a
   **manual wake**. End-to-end recovery must guarantee the respawned agent actually
   *consumes* its inbox, not merely that it restarted.
7. **Gap exposed:** there is **no single muxcode command** that force-terminates a
   hung-but-alive agent, and **no guarantee a respawned agent drains its inbox** —
   stop/restart paths are keystroke- or marker-based and the startup wake is
   unreliable.
8. **Secondary — an over-specified delegation.** Separately, a commit delegation
   crammed a 4-file list **plus** the full commit message body into one
   `muxcode send commit commit "…"` string (~580 chars). The bus had warned twice
   (`payload is N chars (>500)`), but the warnings are non-blocking and were
   ignored. A long / multi-line payload risks the `Bash(muxcode *)` permission
   glob missing, turning a clean delegation into a permission prompt.

The fix has three parts: **(1)** never run blocking/long-running commands in an
agent's interactive pane — route watches to the **watch** agent or a background
`muxcode proc`; **(2)** give `active-with-stale-messages` an automatic recovery
path that escalates to an **OS process kill** (a `muxcode kill`/`reload
--force-kill` command), since marker toggles, `IsAgentAlive`, and send-keys
recovery all fail on a frozen-but-alive agent; and **(3)** make delegations terse
intents and let the receiving agent compose the details:

```bash
# Watch-to-completion → background proc / watch agent, not a foreground --watch
muxcode proc start "gh pr checks 188 --watch" --name pr188-checks

# Delegation → short intent; commit agent stages tracked files + writes the message
muxcode send commit commit "Commit the canvas-api/stream fixes for PR #188 (exclude the untracked doc) and push; report new HEAD." --force --track
```

### Current state (as built)

| Concern | Mechanism | Where |
|---------|-----------|-------|
| Idle-gated delivery | Daemon `checkIdleAgents()` delivers/notifies **only idle** agents; an active pane never gets its inbox — messages pile up | `watcher/`, `bus/notify.go` |
| Stuck-active detection | `diagnose.go` flags `active-with-stale-messages` when `IsAgentIdle: false` + unconsumed inbox; remediation note only ("check pane for prompts/errors") — **no auto-recovery** | `bus/diagnose.go` L728-763 |
| Restart recovery | `StopAgent()`/`StartAgent()` relaunch; on startup the agent re-checks its inbox | `bus/agent_health.go`, `bus/reload.go` |
| Background execution | `muxcode proc` runs long-lived commands detached from any agent pane | `bus/proc.go`, `docs/agent-bus.md` |
| Payload warnings | `validatePayload()` warns on newlines and `>500` chars — **non-blocking stderr** | `tools/muxcode/cmd/send.go` L497-507 |
| Newline risk | Warning text: "payload contains newlines — this may break allowedTools glob matching" | `cmd/send.go` L500-501 |
| Length warning | "payload is N chars (>500) — consider using shorter messages" | `cmd/send.go` L503-504 |
| Blocking delegation | `--wait` polls sender inbox every 500ms until reply/timeout (`MUXCODE_INBOX_POLL_TIMEOUT`, default 600s) | `docs/agent-bus.md`, edit inbox constraint |
| Non-blocking delegation | `--track` creates a tracked task and returns immediately | `cmd/send.go`, daemon |
| Guidance (already exists) | Agent defs say "sends MUST be short, single-line, no newlines"; memory note "Default to --track not --wait" | agent `.md` files, `MEMORY.md` |

The detection already exists, but recovery and prevention do not. The gaps are
**(a)** `active-with-stale-messages` has **no automatic recovery** — a human must
diagnose, find the PID, and `kill` by hand, because every built-in path
(marker toggle, `IsAgentAlive`, send-keys restart) is useless on a frozen-but-alive
agent; **(b)** nothing stops an agent running a blocking foreground command that
wedges it active in the first place; **(c)** payload warnings are ignorable;
**(d)** `--wait` is the de-facto delegation default and gives no signal about what
it's blocked on, so it reads as "stuck."

### Problem statement
muxcode must **manage agent liveness automatically** — the daemon already monitors
health, so it should also detect a frozen/unresponsive agent and kill-and-respawn
it **without user intervention**. Today a wedged agent silently stops consuming its
inbox and work backs up invisibly until a human notices and manually kills the
process. That manual loop must disappear. Secondarily, agents should not wedge
themselves (no blocking foreground commands), and delegations should be terse
intents, not bloated multi-line payloads that risk the permission glob.

## Requirements

### Acceptance criteria

**Automatic freeze recovery (primary — no user intervention)**
- [ ] The daemon **automatically detects a frozen agent** (unresponsive +
      undeliverable inbox past a threshold) and **recovers it with no human
      action** — no manual diagnose, PID hunt, or `kill`.
- [ ] Recovery escalates correctly: gentle keystroke nudge → if still unresponsive,
      **OS process kill (TERM→KILL) + respawn**. The pending inbox persists on disk
      and is re-read on startup, so no messages are lost.
- [ ] **Recovery is verified end-to-end, not just "process respawned."** After
      respawn the daemon confirms the fresh agent reaches its prompt and **actually
      consumes its pending inbox** — closing the post-restart wake gap where a
      respawned agent sits idle without running its startup inbox check and the
      notify never fires. If the inbox isn't drained within a window, the daemon
      re-wakes it.
- [ ] Auto-restart no longer assumes "alive == healthy": a frozen-but-alive agent
      (passes `IsAgentAlive` but unresponsive with a stale inbox) is detected and
      recovered rather than left wedged indefinitely.
- [ ] The watchdog **does not kill healthy-but-busy agents** (e.g. a long
      "Cogitating" turn still making progress) — false-positive avoidance is a
      first-class requirement (signal design in Phase 1).
- [ ] A **force-kill primitive** (`muxcode kill <role>` / `reload --force-kill`)
      terminates the agent's OS process so respawn occurs; the daemon uses it
      internally and it is also available manually as a fallback. It is the one path
      that works on a frozen TUI, where send-keys (`reload`, `GracefulStop`,
      `RestartLocalAgent`) and marker toggles (`agent-health --stop/--start`) fail
      and `IsAgentAlive` still reports alive.
- [ ] The "1 shell still running" indicator is reconciled against **actual**
      running processes so a stale/zombie indicator doesn't mask a deliverable
      agent (diagnosis distinguishes a real hung process from a stale flag).
- [ ] `muxcode diagnose` remediation text for `active-with-stale-messages` reflects
      the automatic recovery + force-kill path, not the (ineffective) send-keys restart.

**Deliverability & delegation hygiene (secondary)**
- [ ] Agents never run blocking/never-exiting commands (`--watch`, `tail -f`, log
      follows, interactive watchers) in their own interactive pane — such work is
      routed to the **watch** agent or a background `muxcode proc`. Enforced by
      guidance and, where feasible, a guard/lint on known offenders.
- [ ] Sending a payload with newlines or `>500` chars produces a **prominent,
      hard-to-miss** warning (not a quiet stderr line) — exact escalation decided
      in design (louder warning, opt-in reject, or auto-fix).
- [ ] The **file-handoff pattern** is documented as the standard way to delegate
      long/structured content: write it to a scratch file (`/tmp/<name>.md`,
      self-describing) and send a short message referencing the file. Works today
      with no new tooling; the bus payload stays one short line.
- [ ] A first-class `--payload-file` / stdin path **formalizes** the file handoff
      so the content is passed by reference, never inlined as newlines.
- [ ] Delegations default to **non-blocking** (`--track`) per existing guidance;
      `--wait` remains available when the result is needed before proceeding.
- [ ] `--wait` shows a **periodic progress line** naming what it is waiting on
      (target role + elapsed) so a healthy poll is not mistaken for a hang.
- [ ] Agent definitions (edit + delegating roles) instruct: **delegate intent,
      not pre-baked artifacts** — e.g. don't hand the commit agent a full commit
      message; describe what to commit and let it compose.
- [ ] The bus continues to function unchanged for compliant short, single-line
      payloads (no regression for the common case).
- [ ] Docs updated: `docs/agent-bus.md` (`muxcode send` hygiene section) and
      `CLAUDE.md` (delegation convention).

### Non-goals
- Rewriting the `--wait`/`--track` polling mechanism itself.
- Hard-blocking all long payloads unconditionally (some legitimate messages are
  long — provide an escape hatch instead).
- Changing commit-agent behavior beyond receiving terser intents.

## Technical approach

Two problem families. **Liveness** (the primary incident): keep agents
deliverable and auto-recover wedged-active ones — threads (1)–(2). **Message
hygiene**: terse, clean delegations — threads (3)–(5).

### 0a. Keep agents deliverable (no blocking foreground commands)
- Guidance: long-running/never-exiting commands (`--watch`, `tail -f`, log
  follows) belong to the **watch** agent or a background `muxcode proc`, never the
  agent's interactive pane.
- Where feasible, a guard/lint flags known offenders (e.g. a `--watch` flag or
  `tail -f` run directly in a non-watch agent pane) and suggests `muxcode proc` /
  the watch agent.

### 0b. Auto-recover `active-with-stale-messages`
- The daemon already knows an agent is active with an unconsumed inbox. Add a
  recovery path: past a threshold (e.g. inbox age > N min while active), attempt a
  gentle keystroke clear; if the agent doesn't return to idle, **escalate to an OS
  process kill + respawn** (not marker toggles or send-keys). The on-disk inbox is
  re-read on startup, so the pending message survives.
- **Why a hard kill is required.** Today every recovery path fails on a frozen
  TUI: `StopAgent`/`StartAgent` only write/remove a marker (`AgentStoppedPath`) and
  never touch the process; `GracefulStop()` / `RestartLocalAgent()` recover via
  send-keys (Escape → `/exit` → C-c) which a frozen pane ignores; and the daemon's
  auto-restart fires only on a **dead** process, while a frozen agent still passes
  `IsAgentAlive`. So recovery must terminate the OS process (TERM→KILL) and let
  auto-restart respawn — exposed as a `muxcode kill <role>` / `reload --force-kill`
  command and used by the automatic path.
- Reconcile the "shell still running" indicator against real processes so a stale
  flag is not treated as a live hang (avoid the dead-end "find the child process to
  kill" path — the thing to kill is the agent CLI process itself).
- **Verify the respawn drained the inbox.** Respawn is not the finish line: after
  the fresh agent reaches its prompt, confirm it consumed the pending inbox. If the
  startup inbox check / notify didn't fire (the post-restart wake gap), the daemon
  re-issues a wake and re-checks until the inbox is drained or it gives up and
  alerts. Recovery succeeds only when the work is actually picked up.

### 1. Payload guardrails & file handoff (`cmd/send.go`)
`validatePayload()` already detects both problems. Options for design:
- Escalate the existing warnings (color/prefix, repeat on send) so they are not
  ignored.
- **File handoff (already usable, document as standard).** For long/structured
  delegations, the proven pattern is: write the content to a scratch file
  (`/tmp/<descriptive-name>.md`) that is self-describing (IDs, bodies, per-item
  instructions), then send a one-line message pointing the agent at the file (e.g.
  "Read /tmp/pr188-comment-replies.md and post each entry's body as a reply to its
  PR #188 review comment id; report how many posted"). The bus stays clean; no
  tooling needed. Real example: 12 PR-comment replies handed off via one file +
  one short `pr-read` message instead of 12 bus messages.
- **Formalize with `--payload-file <path>` / stdin (`-`)** so long or multi-line
  content is passed by reference, never inline — the structured version of the file
  handoff, sidestepping the glob/newline risk entirely.
- Optional strict mode (`MUXCODE_SEND_STRICT=1`) that rejects newline/over-length
  payloads instead of warning.

### 2. Delegation defaults
- Treat `--track` as the recommended default for delegations in guidance.
- Consider a config/env (`MUXCODE_DELEGATE_DEFAULT=track`) so omitting both flags
  defaults to track rather than fire-and-forget-no-tracking. Design decides
  whether to change the default or only document it.

### 3. `--wait` progress indicator
- While polling, emit a heartbeat to stderr every N seconds: `still waiting on
  <role> (Ns elapsed)…` so the human/agent sees liveness.
- Optionally surface the target's status (idle/busy) so "waiting on an idle
  agent" is obviously a fast-return, not a hang.

### 4. Guidance
- Update edit + delegating agent `.md` bodies: delegate **intent**, keep sends
  short/single-line, prefer `--track`.
- Add a "Message hygiene" subsection to `docs/agent-bus.md` and a delegation
  convention bullet to `CLAUDE.md`.

### Key files

| File | Purpose / change |
|------|------------------|
| `tools/muxcode/daemon/daemon.go` | Health-monitor loop — add the frozen-agent watchdog (detect → kill → respawn → verify inbox drained) |
| `tools/muxcode/bus/agent_health.go` | `IsAgentAlive()` is insufficient for frozen-but-alive; add a frozen/responsiveness check + force-kill primitive; auto-restart trigger |
| `tools/muxcode/bus/health.go` | `RestartLocalAgent()` (currently C-c based) — add OS-process-kill path |
| `tools/muxcode/cmd/` | New `muxcode kill <role>` / `reload --force-kill` command |
| `tools/muxcode/bus/notify.go` | Post-respawn wake verification (re-wake until inbox drained — close the wake gap) |
| `tools/muxcode/bus/diagnose.go` | Reconcile "shell still running" vs real processes; update `active-with-stale-messages` remediation to the force-kill/auto-recovery path |
| `tools/muxcode/cmd/send.go` | `validatePayload()` escalation, `--payload-file`/stdin, optional strict mode, `--wait` heartbeat |
| `tools/muxcode/cmd/send_test.go` | Tests for warnings, strict mode, file/stdin payload, heartbeat |
| `agents/code-editor.md` | Delegate-intent + concise-send; route watches to watch/`proc`, never foreground |
| `agents/git-manager.md` | Delegations carry intent (agent composes the commit message); no in-pane `--watch` for PR checks |
| `agents/log-watcher.md`, `agents/command-runner.md` | Confirm watches/long-running commands land here, not in delegating agents |
| `docs/agent-bus.md` | `muxcode send` message-hygiene + "keep agents deliverable" notes |
| `docs/architecture.md` / `docs/agents.md` | Idle-gated delivery + stuck-active recovery behavior |
| `CLAUDE.md` | Delegation + agent-liveness convention bullet |

## Implementation

### Phase 1: Design decisions
- [ ] Decide auto-recovery policy for `active-with-stale-messages`: thresholds
      (inbox age while active), keystroke-clear-then-restart vs straight restart,
      and how many attempts before giving up / alerting.
- [ ] Decide the "no blocking foreground command" enforcement: guidance-only vs a
      guard/lint on known offenders (`--watch`, `tail -f`) in non-watch panes.
- [ ] Decide how to reconcile the "shell still running" indicator with real
      processes in `diagnose.go`.
- [ ] Decide guardrail escalation: louder warning vs opt-in strict reject vs
      auto-fix (strip newlines / truncate).
- [ ] Decide long-payload escape hatch: `--payload-file`, stdin `-`, or both.
- [ ] Decide `--track` default: change the implicit default vs document-only.
- [ ] Decide `--wait` heartbeat interval and whether to include target status.
- [ ] Decide whether to **split** the liveness work into its own spec (see open
      questions) or keep it here.
- [ ] Record decisions in "Open questions" below.

### Phase 2: Agent-liveness recovery (primary)
- [ ] Add a **force-kill command** (`muxcode kill <role>` / `reload --force-kill`)
      that terminates the agent's OS process (TERM→KILL) and lets daemon
      auto-restart respawn it.
- [ ] Implement daemon auto-recovery for `active-with-stale-messages`
      (threshold → gentle keystroke clear → **escalate to force-kill + respawn**).
- [ ] Make auto-restart detect frozen-but-alive agents (active + stale inbox past
      threshold), not just dead processes (`IsAgentAlive` alone is insufficient).
- [ ] Close the **post-restart wake gap**: after respawn, verify the fresh agent
      reaches its prompt and drains its inbox; re-wake if the startup check/notify
      didn't fire. Recovery completes only when the pending work is consumed.
- [ ] Reconcile the "shell still running" indicator against actual processes in
      `diagnose.go`; update its remediation text to the force-kill path (not the
      ineffective send-keys restart).
- [ ] (If chosen) add a guard/lint flagging blocking foreground commands in
      non-watch agent panes, suggesting `muxcode proc` / the watch agent.
- [ ] Unit-test the recovery trigger, the force-kill path, and the stale-indicator
      reconciliation.

### Phase 3: Payload guardrails
- [ ] Implement the agreed escalation in `validatePayload()` / send path.
- [ ] Implement `--payload-file` / stdin payload input.
- [ ] (If chosen) implement `MUXCODE_SEND_STRICT` reject mode.
- [ ] Unit-test warnings, strict mode, and file/stdin payloads in `send_test.go`.

### Phase 4: Delegation defaults and `--wait` UX
- [ ] Apply the `--track` default decision (code and/or docs).
- [ ] Implement the `--wait` progress heartbeat.
- [ ] Unit-test heartbeat emission and default-flag resolution.

### Phase 5: Guidance
- [ ] Update `agents/code-editor.md` and `agents/git-manager.md` (delegate intent,
      concise single-line sends, prefer `--track`, never run in-pane `--watch`).
- [ ] Confirm `agents/log-watcher.md` / `agents/command-runner.md` own watches and
      long-running commands.
- [ ] Update `docs/agent-bus.md` (message hygiene + keep-agents-deliverable +
      file-handoff pattern) and `docs/architecture.md`/`docs/agents.md`
      (idle-gated delivery + recovery).
- [ ] Update `CLAUDE.md` with the delegation + agent-liveness convention bullet.

### Phase 6: Integration test
- [ ] Create `scripts/test-send-hygiene.sh` (runs inside a live muxcode session).
- [ ] Test: a `>500`-char send emits the escalated warning (assert on output).
- [ ] Test: a newline-containing send emits the glob-risk warning.
- [ ] Test: `--payload-file` delivers a long payload with no inline newline warning.
- [ ] Test: a delegation with neither flag behaves per the chosen default.
- [ ] Test: `--wait` prints at least one progress heartbeat before the reply.
- [ ] Test: a compliant short single-line send produces **no** warnings (no regression).
- [ ] Test (freeze recovery, **no manual step**): simulate a frozen-but-alive agent
      (unresponsive to keystrokes, `IsAgentAlive` true) with a pending inbox →
      assert the daemon detects it, force-kills, respawns, and the pending message
      is **processed** within the threshold — with zero human intervention.
- [ ] Test (post-restart wake): after the respawn, assert the fresh agent drains
      its inbox without a manual wake (re-wake fires if the startup check didn't).
- [ ] Test (no false positive): a healthy agent in a long but progressing turn is
      **not** killed by the watchdog.
- [ ] Run the script and verify all checks pass.

## Open questions
- [ ] **Split?** Should the agent-liveness recovery (the primary incident cause) be
      its own backlog spec, leaving this one focused on message hygiene? They share
      the incident but are independently shippable.
- [ ] **Freeze-detection signal:** how does the watchdog reliably tell *frozen*
      from *legitimately busy* (long Cogitating turn)? Candidates: pane content
      static for N s + inbox stale + active, an unanswered liveness nudge, no token
      output for N s. Getting this right is the crux (false kills vs missed hangs).
- [ ] Auto-recovery aggressiveness: thresholds before kill, max attempts, and the
      back-off / alert when recovery itself keeps failing.
- [ ] Post-restart wake: how to confirm the inbox was drained (inbox empty? a
      processed-marker?) and how many re-wakes before alerting.
- [ ] Enforcement of "no blocking foreground command": guidance-only, soft guard
      (warn), or hard guard (deny `--watch`/`tail -f` in non-watch panes)?
- [ ] Guardrail escalation: louder warning, opt-in strict reject, or auto-fix?
- [ ] Long-payload escape hatch: `--payload-file`, stdin `-`, or both?
- [ ] File-handoff scratch location: plain `/tmp/<name>.md` (simple, what's used
      today) vs the session bus dir `/tmp/muxcode-bus-{session}/scratch/` (auto-
      cleaned on session teardown)? And should old scratch files be GC'd?
- [ ] Change the implicit delegation default to `--track`, or document-only and
      leave `--wait` as the explicit choice?
- [ ] `--wait` heartbeat: interval, and should it include target idle/busy status?
- [ ] Should strict mode be per-send (`--strict`), env (`MUXCODE_SEND_STRICT`), or
      role-scoped (stricter for edit→commit)?

## Status

Backlog
