package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SkillDef represents a parsed skill definition file.
type SkillDef struct {
	Name        string
	Description string
	Roles       []string // empty = applies to all roles
	Tags        []string
	Body        string
	Source      string // "project" or "user"
}

// SkillSearchResult pairs a skill with its relevance score.
type SkillSearchResult struct {
	Skill SkillDef
	Score float64
}

// skillDir pairs a directory path with its source label.
type skillDir struct {
	Path   string
	Source string
}

// skillDirs returns skill directories in priority order (project > user).
func skillDirs() []skillDir {
	return []skillDir{
		{Path: SkillsDir(), Source: "project"},
		{Path: UserSkillsDir(), Source: "user"},
	}
}

// parseSkillFile reads and parses a skill markdown file with YAML frontmatter.
func parseSkillFile(path, source string) (SkillDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return SkillDef{}, err
	}

	content := string(data)
	name := strings.TrimSuffix(filepath.Base(path), ".md")

	skill := SkillDef{
		Name:   name,
		Source: source,
	}

	// Split frontmatter from body
	if strings.HasPrefix(content, "---\n") {
		rest := content[4:] // skip opening "---\n"
		idx := strings.Index(rest, "\n---\n")
		if idx >= 0 {
			frontmatter := rest[:idx]
			skill.Body = strings.TrimSpace(rest[idx+5:]) // skip "\n---\n"
			parseFrontmatter(frontmatter, &skill)
		} else {
			skill.Body = strings.TrimSpace(content)
		}
	} else {
		skill.Body = strings.TrimSpace(content)
	}

	return skill, nil
}

// parseFrontmatter parses simple YAML key: value lines into a SkillDef.
func parseFrontmatter(text string, skill *SkillDef) {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, ":")
		if idx < 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		switch key {
		case "name":
			skill.Name = val
		case "description":
			skill.Description = val
		case "roles":
			skill.Roles = parseYAMLList(val)
		case "tags":
			skill.Tags = parseYAMLList(val)
		}
	}
}

// parseYAMLList parses a simple YAML inline list like "[a, b, c]" into a string slice.
func parseYAMLList(val string) []string {
	val = strings.TrimSpace(val)
	if val == "" || val == "[]" {
		return nil
	}
	// Strip brackets
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")
	parts := strings.Split(val, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// ListSkills scans all skill directories and returns de-duplicated skills.
// Higher-priority directories shadow lower-priority ones by name.
func ListSkills() ([]SkillDef, error) {
	seen := map[string]bool{}
	var skills []SkillDef

	for _, dir := range skillDirs() {
		entries, err := os.ReadDir(dir.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
				continue
			}
			name := strings.TrimSuffix(e.Name(), ".md")
			if seen[name] {
				continue // shadowed by higher-priority dir
			}
			skill, err := parseSkillFile(filepath.Join(dir.Path, e.Name()), dir.Source)
			if err != nil {
				continue // skip unreadable files
			}
			seen[name] = true
			skills = append(skills, skill)
		}
	}

	sort.Slice(skills, func(i, j int) bool {
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

// SkillsForRole returns skills that apply to a given role.
// Skills with an empty Roles list apply to all roles.
func SkillsForRole(role string) ([]SkillDef, error) {
	all, err := ListSkills()
	if err != nil {
		return nil, err
	}

	var filtered []SkillDef
	for _, s := range all {
		if len(s.Roles) == 0 || containsRole(s.Roles, role) {
			filtered = append(filtered, s)
		}
	}
	return filtered, nil
}

// containsRole checks if a role is in a roles list.
func containsRole(roles []string, role string) bool {
	for _, r := range roles {
		if r == role {
			return true
		}
	}
	return false
}

// LoadSkill loads a single skill by name, searching directories in priority order.
func LoadSkill(name string) (SkillDef, error) {
	for _, dir := range skillDirs() {
		path := filepath.Join(dir.Path, name+".md")
		if _, err := os.Stat(path); err != nil {
			continue
		}
		return parseSkillFile(path, dir.Source)
	}
	return SkillDef{}, fmt.Errorf("skill not found: %s", name)
}

// SearchSkills searches all skills for the given query terms.
// If roleFilter is non-empty, only skills matching that role are included.
func SearchSkills(query, roleFilter string) ([]SkillSearchResult, error) {
	all, err := ListSkills()
	if err != nil {
		return nil, err
	}

	queryTerms := strings.Fields(strings.ToLower(query))
	if len(queryTerms) == 0 {
		return nil, nil
	}

	var results []SkillSearchResult
	for _, skill := range all {
		if roleFilter != "" && len(skill.Roles) > 0 && !containsRole(skill.Roles, roleFilter) {
			continue
		}
		score := scoreSkill(skill, queryTerms)
		if score > 0 {
			results = append(results, SkillSearchResult{Skill: skill, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	return results, nil
}

// scoreSkill computes a relevance score for a skill against query terms.
// Name matches 3x, description/tags 2x, body 1x.
func scoreSkill(skill SkillDef, queryTerms []string) float64 {
	nameLower := strings.ToLower(skill.Name)
	descLower := strings.ToLower(skill.Description)
	tagsLower := strings.ToLower(strings.Join(skill.Tags, " "))
	bodyLower := strings.ToLower(skill.Body)

	var score float64
	for _, term := range queryTerms {
		score += float64(strings.Count(nameLower, term)) * 3.0
		score += float64(strings.Count(descLower, term)) * 2.0
		score += float64(strings.Count(tagsLower, term)) * 2.0
		score += float64(strings.Count(bodyLower, term))
	}
	return score
}

// CreateSkill writes a new skill file to the project-local skills directory.
func CreateSkill(name, desc, body string, roles, tags []string) error {
	dir := SkillsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", name)
	fmt.Fprintf(&b, "description: %s\n", desc)
	if len(roles) > 0 {
		fmt.Fprintf(&b, "roles: [%s]\n", strings.Join(roles, ", "))
	} else {
		b.WriteString("roles: []\n")
	}
	if len(tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(tags, ", "))
	} else {
		b.WriteString("tags: []\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(body)
	b.WriteString("\n")

	path := filepath.Join(dir, name+".md")
	return os.WriteFile(path, []byte(b.String()), 0644)
}

// FormatSkillList formats skills as a columnar list.
func FormatSkillList(skills []SkillDef) string {
	var b strings.Builder
	for _, s := range skills {
		roles := "*"
		if len(s.Roles) > 0 {
			roles = strings.Join(s.Roles, ",")
		}
		fmt.Fprintf(&b, "%-24s %-16s %s\n", s.Name, roles, s.Description)
	}
	return b.String()
}

// FormatSkillSearchResults formats search results with scores.
func FormatSkillSearchResults(results []SkillSearchResult) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n")
		}
		roles := "*"
		if len(r.Skill.Roles) > 0 {
			roles = strings.Join(r.Skill.Roles, ",")
		}
		fmt.Fprintf(&b, "--- %s [%s] score:%.1f ---\n", r.Skill.Name, roles, r.Score)
		b.WriteString(r.Skill.Description)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatSkillPrompt formats a single skill for injection into an agent prompt.
func FormatSkillPrompt(skill SkillDef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "### Skill: %s\n", skill.Name)
	b.WriteString(skill.Description)
	b.WriteString("\n\n")
	b.WriteString(skill.Body)
	b.WriteString("\n")
	return b.String()
}

// FormatSkillsPrompt formats all role-applicable skills for prompt injection.
func FormatSkillsPrompt(skills []SkillDef) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Available Skills\n\n")
	for i, s := range skills {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(FormatSkillPrompt(s))
	}
	return b.String()
}
