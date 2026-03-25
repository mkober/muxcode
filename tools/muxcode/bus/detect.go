package bus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ProjectType represents a detected project type with optional metadata.
type ProjectType struct {
	Name       string            // e.g. "go", "nodejs", "python"
	Indicators []string          // which indicator files were found
	Metadata   map[string]string // extracted details (module name, scripts, etc.)
}

// indicator defines how to detect a project type.
type indicator struct {
	Name    string
	Files   []string // exact filenames to check
	Globs   []string // glob patterns to check
	Extract func(dir string) map[string]string
}

// indicators is the registry of all detectable project types.
var indicators = []indicator{
	{
		Name:    "go",
		Files:   []string{"go.mod"},
		Extract: extractGoMod,
	},
	{
		Name:    "nodejs",
		Files:   []string{"package.json"},
		Extract: extractPackageJSON,
	},
	{
		Name:  "typescript",
		Files: []string{"tsconfig.json"},
	},
	{
		Name:  "python",
		Files: []string{"pyproject.toml", "setup.py", "setup.cfg", "requirements.txt"},
	},
	{
		Name:  "rust",
		Files: []string{"Cargo.toml"},
	},
	{
		Name:    "cdk",
		Files:   []string{"cdk.json"},
		Extract: extractCdkJSON,
	},
	{
		Name:  "java-maven",
		Files: []string{"pom.xml"},
	},
	{
		Name:  "java-gradle",
		Files: []string{"build.gradle", "build.gradle.kts"},
	},
	{
		Name:  "ruby",
		Files: []string{"Gemfile"},
	},
	{
		Name:  "docker",
		Files: []string{"Dockerfile"},
	},
	{
		Name:  "terraform",
		Globs: []string{"*.tf"},
	},
	{
		Name:  "make",
		Files: []string{"Makefile"},
	},
	{
		Name:  "cpp",
		Files: []string{"CMakeLists.txt"},
		Globs: []string{"*.cpp", "*.cc"},
	},
	{
		Name:  "csharp",
		Globs: []string{"*.csproj", "*.sln"},
	},
	{
		Name:  "gdscript",
		Files: []string{"project.godot"},
	},
	{
		Name:    "php",
		Files:   []string{"composer.json"},
		Extract: extractPHP,
	},
	{
		Name:  "swift",
		Files: []string{"Package.swift"},
		Globs: []string{"*.xcodeproj"},
	},
}

// DetectProject scans dir for project type indicators and returns detected types
// sorted by name.
func DetectProject(dir string) []ProjectType {
	var detected []ProjectType

	for _, ind := range indicators {
		var found []string

		// Check exact file matches
		for _, f := range ind.Files {
			path := filepath.Join(dir, f)
			if _, err := os.Stat(path); err == nil {
				found = append(found, f)
			}
		}

		// Check glob patterns
		for _, g := range ind.Globs {
			pattern := filepath.Join(dir, g)
			matches, err := filepath.Glob(pattern)
			if err == nil && len(matches) > 0 {
				// Record just the glob pattern, not all matches
				found = append(found, g)
			}
		}

		if len(found) == 0 {
			continue
		}

		pt := ProjectType{
			Name:       ind.Name,
			Indicators: found,
		}

		if ind.Extract != nil {
			pt.Metadata = ind.Extract(dir)
		}

		detected = append(detected, pt)
	}

	sort.Slice(detected, func(i, j int) bool {
		return detected[i].Name < detected[j].Name
	})
	return detected
}

// AutoContextFiles converts detected project types into ContextFile entries
// suitable for merging with manual context files. All auto entries use
// Source="auto" and Role="shared".
func AutoContextFiles(dir string) []ContextFile {
	types := DetectProject(dir)
	var files []ContextFile

	for _, pt := range types {
		body := conventionText(pt)
		if body == "" {
			continue
		}
		files = append(files, ContextFile{
			Name:   pt.Name,
			Role:   "shared",
			Body:   body,
			Source: "auto",
		})
	}
	return files
}

// conventionText returns a short convention snippet for a detected project type.
// Metadata values are interpolated where available.
func conventionText(pt ProjectType) string {
	m := pt.Metadata
	if m == nil {
		m = map[string]string{}
	}

	switch pt.Name {
	case "go":
		s := "## Go Project\n"
		if v := m["module"]; v != "" {
			s += fmt.Sprintf("- Module: `%s`\n", v)
		}
		if v := m["go_version"]; v != "" {
			s += fmt.Sprintf("- Go version: %s\n", v)
		}
		s += "- Build: `go build ./...`\n"
		s += "- Test: `go test ./...`\n"
		s += "- Vet: `go vet ./...`\n"
		s += "- Naming: PascalCase exported, camelCase unexported"
		return s

	case "nodejs":
		s := "## Node.js Project\n"
		if v := m["name"]; v != "" {
			s += fmt.Sprintf("- Package: `%s`\n", v)
		}
		if v := m["build"]; v != "" {
			s += fmt.Sprintf("- Build: `npm run build` (%s)\n", v)
		}
		if v := m["test"]; v != "" {
			s += fmt.Sprintf("- Test: `npm test` (%s)\n", v)
		}
		s += "- Naming: camelCase functions, PascalCase classes"
		return s

	case "typescript":
		return "## TypeScript Project\n" +
			"- Compile: `tsc` or `npx tsc`\n" +
			"- Use type annotations on all public APIs\n" +
			"- Naming: camelCase functions, PascalCase types/interfaces"

	case "python":
		return "## Python Project\n" +
			"- Test: `pytest`\n" +
			"- Lint: `ruff check .` or `flake8`\n" +
			"- Format: `ruff format .` or `black .`\n" +
			"- Naming: snake_case functions/variables, PascalCase classes\n" +
			"- Use type hints on function signatures"

	case "rust":
		return "## Rust Project\n" +
			"- Build: `cargo build`\n" +
			"- Test: `cargo test`\n" +
			"- Check: `cargo clippy`\n" +
			"- Naming: snake_case functions, PascalCase types, SCREAMING_SNAKE constants"

	case "cdk":
		s := "## AWS CDK Project\n"
		if v := m["app"]; v != "" {
			s += fmt.Sprintf("- App command: `%s`\n", v)
		}
		s += "- Synth: `cdk synth`\n" +
			"- Diff: `cdk diff`\n" +
			"- Deploy: `cdk deploy`\n" +
			"- Always run build before synth"
		return s

	case "java-maven":
		return "## Java (Maven) Project\n" +
			"- Build: `mvn compile`\n" +
			"- Test: `mvn test`\n" +
			"- Package: `mvn package`\n" +
			"- Naming: camelCase methods, PascalCase classes"

	case "java-gradle":
		return "## Java (Gradle) Project\n" +
			"- Build: `./gradlew build`\n" +
			"- Test: `./gradlew test`\n" +
			"- Naming: camelCase methods, PascalCase classes"

	case "ruby":
		return "## Ruby Project\n" +
			"- Test: `bundle exec rspec` or `bundle exec rake test`\n" +
			"- Lint: `bundle exec rubocop`\n" +
			"- Naming: snake_case methods, PascalCase classes"

	case "docker":
		return "## Docker Project\n" +
			"- Build: `docker build -t <image> .`\n" +
			"- Use multi-stage builds to minimize image size\n" +
			"- Pin base image versions"

	case "terraform":
		return "## Terraform Project\n" +
			"- Init: `terraform init`\n" +
			"- Plan: `terraform plan`\n" +
			"- Apply: `terraform apply`\n" +
			"- Validate: `terraform validate`\n" +
			"- Naming: snake_case for resources and variables"

	case "make":
		return "## Make Project\n" +
			"- Build: `make` or `make build`\n" +
			"- Check Makefile for available targets"

	case "cpp":
		return "## C++ Project\n" +
			"- Build: `cmake --build .` or `make`\n" +
			"- Configure: `cmake -B build`\n" +
			"- Naming: PascalCase classes, camelCase or snake_case functions"

	case "csharp":
		return "## C# Project\n" +
			"- Build: `dotnet build`\n" +
			"- Test: `dotnet test`\n" +
			"- Naming: PascalCase methods/classes, camelCase locals"

	case "gdscript":
		return "## Godot Project\n" +
			"- Run: `godot --path .`\n" +
			"- Naming: snake_case functions, PascalCase classes/nodes"

	case "php":
		s := "## PHP Project\n"
		if v := m["name"]; v != "" {
			s += fmt.Sprintf("- Package: `%s`\n", v)
		}
		s += "- Install: `composer install`\n" +
			"- Test: `./vendor/bin/phpunit`\n" +
			"- Naming: camelCase methods, PascalCase classes"
		return s

	case "swift":
		return "## Swift Project\n" +
			"- Build: `swift build`\n" +
			"- Test: `swift test`\n" +
			"- Naming: camelCase functions/properties, PascalCase types"

	default:
		return ""
	}
}

// FormatDetectOutput formats detected project types as columnar CLI output.
func FormatDetectOutput(types []ProjectType) string {
	if len(types) == 0 {
		return "No project types detected.\n"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%-16s %-24s %s\n", "TYPE", "INDICATORS", "METADATA")
	for _, pt := range types {
		indicators := strings.Join(pt.Indicators, ", ")
		meta := ""
		if len(pt.Metadata) > 0 {
			var parts []string
			// Sort keys for deterministic output
			keys := make([]string, 0, len(pt.Metadata))
			for k := range pt.Metadata {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				parts = append(parts, k+"="+pt.Metadata[k])
			}
			meta = strings.Join(parts, ", ")
		}
		fmt.Fprintf(&b, "%-16s %-24s %s\n", pt.Name, indicators, meta)
	}
	return b.String()
}

// --- Metadata extractors ---

// extractGoMod extracts module name and go version from go.mod.
func extractGoMod(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil
	}

	m := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			m["module"] = strings.TrimSpace(strings.TrimPrefix(line, "module "))
		}
		if strings.HasPrefix(line, "go ") {
			m["go_version"] = strings.TrimSpace(strings.TrimPrefix(line, "go "))
		}
	}
	return m
}

// extractPackageJSON extracts name and script names from package.json.
func extractPackageJSON(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return nil
	}

	var pkg struct {
		Name    string            `json:"name"`
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	m := map[string]string{}
	if pkg.Name != "" {
		m["name"] = pkg.Name
	}
	if v, ok := pkg.Scripts["build"]; ok {
		m["build"] = v
	}
	if v, ok := pkg.Scripts["test"]; ok {
		m["test"] = v
	}
	return m
}

// extractCdkJSON extracts the app command from cdk.json.
func extractCdkJSON(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, "cdk.json"))
	if err != nil {
		return nil
	}

	var cdk struct {
		App string `json:"app"`
	}
	if err := json.Unmarshal(data, &cdk); err != nil {
		return nil
	}

	if cdk.App != "" {
		return map[string]string{"app": cdk.App}
	}
	return nil
}

// extractPHP extracts the package name from composer.json.
func extractPHP(dir string) map[string]string {
	data, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return nil
	}

	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil
	}

	if pkg.Name != "" {
		return map[string]string{"name": pkg.Name}
	}
	return nil
}
