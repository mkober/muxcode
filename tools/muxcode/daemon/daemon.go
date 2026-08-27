package daemon

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

	ollamaRoles     []string
	lastOllamaCheck int64
	ollamaFailCount int  // consecutive probe failures
	ollamaWasDown   bool // set while down, so recovery can be announced once
	ollamaRestarts  int  // capped at 3 to prevent restart loops
	ollamaURL       string
	ollamaModel     string

	lastAgentHealthCheck int64
	agentFailCounts      map[string]int  // role → consecutive liveness failures
	agentRestarts        map[string]int  // role → restart count (capped at 3)
	agentWasDown         map[string]bool // set while down, so recovery can be announced once

	lastCleanupCheck      int64
	lastDiskPressureCheck int64
	lastAutoClearCheck    int64
	lastPromptAgentStart  int64
	ollamaWarmingSince    int64

	lastIdleCheck        int64
	lastNonHookWake      map[string]int64 // role → last non-hook wake time (60s cooldown)
	lastIdleState        map[string]bool  // role → idle state last check, for transition detection
	activeUnnotifiedSeen map[string]int64 // role → when unnotified msgs first seen while "active"
	forceWakeCount       map[string]int   // role → force-deliveries this episode (churn cap)
	churnSuppressed      map[string]bool  // role → force-delivery suppressed until idle / drained

	lastTaskCheck       int64
	taskDeliveredAt     map[string]int64  // msgID → when its wake-up was sent
	taskLastPaneContent map[string]string // role → last pane hash, skips identical re-processing

	lastIdleTaskCheck int64
	idleTaskFirstSeen map[string]int64 // taskID → when first observed idle with in-flight task
	idleTaskRetried   map[string]bool  // taskID → request already re-queued once

	lastHeartbeatCheck int64
	heartbeatInterval  int // seconds; 0 = disabled

	lastEditDiffCheck int64
	lastEditDiffHash  string // last observed `git diff --stat` hash

	lastTrackedTaskCheck int64

	safetyNetRetries map[string]int   // role → consecutive safety-net clears (storm cap)
	safetyNetLast    map[string]int64 // role → last safety-net clear

	lastPaneSweep   int64
	parkedSeen      map[string]string // role → parked prompt text observed last sweep
	lastParkedCheck int64
	parkedResubmits map[string]int // role → consecutive Enter-resubmit attempts

	lastServeCheck    int64
	serveCheckSentFor map[string]int64 // url → last browser-check sent

	lastEventSent map[string]int64 // "action:key" → last send (5-min dedup window)

	editNotifyCount int   // messages sent to edit in the current budget window
	editWindowStart int64 // when the current budget window started

	lastActiveWatchdogCheck int64
	activeSince             map[string]int64 // role → when agent went active (0 = idle)
	lastActiveNudge         map[string]int64 // role → last long-active nudge
	activeNudgeCount        map[string]int   // role → nudges this episode (capped; reset on idle)

	lastStuckCheck  int64
	stuckSeen       map[string]int   // role → consecutive provider-loop sightings (debounce)
	stuckReloads    map[string]int   // role → auto-reload count (capped)
	lastStuckReload map[string]int64 // role → last auto-reload (cooldown)
	stuckGaveUp     map[string]bool  // role → alerted once after hitting the reload cap

	lastPermBlockCheck int64
	permBlockSeen      map[string]int  // role → consecutive permission-block sightings (debounce)
	permBlocked        map[string]bool // role → re-notification suppressed while blocked
	permBlockAlerted   map[string]bool // role → alerted edit once for the current block

	lastAgentDefsCheck int64
	lastPaneSupervise  int64
	paneRecycleDone    bool  // recycle-on-install decided (once per daemon start)
	startedAt          int64 // daemon start time — recycle only panes that predate it

	lastPollHealthCheck int64
	pollGapSince        map[string]int64 // role → when a receipt gap first appeared (0 = none)
	pollGapAlerted      map[string]bool  // role → alerted edit once for the current gap
	pollGapRecovered    map[string]bool  // role → re-drive attempted for the current gap

	lastStallCheck int64
	taskStallSeen  map[string]int // task id → consecutive stall sightings (debounce)
	taskRedrives   map[string]int // task id → redrive count (capped)

	lastForceRespondCheck int64
	frRung                map[string]int      // role → next escalation rung to fire
	frLastFire            map[string]int64    // role → when the last rung fired (cooldown)
	frPostponed           map[string]int      // role → override postponements this episode (capped)
	frHistory             map[string][]string // role → fired-rung history for the final alert

	// Injectable seams — tests drive these without tmux or provider resolution.
	frNotify    func(session, role string) error
	frDeliver   func(session, role string, force bool) error
	frIsIdle    func(session, role string) bool
	frPaneGated func(role string) bool
	agentAlive  func(session, role string) bool
	windowNames func(session string) ([]string, error)

	lastBranchTick      int64
	lastBranch          string // branch the pending seconds belong to
	branchTimePending   int64  // accrued seconds not yet flushed to the ledger
	branchTimeRepoKey   string // cached RepoKey (stable per repo)
	lastBranchFlush     int64
	branchTimeInit      bool // first-tracking lifecycle event already logged
	branchTimeErrLogged bool // current flush-error state already logged
}

// New creates a new Daemon for the given session.
func New(session string, pollSecs, debounceSecs int) *Daemon {
	now := time.Now().Unix()

	// Discover which roles use local LLM. The prompt role is NOT baked in
	// here: its backend is runtime-switchable via the shell config, so
	// its membership is evaluated per tick in effectiveOllamaRoles — a
	// New()-time snapshot went stale the moment the user flipped
	// MUXCODE_PROMPT_BACKEND mid-session (review catch, 2026-08-27).
	ollamaRoles := bus.LocalLLMRoles()

	// Read Ollama config for health probes
	ollamaCfg := bus.DefaultOllamaConfig()

	return &Daemon{
		session:               session,
		pollInterval:          time.Duration(pollSecs) * time.Second,
		debounceSecs:          debounceSecs,
		triggerFile:           bus.TriggerFile(session),
		inboxSizes:            make(map[string]int64),
		lastAlertKey:          make(map[string]int64),
		lastLoopCheck:         now, // skip first interval — avoids stale alerts on startup
		lastCompactCheck:      now, // skip first interval — avoids stale alerts on startup
		lastOllamaCheck:       now, // skip first interval
		ollamaRoles:           ollamaRoles,
		ollamaURL:             ollamaCfg.BaseURL,
		ollamaModel:           ollamaCfg.Model,
		lastAgentHealthCheck:  now, // skip first interval
		agentFailCounts:       make(map[string]int),
		agentRestarts:         make(map[string]int),
		agentWasDown:          make(map[string]bool),
		lastNonHookWake:       make(map[string]int64),
		lastIdleState:         make(map[string]bool),
		activeUnnotifiedSeen:  make(map[string]int64),
		forceWakeCount:        make(map[string]int),
		churnSuppressed:       make(map[string]bool),
		taskDeliveredAt:       make(map[string]int64),
		taskLastPaneContent:   make(map[string]string),
		idleTaskFirstSeen:     make(map[string]int64),
		idleTaskRetried:       make(map[string]bool),
		lastHeartbeatCheck:    now,
		heartbeatInterval:     bus.AgentHeartbeatInterval(),
		lastDiskPressureCheck: now,
		safetyNetRetries:      make(map[string]int),
		safetyNetLast:         make(map[string]int64),
		lastPaneSweep:         now,
		parkedSeen:            make(map[string]string),
		parkedResubmits:       make(map[string]int),
		lastServeCheck:        now,
		serveCheckSentFor:     make(map[string]int64),
		lastEventSent:         make(map[string]int64),
		editWindowStart:       now,
		activeSince:           make(map[string]int64),
		lastActiveNudge:       make(map[string]int64),
		activeNudgeCount:      make(map[string]int),
		stuckSeen:             make(map[string]int),
		stuckReloads:          make(map[string]int),
		lastStuckReload:       make(map[string]int64),
		stuckGaveUp:           make(map[string]bool),
		permBlockSeen:         make(map[string]int),
		permBlocked:           make(map[string]bool),
		permBlockAlerted:      make(map[string]bool),
		lastAgentDefsCheck:    now, // skip first interval — lets stamps settle on startup
		lastPaneSupervise:     now, // skip first interval — the launcher owns launch-time pane creation; a sweep mid-launch sees half-built windows as pane-less and double-creates
		startedAt:             now,
		pollGapSince:          make(map[string]int64),
		pollGapAlerted:        make(map[string]bool),
		pollGapRecovered:      make(map[string]bool),
		taskStallSeen:         make(map[string]int),
		taskRedrives:          make(map[string]int),
		frRung:                make(map[string]int),
		frLastFire:            make(map[string]int64),
		frPostponed:           make(map[string]int),
		frHistory:             make(map[string][]string),
		frNotify:              bus.Notify,
		frDeliver: func(session, role string, force bool) error {
			_, err := bus.ForceDeliver(session, role, force)
			return err
		},
		frIsIdle: func(session, role string) bool {
			return bus.ResolveProvider(role).IsIdle(session, role)
		},
		frPaneGated: func(role string) bool {
			return bus.ResolveProvider(role).SupportsHooks()
		},
		agentAlive:  bus.IsAgentAlive,
		windowNames: bus.TmuxListWindowNames,
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
	fmt.Printf("  Poll: %ds  Debounce: %ds  Msg-check: %ds\n", int(d.pollInterval.Seconds()), d.debounceSecs, msgCheckSecs())
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
		d.checkActiveWatchdog()
		d.checkStuckProviders()
		d.checkStuckPermissions()
		d.checkAgentDefs()
		d.checkCompaction()
		d.checkOllama()
		d.checkAgentHealth()
		d.checkIdleAgents()
		d.checkParkedInput()
		d.checkPaneSweep()
		d.checkPollHealth()
		d.checkForceRespond()
		d.checkControlPanes()
		d.checkNonHookTasks()
		d.checkTrackedTasks()
		d.checkStalledTasks()
		d.checkGraphRuns()
		d.checkNonHookEdits()
		d.checkIdleTaskCompletion()
		d.checkHeartbeat()
		d.checkServeHealth()
		d.checkCleanup()
		d.checkDiskPressure()
		d.checkAutoClear()
		d.checkPromptAgent()
		d.checkBranchTime()
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

				// Notify plan agent to verify progress against active spec
				d.notifyPlanOnReview()
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

// notifyPlanOnReview sends a verification request to the plan agent after
// review completion, if an active spec is set and the review chain is
// configured to notify plan (NotifyPlanOn).
//
// Branch active-time recording rides this same pass as one extra sentence
// naming the branch; the procedure it refers to lives in the planner agent
// definition rather than being restated in every message. The sentence is
// omitted for ignored branches (main/master by default), which accrue no time,
// and when the session directory cannot be resolved — falling back to the
// daemon's cwd there would name a foreign repo's branch and have plan record
// time against the wrong one.
func (d *Daemon) notifyPlanOnReview() {
	if !bus.ChainShouldNotifyPlan("review", "success") {
		return
	}

	specPath := bus.ReadActiveSpec(d.session)
	if specPath == "" {
		return
	}

	// Get changed files from workflow state
	wf := bus.ReadWorkflowState(d.session)
	files := strings.Join(wf.LastFiles, ", ")
	if files == "" {
		files = "(unknown)"
	}

	timeInstruction := ""
	repoDir := bus.SessionRepoDir(d.session)
	if branch := bus.CurrentBranchIn(repoDir); repoDir != "" && !bus.BranchTimeIgnored(branch) {
		timeInstruction = fmt.Sprintf(
			" Also record active time for branch %s into the spec's ## Time Tracking table, "+
				"following the recording process in your agent definition.", branch)
	}

	planMsg := fmt.Sprintf(
		"Review complete — verify progress against spec %s. Changed files: %s. "+
			"Read the spec and the changed files, determine which acceptance criteria and phase steps "+
			"are now satisfied, check them off (- [ ] to - [x]), and update the status field if a phase is complete.%s "+
			"Reply to edit with a summary of what was verified.",
		specPath, files, timeInstruction,
	)

	msg := bus.NewMessage("daemon", "plan", "request", "verify-spec", planMsg, "")
	if err := bus.Send(d.session, msg); err != nil {
		fmt.Fprintf(os.Stderr, "  [daemon] failed to send plan verification: %v\n", err)
		return
	}

	ts := time.Now().Format("15:04:05")
	fmt.Printf("  %s  Review complete — notifying plan to verify spec\n", ts)
	bus.LogLifecycle(d.session, "info", "daemon", "plan-verify", specPath)
	_ = bus.Notify(d.session, "plan")
	d.refreshInboxSizes()
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

		if !d.shouldSendEvent("loop-detected", alert.Role) {
			continue
		}
		if !d.shouldNotifyEdit("event") {
			continue
		}
		msg := bus.NewMessage("daemon", "edit", "event", "loop-detected", alert.Message, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to send loop alert: %v\n", err)
			continue
		}
		if err := bus.Notify(d.session, "edit"); err != nil {
			fmt.Fprintf(os.Stderr, "  [guard] failed to notify edit: %v\n", err)
		}
	}

	d.refreshInboxSizes()
}

// activeWatchdogSecs returns how long an agent may be continuously active
// before the watchdog nudges it. Configurable via MUXCODE_ACTIVE_WATCHDOG_SECS
// (0 disables). Default 600s (10 minutes).
func activeWatchdogSecs() int64 {
	if v := os.Getenv("MUXCODE_ACTIVE_WATCHDOG_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return 600
}

// activeWatchdogMaxNudges returns the maximum number of long-active advisories
// the watchdog will queue during a single continuous-active episode before going
// silent (until the agent returns to idle, which resets the count). This bounds
// the inbox pile-up that would otherwise grow one advisory per threshold window
// indefinitely. Configurable via MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES. Default 2.
func activeWatchdogMaxNudges() int {
	if v := os.Getenv("MUXCODE_ACTIVE_WATCHDOG_MAX_NUDGES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return n
		}
	}
	return 2
}

// checkActiveWatchdog detects an agent that has been continuously "active"
// (not at the idle ❯ prompt) for longer than the threshold — e.g. a runaway
// multi-minute think churning over contradictory data — and queues a one-time
// advisory into the agent's own inbox suggesting it summarize and escalate to
// the user rather than re-deriving. The advisory is delivered when the agent
// next becomes idle (combined-notification path), so it never interrupts work.
//
// Scoped to hook providers (IsAgentIdle is unreliable for non-hook TUIs) and
// skips agents legitimately blocked on --wait/--poll, reloading, or harness
// panes. Runs every 60s.
func (d *Daemon) checkActiveWatchdog() {
	threshold := activeWatchdogSecs()
	if threshold <= 0 {
		return
	}
	now := time.Now().Unix()
	if now-d.lastActiveWatchdogCheck < 60 {
		return
	}
	d.lastActiveWatchdogCheck = now

	for _, role := range bus.KnownRoles {
		// Own pane only — hosted roles share their host's pane.
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) {
			d.activeSince[role] = 0
			d.activeNudgeCount[role] = 0
			continue
		}
		if bus.IsHarnessActive(d.session, role) {
			continue
		}
		// Non-hook providers (OpenCode/Codex) report IsAgentIdle==false always,
		// which would false-positive every cycle. Skip them.
		if provider := bus.ResolveProvider(role); !provider.SupportsHooks() {
			continue
		}
		// Legitimate long-running blocks: an agent draining a --wait delegation
		// or actively polling its inbox is "active" by design, not stuck.
		if bus.IsWaiting(d.session, role) || bus.IsPolling(d.session, role) {
			d.activeSince[role] = 0
			d.activeNudgeCount[role] = 0
			continue
		}

		if bus.IsAgentIdle(d.session, role) {
			d.activeSince[role] = 0
			d.activeNudgeCount[role] = 0
			continue
		}

		// Agent is active — track the start of this active spell.
		if d.activeSince[role] == 0 {
			d.activeSince[role] = now
			continue
		}
		dur := now - d.activeSince[role]
		if dur < threshold {
			continue
		}
		// Re-nudge at most once per threshold window while still active.
		if last, ok := d.lastActiveNudge[role]; ok && (now-last) < threshold {
			continue
		}
		// Cap total advisories per active-episode so the inbox doesn't accumulate
		// one long-active message per threshold window indefinitely (the churn the
		// user hit). 0 disables advisories entirely (never nudge). Reset happens
		// when the agent returns to idle above.
		if d.activeNudgeCount[role] >= activeWatchdogMaxNudges() {
			continue
		}
		d.lastActiveNudge[role] = now
		d.activeNudgeCount[role]++

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Active watchdog: %s active %s with no idle return — nudging to escalate (%d/%d)\n",
			ts, role, formatWatchdogDuration(dur), d.activeNudgeCount[role], activeWatchdogMaxNudges())
		bus.LogLifecycle(d.session, "warn", "daemon", "active-watchdog",
			fmt.Sprintf("%s active %ds", role, dur))

		advisory := fmt.Sprintf(
			"You have been active %s with no return to idle. If you are stuck on contradictory "+
				"or unverifiable data, STOP re-deriving — summarize the conflict and escalate to "+
				"the user with what you know and what you cannot resolve. Note: PII-sensitive "+
				"reads (run/watch/api output) are scrubbed; never measure lengths/sizes/counts "+
				"over scrubbed text — verify via a non-PII aggregation instead.",
			formatWatchdogDuration(dur))
		msg := bus.NewMessage("daemon", role, "event", "long-active", advisory, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [watchdog] failed to queue advisory for %s: %v\n", role, err)
		}
	}
}

// formatWatchdogDuration renders a second count as a compact "Xm" / "Xm Ys".
func formatWatchdogDuration(secs int64) string {
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	m := secs / 60
	s := secs % 60
	if s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

// stuckReloadCap is the max number of automatic reloads the stuck-provider
// watchdog will attempt for a single role before giving up and alerting edit.
const stuckReloadCap = 3

// stuckReloadCooldownSecs is the minimum gap between automatic reloads of the
// same role — long enough for a relaunched agent to settle before re-judging.
const stuckReloadCooldownSecs int64 = 180

// checkStuckProviders auto-reloads non-hook agents (OpenCode/Codex) wedged in
// a provider-side loop or fatal request-validation error. The agent process
// stays alive+active in this state, so checkAgentHealth (dead-process restart)
// and checkIdleAgents (idle wake-up) never fire — the agent would spin forever.
//
// A signature must persist across two consecutive checks (~60s apart) before a
// reload fires, so a transient single error the agent recovers from is ignored.
// Reloads are capped per role and rate-limited to avoid reload storms; on
// hitting the cap the watchdog alerts edit once and stops. Runs every 60s.
func (d *Daemon) checkStuckProviders() {
	if os.Getenv("MUXCODE_STUCK_RELOAD_DISABLE") == "1" {
		return
	}
	now := time.Now().Unix()
	if now-d.lastStuckCheck < 60 {
		return
	}
	d.lastStuckCheck = now

	for _, role := range bus.KnownRoles {
		// Own pane only; skip reloading/harness panes.
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) || bus.IsHarnessActive(d.session, role) {
			continue
		}
		// Only non-hook providers exhibit this wedge — Claude Code recovers via
		// its own mechanisms and IsAgentIdle works for it.
		if bus.ResolveProvider(role).SupportsHooks() {
			continue
		}
		// A dead agent is handled by checkAgentHealth's restart path.
		if !bus.IsAgentAlive(d.session, role) {
			delete(d.stuckSeen, role)
			continue
		}

		target := bus.PaneTarget(d.session, role)
		content, err := bus.TmuxCapturePaneLines(target, 60)
		if err != nil {
			continue
		}
		if !bus.PaneShowsProviderLoop(content) {
			delete(d.stuckSeen, role)
			d.stuckGaveUp[role] = false // recovered — re-arm alerting
			continue
		}

		// Two-sighting debounce.
		d.stuckSeen[role]++
		if d.stuckSeen[role] < 2 {
			continue
		}

		// Cap reached — alert edit once, then stop auto-reloading.
		if d.stuckReloads[role] >= stuckReloadCap {
			if !d.stuckGaveUp[role] {
				d.stuckGaveUp[role] = true
				ts := time.Now().Format("15:04:05")
				fmt.Printf("  %s  Stuck-provider watchdog: %s still wedged after %d reloads — giving up\n", ts, role, stuckReloadCap)
				bus.LogLifecycle(d.session, "error", "daemon", "stuck-provider-giveup",
					fmt.Sprintf("%s wedged after %d reloads", role, stuckReloadCap))
				if d.shouldSendEvent("agent-stuck", role) && d.shouldNotifyEdit("event") {
					msg := bus.NewMessage("daemon", "edit", "event", "agent-stuck",
						fmt.Sprintf("%s is wedged in a provider loop and did not recover after %d auto-reloads — manual intervention needed (try: muxcode reload %s --model <other>).", role, stuckReloadCap, role), "")
					if err := bus.Send(d.session, msg); err == nil {
						_ = bus.Notify(d.session, "edit")
					}
				}
			}
			continue
		}

		// Cooldown between reloads.
		if last, ok := d.lastStuckReload[role]; ok && (now-last) < stuckReloadCooldownSecs {
			continue
		}

		d.stuckReloads[role]++
		d.lastStuckReload[role] = now
		delete(d.stuckSeen, role)

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Stuck-provider watchdog: %s wedged in provider loop — auto-reloading (attempt %d/%d)\n",
			ts, role, d.stuckReloads[role], stuckReloadCap)
		bus.LogLifecycle(d.session, "warn", "daemon", "stuck-provider-reload",
			fmt.Sprintf("%s auto-reload %d/%d", role, d.stuckReloads[role], stuckReloadCap))

		// Reload in a goroutine — GracefulStop + relaunch blocks several
		// seconds; the reload marker (set first by ReloadAgent) makes the next
		// poll cycles skip this role. Reload in place (keep current CLI/model).
		go func(r string) {
			if err := bus.ReloadAgent(d.session, r, "", "", false); err != nil {
				fmt.Fprintf(os.Stderr, "  [watchdog] auto-reload of %s failed: %v\n", r, err)
			}
		}(role)
	}
}

// safetyNetMaxRetries caps safety-net delivery retries within one window, so a
// TUI that genuinely cannot accept input is not stormed with injections.
const safetyNetMaxRetries = 3

// safetyNetRetryWindowSecs is how long a role's exhausted safety-net retry
// budget stays spent before it refills. The budget caps delivery retries at
// safetyNetMaxRetries per window so a genuinely unresponsive TUI can't be
// stormed with injections, while still guaranteeing that a stuck-but-idle agent
// is retried again later instead of being abandoned until someone runs
// `muxcode deliver --force`.
const safetyNetRetryWindowSecs int64 = 60

// safetyNetBudgetAvailable reports whether role may spend a safety-net delivery
// retry now, refilling an exhausted budget once the retry window has elapsed.
//
// The refill is the whole point. The only other reset is the active transition
// in checkIdleAgents (!isIdle && wasIdle), which requires the agent to PROCESS a
// message — the very thing a failed delivery prevents. So an agent whose
// send-keys injections all dropped would burn its budget, never go active, never
// reset, and sit idle with an actionable message indefinitely: the bus looks
// dead, and only a manual `muxcode deliver <role> --force` recovers it. Observed
// live on the review agent, twice. Refilling on a timer bounds the retry rate
// without ever abandoning the agent.
func (d *Daemon) safetyNetBudgetAvailable(role string, now int64) bool {
	if last, ok := d.safetyNetLast[role]; ok && now-last >= safetyNetRetryWindowSecs {
		d.safetyNetRetries[role] = 0
	}
	return d.safetyNetRetries[role] < safetyNetMaxRetries
}

// spendSafetyNetRetry records one safety-net delivery retry for role.
func (d *Daemon) spendSafetyNetRetry(role string, now int64) {
	d.safetyNetRetries[role]++
	d.safetyNetLast[role] = now
	if d.safetyNetRetries[role] >= safetyNetMaxRetries {
		// Budget just ran out — say so. A silently-exhausted budget is exactly
		// what made this failure mode look like a dead bus rather than a stuck
		// agent. It will refill after safetyNetRetryWindowSecs.
		bus.LogLifecycle(d.session, "warn", "daemon", "safety-net-exhausted", role)
	}
}

// permBlockCheckSecs is the polling interval for the permission-block watchdog.
const permBlockCheckSecs int64 = 30

// permBlockDebounce is the number of consecutive sightings of a permission-block
// signature required before the watchdog acts. Avoids reacting to a transient
// live prompt a human is about to approve.
const permBlockDebounce = 2

// checkStuckPermissions breaks the re-notification loop that forms when a
// hook-provider (Claude Code) agent is wedged at a REJECTED permission prompt it
// can never satisfy autonomously — e.g. `./build.sh` denied with no human to
// approve. The agent never sends a response, so its request stays actionable and
// the idle-delivery safety net re-wakes it endlessly (each active→idle flap
// resets the retry counter), while the requester hangs forever waiting.
//
// Unlike checkStuckProviders (non-hook providers, auto-reload), this targets
// hook providers and only ALERTS edit + suppresses re-delivery (d.permBlocked).
// It never reloads and never fabricates a response. Suppression is lifted
// automatically by clearPermBlock once the pane no longer shows a block
// signature (the agent recovered or the user intervened) or the pending request
// drains. Opt out with MUXCODE_PERMBLOCK_WATCHDOG_DISABLE=1.
func (d *Daemon) checkStuckPermissions() {
	if os.Getenv("MUXCODE_PERMBLOCK_WATCHDOG_DISABLE") == "1" {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPermBlockCheck < permBlockCheckSecs {
		return
	}
	d.lastPermBlockCheck = now

	for _, role := range bus.KnownRoles {
		// Own pane only; skip hosted/reloading/harness panes.
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) || bus.IsHarnessActive(d.session, role) {
			continue
		}
		// Hook providers only — non-hook wedges are handled by
		// checkStuckProviders, and the permission-prompt model is
		// Claude-Code-specific.
		if !bus.ResolveProvider(role).SupportsHooks() {
			continue
		}
		// A dead agent is handled by checkAgentHealth's restart path.
		if !bus.IsAgentAlive(d.session, role) {
			d.clearPermBlock(role)
			continue
		}
		// Only meaningful while an actionable request is pending and unanswered —
		// that is exactly what the loop re-delivers. With nothing pending, any
		// "permission" text in the pane is harmless scrollback.
		if !bus.HasActionableMessages(d.session, role) {
			d.clearPermBlock(role)
			continue
		}

		target := bus.PaneTarget(d.session, role)
		content, err := bus.TmuxCapturePaneLines(target, 60)
		if err != nil {
			continue
		}
		if !bus.PaneShowsPermissionBlock(content) {
			// Signature gone — block cleared / recovered. Re-arm + resume delivery.
			d.clearPermBlock(role)
			continue
		}

		// Debounce: require consecutive sightings before acting.
		d.permBlockSeen[role]++
		if d.permBlockSeen[role] < permBlockDebounce {
			continue
		}

		// Suppress re-notification so checkIdleAgents stops waking the agent.
		d.permBlocked[role] = true

		// Alert edit once per block.
		if !d.permBlockAlerted[role] {
			d.permBlockAlerted[role] = true
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  Permission-block watchdog: %s wedged at a rejected permission prompt — suppressing re-notification, alerting edit\n", ts, role)
			bus.LogLifecycle(d.session, "warn", "daemon", "permission-blocked", role)
			if d.shouldSendEvent("permission-blocked", role) && d.shouldNotifyEdit("event") {
				msg := bus.NewMessage("daemon", "edit", "event", "permission-blocked",
					fmt.Sprintf("%s is stuck at a rejected permission prompt (a command was denied with no human to approve) and cannot finish its request — re-notification suppressed to stop the loop. Fix the agent's tool permissions or relaunch it with bypass-permissions, then re-send the request.", role), "")
				if err := bus.Send(d.session, msg); err == nil {
					_ = bus.Notify(d.session, "edit")
				}
			}
		}
	}
}

// clearPermBlock re-arms the permission-block watchdog for a role: it resets the
// sighting counter and, if the role was suppressed, lifts the suppression and
// logs that delivery has resumed. Called when the block signature clears, the
// pending request drains, or the agent dies.
func (d *Daemon) clearPermBlock(role string) {
	delete(d.permBlockSeen, role)
	if d.permBlocked[role] {
		d.permBlocked[role] = false
		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Permission-block watchdog: %s recovered — resuming delivery\n", ts, role)
		bus.LogLifecycle(d.session, "info", "daemon", "permission-block-cleared", role)
	}
	d.permBlockAlerted[role] = false
}

// agentDefsCheckSecs is the polling interval for the agent-definition watchdog.
const agentDefsCheckSecs int64 = 10

// checkAgentDefs auto-reloads a running agent when its resolved definition file
// changes on disk, so editing/reinstalling an agent definition takes effect
// without a manual `muxcode reload`.
//
// The definition the running agent launched with is stamped to a per-session
// marker file by RunAgentLaunch (bus/agentdefs.go). This compares that stamp to
// the current on-disk hash; a mismatch means the definition changed since the
// agent launched. Using an on-disk stamp (not in-memory state) is what makes
// this survive the daemon restart that `./build.sh` triggers via
// `upgrade-daemons` right after reinstalling the defs.
//
// Reloads are deferred while an agent is busy, so a running build/test is never
// interrupted — the reload fires on the next cycle once the agent is idle. The
// orchestrator (edit) and autonomous (auto) agents are never auto-reloaded.
// Opt out with MUXCODE_AGENTDEFS_WATCH_DISABLE=1.
func (d *Daemon) checkAgentDefs() {
	if os.Getenv("MUXCODE_AGENTDEFS_WATCH_DISABLE") == "1" {
		return
	}
	now := time.Now().Unix()
	if now-d.lastAgentDefsCheck < agentDefsCheckSecs {
		return
	}
	d.lastAgentDefsCheck = now

	for _, role := range bus.ReloadableRoles() {
		// Never auto-reload the orchestrator or autonomous agent out from under
		// active work — they drive everything else.
		if role == "edit" || role == "auto" {
			continue
		}
		// Own pane only; skip panes mid-reload or running the local harness.
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) || bus.IsHarnessActive(d.session, role) {
			continue
		}

		onDisk := bus.AgentDefHash(role)
		if onDisk == "" {
			continue
		}
		stamp := bus.ReadAgentDefHash(d.session, role)
		if stamp == "" {
			// No stamp yet (agent launched before this feature shipped, or the
			// stamp was lost). Seed a baseline without reloading — only future
			// changes trigger an auto-reload.
			bus.StampAgentDefHash(d.session, role)
			continue
		}
		if onDisk == stamp {
			continue
		}

		// Definition changed since the agent launched. A dead agent will pick up
		// the new definition when checkAgentHealth restarts it.
		if !bus.IsAgentAlive(d.session, role) {
			continue
		}
		// Defer while busy so we never interrupt in-flight work. Leave the stamp
		// unchanged so we retry once the agent is idle.
		if !bus.IsAgentIdle(d.session, role) {
			continue
		}

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Agent-defs watchdog: %s definition changed — auto-reloading\n", ts, role)
		bus.LogLifecycle(d.session, "info", "daemon", "agent-defs-reload",
			fmt.Sprintf("%s definition changed on disk — auto-reload", role))

		// Reload in a goroutine — GracefulStop + relaunch blocks several seconds.
		// ReloadAgent sets the reload marker first, so subsequent poll cycles skip
		// this role; the relaunch path re-stamps the new hash via RunAgentLaunch.
		go func(r string) {
			if err := bus.ReloadAgent(d.session, r, "", "", false); err != nil {
				fmt.Fprintf(os.Stderr, "  [watchdog] agent-defs auto-reload of %s failed: %v\n", r, err)
			}
		}(role)
	}
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
		// "Context is high" is a LEVEL signal, not an edge event: while one
		// advisory is still unread, a second tells the agent nothing new. The
		// 600s cooldown only rate-limits — it never caps — and system actions are
		// exempt from inbox dedup, so nothing else bounds this. Left unchecked the
		// advisories accumulate for as long as the agent declines to act on them
		// (observed: 239 copies over ~27h, which then wedged that agent's delivery
		// path entirely). Skip while one is already pending.
		if bus.HasPendingAction(d.session, alert.Role, "daemon", "compact-recommended") {
			continue
		}

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

// effectiveOllamaRoles returns the roles under Ollama health right now:
// the launch-time env snapshot plus — evaluated per call — the prompt
// role while its supervision is enabled and its backend is local Ollama.
// The prompt backend is runtime-switchable (PromptBackend reads the
// shell config on every call), so membership must be too. The helper
// only ever ADDS prompt: an explicit MUXCODE_PROMPT_CLI=local in the
// snapshot alongside backend=opencode is contradictory config, and
// removing snapshot entries here would silently mask it.
func (d *Daemon) effectiveOllamaRoles() []string {
	if !bus.PromptAgentEnabled() || bus.PromptBackend(d.session) != "ollama" {
		return d.ollamaRoles
	}
	for _, r := range d.ollamaRoles {
		if r == "prompt" {
			return d.ollamaRoles
		}
	}
	return append(append([]string{}, d.ollamaRoles...), "prompt")
}

// checkOllama runs Ollama health probes every 30 seconds for roles using local LLM.
// Detection timeline: 30s first probe, 60s alert, 90s restart attempt.
// Caps automatic restarts at 3 to prevent restart loops.
func (d *Daemon) checkOllama() {
	roles := d.effectiveOllamaRoles()
	if len(roles) == 0 {
		return
	}

	now := time.Now().Unix()
	if now-d.lastOllamaCheck < 30 {
		return
	}
	d.lastOllamaCheck = now

	// Run inference probe (0 = env-configurable default, MUXCODE_OLLAMA_PROBE_SECS)
	err := bus.CheckOllamaInference(d.ollamaURL, d.ollamaModel, 0)

	// Also check for agent failure sentinels
	hasSentinels := bus.HasOllamaFailSentinel(d.session)

	ts := time.Now().Format("15:04:05")

	if err == nil && !hasSentinels {
		// Healthy
		d.ollamaWarmingSince = 0
		if d.ollamaWasDown {
			// Recovery detected
			fmt.Printf("  %s  Ollama recovered — inference probe healthy\n", ts)
			bus.LogLifecycle(d.session, "info", "daemon", "ollama-recovered", "")
			d.ollamaWasDown = false
			d.ollamaFailCount = 0

			alert := bus.FormatOllamaAlert("recovered", roles, "Ollama is responsive again")
			msg := bus.NewMessage("daemon", "edit", "event", "ollama-recovered", alert, "")
			if sendErr := bus.Send(d.session, msg); sendErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to send recovery alert: %v\n", sendErr)
			}
			d.refreshInboxSizes()
		}
		return
	}

	// Not-confirmed-dead grace (MUX-109): a probe timeout against a server
	// that still answers /api/ps is not proof of a wedge. Two live states
	// produce it — WARMING (model not in memory yet; a restart discards
	// the load in progress and the ladder kills every attempt in a loop)
	// and STRAINED (model loaded, single completion slot saturated by
	// long thinking completions; observed live 2026-08-27 when the ladder
	// killed Ollama twice under the integration script's load, destroying
	// in-flight work each time). Both get the same bounded grace before
	// failures count; a truly dead or hung server fails the /api/ps probe
	// and walks the ladder immediately, and agent fail sentinels always
	// count. The cost is a real loaded-but-wedged inference waiting out
	// the grace before recovery — minutes of delay against the false
	// kill's guaranteed destruction of live work.
	if err != nil && !hasSentinels {
		if responsive, loaded := bus.OllamaModelLoaded(d.ollamaURL, d.ollamaModel, 3*time.Second); responsive {
			state, detail := "warming", "model loading"
			if loaded {
				state, detail = "strained", "model loaded, completion queue saturated"
			}
			if d.ollamaWarmingSince == 0 {
				d.ollamaWarmingSince = now
			}
			graceFor := now - d.ollamaWarmingSince
			if graceFor < bus.OllamaWarmupGraceSecs() {
				fmt.Printf("  %s  Ollama %s: %s (%s, %ds) — probe failure not counted\n", ts, state, d.ollamaModel, detail, graceFor)
				bus.LogLifecycle(d.session, "info", "daemon", "ollama-"+state,
					fmt.Sprintf("model %s: %s for %ds", d.ollamaModel, detail, graceFor))
				return
			}
		}
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
			alert := bus.FormatOllamaAlert("down", roles, errMsg)
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
				alert := bus.FormatOllamaAlert("down", roles,
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
		alert := bus.FormatOllamaAlert("restarting", roles,
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
		for _, role := range roles {
			if role == "prompt" {
				// Headless (MUX-109) — no pane to send-keys into; bounce the
				// process and let checkPromptAgent relaunch it this cycle.
				_ = bus.StopPromptAgent(d.session)
				d.lastPromptAgentStart = 0
				fmt.Printf("  %s  Restarting headless prompt-agent\n", ts)
				continue
			}
			if restartErr := bus.RestartLocalAgent(d.session, role); restartErr != nil {
				fmt.Fprintf(os.Stderr, "  [ollama] failed to restart agent %s: %v\n", role, restartErr)
			} else {
				fmt.Printf("  %s  Relaunched agent: %s\n", ts, role)
				// Clear notified-size so checkIdleAgents re-notifies the
				// new agent about any pending inbox messages.
				bus.ClearNotifiedIDs(d.session, role)
			}
		}

		// Reset fail count to let the next probe cycle detect recovery, and
		// the warming clock with it — the restarted server cold-loads the
		// model again, and a stale warmingSince would burn the fresh load's
		// grace window before it started, reviving the kill loop for exactly
		// the recovery path.
		d.ollamaFailCount = 0
		d.ollamaWarmingSince = 0
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

	// One tmux call for the whole sweep — read after the time gate above so an
	// inert cycle spawns no process at all.
	windows := d.sessionWindows()

	for _, role := range bus.KnownRoles {
		// Skip excluded roles and spawn roles
		if bus.IsAgentHealthExcluded(d.session, role) || bus.IsSpawnRole(role) {
			continue
		}

		// Hosted roles share their host's pane — the host's own probe covers
		// them. Probing them separately is not just redundant here, it is
		// destructive: RestartLocalAgent resolves PaneTarget by role, so
		// restarting "pr-read" sends C-c into the *commit* window's pane and
		// kills a healthy agent. It also doubles the effective restart budget
		// for that pane, since a hosted role's counters are independent of its
		// host's.
		if bus.WindowForRole(role) != role {
			continue
		}

		// A role with no tmux window in this session was never launched.
		// provider.IsAlive fail-safes to "alive" when it cannot capture a pane,
		// so this is the only signal that can definitively say "not here" —
		// and restarting such a role would target a pane it does not own.
		if !roleHasWindow(windows, role) {
			continue
		}

		// Skip intentionally stopped agents
		if bus.IsAgentStopped(d.session, role) {
			continue
		}

		alive := d.agentAlive(d.session, role)

		if alive {
			// Recovery detection
			if d.agentWasDown[role] {
				fmt.Printf("  %s  Agent %s recovered\n", ts, role)
				bus.LogLifecycle(d.session, "info", "daemon", "agent-recovered", role)
				d.agentWasDown[role] = false
				d.agentFailCounts[role] = 0

				if d.shouldSendEvent("agent-health", role) {
					alert := bus.FormatAgentHealthAlert("recovered", role, "Agent is responsive again")
					msg := bus.NewMessage("daemon", "edit", "event", "agent-recovered", alert, "")
					_ = bus.Send(d.session, msg)
					d.refreshInboxSizes()
				}
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
				// Reset like the restart path below. The cap branch is nested
				// under `count == 3`, so without this the counter climbs past 3
				// and never matches again — alert-only mode would fire exactly
				// one alert and then stay silent forever, however long the agent
				// stays down. Resetting lets the count cycle back to 3 so the
				// 600s re-alert above keeps reminding that a manual restart is
				// still needed.
				d.agentFailCounts[role] = 0
				continue
			}

			d.agentRestarts[role]++
			attempt := d.agentRestarts[role]
			fmt.Printf("  %s  Restarting agent %s (attempt %d/3)...\n", ts, role, attempt)
			bus.LogLifecycle(d.session, "warn", "daemon", "agent-restart",
				fmt.Sprintf("%s attempt %d/3", role, attempt))

			// Send restarting alert (deduped with recovery — at most one per 5-min window)
			if d.shouldSendEvent("agent-health", role) {
				alert := bus.FormatAgentHealthAlert("restarting", role,
					fmt.Sprintf("Attempt %d/3 — relaunching agent", attempt))
				msg := bus.NewMessage("daemon", "edit", "event", "agent-restarting", alert, "")
				_ = bus.Send(d.session, msg)
				d.refreshInboxSizes()
			}

			// Attempt restart
			if err := bus.RestartLocalAgent(d.session, role); err != nil {
				fmt.Fprintf(os.Stderr, "  [agent-health] failed to restart %s: %v\n", role, err)
			} else {
				fmt.Printf("  %s  Agent %s restarted successfully\n", ts, role)
				// Clear notified-size so checkIdleAgents re-notifies the
				// new agent about any pending inbox messages.
				bus.ClearNotifiedIDs(d.session, role)
			}

			// Reset fail count to let next probe detect recovery
			d.agentFailCounts[role] = 0
		}
	}
}

// msgCheckSecs returns how often (in seconds) the daemon scans for messages to
// deliver — idle-agent wake-ups (checkIdleAgents) and tracked-task completions
// (checkTrackedTasks). Lower = snappier delivery, at the cost of more frequent
// tmux pane captures. Configurable via MUXCODE_MSG_CHECK_SECS (default 2, was
// 5). The effective floor is the daemon poll interval (--poll, default 2s);
// to go below that, also lower --poll.
func msgCheckSecs() int64 {
	if v := os.Getenv("MUXCODE_MSG_CHECK_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 1 {
			return n
		}
	}
	return 2
}

// shouldWakeIdleOrActionable decides whether checkIdleAgents should proceed to
// deliver an agent's unnotified messages. Actionable (request-type) messages
// always warrant a wake-up. Non-actionable (response/event) messages warrant a
// wake-up ONLY when the agent is idle — so an idle agent still receives a
// stranded response (e.g. a completed tracked task whose one-shot Notify() was
// missed) while an active agent is never interrupted for a mere response.
func shouldWakeIdleOrActionable(hasActionable, isIdle bool) bool {
	return hasActionable || isIdle
}

// checkIdleAgents wakes agents that are idle with unread messages.
// Runs every 5 seconds. For each agent that has unread inbox messages
// and is sitting at the idle prompt (not polling or waiting), triggers a
// wake-up via the provider. Hook providers (Claude Code) get "You have new
// messages" via send-keys; non-hook providers (Codex, OpenCode) get the actual
// message content injected via provider.SendWakeUp(). The edit agent is
// included, but its event-type notifications are capped per 5-minute window by
// the notification budget (see editNotifyBudget); request-type messages to edit
// are never throttled.
// pollHealth timing: how often the backstop runs, how stale an un-receipted
// message must be before it counts as a gap, and how long a gap must persist
// before alerting edit that the poll loop / sidecar is likely down.
const (
	pollHealthIntervalSecs = 15
	pollHealthGapSecs      = 45
	pollHealthAlertSecs    = 120
	// pollHealthListenerlessGapSecs applies to self-poll-capable agents that
	// currently have no listener (see Daemon.listenerless). The generous 45s
	// default exists to avoid mistaking a busy agent for a broken one; when we
	// can see there is no consumer at all, that caution is unnecessary and only
	// delays delivery. Still above the 15s poll interval so a message always
	// gets at least one sweep to be consumed normally first.
	pollHealthListenerlessGapSecs = 20
)

// ackDeliveryActive reports whether the receipt-based delivery cutover is active.
// Default is TRUE: receipt-based delivery (per-message receipts + agent self-poll)
// is the default delivery path, replacing pane-scrape wedge inference. Rollback
// valves, in precedence order:
//   - MUXCODE_DELIVERY_ACK_DISABLE (env) — hard kill switch, forces the old
//     pane-scrape path. Read from the daemon's own process, so it needs a daemon
//     restart to change.
//   - MUXCODE_DELIVERY_ACK=off|0|false|no (env) — explicit startup opt-out;
//     =on|1|true|yes pins it on (redundant with the default, kept for symmetry).
//   - runtime OFF marker (`muxcode delivery-ack off`) — instant, restart-free
//     rollback to the old path for a single session.
//
// Absent all of these, the cutover is ON.
func (d *Daemon) ackDeliveryActive() bool {
	return bus.AckDeliveryActive(d.session)
}

// checkPollHealth is the receipt-gap backstop that replaces pane-scrape wedge
// detection under the delivery-ack cutover. For each agent it looks for inbox
// messages carrying no receipt past pollHealthGapSecs — a positive signal the
// agent's self-poll loop (Claude/harness) or verified-inject sidecar
// (OpenCode/Codex) stopped consuming. It re-drives delivery (ForceDeliver for
// self-pollers, SendWakeUp for non-hook TUIs) and alerts edit once the gap
// persists past pollHealthAlertSecs.
//
// Inert unless the cutover is active: with the old machinery in charge, agents
// are not self-polling and a receipt gap is normal, so running this would
// false-alarm.
// sessionWindows returns the session's tmux window names, or nil if the list
// could not be read. Callers pass the result to roleHasWindow; fetch it once
// per sweep so a multi-role pass costs a single tmux call.
func (d *Daemon) sessionWindows() []string {
	names, err := d.windowNames(d.session)
	if err != nil {
		return nil
	}
	return names
}

// roleHasWindow reports whether role has a tmux window among names. A nil
// names slice means the window list could not be read: a tmux failure is
// session-wide rather than role-specific, so that is treated as indeterminate
// (true) rather than mass-suppressing every role at once.
func roleHasWindow(names []string, role string) bool {
	if names == nil {
		return true
	}
	return bus.RoleHasWindow(names, role)
}

// checkPollHealth is the delivery-ack receipt-gap backstop — the
// positive-signal replacement for pane-scrape wedge detection. A growing
// receipt gap (actionable inbox messages with no receipt past a
// threshold) means the agent's self-poll or delivery sidecar stopped
// consuming; the backstop re-drives delivery once per gap episode, then
// alerts edit if the gap persists. A re-drive whose injection was
// skipped (ErrInjectionSkipped) does not count as attempted — the
// episode stays open so later polls retry (MUX-105). Inert unless the
// receipt-based cutover is active, and gated to live roles that have a
// window in this session.
func (d *Daemon) checkPollHealth() {
	if !d.ackDeliveryActive() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPollHealthCheck < pollHealthIntervalSecs {
		return
	}
	d.lastPollHealthCheck = now

	// One tmux call for the whole sweep — read after the gates above so an
	// inert cycle spawns no process at all.
	windows := d.sessionWindows()

	for _, role := range bus.KnownRoles {
		// Hosted roles share their host's pane/inbox — the host covers them.
		if bus.WindowForRole(role) != role {
			continue
		}
		// Skip agents mid-reload (intentionally down) and permission-blocked ones.
		if bus.IsReloading(d.session, role) || d.permBlocked[role] {
			continue
		}

		// Only backstop a LIVE agent with a genuinely-stuck REQUEST. Three guards,
		// mirroring the checkInboxes delivery gate:
		//   - roleHasWindow: skip a role with no tmux window in this session (never
		//     launched). Such a role can never consume, so every message to it
		//     ages into a permanent gap that no recovery can clear — force-deliver
		//     has no pane to target and fails. This guard is what agentAlive cannot
		//     provide: provider.IsAlive fail-safes to "alive" when it cannot capture
		//     a pane, so a phantom role reads as live.
		//   - agentAlive: skip a role whose agent has crashed to a shell (nothing
		//     to recover — checkAgentHealth handles restarts).
		//   - HasActionableMessages: response-only / informational inbox growth is
		//     not a delivery failure (checkInboxes never wakes on it either); only
		//     an un-consumed request past the threshold signals a dead poll loop.
		// A live agent that simply hasn't consumed yet (busy, or self-poll not yet
		// relaunched) is still covered by the recover-once guard below.
		// Reset any stale gap state when skipping so a later real gap starts clean.
		if !roleHasWindow(windows, role) || !d.agentAlive(d.session, role) || !bus.HasActionableMessages(d.session, role) {
			d.pollGapSince[role] = 0
			d.pollGapAlerted[role] = false
			d.pollGapRecovered[role] = false
			continue
		}

		// A self-poll-capable agent with no listener at all has nothing pulling
		// its inbox right now, so waiting the full threshold just adds latency
		// to a gap we can already explain. Recovery is identical (ForceDeliver
		// below); only the patience differs.
		gapThreshold := pollHealthGapSecs * time.Second
		if d.listenerless(role) {
			gapThreshold = pollHealthListenerlessGapSecs * time.Second
		}

		gap := bus.ReceiptGap(d.session, role, gapThreshold)
		if len(gap) == 0 {
			d.pollGapSince[role] = 0
			d.pollGapAlerted[role] = false
			d.pollGapRecovered[role] = false
			continue
		}
		if d.pollGapSince[role] == 0 {
			d.pollGapSince[role] = now
		}

		// Re-drive delivery ONCE per gap episode, not every poll. Self-pollers
		// (Claude/harness) get a robust force-deliver that re-wakes the poll;
		// non-hook TUIs get another verified-inject attempt. Retrying every cycle
		// churns failed attempts + warnings for an agent that legitimately isn't
		// consuming yet (busy, or a freshly-idle agent whose self-poll loop hasn't
		// launched); one attempt plus the single edit alert below is enough — the
		// gap clears (and re-arms) once a receipt lands.
		if !d.pollGapRecovered[role] {
			d.pollGapRecovered[role] = true
			provider := bus.ResolveProvider(role)
			if provider.SupportsHooks() {
				if _, err := bus.ForceDeliver(d.session, role, true); err != nil {
					bus.LogLifecycle(d.session, "warn", "daemon", "delivery-gap",
						fmt.Sprintf("%s: force-deliver failed during receipt-gap recovery: %v", role, err))
				}
			} else {
				if err := provider.SendWakeUp(d.session, role, false); err != nil {
					if errors.Is(err, bus.ErrInjectionSkipped) {
						// A skip is not a re-drive — keep the episode open so
						// later polls retry (see doc comment).
						d.pollGapRecovered[role] = false
						bus.LogLifecycle(d.session, "warn", "daemon", "delivery-gap-skip",
							fmt.Sprintf("%s: recovery injection skipped, will retry: %v", role, err))
					} else {
						bus.LogLifecycle(d.session, "warn", "daemon", "delivery-gap",
							fmt.Sprintf("%s: wake-up failed during receipt-gap recovery: %v", role, err))
					}
				}
			}
		}

		// Alert edit once if the gap persists well past recovery attempts.
		if now-d.pollGapSince[role] >= pollHealthAlertSecs && !d.pollGapAlerted[role] {
			d.pollGapAlerted[role] = true
			bus.LogLifecycle(d.session, "warn", "daemon", "delivery-gap",
				fmt.Sprintf("%s: %d un-receipted msg(s) for %ds — poll/sidecar may be down",
					role, len(gap), now-d.pollGapSince[role]))
			msg := bus.NewMessage("daemon", "edit", "event", "delivery-gap",
				fmt.Sprintf("%s has %d un-receipted message(s) for %ds — self-poll or delivery sidecar may be down",
					role, len(gap), now-d.pollGapSince[role]), "")
			_ = bus.SendNoCC(d.session, msg)
		}
	}
}

// listenerless reports whether a self-poll-capable agent currently has no
// listener consuming its inbox.
//
// Under the cutover a hook agent's inbox is normally drained by its own
// `muxcode inbox --poll --loop`, and the receipt-gap backstop only needs to
// notice the rare case where that has broken. But there is a routine window
// where no listener exists at all: the runtime reclaims the listener's
// background task while the session is quiet, and the Stop hook relaunches one
// only on the agent's next turn. A message arriving inside that window has
// nothing pulling it, so it waits for the full gap threshold.
//
// Reported so checkPollHealth can shorten its threshold for exactly those roles
// rather than making every role twitchy. Recovery still runs through
// ForceDeliver, so this never resurrects pane-scrape delivery.
//
// Non-hook TUIs (OpenCode, Codex) are excluded: they can never run a listener,
// so "no listener" is their steady state, not a gap — treating it as one is what
// produced the delivery-gap false positives already on record. The local harness
// consumes in-process and is likewise excluded.
func (d *Daemon) listenerless(role string) bool {
	if !bus.ResolveProvider(role).SupportsHooks() {
		return false
	}
	if bus.IsHarnessActive(d.session, role) {
		return false
	}
	return !bus.IsPolling(d.session, role) && !bus.IsWaiting(d.session, role)
}

func (d *Daemon) checkIdleAgents() {
	// Delivery-ack cutover: when receipt-based delivery is active, agents pull
	// their own inboxes (Claude self-poll, harness in-process) and non-hook TUIs
	// are served by checkInboxes->Notify plus the checkPollHealth backstop. The
	// pane-scrape idle-delivery machinery below is bypassed. Default (cutover off)
	// keeps it as the delivery path.
	//
	if d.ackDeliveryActive() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastIdleCheck < msgCheckSecs() {
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

		// Skip roles the permission-block watchdog has suppressed. The agent is
		// wedged at a rejected permission prompt it cannot satisfy autonomously;
		// re-waking it just restarts the loop. checkStuckPermissions lifts this
		// the moment the block signature clears (recovery / user intervention).
		if d.permBlocked[role] {
			continue
		}

		// Detect idle transitions: when an agent goes from not-idle to idle
		// (e.g. after restart, hot reload, or startup), clear stale notification
		// markers and deliver any pending messages as a combined notification.
		//
		// Guard: skip if the notified IDs marker was updated within the last 10s.
		// A send-keys injection causes a brief active→idle transition as the TUI
		// processes the injected text. Without this guard, the idle transition
		// clears the notified IDs and re-delivers the same messages, creating a
		// notification storm (inject → echo → idle → clear → re-inject → ...).
		isIdle := bus.IsAgentIdle(d.session, role)
		wasIdle := d.lastIdleState[role]
		// Reset safety-net retry counter when agent becomes active (processing messages)
		if !isIdle && wasIdle {
			d.safetyNetRetries[role] = 0
		}
		// Reset edit notification budget when edit becomes idle (finished processing)
		if isIdle && !wasIdle && role == "edit" {
			d.resetEditBudget()
		}
		if isIdle && !wasIdle {
			recentlyNotified := bus.IsNotifiedRecently(d.session, role, 10*time.Second)
			if !recentlyNotified {
				// Do NOT clear notified IDs here. Clearing on every active→idle
				// transition re-marks already-delivered-but-unconsumed messages
				// as "new" and re-delivers them on each idle cycle — a
				// re-notification loop for any agent doing many sequential
				// operations (the same handful of build/test/review results and
				// stale notifies re-surfacing endlessly). Restart, reload, and
				// mode-switch clear notified IDs at their own events, and the
				// stale-marker safety-net below handles dropped injections, so
				// clearing here is both redundant and the loop's root cause.
				// UnnotifiedMessages already returns only genuinely-new messages.
				d.lastNonHookWake[role] = 0 // reset non-hook cooldown too
				ts := time.Now().Format("15:04:05")
				fmt.Printf("  %s  Agent %s became idle — delivering newly-arrived messages\n", ts, role)
				bus.LogLifecycle(d.session, "info", "daemon", "idle-transition", role)

				// Deliver combined notification for any accumulated messages.
				// This is the primary deferred delivery mechanism — messages that
				// arrived while the agent was busy are combined into a single
				// notification the moment the agent becomes idle.
				//
				// Hold if the user is mid-typing — injecting would corrupt input.
				// If the window isn't focused, any pending input is stale agent
				// output — clear it with C-u and proceed.
				unnotified := bus.UnnotifiedMessages(d.session, role)
				hasPending := bus.HasPendingInput(d.session, role)
				if hasPending && !bus.IsWindowFocused(d.session, role) {
					target := bus.PaneTarget(d.session, role)
					if err := bus.TmuxClearInput(target); err == nil {
						hasPending = false
						time.Sleep(100 * time.Millisecond)
					}
				}
				if len(unnotified) > 0 && !hasPending {
					provider := bus.ResolveProvider(role)
					text := bus.BuildCombinedNotification(unnotified)
					ids := make([]string, 0, len(unnotified))
					for _, m := range unnotified {
						ids = append(ids, m.ID)
					}
					bus.AddNotifiedIDs(d.session, role, ids)
					_ = bus.SendWakeUpWithText(d.session, role, provider, text, false)
					fmt.Printf("  %s  Delivered combined notification to %s (%d messages)\n", ts, role, len(unnotified))
					bus.LogLifecycle(d.session, "info", "daemon", "idle-combined-wake",
						fmt.Sprintf("%s: %d messages", role, len(unnotified)))
				}
			}
		}
		d.lastIdleState[role] = isIdle

		// Skip if no unnotified messages (content-aware, not size-based).
		unnotified := bus.UnnotifiedMessages(d.session, role)
		if len(unnotified) == 0 {
			// Inbox drained (nothing unnotified) — the churn episode resolved, so
			// reset the force-wake budget and lift any suppression. Without this
			// the reset in the idle branch below is unreachable when the inbox is
			// empty, so a suppressed role would stay permanently undeliverable.
			d.resetChurnGuard(role)
			// Safety net: if the agent has actionable messages but all are
			// marked as notified with a stale marker (>15s), a previous
			// send-keys injection was likely dropped by the TUI. Clear the
			// notified IDs so the next cycle retries delivery. Without this,
			// the agent stays stuck at ❯ until idle-task-rescue fires a
			// synthetic response 30s later.
			//
			// Not gated on isIdle alone: a dropped-Enter injection leaves its
			// text PARKED at the prompt, and long parked text wraps past
			// IsIdle's 8-line capture — the agent reads as "active" while the
			// watchdog branch is also unreachable (it requires unnotified>0).
			// A wider capture showing ❯ proves the agent is deliverable; clear
			// the parked text so the retried injection lands clean.
			//
			// Capped at 3 retries to prevent notification storms when the agent
			// looks idle but can't process input (e.g. during Claude Code's
			// Ideating/Thinking phase where ❯ is visible but the LLM is working).
			//
			// The retry budget REFILLS after safetyNetRetryWindowSecs. It must not
			// be permanently exhaustible: the only other reset is the active
			// transition above, which requires the agent to PROCESS a message — the
			// very thing a failed delivery prevents. An agent whose injections all
			// dropped would burn its 3 retries, never go active, never refill, and
			// sit idle with an actionable message forever, deliverable only by hand
			// via `muxcode deliver <role> --force`. Refilling on a timer preserves
			// the storm cap (3 attempts per window) while letting a stuck agent
			// self-heal once the TUI is ready to accept input again.
			if bus.HasActionableMessages(d.session, role) &&
				!bus.IsNotifiedRecently(d.session, role, 15*time.Second) &&
				d.safetyNetBudgetAvailable(role, now) {
				deliverable := isIdle
				if !deliverable {
					if bus.ClearParkedInput(d.session, role) {
						deliverable = true
						ts := time.Now().Format("15:04:05")
						fmt.Printf("  %s  Safety net: cleared parked input on %s (notified-but-unconsumed) — retrying delivery\n", ts, role)
						bus.LogLifecycle(d.session, "info", "daemon", "parked-input-cleared", role)
					}
				}
				if deliverable {
					bus.ClearNotifiedIDs(d.session, role)
					d.spendSafetyNetRetry(role, now)
				}
			}
			continue
		}
		// Request-type messages always justify a wake-up. Response/event
		// messages do NOT justify interrupting an ACTIVE agent — but an IDLE
		// agent must still receive them. A completed tracked task fires only a
		// single Notify(); if that wake-up is missed (parked input, dropped
		// send-keys), nothing re-delivers a response/event and it strands in
		// the idle inbox forever. Delivering to idle agents (and marking them
		// notified) closes that gap and self-retries until the wake-up lands.
		hasActionable := false
		for _, m := range unnotified {
			// Only requests whose primary destination is this role justify a
			// forced wake-up. Auto-CC'd requests addressed to another agent
			// (e.g. test→review copied into edit's inbox with To="review") are
			// informational — treating them as actionable re-woke edit endlessly
			// for work it can never complete. WindowForRole mirrors delivery
			// routing so genuine and hosted requests still count.
			if m.Type == "request" && bus.WindowForRole(m.To) == role {
				hasActionable = true
				break
			}
		}
		if !shouldWakeIdleOrActionable(hasActionable, isIdle) {
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
			// Best-effort: send wake-up with combined text.
			// Cooldown: once per 60s per role to avoid spam.
			if now-d.lastNonHookWake[role] >= 60 {
				d.lastNonHookWake[role] = now
				_ = provider.SendWakeUp(d.session, role, false)
			}
			continue
		}
		// Only wake idle agents (at ❯ prompt) — don't interrupt active ones.
		// The idle transition above handles deferred delivery when the agent
		// finishes its current work.
		if !isIdle {
			// Watchdog: track how long messages have been pending while the
			// agent appears "active". If >30s, IsAgentIdle may be wrong
			// (e.g. ❯ scrolled beyond the 8-line capture window after hot
			// reload). Do a wider capture to check and deliver if found.
			if _, seen := d.activeUnnotifiedSeen[role]; !seen {
				d.activeUnnotifiedSeen[role] = now
			}
			if now-d.activeUnnotifiedSeen[role] >= watchdogActiveSecs {
				// Churn cap: once we've force-delivered this role churnForceWakeCap
				// times without it draining, stop. Re-injecting every poll burns a
				// full agent turn each time. Back off until the agent genuinely
				// idles or the inbox drains (reset below).
				if d.churnSuppressed[role] {
					continue
				}
				// Gate on PaneShowsRecoverableIdle, NOT PaneHasIdlePrompt: the ❯
				// composer renders even mid-turn, so PaneHasIdlePrompt is true for a
				// genuinely-working agent — force-delivering there interrupts live
				// work and burns a turn (the churn this cap was papering over).
				// PaneShowsRecoverableIdle is true only for a finished-at-prompt
				// agent (❯ present AND not thinking), which is exactly the
				// "active-with-stale-messages" wedge that stranded the message.
				target := bus.PaneTarget(d.session, role)
				content, err := bus.TmuxCapturePaneLines(target, 200)
				if err != nil || !bus.PaneShowsRecoverableIdle(content) {
					// Genuinely working (or capture failed) — nothing at rest to
					// deliver to. Leave the timer past-threshold so the next 5s poll
					// re-checks cheaply once the agent finishes.
					continue
				}
				ts := time.Now().Format("15:04:05")
				if d.forceWakeCount[role] >= churnForceWakeCap {
					// Recoverable-idle yet still not draining after several
					// force-deliveries — a genuinely stuck agent. Suppress to stop
					// the churn and alert edit once (deduped) for human eyes.
					d.churnSuppressed[role] = true
					fmt.Printf("  %s  Churn guard: %s force-delivered %d× without draining — suppressing until idle\n",
						ts, role, d.forceWakeCount[role])
					bus.LogLifecycle(d.session, "warn", "daemon", "churn-suppress",
						fmt.Sprintf("%s: %d force-deliveries, suppressing", role, d.forceWakeCount[role]))
					alert := bus.NewMessage("daemon", "edit", "event", "churn-suppressed",
						fmt.Sprintf("%s force-delivered %d× without draining its inbox — delivery suppressed until it idles. If stuck: muxcode deliver %s --force",
							role, d.forceWakeCount[role], role), "")
					_, _ = bus.SendIfNotDuplicate(d.session, alert)
					d.refreshInboxSizes()
					continue
				}
				// Robust delivery: ForceDeliver runs the hardened text→delay→Enter→
				// verify path, clears stale parked input in an unfocused pane, and
				// rolls back its notified markers if the injection fails — the same
				// escape hatch `muxcode deliver --force` uses, now fired automatically
				// so no human has to notice the wedge and run it by hand.
				d.forceWakeCount[role]++
				fmt.Printf("  %s  Watchdog: %s idle-at-prompt but read active — force-delivering (%d/%d)\n",
					ts, role, d.forceWakeCount[role], churnForceWakeCap)
				bus.LogLifecycle(d.session, "info", "daemon", "watchdog-force-deliver", role)
				if res, derr := bus.ForceDeliver(d.session, role, true); derr == nil && res.Delivered > 0 {
					delete(d.activeUnnotifiedSeen, role)
				}
				continue
			}
			continue
		}
		// Agent is idle — clear watchdog + churn-guard state for the episode.
		delete(d.activeUnnotifiedSeen, role)
		d.resetChurnGuard(role)

		// Hold if the user is mid-typing at the prompt — injecting via
		// send-keys would corrupt their input. If the window isn't focused,
		// any pending input is stale agent output — clear it with C-u and
		// proceed with delivery.
		if bus.HasPendingInput(d.session, role) {
			if bus.IsWindowFocused(d.session, role) {
				continue // user is typing — hold for next cycle
			}
			// Stale input in unfocused window — clear it
			target := bus.PaneTarget(d.session, role)
			if err := bus.TmuxClearInput(target); err != nil {
				continue // can't clear — hold for next cycle
			}
			time.Sleep(100 * time.Millisecond)
		}

		// Agent is already idle with unnotified messages — deliver combined
		// notification immediately.
		ts := time.Now().Format("15:04:05")
		text := bus.BuildCombinedNotification(unnotified)
		ids := make([]string, 0, len(unnotified))
		for _, m := range unnotified {
			ids = append(ids, m.ID)
		}
		bus.AddNotifiedIDs(d.session, role, ids)
		// Distinguish a "stranded response/event" delivery (no actionable
		// request — delivered solely because the agent is idle) from a normal
		// request wake, so it can be observed/monitored. This is the path that
		// rescues a response orphaned by a missed one-shot tracked-task Notify.
		event := "idle-wake"
		if !hasActionable {
			event = "idle-response-wake"
		}
		fmt.Printf("  %s  Waking idle agent %s (%d unnotified messages, %s)\n", ts, role, len(unnotified), event)
		bus.LogLifecycle(d.session, "info", "daemon", event, role)
		_ = bus.SendWakeUpWithText(d.session, role, provider, text, false)
	}
}

// parkedResubmitMax is the number of fast Enter-resubmit attempts the
// parked-input watchdog makes before escalating to clear + re-deliver.
const parkedResubmitMax = 2

// checkParkedInput is the fast, default recovery for a dropped wake-up: when the
// daemon injected a notification but the Enter was eaten by a TUI redraw or an
// overlay (the "How is Claude doing this session?" survey, an autocomplete
// popup), the text parks unsent in the composer and the agent never processes
// it. Rather than wait for the slow 30s checkPaneSweep, this runs on the fast
// poll cycle and re-sends Enter to submit the parked text immediately.
//
// Scope is deliberately narrow so it only ever acts on a genuine dropped
// wake-up, never on live work or user input:
//   - hook providers (Claude Code) only — OpenCode/Codex manage their own input
//   - the agent must be at an idle prompt with text parked in the composer
//   - there must be actionable messages waiting (so the parked text is a
//     notification to submit, not unrelated residue — that stays with the sweep)
//   - never when the window is focused in an attached client (user may be typing)
//
// After parkedResubmitMax resubmits that don't take (overlay keeps eating the
// Enter), it escalates: clear the composer and clear notified IDs so the next
// checkIdleAgents cycle re-delivers the message fresh.
func (d *Daemon) checkParkedInput() {
	// Delivery-ack cutover: parked-input recovery is a pane-scrape delivery
	// mechanism. Under the receipt model Claude self-polls (no send-keys to drop)
	// and non-hook injection recovers its own Enter in confirmInjectionAndConsume,
	// so this is bypassed. Default (cutover off) keeps it.
	if d.ackDeliveryActive() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastParkedCheck < msgCheckSecs() {
		return
	}
	d.lastParkedCheck = now

	for _, role := range bus.KnownRoles {
		// Own pane only; skip panes mid-reload or running the local harness.
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) || bus.IsHarnessActive(d.session, role) {
			continue
		}
		// Claude Code only — OpenCode/Codex TUIs manage their own input.
		if !bus.ResolveProvider(role).SupportsHooks() {
			continue
		}
		// Only act when there are messages to process; otherwise any parked text
		// is unrelated residue owned by the slower checkPaneSweep.
		if !bus.HasActionableMessages(d.session, role) {
			delete(d.parkedResubmits, role)
			continue
		}

		target := bus.PaneTarget(d.session, role)
		content, err := bus.TmuxCapturePaneLines(target, 200)
		if err != nil {
			continue
		}
		// Must be at an idle prompt with text parked in the composer — and NOT
		// mid-turn. PaneHasIdlePrompt alone is true WHILE Claude works (❯ renders
		// in the input box at all times), and the resubmit path below sends
		// Escape, which INTERRUPTS a running turn. Gating on the prompt alone
		// Escape-looped healthy agents every ~2s:
		//
		//	⏺ Starting up — checking my inbox for pending messages.
		//	  ⎿  Interrupted · What should Claude do instead?
		//
		// killing the very turn the wake-up had just started, forever.
		if !bus.PaneShowsRecoverableIdle(content) {
			delete(d.parkedResubmits, role)
			continue
		}
		parked := bus.ParkedInputText(content)
		if parked == "" {
			delete(d.parkedResubmits, role)
			continue
		}
		// Never touch input while the user is viewing/typing in this window —
		// UNLESS the parked text is provably our own wake-up. Focus cannot see
		// authorship: it asks "might a human be here?", never "did a human write
		// this?". Our own dropped-Enter residue will never be submitted by the
		// user because they did not write it, so holding for their Enter holds
		// forever. That is the focused-pane wedge, and it is why an editor's
		// delegation silently stalls with the payload sitting in the target's
		// composer. Text we cannot prove is ours is still left strictly alone.
		if bus.IsWindowFocused(d.session, role) && !bus.IsOwnWakeUpText(parked) {
			continue
		}

		d.parkedResubmits[role]++
		if d.parkedResubmits[role] <= parkedResubmitMax {
			// Re-send Enter to submit the parked wake-up. Dismiss any overlay
			// eating the Enter first — mirrors verifyEnterDelivery's proven retry.
			_ = bus.TmuxSendEscape(target)
			time.Sleep(50 * time.Millisecond)
			_ = bus.TmuxSendKeys(target, "Enter")
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  Parked-input watchdog: resubmitting dropped wake-up on %s (attempt %d/%d)\n",
				ts, role, d.parkedResubmits[role], parkedResubmitMax)
			bus.LogLifecycle(d.session, "info", "daemon", "parked-resubmit",
				fmt.Sprintf("%s resubmit %d/%d", role, d.parkedResubmits[role], parkedResubmitMax))
			continue
		}

		// Resubmit didn't take — clear the composer and re-deliver fresh on the
		// next checkIdleAgents cycle.
		if err := bus.TmuxClearInput(target); err != nil {
			continue
		}
		bus.ClearNotifiedIDs(d.session, role)
		delete(d.parkedResubmits, role)
		ts := time.Now().Format("15:04:05")
		preview := parked
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  %s  Parked-input watchdog: resubmit failed on %s — cleared + re-delivering (%q)\n",
			ts, role, preview)
		bus.LogLifecycle(d.session, "warn", "daemon", "parked-escalate-clear",
			fmt.Sprintf("%s: %s", role, preview))
	}
}

// checkPaneSweep proactively inspects every agent pane via a wide tmux
// capture-pane and self-heals stale parked input — the residue of a
// dropped-Enter injection or an abandoned manual prompt. Parked text is the
// root of the delegation-wedge family: it blocks the next injection from
// landing clean, and long text wraps past IsIdle's 8-line window so the agent
// reads as "active" and every reactive delivery path holds. The sweep runs
// unconditionally — it does not require pending messages — so panes are clean
// BEFORE the next delegation arrives.
//
// Runs every 30 seconds. Two-sighting rule: the same parked text must be
// observed in two consecutive sweeps before clearing — a single sighting may
// be an in-flight injection caught between its text write and Enter keystroke,
// or a notification the agent is about to process.
//
// Never clears under a user's cursor: skipped when the window is focused in an
// attached client. Detached sessions are always fair game (nobody is typing).
func (d *Daemon) checkPaneSweep() {
	// Delivery-ack cutover: the pane sweep is pane-scrape delivery (dropped-Enter
	// resubmit across panes). Bypassed under the receipt model; default keeps it.
	if d.ackDeliveryActive() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPaneSweep < 30 {
		return
	}
	d.lastPaneSweep = now

	for _, role := range bus.KnownRoles {
		// Skip hosted roles — they share a pane with their host
		if bus.WindowForRole(role) != role {
			continue
		}
		if bus.IsReloading(d.session, role) {
			continue
		}
		// Only hook providers (Claude Code) — OpenCode/Codex TUIs manage
		// their own input and have different prompt semantics.
		provider := bus.ResolveProvider(role)
		if !provider.SupportsHooks() {
			continue
		}
		if bus.IsHarnessActive(d.session, role) {
			continue
		}
		// The fast checkParkedInput owns roles with actionable messages — it
		// resubmits the dropped wake-up immediately. This sweep only clears
		// unrelated stale residue, so skip roles that have messages waiting.
		if bus.HasActionableMessages(d.session, role) {
			delete(d.parkedSeen, role)
			continue
		}

		target := bus.PaneTarget(d.session, role)
		content, err := bus.TmuxCapturePaneLines(target, 200)
		if err != nil {
			delete(d.parkedSeen, role)
			continue
		}
		parked := ""
		if bus.PaneHasIdlePrompt(content) {
			parked = bus.ParkedInputText(content)
		}
		if parked == "" {
			delete(d.parkedSeen, role)
			continue
		}

		// First sighting of this exact text — remember it and wait one sweep.
		if d.parkedSeen[role] != parked {
			d.parkedSeen[role] = parked
			continue
		}

		// Same text parked across two consecutive sweeps — stale residue.
		if bus.IsWindowFocused(d.session, role) {
			continue // user is viewing this window — never clear under them
		}
		if err := bus.TmuxClearInput(target); err != nil {
			continue
		}
		delete(d.parkedSeen, role)

		ts := time.Now().Format("15:04:05")
		preview := parked
		if len(preview) > 60 {
			preview = preview[:60] + "..."
		}
		fmt.Printf("  %s  Pane sweep: cleared stale parked input on %s (%q)\n", ts, role, preview)
		bus.LogLifecycle(d.session, "info", "daemon", "pane-sweep-clear",
			fmt.Sprintf("%s: %s", role, preview))

		// With the prompt clean, force pending messages through: clearing the
		// notified markers lets the next checkIdleAgents cycle re-deliver.
		if bus.HasActionableMessages(d.session, role) {
			bus.ClearNotifiedIDs(d.session, role)
		}
	}
}

// checkTrackedTasks auto-completes in-flight tasks whose delivery status has
// reached "responded". This handles --track sends where no --wait polling loop
// is running to call CompleteTask. Also logs the response to console history
// so left-pane views update for non-hook providers.
//
// Timed-out tasks are scanned too: when a response arrives AFTER the sender's
// --wait gave up (TimeoutTask already fired), the reply sits in the sender's
// inbox as a non-actionable response-type message — checkIdleAgents never wakes
// for response-only inboxes, so without this rescue the sender idles forever
// and the user has to prompt for status manually.
//
// Runs every 5 seconds. Skips tasks where --wait is active for the sender
// (IsWaiting), since --wait handles its own completion.
func (d *Daemon) checkTrackedTasks() {
	now := time.Now().Unix()
	if now-d.lastTrackedTaskCheck < msgCheckSecs() {
		return
	}
	d.lastTrackedTaskCheck = now

	tasks, err := bus.ListTasks(d.session, bus.TaskInFlight)
	if err != nil {
		return
	}
	// Late responses: tasks --wait already marked timed-out can still get a
	// reply. Their delivery status flips to "responded" when it lands.
	if timedOut, toErr := bus.ListTasks(d.session, bus.TaskTimedOut); toErr == nil {
		tasks = append(tasks, timedOut...)
	}
	if len(tasks) == 0 {
		return
	}

	for _, task := range tasks {
		// Skip if the sender has an active --wait (it handles its own completion)
		if bus.IsWaiting(d.session, task.From) {
			continue
		}

		// Check delivery status — if "responded", the target agent already
		// sent a reply and MarkResponded fired. Complete the task.
		ds, err := bus.ReadDeliveryStatus(d.session, task.ID)
		if err != nil || ds.Status != bus.StatusResponded {
			// Not responded. Time out tasks stuck in-flight past their timeout
			// (delivered while the agent was busy, then never acted on) so they
			// stop blocking new requests via the in-flight dedup suppression.
			if task.Status == bus.TaskInFlight && bus.TaskExpired(task, now) {
				bus.TimeoutTask(d.session, task.ID)
				ts := time.Now().Format("15:04:05")
				fmt.Printf("  %s  Tracked task %s→%s:%s timed out (no response within %ds) — unblocking new requests\n",
					ts, task.From, task.To, task.Action, task.Timeout)
				bus.LogLifecycle(d.session, "warn", "daemon", "task-timeout",
					fmt.Sprintf("%s→%s:%s expired in-flight", task.From, task.To, task.Action))
			}
			continue
		}

		late := task.Status == bus.TaskTimedOut
		bus.CompleteTask(d.session, task.ID, ds.ResponseID)

		ts := time.Now().Format("15:04:05")
		suffix := ""
		if late {
			suffix = " [late — response arrived after --wait timeout]"
			bus.LogLifecycle(d.session, "info", "daemon", "task-late-response",
				fmt.Sprintf("%s→%s:%s completed after timeout (response: %s)",
					task.From, task.To, task.Action, ds.ResponseID))
		}
		fmt.Printf("  %s  Tracked task %s→%s:%s completed (response: %s)%s\n",
			ts, task.From, task.To, task.Action, ds.ResponseID, suffix)

		// Log to console history for the target role's left-pane view.
		// Shares logTaskToConsoleHistory with the other completion paths rather
		// than mirroring it: this used to be a hand-copy, and every defect in
		// the original lived here too until both were fixed at once.
		if ds.ResponseID != "" {
			if msg, ok := bus.FindMessageByID(d.session, ds.ResponseID); ok {
				logTaskToConsoleHistory(d.session, task.To, task.Action, msg.Payload, false)
			}
		}

		// Wake the sender so it picks up the response. Response-type messages
		// are non-actionable for checkIdleAgents, so without an explicit wake
		// an already-idle sender would never see the reply.
		_ = bus.Notify(d.session, task.From)
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

		// The agent may have already answered for itself. OpenCode/Codex
		// agents send their own replies, and the stop marker this check looks
		// for is just the tail of that same turn — so "pane looks complete"
		// does not mean a response is missing. Synthesizing one anyway
		// delivers a second, pane-scraped response that the loop detector
		// reports as a ping-pong between the two roles.
		if respID, ok := bus.FindResponseSince(d.session, task.To, task.From, task.SentAt); ok {
			bus.CompleteTask(d.session, task.ID, respID)
			delete(d.taskDeliveredAt, task.ID)
			delete(d.taskLastPaneContent, task.To+":"+task.ID)
			continue
		}

		// Never fabricate a completion for an agent that is not running. A dead
		// pane still shows the stop marker from its previous task, and the text
		// around it is just whatever the shell left on screen — that is how a
		// macOS "run chsh -s /bin/zsh" login banner was once recorded as a
		// successful build. checkAgentHealth restarts the agent, and the task
		// is re-delivered then.
		if !d.agentAlive(d.session, task.To) {
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

// checkGraphRuns advances every in-flight graph run one executor tick
// (MUX-014). Runs right after checkTrackedTasks so completions correlated
// this poll route their edges on the same tick. All state lives in the
// per-run store under BusDir()/graphs — the first tick after a daemon
// restart IS the resume scan, no separate recovery path needed.
func (d *Daemon) checkGraphRuns() {
	bus.StepGraphRuns(d.session)
}

// checkStalledTasks is the automatic form of the graph-priority rule
// (user, 2026-08-27): an agent that CONSUMED a request (receipt written)
// but whose turn never started owes a response no other watchdog sees —
// the receipt-gap backstop trusts the receipt, and the task timeout
// waits out its full 600s. Detection: an in-flight task past the stall
// threshold (graph-dispatched tasks stall at half of it) while the
// target pane rests at an idle prompt or holds parked input; a busy
// spinner clears the sighting, so an agent genuinely thinking is never
// interrupted. Two sightings → ForceDeliver with force, whose redrive
// path re-injects the consumed request. Two redrives per task, then one
// give-up log — the task timeout owns final failure. Opt out with
// MUXCODE_TASK_STALL_DISABLE=1; threshold via MUXCODE_TASK_STALL_SECS.
func (d *Daemon) checkStalledTasks() {
	if bus.TaskStallDisabled() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastStallCheck < 30 {
		return
	}
	d.lastStallCheck = now

	tasks, err := bus.ListTasks(d.session, bus.TaskInFlight)
	if err != nil {
		return
	}
	live := make(map[string]bool, len(tasks))
	for _, t := range tasks {
		live[t.ID] = true
		if !bus.TaskStalled(t, now, bus.TaskStallSecs()) {
			continue
		}
		role := bus.WindowForRole(t.To)
		// Harness roles consume in-process (no pane to read); reloads are
		// mid-cycle by design.
		if bus.IsHarnessActive(d.session, role) || bus.IsReloading(d.session, role) {
			continue
		}
		content, err := bus.TmuxCapturePaneLines(bus.PaneTarget(d.session, role), 30)
		if err != nil {
			continue
		}
		if !bus.PaneHasIdlePrompt(content) && !bus.HasPendingInput(d.session, role) {
			delete(d.taskStallSeen, t.ID) // busy — actually working on it
			continue
		}
		d.taskStallSeen[t.ID]++
		if d.taskStallSeen[t.ID] < 2 {
			continue
		}
		delete(d.taskStallSeen, t.ID)
		if d.taskRedrives[t.ID] >= 2 {
			if d.taskRedrives[t.ID] == 2 {
				d.taskRedrives[t.ID]++ // log the give-up exactly once
				bus.LogLifecycle(d.session, "warn", "daemon", "task-stall-giveup",
					fmt.Sprintf("%s→%s:%s still stalled after 2 redrives — task timeout owns it", t.From, t.To, t.Action))
			}
			continue
		}
		d.taskRedrives[t.ID]++
		if res, err := bus.ForceDeliver(d.session, role, true); err == nil && res.Delivered > 0 {
			ts := time.Now().Format("15:04:05")
			fmt.Printf("  %s  Task %s→%s:%s stalled (consumed, agent idle) — re-driven (%d/2)\n",
				ts, t.From, t.To, t.Action, d.taskRedrives[t.ID])
			bus.LogLifecycle(d.session, "info", "daemon", "task-stall-redrive",
				fmt.Sprintf("%s→%s:%s redrive %d/2", t.From, t.To, t.Action, d.taskRedrives[t.ID]))
		}
	}
	// Drop bookkeeping for tasks that completed or timed out.
	for id := range d.taskStallSeen {
		if !live[id] {
			delete(d.taskStallSeen, id)
		}
	}
	for id := range d.taskRedrives {
		if !live[id] {
			delete(d.taskRedrives, id)
		}
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

// watchdogActiveSecs is how long an agent may appear "active" with pending
// unnotified messages before the watchdog does a wider pane capture and
// force-delivers if a real ❯ prompt is found. Kept short so a finished agent
// that pane-scrape idle-detection misreads as busy recovers quickly; once past
// this threshold the wider check runs every poll (every 5s), not once.
const watchdogActiveSecs int64 = 15

// churnForceWakeCap bounds how many times the "read active but pane shows
// recoverable-idle" branch will force-deliver to a role before giving up for the
// episode. Past this, the daemon suppresses further force-deliveries
// (churnSuppressed) until the agent genuinely idles or its inbox drains —
// preventing the re-inject-every-poll loop that burns a full agent turn each
// time. Reset on idle transition.
const churnForceWakeCap int = 3

// resetChurnGuard clears the force-wake budget and lifts any delivery
// suppression for a role — called when the episode resolves (agent idles or its
// inbox drains) so a suppressed role becomes deliverable again.
func (d *Daemon) resetChurnGuard(role string) {
	delete(d.forceWakeCount, role)
	delete(d.churnSuppressed, role)
}

// idleRescueExcluded reports roles exempt from the idle-task rescue.
// The run role is excluded by default: its job is long scripts driven
// from background shells, so its pane legitimately rests at the prompt
// mid-work and the pane-scrape idle read is guaranteed wrong there —
// the rescue fabricated nine reports that way (MUX-112). Excluded tasks
// still resolve via the task timeout. Override the list with
// MUXCODE_IDLE_RESCUE_EXCLUDE (comma-separated; empty disables).
func idleRescueExcluded(role string) bool {
	list := "run"
	if v, ok := os.LookupEnv("MUXCODE_IDLE_RESCUE_EXCLUDE"); ok {
		list = v
	}
	for _, r := range strings.Split(list, ",") {
		if strings.TrimSpace(r) == role {
			return true
		}
	}
	return false
}

// checkIdleTaskCompletion is a safety net for hook-provider agents (Claude Code)
// that go idle without having responded to an in-flight task. This catches the
// failure mode where an agent composes a `muxcode send` command as text output
// in the TUI instead of executing it via the Bash tool — the response silently
// vanishes and the requester's --wait hangs forever.
//
// Two-phase approach:
//  1. First idle detection (after grace period): re-queue the original request
//     into the agent's inbox and re-notify. This handles the common case where
//     the agent consumed the message but went idle without processing it (e.g.,
//     after a compaction or restart where context was lost).
//  2. Second idle detection (after another grace period): the agent had a second
//     chance and still didn't respond. Capture the pane content and send a
//     synthetic response back to the requester.
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
		for k := range d.idleTaskRetried {
			delete(d.idleTaskRetried, k)
		}
		return
	}

	for _, task := range tasks {
		provider := bus.ResolveProvider(task.To)
		// Only handle hook providers — non-hook providers are covered by checkNonHookTasks
		if !provider.SupportsHooks() {
			continue
		}

		if idleRescueExcluded(bus.WindowForRole(task.To)) {
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
			delete(d.idleTaskRetried, task.ID)
			continue
		}

		ts := time.Now().Format("15:04:05")

		// Phase 1: Re-queue the original request and re-notify the agent.
		// This handles the common case where the agent consumed the inbox
		// message but went idle without processing it (e.g., after compaction
		// or restart where context was lost, or when the agent processed a
		// startup message and missed the actual work request).
		if !d.idleTaskRetried[task.ID] {
			d.idleTaskRetried[task.ID] = true
			// Reset grace period timer for the retry phase
			d.idleTaskFirstSeen[task.ID] = now

			fmt.Printf("  %s  Detected idle %s with unresponded task %s (idle %ds) — re-queuing request\n",
				ts, task.To, task.Action, now-firstSeen)
			bus.LogLifecycle(d.session, "info", "daemon", "idle-task-retry",
				fmt.Sprintf("%s idle with unresponded task %s from %s — re-queuing (idle %ds)",
					task.To, task.Action, task.From, now-firstSeen))

			// Re-inject the original request into the agent's inbox
			retryMsg := bus.NewMessage(task.From, task.To, "request", task.Action, task.Payload, "")
			if err := bus.Send(d.session, retryMsg); err != nil {
				fmt.Fprintf(os.Stderr, "  [idle-task-retry] failed to re-queue for %s: %v\n", task.ID, err)
			}

			// Clear notified IDs so the next checkIdleAgents cycle delivers the message
			bus.ClearNotifiedIDs(d.session, task.To)
			// Wake the agent immediately
			_ = bus.Notify(d.session, task.To)
			continue
		}

		// Phase 2: Agent had a second chance and still didn't respond.
		// Capture pane content for the synthetic response.
		fmt.Printf("  %s  Detected idle %s with unresponded task %s (idle %ds, retried) — sending synthetic response\n",
			ts, task.To, task.Action, now-firstSeen)
		bus.LogLifecycle(d.session, "warn", "daemon", "idle-task-rescue",
			fmt.Sprintf("%s idle with unresponded task %s from %s (idle %ds, retry exhausted)",
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
			fmt.Sprintf("[daemon: %s went idle without responding (retried once) — pane content follows]\n%s", task.To, payload),
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
		delete(d.idleTaskRetried, task.ID)

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
	for k := range d.idleTaskRetried {
		if !taskIDs[k] {
			delete(d.idleTaskRetried, k)
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

	// Detected task completion is not a verdict — the "output" here is scraped
	// pane content, so it may be a launch banner or partial reasoning rather
	// than a result. bus.NewBusResponseEntry records it as unverified activity
	// and drops payloads that are plainly TUI chrome.
	entry, ok := bus.NewBusResponseEntry(action, output, errored)
	if !ok {
		return
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

	// Only fire if an auto agent actually exists in this session. The default
	// window set does not include auto, so in most sessions there is nothing
	// to heartbeat: the message would sit un-consumed in auto's inbox, trip the
	// receipt-gap backstop (delivery-gap alerts), and draw a force-deliver
	// retry that cannot succeed against a window that isn't there.
	//
	// A window check is required here — bus.IsAgentAlive reports a phantom role
	// as alive because it cannot capture its pane.
	if !roleHasWindow(d.sessionWindows(), "auto") {
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

// shouldSendEvent returns true if this event should be delivered to edit.
// Suppresses duplicate events with the same action and key within a 5-minute
// window to prevent notification storms from filling the edit context window.
func (d *Daemon) shouldSendEvent(action, key string) bool {
	now := time.Now().Unix()
	dedupKey := action + ":" + key
	if last, ok := d.lastEventSent[dedupKey]; ok && now-last < 300 {
		return false
	}
	d.lastEventSent[dedupKey] = now
	return true
}

// editNotifyBudget is the max number of event-type messages the daemon will
// deliver to edit's inbox per 5-minute window. Request-type messages (direct
// asks) are never throttled. Configurable via MUXCODE_EDIT_NOTIFY_BUDGET.
const editNotifyBudgetDefault = 15

func editNotifyBudget() int {
	if v := os.Getenv("MUXCODE_EDIT_NOTIFY_BUDGET"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return editNotifyBudgetDefault
}

// shouldNotifyEdit returns true if an event-type message should be delivered
// to edit. Enforces a per-window budget to prevent context exhaustion.
// Request-type messages bypass the budget — the edit agent always receives
// direct asks. The window resets every 5 minutes or on edit idle transition.
func (d *Daemon) shouldNotifyEdit(msgType string) bool {
	if msgType == "request" {
		return true
	}
	now := time.Now().Unix()
	if now-d.editWindowStart >= 300 {
		d.editNotifyCount = 0
		d.editWindowStart = now
	}
	budget := editNotifyBudget()
	if d.editNotifyCount >= budget {
		return false
	}
	d.editNotifyCount++
	return true
}

// resetEditBudget resets the notification budget window. Called when
// the edit agent transitions to idle (processing complete).
func (d *Daemon) resetEditBudget() {
	d.editNotifyCount = 0
	d.editWindowStart = time.Now().Unix()
}

// checkServeHealth reads the serve-state.json file every 60 seconds and
// triggers the watch agent to run a Playwright browser check when a Vite
// dev server is detected as running. Each URL is only triggered once per
// 5-minute window to avoid spamming the watch agent.
func (d *Daemon) checkServeHealth() {
	now := time.Now().Unix()
	if now-d.lastServeCheck < 60 {
		return
	}
	d.lastServeCheck = now

	state := bus.ReadServeState(d.session)
	if state == nil {
		return
	}

	running := state.RunningServers()
	if len(running) == 0 {
		return
	}

	for _, srv := range running {
		if !srv.IsViteServer() {
			continue
		}
		url := srv.URL
		if url == "" {
			url = fmt.Sprintf("http://localhost:%d/", srv.Port)
		}

		// Deduplicate: only send once per 5-minute window per URL
		if lastSent, ok := d.serveCheckSentFor[url]; ok && now-lastSent < 300 {
			continue
		}

		// Check if the watch agent is alive before sending
		if !bus.IsAgentAlive(d.session, "watch") {
			continue
		}

		payload := fmt.Sprintf("Dev server %q is running at %s — run a Playwright browser check for console errors and warnings: node scripts/playwright-check.js %s",
			srv.Name, url, url)

		msg := bus.NewMessage("daemon", "watch", "request", "browser-check", payload, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [serve] failed to send browser-check to watch: %v\n", err)
			continue
		}
		if err := bus.Notify(d.session, "watch"); err != nil {
			fmt.Fprintf(os.Stderr, "  [serve] failed to notify watch agent: %v\n", err)
		}
		d.serveCheckSentFor[url] = now
		d.refreshInboxSizes()

		ts := time.Now().Format("15:04:05")
		fmt.Printf("  %s  Serve health: triggered browser check for %s (%s)\n", ts, url, srv.Name)
		bus.LogLifecycle(d.session, "info", "daemon", "serve-browser-check",
			fmt.Sprintf("url=%s name=%s port=%d", url, srv.Name, srv.Port))
	}
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

// Disk-pressure alert cooldowns, in seconds.
//
// The two differ because an alert nobody can act on is pure noise: when cleanup
// freed something, the condition is actionable and worth repeating sooner; when
// it freed nothing, the operator can do nothing with a repeat, so it is held
// back six times longer. Before any cooldown existed, an unactionable repeat
// every 60s produced 813 of the last 1000 lifecycle entries and evicted the
// history needed to diagnose overnight incidents.
const (
	diskPressureCooldownEffective   int64 = 600  // cleanup freed something
	diskPressureCooldownIneffective int64 = 3600 // cleanup freed nothing
)

// shouldAlertDiskPressure decides whether a disk-pressure alert fires now.
//
// Extracted as a pure function so "alerts once, not every cycle" is testable
// without calling CheckDiskPressure — which runs CleanupStale and would delete
// other muxcode sessions' /tmp artifacts on the machine running the test.
//
// seen distinguishes "never alerted" from "alerted at epoch 0": with lastTS
// defaulting to 0, an unseen key and a 1970 timestamp are indistinguishable,
// and the first alert for a role must always fire.
func shouldAlertDiskPressure(lastTS, now int64, seen, ineffective bool) bool {
	if !seen {
		return true
	}
	cooldown := diskPressureCooldownEffective
	if ineffective {
		cooldown = diskPressureCooldownIneffective
	}
	return now-lastTS >= cooldown
}

// checkDiskPressure checks /tmp for genuine pressure every 60 seconds and, when
// found, runs progressive cleanup and alerts the edit agent.
//
// Pressure is decided by bus.TmpPressure — absolute free headroom and muxcode's
// own footprint — not by the volume's percent-used.
// MUXCODE_TMP_CLEANUP_THRESHOLD is now only an on/off switch (0 disables).
func (d *Daemon) checkDiskPressure() {
	if bus.TmpCleanupThreshold() == 0 {
		return
	}

	now := time.Now().Unix()
	if now-d.lastDiskPressureCheck < 60 {
		return
	}
	d.lastDiskPressureCheck = now

	result, err := bus.CheckDiskPressure(d.session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  [disk] check failed: %v\n", err)
		return
	}
	if result == nil {
		return
	}

	ts := time.Now().Format("15:04:05")

	staleCleaned := 0
	if result.StaleResult != nil {
		staleCleaned = result.StaleResult.TotalItems()
	}
	claudeCleaned := 0
	claudeFreed := int64(0)
	if result.ClaudeResult != nil {
		claudeCleaned = len(result.ClaudeResult.Sessions)
		claudeFreed = result.ClaudeResult.BytesFreed
	}

	fmt.Printf("  %s  Disk pressure: /tmp free %s, muxcode footprint %s (volume %d%%) — cleaned %d stale, %d Claude sessions (%s)\n",
		ts, formatDaemonBytes(result.FreeBytes), formatDaemonBytes(result.FootprintBytes),
		result.UsagePct, staleCleaned, claudeCleaned, formatDaemonBytes(claudeFreed))

	alertKey := "disk-pressure:/tmp"
	ineffective := staleCleaned == 0 && claudeCleaned == 0
	lastTS, seen := d.lastAlertKey[alertKey]
	alerting := shouldAlertDiskPressure(lastTS, now, seen, ineffective)

	// Write the lifecycle warn only when the condition is actionable or newly
	// alerted. An unactionable repeat every 60s produced 813 of the last 1000
	// lifecycle entries, and since the log rotates at 1000, that spam evicted
	// the very history needed to diagnose overnight incidents.
	if alerting || !ineffective {
		bus.LogLifecycle(d.session, "warn", "daemon", "disk-pressure",
			fmt.Sprintf("/tmp free=%s footprint=%s volume=%d%% stale=%d claude=%d freed=%s",
				formatDaemonBytes(result.FreeBytes), formatDaemonBytes(result.FootprintBytes),
				result.UsagePct, staleCleaned, claudeCleaned, formatDaemonBytes(claudeFreed)))
	}

	if alerting {
		d.lastAlertKey[alertKey] = now

		payload := fmt.Sprintf(
			"/tmp disk pressure: %s free, muxcode footprint %s (volume %d%% used). Cleaned: %d muxcode artifact(s), %d Claude Code session(s) (%s freed).",
			formatDaemonBytes(result.FreeBytes), formatDaemonBytes(result.FootprintBytes),
			result.UsagePct, staleCleaned, claudeCleaned, formatDaemonBytes(claudeFreed),
		)
		msg := bus.NewMessage("daemon", "edit", "event", "disk-pressure", payload, "")
		if err := bus.Send(d.session, msg); err != nil {
			fmt.Fprintf(os.Stderr, "  [disk] failed to send alert: %v\n", err)
			return
		}
		// Don't Notify() — disk-pressure is an event, not a request. The agent
		// will see it next time it checks inbox. Notifying directly force-wakes
		// the agent for something it can't fix, burning context repeatedly.
		d.refreshInboxSizes()
	}
}

// autoClearCheckSecs throttles the auto-clear scan; with a default 60s quiet
// window, finer-grained checks buy nothing.
const autoClearCheckSecs int64 = 15

// checkAutoClear injects /clear into enrolled episodic agents once their work
// completes (MUX-103). Enrollment is explicit via MUXCODE_AUTO_CLEAR_ROLES
// (default empty = off). A clear fires exactly once per completed task: the
// per-role marker written by ClearAgent gates re-fire across poll cycles, and
// a failing guard in AutoClearEligible (pending inbox, in-flight task, reload,
// busy pane) postpones to a later cycle rather than cancelling — the trigger
// stays armed until the marker records a clear.
func (d *Daemon) checkAutoClear() {
	roles := bus.AutoClearRoles()
	if len(roles) == 0 {
		return
	}
	now := time.Now().Unix()
	if now-d.lastAutoClearCheck < autoClearCheckSecs {
		return
	}
	d.lastAutoClearCheck = now

	quiet := bus.AutoClearQuietSecs()
	for _, role := range roles {
		due, completed := bus.AutoClearDue(d.session, role, now, quiet)
		if !due {
			continue
		}
		if ok, _ := bus.AutoClearEligible(d.session, role); !ok {
			continue
		}
		trigger := fmt.Sprintf("task-completed-%ds-ago", now-completed)
		if err := bus.ClearAgent(d.session, role, "daemon", trigger); err != nil {
			fmt.Fprintf(os.Stderr, "  [auto-clear] %s: %v\n", role, err)
			continue
		}
		fmt.Printf("  %s  Auto-clear: %s (%s)\n", time.Now().Format("15:04:05"), role, trigger)
	}
}

// promptAgentRestartCoolSecs gates prompt-agent (re)starts. The harness
// writes its own PID marker only once its loop is up, so a liveness probe
// right after a start reads "dead" and would double-launch; the cooldown
// covers that gap, and also stops a crash loop (e.g. Ollama down) from
// spawning a fresh process every poll tick.
const promptAgentRestartCoolSecs int64 = 60

// checkPromptAgent supervises the headless prompt-agent (MUX-109). No
// pane hosts it, so none of the pane-based machinery sees it — the daemon
// owns start and restart. Alert-free by design: start failures go to the
// lifecycle log and retry after the cooldown instead of paging edit every
// tick; a persistent failure surfaces through Ollama health, not here.
func (d *Daemon) checkPromptAgent() {
	if !bus.PromptAgentEnabled() {
		return
	}
	now := time.Now().Unix()
	if now-d.lastPromptAgentStart < promptAgentRestartCoolSecs {
		return
	}
	if bus.PromptAgentAlive(d.session) {
		return
	}
	d.lastPromptAgentStart = now
	pid, err := bus.StartPromptAgent(d.session)
	if err != nil {
		bus.LogLifecycle(d.session, "warn", "daemon", "prompt-agent-start-failed", err.Error())
		return
	}
	bus.LogLifecycle(d.session, "info", "daemon", "prompt-agent-start",
		fmt.Sprintf("headless prompt-agent started (pid %d)", pid))
	fmt.Printf("  %s  Prompt-agent started (pid %d)\n", time.Now().Format("15:04:05"), pid)
}

// branchTimeFlushSecs is how often accrued in-memory branch time is written to
// the ledger (also flushed on branch change and on pause).
const branchTimeFlushSecs int64 = 60

// branchTimeIdleSecs returns how long the session may go without user input
// (tmux client activity) before branch-time treats it as idle and pauses
// accumulation — so time spent away from the keyboard is not counted even while
// the session stays attached. Configurable via MUXCODE_BRANCH_TIME_IDLE_SECS;
// 0 disables idle detection (revert to attach-only). Default 300s (5 minutes).
func branchTimeIdleSecs() int64 {
	if v := os.Getenv("MUXCODE_BRANCH_TIME_IDLE_SECS"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return 300
}

// checkBranchTime accumulates active working time onto the current git branch.
// It runs every poll (reusing the daemon cadence, no separate ticker). Time
// accrues while the user is active (attached and typing within branchTimeIdleSecs)
// OR a worker agent is producing output — an agent working on the branch is
// productive time even when the user is only watching, is away while agents run,
// or has detached a background session whose agents keep working. When neither is
// true (idle at the keyboard with idle agents, or detached with idle agents) the
// clock pauses. Time is only tracked on feature branches: shared integration
// branches (main/master by default, configurable via MUXCODE_BRANCH_TIME_IGNORE)
// are skipped entirely.
//
// To keep the hot path cheap, per-tick deltas are accrued in memory and flushed
// to the ledger at most once per branchTimeFlushSecs (or on a branch change /
// pause). The per-tick delta is capped at 2× the poll interval so a laptop sleep
// or clock change cannot inject spurious hours, and a branch change flushes the
// prior branch before resetting the baseline so time never bleeds across branches.
//
// Git state is resolved from the session's own directory, never the daemon's
// process working directory: `muxcode upgrade-daemons` relaunches daemons with a
// detached exec that inherits the caller's cwd, so a build run in another repo
// leaves this daemon sitting in a foreign checkout — attributing time to the
// wrong repo, or silently stopping when that checkout is on main. A tick is
// skipped outright when the session directory cannot be resolved, rather than
// falling back to cwd; pending time survives and the next delta stays bounded by
// the clock-jump cap.
//
// Disable with MUXCODE_BRANCH_TIME_DISABLE=1.
func (d *Daemon) checkBranchTime() {
	if os.Getenv("MUXCODE_BRANCH_TIME_DISABLE") == "1" {
		return
	}

	now := time.Now().Unix()

	// Time accrues while the user is active (a client is attached and they've
	// typed within the idle window) OR a worker agent is producing output — work
	// on the branch counts even when the user is only watching, is away while
	// agents run, or has detached a background session with agents still working.
	// When neither is true the session is genuinely idle: flush and reset the
	// baseline so the paused gap isn't back-counted on return.
	// SessionIdleSeconds returns -1 when no client is attached (detached).
	idle := bus.SessionIdleSeconds(d.session)
	idleMax := branchTimeIdleSecs()
	if !bus.BranchTimeUserActive(idle, idleMax) && !bus.AnyAgentWorking(d.session) {
		d.flushBranchTime()
		d.lastBranchTick = 0
		return
	}

	repoDir := bus.SessionRepoDir(d.session)
	if repoDir == "" {
		return // unresolvable session dir — never fall back to cwd, see doc comment
	}
	branch := bus.CurrentBranchIn(repoDir)
	if bus.BranchTimeIgnored(branch) {
		// Not in a git repo/detached HEAD, or on an ignored integration branch
		// (main/master by default) — flush the prior branch's pending time and
		// stop attributing. Active time is only tracked on feature branches.
		d.flushBranchTime()
		d.lastBranchTick = 0
		d.lastBranch = ""
		return
	}

	// First observation, or a branch switch: flush the prior branch's pending
	// time, then (re)set the baseline. We only count time between two consecutive
	// same-branch observations.
	if d.lastBranchTick == 0 || branch != d.lastBranch {
		d.flushBranchTime()
		d.lastBranchTick = now
		d.lastBranch = branch
		d.lastBranchFlush = now
		return
	}

	delta := now - d.lastBranchTick
	d.lastBranchTick = now
	if delta > 0 {
		maxDelta := int64(d.pollInterval.Seconds()) * 2
		if maxDelta <= 0 {
			maxDelta = 2
		}
		if delta > maxDelta {
			delta = maxDelta // clock-jump guard
		}
		d.branchTimePending += delta
	}

	// Flush periodically.
	if now-d.lastBranchFlush >= branchTimeFlushSecs {
		d.flushBranchTime()
		d.lastBranchFlush = now
	}
}

// flushBranchTime writes the accrued in-memory branch time to the ledger,
// attributing it to d.lastBranch, and resets the pending counter. A no-op when
// nothing is pending.
//
// The repo key is resolved once and cached, and only ever from an explicitly
// resolved session directory. RepoKeyIn("") would fall back to the process
// working directory, and because the key is cached for the daemon's lifetime,
// a single transient tmux failure would pin a foreign repo key permanently —
// writing this session's time into an unrelated repo's ledger with no way to
// self-correct. An unresolved key leaves the time pending for a later flush.
func (d *Daemon) flushBranchTime() {
	if d.branchTimePending <= 0 || d.lastBranch == "" {
		d.branchTimePending = 0
		return
	}
	if d.branchTimeRepoKey == "" {
		if dir := bus.SessionRepoDir(d.session); dir != "" {
			d.branchTimeRepoKey = bus.RepoKeyIn(dir)
		}
	}
	if d.branchTimeRepoKey == "" {
		return // not resolvable as a repo — keep pending for a later flush
	}
	pending := d.branchTimePending
	// Per-tick deltas were already capped; the sum is trusted, so no extra cap.
	total, err := bus.AccumulateBranchTime(d.branchTimeRepoKey, d.lastBranch, pending, 0)
	if err != nil {
		if !d.branchTimeErrLogged {
			d.branchTimeErrLogged = true
			fmt.Fprintf(os.Stderr, "  [branch-time] flush failed: %v\n", err)
		}
		return // keep pending; retry next flush
	}
	d.branchTimeErrLogged = false
	d.branchTimePending = 0
	if !d.branchTimeInit {
		d.branchTimeInit = true
		bus.LogLifecycle(d.session, "info", "daemon", "branch-time",
			fmt.Sprintf("tracking %s (%s)", d.lastBranch, bus.FormatDuration(total)))
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
