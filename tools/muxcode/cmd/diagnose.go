package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Diagnose handles the "muxcode diagnose" subcommand.
// Usage: muxcode diagnose <role> [--json]
//
//	muxcode diagnose --all [--json]
func Diagnose(args []string) {
	session := bus.BusSession()

	jsonOutput := false
	allRoles := false
	var role string

	for _, a := range args {
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
		diagnoseAll(session, jsonOutput)
		return
	}

	if role == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode diagnose <role> [--json]\n")
		fmt.Fprintf(os.Stderr, "       muxcode diagnose --all [--json]\n")
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
		fmt.Print(bus.FormatDiagnosticReport(&report))
	}

	// Exit non-zero if critical findings
	for _, f := range report.Findings {
		if f.Severity == "critical" {
			os.Exit(1)
		}
	}
}

// diagnoseAll runs diagnosis for all diagnosable roles and prints a summary.
func diagnoseAll(session string, jsonOutput bool) {
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

	fmt.Println()
	fmt.Println("  Agent Diagnostics")
	fmt.Println("  " + "─────────────────────────────────────────────────────────────")

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
