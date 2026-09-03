package cmd

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// Graph handles the "muxcode graph" subcommand — the graph-agent
// orchestrator control CLI (MUX-014).
// Usage:
//
//	muxcode graph run <template>|--file <path> [intent...]
//	muxcode graph validate <file.json|template-name>
//	muxcode graph list
//	muxcode graph status [--json] [run-id]
//	muxcode graph cancel <run-id>
//	muxcode graph retry <run-id> --from <node>
//	muxcode graph approve <run-id> <node>
func Graph(args []string) {
	if len(args) == 0 {
		graphUsage()
		os.Exit(1)
	}

	switch args[0] {
	case "run":
		graphRun(args[1:])

	case "validate":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode graph validate <file.json|template-name>")
			os.Exit(1)
		}
		graphValidate(args[1])

	case "list":
		graphList()

	case "status":
		graphStatus(args[1:])

	case "cancel":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode graph cancel <run-id>")
			os.Exit(1)
		}
		if err := bus.CancelGraphRun(bus.BusSession(), args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Run %s canceled\n", args[1])

	case "create":
		graphCreate(args[1:])

	case "export":
		// Print a resolved template's full JSON — the read-back half of
		// modify-via-shadow: export a builtin, adjust the JSON, and
		// `graph create` it as a project-tier template that shadows the
		// builtin (project > user > builtin resolution). Without this,
		// builtins were write-only for the prompt-agent (user-requested,
		// 2026-08-27).
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode graph export <template-name>")
			os.Exit(1)
		}
		g, source, err := bus.ResolveGraphTemplate(args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		data, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "# source: %s\n", source)
		fmt.Println(string(data))

	case "retry":
		graphRetry(args[1:])

	case "ui":
		graphUI(args[1:])

	case "approve":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode graph approve <run-id> <node>")
			os.Exit(1)
		}
		if err := bus.ApproveGraphGate(bus.BusSession(), args[1], args[2]); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Gate %s approved on run %s — the daemon resumes it on its next tick\n", args[2], args[1])

	default:
		fmt.Fprintf(os.Stderr, "Unknown graph subcommand: %s\n", args[0])
		graphUsage()
		os.Exit(1)
	}
}

func graphUsage() {
	fmt.Fprint(os.Stderr, `Usage: muxcode graph <command> [args]

Commands:
  run <template>|--file <path> [intent...]  Start a run (returns immediately; the daemon executes); an omitted intent derives from the branch's spec
  validate <file|template>                  Validate a graph definition file or template
  create --json '<json>'|<file> [--scope project|user]
                                            Validate a definition and write it as a template
                                            (project: .muxcode/graphs/, user: ~/.config/muxcode/graphs/)
  list                                      List resolvable graph templates
  export <template>                         Print a resolved template's JSON (modify + create = shadow a builtin)
  status [--json] [run-id]                  Show a run's per-node state (no id: list all runs)
  cancel <run-id>                           Cancel a run (unstarted nodes are skipped)
  retry <run-id> --from <node>              Re-execute from a node, keeping upstream results
  approve <run-id> <node>                   Release a wait_human gate
  ui [run-id] [--render-once] [--width N]   Interactive run browser / DAG view (MUX-031)
  ui --templates                            Open the template launcher
  ui --gates [--render-once]                Open the pending-gate approval queue
  ui --prompt [--render-once]               Open the Prompt surface (MUX-109)
`)
}

// graphRun starts a run from a template or definition file. It only
// creates the run store — the daemon's next checkGraphRuns tick begins
// execution, so this returns immediately and never blocks the caller.
func graphRun(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: muxcode graph run <template>|--file <path> [spec...]")
		os.Exit(1)
	}

	var g *bus.Graph
	var template string
	var err error
	if args[0] == "--file" {
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode graph run --file <path> [spec...]")
			os.Exit(1)
		}
		template = args[1]
		g, err = bus.LoadGraphFile(template)
		args = args[2:]
	} else {
		template = args[0]
		g, _, err = bus.ResolveGraphTemplate(template)
		args = args[1:]
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	intent := strings.Join(args, " ")
	if intent == "" && tui.TemplateNeedsIntent(g) {
		intent = intentFromActiveSpec(bus.BusSession())
		requireIntent(template, intent)
	}
	run, err := bus.CreateGraphRun(bus.BusSession(), g, template, intent)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Started run %s (%s)\n", run.ID, template)
	fmt.Printf("Status: muxcode graph status %s\n", run.ID)
	if w := bus.UnscopedPhaseGuardWarning(g, intent); w != "" {
		fmt.Printf("Warning: %s\n", w)
	}
}

// requireIntent stops a run whose template interpolates ${intent} when
// none was resolved. An empty intent drives the wrong work rather than
// none: the phase guard scopes to the intent's phase, so an empty one
// leaves it unscoped and the commit ships whatever the tree happens to
// hold. Refusing beats warning and starting anyway — which is what a
// declined picker, or an underivable intent, used to do.
func requireIntent(template, intent string) {
	if intent != "" {
		return
	}
	fmt.Fprintf(os.Stderr, "Error: %s needs an intent and none was resolved — set one with `muxcode spec set <path>`, or pass it: muxcode graph run %s <intent>\n", template, template)
	os.Exit(1)
}

// intentFromActiveSpec derives the run intent from the active spec when
// the caller gave none, and offers a picker when no pointer is set. The
// printed lines are the CLI's confirmation of what the run will drive.
//
// The active spec is the single source: the same file ${current_phase}
// resolves against, so the intent and the work cannot name different
// phases. A run therefore picks up wherever the doc says it is, on any
// branch.
func intentFromActiveSpec(session string) string {
	spec, err := bus.ActiveSpecIntent(session)
	if errors.Is(err, bus.ErrNoActiveSpec) {
		return intentFromSpecChoice(session)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: no intent given and none derived from the active spec: %v\n", err)
		return ""
	}
	fmt.Printf("Active spec: %s\n", spec.Path)
	fmt.Printf("Intent: %s\n", spec.Intent)
	return spec.Intent
}

// intentFromSpecChoice prompts for a spec when no pointer is set, sets it
// active, and returns its intent. A non-interactive caller cannot be
// asked, so it gets the same list as an error and picks with `spec set`.
func intentFromSpecChoice(session string) string {
	choices, err := bus.ListSpecChoices(session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: no active spec set and none to choose from: %v\n", err)
		return ""
	}
	fmt.Fprintln(os.Stderr, "No active spec is set. Choose the spec this run should drive:")
	for i, c := range choices {
		fmt.Fprintf(os.Stderr, "  %2d) [%s] %s\n", i+1, c.Dir, c.Intent)
	}
	if !stdinIsTerminal() {
		fmt.Fprintln(os.Stderr, "Not a terminal — run `muxcode spec set <path>` and start the run again.")
		return ""
	}
	fmt.Fprint(os.Stderr, "Number (Enter to cancel): ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return ""
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(choices) {
		fmt.Fprintln(os.Stderr, "No spec selected — start the run again once one is set.")
		return ""
	}
	pick := choices[n-1]
	if err := bus.WriteActiveSpec(session, pick.Path); err != nil {
		fmt.Fprintf(os.Stderr, "Error: cannot set active spec: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Active spec set: %s\n", pick.Path)
	fmt.Printf("Intent: %s\n", pick.Intent)
	return pick.Intent
}

// stdinIsTerminal reports whether a human can answer the picker. A graph
// run started by an agent or the daemon has no one at stdin, and must
// print the list and stop rather than block forever on a read.
func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func graphRetry(args []string) {
	var runID, from string
	for i := 0; i < len(args); i++ {
		if args[i] == "--from" && i+1 < len(args) {
			from = args[i+1]
			i++
		} else if runID == "" {
			runID = args[i]
		}
	}
	if runID == "" || from == "" {
		fmt.Fprintln(os.Stderr, "Usage: muxcode graph retry <run-id> --from <node>")
		os.Exit(1)
	}
	res, err := bus.RetryGraphRun(bus.BusSession(), runID, from)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	for _, r := range res.Rearmed {
		fmt.Printf("Note: %q sits below satisfied human gate %q (approved %s) — the gate was re-armed and its stale approval purged; approve it again before downstream work fires\n",
			res.Requested, r.Gate, bus.FormatApprovalTime(r.ApprovedAt))
	}
	fmt.Printf("Run %s retrying from %s — the daemon resumes it on its next tick\n", runID, res.From)
}

// graphUI opens the interactive graph TUI (MUX-031), or prints a single
// frame with --render-once — the scriptable seam for integration tests.
// --width overrides the terminal width for deterministic frames in pipes.
func graphUI(args []string) {
	var renderOnce, launcher, gateQueue, promptSurface bool
	var width int
	var runID string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--render-once":
			renderOnce = true
		case args[i] == "--templates":
			launcher = true
		case args[i] == "--gates":
			gateQueue = true
		case args[i] == "--prompt":
			promptSurface = true
		case args[i] == "--width":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --width requires a value")
				os.Exit(1)
			}
			if _, err := fmt.Sscanf(args[i+1], "%d", &width); err != nil {
				fmt.Fprintf(os.Stderr, "Error: invalid --width %q\n", args[i+1])
				os.Exit(1)
			}
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q for graph ui\n", args[i])
			os.Exit(1)
		case runID == "":
			runID = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Error: unexpected argument %q\n", args[i])
			os.Exit(1)
		}
	}
	if launcher && renderOnce {
		fmt.Fprintln(os.Stderr, "Error: --templates has no --render-once form")
		os.Exit(1)
	}

	session := bus.BusSession()
	if renderOnce {
		var frame string
		var err error
		switch {
		case promptSurface:
			frame, err = tui.PromptRenderOnce(session, width)
		case gateQueue:
			frame, err = tui.GateQueueRenderOnce(session, width)
		default:
			frame, err = tui.GraphRenderOnce(session, runID, width)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		fmt.Print(frame)
		return
	}
	if promptSurface {
		tui.NewGraphPromptUI(session).Run()
		return
	}
	if launcher {
		tui.NewGraphLauncherUI(session).Run()
		return
	}
	if gateQueue {
		tui.NewGraphGatesUI(session).Run()
		return
	}
	tui.NewGraphUI(session, runID).Run()
}

// graphValidate loads the target as a file when it exists on disk,
// otherwise as a template name, then prints the validation report.
func graphValidate(target string) {
	var g *bus.Graph
	var source string
	var err error

	if _, statErr := os.Stat(target); statErr == nil {
		g, err = bus.LoadGraphFile(target)
		source = "file"
	} else {
		g, source, err = bus.ResolveGraphTemplate(target)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	v := g.Validate()
	fmt.Printf("Graph %q (%s, %d nodes, %d edges):\n", g.Name, source, len(g.Nodes), len(g.Edges))
	fmt.Print(v.Format())
	if !v.OK() {
		os.Exit(1)
	}
}

// graphCreate validates a composed definition and writes it as a
// template through WriteGraphDefinition — the Prompt surface's create
// intent lands here (MUX-109), which is why the JSON can arrive inline:
// the prompt-agent has no file tools, so the validating CLI is its only
// write path. A failing definition prints its validation report verbatim
// and writes nothing; the gate rule (commit/Atlassian behind wait_human)
// applies to composed graphs identically.
func graphCreate(args []string) {
	scope := bus.GraphScopeProject
	var jsonStr, path string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "--json":
			if i+1 >= len(args) {
				fmt.Fprintln(os.Stderr, "Error: --json requires a value")
				os.Exit(1)
			}
			jsonStr = args[i+1]
			i++
		case args[i] == "--scope":
			if i+1 >= len(args) || (args[i+1] != bus.GraphScopeProject && args[i+1] != bus.GraphScopeUser) {
				fmt.Fprintln(os.Stderr, "Error: --scope requires project or user")
				os.Exit(1)
			}
			scope = args[i+1]
			i++
		case strings.HasPrefix(args[i], "-"):
			fmt.Fprintf(os.Stderr, "Error: unknown flag %q for graph create\n", args[i])
			os.Exit(1)
		case path == "":
			path = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Error: unexpected argument %q\n", args[i])
			os.Exit(1)
		}
	}

	var g *bus.Graph
	var err error
	switch {
	case jsonStr != "" && path != "":
		fmt.Fprintln(os.Stderr, "Error: give either --json or a file, not both")
		os.Exit(1)
	case jsonStr != "":
		g, err = bus.ParseGraph([]byte(jsonStr))
	case path != "":
		g, err = bus.LoadGraphFile(path)
	default:
		fmt.Fprintln(os.Stderr, "Usage: muxcode graph create --json '<json>'|<file> [--scope project|user]")
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	written, v, err := bus.WriteGraphDefinition(g, scope)
	if err != nil {
		if v != nil && !v.OK() {
			fmt.Print(v.Format())
		}
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	for _, w := range v.Warnings {
		fmt.Printf("  WARN: %s\n", w)
	}
	fmt.Printf("Created %s graph %q at %s — launch it with: muxcode graph run %s\n", scope, g.Name, written, g.Name)
}

func graphList() {
	infos := bus.ListGraphTemplates()
	if len(infos) == 0 {
		fmt.Println("No graph templates found")
		return
	}
	fmt.Println("=== Graph Templates ===")
	for _, t := range infos {
		fmt.Printf("%-20s %-8s %s\n", t.Name, t.Source, t.Description)
	}
}

// graphStatus shows one run's node grid, or lists all runs when no id
// is given. --json emits the run, frozen graph, and node statuses as one
// JSON object for scripting.
func graphStatus(args []string) {
	session := bus.BusSession()

	var jsonOut bool
	var runID string
	for _, a := range args {
		if a == "--json" {
			jsonOut = true
		} else if runID == "" {
			runID = a
		}
	}

	if runID == "" {
		runs, err := bus.ListGraphRuns(session)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		if jsonOut {
			out, _ := json.MarshalIndent(runs, "", "  ")
			fmt.Println(string(out))
			return
		}
		if len(runs) == 0 {
			fmt.Println("No graph runs")
			return
		}
		fmt.Println("=== Graph Runs ===")
		for _, r := range runs {
			fmt.Printf("%-40s [%s]  template=%s\n", r.ID, r.State, r.Template)
		}
		return
	}

	run, err := bus.ReadGraphRun(session, runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: unknown run %q: %v\n", runID, err)
		os.Exit(1)
	}
	g, err := bus.ReadGraphRunGraph(session, runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	statuses, err := bus.ReadAllNodeStatuses(session, runID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if jsonOut {
		// Annotate condition branch-takers so machine consumers do not
		// repeat the red-failed mistake — see GraphNodeStatus.Branched.
		for i := range g.Nodes {
			n := &g.Nodes[i]
			if st := statuses[n.ID]; st != nil && bus.ConditionTookBranch(n.Type, st.State, st.Outcome) {
				st.Branched = true
			}
		}
		out, _ := json.MarshalIndent(map[string]any{
			"run":   run,
			"graph": g,
			"nodes": statuses,
		}, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Print(bus.FormatGraphRunColored(run, g, statuses))
}
