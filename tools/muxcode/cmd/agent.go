package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Agent handles the "muxcode agent" subcommand.
func Agent(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent <run|launch|config|status|definition> [flags]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "run":
		agentRun(subArgs)
	case "launch":
		Launch(subArgs)
	case "status":
		agentStatus(subArgs)
	case "config":
		agentConfig(subArgs)
	case "definition":
		agentDefinition(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown agent subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent <run|launch|config|status|definition> [flags]\n")
		os.Exit(1)
	}
}

// definitionRole accepts the roles the probe can attribute a pane to: a known
// window role or a spawn worker. Anything else is rejected up front, because
// the probe would otherwise answer a typo with "unknown" and that reads like
// a verdict about a real agent.
func definitionRole(role string) bool {
	return bus.IsKnownRole(role) || bus.IsSpawnRole(role)
}

// agentDefinition prints the live definition probe for a role — present,
// missing, or unknown (MUX-136): does the claude process in the role's pane
// carry `--agent` and `--agents`? The same positive check the daemon's
// definition watchdog runs, exposed for humans and the integration test.
// Exit 0 present, 1 missing, 2 unknown.
func agentDefinition(args []string) {
	if len(args) < 1 || !definitionRole(args[0]) {
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent definition <role>  (a known role or spawn-<id>)\n")
		os.Exit(2)
	}
	verdict := bus.ProbeAgentDefinition(bus.BusSession(), args[0])
	fmt.Println(verdict)
	switch verdict {
	case bus.DefinitionPresent:
		os.Exit(0)
	case bus.DefinitionMissing:
		os.Exit(1)
	default:
		os.Exit(2)
	}
}

// agentConfig generates the agent config file for a role.
// Used by integration tests to verify config generation without a running session.
func agentConfig(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode agent config <role>\n")
		os.Exit(1)
	}
	role := args[0]

	// Load shell-sourceable config
	bus.LoadShellConfig("")

	provider := bus.ResolveProvider(role)
	if err := provider.WriteAgentConfig(role); err != nil {
		fmt.Fprintf(os.Stderr, "Error: WriteAgentConfig(%s): %v\n", role, err)
		os.Exit(1)
	}
	fmt.Printf("Generated agent config for %s\n", role)
}

// agentStatus prints the autonomous agent's current status to stdout.
func agentStatus(args []string) {
	session := bus.BusSession()
	status := bus.ReadAutonomousAgentStatus(session)
	fmt.Print(bus.FormatAutonomousAgentStatus(status))
}

// agentRun launches the local LLM agent loop for a role.
func agentRun(args []string) {
	role := ""
	model := ""
	url := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--model":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --model requires a value\n")
				os.Exit(1)
			}
			i++
			model = args[i]
		case "--url":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --url requires a value\n")
				os.Exit(1)
			}
			i++
			url = args[i]
		default:
			if role == "" && args[i][0] != '-' {
				role = args[i]
			} else {
				fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
				os.Exit(1)
			}
		}
	}

	if role == "" {
		role = bus.BusRole()
	}
	if role == "" || role == "unknown" {
		fmt.Fprintf(os.Stderr, "Error: role is required (provide as argument or set AGENT_ROLE env)\n")
		os.Exit(1)
	}

	session := bus.BusSession()

	// Build Ollama config with overrides.
	// Resolution: --model flag → MUXCODE_{ROLE}_MODEL → MUXCODE_OLLAMA_MODEL → default
	ollamaCfg := bus.DefaultOllamaConfig()
	if model != "" {
		ollamaCfg.Model = model
	} else {
		ollamaCfg.Model = bus.RoleModel(role)
	}
	if url != "" {
		ollamaCfg.BaseURL = url
	}

	// BusRole is the bus identity (window name) — used for inbox, lock, send.
	// Role is the agent definition role — used for tools, skills, agent def.
	// They differ when the role map remaps window names (e.g. commit→git).
	busRole := bus.BusRole()
	if busRole == "" || busRole == "unknown" {
		busRole = role
	}

	cfg := bus.AgentConfig{
		Role:    role,
		BusRole: busRole,
		Session: session,
		Ollama:  ollamaCfg,
	}

	if busRole != role {
		fmt.Fprintf(os.Stderr, "[agent] Starting local LLM agent: role=%q, bus=%q (model: %s)\n", role, busRole, ollamaCfg.Model)
	} else {
		fmt.Fprintf(os.Stderr, "[agent] Starting local LLM agent for role %q (model: %s)\n", role, ollamaCfg.Model)
	}

	// Set up signal handling for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintf(os.Stderr, "\n[agent] Shutting down...\n")
		cancel()
	}()

	if err := bus.AgentLoop(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
