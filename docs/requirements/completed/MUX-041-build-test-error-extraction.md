# Build/Test Error Extraction

Bash hooks extract error-relevant lines from failed build/test output into structured history.

## Requirements

- Build and test hooks extract error lines into an `errors` field in history JSONL
- Left-pane log views prefer the `errors` field over raw output for failed runs
- Filter out "Exit code:" noise lines from extracted errors
- Color error lines red/yellow in left-pane display
- Extraction runs only on non-zero exit codes

## Key files

| File | Purpose |
|------|---------|
| `scripts/muxcode-build-hook.sh` | Build hook with error line extraction |
| `scripts/muxcode-test-hook.sh` | Test hook with error line extraction |
| `scripts/muxcode-build-log.sh` | Build left-pane log with error-preferred display |
| `scripts/muxcode-test-log.sh` | Test left-pane log with error-preferred display |
