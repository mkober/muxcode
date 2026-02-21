package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// teamConfig represents the structure of a team config.json.
type teamConfig struct {
	Members []teamMember `json:"members"`
}

// teamMember represents a single team member.
type teamMember struct {
	Name string `json:"name"`
}

// taskFile represents the structure of a task JSON file.
type taskFile struct {
	Status  string `json:"status"`
	Subject string `json:"subject"`
}

// RenderTeams returns lines of ANSI-colored text showing teams and tasks.
func RenderTeams() []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return []string{fmt.Sprintf("  %s(none active)%s", Comment, RST)}
	}

	teamsDir := filepath.Join(homeDir, ".claude", "teams")
	tasksDir := filepath.Join(homeDir, ".claude", "tasks")

	entries, err := os.ReadDir(teamsDir)
	if err != nil {
		return []string{fmt.Sprintf("  %s(none active)%s", Comment, RST)}
	}

	var lines []string
	found := 0

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		teamName := entry.Name()
		cfgPath := filepath.Join(teamsDir, teamName, "config.json")

		data, err := os.ReadFile(cfgPath)
		if err != nil {
			continue
		}
		found++

		var cfg teamConfig
		if err := json.Unmarshal(data, &cfg); err != nil {
			lines = append(lines, fmt.Sprintf("  %s%s%s (parse error)", Purple, teamName, RST))
			continue
		}

		// Member names
		var memberNames []string
		for _, m := range cfg.Members {
			if m.Name != "" {
				memberNames = append(memberNames, m.Name)
			}
		}

		memberCount := len(memberNames)
		lines = append(lines, fmt.Sprintf("  %s%s%s (%d members)", Purple, teamName, RST, memberCount))

		if len(memberNames) > 0 {
			lines = append(lines, fmt.Sprintf("    %s%s%s", Comment, strings.Join(memberNames, ", "), RST))
		}

		// Tasks for this team
		taskDir := filepath.Join(tasksDir, teamName)
		taskLines := renderTasks(taskDir)
		lines = append(lines, taskLines...)
	}

	if found == 0 {
		return []string{fmt.Sprintf("  %s(none active)%s", Comment, RST)}
	}

	return lines
}

// renderTasks scans a task directory and returns summary lines.
func renderTasks(taskDir string) []string {
	if _, err := os.Stat(taskDir); os.IsNotExist(err) {
		return nil
	}

	pending := 0
	inProgress := 0
	completed := 0
	var activeSubjects []string

	err := filepath.Walk(taskDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var t taskFile
		if err := json.Unmarshal(data, &t); err != nil {
			return nil
		}

		switch t.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
			if t.Subject != "" {
				activeSubjects = append(activeSubjects, t.Subject)
			}
		case "completed":
			completed++
		}
		return nil
	})
	if err != nil {
		return nil
	}

	if pending == 0 && inProgress == 0 && completed == 0 {
		return nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("    %sTasks: %s%d%s done %s%d%s active %s%d%s pending%s",
		Comment,
		Green, completed, Comment,
		Cyan, inProgress, Comment,
		Dim, pending, Comment,
		RST))

	for _, subj := range activeSubjects {
		lines = append(lines, fmt.Sprintf("    %s  > %s%s", Cyan, subj, RST))
	}

	return lines
}
