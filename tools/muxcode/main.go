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
	"init": true, "send": true, "inbox": true, "memory": true,
	"watch": true, "dashboard": true, "cleanup": true, "notify": true,
	"lock": true, "unlock": true, "is-locked": true, "tools": true,
	"chain": true, "log": true, "prompt": true, "skill": true,
	"context": true, "session": true, "cron": true, "status": true,
	"history": true, "guard": true, "proc": true, "spawn": true,
	"demo": true, "webhook": true, "subscribe": true, "agent": true,
	"api": true, "agent-health": true, "lifecycle": true, "console": true,
	"hook": true, "workflow": true, "pii-scrub": true, "atlassian": true,
	"compact": true, "launch": true, "modal": true,
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
  cleanup     Remove bus session directory
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
  agent       Run local LLM agent loop (run) or launch Claude Code agent (launch)
  api         Manage API collections, environments, and history
  agent-health  Manage agent health monitoring (stop, start, check)
  lifecycle     Persistent lifecycle logging (log, show, list, purge)
  console       Display role-specific status console (replaces log poller scripts)
  hook          Process Claude Code hook events (bash, guard, analyze, inbox-poll)
  workflow      Show or reset workflow state machine (--json, reset)
  atlassian     Jira and Confluence API operations (read, update, comment, search)
  modal         Manage modal windows (open, list, status)
  pii-scrub     Scrub PII and secrets from stdin (pipe filter)
  compact       Wait for agent idle, then inject /compact via tmux
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
	case "pii-scrub":
		cmd.Scrub(args)
	case "atlassian":
		cmd.Atlassian(args)
	case "modal":
		cmd.Modal(args)
	case "compact":
		cmd.Compact(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
