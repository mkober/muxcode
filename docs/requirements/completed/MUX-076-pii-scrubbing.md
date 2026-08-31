# PII Scrubbing

Automatic PII and secret redaction for tool output in api, runner, and watch roles.

## Requirements

- Harness agents use automatic scrubbing in the executor via `ScrubPII` flag
- Claude Code agents pipe tool output through `muxcode pii-scrub`
- Patterns redacted: emails, SSN, credit card numbers (prefix-anchored), phone numbers (separator-required)
- Secret patterns redacted: AWS access keys, AWS secret keys, JWTs, generic secret/token values, dates of birth
- Scrubbing applies only to PII-sensitive roles (`api`, `runner`/`run`, `watch`)
- Redacted values replaced with descriptive placeholders (e.g. `[EMAIL_REDACTED]`, `[SSN_REDACTED]`)
- Redaction count logged to stderr when > 0

## Key files

| File | Purpose |
|------|---------|
| `bus/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, regex patterns (canonical — mirrored in harness) |
| `harness/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, regex patterns (mirror of bus/scrub.go) |
| `cmd/scrub.go` | CLI handler: `pii-scrub` (stdin → stdout pipe) |

## Status

Complete
