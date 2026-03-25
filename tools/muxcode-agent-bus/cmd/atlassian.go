package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

var atlassianUsage = `Usage: muxcode-agent-bus atlassian <service> <action> [args...]

Services:
  jira        Jira issue operations
  confluence  Confluence page operations

Jira actions:
  read <ISSUE-KEY>                      Read issue details and description
  update <ISSUE-KEY> <ADF-JSON-FILE>    Update issue description
  comment <ISSUE-KEY> <ADF-JSON-FILE>   Post a comment on an issue

Confluence actions:
  read <PAGE-ID>                        Read page content
  update <PAGE-ID> <ADF-JSON-FILE>      Update page content
  search <SPACE-KEY> <CQL-QUERY>        Search pages using CQL
`

// Atlassian dispatches Jira and Confluence API commands.
func Atlassian(args []string) {
	if len(args) < 2 {
		fmt.Fprint(os.Stderr, atlassianUsage)
		os.Exit(1)
	}

	cfg, err := bus.LoadAtlassianConfig()
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
		os.Exit(1)
	}

	service := args[0]
	action := args[1]
	rest := args[2:]

	switch service {
	case "jira":
		atlassianJira(cfg, action, rest)
	case "confluence":
		atlassianConfluence(cfg, action, rest)
	default:
		fmt.Fprintf(os.Stderr, "Unknown service: %s\n\n", service)
		fmt.Fprint(os.Stderr, atlassianUsage)
		os.Exit(1)
	}
}

func atlassianJira(cfg *bus.AtlassianConfig, action string, args []string) {
	switch action {
	case "read":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian jira read <ISSUE-KEY>")
			os.Exit(1)
		}
		result, err := bus.JiraRead(cfg, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "update":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian jira update <ISSUE-KEY> <ADF-JSON-FILE>")
			os.Exit(1)
		}
		result, err := bus.JiraUpdate(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "comment":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian jira comment <ISSUE-KEY> <ADF-JSON-FILE>")
			os.Exit(1)
		}
		result, err := bus.JiraComment(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	default:
		fmt.Fprintf(os.Stderr, "Unknown jira action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Actions: read, update, comment")
		os.Exit(1)
	}
}

func atlassianConfluence(cfg *bus.AtlassianConfig, action string, args []string) {
	switch action {
	case "read":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian confluence read <PAGE-ID>")
			os.Exit(1)
		}
		result, err := bus.ConfluenceRead(cfg, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "update":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian confluence update <PAGE-ID> <ADF-JSON-FILE>")
			os.Exit(1)
		}
		result, err := bus.ConfluenceUpdate(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "search":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode-agent-bus atlassian confluence search <SPACE-KEY> <CQL-QUERY>")
			os.Exit(1)
		}
		result, err := bus.ConfluenceSearch(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	default:
		fmt.Fprintf(os.Stderr, "Unknown confluence action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Actions: read, update, search")
		os.Exit(1)
	}
}
