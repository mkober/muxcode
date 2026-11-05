# The Watch Chain Fires on Every Bash Call and Pastes the Raw Command Into the Message

**Tracking:** [mkober/muxcode#135](https://github.com/mkober/muxcode/issues/135)

**Provenance:** filed 2026-09-10 by plan on the user's request relayed by edit (`1789070947`), after the
watch agent reported and then root-caused it itself at 15:58. Verified in code by plan before filing.

The `watch` role's event chain has **no conditions on any branch**. Every classified bash call in the
watch pane sends edit a `notify`, and the message interpolates `${command}` verbatim — so a
multi-line heredoc, a `ps` pipeline or a scratch `echo` block arrives at edit as its own raw text
wrapped in "logs look healthy after deploy (…)". Four such messages reached edit inside three minutes
while watch was investigating an unrelated question.

The same class was already fixed for the **run** chain, which carries a `command_match` allowlist and
a `command_not_match: "muxcode *"` exclusion. Watch never got either.

## Context

### Observed (2026-09-10, session `muxcode`)

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 15:47 | `watch → edit [event:notify]` "Watch completed — logs look healthy after deploy (`BUS_DIR=…` / `echo` / `ls -la` / `cat serve-state.json`)" — a four-line shell block as the message body | bus history | **machine-written (chain)** |
| 15:48 | same shape, body is a `tail` / `ps aux \| grep` block | bus history | **machine-written (chain)** |
| 15:49 | same shape, body is a `ps -o pid,ppid,stat,lstart,command` block | bus history | **machine-written (chain)** |
| 15:58 | watch → run, retracting an unrelated claim and naming this as the cause: the chain "fires on every successful watch-role bash call and interpolates the raw multi-line `${command}` verbatim into the notify — confirmed 4 such spam messages fired to edit" | bus history | agent self-report |

None of the four described a log finding. All fired because a bash call in the watch pane exited 0.

### Recurrence (2026-09-22, session `muxcode`)

| When | Evidence | Source | Provenance |
|------|----------|--------|------------|
| 10:09:23 | `watch → edit [event:notify]` "Watch completed — logs look healthy after deploy (`nohup muxcode inbox --poll --loop > /tmp/watch-listener.log 2>&1 & disown; sleep 1; ps aux \| grep …`)" — the body is a listener-detaching call, not a log finding | bus history | **machine-written (chain)** |

The call itself is the [MUX-156](./MUX-156-orphaned-inbox-listener-consumes-into-the-void.md)
2026-09-22 entry, and is now refused at PreToolUse by `CheckListenerGuard`. The chain defect is
**not** fixed by that change and remains this spec's work. A second facet of the mechanism, verified
in code the same day: `ClassifyCommand`'s bus test is a raw `strings.HasPrefix(command, "muxcode")`
(`bus/hook.go:306`), so a `nohup`-, `cd … &&`- or env-prefixed bus command is not `CmdBus` but
`CmdUnknown` — and in the watch role `ChainEvent` returns `watch` for an unclassified call, whose
`OnSuccess` has no conditions. The `command_not_match: "muxcode *"` exclusion the run chain carries
would have the same blind spot for a prefixed bus call; the fix should match on the statement after
`stripCommandPrefix`, not the raw command.

### Mechanism — verified in code, not inferred

`tools/muxcode/bus/profile.go:976–995`, the `"watch"` chain:

| Branch | Message | Conditions |
|--------|---------|------------|
| `OnSuccess` | `Watch completed — logs look healthy after deploy (${command})` | **none** |
| `OnFailure` | `Watch detected errors (exit ${exit_code}): ${command} — …` | **none** |
| `OnUnknown` | `Watch completed (exit code unknown): ${command}` | **none** |

Contrast `runWatchActions` (`profile.go:555–570`), which builds the run chain's `OnSuccess`:

```go
Conditions: map[string]any{
    "command_match":     p,              // an allowlist of verification commands
    "command_not_match": "muxcode *",    // never fire on a bus call
},
```

A comment at `profile.go:949` refers to a prior **`run-chain-watch-overfire`** fix — the precedent
that this exact overfiring was already recognised and gated for `run`. The watch chain was not
revisited.

Two distinct defects, separable and worth separating:

| # | Defect | Consequence |
|---|--------|-------------|
| 1 | **No condition gate** — any classified bash call in the watch pane notifies edit | Volume: routine investigation becomes a message storm; watch's own `muxcode send` calls also qualify, so the chain can amplify itself |
| 2 | **Raw `${command}` interpolation** | Shape: a multi-line body violates the delegation-message-hygiene rule, and per `CLAUDE.md` such a payload misses the `Bash(muxcode *)` glob and **prompts instead of delivering** — so the noise is not merely cosmetic |

Defect 2 is shared textually by the run and serve chains, which also interpolate `${command}`; there
it is largely masked because their allowlists keep the matched commands short and predictable. Any
fix should decide whether to bound the interpolation everywhere or only gate watch.

### Scope boundary

In scope: the watch chain's missing conditions, and the shape of `${command}` in chain messages. Not
in scope: the PID-ownership mistake watch made in the same window (watch retracted it at 15:58 and it
had no chain involvement), and the `run` chain's own allowlist, which is working as designed.

## Requirements

### Acceptance criteria

- [x] A routine bash call in the watch pane that produces no log finding sends edit **no** `notify` — Phase 1, 2026-11-03: 16 non-firing rows incl. the 2026-09-10 scratch block and 2026-09-22 listener call verbatim, across all three outcomes; `ps` through the real hook
- [x] A genuine watch finding still reaches edit — pinned by a test, so the fix does not buy quiet by disabling the chain — Phase 1, 2026-11-03: `TestWatchChainLogAllowlist` firing rows and `TestWatchRoleChainGate` (`tail` notifies on exit 0 and 1)
- [x] No chain message carries a multi-line body; `${command}` is bounded (single line, truncated with an explicit marker) or omitted — Phase 2, 2026-11-04: `boundCommand` joins lines with ` ⏎ ` and cuts at 200 bytes with `… [truncated N bytes]`, on every chain and subscription expansion; the other variables (`${branch}`, `${serve_url}`, `${changed_files}` — comma-joined, 10 names) were already one line
- [x] A chain message that would exceed the hygiene limits is truncated rather than sent raw, and the truncation is visible in the message — Phase 2, 2026-11-04: `TestChainMessagesPassValidatePayload` expands a 1.5k heredoc through every default chain action that names `${command}` (run, watch, serve all reached) and asserts `validatePayload()` accepts the result and the `[truncated` marker is in it; the raw heredoc failing is the control
- [x] Watch's own `muxcode *` calls never fire the watch chain (the exclusion the run chain already has) — Phase 1, 2026-11-03: `command_not_match: "muxcode *"` on every branch, now also tested after `stripCommandPrefix`, so `cd dir && muxcode …` and `VAR=1 muxcode …` are excluded too (`TestCommandNotMatch`, the nohup and cd-prefixed rows)
- [ ] `docs/hooks.md` chain table and `CLAUDE.md` record the watch chain's gate alongside the run chain's

### Technical approach

The narrow fix is to give the watch chain the treatment the run chain already has: a `command_match`
allowlist of log-inspection commands plus `command_not_match: "muxcode *"`. That reuses
`EvaluateConditions()` and needs no new mechanism.

The interpolation is a separate decision. Bounding `${command}` at the point of substitution fixes it
for every chain at once and is the smaller change; gating watch alone leaves the same trap set for
the next role that gets a chain without an allowlist. Prefer the bounded interpolation, with the
allowlist as well — they address different halves.

**The load-bearing test is the negative control:** a real watch finding must still notify edit. A fix
that simply stops the chain firing would satisfy every "no spam" assertion and silently remove watch's
only route to report.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/profile.go` | the `"watch"` chain (976–995); `runWatchActions` (555–570) as the model; the `run-chain-watch-overfire` comment at 949 |
| `tools/muxcode/bus/conditions.go` | `EvaluateConditions()`, `command_match` / `command_not_match` |
| `tools/muxcode/bus/hook.go` | where chain messages are built and `${command}` is substituted |
| `tools/muxcode/bus/inbox.go` | `validatePayload()` — the hygiene limits the raw body violates |
| `docs/hooks.md`, `CLAUDE.md` | chain tables and the constraint bullet |

## Implementation

### Phase 1: Gate the watch chain

- [x] Add `command_match` (log-inspection allowlist) and `command_not_match: "muxcode *"` to the watch chain's branches — 2026-11-03, as `first_command_match` (the allowlist read after `stripCommandPrefix`, see findings) plus `command_not_match: "muxcode *"` on all three branches (`watchNotifyActions`, `profile.go`)
- [x] Unit tests: a `tail`/`grep` log command fires; a `ps`/`echo`/`ls` scratch command does not; a `muxcode send` never does — `TestWatchChainLogAllowlist` (11 firing, 16 non-firing rows × 3 outcomes), `TestCommandNotMatch`, `TestWatchRoleChainGate`; suite green `./test.sh` ts 1793727902 on the fix tree
- [x] **Negative control:** a genuine log-inspection command still fires the notify — `tail`, log `grep`, `aws`/`kubectl`/`docker logs`, `log show`, cd- and env-prefixed tails notify edit on success, failure and unknown; through the real hook, `tail` on exit 0 and 1

#### Phase 1 findings (2026-11-03)

Verified 12:45 on the fix tree: `./build.sh` ts 1793727826, `./test.sh` ts 1793727902 (both after the
last file write at 1793727792), review `1793727928-review-0a4e79d9` 0 must-fix / 0 should-fix / 0 nits
("prior wildcard finding is resolved"). Uncommitted at verification, on branch
`MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload`
(graph run `1793727228-50-spec-to-pr-5f982531`):

| Item | In the tree |
|------|-------------|
| Gate | `watchLogShapes` (`profile.go`: `tail *`, `journalctl *`, `grep *.log`/`*/log(s)/*`, `aws logs`/`cloudwatch`, `gcloud logging`, `az monitor`, `kubectl logs`/`get events`, `docker(-compose) logs`, `stern`, `log show`/`stream`, `xcrun simctl spawn * log …`); `watchNotifyActions` puts one first-match-wins action per shape on **all three** branches (`OnSuccess`/`OnFailure`/`OnUnknown`), each with `first_command_match: <shape>` and `command_not_match: "muxcode *"` |
| New condition | `first_command_match` (`conditions.go`, the 15th type): the glob is read against `stripCommandPrefix(command)` — a genuine `cd <dir> &&` and env assignments removed — so no `cd * && <reader>` shape is needed. `command_not_match` now tests the stripped command as well as the raw one, so `cd dir && muxcode send …` is excluded (the Recurrence note's blind spot) |
| Tests | `TestWatchChainLogAllowlist` (`conditions_test.go`): 11 firing rows (tail, grep of a log, aws/kubectl/docker logs, `log show`, cd- and env-prefixed tails) × 3 outcomes, 16 non-firing rows (echo, ls, `ps aux \| grep`, cat, a code grep, `gh pr checks`, sleep, curl, the 2026-09-10 scratch block, the 2026-09-22 detached listener, `muxcode send`/`inbox`, and the review's `cd /repo && echo "example && tail app.log"`, `cd … && muxcode send … && tail`, `cd … && ls && tail`); `TestCommandNotMatch` rows for cd- and env-prefixed `muxcode`; `TestWatchRoleChainGate` (`hook_test.go`) through the real `ProcessBashHook` + `BuildChainContext`: the prefixed bus call, the cd-prefixed send and a `ps` resolve no edge, a `tail` notifies edit on exit 0 and 1 |

Review history: the first review (`1793727743`, 1 must-fix) found that a raw `cd * && tail *` shape's
first `*` spanned later `&&`s and quoted text, so `cd /repo && echo "example && tail app.log"` read as
a log read; the fix replaced every `cd *` shape with `first_command_match` and added that command, a
`cd … && muxcode send … && tail` and a `cd … && ls && tail` as non-firing rows. The first verify-spec
(12:42) held the boxes because that suite run predated the fix; the fix tree's own chain ticked them.

Known limit, left for Phase 2 by design: a multi-line block whose **first** line is a log reader still
notifies — the allowlist gates on the first statement, and bounding the body is Phase 2's work.
Follow-up for Phase 3: `first_command_match` is the 15th condition type — `docs/hooks.md`'s table,
`docs/architecture.md`'s count (14 as of 2026-11-02) and `CLAUDE.md`'s "13 types" all move.

### Phase 2: Bound the interpolation

- [x] `${command}` substitution collapses newlines and truncates at a fixed width with an explicit marker — 2026-11-04: `boundCommand` (`profile.go`): lines trimmed, blanks dropped, joined with ` ⏎ `; cut at a rune boundary within `chainCommandMaxLen` = 200 bytes, suffixed `… [truncated N bytes]`
- [x] Applies to every chain that interpolates `${command}` (run, watch, serve), not watch alone — 2026-11-04: the one substitution point is `ExpandMessageWithContext` (`ExpandMessage` now delegates to it), so every chain is bounded; `ExpandSubscriptionMessage` bounds the same way
- [x] Unit test: a multi-line heredoc command yields a single-line bounded message that passes `validatePayload()` — 2026-11-04: `TestChainMessagesPassValidatePayload` (`cmd/send_test.go`, every default chain action naming `${command}`, run/watch/serve asserted reached), `TestBoundCommand` (`profile_test.go`: heredoc with `\r\n` and blank lines, a 237-byte line, a multibyte cut), a subscription row; suite green `./test.sh` ts 1793808379
- [x] **Negative control:** a short single-line command is passed through unchanged — 2026-11-04: `TestBoundCommand` unchanged rows — a `tail`, a tab-holding `grep`, exactly 200 bytes, empty — byte-identical; the raw heredoc failing `validatePayload()` proves the check can see the defect

#### Phase 2 findings (2026-11-04)

Verified 11:06 on the tree: `./build.sh` ts 1793808313, `./test.sh` ts 1793808379 (both after the last
file write at 1793808289), review `1793808406-review-782db78e` 0 must-fix / 0 should-fix / 0 nits
("prior expansion finding resolved"). Uncommitted at verification, on branch
`MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload`
(graph run `1793727228-50-spec-to-pr-5f982531`):

| Item | In the tree |
|------|-------------|
| Bound | `boundCommand` (`profile.go`): a command holding `\n` or `\r` is split, each line trimmed, blanks dropped, the rest joined with ` ⏎ `; then, past `chainCommandMaxLen` (200 bytes — with the longest default template it keeps the message under `validatePayload()`'s 500), cut back to a rune start and suffixed `… [truncated N bytes]`. Within the limit and on one line, the command is returned as is |
| Reach | `ExpandMessageWithContext` substitutes `${command}` through `boundCommand`; `ExpandMessage` is now a nil-ctx call to it, so the run, watch and serve chains (and any user chain) share the bound. `ExpandSubscriptionMessage` applies it too |
| One pass | Both expanders now build a `strings.NewReplacer` and replace every token in one pass over the **template**, with the git and serve lookups keyed on the template rather than the partly expanded string — a substituted value is never rescanned, so a command holding a literal `${changed_files}` cannot expand after the bound (the review's finding; `TestExpandMessageWithContextNoRescan`, `TestExpandSubscriptionMessage_NoRescan`, each with the template's own tokens expanding as the control) |
| Tests | `TestBoundCommand` (heredoc → one line; 237 bytes → 200 + `[truncated 37 bytes]`; `x` + 200 × `é` cut to a valid string at 199 bytes; four unchanged rows; `ExpandMessage` bounding); `TestChainMessagesPassValidatePayload` (`cmd`): a 1.5k heredoc opening with a literal `${changed_files}`, expanded through every default chain action naming `${command}` with a ten-long-path context, passes `validatePayload()` and shows `[truncated`, with run, watch and serve asserted reached and the raw heredoc asserted rejected; a `multi-line command bounded` row in `TestExpandSubscriptionMessage` |

No integration script for this phase; Phase 4's `scripts/test-watch-chain-gate.sh` carries the
"no message body contains a newline" assertion end to end. Residual, outside this spec: `${changed_files}`
is bounded by count (10 names), not bytes — no default chain template names it, so it reaches a message
only from a user-written chain.

### Phase 3: Docs

- [ ] `docs/hooks.md` chain table: the watch chain's gate, beside the run chain's
- [ ] `CLAUDE.md`: extend the run-chain allowlist note to say every notifying chain is gated and `${command}` is bounded
- [ ] `docs/hooks.md` conditions table: a `first_command_match` row — glob read against `stripCommandPrefix(command)` (a genuine `cd <dir> &&` and env assignments removed), why it is not `command_match` with a `cd * &&` shape (the first `*` spans later `&&`s and quoted text — the Phase 1 review finding)
- [ ] `docs/hooks.md` `command_not_match` row: now also tests the prefix-stripped command, so `cd dir && muxcode …` and `VAR=1 muxcode …` are excluded — this narrows the run chain's own `muxcode *` exclusion the same way, never widens it
- [ ] Condition count: `docs/architecture.md` 14 → 15 types with `first_command_match` named in its list (`docs/hooks.md` carries the table, no count; `CLAUDE.md` already reads 15 with both `swiftpm_query` and `first_command_match` — done by the Phase 1 worker, nothing left there)

### Phase 4: Integration test

- [ ] `scripts/test-watch-chain-gate.sh`: run a scratch non-log bash call in a watch-role pane and assert **no** notify reaches edit
- [ ] **Negative control:** a log-inspection call in the same pane **does** notify edit
- [ ] Assert no message body contains a newline
- [ ] Coverage floor pinned to the exact pass count so a skipped section cannot read green
- [ ] Run through the run agent (**foreground**, per MUX-171) and record the counts here

## Notes

**Found by the agent it was firing from.** Watch reported the noise, initially misattributed it to an
orphaned listener it had killed, and then retracted and root-caused it correctly. The retraction is
the reason this is filed against the chain and not against a process. Recorded because the first
explanation was wrong and the record should show why the second is better: the four messages are in
the bus history with chain-written bodies, and the missing `Conditions` are in the source.

**Related:** [MUX-176](../completed/MUX-176-run-chain-fires-success-on-backgrounded-call.md) — the other chain
defect in the same session, where the run chain fired success on an unfinished call. Both are chain
edges firing on evidence that does not support them.

**Recurred 2026-09-22** on a `nohup muxcode inbox …` call, which also showed that a prefixed bus
command escapes the `HasPrefix("muxcode")` classification and lands in the unconditioned watch
chain — recorded under [Recurrence](#recurrence-2026-09-22-session-muxcode); not re-scoped.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-177-watch-chain-fires-every-bash-call-with-raw-command-payload | 3m | 2026-11-03 12:45 |

## Status

In Progress — active spec since 2026-11-03 (set by the user). Phase 1 (Gate the watch chain) complete 2026-11-03 12:45: allowlist + `muxcode *` exclusion on all three branches, review must-fix (raw `cd *` shapes) fixed with `first_command_match`, suite green ts 1793727902, review 1793727928 clean ([Phase 1 findings](#phase-1-findings-2026-11-03)); committed as `66d7622`. Phase 2 (Bound the interpolation) complete 2026-11-04 11:06: `boundCommand` (` ⏎ `-joined, 200-byte cut with a visible marker) on every chain and subscription expansion, one-pass token replacement after the review's rescan finding, suite green ts 1793808379, review 1793808406 clean ([Phase 2 findings](#phase-2-findings-2026-11-04)); uncommitted at verification. Phase 3 (Docs) next. Recurred 2026-09-22 (see [Recurrence](#recurrence-2026-09-22-session-muxcode)).
