# API Testing Agent

## Context

Replace the "console" tmux window (currently a dashboard TUI + bash shell, no agent) with a new "api" agent — a Postman-like API testing specialist. The api agent manages per-project collections and environments stored as JSON files in `.muxcode/api/`, executes requests via `curl`, and tracks request history. New `muxcode-agent-bus api` CLI subcommands provide structured CRUD for collections, environments, and history.

## Storage Structure

```
.muxcode/api/
├── environments/
│   ├── dev.json           # {name, base_url, headers, variables}
│   └── production.json
├── collections/
│   ├── auth.json          # {name, description, base_url, requests: [{name, method, path, headers, body, query}]}
│   └── users.json
└── history.jsonl           # Append-only log of executed requests
```

## Changes

### 1. Core data layer — `bus/api.go` (new file)

Structs: `Environment`, `Collection`, `Request`, `HistoryEntry`

Path helpers (follow `MemoryDir()`/`SkillsDir()` pattern):
- `ApiDir()` → `.muxcode/api`
- `ApiEnvDir()` → `.muxcode/api/environments`
- `ApiCollectionDir()` → `.muxcode/api/collections`
- `ApiHistoryPath()` → `.muxcode/api/history.jsonl`

CRUD functions (follow `cron.go` read/write/list pattern):
- Environment: `ListEnvironments`, `ReadEnvironment`, `CreateEnvironment`, `WriteEnvironment`, `SetEnvironmentVar`, `DeleteEnvironment`
- Collection: `ListCollections`, `ReadCollection`, `CreateCollection`, `WriteCollection`, `AddRequest`, `RemoveRequest`, `DeleteCollection`
- History: `AppendHistory`, `ReadHistory` (with collection filter + limit)
- Formatters: `FormatEnvList`, `FormatEnvDetail`, `FormatCollectionList`, `FormatCollectionDetail`, `FormatHistory`

Filename sanitization: lowercase name, hyphens for spaces, `.json` extension. Directories auto-created via `os.MkdirAll`.

### 2. Tests — `bus/api_test.go` (new file)

CRUD round-trips for environments and collections, variable set/get, request add/remove, history append/read with filtering, auto-create of missing directories, duplicate name handling.

### 3. CLI subcommand — `cmd/api.go` (new file)

Follow `cmd/cron.go` pattern — top-level `Api(args)` dispatching to sub-handlers:

```
api env list
api env get <name>
api env create <name> --base-url <url>
api env set <name> <key> <value>
api env delete <name>
api collection list
api collection get <name>
api collection create <name> [--description desc] [--base-url url]
api collection delete <name>
api collection add-request <collection> <name> --method GET --path /endpoint [--header key:value] [--body json] [--query key=value]
api collection remove-request <collection> <name>
api history [--collection name] [--limit N]
```

### 4. Register in `main.go`

Add `api` to usage string and `case "api": cmd.Api(args)` to switch.

### 5. Add "api" to `bus/config.go`

- Add `"api"` to `KnownRoles` slice (line 17)
- Add `"api": true` to `splitLeftWindows` map (line 32)

### 6. Add "api" tool profile — `bus/profile.go`

New `"api"` entry in `DefaultConfig().ToolProfiles` (after `"pr-read"`):

```go
"api": {
    Include:  []string{"bus", "readonly", "common"},
    CdPrefix: true,
    Tools: []string{
        "Bash(curl*)", "Bash(wget*)", "Bash(http*)",
        "Bash(jq*)", "Bash(python*)", "Bash(node*)",
        "Bash(openssl*)", "Bash(base64*)",
        "Bash(dig*)", "Bash(nslookup*)",
        "Write", "Edit",
    },
},
```

No role alias needed — window name = role name = profile key = `api`.

### 7. Update `muxcode.sh`

- **Line 36**: Replace `console` with `api` in default `MUXCODE_WINDOWS`
- **Line 38**: Add `api` to default `MUXCODE_SPLIT_LEFT`
- **Lines 252-259**: Remove the `if [ "$WIN" = "console" ]` block — `api` will fall through to the standard `is_split_left` handler (terminal left + agent right)

### 8. Update `scripts/muxcode-agent.sh`

- `agent_name()` (line 101): Add `api) echo "api-tester" ;;`
- Fallback prompt (before `*)` at line 209): Add `api)` case with API tester prompt

### 9. Update TUI — `tui/model.go`

- **Line 54**: Remove `&& w != "console"` filter — the `api` window is a real agent window that should appear in the dashboard. The dashboard excludes itself by not being in the window list.
- Update comments at lines 44-45.

### 10. Agent definition — `agents/api-tester.md` (new file)

Frontmatter: `description: API testing specialist — manages collections, environments, executes requests, and tracks history`

Instructions:
- Use `muxcode-agent-bus api` subcommands for collection/environment/history management
- Execute requests via `curl -s -w` with `jq` response formatting
- Support variable substitution: `{{var}}` resolved from active environment
- Support auth patterns: Bearer, Basic, API key (header or query)
- Log each request to history after execution
- Report results to edit: status code, timing, formatted response
- Operate autonomously — bus requests are the user's approval

### 11. Example collection and environment — `examples/api/` (new directory)

Ship a ready-to-use demo that targets a free public API (httpbin.org). This serves as both a test fixture and a template for users to copy into their own projects.

**`examples/api/environments/httpbin.json`**:
```json
{
  "name": "httpbin",
  "base_url": "https://httpbin.org",
  "headers": {},
  "variables": {
    "test_user": "muxcode-demo",
    "test_pass": "demo123"
  },
  "created_at": 0,
  "updated_at": 0
}
```

**`examples/api/collections/httpbin-basics.json`**:
```json
{
  "name": "httpbin-basics",
  "description": "Basic HTTP method tests against httpbin.org",
  "base_url": "https://httpbin.org",
  "requests": [
    {"name": "get-test", "method": "GET", "path": "/get", "headers": {}, "query": {"source": "muxcode"}},
    {"name": "post-json", "method": "POST", "path": "/post", "headers": {"Content-Type": "application/json"}, "body": "{\"message\": \"hello from muxcode\"}"},
    {"name": "status-codes", "method": "GET", "path": "/status/200", "headers": {}},
    {"name": "headers-echo", "method": "GET", "path": "/headers", "headers": {"X-Custom-Header": "muxcode-test"}},
    {"name": "basic-auth", "method": "GET", "path": "/basic-auth/{{test_user}}/{{test_pass}}", "headers": {}},
    {"name": "delay", "method": "GET", "path": "/delay/1", "headers": {}}
  ],
  "created_at": 0,
  "updated_at": 0
}
```

Add an `api import <dir>` subcommand to `cmd/api.go` that copies environments and collections from a source directory into `.muxcode/api/`. This lets users bootstrap from examples:
```bash
muxcode-agent-bus api import examples/api
```

Document in README and agent definition how to use the example:
```bash
# Copy example collection into your project
muxcode-agent-bus api import examples/api
# List what was imported
muxcode-agent-bus api env list
muxcode-agent-bus api collection list
```

### 12. Documentation updates

**`README.md`**:
- Agent table: add `| api | API tester | Claude Code | MUXCODE_API_CLI=local | Manages API collections, executes requests, tracks history |`
- ASCII diagram: add `api` to function key bar
- Default MUXCODE_WINDOWS references: replace `console` with `api`

**`docs/configuration.md`**:
- Line 39: Update default `MUXCODE_WINDOWS` to include `api` instead of `console`
- Line 41: Update default `MUXCODE_SPLIT_LEFT` to include `api`

**`docs/agent-bus.md`**:
- Add `muxcode-agent-bus api` section with all subcommands, data file table, examples

**`CLAUDE.md`**:
- Add `"api"` to KnownRoles listing
- Add `bus/api.go` and `cmd/api.go` to code reference tables
- Add `api` tool profile reference
- Update default MUXCODE_WINDOWS

## Verification

1. `go vet ./...` and `go test ./...` pass in the bus module
2. `make build` succeeds
3. `muxcode-agent-bus api env create dev --base-url http://localhost:8080` creates `.muxcode/api/environments/dev.json`
4. `muxcode-agent-bus api collection create auth --description "Auth API"` creates collection file
5. `muxcode-agent-bus api collection add-request auth login --method POST --path /auth/login` adds request
6. `muxcode-agent-bus api env list` and `api collection list` show created entries
7. `muxcode-agent-bus api history --limit 5` shows recent requests
8. `muxcode-agent-bus api import examples/api` imports the httpbin demo collection and environment
8. `muxcode-agent-bus tools api` shows the expected tool profile
9. Launch `muxcode` — "api" window appears with split-left layout and agent running
10. From edit agent: `muxcode-agent-bus send api request "GET https://httpbin.org/get"` executes and reports back
