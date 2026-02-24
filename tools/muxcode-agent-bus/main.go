package main

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/cmd"
)

var usage = `Usage: muxcode-agent-bus <command> [args...]

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
  session     Session compaction and context management
  cron        Manage scheduled tasks (add, list, remove, enable, disable, history)
  status      Show all agents' current state (busy/idle/inbox/last-activity)
  history     Show recent messages to/from an agent
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}

	subcmd := os.Args[1]
	args := os.Args[2:]

	switch subcmd {
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
	case "session":
		cmd.Session(args)
	case "cron":
		cmd.Cron(args)
	case "status":
		cmd.Status(args)
	case "history":
		cmd.History(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", subcmd)
		fmt.Fprint(os.Stderr, usage)
		os.Exit(1)
	}
}
