# Agent Health Monitoring

## Context

The watcher process runs as a detached background process and can die silently, taking down analyze event routing, loop detection, cron, and all other periodic checks. Individual agents can also crash in their tmux panes with no one noticing. This feature adds two capabilities:

1. **Agent liveness** — the watcher detects dead agents and restarts them
2. **Watcher self-monitoring** — a monitor script detects when the watcher itself dies and relaunches it

## Problem statement

| Failure mode | Impact | Current detection | Gap |
|-------------|--------|-------------------|-----|
| Claude Code crashes in agent pane | Pane falls back to bare shell prompt; agent stops processing inbox | None — no one notices until user checks the window | No automatic detection or restart |
| LLM harness process dies | Local LLM agent stops processing; pane shows shell prompt | None | Same as above |
| Agent exits during startup | `muxcode-agent.sh` fails mid-launch; pane has shell prompt | None | Same |
| Watcher process dies | All periodic checks stop — loop detection, cron, compaction, Ollama health, inbox notifications, agent health | None — watcher runs detached from tmux | No heartbeat mechanism |
| Watcher hangs (infinite loop, blocked I/O) | Same as death — all periodic checks stall | None | Process is alive but not progressing |

## Design

### Agent liveness detection

Agents run in tmux pane 1 (right pane) of their window. When Claude Code is running, the pane shows its TUI. When it exits or crashes, the pane falls back to a bare shell prompt.

#### Detection heuristic

Capture the last 5 lines of the agent pane via `tmux capture-pane -t {session}:{role}.1 -p -S -5` and apply these checks in order:

| Priority | Check | Result | Rationale |
|----------|-------|--------|-----------|
| 1 | Harness marker PID exists and is alive | alive | LLM harness is running |
| 2 | `IsAgentIdle()` sees `❯` character | alive | Claude Code is at idle prompt |
| 3 | Last non-empty line ends with `$` or `%`, no `❯` anywhere | **dead** | Bare shell prompt — agent has exited |
| 4 | `muxcode-agent` or `claude` text in capture | alive | Agent is starting up |
| 5 | Default (indeterminate) | alive | Conservative — avoid false restarts |

**Key design decisions:**
- Check order matters — harness and idle checks are fast paths that skip the more expensive pane capture
- The `$`/`%` heuristic works for bash and zsh. Dollar signs mid-text (e.g. `$50`) don't match because they aren't at the end of the last non-empty line
- The startup check (priority 4) prevents restarts during slow agent initialization
- Default-alive is intentionally conservative — a false "dead" triggers an unnecessary restart, while a false "alive" only delays detection by one probe interval (30s)

#### 3-strike escalation

Probes run every 30 seconds per role. Consecutive failures escalate:

| Strike | Elapsed | Action |
|--------|---------|--------|
| 1 | 30s | Log failure count, increment `agentFailCounts[role]` |
| 2 | 60s | Set `agentWasDown[role] = true`. Send `agent-down` event to edit (dedup 600s via `lastAlertKey`) |
| 3 | 90s | Restart agent via `RestartLocalAgent()`. Send `agent-restarting` event to edit. Increment `agentRestarts[role]`. Reset fail count to 0 (let next probe detect recovery) |

**After restart cap (3 per role per session):** Alert-only mode — periodic `agent-down` events but no more restarts. Manual intervention required.

**Recovery detection:** When a previously-down agent passes a probe, send `agent-recovered` event to edit, reset `agentWasDown[role]` and `agentFailCounts[role]`.

#### Excluded roles

| Role | Reason |
|------|--------|
| `edit` | User's interactive session — never auto-restart |
| `webhook` | Managed separately (PID file), not a tmux-based agent |
| `spawn-*` | Spawn roles are excluded via `IsSpawnRole()` — they have their own lifecycle management in `checkSpawns()` |

#### Intentional stop suppression

A stopped marker file at `/tmp/muxcode-bus-{session}/lock/{role}.stopped` suppresses auto-restart for a role. This prevents the watcher from fighting a user who intentionally stopped an agent.

| Operation | CLI | Effect |
|-----------|-----|--------|
| Stop | `muxcode-agent-bus agent-health --stop <role>` | Writes marker, suppresses auto-restart |
| Start | `muxcode-agent-bus agent-health --start <role>` | Removes marker, re-enables auto-restart |
| Check | `muxcode-agent-bus agent-health --check <role>` | Reports alive/dead/stopped/excluded status |

### Watcher self-monitoring

#### Keepalive mechanism

The watcher writes the current unix timestamp to `/tmp/muxcode-bus-{session}/watcher.keepalive` at the top of each poll loop iteration (every 2s by default). This detects both death and hangs — a stuck watcher stops updating the file.

#### Monitor script (`scripts/muxcode-watcher-monitor.sh`)

Background bash loop launched alongside the watcher in `muxcode.sh`:

| Parameter | Value | Description |
|-----------|-------|-------------|
| Check interval | 15s | Sleep between staleness checks |
| Max age | 30s | Keepalive older than this triggers restart |
| Exit condition | Session gone | Exits if `tmux has-session` fails |
| Restart method | `pkill -f` + relaunch | Same pattern as `muxcode.sh` watcher startup |

**Startup in `muxcode.sh`:**
```bash
# Kill stale processes from previous sessions
pkill -f "muxcode-agent-bus watch $SESSION" 2>/dev/null || true
pkill -f "muxcode-watcher-monitor.sh $SESSION" 2>/dev/null || true
sleep 0.1
# Launch both
muxcode-agent-bus watch "$SESSION" &>/dev/null &
muxcode-watcher-monitor.sh "$SESSION" &>/dev/null &
```

### Session re-init cleanup

On session restart, `purgeStaleFiles()` in `bus/setup.go` already removes all files in the `lock/` directory, which covers `*.stopped` markers. The `watcher.keepalive` file is overwritten immediately when the new watcher starts. No additional re-init logic is needed.

### System action registration

All health events are registered in `isSystemAction()` in `bus/guard.go` to prevent false loop detection:

- `agent-down`
- `agent-restarting`
- `agent-recovered`

These join the existing system actions: `loop-detected`, `compact-recommended`, `proc-complete`, `spawn-complete`, `ollama-down`, `ollama-recovered`, `ollama-restarting`.

## Implementation status

### Completed (uncommitted)

| # | Change | File | Status |
|---|--------|------|--------|
| 1 | Agent liveness detection + stopped markers | `bus/agent_health.go` | ✅ Written |
| 2 | Unit tests (stopped round-trip, `isShellPrompt`, alert formatting, excluded roles) | `bus/agent_health_test.go` | ✅ Written |
| 3 | Watcher `checkAgentHealth()` (30s interval, 3-strike, recovery) + `touchKeepalive()` + struct fields | `watcher/watcher.go` | ✅ Written |
| 4 | Watcher keepalive path + staleness check + restart | `bus/watcher_health.go` | ✅ Written |
| 5 | Unit tests (keepalive path, fresh/stale/missing) | `bus/watcher_health_test.go` | ✅ Written |
| 6 | Watcher monitor bash script | `scripts/muxcode-watcher-monitor.sh` | ✅ Written |
| 7 | Monitor launch in session startup | `muxcode.sh` | ✅ Written |
| 8 | System action registration (`agent-down`, `agent-restarting`, `agent-recovered`) | `bus/guard.go` | ✅ Written |
| 9 | `Health` field in `AgentStatus` + `HEALTH` column in status table | `bus/inspect.go` | ✅ Written |
| 10 | `agent-health` CLI subcommand (--stop/--start/--check) | `cmd/agent_health.go` | ✅ Written |
| 11 | `agent-health` registered in dispatch table + usage text | `main.go` | ✅ Written |

### Remaining work

| # | Task | File(s) | Priority |
|---|------|---------|----------|
| 12 | Build and run tests | — | High |
| 13 | Add `agent_health.go`, `watcher_health.go` to CLAUDE.md code reference table | `CLAUDE.md` | High |
| 14 | Add system actions (`agent-down`, `agent-restarting`, `agent-recovered`) to CLAUDE.md system actions list | `CLAUDE.md` | High |
| 15 | Add `agent-health` to agent-bus.md CLI reference | `docs/agent-bus.md` | Medium |
| 16 | Add agent health section to agents.md (near Ollama health monitoring section) | `docs/agents.md` | Medium |
| 17 | Add watcher keepalive + monitor to architecture.md (watcher section) | `docs/architecture.md` | Medium |
| 18 | Move from Planned to Implemented in backlog.md | `docs/requirements/backlog.md` | Medium |
| 19 | Add `watcher.keepalive` to configuration.md ephemeral directory listing | `docs/configuration.md` | Low |
| 20 | Manual verification: kill agent pane, wait 90s, verify restart + edit notification | — | High |
| 21 | Manual verification: kill watcher, wait 30s, verify monitor restarts it | — | High |
| 22 | Manual verification: `muxcode-agent-bus status` shows HEALTH column | — | High |

## Data files

| File | Location | Purpose | Lifecycle |
|------|----------|---------|-----------|
| `watcher.keepalive` | `/tmp/muxcode-bus-{session}/` | Unix timestamp, updated every poll loop | Overwritten on watcher start |
| `{role}.stopped` | `/tmp/muxcode-bus-{session}/lock/` | Marker file suppressing auto-restart | Written by `--stop`, removed by `--start`, purged on re-init |

## Alert flow

```
Agent pane crashes
  ↓
watcher checkAgentHealth() (30s interval)
  ↓
IsAgentAlive() → false (shell prompt detected)
  ↓
Strike 1 (30s): log, increment fail count
  ↓
Strike 2 (60s): send "agent-down" event → edit inbox
  ↓
Strike 3 (90s): RestartLocalAgent(), send "agent-restarting" → edit
  ↓
Next probe: IsAgentAlive() → true
  ↓
Send "agent-recovered" → edit, reset counters
```

```
Watcher dies or hangs
  ↓
muxcode-watcher-monitor.sh (15s interval)
  ↓
Reads watcher.keepalive timestamp
  ↓
Age > 30s → stale
  ↓
pkill -f "muxcode-agent-bus watch $SESSION"
  ↓
Relaunch: muxcode-agent-bus watch "$SESSION" &>/dev/null &
```

## Verification plan

| Test | Steps | Expected result |
|------|-------|-----------------|
| Agent crash detection | Kill Claude Code in a non-edit agent pane (`Ctrl-C` or `kill`). Wait 90s. | Watcher logs 3 strikes, edit receives `agent-down` then `agent-restarting`, agent relaunches. |
| Agent recovery | After restart, check `muxcode-agent-bus status`. | Role shows `alive` in HEALTH column. Edit receives `agent-recovered`. |
| Intentional stop | Run `muxcode-agent-bus agent-health --stop build`, kill build agent. Wait 90s. | Watcher skips build in health checks. Status shows `stopped`. |
| Intentional start | Run `muxcode-agent-bus agent-health --start build`. | Watcher resumes monitoring build. Next probe detects dead → escalation begins. |
| Restart cap | Kill an agent 4 times in sequence. | First 3 auto-restart. 4th triggers alert-only mode ("Restart cap (3) reached"). |
| Excluded roles | Kill edit pane's Claude Code. Wait 90s. | No health alerts — edit is excluded. |
| Watcher death | `pkill -f "muxcode-agent-bus watch"`. Wait 30s. | Monitor detects stale keepalive, relaunches watcher. |
| Watcher hang | Not easily simulated — verify keepalive file is being updated during normal operation. | `cat /tmp/muxcode-bus-{session}/watcher.keepalive` shows recent timestamp. |
| Session restart | Kill session, restart with same name. | Lock dir purged (stopped markers removed), keepalive overwritten by new watcher. |
| Status table | Run `muxcode-agent-bus status`. | HEALTH column shows alive/excluded/stopped/dead for each role. |
| JSON status | Run `muxcode-agent-bus status --json`. | Each entry has `"health"` field. |
| Unit tests | `cd tools/muxcode-agent-bus && go test ./...` | All tests pass including new `agent_health_test.go` and `watcher_health_test.go`. |

## Relationship to Ollama health monitoring

Agent health monitoring follows the same 3-strike pattern as Ollama health (`bus/health.go`, `watcher/watcher.go:checkOllama()`):

| Aspect | Ollama health | Agent health |
|--------|--------------|--------------|
| Probe interval | 30s | 30s |
| Alert threshold | 2 consecutive failures | 2 consecutive failures |
| Restart threshold | 3 consecutive failures | 3 consecutive failures |
| Restart cap | 3 per session | 3 per role per session |
| Alert dedup | 600s cooldown via `lastAlertKey` | 600s cooldown via `lastAlertKey` |
| Recovery detection | `ollamaWasDown` flag | `agentWasDown[role]` map |
| System actions | `ollama-down`, `ollama-recovered`, `ollama-restarting` | `agent-down`, `agent-recovered`, `agent-restarting` |

The two systems are independent — Ollama health monitors the inference server, agent health monitors individual agent processes. An agent using Ollama could trigger both if the Ollama server dies (Ollama health detects the server failure, agent health detects the agent crashing after repeated `ChatComplete` errors).
