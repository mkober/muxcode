package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Graph node types. Nodes are the units the daemon executor schedules;
// edges route between them by outcome.
const (
	NodeSend      = "send"       // deliver a bus message to a role
	NodeSpawn     = "spawn"      // start an ephemeral worker (StartSpawn)
	NodeWaitHuman = "wait_human" // block until explicit user approval
	NodeWaitEvent = "wait_event" // block until a named bus event
	NodeJoin      = "join"       // fan-in barrier (all/any/quorum)
	NodeCondition = "condition"  // route by EvaluateConditions() outcome
	NodeMap       = "map"        // dynamic fan-out over an item list
)

// Join barrier policies for join nodes.
const (
	JoinAll    = "all"
	JoinAny    = "any"
	JoinQuorum = "quorum"
)

// Edge outcomes reuse the package-level OutcomeSuccess/OutcomeFailure
// constants (history_provenance.go) — the same vocabulary as chains. An
// empty Edge.Outcome means OutcomeSuccess; custom outcome strings are
// allowed and the executor routes whatever outcome a node produces.

// Node is one vertex of a graph definition. Which fields are required
// depends on Type — see Validate() for the per-type rules.
type Node struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role,omitempty"`       // send/spawn/map: target role
	Action     string         `json:"action,omitempty"`     // send: bus action
	Message    string         `json:"message,omitempty"`    // send/spawn/map: message template (${intent} interpolated at run time)
	Conditions map[string]any `json:"conditions,omitempty"` // condition: same dialect as chains (EvaluateConditions)
	Join       string         `json:"join,omitempty"`       // join: all|any|quorum
	Quorum     int            `json:"quorum,omitempty"`     // join: required count when policy is quorum
	Items      string         `json:"items,omitempty"`      // map: item-list source expression
	Event      string         `json:"event,omitempty"`      // wait_event: bus event name to wait for
	Guard      string         `json:"guard,omitempty"`      // send/spawn: dispatch-time predicate evaluated by the daemon (see knownNodeGuards)
	TimeoutSec int            `json:"timeout_secs,omitempty"`
}

// knownNodeGuards lists the dispatch-time predicates a send/spawn node may
// declare. GuardSpecComplete blocks dispatch while the active spec has open
// checkbox items — the mechanism is daemon-side (MUX-114) because an
// instruction to the receiving agent is exactly the guard style that defect
// showed fails open. GuardPhaseComplete blocks only while the phase named
// in the run's intent ("Phase 1: …") has open items — a full-spec guard on
// a ship gate would block every legitimate partial-phase ship (user
// decision 2026-08-28); with no phase in the intent it passes through.
// GuardPhaseProgress blocks a per-phase commit whose loop iteration did
// not complete its phase (completed phases must reach iterations+1) —
// the trigger for the multi-phase stuck gate (MUX-121 decision 4).
const (
	GuardSpecComplete  = "spec-complete"
	GuardPhaseComplete = "phase-complete"
	GuardPhaseProgress = "phase-progress"
)

var knownNodeGuards = map[string]bool{
	GuardSpecComplete:  true,
	GuardPhaseComplete: true,
	GuardPhaseProgress: true,
}

// Edge routes from one node to another when the source node produces the
// given outcome. Multiple edges from the same node with the same outcome
// fan out in parallel. MaxIterations marks an explicit loop edge: it caps
// how many times the edge may fire in one run, and exempts the cycle it
// closes from the DAG check — cycles without a capped edge are invalid.
// MaxIterationsFromSpec derives the cap from the active spec's phase
// count at run creation (MUX-121: a fixed cap silently truncates a long
// spec or over-allows a short one); CreateGraphRun resolves it into
// MaxIterations on the frozen copy, so the executor sees only numbers.
type Edge struct {
	From                  string `json:"from"`
	To                    string `json:"to"`
	Outcome               string `json:"outcome,omitempty"` // empty means "success"
	MaxIterations         int    `json:"max_iterations,omitempty"`
	MaxIterationsFromSpec bool   `json:"max_iterations_from_spec,omitempty"`
}

// Graph is a declarative multi-agent orchestration definition.
type Graph struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// RequiresSpec gates run creation on an active requirements spec
	// (muxcode spec set) — for graphs whose nodes implement against it.
	RequiresSpec bool   `json:"requires_spec,omitempty"`
	Start        string `json:"start"`
	Nodes        []Node `json:"nodes"`
	Edges        []Edge `json:"edges"`
}

// GraphValidation collects the outcome of Graph.Validate. Errors block
// execution; warnings are advisory.
type GraphValidation struct {
	Errors   []string
	Warnings []string
}

// OK reports whether the graph is executable (no errors).
func (v *GraphValidation) OK() bool {
	return len(v.Errors) == 0
}

func (v *GraphValidation) errf(format string, a ...any) {
	v.Errors = append(v.Errors, fmt.Sprintf(format, a...))
}

func (v *GraphValidation) warnf(format string, a ...any) {
	v.Warnings = append(v.Warnings, fmt.Sprintf(format, a...))
}

// Format renders the validation result for human-readable CLI output.
func (v *GraphValidation) Format() string {
	var b strings.Builder
	for _, e := range v.Errors {
		fmt.Fprintf(&b, "  ERROR: %s\n", e)
	}
	for _, w := range v.Warnings {
		fmt.Fprintf(&b, "  WARN:  %s\n", w)
	}
	if v.OK() {
		b.WriteString("  OK\n")
	}
	return b.String()
}

// knownNodeTypes lists all recognized node types.
var knownNodeTypes = map[string]bool{
	NodeSend:      true,
	NodeSpawn:     true,
	NodeWaitHuman: true,
	NodeWaitEvent: true,
	NodeJoin:      true,
	NodeCondition: true,
	NodeMap:       true,
}

// gatedAtlassianActions are the plan agent's Atlassian write actions from
// the delegation protocol (reads like jira-read stay open). Nodes sending
// these require an upstream wait_human gate, same as git mutations.
var gatedAtlassianActions = map[string]bool{
	"jira-write":       true,
	"confluence-write": true,
}

// nodeRequiresGate reports whether a node fires a git mutation or an
// Atlassian write and therefore must sit downstream of a wait_human gate.
// Applies to every node type that delivers work to a role — send, spawn,
// and map (whose fanned-out workers inherit its role). The commit role's
// only read-shaped action is pr-read; everything else addressed to commit
// is treated as a mutation. Authority gates (CheckCommitAuthority,
// CheckAtlassianAuthority) remain the runtime backstop — this rule catches
// the violation at validate time.
func nodeRequiresGate(n *Node) bool {
	if n.Type != NodeSend && n.Type != NodeSpawn && n.Type != NodeMap {
		return false
	}
	role := NormalizeBusRole(n.Role)
	if role == "commit" && n.Action != "pr-read" {
		return true
	}
	return gatedAtlassianActions[n.Action]
}

// NodeRequiresGate reports whether a node fires a git mutation or an
// Atlassian write. Exported for the gate queue (MUX-031), which must flag
// approvals releasing such nodes with the same predicate the validator's
// gate rule applies — a second implementation would drift.
func NodeRequiresGate(n *Node) bool {
	return nodeRequiresGate(n)
}

// ParseGraph decodes a graph definition from JSON.
func ParseGraph(data []byte) (*Graph, error) {
	var g Graph
	if err := json.Unmarshal(data, &g); err != nil {
		return nil, fmt.Errorf("invalid graph JSON: %w", err)
	}
	return &g, nil
}

// LoadGraphFile reads and decodes a graph definition file.
func LoadGraphFile(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseGraph(data)
}

// nodeByID returns a lookup map, reporting missing and duplicate ids into v.
func (g *Graph) nodeByID(v *GraphValidation) map[string]*Node {
	byID := make(map[string]*Node, len(g.Nodes))
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" {
			v.errf("node %d has no id", i)
			continue
		}
		if _, dup := byID[n.ID]; dup {
			v.errf("duplicate node id %q", n.ID)
			continue
		}
		byID[n.ID] = n
	}
	return byID
}

// outgoing returns edge indices grouped by source node id.
func (g *Graph) outgoing() map[string][]int {
	out := make(map[string][]int)
	for i, e := range g.Edges {
		out[e.From] = append(out[e.From], i)
	}
	return out
}

// incoming returns edge indices grouped by target node id.
func (g *Graph) incoming() map[string][]int {
	in := make(map[string][]int)
	for i, e := range g.Edges {
		in[e.To] = append(in[e.To], i)
	}
	return in
}

// Validate performs full structural validation of a graph definition:
// reference integrity, per-type field rules, reachability from start,
// the capped-cycle DAG rule, join-policy sanity, and the wait_human gate
// rule for git/Atlassian mutations.
func (g *Graph) Validate() *GraphValidation {
	v := &GraphValidation{}

	if g.Name == "" {
		v.errf("graph has no name")
	}
	if len(g.Nodes) == 0 {
		v.errf("graph has no nodes")
		return v
	}

	byID := g.nodeByID(v)

	if g.Start == "" {
		v.errf("graph has no start node")
	} else if _, ok := byID[g.Start]; !ok {
		v.errf("start node %q is not defined", g.Start)
	}

	for i := range g.Nodes {
		g.validateNode(&g.Nodes[i], v)
	}
	g.validateEdges(byID, v)

	// Structural passes need resolvable references to mean anything.
	if !v.OK() {
		return v
	}

	g.validateReachability(v)
	g.validateAcyclic(v)
	g.validateJoins(v)
	g.validateGates(byID, v)
	g.validateGateText(byID, v)
	return v
}

// validateNode enforces per-type required fields and flags fields that
// have no meaning for the node's type.
func (g *Graph) validateNode(n *Node, v *GraphValidation) {
	if n.ID == "" {
		return // already reported by nodeByID
	}
	if !knownNodeTypes[n.Type] {
		v.errf("node %q has unknown type %q", n.ID, n.Type)
		return
	}

	requireRole := func() {
		if n.Role == "" {
			v.errf("%s node %q requires a role", n.Type, n.ID)
		} else if !IsKnownRole(NormalizeBusRole(n.Role)) {
			v.errf("%s node %q references unknown role %q", n.Type, n.ID, n.Role)
		} else if NormalizeBusRole(n.Role) == "prompt" {
			// Prompt requests carry a human's typed words — the approve
			// guard trusts them as such — so a graph cannot dispatch one.
			// CheckPromptAuthority also refuses at runtime; failing here
			// beats a run dying mid-flight (see bus/prompt_authority.go).
			v.errf("%s node %q targets the prompt role — prompt requests are human-initiated and cannot be dispatched by a graph", n.Type, n.ID)
		}
	}
	requireMessage := func() {
		if n.Message == "" {
			v.errf("%s node %q requires a message", n.Type, n.ID)
		}
	}

	switch n.Type {
	case NodeSend:
		requireRole()
		requireMessage()
		if n.Action == "" {
			v.errf("send node %q requires an action", n.ID)
		}
	case NodeSpawn:
		requireRole()
		requireMessage()
	case NodeMap:
		requireRole()
		requireMessage()
		if n.Items == "" {
			v.errf("map node %q requires an items source", n.ID)
		}
	case NodeCondition:
		if len(n.Conditions) == 0 {
			v.errf("condition node %q has no conditions", n.ID)
		}
		for _, w := range ValidateConditions(n.Conditions) {
			// Unknown condition types are warnings for chains (forward
			// compatibility) but errors here: the executor routes edges on
			// the evaluation result, so a typo would silently route failure.
			v.errf("condition node %q: %s", n.ID, w)
		}
	case NodeJoin:
		if n.Join == "" {
			v.errf("join node %q requires a join policy (all|any|quorum)", n.ID)
		}
	case NodeWaitEvent:
		if n.Event == "" {
			v.errf("wait_event node %q requires an event name", n.ID)
		}
	case NodeWaitHuman:
		// No required fields; Message is an optional approval prompt.
	}

	if n.Join != "" && n.Type != NodeJoin {
		v.errf("node %q has a join policy but type %q", n.ID, n.Type)
	}
	if n.Guard != "" {
		if !knownNodeGuards[n.Guard] {
			v.errf("node %q has unknown guard %q (known: %s, %s, %s)", n.ID, n.Guard, GuardSpecComplete, GuardPhaseComplete, GuardPhaseProgress)
		}
		if n.Type != NodeSend && n.Type != NodeSpawn {
			v.errf("node %q has a guard but type %q — guards are dispatch-time and only apply to send/spawn nodes", n.ID, n.Type)
		}
	}
	if len(n.Conditions) > 0 && n.Type != NodeCondition {
		v.warnf("node %q has conditions but type %q — they are ignored", n.ID, n.Type)
	}
	if n.TimeoutSec < 0 {
		v.errf("node %q has negative timeout_secs", n.ID)
	}
}

// validateEdges checks reference integrity and duplicate edges.
func (g *Graph) validateEdges(byID map[string]*Node, v *GraphValidation) {
	seen := make(map[string]bool)
	for i, e := range g.Edges {
		if e.From == "" || e.To == "" {
			v.errf("edge %d is missing from/to", i)
			continue
		}
		if _, ok := byID[e.From]; !ok {
			v.errf("edge %d references undefined node %q", i, e.From)
		}
		if _, ok := byID[e.To]; !ok {
			v.errf("edge %d references undefined node %q", i, e.To)
		}
		if e.MaxIterations < 0 {
			v.errf("edge %s->%s has negative max_iterations", e.From, e.To)
		}
		if e.MaxIterationsFromSpec && e.MaxIterations > 0 {
			v.errf("edge %s->%s sets both max_iterations and max_iterations_from_spec — choose one", e.From, e.To)
		}
		key := e.From + "\x00" + e.To + "\x00" + edgeOutcome(e)
		if seen[key] {
			v.errf("duplicate edge %s->%s on outcome %q", e.From, e.To, edgeOutcome(e))
		}
		seen[key] = true
	}
}

// edgeOutcome resolves the effective outcome of an edge (empty = success).
func edgeOutcome(e Edge) string {
	if e.Outcome == "" {
		return OutcomeSuccess
	}
	return e.Outcome
}

// validateReachability requires every node to be reachable from start.
func (g *Graph) validateReachability(v *GraphValidation) {
	out := g.outgoing()
	reached := map[string]bool{g.Start: true}
	queue := []string{g.Start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, i := range out[cur] {
			to := g.Edges[i].To
			if !reached[to] {
				reached[to] = true
				queue = append(queue, to)
			}
		}
	}
	for _, n := range g.Nodes {
		if !reached[n.ID] {
			v.errf("node %q is unreachable from start", n.ID)
		}
	}
}

// validateAcyclic runs a DFS cycle check over the graph with capped loop
// edges removed — explicitly capped (max_iterations > 0) or spec-derived
// (resolved to a number at run creation). Any cycle that survives has no
// iteration bound and would run forever, so it is an error.
func (g *Graph) validateAcyclic(v *GraphValidation) {
	out := make(map[string][]string)
	for _, e := range g.Edges {
		if e.MaxIterations > 0 || e.MaxIterationsFromSpec {
			continue
		}
		out[e.From] = append(out[e.From], e.To)
	}

	const (
		white = 0 // unvisited
		gray  = 1 // on the current DFS path
		black = 2 // fully explored
	)
	color := make(map[string]int)

	var visit func(id string) string
	visit = func(id string) string {
		color[id] = gray
		for _, to := range out[id] {
			switch color[to] {
			case gray:
				return to
			case white:
				if c := visit(to); c != "" {
					return c
				}
			}
		}
		color[id] = black
		return ""
	}

	for _, n := range g.Nodes {
		if color[n.ID] == white {
			if c := visit(n.ID); c != "" {
				v.errf("uncapped cycle through node %q — loops require max_iterations on a loop edge", c)
				return
			}
		}
	}
}

// validateJoins enforces join-policy sanity against actual incoming edges.
func (g *Graph) validateJoins(v *GraphValidation) {
	in := g.incoming()
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeJoin {
			if n.Quorum != 0 {
				v.warnf("node %q has quorum but is not a join node", n.ID)
			}
			continue
		}
		inCount := len(in[n.ID])
		switch n.Join {
		case JoinAll, JoinAny:
			if n.Quorum != 0 {
				v.warnf("join node %q has quorum but policy %q", n.ID, n.Join)
			}
		case JoinQuorum:
			if n.Quorum < 1 || n.Quorum > inCount {
				v.errf("join node %q quorum %d out of range (1..%d incoming edges)", n.ID, n.Quorum, inCount)
			}
		case "":
			// reported by validateNode
		default:
			v.errf("join node %q has unknown policy %q (want all|any|quorum)", n.ID, n.Join)
		}
		if inCount < 2 {
			v.warnf("join node %q has %d incoming edge(s) — barrier has nothing to join", n.ID, inCount)
		}
	}
}

// reachableStoppingAt returns the node ids reachable from start without
// crossing any node for which stop returns true. Stopped nodes are
// entered (present in the set) but their outgoing edges are not
// followed — territory beyond them is unreachable by this walk. Backs
// the validate-time gate rule; the retry re-arm cut uses gateTerritory,
// the per-gate walk, for the same stop-at-gates semantics.
func (g *Graph) reachableStoppingAt(byID map[string]*Node, stop func(*Node) bool) map[string]bool {
	out := g.outgoing()
	seen := map[string]bool{g.Start: true}
	queue := []string{g.Start}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if n, ok := byID[cur]; ok && stop(n) {
			continue
		}
		for _, i := range out[cur] {
			to := g.Edges[i].To
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
			}
		}
	}
	return seen
}

// gateTerritory returns the set of nodes a gate releases: everything
// reachable from gateID without crossing another wait_human node, with
// only success edges followed on the first hop (a gate only produces
// success — its other edges never fire; PR #49 Copilot). The single
// implementation of gate-territory semantics, shared by validateGateText
// and the retry re-arm cut (staleApprovalGates, MUX-132): a node in a
// gate's territory has that gate as the LAST gate on at least one path,
// so the gate's approval is one of the consents that released it.
func (g *Graph) gateTerritory(byID map[string]*Node, gateID string) map[string]bool {
	out := g.outgoing()
	seen := map[string]bool{gateID: true}
	queue := []string{gateID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur != gateID {
			if n, ok := byID[cur]; ok && n.Type == NodeWaitHuman {
				continue
			}
		}
		for _, ei := range out[cur] {
			e := g.Edges[ei]
			if cur == gateID && edgeOutcome(e) != OutcomeSuccess {
				continue
			}
			if !seen[e.To] {
				seen[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}
	return seen
}

// validateGates enforces the wait_human gate rule: no git-mutation or
// Atlassian-write node may be reachable from start without crossing a
// wait_human node. Traversal stops at wait_human nodes — everything
// beyond them is gated territory.
func (g *Graph) validateGates(byID map[string]*Node, v *GraphValidation) {
	ungated := g.reachableStoppingAt(byID, func(n *Node) bool { return n.Type == NodeWaitHuman })

	for i := range g.Nodes {
		n := &g.Nodes[i]
		if nodeRequiresGate(n) && ungated[n.ID] {
			v.errf("node %q fires a git/Atlassian mutation without an upstream wait_human gate", n.ID)
		}
		if isDeployNode(n) && ungated[n.ID] {
			v.warnf("deploy node %q has no upstream wait_human gate — launching the run is its only approval", n.ID)
		}
	}
}

// isDeployNode reports whether a node delivers work to the deploy role —
// an infrastructure mutation outside the git/Atlassian gate rule, flagged
// as a warning rather than gated (MUX-114 Phase 3).
func isDeployNode(n *Node) bool {
	if n.Type != NodeSend && n.Type != NodeSpawn && n.Type != NodeMap {
		return false
	}
	return NormalizeBusRole(n.Role) == "deploy"
}

// Gate-text keyword classes (MUX-114 Phase 3). A gate message "names" a
// dominated mutation when it carries at least one term of the mutation's
// class, matched on word boundaries — plain substring would let "Approve"
// satisfy "pr". Prefix matching covers inflections (commit/committing,
// push/pushing, reply/replying). A heuristic, so violations are warnings:
// user graphs must not be blocked by vocabulary, while the builtin pin
// test holds shipped templates to zero warnings.
var gateTextGitExact = map[string]bool{"pr": true, "gh": true, "git": true}

var gateTextGitPrefixes = []string{
	"commit", "push", "stag", "checkout", "branch", "rebase",
	"merge", "tag", "release", "issue", "comment", "repl",
}

var gateTextAtlassianPrefixes = []string{
	"jira", "confluence", "tracker", "story", "issue", "comment",
}

// gateNamesMutation reports whether a gate message names the mutation
// class of a dominated node.
func gateNamesMutation(msg string, n *Node) bool {
	tokens := strings.FieldsFunc(strings.ToLower(msg), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	git := NormalizeBusRole(n.Role) == "commit"
	prefixes := gateTextAtlassianPrefixes
	if git {
		prefixes = gateTextGitPrefixes
	}
	for _, t := range tokens {
		if git && gateTextGitExact[t] {
			return true
		}
		for _, p := range prefixes {
			if strings.HasPrefix(t, p) {
				return true
			}
		}
	}
	return false
}

// validateGateText warns when a wait_human gate's message fails to name a
// mutation its approval releases. The territory a gate releases is
// everything reachable from it without crossing another gate — the
// nearest-gate principle: a later gate re-states what it dominates, so an
// earlier one need not (MUX-114: gate2 said "review feedback" while three
// nodes later a spec move was pushed).
func (g *Graph) validateGateText(byID map[string]*Node, v *GraphValidation) {
	for i := range g.Nodes {
		gate := &g.Nodes[i]
		if gate.Type != NodeWaitHuman {
			continue
		}
		seen := g.gateTerritory(byID, gate.ID)
		for j := range g.Nodes {
			n := &g.Nodes[j]
			if n.ID == gate.ID || !seen[n.ID] || !nodeRequiresGate(n) {
				continue
			}
			if !gateNamesMutation(gate.Message, n) {
				v.warnf("gate %q releases mutation node %q but its message does not name the mutation — the approval must state its consequence", gate.ID, n.ID)
			}
		}
	}
}

// GraphTemplateInfo describes one resolvable template for graph list.
type GraphTemplateInfo struct {
	Name        string
	Source      string // "project", "user", or "builtin"
	Description string
}

// Writable graph template scopes. Builtins are read-only — there is
// deliberately no scope naming them.
const (
	GraphScopeProject = "project"
	GraphScopeUser    = "user"
)

// projectGraphDir is the project-local template directory (tier 1).
const projectGraphDir = ".muxcode/graphs"

// userGraphDir returns the user-level template directory (tier 2).
func userGraphDir() string {
	home, _ := os.UserHomeDir()
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "muxcode", "graphs")
}

// ResolveGraphTemplate finds a graph template by name using the same
// 3-tier precedence as agent files: project > user > builtin. Returns
// the parsed graph and its source tier.
func ResolveGraphTemplate(name string) (*Graph, string, error) {
	p := filepath.Join(projectGraphDir, name+".json")
	if _, err := os.Stat(p); err == nil {
		g, err := LoadGraphFile(p)
		return g, "project", err
	}

	if dir := userGraphDir(); dir != "" {
		p = filepath.Join(dir, name+".json")
		if _, err := os.Stat(p); err == nil {
			g, err := LoadGraphFile(p)
			return g, "user", err
		}
	}

	if data, ok := builtinGraphJSON[name]; ok {
		g, err := ParseGraph([]byte(data))
		return g, "builtin", err
	}

	if current, ok := renamedGraphTemplates[name]; ok {
		return nil, "", fmt.Errorf("unknown graph template: %q (renamed to %q)", name, current)
	}
	return nil, "", fmt.Errorf("unknown graph template: %q", name)
}

// renamedGraphTemplates maps retired builtin names to their current ones.
// A retired name fails loudly naming its successor rather than resolving
// as an alias — the rename exists so the name says what the template
// does, and an alias would keep the old one in circulation.
var renamedGraphTemplates = map[string]string{
	"req-code-pr":     "spec-to-pr",
	"story-lifecycle": "spec-to-pr", // removed — spec-to-pr covers the same arc
}

// ListGraphTemplates enumerates all resolvable templates across the three
// tiers. A name appearing in multiple tiers is listed once at its highest
// precedence, matching what ResolveGraphTemplate would load.
func ListGraphTemplates() []GraphTemplateInfo {
	seen := make(map[string]bool)
	var infos []GraphTemplateInfo

	addDir := func(dir, source string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".json")
			if seen[name] {
				continue
			}
			seen[name] = true
			desc := ""
			if g, err := LoadGraphFile(filepath.Join(dir, e.Name())); err == nil {
				desc = g.Description
			}
			infos = append(infos, GraphTemplateInfo{Name: name, Source: source, Description: desc})
		}
	}

	addDir(projectGraphDir, "project")
	if dir := userGraphDir(); dir != "" {
		addDir(dir, "user")
	}
	for name, data := range builtinGraphJSON {
		if seen[name] {
			continue
		}
		seen[name] = true
		desc := ""
		if g, err := ParseGraph([]byte(data)); err == nil {
			desc = g.Description
		}
		infos = append(infos, GraphTemplateInfo{Name: name, Source: "builtin", Description: desc})
	}

	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

// graphScopeDir maps a writable scope to its template directory.
func graphScopeDir(scope string) (string, error) {
	switch scope {
	case GraphScopeProject:
		return projectGraphDir, nil
	case GraphScopeUser:
		dir := userGraphDir()
		if dir == "" {
			return "", fmt.Errorf("cannot resolve user graph directory: no home directory")
		}
		return dir, nil
	}
	return "", fmt.Errorf("unknown graph scope %q (want %q or %q)", scope, GraphScopeProject, GraphScopeUser)
}

// WriteGraphDefinition validates g and writes it as <name>.json into the
// scope's template directory, creating the directory on first write so a
// fresh checkout with no .muxcode/graphs/ works. Validation runs before
// anything touches the filesystem: a failing definition leaves no file
// and no directory behind, and the returned GraphValidation carries the
// errors for the caller to report. The write itself is atomic (tmp +
// rename, like the run store) so a crash cannot leave a half-written
// template that ResolveGraphTemplate would later load. The graph name
// doubles as the filename, so names that would escape the directory or
// hide the file are rejected.
func WriteGraphDefinition(g *Graph, scope string) (string, *GraphValidation, error) {
	dir, err := graphScopeDir(scope)
	if err != nil {
		return "", nil, err
	}
	v := g.Validate()
	if !v.OK() {
		return "", v, fmt.Errorf("graph %q failed validation; not written", g.Name)
	}
	if strings.ContainsAny(g.Name, `/\`) || strings.HasPrefix(g.Name, ".") {
		return "", v, fmt.Errorf("graph name %q is not usable as a template filename", g.Name)
	}
	// Required at the creation chokepoint only — not in Validate(), which
	// also gates RUNNING existing description-less template files. The
	// launcher lists every template with its description; a new graph
	// without one is an unlabeled row nobody can tell apart later.
	if strings.TrimSpace(g.Description) == "" {
		return "", v, fmt.Errorf("graph %q has no description — add a one-line \"description\" field; the launcher lists templates by it", g.Name)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", v, err
	}
	data, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		return "", v, err
	}
	path := filepath.Join(dir, g.Name+".json")
	if err := atomicWriteFile(path, append(data, '\n')); err != nil {
		return "", v, err
	}
	return path, v, nil
}
