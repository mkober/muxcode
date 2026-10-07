# A Sensitive Role's Own Conversation Is Never Scrubbed — PostToolUse Cannot Reach It

**Tracking:** _(no GitHub issue — the user's instruction, 2026-10-07, when this was filed)_

**Provenance:** filed 2026-10-07 by plan on the user's request relayed by edit, as the deferral target
for [MUX-179](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md)'s criterion 2
("never reaches `<role>-history.jsonl` **or the conversation** unredacted"). MUX-179 met the history
half at every writer and corrected the `CLAUDE.md` promise for the other half rather than keep it. This
spec is the other half.

`CLAUDE.md` once said that `api`, `run` and `watch` output is redacted *before it enters the
conversation*. After MUX-179 it says what is true: the **history row** is redacted — credentials for
every role, PII as well for the sensitive four — and the agent's own conversation is not. A `watch`
agent that tails a log carrying a bearer token, or an `api` agent whose response body holds one, reads
it in full; so does its provider's transcript. The scrubber exists, is wired, and is tested — on a road
the agent never looks at.

## Context

### What is covered, and by which road (after MUX-179)

| Road | Scrubbed | Code |
|------|----------|------|
| Local LLM harness — tool output entering the model's conversation | PII and credentials, for `api`, `run`, `runner`, `watch` | `harness/loop.go` → `Executor.ScrubPII` |
| `<role>-history.jsonl` — hook capture, synthesized reply rows, `muxcode log`, `muxcode agent`'s `logBashToHistory` | credentials for every role; PII as well for the four | `ScrubForRole` (`bus/scrub.go`), ANSI stripped first |
| A Claude, Codex or OpenCode agent's **own conversation** | **nothing automatic** — `\| muxcode pii-scrub` by instruction in the `api`/`runner`/`watch` definitions, which nothing enforces | — |
| `muxcode agent`'s model copy of a result; a history row's `Command`/`Description`; bus message payloads in `inbox/` and `log.jsonl` | nothing | — |

### Why the existing road cannot do it

- **`PostToolUse` fires after the provider has shown the agent the output.** The hook that captures
  history (`ProcessBashHook`, `bus/hook.go`) is the last thing to run, not the first; by then the raw
  text is in the conversation and the transcript. `bus/hook.go:1607` notes `hookSpecificOutput` as a
  response shape that may diverge later — whether any provider lets a hook *rewrite* a tool result is
  an open question this spec must answer per provider, not assume.
- **The pipe is advice.** `muxcode pii-scrub` works, and the three definitions ask for it, but an
  agent that runs `ps eww` or `env` bare — as `plan` did on 2026-09-11 (MUX-156's third occurrence)
  — gets the unscrubbed text, and the guard (`CheckGuard`, `bus/hook.go`) has no rule about it.
- **Three providers, three roads.** Claude has hooks; Codex has the hook road (`MUXCODE_CODEX_HOOKS`)
  and the scrape road; OpenCode is scrape-only. A mechanism that works on one may not exist on another,
  and the docs must say so per road rather than promise the union.

### Blast radius

Every sensitive role on every provider, for exactly the data the role exists to handle: `api` response
bodies, `run` script output, `watch` log tails. The original leak was a credential from a diagnostic
command on a non-sensitive role, which MUX-179's credential-everywhere rule now catches **in history**
— and still shows the agent.

## Requirements

### Acceptance criteria

- [ ] For each provider road (Claude hooks, Codex hook road, Codex/OpenCode scrape road) the spec records, with evidence, whether a hook or wrapper can **alter a tool result before the agent sees it** — a decision per road, not a hope ([Decision 1](#decision-1--per-provider-mechanism))
- [ ] Where a road can, a sensitive role's tool output is scrubbed **before** it enters the conversation, with the `PIIScrubNotice` banner, and a test proves the agent-facing text is the scrubbed one
- [ ] Where no road can, the guard enforces the pipe for the commands known to leak: a bare `ps eww`, `ps e`, `env`, `printenv`, `cat` of a muxcode config file, in a PII-sensitive role, is **denied** with the piped form named in the reason; the piped form (`… | muxcode pii-scrub`) is allowed; non-sensitive roles are untouched (`CheckGuard`)
- [ ] **Negative control:** the same commands on a non-sensitive role, and ordinary commands on a sensitive role, pass the guard unchanged
- [ ] `muxcode agent`'s model copy of a result goes through `ScrubForRole` — the one conversation road muxcode owns outright
- [ ] `CLAUDE.md` and [`docs/agents.md`](../../agents.md) *Coverage by road* state, per provider, what the conversation road now covers and what it still does not
- [ ] `bash scripts/test-pii-scrub-conversation.sh` passes

### Technical approach

Investigate before building. Phase 1 establishes, per provider, what a hook can and cannot do to a tool
result — for Claude, whether a `PostToolUse` or `PreToolUse` response can replace the result the model
receives; for the Codex hook road, the same question against its hook contract; for the scrape roads,
nothing short of a wrapper executor. The answer decides whether Phase 3 exists. Phase 2 is the floor that
exists regardless: a guard rule in `CheckGuard` for the known-leaky commands on sensitive roles, denying
the bare form and naming the piped one — the shape the edit guard already uses for prohibited commands.
`muxcode agent`'s model copy is muxcode's own code and is scrubbed in Phase 2 as well.

### Key files

| File | Role |
|------|------|
| `tools/muxcode/bus/hook.go` | `ProcessBashHook` (the history road), `CheckGuard`/`GuardDecisionFor` (the guard), `hookSpecificOutput` note at `:1607` |
| `tools/muxcode/bus/scrub.go` | `ScrubForRole`, `IsPIISensitiveRole` |
| `tools/muxcode/cmd/agent.go` | the model copy of a result |
| `tools/muxcode/bus/provider_claude.go`, `provider_codex.go`, `provider_opencode.go` | per-provider roads |
| `agents/api-tester.md`, `agents/runner.md`, `agents/log-watcher.md` | where the pipe is currently advice |
| `scripts/test-pii-scrub-conversation.sh` (new) | integration test |

## Implementation

### Phase 1: Per-provider mechanism

- [ ] Claude: test whether a hook response can replace the tool result the model receives; record the finding with the Claude Code version
- [ ] Codex hook road: the same against its hook contract; scrape roads: record that only a wrapper could
- [ ] Record [Decision 1](#decision-1--per-provider-mechanism)

### Phase 2: The floor — guard-enforced pipe and the model copy

- [ ] `CheckGuard`: deny the bare leaky commands on a PII-sensitive role, reason naming `… | muxcode pii-scrub`; allow the piped form
- [ ] `muxcode agent`'s model copy through `ScrubForRole`
- [ ] Tests: denied bare, allowed piped; **negative control:** non-sensitive role and ordinary commands untouched

### Phase 3: Pre-conversation scrub where a road allows it

- [ ] Implement for each road Phase 1 found able; a test that reads the agent-facing result, not the history row
- [ ] Skip, with the reason recorded, for each road that cannot

### Phase 4: Docs

- [ ] `CLAUDE.md` PII bullet and `docs/agents.md` *Coverage by road*: the conversation row per provider; the definitions' pipe instruction becomes "enforced by the guard"

### Phase 5: Integration test

- [ ] Create `scripts/test-pii-scrub-conversation.sh` — hermetic scratch bus; a sensitive role's bare `ps eww` is denied by the guard and the piped form passes; a non-sensitive role's is allowed (negative control); the model copy of a result is scrubbed; where Phase 3 landed, the agent-facing result is scrubbed
- [ ] Coverage floor pinned to the exact pass count; run through the run agent (foreground) and record counts here

## Decisions

### Decision 1 — per-provider mechanism

Open until Phase 1. The honest default, if no provider road can alter a result, is that the guard is
the whole fix and the docs say the conversation is protected by **denial**, not redaction.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-179](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md) | Parent — the history half, done; criterion 2's conversation half deferred here |
| [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) | Where the original leak and the narrow `ps eww -p … \| grep` idiom are recorded |
| [MUX-157](./MUX-157-role-boundary-an-agent-can-ignore.md) | The same shape one road up — a rule with no enforcement |

## Status

Backlog
