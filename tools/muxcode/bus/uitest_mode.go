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

			// Kill any stale agent window from previous runs.
			if ctx.TmuxWindowExists("agent") {
				// Switch to edit first to avoid killing the active window.
				ModeSwitch(ctx.Session, "edit", "edit")
				ctx.TmuxKillWindow("agent")
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
			{"cycle edit to agent", testModeCycleEditToAgent},
			{"verify agent panes", testModeVerifyAgentPanes},
			{"cycle agent to edit", testModeCycleAgentToEdit},
			{"verify edit restored", testModeVerifyEditRestored},
			{"direct switch to agent", testModeDirectSwitchToAgent},
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

			// Kill agent window if it exists.
			if ctx.TmuxWindowExists("agent") {
				ctx.TmuxKillWindow("agent")
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
	ctx.AssertNoError(err, "cycle edit → agent")

	state, err := ReadModeCycleState(ctx.Session, "edit")
	ctx.AssertNoError(err, "read state after cycle")
	if state == nil {
		return
	}

	ctx.AssertEqual(strconv.Itoa(state.Current), "1", "current index is 1 (agent)")

	current := CurrentModeAgent(state)
	ctx.AssertTrue(current != nil && current.Mode == "agent", "current mode is agent")

	// agent window should exist.
	ctx.AssertTrue(ctx.TmuxWindowExists("agent"), "agent window created")

	// agent should have 2 panes.
	_, panes, found := ctx.TmuxWindowInfo("agent")
	ctx.AssertTrue(found && panes == 2, "agent has 2 panes")
}

// testModeVerifyAgentPanes checks that the agent window is now at the host index.
// With window-swap, each window keeps its own panes — they just exchange indices.
func testModeVerifyAgentPanes(ctx *UITestContext) {
	// Brief pause for the agent launch command to render in the pane.
	ctx.Sleep(500 * time.Millisecond)

	// agent window (now at index 2) pane 1 should have the autonomous agent.
	content := ctx.TmuxCapturePane(ctx.Session+":agent.1", 30)
	hasAgent := strings.Contains(content, "autonomous-agent") ||
		strings.Contains(content, "launch agent")
	ctx.AssertTrue(hasAgent, "agent.1 has autonomous agent")

	// edit window (swapped to agent's old index) pane 1 should still have the edit agent.
	content = ctx.TmuxCapturePane(ctx.Session+":edit.1", 30)
	ctx.AssertContains(content, "code-editor", "edit.1 still has edit agent")
}

// testModeCycleAgentToEdit cycles back from agent to edit (the critical round-trip).
func testModeCycleAgentToEdit(ctx *UITestContext) {
	err := ModeCycle(ctx.Session, "edit")
	ctx.AssertNoError(err, "cycle agent → edit")

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
	// Edit window pane 1 should have the edit agent again.
	content := ctx.TmuxCapturePane(ctx.Session+":edit.1", 30)
	ctx.AssertContains(content, "code-editor", "edit.1 has edit agent after round-trip")

	// Agent pane 1 should have the autonomous agent.
	content = ctx.TmuxCapturePane(ctx.Session+":agent.1", 30)
	hasAgent := strings.Contains(content, "autonomous-agent") ||
		strings.Contains(content, "launch agent")
	ctx.AssertTrue(hasAgent, "agent.1 has autonomous agent after round-trip")
}

// testModeDirectSwitchToAgent tests the direct switch command.
func testModeDirectSwitchToAgent(ctx *UITestContext) {
	err := ModeSwitch(ctx.Session, "edit", "agent")
	ctx.AssertNoError(err, "switch directly to agent")

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
		ctx.AssertNoError(err, label+" → agent")

		state, _ := ReadModeCycleState(ctx.Session, "edit")
		if state != nil {
			ctx.AssertEqual(strconv.Itoa(state.Current), "1", label+" state is agent")
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
	// Cycle to agent — windows swap indices.
	ModeCycle(ctx.Session, "edit") // → agent

	// During agent mode: agent is at index 2, edit is at agent's old index.
	agentIdx, _, agentFound := ctx.TmuxWindowInfo("agent")
	ctx.AssertTrue(agentFound, "agent window exists while in agent mode")
	ctx.AssertEqual(strconv.Itoa(agentIdx), "2", "agent at index 2 during agent mode")

	_, _, editFound := ctx.TmuxWindowInfo("edit")
	ctx.AssertTrue(editFound, "edit window still exists during agent mode")

	ModeCycle(ctx.Session, "edit") // → edit

	// After cycling back: edit is at index 2 again.
	editIdx, _, editFound := ctx.TmuxWindowInfo("edit")
	ctx.AssertTrue(editFound, "edit window exists after cycling back")
	ctx.AssertEqual(strconv.Itoa(editIdx), "2", "edit window back at index 2 after cycling back")
}
