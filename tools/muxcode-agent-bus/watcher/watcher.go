package watcher

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/mkober/muxcode/tools/muxcode-agent-bus/bus"
)

// Watcher monitors agent inboxes and a trigger file for file-edit events.
type Watcher struct {
	session         string
	pollInterval    time.Duration
	debounceSecs    int
	triggerFile     string
	inboxSizes      map[string]int64
	lastTriggerSize int64
	pendingSince    int64
	cronEntries     []bus.CronEntry
	lastCronLoad    int64
	lastLoopCheck   int64
	lastAlertKey    map[string]int64
}

// New creates a new Watcher for the given session.
func New(session string, pollSecs, debounceSecs int) *Watcher {
	return &Watcher{
		session:      session,
		pollInterval: time.Duration(pollSecs) * time.Second,
		debounceSecs: debounceSecs,
		triggerFile:  bus.TriggerFile(session),
		inboxSizes:   make(map[string]int64),
		lastAlertKey: make(map[string]int64),
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
	fmt.Println()

	for {
		w.checkInboxes()
		w.checkTrigger()
		w.checkCron()
		w.checkLoops()
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
func (w *Watcher) loadCron() {
	now := time.Now().Unix()
	if now-w.lastCronLoad < 10 {
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

// checkLoops runs loop detection every 30 seconds and sends alerts to the edit agent.
// Deduplicates alerts within a 5-minute cooldown to avoid spamming.
func (w *Watcher) checkLoops() {
	now := time.Now().Unix()
	if now-w.lastLoopCheck < 30 {
		return
	}
	w.lastLoopCheck = now

	alerts := bus.CheckAllLoops(w.session)
	if len(alerts) == 0 {
		return
	}

	// Filter out alerts that were already sent within the cooldown window
	fresh := bus.FilterNewAlerts(alerts, w.lastAlertKey, 300)
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
		if err := bus.Notify(w.session, "edit"); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to notify edit: %v\n", err)
		}
	}

	w.refreshInboxSizes()
}
