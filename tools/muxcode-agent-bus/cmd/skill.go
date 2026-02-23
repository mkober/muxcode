package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Skill handles the "muxcode-agent-bus skill" subcommand.
func Skill(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill <list|load|search|create|prompt> [args...]\n")
		os.Exit(1)
	}

	subcmd := args[0]
	subArgs := args[1:]

	switch subcmd {
	case "list":
		skillList(subArgs)
	case "load":
		skillLoad(subArgs)
	case "search":
		skillSearch(subArgs)
	case "create":
		skillCreate(subArgs)
	case "prompt":
		skillPrompt(subArgs)
	default:
		fmt.Fprintf(os.Stderr, "Unknown skill subcommand: %s\n", subcmd)
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill <list|load|search|create|prompt> [args...]\n")
		os.Exit(1)
	}
}

func skillList(args []string) {
	roleFilter := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --role requires a value\n")
				os.Exit(1)
			}
			i++
			roleFilter = args[i]
		default:
			fmt.Fprintf(os.Stderr, "Unknown flag: %s\n", args[i])
			fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill list [--role ROLE]\n")
			os.Exit(1)
		}
	}

	var skills []bus.SkillDef
	var err error
	if roleFilter != "" {
		skills, err = bus.SkillsForRole(roleFilter)
	} else {
		skills, err = bus.ListSkills()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing skills: %v\n", err)
		os.Exit(1)
	}

	if len(skills) > 0 {
		fmt.Print(bus.FormatSkillList(skills))
	}
}

func skillLoad(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill load <name>\n")
		os.Exit(1)
	}

	skill, err := bus.LoadSkill(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading skill: %v\n", err)
		os.Exit(1)
	}

	fmt.Print(bus.FormatSkillPrompt(skill))
}

func skillSearch(args []string) {
	var queryParts []string
	roleFilter := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--role":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --role requires a value\n")
				os.Exit(1)
			}
			i++
			roleFilter = args[i]
		default:
			queryParts = append(queryParts, args[i])
		}
	}

	query := strings.Join(queryParts, " ")
	if query == "" {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill search <query> [--role ROLE]\n")
		os.Exit(1)
	}

	results, err := bus.SearchSkills(query, roleFilter)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error searching skills: %v\n", err)
		os.Exit(1)
	}

	if len(results) > 0 {
		fmt.Print(bus.FormatSkillSearchResults(results))
	}
}

func skillCreate(args []string) {
	var roles, tags []string
	var positional []string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--roles":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --roles requires a value\n")
				os.Exit(1)
			}
			i++
			roles = splitAndTrim(args[i])
		case "--tags":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "Error: --tags requires a value\n")
				os.Exit(1)
			}
			i++
			tags = splitAndTrim(args[i])
		default:
			positional = append(positional, args[i])
		}
	}

	if len(positional) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: muxcode-agent-bus skill create <name> <desc> [--roles r1,r2] [--tags t1,t2] [body]\n")
		os.Exit(1)
	}

	name := positional[0]
	desc := positional[1]
	body := ""
	if len(positional) > 2 {
		body = strings.Join(positional[2:], " ")
	}

	if err := bus.CreateSkill(name, desc, body, roles, tags); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating skill: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(os.Stderr, "Created skill: %s\n", name)
}

func skillPrompt(args []string) {
	role := bus.BusRole()
	if len(args) > 0 {
		role = args[0]
	}

	skills, err := bus.SkillsForRole(role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading skills for role %s: %v\n", role, err)
		os.Exit(1)
	}

	output := bus.FormatSkillsPrompt(skills)
	if output != "" {
		fmt.Print(output)
	}
}

// splitAndTrim splits a comma-separated string and trims whitespace from each element.
func splitAndTrim(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
