package bus

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSkillFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", dir, err)
	}
	path := filepath.Join(dir, name+".md")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func TestParseSkillFile(t *testing.T) {
	dir := t.TempDir()
	content := `---
name: cdk-diff
description: Run CDK diff to preview infrastructure changes
roles: [deploy, edit]
tags: [aws, cdk]
---

Always run cdk diff before deploying.
Check for unexpected deletions.
`
	writeSkillFile(t, dir, "cdk-diff", content)

	skill, err := parseSkillFile(filepath.Join(dir, "cdk-diff.md"), "project")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}

	if skill.Name != "cdk-diff" {
		t.Errorf("name: got %q, want %q", skill.Name, "cdk-diff")
	}
	if skill.Description != "Run CDK diff to preview infrastructure changes" {
		t.Errorf("description: got %q", skill.Description)
	}
	if len(skill.Roles) != 2 || skill.Roles[0] != "deploy" || skill.Roles[1] != "edit" {
		t.Errorf("roles: got %v, want [deploy edit]", skill.Roles)
	}
	if len(skill.Tags) != 2 || skill.Tags[0] != "aws" || skill.Tags[1] != "cdk" {
		t.Errorf("tags: got %v, want [aws cdk]", skill.Tags)
	}
	if !strings.Contains(skill.Body, "Always run cdk diff") {
		t.Errorf("body missing expected content: %q", skill.Body)
	}
	if skill.Source != "project" {
		t.Errorf("source: got %q, want %q", skill.Source, "project")
	}
}

func TestParseSkillFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	content := "Just a plain markdown body."
	writeSkillFile(t, dir, "plain", content)

	skill, err := parseSkillFile(filepath.Join(dir, "plain.md"), "user")
	if err != nil {
		t.Fatalf("parseSkillFile: %v", err)
	}
	if skill.Name != "plain" {
		t.Errorf("name: got %q, want %q", skill.Name, "plain")
	}
	if skill.Body != "Just a plain markdown body." {
		t.Errorf("body: got %q", skill.Body)
	}
	if skill.Source != "user" {
		t.Errorf("source: got %q, want %q", skill.Source, "user")
	}
}

func TestListSkills_Empty(t *testing.T) {
	t.Setenv("BUS_SKILLS_DIR", filepath.Join(t.TempDir(), "nope"))
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope2"))

	skills, err := ListSkills()
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

func TestListSkills_FindsSkills(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))

	writeSkillFile(t, projDir, "beta-skill", "---\nname: beta-skill\ndescription: B\nroles: []\ntags: []\n---\n\nBeta body")
	writeSkillFile(t, projDir, "alpha-skill", "---\nname: alpha-skill\ndescription: A\nroles: []\ntags: []\n---\n\nAlpha body")

	skills, err := ListSkills()
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(skills))
	}
	// Should be sorted alphabetically
	if skills[0].Name != "alpha-skill" {
		t.Errorf("first skill: got %q, want %q", skills[0].Name, "alpha-skill")
	}
	if skills[1].Name != "beta-skill" {
		t.Errorf("second skill: got %q, want %q", skills[1].Name, "beta-skill")
	}
}

func TestListSkills_ProjectShadowsUser(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	userDir := filepath.Join(t.TempDir(), "user")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", userDir)

	writeSkillFile(t, projDir, "shared-skill", "---\nname: shared-skill\ndescription: From project\nroles: []\ntags: []\n---\n\nProject version")
	writeSkillFile(t, filepath.Join(userDir, "skills"), "shared-skill", "---\nname: shared-skill\ndescription: From user\nroles: []\ntags: []\n---\n\nUser version")
	writeSkillFile(t, filepath.Join(userDir, "skills"), "user-only", "---\nname: user-only\ndescription: Only in user\nroles: []\ntags: []\n---\n\nUser only body")

	skills, err := ListSkills()
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("expected 2 skills (shadowed + user-only), got %d", len(skills))
	}

	// Find shared-skill and verify it came from project
	found := false
	for _, s := range skills {
		if s.Name == "shared-skill" {
			found = true
			if s.Source != "project" {
				t.Errorf("shared-skill source: got %q, want %q", s.Source, "project")
			}
			if s.Description != "From project" {
				t.Errorf("shared-skill description: got %q, want %q", s.Description, "From project")
			}
		}
	}
	if !found {
		t.Error("shared-skill not found in results")
	}
}

func TestSkillsForRole(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))

	writeSkillFile(t, projDir, "build-only", "---\nname: build-only\ndescription: For build\nroles: [build]\ntags: []\n---\n\nBuild stuff")
	writeSkillFile(t, projDir, "all-roles", "---\nname: all-roles\ndescription: For everyone\nroles: []\ntags: []\n---\n\nGlobal stuff")
	writeSkillFile(t, projDir, "edit-test", "---\nname: edit-test\ndescription: For edit and test\nroles: [edit, test]\ntags: []\n---\n\nEdit and test stuff")

	// Build role should get build-only + all-roles
	buildSkills, err := SkillsForRole("build")
	if err != nil {
		t.Fatalf("SkillsForRole(build): %v", err)
	}
	if len(buildSkills) != 2 {
		t.Errorf("build: expected 2 skills, got %d", len(buildSkills))
	}

	// Edit role should get all-roles + edit-test
	editSkills, err := SkillsForRole("edit")
	if err != nil {
		t.Fatalf("SkillsForRole(edit): %v", err)
	}
	if len(editSkills) != 2 {
		t.Errorf("edit: expected 2 skills, got %d", len(editSkills))
	}

	// Test role should get all-roles + edit-test
	testSkills, err := SkillsForRole("test")
	if err != nil {
		t.Fatalf("SkillsForRole(test): %v", err)
	}
	if len(testSkills) != 2 {
		t.Errorf("test: expected 2 skills, got %d", len(testSkills))
	}

	// Deploy role should get only all-roles
	deploySkills, err := SkillsForRole("deploy")
	if err != nil {
		t.Fatalf("SkillsForRole(deploy): %v", err)
	}
	if len(deploySkills) != 1 {
		t.Errorf("deploy: expected 1 skill, got %d", len(deploySkills))
	}
}

func TestLoadSkill(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))

	writeSkillFile(t, projDir, "my-skill", "---\nname: my-skill\ndescription: A test skill\nroles: [edit]\ntags: [test]\n---\n\nSkill body here")

	skill, err := LoadSkill("my-skill")
	if err != nil {
		t.Fatalf("LoadSkill: %v", err)
	}
	if skill.Name != "my-skill" {
		t.Errorf("name: got %q, want %q", skill.Name, "my-skill")
	}
	if skill.Description != "A test skill" {
		t.Errorf("description: got %q", skill.Description)
	}
}

func TestLoadSkill_NotFound(t *testing.T) {
	t.Setenv("BUS_SKILLS_DIR", filepath.Join(t.TempDir(), "nope"))
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope2"))

	_, err := LoadSkill("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent skill")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error should mention 'not found': %v", err)
	}
}

func TestSearchSkills(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))

	writeSkillFile(t, projDir, "go-testing", "---\nname: go-testing\ndescription: Go testing patterns\nroles: [test]\ntags: [go, testing]\n---\n\nRun go test ./...")
	writeSkillFile(t, projDir, "code-review", "---\nname: code-review\ndescription: Code review checklist\nroles: [review]\ntags: [review]\n---\n\nCheck for bugs")

	results, err := SearchSkills("testing", "")
	if err != nil {
		t.Fatalf("SearchSkills: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Skill.Name != "go-testing" {
		t.Errorf("expected go-testing, got %q", results[0].Skill.Name)
	}
}

func TestSearchSkills_CaseInsensitive(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)
	t.Setenv("MUXCODE_CONFIG_DIR", filepath.Join(t.TempDir(), "nope"))

	writeSkillFile(t, projDir, "cdk-deploy", "---\nname: cdk-deploy\ndescription: CDK Deployment Patterns\nroles: [deploy]\ntags: [AWS, CDK]\n---\n\nAlways run CDK diff first")

	results, err := SearchSkills("cdk", "")
	if err != nil {
		t.Fatalf("SearchSkills: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result for case-insensitive match, got %d", len(results))
	}
	if results[0].Skill.Name != "cdk-deploy" {
		t.Errorf("expected cdk-deploy, got %q", results[0].Skill.Name)
	}
}

func TestCreateSkill(t *testing.T) {
	projDir := filepath.Join(t.TempDir(), "proj")
	t.Setenv("BUS_SKILLS_DIR", projDir)

	err := CreateSkill("new-skill", "A new skill", "Do the thing", []string{"build", "test"}, []string{"go"})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}

	// Verify file was created
	path := filepath.Join(projDir, "new-skill.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	content := string(data)
	if !strings.Contains(content, "name: new-skill") {
		t.Error("missing name in frontmatter")
	}
	if !strings.Contains(content, "description: A new skill") {
		t.Error("missing description in frontmatter")
	}
	if !strings.Contains(content, "roles: [build, test]") {
		t.Error("missing roles in frontmatter")
	}
	if !strings.Contains(content, "tags: [go]") {
		t.Error("missing tags in frontmatter")
	}
	if !strings.Contains(content, "Do the thing") {
		t.Error("missing body content")
	}
}

func TestFormatSkillsPrompt(t *testing.T) {
	skills := []SkillDef{
		{
			Name:        "skill-a",
			Description: "First skill",
			Body:        "Body of skill A",
		},
		{
			Name:        "skill-b",
			Description: "Second skill",
			Body:        "Body of skill B",
		},
	}

	output := FormatSkillsPrompt(skills)
	if !strings.Contains(output, "## Available Skills") {
		t.Error("missing Available Skills header")
	}
	if !strings.Contains(output, "### Skill: skill-a") {
		t.Error("missing skill-a header")
	}
	if !strings.Contains(output, "### Skill: skill-b") {
		t.Error("missing skill-b header")
	}
	if !strings.Contains(output, "Body of skill A") {
		t.Error("missing skill-a body")
	}
	if !strings.Contains(output, "Body of skill B") {
		t.Error("missing skill-b body")
	}
}

func TestFormatSkillsPrompt_Empty(t *testing.T) {
	output := FormatSkillsPrompt(nil)
	if output != "" {
		t.Errorf("expected empty output for nil skills, got %q", output)
	}

	output = FormatSkillsPrompt([]SkillDef{})
	if output != "" {
		t.Errorf("expected empty output for empty skills, got %q", output)
	}
}

func TestParseYAMLList(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"[a, b, c]", []string{"a", "b", "c"}},
		{"[single]", []string{"single"}},
		{"[]", nil},
		{"", nil},
		{"[a,b,c]", []string{"a", "b", "c"}},
		{"[ spaced , values ]", []string{"spaced", "values"}},
	}

	for _, tt := range tests {
		got := parseYAMLList(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseYAMLList(%q): got %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseYAMLList(%q)[%d]: got %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
