# Cross-session window resize on client resize

Refit every window in **every** tmux session — including detached subsessions — when the attached client changes size, so a monitor-resolution or terminal resize tracks live across all muxcode sessions instead of only the one currently in view.

Implemented as a `muxcode resize` subcommand invoked by the tmux `client-resized` hook.

## Problem

### Observed behavior

The `client-resized` hook auto-refit windows after a terminal/monitor resize, but **only in the current session**. When multiple muxcode sessions ran on the same tmux server (switchable subsessions via `prefix + b → Switch Session`), a resize left every detached subsession at its old geometry — clipped — until the user switched to that subsession **and manually resized the terminal again**.

### Root cause

The original hook was an inline one-liner:

```
set-hook -g client-resized 'run-shell -b "tmux list-windows | cut -d: -f1 | xargs -I{} tmux resize-window -t :{} -A"'
```

Two independent limitations made it single-session-only:

1. **`list-windows` (no `-a`) sees only the current session**, and `resize-window -t :{}` targets the current session — so other sessions were never visited.
2. **`resize-window -A` is a no-op for a detached session.** `-A` fits a window to its *largest connected client*; a detached subsession has **no** client attached, so there is nothing to fit to. This means even a naive cross-session one-liner (`list-windows -a` + `resize-window -A`) could **not** have fixed detached subsessions — they need their target size pushed explicitly.

## Requirements

### Acceptance criteria

- [x] A monitor/terminal resize refits windows in **all** tmux sessions on the server, not just the attached one
- [x] Detached subsessions are sized correctly so switching to one shows it un-clipped with **no manual resize**
- [x] Attached sessions continue to use `resize-window -A` (status-bar-aware, honours the client's true size)
- [x] Session names containing `:` are targeted correctly (the old `cut -d:` form could not handle these)
- [x] No `#{...}` / `$var` escaping pitfalls in the tmux hook string
- [x] Behavior covered by Go unit tests
- [x] Integration test exercises the detached-subsession path
- [x] All existing tests pass (`go test ./...`), `go vet` clean

### Out of scope

- Per-client differential sizing when multiple clients of different sizes attach to different sessions (muxcode's model is a single terminal driving all sessions; the largest attached client is the reference)
- Changing tmux's `window-size` / `aggressive-resize` policy

## Technical approach

A `muxcode resize` subcommand (`bus.ResizeAllWindows()`) replaces the inline hook. Moving the logic into Go gives cross-session coverage, correct detached-session handling, no shell-escaping pitfalls, and unit-test coverage.

**Two passes**, because `resize-window -A` is a no-op for detached sessions:

1. **Attached sessions** → `resize-window -A` (ideal: accounts for the status bar and each client's true size). The first attached window is remembered as the fit-size reference.
2. **Read the fit size back** from that attached window via a targeted `display-message` *after* pass 1 (so it reflects the post-resize, un-clipped geometry — reading the pre-pass listing could capture a stale clipped size). Then **push that explicit size** to every window in detached sessions with `resize-window -x/-y`.

The explicit size on a detached session is overridden automatically the next time a client attaches (tmux re-fits on attach), so it does no harm.

If no session is attached (no reference to copy from), detached windows are left untouched.

### Key files

| File | Purpose |
|------|---------|
| `tools/muxcode/bus/resize.go` | `ResizeAllWindows()`, `listAllWindows()`, `windowSize()` — core two-pass logic |
| `tools/muxcode/bus/resize_test.go` | Unit tests (5) covering attached `-A`, detached explicit-size, no-attached skip, colon-in-session-name, attached-flag parsing |
| `tools/muxcode/cmd/resize.go` | `Resize()` subcommand handler |
| `tools/muxcode/main.go` | Dispatch `case "resize"` + usage line |
| `tools/muxcode/bus/tmux.go` | `TmuxResizeWindowToSize()` helper (explicit `-x/-y` resize) |
| `config/tmux.conf` | `client-resized` hook → `run-shell -b "muxcode resize"` + rationale comment |
| `scripts/test-resize-hook.sh` | Integration test (Phase 3b exercises the detached-subsession refit) |

Note: distinct from the pre-existing `bus/launcher.go` `ResizeWindows(session)` single-session helper run at launch.

## Implementation

### Phase 1: Core subcommand

- [x] Add `TmuxResizeWindowToSize(target, w, h)` to `bus/tmux.go`
- [x] Implement `listAllWindows()` (tab-delimited `list-windows -a`, attached flag)
- [x] Implement `windowSize(target)` (targeted `display-message` post-pass-1 read)
- [x] Implement `ResizeAllWindows()` two-pass logic
- [x] Add `cmd/resize.go` handler and wire `case "resize"` + usage into `main.go`

### Phase 2: Hook wiring

- [x] Replace the inline `client-resized` hook in `config/tmux.conf` with `run-shell -b "muxcode resize"`
- [x] Document the rationale (cross-session coverage, detached no-op, no escaping pitfalls) in a comment block

### Phase 3: Unit tests

- [x] `TestResizeAllWindows_AttachedUsesAuto` — attached windows use `-A`, no explicit size
- [x] `TestResizeAllWindows_DetachedGetsExplicitSize` — detached windows get `-x/-y` fit size
- [x] `TestResizeAllWindows_NoAttachedClientSkipsDetached` — nothing attached → no resize
- [x] `TestResizeAllWindows_SessionNameWithColon` — exact `-t foo:bar:2` target asserted
- [x] `TestListAllWindows_ParsesAttachedFlag` — attached-flag parsing

### Phase 4: Integration test

- [x] Update `scripts/test-resize-hook.sh` Phase 2 to assert the hook delegates to `muxcode resize`
- [x] Phase 3 runs `muxcode resize` as the hook action
- [x] Add Phase 3b: create a detached session clipped to 40x10, run `muxcode resize`, assert it grows to the attached client's fit size
- [x] Reap the temp session via the `EXIT` trap
- [x] Run `bash scripts/test-resize-hook.sh` in a live session and verify all phases pass

## Troubleshooting (if the issue recurs)

If a subsession is clipped after a resize again, check in order:

1. **Hook registered?** `tmux show-hooks -g | grep client-resized` should show `run-shell -b "muxcode resize"`. If missing/stale, reload: `tmux source-file ~/.config/muxcode/tmux.conf`.
2. **`muxcode resize` on PATH and current?** The hook calls the installed binary (`~/.local/bin/muxcode`). After code changes, run `./build.sh` (delegated to build agent) so `make install` refreshes it — a stale binary still ships the old behavior.
3. **Detached sessions still clipped but attached one is fine?** Confirms pass 2 isn't firing — check `ResizeAllWindows()` found an attached fit target (`fitTarget != ""`) and `windowSize()` returned `ok`. If `windowSize()` returns `0,0,false`, the `display-message` format or status-bar geometry changed.
4. **Wrong size pushed to detached sessions?** The fit size is read *after* pass 1's `-A`. If a different status-bar height or `status-format` is set per-session, the copied window size may be off — detached sessions must share the attached session's status-bar geometry for the copy to be exact.
5. **`resize-window -A` reverted to a no-op assumption?** Remember `-A` only fits to *connected* clients; never rely on it for detached sessions — they require the explicit `-x/-y` push.
6. **Run the integration test** to localize the regression: `bash scripts/test-resize-hook.sh` (Phase 3b is the cross-session check).

## Status

Complete
