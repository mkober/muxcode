package watcher

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
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
}

// New creates a new Watcher for the given session.
func New(session string, pollSecs, debounceSecs int) *Watcher {
	now := time.Now().Unix()

	// Discover which roles use local LLM
	ollamaRoles := bus.LocalLLMRoles()

	// Read Ollama config for health probes
	ollamaCfg := bus.DefaultOllamaConfig()

	return &Watcher{
		session:          session,
		pollInterval:     time.Duration(pollSecs) * time.Second,
		debounceSecs:     debounceSecs,
		triggerFile:      bus.TriggerFile(session),
		inboxSizes:       make(map[string]int64),
		lastAlertKey:     make(map[string]int64),
		lastLoopCheck:    now, // skip first interval — avoids stale alerts on startup
		lastCompactCheck: now, // skip first interval — avoids stale alerts on startup
		lastOllamaCheck:  now, // skip first interval
		ollamaRoles:      ollamaRoles,
		ollamaURL:        ollamaCfg.BaseURL,
		ollamaModel:      ollamaCfg.Model,
	}
}

// Run starts the main watcher loop. It never returns under normal operation.
func (w *Watcher) Run() error {
	busDir := bus.BusDir(w.session)

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
		w.checkInboxes()
		w.checkTrigger()
		w.checkCron()
		w.checkProcs()
		w.checkSpawns()
		w.checkLoops()
		w.checkCompaction()
		w.checkOllama()
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
// Only notifies agents that are NOT directly targeted by cmd/send.go.
// cmd/send.go already calls Notify() for the direct recipient, and
// auto-CC'd messages to edit don't need immediate notification since
// edit will see them on its next inbox read. The watcher's role is to
// catch messages that arrive without a Notify (e.g. auto-CC).
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
			// Skip edit — cmd/send.go notifies on direct sends, and
			// auto-CC messages are seen on next inbox read. Notifying
			// here causes duplicates.
			if role != "edit" {
				ts := time.Now().Format("15:04:05")
				fmt.Printf("  %s  New message(s) for %s — notifying\n", ts, role)
				_ = bus.Notify(w.session, role)
			}
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

		// Notify target agent
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

		payload := fmt.Sprintf("Background process completed: %s\n  Command: %s\n  Status: %s  Exit code: %d\n  Log: %s",
			entry.ID, entry.Command, entry.Status, entry.ExitCode, entry.LogFile)

		msg := bus.NewMessage("proc", entry.Owner, "event", "proc-complete", payload, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [proc] failed to send completion event to %s: %v\n", entry.Owner, err)
			continue
		}

		// Skip Notify for edit — tmux send-keys disrupts Claude Code input buffer
		if entry.Owner != "edit" {
			if err := bus.Notify(w.session, entry.Owner); err != nil {
				fmt.Fprintf(os.Stderr, "  [proc] failed to notify %s: %v\n", entry.Owner, err)
			}
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

		// Skip Notify for edit — tmux send-keys disrupts Claude Code input buffer
		if entry.Owner != "edit" {
			if err := bus.Notify(w.session, entry.Owner); err != nil {
				fmt.Fprintf(os.Stderr, "  [spawn] failed to notify %s: %v\n", entry.Owner, err)
			}
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

		msg := bus.NewMessage("watcher", "edit", "event", "loop-detected", alert.Message, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to send loop alert: %v\n", err)
			continue
		}
		// Skip Notify for edit — same pattern as checkInboxes(). Edit reads
		// its inbox frequently; injecting tmux send-keys while Claude Code
		// is mid-turn causes text to get stuck in the input buffer.
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

		msg := bus.NewMessage("watcher", alert.Role, "event", "compact-recommended", alert.Message, "")
		if err := bus.Send(w.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [compact] failed to send compact alert to %s: %v\n", alert.Role, err)
			continue
		}
		// Skip Notify for edit — tmux send-keys disrupts Claude Code input buffer
		if alert.Role != "edit" {
			if err := bus.Notify(w.session, alert.Role); err != nil {
				fmt.Fprintf(os.Stderr, "  [compact] failed to notify %s: %v\n", alert.Role, err)
			}
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
