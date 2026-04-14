package cmd

import (
	"fmt"
	"os"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

var atlassianUsage = `Usage: muxcode atlassian <service> <action> [args...]

Services:
  jira        Jira issue operations
  confluence  Confluence page operations

Jira actions:
  read <ISSUE-KEY>                        Read issue details, links, and description
  update <ISSUE-KEY> <ADF-JSON-FILE>      Update issue description
  comment <ISSUE-KEY> <ADF-JSON-FILE>     Post a comment on an issue
  comments <ISSUE-KEY>                    Read comments on an issue
  link-types                              List available issue link types
  link <TYPE> <SOURCE-KEY> <TARGET-KEY>   Link two issues (source -[type]-> target)
  transitions <ISSUE-KEY>                 List available workflow transitions
  transition <ISSUE-KEY> <TRANSITION-ID>  Transition issue to a new status
  search <JQL-QUERY>                      Search issues using JQL
  create-subtask <PARENT-KEY> <SUMMARY> [PROJECT-KEY]  Create a subtask

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
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira read <ISSUE-KEY>")
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
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira update <ISSUE-KEY> <ADF-JSON-FILE>")
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
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira comment <ISSUE-KEY> <ADF-JSON-FILE>")
			os.Exit(1)
		}
		result, err := bus.JiraComment(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "comments":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira comments <ISSUE-KEY>")
			os.Exit(1)
		}
		comments, err := bus.JiraReadComments(cfg, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(bus.FormatJiraComments(args[0], comments))

	case "link-types":
		types, err := bus.JiraListLinkTypes(cfg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(bus.FormatLinkTypes(types))

	case "link":
		if len(args) < 3 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira link <TYPE> <SOURCE-KEY> <TARGET-KEY>")
			os.Exit(1)
		}
		// SOURCE performs the outward action on TARGET (inward).
		// e.g. link "Blocks" A B → A blocks B (A=outward, B=inward)
		result, err := bus.JiraLinkIssues(cfg, args[0], args[2], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "transitions":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira transitions <ISSUE-KEY>")
			os.Exit(1)
		}
		transitions, err := bus.JiraListTransitions(cfg, args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(bus.FormatTransitions(args[0], transitions))

	case "transition":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira transition <ISSUE-KEY> <TRANSITION-ID>")
			os.Exit(1)
		}
		result, err := bus.JiraTransitionIssue(cfg, args[0], args[1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	case "search":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira search <JQL-QUERY>")
			os.Exit(1)
		}
		issues, total, err := bus.JiraSearch(cfg, args[0], 50)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(bus.FormatJiraSearch(issues, total, args[0]))

	case "create-subtask":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian jira create-subtask <PARENT-KEY> <SUMMARY> [PROJECT-KEY]")
			os.Exit(1)
		}
		projectKey := ""
		if len(args) >= 3 {
			projectKey = args[2]
		}
		result, err := bus.JiraCreateSubtask(cfg, args[0], projectKey, args[1], nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(result)

	default:
		fmt.Fprintf(os.Stderr, "Unknown jira action: %s\n", action)
		fmt.Fprintln(os.Stderr, "Actions: read, update, comment, comments, link-types, link, transitions, transition, search, create-subtask")
		os.Exit(1)
	}
}

func atlassianConfluence(cfg *bus.AtlassianConfig, action string, args []string) {
	switch action {
	case "read":
		if len(args) < 1 {
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian confluence read <PAGE-ID>")
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
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian confluence update <PAGE-ID> <ADF-JSON-FILE>")
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
			fmt.Fprintln(os.Stderr, "Usage: muxcode atlassian confluence search <SPACE-KEY> <CQL-QUERY>")
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
