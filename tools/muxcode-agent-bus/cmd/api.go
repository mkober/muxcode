package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Api handles the "muxcode-agent-bus api" subcommand.
func Api(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api <env|collection|history|import> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "env":
		apiEnv(subArgs)
	case "collection":
		apiCollection(subArgs)
	case "history":
		apiHistory(subArgs)
	case "import":
		apiImport(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown api subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api <env|collection|history|import> [args...]\n")
		os.Exit(1)
	}
}

// --- Environment sub-handlers ---

func apiEnv(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env <list|get|create|set|delete> [args...]\n")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		apiEnvList()
	case "get":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env get <name>\n")
			os.Exit(1)
		}
		apiEnvGet(args[1])
	case "create":
		apiEnvCreate(args[1:])
	case "set":
		if len(args) < 4 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env set <name> <key> <value>\n")
			os.Exit(1)
		}
		apiEnvSet(args[1], args[2], strings.Join(args[3:], " "))
	case "delete":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env delete <name>\n")
			os.Exit(1)
		}
		apiEnvDelete(args[1])
	default:
		fmt.Fprintf(os.Stderr, "Unknown env subcommand: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env <list|get|create|set|delete> [args...]\n")
		os.Exit(1)
	}
}

func apiEnvList() {
	envs, err := bus.ListEnvironments()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing environments: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatEnvList(envs))
}

func apiEnvGet(name string) {
	env, err := bus.ReadEnvironment(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading environment %q: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatEnvDetail(env))
}

func apiEnvCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api env create <name> --base-url <url>\n")
		os.Exit(1)
	}

	name := args[0]
	baseURL := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--base-url":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --base-url requires a value\n")
				os.Exit(1)
			}
			i++
			baseURL = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	err := bus.CreateEnvironment(bus.Environment{
		Name:    name,
		BaseURL: baseURL,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating environment: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created environment: %s\n", name)
	if baseURL != "" {
		fmt.Printf("  Base URL: %s\n", baseURL)
	}
}

func apiEnvSet(name, key, value string) {
	err := bus.SetEnvironmentVar(name, key, value)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error setting variable: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Set %s = %s on environment %s\n", key, value, name)
}

func apiEnvDelete(name string) {
	err := bus.DeleteEnvironment(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting environment: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted environment: %s\n", name)
}

// --- Collection sub-handlers ---

func apiCollection(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection <list|get|create|delete|add-request|remove-request> [args...]\n")
		os.Exit(1)
	}

	switch args[0] {
	case "list":
		apiCollectionList()
	case "get":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection get <name>\n")
			os.Exit(1)
		}
		apiCollectionGet(args[1])
	case "create":
		apiCollectionCreate(args[1:])
	case "delete":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection delete <name>\n")
			os.Exit(1)
		}
		apiCollectionDelete(args[1])
	case "add-request":
		apiCollectionAddRequest(args[1:])
	case "remove-request":
		if len(args) < 3 {
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection remove-request <collection> <name>\n")
			os.Exit(1)
		}
		apiCollectionRemoveRequest(args[1], args[2])
	default:
		fmt.Fprintf(os.Stderr, "Unknown collection subcommand: %s\n", args[0])
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection <list|get|create|delete|add-request|remove-request> [args...]\n")
		os.Exit(1)
	}
}

func apiCollectionList() {
	cols, err := bus.ListCollections()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing collections: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatCollectionList(cols))
}

func apiCollectionGet(name string) {
	col, err := bus.ReadCollection(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading collection %q: %v\n", name, err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatCollectionDetail(col))
}

func apiCollectionCreate(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection create <name> [--description desc] [--base-url url]\n")
		os.Exit(1)
	}

	name := args[0]
	desc := ""
	baseURL := ""

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--description":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --description requires a value\n")
				os.Exit(1)
			}
			i++
			desc = args[i]
		case "--base-url":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --base-url requires a value\n")
				os.Exit(1)
			}
			i++
			baseURL = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	err := bus.CreateCollection(bus.Collection{
		Name:        name,
		Description: desc,
		BaseURL:     baseURL,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating collection: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Created collection: %s\n", name)
}

func apiCollectionDelete(name string) {
	err := bus.DeleteCollection(name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error deleting collection: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted collection: %s\n", name)
}

func apiCollectionAddRequest(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api collection add-request <collection> <name> --method METHOD --path PATH [--header key:value] [--body json] [--query key=value]\n")
		os.Exit(1)
	}

	collection := args[0]
	name := args[1]
	method := "GET"
	path := ""
	headers := make(map[string]string)
	body := ""
	query := make(map[string]string)

	for i := 2; i < len(args); i++ {
		switch args[i] {
		case "--method":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --method requires a value\n")
				os.Exit(1)
			}
			i++
			method = strings.ToUpper(args[i])
		case "--path":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --path requires a value\n")
				os.Exit(1)
			}
			i++
			path = args[i]
		case "--header":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --header requires key:value\n")
				os.Exit(1)
			}
			i++
			parts := strings.SplitN(args[i], ":", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "Error: --header must be key:value format\n")
				os.Exit(1)
			}
			headers[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		case "--body":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --body requires a value\n")
				os.Exit(1)
			}
			i++
			body = args[i]
		case "--query":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --query requires key=value\n")
				os.Exit(1)
			}
			i++
			parts := strings.SplitN(args[i], "=", 2)
			if len(parts) != 2 {
				fmt.Fprintf(os.Stderr, "Error: --query must be key=value format\n")
				os.Exit(1)
			}
			query[parts[0]] = parts[1]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			os.Exit(1)
		}
	}

	req := bus.Request{
		Name:   name,
		Method: method,
		Path:   path,
	}
	if len(headers) > 0 {
		req.Headers = headers
	}
	if body != "" {
		req.Body = body
	}
	if len(query) > 0 {
		req.Query = query
	}

	err := bus.AddRequest(collection, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error adding request: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Added request %q to collection %q\n", name, collection)
	fmt.Printf("  %s %s\n", method, path)
}

func apiCollectionRemoveRequest(collection, name string) {
	err := bus.RemoveRequest(collection, name)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error removing request: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Removed request %q from collection %q\n", name, collection)
}

// --- History ---

func apiHistory(args []string) {
	collection := ""
	limit := 0

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--collection":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --collection requires a value\n")
				os.Exit(1)
			}
			i++
			collection = args[i]
		case "--limit":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --limit requires a value\n")
				os.Exit(1)
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error: --limit must be a number\n")
				os.Exit(1)
			}
			limit = n
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api history [--collection name] [--limit N]\n")
			os.Exit(1)
		}
	}

	entries, err := bus.ReadApiHistory(collection, limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading API history: %v\n", err)
		os.Exit(1)
	}
	fmt.Print(bus.FormatApiHistory(entries))
}

// --- Import ---

func apiImport(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus api import <source-dir>\n")
		os.Exit(1)
	}

	srcDir := args[0]
	envCount, colCount, err := bus.ImportApiDir(srcDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error importing: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Imported %d environment(s) and %d collection(s) from %s\n", envCount, colCount, srcDir)
}
