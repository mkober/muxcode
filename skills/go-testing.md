---
name: go-testing
description: Go testing patterns and conventions
roles: [test, build]
tags: [go, testing]
---

## Test conventions

- Test files: `*_test.go` in the same package (not `_test` suffix)
- Use `t.TempDir()` for temp directories (auto-cleaned)
- Use `t.Setenv()` for environment overrides (auto-restored)
- Use `t.Helper()` in test helper functions
- Table-driven tests for multiple inputs

## Running tests

- `go test ./...` — run all tests
- `go test -v ./...` — verbose output
- `go test -run TestFoo ./pkg/` — run specific test
- `go vet ./...` — static analysis (always run before tests)

## Assertions

- stdlib only: use `if got != want` patterns
- `t.Errorf` for non-fatal, `t.Fatalf` for fatal assertions
- Include got/want in error messages: `t.Errorf("got %q, want %q", got, want)`
