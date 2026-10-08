# The PII Scrub Role Gate Has No Call Site on the Bus Road

**Tracking:** [mkober/muxcode#137](https://github.com/mkober/muxcode/issues/137)

**Provenance:** filed 2026-09-11 by plan on the user's request relayed by commit (`1789143390`), from a
credential leak plan caused while diagnosing [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md).
Verified in code before filing.

`CLAUDE.md` states that `api`, `run` and `watch` output is redacted before it enters the conversation.
That holds for the **local LLM harness only**. In the bus module — every real Claude, Codex and
OpenCode agent — `IsPIISensitiveRole` is **dead code**: defined, unit-tested, and called from nowhere.
No role's tool output is scrubbed on that road.

## Context

### Observed — a real key in clear text

While identifying an orphaned listener's role, plan ran `ps eww -p 21146`, which dumps the whole
environment. `MUXCODE_OPENCODE_API_KEY` was printed in clear text into the agent's conversation and
from there into `plan-history.jsonl`. Recorded at MUX-156's third-occurrence note (committed
`e23f9ee`). The key requires rotation.

`plan` is not in the sensitive-role list — but as the mechanism below shows, membership would not
have helped.

### Mechanism — verified in code

| Module | `IsPIISensitiveRole` callers | Effect |
|--------|------------------------------|--------|
| `tools/muxcode-llm-harness/harness/` | `loop.go:133` → `executor.ScrubPII = true` | **works** — api/run/watch/runner output scrubbed |
| `tools/muxcode/bus/` | **none** (definition + `scrub_test.go:188` only) | **no role output is scrubbed at all** |

Bus-side `ScrubPII` / `ScrubPIIWithNotice` call sites, in full:

| Site | Scope |
|------|-------|
| `cmd/scrub.go:21` | the manual `muxcode pii-scrub` pipe filter — opt-in, never automatic |
| `bus/snapshot.go:91, 98, 107` | snapshot log, pane and procs |

Nothing on the PostToolUse/history path calls either. `bus/scrub.go:166` carries the same
`piiSensitiveRoles` map as the harness, so the two modules *look* identical at a glance; only the
harness wires it to anything.

### The test does not catch it

`TestIsPIISensitiveRole_Bus` (`bus/scrub_test.go:188`) passes today. It asserts the **map's contents**
— that `api`/`run`/`watch` are members and others are not — never that scrubbing occurs. It would
pass unchanged if every call site were deleted, which is exactly what has happened. A green suite has
been reporting this feature as present.

### Why it matters beyond one key

Agent tool output lands in `<role>-history.jsonl`, in lifecycle logs, and in the conversation itself.
Diagnosis is routine across every role — `ps`, `env`, `cat` of a config, `muxcode config list` — and
the roles that do it most (`plan`, `edit`, `commit`, `build`) are precisely the ones outside the map.
The documented guarantee reads as cover that is not there.

### Scope boundary

In scope: the missing bus-side call site, the map's membership, and the vacuous test. Not in scope:
the harness path (working), the snapshot path (working), and MUX-156 itself — the leak surfaced while
diagnosing it but is independent of it.

## Requirements

### Acceptance criteria

- [x] Tool output from a sensitive role is scrubbed **on the bus road**, not only in the harness (Phase 1: `ProcessBashHook` → `GetScrubbedOutput`; `TestProcessBashHook_SensitiveRoleRedactsSecret`)
- [x] A secret in a diagnostic command's output never reaches `<role>-history.jsonl` or the conversation unredacted — **conversation half deferred to [MUX-203](../drafts/MUX-203-sensitive-role-conversation-is-never-scrubbed.md)** (user decision 2026-10-07, option 3: close the credential gaps here, file the conversation half as its own defect) (**history half met** in Phase 1 — `TestProcessBashHook_SensitiveRoleRedactsSecret`, `…RedactsSecretAcrossTail`, `TestGetScrubbedOutput_RedactsBeforeClip`; the history row's `Command`/`Description` fields are still unscrubbed. Phase 3 widened the history half: credentials are redacted for **every** role on all four history writers, so the original `plan` leak shape is covered. **The conversation half is not**: `PostToolUse` fires after the provider has already shown the agent the raw output, so this road cannot redact what the agent sees — a different mechanism, or a narrowed promise, is for [Phase 3](#phase-3-decide-coverage) to decide)
- [x] **Negative control:** ordinary output with no secret is passed through **unchanged** — no banner, no truncation, no altered exit code (Phase 1: `TestProcessBashHook_SensitiveRoleCleanOutputUnchanged`, `TestGetScrubbedOutput_CleanMatchesGetOutput`)
- [x] A test fails if the call site is removed — the current test passes with the feature gone (Phase 1: `TestProcessBashHook_SensitiveRoleRedactsSecret` drives `ProcessBashHook` end to end and reads the history row — it fails if the `IsPIISensitiveRole` gate is removed, which the membership-only `TestIsPIISensitiveRole_Bus` never did; Phase 2 replaced that test with `TestProcessBashHook_ScrubsExactlyTheSensitiveRoles`, which also fails on a membership change either way)
- [x] The role list is reconsidered on evidence: the leak came from `plan`, which is not a member (Phase 3: reconsidered and **kept** — the leak was a credential, and credentials are now scrubbed for every role regardless of the list; PII stays gated on the roles that handle external data. [Decision 1](#decision-1--credentials-everywhere-pii-by-role))
- [x] `CLAUDE.md` and [`docs/agents.md`](../../agents.md) state what is actually covered, per road (Phase 4: `CLAUDE.md:140`; `agents.md` *Coverage by road*; the uncovered roads named as plainly as the covered ones)

### Technical approach

Find the bus-side equivalent of the harness's `loop.go:133` — the point where a role's tool output is
captured and written to history — and gate it on `IsPIISensitiveRole` there. The predicate, the
patterns and the banner already exist and are shared; only the wiring is missing.

Then decide the membership question separately, on evidence rather than intuition. Two shapes:

| Option | Trade-off |
|--------|-----------|
| Widen the list to every role | Safe by default; scrubbing cost and false-positive redaction on all output |
| Keep the list, scrub *credential patterns* everywhere | Narrower; needs the secret patterns split from the PII patterns |

The second is the better fit for this evidence: what leaked was a credential, not PII, and credentials
are cheap to match with high precision. `ScrubPIIWithNotice`'s banner requirement stands either way —
a redaction must announce itself so a placeholder is never mistaken for data.

**The load-bearing test is the negative control.** A fix that scrubs aggressively would satisfy every
"no secret in history" assertion while corrupting ordinary output, and agents reason over that output.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/scrub.go` | `piiSensitiveRoles` (166), `IsPIISensitiveRole` (175) — the dead gate |
| `tools/muxcode-llm-harness/harness/loop.go` | `:133`, the working call site to mirror |
| `tools/muxcode/bus/hook.go` | PostToolUse path — where tool output is captured |
| `tools/muxcode/bus/scrub_test.go` | `:188` `TestIsPIISensitiveRole_Bus` — the vacuous test |
| `CLAUDE.md`, `docs/agents.md` | the guarantee as currently written |

## Implementation

### Phase 1: Wire the gate

- [x] Call `IsPIISensitiveRole` on the bus-side tool-output path, mirroring `loop.go:133` (`ProcessBashHook`, `bus/hook.go`: a PII-sensitive role's output is captured through `GetScrubbedOutput`, which runs `ScrubPII` on the **whole ANSI-stripped response before both** the line tail and the `maxChars` clip — each limit can split a secret from what identifies it — then applies the limits through the one shared `clipOutput(text, maxLines, maxChars)`, then prepends the `PIIScrubNotice` banner counting every redaction. `GetOutput`'s output is unchanged. Covers `api`, `run`, `runner`, `watch` — the current `IsPIISensitiveRole` set)
- [x] Unit test: sensitive-role output containing a key is redacted with the banner (`TestProcessBashHook_SensitiveRoleRedactsSecret` — a `ps eww` environment dump carrying an API key, the shape of the original leak; the history row holds the placeholder under the `[muxcode pii-scrub:` banner; `TestGetScrubbedOutput_RedactsBeforeClip` for the straddling key)
- [x] **Negative control:** non-secret output is byte-identical after the call (`TestProcessBashHook_SensitiveRoleCleanOutputUnchanged` — no banner, no truncation, exit code and outcome untouched; `TestGetScrubbedOutput_CleanMatchesGetOutput` — tailed and clipped clean output equals `GetOutput`'s)

#### Phase 1 verification note

Verified 2026-10-07 14:15 by plan from the working tree (run `1791395671`, user-launched). First pass:
review **failed** with one must-fix — `GetScrubbedOutput` scrubbed the *tail*: `outputTail` kept the last
`maxLines` lines first, so a `password=` label on a discarded line with `SuperSecret123` on a kept line
stored the secret unredacted, the scrubber never seeing the label it matches on. Second pass, after the
`fix` worker: the scrub runs on the whole stripped response before both limits, `outputTail` is gone in
favour of one `clipOutput` shared with `GetOutput`, and `TestProcessBashHook_SensitiveRoleRedactsSecretAcrossTail`
pins the exact shape — label on the 16th-from-last line, value on the first kept one, a fixture guard
proving the unscrubbed tail would drop the label and keep the value. Test node **success** on the full
suite both passes; review 0 must-fix. The worker's own follow-ups, carried to Phase 3: the conversation
half of criterion 2 needs another mechanism (`PostToolUse` is too late); the history row's `Command` and
`Description` fields are unscrubbed; `plan`, `edit`, `commit` and `build` stay unscrubbed pending the
coverage decision — the original leak came from `plan`.

### Phase 2: Make the test non-vacuous

- [x] Replace/augment `TestIsPIISensitiveRole_Bus` so it fails when the call site is removed (**replaced** — removed from `bus/scrub_test.go`; `TestProcessBashHook_ScrubsExactlyTheSensitiveRoles` in `bus/hook_test.go` drives `ProcessBashHook` with a `git commit` row carrying an author email, which every role logs to `commit-history.jsonl`, and asserts the stored row: redacted under the banner for `api`, `run`, `runner`, `watch`; byte-identical for `build`, `test`, `edit`, `review`, `commit` — the commit agent keeps its own author email. It fails if the call site goes, a member drops out, or a non-member is scrubbed; the old test caught only edits to the map. An email was chosen over a credential so the negative control survives a Phase 3 "credentials everywhere" decision; `plan` is in neither list, left for Phase 3)

#### Phase 2 verification note

Verified 2026-10-07 14:20 by plan from the working tree (run `1791395671`; Phase 1 committed as
`47a63c5`). Test-only change; test node **success** on the full suite, review 0 must-fix ("the old
map-only test adds no distinct protection"). The criterion *a test fails if the call site is removed*,
ticked in Phase 1 on `TestProcessBashHook_SensitiveRoleRedactsSecret`, now rests on this replacement as
well, which additionally fails on a membership change in either direction.
- [x] Assert the *behaviour* (output redacted) rather than the map's membership (already met by Phase 1's `TestProcessBashHook_SensitiveRoleRedactsSecret` and `…AcrossTail`, which drive `ProcessBashHook` and read the history row; the membership-only `TestIsPIISensitiveRole_Bus` is the step above's to replace or keep)

### Phase 3: Decide coverage

- [x] Split credential patterns from PII patterns, or widen the role list — recorded with the reasoning (**split, role list kept** — [Decision 1](#decision-1--credentials-everywhere-pii-by-role) below, recorded at the boundary in `ScrubForRole`'s doc comment, `bus/scrub.go`. The scrub is now a rule table — `scrubRules`, each rule flagged `credential` — with `ScrubPII` unchanged in output and count, new `ScrubSecrets` (JWTs, AWS keys, labelled secrets; PII-shaped text left alone) and `ScrubForRole` choosing by role — a `ScrubForRoleWithNotice` twin was added and then removed as callerless in the fix round; each writer prepends the `PIIScrubNotice` itself. Applied on all four history writers: hook capture (`GetScrubbedOutput` now takes the role — **every** role scrubbed), `NewBusResponseEntry` (now takes the role; summary built from scrubbed text without the banner), `muxcode log` (output and summary), `muxcode agent`'s `logBashToHistory` (before the length cap))
- [x] Pin the chosen shape with a test for a `plan`-role diagnostic leaking a key (`TestNewBusResponseEntry_PlanDiagnosticKeyRedacted` — the bus-response road is how a `plan` `ps eww` reaches `plan-history.jsonl`, since the hook keeps no `CmdUnknown` row for `plan`: the key is redacted, the email kept, the summary is the reply line. Also `TestProcessBashHook_ScrubsCredentialsForEveryRole` (10 roles incl. `plan`), `TestScrubSecrets_CredentialsOnly` (negative control: PII kept), `TestRunLogScrubsCredentials`, `TestLogBashToHistory_ScrubsCredentials`; the Phase 2 email control still holds)

#### Phase 3 verification note

Verified 2026-10-07 14:40 by plan from the working tree (run `1791395671`; Phase 2 committed as
`07993c5`). First pass: the decision and the pin stood, but review returned one should-fix — `cmd/log.go`
dropped the summary's redaction count, so a secret appearing only in the summary was masked with **no
notice**. Second pass, after the `fix` worker: `runLog` keeps both counts from `ScrubForRole` on output
and summary and opens the output with **one** `PIIScrubNotice` counting both whenever either was
redacted — a summary-only redaction leaves an output holding the notice alone, the summary staying a
single readable line; `TestRunLogScrubsCredentials` is now a table (both fields → one notice with two
values; summary only → notice with one; clean → byte-identical, no notice). Test node **success** on the
full suite both passes; review 0 must-fix, one nit (this spec named the removed helper — fixed above).
The worker's follow-ups, carried forward for Phase 4's docs to state honestly and for later specs:
conversations are unscrubbed, the `muxcode agent` model copy included; the history row's `Command` and
`Description` fields are unscrubbed; bus message payloads in `inbox/` and `log.jsonl` are unscrubbed;
the harness still scrubs by the role list alone (noted in `scrub.go`'s header).

### Phase 4: Docs

- [x] `CLAUDE.md` PII bullet and [`docs/agents.md`](../../agents.md): what is covered, on which road (`CLAUDE.md:140` rewritten per road by edit; `docs/agents.md` § *PII scrubbing* by plan — the "equivalent filtering" sentence corrected to an opt-in pipe, and a **Coverage by road** subsection: a five-row table (harness conversation; history credentials for every role; history PII for the four roles; the agent's own conversation by instruction only; the uncovered copies), the reasoning for the split, the scrub order per writer, how `plan`'s key reached its history, the seven pinning tests. The `agents.md` feature-table row and `README.md`'s PII bullet made per-road honest; the worker fixed `README.md:181`, whose `watch` row claimed output is scrubbed before the model — false for an OpenCode `watch`)
- [x] Note the narrow-env-read idiom recorded on MUX-156 as the safe way to read another process's role (`CLAUDE.md:140` and `agents.md` *Coverage by road*: `ps eww -p <pid> | tr ' ' '\n' | grep -E '^(AGENT_ROLE|BUS_SESSION)='` — never a bare `ps eww` or `env`, whose output reaches the conversation unscrubbed whatever the role; the 2026-09-11 leak cited, date checked against MUX-156)

#### Phase 4 verification note

Verified 2026-10-07 14:50 by plan from the working tree (run `1791395671`; Phase 3 committed as
`78c9b9d`). Docs-only phase; test node **success**. First review pass raised **two should-fixes
against plan's own text**: the credential set was overstated — "labelled `key=`" — when
`piiGenericSecretRe` matches `api_key`, `api_secret`, `auth_token`, `token`, `secret`, `password`,
`passwd` and `authorization` labels only; and "whole ANSI-stripped response before tail and clip" was
claimed for every history writer when only hook capture strips ANSI and tails lines — synthesized rows
and `muxcode log` store whole, `logBashToHistory` scrubs before its 8000-char cap. Both fixed in
`agents.md` and `CLAUDE.md` with matching wording, and Decision 1 here aligned; second review pass
0/0/0. **Follow-up surfaced here, then fixed**: a bare `key=` and an `Authorization: Bearer <token>` header
— the commonest header in API output, on a PII-sensitive role — survived every rule. On 2026-10-07 the
user had edit close them in `bus/scrub.go` and the harness mirror: `piiAuthHeaderRe` (`Bearer`/`Basic`/
`Token`/`Digest`), quoted JSON labels in `piiGenericSecretRe`, `piiBareKeyRe` gated by `secretShaped`,
and `StripANSI` inside `ScrubForRole` so every writer — not only hook capture — matches through colour
codes (`TestScrubPII_HeaderQuotedLabelAndBareKey`, `TestScrubSecrets_HeaderQuotedLabelAndBareKey`,
`TestScrubForRole_ColoredLabelRedacted`). The docs were updated to match.

### Phase 5: Integration test

- [x] `scripts/test-pii-scrub-roles.sh`: a scratch agent emits a fake key; assert it is absent from `<role>-history.jsonl` and the pane (172 lines, hermetic scratch bus through the installed binary; the scratch agent is its tool results and replies fed to the **real** writers — `muxcode hook bash`, `muxcode log`, and an edit `send --wait` that plan answers with `--reply-to`, the synthesized-reply road a `plan` diagnostic takes into `plan-history.jsonl`. Sections: a `run` `ps eww` key (row, notice, `muxcode console run --once` shows the placeholder); a label on the line the 15-line tail drops; a `plan` `git` output token (row, notice, commit pane); a `plan` self-report (output and summary, notice); a `plan` reply to `--wait` (request reached plan, row has no key, notice, summary is the reply line))
- [x] **Negative control:** a run with no secret leaves output unchanged (three: clean `run` output byte-identical with no notice and shown in the pane; a commit author email byte-identical and rendered in the commit pane — so every pane check can fail; a clean self-report unchanged)
- [x] Coverage floor pinned to the exact pass count (`EXPECTED_PASS=17`)
- [x] Run through the run agent (**foreground**, per MUX-171) and record counts here (run agent, 2026-10-07 14:52, task `1791399107-spawn-bcff0029-fd095d59`: **17 passed, 0 failed, floor 17, exit 0**)

#### Phase 5 verification note

Verified 2026-10-07 15:00 by plan from the working tree (run `1791395671`; Phase 4 committed as
`ac9958f`). Test node **success**; review 0/0/0 ("exercises real history writers and panes with clean
controls and a 17-check floor"). The script's header states the one thing it does not cover — the
agent's own conversation, which `PostToolUse` cannot reach — and points at *Coverage by road*. The script
is listed in `CLAUDE.md`'s integration-test table with every other `scripts/test-*.sh` (added after this
note was first written; Copilot 4211330106 caught the stale sentence on the PR).

**One box stays open after this phase: criterion 2.** Its history half is met and tested at every
writer; its conversation half — "never reaches … the conversation unredacted" — is **not met**, and
cannot be on this road. Phase 4 chose to correct the promise in `CLAUDE.md` rather than keep it. The
criterion as written therefore cannot be ticked honestly, and `close-spec` will refuse on it. Two ways
to close, both the user's call: narrow the criterion to the history row (what the spec actually
delivered), or file the conversation half as its own defect — alongside the `Authorization: Bearer`
gap from Phase 4 — and tick this box as deferred to it.

## Notes

**Found by causing it.** Plan leaked the key itself while diagnosing MUX-156, then checked whether the
scrubber should have caught it and found the gate unwired. Recorded that way because the narrow
`ps eww … | grep -E '^(AGENT_ROLE|BUS_SESSION)='` idiom now on MUX-156 is a workaround for a missing
control, not a fix.

**Related:** [MUX-156](../backlog/MUX-156-orphaned-inbox-listener-consumes-into-the-void.md) (where the leak is
recorded); [MUX-157](../backlog/MUX-157-role-boundary-an-agent-can-ignore.md) (a rule with no enforcement — the
same shape, one road up).

## Decisions

### Decision 1 — credentials everywhere, PII by role

**Decided 2026-10-07 by the Phase 3 worker, within the latitude the step gave it; recorded here and in
`ScrubForRole`'s doc comment (`bus/scrub.go`).** The step offered two shapes — split the credential
patterns from the PII patterns, or widen the role list — and the evidence chose the first. The leak
that motivated this spec was a **credential**, from `plan`, a role outside the list; credentials (JWTs,
AWS keys, and a value after `=`/`:` whose label contains `api_key`, `api_secret`, `auth_token`, `token`,
`secret`, `password`, `passwd` or `authorization` — `piiGenericSecretRe`, quoted JSON labels included;
an `Authorization:` header with a `Bearer`/`Basic`/`Token` scheme, or `Digest` redacted to the end of the
line — `piiAuthHeaderRe`; and a
bare `key=` whose value is secret-shaped — `piiBareKeyRe` + `secretShaped`. The last two were gaps the
Phase 4 review found and the user had edit close on 2026-10-07, harness mirror included, with ANSI now
stripped inside `ScrubForRole` for every writer) match with high precision, so redacting them for every
role costs almost nothing. The PII patterns are different: an email matches a commit's author line, an
SSN-shaped number matches an id in a test log — ordinary output agents reason over — so they stay gated
on the roles that handle external data (`api`, `run`, `runner`, `watch`). `ScrubForRole` is that rule:
`ScrubPII` for a sensitive role, `ScrubSecrets` for every other. Widening the list instead would have
put `plan`'s commit messages and review quotes under PII redaction to stop a leak that PII patterns
never matched in the first place. `plan` therefore stays out of the sensitive set — its leak shape is
covered by the credential half.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road | 53m | 2026-10-07 15:00 |

## Status

Complete — 2026-10-07. All five phases and every acceptance criterion verified; closed out the same day
by the `50-spec-to-pr` run `1791395671` and moved to `completed/`. Filed 2026-09-11; set active and moved
to `drafts/` on the user's instruction 2026-10-07 (rank 1 / Tier 1); phases on
`MUX-179-pii-scrub-role-gate-has-no-call-site-on-the-bus-road`: `47a63c5`, `07993c5`, `78c9b9d`, `ac9958f`,
`ec93523`, plus the user-directed credential-gap fixes after Phase 5. Criterion 2's conversation half is
deferred to [MUX-203](../drafts/MUX-203-sensitive-role-conversation-is-never-scrubbed.md). Active branch
time 53m.
