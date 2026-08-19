# Project-Aware Context

Auto-detect project type and inject convention snippets into all agent prompts.

## Requirements

- Detect 17 project types via indicator files and glob patterns
- Extract metadata from `go.mod`, `package.json`, `cdk.json`, `composer.json`
- Generate convention text snippets tailored to each detected project type
- Manual `context.d/` files shadow auto-detected context by filename
- `--no-auto` flag opts out of auto-detection for a project
- Detection runs at session init and results are cached

## Key files

| File | Purpose |
|------|---------|
| `bus/detect.go` | `DetectProject()`, `AutoContextFiles()`, `conventionText()`, `FormatDetectOutput()` |
