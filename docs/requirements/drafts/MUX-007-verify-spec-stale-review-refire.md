# Verify-Spec Stale Review Refire

One review completion can generate an unbounded stream of identical `verify-spec` requests to the plan agent. The daemon fires the reviewed-transition on **any** growth of edit's inbox while **any** unconsumed review→edit message exists — and plan's mandated "reply to edit" is itself growth, so plan's own compliance sustains the loop.

## Context

### Observed failure (2026-08-13, 12:07–12:09)

- Review completed once (its response to edit sat unconsumed while edit was busy in a long turn).
- Plan received 4 identical `verify-spec` requests in ~2 minutes (12:07:40, 12:08:09, 12:08:40, 12:09:36), each naming only the spec doc itself as the changed file.
- Each fire landed on the daemon poll ~30s after plan sent a reply to edit — 1:1 correlation with plan's replies (verification summary, then per-message no-op replies, then a loop alert).
- `muxcode diagnose review` during the storm: review **idle, alive, inbox empty** — no reviews were running. The generator was the daemon re-firing on stale state.
- The loop only terminated when plan stopped replying to edit.

### Root cause

`daemon/daemon.go` `checkInboxes()` (~:360-368):

```go
if size > prev && size > 0 {
    if role == "edit" && bus.HasNewMessageFrom(d.session, "edit", "review") {
        bus.TransitionWorkflow(d.session, bus.StateReviewed, "daemon:review-complete", ...)
        d.notifyPlanOnReview()
    }
}
```

- The growth check (`size > prev`) is sender-agnostic: **any** message to edit trips it.
- `HasNewMessageFrom()` (`bus/workflow.go` ~:350) just peeks for **any** unconsumed message from review — it has no notion of "new since last check". It matches on `m.From` **alone**: it never checks `m.To` or `m.Type`, so an auto-CC'd `review→test` *response* — a message neither addressed to edit nor reporting review completion to anyone — fully satisfies the "review completed" test.
- So the condition is really "edit got mail while a review message is still unconsumed", which stays true for as long as edit is busy — and every re-fire both re-transitions the workflow state and sends plan another `verify-spec`.
- Plan's `verify-spec` instructions end with "Reply to edit with a summary" — that reply is the next inbox growth. One review completion + one busy edit + one compliant plan = self-sustaining loop.

### Impact

1. **Duplicate work requests**: plan burns turns re-verifying an unchanged spec; its no-op replies add noise to edit's already-backlogged inbox.
2. **Workflow state churn**: `TransitionWorkflow(StateReviewed)` re-fires per echo, so the state log records review completions that never happened.
3. **Self-amplifying under load**: the busier edit is, the longer the review message sits unconsumed, the more echoes fire — worst exactly when the session is busiest.
4. **Time-recording double exposure**: on a non-ignored branch each echo would also re-run the time-recording pass (harmless in value terms — absolute totals are idempotent — but each pass costs a ledger read/write cycle).
5. **Plan silence does not stop it (observed 2026-08-24, 16:27–16:32)**: one MUX-103 review completion produced **7** `verify-spec` echoes. Plan replied to edit exactly once (the first, legitimate one) and stayed silent through the rest — echoes kept firing anyway, fed by chain auto-CC traffic alone. This falsifies the "one compliant plan" framing in the root cause above: plan's reply is *one* fuel source, not a necessary one, so instructing plan to reply less is not a mitigation. The two unconsumed review messages in edit's inbox were both `review→test` responses (`reply_to` a test task), never a review→edit report. Echo #6 and #7 named `docs/requirements/backlog/backlog.md` as the changed file — plan's own prior verification edit, fed back as the trigger.
6. **Amplified by any edit-inbox storm (observed 2026-08-17, ~11:02)**: a build↔test chain loop pumped auto-CC copies into edit's inbox every ~7–8s (22 unconsumed messages, daemon `loop-detected` queued among them); with one stale review message in the pile, **every** CC re-fired `verify-spec` at plan at the same cadence — 7+ echoes in under a minute, sustained without plan sending anything. The refire bug turns any unrelated message storm into a verify-spec storm.
7. **Changed-files can name paths outside the repo (observed 2026-08-25, 10:08)**: an echo
   arrived whose `Changed files:` was `/tmp/tmux-layout-bindings.md` — a scratch delegation
   handoff file the plan agent had just written for an unrelated tmux-config task, with no
   connection to the active spec or even to the repository. So the refire does not merely
   replay a stale *repo* diff; whatever file-write signal feeds the changed-files list is
   picking up writes anywhere on disk, then asking plan to verify a Go orchestrator spec
   against a markdown scratch file. Two consequences: the "is this an echo?" heuristic
   cannot rely on the changed-files list being repo-scoped, and any agent writing to `/tmp`
   (the documented pattern for long delegation payloads, see `CLAUDE.md` delegation
   hygiene) becomes an echo trigger. Suggests the fix should also constrain changed-files
   to repo-relative paths, independent of the refire cause.

8. **Changed-files named the user's credentials file (observed 2026-08-27, 09:54)** — this
   raises item 7 from untidy to a disclosure concern. An echo arrived whose `Changed files:`
   was `/Users/<user>/.config/muxcode/config`: the muxcode config, which by design holds
   `JIRA_API_TOKEN` and now `MUXCODE_OPENCODE_API_KEY`. The verify-spec instruction reads
   *"Read the spec and the changed files"*, so the message is a standing invitation for a
   receiving agent to open a secrets file and pull its contents into an LLM context — and,
   via the normal reply path, potentially into a bus message or a doc.

   Nothing leaked here: the agent that received it recognised the path and declined to read
   it. But that is a judgement call standing in for a control, and it will not hold for every
   agent on every pass. Two requirements follow, and the second is the one that matters:

   - **Scope changed-files to the repository** — the fix already proposed in item 7, which
     would have prevented this instance.
   - **Never instruct an agent to read a path outside the repo, credentials or not.** Path
     scoping fixes the observed case; it does not fix the general one, since a repo-relative
     path can also be sensitive. The instruction itself should not name arbitrary files as
     required reading.

   **Reproducible, not a one-off**: it fired again at 10:15 the moment plan appended an
   addendum to that same `/tmp` file. Two writes to one out-of-repo scratch file, two
   echoes — so the trigger is the write itself, and any agent following the documented
   `/tmp` handoff pattern will keep generating them.

   And at 10:23 it fired against the MUX-014 spec naming `config/tmux.conf` — an *in-repo*
   file from a completely unrelated task (a tmux keybinding fix). So the defect is not only
   that paths escape the repo: **the changed-files list is never correlated with the active
   spec at all.** Any write anywhere, by any agent, on any task, re-fires `verify-spec` at
   whatever spec happens to be active. Constraining paths to the repo (above) is therefore
   necessary but not sufficient — the fix also needs a relevance test, or `verify-spec`
   will keep asking plan to verify a Go orchestrator against a tmux config.

   Same session, 10:00–10:09: **~10 echoes from 3 genuine review completions.** One echo
   was *not* a pure echo — `graph_run_test.go` appeared on disk between the notification
   and the check, so a delta check against the working tree (not the message) is what
   distinguishes a stale replay from a real change. Verifying on the message's file list
   alone would have recorded "no run-store tests exist" while 13 of them existed.

9. **The payload shape varies within one burst, so it is not a genuineness signal (observed
   2026-08-27, 17:49–17:53)** — four `verify-spec` messages in ~4 minutes against the MUX-109
   spec, in three different shapes:

   | Time | `Changed files:` | Tree actually changed? |
   |------|------------------|------------------------|
   | 17:49 | `tui/prompt.go` | **Yes** — `prompt.go` written 17:49:22 |
   | 17:52:27 | `tui/graph_ui.go` | **Yes** — `graph_ui.go` written 17:51:57 |
   | 17:52:47 | `tui/graph_ui.go` | No — same file, same mtime, 20 s later |
   | 17:53:39 | *(field absent entirely)* | No |

   Two points, and the second is the one that constrains the fix:

   - **The list under-reports even when genuine.** Both real fires named a single file while
     **15 files** were modified in the working tree. An agent verifying from the message would
     have missed the `graph_ui.go` wiring on the first pass and the `prompt.go` work on the
     second — reinforcing item 8's closing note that the working-tree delta, not the message,
     is the source of truth.
   - **A receiving agent cannot filter echoes cheaply.** Half of this burst was genuine, so
     "ignore repeat verify-spec messages" would have dropped real work; and the shapes are not
     separable — the duplicate carried the *same* well-formed file list as the genuine fire,
     while the fourth carried none. Any correct plan-side heuristic reduces to re-deriving the
     tree delta on every fire, which is the cost the fix is supposed to remove. **This belongs
     in the daemon**, per the once-per-completion gate below; there is no cheap receiver-side
     mitigation to fall back on in the meantime.

10. **The loop closes on the verifying agent's own doc edit (observed 2026-08-28, 11:02–11:04)** —
    a genuine `verify-spec` fired at 11:02:57 against MUX-114 naming `bus/graph_test.go`
    (written 11:01:43 — real). Plan did the verification pass, editing the spec at ~11:03.
    **90 seconds later a second `verify-spec` arrived naming that spec file itself** as the
    changed file. The only thing that had changed on disk was plan's own verification output.

    This is item 5's mechanism observed end to end in a single cycle, and it makes the shape
    concrete: **the act of verifying a spec produces the write that requests verifying it again.**
    Left unbroken by an agent that re-records or re-replies, it is a closed loop rather than a
    decaying echo — every pass generates its own next trigger.

    Two things distinguish this instance from item 9's burst, and both matter to the fix:

    - **It *was* cheaply separable, unlike item 9.** The changed-files list named exactly one
      path, that path was the active spec itself, and no source file had changed since the prior
      pass. A daemon-side rule — *never fire `verify-spec` when the only changed file is the
      active spec* — would have suppressed it with no risk of dropping real work. That is a
      narrower and cheaper gate than the once-per-completion fix, and it is complementary to it,
      not a substitute: it closes the self-feeding loop specifically.
    - **Correct handling costs a full delta check anyway.** Plan still had to stat every `.go`
      file to prove nothing had landed, because item 8's closing note stands — an echo-looking
      message is not proof the world is unchanged. The receiver-side cost item 9 identified is
      unchanged; what is new is that *this* sub-case has a trivial daemon-side test.

    Handling applied (and worth pinning as the documented behaviour until the fix lands):
    consume silently, **do not reply** (a reply grows edit's inbox and re-arms the trigger),
    **do not re-record branch time**, and **do not re-edit the spec** — since re-recording writes
    the spec file and thereby manufactures the next echo. Note the mild conflict this creates
    with the standing instruction to record time whenever the message asks: on an echo the right
    move is to skip, and let the unrecorded seconds settle on the next genuine pass.

    **Controlled follow-up, same session (11:08–11:12): silence is not sufficient.** After the
    echo above, plan made *no write and no reply of any kind* for 3.5 minutes — and a further
    `verify-spec` fired at 11:12 anyway, again naming the spec file whose mtime was still
    11:08:27. This is item 5's finding reproduced under a cleaner control (there, plan had at
    least replied once; here it did nothing at all), and it settles the relationship between the
    two mechanisms: **plan's own writes are a fuel source, not the fuel source.** Correct
    receiver-side handling stops the loop from being *self*-amplifying; it does not stop the
    echoes, because other traffic keeps re-arming the trigger. Tally for the burst: six fires in
    ten minutes, two genuine, four echoes. Nothing a receiving agent can do is a mitigation —
    only the daemon-side gate below is.

### New evidence (2026-08-31): a 14-fire census, and the case that rules out the cheap fix

Folded in from [`MUX-127`](../backlog/MUX-127-review-completion-routing.md) Defect B, which was found
independently and then recognised as this defect. MUX-127 records it as **new evidence for MUX-007,
not a new defect**, and names these two specs as probable merge targets.

**The census: 14 `verify-spec` fires in ~50 minutes, 2 genuine.** The daemon's own loop detector
flagged it twice (`loop-detected plan type=message` at 13:20:51 and 13:51:37).

| Fire | Named changed file | Genuine? |
|------|--------------------|----------|
| 1 | `bus/pane_test.go` — inside a spawn worktree under `$TMPDIR`, **absent from the repo** | Partly — real work existed, but not at the named path |
| 2–4 | `bus/spawn.go`, `bus/spawn_test.go` — MUX-120 work, unrelated to the active spec | No |
| 5–10 | plan's own edits to MUX-117 / MUX-123 / MUX-126 | No |
| 11 | MUX-117 doc edit — but the graph run had transitioned to `failed` underneath | **Yes** |
| 12–14 | plan's own MUX-117 edit, run already terminal, code tree byte-identical | No |

**This census confirms the root cause above rather than competing with it.** Fires 13–14 arrived
*after* plan stopped writing docs — the cycle does not need continuous input to sustain itself, which
is exactly what "fires on any growth of edit's inbox while any unconsumed review→edit message exists"
predicts. The 2026-08-13 mechanism explains the 2026-08-31 observation.

Two things the census adds that this spec did not previously have:

**B1 — the changed-files list is provenance-blind.** Fire 1 named an absolute path inside a spawn's
detached-HEAD worktree (`$TMPDIR/muxcode-spawn-muxcode/spawn-<hex>/…`). Read literally, it asserted a
file the branch did not contain. **A verifier that trusted it would have checked off a phase against
code absent from the repo** — the failure mode is a false `- [x]`, not merely a wasted fire.

**B2 — plan's own writes are indistinguishable from implementation progress.** Nothing marks a changed
file as *"the verifier wrote this in response to the last fire."* The signal that work progressed and
the signal that plan responded are the same signal.

#### This constrains an existing acceptance criterion

The criterion below — *"No `verify-spec` fires when the only changed file is the active spec itself"* —
**would have suppressed fire 11**, the one fire in fourteen that mattered. Fire 11 named a doc edit as
its only change, yet was genuine: the graph run had transitioned to `failed` underneath it, and that
fire is how MUX-127's Defect A was discovered at all.

So filename shape is the wrong discriminator. Suppression must key on **state movement**. The
discriminator fitting all 14 fires: every echo had a **byte-identical code tree**; the genuine one did
not. Note that fire 11's movement was a *graph run state* transition rather than a file edit, so a
working-tree fingerprint alone is insufficient — run state must be part of the comparison.

#### Secondary cost: suppressed time recording

Each fire also asks plan to record branch active time — itself a doc write, and therefore more fuel.
Plan began declining that write on echo fires, so the ledger drifted from the recorded value (17m vs
12m in the doc). Correct behaviour and loop-avoiding behaviour are in direct conflict, which indicates
the gate is misplaced rather than that the agent is choosing badly.

### Related: a dangling pointer after an automated spec move

Observed live 2026-08-28 17:12. When commit performs a spec's `drafts/` → `completed/` move (as in
`4c3adeb`), the **active-spec pointer is not cleared** — it still names the old `drafts/` path. The
next `close-spec` guard then reports *"cannot read active spec"* and fails its run.

**The guard behaved correctly**: failing loudly on an unreadable spec is exactly what
[MUX-114](../completed/MUX-114-close-spec-node-has-no-completion-check.md) built it to do, and it is
far better than proceeding on a spec it cannot read. The defect is upstream — the pointer outlives
the file it points at.

This belongs here rather than with the guard because a dangling pointer is a *pointer-driven daemon
behaviour* problem: the same stale path also makes `verify-spec` fire at a nonexistent file on every
review, which is this spec's subject. It was previously hit manually (MUX-103, 2026-08-25) and the
cleanup was a documented manual step; now that the move is automated, **the manual step has no
owner**. Cleared by hand again on 2026-08-28.

- [ ] Clearing (or repointing) the active-spec pointer is part of whatever performs the move, not a
      step a human is expected to remember
- [ ] A pointer naming a path that no longer exists is detected and reported, rather than surfacing
      only as a downstream guard failure

## Requirements

### Proposed fix

Make the reviewed-transition fire once per actual review completion. Options, roughly in order of robustness:

1. **Track the last-seen review message ID** — daemon remembers the ID of the review→edit message that last triggered the transition; `HasNewMessageFrom` gains a variant returning the newest matching message ID so the daemon only fires when it changes.
2. **Inspect the growth delta** — only fire when the newly appended bytes (messages after `prev`) contain a message from review, so unrelated senders growing edit's inbox never trip the check.
3. **Dedup at the send** — keep the trigger as-is but make `notifyPlanOnReview()` idempotent per workflow transition (e.g. skip if the workflow state is already `StateReviewed` with the same outcome and no intervening state change).

Option 1 or 2 also fixes the workflow-state churn; option 3 alone does not.

### Acceptance criteria

- [ ] One review completion produces exactly one `verify-spec` request to plan, regardless of how many other messages edit receives before draining its inbox
- [ ] Plan replying to edit while a review message sits unconsumed does not re-fire the transition or `notifyPlanOnReview()`
- [ ] `TransitionWorkflow(StateReviewed)` fires once per actual review completion
- [ ] A genuine second review completion (new review→edit message) still fires a new `verify-spec`
- [ ] Existing daemon and workflow tests still pass
- [ ] **No `verify-spec` fires when nothing moved** — the self-feeding loop in item 10, where the verification pass's own doc edit requests the next verification. Cheap and separable, and worth landing even if the once-per-completion gate slips. **Amended 2026-08-31:** this was originally written as *"when the only changed file is the active spec itself."* The fire-11 case refutes that shape — see [New evidence](#this-constrains-an-existing-acceptance-criterion). Suppression must key on **state movement**, not filename shape
- [ ] **Negative control:** a genuine review completion that changes source files *and* touches the active spec still fires — a fix that suppresses on "spec was touched" rather than "nothing moved" cannot pass
- [ ] **Negative control (fire-11 shape):** a fire whose only changed file is the active spec but where the **graph run state changed** still fires. A working-tree fingerprint alone fails this; run state must be part of the comparison
- [ ] **Changed-files paths are provenance-checked (B1)** — a path outside the repo working tree (e.g. inside a spawn worktree under `$TMPDIR`) is never presented to the verifier as a repo file. A verifier must not be able to check off a phase against code absent from the branch

### Key files

| File | Change |
|------|--------|
| `daemon/daemon.go` | `checkInboxes()` reviewed-transition gate (~:360-368) |
| `bus/workflow.go` | `HasNewMessageFrom()` or an ID-aware variant |
| `daemon/daemon_test.go` / `bus/workflow_test.go` | Unit tests for once-per-completion semantics |

## Implementation

### Phase 1: Once-per-completion gate

- [x] Implement the chosen gate (last-seen review message ID or growth-delta inspection) — **option 1
      built**: `NewestMessageIDFrom` (with an `m.To == role` addressee filter that kills the auto-CC
      false trigger) + on-disk `reviewed-transition.last` written via `atomicWriteFile`, wired at
      `daemon.go:381`, zero orphaned `HasNewMessageFrom` callers
- [x] Unit tests: unrelated inbox growth does not re-fire; a new review message does; state transitions
      once per completion — all three covered and passing (`TestCheckInboxes_VerifySpecOncePerReviewCompletion`
      asserts all three in sequence, including the *positive* control that a genuine second completion
      fires; plus `TestCheckInboxes_ReviewCCDoesNotFire`, `TestCheckInboxes_ReviewedMarkerSurvivesDaemonRestart`,
      `TestNewestMessageIDFrom`, `TestReviewedMarkerRoundtrip`). Verified green 2026-08-31: `tools/muxcode`
      **2027 PASS / 0 FAIL / 1 SKIP**, exit 0, all 4 packages
- [x] **Must-fix resolved — the gate was defeated on its own error path.** Found by review and
      confirmed independently: `WriteReviewedMarker` failure logged to stderr and **fell through**,
      firing `TransitionWorkflow` + `notifyPlanOnReview` anyway, leaving the marker stale so every
      subsequent poll re-fired — this spec's own storm, resurrected. Now **fail-closed**: the
      transition sits in an `else`, so it runs only on a successful write, and the write is atomic
      (tmp+rename). A withheld completion is retried on the next inbox growth once writable
- [x] **Negative control for the above** — `TestCheckInboxes_MarkerWriteFailureWithholdsTransition`
      injects a real failure (a directory at the marker path defeats the rename) and asserts **both**
      halves: zero transitions and zero `verify-spec` while broken, then exactly one fire and
      `StateReviewed` after recovery — triggered by *unrelated* growth, proving the withheld completion
      is retried rather than lost. The recovery half is load-bearing: a withhold-only test passes just
      as well on a gate that is permanently stuck, trading a storm for silent deafness.
      **Its absence is why a 2652-assertion green suite still shipped the defect** — every prior test
      exercised the happy path, so the error path had no coverage at all

### Phase 2: Integration test

- [ ] Create `scripts/test-verify-spec-refire.sh` (or extend an existing daemon integration script)
- [ ] Test: seed edit's inbox with one review response, send two unrelated messages to edit → assert exactly one `verify-spec` lands in plan's inbox
- [ ] Test: append a second review response → assert a second `verify-spec` fires
- [ ] Run the script and verify all checks pass

## Deferred — minor, not phase-scoped

Tracked for close-out but deliberately outside the phase checkboxes: these sit under an `##` heading,
so `SpecPhases` assigns them to no phase and they cannot block a per-phase commit, while `SpecOpenItems`
still counts them so the spec cannot close with them open.

- [ ] `reviewed-transition.last` is not cleared by `purgeStaleFiles` (`bus/setup.go:150`). Low severity —
      message IDs are unique, so a surviving marker cannot suppress a legitimate transition — but it is
      stale session data outliving its session. Raised during Phase 1 verification; orthogonal to the
      once-per-completion gate, so blocking Phase 1 on it would be over-strict

## Provenance

Found by the plan agent on 2026-08-13 while receiving the echo storm first-hand during branch-time-tracking verification; confirmed via `muxcode diagnose review` (idle/empty during the storm) and reading `checkInboxes()` + `HasNewMessageFrom()`. The bug is in committed code — today's working-tree change to `notifyPlanOnReview()` only touched the message body.

## Time Tracking

| Branch | Active time | Last updated |
|--------|-------------|--------------|
| MUX-007-verify-spec-stale-review-refire | 29m | 2026-08-31 21:41 |

## Status

In Progress — moved to `drafts/` 2026-08-31 and folded in
[`MUX-127`](../backlog/MUX-127-review-completion-routing.md) Defect B as new evidence; the fire-11 case
amended one acceptance criterion and added three.

**Phase 1 complete, 4/4** (2026-08-31). The once-per-completion gate is built, fail-closed on its
marker-write error path, and covered by a negative control with both withhold and recovery halves.
Verified against the primary artifact — `./test.sh` **exit 0**, **2652 assertions passing, 0 failing**,
with verbatim `--- PASS:` lines for all six MUX-007 tests — not from an agent's summary. The gate's
own error path had shipped a defect past a fully green suite; that is now closed and pinned.

Phase 2 (integration test) remains open. One minor item is [deferred](#deferred--minor-not-phase-scoped)
outside the phase checkboxes.
