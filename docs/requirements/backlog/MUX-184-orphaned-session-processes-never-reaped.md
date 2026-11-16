# MUX-184: Orphaned Session Processes Are Never Reaped

**Tracking:** [mkober/muxcode#140](https://github.com/mkober/muxcode/issues/140)

Killing a tmux session kills its panes and nothing else. The daemon, its monitor, every inbox
listener, the headless harness and the background procs a session launched are detached
(`Setsid`/`Setpgid`), so they reparent to launchd and run on — polling a bus directory nobody reads,
and **recreating it when it is deleted**. Nothing reaps them at close, at the next launch, or on any
schedule. The one sweep that exists, `upgrade-daemons`, runs only from `./build.sh` and covers one of
the families below.

Three sub-defects sit under the headline, each verified in code. The tmux `session-closed` hook the
launcher installs runs `muxcode cleanup <session>` — and that command **skips the session it is
given**, so the close-time cleanup is a no-op for the only session it was installed for. File cleanup
without process cleanup is **undone**: the live daemon recreates the bus dir, and a live monitor
relaunches a killed daemon within 30 s. And the daemon **orphans its own harness** when it dies,
because it traps no signal and the harness sits in its own process group — so the sweep that fixes
daemon orphans manufactures harness orphans.

## Context

### Source and standard of evidence

Observed **first-hand by edit** on 2026-09-14 while cleaning up four stray sessions a test agent had
launched; written up as `/tmp/mux-184-handoff.md` (non-durable) and filed on the user's instruction
relayed by edit. **Every mechanism claim below was verified by plan against the tree at `3f9a2cb`**
(plus the `tools/muxcode/main.go` launcher guard, since committed in `7bcd657`, that closes the
multi-arg and subcommand-named forms of the stray-session cause). That guard was not the whole
cause: on 2026-09-15 a single-arg `muxcode agents` — one edit from `agent` and a real directory —
still launched a 10-window fleet with cwd `agents/`; `routeFor` now refuses a bare near-miss with a
did-you-mean, path spelling (`muxcode ./agents`) being the escape hatch. See
[`agent-bus.md`](../../agent-bus.md#muxcode-version).
Three claims in the handoff did not survive verification and are recorded corrected, not silently:

| Handoff said | Tree says |
|---|---|
| `CleanStaleMarkers()` at `bus/setup.go:105`, "reload markers only" | `bus/config.go:468`; it removes `waiting-*`/`polling-*` markers older than 10 min. Reload markers are not its subject |
| "Session launch reaps no processes" | `killStaleProcesses` (`bus/launcher.go:506`) `pkill -f`s the daemon and monitor **of the session name being launched** — two anchored argv patterns, that name only. It reaps nothing for any other session |
| The mechanism table had no close-time row | The launcher installs a tmux `session-closed` hook (`launcher.go:312`) running `muxcode cleanup <session>` — the close-time mechanism exists, and is a no-op for its own session ([sub-defect A](#sub-defect-a--the-close-hook-cleans-every-session-but-its-own)) |

### Observed

| # | Evidence (2026-09-14 unless noted) | Standing |
|---|---|---|
| 1 | A test agent, guessing the CLI's calling convention, started four stray sessions — `memory`, `send`, `__help`, `test` — and 93 panes. `tmux kill-session` on all four left their daemons running: `muxcode watch memory` (65416), `watch send` (76613), `watch __help` (92565), `watch test` (13498), all ppid 1 | edit, first-hand. No lifecycle log exists for any of the four at filing (`~/.config/muxcode/logs/` has no `memory.log`, `send.log`, `__help.log` or `test.log`), so the stray launches are not on the one record a reaper might otherwise consult |
| 2 | `rm -rf /tmp/muxcode-bus-send` succeeded; the directory **reappeared** — the live `muxcode watch send` daemon recreated it | edit, first-hand. [Sub-defect B](#sub-defect-b--file-cleanup-without-process-cleanup-is-undone) |
| 3 | 19 further orphans with no live parent, oldest from 09-10 (four days): 14× `muxcode-llm-harness run prompt`, 1× `bash -c while true; do muxcode inbox --poll; done`, 1× `bash /tmp/muxcode-bus-is-advising-gateway/inbox-listener.sh` (session long gone), 3× `tmux -L escabs-* new-session … escape-chord-receiver.py`. All 19 exited on SIGTERM alone | edit, first-hand |
| 4 | This repo's own lifecycle log carries five `daemon-orphan-killed` rows for session `muxcode` — 09-10 15:45, 09-13 13:18, 09-14 09:24, 11:23 and 14:34 — each a daemon that outlived its tmux session and was reaped only by the next `./build.sh` | plan, `~/.config/muxcode/logs/muxcode.log`. First-hand and durable: this session's own daemon leaks roughly once a day |
| 5 | 11 `/tmp/muxcode-bus-test-prelaunch-*` directories written at 14:57 by an integration run, sitting with no tmux session behind them | plan, `ls /tmp`. The manual-only sweep was not run, and the close hook could not have removed them (sub-defect A) |
| 6 | The four live `muxcode inbox --poll --loop` listeners carry **no session name in argv** — their session is `BUS_SESSION` in the environment, inherited from the pane | plan, `ps -axo pid,ppid,command`. An argv-matching reaper cannot own this family |

Orphans are not inert. An orphaned listener consumes bus messages for a session that no longer
exists ([MUX-156](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) — 31 alive at its
second occurrence, a user-authorised `cdk deploy` acked and never run). Orphaned harnesses burn CPU
and memory ([MUX-147](./MUX-147-process-leak-and-memory-footprint.md) — 36 holding ~243 MB at its
measurement). This spec is the mechanism both of those symptoms share.

### Mechanism — what runs, when, and what it reaps

| Mechanism | Code | Runs | Reaps processes? | Reach |
|---|---|---|---|---|
| `killStaleProcesses` | `bus/launcher.go:506` | session launch | **yes** — `pkill -f 'muxcode watch [--monitor ]<s>$'` | the launching session's own name only; two argv shapes |
| `CleanStaleMarkers` | `bus/config.go:468` | bus init | no — `waiting-*`/`polling-*` markers older than 10 min | files, own session |
| `session-closed` hook → `muxcode cleanup <s>` | `bus/launcher.go:312` → `cmd/cleanup.go:56-64` → `bus.CleanupStale` → `shouldClean` (`bus/cleanup.go:186`) | session close | no | files of **other** dead sessions; **never its own** (sub-defect A) |
| `muxcode cleanup` | same | manual | no | bus dirs, preview and trigger files, spawn dirs and logs of dead sessions |
| `upgrade-daemons` orphan path | `bus/upgrade.go:145-221` | `./build.sh` | **yes** — monitor first, then daemon (`killProcess`: TERM, 2 s, KILL) | `muxcode watch` argv only; every session on the machine |
| `terminateMarkerHolder` | `bus/notify.go:729` | a successor listener claims the role's marker | yes — the incumbent listener | one process, only when replaced |
| `StopPromptAgent` | `bus/prompt_agent.go:270` | `daemon/daemon.go:1666`, on an Ollama restart | yes — via `harness-<role>.pid` | never on daemon exit |
| `StopProc` / `spawn stop` / `StopWebhook` | `bus/proc.go:423`, `bus/spawn.go`, `bus/webhook.go:262` | on request | yes — by registry | never at session end |

The daemon (`muxcode watch`) has **no signal handler** — no `signal.Notify` in `daemon/daemon.go` or
`cmd/watch.go` — and no session-liveness check of its own: nothing under `daemon/` asks
`TmuxHasSession`. It runs until something outside it kills it. The single-session `bus.Cleanup()`
(`bus/cleanup.go:34`, bus dir plus trigger file) has no non-test caller.

### Sub-defect A — the close hook cleans every session but its own

`TmuxSetHook(session, "session-closed", "run-shell 'muxcode cleanup <session>'")` (`launcher.go:312`).
`Cleanup()` takes the positional as `session` and passes it to
`CleanupStale(session, dryRun=false, includeActive=false)` as **`currentSession`**
(`cmd/cleanup.go:56-64`). `shouldClean(s, currentSession, includeActive)` returns **false when
`s == currentSession` and `--all` is not set** (`bus/cleanup.go:186-188`) — before it asks tmux
anything. The hook therefore removes the artifacts of *other* already-dead sessions and leaves the
closing session's own bus dir, trigger file and spawn dir in place.
[`docs/agent-bus.md`](../../agent-bus.md#muxcode-cleanup) documents the hook as removing the
session's bus directory; the code does not. Whether the hook fires at all on `tmux kill-session`, and
when the server exits with its last session, is a Phase 1 measurement, not a claim — the outcome for
the closing session is the same either way.

### Sub-defect B — file cleanup without process cleanup is undone

The daemon's poll writes through `MkdirAll` sites — `bus/delivery.go:319` (`writeDeliveryStatus`,
added by `e9e3941` the same day), `bus/inbox.go:168,263,334` — so a deleted bus dir is back within a
poll; which writer edit's `send` daemon hit first is a Phase 1 finding. And a daemon killed alone is
back within 30 s while its monitor lives: `muxcode watch --monitor` relaunches a daemon whose
keepalive is stale (`RestartDaemon`, `bus/daemon_health.go:78`). `UpgradeDaemons` already encodes the
ordering that follows — *"Monitor first — a live monitor would relaunch the daemon we just killed"*
(`upgrade.go:206`). The reaper inherits it: **processes before files, monitor before daemon.**

### Sub-defect C — the daemon orphans its own harness

`StartPromptAgent` (`bus/prompt_agent.go:240-263`) launches `muxcode-llm-harness run prompt` as a
child of the daemon with `Setpgid: true` and an environment-only session (`promptAgentEnv`; the
harness reads `MUXCODE_SESSION`/`BUS_SESSION`, `tools/muxcode-llm-harness/harness/config.go:44-48`).
Its pid is recorded in `harness-prompt.pid`. When the daemon is killed — by `killStaleProcesses`, by
`upgrade-daemons`, by hand — nothing signals the harness: the daemon traps no signal, and the harness
is not in the daemon's process group. It reparents to launchd and polls forever. That is the
mechanism behind evidence row 3's fourteen harnesses and MUX-147's thirty-six, and it is triggered by
the one tool that reaps orphans today.

### Families and what proves their ownership

A reaper may kill only what it can prove a dead session owns. The proof available differs per family:

| Family | argv shape | Session in argv? | Pid record in the bus dir | Note |
|---|---|---|---|---|
| Daemon | `muxcode watch <s>` | yes | none — `daemon.keepalive` is a timestamp, `daemon.version` a build | `parseDaemonProcs` (`upgrade.go:80`) already parses the argv |
| Monitor | `muxcode watch --monitor <s>` | yes | none | must die **before** the daemon |
| Claude inbox listener | `muxcode inbox --poll --loop` | **no** — `BUS_SESSION` env | `polling-<role>.marker` holds the pid (`claimMarker`, `notify.go:682-685`) | MUX-156's subject |
| Legacy listeners | `bash -c while true; do muxcode inbox --poll; done`; `bash /tmp/muxcode-bus-<s>/inbox-listener.sh` | loop: no; script: the path names the session | the inner `inbox --poll` claims the marker per iteration; the bash parent is unrecorded | neither shape is produced by the current binary; both were alive on 09-14 |
| Headless harness | `muxcode-llm-harness run <role>` | **no** — env | `harness-<role>.pid` (`IsHarnessActive`, `notify.go:18`) | MUX-147's subject |
| `muxcode proc` children | arbitrary | no | proc registry (`ReadProcEntries`, `proc.go:125`); own pgid (`StartProc`, `:252`) | `StopProc` (`:423`) kills the group |
| Spawn workers | provider CLI | no | `spawn.jsonl` | `spawn stop` |
| Webhook | `muxcode webhook …` | unverified | `WebhookPidPath` | `StopWebhook` |
| Launch helpers | `muxcode launch --resize <s>`, `--auto-accept <s> …` (`launcher.go:315-318`) | yes | none | whether they exit on their own when the session goes is **unverified** (Phase 1) |
| Integration-test tmux servers | `tmux -L escabs-<pid> …` | not session-owned at all | the launching script's pid is in the socket name | owned by a script's `EXIT` trap (`scripts/test-escape-absorber.sh:50-58`); how three escaped it is not established |

**Proof per family — Decision 1, recorded 2026-11-15.** Two proofs exist and nothing else counts: the
session name in argv, or a recorded pid that still names the same process. No proof → no kill, an
`orphan-unproven` row. There is no environment-probe proof.

*Amended 2026-11-16 by the user (relayed by edit, `1794844857-edit-5f52a782`): the legacy
`inbox-listener.sh` script form, whose path names the session, counts as argv proof — the same
evidence class as the daemon, monitor and launch helpers — and **is reaped**. Plan had read the
decision as leaving it unproven; that reading is reversed.*

| Family | Proof | Consequence |
|---|---|---|
| Daemon, monitor, launch helpers | Session name in argv | Reaped on the argv census when the named session is positively absent |
| Legacy listener, script form (`bash <bus base>/muxcode-bus-<s>/inbox-listener.sh`) | Session name in argv — the script path's `muxcode-bus-<s>` directory names it | Reaped on the argv census when the named session is positively absent, like the row above. The parser takes the session from the bus-directory component of the path only; a path that does not parse to one is unproven |
| Claude inbox listener, headless harness, `muxcode proc` children, spawn workers, webhook | The pid record under the dead session's bus dir, **and** that pid still naming the recorded process (pid reuse detectable — whether each record carries enough for that is Phase 1's finding; where it does not, Phase 3 adds it) | Reaped from the record; a record whose pid now names something else is refused and logged |
| Legacy listener, loop form (`bash -c while true; do muxcode inbox --poll; done`) | Neither. The bash parent is unrecorded and its argv carries no session | `orphan-unproven`, never killed. Its inner `muxcode inbox --poll` is reaped per iteration through the marker record like any listener |
| `escabs-<pid>` test tmux servers | Its own rule (Decision 3): the pid in the socket name is dead | Reaped with its own lifecycle row; not session-owned, so the session-liveness bar does not apply — the socket-name pid's liveness does |

Two doctrines already in the tree bound the design. `isTmuxSessionAlive` (`bus/cleanup.go:210-240`)
demands **positive absence** — the server answered and the name was not listed — because on
2026-09-08 a sandboxed probe read every live session as stale and an attached session lost its bus
dir repeatedly. Killing is less reversible than deleting; the reaper holds that bar or higher. And
`ListDaemonProcs` is `ps`, which the Codex build sandbox cannot exec
([MUX-161](../completed/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md)) — a `ps`-only reaper inherits
that blindness, which is one reason the pid records above matter.

### Blast radius

- Every `tmux kill-session`, same-name relaunch of a *different* session, or tmux server crash leaks at least two processes per session — daemon and monitor — plus one listener per Claude role and one harness per local role. Evidence row 4 puts it at about once a day for this repo's main session alone; evidence row 3 puts the machine-wide backlog at 19 after four days.
- A dead session's daemon keeps its bus dir alive, keeps attempting delivery, and keeps writing lifecycle rows under a session name that no longer exists; a dead session's listener eats a live role's messages if the name is reused (MUX-156).
- Recovery is by hand — `ps`, read argv, infer ownership, `kill` — and the inference is exactly what this spec says a reaper must not do.

### Family

- [MUX-147](./MUX-147-process-leak-and-memory-footprint.md) — the harness family. Its Phases 2–3 (reaper design; *"may a daemon reap another session's processes?"*) are this spec's Phases 2–3 restricted to one family; its footprint half is untouched here. Since 2026-11-15 its reaper items are ticked there as deferred to this spec (Decision 5).
- [MUX-156](../completed/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) — the listener family. Its listener-side self-exit and receipt attribution stay there; its daemon-side census is this reaper.
- [MUX-161](../completed/MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) — `ps` is unavailable to a Codex build agent; any reaper on the `ps` road shares it.
- [MUX-008](./MUX-008-unverified-daemon-auto-restart.md) — the fire-and-hope relaunch; bears on whether a reaped daemon stays reaped.
- [MUX-171](../completed/MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — one way an integration script dies mid-run; whether that is how the `escabs` servers lost their trap is not established.

## Requirements

### Acceptance criteria

- [ ] Closing a session — `tmux kill-session`, a same-name relaunch, or the tmux server exiting — leaves **no process whose bus session is that session** alive past a bounded grace period: daemon, monitor, listeners, harness, proc children, spawn workers, webhook, launch helpers
- [ ] The `session-closed` hook, or its replacement, removes **the closing session's own** artifacts — sub-defect A closed, with a negative control proving it does not touch a live session's
- [ ] Process cleanup precedes file cleanup, and the monitor stops before the daemon — a reaped session's bus dir does not reappear (sub-defect B)
- [ ] A daemon sent TERM or INT stops its children — harness and proc children — before it exits; a daemon that dies without the chance (`kill -9`) leaves a harness the reaper finds by its pid record (sub-defect C, Decision 4)
- [ ] **Negative control:** a daemon re-exec'ing in place for `upgrade-daemons` does **not** stop its children — the harness survives the re-exec under the same pid (Decision 4)
- [ ] Ownership is proved before any kill: by the session name in argv (daemon, monitor, launch helpers, and the legacy `inbox-listener.sh` script form, whose path names it), or by a pid record under that session's bus dir whose pid still names the same process (every other family) — **never by bare process name, never by an environment probe**, and never while the session's liveness is unknown (the `isTmuxSessionAlive` positive-absence bar) (Decision 1)
- [ ] A live session's processes are never reaped: the integration test runs a scratch live session beside the dead one and proves every one of its processes survives the sweep
- [ ] Every kill writes a lifecycle row naming session, family, pid and the proof used (`orphan-reaped`); a refusal for want of proof writes `orphan-unproven`
- [ ] A legacy `inbox-listener.sh` listener whose script path names a dead session is reaped on argv proof; the `bash -c while true …` loop form, which names no session, is left alone and logged `orphan-unproven` (negative control) (Decision 1 as amended 2026-11-16)
- [ ] Reaping at launch covers **every** dead session on the machine, not only the name being launched — `killStaleProcesses` becomes the family sweep or is subsumed by it
- [ ] `muxcode cleanup` reaps a dead session's processes before removing its files, or reports what it could not prove and leaves both in place
- [ ] The sweep runs automatically at every session launch; `MUXCODE_ORPHAN_REAP=0` disables it, and with it set a launch reaps nothing (Decision 2)
- [ ] An `escabs-<pid>` test tmux server is reaped when the pid in its socket name is dead, with its own lifecycle row; one whose pid is alive survives (Decision 3)
- [ ] `scripts/test-orphan-reaper.sh` exercises the sweep hermetically with a coverage floor

### Technical approach — the three shapes, composed

Three shapes. The user's decisions of 2026-11-15 compose them: Decision 1's two proofs are options 1
(argv, for the families that carry the session — daemon, monitor, launch helpers and, since
2026-11-16, the script-form legacy listener) and 2 (pid records, for the rest); Decision 2
makes the launch-time sweep the trigger that does not depend on tmux; option 3, the fixed hook, stays
as the immediate trigger for the closing session.

| Option | Shape | Catches | Risk |
|---|---|---|---|
| 1 — extend `upgrade-daemons`' sweep | `ListDaemonProcs`-style `ps` census, one parser per argv family; kill when `TmuxHasSession(argv session)` is positively false | daemon, monitor, launch helpers, the path-form legacy listener | argv-less families (listener, harness, procs, spawns) are invisible; `ps` is refused in the Codex sandbox (MUX-161); pid reuse between census and kill |
| 2 — a session-owned pid registry | every `startDetachedProcess`, `StartPromptAgent`, `claimMarker` and `StartProc` already leaves a record — unify them under one reader that, for a dead session's bus dir, verifies each pid still names the recorded process (start time or argv), kills it, then lets `RemoveAll` run | every family the session launched, argv-less ones included; no `ps` for the common case | the daemon and monitor leave no pid record today — add one at launch; the record lives in the dir being deleted, so the reader must run before removal |
| 3 — the tmux hook, fixed | `session-closed` → a `cleanup` form that does not skip its argument, running the option-2 reader first | the closing session, immediately | fires only if tmux still runs the hook for a killed session; not a substitute for the launch-time sweep (crashes, `kill -9` of the server) |

An environment probe (`ps -E` on the caller's own processes on macOS) was considered as a fourth
proof for argv-less processes and **ruled out** by Decision 1: it is not portable, and an argv-less
process with no usable pid record is left alone and logged.

Automatic at launch is decided (Decision 2) — the manual sweep was never run (evidence row 5) — with
an opt-out (`MUXCODE_ORPHAN_REAP=0`). TERM, a grace, then KILL, mirroring `killProcess`
(`upgrade.go:250`): all 19 died on TERM, so KILL is the backstop, not the path.

### Key files

| File | Role |
|---|---|
| `tools/muxcode/bus/launcher.go:192,312,506-528,541-558` | kills the same-name session; installs the close hook; `killStaleProcesses`; `startDetachedProcess` (`Setsid`) |
| `tools/muxcode/bus/cleanup.go:34-45,50-95,186-240` | single-session `Cleanup` (no callers); `CleanupStale`; `shouldClean`; `isTmuxSessionAlive` |
| `tools/muxcode/cmd/cleanup.go:25-64` | the positional becomes `currentSession` |
| `tools/muxcode/bus/upgrade.go:68-135,145-221,250-259` | `ListDaemonProcs`/`parseDaemonProcs`; orphan plan and kill order; `killProcess` |
| `tools/muxcode/bus/daemon_health.go:78-98` | `RestartDaemon` — the monitor's relaunch |
| `tools/muxcode/bus/prompt_agent.go:240-280` | harness launch (`Setpgid`, env session); `StopPromptAgent` |
| `tools/muxcode/bus/notify.go:18-40,682-740` | `IsHarnessActive`; `claimMarker`/`releaseMarker`; `terminateMarkerHolder` |
| `tools/muxcode/bus/proc.go:125-260,423-447` | proc registry; `StartProc` (`Setpgid`); `StopProc` |
| `tools/muxcode/bus/config.go:449-500` | marker paths; `CleanStaleMarkers` |
| `tools/muxcode/daemon/daemon.go` | no signal handler; `StopPromptAgent` only at `:1666`; `touchKeepalive` `:1691` |
| `tools/muxcode-llm-harness/harness/config.go:44-48` | the harness's session comes from the environment |
| `scripts/test-escape-absorber.sh:50-58`, `scripts/test-prompt-mode.sh:61-70` | test tmux servers and their traps |
| `docs/agent-bus.md` (`muxcode cleanup`, `muxcode upgrade-daemons`) | documents the hook as removing the session's bus dir |

## Implementation

### Phase 1: Establish the boundary

- [ ] On a scratch session, enumerate every process the launcher and daemon start, with ppid, pgid, `Setsid`/`Setpgid`, argv and environment session — the families table above is the hypothesis; the census is the fact
- [ ] Kill the scratch session three ways (`tmux kill-session`; same-name relaunch; `kill -9` the tmux server) and record which processes survive each, and whether the `session-closed` hook fired (a `session-cleanup` lifecycle row)
- [ ] Confirm sub-defect A empirically: after `kill-session`, the scratch session's own bus dir is still present
- [ ] Confirm sub-defect B: delete the dead session's bus dir and time its reappearance, naming the writer; kill the daemon alone and time its relaunch by the monitor
- [ ] Confirm sub-defect C: kill the daemon; the harness's ppid becomes 1 and `harness-prompt.pid` still names it
- [ ] Record whether `muxcode launch --resize` and `--auto-accept` exit on their own when the session goes
- [ ] Record which pid records exist per family and whether each carries enough to detect pid reuse (start time, argv)
- [ ] Record the findings here before choosing an option

### Phase 2: Choose the design

All five were decided by the user on 2026-11-15, ahead of Phase 1's census, and are ticked as
decided — no code is claimed by them. Phase 1 still runs first: a finding that contradicts a decision
(a pid record that cannot detect reuse, a launch helper that exits by itself) is recorded there and
taken back to the user rather than worked around.

- [x] Decide the proof per family ([Decision 1](#decision-1--what-proves-ownership-per-family)) and record it in the families table — decided 2026-11-15: argv session name, or a recorded pid still naming the same process; recorded under [Families](#families-and-what-proves-their-ownership)
- [x] Decide automatic-at-launch vs opt-in, and the opt-out ([Decision 2](#decision-2--automatic-at-launch-or-opt-in)) — decided 2026-11-15: automatic at every launch, `MUXCODE_ORPHAN_REAP=0`
- [x] Decide the `escabs` servers — in scope with socket-pid proof, or out ([Decision 3](#decision-3--are-the-escabs-test-servers-in-scope)) — decided 2026-11-15: in scope
- [x] Decide whether the daemon gains a signal handler that stops its children, or the reaper alone covers sub-defect C ([Decision 4](#decision-4--should-the-daemon-stop-its-own-children-on-exit)) — decided 2026-11-15: the handler, on TERM/INT, not on re-exec
- [x] Decide the relation to MUX-147 Phase 3 and MUX-156 Phase 3 — absorbed here, or those specs keep their family-specific halves and depend on this one ([Decision 5](#decision-5--what-becomes-of-mux-147s-and-mux-156s-reaper-phases)) — and record it in both specs and the backlog — decided 2026-11-15: their reaper phases point here; applied to MUX-147, MUX-156 and `backlog.md` the same day

### Phase 3: Reaper and registry

- [ ] Daemon and monitor write a pid record at launch (`daemon.pid`, `monitor.pid`, with start time) — the two families with no record today
- [ ] One reader over all records of a bus dir: verifies each pid still names the recorded process, then TERM → grace → KILL in the order monitor, daemon, the rest
- [ ] Argv census for the argv-carrying families — daemon, monitor, launch helpers and the legacy `inbox-listener.sh` script form, its session parsed from the `muxcode-bus-<s>` component of the script path — on the `ListDaemonProcs` road, gated by `TmuxHasSession` positive absence; when `ps` is unavailable it refuses that road and logs `orphan-census-unavailable` (MUX-161)
- [ ] Launch-time sweep over every dead session on the machine, replacing or subsuming `killStaleProcesses`; `MUXCODE_ORPHAN_REAP=0` honoured
- [ ] `escabs-<pid>` rule: a test tmux server whose socket-name pid is dead is killed and logged with its own row; a live pid's server is left alone
- [ ] `orphan-reaped` / `orphan-unproven` lifecycle rows per pid, naming session, family and proof
- [ ] Unit tests with negative controls: a record naming a pid now held by an unrelated process → refused; an argv-less process with no record → refused, `orphan-unproven`; a script-form legacy listener naming a dead session → reaped, the loop form → refused, and a script path that parses to no session → refused; a session the server lists → nothing killed; a probe that failed → nothing killed; an `escabs` socket whose pid is alive → nothing killed

### Phase 4: Close hook and cleanup ordering

- [ ] `muxcode cleanup` runs the reader before `RemoveAll` on each dead session's bus dir; a dir whose processes could not all be proved is left in place with a report
- [ ] The `session-closed` hook invokes a form that does **not** skip its own session (`--session <s>` or a dedicated subcommand), closing sub-defect A; `--all` semantics unchanged
- [ ] The daemon traps TERM/INT and stops its harness and proc children before exiting (Decision 4)
- [ ] The in-place re-exec `upgrade-daemons` requests does not take that path: children keep running across it (Decision 4)
- [ ] `docs/agent-bus.md` (`muxcode cleanup`, `muxcode upgrade-daemons`), `docs/architecture.md` (session re-init) and `CLAUDE.md` describe the sweep, its order, the proof rule and the opt-out

### Phase 5: Integration test

- [ ] Create `scripts/test-orphan-reaper.sh` — hermetic: scratch sessions on a scratch tmux socket, scratch `BUS_SESSION`s, stand-in harness and listener processes that hold their pid records the way the real ones do
- [ ] Dead-session sweep: launch a scratch session with daemon, monitor, a listener stand-in and a harness stand-in; kill the session; run the sweep → all four gone, bus dir removed and still absent after twice the daemon poll, an `orphan-reaped` row naming each
- [ ] Live-session negative control: a second scratch session stays up through the sweep; every one of its processes and its bus dir survive
- [ ] Unproven negative control: a bus dir whose pid record names a pid now held by a sleeper the test started → the sleeper survives, `orphan-unproven` is logged, the dir is left in place
- [ ] Legacy listeners: a stand-in `bash <scratch bus dir>/inbox-listener.sh` for the dead session is reaped with an `orphan-reaped` row naming argv proof; a `bash -c 'while true; …'` loop stand-in survives with `orphan-unproven`; the same script form under the live session survives
- [ ] Order control: in a deliberately mis-ordered run the monitor dies last and the daemon relaunches (the failure the order rule prevents); in the real order it does not
- [ ] Close-hook control: `tmux kill-session` on a scratch session with the fixed hook → its own bus dir is gone with no manual command
- [ ] `ps`-unavailable control: with the census road disabled, the registry road alone still reaps the recorded families and logs `orphan-census-unavailable` for the rest
- [ ] Opt-out control: with `MUXCODE_ORPHAN_REAP=0` a launch beside a dead session reaps nothing and removes nothing
- [ ] Daemon children: TERM a scratch daemon → its harness stand-in is gone before the daemon exits; request an in-place re-exec → the stand-in survives under the same pid
- [ ] `escabs`: a scratch `tmux -L escabs-<pid>` server whose pid is dead is reaped with its own row; one named for a live pid survives
- [ ] Coverage floor: every section above asserts at least once; a skipped section fails the script
- [ ] Run the script and record the pass/fail counts here

## Decisions — the user's, recorded 2026-11-15

Answered by the user in conversation and relayed by edit (`1794787239-edit-1aacf9a4`). Each
subsection keeps the question as it was put; this table is the answer.

| # | Decision | Chosen |
|---|----------|--------|
| 1 | What proves ownership | **Session name in argv** for the daemon, monitor and launch helpers; for every other family **a recorded pid that still names the same process**, so pid reuse is detectable. No proof → no kill, an `orphan-unproven` row. **No environment-probe proof.** *Amended 2026-11-16:* the legacy `inbox-listener.sh` script form, whose path names the session, **is argv proof and is reaped**; the `bash -c` loop form stays unproven |
| 2 | Automatic or opt-in | **Automatic at every session launch, sweeping all dead sessions.** Opt out with `MUXCODE_ORPHAN_REAP=0` |
| 3 | The `escabs-<pid>` test tmux servers | **In scope.** Reaped when the pid in the socket name is dead — its own proof rule and its own lifecycle row |
| 4 | Whether the daemon stops its own children | **Yes, on TERM/INT** (harness, proc children). **Not** on the in-place re-exec `upgrade-daemons` requests |
| 5 | MUX-147's and MUX-156's reaper phases | **They point here.** Each keeps only its family-specific half. Applied 2026-11-15 to both specs and `backlog.md` |

### Decision 1 — what proves ownership, per family

The argv session name is proof for the daemon, monitor and launch helpers; a pid record under the
session's bus dir is proof for the listener, harness, procs, spawns and webhook **if** the record also
lets the reader detect pid reuse. Whether a record without a start time is enough is a Phase 1
finding.

On 2026-11-16 the user extended argv proof to the legacy `inbox-listener.sh` script form: its path
runs through the session's bus directory, which is the same class of evidence as a session name
passed as an argument. Plan had recorded it as unproven and asked; the answer was to reap it.

### Decision 2 — automatic at launch, or opt-in

The manual sweep was never run (evidence row 5), and `upgrade-daemons`' sweep runs only when someone
builds. Automatic with an opt-out is the evidence's answer; the cost is a launch that kills processes
the user did not name, which is why the proof rule and the `orphan-reaped` row exist.

### Decision 3 — are the `escabs` test servers in scope?

They are not session-owned; their socket name carries the launching script's pid, which is a proof
of its own kind. Either the reaper learns that rule, or the scripts' traps are hardened and the
servers are declared out of scope — but not left to default.

### Decision 4 — should the daemon stop its own children on exit?

A TERM handler that calls `StopPromptAgent` and stops proc children closes sub-defect C at the source
and stops `upgrade-daemons` manufacturing orphans. It also changes daemon shutdown semantics for every
caller that kills it today, so it is decided, not assumed.

### Decision 5 — what becomes of MUX-147's and MUX-156's reaper phases

Both propose a daemon-side sweep for their one family. This spec is the sweep for every family. The
clean cut is: their family-specific halves (footprint; listener self-exit and receipt attribution)
stay, their reaper phases point here. Recorded in Phase 2 and applied to both specs and the backlog
on the user's say.

## Out of scope

- **The stray-session cause** — the launcher routing a subcommand call that lost its subcommand into a session launch; guarded in `tools/muxcode/main.go` the same day (uncommitted at filing).
- **MUX-147's footprint half** and **MUX-156's listener-side self-exit and receipt attribution** — related, separately specified.
- **Why three `escabs` servers escaped their `EXIT` trap** — recorded as unestablished; the reaper covers them regardless (Decision 3).
- **`muxcode cleanup --claude`** (Claude Code `/tmp` dirs) — files, not processes; unchanged.

## Status

**Backlog** — 5/52. Parked 2026-11-13 on five open decisions; unparked 2026-11-15 once the user's
Decisions 1–5 were recorded and the criteria and phases rewritten to them (44 items became 50).
Decision 1 was amended by the user on 2026-11-16 — the legacy `inbox-listener.sh` script form is argv
proof and is reaped — which added one criterion and one integration step (50 became 52).
The five ticked items are Phase 2's "decide" steps, ticked as decided — no code exists yet, and
Phase 1 is the open phase.

Filed 2026-09-14 on the user's instruction relayed by edit, from edit's first-hand cleanup of four
stray sessions. Every mechanism claim verified by plan against `3f9a2cb`; three handoff claims
corrected inline (the marker cleaner's location and scope; launch does reap the same-name daemon and
monitor; the close hook exists and skips its own session). Sub-defects A and C, the daemon's missing
signal handler, the per-family proof table and the five `daemon-orphan-killed` rows are plan's
findings, not the report's. Not started.
