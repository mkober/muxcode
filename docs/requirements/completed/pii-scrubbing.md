# PII Scrubbing

Automatic PII and secret redaction for tool output in api, runner, and watch roles.

## Requirements

- Harness agents use automatic scrubbing in the executor via `ScrubPII` flag
- Claude Code agents pipe tool output through `muxcode-pii-scrub.sh`
- Patterns redacted: emails, SSN, credit card numbers (prefix-anchored), phone numbers (separator-required)
- Secret patterns redacted: AWS access keys, JWTs, generic secret/token values
- Scrubbing applies only to PII-sensitive roles (`api`, `runner`/`run`, `watch`)
- Redacted values replaced with descriptive placeholders (e.g. `[EMAIL]`, `[SSN]`)

## Key files

| File | Purpose |
|------|---------|
| `harness/scrub.go` | `ScrubPII()`, `IsPIISensitiveRole()`, regex patterns for PII and secrets |
| `scripts/muxcode-pii-scrub.sh` | Pipe-through scrub script for Claude Code agents |
