package bus

import (
	"fmt"
	"strings"
	"time"
)

// AgentReloadStatus describes an agent's current provider/model for the selector TUI.
type AgentReloadStatus struct {
	Role         string // agent role name
	Window       string // tmux window name
	CLI          string // current provider CLI (claude, opencode, codex, local)
	Model        string // current model ID
	Alive        bool   // true if agent process is alive
	Orchestrator bool   // true for edit/auto — shown with ⚠, excluded from "select all"
	FKey         string // F-key label (e.g., "F3")
}

// ReloadResult describes the outcome of a single agent reload within a batch.
type ReloadResult struct {
	Role     string
	Success  bool
	Error    error
	OldCLI   string
	OldModel string
	NewCLI   string
	NewModel string
	Duration time.Duration
}

// ReloadProgress is called during ReloadBatch to report per-agent progress.
type ReloadProgress func(index int, result ReloadResult)

// ActiveAgentStatuses returns reload status for all reloadable agents in the session.
// Includes all agents: standard and mode-cycled (plan, research, edit, auto).
// Excludes only hosted roles (docs, pr-read) which share their host's process.
func ActiveAgentStatuses(session string) []AgentReloadStatus {
	var statuses []AgentReloadStatus
	for _, role := range ReloadableRoles() {
		window := WindowForRole(role)
		cli := ResolveProviderCLI(role)
		rc := EffectiveConfig(role)
		alive := IsAgentAlive(session, role)
		fkey := WindowFKey(session, window)

		statuses = append(statuses, AgentReloadStatus{
			Role:         role,
			Window:       window,
			CLI:          cli,
			Model:        rc.Model,
			Alive:        alive,
			Orchestrator: role == "edit" || role == "auto",
			FKey:         fkey,
		})
	}
	return statuses
}

// ReloadableRoles returns roles eligible for reload.
// Includes all agents including mode-cycled (plan, research, edit, auto).
// Excludes only hosted roles (docs, pr-read) which share their host's process,
// and non-agent roles (webhook, api).
func ReloadableRoles() []string {
	var roles []string
	for _, role := range KnownRoles {
		// Skip hosted roles — they share their host's process
		if IsHostedRole(role) {
			continue
		}
		// Skip non-agent roles
		if role == "webhook" || role == "api" {
			continue
		}
		roles = append(roles, role)
	}
	return roles
}

// ReloadBatch reloads multiple agents sequentially with CLI/model overrides.
// Returns per-agent results. Continues on individual failures (failure isolation).
// The optional progress callback is invoked after each agent completes.
func ReloadBatch(session string, roles []string, cli, model string, compact bool, progress ReloadProgress) []ReloadResult {
	var results []ReloadResult
	for i, role := range roles {
		if i > 0 {
			time.Sleep(3 * time.Second) // 3s gap between agents
		}

		start := time.Now()
		oldCLI := ResolveProviderCLI(role)
		oldRC := EffectiveConfig(role)

		err := ReloadAgent(session, role, cli, model, compact)
		elapsed := time.Since(start)

		newCLI := ResolveProviderCLI(role)
		newRC := EffectiveConfig(role)

		result := ReloadResult{
			Role:     role,
			Success:  err == nil,
			Error:    err,
			OldCLI:   oldCLI,
			OldModel: oldRC.Model,
			NewCLI:   newCLI,
			NewModel: newRC.Model,
			Duration: elapsed,
		}
		results = append(results, result)

		if progress != nil {
			progress(i, result)
		}
	}
	return results
}

// AbbreviateModel shortens a model ID for compact display.
//
//	"claude-sonnet-4-6"          → "sonnet-4-6"
//	"opencode-go/minimax-m2.5"   → "minimax-m2.5"
//	"gpt-5.5"                    → "gpt-5.5" (already short)
func AbbreviateModel(model string) string {
	// Strip org/namespace prefix (e.g. "opencode-go/minimax-m2.5")
	if i := strings.LastIndex(model, "/"); i >= 0 {
		return model[i+1:]
	}
	// Strip "claude-" prefix
	if strings.HasPrefix(model, "claude-") {
		return model[len("claude-"):]
	}
	return model
}

// FormatReloadResults returns a human-readable summary of batch reload results.
func FormatReloadResults(results []ReloadResult) string {
	var b strings.Builder
	succeeded := 0
	for _, r := range results {
		if r.Success {
			succeeded++
		}
	}

	for _, r := range results {
		if r.Success {
			b.WriteString(fmt.Sprintf("  ✓ %-10s %s → %s  (%s)\n",
				r.Role, r.OldCLI, r.NewCLI, r.Duration.Round(time.Second)))
		} else {
			b.WriteString(fmt.Sprintf("  ✗ %-10s %v\n", r.Role, r.Error))
		}
	}

	b.WriteString(fmt.Sprintf("\nReloaded %d/%d agents successfully", succeeded, len(results)))
	if succeeded > 0 && results[0].NewCLI != "" {
		b.WriteString(fmt.Sprintf(" (%s / %s)", results[0].NewCLI, AbbreviateModel(results[0].NewModel)))
	}
	b.WriteString("\n")
	return b.String()
}
