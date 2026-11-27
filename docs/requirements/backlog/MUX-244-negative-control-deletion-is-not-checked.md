# MUX-244: Deleting or Inverting a Negative Control Leaves the Suite Green and Nothing Flags It

**Tracking:** [mkober/muxcode#257](https://github.com/mkober/muxcode/issues/257)

**Split from:** [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md) — its negative-control
criterion, its forged-hold criterion and the controls half of its Phase 4, moved here on the user's
decision of 2026-11-24 relayed by edit.

A control test exists to prove the other tests can fail. Delete it, or flip its assertion, and every
remaining test still passes — on code that works and on code that does nothing. On 2026-09-08 a test
agent deleted the one positive control behind four refusal tests; the suite stayed green. Catching
it took a person reading the diff.

## Context

### Observed (2026-09-08)

From [MUX-157](../completed/MUX-157-role-boundary-an-agent-can-ignore.md), verified there against `c4997ed`.

`TestExecUnverifiedHoldReleasedByUser` proved that `forgeApproval` writes a marker the code actually
reads. Its siblings (`…RefusesAgentApproval`, `…RefusesStrippedIdentityApproval`,
`…RefusesWhenAncestryUnreadable`) assert *pending* — which a node that was never approved at all
also satisfies. The test agent deleted the positive sibling; edit restored it by hand; at `c4997ed`
it had been **inverted** to assert *pending* too, so all four agreed with a node never approved.

This was the second time a removed control turned a green suite into a meaningless one. The first
produced rule 3 of [`docs/tui-style.md`](../../tui-style.md) ("Test the negative case, or the test
proves nothing"), which is written for the TUI and read by whoever reviews a TUI change.

### State on 2026-11-24

| Fact | How established |
|------|-----------------|
| The forged-hold tests have their positive control back under its own name: `TestExecUnverifiedHoldReleasedByAuditedUserApproval` (`bus/graph_exec_test.go:1822`), which differs from the refusal above it by exactly the audit row and asserts *running*; its comment says why it exists | Read by plan; added in `77f32b8` (MUX-144 Phase 2) |
| The rule exists as prose in three places: `docs/tui-style.md` rule 3, `skills/tui-style.md` (the TUI checklist) and `skills/test-audit.md:54`, `:108`, where a guard test with no negative control is a **should-fix** | Read by plan |
| `agents/code-reviewer.md` does not name removing a control as a finding category | `grep -i 'negative control' agents/code-reviewer.md` finds nothing |
| Nothing mechanical looks at a diff for a removed test or a flipped assertion | No such script under `scripts/`; no suite test names the project's controls |

### Undecided — the mechanical form is not chosen

MUX-157 named two forms and chose neither:

| Option | Idea | Cost |
|--------|------|------|
| A. A registry test | A suite test lists the project's control tests by name and fails when one is missing | Catches deletion and renaming; cannot see an inverted assertion; the list is maintained by hand and a control nobody registered is unprotected |
| B. A diff check | A script the review chain runs over the diff: a removed `func Test…`, or a changed assertion in a test marked as a control, is reported as a must-fix | Sees inversion as well as deletion; needs a marking convention (a comment tag on control tests) and reads diffs, so it runs in review, not in the suite |
| Both | The registry for deletion, the diff check for inversion | Two things to maintain |

Open with it: whether a flagged removal **blocks** (review `EXIT=1`) or is reported for a person to
judge — a control is sometimes removed for a good reason, with its replacement in the same diff.

Until the form is chosen, Phase 2 cannot be worked unattended. Phase 1 can.

## Requirements

### Acceptance criteria

- [x] The forged-hold quartet has its positive control back under its own name — shipped as
      `TestExecUnverifiedHoldReleasedByAuditedUserApproval` (`bus/graph_exec_test.go:1822`,
      `77f32b8`); read 2026-11-24, not re-run for this spec
- [ ] Removing or weakening a negative control is a **must-fix** review category, stated in
      `agents/code-reviewer.md` and in `skills/test-audit.md` (today: should-fix, and only for a
      missing control, not a removed one)
- [ ] The rule is project-wide: stated once outside the TUI guide, with `docs/tui-style.md` rule 3
      linking to it
- [ ] The mechanical form is chosen by the user and recorded here
- [ ] A diff that removes a named control, or inverts its assertion where the chosen form can see
      it, is flagged without a reviewer having to notice
- [ ] Negative control: a diff that adds tests, or edits a test that is not a control, is not
      flagged

### Key files

| File | Purpose |
|------|---------|
| `agents/code-reviewer.md` | The finding categories and the `EXIT=` rule (edit's file) |
| `skills/test-audit.md` | `:54`, `:108` — the existing should-fix row |
| `docs/tui-style.md`, `skills/tui-style.md` | Rule 3, the TUI statement of the same rule |
| `tools/muxcode/bus/graph_exec_test.go` | `:1782`–`:1833` — the forged-hold tests, the worked example |
| `scripts/` | The diff check, if option B or both |

## Implementation

### Phase 1: The rule in prose

- [ ] `agents/code-reviewer.md` (applied by edit): removing a control test, or changing what it
      asserts, is a must-fix unless the same diff carries its replacement; the finding names the
      test
- [ ] `skills/test-audit.md`: a removed or inverted control is must-fix; the missing-control row
      stays as it is
- [ ] A project-wide statement of the rule in `docs/` (the place is the implementer's: `CLAUDE.md`
      Code conventions or a testing note), linked from `docs/tui-style.md` rule 3

### Phase 2: The mechanical check

- [ ] Put options A, B and both to the user, with the blocking question; record the answer as
      `## Decisions (user, <date>)` and rewrite the steps below to it
- [ ] Implement the chosen form
- [ ] Seed it with the project's existing controls — at least the forged-hold positive control and
      the negative controls the integration scripts name in their headers
- [ ] Negative control: adding a test, and editing a non-control test, raise nothing

### Phase 3: Integration test

- [ ] Create `scripts/test-control-removal-check.sh` (hermetic: a scratch copy of the tree or a
      scratch git repository, no daemon): deleting `TestExecUnverifiedHoldReleasedByAuditedUserApproval`
      from the copy is flagged by name
- [ ] If the chosen form sees inversion: flipping that test's expected state from running to pending
      is flagged
- [ ] Negative control: the unmodified tree raises nothing; a copy with an added test raises nothing
- [ ] Coverage floor so a skipped section cannot report green
- [ ] Run the script and record pass/fail counts in this spec

## Status

**Backlog — parked at filing 2026-11-24** by plan, under the rule that a spec holding an unrecorded
design decision is not worked unattended. Filed as a split from MUX-157. One criterion ticked as already shipped. The
mechanical form (registry test, diff check or both; blocking or advisory) is undecided — Phase 1 can
be worked without it, Phase 2 cannot.
