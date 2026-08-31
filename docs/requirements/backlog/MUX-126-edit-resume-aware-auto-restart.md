# Resume-Aware Auto-Restart for the Edit Agent

An environmental event on 2026-08-31 killed all four Claude Code agent processes at once — `edit`,
`plan`, `run`, `commit` (OpenCode agents were unaffected). The daemon restarted `plan`, `run` and
`commit` with their **full launch flags but fresh conversations**. `edit` is hard-excluded from
auto-restart, so it came back only via a bare `claude --resume`, which restores the conversation and
**none of the launch flags**.

The resumed `edit` session therefore ran **without `--dangerously-skip-permissions`**, and without
`--agent`, `--agents`, `--allowedTools` and `--append-system-prompt`. The user's interactive agent
silently lost the permission mode and tool profile it was launched with.

Today the two recovery paths are strictly complementary, and each is missing exactly what the other
has:

| Path | Conversation | Launch flags |
|------|--------------|--------------|
| Daemon restart (`plan`/`run`/`commit`) | ✗ lost | ✓ full |
| Manual bare `claude --resume` (`edit`) | ✓ kept | ✗ lost |

This spec makes one path that keeps both.

Tracking: [#51](https://github.com/mkober/muxcode/issues/51)

## Context

### Recurrence: a second simultaneous death, same day

The opening incident is **not a one-off**. At `13:42:31` the same day, three Claude Code agents —
`plan`, `run`, `commit` — failed their health check *in the same second*. OpenCode agents were again
unaffected. Cause remains unknown.

Timeline as recorded in the lifecycle log (not reconstructed from memory):

| Time | Event |
|------|-------|
| 13:42:31 | `agent-health-fail` ×3 — `plan`, `run`, `commit` failure #1, same second |
| 13:43:02 | failure #2, all three |
| 13:43:33–34 | failure #3 → `agent-restart` **attempt 1/3** each → `agent launch cli=claude` |
| 13:43:50 | `force-deliver plan: 2 messages` |
| 13:44:03 | `agent-recovered` ×3 |
| 13:44:06 | `force-deliver run: 1`, `commit: 1` |

Two counters are easy to conflate and are distinct: the restart fires on the **third failed health
check**, and the restart itself then succeeded on **attempt 1 of 3**. Recovery took ~92 s end to end,
~30 s from restart to recovered. No work was lost — `plan`'s pending messages were force-delivered
and processed.

What this recurrence contributes to this spec:

- **The premise is durable.** A simultaneous multi-agent Claude death has now happened twice in one
  day, so the recovery path this spec changes is exercised in practice, not hypothetically.
- **The non-edit path worked exactly as designed** — full launch flags, fresh conversation, recovered
  in ~30 s. That is the half of the table this spec wants to preserve while adding the other half.
- **`edit` survived this time**, so the gap this spec exists to close was *not* exercised. Had `edit`
  been in the set, it would once again have come back without `--dangerously-skip-permissions`. The
  spec's value is unchanged; this incident simply did not happen to demonstrate it.
- **Root cause is still open** (see Risks). Three Claude processes dying in the same second while
  OpenCode agents in the same session are untouched points at something Claude-specific or
  environmental rather than at any one agent's workload.

### Why `edit` is excluded today

`agentHealthExcludedRoles` (`bus/agent_health.go:13`) is a two-entry map with a one-line rationale:

```go
// edit: user's interactive session.
// webhook: managed separately, not a tmux-based agent.
var agentHealthExcludedRoles = map[string]bool{
	"edit":    true,
	"webhook": true,
}
```

The exclusion is sound in its original intent — an auto-restart injects `C-c` followed by a launch
command **into the pane the human is typing in**. It is the blast radius, not the restart itself,
that justified the carve-out. Removing the exclusion without preserving that caution would trade a
lost permission mode for a killed live session.

### How a restart actually relaunches (correcting the request notes)

The request notes describe relaunching "with the full `BuildExecArgs` flag set". That is the right
outcome but not the mechanism, and the difference decides where the change goes.

`RestartLocalAgent` (`bus/health.go:281`) does **not** exec anything directly. It sends keys:

```go
launchCmd := fmt.Sprintf("muxcode agent launch %s", role)
```

So flags are not assembled at the restart site at all — `muxcode agent launch` resolves the provider
and calls that provider's `BuildExecArgs`, which is why daemon-restarted roles already come back
fully flagged. **The gap is not missing flags; it is that `muxcode agent launch` has no notion of
resuming.** The change point is therefore the launch command plus an id scraped before relaunch —
not a new flag-assembly path.

`BuildExecArgs` is also a **`Provider` interface method** (`bus/provider.go:40`), implemented
per-provider, not a `bus/launch.go` function as the CLAUDE.md code-reference table currently lists
it. Only the Claude implementation can honour `--resume`.

### Sequencing constraint

`RestartLocalAgent` sends `C-c`, sleeps 500 ms, then types the launch command into the pane. The
session id is only available from the dead pane's exit message:

```
Resume this session with: claude --resume <id>
```

That text is **destroyed by the relaunch itself**. The scrape must therefore happen *before* the
`C-c`/send-keys sequence, not inside or after it. A resume implementation that reads the pane after
relaunching will find its own launch command and silently fall back to a fresh start every time —
passing any test that only asserts "the agent came back".

The codebase already knows this exact string: `provider_claude.go:257–258` and `:278` special-case
it so the exit message does not false-positive the startup check. That precedent is the parsing
site to reuse, not a second dialect.

### Exclusion fan-out

`IsAgentHealthExcluded` (`bus/agent_health.go:44`) has **three non-test call sites**, so
un-excluding `edit` changes behaviour in three places, not one:

| Call site | Effect of un-excluding `edit` |
|-----------|-------------------------------|
| `daemon/daemon.go:1600` | edit enters the health sweep — the intended change |
| `cmd/agent_health.go:36` | `muxcode agent-health` starts reporting/acting on edit |
| `bus/inspect.go:34` | edit's health surfaces in inspection output |

Scoping the change to the daemon alone would leave the other two inconsistent. `webhook` must remain
excluded throughout.

## Requirements

### Acceptance criteria

- [ ] A dead `edit` agent is auto-restarted by the daemon with **both** its conversation and its full
      launch flags — `--resume <id>` **and** `--dangerously-skip-permissions`, `--agent`, `--agents`,
      `--allowedTools`, `--append-system-prompt`
- [ ] The session id is scraped from the pane **before** any `C-c` or relaunch keystroke is sent
- [ ] When no session id can be scraped, the restart falls back to a **fresh launch with full flags**
      (current non-edit behaviour) — never a flagless resume
- [ ] `--resume <scraped-id>` is used rather than `--continue` (most-recent-in-cwd resolves wrongly
      when `auto` runs Claude in the same repo)
- [ ] Daemon restarts of the other Claude roles (`plan`, `run`, `commit`) also resume their
      conversation, with fresh-start as fallback
- [ ] Non-Claude providers (OpenCode, Codex, local harness) are unaffected — no resume is attempted
- [ ] `webhook` remains excluded from auto-restart
- [ ] Restart fires only on the existing bare-shell-prompt down-detection (3 failed health checks) —
      never against a busy or frozen-but-alive process
- [ ] Existing reload markers and `agent-health --stop` markers still suppress the restart
- [ ] The existing restart cap (3 attempts) and `agent-restarting`/`agent-down` alerts still apply to
      `edit`
- [ ] Feature is **default ON** with an env opt-out (`MUXCODE_EDIT_AUTO_RESTART_DISABLE=1`)
- [ ] Lifecycle events are emitted for detect, scrape (hit and miss) and relaunch
- [ ] A manual escape hatch exists: `muxcode resume <role>` (or `muxcode reload <role> --resume`)
- [ ] All three `IsAgentHealthExcluded` call sites behave consistently for `edit`

### Technical approach

1. **Teach the launcher to resume.** Add a resume option to `muxcode agent launch` that appends
   `--resume <id>` for the Claude provider only. Every other flag continues to come from the
   provider's existing `BuildExecArgs`, so the flag set cannot drift from a normal launch.
2. **Scrape before interrupting.** In the restart path, capture the pane and extract the id from
   `Resume this session with: claude --resume <id>` *first*, reusing the string already recognised at
   `provider_claude.go:257–278`. Pass the id to the launch command; on no match, launch fresh.
3. **Un-exclude `edit`, keep the caution.** Remove `edit` from `agentHealthExcludedRoles` and gate
   its restart on the env opt-out plus the unchanged 3-failed-check down-detection. `webhook` stays.
4. **Generalise to the other Claude roles.** The same scrape-then-resume applies to `plan`, `run`
   and `commit`, turning a cold restart into a mid-task resume.
5. **Manual path.** `muxcode resume <role>` performs the same scrape-then-relaunch on demand.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/agent_health.go` | `agentHealthExcludedRoles` (:13), `IsAgentHealthExcluded` (:44) — the exclusion to lift |
| `tools/muxcode/bus/health.go` | `RestartLocalAgent` (:281) — scrape must precede its `C-c`/send-keys |
| `tools/muxcode/bus/provider_claude.go` | `PermFlags` (:54), flag assembly (:82), resume-string precedent (:257–278) |
| `tools/muxcode/bus/provider.go` | `BuildExecArgs` interface method (:40) — Claude-only resume support |
| `tools/muxcode/daemon/daemon.go` | Health sweep exclusion check (:1600), restart cap and alerts (~:1695) |
| `tools/muxcode/cmd/agent_health.go` | Second exclusion call site (:36) |
| `tools/muxcode/bus/inspect.go` | Third exclusion call site (:34) |
| `scripts/test-edit-auto-resume.sh` | Integration test (Phase 5) |

## Implementation

### Phase 1: Session id scrape

- [ ] Add a scrape helper that extracts `<id>` from `Resume this session with: claude --resume <id>`
- [ ] Reuse the recognition point at `provider_claude.go:257–278` rather than adding a second pattern
- [ ] Return a clear "no id found" result distinct from an empty id
- [ ] Unit tests: id present, id absent, malformed line, multiple occurrences (take the most recent)

### Phase 2: Resume-capable launch

- [ ] Add a resume option to `muxcode agent launch` that appends `--resume <id>` for Claude only
- [ ] Verify every other flag still comes from `BuildExecArgs` (no parallel flag-assembly path)
- [ ] No-op the option for OpenCode, Codex and local harness providers
- [ ] Unit tests asserting `--resume` **and** `--dangerously-skip-permissions` are both present

### Phase 3: Restart path wiring

- [ ] Scrape the pane **before** sending `C-c` in `RestartLocalAgent`
- [ ] Pass the scraped id into the relaunch command; fall back to a fresh flagged launch on no id
- [ ] Emit lifecycle events for detect, scrape-hit, scrape-miss and relaunch
- [ ] Confirm reload markers and `agent-health --stop` markers still suppress the restart

### Phase 4: Un-exclude edit

- [ ] Remove `edit` from `agentHealthExcludedRoles`; keep `webhook`
- [ ] Add the `MUXCODE_EDIT_AUTO_RESTART_DISABLE=1` opt-out (default ON)
- [ ] Update the rationale comment to record why edit is now included and what still protects the pane
- [ ] Audit all three `IsAgentHealthExcluded` call sites for consistent behaviour
- [ ] Confirm the restart cap and `agent-restarting`/`agent-down` alerts apply to edit

### Phase 5: Integration test

- [ ] Create `scripts/test-edit-auto-resume.sh` with end-to-end verification
- [ ] Kill a scratch edit pane's `claude` process → verify relaunch carries **both** `--resume <id>`
      and `--dangerously-skip-permissions`
- [ ] **Negative control**: no scrapeable id → verify fallback is a *fresh flagged* launch, and
      assert the absence of a flagless resume (not merely that the agent returned)
- [ ] **Negative control**: `MUXCODE_EDIT_AUTO_RESTART_DISABLE=1` → verify no restart fires
- [ ] **Negative control**: a busy/alive agent → verify no restart fires
- [ ] Verify `webhook` exclusion still holds
- [ ] Verify reload-marker and `agent-health --stop` suppression still hold
- [ ] Assert the scrape happens before the interrupt — a post-relaunch scrape must fail the test, not
      silently pass via the fallback
- [ ] Include a coverage floor so a skipped run cannot report green
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Restart injects into the user's live pane | The original reason for the exclusion | Fire only on the existing 3-failed-check down-detection; never on busy/alive |
| Scrape placed after relaunch | Reads its own launch command, silently falls back forever | Phase 5 asserts ordering explicitly |
| Fallback masks a broken scrape | Every run "succeeds" via fresh start; the feature is inert | Negative control distinguishes resumed from fresh |
| `--continue` used instead of `--resume` | Resolves to the wrong session when `auto` runs Claude in the same repo | Criterion pins `--resume <id>` |
| Un-exclusion applied only in the daemon | Two other call sites diverge | Phase 4 audits all three |
| Underlying cause of simultaneous Claude death is unknown | This spec improves *recovery* and does nothing about *frequency*; two multi-agent deaths in one day means the path will keep being exercised | Out of scope here — needs its own root-cause investigation once the current run settles. Evidence so far: both events hit only Claude Code agents, OpenCode agents in the same session survived both, and the deaths land within the same second across unrelated roles |

## Status

Backlog
