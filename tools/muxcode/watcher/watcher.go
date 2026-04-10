package watcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Watcher monitors agent inboxes and a trigger file for file-edit events.
type Watcher struct {
	session          string
	pollInterval     time.Duration
	debounceSecs     int
	triggerFile      string
	inboxSizes       map[string]int64
	lastTriggerSize  int64
	pendingSince     int64
	cronEntries      []bus.CronEntry
	lastCronLoad     int64
	lastLoopCheck    int64
	lastCompactCheck int64
	lastAlertKey     map[string]int64
	hasRunningProcs  bool
	hasRunningSpawns bool
	lastProcSize     int64
	lastSpawnSize    int64
	// Ollama health monitoring
	ollamaRoles     []string // populated once in New()
	lastOllamaCheck int64    // 30s interval
	ollamaFailCount int      // consecutive probe failures
	ollamaWasDown   bool     // for recovery detection
	ollamaRestarts  int      // cap at 3 to prevent restart loops
	ollamaURL       string   // Ollama base URL
	ollamaModel     string   // Ollama model name
	// Agent health monitoring
	lastAgentHealthCheck int64           // 30s interval
	agentFailCounts      map[string]int  // consecutive failures per role
	agentRestarts        map[string]int  // restart count per role (cap at 3)
	agentWasDown         map[string]bool // for recovery detection
	// Delivery/task cleanup
	lastCleanupCheck int64 // 300s interval
	// Idle agent wake-up
	lastIdleCheck   int64            // 5s interval
	lastNonHookWake map[string]int64 // cooldown: last wake time per non-hook role (60s)
}

// New creates a new Watcher for the given session.
func New(session string, pollSecs, debounceSecs int) *Watcher {
	now := time.Now().Unix()

	// Discover which roles use local LLM
	ollamaRoles := bus.LocalLLMRoles()

	// Read Ollama config for health probes
	ollamaCfg := bus.DefaultOllamaConfig()

	return &Watcher{
		session:              session,
		pollInterval:         time.Duration(pollSecs) * time.Second,
		debounceSecs:         debounceSecs,
		triggerFile:          bus.TriggerFile(session),
		inboxSizes:           make(map[string]int64),
		lastAlertKey:         make(map[string]int64),
		lastLoopCheck:        now, // skip first interval — avoids stale alerts on startup
		lastCompactCheck:     now, // skip first interval — avoids stale alerts on startup
		lastOllamaCheck:      now, // skip first interval
		ollamaRoles:          ollamaRoles,
		ollamaURL:            ollamaCfg.BaseURL,
		ollamaModel:          ollamaCfg.Model,
		lastAgentHealthCheck: now, // skip first interval
		agentFailCounts:      make(map[string]int),
		agentRestarts:        make(map[string]int),
		agentWasDown:         make(map[string]bool),
		lastNonHookWake:      make(map[string]int64),
	}
}

// acquireWatcherLock ensures only one watcher runs per session.
// Uses flock on a lock file for race-free single-instance enforcement.
// Returns an unlock function, or an error if another watcher is already running.
func acquireWatcherLock(session string) (func(), error) {
	lockPath := filepath.Join(bus.BusDir(session), "lock", "watcher.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot open watcher lock: %w", err)
	}

	// Non-blocking exclusive lock — fails immediately if another watcher holds it
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another watcher is already running for session %s", session)
	}

	// Write our PID for diagnostics (the flock is the real guard, not the PID)
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = f.WriteString(strconv.Itoa(os.Getpid()))

	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// Run starts the main watcher loop. It never returns under normal operation.
// Acquires a per-session flock to prevent duplicate watcher processes — stale
// watchers from previous session starts cause duplicate tmux notifications.
func (w *Watcher) Run() error {
	busDir := bus.BusDir(w.session)

	// Single-instance enforcement: exit immediately if another watcher is running
	unlock, err := acquireWatcherLock(w.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s\n", err)
		bus.LogLifecycle(w.session, "error", "watcher", "lock-failed", err.Error())
		return err
	}
	defer unlock()

	bus.LogLifecycleWithPID(w.session, "info", "watcher", "started",
		fmt.Sprintf("Poll: %ds, Debounce: %ds", int(w.pollInterval.Seconds()), w.debounceSecs),
		os.Getpid())

	fmt.Println("  Agent Bus Watcher")
	fmt.Printf("  Session: %s\n", w.session)
	fmt.Printf("  Bus: %s\n", busDir)
	fmt.Printf("  Trigger: %s\n", w.triggerFile)
	fmt.Printf("  Poll: %ds  Debounce: %ds\n", int(w.pollInterval.Seconds()), w.debounceSecs)
	if len(w.ollamaRoles) > 0 {
		fmt.Printf("  Ollama monitoring: %s (roles: %s)\n", w.ollamaURL, strings.Join(w.ollamaRoles, ", "))
	}
	fmt.Println()

	for {
		w.touchKeepalive()
		w.checkInboxes()
		w.checkTrigger()
		w.checkCron()
		w.checkProcs()
		w.checkSpawns()
		w.checkLoops()
		w.checkCompaction()
		w.checkOllama()
		w.checkAgentHealth()
		w.checkIdleAgents()
		w.checkCleanup()
		time.Sleep(w.pollInterval)
	}
}

// refreshInboxSizes updates the tracked inbox sizes without sending notifications.
// Call this after programmatically adding messages to prevent checkInboxes from
// re-notifying for messages that were already handled.
func (w *Watcher) refreshInboxSizes() {
	for _, role := range bus.KnownRoles {
		inboxPath := bus.InboxPath(w.session, role)
		info, err := os.Stat(inboxPath)
		if err != nil {
			w.inboxSizes[role] = 0
			continue
		}
		w.inboxSizes[role] = info.Size()
	}
}

// checkInboxes polls all agent inboxes for new messages.
// When inbox growth is detected, writes the trigger file (via Notify) so
// agents running `muxcode inbox --poll` pick up the messages. Also sends
// a display-message for human visibility.
//
// cmd/send.go already calls Notify() for direct recipients. The watcher
// catches messages that arrive without a Notify (e.g. auto-CC, hooks).
func (w *Watcher) checkInboxes() {
	for _, role := range bus.KnownRoles {
		inboxPath := bus.InboxPath(w.session, role)
		info, err := os.Stat(inboxPath)
		if err != nil {
			w.inboxSizes[role] = 0
			continue
		}

		size := info.Size()
		prev := w.inboxSizes[role]

		if size > prev && size > 0 {
			// Workflow: detect review→edit messages for reviewed transition
			if role == "edit" && bus.HasNewMessageFrom(w.session, "edit", "review") {
				bus.TransitionWorkflow(w.session, bus.StateReviewed, "watcher:review-complete",
					bus.WithOutcome("review", "complete"))
			}

			// Notify writes the trigger file and optionally sends a
			// display-message. Dedup is handled inside Notify via
			// file locking + cooldown.
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  New message(s) for %s — notifying\n", ts, role)
			bus.LogLifecycle(w.session, "info", "watcher", "inbox-notify", role)
			_ = bus.Notify(w.session, role)
		}

		w.inboxSizes[role] = size
	}
}

// checkTrigger monitors the trigger file for file-edit events with debouncing.
func (w *Watcher) checkTrigger() {
	info, err := os.Stat(w.triggerFile)
	if err != nil || info.Size() == 0 {
		return
	}

	size := info.Size()
	now := time.Now().Unix()

	if size != w.lastTriggerSize {
		if w.pendingSince == 0 {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  Claude edits detected, waiting to stabilize...\n", ts)
		}
		w.pendingSince = now
		w.lastTriggerSize = size
	} else if w.pendingSince > 0 {
		elapsed := now - w.pendingSince
		if elapsed >= int64(w.debounceSecs) {
			w.routeTrigger()
			// Truncate the trigger file
			f, err := os.OpenFile(w.triggerFile, os.O_WRONLY|os.O_TRUNC, 0644)
			if err == nil {
				f.Close()
			}
			w.pendingSince = 0
			w.lastTriggerSize = 0
		}
	}
}

// routeTrigger reads the trigger file, extracts unique file paths, and sends
// an aggregate analyze event. Individual file routing (test/deploy/build) is
// handled by claude-teach-hook.sh to avoid duplicate messages.
func (w *Watcher) routeTrigger() {
	f, err := os.Open(w.triggerFile)
	if err != nil {
		return
	}
	defer f.Close()

	// Collect unique file paths
	seen := make(map[string]bool)
	var files []string

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Format: "timestamp filepath" — split by first space
		parts := strings.SplitN(line, " ", 2)
		var fp string
		if len(parts) == 2 {
			fp = strings.TrimSpace(parts[1])
		} else {
			fp = parts[0]
		}
		if fp != "" && !seen[fp] {
			seen[fp] = true
			files = append(files, fp)
		}
	}

	if len(files) == 0 {
		return
	}

	ts := time.Now().Format("15:04:05")
	fmt.Printf("  %s  Edits stabilized — routing %d file(s)\n", ts, len(files))
	bus.LogLifecycle(w.session, "info", "watcher", "trigger-route",
		fmt.Sprintf("%d file(s): %s", len(files), strings.Join(files, ", ")))

	// Workflow: transition to analyzing
	bus.TransitionWorkflow(w.session, bus.StateAnalyzing, "watcher:analyze-route",
		bus.WithFiles(files))

	// Send aggregate event to analyze agent
	fileList := strings.Join(files, ", ")
	analyzePayload := fmt.Sprintf("Claude edited files: %s — Read those files and explain what was changed and why.", fileList)
	msg := bus.NewMessage("watcher", "analyze", "event", "analyze", analyzePayload, "")
	if err := bus.Send(w.session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "  [route] failed to send analyze event: %v\n", err)
		return
	}

	// Notify the analyze agent
	if err := bus.Notify(w.session, "analyze"); err != nil {
		fmt.Fprintf(os.Stderr, "  [route] failed to notify analyze: %v\n", err)
	}

	// Refresh inbox sizes so checkInboxes doesn't re-notify for the
	// message we just sent (prevents double notification).
	w.refreshInboxSizes()
}

// loadCron reloads cron entries from disk at most once per 10 seconds.
// Skips loading if the cron file is empty or missing.
func (w *Watcher) loadCron() {
	now := time.Now().Unix()
	if now-w.lastCronLoad < 10 {
		return
	}

	// Skip if cron file is empty or missing
	info, err := os.Stat(bus.CronPath(w.session))
	if err != nil || info.Size() == 0 {
		w.cronEntries = nil
		w.lastCronLoad = now
		return
	}

	entries, err := bus.ReadCronEntries(w.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [cron] failed to read cron entries: %v\n", err)
		return
	}
	w.cronEntries = entries
	w.lastCronLoad = now
}

// checkCron iterates cached cron entries, fires due ones, and updates state.
func (w *Watcher) checkCron() {
	w.loadCron()

	now := time.Now().Unix()
	fired := false
	for _, entry := range w.cronEntries {
		if !bus.CronDue(entry, now) {
			continue
		}

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Cron firing: %s → %s:%s\n", ts, entry.ID, entry.Target, entry.Action)
		bus.LogLifecycle(w.session, "info", "watcher", "cron-fire",
			fmt.Sprintf("%s → %s:%s", entry.ID, entry.Target, entry.Action))

		msgID, err := bus.ExecuteCron(w.session, entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to execute %s: %v\n", entry.ID, err)
			continue
		}

		fired = true

		// Update last run timestamp
		if err := bus.UpdateLastRun(w.session, entry.ID, now); err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to update last_run for %s: %v\n", entry.ID, err)
		}

		// Append history
		histEntry := bus.CronHistoryEntry{
			CronID:    entry.ID,
			TS:        now,
			MessageID: msgID,
			Target:    entry.Target,
			Action:    entry.Action,
		}
		if err := bus.AppendCronHistory(w.session, histEntry); err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to append history for %s: %v\n", entry.ID, err)
		}

		// Notify target agent (harness panes are skipped inside Notify)
		if err := bus.Notify(w.session, entry.Target); err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to notify %s: %v\n", entry.Target, err)
		}
	}

	if fired {
		// Refresh inbox sizes after cron messages to prevent double notification
		w.refreshInboxSizes()
		// Force cron reload on next cycle so updated last_run_ts values are picked up
		w.lastCronLoad = 0
	}
}

// checkProcs polls running background processes and notifies owners on completion.
// Skips entirely if proc file is empty/missing and no running procs are tracked.
func (w *Watcher) checkProcs() {
	// Skip if proc file is empty/missing and no running procs cached
	info, err := os.Stat(bus.ProcPath(w.session))
	currentSize := int64(0)
	if err == nil {
		currentSize = info.Size()
	}
	if currentSize == 0 && !w.hasRunningProcs {
		return
	}
	// Reset running flag if file size changed (new proc may have been added)
	if currentSize != w.lastProcSize {
		w.hasRunningProcs = true
		w.lastProcSize = currentSize
	}

	completed, err := bus.RefreshProcStatus(w.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [proc] failed to refresh proc status: %v\n", err)
		return
	}

	// Update running state: check if any procs are still running
	entries, _ := bus.ReadProcEntries(w.session)
	hasRunning := false
	for _, e := range entries {
		if e.Status == "running" {
			hasRunning = true
			break
		}
	}
	w.hasRunningProcs = hasRunning

	if len(completed) == 0 {
		return
	}

	for _, entry := range completed {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Process completed: %s (status: %s, exit: %d)\n",
			ts, entry.ID, entry.Status, entry.ExitCode)
		bus.LogLifecycle(w.session, "info", "watcher", "proc-complete",
			fmt.Sprintf("%s status=%s exit=%d", entry.ID, entry.Status, entry.ExitCode))

		payload := fmt.Sprintf("Background process completed: %s\n  Command: %s\n  Status: %s  Exit code: %d\n  Log: %s",
			entry.ID, entry.Command, entry.Status, entry.ExitCode, entry.LogFile)

		msg := bus.NewMessage("proc", entry.Owner, "event", "proc-complete", payload, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [proc] failed to send completion event to %s: %v\n", entry.Owner, err)
			continue
		}

		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(w.session, entry.Owner); err != nil {
			fmt.Fprintf(os.Stderr, "  [proc] failed to notify %s: %v\n", entry.Owner, err)
		}

		// Mark as notified
		_ = bus.UpdateProcEntry(w.session, entry.ID, func(e *bus.ProcEntry) {
			e.Notified = true
		})
	}

	w.refreshInboxSizes()
}

// checkSpawns polls running spawned agents and notifies owners on completion.
// Skips entirely if spawn file is empty/missing and no running spawns are tracked.
func (w *Watcher) checkSpawns() {
	// Skip if spawn file is empty/missing and no running spawns cached
	info, err := os.Stat(bus.SpawnPath(w.session))
	currentSize := int64(0)
	if err == nil {
		currentSize = info.Size()
	}
	if currentSize == 0 && !w.hasRunningSpawns {
		return
	}
	// Reset running flag if file size changed (new spawn may have been added)
	if currentSize != w.lastSpawnSize {
		w.hasRunningSpawns = true
		w.lastSpawnSize = currentSize
	}

	completed, err := bus.RefreshSpawnStatus(w.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [spawn] failed to refresh spawn status: %v\n", err)
		return
	}

	// Update running state: check if any spawns are still running
	entries, _ := bus.ReadSpawnEntries(w.session)
	hasRunning := false
	for _, e := range entries {
		if e.Status == "running" {
			hasRunning = true
			break
		}
	}
	w.hasRunningSpawns = hasRunning

	if len(completed) == 0 {
		return
	}

	for _, entry := range completed {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Spawn completed: %s (role: %s, window: %s)\n",
			ts, entry.ID, entry.Role, entry.Window)
		bus.LogLifecycle(w.session, "info", "watcher", "spawn-complete",
			fmt.Sprintf("%s role=%s window=%s", entry.ID, entry.Role, entry.Window))

		// Try to extract the last result message from the spawn
		resultInfo := "No result message found."
		if result, ok := bus.GetSpawnResult(w.session, entry.SpawnRole); ok {
			resultInfo = result.Payload
			if len(resultInfo) > 200 {
				resultInfo = resultInfo[:200] + "..."
			}
		}

		payload := fmt.Sprintf("Spawned agent completed: %s\n  Role: %s  Spawn Role: %s\n  Task: %s\n  Result: %s",
			entry.ID, entry.Role, entry.SpawnRole, entry.Task, resultInfo)

		msg := bus.NewMessage("spawn", entry.Owner, "event", "spawn-complete", payload, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] failed to send completion event to %s: %v\n", entry.Owner, err)
			continue
		}

		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(w.session, entry.Owner); err != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] failed to notify %s: %v\n", entry.Owner, err)
		}

		// Mark as notified
		_ = bus.UpdateSpawnEntry(w.session, entry.ID, func(e *bus.SpawnEntry) {
			e.Notified = true
		})
	}

	w.refreshInboxSizes()
}

// checkLoops runs loop detection every 60 seconds and sends alerts to the edit agent.
// Deduplicates alerts within a 10-minute cooldown to avoid spamming.
func (w *Watcher) checkLoops() {
	now := time.Now().Unix()
	if now-w.lastLoopCheck < 60 {
		return
	}
	w.lastLoopCheck = now

	alerts := bus.CheckAllLoops(w.session)
	if len(alerts) == 0 {
		return
	}

	// Filter out alerts that were already sent within the cooldown window.
	// Cooldown (600s) must exceed detection window (300s) to prevent
	// loop-detected events from sustaining their own detection window.
	fresh := bus.FilterNewAlerts(alerts, w.lastAlertKey, 600)
	if len(fresh) == 0 {
		return
	}

	for _, alert := range fresh {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Loop detected: %s (%s)\n", ts, alert.Role, alert.Type)
		bus.LogLifecycle(w.session, "warn", "watcher", "loop-detected",
			fmt.Sprintf("%s type=%s", alert.Role, alert.Type))

		msg := bus.NewMessage("watcher", "edit", "event", "loop-detected", alert.Message, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to send loop alert: %v\n", err)
			continue
		}
		// Notify edit via display-message (passive status bar flash)
		if err := bus.Notify(w.session, "edit"); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to notify edit: %v\n", err)
		}
	}

	w.refreshInboxSizes()
}

// checkCompaction runs compaction checks every 120 seconds and sends recommendations
// to the role itself. Deduplicates alerts within a 10-minute cooldown.
func (w *Watcher) checkCompaction() {
	now := time.Now().Unix()
	if now-w.lastCompactCheck < 120 {
		return
	}
	w.lastCompactCheck = now

	th := bus.DefaultCompactThresholds()
	alerts := bus.CheckCompaction(w.session, th)
	if len(alerts) == 0 {
		return
	}

	// Filter out alerts that were already sent within the cooldown window (600s = 10 min)
	fresh := bus.FilterNewCompactAlerts(alerts, w.lastAlertKey, 600)
	if len(fresh) == 0 {
		return
	}

	for _, alert := range fresh {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Compact recommended: %s (total: %s)\n", ts, alert.Role, formatWatcherBytes(alert.TotalBytes))
		bus.LogLifecycle(w.session, "warn", "watcher", "compact-alert",
			fmt.Sprintf("%s total=%s", alert.Role, formatWatcherBytes(alert.TotalBytes)))

		msg := bus.NewMessage("watcher", alert.Role, "event", "compact-recommended", alert.Message, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [compact] failed to send compact alert to %s: %v\n", alert.Role, err)
			continue
		}
		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(w.session, alert.Role); err != nil {
			fmt.Fprintf(os.Stderr, "  [compact] failed to notify %s: %v\n", alert.Role, err)
		}
	}

	w.refreshInboxSizes()
}

// checkOllama runs Ollama health probes every 30 seconds for roles using local LLM.
// Detection timeline: 30s first probe, 60s alert, 90s restart attempt.
// Caps automatic restarts at 3 to prevent restart loops.
func (w *Watcher) checkOllama() {
	if len(w.ollamaRoles) == 0 {
		return
	}

	now := time.Now().Unix()
	if now-w.lastOllamaCheck < 30 {
		return
	}
	w.lastOllamaCheck = now

	// Run inference probe
	err := bus.CheckOllamaInference(w.ollamaURL, w.ollamaModel, bus.OllamaProbeTimeout)

	// Also check for agent failure sentinels
	hasSentinels := bus.HasOllamaFailSentinel(w.session)

	ts := time.Now().Format("15:04:05")

	if err == nil && !hasSentinels {
		// Healthy
		if w.ollamaWasDown {
			// Recovery detected
			fmt.Printf("  %s  Ollama recovered — inference probe healthy\n", ts)
			bus.LogLifecycle(w.session, "info", "watcher", "ollama-recovered", "")
			w.ollamaWasDown = false
			w.ollamaFailCount = 0

			alert := bus.FormatOllamaAlert("recovered", w.ollamaRoles, "Ollama is responsive again")
			msg := bus.NewMessage("watcher", "edit", "event", "ollama-recovered", alert, "")
			if sendErr := bus.Send(w.session, msg); sendErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to send recovery alert: %v\n", sendErr)
			}
			w.refreshInboxSizes()
		}
		return
	}

	// Unhealthy
	w.ollamaFailCount++
	errMsg := ""
	if err != nil {
		errMsg = err.Error()
	}
	if hasSentinels {
		if errMsg != "" {
			errMsg += "; agent failure sentinels detected"
		} else {
			errMsg = "agent failure sentinels detected"
		}
	}

	fmt.Printf("  %s  Ollama probe failure #%d: %s\n", ts, w.ollamaFailCount, errMsg)
	bus.LogLifecycle(w.session, "warn", "watcher", "ollama-probe-fail",
		fmt.Sprintf("failure #%d: %s", w.ollamaFailCount, errMsg))

	// Second consecutive failure (60s) — send ollama-down alert
	if w.ollamaFailCount == 2 && !w.ollamaWasDown {
		w.ollamaWasDown = true

		// Dedup via lastAlertKey with 600s cooldown
		alertKey := bus.OllamaHealthAlertKey("down")
		if lastTS, ok := w.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
			w.lastAlertKey[alertKey] = now
			alert := bus.FormatOllamaAlert("down", w.ollamaRoles, errMsg)
			msg := bus.NewMessage("watcher", "edit", "event", "ollama-down", alert, "")
			if sendErr := bus.Send(w.session, msg); sendErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to send down alert: %v\n", sendErr)
			}
			w.refreshInboxSizes()
		}
	}

	// Third consecutive failure (90s) — attempt restart
	if w.ollamaFailCount == 3 {
		if w.ollamaRestarts >= 3 {
			// Cap reached — periodic alerts only
			alertKey := bus.OllamaHealthAlertKey("down")
			if lastTS, ok := w.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
				w.lastAlertKey[alertKey] = now
				alert := bus.FormatOllamaAlert("down", w.ollamaRoles,
					fmt.Sprintf("Restart cap (3) reached. %s. Manual intervention required.", errMsg))
				msg := bus.NewMessage("watcher", "edit", "event", "ollama-down", alert, "")
				_ = bus.Send(w.session, msg)
				w.refreshInboxSizes()
			}
			return
		}

		fmt.Printf("  %s  Attempting Ollama restart (#%d)...\n", ts, w.ollamaRestarts+1)
		bus.LogLifecycle(w.session, "warn", "watcher", "ollama-restart",
			fmt.Sprintf("attempt %d/3", w.ollamaRestarts+1))
		w.ollamaRestarts++

		// Send restarting alert
		alert := bus.FormatOllamaAlert("restarting", w.ollamaRoles,
			fmt.Sprintf("Attempt %d/3 — killing and restarting ollama serve", w.ollamaRestarts))
		msg := bus.NewMessage("watcher", "edit", "event", "ollama-restarting", alert, "")
		_ = bus.Send(w.session, msg)
		w.refreshInboxSizes()

		// Attempt restart with 30s timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		restartErr := bus.RestartOllama(ctx, w.ollamaURL)
		cancel()

		if restartErr != nil {
			fmt.Fprintf(os.Stderr, "  [ollama] restart failed: %v\n", restartErr)
			return
		}

		fmt.Printf("  %s  Ollama restarted successfully, relaunching agents...\n", ts)

		// Relaunch affected agents
		for _, role := range w.ollamaRoles {
			if restartErr := bus.RestartLocalAgent(w.session, role); restartErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to restart agent %s: %v\n", role, restartErr)
			} else {
				fmt.Printf("  %s  Relaunched agent: %s\n", ts, role)
			}
		}

		// Reset fail count to let the next probe cycle detect recovery
		w.ollamaFailCount = 0
	}
}

// touchKeepalive writes the current timestamp to the watcher keepalive file.
// Called at the top of each poll loop iteration so the watcher monitor can
// detect if the watcher process has died or become stuck.
func (w *Watcher) touchKeepalive() {
	bus.TouchKeepalive(w.session)
}

// checkAgentHealth probes agent liveness every 30 seconds using a 3-strike
// escalation pattern: log → alert edit → restart (capped at 3 restarts).
// Excludes edit and webhook roles. Respects intentional stop markers.
func (w *Watcher) checkAgentHealth() {
	now := time.Now().Unix()
	if now-w.lastAgentHealthCheck < 30 {
		return
	}
	w.lastAgentHealthCheck = now

	ts := time.Now().Format("15:04:05")

	for _, role := range bus.KnownRoles {
		// Skip excluded roles and spawn roles
		if bus.IsAgentHealthExcluded(role) || bus.IsSpawnRole(role) {
			continue
		}

		// Skip intentionally stopped agents
		if bus.IsAgentStopped(w.session, role) {
			continue
		}

		alive := bus.IsAgentAlive(w.session, role)

		if alive {
			// Recovery detection
			if w.agentWasDown[role] {
				fmt.Printf("  %s  Agent %s recovered\n", ts, role)
				bus.LogLifecycle(w.session, "info", "watcher", "agent-recovered", role)
				w.agentWasDown[role] = false
				w.agentFailCounts[role] = 0

				alert := bus.FormatAgentHealthAlert("recovered", role, "Agent is responsive again")
				msg := bus.NewMessage("watcher", "edit", "event", "agent-recovered", alert, "")
				_ = bus.Send(w.session, msg)
				w.refreshInboxSizes()
			}
			continue
		}

		// Agent appears dead — increment fail count
		w.agentFailCounts[role]++
		count := w.agentFailCounts[role]

		fmt.Printf("  %s  Agent %s health check failure #%d\n", ts, role, count)
		bus.LogLifecycle(w.session, "warn", "watcher", "agent-health-fail",
			fmt.Sprintf("%s failure #%d", role, count))

		// Strike 2 (60s) — alert edit
		if count == 2 {
			w.agentWasDown[role] = true

			alertKey := bus.AgentHealthAlertKey(role, "down")
			if lastTS, ok := w.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
				w.lastAlertKey[alertKey] = now
				alert := bus.FormatAgentHealthAlert("down", role, "Agent pane shows bare shell prompt")
				msg := bus.NewMessage("watcher", "edit", "event", "agent-down", alert, "")
				if err := bus.Send(w.session, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  [agent-health] failed to send down alert for %s: %v\n", role, err)
				}
				w.refreshInboxSizes()
			}
		}

		// Strike 3 (90s) — attempt restart
		if count == 3 {
			if w.agentRestarts[role] >= 3 {
				// Cap reached — alert-only mode
				alertKey := bus.AgentHealthAlertKey(role, "down")
				if lastTS, ok := w.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
					w.lastAlertKey[alertKey] = now
					alert := bus.FormatAgentHealthAlert("down", role,
						fmt.Sprintf("Restart cap (3) reached. Manual intervention required."))
					msg := bus.NewMessage("watcher", "edit", "event", "agent-down", alert, "")
					_ = bus.Send(w.session, msg)
					w.refreshInboxSizes()
				}
				continue
			}

			w.agentRestarts[role]++
			attempt := w.agentRestarts[role]
			fmt.Printf("  %s  Restarting agent %s (attempt %d/3)...\n", ts, role, attempt)
			bus.LogLifecycle(w.session, "warn", "watcher", "agent-restart",
				fmt.Sprintf("%s attempt %d/3", role, attempt))

			// Send restarting alert
			alert := bus.FormatAgentHealthAlert("restarting", role,
				fmt.Sprintf("Attempt %d/3 — relaunching agent", attempt))
			msg := bus.NewMessage("watcher", "edit", "event", "agent-restarting", alert, "")
			_ = bus.Send(w.session, msg)
			w.refreshInboxSizes()

			// Attempt restart
			if err := bus.RestartLocalAgent(w.session, role); err != nil {
				fmt.Fprintf(os.Stderr, "  [agent-health] failed to restart %s: %v\n", role, err)
			} else {
				fmt.Printf("  %s  Agent %s restarted successfully\n", ts, role)
			}

			// Reset fail count to let next probe detect recovery
			w.agentFailCounts[role] = 0
		}
	}
}

// checkIdleAgents wakes agents that are idle with unread messages.
// Runs every 5 seconds. For each non-edit agent that has unread inbox messages
// and is sitting at the idle prompt (not polling or waiting), injects
// "You have new messages" via send-keys. This replaces the LLM-driven polling
// loop — agents no longer need to run `muxcode inbox --poll` themselves.
// The edit agent is excluded because it uses background polling managed by
// the user/orchestrator.
func (w *Watcher) checkIdleAgents() {
	now := time.Now().Unix()
	if now-w.lastIdleCheck < 5 {
		return
	}
	w.lastIdleCheck = now

	for _, role := range bus.KnownRoles {
		// Edit agent manages its own polling via background Bash tool
		if role == "edit" {
			continue
		}
		// Skip hosted roles — they share a pane with their host
		if bus.WindowForRole(role) != role {
			continue
		}
		// Skip if no unread messages
		if !bus.HasMessages(w.session, role) {
			continue
		}
		// Skip if agent is polling or waiting (already watching inbox)
		if bus.IsPolling(w.session, role) || bus.IsWaiting(w.session, role) {
			continue
		}
		// Skip harness panes — they poll inbox directly
		if bus.IsHarnessActive(w.session, role) {
			continue
		}
		// Skip non-hook providers (OpenCode TUI, local LLM) — they cannot
		// be reliably woken via send-keys. IsIdle always returns false for
		// these providers, but skipping early avoids unnecessary pane captures.
		provider := bus.ResolveProvider(role)
		if !provider.SupportsHooks() {
			// Best-effort: send display-message so user sees a flash.
			// Cooldown: once per 60s per role to avoid display-message spam
			// since this check runs every 5s.
			if now-w.lastNonHookWake[role] >= 60 {
				w.lastNonHookWake[role] = now
				_ = provider.SendWakeUp(w.session, role)
			}
			continue
		}
		// Only wake idle agents (at ❯ prompt) — don't interrupt active ones
		if !bus.IsAgentIdle(w.session, role) {
			continue
		}

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Waking idle agent %s (unread messages)\n", ts, role)
		bus.LogLifecycle(w.session, "info", "watcher", "idle-wake", role)
		_ = bus.Notify(w.session, role)
	}
}

// checkCleanup removes expired delivery status and task files every 5 minutes.
// Delivery and task files accumulate from --wait send operations; without
// periodic cleanup they grow unbounded over long sessions.
func (w *Watcher) checkCleanup() {
	now := time.Now().Unix()
	if now-w.lastCleanupCheck < 300 {
		return
	}
	w.lastCleanupCheck = now

	// Clean delivery status files older than 1 hour
	deliveryCleaned := bus.CleanExpiredDeliveries(w.session, 1*time.Hour)
	// Clean task files older than 1 hour
	tasksCleaned := bus.CleanExpiredTasks(w.session, 1*time.Hour)

	if deliveryCleaned > 0 || tasksCleaned > 0 {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Cleanup: %d delivery, %d task files removed\n", ts, deliveryCleaned, tasksCleaned)
		bus.LogLifecycle(w.session, "info", "watcher", "cleanup",
			fmt.Sprintf("delivery=%d tasks=%d", deliveryCleaned, tasksCleaned))
	}
}

// formatWatcherBytes is a simple bytes formatter for watcher log lines.
func formatWatcherBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	}
	kb := float64(b) / 1024
	if kb < 1024 {
		return fmt.Sprintf("%.0f KB", kb)
	}
	mb := kb / 1024
	return fmt.Sprintf("%.1f MB", mb)
}
