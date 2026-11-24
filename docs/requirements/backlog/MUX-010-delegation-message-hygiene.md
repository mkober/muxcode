# MUX-010: Delegation Message Hygiene

**Tracking:** [mkober/muxcode#10](https://github.com/mkober/muxcode/issues/10)

**Split 2026-11-24** on the user's decision, relayed by edit. This spec was "Agent-Freeze
Auto-Recovery & Delegation Hygiene"; it keeps the hygiene half and the id. The frozen-but-alive
watchdog — detecting a hung agent, force-killing and respawning it, verifying the respawn drained
its inbox — moved whole to
[MUX-241](./MUX-241-no-watchdog-recovers-a-frozen-but-alive-agent.md), parked. Issue #10 still
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

## Requirements

### Acceptance criteria

- [ ] `muxcode send <to> <action> --payload-file <path>` sends the file's content as the payload
- [ ] `muxcode send <to> <action> -` reads the payload from stdin
- [ ] A payload given by file or stdin is not warned about for its newlines or length — passing it
      by reference is the remedy the warning asks for — and the command line that sent it is a
      single line matching `Bash(muxcode *)`
- [ ] A missing or unreadable `--payload-file` fails with exit 1 naming the path, and sends nothing
- [ ] An inline payload with a newline or past 500 characters prints a warning that is hard to miss:
      a prefixed block on stderr naming the cause, the consequence (the permission glob) and the two
      remedies (`--payload-file`, `/tmp/<name>.md` plus a pointer)
- [ ] With `MUXCODE_SEND_STRICT` set, that same payload is refused: NOT SENT, exit 1, nothing in
      either inbox or the log
- [ ] Without `MUXCODE_SEND_STRICT`, it is still sent, as today
- [ ] `--wait` prints `still waiting on <role> (<n>s elapsed)` to stderr about every 30 s until the
      reply, the degrade to a tracked task, or the timeout; stdout carries only what it carries today
- [ ] Negative control: a compliant short single-line send prints no warning and no heartbeat before
      a reply that lands inside the first interval
- [ ] `docs/agent-bus.md` documents `--payload-file`, stdin, `MUXCODE_SEND_STRICT`, the heartbeat
      and `--track` as the default to reach for; its "future `--payload-file`" sentence is gone
- [ ] `docs/configuration.md` lists `MUXCODE_SEND_STRICT`
- [ ] `CLAUDE.md`'s delegation-hygiene bullet names the new flags
- [ ] `agents/code-editor.md` and `agents/git-manager.md` say: delegate intent, not pre-baked
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

- [ ] Read every road a payload travels after `Send` — inbox render (`FormatMessage`), wake
      injection, chain and subscription templates, the task store, auto-CC — and record here what a
      multi-line payload does on each
- [ ] Record whether any of them needs a change before `--payload-file` can ship; if one does, add
      it to Phase 2 as its own step

### Phase 2: Payload by reference

- [ ] Implement `--payload-file <path>` and stdin `-`; refuse more than one payload source
- [ ] By-reference payloads skip the newline and length warnings
- [ ] Unit tests: file, stdin, missing file, two sources, a by-reference payload at the inbox cap

### Phase 3: Louder warning and strict mode

- [ ] Rewrite the warning as a prefixed stderr block naming cause, consequence and remedies
- [ ] Implement `MUXCODE_SEND_STRICT`: refuse, NOT SENT, exit 1
- [ ] Unit tests: warning text for each cause; strict refuses and writes nothing; strict off still
      sends; a compliant payload prints nothing under either setting

### Phase 4: `--wait` heartbeat

- [ ] Emit the heartbeat on stderr every 30 s inside the poll loop
- [ ] Unit test with a shortened interval: at least one heartbeat before a late reply, none before
      an early one, stdout unchanged

### Phase 5: Guidance

- [ ] Update `agents/code-editor.md` and `agents/git-manager.md`
- [ ] Update `docs/agent-bus.md`, `docs/configuration.md` and `CLAUDE.md`

### Phase 6: Integration test

- [ ] Create `scripts/test-send-hygiene.sh` — hermetic: muxcode built from the tree, scratch HOME,
      state and bus, no daemon or tmux, the script playing the receiver
- [ ] A `>500`-char inline send prints the warning block and is delivered
- [ ] A newline-carrying inline send prints the warning block and is delivered
- [ ] The same two under `MUXCODE_SEND_STRICT=1` are NOT SENT, exit 1, absent from inbox and log
- [ ] `--payload-file` delivers a long multi-line payload with no warning; the receiver's
      `muxcode inbox` shows it whole
- [ ] Stdin `-` delivers the same payload
- [ ] `--wait` with a receiver that answers after two shortened intervals prints at least one
      heartbeat on stderr and the reply on stdout
- [ ] Negative control: a compliant short send prints no warning and, answered at once, no heartbeat
- [ ] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [ ] Run the script and confirm all checks pass

## Status

**Backlog** — 0/35. Unparked 2026-11-24 on the user's decisions, relayed by edit and recorded above;
rewritten the same day to the hygiene half. Not started.
