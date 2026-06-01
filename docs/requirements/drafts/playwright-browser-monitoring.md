# Playwright browser monitoring

Add Playwright-based browser monitoring to the watch agent. When the serve agent is running a Vite dev server, the watch agent checks the URL with a headless browser and reports console errors, warnings, and uncaught exceptions to the edit agent.

## Context

### Current state

| Aspect | Current behavior |
|--------|-----------------|
| Watch agent | Tails logs only — CloudWatch, Kubernetes, Docker, local files |
| Serve agent | Manages dev server lifecycle, writes `serve-state.json` with running server metadata |
| Event chains | `deploy → run → watch` chain exists; no `serve` chain |
| Browser checks | Not supported — no headless browser integration |
| Serve → watch link | None — the serve and watch agents don't communicate |

### Problem

Frontend dev servers (Vite, SvelteKit) can start successfully and return HTTP 200, but still have runtime issues visible only in the browser console:
- React/Vue/Svelte hydration errors
- Missing module imports that fail at runtime
- Uncaught exceptions from component initialization
- Console warnings about deprecated APIs or misconfiguration

The watch agent only monitors logs — it cannot detect browser-side issues. The serve agent verifies HTTP health but not page-level correctness.

### Goal

1. Add a Playwright browser check script that navigates to a URL, captures console errors/warnings/exceptions, and outputs a JSON report
2. Extend the watch agent with a `browser-check` action that runs the Playwright script
3. Add a `serve` event chain so hook-supporting providers trigger watch after a server starts
4. Add daemon-level serve state monitoring that periodically triggers browser checks for running Vite servers

## Design

### 1. Playwright check script (`scripts/playwright-check.js`)

A Node.js script using Playwright's Chromium browser:

```
Usage: node scripts/playwright-check.js <url> [--timeout <ms>] [--wait <ms>]

Exit codes:
  0 — clean (no issues)
  1 — issues found (errors, warnings, or exceptions)
  2 — browser/navigation failure
```

**Output**: JSON report with fields:
- `url`, `status` — target URL and HTTP status
- `errors` — array of `{text, url, line}` from `console.error`
- `warnings` — array of `{text, url, line}` from `console.warn` (noise filtered)
- `exceptions` — array of `{message, stack}` from uncaught page errors
- `total_issues` — count of all issues
- `checked_at` — ISO timestamp

**Noise filtering**: Common non-actionable warnings are excluded:
- `[vite] connecting...` (Vite HMR startup)
- `DevTools` (browser extension prompts)
- `Download the React DevTools` (React dev mode)

### 2. Watch agent browser monitoring

The watch agent definition (`agents/log-watcher.md`) gains a **Browser monitoring (Playwright)** section:

- New action: `browser-check` — runs `node scripts/playwright-check.js <url>` and reports results
- Reads `serve-state.json` to discover running server URLs
- Reports clean checks and issues to the edit agent via bus messages
- First-time setup: `npx playwright install chromium` (one-time per machine)
- Scope updated from "log tailing only" to "log tailing and browser monitoring"

### 3. Watch agent tool profile

New permissions added to the `watch` tool profile:

| Permission | Purpose |
|------------|---------|
| `Bash(npx playwright*)` | Run Playwright CLI and browser checks |
| `Bash(curl*)` | HTTP health checks alongside browser checks |
| `Read(/tmp/muxcode-bus-*/serve-state.json)` | Read serve state (scoped — not broad `/tmp` access) |
| `Read(/private/tmp/muxcode-bus-*/serve-state.json)` | macOS symlink variant |

### 4. Serve event chain

New `serve` entry in `EventChains`:

| Outcome | Action |
|---------|--------|
| `on_success` | Send `browser-check` request to watch agent with instruction to read serve-state.json for URLs |
| `on_failure` | Send failure notification to edit agent |

The chain message uses `${command}` (supported template var) — not a custom `${serve_url}` var.

### 5. Serve state integration (`bus/serve_state.go`)

New types and functions for reading the serve agent's state file:

| Export | Purpose |
|--------|---------|
| `ServeState` | Struct matching `serve-state.json` schema |
| `ServerEntry` | Individual server entry with name, command, port, pid, url, status |
| `ServeStatePath()` | Returns path to serve-state.json for a session |
| `ReadServeState()` | Reads and parses the state file (nil on missing/malformed) |
| `RunningServers()` | Filters to status="running" entries |
| `IsViteServer()` | Detects Vite servers by name, command patterns, or port (5173/5174) |

**Vite detection heuristics**:
- Name: `vite`, `svelte`, `sveltekit`
- Command contains: `vite`, `npx vite`, `pnpm dev`, `npm run dev`, `yarn dev`
- Port: 5173, 5174 (default Vite ports)
- Excluded: Astro (port 4321) — uses Vite internally but is a different framework

### 6. Daemon serve health check

New `checkServeHealth()` method in the daemon poll loop:

- **Interval**: Every 60 seconds
- **Logic**: Read `serve-state.json` → filter running Vite servers → send `browser-check` to watch
- **Dedup**: Each URL is only triggered once per 5-minute window (per-URL tracking in `serveCheckSentFor` map)
- **Guard**: Only sends if the watch agent is alive (`bus.IsAgentAlive`)
- **Logging**: Lifecycle log entry with url, name, port for each triggered check

### 7. Serve agent notification

The serve agent definition (`agents/dev-server.md`) gains step 6 in the startup sequence: after reporting success, notify the watch agent directly for immediate browser monitoring of frontend servers.

## Requirements

### Acceptance criteria

- [x] Playwright check script captures console.error, console.warn, and uncaught exceptions
- [x] Script outputs structured JSON report with exit codes 0/1/2
- [x] Common Vite/React noise warnings are filtered out
- [x] Watch agent handles `browser-check` action and runs the script
- [x] Watch agent tool profile includes Playwright and scoped serve-state read permissions
- [x] Serve event chain triggers watch on success, notifies edit on failure
- [x] Daemon detects running Vite servers from serve-state.json
- [x] Daemon deduplicates browser checks per URL (5-min window)
- [x] Daemon only triggers checks when watch agent is alive
- [x] Serve agent notifies watch after starting frontend servers
- [x] `IsViteServer()` correctly identifies Vite/SvelteKit servers and excludes Go/Flask/Django/Astro
- [x] All new Go code has unit tests
- [x] Existing tests pass (no regressions)
- [x] Test fixture React app with mode-controlled console output (clean/error/warning/exception/all)
- [x] Integration test script verifies exit codes, issue detection, message capture, and JSON structure

### Key files

| File | Purpose |
|------|---------|
| `scripts/playwright-check.js` | Headless browser check script |
| `agents/log-watcher.md` | Watch agent definition (browser monitoring section) |
| `agents/dev-server.md` | Serve agent definition (watch notification step) |
| `tools/muxcode/bus/serve_state.go` | Serve state types and reading logic |
| `tools/muxcode/bus/serve_state_test.go` | Unit tests for serve state |
| `tools/muxcode/bus/profile.go` | Watch tool profile + serve event chain |
| `tools/muxcode/daemon/daemon.go` | `checkServeHealth()` daemon method |
| `test/fixtures/vite-react-app/` | Minimal React + Vite test fixture (mode-controlled console output) |
| `scripts/test-playwright-check.sh` | Integration test — starts fixture app, runs Playwright checks per mode |
| `scripts/test-browser-monitor-e2e.sh` | E2E test — serve-state, Vite detection, browser checks, dedup, non-Vite filtering |

## Implementation

### Phase 1: Playwright check script
- [x] Create `scripts/playwright-check.js` with URL navigation, console capture, exception capture
- [x] Support `--timeout` and `--wait` CLI args
- [x] Filter common non-actionable warnings
- [x] Output structured JSON report with exit codes

### Phase 2: Serve state integration
- [x] Create `bus/serve_state.go` with `ServeState`, `ServerEntry`, `ReadServeState()`
- [x] Implement `RunningServers()` filter and `IsViteServer()` detection
- [x] Use `strings.Contains` (stdlib) for command pattern matching
- [x] Create `bus/serve_state_test.go` with tests for read, parse, filter, detection, nil safety, malformed JSON

### Phase 3: Watch agent and tool profile
- [x] Add `Bash(npx playwright*)`, `Bash(curl*)` to watch tool profile
- [x] Add scoped `Read(/tmp/muxcode-bus-*/serve-state.json)` permissions
- [x] Add browser monitoring section to `agents/log-watcher.md`
- [x] Update scope boundaries to include browser monitoring

### Phase 4: Event chain and serve agent
- [x] Add `serve` event chain with on_success → watch:browser-check
- [x] Add on_failure → edit:notify for serve failures
- [x] Add step 6 to `agents/dev-server.md` for watch notification

### Phase 5: Daemon integration
- [x] Add `checkServeHealth()` to daemon poll loop
- [x] Add `lastServeCheck` and `serveCheckSentFor` fields to Daemon struct
- [x] Implement 60s check interval with 5-min per-URL dedup
- [x] Handle `bus.Send` and `bus.Notify` errors
- [x] Add lifecycle logging for triggered checks

### Phase 6: Test fixture React app
- [x] Create `test/fixtures/vite-react-app/` — minimal React + Vite app on port 5199
- [x] App behavior controlled by `?mode=` URL parameter: `clean`, `error`, `warning`, `exception`, `all`
- [x] `clean` mode renders with zero console output (baseline)
- [x] `error` mode emits `console.error` on component mount
- [x] `warning` mode emits `console.warn` on component mount
- [x] `exception` mode throws uncaught exception via `setTimeout`
- [x] `all` mode combines error + warning + exception
- [x] Create `scripts/test-playwright-check.sh` integration test script
- [x] Test verifies: exit codes (0/1/2), issue counts, message text capture, JSON structure
- [x] Test covers all modes: clean, error, warning, exception, all, invalid URL
- [x] Add `node_modules/` to `.gitignore`

### Phase 7: End-to-end agent integration test
- [x] Create `scripts/test-browser-monitor-e2e.sh` — 7-phase e2e test script (13 assertions)
- [x] Start the fixture Vite app and verify HTTP readiness
- [x] Verify `serve-state.json` written with status="running" and name="vite"
- [x] Verify `IsViteServer` detection logic matches fixture server
- [x] Verify Playwright browser-check clean mode (exit 0, 0 issues)
- [x] Verify Playwright browser-check error mode (exit 1, console.error captured)
- [x] Verify non-Vite servers (Go on :8080, Flask on :5000) correctly excluded
- [x] Verify dedup prevents duplicate browser-check messages within window

## Status

Complete — all 7 phases implemented and verified
