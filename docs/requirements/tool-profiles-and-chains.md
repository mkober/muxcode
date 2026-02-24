# Tool Profiles and Event Chains

## Purpose

Per-role tool permissions and event-driven automation chains were hardcoded in bash scripts — difficult to maintain, test, and override per-project. This feature moves tool profiles, event chains, and auto-CC configuration to a single JSON config file with three-tier resolution, making them maintainable, testable, and per-project overridable.

## Requirements

### Tool profiles

- Each agent role must have a defined set of allowed tools
- Tool definitions must support shared groups (bus operations, read-only tools, common CLI utilities) referenced by name
- Roles must compose their tool list from shared group includes plus role-specific tools
- A `cd_prefix` option must auto-generate `Bash(cd * && ...)` variants to eliminate manual duplication
- Tool lists must be resolvable via CLI for use by the agent launcher
- Tool profiles must support JSON output for programmatic consumption

### Event chains

- Build, test, and deploy events must support configurable on_success, on_failure, and on_unknown handlers
- Each handler must specify a target agent, action, message template, and message type
- Message templates must support variable substitution for exit code and command
- Chains must support a notify_analyst flag per event type for watcher routing
- Chain execution must be callable via CLI from hook scripts
- Chains must support dry-run mode for testing

### Configuration

- Config must use three-tier resolution: project-local > user-level > compiled-in defaults
- If no config file exists, compiled-in defaults must reproduce current behavior (backward compatible)
- Auto-CC roles (which roles auto-copy messages to the edit agent) must be config-driven
- Send policy per role must restrict which agents a role can directly message

## Acceptance criteria

- `tools <role>` outputs the resolved tool list for any role
- `tools <role> --json` outputs as a JSON array
- `chain <event_type> <outcome>` sends the configured chain message
- `chain <event_type> <outcome> --dry-run` shows what would be sent without sending
- A project-local config file overrides user-level and default config
- Removing all config files falls back to compiled-in defaults with no behavior change
- Auto-CC roles are read from config, not hardcoded
- Build success triggers test, test success triggers review, deploy success triggers verify
- All failures notify the edit agent with exit code and command details

## Status

Implemented
