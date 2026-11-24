# MUX-239: `Validate()` Accepts a Graph Whose Review Node Has No Failure Route

**Tracking:** [mkober/muxcode#253](https://github.com/mkober/muxcode/issues/253)

**Split from:** [MUX-127](../completed/MUX-127-review-completion-routing.md), closed as overtaken
2026-11-24. This is the one item of it the user kept.

On 2026-08-31 run `1788195259-req-code-pr-3fbcc7da` reached review with tests passing; review
returned a failure carrying two must-fix findings, and the run died with
`graph-run-failed  node review failed with no live edge`. The template wired `build → fix` and
`test → fix` but no `review → fix`. `50-spec-to-pr` and its siblings carry that edge today
(`bus/graph_templates.go`); whether every builtin's every review node has a failure route was not
checked when this was filed — `30-build-test-review`'s review node, for one, has no outgoing edge at
all, which may be a terminal by design. Phase 1 surveys them. Nothing stops the next template — a builtin edit, or a user's or project's own — from being written without
it: `Validate()` (`bus/graph.go`) rejects uncapped cycles and ungated commits, and has no rule here.

## Decisions (user, 2026-11-24)

Relayed by edit. `Validate()` rejects a graph whose review node has no failure route. MUX-127 left
this as "decide and implement whether"; it is decided.

## Requirements

### Acceptance criteria

- [ ] `Validate()` reports an error for a graph holding a send node addressed to `review` whose
      outgoing edges include none taken on `failure`
- [ ] The error names the node and says what is missing
- [ ] `muxcode graph validate` and `graph run` both refuse such a graph, as they do an uncapped cycle
- [ ] Every builtin template passes the rule
- [ ] Negative control: a graph whose review node routes failure anywhere — a `fix` node, a
      `wait_human` gate, a terminal — passes; the rule asks for a route, not for a repair

### Technical approach

One rule in `Validate()` beside the cycle-cap and commit-gate rules. "Review node" is a send node
whose role is `review`; a `condition` node downstream that branches on the review's outcome counts
as a route only if the review node itself has a failure edge to reach it. Phase 1 checks every
builtin first, so the rule cannot land red.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/graph.go` | `Validate()` — the rule |
| `tools/muxcode/bus/graph_templates.go` | The builtins the rule must accept |
| `docs/agent-bus.md` | `muxcode graph validate` lists what is rejected |

## Implementation

### Phase 1: Survey

- [ ] List every builtin template's review nodes and their failure edges; record any without one
      here and fix the template in the same change as the rule

### Phase 2: The rule

- [ ] Add the rule to `Validate()` with a unit test per case: no failure edge (rejected), failure
      edge to `fix` (accepted), failure edge to a gate (accepted), no review node (accepted)
- [ ] `TestBuiltin…` style test: every builtin validates clean
- [ ] Add the rule to the rejected-shapes list in `docs/agent-bus.md`

### Phase 3: Integration test

- [ ] Create `scripts/test-review-failure-route.sh` — hermetic, muxcode built from the tree,
      scratch HOME and bus, no daemon
- [ ] A project template with a review node and no failure edge: `graph validate` exits non-zero
      naming the node, and `graph run` creates no run
- [ ] Negative control: the same template with a `review → fix` failure edge validates and a run is
      created
- [ ] Every builtin listed by `graph list` validates clean
- [ ] Coverage floor, and the script is named in `CLAUDE.md`'s scripts row
- [ ] Run the script and confirm all checks pass

## Status

**Backlog** — 0/15. Filed 2026-11-24 by plan on the user's decision relayed by edit, when MUX-127
closed. Not started.
