# The Prompt Surface's Inject Loses Its First Character to Its Escape Prefix

The control pane's Prompt surface in inject mode (`Ctrl-T`) delivers typed text to the window's
active agent through `InjectPromptText` (`bus/prompt_inject.go`): `Escape` → literal text → 150 ms →
`Enter`. The Escape is there for the same reason the wake path sends one — to dismiss an overlay that
would otherwise eat the Enter. But on this path the text follows the Escape with **no gap and nothing
in between**, and a TUI key parser that holds a bare `ESC` while it waits to see whether an escape
sequence follows fuses it with the first byte of the text into a Meta chord — `ESC h` is Alt-h — which
the composer does not bind. The first character vanishes; the rest is typed and submitted. The surface
then reports `⇒ injected to edit`, so the control pane shows success while the agent works from a
mangled prompt. Reported by the user 2026-09-09.

## Context

### Observed (2026-09-09, session `muxcode`)

| What | Value |
|------|-------|
| Report | "when I inject from prompt the first character of the text is getting stripped away" |
| Surface | `muxcode graph ui --prompt`, `Ctrl-T` → `⇒ inject: edit agent`, Enter |
| Agent side | `hello world` arrives as `ello world`; a dash-leading line loses its `-` |
| Surface side | notice `⇒ injected to edit` — no signal that anything was lost |
| Stopgap | none clean: `submitPrompt` trims the input (`tui/graph_ui.go:1354`), so a leading space is gone before it could absorb the Escape; only a throwaway leading character survives as a workaround |

### Mechanism — verified

The two paths that type into a Claude composer differ in exactly one place:

| Path | Sequence | Source |
|------|----------|--------|
| Prompt inject | `Escape` → **`-l -- <text>` at once** → 150 ms → `Enter` | `bus/prompt_inject.go:34–41` |
| Wake-up | `Escape` → 100 ms → `C-e C-u C-a C-k` → 100 ms → `-l -- <text>` → 200 ms → `Enter` | `bus/notify.go:878–892` |

Probed at a raw-mode receiver (node v24.16.0 `readline.emitKeypressEvents`, tmux 3.6a, isolated
`-L` server, each key or payload its own `send-keys` call):

| Case | Sequence | Parser saw |
|------|----------|------------|
| A | `Escape`, text (0 ms) | `meta h` then `ello world` — **first character gone** |
| B | `Escape`, 100 ms, text | same — the gap sits inside the parser's escape window |
| C | `Escape`, `- dash probe` (0 ms) | `meta -` then ` dash probe` |
| D | `Escape`, `C-e`, text | `meta ^E` (dropped), then `hello world` **intact** |
| E | `Escape`, `C-e C-u C-a C-k`, text (the wake shape) | `meta ^E`, `^U ^A ^K`, `hello world` intact |
| F | `Escape`, 600 ms, text | `escape`, then `hello world` intact |
| G | `Escape`, 50 ms, `Enter` (the parked re-submit shape) | `meta return` — **Alt-Enter, not Enter** |

Three facts follow:

- The fusion is the **parser's, not tmux's**: every case delivered `ESC` and the payload as separate
  pty chunks (`CHUNK ""`, `CHUNK "hello world"`) and the receiver still merged them. A parser
  that holds a bare `ESC` pending does so across reads until its escape timeout — 500 ms for node's
  readline (`ESCAPE_CODE_TIMEOUT`); Claude Code's window is undocumented and is measured in Phase 1.
- The wake path is safe **because of the absorber, not the sleep**: `C-e` lands between the Escape
  and the text and is what the pending `ESC` fuses with (case E). The 100 ms each side matters for
  the opposite parser model — one that reads chunk-by-chunk sees the Escape alone and then a plain
  `C-e` — so both models are covered only when a non-payload key follows the Escape *and* a gap sits
  on each side of it.
- A gap alone is the wrong instrument: it is a bet on the receiver's constant (case B loses, case F
  wins), and the constant belongs to a program muxcode does not ship.

### The family — every site that sends Escape before another key

| Site | After the Escape | Verdict |
|------|------------------|---------|
| `bus/prompt_inject.go:34` | literal text, no gap | **the defect** |
| `bus/notify.go:933` (`verifyEnterDelivery` re-submit) | 50 ms → `Enter` | same shape as case G — **measured 12:31: submits** on claude 2.1.258; the 50 ms gap clears a ~40 ms window at its edge, so not broken today, routed through the preamble for margin |
| `daemon/daemon.go:2523` (parked-input watchdog) | 50 ms → `Enter` | same as above |
| `bus/notify.go:878` (wake path) | 100 ms → `C-e C-u C-a C-k` → 100 ms → text | safe by absorption (case E) |
| `bus/clear.go:35` (`/clear`) | 100 ms → `C-u` → 100 ms → `/clear` | safe by absorption |
| `bus/provider_claude.go:386` (`/compact`) | 100 ms → `C-u` → 100 ms → `/compact` | safe by absorption |
| `bus/reload.go:134` (`/exit`) | 200 ms → `C-u` → 100 ms → `/exit` | safe by absorption |
| `bus/hook.go:1646`, `daemon/daemon.go:3090` | Escape Escape → nvim `:` command | nvim, not a composer — out of scope |

Nothing under `docs/` states the rule. `CLAUDE.md`'s pitfall list has the sibling ("text + Enter in
one write drops the Enter") but not this one. muxcode's own key reader faces the same ambiguity from
the other side (`handlePromptEscape`, `tui/graph_ui.go`; [`docs/tui-style.md`](../../tui-style.md)
"Escape sequences disambiguated") — the fix here is the mirror image of that rule.

### Scope boundary

Unchanged: the MUX-104 `-l --` literal form, the separate-Enter rule and the text→Enter delay. Not in
scope: whether inject should clear a parked composer before typing (today it appends; the wake path
clears only when the window is unfocused, and the Prompt surface's window is focused by construction,
so that guard would never fire — a decision owed, recorded in Notes). Not in scope: the nvim sites.

## Requirements

### Acceptance criteria

- [ ] An injected prompt arrives whole — first character included — at a receiver that fuses a pending `ESC` with the next byte, for plain, dash-leading and single-character payloads — _dash-leading: evidenced at the receiver through the real surface (`test-prompt-mode.sh`) and the helper (`test-escape-absorber.sh`); plain: evidenced on the real composer (live matrix shape c). **Single-character: not exercised post-fix** — the live matrix has only the pre-fix a3 (`x` lost entirely). One more receiver case in `test-escape-absorber.sh` — preamble → `-l -- x` → `key x` logged — closes this_
- [x] `Escape` is never adjacent to a payload or to `Enter` in any sequence muxcode types into a composer: a non-payload absorber key separates them, with a gap on each side of the Escape as the wake path already has (`Escape` → 100 ms → absorber → 100 ms → payload) — _all four sites call `TmuxDismissOverlay`; review 13:21:01 EXIT=0_
- [ ] One helper produces the Escape-plus-absorber preamble, and `InjectPromptText`, `SendWakeUpWithText`, the `verifyEnterDelivery` re-submit and the daemon's parked-input watchdog all use it — no site hand-rolls `send-keys Escape` ahead of a payload or an Enter — _the four named sites do; `clear.go:35`, `provider_claude.go:386` and `reload.go:134` still hand-roll `Escape` → `C-u` → `/clear`·`/compact`·`/exit` through `exec.Command` (a safe shape, unpinnable through the runner seam). Decision owed: migrate them with their delays kept, or narrow this clause to the four sites_
- [x] The MUX-104 `-l --` form, the separate-Enter rule and the text→Enter delay are unchanged: `TestInjectPromptText_DashLeadingIntact` and `tmux_literal_test.go` pass unmodified
- [x] A unit-level shape check over recorded tmux argv rejects any sequence in which an Escape call is directly followed by a literal or Enter call; the pre-fix `InjectPromptText` sequence is the negative control that must fail it — _`assertEscapeAbsorbed` + `TestEscapeAbsorbViolation_NegativeControl`_
- [x] The integration receiver can see the defect: `scripts/test-prompt-mode.sh` injects into a receiver that models the pending-`ESC` parser instead of `cat`; the old sequence driven by hand against it logs a chord (negative control) and the fixed surface does not — _run agent 14:46:24: both halves confirmed_
- [x] The live matrix against a real Claude Code composer is recorded in this spec's Notes: which shapes lose the first character, and whether `Escape`, 50 ms, `Enter` submits or inserts a newline — _recorded 12:34 from the 12:31 run: a and a3 lose, c and every gap ≥ 50 ms keep; d and e submit_
- [x] Docs name the rule: `CLAUDE.md` pitfalls (sibling of the text + Enter bullet), `docs/architecture.md` Prompt-surface paragraph and delivery section — _review 14:02:16 LGTM 0/0/0: "describes the absorbed preamble in both the wake-up and Prompt-surface sections", cross-checked against the helper and the live matrix_

### Technical approach

**Primary — one preamble helper, mirroring the wake path.** Add `TmuxDismissOverlay(target)` (name
open) to `bus/tmux.go`, through the `tmuxRunner` seam so it is argv-pinnable: `send-keys Escape` →
100 ms → `send-keys C-e` → 100 ms. Two writes, not one `send-keys Escape C-e`: a single write puts
`ESC ^E` in one chunk, and a chunk-at-a-time parser that strips a leading `ESC` and keeps the rest
would type a raw `^E` into the composer; the gap lets that parser see the Escape alone, and the
absorber covers the parser that holds it pending. `C-e` (cursor to end) over `C-u`: a no-op on an
empty composer and it leaves parked text alone, keeping inject's append semantics until the decision
in Notes is taken. Route the four sites in the family table through it: inject keeps `TmuxSendLiteral`
→ 150 ms → `Enter` after the helper; the wake path keeps `TmuxClearInput` after it (its absorber
becomes redundant, the sequence stays uniform); the two re-submits become helper → `Enter`. The three
`C-u` sites are already the right shape — migrate them only if their own delays are preserved.

**Verification at the parser, not the shell.** `scripts/test-prompt-mode.sh` section 4 injects into
a `cat` pane (line 74) — `cat` echoes every byte, so the existing "dash-leading payload injected
intact" check passes with the defect in place. The receiver becomes a small raw-mode reader
(`scripts/lib/escape-chord-receiver.py`, python3 being the hooks' existing fallback runtime) that
applies the pending-`ESC` rule (an `ESC` followed by a byte within 500 ms is logged as one chord) and
writes what it saw to a file; the script asserts the payload's first key arrived plain, and drives
the old sequence by hand as the negative control that must log a chord. Skip-with-reason plus the
coverage floor if python3 is absent.

**Fallback — a gap sized past the receiver's window.** Only if the absorber proves to interfere with a
composer (Phase 1's matrix): a delay above the measured Claude escape window, documented on the
constant with the measurement. Rejected as primary because it encodes another program's constant.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/prompt_inject.go` | `InjectPromptText` — the defect (line 34: Escape straight into the literal write) |
| `tools/muxcode/bus/tmux.go` | `TmuxSendEscape`, `TmuxSendLiteral`, `TmuxClearInput`; home of the new preamble helper |
| `tools/muxcode/bus/notify.go` | `SendWakeUpWithText` (878–892, the safe shape) and `verifyEnterDelivery` re-submit (933–935) |
| `tools/muxcode/daemon/daemon.go` | parked-input watchdog re-submit (2523–2525) |
| `tools/muxcode/bus/prompt_inject_test.go`, `tmux_literal_test.go` | argv-level pins to extend with the Escape-adjacency shape check |
| `tools/muxcode/tui/graph_ui.go` | `submitPrompt` (1353) — caller; `handlePromptEscape` — the same ambiguity from the reader's side |
| `scripts/test-prompt-mode.sh` | section 4 (204–241): the `cat` receiver to replace |
| `scripts/test-send-keys-dash.sh` | the MUX-104 sibling — its receiver has the same blind spot |
| `CLAUDE.md`, `docs/architecture.md` | the rule, next to the text + Enter pitfall and the Prompt-surface paragraph |

## Implementation

### Phase 1: Reproduce and measure on the real composer

- [x] Write `scripts/lib/escape-chord-receiver.py`: raw-mode stdin, pending-`ESC` rule with a 500 ms window, one line per key or chord to the log file given as its argument — _written 11:06 by run `2338488d`'s implement worker (window across reads, `key`/`chord M-`/`seq` lines, pipe-drivable); inspected by the 11:39:57 review (EXIT=0); not yet exercised by any script — Phase 4 wires it in_
- [x] Run the matrix against a scratch Claude Code pane and record it in Notes: (a) today's inject sequence, (b) `Escape` → 100 ms → text, (c) `Escape` → 100 ms → `C-e` → 100 ms → text, (d) text parked then `Escape` → 50 ms → `Enter` — first character kept or lost, submitted or newline, per shape — _run 12:31 on claude 2.1.258 / tmux 3.6a / haiku by `scripts/probe-escape-matrix.sh` (12:30 revision: default report outside the scratch dir, a fine gap sweep added), recorded as `scripts/fixtures/mux163-escape-matrix.txt` and in Notes below; review 12:34:29 EXIT=0 inspected it (its should-fix: `mktemp /tmp/esc-matrix-XXXXXX.txt` is not a portable template on macOS — the Xs must be trailing)_

### Phase 2: One preamble for every Escape-before-payload site

- [x] `TmuxDismissOverlay` in `bus/tmux.go` via the `tmuxRunner` seam: `Escape` → 100 ms → `C-e` → 100 ms as two writes; `TestTmuxDismissOverlay_ArgvShape` pins the two separate calls — _`dismissOverlayGap` 100 ms, sized past the measured 30–50 ms window with the absorber as the protection; suite green 13:20:02_
- [x] `InjectPromptText`: helper in place of the bare `TmuxSendEscape`; literal → 150 ms → Enter unchanged — _lap 7: `TmuxDismissOverlay` returns the first Escape/`C-e` send error and `InjectPromptText` propagates it with the target, so a failed preamble stops the payload (`TestInjectPromptText_PreambleErrorPropagates`); the recovery callers keep their best-effort contract explicitly_
- [x] `SendWakeUpWithText`: helper, then the existing `TmuxClearInput` and delays
- [x] `verifyEnterDelivery` re-submit and the daemon parked-input watchdog: helper, then `Enter` — _lap 7: both delegate to one `TmuxResubmitEnter` (preamble → `Enter`), so the daemon branch no longer owns a send sequence_
- [x] `assertEscapeAbsorbed(calls)` test helper: fails when a `send-keys … Escape` call is directly followed by a `-l --` or `Enter` call; applied to every function above; the pre-fix sequence is the negative control — _`escape_absorb_test.go`: helper shape, negative controls for the old Escape → literal and Escape → Enter sequences, and the check applied to `InjectPromptText`, `SendWakeUpWithText`, `verifyEnterDelivery` and `TmuxResubmitEnter` (the daemon's only path); review 13:31:51 LGTM_
- [x] Existing argv pins pass unmodified (`TestInjectPromptText_DashLeadingIntact`, `TestTmuxSendLiteral_*`) — _neither test file changed; `go test -p 1 -count=1 ./...` exit 0 at 13:20:02_

### Phase 3: Docs

- [x] `CLAUDE.md` editing pitfalls: a sibling bullet — Escape before a payload needs an absorber key with a gap on each side, and why a gap alone is a bet on the receiver's escape window — _edit, `CLAUDE.md:83` ("tmux send-keys Escape before a payload"), run `2338488d` lap 8_
- [x] `docs/architecture.md`: one sentence in the Prompt-surface paragraph (inject delivery shape) and one in the delivery section where the wake path's Escape is explained — _plan 14:05 on the implement worker's handoff (`/tmp/mux163-phase3-architecture.md`): the Prompt-surface paragraph and the "Idle Claude Code agents" wake-up item, both linking the drafts path until close-out_

### Phase 4: Integration test

- [ ] `scripts/test-prompt-mode.sh` section 4: the scratch agent pane runs the chord receiver instead of `cat`; assert the injected payload's first key arrived plain and the full payload is present; skip-with-reason and coverage floor if python3 is absent — _rewired 14:41 (lap 9 worker): `respawn-pane` runs the receiver in `edit.1`, the payload is reconstructed from the key log and compared whole, the receipt is now "surface input cleared"; run agent 14:46:24 (task `1788979563`, `MUXCODE_PROMPT_BACKEND=ollama`): 24 / 0 / 3, exit 0, "chord fusion fix confirmed". **Re-opened on review 14:47:49 should-fix (`:278`)**: with python3 or the receiver missing the parser checks are swapped for the `cat` echo and the unchanged global floor of 18 is met by unrelated checks — a deleted receiver reads green. Lap 10 (14:54) resolved half: a missing receiver now **fails** (`:243`). Review 14:58:52 — still open: python3 missing only increments SKIP twice and the summary labels those as live-model skips; the global `PASS ≥ 18` floor (`:458`) still passes without either parser assertion (24 → 22). Required: a parser-section completion flag, exit 2 when that required section could not run (after honouring real failures), full success only with both parser checks_
- [x] Negative control in the same section: hand-drive `send-keys Escape` then `send-keys -l -- '- dash inject probe'` at the receiver and assert a chord is logged — the receiver must be able to see the defect — _same run: "negative control shows pre-fix defect"_
- [x] `scripts/test-send-keys-dash.sh` (or a sibling `scripts/test-escape-absorber.sh`): the wake-path re-submit shape arrives as a plain `Enter`, not a chord — _sibling `scripts/test-escape-absorber.sh` (14:14): absorbed literal + its pre-fix control, absorbed re-submit `Enter` + its pre-fix Meta-Enter control, floor 5; **run agent 14:45:06 (task `1788979489`): 5 passed, 0 failed, 1 skipped, exit 0** — "re-submit Enter arrives plain not Meta-Enter; negative controls both confirmed"_
- [ ] Opt-in live section (`MUXCODE_ESC_PROBE_LIVE=1`): the Phase 1 matrix automated against a real Claude pane, recording the outcome per shape — _in `test-escape-absorber.sh` (`:131–149`) via `probe-escape-matrix.sh --out`, asserting shape a loses and shape c keeps; worker-reported 7/0/0 live. **Re-opened on review 14:47:49 should-fix (`:134`)**: the wrapper discards the probe's output and maps every non-zero exit to a prerequisite skip, but the probe exits 2 for SKIP and 1 for a real failure. Lap 10 (14:53) added skip/fail branches — review 14:58:52: they cannot run, because the probe is a bare command under `set -euo pipefail` and errexit ends the script before `rc=$?`; `probe.log` is then deleted by cleanup, losing the reason. Required: `rc=0; bash … >log 2>&1 || rc=$?` (or `if`/`else` around the command), and verify exit 2 and an unexpected failure with controlled probe outcomes, not only a successful run_
- [x] Run the scripts and record pass/fail counts in this spec — _run agent rows: `test-escape-absorber.sh` **5 passed / 0 failed / 1 skipped** (14:45:06, task `1788979489`; the skip is the opt-in live matrix), `test-prompt-mode.sh` **24 / 0 / 3** (14:46:24, task `1788979563`; the skips are the model-gated intents). Worker-reported in addition: absorber live `MUXCODE_ESC_PROBE_LIVE=1` 7 / 0 / 0_

## Notes

- Filed 2026-09-09 10:47 by plan on the user's direct report. Mechanism verified with a throwaway
  node readline receiver (kept out of the tree; the Phase 4 receiver replaces it). All seven probe
  cases used separate `send-keys` calls per key and still showed the fusion, so "two pty writes" is
  not the protection it looks like.
- Decision owed: should inject clear a parked composer before typing? Today it appends. The wake path
  clears only when the window is unfocused (`notify.go:473`); the Prompt surface lives in the same
  window as its target, so that guard never fires for inject. Recommendation: keep append semantics;
  a user who parked a draft in the agent composer and then injected chose to send.
- Related: [MUX-109](../completed/MUX-109-prompt-mode-graph-control-pane.md) (Phase 6 added the inject
  path and its Escape prefix, commit `a9e27b1`); [MUX-104](../completed/MUX-104-send-keys-dash-payload.md)
  (the previous injection-argv defect — its `-l --` form is untouched, and its test receiver is a
  shell with the same blind spot as this one's); [MUX-117](../completed/MUX-117-pane-targeting-by-identity.md)
  (pane resolution on this path); [MUX-012](../backlog/MUX-012-remove-gated-pane-scrape-delivery.md) (the wake
  path's future — the helper outlives the scrape road because inject and the re-submits use it too).

- 2026-09-09 11:05: on the user's instruction moved to `drafts/` (a filesystem rename — the file had
  never been committed, so no `git mv`), set as the active spec, and `spec-to-pr` run
  `1788966148-spec-to-pr-2338488d` started with the derived intent "Phase 1: Reproduce and measure on
  the real composer". Two things flagged at launch: the run works on the current branch
  `MUX-159-codex-hooks-provider` (PR #78) because the template creates no branch, and edit was
  mid-fix on [MUX-164](./MUX-164-codex-trust-prompt-reads-idle-wakeup-into-shell.md) in the same
  checkout — `bus/notify.go` is touched by both specs, and the first phase gate is where the mixed
  diff becomes visible.

- 2026-09-09 11:45 verify-spec (run `2338488d` update-spec node, after loop 4): Phase 1 **1/2**.
  Run history: implement 11:05–11:22 (worker `spawn-e011b05c`, 1036 s) wrote the receiver and the
  probe script; every `fix` dispatch in loops 1–3 addressed a MUX-164 failure on the mixed diff
  flagged at launch, and loop 1's fix introduced a MUX-164 regression (a `force` pass-through that
  let automatic recoveries type into an uncaptured pane — review 11:35:33 EXIT=1 must-fix) that loop
  4 reverted; suite green 11:39:07 (`go test -p 1 -count=1 ./...` exit 0), review 11:39:57 EXIT=0
  with two should-fixes (the probe's report retention; MUX-164 docs/live follow-ups). The matrix
  itself was not captured, so AC7 and the step it feeds stay open and `spec_phases_remaining` will
  re-enter Phase 1. The commit gate that follows carries MUX-164's uncommitted changes as well.

- 2026-09-09 12:31 — **live matrix on the real composer** (claude 2.1.258, tmux 3.6a, model haiku;
  `scripts/probe-escape-matrix.sh`, every key its own `send-keys` call; artifact
  `scripts/fixtures/mux163-escape-matrix.txt`):

  | Shape | Sequence | Result |
  |-------|----------|--------|
  | a | `Escape` → `hello world` (0 ms) | **first char LOST** — composer `ello world` |
  | a2 | `Escape` → `- dash probe` (0 ms) | kept — boundary luck, see the sweep |
  | a3 | `Escape` → `x` (0 ms) | **LOST** — the whole prompt vanishes |
  | b | `Escape` → 100 ms → text | kept |
  | c | `Escape` → 100 ms → `C-e` → 100 ms → text (the preamble) | kept |
  | gap | `Escape` → 0.2…1.0 s → text | kept, all seven |
  | window | `Escape` → 0 / 10 / 20 / 30 ms → text, 5 trials each | **0/5** kept at every gap |
  | window | `Escape` → 50 / 75 ms → text, 5 trials each | **5/5** kept at both |
  | d | parked text → `Escape` → 50 ms → `Enter` | **SUBMITTED** — not a newline |
  | e | parked text → `Escape` → 100 ms → `C-e` → 100 ms → `Enter` | SUBMITTED |

  Two things this settles. **Claude Code's escape window is between 30 and 50 ms** — an order of
  magnitude under node readline's 500 ms, so the receiver in Phase 4 is stricter than the composer
  it models, which is the right way round. Separate `send-keys` calls carry ~10–30 ms of fork/exec
  overhead each, which is why a2's dash survived a "0 ms" gap: a gap-only fix is a bet on that
  overhead landing past a ~40 ms line, while the preamble (c) keeps the character regardless of the
  window. And **the re-submit shapes submit**: the Meta-Enter fusion the readline probe showed (case
  G) does not reproduce on this Claude version, so `verifyEnterDelivery` and the parked-input
  watchdog are not broken today — their 50 ms sits at the window's edge, and Phase 2 routes them
  through the preamble for margin and uniformity, not as a defect fix.
- 2026-09-09 12:34 verify-spec (run `2338488d` update-spec node, lap 5): Phase 1 **2/2**, AC7
  ticked. Lap history since 11:45: the user approved `phase-gate` at 12:24:50 and the `commit` node
  was **declined by the phase-progress guard** ("0 phases complete — this commit's phase is still
  open") because Phase 1 stood at 1/2 without the matrix; the user approved `stuck-gate` at 12:25:02,
  the implement worker (`spawn-c1c2a2a7`, reused) ran the probe 12:25–12:32, build/test green
  12:33:32 (`go test -p 1 -count=1 ./...` exit 0), review 12:34:29 EXIT=0 with two should-fixes (the
  probe's macOS `mktemp` template; MUX-164's live-launch coverage). The phase is complete, so the
  next `commit` passes the guard — still on `MUX-159-codex-hooks-provider`, still carrying MUX-164's
  tree.

- 2026-09-09 13:25 verify-spec (run `2338488d` update-spec node, lap 6): Phase 2 **5/6**, AC2/AC4/AC5
  ticked → 11/23. The implement worker (725 s) added `TmuxDismissOverlay` (`tmux.go`, two writes,
  `dismissOverlayGap` 100 ms, absorber `C-e`), routed `InjectPromptText`, `SendWakeUpWithText`,
  `verifyEnterDelivery` and the daemon parked-input retry through it, wrote
  `escape_absorb_test.go` (helper shape, negative control, three site checks) and fixed the probe's
  macOS `mktemp` template; `go vet` 13:17:09 and `go test -p 1 -count=1 ./...` 13:20:02 exit 0; review
  13:21:01 EXIT=0 with two should-fixes: (1) the void helper discards preamble errors, so
  `InjectPromptText` can report success after a failed dismissal — return and propagate, with a
  runner-error negative control; (2) the daemon branch has no adjacency assertion. (2) is Phase 2
  step 5's "every function above", so the phase stays open and the commit guard will decline this
  lap; the stuck-gate re-entry is where both land. AC3 stays open on a wording decision (the three
  `Escape` → `C-u` slash-command sites still hand-roll through `exec.Command`).

- 2026-09-09 13:34 verify-spec pair (daemon review chain 1788975112-1345670b + run `2338488d`
  update-spec, lap 7): Phase 2 **6/6 — complete**, spec 12/23. The commit guard declined the 13:24
  lap as expected ("1 commits shipped but only 1 phases complete"); the user approved `stuck-gate`
  13:25:04 and the worker (302 s) resolved both should-fixes: `TmuxDismissOverlay` returns its first
  send error, `InjectPromptText` propagates it (`TestInjectPromptText_PreambleErrorPropagates`), and
  the re-submit became a shared `TmuxResubmitEnter` used by `verifyEnterDelivery` and the daemon
  (`TestEscapeAbsorbed_ResubmitEnter`). `go build`/`go vet` 13:29:01 and `go test -p 1 -count=1 ./...`
  13:31:11 exit 0; review 13:31:51 **LGTM 0/0/0** EXIT=0. AC3 stays open on the wording decision
  above; AC1/AC6 wait for the Phase 4 receiver, AC8 for Phase 3.

- 2026-09-09 14:08 verify-spec (run `2338488d` update-spec node, lap 8): Phase 2 was committed as
  `8d48888` at 13:54:04 ("MUX-163 Phase 2: One preamble for every Escape-before-payload site"). Lap
  8 (implement 13:54–14:00, worker reused) was Phase 3: edit wrote the `CLAUDE.md` bullet, the worker
  handed plan the `architecture.md` prose, both landed before build/test (green 14:01:28) and review
  14:02:16 (**LGTM 0/0/0** — "Phase 3 documentation matches implementation"). Because Phase 3 was
  already ticked when the node fired, the dispatch derived **Phase 4** as current — and Phase 4 has
  had no work (`scripts/` unchanged), so nothing is ticked there: 0/5. Phase count 3 complete against
  2 shipped, so the commit gate should carry the docs; the next lap implements Phase 4.

- 2026-09-09 14:47 update-docs (worker `spawn-c1c2a2a7`, lap 9 — Phase 4): Phase 3 was committed
  as `7191251` at 14:10:24 ("Docs — name the Escape-preamble rule"). The worker wrote
  `scripts/test-escape-absorber.sh` and rewired `test-prompt-mode.sh` section 4 and reported the
  counts above, but the store held no run-agent task for either script and the lap's test/review
  nodes had not run; plan dispatched both scripts to the run agent for evidence before ticking (the
  second request deduplicated behind the first in-flight `run:run` task and follows it).
- 2026-09-09 14:52: both run-agent rows landed and match the worker's counts — Phase 4 **5/5**,
  AC6 ticked, spec 21/23. Two criteria remain and both would keep `spec_phases_remaining` true after
  the Phase 4 commit: **AC1** needs one single-character receiver case (named above — a small lap),
  and **AC3** is the wording decision on the three `Escape` → `C-u` slash-command sites, which no lap
  can take.
- 2026-09-09 14:58 verify-spec (run `2338488d` update-spec node, lap 9 — dispatched 14:47:51 as
  "(no open phase)" because it fired before the Phase 4 ticks): suite green 14:46:51; review 14:47:49
  EXIT=0 with two should-fixes on Phase 4's scripts, so steps 1 and 4 are **re-opened** (annotated
  above) — Phase 4 3/5, spec 19/23. Next lap: the required-section floor in `test-prompt-mode.sh`, the
  live wrapper's exit-code classification, and AC1's single-character case; AC3 waits on the user.
  The commit guard will hold this lap's Phase 4 work until the phase closes.
- 2026-09-09 15:02 verify-spec pair (review chain 1788980333-125a252a + run node, lap 10): the guard
  declined the Phase 4 commit as expected ("3 commits shipped but only 3 phases complete"), the user
  approved `stuck-gate` 14:50:20, and the worker's fix lap (14:50–14:57) resolved half of each
  should-fix; suite green 14:58:08; review 14:58:52 EXIT=0 with the residue recorded on steps 1 and 4
  above (errexit-safe status capture; a parser-section flag with exit 2). No script re-run through
  the run agent this lap and no single-character case yet. Phase 4 stays 3/5, spec 19/23.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-159-codex-hooks-provider | 5h 51m | 2026-09-09 16:09 |

The run works on the MUX-159 branch (the `spec-to-pr` template creates none); recorded against the
active spec as the pointer directs, the mismatch flagged to edit. The same ledger also backs
MUX-164's row — one branch, two specs.

## Status

**In Progress** — 19/23. Filed 2026-09-09 10:47; `spec-to-pr` run `2338488d` started 11:05; Phase 1
complete 12:34 and **committed `67ad9dc` 13:06** on `MUX-159-codex-hooks-provider` with MUX-164's
implementation (no push); Phase 2 complete 13:31 and committed `8d48888` 13:54; Phase 3 complete 14:02
and committed `7191251` 14:10; Phase 4 3/5 — scripts run green (5/0/1, 24/0/3, run-agent rows) but
steps 1 and 4 stay open after lap 10 half-resolved the 14:47 should-fixes (python3-missing floor;
errexit-safe live probe status). Open: those two, AC1 (single-character receiver case) and AC3
(wording decision) — see Notes.
