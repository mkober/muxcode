package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode/bus"
	"github.com/mkober/muxcode/tools/muxcode/tui"
)

// Remote handles the "muxcode remote" subcommand.
//
// Usage:
//
//	muxcode remote list                           List active muxcode sessions
//	muxcode remote status <session>               Agent status overview for a session
//	muxcode remote capture <session> <role> [N]   Capture last N lines from agent pane
//	muxcode remote inbox <session> [role]          Read agent inbox(es) in a session
//	muxcode remote log <session> <role> [N]        Show last N messages for a role
//	muxcode remote diagnose <session> <role>       Run diagnostics on a remote agent
//	muxcode remote diagnose <session> --all        Diagnose all agents in a session
func Remote(args []string) {
	if len(args) < 1 {
		// No subcommand — launch interactive TUI
		session := bus.BusSession()
		remote := tui.NewRemoteUI(session)
		sel := remote.Run()
		if sel != nil {
			executeRemoteSelection(session, sel)
		}
		return
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "list", "ls":
		remoteList(subArgs)
	case "status":
		remoteStatus(subArgs)
	case "capture", "cap":
		remoteCapture(subArgs)
	case "inbox":
		remoteInbox(subArgs)
	case "log":
		remoteLog(subArgs)
	case "diagnose", "diag":
		remoteDiagnose(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown remote subcommand: %s\n", subcmd)
		printRemoteUsage()
		os.Exit(1)
	}
}

func printRemoteUsage() {
	fmt.Fprintf(os.Stderr, `Usage: muxcode remote [command] [args...]

  muxcode remote                       Launch interactive TUI (session browser)

Commands:
  list                             List all muxcode sessions
  status <session>                 Agent status overview for a remote session
  capture <session> <role> [N]     Capture last N lines from an agent's tmux pane (default: 30)
  inbox <session> [role]           Read agent inbox(es) — all roles if role omitted
  log <session> <role> [N]         Show last N messages for a role (default: 20)
  diagnose <session> <role>        Run diagnostics on a remote agent
  diagnose <session> --all         Diagnose all agents in a remote session
`)
}

func remoteList(args []string) {
	currentSession := bus.BusSession()
	sessions, err := bus.DiscoverSessions("", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering sessions: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatSessionList(sessions, currentSession))
}

func remoteStatus(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote status <session>\n")
		os.Exit(1)
	}
	session := resolveRemoteSession(args[0])
	fmt.Print(bus.RemoteOverview(session))
}

func remoteCapture(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote capture <session> <role> [lines]\n")
		os.Exit(1)
	}

	session := resolveRemoteSession(args[0])
	role := args[1]

	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
		os.Exit(1)
	}

	lines := 30
	if len(args) > 2 {
		n := 0
		if _, err := fmt.Sscanf(args[2], "%d", &n); err == nil && n > 0 {
			lines = n
		}
	}

	if !bus.TmuxHasSession(session) {
		fmt.Fprintf(os.Stderr, "Error: tmux session %q not found (session is dead — capture requires a live tmux session)\n", session)
		os.Exit(1)
	}

	output, err := bus.RemoteAgentCapture(session, role, lines)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error capturing pane for %s in %s: %v\n", role, session, err)
		os.Exit(1)
	}

	idle := "active"
	if bus.RemoteAgentIsIdle(session, role) {
		idle = "idle"
	}

	fmt.Printf("=== %s:%s (last %d lines, %s) ===\n", session, role, lines, idle)
	fmt.Println(output)
}

func remoteInbox(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote inbox <session> [role]\n")
		os.Exit(1)
	}

	session := resolveRemoteSession(args[0])

	if len(args) > 1 {
		// Single role
		role := args[1]
		if !bus.IsKnownRole(role) {
			fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
			os.Exit(1)
		}
		summary := bus.GetRemoteInbox(session, role)
		fmt.Print(bus.FormatRemoteInbox(summary))
		return
	}

	// All roles
	fmt.Printf("\n  Inboxes for session: %s\n", session)
	fmt.Println("  " + strings.Repeat("─", 60))

	any := false
	for _, role := range bus.KnownRoles {
		summary := bus.GetRemoteInbox(session, role)
		if summary.Count > 0 {
			fmt.Print(bus.FormatRemoteInbox(summary))
			any = true
		}
	}
	if !any {
		fmt.Println("  All inboxes empty")
	}
	fmt.Println()
}

func remoteLog(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote log <session> <role> [N]\n")
		os.Exit(1)
	}

	session := resolveRemoteSession(args[0])
	role := args[1]

	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
		os.Exit(1)
	}

	limit := 20
	if len(args) > 2 {
		n := 0
		if _, err := fmt.Sscanf(args[2], "%d", &n); err == nil && n > 0 {
			limit = n
		}
	}

	msgs := bus.ReadLogHistory(session, role, limit)
	if len(msgs) == 0 {
		fmt.Printf("No messages found for %s in session %s\n", role, session)
		return
	}

	fmt.Print(bus.FormatHistory(msgs, role))
}

func remoteDiagnose(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote diagnose <session> <role>\n")
		fmt.Fprintf(os.Stderr, "       muxcode remote diagnose <session> --all\n")
		os.Exit(1)
	}

	session := resolveRemoteSession(args[0])

	jsonOutput := false
	allRoles := false
	var role string

	for _, a := range args[1:] {
		switch a {
		case "--json":
			jsonOutput = true
		case "--all":
			allRoles = true
		default:
			if role == "" {
				role = a
			}
		}
	}

	if allRoles {
		remoteDiagnoseAll(session, jsonOutput)
		return
	}

	if role == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode remote diagnose <session> <role> [--json]\n")
		os.Exit(1)
	}

	if !bus.IsKnownRole(role) {
		fmt.Fprintf(os.Stderr, "Unknown role: %s\n", role)
		os.Exit(1)
	}

	report := bus.CollectEvidence(session, role)
	report.Timeline = bus.BuildTimeline(session, role, 20)
	bus.RunDiagnostics(&report)

	if jsonOutput {
		fmt.Println(bus.FormatDiagnosticJSON(&report))
	} else {
		fmt.Printf("  Remote session: %s\n\n", session)
		fmt.Print(bus.FormatDiagnosticReport(&report))
	}

	for _, f := range report.Findings {
		if f.Severity == "critical" {
			os.Exit(1)
		}
	}
}

func remoteDiagnoseAll(session string, jsonOutput bool) {
	roles := bus.DiagnosableRoles()

	if jsonOutput {
		var reports []bus.DiagnosticReport
		for _, role := range roles {
			report := bus.CollectEvidence(session, role)
			report.Timeline = bus.BuildTimeline(session, role, 10)
			bus.RunDiagnostics(&report)
			reports = append(reports, report)
		}
		data, _ := json.MarshalIndent(reports, "", "  ")
		fmt.Println(string(data))
		return
	}

	fmt.Printf("\n  Remote Session: %s\n", session)
	fmt.Println("  " + strings.Repeat("─", 60))

	hasCritical := false
	for _, role := range roles {
		report := bus.CollectEvidence(session, role)
		report.Timeline = bus.BuildTimeline(session, role, 10)
		bus.RunDiagnostics(&report)
		fmt.Println(bus.FormatDiagnosticSummary(&report))
		for _, f := range report.Findings {
			if f.Severity == "critical" {
				hasCritical = true
			}
		}
	}
	fmt.Println()

	if hasCritical {
		os.Exit(1)
	}
}

// resolveRemoteSession resolves a session name, supporting prefix matching.
// If the given name matches exactly, use it. Otherwise, try to find a unique
// prefix match among active bus directories.
func resolveRemoteSession(input string) string {
	// Exact match — return as-is
	busDir := bus.BusDir(input)
	if _, err := os.Stat(busDir); err == nil {
		return input
	}

	// Try prefix match
	sessions, err := bus.DiscoverSessions("", false)
	if err != nil {
		return input
	}

	var matches []string
	for _, s := range sessions {
		if strings.HasPrefix(s.Name, input) {
			matches = append(matches, s.Name)
		}
	}

	switch len(matches) {
	case 0:
		fmt.Fprintf(os.Stderr, "No session found matching %q\n", input)
		fmt.Fprintf(os.Stderr, "Run 'muxcode remote list' to see available sessions\n")
		os.Exit(1)
	case 1:
		return matches[0]
	default:
		fmt.Fprintf(os.Stderr, "Ambiguous session prefix %q matches: %s\n",
			input, strings.Join(matches, ", "))
		os.Exit(1)
	}

	return input // unreachable
}

// executeRemoteSelection runs the selected action and sends results to the edit agent.
func executeRemoteSelection(currentSession string, sel *tui.RemoteSelection) {
	target := sel.Session
	if sel.Role != "" {
		target += ":" + sel.Role
	}

	fmt.Printf("\n  Running %s on %s...\n", sel.Action, target)

	var result string

	switch sel.Action {
	case tui.ActionCapture:
		result = executeCapture(sel.Session, sel.Role)
	case tui.ActionInbox:
		result = executeInbox(sel.Session, sel.Role)
	case tui.ActionDiagnose:
		result = executeDiagnose(sel.Session, sel.Role)
	case tui.ActionAllInboxes:
		result = executeAllInboxes(sel.Session)
	case tui.ActionDiagnoseAll:
		result = executeDiagnoseAll(sel.Session)
	}

	if result == "" {
		fmt.Println("  No results")
		return
	}

	// Send results to the edit agent
	payload := fmt.Sprintf("Remote investigation: %s %s:%s\n%s", sel.Action, sel.Session, sel.Role, result)
	// Truncate payload for bus message (keep under 4KB to avoid oversized inbox entries)
	if len(payload) > 4000 {
		payload = payload[:3997] + "..."
	}
	m := bus.NewMessage("daemon", "edit", "request", "remote-investigate", payload, "")
	if err := bus.Send(currentSession, m); err != nil {
		fmt.Fprintf(os.Stderr, "  Error sending to edit: %v\n", err)
		return
	}

	// Wake the edit agent to process the message
	_ = bus.Notify(currentSession, "edit")

	fmt.Printf("  Sent results to edit agent (%d bytes)\n", len(result))
}

func executeCapture(session, role string) string {
	if !bus.TmuxHasSession(session) {
		return fmt.Sprintf("Session %q is dead — pane capture requires a live tmux session", session)
	}
	output, err := bus.RemoteAgentCapture(session, role, 40)
	if err != nil {
		return fmt.Sprintf("Error capturing %s: %v", role, err)
	}
	idle := "active"
	if bus.RemoteAgentIsIdle(session, role) {
		idle = "idle"
	}
	return fmt.Sprintf("Agent %s is %s. Pane capture:\n%s", role, idle, output)
}

func executeInbox(session, role string) string {
	summary := bus.GetRemoteInbox(session, role)
	return bus.FormatRemoteInbox(summary)
}

func executeDiagnose(session, role string) string {
	report := bus.CollectEvidence(session, role)
	report.Timeline = bus.BuildTimeline(session, role, 20)
	bus.RunDiagnostics(&report)

	var b strings.Builder
	// Compact summary for bus message
	health := "dead"
	if report.AgentState.IsAlive {
		health = "alive"
	} else if report.AgentState.IsStopped {
		health = "stopped"
	}
	b.WriteString(fmt.Sprintf("Agent: %s  Health: %s  Inbox: %d msgs (%d actionable)\n",
		role, health, report.InboxState.MessageCount, report.InboxState.ActionableCount))

	if report.AgentState.IsIdle {
		b.WriteString("State: idle\n")
	} else {
		b.WriteString("State: active\n")
	}

	if len(report.Findings) == 0 {
		b.WriteString("Findings: none — agent appears healthy\n")
	} else {
		b.WriteString("Findings:\n")
		for _, f := range report.Findings {
			b.WriteString(fmt.Sprintf("  [%s] %s: %s\n", f.Severity, f.FailureMode, f.Summary))
			if len(f.Remediation) > 0 {
				b.WriteString(fmt.Sprintf("    Fix: %s\n", f.Remediation[0]))
			}
		}
	}
	return b.String()
}

func executeAllInboxes(session string) string {
	var b strings.Builder
	any := false
	for _, role := range bus.KnownRoles {
		summary := bus.GetRemoteInbox(session, role)
		if summary.Count > 0 {
			b.WriteString(bus.FormatRemoteInbox(summary))
			any = true
		}
	}
	if !any {
		b.WriteString("All inboxes empty")
	}
	return b.String()
}

func executeDiagnoseAll(session string) string {
	var b strings.Builder
	for _, role := range bus.DiagnosableRoles() {
		report := bus.CollectEvidence(session, role)
		report.Timeline = bus.BuildTimeline(session, role, 10)
		bus.RunDiagnostics(&report)

		marker := "✓"
		agentHealth := "alive"
		if !report.AgentState.IsAlive {
			marker = "✗"
			agentHealth = "dead"
		}
		if report.AgentState.IsStopped {
			agentHealth = "stopped"
		}
		findingCount := len(report.Findings)
		critCount := 0
		for _, f := range report.Findings {
			if f.Severity == "critical" {
				critCount++
			}
		}

		b.WriteString(fmt.Sprintf("  %s %-12s health:%-8s inbox:%-3d findings:%d",
			marker, role, agentHealth, report.InboxState.MessageCount, findingCount))
		if critCount > 0 {
			b.WriteString(fmt.Sprintf(" (%d critical)", critCount))
		}
		b.WriteByte('\n')
	}
	return b.String()
}
