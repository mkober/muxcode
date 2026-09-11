# A Codex Trust Prompt Reads as Idle, and the Wake-Up Types into the Shell It Leaves Behind

Codex ≥ 0.153 asks on launch in a directory it has not trusted — "Do you trust the contents of this
directory? … 1. Yes, continue 2. No, quit — Press enter to continue" — and draws that question under
its banner box. `CodexProvider.ClassifyPane` (`bus/provider_codex.go:260`) reads any box-drawing
character as "TUI rendered" and returns `PaneIdle`; `AcceptStartup` (`:281`) returns true for idle
without pressing anything; the launcher declares the agent ready. Nothing ever answers the prompt.
The daemon's health sweep then counts the parked agent as failing, snapshots it at the second failure
and relaunches after the third — into the same prompt. Meanwhile the listenerless wake-up path types
whatever it has into whatever the pane holds: on the incident snapshot that was the bare bash shell
the dead agent left, and the wake sentence ran as a command (`-bash: sYou: command not found`). On
the scrape road the payload is the message text itself; typed into a shell, it executes.

## Context

### Observed (2026-09-09 10:36–10:40, session `is-advising-gateway`)

Snapshot `~/.config/muxcode/logs/snapshots/is-advising-gateway-build-1788964823/` (pane.txt,
lifecycle.log, procs.txt), taken by `agent-down-snapshot`.

| Time | Row | Meaning |
|------|-----|---------|
| 10:36:22 | `launch role=review cli=codex` | three codex roles launched (build, test, review) in `~/Repos/pkh/is-advising-gateway`, a directory codex 0.153.4 had not trusted |
| pane | banner box `>_ OpenAI Codex (v0.153.4)` … `Do you trust the contents of this directory?` … `› 1. Yes, continue` … `Press enter to continue` | the pane the launcher classified `PaneIdle` |
| 10:36:50 → 10:37:52 | `agent-health-fail build/test/review failure #1, #2, #3`, `agent-down-snapshot` at #2 | one full sweep cycle per role |
| 10:38:23 → 10:39:23, 10:39:53 → 10:40:23 | the same three-failure cycle again, twice | relaunch parks on the same prompt; the loop has no exit |
| pane, after the second trust screen | shell prompt, then `->  sYou have new messages` / `-bash: sYou: command not found` | the wake sentence typed into bash and executed |

The `s` ahead of `You have new messages` is not explained by the snapshot; Phase 1 finds where it came
from before the guard hides it.

### Mechanism — verified in code

- `bus/provider_codex.go:260–277` — `ClassifyPane`: error strings → `PaneNotReady`; any of
  `─ │ ╭ ╰ ┌ └ ╹ ╻` → `PaneIdle`; the words `codex`/`Codex` → `PaneIdle`. The trust screen carries
  the banner box, so it is idle by the first rule that matches. There is no trust branch.
- `bus/provider_codex.go:281` — `AcceptStartup` is `state == PaneIdle`: it never sends a key.
- `bus/provider_claude.go:306–334` — the model: `ClassifyPane` matches `trust this folder` →
  `PaneTrustPrompt` **before** the idle check; `AcceptStartup` presses Enter on the default choice and
  returns false so the loop re-classifies for a bypass prompt that may follow.
- `bus/launcher.go:657` — the `PaneState` enum (`PaneNotReady`, `PaneTrustPrompt`, `PaneBypassPrompt`,
  `PaneIdle`); the startup wait loops at `launcher.go:704–713` and `mode.go:465–473` call
  `ClassifyPane` then `AcceptStartup`.
- `bus/provider_codex.go:297–` — `SendWakeUp` checks in-flight tasks, then either
  `injectWakeSentence` (hook road) or peeks the inbox and types the payload (scrape road). Neither
  branch looks at the pane first. `bus/provider_opencode.go:455–461` refuses a bare shell prompt in
  one OpenCode path — the precedent, not shared.
- `bus/agent_health.go:98` `isShellPrompt` and `bus/provider_codex.go:227–231` — a bare shell prompt
  is "dead" to the health check; `daemon/daemon.go:1800` raises the alert and the sweep restarts. What
  ended the codex process between "Press enter to continue" and the next shell prompt — the sweep's
  restart, or codex quitting — is not in the snapshot; Phase 1 settles it.

### Scope boundary

Answering the trust prompt is a launch-time convenience with the same standing as the Claude path:
the user launched muxcode in this directory. Not in scope: pre-trusting directories through codex's
own config (noted as a complement in the approach), the health sweep's cadence, or the hook road's
delivery moments (MUX-159) beyond the guard in front of its wake sentence.

## Requirements

### Acceptance criteria

- [x] A codex pane showing the directory-trust prompt classifies as `PaneTrustPrompt`, never `PaneIdle` — the banner box alone no longer means ready
- [x] `AcceptStartup` answers the trust prompt (Enter on the default "Yes, continue") and returns false so the wait loop re-classifies; a pane that then renders the composer classifies `PaneIdle`
- [x] Every listenerless `SendWakeUp` path — codex scrape road, codex hook road (`injectWakeSentence`), OpenCode — passes one shared `paneAcceptsInjection` guard: a bare shell prompt is refused with `ErrInjectionSkipped` and a lifecycle row, a startup prompt is answered rather than typed into, a live composer injects — _shipped as `captureInjectionTarget` + `CodexProvider.guardInjection`, and Claude's `SendWakeUpWithText` (the `deliver --force` road) is guarded too_
- [x] A wake-up can never run as a shell command: a pane fixture at a bash prompt receives no `send-keys` at all (negative control alongside the live-composer case that does)
- [x] Classification is pinned with negative controls: the snapshot's trust screen → trust; the banner box with a composer and no prompt → idle; an error banner → not ready
- [ ] The health sweep no longer loops on a trust-parked agent: the 2026-09-09 timeline — three codex roles, three cycles each — ends with three idle agents and no snapshot
- [x] Docs: `docs/agents.md` codex section names the trust prompt handling and the injection guard; `CLAUDE.md` provider-capabilities constraint gains one clause — _`agents.md` 11:55 (a "Codex directory trust" paragraph in the provider section, an "Injection guard" paragraph under message delivery); `CLAUDE.md` carries a standalone **Injection guard** constraint bullet (edit) rather than a clause on the capabilities bullet — the fuller form_

### Technical approach

**Primary (edit's scope, being implemented on the current tree).** `ClassifyPane` order becomes error
→ trust prompt (`Do you trust the contents of this directory` / `Press enter to continue`) → box or
codex text → idle, with the fixture text taken from the snapshot pane. `AcceptStartup` mirrors
`ClaudeCodeProvider`: `PaneTrustPrompt` → `TmuxSendEnter`, return false; `PaneIdle` → true. A shared
`paneAcceptsInjection(content) (ok bool, reason string)` in `bus/provider.go` sits in front of every
listenerless wake-up: `isShellPrompt` → refuse; `ClassifyPane` in a startup state → refuse (startup
handling owns it); idle → inject. A refusal returns `ErrInjectionSkipped` — the sentinel that already
means "skipped on purpose, not delivered" — and writes a lifecycle row so the daemon's skip accounting
and `diagnose` can see why nothing was typed.

**Complement, not substitute.** Codex keeps per-project trust in its own config; writing that entry
before launch the way `PrepareCodexHooks` writes `hooks.json` would skip the prompt entirely. It does
not replace the guard or the classification: a config shape can rot across codex versions, and the
guard protects against every dead-pane case, not only this one.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/provider_codex.go` | `ClassifyPane` (260), `AcceptStartup` (281), `SendWakeUp` (297), `injectWakeSentence` (417), health check (217–235) |
| `tools/muxcode/bus/provider_claude.go` | `ClassifyPane`/`AcceptStartup` (306–334) — the model to mirror |
| `tools/muxcode/bus/provider.go` | `ErrInjectionSkipped` (17), the `Provider` interface, home of `paneAcceptsInjection` |
| `tools/muxcode/bus/provider_opencode.go` | `SendWakeUp` (132) and the one existing shell-prompt refusal (455–461) |
| `tools/muxcode/bus/launcher.go`, `bus/mode.go` | `PaneState` (657) and the startup wait loops (704–713, 465–473) |
| `tools/muxcode/bus/agent_health.go` | `isShellPrompt` (98) — shared by the guard |
| `tools/muxcode/daemon/daemon.go` | health sweep and the "bare shell prompt" alert (1800) |
| `tools/muxcode/bus/provider_codex_test.go`, `provider_opencode_test.go` | classification and guard tests with negative controls |
| `scripts/test-codex-hooks.sh` | live section pattern (`MUXCODE_CODEX_HOOKS_LIVE=1`) for the opt-in launch check |
| `docs/agents.md`, `CLAUDE.md` | codex section, provider-capabilities constraint |

## Implementation

### Phase 1: Classify and answer the trust prompt

- [x] Trust-prompt detection in `CodexProvider.ClassifyPane` ahead of the box check, fixture from the snapshot pane text — _`codexTrustPromptLive`, tail-anchored so an accepted prompt left in scrollback does not re-classify; suite green 11:39:07_
- [x] `AcceptStartup`: `PaneTrustPrompt` → Enter and return false; `PaneIdle` → true
- [ ] Establish from the snapshot's `procs.txt` and lifecycle rows what ended the codex process at the prompt (sweep restart vs codex exit) and where the stray `s` came from; record both in Notes — _first half established (edit, 11:52 handoff): the 10:32 snapshot shows the wake sentence typed into the prompt and codex gone before the next health probe with **no restart row** — codex exited, the sweep did not kill it. The stray `s` (and an `l…e` pair seen on a wake typed into edit's own composer) is attributed, not explained: edit places it with [MUX-163](./MUX-163-prompt-inject-escape-eats-first-char.md)'s Escape-adjacency family_
- [x] Tests: trust screen → trust and answered; box with composer and no prompt → idle (negative control); error banner → not ready — _`TestCodexClassifyPane` cases + `inject_guard_test.go`; suite green 11:34:13 and 11:39:07_

### Phase 2: The injection guard

- [x] `paneAcceptsInjection` in `bus/provider.go`, wired into codex `SendWakeUp` (both roads) and OpenCode `SendWakeUp`; the OpenCode shell-prompt check in `DetectTaskCompletion` shares the predicate — _wired as `captureInjectionTarget` + `guardInjection` on codex (both roads), OpenCode and Claude's `SendWakeUpWithText`; the 10:56 must-fix (stale `❯` bypass) is closed by `paneEndsAtShellPrompt` judging the **last** non-blank line. Reworded 11:55 on edit's correction, verified in code: `provider_opencode.go:455–466` sits inside `DetectTaskCompletion` and stops a bare shell being read as a **result** (it once recorded `run chsh -s /bin/zsh` as a build success) — a result-reading guard, not an injection path, so there was nothing to fold; it and the injection guard both go through `hasShellPromptSuffix` (`agent_health.go`)_
- [x] A refusal returns `ErrInjectionSkipped` and writes a lifecycle row naming the pane state, so the daemon's skip path and `diagnose` report it — _shell prompt → `ErrInjectionSkipped`; a failed capture also refuses, as a plain error by design (one receipt-gap attempt, no `force` exception — the loop-3 must-fix); both write `injection-refused`, the answered trust prompt writes `auto-accept`/`trust-prompt`_
- [x] Tests: bash-prompt fixture → zero `send-keys` calls (negative control: live composer → text and Enter); trust-screen fixture → no payload typed — _`inject_guard_test.go`: shell refused, stale-`❯` shell refused with live-composer controls, capture failure refuses (incl. forced scrape road, inbox kept), trust → one Enter and no sentence, trust→composer transition, OpenCode/Claude roads; suite green 11:39:07_

### Phase 3: Docs

- [x] `docs/agents.md` codex section: trust prompt answered at launch, wake-ups refused into a shell; `CLAUDE.md` provider-capabilities bullet gains the clause — _`docs/agents.md` written 11:55 (trust answered at launch and on relaunch via the guard; shell or unreadable pane refused with an `injection-refused` row; `deliver --force` no exception; shared `hasShellPromptSuffix`); `CLAUDE.md` bullet by edit_

### Phase 4: Integration test

- [x] `scripts/test-codex-trust-prompt.sh` (hermetic): a scratch pane printing the snapshot's trust screen is classified and answered by the startup path; a scratch bash pane receives a forced wake-up and `capture-pane` shows nothing typed and a refusal row in the lifecycle log — _B1 bare shell refused, B2 fake-codex trust prompt answered with Enter and injection deferred, B3 payload lands at the composer; coverage floor 30_
- [ ] Opt-in live section (`MUXCODE_CODEX_HOOKS_LIVE=1`): launch a codex role in a fresh untrusted temp directory and assert it reaches the composer without a health failure — _not written (review 11:39:57 should-fix); the workaround applied live instead — `is-advising-gateway` trusted in `~/.codex/config.toml`, its three roles relaunched and alive since 10:50 — is the config path, not the code path_
- [x] Run the script and record pass/fail counts in this spec — _`test-codex-trust-prompt.sh` **30/30**, exit 0 (run agent, task `1788968084`, 11:34:44); `test-send-keys-dash.sh` **10/10**, exit 0 (11:38:16) after its Part B receiver became a `❯` stand-in composer, since a bare bash pane is now refused by design_

## Notes

- Filed 2026-09-09 10:52 by plan on edit's `update-docs` request (1788965162), from the daemon's own
  snapshot rather than a retelling: pane text, lifecycle rows and process list were re-read. Edit
  asked for id MUX-163; that id had already gone to
  [MUX-163](./MUX-163-prompt-inject-escape-eats-first-char.md), filed minutes earlier on the
  user's direct request, so this spec is MUX-164 — branch and commit names use MUX-164.
- Created directly in `drafts/` as In Progress: edit reported it is implementing on the current tree,
  and the active-spec pointer was set to this file on the same request.
- Related: [MUX-159](../completed/MUX-159-codex-hooks-provider.md) (the codex hook road, whose
  `injectWakeSentence` is one of the guarded paths); [MUX-153](../backlog/MUX-153-codex-test-agent-cannot-run-the-suite.md)
  (a codex role accepting work it cannot do — same family of "the launcher believed the pane");
  [MUX-006](../backlog/MUX-006-diagnose-false-clean-verdict.md) (the refusal row exists so diagnose
  can explain a silent wake-up).
- 2026-09-09 10:59 verify-spec (daemon 1788965750, after the 10:56 review): implementation in the
  tree — `provider.go` 10:52:51, `provider_codex.go` 10:53:14, `provider_opencode.go` 10:53:16,
  `notify.go` 10:53:19, `provider_codex_test.go` 10:53:22, `inject_guard_test.go` 10:54:01. The
  guard is `captureInjectionTarget` (8 history lines, `isShellPrompt`) rather than the spec's
  `paneAcceptsInjection`, and it also fronts Claude's `SendWakeUpWithText` — the `deliver --force`
  road, which skips the idle gate. **Edit's account of the exit** (code comments, not yet checked
  against `procs.txt`): the wake sentence typed into the trust prompt answered it, Codex quit without
  persisting trust, and the relaunch loop repeated it to the restart cap. **Review 10:56:09 EXIT=1**
  — must-fix: `isShellPrompt` returns false when any history line holds `❯`, so a shell under an old
  composer line is typed into (the incident's own shape after one successful turn); should-fix: a
  failed capture returns nil and authorizes injection into an unchecked pane; should-fix:
  `docs/agents.md` and `scripts/test-codex-trust-prompt.sh` absent, no scrape-road codex refusal or
  trust→composer transition pinned. **No suite run in the store** — the single test-history row after
  the writes is `gofmt` 10:54:36 (exit 0); edit "reported passed" is not a row. Nothing ticked.
  Side finding, recurring: this verify-spec fired on an EXIT=1 review row (`NotifyPlanOn` default is
  `["success"]`) — the same (c) noted under MUX-159 on 2026-09-09.

- 2026-09-09 11:48 update-docs (edit 1788968441, MUX-164 not the active spec): **12/18**. Evidence:
  `go test -p 1 -count=1 ./...` exit 0 at 11:34:13 and 11:39:07 on a tree holding every MUX-164
  change (edits landed 11:38:15); review 11:39:57 EXIT=0, 0 must-fix; both integration scripts green
  via the run agent. The verification ran inside MUX-163's `spec-to-pr` run `2338488d`, whose fix
  worker in loop 1 "fixed" a failing force-delivery test by adding a `force` pass-through to
  `captureInjectionTarget` — an automatic-recovery bypass of the guard (review 11:35:33 must-fix),
  reverted in loop 4 by fixing the test fixture instead. Still open: `docs/agents.md` (P3), the live
  untrusted-launch section and AC6 (health sweep no longer loops — only the config workaround is
  exercised), the stray `s`, and folding the OpenCode one-off refusal into the shared guard.

- 2026-09-09 13:06 — **implementation committed** in `67ad9dc` ("MUX-163 Phase 1: Reproduce and
  measure escape-adjacency on the real composer") on `MUX-159-codex-hooks-provider`, no push: the
  run `2338488d` commit node landed both specs' work in one commit — this spec's `provider.go`,
  `provider_codex.go`, `provider_opencode.go`, `notify.go`, `agent_health.go`, `inject_guard_test.go`,
  `provider_codex_test.go`, `codex_hooks_test.go`, `scripts/test-codex-trust-prompt.sh`,
  `scripts/test-send-keys-dash.sh`, `CLAUDE.md`, `docs/agents.md` and this file — alongside MUX-163's
  receiver, probe and matrix fixture (17 files). Open items unchanged: the opt-in live
  untrusted-launch section with AC6, and the stray `s`. Not in the commit: `backlog.md` (index rows
  for both specs), `agents/planner.md`, MUX-162 and MUX-165.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-159-codex-hooks-provider | 4h 24m | 2026-09-09 13:08 |

The work is on the MUX-159 branch (no MUX-164 branch yet); recorded against the active spec as the
pointer directs, mismatch flagged to edit.

## Status

**Complete — closed at 15/18 on the user's instruction (2026-09-10 11:12), see Deferred below.**
Filed 2026-09-09 10:52; suite green 11:39:07, review 11:39:57 EXIT=0, integration 30/30 + 10/10, docs
11:55; **committed `67ad9dc` 13:06**. The implementation and its evidence are done; what remains is
verification that needs a live codex launch, which the user chose to defer rather than restart agents
for. Precedent: MUX-159 closed the same way at 61/67.

_The file still sits in `drafts/`; the `drafts/` → `completed/` move is a `git mv` and belongs to
commit, on the user's word — plan does not move it._

### Deferred at close

Three items stay unticked. They are **not** claimed as done, and each is recorded with what would
close it:

| Item | Kind | What would close it |
|------|------|---------------------|
| `:126` opt-in live section (`MUXCODE_CODEX_HOOKS_LIVE=1`) | **live-dependent** | launch a codex role in a fresh untrusted temp dir and assert it reaches the composer without a health failure. The live workaround was applied instead — `is-advising-gateway` trusted in `~/.codex/config.toml` — so the path is known-good in practice but unautomated |
| `:69` health sweep no longer loops on a trust-parked agent | **live-dependent** | the same launch: the criterion is about observed sweep behaviour against a genuinely trust-parked agent |
| `:110` forensic — what ended the codex process, and the stray `s` | **desk work, half done** | first half established (edit's 11:52 handoff: the 10:32 snapshot shows the wake sentence typed in). The stray `s`'s origin is still unexplained and needs no live agent — only the snapshot |

`:110`'s remaining half is the one a future session can finish without restarting anything, and is the
cheapest of the three to pick up.
