package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mkober/muxcode/tools/muxcode/bus"
)

// Daemon monitors agent inboxes and a trigger file for file-edit events.
type Daemon struct {
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
	// Non-hook task completion detection
	lastTaskCheck       int64             // 5s interval
	taskDeliveredAt     map[string]int64  // msgID -> unix time when message was delivered (wake-up sent)
	taskLastPaneContent map[string]string // role -> last pane hash to avoid re-processing identical content
	// Hook-provider idle task detection (safety net for dropped responses)
	lastIdleTaskCheck int64            // 10s interval
	idleTaskFirstSeen map[string]int64 // taskID -> unix time when first observed idle with in-flight task
	// Agent heartbeat
	lastHeartbeatCheck int64 // tracks last heartbeat fire time
	heartbeatInterval  int   // seconds between heartbeats (0 = disabled)
	// Non-hook edit file change detection
	lastEditDiffCheck int64  // 10s debounce interval
	lastEditDiffHash  string // last observed git diff --stat hash
}

// New creates a new Daemon for the given session.
func New(session string, pollSecs, debounceSecs int) *Daemon {
	now := time.Now().Unix()

	// Discover which roles use local LLM
	ollamaRoles := bus.LocalLLMRoles()

	// Read Ollama config for health probes
	ollamaCfg := bus.DefaultOllamaConfig()

	return &Daemon{
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
		taskDeliveredAt:      make(map[string]int64),
		taskLastPaneContent:  make(map[string]string),
		idleTaskFirstSeen:    make(map[string]int64),
		lastHeartbeatCheck:   now,
		heartbeatInterval:    bus.AgentHeartbeatInterval(),
	}
}

// acquireDaemonLock ensures only one daemon runs per session.
// Uses flock on a lock file for race-free single-instance enforcement.
// Returns an unlock function, or an error if another daemon is already running.
func acquireDaemonLock(session string) (func(), error) {
	lockPath := filepath.Join(bus.BusDir(session), "lock", "daemon.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, fmt.Errorf("cannot open daemon lock: %w", err)
	}

	// Non-blocking exclusive lock — fails immediately if another daemon holds it
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another daemon is already running for session %s", session)
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

// Run starts the main daemon loop. It never returns under normal operation.
// Acquires a per-session flock to prevent duplicate daemon processes — stale
// daemons from previous session starts cause duplicate tmux notifications.
func (d *Daemon) Run() error {
	busDir := bus.BusDir(d.session)

	// Set BUS_SESSION so downstream code (ResolveProviderCLI → BusSession())
	// resolves the correct session. Without this, BusSession() falls through
	// to "default" and runtime overrides (e.g. hot-reload CLI changes) are
	// never read — the daemon resolves stale provider defaults for agents
	// that were hot-reloaded to a different CLI.
	os.Setenv("BUS_SESSION", d.session)

	// Single-instance enforcement: exit immediately if another daemon is running
	unlock, err := acquireDaemonLock(d.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  %s\n", err)
		bus.LogLifecycle(d.session, "error", "daemon", "lock-failed", err.Error())
		return err
	}
	defer unlock()

	bus.LogLifecycleWithPID(d.session, "info", "daemon", "started",
		fmt.Sprintf("Poll: %ds, Debounce: %ds", int(d.pollInterval.Seconds()), d.debounceSecs),
		os.Getpid())

	fmt.Println("  Agent Bus Daemon")
	fmt.Printf("  Session: %s\n", d.session)
	fmt.Printf("  Bus: %s\n", busDir)
	fmt.Printf("  Trigger: %s\n", d.triggerFile)
	fmt.Printf("  Poll: %ds  Debounce: %ds\n", int(d.pollInterval.Seconds()), d.debounceSecs)
	if len(d.ollamaRoles) > 0 {
		fmt.Printf("  Ollama monitoring: %s (roles: %s)\n", d.ollamaURL, strings.Join(d.ollamaRoles, ", "))
	}
	if d.heartbeatInterval > 0 {
		fmt.Printf("  Agent heartbeat: every %ds\n", d.heartbeatInterval)
	}
	fmt.Println()

	for {
		d.touchKeepalive()
		d.checkInboxes()
		d.checkTrigger()
		d.checkCron()
		d.checkProcs()
		d.checkSpawns()
		d.checkLoops()
		d.checkCompaction()
		d.checkOllama()
		d.checkAgentHealth()
		d.checkIdleAgents()
		d.checkNonHookTasks()
		d.checkNonHookEdits()
		d.checkIdleTaskCompletion()
		d.checkHeartbeat()
		d.checkCleanup()
		time.Sleep(d.pollInterval)
	}
}

// refreshInboxSizes updates the tracked inbox sizes without sending notifications.
// Call this after programmatically adding messages to prevent checkInboxes from
// re-notifying for messages that were already handled.
func (d *Daemon) refreshInboxSizes() {
	for _, role := range bus.KnownRoles {
		inboxPath := bus.InboxPath(d.session, role)
		info, err := os.Stat(inboxPath)
		if err != nil {
			d.inboxSizes[role] = 0
			continue
		}
		d.inboxSizes[role] = info.Size()
	}
}

// checkInboxes polls all agent inboxes for new messages.
// When inbox growth is detected, writes the trigger file (via Notify) so
// agents running `muxcode inbox --poll` pick up the messages. Also sends
// a display-message for human visibility.
//
// cmd/send.go already calls Notify() for direct recipients. The daemon
// catches messages that arrive without a Notify (e.g. auto-CC, hooks).
func (d *Daemon) checkInboxes() {
	for _, role := range bus.KnownRoles {
		inboxPath := bus.InboxPath(d.session, role)
		info, err := os.Stat(inboxPath)
		if err != nil {
			d.inboxSizes[role] = 0
			continue
		}

		size := info.Size()
		prev := d.inboxSizes[role]

		if size > prev && size > 0 {
			// Workflow: detect review→edit messages for reviewed transition
			if role == "edit" && bus.HasNewMessageFrom(d.session, "edit", "review") {
				bus.TransitionWorkflow(d.session, bus.StateReviewed, "daemon:review-complete",
					bus.WithOutcome("review", "complete"))
			}

			// Only notify if the inbox has actionable (request-type) messages.
			// Response-only inbox growth (e.g. heartbeat-ack routed to edit
			// via daemon→edit normalization) should not wake agents — responses
			// are informational and get consumed on the next natural wake-up.
			if !bus.HasActionableMessages(d.session, role) {
				d.inboxSizes[role] = size
				continue
			}

			// Notify writes the trigger file and optionally sends a
			// display-message. Dedup is handled inside Notify via
			// file locking + cooldown.
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  New message(s) for %s — notifying\n", ts, role)
			bus.LogLifecycle(d.session, "info", "daemon", "inbox-notify", role)
			_ = bus.Notify(d.session, role)
		}

		d.inboxSizes[role] = size
	}
}

// checkTrigger monitors the trigger file for file-edit events with debouncing.
func (d *Daemon) checkTrigger() {
	info, err := os.Stat(d.triggerFile)
	if err != nil || info.Size() == 0 {
		return
	}

	size := info.Size()
	now := time.Now().Unix()

	if size != d.lastTriggerSize {
		if d.pendingSince == 0 {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  Claude edits detected, waiting to stabilize...\n", ts)
		}
		d.pendingSince = now
		d.lastTriggerSize = size
	} else if d.pendingSince > 0 {
		elapsed := now - d.pendingSince
		if elapsed >= int64(d.debounceSecs) {
			d.routeTrigger()
			// Truncate the trigger file
			f, err := os.OpenFile(d.triggerFile, os.O_WRONLY|os.O_TRUNC, 0644)
			if err == nil {
				f.Close()
			}
			d.pendingSince = 0
			d.lastTriggerSize = 0
		}
	}
}

// routeTrigger reads the trigger file, extracts unique file paths, and sends
// an aggregate analyze event. Individual file routing (test/deploy/build) is
// handled by claude-teach-hook.sh to avoid duplicate messages.
func (d *Daemon) routeTrigger() {
	f, err := os.Open(d.triggerFile)
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
	bus.LogLifecycle(d.session, "info", "daemon", "trigger-route",
		fmt.Sprintf("%d file(s): %s", len(files), strings.Join(files, ", ")))

	// Workflow: transition to analyzing
	bus.TransitionWorkflow(d.session, bus.StateAnalyzing, "daemon:analyze-route",
		bus.WithFiles(files))

	// Send aggregate event to analyze agent
	fileList := strings.Join(files, ", ")
	analyzePayload := fmt.Sprintf("Claude edited files: %s — Read those files and explain what was changed and why.", fileList)
	msg := bus.NewMessage("daemon", "analyze", "event", "analyze", analyzePayload, "")
	if err := bus.Send(d.session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "  [route] failed to send analyze event: %v\n", err)
		return
	}

	// Notify the analyze agent
	if err := bus.Notify(d.session, "analyze"); err != nil {
		fmt.Fprintf(os.Stderr, "  [route] failed to notify analyze: %v\n", err)
	}

	// Refresh inbox sizes so checkInboxes doesn't re-notify for the
	// message we just sent (prevents double notification).
	d.refreshInboxSizes()
}

// loadCron reloads cron entries from disk at most once per 10 seconds.
// Skips loading if the cron file is empty or missing.
func (d *Daemon) loadCron() {
	now := time.Now().Unix()
	if now-d.lastCronLoad < 10 {
		return
	}

	// Skip if cron file is empty or missing
	info, err := os.Stat(bus.CronPath(d.session))
	if err != nil || info.Size() == 0 {
		d.cronEntries = nil
		d.lastCronLoad = now
		return
	}

	entries, err := bus.ReadCronEntries(d.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [cron] failed to read cron entries: %v\n", err)
		return
	}
	d.cronEntries = entries
	d.lastCronLoad = now
}

// checkCron iterates cached cron entries, fires due ones, and updates state.
func (d *Daemon) checkCron() {
	d.loadCron()

	now := time.Now().Unix()
	fired := false
	for _, entry := range d.cronEntries {
		if !bus.CronDue(entry, now) {
			continue
		}

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Cron firing: %s → %s:%s\n", ts, entry.ID, entry.Target, entry.Action)
		bus.LogLifecycle(d.session, "info", "daemon", "cron-fire",
			fmt.Sprintf("%s → %s:%s", entry.ID, entry.Target, entry.Action))

		msgID, err := bus.ExecuteCron(d.session, entry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to execute %s: %v\n", entry.ID, err)
			continue
		}

		fired = true

		// Update last run timestamp
		if err := bus.UpdateLastRun(d.session, entry.ID, now); err != nil {
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
		if err := bus.AppendCronHistory(d.session, histEntry); err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to append history for %s: %v\n", entry.ID, err)
		}

		// Notify target agent (harness panes are skipped inside Notify)
		if err := bus.Notify(d.session, entry.Target); err != nil {
			fmt.Fprintf(os.Stderr, "  [cron] failed to notify %s: %v\n", entry.Target, err)
		}
	}

	if fired {
		// Refresh inbox sizes after cron messages to prevent double notification
		d.refreshInboxSizes()
		// Force cron reload on next cycle so updated last_run_ts values are picked up
		d.lastCronLoad = 0
	}
}

// checkProcs polls running background processes and notifies owners on completion.
// Skips entirely if proc file is empty/missing and no running procs are tracked.
func (d *Daemon) checkProcs() {
	// Skip if proc file is empty/missing and no running procs cached
	info, err := os.Stat(bus.ProcPath(d.session))
	currentSize := int64(0)
	if err == nil {
		currentSize = info.Size()
	}
	if currentSize == 0 && !d.hasRunningProcs {
		return
	}
	// Reset running flag if file size changed (new proc may have been added)
	if currentSize != d.lastProcSize {
		d.hasRunningProcs = true
		d.lastProcSize = currentSize
	}

	completed, err := bus.RefreshProcStatus(d.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [proc] failed to refresh proc status: %v\n", err)
		return
	}

	// Update running state: check if any procs are still running
	entries, _ := bus.ReadProcEntries(d.session)
	hasRunning := false
	for _, e := range entries {
		if e.Status == "running" {
			hasRunning = true
			break
		}
	}
	d.hasRunningProcs = hasRunning

	if len(completed) == 0 {
		return
	}

	for _, entry := range completed {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Process completed: %s (status: %s, exit: %d)\n",
			ts, entry.ID, entry.Status, entry.ExitCode)
		bus.LogLifecycle(d.session, "info", "daemon", "proc-complete",
			fmt.Sprintf("%s status=%s exit=%d", entry.ID, entry.Status, entry.ExitCode))

		payload := fmt.Sprintf("Background process completed: %s\n  Command: %s\n  Status: %s  Exit code: %d\n  Log: %s",
			entry.ID, entry.Command, entry.Status, entry.ExitCode, entry.LogFile)

		msg := bus.NewMessage("proc", entry.Owner, "event", "proc-complete", payload, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [proc] failed to send completion event to %s: %v\n", entry.Owner, err)
			continue
		}

		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(d.session, entry.Owner); err != nil {
			fmt.Fprintf(os.Stderr, "  [proc] failed to notify %s: %v\n", entry.Owner, err)
		}

		// Mark as notified
		_ = bus.UpdateProcEntry(d.session, entry.ID, func(e *bus.ProcEntry) {
			e.Notified = true
		})
	}

	d.refreshInboxSizes()
}

// checkSpawns polls running spawned agents and notifies owners on completion.
// Skips entirely if spawn file is empty/missing and no running spawns are tracked.
func (d *Daemon) checkSpawns() {
	// Skip if spawn file is empty/missing and no running spawns cached
	info, err := os.Stat(bus.SpawnPath(d.session))
	currentSize := int64(0)
	if err == nil {
		currentSize = info.Size()
	}
	if currentSize == 0 && !d.hasRunningSpawns {
		return
	}
	// Reset running flag if file size changed (new spawn may have been added)
	if currentSize != d.lastSpawnSize {
		d.hasRunningSpawns = true
		d.lastSpawnSize = currentSize
	}

	completed, err := bus.RefreshSpawnStatus(d.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [spawn] failed to refresh spawn status: %v\n", err)
		return
	}

	// Update running state: check if any spawns are still running
	entries, _ := bus.ReadSpawnEntries(d.session)
	hasRunning := false
	for _, e := range entries {
		if e.Status == "running" {
			hasRunning = true
			break
		}
	}
	d.hasRunningSpawns = hasRunning

	if len(completed) == 0 {
		return
	}

	for _, entry := range completed {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Spawn completed: %s (role: %s, window: %s)\n",
			ts, entry.ID, entry.Role, entry.Window)
		bus.LogLifecycle(d.session, "info", "daemon", "spawn-complete",
			fmt.Sprintf("%s role=%s window=%s", entry.ID, entry.Role, entry.Window))

		// Try to extract the last result message from the spawn
		resultInfo := "No result message found."
		if result, ok := bus.GetSpawnResult(d.session, entry.SpawnRole); ok {
			resultInfo = result.Payload
			if len(resultInfo) > 200 {
				resultInfo = resultInfo[:200] + "..."
			}
		}

		payload := fmt.Sprintf("Spawned agent completed: %s\n  Role: %s  Spawn Role: %s\n  Task: %s\n  Result: %s",
			entry.ID, entry.Role, entry.SpawnRole, entry.Task, resultInfo)

		msg := bus.NewMessage("spawn", entry.Owner, "event", "spawn-complete", payload, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] failed to send completion event to %s: %v\n", entry.Owner, err)
			continue
		}

		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(d.session, entry.Owner); err != nil {
			fmt.Fprintf(os.Stderr, "  [spawn] failed to notify %s: %v\n", entry.Owner, err)
		}

		// Mark as notified
		_ = bus.UpdateSpawnEntry(d.session, entry.ID, func(e *bus.SpawnEntry) {
			e.Notified = true
		})
	}

	d.refreshInboxSizes()
}

// checkLoops runs loop detection every 60 seconds and sends alerts to the edit agent.
// Deduplicates alerts within a 10-minute cooldown to avoid spamming.
func (d *Daemon) checkLoops() {
	now := time.Now().Unix()
	if now-d.lastLoopCheck < 60 {
		return
	}
	d.lastLoopCheck = now

	alerts := bus.CheckAllLoops(d.session)
	if len(alerts) == 0 {
		return
	}

	// Filter out alerts that were already sent within the cooldown window.
	// Cooldown (600s) must exceed detection window (300s) to prevent
	// loop-detected events from sustaining their own detection window.
	fresh := bus.FilterNewAlerts(alerts, d.lastAlertKey, 600)
	if len(fresh) == 0 {
		return
	}

	for _, alert := range fresh {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Loop detected: %s (%s)\n", ts, alert.Role, alert.Type)
		bus.LogLifecycle(d.session, "warn", "daemon", "loop-detected",
			fmt.Sprintf("%s type=%s", alert.Role, alert.Type))

		msg := bus.NewMessage("daemon", "edit", "event", "loop-detected", alert.Message, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to send loop alert: %v\n", err)
			continue
		}
		// Notify edit via display-message (passive status bar flash)
		if err := bus.Notify(d.session, "edit"); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to notify edit: %v\n", err)
		}
	}

	d.refreshInboxSizes()
}

// checkCompaction runs compaction checks every 120 seconds and sends recommendations
// to the role itself. Deduplicates alerts within a 10-minute cooldown.
func (d *Daemon) checkCompaction() {
	now := time.Now().Unix()
	if now-d.lastCompactCheck < 120 {
		return
	}
	d.lastCompactCheck = now

	th := bus.DefaultCompactThresholds()
	alerts := bus.CheckCompaction(d.session, th)
	if len(alerts) == 0 {
		return
	}

	// Filter out alerts that were already sent within the cooldown window (600s = 10 min)
	fresh := bus.FilterNewCompactAlerts(alerts, d.lastAlertKey, 600)
	if len(fresh) == 0 {
		return
	}

	for _, alert := range fresh {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Compact recommended: %s (total: %s)\n", ts, alert.Role, formatDaemonBytes(alert.TotalBytes))
		bus.LogLifecycle(d.session, "warn", "daemon", "compact-alert",
			fmt.Sprintf("%s total=%s", alert.Role, formatDaemonBytes(alert.TotalBytes)))

		msg := bus.NewMessage("daemon", alert.Role, "event", "compact-recommended", alert.Message, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [compact] failed to send compact alert to %s: %v\n", alert.Role, err)
			continue
		}
		// Notify uses display-message for all Claude Code panes (safe, non-intrusive).
		// Harness panes are skipped inside Notify() — they poll inbox directly.
		if err := bus.Notify(d.session, alert.Role); err != nil {
			fmt.Fprintf(os.Stderr, "  [compact] failed to notify %s: %v\n", alert.Role, err)
		}
	}

	d.refreshInboxSizes()
}

// checkOllama runs Ollama health probes every 30 seconds for roles using local LLM.
// Detection timeline: 30s first probe, 60s alert, 90s restart attempt.
// Caps automatic restarts at 3 to prevent restart loops.
func (d *Daemon) checkOllama() {
	if len(d.ollamaRoles) == 0 {
		return
	}

	now := time.Now().Unix()
	if now-d.lastOllamaCheck < 30 {
		return
	}
	d.lastOllamaCheck = now

	// Run inference probe
	err := bus.CheckOllamaInference(d.ollamaURL, d.ollamaModel, bus.OllamaProbeTimeout)

	// Also check for agent failure sentinels
	hasSentinels := bus.HasOllamaFailSentinel(d.session)

	ts := time.Now().Format("15:04:05")

	if err == nil && !hasSentinels {
		// Healthy
		if d.ollamaWasDown {
			// Recovery detected
			fmt.Printf("  %s  Ollama recovered — inference probe healthy\n", ts)
			bus.LogLifecycle(d.session, "info", "daemon", "ollama-recovered", "")
			d.ollamaWasDown = false
			d.ollamaFailCount = 0

			alert := bus.FormatOllamaAlert("recovered", d.ollamaRoles, "Ollama is responsive again")
			msg := bus.NewMessage("daemon", "edit", "event", "ollama-recovered", alert, "")
			if sendErr := bus.Send(d.session, msg); sendErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to send recovery alert: %v\n", sendErr)
			}
			d.refreshInboxSizes()
		}
		return
	}

	// Unhealthy
	d.ollamaFailCount++
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

	fmt.Printf("  %s  Ollama probe failure #%d: %s\n", ts, d.ollamaFailCount, errMsg)
	bus.LogLifecycle(d.session, "warn", "daemon", "ollama-probe-fail",
		fmt.Sprintf("failure #%d: %s", d.ollamaFailCount, errMsg))

	// Second consecutive failure (60s) — send ollama-down alert
	if d.ollamaFailCount == 2 && !d.ollamaWasDown {
		d.ollamaWasDown = true

		// Dedup via lastAlertKey with 600s cooldown
		alertKey := bus.OllamaHealthAlertKey("down")
		if lastTS, ok := d.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
			d.lastAlertKey[alertKey] = now
			alert := bus.FormatOllamaAlert("down", d.ollamaRoles, errMsg)
			msg := bus.NewMessage("daemon", "edit", "event", "ollama-down", alert, "")
			if sendErr := bus.Send(d.session, msg); sendErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to send down alert: %v\n", sendErr)
			}
			d.refreshInboxSizes()
		}
	}

	// Third consecutive failure (90s) — attempt restart
	if d.ollamaFailCount == 3 {
		if d.ollamaRestarts >= 3 {
			// Cap reached — periodic alerts only
			alertKey := bus.OllamaHealthAlertKey("down")
			if lastTS, ok := d.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
				d.lastAlertKey[alertKey] = now
				alert := bus.FormatOllamaAlert("down", d.ollamaRoles,
					fmt.Sprintf("Restart cap (3) reached. %s. Manual intervention required.", errMsg))
				msg := bus.NewMessage("daemon", "edit", "event", "ollama-down", alert, "")
				_ = bus.Send(d.session, msg)
				d.refreshInboxSizes()
			}
			return
		}

		fmt.Printf("  %s  Attempting Ollama restart (#%d)...\n", ts, d.ollamaRestarts+1)
		bus.LogLifecycle(d.session, "warn", "daemon", "ollama-restart",
			fmt.Sprintf("attempt %d/3", d.ollamaRestarts+1))
		d.ollamaRestarts++

		// Send restarting alert
		alert := bus.FormatOllamaAlert("restarting", d.ollamaRoles,
			fmt.Sprintf("Attempt %d/3 — killing and restarting ollama serve", d.ollamaRestarts))
		msg := bus.NewMessage("daemon", "edit", "event", "ollama-restarting", alert, "")
		_ = bus.Send(d.session, msg)
		d.refreshInboxSizes()

		// Attempt restart with 30s timeout
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		restartErr := bus.RestartOllama(ctx, d.ollamaURL)
		cancel()

		if restartErr != nil {
			fmt.Fprintf(os.Stderr, "  [ollama] restart failed: %v\n", restartErr)
			return
		}

		fmt.Printf("  %s  Ollama restarted successfully, relaunching agents...\n", ts)

		// Relaunch affected agents
		for _, role := range d.ollamaRoles {
			if restartErr := bus.RestartLocalAgent(d.session, role); restartErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to restart agent %s: %v\n", role, restartErr)
			} else {
				fmt.Printf("  %s  Relaunched agent: %s\n", ts, role)
				// Clear notified-size so checkIdleAgents re-notifies the
				// new agent about any pending inbox messages.
				bus.ClearNotifiedSize(d.session, role)
			}
		}

		// Reset fail count to let the next probe cycle detect recovery
		d.ollamaFailCount = 0
	}
}

// touchKeepalive writes the current timestamp to the daemon keepalive file.
// Called at the top of each poll loop iteration so the daemon monitor can
// detect if the daemon process has died or become stuck.
func (d *Daemon) touchKeepalive() {
	bus.TouchKeepaliveDaemon(d.session)
}

// checkAgentHealth probes agent liveness every 30 seconds using a 3-strike
// escalation pattern: log → alert edit → restart (capped at 3 restarts).
// Excludes edit and webhook roles. Respects intentional stop markers.
func (d *Daemon) checkAgentHealth() {
	now := time.Now().Unix()
	if now-d.lastAgentHealthCheck < 30 {
		return
	}
	d.lastAgentHealthCheck = now

	ts := time.Now().Format("15:04:05")

	for _, role := range bus.KnownRoles {
		// Skip excluded roles and spawn roles
		if bus.IsAgentHealthExcluded(d.session, role) || bus.IsSpawnRole(role) {
			continue
		}

		// Skip intentionally stopped agents
		if bus.IsAgentStopped(d.session, role) {
			continue
		}

		alive := bus.IsAgentAlive(d.session, role)

		if alive {
			// Recovery detection
			if d.agentWasDown[role] {
				fmt.Printf("  %s  Agent %s recovered\n", ts, role)
				bus.LogLifecycle(d.session, "info", "daemon", "agent-recovered", role)
				d.agentWasDown[role] = false
				d.agentFailCounts[role] = 0

				alert := bus.FormatAgentHealthAlert("recovered", role, "Agent is responsive again")
				msg := bus.NewMessage("daemon", "edit", "event", "agent-recovered", alert, "")
				_ = bus.Send(d.session, msg)
				d.refreshInboxSizes()
			}
			continue
		}

		// Agent appears dead — increment fail count
		d.agentFailCounts[role]++
		count := d.agentFailCounts[role]

		fmt.Printf("  %s  Agent %s health check failure #%d\n", ts, role, count)
		bus.LogLifecycle(d.session, "warn", "daemon", "agent-health-fail",
			fmt.Sprintf("%s failure #%d", role, count))

		// Strike 2 (60s) — alert edit
		if count == 2 {
			d.agentWasDown[role] = true

			alertKey := bus.AgentHealthAlertKey(role, "down")
			if lastTS, ok := d.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
				d.lastAlertKey[alertKey] = now
				alert := bus.FormatAgentHealthAlert("down", role, "Agent pane shows bare shell prompt")
				msg := bus.NewMessage("daemon", "edit", "event", "agent-down", alert, "")
				if err := bus.Send(d.session, msg); err != nil {
					fmt.Fprintf(os.Stderr, "  [agent-health] failed to send down alert for %s: %v\n", role, err)
				}
				d.refreshInboxSizes()
			}
		}

		// Strike 3 (90s) — attempt restart
		if count == 3 {
			if d.agentRestarts[role] >= 3 {
				// Cap reached — alert-only mode
				alertKey := bus.AgentHealthAlertKey(role, "down")
				if lastTS, ok := d.lastAlertKey[alertKey]; !ok || (now-lastTS) >= 600 {
					d.lastAlertKey[alertKey] = now
					alert := bus.FormatAgentHealthAlert("down", role,
						fmt.Sprintf("Restart cap (3) reached. Manual intervention required."))
					msg := bus.NewMessage("daemon", "edit", "event", "agent-down", alert, "")
					_ = bus.Send(d.session, msg)
					d.refreshInboxSizes()
				}
				continue
			}

			d.agentRestarts[role]++
			attempt := d.agentRestarts[role]
			fmt.Printf("  %s  Restarting agent %s (attempt %d/3)...\n", ts, role, attempt)
			bus.LogLifecycle(d.session, "warn", "daemon", "agent-restart",
				fmt.Sprintf("%s attempt %d/3", role, attempt))

			// Send restarting alert
			alert := bus.FormatAgentHealthAlert("restarting", role,
				fmt.Sprintf("Attempt %d/3 — relaunching agent", attempt))
			msg := bus.NewMessage("daemon", "edit", "event", "agent-restarting", alert, "")
			_ = bus.Send(d.session, msg)
			d.refreshInboxSizes()

			// Attempt restart
			if err := bus.RestartLocalAgent(d.session, role); err != nil {
				fmt.Fprintf(os.Stderr, "  [agent-health] failed to restart %s: %v\n", role, err)
			} else {
				fmt.Printf("  %s  Agent %s restarted successfully\n", ts, role)
				// Clear notified-size so checkIdleAgents re-notifies the
				// new agent about any pending inbox messages.
				bus.ClearNotifiedSize(d.session, role)
			}

			// Reset fail count to let next probe detect recovery
			d.agentFailCounts[role] = 0
		}
	}
}

// checkIdleAgents wakes agents that are idle with unread messages.
// Runs every 5 seconds. For each non-edit agent that has unread inbox messages
// and is sitting at the idle prompt (not polling or waiting), triggers a
// wake-up via the provider. Hook providers (Claude Code) get "You have new
// messages" via send-keys; non-hook providers (Codex, OpenCode) get the actual
// message content injected via provider.SendWakeUp(). The edit agent is
// excluded because it uses background polling managed by the user/orchestrator.
func (d *Daemon) checkIdleAgents() {
	now := time.Now().Unix()
	if now-d.lastIdleCheck < 5 {
		return
	}
	d.lastIdleCheck = now

	for _, role := range bus.KnownRoles {
		// Skip hosted roles — they share a pane with their host
		if bus.WindowForRole(role) != role {
			continue
		}
		// Skip roles being reloaded — agent is intentionally down during
		// the stop→reconfigure→relaunch cycle
		if bus.IsReloading(d.session, role) {
			continue
		}
		// Skip if no actionable messages. Only wake agents for request-type
		// messages — responses and events are informational and don't require
		// the agent to act. This prevents echo loops where agents keep
		// acknowledging each other's responses.
		if !bus.HasActionableMessages(d.session, role) {
			continue
		}
		// Skip if agent is polling or waiting (already watching inbox)
		if bus.IsPolling(d.session, role) || bus.IsWaiting(d.session, role) {
			continue
		}
		// Skip harness panes — they poll inbox directly
		if bus.IsHarnessActive(d.session, role) {
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
			if now-d.lastNonHookWake[role] >= 60 {
				d.lastNonHookWake[role] = now
				_ = provider.SendWakeUp(d.session, role)
			}
			continue
		}
		// Only wake idle agents (at ❯ prompt) — don't interrupt active ones
		if !bus.IsAgentIdle(d.session, role) {
			continue
		}

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Waking idle agent %s (unread messages)\n", ts, role)
		bus.LogLifecycle(d.session, "info", "daemon", "idle-wake", role)
		// Clear notified-size before calling Notify so a stale marker from
		// notifyDisplayMessage can't suppress the send-keys injection. The
		// race: Notify() re-checks IsAgentIdle inside — if tmux capture-pane
		// returns a stale snapshot (no ❯), it falls to notifyDisplayMessage
		// which calls markNotified() without actually waking the agent.
		// Subsequent retries see the marker and are suppressed for 30s.
		// Clearing here ensures the send-keys path proceeds unconditionally.
		bus.ClearNotifiedSize(d.session, role)
		_ = bus.Notify(d.session, role)
	}
}

// checkNonHookTasks monitors in-flight tasks targeting non-hook providers
// (OpenCode TUI, local LLM) by capturing their tmux pane content and detecting
// task completion or errors. When a task completes, sends a synthetic response
// message back to the requesting agent so --wait unblocks and the delivery
// lifecycle completes normally.
//
// Runs every 5 seconds. Only processes tasks that have been in-flight for at
// least 5 seconds (grace period for the agent to start working).
func (d *Daemon) checkNonHookTasks() {
	now := time.Now().Unix()
	if now-d.lastTaskCheck < 5 {
		return
	}
	d.lastTaskCheck = now

	// Find in-flight tasks targeting non-hook providers
	tasks, err := bus.ListTasks(d.session, bus.TaskInFlight)
	if err != nil || len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		provider := bus.ResolveProvider(task.To)
		if provider.SupportsHooks() {
			continue // hook providers handle their own completion
		}

		// Grace period: wait at least 5s after the task was sent before checking.
		// This avoids false positives from the previous task's stop marker still
		// being visible when a new task starts.
		if now-task.SentAt < 5 {
			continue
		}

		// Track when we first saw this task delivered (wake-up was sent).
		// The grace period starts from delivery, not from our first observation.
		if _, ok := d.taskDeliveredAt[task.ID]; !ok {
			d.taskDeliveredAt[task.ID] = now
		}

		// Require at least 3s since we started tracking this task
		if now-d.taskDeliveredAt[task.ID] < 3 {
			continue
		}

		// Capture the agent's pane (30 lines for context)
		target := bus.PaneTarget(d.session, task.To)
		paneContent, err := bus.TmuxCapturePaneLines(target, 30)
		if err != nil {
			continue
		}

		// Skip if pane content hasn't changed since last check —
		// avoids re-analyzing the same static output.
		contentKey := task.To + ":" + task.ID
		if paneContent == d.taskLastPaneContent[contentKey] {
			continue
		}
		d.taskLastPaneContent[contentKey] = paneContent

		// Ask the provider to analyze the pane content
		completed, errored, summary := provider.DetectTaskCompletion(d.session, task.To, paneContent)
		if !completed {
			continue
		}

		// Task completed — send synthetic response to the requester
		ts := time.Now().Format("15:04:05")
		status := "succeeded"
		action := "response"
		if errored {
			status = "completed with errors"
			action = "error"
		}
		fmt.Printf("  %s  Detected %s task %s (%s)\n", ts, task.To, status, task.Action)
		bus.LogLifecycle(d.session, "info", "daemon", "task-detected",
			fmt.Sprintf("%s task %s from %s: %s", task.To, task.Action, task.From, status))

		// Truncate summary for the message payload (keep it reasonable)
		payload := summary
		if len(payload) > 2000 {
			payload = payload[:1997] + "..."
		}

		// Send response back to the original requester
		msg := bus.NewMessage(task.To, task.From, "response", action, payload, task.ID)
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [task-detect] failed to send response for %s: %v\n", task.ID, err)
			continue
		}

		// Mark the task as completed so it's not re-detected on next check.
		// Send() already called MarkResponded() on the delivery status (via
		// ReplyTo), so waitForResponse() in --wait will also unblock.
		bus.CompleteTask(d.session, task.ID, msg.ID)

		// Log to console history so the left-pane console view updates.
		// Non-hook providers don't have PostToolUse hooks to write history,
		// so the daemon writes it when it detects task completion.
		logTaskToConsoleHistory(d.session, task.To, task.Action, payload, errored)

		// Clean up tracking state for this task
		delete(d.taskDeliveredAt, task.ID)
		delete(d.taskLastPaneContent, contentKey)

		// Notify the requester so they pick up the response
		_ = bus.Notify(d.session, task.From)
	}
}

// idleTaskGracePeriod is how long a hook-provider agent must be idle with an
// in-flight task before the daemon sends a synthetic response. This gives the
// agent time to send its own response via the Bash tool before the safety net
// kicks in. 30 seconds covers normal response composition time while catching
// the case where the agent output the send command as text instead of executing it.
// checkNonHookEdits detects file changes made by the edit agent on a non-hook
// provider (e.g. OpenCode). Since PostToolUse Write/Edit hooks don't fire,
// the daemon polls git diff --stat every 10 seconds to detect new changes.
// On new changes: transitions workflow to StateEditing and writes the analyze
// trigger file so the analyze agent picks up the changed files.
// Skips entirely when the edit agent runs on Claude Code (hooks handle it).
func (d *Daemon) checkNonHookEdits() {
	now := time.Now().Unix()
	if now-d.lastEditDiffCheck < 10 {
		return
	}
	d.lastEditDiffCheck = now

	// Only run for non-hook edit providers
	provider := bus.ResolveProvider("edit")
	if provider.SupportsHooks() {
		return
	}

	// Run git diff --stat with a short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "diff", "--stat")
	out, err := cmd.Output()
	if err != nil {
		return // git not available or not in a repo — skip silently
	}

	diffOutput := strings.TrimSpace(string(out))
	if diffOutput == "" {
		return // no changes
	}

	// Hash the diff output to detect changes
	h := sha256.Sum256(out)
	hash := hex.EncodeToString(h[:8]) // short hash is sufficient for change detection

	if hash == d.lastEditDiffHash {
		return // no new changes since last check
	}
	d.lastEditDiffHash = hash

	// Persist hash to state file for cross-restart consistency
	_ = os.WriteFile(bus.EditDiffHashPath(d.session), []byte(hash), 0644)

	// Transition workflow to StateEditing
	bus.TransitionWorkflow(d.session, bus.StateEditing, "daemon:edit-diff",
		bus.WithFiles(extractDiffFiles(diffOutput)))

	// Write analyze trigger file with changed file paths
	triggerPath := bus.TriggerFile(d.session)
	var triggerLines []string
	ts := fmt.Sprintf("%d", now)
	for _, f := range extractDiffFiles(diffOutput) {
		triggerLines = append(triggerLines, ts+" "+f)
	}
	if len(triggerLines) > 0 {
		_ = os.WriteFile(triggerPath, []byte(strings.Join(triggerLines, "\n")+"\n"), 0644)
	}

	// Show changed files in Neovim (pane 0) so the user can see what
	// the non-hook edit agent (e.g. OpenCode) changed. The first file
	// opens with a git diff split (HEAD vs current); subsequent files
	// are just reloaded in the background.
	if files := extractDiffFiles(diffOutput); len(files) > 0 {
		showEditInNeovim(d.session, files[0], true) // diff split for first file
		for _, f := range files[1:] {
			time.Sleep(200 * time.Millisecond)
			showEditInNeovim(d.session, f, false) // reload only for remaining files
		}
	}
}

// showEditInNeovim reloads a changed file in the Neovim edit pane (pane 0)
// so the user can see what a non-hook edit agent (OpenCode, Codex) changed.
// When withDiff is true, it opens a git diff split showing HEAD vs current.
func showEditInNeovim(session, file string, withDiff bool) {
	pane := session + ":edit.0"

	// Escape single quotes for vimscript single-quoted strings.
	// fnameescape() handles all other special chars (%, #, |, spaces, etc).
	nvimFile := strings.ReplaceAll(file, "'", "''")
	// Shell-quote the file path for git commands inside :0r!
	shellFile := "'" + strings.ReplaceAll(file, "'", "'\\''") + "'"

	// Dismiss any pending "Press ENTER" prompts
	exec.Command("tmux", "send-keys", "-t", pane, "Escape").Run()
	time.Sleep(50 * time.Millisecond)
	exec.Command("tmux", "send-keys", "-t", pane, "Escape").Run()
	time.Sleep(50 * time.Millisecond)

	if withDiff {
		// Clean up any stale diff from a previous file
		exec.Command("tmux", "send-keys", "-t", pane,
			":sil! exe 'b!'.get(g:,'_mux_buf',bufnr()) | sil! diffoff! | sil! only | sil! set number",
			"Enter").Run()
		time.Sleep(200 * time.Millisecond)

		// Open file and create diff split with git HEAD version:
		//   Left (original): HEAD version from git
		//   Right (current): working tree version
		// Uses fnameescape() for safe vim path handling (spaces, %, #, etc.)
		// and shell-quoting for the git show command.
		cmd := fmt.Sprintf(":sil! exe 'e! ' . fnameescape('%s') | sil! setlocal foldlevel=99 | sil! set number | sil! diffthis | sil! new | sil! setlocal buftype=nofile bufhidden=wipe number | sil! exe '0r! git show HEAD:%s 2>/dev/null' | sil! 0delete _ | sil! diffthis | sil! setlocal foldlevel=99 | sil! norm! zR | sil! wincmd p | sil! setlocal foldlevel=99 | sil! norm! zR",
			nvimFile, shellFile)
		exec.Command("tmux", "send-keys", "-t", pane, cmd, "Enter").Run()
	} else {
		// Just reload the file without diff split
		exec.Command("tmux", "send-keys", "-t", pane,
			fmt.Sprintf(":sil! exe 'e! ' . fnameescape('%s') | sil! set number | sil! nohlsearch", nvimFile),
			"Enter").Run()
	}
}

// extractDiffFiles parses git diff --stat output to extract changed file paths.
// Each line has the format: " path/to/file | N ++--"
func extractDiffFiles(diffStat string) []string {
	var files []string
	for _, line := range strings.Split(diffStat, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "---") || strings.Contains(line, "files changed") || strings.Contains(line, "file changed") {
			continue
		}
		// Split on " | " to get the file path
		parts := strings.SplitN(line, " | ", 2)
		if len(parts) >= 1 {
			f := strings.TrimSpace(parts[0])
			if f != "" {
				files = append(files, f)
			}
		}
	}
	return files
}

const idleTaskGracePeriod int64 = 30

// checkIdleTaskCompletion is a safety net for hook-provider agents (Claude Code)
// that go idle without having responded to an in-flight task. This catches the
// failure mode where an agent composes a `muxcode send` command as text output
// in the TUI instead of executing it via the Bash tool — the response silently
// vanishes and the requester's --wait hangs forever.
//
// When detected (agent idle + in-flight task + grace period elapsed), captures
// the agent's pane content and sends a synthetic response back to the requester,
// similar to checkNonHookTasks() but triggered by idle detection instead of
// task completion heuristics.
//
// Runs every 10 seconds to avoid excessive tmux capture-pane calls.
func (d *Daemon) checkIdleTaskCompletion() {
	now := time.Now().Unix()
	if now-d.lastIdleTaskCheck < 10 {
		return
	}
	d.lastIdleTaskCheck = now

	// Find in-flight tasks
	tasks, err := bus.ListTasks(d.session, bus.TaskInFlight)
	if err != nil || len(tasks) == 0 {
		// Clean up tracking state when no in-flight tasks exist
		for k := range d.idleTaskFirstSeen {
			delete(d.idleTaskFirstSeen, k)
		}
		return
	}

	for _, task := range tasks {
		provider := bus.ResolveProvider(task.To)
		// Only handle hook providers — non-hook providers are covered by checkNonHookTasks
		if !provider.SupportsHooks() {
			continue
		}

		// Skip tasks that are too fresh (< 10s) — agent may still be working
		if now-task.SentAt < 10 {
			continue
		}

		// Check if the target agent is idle (at ❯ prompt)
		if !bus.IsAgentIdle(d.session, task.To) {
			// Agent is active — reset tracking for this task
			delete(d.idleTaskFirstSeen, task.ID)
			continue
		}

		// Agent is idle with an in-flight task — track grace period
		firstSeen, tracked := d.idleTaskFirstSeen[task.ID]
		if !tracked {
			d.idleTaskFirstSeen[task.ID] = now
			continue // start grace period
		}

		// Wait for grace period before acting
		if now-firstSeen < idleTaskGracePeriod {
			continue
		}

		// Grace period elapsed — re-read the task to confirm it's still in-flight.
		// The real agent may have responded during the grace period, in which
		// case CompleteTask already flipped the status.
		freshTask, freshErr := bus.ReadTask(d.session, task.ID)
		if freshErr != nil || freshTask.Status != bus.TaskInFlight {
			delete(d.idleTaskFirstSeen, task.ID)
			continue
		}

		// Agent is stuck idle without having responded.
		// Capture pane content for the synthetic response.
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Detected idle %s with unresponded task %s (idle %ds) — sending synthetic response\n",
			ts, task.To, task.Action, now-firstSeen)
		bus.LogLifecycle(d.session, "warn", "daemon", "idle-task-rescue",
			fmt.Sprintf("%s idle with unresponded task %s from %s (idle %ds)",
				task.To, task.Action, task.From, now-firstSeen))

		target := bus.PaneTarget(d.session, task.To)
		paneContent, err := bus.TmuxCapturePaneLines(target, 50)
		if err != nil {
			paneContent = "(pane capture failed)"
		}

		// Truncate for the message payload
		payload := paneContent
		if len(payload) > 2000 {
			payload = payload[:1997] + "..."
		}

		// Send synthetic response back to the original requester
		msg := bus.NewMessage(task.To, task.From, "response", "response",
			fmt.Sprintf("[daemon: %s went idle without responding — pane content follows]\n%s", task.To, payload),
			task.ID)
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [idle-task-rescue] failed to send response for %s: %v\n", task.ID, err)
			continue
		}

		// Mark the task as completed
		bus.CompleteTask(d.session, task.ID, msg.ID)

		// Log to console history so the left-pane console view updates.
		logTaskToConsoleHistory(d.session, task.To, task.Action, payload, false)

		// Clean up tracking state
		delete(d.idleTaskFirstSeen, task.ID)

		// Notify the requester
		_ = bus.Notify(d.session, task.From)
	}

	// Clean up tracking for tasks that are no longer in-flight
	taskIDs := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		taskIDs[t.ID] = true
	}
	for k := range d.idleTaskFirstSeen {
		if !taskIDs[k] {
			delete(d.idleTaskFirstSeen, k)
		}
	}
}

// logTaskToConsoleHistory writes a history entry to the role's console history
// JSONL file when the daemon detects task completion for a non-hook provider.
// This replaces the PostToolUse hook logging that hook-based providers use —
// without it, left-pane console views for non-hook agents remain empty.
func logTaskToConsoleHistory(session, role, action, output string, errored bool) {
	// Skip research — the research agent self-logs findings via `muxcode log`
	// with richer metadata (output-file content, structured fields) that
	// renderResearch expects.
	if role == "research" {
		return
	}

	outcome := "success"
	exitCode := "0"
	if errored {
		outcome = "failure"
		exitCode = "1"
	}

	// Build a summary from the action (e.g. "review", "build", "test")
	summary := action
	if len(output) > 200 {
		// Use first line as summary if output is long
		if idx := strings.Index(output, "\n"); idx > 0 && idx < 200 {
			summary = output[:idx]
		} else if len(output) > 200 {
			summary = output[:200] + "..."
		}
	} else if output != "" {
		summary = output
	}

	entry := bus.HookHistoryEntry{
		TS:       time.Now().Unix(),
		Command:  action,
		ExitCode: exitCode,
		Outcome:  outcome,
		Output:   output,
		Summary:  summary,
	}

	historyPath := bus.HistoryPath(session, role)
	if err := bus.WriteHookHistory(historyPath, entry, 100); err != nil {
		fmt.Fprintf(os.Stderr, "  [console-log] failed to write %s history: %v\n", role, err)
	}
}

// checkHeartbeat fires a heartbeat message to the agent role at the configured
// interval. The heartbeat triggers the agent to check for higher-priority stories,
// PR status updates, and stale delegations. Only fires if the agent role is active
// (has a running process in its pane). Writes the last heartbeat timestamp to
// the agent-last-heartbeat state file.
func (d *Daemon) checkHeartbeat() {
	if d.heartbeatInterval <= 0 {
		return
	}
	now := time.Now().Unix()
	if now-d.lastHeartbeatCheck < int64(d.heartbeatInterval) {
		return
	}
	d.lastHeartbeatCheck = now

	// Only fire if the auto role is known and has an active pane.
	// Skip if auto has no inbox (never launched).
	inboxPath := bus.InboxPath(d.session, "auto")
	if _, err := os.Stat(filepath.Dir(inboxPath)); os.IsNotExist(err) {
		return
	}

	// Write heartbeat timestamp to state file
	heartbeatPath := bus.AgentHeartbeatPath(d.session)
	_ = os.WriteFile(heartbeatPath, []byte(fmt.Sprintf("%d", now)), 0644)

	// Send heartbeat message to auto inbox
	msg := bus.NewMessage("daemon", "auto", "request", "heartbeat",
		"Heartbeat tick — check for higher-priority stories, PR status on open PRs, and stale delegations", "")
	if err := bus.Send(d.session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "  [heartbeat] failed to send to auto: %v\n", err)
		return
	}

	ts := time.Now().Format("15:04:05")
	fmt.Printf("  %s  Heartbeat fired to auto\n", ts)
	bus.LogLifecycle(d.session, "info", "daemon", "heartbeat", "auto")

	// Notify the auto agent
	_ = bus.Notify(d.session, "auto")
	d.refreshInboxSizes()
}

// checkCleanup removes expired delivery status and task files every 5 minutes.
// Delivery and task files accumulate from --wait send operations; without
// periodic cleanup they grow unbounded over long sessions.
func (d *Daemon) checkCleanup() {
	now := time.Now().Unix()
	if now-d.lastCleanupCheck < 300 {
		return
	}
	d.lastCleanupCheck = now

	// Clean delivery status files older than 1 hour
	deliveryCleaned := bus.CleanExpiredDeliveries(d.session, 1*time.Hour)
	// Clean task files older than 1 hour
	tasksCleaned := bus.CleanExpiredTasks(d.session, 1*time.Hour)
	// Clean stale reload markers (>60s — reload crashed or timed out)
	reloadCleaned := bus.CleanStaleReloadMarkers(d.session)

	if deliveryCleaned > 0 || tasksCleaned > 0 || reloadCleaned > 0 {
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Cleanup: %d delivery, %d task, %d reload marker files removed\n",
			ts, deliveryCleaned, tasksCleaned, reloadCleaned)
		bus.LogLifecycle(d.session, "info", "daemon", "cleanup",
			fmt.Sprintf("delivery=%d tasks=%d reload-markers=%d", deliveryCleaned, tasksCleaned, reloadCleaned))
	}
}

// formatDaemonBytes is a simple bytes formatter for daemon log lines.
func formatDaemonBytes(b int64) string {
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
