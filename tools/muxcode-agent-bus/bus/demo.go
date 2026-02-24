package bus

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DemoStep represents a single step in a demo scenario.
type DemoStep struct {
	Description string        // Printed during execution
	Window      string        // tmux window to switch to (empty = no switch)
	Action      string        // "select-window", "send", "lock", "unlock", "sleep"
	Role        string        // Target agent role
	BusAction   string        // Bus action field (for send)
	MsgType     string        // "request", "event", "response"
	Payload     string        // Message payload
	DelayAfter  time.Duration // Pause after step (scaled by speed)
}

// DemoScenario is a named sequence of demo steps.
type DemoScenario struct {
	Name        string
	Description string
	Steps       []DemoStep
}

// DemoOptions controls demo execution behavior.
type DemoOptions struct {
	Speed    float64 // Multiplier for delays (2.0 = fast, 0.5 = slow)
	DryRun   bool    // Print steps without executing
	NoSwitch bool    // Skip tmux window switching
}

// RunDemo executes a demo scenario with the given options.
// Returns the total elapsed time.
func RunDemo(session string, scenario DemoScenario, opts DemoOptions) (time.Duration, error) {
	if opts.Speed <= 0 {
		opts.Speed = 1.0
	}

	start := time.Now()

	fmt.Printf("=== Demo: %s ===\n", scenario.Name)
	fmt.Printf("    %s\n", scenario.Description)
	fmt.Printf("    Speed: %.1fx  Steps: %d\n\n", opts.Speed, len(scenario.Steps))

	for i, step := range scenario.Steps {
		stepNum := i + 1
		scaledDelay := ScaleDelay(step.DelayAfter, opts.Speed)

		// Print step description
		prefix := fmt.Sprintf("[%2d/%d]", stepNum, len(scenario.Steps))
		if opts.DryRun {
			fmt.Printf("%s %s (delay: %s)\n", prefix, step.Description, scaledDelay)
			printStepDetail(step)
			continue
		}

		fmt.Printf("%s %s\n", prefix, step.Description)

		// Execute action
		if err := executeStep(session, step, opts); err != nil {
			return time.Since(start), fmt.Errorf("step %d (%s): %w", stepNum, step.Description, err)
		}

		// Scaled delay
		if scaledDelay > 0 {
			time.Sleep(scaledDelay)
		}
	}

	elapsed := time.Since(start)
	fmt.Printf("\n=== Done (%s) ===\n", elapsed.Round(time.Millisecond))
	return elapsed, nil
}

// ScaleDelay applies the speed factor to a delay duration.
func ScaleDelay(d time.Duration, speed float64) time.Duration {
	if speed <= 0 {
		speed = 1.0
	}
	return time.Duration(float64(d) / speed)
}

// executeStep dispatches a single demo step.
func executeStep(session string, step DemoStep, opts DemoOptions) error {
	switch step.Action {
	case "select-window":
		if opts.NoSwitch {
			return nil
		}
		return tmuxSelectWindow(session, step.Window)

	case "send":
		msg := NewMessage("demo", step.Role, step.MsgType, step.BusAction, step.Payload, "")
		if err := Send(session, msg); err != nil {
			return fmt.Errorf("send to %s: %w", step.Role, err)
		}
		// Notify the target agent
		_ = Notify(session, step.Role)
		return nil

	case "lock":
		return Lock(session, step.Role)

	case "unlock":
		return Unlock(session, step.Role)

	case "sleep":
		// Pure delay — handled by DelayAfter
		return nil

	default:
		return fmt.Errorf("unknown action: %s", step.Action)
	}
}

// tmuxSelectWindow switches the active tmux window.
func tmuxSelectWindow(session, window string) error {
	target := session + ":" + window
	cmd := exec.Command("tmux", "select-window", "-t", target)
	return cmd.Run()
}

// printStepDetail prints additional step details for dry-run output.
func printStepDetail(step DemoStep) {
	switch step.Action {
	case "select-window":
		fmt.Printf("       -> tmux select-window -t SESSION:%s\n", step.Window)
	case "send":
		fmt.Printf("       -> bus.Send(demo -> %s, type=%s, action=%s)\n", step.Role, step.MsgType, step.BusAction)
		if step.Payload != "" {
			payload := step.Payload
			if len(payload) > 60 {
				payload = payload[:57] + "..."
			}
			fmt.Printf("       -> payload: %s\n", payload)
		}
	case "lock":
		fmt.Printf("       -> bus.Lock(%s)\n", step.Role)
	case "unlock":
		fmt.Printf("       -> bus.Unlock(%s)\n", step.Role)
	case "sleep":
		fmt.Printf("       -> (pure delay)\n")
	}
}

// BuiltinScenarios returns all built-in demo scenarios.
func BuiltinScenarios() []DemoScenario {
	return []DemoScenario{
		BuildTestReviewScenario(),
	}
}

// BuildTestReviewScenario returns the default build-test-review-commit demo.
// Demonstrates the full delegation cycle: edit -> build -> test -> review -> commit.
func BuildTestReviewScenario() DemoScenario {
	return DemoScenario{
		Name:        "build-test-review",
		Description: "Full build-test-review-commit cycle across agent windows",
		Steps: []DemoStep{
			// 1. Show edit window
			{
				Description: "Show edit window",
				Window:      "edit",
				Action:      "select-window",
				DelayAfter:  1500 * time.Millisecond,
			},
			// 2. Edit delegates build
			{
				Description: "Edit agent delegates build",
				Role:        "build",
				Action:      "send",
				BusAction:   "build",
				MsgType:     "request",
				Payload:     "Run ./build.sh and report results",
				DelayAfter:  1000 * time.Millisecond,
			},
			// 3. Switch to build window
			{
				Description: "Switch to build window",
				Window:      "build",
				Action:      "select-window",
				DelayAfter:  500 * time.Millisecond,
			},
			// 4. Build agent busy
			{
				Description: "Build agent working",
				Role:        "build",
				Action:      "lock",
				DelayAfter:  2500 * time.Millisecond,
			},
			// 5. Build complete
			{
				Description: "Build complete",
				Role:        "build",
				Action:      "unlock",
				DelayAfter:  500 * time.Millisecond,
			},
			// 6. Chain fires: build -> test
			{
				Description: "Hook chain fires: build -> test",
				Role:        "test",
				Action:      "send",
				BusAction:   "test",
				MsgType:     "event",
				Payload:     "Build succeeded — run tests",
				DelayAfter:  500 * time.Millisecond,
			},
			// 7. Switch to test window
			{
				Description: "Switch to test window",
				Window:      "test",
				Action:      "select-window",
				DelayAfter:  500 * time.Millisecond,
			},
			// 8. Test agent running
			{
				Description: "Test agent running",
				Role:        "test",
				Action:      "lock",
				DelayAfter:  3000 * time.Millisecond,
			},
			// 9. Tests pass
			{
				Description: "Tests pass",
				Role:        "test",
				Action:      "unlock",
				DelayAfter:  500 * time.Millisecond,
			},
			// 10. Chain fires: test -> review
			{
				Description: "Hook chain fires: test -> review",
				Role:        "review",
				Action:      "send",
				BusAction:   "review",
				MsgType:     "event",
				Payload:     "Tests passed — review the diff",
				DelayAfter:  500 * time.Millisecond,
			},
			// 11. Switch to review window
			{
				Description: "Switch to review window",
				Window:      "review",
				Action:      "select-window",
				DelayAfter:  500 * time.Millisecond,
			},
			// 12. Review agent analyzing
			{
				Description: "Review agent analyzing",
				Role:        "review",
				Action:      "lock",
				DelayAfter:  2500 * time.Millisecond,
			},
			// 13. Review complete
			{
				Description: "Review complete",
				Role:        "review",
				Action:      "unlock",
				DelayAfter:  500 * time.Millisecond,
			},
			// 14. Switch back to edit window
			{
				Description: "Results arrive at edit window",
				Window:      "edit",
				Action:      "select-window",
				DelayAfter:  1500 * time.Millisecond,
			},
			// 15. Review results sent to edit
			{
				Description: "Review results delivered to edit agent",
				Role:        "edit",
				Action:      "send",
				BusAction:   "review",
				MsgType:     "response",
				Payload:     "All checks passed. No issues found.",
				DelayAfter:  2000 * time.Millisecond,
			},
			// 16. Delegate commit
			{
				Description: "Edit agent delegates commit",
				Role:        "commit",
				Action:      "send",
				BusAction:   "commit",
				MsgType:     "request",
				Payload:     "Stage and commit the current changes",
				DelayAfter:  500 * time.Millisecond,
			},
			// 17. Switch to commit window
			{
				Description: "Switch to commit window",
				Window:      "commit",
				Action:      "select-window",
				DelayAfter:  500 * time.Millisecond,
			},
			// 18. Commit agent working
			{
				Description: "Git manager committing",
				Role:        "commit",
				Action:      "lock",
				DelayAfter:  2000 * time.Millisecond,
			},
			// 19. Commit complete
			{
				Description: "Commit complete",
				Role:        "commit",
				Action:      "unlock",
				DelayAfter:  500 * time.Millisecond,
			},
			// 20. Return to edit
			{
				Description: "Return to edit window",
				Window:      "edit",
				Action:      "select-window",
				DelayAfter:  1000 * time.Millisecond,
			},
		},
	}
}

// GetScenario returns a scenario by name, or an error if not found.
func GetScenario(name string) (DemoScenario, error) {
	for _, s := range BuiltinScenarios() {
		if s.Name == name {
			return s, nil
		}
	}
	return DemoScenario{}, fmt.Errorf("unknown scenario: %s", name)
}

// FormatScenarioList returns a human-readable list of available scenarios.
func FormatScenarioList(scenarios []DemoScenario) string {
	var b strings.Builder

	b.WriteString("Available demo scenarios:\n\n")
	for _, s := range scenarios {
		b.WriteString(fmt.Sprintf("  %-24s %s\n", s.Name, s.Description))
		totalDelay := time.Duration(0)
		for _, step := range s.Steps {
			totalDelay += step.DelayAfter
		}
		b.WriteString(fmt.Sprintf("  %-24s %d steps, ~%s at 1.0x speed\n", "", len(s.Steps), totalDelay.Round(time.Second)))
		b.WriteString("\n")
	}

	return b.String()
}
