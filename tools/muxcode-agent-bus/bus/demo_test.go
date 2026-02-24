package bus

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
	"time"
)

func TestScaleDelay(t *testing.T) {
	base := 2 * time.Second

	tests := []struct {
		speed    float64
		expected time.Duration
	}{
		{1.0, 2 * time.Second},
		{2.0, 1 * time.Second},
		{0.5, 4 * time.Second},
		{4.0, 500 * time.Millisecond},
		{0.0, 2 * time.Second},  // zero defaults to 1.0
		{-1.0, 2 * time.Second}, // negative defaults to 1.0
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("speed_%.1f", tt.speed), func(t *testing.T) {
			got := ScaleDelay(base, tt.speed)
			if got != tt.expected {
				t.Errorf("ScaleDelay(%v, %.1f) = %v, want %v", base, tt.speed, got, tt.expected)
			}
		})
	}
}

func TestScaleDelay_Zero(t *testing.T) {
	got := ScaleDelay(0, 2.0)
	if got != 0 {
		t.Errorf("ScaleDelay(0, 2.0) = %v, want 0", got)
	}
}

func TestBuiltinScenarios(t *testing.T) {
	scenarios := BuiltinScenarios()

	if len(scenarios) == 0 {
		t.Fatal("expected at least one built-in scenario")
	}

	for _, s := range scenarios {
		if s.Name == "" {
			t.Error("scenario has empty name")
		}
		if s.Description == "" {
			t.Errorf("scenario %q has empty description", s.Name)
		}
		if len(s.Steps) == 0 {
			t.Errorf("scenario %q has no steps", s.Name)
		}
	}
}

func TestBuildTestReviewScenario(t *testing.T) {
	s := BuildTestReviewScenario()

	if s.Name != "build-test-review" {
		t.Errorf("expected name 'build-test-review', got %q", s.Name)
	}

	if len(s.Steps) != 20 {
		t.Errorf("expected 20 steps, got %d", len(s.Steps))
	}

	// Verify first step is select-window to edit
	first := s.Steps[0]
	if first.Action != "select-window" || first.Window != "edit" {
		t.Errorf("expected first step to select edit window, got action=%s window=%s", first.Action, first.Window)
	}

	// Verify last step returns to edit
	last := s.Steps[len(s.Steps)-1]
	if last.Action != "select-window" || last.Window != "edit" {
		t.Errorf("expected last step to select edit window, got action=%s window=%s", last.Action, last.Window)
	}

	// Count total delay at 1.0x
	totalDelay := time.Duration(0)
	for _, step := range s.Steps {
		totalDelay += step.DelayAfter
	}
	// Expected ~20.5 seconds at 1.0x
	if totalDelay < 15*time.Second || totalDelay > 30*time.Second {
		t.Errorf("total delay %v outside expected range [15s, 30s]", totalDelay)
	}

	// Verify all actions are valid
	validActions := map[string]bool{
		"select-window": true,
		"send":          true,
		"lock":          true,
		"unlock":        true,
		"sleep":         true,
	}
	for i, step := range s.Steps {
		if !validActions[step.Action] {
			t.Errorf("step %d: invalid action %q", i+1, step.Action)
		}
		if step.Description == "" {
			t.Errorf("step %d: empty description", i+1)
		}
	}
}

func TestBuildTestReviewScenario_SendSteps(t *testing.T) {
	s := BuildTestReviewScenario()

	var sendSteps []DemoStep
	for _, step := range s.Steps {
		if step.Action == "send" {
			sendSteps = append(sendSteps, step)
		}
	}

	// Verify send steps have required fields
	for i, step := range sendSteps {
		if step.Role == "" {
			t.Errorf("send step %d: empty role", i)
		}
		if step.BusAction == "" {
			t.Errorf("send step %d: empty bus action", i)
		}
		if step.MsgType == "" {
			t.Errorf("send step %d: empty message type", i)
		}
		if step.Payload == "" {
			t.Errorf("send step %d: empty payload", i)
		}
	}
}

func TestBuildTestReviewScenario_LockUnlockBalance(t *testing.T) {
	s := BuildTestReviewScenario()

	locks := make(map[string]int)
	unlocks := make(map[string]int)

	for _, step := range s.Steps {
		switch step.Action {
		case "lock":
			locks[step.Role]++
		case "unlock":
			unlocks[step.Role]++
		}
	}

	// Every lock should have a matching unlock
	for role, count := range locks {
		if unlocks[role] != count {
			t.Errorf("role %s: %d locks but %d unlocks", role, count, unlocks[role])
		}
	}
}

func TestGetScenario(t *testing.T) {
	s, err := GetScenario("build-test-review")
	if err != nil {
		t.Fatalf("GetScenario: %v", err)
	}
	if s.Name != "build-test-review" {
		t.Errorf("expected name 'build-test-review', got %q", s.Name)
	}

	_, err = GetScenario("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent scenario")
	}
}

func TestFormatScenarioList(t *testing.T) {
	scenarios := BuiltinScenarios()
	out := FormatScenarioList(scenarios)

	if !strings.Contains(out, "Available demo scenarios") {
		t.Error("expected header in output")
	}
	if !strings.Contains(out, "build-test-review") {
		t.Error("expected scenario name in output")
	}
	if !strings.Contains(out, "steps") {
		t.Error("expected step count in output")
	}
}

func TestFormatScenarioList_Empty(t *testing.T) {
	out := FormatScenarioList(nil)
	if !strings.Contains(out, "Available demo scenarios") {
		t.Error("expected header even for empty list")
	}
}

func TestRunDemo_DryRun(t *testing.T) {
	scenario := DemoScenario{
		Name:        "test-dry",
		Description: "Test dry-run scenario",
		Steps: []DemoStep{
			{Description: "Step one", Action: "select-window", Window: "edit", DelayAfter: 100 * time.Millisecond},
			{Description: "Step two", Action: "lock", Role: "build", DelayAfter: 200 * time.Millisecond},
			{Description: "Step three", Action: "send", Role: "test", BusAction: "test", MsgType: "request", Payload: "hello", DelayAfter: 100 * time.Millisecond},
			{Description: "Step four", Action: "unlock", Role: "build", DelayAfter: 100 * time.Millisecond},
			{Description: "Step five", Action: "sleep", DelayAfter: 500 * time.Millisecond},
		},
	}

	opts := DemoOptions{
		Speed:  1.0,
		DryRun: true,
	}

	elapsed, err := RunDemo("test-session", scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo dry-run: %v", err)
	}

	// Dry-run should complete nearly instantly (no delays)
	if elapsed > 2*time.Second {
		t.Errorf("dry-run took too long: %v", elapsed)
	}
}

func TestRunDemo_WithSend(t *testing.T) {
	session := fmt.Sprintf("test-demo-send-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	scenario := DemoScenario{
		Name:        "test-send",
		Description: "Test sending messages",
		Steps: []DemoStep{
			{
				Description: "Send to build",
				Action:      "send",
				Role:        "build",
				BusAction:   "build",
				MsgType:     "request",
				Payload:     "Test message from demo",
				DelayAfter:  0,
			},
		},
	}

	opts := DemoOptions{
		Speed:    1.0,
		NoSwitch: true, // No tmux available in tests
	}

	_, err := RunDemo(session, scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}

	// Verify message was delivered to build inbox
	msgs, err := Peek(session, "build")
	if err != nil {
		t.Fatalf("Peek: %v", err)
	}

	found := false
	for _, m := range msgs {
		if m.From == "demo" && m.Action == "build" && m.Payload == "Test message from demo" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected demo message in build inbox")
	}
}

func TestRunDemo_LockUnlock(t *testing.T) {
	session := fmt.Sprintf("test-demo-lock-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })

	if err := Init(session, memDir); err != nil {
		t.Fatalf("Init: %v", err)
	}

	scenario := DemoScenario{
		Name:        "test-lock",
		Description: "Test lock/unlock",
		Steps: []DemoStep{
			{Description: "Lock build", Action: "lock", Role: "build", DelayAfter: 0},
		},
	}

	opts := DemoOptions{Speed: 1.0, NoSwitch: true}
	_, err := RunDemo(session, scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo lock: %v", err)
	}

	if !IsLocked(session, "build") {
		t.Error("expected build to be locked after lock step")
	}

	// Now unlock
	scenario.Steps = []DemoStep{
		{Description: "Unlock build", Action: "unlock", Role: "build", DelayAfter: 0},
	}

	_, err = RunDemo(session, scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo unlock: %v", err)
	}

	if IsLocked(session, "build") {
		t.Error("expected build to be unlocked after unlock step")
	}
}

func TestRunDemo_SpeedScaling(t *testing.T) {
	scenario := DemoScenario{
		Name:        "test-speed",
		Description: "Test speed scaling",
		Steps: []DemoStep{
			{Description: "Wait", Action: "sleep", DelayAfter: 200 * time.Millisecond},
			{Description: "Wait", Action: "sleep", DelayAfter: 200 * time.Millisecond},
		},
	}

	// At 10x speed, 400ms of delays should take ~40ms
	opts := DemoOptions{Speed: 10.0, DryRun: true}
	elapsed, err := RunDemo("test", scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo: %v", err)
	}

	// Dry-run doesn't sleep, so it should be very fast
	if elapsed > 1*time.Second {
		t.Errorf("expected fast dry-run, got %v", elapsed)
	}
}

func TestRunDemo_InvalidAction(t *testing.T) {
	session := fmt.Sprintf("test-demo-invalid-%d", rand.Int())
	memDir := t.TempDir()
	t.Cleanup(func() { _ = Cleanup(session) })
	_ = Init(session, memDir)

	scenario := DemoScenario{
		Name:        "test-invalid",
		Description: "Test invalid action",
		Steps: []DemoStep{
			{Description: "Bad step", Action: "explode", DelayAfter: 0},
		},
	}

	opts := DemoOptions{Speed: 1.0, NoSwitch: true}
	_, err := RunDemo(session, scenario, opts)
	if err == nil {
		t.Fatal("expected error for invalid action")
	}
	if !strings.Contains(err.Error(), "unknown action") {
		t.Errorf("expected 'unknown action' in error, got: %v", err)
	}
}

func TestRunDemo_DefaultSpeed(t *testing.T) {
	scenario := DemoScenario{
		Name:        "test-default-speed",
		Description: "Test zero speed defaults to 1.0",
		Steps:       []DemoStep{},
	}

	// Speed=0 should not panic
	opts := DemoOptions{Speed: 0, DryRun: true}
	_, err := RunDemo("test", scenario, opts)
	if err != nil {
		t.Fatalf("RunDemo with zero speed: %v", err)
	}
}
