package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mkober/muxcode/tools/muxcode/cmd"
)

// knownSubcommands lists all valid subcommands for disambiguation.
// When invoked as "muxcode", any arg that isn't a known subcommand
// is treated as a project path (routes to the launcher).
var knownSubcommands = map[string]bool{
	"init": true, "send": true, "inbox": true, "memory": true, "tasks": true, "plugin": true, "model": true, "diagnose": true,
	"watch": true, "dashboard": true, "cleanup": true, "notify": true,
	"lock": true, "unlock": true, "is-locked": true, "tools": true,
	"chain": true, "log": true, "prompt": true, "skill": true,
	"context": true, "session": true, "cron": true, "status": true, "uitest": true,
	"history": true, "guard": true, "proc": true, "spawn": true,
	"demo": true, "webhook": true, "subscribe": true, "agent": true,
	"api": true, "agent-health": true, "lifecycle": true, "console": true,
	"hook": true, "workflow": true, "pii-scrub": true, "atlassian": true,
	"compact": true, "launch": true, "modal": true, "mode": true,
	"reload": true, "config": true, "provider-select": true,
	"simulate": true, "track": true, "remote": true, "spec": true,
}

var usage = `Usage: muxcode <command> [args...]

Launcher:
  muxcode                   Interactive project picker → launch tmux session
  muxcode <path>            Launch session for project directory
  muxcode <path> <name>     Launch with custom session name
  muxcode launch [args]     Explicit launch subcommand

Commands:
  init        Initialize bus directories and memory
  send        Send a message to an agent
  inbox       Read messages from your inbox
  memory      Read/write persistent agent memory
  watch       Watch for file changes and route events
  dashboard   Launch the agent dashboard TUI
  cleanup     Remove stale session temp files (--dry-run, --all)
  notify      Send tmux notification to an agent
  lock        Set agent lock (busy indicator)
  unlock      Remove agent lock
  is-locked   Check if agent is locked
  tools       List allowed tools for a role
  chain       Execute an event chain action
  log         Append an entry to a role's history log
  prompt      Output shared agent coordination prompt for a role
  skill       Manage reusable instruction skills/plugins
  context     Manage per-agent drop-in context files
  session     Session compaction and context management
  cron        Manage scheduled tasks (add, list, remove, enable, disable, history)
  status      Show all agents' current state (busy/idle/inbox/last-activity)
  history     Show recent messages to/from an agent
  guard       Check for agent loop patterns (command retries, message ping-pong)
  proc        Manage background processes (start, list, status, log, stop, clean)
  spawn       Manage spawned agent sessions (start, list, status, result, stop, clean)
  demo        Run scripted demo scenarios (run, list)
  webhook     Manage webhook HTTP endpoint (start, stop, status)
  subscribe   Manage event subscriptions (add, list, remove, enable, disable)
  agent       Run local LLM agent loop (run), launch agent (launch), or generate config (config)
  api         Manage API collections, environments, and history
  agent-health  Manage agent health monitoring (stop, start, check)
  lifecycle     Persistent lifecycle logging (log, show, list, purge)
  console       Display role-specific status console (replaces log poller scripts)
  hook          Process Claude Code hook events (bash, guard, analyze, inbox-poll)
  workflow      Show or reset workflow state machine (--json, reset)
  atlassian     Jira and Confluence API operations (read, update, comment, search)
  modal         Manage modal windows (open, list, status)
  mode          Cycle between agent modes on a window (cycle, status, switch, list)
  resize        Resize every window in every session to fit the connected client
  deliver       Force-deliver an agent's pending inbox into its pane (--force)
  uitest        Run integration tests in a live tmux session (--list, --verbose)
  tasks         List delegated tasks tracked via --wait (--all, --status)
  track         Show delivery status for a message ID
  pii-scrub     Scrub PII and secrets from stdin (pipe filter)
  reload        Stop an agent, reconfigure, and relaunch (hot reload)
  config        View or change agent CLI/model configuration (set, get, list)
  plugin        Manage LLM provider plugins (list, add, remove, sync)
  model         Manage provider models for hot reload (list, add, remove, default)
  provider-select  Interactive provider/model selector TUI (used by modal)
  compact       Wait for agent idle, then inject /compact via tmux
  diagnose      Diagnose why an agent isn't responding (evidence + root cause)
  remote        Investigate agents in other muxcode sessions (TUI browser, or list/status/capture/inbox/diagnose)
  spec          Manage the active requirements spec for plan agent verification (set, get, clear)
`

func main() {
	base := filepath.Base(os.Args[0])

	// When invoked as "muxcode" (not via "muxcode <subcommand>"), route to launcher
	// unless the first arg is a known subcommand.
	if base == "muxcode" {
		if len(os.Args) < 2 {
			// Bare "muxcode" → interactive project picker
			cmd.RunLauncher(nil)
			return
		}
		if !knownSubcommands[os.Args[1]] {
			// "muxcode <path> [<name>]" → launch with path
			cmd.RunLauncher(os.Args[1:])
			return
		}
	}

	// Standard subcommand dispatch
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	switch subcmd {
	case "launch":
		cmd.RunLauncher(args)
	case "init":
		cmd.Init(args)
	case "send":
		cmd.Send(args)
	case "inbox":
		cmd.Inbox(args)
	case "memory":
		cmd.Memory(args)
	case "watch":
		cmd.Watch(args)
	case "dashboard":
		cmd.Dashboard(args)
	case "cleanup":
		cmd.Cleanup(args)
	case "notify":
		cmd.Notify(args)
	case "lock":
		cmd.Lock(args)
	case "unlock":
		cmd.Unlock(args)
	case "is-locked":
		cmd.IsLocked(args)
	case "tools":
		cmd.Tools(args)
	case "chain":
		cmd.Chain(args)
	case "log":
		cmd.Log(args)
	case "prompt":
		cmd.Prompt(args)
	case "skill":
		cmd.Skill(args)
	case "context":
		cmd.Context(args)
	case "session":
		cmd.Session(args)
	case "cron":
		cmd.Cron(args)
	case "status":
		cmd.Status(args)
	case "history":
		cmd.History(args)
	case "guard":
		cmd.Guard(args)
	case "proc":
		cmd.Proc(args)
	case "spawn":
		cmd.Spawn(args)
	case "demo":
		cmd.Demo(args)
	case "webhook":
		cmd.Webhook(args)
	case "subscribe":
		cmd.Subscribe(args)
	case "agent":
		cmd.Agent(args)
	case "api":
		cmd.Api(args)
	case "agent-health":
		cmd.AgentHealth(args)
	case "lifecycle":
		cmd.Lifecycle(args)
	case "console":
		cmd.Console(args)
	case "hook":
		cmd.Hook(args)
	case "workflow":
		cmd.Workflow(args)
	case "reload":
		cmd.Reload(args)
	case "config":
		cmd.Config(args)
	case "plugin":
		cmd.Plugin(args)
	case "model":
		cmd.Model(args)
	case "provider-select":
		cmd.ProviderSelect(args)
	case "pii-scrub":
		cmd.Scrub(args)
	case "atlassian":
		cmd.Atlassian(args)
	case "modal":
		cmd.Modal(args)
	case "mode":
		cmd.Mode(args)
	case "resize":
		cmd.Resize(args)
	case "deliver":
		cmd.Deliver(args)
	case "uitest":
		cmd.UITest(args)
	case "simulate":
		cmd.Simulate(args)
	case "compact":
		cmd.Compact(args)
	case "tasks":
		cmd.Tasks(args)
	case "track":
		cmd.Track(args)
	case "diagnose":
		cmd.Diagnose(args)
	case "remote":
		cmd.Remote(args)
	case "spec":
		cmd.Spec(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
