package bus

import (
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// UITest represents a single integration test case.
type UITest struct {
	Name string
	Fn   func(ctx *UITestContext)
}

// UITestSuite groups related integration tests under a common name
// with shared setup and cleanup.
type UITestSuite struct {
	Name    string
	Setup   func(ctx *UITestContext) error
	Tests   []UITest
	Cleanup func(ctx *UITestContext)
}

// UITestContext provides the session context and assertion helpers
// for integration tests running in a live tmux session.
type UITestContext struct {
	Session string
	Pass    int
	Fail    int
	Total   int
}

// --- Assertions ---

// AssertEqual checks that got == want.
func (ctx *UITestContext) AssertEqual(got, want, msg string) {
	ctx.Total++
	if got == want {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s (expected %q, got %q)\n", ColorRed, ColorReset, msg, want, got)
	}
}

// AssertNotEqual checks that got != want.
func (ctx *UITestContext) AssertNotEqual(got, notWant, msg string) {
	ctx.Total++
	if got != notWant {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s (should not equal %q)\n", ColorRed, ColorReset, msg, notWant)
	}
}

// AssertContains checks that haystack contains needle.
func (ctx *UITestContext) AssertContains(haystack, needle, msg string) {
	ctx.Total++
	if strings.Contains(haystack, needle) {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s (expected to contain %q)\n", ColorRed, ColorReset, msg, needle)
	}
}

// AssertTrue checks that cond is true.
func (ctx *UITestContext) AssertTrue(cond bool, msg string) {
	ctx.Total++
	if cond {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s\n", ColorRed, ColorReset, msg)
	}
}

// AssertFalse checks that cond is false.
func (ctx *UITestContext) AssertFalse(cond bool, msg string) {
	ctx.Total++
	if !cond {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s\n", ColorRed, ColorReset, msg)
	}
}

// AssertNoError checks that err is nil.
func (ctx *UITestContext) AssertNoError(err error, msg string) {
	ctx.Total++
	if err == nil {
		ctx.Pass++
		fmt.Printf("  %sPASS%s %s\n", ColorGreen, ColorReset, msg)
	} else {
		ctx.Fail++
		fmt.Printf("  %sFAIL%s %s (%v)\n", ColorRed, ColorReset, msg, err)
	}
}

// --- Tmux helpers ---

// TmuxCapturePane captures the last N lines from a tmux pane, stripping ANSI codes.
func (ctx *UITestContext) TmuxCapturePane(target string, lines int) string {
	out, err := exec.Command("tmux", "capture-pane", "-t", target, "-p",
		"-S", fmt.Sprintf("-%d", lines)).Output()
	if err != nil {
		return ""
	}
	return stripANSI(string(out))
}

// TmuxListWindowNames returns all window names in the session.
func (ctx *UITestContext) TmuxListWindowNames() []string {
	out, err := exec.Command("tmux", "list-windows", "-t", ctx.Session,
		"-F", "#{window_name}").Output()
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	var names []string
	for _, l := range lines {
		if l != "" {
			names = append(names, l)
		}
	}
	return names
}

// TmuxWindowExists checks if a named window exists in the session.
func (ctx *UITestContext) TmuxWindowExists(name string) bool {
	for _, w := range ctx.TmuxListWindowNames() {
		if w == name {
			return true
		}
	}
	return false
}

// TmuxWindowInfo returns "index name panes" for a window by name.
func (ctx *UITestContext) TmuxWindowInfo(name string) (index, panes int, found bool) {
	out, err := exec.Command("tmux", "list-windows", "-t", ctx.Session,
		"-F", "#{window_index} #{window_name} #{window_panes}").Output()
	if err != nil {
		return 0, 0, false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 3 && parts[1] == name {
			idx, _ := fmt.Sscanf(parts[0], "%d", &index)
			cnt, _ := fmt.Sscanf(parts[2], "%d", &panes)
			return index, panes, idx > 0 && cnt > 0
		}
	}
	return 0, 0, false
}

// TmuxKillWindow kills a window by name, ignoring errors.
func (ctx *UITestContext) TmuxKillWindow(name string) {
	exec.Command("tmux", "kill-window", "-t", ctx.Session+":"+name).Run()
}

// Sleep is a convenience wrapper around time.Sleep.
func (ctx *UITestContext) Sleep(d time.Duration) {
	time.Sleep(d)
}

// --- Runner ---

// UITestRunner runs test suites and reports results.
type UITestRunner struct {
	Session string
	Verbose bool
}

// RunSuites runs all provided suites and returns the total failure count.
func (r *UITestRunner) RunSuites(suites []UITestSuite) int {
	totalPass, totalFail, totalCount := 0, 0, 0

	fmt.Printf("%s=== MuxCode Integration Tests ===%s\n", ColorPurple, ColorReset)
	fmt.Printf("Session: %s\n\n", r.Session)

	for _, suite := range suites {
		sPass, sFail, sTotal := r.runSuite(suite)
		totalPass += sPass
		totalFail += sFail
		totalCount += sTotal
	}

	// Summary
	fmt.Println()
	fmt.Printf("%s==========================================%s\n", ColorPurple, ColorReset)
	if totalFail == 0 {
		fmt.Printf("Results: %s%d passed%s, %d total\n",
			ColorGreen, totalPass, ColorReset, totalCount)
	} else {
		fmt.Printf("Results: %s%d passed%s, %s%d failed%s, %d total\n",
			ColorGreen, totalPass, ColorReset,
			ColorRed, totalFail, ColorReset,
			totalCount)
	}
	fmt.Printf("%s==========================================%s\n", ColorPurple, ColorReset)

	return totalFail
}

// runSuite runs a single suite: setup → tests → cleanup.
func (r *UITestRunner) runSuite(suite UITestSuite) (pass, fail, total int) {
	fmt.Printf("%s--- %s ---%s\n", ColorCyan, suite.Name, ColorReset)

	ctx := &UITestContext{Session: r.Session}

	// Setup
	if suite.Setup != nil {
		if err := suite.Setup(ctx); err != nil {
			fmt.Printf("  %sSETUP FAILED%s: %v\n\n", ColorRed, ColorReset, err)
			return 0, 1, 1
		}
	}

	// Cleanup always runs
	if suite.Cleanup != nil {
		defer suite.Cleanup(ctx)
	}

	// Run each test
	for _, test := range suite.Tests {
		if r.Verbose {
			fmt.Printf("\n  %s%s%s\n", ColorDim, test.Name, ColorReset)
		}
		test.Fn(ctx)
	}

	fmt.Println()
	return ctx.Pass, ctx.Fail, ctx.Total
}

// ListSuites prints available test suites without running them.
func ListSuites(suites []UITestSuite) {
	fmt.Println("Available integration test suites:")
	for _, s := range suites {
		fmt.Printf("  %s%-15s%s (%d tests)\n", ColorCyan, s.Name, ColorReset, len(s.Tests))
		for _, t := range s.Tests {
			fmt.Printf("    %s%s%s\n", ColorDim, t.Name, ColorReset)
		}
	}
}

// AllUITestSuites returns all registered integration test suites.
func AllUITestSuites() []UITestSuite {
	return []UITestSuite{
		ModeCycleTestSuite(),
	}
}

// stripANSI removes ANSI escape sequences from a string.
func stripANSI(s string) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\033' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we hit a letter (A-Z, a-z)
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip the terminating letter
			}
			i = j
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}
