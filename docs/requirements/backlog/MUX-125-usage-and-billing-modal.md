# Usage and Billing Modal

A modal window showing token usage, plan headroom and spend for Claude and OpenCode — **the same
numbers the provider websites show** — without leaving tmux.

The requirement is explicit: pull usage from **the providers' APIs**, so the modal agrees with the
account pages rather than approximating them. That rules out a locally-derived estimate as the
primary source, and makes *"which endpoint returns the website's numbers, and can this machine reach
it?"* the first question to answer.

**It is not yet known that such an endpoint exists for a Claude subscription.** No endpoint is named
in this spec, because none has been verified — inventing one is how this feature ships confidently
wrong numbers.

Tracking: _(no GitHub issue yet)_

## Context

### What is known about Claude's surface — and what is not

**Known.** There is no `ANTHROPIC_API_KEY` in the tree, no `~/.claude/.credentials.json`, and no
usage-endpoint reference anywhere under `~/.claude`. Claude Code authenticates by OAuth held in the
macOS Keychain, so there is no locally-readable key to call an API with, and no discoverable
endpoint to point at.

**Known to exist, but for a different account object.** Anthropic's Admin API
(`/v1/organizations/usage_report/messages`, `/v1/organizations/cost_report`) reports **organization
API usage** and needs an admin-scoped key. Those are the Console's numbers. They are *not* a Claude
subscription's usage, and pointing the modal at them would produce an authoritative-looking report
about the wrong thing.

**Unknown, and Phase 1's job.** Whether any endpoint returns *subscription* usage — the figures the
Claude account page shows — and whether the Keychain OAuth token is accepted by it. Both must be
established before any renderer is written. If the answer is no, that is a finding, not a failure:
it converts this into a choice between an admin-key path measuring something else and a local
estimate that will not match the website, and **the user should make that choice knowingly.**

**The fallback, if it comes to that.** Claude Code writes per-message `usage` into
`~/.claude/projects/<project>/*.jsonl`. Measured on one live transcript:

| Field | Total |
|-------|-------|
| `input_tokens` | 2,466 |
| `cache_creation_input_tokens` | 2,394,486 |
| `cache_read_input_tokens` | **518,522,752** |
| `output_tokens` | 887,451 |

This is real data and works offline — but it measures *this machine's sessions*, not the account. It
must never be presented as website-parity.

### The trap that will produce a wrong number

**A modal that sums `input_tokens` reports 2,466 instead of ~521 million — five orders of magnitude
low.** Cache reads dominate real input by roughly 200× in a long session, and they are a *separate
field*, not included in `input_tokens`.

Cost makes it worse, because the three input classes are not priced alike: cache **reads** bill at a
fraction of base input, cache **creation** at a premium, and base input in between. Summing them
into one "input" number is wrong for volume *and* for money, in opposite directions.

Any implementation must break out all four fields. This is the single most likely way to ship a
plausible, confidently wrong modal.

### OpenCode

`MUXCODE_OPENCODE_API_KEY` exists and resolves through the standard chain (env → `.muxcode/config` →
`~/.config/muxcode/config`) at `bus/prompt_agent.go:162-168`, so **a usable credential is already in
place** — which makes OpenCode the more tractable half. Whether its gateway exposes a usage or
billing endpoint, and whether that endpoint's numbers match the OpenCode dashboard, is unverified.
Phase 1 answers it.

### Subscription plans and overage — the part that is *not* observable

The modal must relate usage to **the subscription plan in force**, and show **fees incurred once a
limit is exceeded**. That is the most useful view for a subscription user: dollars-per-token is
meaningless on a flat plan, while *"how much headroom is left, and what happens when it runs out"*
is exactly the question.

**None of it is available locally.** Searched the live transcript for any key matching
`limit|plan|tier|quota|rate|subscri|overage|cost|bill`: **no matches**. The only related field is
`usage.service_tier`, which reads `"standard"` on all 1,708 rows — that is the API service tier, not
a subscription plan, and it does not vary with plan or with remaining headroom.

So the plan, its limits, its reset window, and its overage behaviour must come from **configuration
or a verified endpoint**, never inference. Three things must be established before any of it renders,
because each has opposite failure modes:

| Unknown | Why guessing is worse than omitting |
|---------|-------------------------------------|
| Which plan is in force | Limits differ per tier; the wrong tier makes every headroom figure wrong |
| What the limit actually meters | A cap on messages in a rolling window and a cap on tokens produce different bars from the same data |
| What happens at the limit | **Blocking and billing are opposite behaviours.** Showing an accrued "fee" on a plan that simply pauses until the window resets invents a charge that does not exist; showing "blocked" on a plan that bills overage hides a real one |

A modal that displays a confident dollar figure derived from an assumed plan is the worst outcome
here — it is the money version of the `input_tokens` trap below, and a user would have no reason to
doubt it.

**Recommended v1**: report *measured* usage (which is solid) against a **user-configured** plan
limit, with the overage rule stated as configured rather than inferred. Headroom and reset-window
countdown carry most of the value and cannot be wrong about facts the tool does not know.

### What already exists to build on

| Piece | Where |
|-------|-------|
| Modal framework — `ModalConfig{Name, Title, Width, Height, Command, Sizes, Role}` | `bus/modal.go:125` |
| Popup registry + launcher — `DefaultPopupConfigs`, `OpenPopup`, `PopupNames` | `bus/popup.go` |
| Existing modals to match in style | `session-picker`, `switch-session`, `remote-sessions`, `save-memory`, `edit-config` |
| Interactive TUI precedent | `tui/provider_select.go`, `tui/remote.go` |
| `prefix + b` menu to hang a binding from | `config/tmux.conf:53` |

A new modal is a registry entry plus a renderer, not new plumbing.

## Open decisions

- [ ] **Scope of "usage"**: this session only, this project across sessions, or all projects? The
      transcript layout supports all three; the per-project directory makes project-scoped natural.
- [ ] **Does the OpenCode gateway expose usage/billing at all?** If not, is a locally-accumulated
      estimate acceptable, or is OpenCode simply out of scope?
- [ ] **Are prices hardcoded, configured, or omitted?** Hardcoded rates go stale silently and would
      make the modal *confidently* wrong; token counts alone never go stale. A defensible v1 shows
      tokens and leaves money to a configured rate table.
- [ ] **Live-updating or on-open snapshot?** Existing modals are one-shot; a poller costs complexity
      and the numbers move slowly.
- [ ] **Where do plan limits and overage rules come from?** User config is the only source available
      today; a verified endpoint would be better if one exists. Until then the modal reports what it
      was told, and says so.
- [ ] **What does the limit meter — messages in a rolling window, or tokens?** The bar is drawn
      differently for each, from the same underlying data.
- [ ] **Does the plan bill overage or block at the limit?** These are opposite; the modal must show
      whichever is true and never invent a fee.
- [ ] **Per-agent breakdown?** Ten roles run concurrently; "which agent burned the tokens" may be
      the more useful question than the total.

## Requirements

### Acceptance criteria

- [ ] A modal opens from the `prefix + b` menu and shows current usage without leaving tmux
- [ ] Figures come from **the providers' APIs**, and the modal's numbers **match the provider
      website** for the same period — parity checked by hand at least once and recorded
- [ ] Each figure names its **source endpoint**; a figure with no verified endpoint is labelled as an
      estimate or omitted, never shown unmarked beside API-sourced ones
- [ ] All four token classes are reported separately: base input, cache creation, cache read, output
- [ ] The modal never presents a single conflated "input" figure — the 2,466-vs-521M error is
      impossible by construction
- [ ] If a cost figure is shown, its rate source is named and its staleness is visible; if rates are
      unknown the modal says so rather than showing a plausible wrong number
- [ ] OpenCode is either reported from a **verified** endpoint, or its absence is stated explicitly
      in the modal — never silently omitted
- [ ] If no subscription-usage endpoint exists for Claude, the modal says so plainly; a local
      estimate, if shown at all, is **visibly labelled** as this machine's sessions rather than the
      account's total
- [ ] Usage is shown **against the plan in force**, with remaining headroom and the reset-window
      countdown — the useful view on a flat subscription
- [ ] Plan tier, limit, and overage rule are **configured or fetched from a verified source**, never
      inferred; the modal names where each came from
- [ ] Overage is reported per the *actual* plan behaviour: an accrued fee **only** where the plan
      bills one, and "limit reached — paused until <reset>" where it blocks. **No fabricated charge**
- [ ] With no plan configured, the modal shows measured usage and states that no limit is configured
      — it does not assume a tier
- [ ] A session with no transcript renders an explicit empty state, keeping header and footer
- [ ] The modal clamps to the pane and degrades on a narrow terminal rather than overflowing
- [ ] Colours come from `tui/styles.go`; state is legible without colour
- [ ] `go vet ./...` and `go test ./...` green

### Technical approach

Read-only over `~/.claude/projects/<project>/*.jsonl`, aggregating `message.usage`. Malformed lines
are skipped rather than fatal — transcripts are appended live and the last line may be partial (a
mid-write read has already caused one wrong conclusion in this repo).

Render through the existing modal registry. Follow the pure-renderer rule from
[`docs/tui-style.md`](../../tui-style.md): snapshot in, string out, reachable via `--render-once` so
frames are testable without tmux.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/modal.go` | `ModalConfig` — the registry entry |
| `tools/muxcode/bus/popup.go` | `DefaultPopupConfigs`, `OpenPopup` |
| `tools/muxcode/bus/prompt_agent.go` | `MUXCODE_OPENCODE_API_KEY` resolution chain |
| `tools/muxcode/tui/styles.go` | Palette |
| `config/tmux.conf` | `prefix + b` menu entry |
| `docs/tui-style.md` | Renderer rules this must satisfy |

## Implementation

### Phase 1: Find and verify the endpoints (no UI work)

- [ ] Determine whether **any** Anthropic endpoint returns *subscription* usage (the account page's
      figures), and whether the Keychain OAuth token authenticates against it
- [ ] Determine whether the **OpenCode gateway** exposes usage/billing, and whether its numbers match
      the OpenCode dashboard
- [ ] For each endpoint found: record auth method, scope, rate limits, and the exact fields that
      correspond to the website's displayed numbers
- [ ] **If no subscription endpoint exists**, present the options — admin-key path (different
      account object) vs. labelled local estimate — and get an explicit decision before Phase 2
- [ ] Confirm the transcript schema, only if the fallback is chosen
- [ ] Decide the scope and pricing questions above
- [ ] Verify no `ANTHROPIC_API_KEY` path exists that would change the Claude conclusion
- [ ] Establish, from an authoritative source, what the subscription plan meters, what its limits and
      reset window are, and **whether exceeding it bills or blocks**
- [ ] Decide the config shape for plan/limit/overage (and whether any endpoint can supply it)

### Phase 2: Aggregation

- [ ] Read and aggregate transcripts, all four token classes kept distinct
- [ ] Skip malformed/partial trailing lines without failing
- [ ] Unit tests over a fixture transcript with known totals
- [ ] **Negative control**: a fixture whose `input_tokens` and `cache_read_input_tokens` differ by
      orders of magnitude — asserting the reported figure is not the small one

### Phase 3: Modal

- [ ] Register the modal; add the `prefix + b` entry
- [ ] Pure renderer with `--render-once`
- [ ] Explicit empty state; narrow-pane degradation

### Phase 4: Integration test

- [ ] Create `scripts/test-usage-modal.sh` (hermetic: fixture transcripts, scratch config)
- [ ] Test: known-total fixture renders those totals
- [ ] Test: all four token classes appear separately
- [ ] Test: no-transcript case renders the empty state with header and footer intact
- [ ] Test: narrow pane degrades rather than overflowing
- [ ] Test: usage renders against a configured plan with correct headroom and reset countdown
- [ ] **Negative control**: with **no** plan configured, no limit bar and no fee are shown — the
      modal cannot invent a tier
- [ ] **Negative control**: on a block-at-limit plan the modal shows "paused until <reset>" and
      **never** an accrued fee — the fabricated-charge case, asserted directly
- [ ] **Negative control**: no network call is made (run with networking unavailable and assert
      identical output)
- [ ] **Negative control**: a fixture that would trip the conflated-input bug fails if the code
      regresses to summing one field
- [ ] Coverage floor itemized against the actual check count
- [ ] Run the script and verify all checks pass

## Risks

| Risk | Why it matters | Mitigation |
|------|----------------|------------|
| Summing `input_tokens` only | Reports 2,466 instead of ~521M — plausible, wrong, and unnoticed | Four classes kept separate; negative-control fixture |
| Calling the Admin API for Claude | Returns org API usage, not this subscription session — an authoritative-looking wrong number | Local transcripts only; criterion forbids the call |
| Hardcoded prices | Rates change; a stale table is confidently wrong about money | Name the rate source and its date, or show tokens only |
| Assuming an OpenCode usage endpoint | The key exists; the endpoint is unverified | Phase 1 answers it before any UI |
| Presenting local estimates as website figures | They measure different things — this machine's sessions vs the account — and the gap is invisible to the reader | Source named per figure; estimates visibly labelled |
| Assuming an endpoint exists | No Claude subscription-usage endpoint is verified; naming one would be invention | Phase 1 is discovery, and may conclude "none" |
| Using the Admin API as a stand-in | It reports organization API usage — authoritative-looking, wrong object | Explicitly separated in Context; decision required |
| Inventing an overage fee | A plan that blocks has no fee; a confident dollar figure would be believed | Overage rule configured, never inferred; negative control asserts it |
| Assuming a plan tier | Every headroom figure inherits the error | No plan configured ⇒ no limit bar |
| Reading a transcript mid-write | The live session appends continuously; a partial last line is normal | Skip malformed lines; never treat a short read as a total |

## Status

Draft
