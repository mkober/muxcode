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
		"muxcode-agent-bus inbox",
		"### Send Messages",
		"muxcode-agent-bus send",
		"### Memory",
		"muxcode-agent-bus memory context",
		"### Skills",
		"muxcode-agent-bus skill",
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

func TestSharedPrompt_BuildNoSendRestrictions(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// No send policy denies — all agents report to edit
	prompt := SharedPrompt("build")
	if strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt(build) should not include send restrictions (no deny policy)")
	}
}

func TestSharedPrompt_TestNoSendRestrictions(t *testing.T) {
	SetConfig(DefaultConfig())
	defer SetConfig(nil)

	// No send policy denies — all agents report to edit
	prompt := SharedPrompt("test")
	if strings.Contains(prompt, "### Send Restrictions") {
		t.Error("SharedPrompt(test) should not include send restrictions (no deny policy)")
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
