package bus

import (
	"fmt"
	"strings"
)

// Freshness condition types (MUX-166). Both read one BranchFreshness
// measurement per evaluation (ChainContext.branchFreshness) and fail closed
// while it is unmeasured, so a refresh node carrying both routes a fetch
// failure to its sync gate rather than to implement.
const (
	CondBranchBehind              = "branch_behind"
	CondUpstreamTouchesSpecFiles  = "upstream_touches_spec_files"
	branchFreshnessLifecycleEvent = "branch-freshness"
)

// isFreshnessCheck reports whether a condition node measures branch
// freshness — the node validateFreshness requires before a spawn.
func isFreshnessCheck(n *Node) bool {
	if n.Type != NodeCondition {
		return false
	}
	_, behind := n.Conditions[CondBranchBehind]
	_, touches := n.Conditions[CondUpstreamTouchesSpecFiles]
	return behind || touches
}

// branchFreshness measures the session repo against origin/main once per
// evaluation, with the active spec's named files, and writes one
// branch-freshness lifecycle row naming the run — fresh or stale, the
// numbers either way, so the threshold can be tuned from evidence.
func (ctx *ChainContext) branchFreshness() *Freshness {
	if ctx.freshness != nil {
		return ctx.freshness
	}
	spec, repo, okSpec, transient, refused := activeSpecRepo(ctx.Session)
	var f Freshness
	switch {
	case transient:
		f = unmeasuredFreshness("repo dir unavailable this tick")
	case refused:
		f = unmeasuredFreshness("active spec pointer resolves outside the repo")
	default:
		if !okSpec {
			spec, repo = "", SessionRepoDir(ctx.Session)
		}
		if repo == "" {
			f = unmeasuredFreshness("repo dir unavailable this tick")
		} else {
			f = BranchFreshness(repo, spec)
		}
	}
	level, run := "info", ""
	if f.Stale {
		level = "warn"
	}
	if ctx.GraphRun != nil {
		run = ctx.GraphRun.ID + ": "
	}
	LogLifecycle(ctx.Session, level, "daemon", branchFreshnessLifecycleEvent, run+f.Summary())
	ctx.freshness = &f
	return ctx.freshness
}

// freshnessOutput is the measurement's summary when this evaluation took
// one, else "" — a condition node records it as its output for gate text.
func (ctx *ChainContext) freshnessOutput() string {
	if ctx == nil || ctx.freshness == nil {
		return ""
	}
	return ctx.freshness.Summary()
}

func unmeasuredFreshness(reason string) Freshness {
	return Freshness{
		FetchAge:   -1,
		MaxBehind:  freshnessMaxBehind(""),
		Stale:      true,
		Reasons:    []string{reason},
		Unmeasured: []string{reason},
	}
}

// branchBehindMax reads branch_behind's value: {"max": N} with N ≥ 1, or {}
// / true for MUXCODE_FRESHNESS_MAX_BEHIND (returned as 0).
func branchBehindMax(value any) (int, error) {
	switch v := value.(type) {
	case bool:
		if v {
			return 0, nil
		}
	case map[string]any:
		if len(v) == 0 {
			return 0, nil
		}
		raw, has := v["max"]
		if !has {
			break
		}
		var n int
		switch m := raw.(type) {
		case float64:
			n = int(m)
			if float64(n) != m {
				n = 0
			}
		case int:
			n = m
		}
		if n >= 1 && len(v) == 1 {
			return n, nil
		}
	}
	return 0, fmt.Errorf(`branch_behind must be {"max": N} with N ≥ 1, {} or true, got %v`, value)
}

// evalBranchBehind passes when the branch is fewer than max commits behind
// origin/main — max is the count that gates, as MUXCODE_FRESHNESS_MAX_BEHIND
// is, and defaults to it. An unmeasured branch fails.
func evalBranchBehind(value any, ctx *ChainContext) ConditionResult {
	result := ConditionResult{Type: CondBranchBehind}
	max, err := branchBehindMax(value)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	f := ctx.branchFreshness()
	if max == 0 {
		max = f.MaxBehind
	}
	result.Pattern = fmt.Sprintf("max %d", max)
	if len(f.Unmeasured) > 0 {
		result.Detail = "freshness unmeasured: " + strings.Join(f.Unmeasured, "; ")
		return result
	}
	result.Passed = f.Behind < max
	result.Detail = fmt.Sprintf("%d behind %s, gate at %d", f.Behind, FreshnessBase, max)
	return result
}

// evalUpstreamTouchesSpecFiles passes when whether origin/main's changes
// since the merge base touch a spec-named file equals the value — with no
// active spec, a file the branch itself changes (BranchFreshness). An
// unmeasured branch fails for either value: unknown is never "untouched".
func evalUpstreamTouchesSpecFiles(value any, ctx *ChainContext) ConditionResult {
	result := ConditionResult{Type: CondUpstreamTouchesSpecFiles}
	want, ok := value.(bool)
	if !ok {
		result.Detail = fmt.Sprintf("upstream_touches_spec_files must be true or false, got %T", value)
		return result
	}
	result.Pattern = fmt.Sprintf("%v", want)
	f := ctx.branchFreshness()
	if len(f.Unmeasured) > 0 {
		result.Detail = "freshness unmeasured: " + strings.Join(f.Unmeasured, "; ")
		return result
	}
	touches := len(f.Overlap) > 0
	result.Passed = touches == want
	names := make([]string, len(f.Overlap))
	for i, c := range f.Overlap {
		names[i] = c.String()
	}
	result.Detail = fmt.Sprintf("upstream touches %s=%v %v", f.overlapNoun(), touches, names)
	return result
}

// freshnessValueWarnings checks the two freshness conditions' value shapes.
func freshnessValueWarnings(conditions map[string]any) []string {
	var out []string
	if v, has := conditions[CondBranchBehind]; has {
		if _, err := branchBehindMax(v); err != nil {
			out = append(out, err.Error())
		}
	}
	if v, has := conditions[CondUpstreamTouchesSpecFiles]; has {
		if _, ok := v.(bool); !ok {
			out = append(out, fmt.Sprintf("upstream_touches_spec_files must be true or false, got %v", v))
		}
	}
	return out
}

// implementsWork reports whether a node writes code: a spawn worker, or a
// send or map addressed to edit, the role that implements.
func implementsWork(n *Node) bool {
	switch n.Type {
	case NodeSpawn:
		return true
	case NodeSend, NodeMap:
		return NormalizeBusRole(n.Role) == "edit"
	}
	return false
}

// validateFreshness holds every builtin template, and any requires_spec
// template, to MUX-166: no node that implements work (implementsWork) is
// reachable from start, or from a loop re-entry, without crossing a
// freshness check. A re-entry is the target of a capped success edge whose
// source is not itself a worker: a lap that a worker re-enters (fix ->
// build) is already behind the check that worker sat behind. Every edge is
// followed, failure edges included, so a worker released by a failing send
// (60-integration-suite's suite -> fix) is held to the rule too.
//
// A user or project template is held to it only when it requires a spec or
// carries a builtin's name — one that shadows a builtin is run in its place.
func (g *Graph) validateFreshness(v *GraphValidation) {
	if _, builtin := builtinGraphJSON[g.Name]; !builtin && !g.RequiresSpec {
		return
	}
	roots := []string{g.Start}
	for _, e := range g.Edges {
		if e.Capped() && (e.Outcome == "" || e.Outcome == OutcomeSuccess) {
			if src := g.node(e.From); src == nil || !implementsWork(src) {
				roots = append(roots, e.To)
			}
		}
	}
	reported := map[string]bool{}
	for _, root := range roots {
		seen := map[string]bool{}
		queue := []string{root}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			n := g.node(id)
			if seen[id] || n == nil || isFreshnessCheck(n) {
				continue
			}
			seen[id] = true
			if implementsWork(n) {
				if !reported[id] {
					reported[id] = true
					v.errf("%s node %q is reachable from %q without crossing a freshness check (%s / %s) — a run could build on a stale base (MUX-166)",
						n.Type, id, root, CondBranchBehind, CondUpstreamTouchesSpecFiles)
				}
				continue
			}
			for _, e := range g.Edges {
				if e.From == id {
					queue = append(queue, e.To)
				}
			}
		}
	}
}
