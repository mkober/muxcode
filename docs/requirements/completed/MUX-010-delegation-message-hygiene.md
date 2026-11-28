# MUX-010: Delegation Message Hygiene

**Tracking:** [mkober/muxcode#10](https://github.com/mkober/muxcode/issues/10)

**Split 2026-11-24** on the user's decision, relayed by edit. This spec was "Agent-Freeze
Auto-Recovery & Delegation Hygiene"; it keeps the hygiene half and the id. The frozen-but-alive
watchdog — detecting a hung agent, force-killing and respawning it, verifying the respawn drained
its inbox — moved whole to
[MUX-241](../backlog/MUX-241-no-watchdog-recovers-a-frozen-but-alive-agent.md), parked. Issue #10 still
carries the old title and both halves.

A delegation is a `muxcode send` string. It must be short, single-line and intent-level, because a
payload with a newline or past 500 characters misses the `Bash(muxcode *)` permission glob and
raises a prompt instead of delivering. Today that rule is a quiet stderr line the sender can ignore,
long content has no first-class road, and a healthy `--wait` prints nothing while it polls.

## Context

### Origin — observed incident

In the `is-service-providers-gateway` subsession a commit delegation crammed a four-file list plus
the full commit message body into one `muxcode send commit commit "…"` string (~580 chars). The bus
warned twice (`payload is N chars (>500)`); the warnings are non-blocking and were ignored. The same
incident's primary cause, a commit agent frozen for ~8.5 minutes, is MUX-241's.

### Current state (checked 2026-11-24)

| Concern | Today | Where |
|---------|-------|-------|
| Payload warnings | `validatePayload()` returns two plain warnings — newlines, `>500` chars — printed to stderr, non-blocking | `tools/muxcode/cmd/send.go` |
| Long content | The file handoff (`/tmp/<name>.md` plus a one-line pointer) is documented; `docs/agent-bus.md` says a future `--payload-file` "will formalize this" | `docs/agent-bus.md`, `CLAUDE.md` |
| `--payload-file`, stdin | Do not exist | — |
| Strict mode | Does not exist | — |
| `--wait` | Polls silently; after `MUXCODE_WAIT_DEGRADE_SECS` (90) it becomes a tracked task and returns | `cmd/send.go` |
| `--track` | Documented as preferred; a send with neither flag is untracked | `CLAUDE.md`, agent definitions |
| Blocking commands in an agent's pane | Guidance only ("Keep agents deliverable", `CLAUDE.md`) | — |

## Decisions (user, 2026-11-24)

Relayed by edit. These replace the old Phase 1's nine design decisions and the Open questions list.

| Question | Decision |
|----------|----------|
| Long-payload road | Both: `--payload-file <path>` and stdin (`-`) |
| `--wait` liveness | A heartbeat line on **stderr** about every 30 s, naming the target role and elapsed time |
| Guardrail escalation | A louder warning. No auto-fix (no stripping or truncating) |
| Strict mode | Opt-in, by environment: `MUXCODE_SEND_STRICT` rejects what the warning names |
| `--track` default | Document only. The implicit default does not change |
| Frozen-agent recovery | Split out — MUX-241, parked: the freeze signal is undesigned |

Not decided, and not in this spec: a guard or lint against blocking foreground commands
(`--watch`, `tail -f`) in a non-watch pane. It stays guidance; it is listed in MUX-241, since it is
prevention for the same freeze.

### Added 2026-11-28 (edit's call)

Made by edit under the user's standing autonomy while `55-defect-backlog-loop` worked this spec.
Not a user decision; the user may overrule either.

| Question | Edit's call |
|----------|-------------|
| Do the two steps Phase 1 added to Phase 2 need criteria? | Yes, one each: a typed wake delivers a multi-line payload as one submitted prompt; a payload line cannot render as a `To reply:` header. The indent step is therefore required, not only recommended |
| Which values of `MUXCODE_SEND_STRICT` are off? | Empty, `0`, `false`, `no`, `off` — trimmed, any case. Any other value is on. The user's decision said "opt-in, by environment" and named no values; as first built, any value but empty or `0` was on, so `=false` turned strict on. Reported to plan by the run's worker as edit's call |
| Is header spoofing new with `--payload-file`? | No. It is an existing render ambiguity — an inline newline is warned about and still sent — that file and stdin payloads make easier to reach. The Phase 1 findings were reworded to say so |

## Requirements

### Acceptance criteria

- [x] `muxcode send <to> <action> --payload-file <path>` sends the file's content as the payload
- [x] `muxcode send <to> <action> -` reads the payload from stdin
- [x] A payload given by file or stdin is not warned about for its newlines or length — passing it
      by reference is the remedy the warning asks for — and the command line that sent it is a
      single line matching `Bash(muxcode *)`
- [x] A missing or unreadable `--payload-file` fails with exit 1 naming the path, and sends nothing
- [x] An inline payload with a newline or past 500 characters prints a warning that is hard to miss:
      a prefixed block on stderr naming the cause, the consequence (the permission glob) and the two
      remedies (`--payload-file`, `/tmp/<name>.md` plus a pointer)
- [x] With `MUXCODE_SEND_STRICT` set, that same payload is refused: NOT SENT, exit 1, nothing in
      either inbox or the log
- [x] Without `MUXCODE_SEND_STRICT`, it is still sent, as today
- [x] `--wait` prints `still waiting on <role> (<n>s elapsed)` to stderr about every 30 s until the
      reply, the degrade to a tracked task, or the timeout; stdout carries only what it carries today
- [x] Negative control: a compliant short single-line send prints no warning and no heartbeat before
      a reply that lands inside the first interval
- [x] A typed wake (OpenCode, scrape-road Codex) delivers a multi-line payload as one submitted
      prompt, never submitting mid-payload (added 2026-11-28, edit's call)
- [x] A payload line cannot render as a `To reply:` header in the receiver's inbox: continuation
      lines are indented (added 2026-11-28, edit's call)
- [x] `docs/agent-bus.md` documents `--payload-file`, stdin, `MUXCODE_SEND_STRICT`, the heartbeat
      and `--track` as the default to reach for; its "future `--payload-file`" sentence is gone
- [x] `docs/configuration.md` lists `MUXCODE_SEND_STRICT`
- [x] `CLAUDE.md`'s delegation-hygiene bullet names the new flags
- [x] `agents/code-editor.md` and `agents/git-manager.md` say: delegate intent, not pre-baked
      artifacts; long content goes by `--payload-file`

### Non-goals

- Changing the `--wait`/`--track` mechanism, or which is the implicit default
- Rejecting long payloads unconditionally — some are legitimate; strict mode is opt-in
- Anything about a frozen agent (MUX-241)

### Technical approach

- **Payload source** is resolved before `validatePayload()`: inline argument, `--payload-file`, or
  `-`. More than one is an error. A by-reference payload skips the newline and length warnings but
  is still subject to the inbox cap and every authority check — it is the same message.
- **How a receiver sees a multi-line payload** is this spec's one piece of design left to its first
  phase: the bus log and inbox are JSONL, so newlines are already escaped at rest, but chain
  templates, wake injection and `boundCommand` assume one line. Phase 1 reads those roads and
  records what a multi-line payload does on each before any flag ships.
- **Strict mode** reuses the warning's own predicate, so the two can never disagree.
- **Heartbeat** runs in the `--wait` poll loop on a 30 s ticker, stderr only, so a caller parsing
  stdout is unaffected. With the default 90 s degrade a waiting send prints two or three lines.

### Key files

| File | Purpose / change |
|------|------------------|
| `tools/muxcode/cmd/send.go` | Payload source, `validatePayload()` escalation, strict mode, heartbeat |
| `tools/muxcode/cmd/send_test.go` | Tests for each |
| `agents/code-editor.md`, `agents/git-manager.md` | Delegate-intent wording |
| `docs/agent-bus.md` | `muxcode send` hygiene section |
| `docs/configuration.md` | `MUXCODE_SEND_STRICT` |
| `CLAUDE.md` | Delegation-hygiene bullet |

## Implementation

### Phase 1: Survey

- [x] Read every road a payload travels after `Send` — inbox render (`FormatMessage`), wake
      injection, chain and subscription templates, the task store, auto-CC — and record here what a
      multi-line payload does on each
- [x] Record whether any of them needs a change before `--payload-file` can ship; if one does, add
      it to Phase 2 as its own step

### Phase 2: Payload by reference

- [x] Flatten newlines in the typed wake prompt on the OpenCode and scrape-road Codex roads, reusing
      `redriveNewlines` so every typed road flattens one way. Unit test: a two-line payload is typed
      as one line with its reply instructions following in the same prompt; control — a one-line
      payload is typed unchanged (added by Phase 1)
- [x] Indent `FormatMessage` continuation lines so a payload line can never begin `To reply:`,
      `Reply to:` or `--- Message from`. Unit test: a payload carrying a forged `To reply:` line
      renders indented and the real one is the only `To reply:` at column 0; control — a one-line
      payload renders byte-identical to today (added by Phase 1)
- [x] Implement `--payload-file <path>` and stdin `-`; refuse more than one payload source
- [x] By-reference payloads skip the newline and length warnings
- [x] Unit tests: file, stdin, missing file, two sources, a by-reference payload at the inbox cap

### Phase 3: Louder warning and strict mode

- [x] Rewrite the warning as a prefixed stderr block naming cause, consequence and remedies
- [x] Implement `MUXCODE_SEND_STRICT`: refuse, NOT SENT, exit 1
- [x] Unit tests: warning text for each cause; strict refuses and writes nothing; strict off still
      sends; a compliant payload prints nothing under either setting

### Phase 4: `--wait` heartbeat

- [x] Emit the heartbeat on stderr every 30 s inside the poll loop
- [x] Unit test with a shortened interval: at least one heartbeat before a late reply, none before
      an early one, stdout unchanged

### Phase 5: Guidance

- [x] Update `agents/code-editor.md` and `agents/git-manager.md`
- [x] Update `docs/agent-bus.md`, `docs/configuration.md` and `CLAUDE.md`

### Phase 6: Integration test

- [x] Create `scripts/test-send-hygiene.sh` — hermetic: muxcode built from the tree, scratch HOME,
      state and bus, no daemon or tmux, the script playing the receiver
- [x] A `>500`-char inline send prints the warning block and is delivered
- [x] A newline-carrying inline send prints the warning block and is delivered
- [x] The same two under `MUXCODE_SEND_STRICT=1` are NOT SENT, exit 1, absent from inbox and log
- [x] `--payload-file` delivers a long multi-line payload with no warning; the receiver's
      `muxcode inbox` shows it whole
- [x] Stdin `-` delivers the same payload
- [x] `--wait` with a receiver that answers after two shortened intervals prints at least one
      heartbeat on stderr and the reply on stdout — as built, one real 30 s interval: the interval
      has no override, so the receiver answers after ~35 s
- [x] Negative control: a compliant short send prints no warning and, answered at once, no heartbeat
- [x] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [x] Run the script and confirm all checks pass

## Findings for Phase 1

Surveyed by the run's edit worker 2026-11-28, read at the working tree; no code changed and nothing
was executed. Plan checked the two roads flagged for a change against the tree: `redriveNewlines`
(`bus/deliver.go`), the raw `Content:` line in `FormatMessage` (`bus/message.go`) and the
`TmuxSendLiteral` call on both wake roads are as described. That a typed newline submits the
composer is read from `send-keys -l`'s behaviour, not observed on a live pane.

| Road | Where | A multi-line payload today | Change before `--payload-file`? |
|------|-------|----------------------------|---------------------------------|
| At rest: inbox, bus log, task store, history | `bus/inbox.go` JSONL; `bus/task.go` (`Task.Payload`, copied by `CreateTask`) | Escaped by JSON and round-trips whole. The harness's `ParseMessages` splits JSONL by line and is safe for the same reason | No |
| Inbox render | `FormatMessage` (`bus/message.go`) | `Content: <payload>` is printed raw, so continuation lines start at column 0 between `Content:` and the real `Reply to:` / `To reply:` lines. A payload line shaped like `To reply: muxcode send …` or `--- Message from …` reads as a header. This ambiguity exists today: an inline payload with a newline is warned about and still sent (`validatePayload`, `cmd/send.go`). File and stdin payloads make it easier to reach, not new | **Yes** — indent continuation lines |
| Wake — Claude | `SendWakeUpWithText`, the fixed "You have new messages" | The payload is never typed | No |
| Wake — Codex hook road | `hook stop` / `hook prompt-submit` (`additionalContext`, JSON) | Never typed into the pane | No |
| Wake — OpenCode | `OpenCodeProvider.SendWakeUp` (`bus/provider_opencode.go`) | Payloads are joined with ` \| ALSO: ` and typed by `TmuxSendLiteral` (`send-keys -l`). A newline typed into the composer is Enter: the prompt submits mid-payload, the reply instructions after it arrive as a second, orphaned prompt, and `injectionNeedle` (the prompt's tail) no longer matches | **Yes** — flatten newlines in the typed prompt |
| Wake — scrape-road Codex | `CodexProvider.SendWakeUp` (`bus/provider_codex.go`) | Same construction, same typing, same hazard | **Yes** — the same change |
| Re-drive (stall watchdog, executor, `deliver --force`) | `redriveBlock` (`bus/deliver.go`) | Already flattened by `redriveNewlines` (`\n` → ` ⏎ `, MUX-232) | No — it is the replacer the two wake roads reuse |
| Chain and subscription templates | `ExpandMessageWithContext` (`bus/profile.go`), `ExpandSubscriptionMessage` (`bus/subscribe.go`) | They substitute only `${command}` (bounded), exit code, event, outcome, branch and changed files — never a message payload | No |
| Graph interpolation of a node's output | `bus/graph_exec.go` (`st.Output`, `truncateAtRune`) | A reply sent by `--payload-file` can be multi-line and is interpolated into a downstream dispatch, which then rides the render and wake roads above | No separate change — covered by the two above |
| Auto-CC | the copy in `bus/inbox.go` | Keeps the original's payload, so behaves as the original | No |
| Dedup, relay suppression, response brake | compare payload text | Unaffected by newlines; the brake folds whitespace | No |

**What Phase 2 gained.** Two steps, each its own box at the head of the phase: flattening the typed
wake prompt (without it a multi-line payload splits into two prompts on OpenCode and scrape-road
Codex) and indenting `FormatMessage` continuation lines. Both bear on the
Phase 6 step "the receiver's `muxcode inbox` shows it whole": the bytes arrive either way, but only
with them does the receiver act on the payload as one message with one reply route.

**Criteria added 2026-11-28.** As first recorded, neither change had an acceptance criterion behind
it. Edit added one for each (see "Added 2026-11-28" under Decisions), so the spec now closes on
both. The header-forging road is an existing render ambiguity that file and stdin payloads make
easier to reach; an earlier wording of this section called it new, which was wrong.

## Findings for Phase 2

Implemented by the run's edit worker 2026-11-28. Recorded by plan from the worker's report, the
diff, the pin script and its logs.

### What changed

| File | Change |
|------|--------|
| `cmd/send.go` | `resolvePayload` is the one payload source: the inline argument, `--payload-file <path>`, or stdin when the inline argument is `-` (test seam `sendStdin`). File and stdin are by reference: sent byte for byte, a trailing LF or CRLF kept, and `validatePayload`'s warnings skipped. Inline plus `--payload-file` is refused ("give the payload once"), as are no source and an unreadable file, which names its path — each exits 1 saying nothing was sent. The flag loop is its own function, `parseSendFlags`, which returns errors rather than exiting, so a refused command line reaches no send: `--payload-file` given twice or with an empty path is refused. The usage lines name the new forms |
| `bus/provider_opencode.go`, `bus/provider_codex.go` | The OpenCode and scrape-road Codex wakes flatten each payload with `redriveNewlines` (`\n`, `\r\n`, `\r` → ` ⏎ `) before typing — the replacer the re-drive already used |
| `bus/message.go` | `FormatMessage` indents a multi-line payload's continuation lines by two spaces (`contentContinuation`), so no payload line begins `To reply:`, `Reply to:` or `--- Message from`. A one-line payload renders byte-identical |

### Evidence

Pin `/tmp/mux010-p2-pin.sh`, rerun by the run agent after the review fixes (task
`1795848568-spawn-a5769f4f-5d175087`, 01:49, after the last code edit): **10 passed, 0 failed**.
The first run, before review, was 9/0.

| Check | Result |
|-------|--------|
| `go vet ./bus ./cmd` | clean |
| `TestResolvePayload` | 7 cases pass, pinning exact bytes: a file keeps its trailing newline, stdin without one, CRLF kept, inline control, missing file, two sources, no source |
| `TestParseSendFlags` | 7 refusals pass — `--payload-file` twice, an empty path alone and beside an inline payload, a missing path, an unknown flag, a second inline argument, `--wait` with `--track` — and a well-formed command line parses as the control |
| `TestResolvedPayloadIsStillCapped` | a stdin-resolved two-line payload sent to an inbox at `InboxCap` is refused, nothing appended |
| `TestTypedWakeRoads_FlattenMultiLinePayloads` | OpenCode and scrape-road Codex each pass a multi-line case (typed in one `send-keys -l` call, the reply instruction in the same prompt) and a one-line control |
| `TestFormatMessage_IndentsContinuationLines` | forged `To reply:` and `--- Message from` lines render indented; the only column-0 `To reply:` is the real one; a one-line payload is unchanged |
| Mutant: no flatten on the OpenCode road | fails the OpenCode multi-line case; its one-line control still passes |
| Mutant: no indent | fails the `FormatMessage` test |

### Review must-fixes (2026-11-28)

| Finding | Fix |
|---------|-----|
| A file or stdin payload lost one trailing newline, so what was sent was not what was given | `trimOneNewline` deleted; the payload is sent byte for byte and the test pins the exact bytes |
| The flag loop called `os.Exit` inside `Send`, and `--payload-file` could be given twice or with an empty path | The loop moved to `parseSendFlags`, which returns an error for each; `TestParseSendFlags` covers them |

A consequence of the first fix: a file ending in a newline, as most do, sends a payload ending in
one. A typed wake then ends ` ⏎`, and the inbox render ends with an indented empty line.

### Limits

- **The Codex flatten has no mutant.** Its test cases pass, but only the OpenCode line was mutated,
  so nothing shows the Codex case would fail with its flatten removed.
- **The warning skip is not unit-tested.** It lives in `Send` and writes to stderr; Phase 6's script
  is to assert that a `--payload-file` send prints no warning. Until then it is read from the code
  (`if !byRef`), not run.
- **Nothing was run on a live pane.** The wake tests assert what is passed to `send-keys`, not what a
  composer does with it.
- **Behaviour that changed beyond the flags.** An inline payload of exactly `-` now means stdin and
  can no longer be sent as text. Every multi-line payload — inline ones already in use included —
  now renders indented in `muxcode inbox`.
- **Criteria still open after this phase.** The file, stdin, no-warning and missing-file criteria
  are pinned at `resolvePayload` and `parseSendFlags`, not through a real `muxcode send`; they wait
  for Phase 6's script. The typed-wake criterion waits too: its test asserts what is handed to
  `send-keys` on both roads, with a mutant on one.

### Verified 2026-11-28

The full `./test.sh` ran 01:51–01:53, after the last code edit (01:49): both modules passed, no
`--- FAIL` in either log. Review of the fixed code: 0 must-fix, 0 should-fix, 0 nits. On that the
phase's four code steps were ticked, and one criterion — a payload line cannot render as a
`To reply:` header — whose unit test is the criterion itself and fails with the indent removed.

## Findings for Phase 3

Implemented by the run's edit worker 2026-11-28, in `cmd/send.go` alone. Recorded by plan from the
worker's report, the code, the tests and the pin log.

### What changed

| Piece | Behaviour |
|-------|-----------|
| Warning block (`payloadWarningBlock`) | An inline payload `validatePayload` flags prints a block on stderr, every line prefixed `!! `: a `WARNING` header, one `cause:` line per cause (newlines; `N chars (>500)`), the consequence (the command line misses the `Bash(muxcode *)` permission glob, so the agent gets a permission prompt instead of the send), two remedies (`--payload-file <path>` or `-` for stdin; `/tmp/<name>.md` plus a one-line pointer), and that `MUXCODE_SEND_STRICT=1` refuses |
| Strict mode (`sendStrict`) | `MUXCODE_SEND_STRICT` is off for empty, `0`, `false`, `no` and `off` (trimmed, any case) and on for any other value. On, it refuses that same payload: `NOT SENT — <to> <action> — MUXCODE_SEND_STRICT refuses…`, exit 1, before anything is written. It reads `validatePayload`'s own result, so the warning and the refusal cannot disagree |
| By-reference payloads | Skip both the block and the refusal |

### Evidence

Pin `/tmp/mux010-p3-pin.sh`, last run by the run agent after the off-values change (task
`1795849385-spawn-a5769f4f-f3575cc1`, 02:03, after the last edit at 02:02:41): **6 passed, 0
failed**; the `cmd` send tests, 21 pass. A second pin in the same task,
`/tmp/mux010-p3-warnmut.sh`, runs the no-warning mutant. Earlier runs were 5/0 twice: before the
review fix, and after it.

| Check | Result |
|-------|--------|
| `TestPayloadWarningBlock` | newline, long, both: every line carries the prefix, and the block names its causes, the consequence, both remedies and the strict variable |
| `TestSendStrict` | off: empty, `0`, `false`, `FALSE`, ` no `, `Off`; on: `1`, `true`, `yes`, `on` |
| `TestSendCLI_StrictRefusesWhatTheWarningNames` | the real `Send`, in process, stderr captured apart from stdout, 8 cases. Under strict (`1`, and `true`) a flagged payload prints the block, then is NOT SENT, exit 1, absent from inbox and log. With strict unset, `0` and `false` the flagged payload prints the block and is delivered. A compliant payload prints no block and is delivered with strict on and with it off. The block never reaches stdout |
| Mutant: strict ignored | fails the three strict cases; the five others pass |
| Mutant: warning not printed | fails the six flagged cases; the two compliant controls pass. The scratch script's own PASS line still says four; plan counted six `--- FAIL` and two `--- PASS` subtests in its log |

### Review should-fix (2026-11-28)

Review found the production guard correct and one should-fix on the test: it captured stdout only,
so nothing asserted the warning was printed, or that a compliant send printed none. Plan had held
the unit-test step on the same gap. The helper now captures stderr separately (`runSendCapture`;
`runSendAs` wraps it, its 12 callers unchanged) and the test gained the output assertions and a
compliant case with strict off. The first pin, before the fix, was also 5/0: it proved the refusal,
not the warning.

### Limits

- **The off values are edit's call, not the user's** (see "Added 2026-11-28" under Decisions). An
  unrecognised value — a typo such as `flase` — still turns strict on.
- **Phase 5 must state the off spellings** where it documents `MUXCODE_SEND_STRICT`, in
  `docs/configuration.md` and `docs/agent-bus.md`.
- **Tested in process, not as a command.** The strict and warning tests call the real `Send` inside
  the test binary. Phase 6's script is what runs them through a built `muxcode`.

### Verified 2026-11-28

The full `./test.sh` ran 02:02–02:04 on the code as it stands — its log holds `TestSendStrict` and
the `strict=false` case, both added at 02:02 — and both modules passed with no `--- FAIL`. Review:
0 must-fix, 0 should-fix, 0 nits. On that the two code steps were ticked, and three criteria: the
warning block, the strict refusal, and still-sent without strict. Each is asserted against the real
`Send` and fails under a mutant.

## Findings for Phase 4

Implemented by the run's edit worker 2026-11-28, in `cmd/send.go` alone. Recorded by plan from the
worker's report, the code, the test and the pin log.

### What changed

`waitForResponse` prints `still waiting on <target> (<n>s elapsed)` every `waitHeartbeatEvery`
(30 s) through `waitHeartbeatOut`, which is `os.Stderr`. The check runs after each poll's reply
checks, so a reply landing inside the first interval prints none. The loop ends on the reply, the
degrade to a tracked task, or the timeout, and the heartbeat ends with it. With the default 90 s
degrade a waiting send prints two lines, at about 30 and 60 s, and a third only if the 90 s check
falls after a poll.

### Evidence

Pin `/tmp/mux010-p4-pin.sh`, run by the run agent (task `1795849609-spawn-a5769f4f-63516654`,
02:07, after the last code edit at 02:06): **5 passed, 0 failed**; the `cmd` wait and send tests,
20 pass.

| Check | Result |
|-------|--------|
| `TestWaitForResponseHeartbeat`, late reply (interval 300 ms, reply at ~1.6 s) | at least one heartbeat on the writer; stdout carries the answer and no heartbeat |
| `TestWaitForResponseHeartbeat`, control: a reply already waiting | no heartbeat |
| Mutant: heartbeat print removed | fails the late-reply case; the control passes |

### Limits

- **Stderr is read, not run.** The test swaps the writer for a buffer, so it shows the heartbeat
  goes to that writer and not stdout. That the writer is `os.Stderr` in a real send is read from
  its declaration; Phase 6's script is what observes it on a built `muxcode`.
- **The 30 s interval and the degrade and timeout endings are not timed.** The test shortens the
  interval and ends on a reply; no case waits through a degrade or a timeout.
- **Each interval is measured from the last print, not from the start,** so heartbeats drift later
  by up to one poll each time — "about every 30 s", as the criterion says.

### Verified 2026-11-28

The full `./test.sh` ran 02:08–02:10, after the last edit (02:06), with `TestWaitForResponseHeartbeat`
in its log and no `--- FAIL` in either module. Review: 0 must-fix, 0 should-fix, 0 nits, noting
that real stderr and the degrade and timeout timing remain for Phase 6. On that the code step was
ticked. The heartbeat criterion and the negative-control criterion stay open for the same reason:
neither has been observed on a real send.

## Findings for Phase 5

Guidance only, 2026-11-28; no code changed.

| File | By | What it now says |
|------|----|------------------|
| `agents/code-editor.md` | edit worker | A new subsection, "Long content — delegate intent, send the rest by reference": delegate intent, not pre-baked artifacts; content that must travel goes by `--payload-file` or stdin; the `!!` block, strict mode and the heartbeat are explained |
| `agents/git-manager.md` | edit worker | A Conventions bullet saying the same for the commit agent's own delegations |
| `CLAUDE.md` | edit worker | The delegation-hygiene bullet names `--payload-file`, stdin, the warning block, `MUXCODE_SEND_STRICT` with its off values, the heartbeat, and how a multi-line payload is rendered |
| `docs/agent-bus.md` | plan | `muxcode send`: the usage line and bullets for `-` and `--payload-file`; a new "Payload sources" subsection (the three sources, byte for byte, one source only, the warning block, strict mode, how each road renders a multi-line payload); the heartbeat on `--wait`; a strict-mode row in the NOT SENT table; the hygiene bullets, with `--track` as the default to reach for. The "future `--payload-file`" sentence is gone |
| `docs/configuration.md` | plan | A `MUXCODE_SEND_STRICT` row: off for empty, `0`, `false`, `no`, `off`; on otherwise; read from the sending process's environment |

**Review should-fix (2026-11-28).** `agents/code-editor.md` first told an agent to re-send by
reference after seeing the `!!` block. With strict mode off that message had already been
delivered, so a second copy duplicates the work once the dedup window passes. It now says a warning
is not a refusal: the message was sent, do not send it again, send the next one by reference; only
a send that printed `NOT SENT` went nowhere and is retried. `agents/git-manager.md` and `CLAUDE.md`
never said to re-send and are unchanged. Plan added the same point to `docs/agent-bus.md`, whose
"treat it as an error to fix" could be read the same wrong way.

**Verified 2026-11-28.** Review of the guidance, after the wording fix: 0 must-fix, 0 should-fix,
0 nits. The worker also reported seeing the heartbeat live — two `still waiting` lines on stderr
during a `--wait` on plan. That is the worker's observation, not a recorded run, so the heartbeat
criterion stays open for Phase 6's script.

Both steps and the four guidance criteria were ticked on reading the files. The docs describe the
code as verified in Phases 2–4; nothing in them has been run. Phase 6's script row in `CLAUDE.md`
is that phase's own step and is not written yet.

## Findings for Phase 6

`scripts/test-send-hygiene.sh`, written by the run's edit worker 2026-11-28. Recorded by plan from
the script, its log (`/tmp/test-send-hygiene.log`) and the run agent's reply.

### The script

Hermetic: muxcode built from the tree, a scratch `HOME`, state and bus, no daemon and no tmux; the
script plays plan. Every send's stdout and stderr go to separate files, so the warning block and
the heartbeat are observed on the real stderr stream. About a minute, because the heartbeat
interval is the real 30 s. It sets `MUXCODE_CONSUMER_GUARD=0`, since a script reading plan's inbox
is no window or listener. Coverage floor 22.

### Run

Run agent task `1795850377-spawn-a5769f4f-05a379e2`, 02:20, after the last script edit (02:19):
**22 passed, 0 failed**. Plan counted 22 `ok:` lines and no `FAIL` in the log. A missing-file case
was added afterwards (see "What it closed"); the rerun at 02:23 is **23 passed, 0 failed, floor
23**, section C now 7 checks, and plan counted 23 `ok:` lines.

| Section | Checks | What it shows |
|---------|--------|---------------|
| Setup | 1 | built from this tree, scratch bus initialised |
| A | 5 | a 520-character payload and a two-line payload each print the `!!` block on stderr naming the cause; the block names the consequence and both remedies; nothing of it on stdout; both delivered, exit 0 |
| B | 4 | the same under `MUXCODE_SEND_STRICT=1`: NOT SENT, exit 1, the warning printed first, absent from plan's inbox and the bus log |
| C | 6 | a 20-line payload by `--payload-file` and by stdin: no warning; both in plan's inbox byte for byte, trailing newline included; `muxcode inbox` shows every line, continuation lines indented, none at column 0; `--payload-file` twice is refused, exit 1 |
| D | 4 | `--wait` on a request answered after ~35 s: one `still waiting on plan (Ns elapsed)` line on stderr, none on stdout, the reply on stdout |
| E | 2 | negative control: a compliant request answered at once prints no warning and no heartbeat |

The first run (task `1795850292`) was 17 passed, 5 failed: the script's own sends repeated one
`(from, to, action)` inside the CLI's 30 s dedup window. Each send now uses its own action rather
than `--force`, so nothing under test is bypassed.

### What it closed

Five criteria were ticked on the 22/0 run: the file source, the stdin source, no warning for
either, the heartbeat, and the negative control. Plan held two open and named what would close
each; the worker supplied both, run by the run agent (task `1795850539-spawn-a5769f4f-61354d9d`,
02:23):

| Criterion | Was open because | Closed by |
|-----------|------------------|-----------|
| A missing or unreadable `--payload-file` fails with exit 1 naming the path, and sends nothing | The script had no such case; it was pinned only at `resolvePayload` | A new case in section C: a `--payload-file` naming a missing file exits 1, stderr names the path and says nothing was sent, the bus log's line count is unchanged and plan's inbox holds nothing from it. The script is now **23 passed, 0 failed, floor 23** |
| A typed wake (OpenCode, scrape-road Codex) delivers a multi-line payload as one submitted prompt | The mutant removed the flatten on the OpenCode road only | `/tmp/mux010-codex-mut.sh`: a copy of the tree without the Codex flatten fails `codex_scrape_road/two_lines` while the Codex one-line control and both OpenCode cases pass |

### Limits

- **Missing, not unreadable.** The script's case is a path that does not exist. A file that exists
  and cannot be read takes the same `os.ReadFile` error branch and is not run separately.
- **The typed wake is proven at `send-keys`, not on a pane.** Both roads are shown to hand tmux one
  flattened line, each with a mutant. No test types into a live composer.
- **One heartbeat, one ending.** Section D waits through a single real interval and ends on the
  reply. Two consecutive heartbeats, and the degrade and timeout endings, are not observed.
- **Phase 6's heartbeat step asked for two shortened intervals.** The interval has no override
  outside the test binary, so the script uses one real one. Ticked with that noted on the step.
- **The block's full text is checked on the long payload only;** the newline payload is checked for
  its cause line.
- **No control build.** The script has a negative control (section E) but no run against a copy of
  the tree with a feature removed; the mutants live in the phase pins, not here.

## Status

**Complete** — closed out 2026-11-28 by `55-defect-backlog-loop` run 1795811929 at 39/39: 15
acceptance criteria and 24 phase steps, none open, none deferred.

### What shipped

| Piece | Behaviour |
|-------|-----------|
| Payload by reference | `muxcode send` takes its payload from exactly one source: inline, `--payload-file <path>`, or stdin (`-`). File and stdin are sent byte for byte and print no warning; two sources, an empty or repeated `--payload-file`, or a file that cannot be read exit 1 and send nothing |
| Warning block | An inline payload with a newline or past 500 characters prints a `!! `-prefixed block on stderr naming the cause, the consequence and both remedies, and is still sent |
| Strict mode | `MUXCODE_SEND_STRICT` refuses that same payload — NOT SENT, exit 1, nothing written. Off for empty, `0`, `false`, `no`, `off`; on otherwise |
| `--wait` heartbeat | `still waiting on <role> (<n>s elapsed)` on stderr about every 30 s; stdout unchanged |
| Multi-line rendering | The OpenCode and scrape-road Codex wakes flatten newlines to ` ⏎ ` before typing; `FormatMessage` indents continuation lines so none can pass for a `To reply:` header |
| Guidance | `agents/code-editor.md`, `agents/git-manager.md`, `CLAUDE.md`, `docs/agent-bus.md`, `docs/configuration.md` |
| Integration script | `scripts/test-send-hygiene.sh`, 23 passed, 0 failed, floor 23 |

### Left as recorded

| Item | State |
|------|-------|
| Edit's calls, not user decisions | A criterion behind each of the two steps Phase 1 added; the strict-mode off values. Under "Added 2026-11-28 (edit's call)"; the user may overrule either |
| Behaviour changed beyond the flags | An inline payload of exactly `-` now means stdin. Every multi-line payload, inline ones already in use included, renders indented in `muxcode inbox`. A file ending in a newline sends a payload ending in one |
| Proven at `send-keys`, not on a pane | Both typed-wake roads are shown to hand tmux one flattened line, each with a mutant. Nothing types into a live composer |
| Heartbeat coverage | One real interval and the reply ending are observed. Two consecutive heartbeats and the degrade and timeout endings are not |
| Missing, not unreadable | The script's refusal case is a path that does not exist; an unreadable file takes the same branch and is not run separately |
| A mistyped strict value | `MUXCODE_SEND_STRICT=flase` turns strict mode on |
| Issue #10 | Still open, and still carries the pre-split title and both halves. Not touched by this run |
| Frozen-agent recovery | Not this spec — [MUX-241](../backlog/MUX-241-no-watchdog-recovers-a-frozen-but-alive-agent.md), parked |

### How it got here

Unparked 2026-11-24 on the user's decisions and rewritten that day to the hygiene half, at 0/35.
Activated 2026-11-28 as the best-ranked unparked row of the Defects table. Phase 1's survey added
two steps to Phase 2 (37) and edit a criterion behind each (39). Review returned must-fixes or
should-fixes on Phases 2, 3 and 5, each fixed and re-reviewed clean; plan held two criteria open
after Phase 6 until a missing-file script case and a Codex-road mutant were supplied. On the final
tree review was 0 must-fix, 0 should-fix, 0 nits, and the full `./test.sh` passed both modules —
at 02:25 from Go's test cache, no Go file having changed since the executed runs of 02:08 and
02:16.
