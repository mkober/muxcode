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
exists ([MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) — 31 alive at its
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
([MUX-161](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md)) — a `ps`-only reaper inherits
that blindness, which is one reason the pid records above matter.

### Blast radius

- Every `tmux kill-session`, same-name relaunch of a *different* session, or tmux server crash leaks at least two processes per session — daemon and monitor — plus one listener per Claude role and one harness per local role. Evidence row 4 puts it at about once a day for this repo's main session alone; evidence row 3 puts the machine-wide backlog at 19 after four days.
- A dead session's daemon keeps its bus dir alive, keeps attempting delivery, and keeps writing lifecycle rows under a session name that no longer exists; a dead session's listener eats a live role's messages if the name is reused (MUX-156).
- Recovery is by hand — `ps`, read argv, infer ownership, `kill` — and the inference is exactly what this spec says a reaper must not do.

### Family

- [MUX-147](./MUX-147-process-leak-and-memory-footprint.md) — the harness family. Its Phases 2–3 (reaper design; *"may a daemon reap another session's processes?"*) are this spec's Phases 2–3 restricted to one family; its footprint half is untouched here. Since 2026-11-15 its reaper items are ticked there as deferred to this spec (Decision 5).
- [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) — the listener family. Its listener-side self-exit and receipt attribution stay there; its daemon-side census is this reaper.
- [MUX-161](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md) — `ps` is unavailable to a Codex build agent; any reaper on the `ps` road shares it.
- [MUX-008](../backlog/MUX-008-unverified-daemon-auto-restart.md) — the fire-and-hope relaunch; bears on whether a reaped daemon stays reaped.
- [MUX-171](./MUX-171-stall-watchdog-redrive-kills-busy-claude-tool.md) — one way an integration script dies mid-run; whether that is how the `escabs` servers lost their trap is not established.

### Findings at activation (2026-11-16)

Re-read by plan against `00d7ed8`. This spec was verified against `3f9a2cb` on 2026-09-14, and two
months of work landed under it. The three sub-defects stand. Five things in the text above no longer
describe the tree, and three of them change what Phases 1 and 3 have to do. Line references above
this section are the September ones; the ones below are current.

| Spec says | Tree at `00d7ed8` |
|-----------|-------------------|
| Bus directories are `/tmp/muxcode-bus-<s>` (evidence rows 2, 3, 5; the legacy listener's script path) | Since [MUX-208](./MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) they are `$XDG_STATE_HOME/muxcode/bus/muxcode-bus-<s>` (`BusDirBase`), and a `/tmp` one is migrated on first resolution (`MigrateLegacyBusDir`, `bus/busdir_migrate.go:88`). A sweep over "every dead session on the machine" walks `BusDirBase()`, and has to decide what it does with an unmigrated `/tmp` directory. The script-form legacy listener's argv may name either base; the parser takes the session from the `muxcode-bus-<s>` path component wherever it sits. No code in the tree mentions `inbox-listener.sh` — it is a shape only old binaries produced |
| Families table: daemon and monitor have **no** pid record | They now hold kernel record locks — `lock/daemon.owner` and `lock/monitor.owner` under the bus dir (`AcquireInstanceLock`, `bus/pidlock.go:48`; `DaemonOwnerPath` / `MonitorOwnerPath`, `bus/config.go:594`, `:604`; [MUX-161](./MUX-161-upgrade-daemons-ps-blocked-in-codex-sandbox.md)). `LockHolderPid` asks the kernel which process holds one. That is stronger than a pid file: a lock cannot be held by a recycled pid. `DaemonPidPath` (`daemon.pid`, `config.go:582`) exists as a path helper only |
| Phase 3 step 1: "daemon and monitor write a pid record at launch (`daemon.pid`, `monitor.pid`, with start time)" | Largely overtaken by the owner locks. Whether to add pid files anyway or use the lock holder is Phase 1's to record; the lock holder needs no start time to rule out pid reuse |
| The monitor outlives its session and "must die before the daemon" | The monitor now **exits by itself** when `tmux has-session` fails (`cmd/watch.go:116–120`, a `session-gone` row), about every 30 s. So after a session closes the monitor is gone within a check and only the daemon is left — nothing relaunches it, and nothing kills it. Two consequences: the monitor-before-daemon ordering matters only inside that window; and the check is **not** positive absence — any failure of `has-session`, a tmux blip included, ends the monitor of a **live** session, leaving its daemon unsupervised. On 2026-11-16 13:44 the MUX-145 acceptance worker reported exactly that on session `app-portal-services` two minutes after a daemon upgrade (its report, not verified by plan) |
| `upgrade-daemons`' orphan path runs "only from `./build.sh`" and reaps by `ps` | It also runs on attach to a running session, scoped to that session, and since MUX-161 discovers daemons through the owner locks when `ps` cannot run. On that no-`ps` road it **kills nothing**, orphans included — it files restart requests instead (`lock/daemon.restart`), and the daemon re-execs in place. That re-exec is the one Decision 4 says must not stop the daemon's children |
| The daemon has no signal handler and no session-liveness check | Still true: no `signal.Notify` and no `TmuxHasSession` under `daemon/` or in `cmd/watch.go`'s daemon path |
| The Claude inbox listener is an orphan family with no self-exit | [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) shipped on 2026-11-16: a listener whose parent is gone exits on its next tick without consuming, `IsPolling` no longer counts an orphaned holder, and `SetPolling` writes a `polling-<role>.parent` sidecar (`<listener pid> <parent pid>`). The reaper is now the backstop for this family, not the only remedy — and the sidecar is a second fact the reader can check beside the marker's pid |
| Spawn workers are reaped only by `spawn stop` | [MUX-195](./MUX-195-graph-runs-never-reuse-idle-workers.md) reaps an idle worker after `MUXCODE_SPAWN_IDLE_SECS` (`spawn-reaped`) — while its daemon lives. A dead session's workers are still this spec's |
| Sub-defect A: the close hook skips its own session | Still true. `TmuxSetHook(session, "session-closed", …)` at `bus/launcher.go:320`; `cmd/cleanup.go:64` passes the positional as `currentSession`; `shouldClean` at `bus/cleanup.go:196` |
| Other line references | `killStaleProcesses` `launcher.go:518`; `startDetachedProcess` `:539`; `isTmuxSessionAlive` `cleanup.go:269`; `CleanStaleMarkers` `config.go:636`; `RestartDaemon` `daemon_health.go:111`; `StartPromptAgent` `prompt_agent.go:239`, `StopPromptAgent` `:270`; `claimMarker` `notify.go:692`, `terminateMarkerHolder` `:739`; `StartProc` `proc.go:228`, `StopProc` `:423`; `ListDaemonProcs` `upgrade.go:82`, `parseDaemonProcs` `:180`, `UpgradeDaemons` `:286`, `killProcess` `:525`; `StopWebhook` is now `StopWebhookProcess`, `webhook.go:250` |
| `scripts/test-orphan-reaper.sh`; `orphan-reaped` / `orphan-unproven` rows; `MUXCODE_ORPHAN_REAP` | None exists |

**One reading that is plan's, flagged for the user.** Decision 1 names two proofs: the session name
in argv, or "a recorded pid that still names the same process". A kernel-held owner lock is neither in
so many words, but it is the second proof in a stronger form — the kernel, not a file, says which
process it is. Plan reads it as within Decision 1 for the daemon and monitor, where it also covers the
case `ps` cannot run. If the user meant argv **only** for those two families, the no-`ps` road reaps
neither.

## Requirements

### Acceptance criteria

- [x] Closing a session — `tmux kill-session`, a same-name relaunch, or the tmux server exiting — leaves **no process whose bus session is that session** alive past a bounded grace period: daemon, monitor, listeners, harness, proc children, spawn workers, webhook, launch helpers
      — **left open at Phase 5's verification (2026-11-16): two of the three roads are shown, the third is not.** `tmux kill-session`: shown — the global close hook reaps the daemon and removes the bus dir (script section F). A same-name relaunch: shown — the launch sweep reaps the old pair (section D, `PrepareSessionLaunch`). **The tmux server exiting: not met as worded and not tested.** No hook runs when the server dies, so the daemon and its harness stay alive — Phase 1 measured exactly that — until the *next* launch on the machine, or a `muxcode cleanup`, sweeps them. That is bounded by an event, not by a grace period, and no section of the script kills a server and then sweeps. Either a section shows the next launch reaping a server-killed session's daemon and harness and this wording is accepted as "bounded by the next launch", or something else has to notice a dead server.
      **Ticked 2026-11-16 19:3x:** the acceptance worker took the second course — the daemon now notices its own session gone and reaps it (`checkSessionGone`) — and script section J shows all three roads leaving nothing alive within 30 s, the server kill among them (56 passed, 0 failed, floor 56)
- [x] The `session-closed` hook, or its replacement, removes **the closing session's own** artifacts — sub-defect A closed, with a negative control proving it does not touch a live session's
- [x] Process cleanup precedes file cleanup, and the monitor stops before the daemon — a reaped session's bus dir does not reappear (sub-defect B)
- [x] A daemon sent TERM or INT stops its children — harness and proc children — before it exits; a daemon that dies without the chance (`kill -9`) leaves a harness the reaper finds by its pid record (sub-defect C, Decision 4)
- [x] **Negative control:** a daemon re-exec'ing in place for `upgrade-daemons` does **not** stop its children — the harness survives the re-exec under the same pid (Decision 4)
- [x] Ownership is proved before any kill: by the session name in argv (daemon, monitor, launch helpers, and the legacy `inbox-listener.sh` script form, whose path names it), or by a pid record under that session's bus dir whose pid still names the same process (every other family) — **never by bare process name, never by an environment probe**, and never while the session's liveness is unknown (the `isTmuxSessionAlive` positive-absence bar) (Decision 1)
- [x] A live session's processes are never reaped: the integration test runs a scratch live session beside the dead one and proves every one of its processes survives the sweep
- [x] Every kill writes a lifecycle row naming session, family, pid and the proof used (`orphan-reaped`); a refusal for want of proof writes `orphan-unproven`
- [x] A legacy `inbox-listener.sh` listener whose script path names a dead session is reaped on argv proof; the `bash -c while true …` loop form, which names no session, is left alone and logged `orphan-unproven` (negative control) (Decision 1 as amended 2026-11-16)
- [x] Reaping at launch covers **every** dead session on the machine, not only the name being launched — `killStaleProcesses` becomes the family sweep or is subsumed by it
- [x] `muxcode cleanup` reaps a dead session's processes before removing its files, or reports what it could not prove and leaves both in place
- [x] The sweep runs automatically at every session launch; `MUXCODE_ORPHAN_REAP=0` disables it, and with it set a launch reaps nothing (Decision 2)
- [x] An `escabs-<pid>` test tmux server is reaped when the pid in its socket name is dead, with its own lifecycle row; one whose pid is alive survives (Decision 3)
- [x] `scripts/test-orphan-reaper.sh` exercises the sweep hermetically with a coverage floor

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

- [x] On a scratch session, enumerate every process the launcher and daemon start, with ppid, pgid, `Setsid`/`Setpgid`, argv and environment session — the families table above is the hypothesis; the census is the fact
- [x] Kill the scratch session three ways (`tmux kill-session`; same-name relaunch; `kill -9` the tmux server) and record which processes survive each, and whether the `session-closed` hook fired (a `session-cleanup` lifecycle row) — survivors recorded; whether the hook fired could **not** be told, since it logs nothing when it skips its own session
- [x] Confirm sub-defect A empirically: after `kill-session`, the scratch session's own bus dir is still present
- [x] Confirm sub-defect B: delete the dead session's bus dir and time its reappearance, naming the writer; kill the daemon alone and time its relaunch by the monitor — the measurement was made and B's reappearance was **not reproduced** (no traffic to the idle orphan, so no writer named); the monitor relaunch was timed at ~32 s and ~48 s. Ticked as measured, not as confirmed
- [x] Confirm sub-defect C: kill the daemon; the harness's ppid becomes 1 and `harness-prompt.pid` still names it
- [x] Record whether `muxcode launch --resize` and `--auto-accept` exit on their own when the session goes
- [x] Record which pid records exist per family and whether each carries enough to detect pid reuse (start time, argv)
- [x] Record the findings here before choosing an option

### Results for Phase 1

Recorded 2026-11-16 from the worker's **revised** findings file
(`/tmp/mux184-phase1-findings-v2.md`), which replaced the first after review `1794870355` sent the
census back. No code changed: this phase is a census. Plan did not re-run it, but read v3's raw
output (79 lines) before ticking and found it consistent with the tables below. Verified and ticked
2026-11-16: no source file changed; `./build.sh` exit 0 at 18:11:56, `./test.sh` exit 0 at 18:12:13,
review at 18:12:52 with 0 must-fix, 0 should-fix, 1 nit (`/tmp/muxcode-review-1794870737.txt`). The
nit, recorded in the survivors table's note: `edit`'s listener had no marker record on the relaunch
and server-kill roads, so the listener rows there rest on `build`'s listener alone.

**Sources — what was measured and what was only reported.**

| Source | Standing |
|--------|----------|
| Census v3 — `/tmp/mux184-p1-census3.sh`, run task `1794870519-spawn-926e95e5-24647fe5`, exit 0, output `/tmp/mux184-p1-census3.txt` | **Measured, authoritative.** Tree-built muxcode at `00d7ed8`, a private tmux server, scratch HOME and state; nothing left running afterwards. Every family is tracked by its **recorded** pid — daemon and monitor by the kernel holder of `lock/<x>.owner`, the harness by `harness-prompt.pid`, each listener by `polling-<role>.marker`, each agent by its pane's child, a spawn worker by its window's pane — with liveness and ppid printed before and after each kill |
| Census runs 1 and 2 — tasks `1794868620-…-f7df428b`, `1794869432-…-8438a4e9` | **Superseded.** Their harness variable was empty (an env-match bug in the script), so their harness rows proved only that a pid was recorded, not that the process survived. Kept for two measurements v3 did not repeat: the launch helpers, and sub-defect B |
| Four harness pids (76780, 96550, 95235, 25598) alive at ppid 1 | **Reported**, not census: seen by edit's own `ps` after run 2. v3 replaces them |
| Stand-ins | The harness is a stand-in that writes `harness-prompt.pid` and loops — the real one exits at once with no model backend under a scratch HOME. The agents are the claude-stub, which supervises a real `muxcode inbox --poll --loop`; they are not real Claude |

**Census at launch.**

| Family | Pid record | ppid | pgid |
|--------|------------|------|------|
| Daemon | `lock/daemon.owner` (kernel holder) | 1 | own |
| Monitor | `lock/monitor.owner` (kernel holder) | 1 | own |
| Harness | `harness-prompt.pid` | **the daemon** | own (`Setpgid`) |
| Agent (claude-stub) | none — the pane's child | the pane's shell | own |
| Listener | `polling-<role>.marker` + `polling-<role>.parent` | **the agent** | **the agent's** |
| Spawn worker | `spawn.jsonl` — a window name, **no pid field** | the spawn pane's shell | own |
| Launch helpers (`--resize`, `--auto-accept`) | none | — | gone 40 s after launch (runs 1–2) |

**How the agents were started, and what it means for Phase 5.** `SetupPath` (`bus/launcher.go:176`)
puts `/opt/homebrew/bin` first on darwin, so under a plain `muxcode launch` the panes run the real
`claude` and `codex`, not a stub on a scratch `PATH` — which is what runs 1 and 2 hit. v3 got round it
by respawning each agent pane with the scratch `PATH` exported and typing `muxcode agent launch
<role>`. Phase 5's script needs the same seam, or stand-in processes as its first step already says.

**Survivors per kill road.**

| Road | Daemon | Monitor | Harness | Agents and listeners | Spawn worker |
|------|--------|---------|---------|----------------------|--------------|
| `tmux kill-session` | **Alive**, ppid 1 | Gone — a `session-gone` row | **Alive**, still a child of the orphaned daemon | Gone | Gone |
| Same-name relaunch (kill the session, then launch that name; launching a name whose session is live only attaches) | Gone — `killStaleProcesses` | Gone | **Alive, ppid 1** — orphaned by the relaunch | Gone | Not run |
| `kill -9` of the tmux server | **Alive**, ppid 1 | Gone | **Alive**, a child of the orphaned daemon | Gone | Not run |

So after any close, two processes are left: the daemon and its harness. That matches the 2026-09-14
field evidence — daemons at ppid 1 and fourteen harnesses. The launch helpers exit by themselves and
are not an orphan family on this evidence.

"Agents and listeners: gone" rests on both roles for `kill-session`, and on `build` alone for the
other two roads: on those, `edit`'s `polling-edit.marker` did not exist when the census read it, so
its listener was never tracked (`edit`'s agent process was, and died).

The listeners died with their agent's **process group**: there was no `listener-orphan-exit` row, so
it was pane death (SIGHUP to the group) that removed them on these roads, not MUX-156's self-exit.
The orphaned listeners of September came from other roads — a detached listener, a relaunch cycle —
which this census did not reproduce.

**The sub-defects.**

| | Result |
|---|--------|
| A — the close hook skips its own session | **Measured.** After `kill-session` the session's own bus dir is still present, with zero `session-cleanup` rows. Whether the hook *fired* cannot be told: its `cleanup <s>` skips its own session and logs nothing for the skip, so firing and not firing look the same |
| B — file cleanup is undone | **Not reproduced** (runs 1–2) — recorded as not reproduced, not as refuted. With the bus dir deleted, an idle orphaned daemon did not recreate it in 40 s; the `MkdirAll` writers sit on the send and delivery roads, which an idle orphan does not exercise, so the 2026-09-14 reappearance needs traffic. The other half was measured: a monitor relaunches a TERMed daemon while the session lives in about 32 s and 48 s |
| C — the daemon orphans its harness | **Measured** (v3). TERM kills the daemon at once; the harness, tracked by the pid in `harness-prompt.pid`, goes from ppid = the daemon to ppid 1 and stays alive, and is still alive after its session is killed too. A same-name relaunch orphans the first instance's harness the same way |

**Pid records per family — can each detect pid reuse?**

| Family | Record | Reuse detectable? |
|--------|--------|-------------------|
| Daemon, monitor | `lock/<x>.owner`, a kernel-held record lock | **Yes** — a lock cannot be held by a recycled pid. No pid file is needed; Phase 3's first step is overtaken, as the activation findings expected |
| Headless harness | `harness-<role>.pid`, a bare pid | **No** — needs an identity token (Phase 3) |
| Webhook | Its pid file, a bare pid | **No** — the same |
| Claude listener | `polling-<role>.marker` (pid) and `polling-<role>.parent` (`<pid> <ppid>`) | Partly — the parent pid is a second fact, but there is no start time |
| `muxcode proc` children | `proc.jsonl`: pid, `started_at`, command | **Not as recorded.** `started_at` is `time.Now().Unix()` taken by `StartProc` after `cmd.Start` (`bus/proc.go:271`) — application wall time at one-second resolution, not the OS's start time for the process. Comparing the two needs a tolerance and a match rule Phase 3 has to define before it is authority to kill. The first findings file called this "yes"; review corrected it |
| Spawn workers | `spawn.jsonl`: a window name, **no pid** | No pid at all — but the worker **died with its pane** on `kill-session` (v3), which was the unmeasured question |

**Four points the worker raised for the user**, as it put them — refinements to the decisions, not
worked around. Plan's note on each:

| # | The point | Plan's note |
|---|-----------|-------------|
| 1 | Harness and webhook records carry no identity token, and proc's `started_at` is wall time, not an OS start token — so Decision 1's "recorded pid that still names the same process" needs Phase 3 to define the token and the match rule | Inside the decision as recorded — the proof table already says "where it does not, Phase 3 adds it". What is new is that **proc children** need it too; the table above had them as already covered. Phase 3 defines one token for all three families |
| 2 | Spawn workers have no pid record, but died with their pane on `kill-session` | Measured on one road only (`kill-session`); the relaunch and server-kill roads were not run for a worker. On that evidence the family needs no reaper for a closed session and the proof table's "spawn workers" entry describes a record that does not exist. Phase 3 should either run the other two roads and drop the family from the reader, or add a pid to `spawn.jsonl` — and say which |
| 3 | The monitor already exits on its own after a close, so "monitor before daemon" matters only inside its ~30 s check window | Agrees with the activation findings. The ordering criterion still stands for that window, and for `muxcode cleanup` run by hand right after a close |
| 4 | A same-name relaunch orphans the first instance's harness through `killStaleProcesses` — the launch-time road this spec extends is itself a sub-defect C trigger until the daemon stops its children on TERM | Decision 4 (the TERM/INT handler) is what closes it, so Phase 4's handler step is a precondition for Phase 3's launch-time sweep being safe, not a later nicety. The phases may need to land in that order, or together |

**Phase 1's steps against what was measured.**

| Step | Standing |
|------|----------|
| Enumerate every process the launcher and daemon start | Measured (v3) — every family, agents, listeners and a spawn worker included |
| Kill the session three ways and record survivors | Measured (v3) for daemon, monitor, harness, agents and listeners on all three roads; the spawn worker on `kill-session` only. Whether the close hook fired could not be told |
| Confirm sub-defect A | Measured |
| Confirm sub-defect B | **Half.** The monitor relaunch is measured; the bus dir reappearing was not reproduced — it needs traffic. Phase 5's order control exercises the relaunch half; nothing yet exercises the reappearance |
| Confirm sub-defect C | Measured |
| Whether the launch helpers exit by themselves | They do — gone 40 s after launch (runs 1–2) |
| Which pid records exist and whether each detects reuse | Recorded above, with the proc correction |
| Record the findings here | This section |

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

- [x] Daemon and monitor write a pid record at launch (`daemon.pid`, `monitor.pid`, with start time) — the two families with no record today — **overtaken, not implemented:** since MUX-161 both hold a kernel owner lock, which is a reuse-proof record already; no pid file is written (see Results for Phase 3)
- [x] One reader over all records of a bus dir: verifies each pid still names the recorded process, then TERM → grace → KILL in the order monitor, daemon, the rest
- [x] Argv census for the argv-carrying families — daemon, monitor, launch helpers and the legacy `inbox-listener.sh` script form, its session parsed from the `muxcode-bus-<s>` component of the script path — on the `ListDaemonProcs` road, gated by `TmuxHasSession` positive absence; when `ps` is unavailable it refuses that road and logs `orphan-census-unavailable` (MUX-161)
- [x] Launch-time sweep over every dead session on the machine, replacing or subsuming `killStaleProcesses`; `MUXCODE_ORPHAN_REAP=0` honoured — replaced: `killStaleProcesses` is deleted and a launch is refused while an unproven same-name daemon survives, which is more than this step asked and is with the user (Results for Phase 3)
- [x] `escabs-<pid>` rule: a test tmux server whose socket-name pid is dead is killed and logged with its own row; a live pid's server is left alone
- [x] `orphan-reaped` / `orphan-unproven` lifecycle rows per pid, naming session, family and proof
- [x] Unit tests with negative controls: a record naming a pid now held by an unrelated process → refused; an argv-less process with no record → refused, `orphan-unproven`; a script-form legacy listener naming a dead session → reaped, the loop form → refused, and a script path that parses to no session → refused; a session the server lists → nothing killed; a probe that failed → nothing killed; an `escabs` socket whose pid is alive → nothing killed

### Results for Phase 3

Recorded 2026-11-16 from the worker's notes (`/tmp/mux184-phase3-notes.md`); plan read the file and
function lists of the new code and the launch call site, not every line of the reaper. Uncommitted.
At the time of each recording the worker had only type-checked and vetted both modules; the tests
were first run by the graph's test node. Verified and ticked 2026-11-16 once all three had run after
the last edit (18:37:12): `./build.sh` exit 0 at 18:38:29, `./test.sh` exit 0 at 18:40:01 (both
modules passed, the fourteen reaper tests among them), review at 18:42:02 with 0 must-fix,
0 should-fix, 0 nits (`/tmp/muxcode-review-1794872405.txt`) — its third pass, after four must-fixes
and then two more. The graph's phase report still described the first cut (the default-socket
fallback, the 3 s proc match, `killStaleProcesses` kept, nine tests); what was verified is the tree
after both rounds. This section is in the order the work happened: the first two tables are the first
cut, and the two "review fixes" tables override them where they differ.

**The registry — `bus/owner_lock.go`.**

| Item | Result |
|------|--------|
| What it is | A directory of kernel record locks, `<bus>/lock/owners/`, one file per process: `<family>.<role>.<pid>.owner` (role `-` when there is none). `HoldOwnerLock` takes the lock and holds it for the life of the process |
| Who holds one | The inbox listener (`cmd/inbox.go`), the webhook (`ServeWebhook`) and the headless harness. The harness is a separate Go module that cannot import `bus`, so it carries **its own copy** of the lock code (`tools/muxcode-llm-harness/harness/owner_lock.go`); `TestOwnerLockName` pins the file-name layout the two must agree on |
| The proof | The kernel's answer to "which process holds this lock" (`LockHolderPid`, MUX-161) — it cannot be a recycled pid, and it needs no `ps` |
| Phase 3 step 1 | **Not implemented, overtaken.** No `daemon.pid` or `monitor.pid` is written; the reader uses the daemon's and monitor's existing owner locks |

**The reaper — `bus/reaper.go`** (`ReapOrphans`, `SweepOrphansAtLaunch`, `OrphanReapDisabled`).

| Item | Result |
|------|--------|
| Is the session dead? | Positive absence on the tmux server the session ran on: `list-sessions`, with "no server running" and a missing or refused socket read as an empty server. Anything else — "Operation not permitted", for instance — is **unknown**: nothing is reaped and one `orphan-sweep-skipped` row is written |
| Records of a dead session | The holders of `daemon.owner` and `monitor.owner`; the holders of every lock under `lock/owners/` (a file nobody holds is removed); and running `proc.jsonl` entries that pass the match rule below |
| The proc match rule | *First cut, removed by review fix 3 below:* `procRecordMatches` matched the OS start time to within 3 s before and 1 s after `started_at`, plus an argv of `sh -c …<command>`. A proc is now proved by an owner lock like every other family |
| The argv census (`ps`) | Daemon, monitor and launch helpers of dead sessions **that have a bus dir under this installation's bases**, so a test's scratch daemon is never treated as ours. A legacy `inbox-listener.sh` whose path's parent is one of those bases is argv proof and is reaped; any other script path is unproven. Among processes at ppid 1 only: the `bash -c` loop form, a harness and an `inbox --poll` listener that no owner lock of **any** session accounts for are logged `orphan-unproven` and left |
| `escabs` | A `tmux -L escabs-<pid>` server whose pid is dead is killed with its own row, `escabs-reaped`; one whose pid is alive is left |
| Order and kill | Monitor, then daemon, then the rest; TERM, 2 s, KILL. *The first cut's notes said each target was rechecked just before its kill. Review found that was not true of every target; the recheck that now exists is review fix 2 below* |
| Rows | `orphan-reaped session= family= pid= proof=`; `orphan-unproven … reason=`; `orphan-census-unavailable` when `ps` is refused — the argv census and the proc-record proof are then skipped and the owner-lock road still runs |
| At launch | `LaunchSession` calls `SweepOrphansAtLaunch` where `killStaleProcesses` ran (`bus/launcher.go:237–238`), before the session exists, so the name being relaunched is swept too — its old harness included. `killStaleProcesses` stays directly after it as a backstop, for a sweep that could not judge or was switched off: the new daemon cannot take its instance lock while an old one holds it |
| The opt-out | `MUXCODE_ORPHAN_REAP=0` skips the sweep: nothing beside the launch name's own daemon and monitor is reaped |
| Not covered | Spawn workers — no record, and they died with their pane in the census (one road measured) |
| Tests | `bus/reaper_test.go`, ten tests: order and rows on the owner-lock road with `ps` refused, against real child processes holding real locks; a live session, another server's socket, an unrecorded socket seen from a private server and a failed probe each kill nothing; a matching recorded socket is judged; proc records — a reused pid refused, a matching one killed as a group, `ps` refused → none; the census — argv daemon, monitor, helper and script listener reaped, the loop form, a foreign script path and a lockless orphan harness unproven, a live-parented harness and a scratch daemon untouched; `escabs` dead and alive; the opt-out; the lock name; the socket record |

**Review fixes — four must-fixes from review `1794871484`, all in this phase.** Recorded 2026-11-16
from the worker's fix notes (`/tmp/mux184-phase3-fix-notes.md`). Where a row above disagrees, these
are current.

| # | Must-fix | What changed |
|---|----------|--------------|
| 1 | **Provenance** — the reaper assumed the default tmux server when a session had recorded no socket | A session absent from the server is dead **only** when its `<bus>/tmux.socket` names this server. No record, an unreadable record, or another server's socket → its processes are logged `orphan-unproven`, with the reason, and never killed. The "assume the standard default socket" fallback is gone |
| 2 | **Rechecks** — not every target was re-proved before its kill | Every candidate carries one proof function: its owner lock still held by that pid; or its argv re-read (`ps -p <pid>`) still naming the same family and session; or, for `escabs`, the argv and the socket pid still dead. Each also re-asks tmux that the session is still absent where it ran. The proof runs **before TERM and again before KILL**. Failing before TERM: not signalled, `orphan-unproven`. Failing after TERM: the KILL is withheld, `orphan-kill-failed` |
| 3 | **Proc identity** — the time-window and substring match was not proof | Removed. `StartProc` now launches every proc through `muxcode proc exec-held <owners-dir> <id> -- sh -c …` (`ExecHoldingOwnerLock`, `bus/proc_owner.go`): it takes `lock/owners/proc.<id>.<pid>.owner` on a descriptor left open across exec, then execs the command, so the recorded pid **is** the kernel lock's holder — the same proof as every other family. Killed by process group. A `proc.jsonl` entry with no such lock (one started before this change, or by a non-muxcode caller) is unproven and never matched |
| 4 | **Outcomes** — a kill was counted as done when the signal was sent | `terminate()` reports "reaped" only once the pid is confirmed gone. A signal error or a survivor is `orphan-kill-failed` with the reason. A surviving monitor holds back that session's daemon and everything after it; a surviving daemon holds back the rest — not signalled, reason "held back". `DryRun` returns the plan in kill order and sends and logs nothing |

Tests after the fixes (`bus/reaper_test.go`): real processes and real signals on the owner-lock road,
five families including a proc killed as a group, each confirmed gone; a real exec-held proc keeps its
lock across exec and is reaped, a lockless proc record is unproven and survives; provenance — a listed
session, another server, no record, an unreadable record and a failed probe each kill nothing, and a
recorded default socket seen from the default server is killed; rechecks — argv changed after the
census, a session relaunched before the kill (no signal), argv changed during the TERM grace (TERM
only, KILL withheld); outcomes — a surviving monitor holds back the daemon and a helper, EPERM,
working signals, the dry run. Type-checked by run task `1794871853-spawn-926e95e5-2afd1590`; not yet
run at the time of recording.

**Second round of review fixes — review `1794872030`.** Recorded 2026-11-16 from
`/tmp/mux184-phase3-fix2-notes.md`. These override the "At launch" and "The opt-out" rows of the
reaper table above.

| # | What changed |
|---|--------------|
| 1 | **Whole-group termination.** A proc is reaped only once its whole process group is gone (`reaperGroupAlive`, `kill(-pgid, 0)`). Its owner lock dies with the group's leader, so after TERM the group itself is the anchor — a process-group id is not reused while any member lives — and the group is watched from TERM on. A group still alive before KILL is the one the lock proved, so KILL needs only the session's absence re-asked. A group that outlives KILL is `orphan-kill-failed`, "process group <pgid> survived KILL" |
| 2 | **`killStaleProcesses` is deleted** — the name-matched `pkill` is gone. `LaunchSession` calls `PrepareSessionLaunch`: the sweep runs, and then **the launch is stopped** (`ErrLaunchBlocked`, a `launch-blocked` row naming each survivor and why) while any daemon or monitor of that name survives it — an owner-lock holder, or a `muxcode watch <name>` in the process list. A proven same-server pair is reaped and the launch proceeds. A same-name daemon on another tmux server, one of unknown provenance, or one whose monitor survived is left running and the launch refuses |
| 3 | **Docs now wrong, for Phase 4:** the launcher's `stale-kill` lifecycle row no longer exists. `docs/agent-bus.md` (the lifecycle events table) and `docs/architecture.md` (`LaunchSession` events) should drop it, add `launch-blocked`, and add the reaper's rows — `orphan-reaped`, `orphan-unproven`, `orphan-kill-failed`, `orphan-census-unavailable`, `orphan-sweep-skipped`, `escabs-reaped` |

Tests added: a real proc tree whose leader exits on TERM while its child ignores TERM — reaped only
after the group KILL, the group confirmed empty, the signals exactly `-pgid` TERM then `-pgid` KILL; a
group surviving KILL → failed, naming the group; `PrepareSessionLaunch` — a same-name daemon on another
server, one of unknown provenance, a surviving monitor (only the monitor signalled), a proven
same-server pair replaced, and the opt-out with no survivor (proceeds) and with one (blocked).
Type-checked by run task `1794872233-spawn-926e95e5-0bb60261`; not yet run at recording.

**This changes what a relaunch does — a user-visible regression risk, not decided by the user.**

| Before | After |
|--------|-------|
| Launching a name whose old daemon was still alive: `killStaleProcesses` killed that daemon and monitor by name and the launch went ahead | The sweep reaps them only if it can **prove** them — the daemon recorded a tmux socket and that socket names this server. Otherwise they are left alone and **the launch is refused**, the error naming the pid to stop by hand |
| `MUXCODE_ORPHAN_REAP=0`: the same-name daemon was still killed by the `pkill` | Nothing is reaped, so a surviving same-name daemon **blocks the launch** |

Who meets it: anyone relaunching a session name whose previous daemon was started before this change
and never restarted — it recorded no socket. `./build.sh` runs `upgrade-daemons`, which restarts live
daemons (they then record a socket) and kills orphaned ones when `ps` is available; after that the
refusal should be rare. Until then, and on any machine that upgrades without that step, a relaunch
that worked yesterday stops with an error. Safer than killing on a name match — the case it guards
against is a same-name daemon belonging to a different tmux server — but nobody asked for a launch to
be refusable, and Decision 2's opt-out now has the opposite effect on this one road.

*Measured on this machine, 2026-11-16 18:4x (edit's note, re-checked by plan):* all five bus
directories under the state base — `dps-data-services-pipelines`, `harley-ai`,
`is-admissions-gateway`, `muxcode`, `tibo`, the five live sessions — already hold a `tmux.socket`
naming `/private/tmp/tmux-501/default`. The Phase 3 build ran `upgrade-daemons`, which restarted
their daemons onto the new binary, and each recorded its socket at start. So here, today, no live
session's daemon is unproven and none would block its own relaunch. The risk is real for a daemon
that was already orphaned before the upgrade, and for a machine that takes the new binary without the
restart.

**Two consequences of the fixes worth the user's eye.**

- **Fix 1 means sessions that exist today are never reaped automatically.** A dead session is judged
  only if its daemon recorded a socket, and only daemons started after this change do. Every session
  whose daemon predates it and is never restarted is reported `orphan-unproven`, not cleaned up. The
  backlog of orphans already on a machine is therefore **not** cleared by shipping this; `muxcode
  upgrade-daemons` restarting a live daemon is what gets its socket recorded.
- **Fix 3 changes how every `muxcode proc` starts**, not only how one is reaped: each now runs through
  an extra `muxcode proc exec-held` step that takes a lock and then execs the command. That is a new
  hop on a road the run and watch agents use constantly, and it was added inside a reaper phase.

**Three things here go beyond what was decided — for the user.**

| | What was built | Against the decisions |
|---|----------------|----------------------|
| 1 | **Kernel owner locks as the proof for the listener, harness and webhook**, not a pid file with an identity token | Decision 1 says "a recorded pid that still names the same process". Plan had flagged, at activation, a reading that a kernel lock is that proof in a stronger form for the daemon and monitor, and asked. The worker built on that reading and **extended it to three more families** before the user answered. It is the more robust design — no token to define, no `ps` needed — but it is a different mechanism from the one the decision's words describe, and it puts a second copy of the lock code in the harness module |
| 2 | **A tmux-socket guard** (`bus/tmux_socket.go`): a session is judged dead only by the server it ran on — the socket its daemon recorded in `<bus>/tmux.socket`, refreshed at every daemon start, else the standard default socket `/tmp/tmux-<uid>/default` | **Not in the spec at all; the worker flagged it.** It exists because a launch from inside a test's private tmux server would otherwise read every real session as absent and reap it. It narrows the reaper — it can only make it kill less — and it is the same class of rule as the positive-absence bar. The cost, **wider since review fix 1** removed the default-socket fallback: a session whose daemon never recorded a socket is never judged on *any* server, so its orphans are never reaped |
| 3 | ~~The proc match rule's tolerance — 3 s before, 1 s after, plus the argv~~ | **Superseded by review fix 3.** There is no tolerance any more; a proc is proved by an owner lock. What replaces it as a point for the user is the new launch hop, above |

**Blast radius, stated once.** This phase adds code that runs at **every session launch** and sends
TERM and KILL to processes on the user's machine. Every proof on the kill path is positive — a held
lock, a matching record, an argv naming a session this installation owns and its server reports
absent — and each is rechecked immediately before the kill. `MUXCODE_ORPHAN_REAP=0` turns it off.

### Phase 4: Close hook and cleanup ordering

- [x] `muxcode cleanup` runs the reader before `RemoveAll` on each dead session's bus dir; a dir whose processes could not all be proved is left in place with a report
- [x] The `session-closed` hook invokes a form that does **not** skip its own session (`--session <s>` or a dedicated subcommand), closing sub-defect A; `--all` semantics unchanged
- [x] The daemon traps TERM/INT and stops its harness and proc children before exiting (Decision 4) — the harness; the daemon has no proc children to stop (the CLI starts procs). The handler ends in `os.Exit` and has no unit test: its proof is Phase 5's
- [x] The in-place re-exec `upgrade-daemons` requests does not take that path: children keep running across it (Decision 4) — true by construction (`syscall.Exec`, no signal); **not yet demonstrated**, Phase 5's daemon-children step does that
- [x] `docs/agent-bus.md` (`muxcode cleanup`, `muxcode upgrade-daemons`), `docs/architecture.md` (session re-init) and `CLAUDE.md` describe the sweep, its order, the proof rule and the opt-out

### Results for Phase 4

Recorded 2026-11-16 from the worker's notes (`/tmp/mux184-phase4-notes.md`); plan read the cleanup
CLI, the daemon's signal handler and the hook line, not every changed file. Uncommitted. When
recorded, the worker had type-checked and vetted both modules (run task
`1794872877-spawn-926e95e5-f2045870`); the tests first ran in the graph's test node. Verified and
ticked 2026-11-16 once all three had run after the last edit (18:54:55): `./build.sh` exit 0 at
18:55:16, `./test.sh` exit 0 at 18:56:50 (both modules passed), review at 18:57:20 with 0 must-fix,
0 should-fix, 0 nits (`/tmp/muxcode-review-1794873414.txt`) — its second pass on this phase.

| Step | Result |
|------|--------|
| Cleanup reaps first | `CleanupStale` and the new `CleanupSession` share `cleanupStale` (`bus/cleanup.go`). Before any removal, `reapBeforeRemoving` runs `ReapOrphans` scoped to the sessions being cleaned (`ReapOptions.Sessions` — no `escabs` servers and no session-less orphans from this road). Each bus dir whose session still has a live process (`sessionProcesses`: owner-lock holders of every family, plus argv when `ps` runs) moves to `result.Held`: kept, and reported as "Left N bus directory(s) in place …". A dry run plans the reap ("Would reap"), sends no signal and logs nothing. The daemon's disk-pressure cleanup takes the same road |
| The close hook — sub-defect A | `muxcode cleanup --session <s>` → `CleanupSession`: that session alone, its own name included, once tmux no longer lists it. Rows `session-cleanup` (removed) or `session-cleanup-held`. The launcher's `session-closed` hook now runs that form (`bus/launcher.go:321–322`). `--all` is unchanged |
| The daemon on TERM/INT — Decision 4 | `trapShutdown` (`daemon/daemon.go`): stops the prompt harness this run started through its own process handle (`StartPromptAgentProcess` / `StopOwnChild` — reuse-safe, and it covers the window before the harness has taken its owner lock), and any harness its owner locks prove (`StopSessionHarness`); TERM, 1.5 s, KILL; logs `daemon-stopped`; releases its locks; exits. A mutex and a `stopping` flag keep `checkPromptAgent` from starting a harness after the stop began |
| "…and proc children" | **Narrower than Decision 4's words.** The daemon starts no procs — only the CLI calls `StartProc` — so it has no proc children to stop, and a daemon TERM leaves agents' background procs running. They are reaped when their session is dead, by their owner lock, like any other family |
| Re-exec keeps children | `ReexecForRestart` is `syscall.Exec` with no signal, so the handler never runs and the harness survives under the same daemon pid. **Not yet proved** — Phase 5's daemon-children section is where it is tested |
| Docs | `CLAUDE.md`: a new "Orphan reaper" bullet under Key constraints (the worker). `docs/agent-bus.md`: `muxcode cleanup` rewritten (both forms, processes before files, the held report, dry run), a harness bullet under `muxcode upgrade-daemons`, and the lifecycle event catalog — `stale-kill` replaced by `launch-blocked`, plus six `reaper` rows, `session-cleanup-held`, `daemon-stopped` and `harness-stop-failed`. `docs/architecture.md`: a new subsection under Session re-init, "What a closed session leaves behind, and the orphan reaper" — the proof rule, the socket record, the order, when it runs, and that a launch can be refused; the instrumented-components table. `docs/configuration.md`: `MUXCODE_ORPHAN_REAP` (plan) |
| Tests | `bus/cleanup_reap_test.go`: `CleanupSession` reaps the closed session's own daemon, then removes its bus dir, with a `session-cleanup` row; an unproven daemon keeps its dir (held, `session-cleanup-held`, the report text) and a live session is untouched; `CleanupStale` reaps a stale harness before removing, and a dry run plans without signalling; `StopSessionHarness` stops a lock-proven harness and never signals a pid named only by `harness-<role>.pid`; `StopOwnChild` stops a running child and reads a finished or absent one as gone. **The TERM handler itself is not unit-tested** — it ends in `os.Exit` — and is left to Phase 5 |

**Review fixes, same phase.** Recorded 2026-11-16 from the worker's request; type-checked by run task
`1794873273-spawn-926e95e5-8e7c34bd`.

| # | What changed |
|---|--------------|
| 1 | Cleanup now holds a session the reaper left **unresolved** in any way — any failed or unproven outcome for it (a live proc record with no lock, a proc group that outlived its leader and KILL), not only a process still alive behind a lock or an argv. The first cut would have deleted such a session's files with a process of it still running |
| 2 | A held session keeps **all** its artifacts — the bus dir, its spawn worktrees in both layouts, and its trigger and preview files — each mapped to its session. The first cut held only the bus dir |
| Test | `TestCleanupHoldsAnUnresolvedSessionWhole` (`bus/cleanup_reap_test.go`): a real lockless proc; a real leaderless process group with the KILL withheld; and beside each, a clean dead session that loses all three kinds of artifact |
| Docs | `docs/agent-bus.md` `muxcode cleanup`: the held row now lists the three ways a session is left unresolved and says the whole session is held (plan) |

The docs describe Phase 3's three unanswered choices as built: the owner-lock proof, the socket rule
and the refusable launch. If the user reverses any of them, the new architecture subsection, the
`launch-blocked` row and the `MUXCODE_ORPHAN_REAP` entry change with it.

### Phase 5: Integration test

- [x] Create `scripts/test-orphan-reaper.sh` — hermetic: scratch sessions on a scratch tmux socket, scratch `BUS_SESSION`s, stand-in harness and listener processes that hold their pid records the way the real ones do
- [x] Dead-session sweep: launch a scratch session with daemon, monitor, a listener stand-in and a harness stand-in; kill the session; run the sweep → all four gone, bus dir removed and still absent after twice the daemon poll, an `orphan-reaped` row naming each
- [x] Live-session negative control: a second scratch session stays up through the sweep; every one of its processes and its bus dir survive
- [x] Unproven negative control: a bus dir whose pid record names a pid now held by a sleeper the test started → the sleeper survives, `orphan-unproven` is logged, the dir is left in place
- [x] Legacy listeners: a stand-in `bash <scratch bus dir>/inbox-listener.sh` for the dead session is reaped with an `orphan-reaped` row naming argv proof; a `bash -c 'while true; …'` loop stand-in survives with `orphan-unproven`; the same script form under the live session survives
- [x] Order control: in a deliberately mis-ordered run the monitor dies last and the daemon relaunches (the failure the order rule prevents); in the real order it does not
- [x] Close-hook control: `tmux kill-session` on a scratch session with the fixed hook → its own bus dir is gone with no manual command
- [x] `ps`-unavailable control: with the census road disabled, the registry road alone still reaps the recorded families and logs `orphan-census-unavailable` for the rest
- [x] Opt-out control: with `MUXCODE_ORPHAN_REAP=0` a launch beside a dead session reaps nothing and removes nothing
- [x] Daemon children: TERM a scratch daemon → its harness stand-in is gone before the daemon exits; request an in-place re-exec → the stand-in survives under the same pid
- [x] `escabs`: a scratch `tmux -L escabs-<pid>` server whose pid is dead is reaped with its own row; one named for a live pid survives
- [x] Coverage floor: every section above asserts at least once; a skipped section fails the script
- [x] Run the script and record the pass/fail counts here

### Results for Phase 5

Recorded 2026-11-16 from the worker's notes (`/tmp/mux184-phase5-notes.md`); plan read the script's
header, the new hook installer and `CleanupSession`. Uncommitted. Verified and ticked 2026-11-16
once all three had run after the last source edit (19:11:19): `./build.sh` exit 0 at 19:12:13,
`./test.sh` exit 0 at 19:13:47, review at 19:14:32 with 0 must-fix, 0 should-fix, 0 nits
(`/tmp/muxcode-review-1794874432.txt`); the run agent's own history row confirms run 3 (exit 0,
"42 passed, 0 failed (floor 42)"). Plan's doc corrections (19:12:24–29) landed between the build and
the test. All thirteen Phase 5 steps and thirteen of the fourteen criteria were ticked; the first
criterion was left open, with the reason written on it.

| Item | Result |
|------|--------|
| The script | `scripts/test-orphan-reaper.sh` — a tree-built muxcode on a private tmux server. Fixtures run a real `muxcode watch` daemon and monitor, a real `muxcode inbox --poll --loop` listener under a live parent, and a stand-in harness the daemon starts that takes its owner lock as the real harness does. Agents are the claude-stub, reached through `MUXCODE_SHELL_INIT` because `SetupPath` pins `/opt/homebrew/bin` |
| A — dead-session sweep | Through `muxcode cleanup`: daemon, monitor, listener and harness gone, an `orphan-reaped` row for each, the bus dir absent after twice the poll |
| B — live-session control | A live session is untouched |
| C — unproven control | An unproven proc record → the sleeper survives, `orphan-unproven` is logged, the dir is held |
| D — the launch sweep | A legacy script listener reaped on argv; the loop form unproven and alive; a live session's script listener alive; an `escabs` server with a dead pid reaped (`escabs-reaped`), one with a live pid alive |
| E — the opt-out | A launch under `MUXCODE_ORPHAN_REAP=0` reaps and removes nothing; the next, sweeping launch then reaps that dead session |
| F — the close hook | `tmux kill-session` alone removes the launched session's bus dir and its daemon |
| G — `ps` unavailable | The owner-lock road reaps, `orphan-census-unavailable` is logged, an argv-only listener is left |
| H — daemon children | TERM → the harness is gone and `daemon-stopped` is logged; an `upgrade-daemons` re-exec on the no-`ps` road → the same daemon pid and the same harness pid |
| I — order | A daemon killed under a live monitor is relaunched; monitor-first, it stays down |
| Run 1 | Task `1794873777-…-ab3e9d14`: 40 passed, 2 failed — both in F: the close hook never fired (code fix 1) |
| Run 2 | Task `1794874014-…-53602c88`: 41 passed, 1 failed — in G: the now-working global hook reaped G's legacy listener, with `ps`, before G's `ps`-less sweep ran. A fixture fault; G now removes the hook before its `kill-session` |
| Run 3 | Task `1794874151-spawn-926e95e5-4be9510c`: **42 passed, 0 failed, exit 0**, floor 42, pinned to the exact count |

**Three code fixes the script drove.**

| # | Fix |
|---|-----|
| 1 | **The close hook never fired at all** — deeper than sub-defect A as filed. A `session-closed` hook set on a session does not fire for the session tmux is destroying (probe on tmux 3.8, run task `1794873967-spawn-926e95e5-b8e66806`: per-session no; global yes, with `#{hook_session_name}`). That is why no `session-cleanup` row was ever seen, and it answers Phase 1's "whether the hook fired cannot be told": it did not. The launcher now installs a **global** indexed hook, `session-closed[184]` (`InstallSessionClosedHook`, `bus/tmux.go`), running `muxcode cleanup --session #{q:hook_session_name}`. It fires for every session closing on the server; one with no bus dir cleans nothing. `TmuxSetHook`, left without a caller, is deleted with its test; `TestInstallSessionClosedHook` pins the arguments |
| 2 | A proven candidate already gone when its turn comes — a harness its own daemon stopped on TERM earlier in the same sweep — is counted reaped, with the note "exited before its own signal" and no signal sent. Before, it read as unproven and cleanup held the whole session. A unit control was added |
| 3 | `daemon-stopped` now counts the harness the daemon stopped through its own process handle |

Type-checked after the fixes by run task `1794874280-spawn-926e95e5-567d6e87`.

**Fix 1 changes what closing a session does — for the user, before this ships.** The spec asked for
it in so many words: "the `session-closed` hook … removes the closing session's own artifacts". What
the spec did not weigh is that until today that hook had **never** run, so every muxcode session's bus
directory has always outlived a `tmux kill-session`, and other behaviour grew up on top of that:

| Lives in the bus directory | What relied on it surviving a close |
|----------------------------|-------------------------------------|
| `graphs/` — the graph run store, parked and running runs | "The first tick after a daemon restart is the resume"; a gated run waiting on a human ([MUX-208](./MUX-208-durable-bus-state-in-tmp-deleted-by-macos-tmp-cleaner.md) moved it out of `/tmp` precisely so it would not be deleted) |
| `active-spec` | Kept across a re-init since [MUX-168](./MUX-168-reinit-purges-active-spec-graph-run-survives.md), so a relaunched session's run still has its spec |
| `config/*.env` — runtime provider and model overrides | A reload's `--cli` / `--model` choice, kept for the session's life |
| Inboxes, the task store, `<role>-history.jsonl`, `log.jsonl` | Undelivered messages and the evidence `verify-spec` reads |

With the hook working, `tmux kill-session` followed by a relaunch of the same name starts from an
empty bus directory: parked graph runs are gone, the active spec is unset, provider overrides are
back to their defaults. `CleanupSession` removes the whole directory with no carve-out
(`bus/cleanup.go`). It still survives a tmux server crash or `kill-server`, where no hook runs, and it
is kept when a process could not be reaped. This is the criterion as written, working for the first
time — but it reverses a de-facto guarantee nobody chose to give up. Plan has not weighed it against
MUX-208 and MUX-168 beyond noting the collision; that is the user's call.

It is also a **global** tmux hook where the old one was per-session: muxcode now sets a hook on the
user's tmux server that runs for every session closing on it, at index 184.

**The acceptance worker's addition — for the criterion left open at verification.** Recorded
2026-11-16 from the worker's request; plan read `checkSessionGone` and `SessionPositivelyGone`.
Verified and the criterion ticked 2026-11-16 once all three had run after the last edit (19:33:47):
`./build.sh` exit 0 at 19:34:24, `./test.sh` exit 0 at 19:36:04, review at 19:36:44 with 0 must-fix,
0 should-fix, 0 nits (`/tmp/muxcode-review-1794875769.txt`); the run agent's history row confirms the
script at 56 passed, 0 failed, floor 56.

| Item | Result |
|------|--------|
| The gap | After the tmux server exits no hook runs, so the daemon and harness lived on until the next launch or cleanup — the open criterion's third road |
| The fix | **The daemon ends itself when its session is gone.** `checkSessionGone` (`daemon/daemon.go`) runs every 10 s once the daemon has recorded its server's socket. `bus.SessionPositivelyGone` asks that server, by the reaper's own bar: an unanswered probe, no record or another server's record all read as *not* gone. On a positive answer the daemon stops its harness, runs `ReapOrphans` scoped to its own session, logs `daemon-session-gone` with the counts, releases its instance lock and exits. Off under `MUXCODE_ORPHAN_REAP=0`. It removes no files |
| Script section J | Each closing road, each within a 30 s grace, for daemon, monitor, listener, harness, a proc group, a webhook lock holder, a spawn pane and the launch helpers. **J1** `kill-session` with the close hook removed — all gone, by the daemon's own `daemon-session-gone`. **J2** a same-name relaunch — by the relaunch's sweep (the first instance launched under the opt-out so its own daemon cannot get there first). **J3** `kill -9` of the tmux server — all gone |
| Run | Run task `1794874738-spawn-926e95e5-5b60cdb3`: 52 passed, 0 failed, exit 0, floor 52 (was 42) |
| Unit | `TestSessionPositivelyGone`. Type-checked by run task `1794874887-spawn-926e95e5-f6cd831d` |
| Docs | `docs/agent-bus.md`: the `daemon-session-gone` event row and a bullet under `muxcode upgrade-daemons`; `docs/architecture.md`: a row in "when it runs" and the components table (plan) |

**The rename risk below was closed the same evening.** After plan raised it, `tmux.socket` gained a
second line holding the tmux **session id** (`RecordTmuxSocket`, `bus/tmux_socket.go`), and a session
is dead only when neither its name nor its id is listed on its server — the id survives
`tmux rename-session`, so a renamed session reads as live and is never reaped (`bus/reaper.go`,
"renamed: its session id … is still listed"). One limit: a record written before this has a single
line — this session's own `tmux.socket` did when plan looked — and is judged on the name alone until
its daemon next restarts and rewrites it. Review also corrected `docs/configuration.md`:
`MUXCODE_ORPHAN_REAP=0` turns off the daemon's session-gone check as well as the launch sweep, read
from the daemon's inherited environment (plan had written "launch sweep" only).

The rename guard, as tested: `sessionVerdict` requires the recorded session id to be absent as well as
the name. The id is read from `tmux list-sessions`, because `display-message -t` returns an empty
`#{session_id}` when no client is attached (probe on tmux 3.8, run task
`1794875406-spawn-926e95e5-26461d92`) — a first run of the new control, 54 passed and 2 failed, is
what found the empty id. Script section **J0**, a rename control, was added:
`scripts/test-orphan-reaper.sh` now 56 passed, 0 failed, exit 0, floor 56 (run task
`1794875430-spawn-926e95e5-083acf28`); type-checked by `1794875613-spawn-926e95e5-ee9e9202`.

**A pre-existing defect the worker found here and did not change:** the monitor
(`cmd/watch.go`, `tmux has-session -t <name>`) and the inbox listener both check their session **by
name**, so both exit on `tmux rename-session`, and a renamed session loses its monitor and its message
delivery. The reaper no longer kills a renamed session's processes, but those two end themselves. Not
filed; suggested to edit.

The note as first recorded:

- **Renaming a live session.** `tmux rename-session` makes the old name positively absent from the
  very server the daemon recorded, which is exactly the condition `checkSessionGone` acts on. The
  daemon would then reap the processes holding owner locks under that bus directory — the agents'
  inbox listeners and any background procs of a session that is alive under its new name — and exit.
  Before this, a rename left the daemon running (only the monitor exited). No test renames a session.
  This is a reading of the code, not an observation.

**Docs corrected** (plan): `docs/agent-bus.md` `muxcode cleanup` — the hook is the global
`session-closed[184]`, the old one never fired, and a paragraph saying a close now deletes the bus
directory and what is in it; `docs/architecture.md` — the "when it runs" row; `docs/configuration.md`
— the bus-directory lifetime line. `CLAUDE.md` — the reaper bullet and the `test-orphan-reaper` entry
(the worker).

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

**Complete** — activated and closed 2026-11-16 by `55-defect-backlog-loop` run `1794626267-55-defect-backlog-loop-15547e2e` (Defects rank 14 then; ranks 1–13 were parked). Uncommitted at close-out; the run's ship step commits it, and edit is holding that step's gate on the user's four decisions below. 52/52 — Phases 1–5 complete and all fourteen criteria met; the last, left open at Phase 5's verification, was closed by the daemon's own session-gone check. Nothing is open in the checklist. **Not ready to ship on that alone: four things are with the user, unanswered, and edit is holding the ship gate on them**, the last the most consequential: with the close hook working for the first time, `tmux kill-session` now deletes the session's whole bus directory — graph runs, the active-spec pointer, provider overrides, inboxes (Results for Phase 5). The other three are from Phase 3 (Results for Phase 3): owner locks as the proof for the listener, harness and webhook; the tmux-socket guard; and a launch being refused while an unproven same-name daemon survives. The tree has moved a long way since this was written: read [Findings at activation](#findings-at-activation-2026-11-16) before Phase 1.

History: parked 2026-11-13 on five open decisions; unparked 2026-11-15 once the user's
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
