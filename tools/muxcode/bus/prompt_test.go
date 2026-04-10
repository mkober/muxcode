package bus

import (
	"strings"
	"testing"
)

func TestSharedPrompt_ContainsRequiredSections(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	prompt := SharedPrompt("edit")

	sections := []string{
		"## Agent Coordination",
		"### Check Messages",
		"muxcode inbox",
		"### Send Messages",
		"muxcode send",
		"### Memory",
		"muxcode memory context",
		"### Skills",
		"muxcode skill",
		"### Protocol",
		"--type response --reply-to",
	}

	for _, section := range sections {
		if !strings.Contains(prompt, section) {
			t.Errorf("SharedPrompt missing required section: %q", section)
		}
	}
}

func TestSharedPrompt_ContainsTargets(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	prompt := SharedPrompt("build")
	targets := []string{"edit", "build", "test", "review", "deploy", "run", "commit", "analyze", "docs", "research"}
	for _, target := range targets {
		if !strings.Contains(prompt, target) {
			t.Errorf("SharedPrompt missing target %q", target)
		}
	}
}

func TestSharedPrompt_BuildHasSendRestrictions(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// build → test is denied by default send policy
	prompt := SharedPrompt("build")
	if !strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt(build) should include send restrictions (build → test denied)")
	}
	if !strings.Contains(prompt, "test") {
		t.Error("SharedPrompt(build) restrictions should mention test")
	}
}

func TestSharedPrompt_TestHasSendRestrictions(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// test → review is denied by default send policy
	prompt := SharedPrompt("test")
	if !strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt(test) should include send restrictions (test → review denied)")
	}
	if !strings.Contains(prompt, "review") {
		t.Error("SharedPrompt(test) restrictions should mention review")
	}
}

func TestSharedPrompt_EditNoRestrictions(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	prompt := SharedPrompt("edit")
	if strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt(edit) should not include send restrictions (no policy for edit)")
	}
}

func TestSharedPrompt_NilPolicy(t *testing.T) {
	cfg := DefaultConfig()
	cfg.SendPolicy = nil
	SetConfig(cfg)
	defer SetConfig(nil)

	prompt := SharedPrompt("build")
	if strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt should not include send restrictions with nil policy")
	}
}

func TestSharedPrompt_SingleLineWarning(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	prompt := SharedPrompt("review")
	if !strings.Contains(prompt, "single-line") {
		t.Error("SharedPrompt should contain single-line warning")
	}
}

// --- Phase 3: non-hook provider prompt instructions ---

func TestSharedPrompt_NonHookProvider_HasManualBusSection(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// OpenCode build agent should get manual bus messaging instructions
	t.Setenv("MUXCODE_BUILD_CLI", "opencode")
	prompt := SharedPrompt("build")
	if !strings.Contains(prompt, "### Manual Bus Messaging") {
		t.Error("SharedPrompt(build) with opencode provider should include Manual Bus Messaging section")
	}
	if !strings.Contains(prompt, "muxcode send edit build") {
		t.Error("SharedPrompt(build) should include build result message example")
	}
	if !strings.Contains(prompt, "muxcode send edit test") {
		t.Error("SharedPrompt(build) should include test result message example")
	}
}

func TestSharedPrompt_NonHookProvider_LocalAlsoHasManualBus(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	t.Setenv("MUXCODE_TEST_CLI", "local")
	prompt := SharedPrompt("test")
	if !strings.Contains(prompt, "### Manual Bus Messaging") {
		t.Error("SharedPrompt(test) with local provider should include Manual Bus Messaging section")
	}
}

func TestSharedPrompt_HookProvider_NoManualBusSection(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// Claude Code provider should NOT get manual bus messaging instructions
	t.Setenv("MUXCODE_BUILD_CLI", "claude")
	prompt := SharedPrompt("build")
	if strings.Contains(prompt, "### Manual Bus Messaging") {
		t.Error("SharedPrompt(build) with claude provider should NOT include Manual Bus Messaging section")
	}
}

func TestSharedPrompt_EditAgent_NoManualBusSection(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// Edit agent should never get manual bus section even with non-hook provider
	t.Setenv("MUXCODE_EDIT_CLI", "opencode")
	prompt := SharedPrompt("edit")
	if strings.Contains(prompt, "### Manual Bus Messaging") {
		t.Error("SharedPrompt(edit) should NOT include Manual Bus Messaging section (edit is the orchestrator)")
	}
}
