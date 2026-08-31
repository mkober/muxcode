package bus

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ModeCycleTestSuite returns the integration test suite for mode cycling.
// Tests the full mode cycle lifecycle: state management, pane swapping,
// round-trip cycling, direct switching, and window name stability.
//
// Requires: a running muxcode session with the edit window at index 2.
func ModeCycleTestSuite() UITestSuite {
	return UITestSuite{
		Name: "mode-cycle",
		Setup: func(ctx *UITestContext) error {
			fmt.Println("  Setting up: reset mode state, clean stale hold windows")

			// Kill any stale auto window from previous runs.
			if ctx.TmuxWindowExists("auto") {
				// Switch to edit first to avoid killing the active window.
				ModeSwitch(ctx.Session, "edit", "edit")
				ctx.TmuxKillWindow("auto")
			}

			// Reset mode state to edit (index 0).
			state := DefaultModeCycleState()
			if err := WriteModeCycleState(ctx.Session, state); err != nil {
				return fmt.Errorf("reset state: %w", err)
			}

			return nil
		},
		Tests: []UITest{
			{"initial state", testModeInitialState},
			{"cycle edit to auto", testModeCycleEditToAgent},
			{"verify agent panes", testModeVerifyAgentPanes},
			{"cycle auto to edit", testModeCycleAgentToEdit},
			{"verify edit restored", testModeVerifyEditRestored},
			{"direct switch to auto", testModeDirectSwitchToAgent},
			{"direct switch to edit", testModeDirectSwitchToEdit},
			{"idempotent switch", testModeIdempotentSwitch},
			{"rapid round-trips", testModeRapidRoundTrips},
			{"window name stability", testModeWindowNameStability},
		},
		Cleanup: func(ctx *UITestContext) {
			fmt.Printf("  %sCleaning up: restoring edit mode%s\n", ColorDim, ColorReset)

			// Ensure we're back on edit mode.
			state, err := ReadModeCycleState(ctx.Session, "edit")
			if err == nil && state.Current != 0 {
				ModeSwitch(ctx.Session, "edit", "edit")
			}

			// Kill auto window if it exists.
			if ctx.TmuxWindowExists("auto") {
				ctx.TmuxKillWindow("auto")
			}

			// Reset state to default.
			WriteModeCycleState(ctx.Session, DefaultModeCycleState())
		},
	}
}

// testModeInitialState verifies the clean starting state.
func testModeInitialState(ctx *UITestContext) {
	state, err := ReadModeCycleState(ctx.Session, "edit")
	ctx.AssertNoError(err, "read initial state")
	if state == nil {
		return
	}

	ctx.AssertEqual(strconv.Itoa(state.Current), "0", "initial mode is edit (index 0)")
	ctx.AssertEqual(state.Window, "edit", "host window is edit")
	ctx.AssertEqual(strconv.Itoa(len(state.Agents)), "2", "two modes registered")

	current := CurrentModeAgent(state)
	ctx.AssertTrue(current != nil && current.Mode == "edit", "current agent is edit")
}

// testModeCycleEditToAgent cycles from edit to agent and verifies state.
func testModeCycleEditToAgent(ctx *UITestContext) {
	err := ModeCycle(ctx.Session, "edit")
	ctx.AssertNoError(err, "cycle edit → auto")

	state, err := ReadModeCycleState(ctx.Session, "edit")
	ctx.AssertNoError(err, "read state after cycle")
	if state == nil {
		return
	}

	ctx.AssertEqual(strconv.Itoa(state.Current), "1", "current index is 1 (auto)")

	current := CurrentModeAgent(state)
	ctx.AssertTrue(current != nil && current.Mode == "auto", "current mode is auto")

	// auto window should exist.
	ctx.AssertTrue(ctx.TmuxWindowExists("auto"), "auto window created")

	// auto should have 2 panes.
	_, panes, found := ctx.TmuxWindowInfo("auto")
	ctx.AssertTrue(found && panes == 2, "auto has 2 panes")
}

// testModeVerifyAgentPanes checks that the agent window is now at the host index.
// With window-swap, each window keeps its own panes — they just exchange indices.
func testModeVerifyAgentPanes(ctx *UITestContext) {
	// Brief pause for the agent launch command to render in the pane.
	ctx.Sleep(500 * time.Millisecond)

	// auto window's agent pane should have the autonomous agent.
	content := ctx.TmuxCapturePane(PaneTargetForWindow(ctx.Session, "auto", PaneTagAgent), 30)
	hasAgent := strings.Contains(content, "autonomous-agent") ||
		strings.Contains(content, "launch auto")
	ctx.AssertTrue(hasAgent, "auto agent pane has autonomous agent")

	// edit window's agent pane should still have the edit agent.
	content = ctx.TmuxCapturePane(PaneTargetForWindow(ctx.Session, "edit", PaneTagAgent), 30)
	ctx.AssertContains(content, "code-editor", "edit agent pane still has edit agent")
}

// testModeCycleAgentToEdit cycles back from agent to edit (the critical round-trip).
func testModeCycleAgentToEdit(ctx *UITestContext) {
	err := ModeCycle(ctx.Session, "edit")
	ctx.AssertNoError(err, "cycle auto → edit")

	state, err := ReadModeCycleState(ctx.Session, "edit")
	ctx.AssertNoError(err, "read state after round-trip")
	if state == nil {
		return
	}

	ctx.AssertEqual(strconv.Itoa(state.Current), "0", "current index back to 0 (edit)")

	current := CurrentModeAgent(state)
	ctx.AssertTrue(current != nil && current.Mode == "edit", "current mode is edit")
}

// testModeVerifyEditRestored checks that the edit agent is back in the edit window.
func testModeVerifyEditRestored(ctx *UITestContext) {
	// Edit window's agent pane should have the edit agent again.
	content := ctx.TmuxCapturePane(PaneTargetForWindow(ctx.Session, "edit", PaneTagAgent), 30)
	ctx.AssertContains(content, "code-editor", "edit agent pane has edit agent after round-trip")

	// Auto window's agent pane should have the autonomous agent.
	content = ctx.TmuxCapturePane(PaneTargetForWindow(ctx.Session, "auto", PaneTagAgent), 30)
	hasAgent := strings.Contains(content, "autonomous-agent") ||
		strings.Contains(content, "launch auto")
	ctx.AssertTrue(hasAgent, "auto agent pane has autonomous agent after round-trip")
}

// testModeDirectSwitchToAgent tests the direct switch command.
func testModeDirectSwitchToAgent(ctx *UITestContext) {
	err := ModeSwitch(ctx.Session, "edit", "auto")
	ctx.AssertNoError(err, "switch directly to auto")

	state, _ := ReadModeCycleState(ctx.Session, "edit")
	if state != nil {
		ctx.AssertEqual(strconv.Itoa(state.Current), "1", "direct switch set current to 1")
	}
}

// testModeDirectSwitchToEdit tests switching back directly.
func testModeDirectSwitchToEdit(ctx *UITestContext) {
	err := ModeSwitch(ctx.Session, "edit", "edit")
	ctx.AssertNoError(err, "switch directly to edit")

	state, _ := ReadModeCycleState(ctx.Session, "edit")
	if state != nil {
		ctx.AssertEqual(strconv.Itoa(state.Current), "0", "direct switch set current to 0")
	}
}

// testModeIdempotentSwitch tests switching to the already-active mode is a no-op.
func testModeIdempotentSwitch(ctx *UITestContext) {
	// Already on edit (index 0).
	err := ModeSwitch(ctx.Session, "edit", "edit")
	ctx.AssertNoError(err, "idempotent switch to edit (no-op)")

	state, _ := ReadModeCycleState(ctx.Session, "edit")
	if state != nil {
		ctx.AssertEqual(strconv.Itoa(state.Current), "0", "state unchanged after idempotent switch")
	}
}

// testModeRapidRoundTrips cycles 3 full round-trips and verifies consistency.
func testModeRapidRoundTrips(ctx *UITestContext) {
	for i := 1; i <= 3; i++ {
		label := fmt.Sprintf("rapid cycle %d", i)

		err := ModeCycle(ctx.Session, "edit")
		ctx.AssertNoError(err, label+" → auto")

		state, _ := ReadModeCycleState(ctx.Session, "edit")
		if state != nil {
			ctx.AssertEqual(strconv.Itoa(state.Current), "1", label+" state is auto")
		}

		err = ModeCycle(ctx.Session, "edit")
		ctx.AssertNoError(err, label+" → edit")

		state, _ = ReadModeCycleState(ctx.Session, "edit")
		if state != nil {
			ctx.AssertEqual(strconv.Itoa(state.Current), "0", label+" state is edit")
		}
	}
}

// testModeWindowNameStability verifies windows swap indices correctly.
// With window-swap, the edit and agent windows exchange indices.
func testModeWindowNameStability(ctx *UITestContext) {
	// Cycle to auto — windows swap indices.
	ModeCycle(ctx.Session, "edit") // → auto

	// During auto mode: auto is at index 2, edit is at auto's old index.
	autoIdx, _, autoFound := ctx.TmuxWindowInfo("auto")
	ctx.AssertTrue(autoFound, "auto window exists while in auto mode")
	ctx.AssertEqual(strconv.Itoa(autoIdx), "2", "auto at index 2 during auto mode")

	_, _, editFound := ctx.TmuxWindowInfo("edit")
	ctx.AssertTrue(editFound, "edit window still exists during auto mode")

	ModeCycle(ctx.Session, "edit") // → edit

	// After cycling back: edit is at index 2 again.
	editIdx, _, editFound := ctx.TmuxWindowInfo("edit")
	ctx.AssertTrue(editFound, "edit window exists after cycling back")
	ctx.AssertEqual(strconv.Itoa(editIdx), "2", "edit window back at index 2 after cycling back")
}
