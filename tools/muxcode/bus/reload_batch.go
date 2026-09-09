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
	Windowless   bool   // true if the role has no window — configurable, not reloadable
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

	// ConfigOnly records that the role had no window, so its provider/model was
	// persisted for a future launch and nothing was stopped or relaunched.
	ConfigOnly bool
}

// ConfigureWindowlessRole sets a role's CLI and model without stopping or
// relaunching anything, writing BOTH config stores.
//
// Each store alone is insufficient, and picking one was the original bug. The
// shell config survives the session but is never consulted by
// ResolveProviderCLI — it reaches a role only by being sourced into the
// environment at launch, so a write to it is invisible for the whole current
// session, and worse, is outranked by the value the session already exported.
// The runtime override is read directly and outranks that stale env, so it is
// what makes the change real now — but it lives in the session's bus dir and
// dies with it.
//
// So: the override makes the setting take effect and show up immediately, the
// shell config carries it to the next session.
func ConfigureWindowlessRole(session, role, cli, model string) error {
	set := func(key, value string) error {
		if value == "" {
			return nil
		}
		if err := SetShellConfigValue(key, value); err != nil {
			return fmt.Errorf("persist %s for %s: %w", key, role, err)
		}
		if err := WriteRuntimeOverride(session, role, key, value); err != nil {
			return fmt.Errorf("override %s for %s: %w", key, role, err)
		}
		return nil
	}
	if err := set(RoleCLIEnvVar(role), cli); err != nil {
		return err
	}
	return set(RoleModelEnvVar(role), model)
}

// ReloadProgress is called during ReloadBatch to report per-agent progress.
type ReloadProgress func(index int, result ReloadResult)

// ActiveAgentStatuses returns reload status for all reloadable agents in the session.
// Includes all agents: standard and mode-cycled (plan, research, edit, auto).
// Excludes only hosted roles (docs, pr-read) which share their host's process.
//
// Roles absent from the session's window list are marked Windowless and never
// reported Alive. ReloadableRoles walks KnownRoles, which is a superset of the
// launched windows, and IsAgentAlive fail-safes to "alive" when it cannot
// capture a pane — so a role that was never launched (analyze, in every default
// session) otherwise appeared here as a live, selectable reload target whose
// reload could only ever fail. The window list is read once for the sweep; an
// unreadable list is indeterminate, so nothing is marked windowless.
func ActiveAgentStatuses(session string) []AgentReloadStatus {
	var statuses []AgentReloadStatus
	windows, windowsErr := TmuxListWindowNames(session)
	windowsKnown := windowsErr == nil && len(windows) > 0
	for _, role := range ReloadableRoles() {
		// The prompt-agent is headless (no window, no F-key) and its
		// provider/model live in the backend setting, not a launch config.
		if role == promptAgentRole {
			statuses = append(statuses, promptAgentStatus(session))
			continue
		}
		window := WindowForRole(role)
		cli := ResolveProviderCLI(role)
		rc := EffectiveConfig(role)
		windowless := windowsKnown && !RoleWindowPresent(windows, role)
		alive := !windowless && IsAgentAlive(session, role)
		fkey := WindowFKey(session, window)

		statuses = append(statuses, AgentReloadStatus{
			Role:         role,
			Window:       window,
			CLI:          cli,
			Model:        rc.Model,
			Alive:        alive,
			Windowless:   windowless,
			Orchestrator: role == "edit" || role == "auto",
			FKey:         fkey,
		})
	}
	return statuses
}

// promptAgentStatus builds the selector row for the headless prompt
// role, translating its backend into the selector's CLI vocabulary
// (opencode ↔ gateway, local ↔ ollama).
func promptAgentStatus(session string) AgentReloadStatus {
	backend, model := PromptBackendInfo(session)
	cli := "opencode"
	if backend == "ollama" {
		cli = "local"
	}
	return AgentReloadStatus{
		Role:  promptAgentRole,
		CLI:   cli,
		Model: model,
		Alive: PromptAgentAlive(session),
	}
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

// ConfigOnlyRole reports whether applying to role should persist its config
// instead of reloading it, because the role has no window to run in.
//
// The prompt role is excluded: it is headless by design, has no window either,
// and ReloadBatch already routes it to its own reload path.
//
// An unreadable window list (windowsKnown false) is indeterminate and takes the
// normal reload path, so a transient tmux failure cannot silently convert every
// reload in a batch into a config write.
func ConfigOnlyRole(windows []string, windowsKnown bool, role string) bool {
	if !windowsKnown || role == promptAgentRole {
		return false
	}
	return !RoleWindowPresent(windows, role)
}

// ReloadBatch reloads multiple agents sequentially with CLI/model overrides.
// Returns per-agent results. Continues on individual failures (failure isolation).
// The optional progress callback is invoked after each agent completes.
//
// A role with no window is configured rather than reloaded: its provider/model is
// persisted for a future launch, and no stop or relaunch is attempted. Sequencing
// also skips the inter-agent gap for those, which exists to stagger relaunches.
func ReloadBatch(session string, roles []string, cli, model string, compact bool, progress ReloadProgress) []ReloadResult {
	var results []ReloadResult
	windows, windowsErr := TmuxListWindowNames(session)
	windowsKnown := windowsErr == nil && len(windows) > 0
	relaunched := 0
	for i, role := range roles {
		configOnly := ConfigOnlyRole(windows, windowsKnown, role)
		if relaunched > 0 && !configOnly {
			time.Sleep(3 * time.Second) // 3s gap between agents
		}
		if !configOnly {
			relaunched++
		}

		start := time.Now()
		var result ReloadResult
		if configOnly {
			oldCLI := ResolveProviderCLI(role)
			oldRC := EffectiveConfig(role)
			err := ConfigureWindowlessRole(session, role, cli, model)
			newCLI, newModel := ResolveProviderCLI(role), EffectiveConfig(role).Model
			result = ReloadResult{
				Role: role, Success: err == nil, Error: err, ConfigOnly: true,
				OldCLI: oldCLI, OldModel: oldRC.Model,
				NewCLI: newCLI, NewModel: newModel,
				Duration: time.Since(start),
			}
			results = append(results, result)
			if progress != nil {
				progress(i, result)
			}
			continue
		}
		if role == promptAgentRole {
			old := promptAgentStatus(session)
			err := ReloadPromptAgent(session, cli, model)
			now := promptAgentStatus(session)
			result = ReloadResult{
				Role: role, Success: err == nil, Error: err,
				OldCLI: old.CLI, OldModel: old.Model,
				NewCLI: now.CLI, NewModel: now.Model,
				Duration: time.Since(start),
			}
			results = append(results, result)
			if progress != nil {
				progress(i, result)
			}
			continue
		}
		oldCLI := ResolveProviderCLI(role)
		oldRC := EffectiveConfig(role)

		err := ReloadAgent(session, role, cli, model, compact)
		elapsed := time.Since(start)

		newCLI := ResolveProviderCLI(role)
		newRC := EffectiveConfig(role)

		result = ReloadResult{
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
//	"claude-sonnet-5"          → "sonnet-5"
//	"opencode-go/minimax-m3"   → "minimax-m3"
//	"gpt-5.5"                    → "gpt-5.5" (already short)
func AbbreviateModel(model string) string {
	// Strip org/namespace prefix (e.g. "opencode-go/minimax-m3")
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
