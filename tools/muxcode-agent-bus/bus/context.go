package bus

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ContextFile represents a context file read from a context.d directory.
type ContextFile struct {
	Name    string // filename without .md extension
	Role    string // "shared" or a specific role name
	Body    string // file content
	Source  string // "project" or "user"
	Path    string // full filesystem path
}

// contextDir pairs a directory path with its source label.
type contextDir struct {
	Path   string
	Source string
}

// contextDirs returns context directories in priority order (project > user).
func contextDirs() []contextDir {
	return []contextDir{
		{Path: ContextDir(), Source: "project"},
		{Path: UserContextDir(), Source: "user"},
	}
}

// ReadContextFiles scans all context directories and returns de-duplicated context files.
// Higher-priority directories shadow lower-priority ones by (role, name) key.
// Only .md files are read; subdirectories within role dirs and other extensions are ignored.
func ReadContextFiles() ([]ContextFile, error) {
	type fileKey struct {
		role string
		name string
	}
	seen := map[fileKey]bool{}
	var files []ContextFile

	for _, dir := range contextDirs() {
		entries, err := os.ReadDir(dir.Path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}

		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			roleName := e.Name()

			// Read .md files in this role subdirectory
			roleDir := filepath.Join(dir.Path, roleName)
			roleEntries, err := os.ReadDir(roleDir)
			if err != nil {
				continue
			}

			for _, re := range roleEntries {
				if re.IsDir() || !strings.HasSuffix(re.Name(), ".md") {
					continue
				}
				name := strings.TrimSuffix(re.Name(), ".md")
				key := fileKey{role: roleName, name: name}
				if seen[key] {
					continue // shadowed by higher-priority dir
				}

				path := filepath.Join(roleDir, re.Name())

				// Skip files larger than 100KB to prevent bloated prompts
				info, err := re.Info()
				if err != nil || info.Size() > 100*1024 {
					continue
				}

				data, err := os.ReadFile(path)
				if err != nil {
					continue // skip unreadable files
				}

				seen[key] = true
				files = append(files, ContextFile{
					Name:   name,
					Role:   roleName,
					Body:   strings.TrimSpace(string(data)),
					Source: dir.Source,
					Path:   path,
				})
			}
		}
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].Role != files[j].Role {
			return files[i].Role < files[j].Role
		}
		return files[i].Name < files[j].Name
	})
	return files, nil
}

// ContextFilesForRole returns context files that apply to a given role.
// This includes all "shared" files plus files in the role-specific directory.
func ContextFilesForRole(role string) ([]ContextFile, error) {
	all, err := ReadContextFiles()
	if err != nil {
		return nil, err
	}

	var filtered []ContextFile
	for _, f := range all {
		if f.Role == "shared" || f.Role == role {
			filtered = append(filtered, f)
		}
	}
	return filtered, nil
}

// FormatContextPrompt formats context files for injection into an agent prompt.
// Output format:
//
//	## Project Context
//
//	### <name>
//	<file content>
func FormatContextPrompt(files []ContextFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Project Context\n\n")
	for i, f := range files {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n", f.Name)
		b.WriteString(f.Body)
		b.WriteString("\n")
	}
	return b.String()
}

// FormatContextList formats context files as a columnar list with header.
func FormatContextList(files []ContextFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%-24s %-16s %s\n", "NAME", "ROLE", "SOURCE")
	for _, f := range files {
		fmt.Fprintf(&b, "%-24s %-16s %s\n", f.Name, f.Role, f.Source)
	}
	return b.String()
}
