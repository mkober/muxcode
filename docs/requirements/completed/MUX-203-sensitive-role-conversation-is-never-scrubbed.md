# A Sensitive Role's Own Conversation Is Never Scrubbed — PostToolUse Cannot Reach It

**Tracking:** _(no GitHub issue filed yet)_

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

- [x] For each provider road (Claude hooks, Codex hook road, Codex/OpenCode scrape road) the spec records, with evidence, whether a hook or wrapper can **alter a tool result before the agent sees it** — a decision per road, not a hope ([Decision 1](#decision-1--per-provider-mechanism)) (Phase 1: live probes with random sentinels and negative controls on claude 2.1.293, codex-cli 0.160.0 and OpenCode 1.18.34; the findings table and Decision 1)
- [x] Where a road can, a sensitive role's tool output is scrubbed **before** it enters the conversation, with the `PIIScrubNotice` banner, and a test proves the agent-facing text is the scrubbed one (Phase 3: Claude on exit 0 via the sync `hook scrub` answer; the Codex hook road on both outcomes via the `PreToolUse` wrap; OpenCode via the generated plugin — each tested on the agent-facing result, never the history row. The Claude non-zero-exit gap and the scrape road are the recorded residuals of Decision 1)
- [x] Where no road can, the guard enforces the pipe for the commands known to leak: a bare `ps eww`, `ps e`, `env`, `printenv`, `cat` of a muxcode config file, in a PII-sensitive role, is **denied** with the piped form named in the reason; the piped form (`… | muxcode pii-scrub`) is allowed; non-sensitive roles are untouched (`CheckGuard`) (Phase 2: `CheckPIIPipeGuard` — with one sharpening the review forced: a piped form is allowed only when it hands the scrubber **labelled** text, so `printenv NAME | …` and `env | cut -d= -f2 | …` are refused and the remedy filters *after* the scrub)
- [x] **Negative control:** the same commands on a non-sensitive role, and ordinary commands on a sensitive role, pass the guard unchanged (Phase 2: `CheckPIIPipeGuard`, `bus/pii_guard.go` — `TestCheckPIIPipeGuard_NonSensitiveRoleUntouched`, `TestCheckPIIPipeGuard_OrdinaryCommandsAllowed` (`ps -ef`, `env` running a command), `TestCheckPIIPipeGuard_SpawnWorkerUsesBaseRole`)
- [x] `muxcode agent`'s model copy of a result goes through `ScrubForRole` — the one conversation road muxcode owns outright (Phase 2: in `ToolExecutor.Execute`, before the output cap; `TestProcessMessages_ModelCopyScrubbed`, `TestExecuteRead_ScrubsBeforeTruncation`)
- [x] `CLAUDE.md` and [`docs/agents.md`](../../agents.md) *Coverage by road* state, per provider, what the conversation road now covers and what it still does not (Phase 4: eight per-provider rows and a *Not covered* list in `agents.md`; `CLAUDE.md:140`; denial and redaction stated as separate facts per road)
- [x] `bash scripts/test-pii-scrub-conversation.sh` passes (run agent 2026-10-08: hermetic 25 passed, 0 failed at floor 25; with `MUXCODE_PII_CONVERSATION_LIVE=1`, 25/0 plus 4 live — a real Claude and a real OpenCode each shown the placeholder and never the fixture secret in any model-visible text, with `build`-role controls reading the raw value)

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

- [x] Claude: test whether a hook response can replace the tool result the model receives; record the finding with the Claude Code version (live on claude 2.1.293: a **synchronous** `PostToolUse` `updatedToolOutput` in the Bash output shape replaces exit-0 output; the `{type,text}` shape and an `async` hook are ignored; a non-zero exit fires only `PostToolUseFailure`, which cannot replace; the `PreToolUse` wrap works only with `permissionDecision:"allow"` — findings table above)
- [x] Codex hook road: the same against its hook contract; scrape roads: record that only a wrapper could (live on codex-cli 0.160.0: `PreToolUse` `updatedInput` wrap + `allow` scrubs both outcomes with the rollout raw-free; `PostToolUse` `decision:block` replaces but mislabels success as failure and leaves the rollout raw; the scrape road runs no hook at all — not even the guard. OpenCode 1.18.34 probed too: a `tool.execute.after` plugin replaces, the WAL keeps raw)
- [x] Record [Decision 1](#decision-1--per-provider-mechanism) (filled per road; its coverage paragraph rewritten after review to state per-provider, per-outcome results from the table)

#### Phase 1 verification note

Verified 2026-10-07 16:55 by plan from the spec itself (run `1791404721`; a decision phase, no code
changed, test node success on the unchanged tree). Review returned **0 must-fix, 1 should-fix**:
Decision 1's closing paragraph claimed on-disk records are covered only where the scrub happens before
execution, contradicting the table's own Claude exit-0 probe, which found the transcript raw-free after
a post-execution replacement. Plan rewrote that paragraph per road and per outcome, as the review
prescribed, before the fix node reached it — the text was plan's; the fix node folded in one more fact
(Claude telemetry captures the original before any hook) and the second review pass returned 0/0/0.
Two items the worker left for the user at the Phase 1 commit gate: the Claude `PreToolUse` wrap is
**declined** because its `permissionDecision:"allow"` bypasses the role's allowlist; and the
`PostToolUseFailure` finding — `hook bash` never sees a failing Bash call on claude 2.1.293 — is surfaced
here but **not filed**, pending the user. The reviewer's own caveat stands and is worth keeping in view: the probes' transcripts were not supplied and the reviewer did not execute them;
Phase 5's integration script is where the Claude and Codex claims get re-proven on this repo's own
binary.

#### Phase 1 findings — per-provider mechanism (live, 2026-10-07)

Probed live by the Phase 1 worker (run `1791404721`), superseding the earlier docs-only lead.

**Method (reproducible).** Each probe ran the real CLI headless in a scratch project with
`BUS_SESSION`/`AGENT_ROLE` unset (muxcode's own hooks no-op), asked the model to run
`echo ORIGINAL-$RANDOM$RANDOM` (or `…; exit 3`) and reply with the tool result verbatim. A hook or plugin
swapped in `REPLACED-SENTINEL-9b2e` (or piped through `sed s/ORIGINAL/SCRUBBED/`). The raw value is random,
so the model can report it only if it saw it; the sentinel exists only in the hook. Every positive has a
negative control (hook silent → model reports the raw value). The provider's on-disk record was then
grepped for the raw value.

| Road (version) | Mechanism | Exit 0 | Non-zero exit | Raw value in the provider's on-disk record |
|---|---|---|---|---|
| Claude hooks (claude 2.1.293) | `PostToolUse` **sync** hook, `hookSpecificOutput.updatedToolOutput` in the **Bash output shape** `{stdout, stderr, interrupted, isImage}`, no `decision` needed | replaced (stdout and stderr cases) | **not reachable** — a non-zero exit fires `PostToolUseFailure` only, whose only output field is `additionalContext`; `updatedToolOutput` there is ignored, model saw raw | transcript: absent on exit 0; present on failure |
| Claude hooks | same field with a `{type,text}` value | **ignored silently**, model saw raw — a value not matching the tool's output schema is dropped (documented) | — | — |
| Claude hooks | `PostToolUse` hook with `"async": true` (how muxcode registers `hook bash`) | **cannot replace** — hook fired, model saw raw | — | — |
| Claude hooks | `PreToolUse` `updatedInput.command` = `{ <cmd>\n} 2>&1 \| <scrub>; exit ${PIPESTATUS[0]}` + `permissionDecision:"allow"` | scrubbed | scrubbed, `Exit code 3` kept | absent |
| Claude hooks | same rewrite **without** `permissionDecision` | **denied** — "A group in braces or double parentheses in this command can't be checked before it runs"; permission rules are evaluated against the rewritten command | — | — |
| Codex hook road (codex-cli 0.160.0, `--dangerously-bypass-hook-trust`, project `.codex/hooks.json`) | `PostToolUse` `decision:"block"` + `reason` (no rewrite field exists; `updatedMCPToolOutput` is parsed but unsupported) | replaced by `reason`, but framed by Codex as `Script failed … Script error:` — a scrubbed success reads as a failure | replaced, same framing; `PostToolUse` **does** fire on failure | rollout `~/.codex/sessions/…jsonl`: **raw kept** |
| Codex hook road | `PreToolUse` `updatedInput.command` (same wrap) + `permissionDecision:"allow"` (the only shape Codex accepts for `updatedInput`) | scrubbed | scrubbed, `exit_code` 3 kept in the rollout | absent |
| Codex scrape road (`MUXCODE_CODEX_HOOKS=0`, codex < 0.153, or `[features] hooks=false`) | none — no hook runs, so neither a rewrite **nor the guard** reaches it | — | — | — |
| OpenCode (1.18.34, `@opencode-ai/plugin` 1.18.33) | project plugin `.opencode/plugin/*.ts`, `"tool.execute.after"` mutating `output.output` (and `output.metadata.output`, which the TUI renders) | replaced | replaced, `metadata.exit` 3 kept | `~/.local/share/opencode/opencode.db-wal`: **raw kept** (the bash tool persists streaming output before the after-hook runs) |

Notes:

- muxcode ships no OpenCode plugin today (the plugin bridge is backlog [MUX-011](../backlog/MUX-011-opencode-plugin-hook-bridge.md)). `tool.execute.before` (mutable `output.args`) and `shell.env` (mutable `output.env`) exist in the installed types but were not probed.
- Docs sources: `code.claude.com/docs/en/hooks` (PostToolUse/PostToolUseFailure decision control); `learn.chatgpt.com/docs/hooks` (Codex PreToolUse/PostToolUse). Codex docs carry no version numbers, so 0.160.0 is the verified floor.
- The Phase 2 guard floor (`CheckGuard`) is itself a `PreToolUse` hook: it reaches Claude and the Codex hook road only. OpenCode's equivalent is a `deny` permission rule emitted from the profile's `DenyTools`; the Codex scrape road has no enforcement point at all.
- **Surfaced, out of scope, not filed from here** (edit is raising it with the user): muxcode registers `muxcode hook bash` on `PostToolUse` only (`config/settings.json`), and on claude 2.1.293 a non-zero-exit Bash call fires `PostToolUseFailure` and **no** `PostToolUse` — verified with a PostToolUse-only config (`exit 3`, hook fired 0 times). On Claude a failing command therefore writes no history row and feeds no hook-driven failure chain.

### Phase 2: The floor — guard-enforced pipe and the model copy

- [x] `CheckGuard`: deny the bare leaky commands on a PII-sensitive role, reason naming `… | muxcode pii-scrub`; allow the piped form (`CheckPIIPipeGuard`, `bus/pii_guard.go`, chained into `GuardDecisionFor` after the listener guard: on `api`/`run`/`runner`/`watch` — spawn workers by base role — a bare `env`, `printenv`, `ps eww`/`e`/`-E` or `cat` of a muxcode config is denied, `piiPipeReason` naming the remedy; `| muxcode pii-scrub` as the very next stage is allowed. After the review must-fix, **`printenv NAME` is denied even when piped** — it prints bare values no label rule can match — and the remedy preserves labels first: `printenv | muxcode pii-scrub | grep -E '^(NAME)='`; `env | cut -d= -f2 | …` is likewise refused)
- [x] `muxcode agent`'s model copy through `ScrubForRole` (after the review must-fix, redaction moved **into `ToolExecutor.Execute`** as the single scrub-then-cut point for every tool result — output, errors, write/edit reports, which can echo a command or path — `ScrubRole` set from the bus role, `ScrubForRoleWithNotice` before the `MaxOutputLen` cut, the bash/grep status line appended after it so truncation cannot drop it; `logBashToHistory` keeps its own pass as the row's guarantee for any other caller)
- [x] Tests: denied bare, allowed piped; **negative control:** non-sensitive role and ordinary commands untouched (`pii_guard_test.go`: `…_DeniesBareLeakyCommands`, `…_RemedyPassesGuard`, `…_AllowsScrubbedPipe`, `…_OrdinaryCommandsAllowed`, `…_NonSensitiveRoleUntouched`, `…_SpawnWorkerUsesBaseRole`, `TestGuardDecisionFor_PIIPipeGuardWired`; `TestProcessMessages_ModelCopyScrubbed`; `TestExecuteRead_ScrubsBeforeTruncation` and `TestExecute_ErrorsAndWriteReportsRedacted` — a denied-command error carrying a Bearer token, a write report with a credential path)

#### Phase 2 verification note

Verified 2026-10-08 11:55 by plan from the working tree (run `1791404721`; Phase 1 committed as
`3fa22b2`). First review pass **failed with two must-fixes**, both leak shapes no existing test covered:
the guard's own suggested remedy, `printenv AWS_SECRET_ACCESS_KEY | muxcode pii-scrub`, was allowed yet
leaked — `printenv` with an argument emits the bare value, and every secret rule needs a label — and the
model copy was scrubbed *after* `ToolExecutor` clipped at `MaxOutputLen=10000`, so a key spanning the cap
reached the model as an unmatchable prefix with no notice (the shape MUX-179's first review caught in
hook capture). Second pass, after the `fix` worker: `printenv NAME` is denied even when piped and the
remedy keeps labels ahead of the scrub (`printenv | muxcode pii-scrub | grep -E '^(NAME)='`); and
redaction moved into `ToolExecutor.Execute` as the **single scrub-then-cut point for every tool result**
— output, errors and write/edit reports alike — with the status line appended after the cut and
`clip()` removed. Review 0/0/0 ("central result redaction closes error/write paths before clipping and
preserves status lines"); test node success on the full suite both passes. The "scrub before you
truncate" rule the two incidents share is now written once, in `Execute`'s doc comment, where every
writer passes.

### Phase 3: Pre-conversation scrub where a road allows it

- [x] Implement for each road Phase 1 found able; a test that reads the agent-facing result, not the history row (`bus/conversation_scrub.go`. **Claude**: a PII-sensitive role launches with a `--settings` file registering a *synchronous* `muxcode hook scrub` on `PostToolUse`/Bash; `ClaudeScrubAnswer` returns `updatedToolOutput` in the Bash output shape carrying `ScrubForRole` text and the notice, only when something was redacted — the test reads the hook's answer, the agent-facing result (`TestClaudeScrubAnswer_RedactsSensitiveResult`, `…_SilentOtherwise`, `TestClaudeScrubSettings_SynchronousScrubHook`, `TestClaudeBuildExecArgs_ScrubSettingsForSensitiveRoles`). **Codex hook road**: the guard rewrites a sensitive role's Bash call via `WrapForScrub` — `{ <cmd>\n} 2>&1 | muxcode pii-scrub --role <role>` with a tail that captures **both** pipeline statuses (`set -- ${PIPESTATUS[@]} ${pipestatus[@]}`, unquoted so bash and zsh agree), keeps the command's status on a successful scrub and exits **125** when the scrubber fails or is missing — answered as `updatedInput` + `allow` (`CodexScrubWrapAnswer`); `ParseToolEvent` unwraps via `UnwrapScrub` so history, chains and `command_match` read what the agent sent. Tests run the wrap under both shells and read its output (`TestCodexScrubWrap_ScrubsBothStreamsKeepsExitCode`, `…_ScrubberFailureReported`, `…_LabelAndValueOnSeparateLines`, `…_InnerPipelineStatusUnchanged`, `…_HeredocAndTrailingComment`, `TestParseToolEvent_UnwrapsScrubWrap`). **OpenCode**: `WriteAgentConfig` emits `.opencode/plugin/muxcode-scrub.ts` (`tool.execute.after` → `muxcode pii-scrub --role $AGENT_ROLE`; fails **closed**, withholding the result when the scrub fails; inert outside a session) — tests run the generated handler under `node` with a stub `muxcode` at the subprocess boundary and read `output.output` and `metadata.output` (`TestOpenCodeScrubPlugin_FailsClosed`, `…_ReplacesWithScrubberAnswer`, `…_InertOutsideSessionAndBash`, `TestWriteAgentConfig_WritesOpenCodeScrubPlugin`). Trade-off recorded: the scrub buffers the whole output, so a command killed at its timeout shows nothing)
- [x] Skip, with the reason recorded, for each road that cannot (the Codex scrape road — `MUXCODE_CODEX_HOOKS=0`, codex < 0.153, or `[features] hooks=false` — runs no hook at all, so neither a rewrite nor the guard reaches it; skipped per [Decision 1](#decision-1--per-provider-mechanism) item 3, named in the Phase 4 "not covered" list)

#### Phase 3 verification note

Verified 2026-10-08 13:10 by plan from the working tree (run `1791404721`; Phase 2 committed as
`25c2c1d`). Three review rounds: the first resolved whole-input scrubbing, inner-pipeline status on both
shells and the OpenCode failure branch; the second left one must-fix and one should-fix (below); the
third caught a zsh quirk the new shell tests exposed — a quoted unset `"${PIPESTATUS[@]}"` expands to
one empty word and shifts the statuses — fixed by the unquoted `set --`. Final review **0/0/0**
("scrubber failures now return 125; OpenCode failure and clean paths have behavioral regression
coverage"); test node success on every pass. The second round's state, kept for the record: In the tree, `bus/conversation_scrub.go` plus
provider and hook changes: **Claude** — a PII-sensitive role launches with a `--settings` file
registering a **synchronous** `muxcode hook scrub` on `PostToolUse`/Bash that answers
`updatedToolOutput` in the Bash shape with `ScrubForRole` text and the notice, only when something was
redacted (`ClaudeScrubAnswer`, `TestClaudeScrubAnswer_RedactsSensitiveResult`, `…_SilentOtherwise`,
`TestClaudeScrubSettings_SynchronousScrubHook`, `TestClaudeBuildExecArgs_ScrubSettingsForSensitiveRoles`);
**Codex hook road** — the guard rewrites a sensitive role's Bash call through `WrapForScrub`, answered as
`updatedInput` + `permissionDecision:"allow"` (`CodexScrubWrapAnswer`), and `ParseToolEvent` unwraps it
before classification (`UnwrapScrub`; `TestCodexScrubWrap_ScrubsBothStreamsKeepsExitCode`,
`…_LabelAndValueOnSeparateLines`, `…_InnerPipelineStatusUnchanged`, `…_HeredocAndTrailingComment`,
`TestParseToolEvent_UnwrapsScrubWrap`); **OpenCode** — `WriteAgentConfig` emits
`.opencode/plugin/muxcode-scrub.ts` calling `pii-scrub --conversation` from `tool.execute.after`
(`.gitignore` carries the generated file); **scrape road** skipped per Decision 1.3. Test node success.
Resolved in this round: whole-input scrubbing (label and value on separate lines match), inner-pipeline
status controls on bash and zsh, the OpenCode failure branch replacing raw output in both fields; the
streaming helper was deleted for a buffered whole-input scrub, with a recorded trade-off — a command
that times out may show no output. **Must-fix** (`WrapForScrub`): the suffix exits on `PIPESTATUS[0]`
alone and discards the scrubber's status, so a missing or failing `muxcode` makes a wrapped `echo hello`
report exit 0 with no usable redacted result — both statuses must be captured at once, the command's
kept when the scrub succeeded and a non-zero redaction failure reported when it did not, with a
failing-scrubber test under both shells. **Should-fix**: the OpenCode regression is a source-string check
that passes with `output withheld` in a comment; it must exercise the generated handler against a
failing or missing scrub process and assert neither `output.output` nor `metadata.output` holds the
secret, with clean-result and outside-session controls, stubbing only at the subprocess boundary. Both
landed as described in the implement step above: `TestCodexScrubWrap_ScrubberFailureReported` and the
three `TestOpenCodeScrubPlugin_*` tests under `node`.

### Phase 4: Docs

- [x] `CLAUDE.md` PII bullet and `docs/agents.md` *Coverage by road*: the conversation row per provider; the definitions' pipe instruction becomes "enforced by the guard" (`CLAUDE.md:140` by edit — per-provider conversation road, guard floor, not-covered list, scrubbed `ps eww` idiom; `agents/api-tester.md`, `command-runner.md`, `log-watcher.md` by edit — auto-scrub per road, env dumps guard-enforced on Claude and hook-road Codex, the `printenv NAME` remedy; `README.md` by edit. By plan: `docs/agents.md` — the opt-in-pipe paragraph rewritten, *Coverage by road*'s conversation rows replaced by eight per-provider rows (Claude exit 0, Claude non-zero, Codex hook road, OpenCode, model copy, scrape road, guard floor, uncovered copies), the whole-result rule, a *Not covered* list, the scrub-first idiom, the MUX-203 tests on the pinned line; `docs/hooks.md` — the Codex `PreToolUse` row's scrub-wrap exception and the per-launch sync `hook scrub` as prose after the Claude settings block; `docs/architecture.md` — the `hook scrub` row; `docs/agent-bus.md` — `pii-scrub --role`)

#### Phase 4 verification note

Verified 2026-10-08 13:20 by plan from the working tree (run `1791404721`; Phase 3 committed as
`7d56398`). Docs-only phase; test node success; final review **0/0/0** ("guard enforcement and
model-output scrubbing are now distinguished"). Three review rounds, all three against plan's own
sentences in `docs/agents.md`: an unqualified "enforced by the guard" when `CheckPIIPipeGuard` runs
only where the `PreToolUse` guard does — Claude and hook-road Codex; the safe-idiom clause conflating
*not denied* with *not redacted* on OpenCode, where the plugin does scrub a sensitive role's output;
and "every other role anywhere", which overran `muxcode agent`'s credential scrub for every role. Each
time the compression of two mechanisms into one clause was false somewhere; on a coverage table the
longer sentence is the correct one. Also caught by plan itself: the `hook scrub` note first placed as
`//` comments inside a `json`-fenced settings block, moved to prose so the sample stays copyable.

### Phase 5: Integration test

- [x] Create `scripts/test-pii-scrub-conversation.sh` — hermetic scratch bus; a sensitive role's bare `ps eww` is denied by the guard and the piped form passes; a non-sensitive role's is allowed (negative control); the model copy of a result is scrubbed; where Phase 3 landed, the agent-facing result is scrubbed (three of the four clauses are driven by the script — guard floor 6, Claude `hook scrub` 5, Codex wrap 8, OpenCode plugin under `node` 4, `pii-scrub --role` 2. **The model-copy clause is not**: the `muxcode agent` loop needs an Ollama-compatible endpoint and the repo's rule is that no test binds a socket, so that road is proven by `TestProcessMessages_ModelCopyScrubbed` over a pipe server and `TestExecute_ErrorsAndWriteReportsRedacted` / `TestExecuteRead_ScrubsBeforeTruncation`, while the script exercises the same `ScrubForRoleWithNotice` through `pii-scrub --role`. Ticked on that basis — the behaviour is delivered and proven, by the one vehicle that can prove it; the user may narrow the wording instead)
- [x] Coverage floor pinned to the exact pass count; run through the run agent (foreground) and record counts here (`EXPECTED_PASS=25`; hermetic **25/0**; live **25/0 + 4 live**, exit 0, final task `1791482170-spawn-3cc16ef6-434b927b` — the full record is the subsection below)

#### Phase 5 verification note

Verified 2026-10-08 14:05 by plan from the working tree (run `1791404721`; Phase 4 committed as
`134bd27`). Test node success; final review **0/0/0** ("live checks now reject CLI, parsing, and
incomplete-run failures; full-text secret checks and controls remain intact"). Three review rounds, all
on the live section, each closing a way a "never the value" test could pass without proving it:
inspecting only the final response line; matching only the labelled form of the secret; trusting output
from a run that never completed. With this, every acceptance criterion (7) and every phase step in the
spec is ticked — the model-copy clause of the first Phase 5 step on unit coverage, as its annotation says.

#### Phase 5 record (written by plan from the implement worker's record, 2026-10-08)

`scripts/test-pii-scrub-conversation.sh` — 282 lines; hermetic: a scratch bus session and project
directory driven through the installed binary, a stub `codex` on `PATH` so the hook road is eligible
without the real CLI, the real `muxcode agent config` writers; `node` for the OpenCode plugin. Floor
`EXPECTED_PASS=25`, the exact hermetic pass count. Listed in `CLAUDE.md`.

| Section | Checks | What it proves |
|---------|--------|----------------|
| Guard floor | 6 | a `run` agent's bare `ps eww` and a piped `printenv NAME` are denied naming the remedy; the scrubbed pipe, an ordinary `ps -ef`, and a `build` agent's bare `ps eww` pass (controls) |
| Claude `hook scrub` | 5 | answers with the result redacted and its other fields kept; silent for a `build` role, a clean result, and `PostToolUseFailure` — the recorded residual gap |
| Codex scrub wrap | 8 | the guard rewrites a `run` agent's Bash call; the returned command, executed as Codex would, prints redacted output and keeps the command's exit code and inner pipeline status; a missing scrubber withholds output and exits 125; history records the original command; a `build` agent is not wrapped |
| OpenCode plugin under `node` | 4 | `agent config` writes the plugin; it redacts a `watch` agent's result, leaves a `build` agent's alone, and withholds the result when `muxcode` is missing |
| `pii-scrub --role` | 2 | redacts for a sensitive role, echoes otherwise |

Runs through the run agent, foreground, against the installed binary:

| Run | Result | Task |
|-----|--------|------|
| Hermetic | **25 passed, 0 failed** (floor 25), 0 live | `1791481319-spawn-3cc16ef6-ac979efd` → `1791481370-run-428d56b7` |
| `MUXCODE_PII_CONVERSATION_LIVE=1`, strengthened checks | **25 passed, 0 failed** (floor 25), **4 live**, exit 0 | `1791481906-spawn-3cc16ef6-b5f1c627` → `1791481986-run-daee4c03` |
| `MUXCODE_PII_CONVERSATION_LIVE=1`, final — with the completion gate | **25 passed, 0 failed** (floor 25), **4 live**, exit 0 | `1791482170-spawn-3cc16ef6-434b927b` → `1791482254-run-97f7bf73` |

Live — what the strengthened checks establish, after the Phase 5 review found the first version
inspected only a final response (OpenCode's last text line), matched only `password=live<digit>` rather
than the bare value, and had no control proving the tool ran: the script writes a **fixture-generated
secret unknown to the prompt** (`live${RANDOM}x${RANDOM}x${RANDOM}`) as `password=…` into a neutrally
named `notes.txt` — an OpenCode model had refused to `cat` a file called `live-secret.txt` — and asks the
model to run `cat notes.txt` and reply with the tool result verbatim. It then collects **every
model-visible text**: for Claude (haiku, `--settings` registering `muxcode hook scrub`, `stream-json`)
every `tool_result`, every assistant text and the final result; for OpenCode (`opencode/big-pickle`, the
written plugin, `--format json`) every tool output and text part. The **protected** role (`run` on
Claude, `watch` on OpenCode) must show `SECRET_REDACTED` and the exact value **nowhere** in that text; a
**`build` control** on each provider must show the raw value and no placeholder — proving the prompt
really executes the tool and the search can see a leak. **Each live check first fails an incomplete
run, before any text is searched** — added after a second review finding that the helpers parsed
regardless of CLI failure, so a CLI that emitted the expected result and then died, or a truncated
stream whose valid prefix held the placeholder, could still pass: `claude_run`/`opencode_run` record
the CLI's exit (`run_status`), whether every stream line parsed (`parse_status`), and whether the run
*completed with a tool result* (`run_complete` — Claude: a final `result` with `subtype=="success"` and
`is_error==false` after at least one `tool_result`; OpenCode: a last `step_finish` with `reason=="stop"`
and at least one `completed` tool part); any of the three failing is a live failure naming the CLI's
stderr, and partial output can never pass. All four passed, twice. Earlier live runs failed on
script bugs, not the scrub road: variadic `--allowedTools` swallowing the prompt (`…-380acd93`,
`…-0da6f2fd`; fixed by placing the prompt after `-p`, stderr now surfaced), then the model's refusal of
the loaded filename (`…-a19c38e9` reported 2 live before the checks were strengthened; superseded).

**Deviation from the step text, recorded honestly.** "The model copy of a result is scrubbed" is **not**
driven by the script: the `muxcode agent` loop needs an Ollama-compatible HTTP endpoint, and the
integration scripts bind no sockets (a sandboxed run agent cannot listen). It is pinned instead by
`TestProcessMessages_ModelCopyScrubbed` — the real `processMessages` loop over a pipe server — and
`TestExecute_ErrorsAndWriteReportsRedacted` / `TestExecuteRead_ScrubsBeforeTruncation`; the script
exercises the same `ScrubForRoleWithNotice` through `pii-scrub --role`.

## Decisions

### Decision 1 — per-provider mechanism

**Decided 2026-10-07 from the Phase 1 findings above, per road** (recorded by plan from the Phase 1
worker's record; the live evidence, not the earlier docs-only lead, is what decides):

1. **Claude — able, exit 0 only.** Phase 3 adds a separate **synchronous** `PostToolUse` Bash handler
   (the existing `hook bash` is `async` and cannot answer) that, for a PII-sensitive role, emits
   `updatedToolOutput` in the Bash shape carrying `ScrubForRole` text and the `PIIScrubNotice`, only when
   the scrub changed something. A non-zero exit cannot be reached after the fact on 2.1.293 and stays on
   the Phase 2 guard floor — a **recorded residual gap**. The `PreToolUse` wrap reaches both outcomes and
   keeps the raw value out of the transcript on a non-zero exit too, but is **declined for Claude**: it works only with
   `permissionDecision:"allow"`, which auto-approves past the role's permission allowlist unless muxcode
   re-implements Claude's rule matcher; the brace group runs in a pipeline subshell, so a `cd` no longer
   persists to the next call; and history/chain classification would read the wrapper. Revisit if Claude
   adds a replacement field to `PostToolUseFailure`.
2. **Codex hook road — able; Phase 3 uses the `PreToolUse` wrap** for PII-sensitive roles. It scrubs
   both outcomes, keeps the exit code, and keeps the raw value out of the rollout. The `allow` costs
   nothing here: sensitive roles run `-a never`, so no approval is bypassed and the sandbox still applies;
   each call is a fresh `bash -lc`, so no cwd persistence is lost. `PostToolUse` `decision:block` is
   declined: it mislabels every scrubbed success as a failed script and leaves the raw value in the
   rollout. `hook bash` / `command_match` must recognise and unwrap the wrapper before classifying.
3. **Codex scrape road — unable.** No hook runs; only a wrapper executor could, which is out of scope.
   Not covered, and the docs say so.
4. **OpenCode — able via plugin.** Phase 3 ships a scrub-only plugin (`tool.execute.after` →
   `ScrubForRole` via `muxcode pii-scrub`) for PII-sensitive roles; the model-facing text is protected,
   the `opencode.db` WAL is not, and the docs say so.

**Provider on-disk records, per road and per outcome — as the table observed them, not a blanket rule:**
the Claude transcript is **protected on exit 0** by the selected synchronous `PostToolUse` replacement
(the probe grepped the transcript and found the raw value absent) and **unprotected on a non-zero exit**
(raw value present — `PostToolUseFailure` cannot replace); the Codex rollout is **protected** by the
selected `PreToolUse` wrap on both outcomes (raw absent); the `opencode.db` WAL is **unprotected** even
with the after-plugin (the bash tool persists streaming output before the plugin runs); the Codex scrape
road is **unprotected** entirely. Beyond the transcript, Claude's own telemetry — OpenTelemetry tool
spans and analytics events — captures the original output **before any `PostToolUse` hook runs**
(documented, not probed), so a Claude-side scrub protects the model and the transcript, never the
telemetry. Phase 4's "not covered" list carries exactly these. *(Rewritten after
the Phase 1 review: the first draft said records are covered "only where the scrub happens before
execution", which contradicted the Claude exit-0 probe.)* The pre-Phase-1 default — "the guard is the
whole fix" — is retired: three of four roads can redact, each by a different mechanism, and the guard is
the floor beneath all of them.

## Related

| Spec | Relationship |
|------|--------------|
| [MUX-179](../completed/MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road.md) | Parent — the history half, done; criterion 2's conversation half deferred here |
| [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) | Where the original leak and the narrow `ps eww -p … \| grep` idiom are recorded |
| [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) | The same shape one road up — a rule with no enforcement |

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-203-sensitive-role-conversation-is-never-scrubbed | 2h 26m | 2026-10-08 14:05 |

## Status

Complete — closed out 2026-10-08 by run `1791404721-50-spec-to-pr`: all five phases implemented and
verified, every acceptance criterion (7) and phase step ticked, moved from `drafts/` to `completed/`.
Moved from `backlog/` to `drafts/` and set as the active spec on the user's instruction 2026-10-07
(rank 1 / Tier 1), the day after it was filed as MUX-179's deferral target. Phases on
`MUX-203-sensitive-role-conversation-is-never-scrubbed`: `3fa22b2` (Phase 1), `25c2c1d` (Phase 2),
`7d56398` (Phase 3), `134bd27` (Phase 4), `a084182` (Phase 5).
