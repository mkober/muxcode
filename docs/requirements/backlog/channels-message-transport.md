# Channels-based message transport

Explore adopting Claude Code's **Channels** feature (`--channels`, v2.1.80+, research preview) as an *optional, opt-in* transport for injecting messages into a running Claude Code agent session — an alternative to the current `tmux send-keys` mechanism, **not** a replacement.

This is exploratory/research-flavored. Channels is a native, supported API for pushing an inbound event into a live session, but it is gated behind Anthropic auth and the Claude Code provider, so it can never be the sole transport. `send-keys` remains the universal, auth-agnostic, all-providers fallback.

## Context

### How muxcode injects messages today

muxcode wakes a running agent by simulating keystrokes into its PTY with `tmux send-keys` — injecting "You have new messages" (Claude Code) or the message payload directly (non-hook providers).

| Component | Role |
|-----------|------|
| `bus/notify.go` — `Notify()` | Writes `trigger-{role}.notify` and routes the wake to the provider |
| `provider_claude.go` — `SendWakeUp()`, `SendWakeUpWithText()` | send-keys injection for Claude Code agents |
| daemon `checkIdleAgents()` | Every 5s, wakes **idle** agents with actionable inbox messages |

### Why send-keys is the baseline

- It is the only fully general, production-ready, auth-agnostic way to inject into an **already-running** interactive session.
- It works on **all** auth backends: Bedrock, Vertex, Foundry, and Anthropic.
- It works across **all** providers: Claude Code, OpenCode, Codex CLI, and the local LLM harness.

### What Channels is

| Aspect | Detail |
|--------|--------|
| Flag | `--channels` (Claude Code v2.1.80+) |
| Status | **Research preview** — API may change |
| Mechanism | Pushes an inbound event into a live session; arrives as a `<channel source="...">` event the model reads and responds to |
| Docs | https://code.claude.com/docs/en/channels.md |
| Demo | A "fakechat" localhost HTTP channel receiver referenced in the docs |
| Access control | An allowlist controls who may push messages |

Conceptually Channels does the same thing as send-keys (push into a live session) but via a supported API rather than keystroke simulation.

### Constraints and tradeoffs

| Constraint | Impact |
|-----------|--------|
| Research preview | API surface may change before stabilizing — adoption risk |
| **Anthropic auth only** (claude.ai login or Console API key) | **Not available on Bedrock, Vertex, or Foundry** — the single biggest limiter, since many muxcode users run on Bedrock |
| Session must start with `--channels` | Cannot attach to an already-running session that lacks the flag — affects `BuildExecArgs()` in `bus/launch.go` |
| Claude Code provider only | OpenCode / Codex / local-harness still require send-keys — Channels can never be the sole transport |
| Allowlist required | The daemon/bus identity must be authorized to push |

## Requirements

### Acceptance criteria

- [ ] Research doc captures Channels capabilities, constraints, and how they map to muxcode's injection model
- [ ] A decision is recorded on whether/when Channels is worth adopting given the Bedrock-auth limitation
- [ ] A transport-abstraction design is specified that keeps send-keys as the universal fallback
- [ ] A config + auth-detection gating design is specified (Channels enabled only when Claude Code + Anthropic auth is detected)
- [ ] Launch-path change is specified: append `--channels` to Claude Code agents only when the transport is enabled
- [ ] Daemon wake-path change is specified: route injection through the channel when available, fall back to send-keys
- [ ] Latency/reliability of Channels vs send-keys is measured and documented
- [ ] Whether Channels delivers to a **busy** (non-idle) session better than send-keys is investigated and documented
- [ ] Integration test injects a message via a channel into a running Claude Code session and confirms the agent receives and acts on it, with the send-keys fallback path also verified

### Out of scope

- Making Channels the default or sole transport — send-keys stays the universal fallback
- Channels support for non-Claude-Code providers (OpenCode, Codex, local harness)
- Channels on Bedrock/Vertex/Foundry auth backends (not supported by Claude Code)

## Technical approach

### 1. Transport abstraction

Introduce an injection-transport concept so a provider can declare a preferred transport, with send-keys as the always-available fallback.

- [ ] Define a transport interface (e.g. `WakeTransport`) with a single inject/wake operation and an `Available()` check
- [ ] Implement `SendKeysTransport` (wraps the current `SendWakeUp` path) — always available
- [ ] Implement `ChannelTransport` (posts to the localhost HTTP channel receiver) — available only when gated conditions pass
- [ ] `Notify()` selects the preferred transport, falling back to send-keys when the preferred one reports unavailable or fails

### 2. Config + auth-detection gating

- [ ] Add a config flag (e.g. `MUXCODE_CHANNELS` / `config set channels true`) to opt in
- [ ] Detect Anthropic auth (claude.ai login or Console API key) vs Bedrock/Vertex/Foundry — Channels enabled only when Claude Code + Anthropic auth detected
- [ ] When gating fails, silently fall back to send-keys with a one-time informational log

### 3. Launch path

- [ ] When the channel transport is enabled for a Claude Code agent, append `--channels` in `BuildExecArgs()` (`bus/launch.go`)
- [ ] Document that switching the transport on/off requires relaunching the affected agents (cannot attach `--channels` to a live session)

### 4. Daemon wake path

- [ ] Build a localhost HTTP channel receiver the daemon/bus posts to (modeled on the docs' "fakechat" demo)
- [ ] Register the daemon/bus identity in the Channels allowlist
- [ ] `checkIdleAgents()` routes injection through the channel when available, falling back to send-keys on unavailability or post error
- [ ] Investigate whether Channels can deliver to a **busy** (non-idle) session — if so, document the difference from send-keys (which the daemon must defer until idle)

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/notify.go` | `Notify()` — transport selection + fallback |
| `tools/muxcode/bus/provider_claude.go` | `SendWakeUp()` / `SendWakeUpWithText()` — existing send-keys transport |
| `tools/muxcode/bus/launch.go` | `BuildExecArgs()` — append `--channels` when enabled |
| `tools/muxcode/watcher/watcher.go` | `checkIdleAgents()` — daemon wake path routing |
| `tools/muxcode/bus/config_file.go` | Config flag read/write for the opt-in |
| (new) channel receiver | Localhost HTTP endpoint the daemon/bus posts to |

## Implementation

### Phase 1: Research and decision

- [ ] Read the Channels docs and prototype the localhost HTTP channel receiver ("fakechat" demo)
- [ ] Confirm the auth restriction empirically (verify Channels is unavailable on Bedrock/Vertex/Foundry)
- [ ] Measure Channels injection latency and reliability against send-keys
- [ ] Determine whether Channels delivers to a busy (non-idle) session
- [ ] Record a go/no-go/defer decision in this doc given the Bedrock-auth limitation and research-preview risk

### Phase 2: Transport abstraction

- [ ] Define the `WakeTransport` interface with inject + `Available()`
- [ ] Implement `SendKeysTransport` wrapping the current path (always available)
- [ ] Implement `ChannelTransport` posting to the channel receiver
- [ ] Refactor `Notify()` to select preferred transport with send-keys fallback

### Phase 3: Gating and launch wiring

- [ ] Add the config opt-in flag and read it through the resolution chain
- [ ] Implement Anthropic-auth detection vs Bedrock/Vertex/Foundry
- [ ] Append `--channels` in `BuildExecArgs()` only for gated Claude Code agents
- [ ] Wire `checkIdleAgents()` to route through the channel when available

### Phase 4: Integration test

- [ ] Create `scripts/test-channels-transport.sh` end-to-end automation
- [ ] Test: launch a Claude Code agent with `--channels` enabled in a live session
- [ ] Test: post a message to the channel receiver → verify the agent receives and acts on it
- [ ] Test: with Channels disabled (or auth ungated) → verify the send-keys fallback path delivers
- [ ] Test: simulate a channel-post failure → verify automatic fallback to send-keys
- [ ] Run `bash scripts/test-channels-transport.sh` in a live session and verify all steps pass

## Open questions

- [ ] Is the Anthropic-auth-only restriction a dealbreaker for the muxcode user base, given many users run on Bedrock?
- [ ] Will Channels survive research preview into a stable API before this is worth building?
- [ ] What are the latency and reliability of Channels vs send-keys?
- [ ] Does Channels deliver to a busy (non-idle) session better than send-keys (which the daemon must defer until idle)?

## Status

Backlog
