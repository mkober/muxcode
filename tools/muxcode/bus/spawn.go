package bus

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SpawnEntry represents a tracked spawned agent session.
type SpawnEntry struct {
	ID          string `json:"id"`
	Role        string `json:"role"`       // base role, e.g. "research"
	SpawnRole   string `json:"spawn_role"` // bus role + window name, e.g. "spawn-a1b2c3d4"
	Owner       string `json:"owner"`      // requesting agent, e.g. "edit"
	Task        string `json:"task"`       // task description
	Status      string `json:"status"`     // "running", "completed", "stopped"
	Window      string `json:"window"`     // tmux window name (= SpawnRole)
	StartedAt   int64  `json:"started_at"`
	FinishedAt  int64  `json:"finished_at"`
	Notified    bool   `json:"notified"`
	Worktree    string `json:"worktree,omitempty"`     // worktree directory path
	WorktreeRef string `json:"worktree_ref,omitempty"` // git ref used (commit SHA)
	SeedMsgID   string `json:"seed_msg_id,omitempty"`  // ID of the seeded spawn-task request (latest iteration for reused workers)
	RunID       string `json:"run_id,omitempty"`       // graph run key half — set on graph-dispatched workers (MUX-131 reuse)
	NodeID      string `json:"node_id,omitempty"`      // graph node key half — with RunID, the per-run+node reuse key
	ResumeID    string `json:"resume_id,omitempty"`    // session a dead worker was resumed into, pending definition verification (MUX-139)
	ResumedAt   int64  `json:"resumed_at,omitempty"`   // when that resume was typed; bounds the verification
	ReadyAt     int64  `json:"ready_at,omitempty"`     // first sighting of the resumed session's prompt with no definition warning
	StopPending string `json:"stop_pending,omitempty"` // node failure awaiting this worker's confirmed stop
	IdleSince   int64  `json:"idle_since,omitempty"`   // when its run released it to the idle pool; 0 while owned or busy (MUX-195)
	Spec        string `json:"spec,omitempty"`         // active spec when its current seed was sent; adoption clears context when it changed (MUX-195)
	Display     string `json:"-"`                      // render-time status from SpawnDisplayStatus; never persisted
}

// ReadSpawnEntries reads all spawn entries from the spawn JSONL file.
func ReadSpawnEntries(session string) ([]SpawnEntry, error) {
	entries, _, err := scanSpawnEntries(session)
	return entries, err
}

// scanSpawnEntries reads the registry like ReadSpawnEntries and also counts
// the malformed lines it skipped, so a caller that must not miss a worker —
// graph cancel — can refuse a partial registry instead of reading it as
// complete. A line that parses but lacks an ID or SpawnRole (`null`, `{}`)
// identifies no worker and is malformed too (Copilot on PR #91).
func scanSpawnEntries(session string) (entries []SpawnEntry, malformed int, err error) {
	data, err := os.ReadFile(SpawnPath(session))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var e SpawnEntry
		if err := json.Unmarshal(line, &e); err != nil || e.ID == "" || e.SpawnRole == "" {
			malformed++
			continue
		}
		entries = append(entries, e)
	}
	return entries, malformed, scanner.Err()
}

// WriteSpawnEntries overwrites the spawn JSONL file with the given entries.
// Callers hold withSpawnRegistryLock across their read and this write.
func WriteSpawnEntries(session string, entries []SpawnEntry) error {
	var buf bytes.Buffer
	for _, e := range entries {
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		buf.Write(data)
		buf.WriteByte('\n')
	}
	return os.WriteFile(SpawnPath(session), buf.Bytes(), 0644)
}

// GetSpawnEntry returns a single spawn entry by ID.
func GetSpawnEntry(session, id string) (SpawnEntry, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, err
	}

	for _, e := range entries {
		if e.ID == id {
			return e, nil
		}
	}
	return SpawnEntry{}, fmt.Errorf("spawn not found: %s", id)
}

// SpawnBaseRole returns the base role a spawn worker was launched as
// (spawn-a1b2c3d4 → edit), or role unchanged when it is not a spawn role or
// has no registry entry. A worker's provider, launch flags and instructions
// all come from its base role; only inbox delivery uses the spawn role.
func SpawnBaseRole(session, role string) string {
	if !IsSpawnRole(role) || session == "" {
		return role
	}
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return role
	}
	for _, e := range entries {
		if e.SpawnRole == role && e.Role != "" {
			return e.Role
		}
	}
	return role
}

// spawnRegistryMu and the flock taken by withSpawnRegistryLock serialize
// every read-modify-write of the spawn registry. The daemon and the CLI's
// spawn commands write it from separate processes, and a worker handed to a
// new run but overwritten by a concurrent writer's stale copy would belong to
// its finished run again (MUX-195). Holders must not re-enter it.
var spawnRegistryMu sync.Mutex

func withSpawnRegistryLock(session string, fn func() error) error {
	spawnRegistryMu.Lock()
	defer spawnRegistryMu.Unlock()

	lockPath := SpawnPath(session) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

// UpdateSpawnEntry applies a mutation function to a spawn entry by ID, under
// the registry lock.
func UpdateSpawnEntry(session, id string, fn func(*SpawnEntry)) error {
	return withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		for i, e := range entries {
			if e.ID == id {
				fn(&entries[i])
				return WriteSpawnEntries(session, entries)
			}
		}
		return fmt.Errorf("spawn not found: %s", id)
	})
}

// StartSpawn creates a tmux window, seeds the inbox with the task, and launches
// an agent. When useWorktree is true, the spawn gets its own git worktree at
// the current HEAD commit for filesystem isolation. Returns the SpawnEntry.
func StartSpawn(session, role, task, owner string, useWorktree bool) (SpawnEntry, error) {
	return StartSpawnOwned(session, role, task, owner, useWorktree, "", "", "")
}

// StartSpawnOwned is StartSpawn with graph ownership (run+node, the reuse
// key) stamped into the entry at birth. A post-creation stamp left a
// window — and a swallowed error path — where a fast reply or failed
// update produced a permanently unowned worker: never reused, never
// persistent, reaped as an ordinary spawn (review must-fix, 2026-09-01).
// Empty ids mean an unowned CLI spawn, the previous behavior exactly. A
// non-empty seedID is the id the seed is sent under, so a caller that
// persisted it before dispatch can find this worker again after a restart.
func StartSpawnOwned(session, role, task, owner string, useWorktree bool, runID, nodeID, seedID string) (SpawnEntry, error) {
	// Generate spawn ID and extract 8-hex suffix for compact window name
	fullID := NewMsgID("spawn")
	parts := strings.Split(fullID, "-")
	suffix := parts[len(parts)-1] // 8-hex suffix
	spawnRole := "spawn-" + suffix

	entry := SpawnEntry{
		ID:        fullID,
		Role:      role,
		SpawnRole: spawnRole,
		Owner:     owner,
		Task:      task,
		Status:    "running",
		Window:    spawnRole,
		StartedAt: time.Now().Unix(),
		RunID:     runID,
		NodeID:    nodeID,
		Spec:      ReadActiveSpec(session),
	}

	// Create worktree if requested
	if useWorktree {
		wtPath, wtRef, err := createSpawnWorktree(session, spawnRole)
		if err != nil {
			// Fall back to shared CWD with warning
			fmt.Fprintf(os.Stderr, "Warning: worktree creation failed (%v), using shared CWD\n", err)
		} else {
			entry.Worktree = wtPath
			entry.WorktreeRef = wtRef
		}
	}

	// Ensure inbox directory exists and touch inbox file for spawn role
	inboxDir := filepath.Dir(InboxPath(session, spawnRole))
	if err := os.MkdirAll(inboxDir, 0755); err != nil {
		return SpawnEntry{}, fmt.Errorf("creating inbox dir: %v", err)
	}
	if err := touchFile(InboxPath(session, spawnRole)); err != nil {
		return SpawnEntry{}, fmt.Errorf("touching inbox: %v", err)
	}

	// Seed inbox with task message
	msg := NewMessage(owner, spawnRole, "request", "spawn-task", task, "")
	if seedID != "" {
		msg.ID = seedID
	}
	if err := Send(session, msg); err != nil {
		return SpawnEntry{}, fmt.Errorf("seeding inbox: %v", err)
	}
	entry.SeedMsgID = msg.ID

	if err := spawnLaunchFn(session, entry); err != nil {
		return SpawnEntry{}, err
	}

	err := withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		return WriteSpawnEntries(session, append(entries, entry))
	})
	if err != nil {
		return SpawnEntry{}, err
	}
	return entry, nil
}

// spawnLaunchFn is the external boundary StartSpawnOwned crosses — a tmux
// window and a live agent process. A package variable so unit tests can
// count worker launches without tmux.
var spawnLaunchFn = launchSpawnWindow

// launchSpawnWindow opens a worker window — console left, agent right, like
// every window — types the agent launch and wakes it for its seeded first
// turn. The window keeps its spawn-id name for targeting while the status bar
// reads "Worker": the id says nothing to a human scanning tabs (user request
// 2026-08-28).
func launchSpawnWindow(session string, entry SpawnEntry) error {
	spawnRole := entry.SpawnRole
	launcher, err := findMuxcodeBinary()
	if err != nil {
		return fmt.Errorf("finding muxcode binary: %v", err)
	}

	if err := exec.Command("tmux", "new-window", "-t", session, "-n", spawnRole).Run(); err != nil {
		return fmt.Errorf("creating tmux window: %v", err)
	}
	TmuxSetWindowOption(session+":"+spawnRole, "@display-name", "Worker")
	TmuxSetWindowOption(session+":"+spawnRole, "@display-name-upper", "WORKER")

	if err := exec.Command("tmux", "split-window", "-h", "-t", session+":"+spawnRole).Run(); err != nil {
		return fmt.Errorf("splitting window: %v", err)
	}

	// Stamp pane identity while creation-order indices still hold (MUX-117).
	if terr := TagWindowPanes(session, spawnRole); terr != nil && !errors.Is(terr, ErrPaneTagUnsupported) {
		fmt.Fprintf(os.Stderr, "Warning: pane tagging failed for %s — window marked broken, deliveries error rather than risk index misdelivery: %v\n", spawnRole, terr)
	}
	// Creation-instant: launch survives tag failure — see CreationPaneTarget.
	agentPane := CreationPaneTarget(session, spawnRole, PaneTagAgent)

	// Console is view only — a failure must not block the spawn.
	consolePane := PaneTargetForWindow(session, spawnRole, PaneTagLeft)
	_ = exec.Command("tmux", "select-pane", "-t", consolePane, "-T", "CONSOLE").Run()
	sendKeysThenEnter(consolePane, fmt.Sprintf("%s console %s", launcher, spawnRole))

	if err := sendKeysThenEnter(agentPane, spawnLaunchCommand(entry.Worktree, spawnRole, launcher, entry.Role)); err != nil {
		return fmt.Errorf("launching agent: %v", err)
	}

	go wakeSpawnedAgent(session, spawnRole)
	return nil
}

// NthSpawnWindowIndex returns the window_index of the nth live spawn
// window (1-based), ordered by index ascending. Spawn windows are
// resolved by the spawn- name prefix, never by a fixed index — they are
// dynamic, and the index a worker occupies shifts as earlier workers
// exit (MUX-128). false means the slot is empty, which callers treat as
// a clean no-op.
func NthSpawnWindowIndex(session string, n int) (int, bool) {
	if n < 1 {
		return 0, false
	}
	out, err := TmuxOutput("list-windows", "-t", session, "-F", "#{window_index}:#{window_name}")
	if err != nil {
		return 0, false
	}
	var idxs []int
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		idx, name, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || !strings.HasPrefix(name, "spawn-") {
			continue
		}
		if v, convErr := strconv.Atoi(idx); convErr == nil {
			idxs = append(idxs, v)
		}
	}
	sort.Ints(idxs)
	if n > len(idxs) {
		return 0, false
	}
	return idxs[n-1], true
}

// spawnLaunchCommand is the launch a spawn types into its worker pane, run
// from worktree when one is set. AGENT_ROLE is the spawn-specific role so the
// agent reads its own inbox, not the base role's.
func spawnLaunchCommand(worktree, spawnRole, launcher, role string) string {
	cmd := fmt.Sprintf("AGENT_ROLE=%s %s", spawnRole, AgentLaunchCommand(launcher, role, LaunchReasonSpawn))
	if worktree != "" {
		return fmt.Sprintf("cd %s && %s", worktree, cmd)
	}
	return cmd
}

// sendKeysThenEnter types text and presses Enter as two pty writes with a
// settle delay — text and Enter in one write is the documented
// dropped-Enter pitfall (PR #50 Copilot flagged the one-call form here).
func sendKeysThenEnter(target, text string) error {
	if err := exec.Command("tmux", "send-keys", "-t", target, "-l", "--", text).Run(); err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	return exec.Command("tmux", "send-keys", "-t", target, "Enter").Run()
}

// wakeSpawnedAgent delivers a spawned agent's first turn: a fresh agent
// never types by itself, so the seeded task sits in an inbox nothing
// delivers (live gap, MUX-120 — a graph worker idled 4.5min until a
// manual deliver --force). Reuses wakeAfterReload's prompt-ready wait;
// async because the daemon's map fan-out must not stall the keepalive
// past the monitor threshold. A caller that exits immediately (CLI
// spawn) cuts the goroutine short and no daemon backstop covers spawn
// roles — checkPollHealth iterates static KnownRoles — so that path
// remains open and is tracked in MUX-120. This replaced a fixed 2s Notify
// which fired while the agent was still initializing: that fell to the
// displayMessage path and poisoned the notified dedup for 30s, actively
// suppressing later wakes.
func wakeSpawnedAgent(session, spawnRole string) {
	LogLifecycle(session, "info", "daemon", "spawn-wake", spawnRole)
	wakeAfterReload(session, spawnRole)
}

// StopSpawn kills the tmux window for a spawn, cleans up the worktree, and
// marks it stopped. A kill failure is an error only while the window is
// still live — an already-gone window is the stop having happened — and a
// live window leaves the entry running so nothing reads a worker as
// stopped that is still editing (review should-fix 2026-09-09).
func StopSpawn(session, id string) error {
	entry, err := GetSpawnEntry(session, id)
	if err != nil {
		return err
	}

	if entry.Status != "running" {
		return fmt.Errorf("spawn %s is not running (status: %s)", id, entry.Status)
	}
	if entry.RunID != "" {
		LogLifecycle(session, "info", "spawn", "spawn-stop",
			fmt.Sprintf("%s: worker of run %s node %s stopped — the executor replaces a lost worker; cancel the run to stop the work", id, entry.RunID, entry.NodeID))
	}

	if err := spawnKillWindowFn(session, entry.Window); err != nil && spawnWindowExistsFn(session, entry.Window) {
		return fmt.Errorf("spawn %s: kill-window failed with the window still live: %v", id, err)
	}

	// Clean up worktree
	if err := removeSpawnWorktree(entry.Worktree); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: worktree cleanup failed for %s: %v\n", entry.SpawnRole, err)
	}

	// Update entry
	return UpdateSpawnEntry(session, id, func(e *SpawnEntry) {
		e.Status = "stopped"
		e.FinishedAt = time.Now().Unix()
	})
}

// CheckSpawnWindow checks if a tmux window exists for a spawn entry. A failed
// tmux query reads as no window; a caller that must not mistake a failed
// lookup for a stopped worker uses LookupSpawnWindow.
func CheckSpawnWindow(session, window string) bool {
	live, err := LookupSpawnWindow(session, window)
	return err == nil && live
}

// LookupSpawnWindow reports whether a tmux window exists, with the error when
// the session's windows could not be listed — absence is proven only by a
// listing that succeeded.
func LookupSpawnWindow(session, window string) (bool, error) {
	out, err := exec.Command("tmux", "list-windows", "-t", session, "-F", "#{window_name}").Output()
	if err != nil {
		return false, fmt.Errorf("tmux list-windows -t %s: %w", session, err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == window {
			return true, nil
		}
	}
	return false, nil
}

// Seams for RefreshSpawnStatus so unit tests can simulate live windows
// and observe kills without tmux.
var (
	spawnWindowLookupFn = LookupSpawnWindow
	spawnWindowExistsFn = CheckSpawnWindow
	spawnKillWindowFn   = func(session, window string) error {
		return exec.Command("tmux", "kill-window", "-t", session+":"+window).Run()
	}
)

var (
	// errNoRunWorker: the run has no live worker of the base role.
	errNoRunWorker = errors.New("run has no live worker")
	// errRunWorkerBusy: the run's workers are all held by its other
	// unfinished nodes; the dispatch waits rather than seeding over them.
	errRunWorkerBusy = errors.New("run's worker is busy")
)

// reserveRunWorker hands a run's newest live worker of a base role to nodeID,
// applying own to it in the same locked write that checked it free — so no
// second dispatch can see it free in between — and returns the entry as it
// was before own. Keyed on the run alone, every spawn and map node of the run
// shares one worker (MUX-195; per run+node before, MUX-131 Defect B).
// Liveness is the window check, not the status alone: a crashed worker's
// entry can still read "running", and reuse must never wedge a run behind a
// corpse. A worker owed a stop, or named in exclude (another lane of the same
// map), is skipped.
//
// A worker another node still needs (runWorkerHolder) is never taken: two
// nodes on one worker both harvest whichever seed it carries last, so one
// node's result is lost and the other's is recorded twice. With only such
// workers the error wraps errRunWorkerBusy naming the holder; with none at
// all it is errNoRunWorker.
func reserveRunWorker(session, runID, nodeID, role string, exclude []string, own func(*SpawnEntry)) (SpawnEntry, error) {
	if runID == "" {
		return SpawnEntry{}, errNoRunWorker
	}
	var prev SpawnEntry
	err := withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		statuses, serr := ReadAllNodeStatuses(session, runID)
		if errors.Is(serr, os.ErrNotExist) {
			serr = nil
		}
		busy := ""
		for i := len(entries) - 1; i >= 0; i-- {
			e := entries[i]
			if e.Status != "running" || e.RunID != runID || e.Role != role || e.StopPending != "" ||
				slices.Contains(exclude, e.SpawnRole) || !spawnWindowExistsFn(session, e.Window) {
				continue
			}
			holder := runWorkerHolder(session, e, nodeID, statuses)
			if serr != nil {
				holder = "unknown (node statuses unreadable: " + serr.Error() + ")"
			}
			if holder != "" {
				if busy == "" {
					busy = fmt.Sprintf("node %s holds %s", holder, e.SpawnRole)
				}
				continue
			}
			prev = e
			own(&entries[i])
			return WriteSpawnEntries(session, entries)
		}
		if busy != "" {
			return fmt.Errorf("%w: %s", errRunWorkerBusy, busy)
		}
		return errNoRunWorker
	})
	return prev, err
}

// runWorkerHolder names the node other than nodeID that still needs w, or ""
// when nodeID may take it: a running node whose task list names w — it
// harvests whatever seed w carries — or, while w's seed is unanswered, the
// unfinished node it was reserved for (a dispatch cut short before its node
// went running). A node whose status is missing is presumed unfinished:
// failing to prove a worker free must read as busy.
func runWorkerHolder(session string, w SpawnEntry, nodeID string, statuses map[string]*GraphNodeStatus) string {
	for id, st := range statuses {
		if id != nodeID && st.State == GraphNodeRunning && slices.Contains(strings.Split(st.TaskID, ","), w.SpawnRole) {
			return id
		}
	}
	if w.NodeID == nodeID || w.NodeID == "" || spawnHasResponded(session, w) {
		return ""
	}
	if st, ok := statuses[w.NodeID]; ok && st.State != GraphNodeReady && st.State != GraphNodeRunning {
		return ""
	}
	return w.NodeID
}

// workerAdoptable reports whether e is the session's idle worker of a base
// role: running with its window alive, released by its run (idleEligible and
// no longer persistent), and its current seed answered. A busy worker is
// never adoptable — a new seed would queue behind work in progress.
func workerAdoptable(session string, e SpawnEntry, role string) bool {
	return e.Status == "running" && e.Role == role && idleEligible(e) &&
		!spawnPersistent(session, e) && spawnHasResponded(session, e) &&
		spawnWindowExistsFn(session, e.Window)
}

// findIdleWorker returns the newest adoptable worker of a base role.
func findIdleWorker(session, role string) (SpawnEntry, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		if workerAdoptable(session, entries[i], role) {
			return entries[i], true
		}
	}
	return SpawnEntry{}, false
}

// claimIdleWorker hands cand to a new owner: own rewrites it in one write
// under the registry lock, after re-checking that it is still adoptable and
// has not changed hands since it was found. Two runs dispatching at once
// therefore cannot both take it, and there is no instant at which it belongs
// to two runs or to none.
func claimIdleWorker(session string, cand SpawnEntry, own func(*SpawnEntry)) (SpawnEntry, error) {
	var claimed SpawnEntry
	err := withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		for i, e := range entries {
			if e.ID != cand.ID {
				continue
			}
			if e.RunID != cand.RunID || e.SeedMsgID != cand.SeedMsgID || !workerAdoptable(session, e, cand.Role) {
				return fmt.Errorf("worker %s is no longer idle", cand.SpawnRole)
			}
			own(&entries[i])
			claimed = entries[i]
			return WriteSpawnEntries(session, entries)
		}
		return fmt.Errorf("spawn not found: %s", cand.ID)
	})
	return claimed, err
}

// releaseClaim returns a claimed worker to the idle pool exactly as cand
// found it, provided it still carries the claim's seed id: a claim whose
// follow-up failed must not leave the worker owned but untasked.
func releaseClaim(session string, cand SpawnEntry, seedID string) error {
	return UpdateSpawnEntry(session, cand.ID, func(e *SpawnEntry) {
		if e.SeedMsgID != seedID {
			return
		}
		e.RunID, e.NodeID, e.Owner, e.IdleSince = cand.RunID, cand.NodeID, cand.Owner, cand.IdleSince
		e.SeedMsgID, e.Task, e.Spec = cand.SeedMsgID, cand.Task, cand.Spec
	})
}

// dropStaleSeeds consumes spawn-task requests still pending in a worker's
// inbox: work a previous owner queued that must not reach the new one.
func dropStaleSeeds(session, spawnRole string) {
	msgs, err := Peek(session, spawnRole)
	if err != nil {
		return
	}
	for _, m := range msgs {
		if m.Type == "request" && m.Action == "spawn-task" {
			_, _ = ConsumeByID(session, spawnRole, m.ID)
		}
	}
}

// ReseedSpawn seeds a new iteration's task into an existing live worker's
// inbox and wakes it, instead of building a fresh worker (MUX-131 Defect
// B: three workers, one task, one run). The worktree advances to the
// branch tip first (advanceSpawnWorktree) — reseed is where a reused
// worker picks up what the gated commit node shipped between iterations.
// The entry's SeedMsgID moves to
// the new seed's id BEFORE the seed is sent — the reviewed-marker
// ordering (MUX-007): identity first, then the thing it identifies. Sent
// first, a window exists where spawnGroupOutcome reads the OLD id, whose
// previous-iteration reply is already responded, and falsely completes
// the new iteration; identity-first fails closed instead — an id whose
// message does not exist yet can never read as responded, and a send
// failure leaves a stalled node for the watchdogs, not a phantom
// completion.
func ReseedSpawn(session string, entry SpawnEntry, task string) (string, error) {
	advanceSpawnWorktree(session, entry)
	msg := NewMessage(entry.Owner, entry.SpawnRole, "request", "spawn-task", task, "")
	spec := ReadActiveSpec(session)
	if err := UpdateSpawnEntry(session, entry.ID, func(e *SpawnEntry) {
		e.SeedMsgID = msg.ID
		e.Task = task
		e.Spec = spec
	}); err != nil {
		return "", err
	}
	return sendSpawnSeed(session, entry, msg)
}

// sendSpawnSeed sends a seed whose id the entry already carries and wakes
// the worker. A dedup-suppressed send adopts the already-pending identical
// seed (retry --from racing an unconsumed seed) — same adopt rule as the
// NodeSend dispatch path; falling back to a fresh worker there would strand
// the pending seed AND rebuild the worker the suppression proves is already
// tasked.
func sendSpawnSeed(session string, entry SpawnEntry, msg Message) (string, error) {
	if err := Send(session, msg); err != nil {
		if !errors.Is(err, ErrSendSuppressed) {
			return "", fmt.Errorf("reseeding inbox: %v", err)
		}
		if pm, found := FindPendingInboxRequest(session, msg.To, msg.From, msg.Action, msg.Payload); found {
			msg.ID = pm.ID
			if err := UpdateSpawnEntry(session, entry.ID, func(e *SpawnEntry) {
				e.SeedMsgID = msg.ID
			}); err != nil {
				return "", err
			}
		}
	}
	graphSpawnWakeFn(session, entry.SpawnRole)
	return msg.ID, nil
}

// spawnPersistent reports whether a responded worker must be kept alive
// for reuse: graph-owned (RunID set) with its run still in flight. A
// missing or terminal run releases the worker to the normal reap path —
// which is also how a run's workers are accounted for at the end.
func spawnPersistent(session string, e SpawnEntry) bool {
	if e.RunID == "" {
		return false
	}
	run, err := ReadGraphRun(session, e.RunID)
	if err != nil {
		return false
	}
	return run.State == GraphRunRunning
}

// spawnHasResponded reports whether the worker answered its seeded
// spawn-task, read from the delivery store — the same responded status
// hasReceipt trusts. When the seed's record is gone it falls back to the
// worker's own reply in the session log: CleanExpiredDeliveries drops
// records an hour after sending, and reading that absence as "unanswered"
// stranded finished runs' workers for hours, neither held nor reaped
// (MUX-195; the GC itself is MUX-135). Only the worker's reply counts — a
// failure to prove an answer must read as busy. Entries that predate
// SeedMsgID keep the window-gone-only lifecycle.
func spawnHasResponded(session string, e SpawnEntry) bool {
	if e.SeedMsgID == "" {
		return false
	}
	ds, err := ReadDeliveryStatus(session, e.SeedMsgID)
	if errors.Is(err, os.ErrNotExist) {
		return workerRepliedInLog(session, e.SpawnRole, e.SeedMsgID)
	}
	if err != nil {
		return false
	}
	return ds.Status == StatusResponded
}

// workerRepliedInLog reports whether the session log holds spawnRole's reply
// to seedID. It scans newest-first, decodes only lines naming the seed, and
// stops at the seed itself, since a reply is always logged after its request
// — the scan costs the traffic since the seed, not the whole log.
func workerRepliedInLog(session, spawnRole, seedID string) bool {
	data, err := os.ReadFile(LogPath(session))
	if err != nil {
		return false
	}
	needle := []byte(seedID)
	lines := bytes.Split(data, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], needle) {
			continue
		}
		m, err := DecodeMessage(bytes.TrimSpace(lines[i]))
		if err != nil {
			continue
		}
		if m.ID == seedID {
			return false
		}
		if m.ReplyTo == seedID && m.From == spawnRole {
			return true
		}
	}
	return false
}

// idleEligible reports whether a worker its run has released may be held in
// the idle pool rather than reaped: graph-owned, and not owed a stop.
func idleEligible(e SpawnEntry) bool {
	return e.RunID != "" && e.StopPending == ""
}

// SpawnDisplayStatus is the status a person should read for an entry:
// "parked" for a graph worker held between iterations — running, its run
// in flight, its current seed answered — "idle" for one its run has
// released, held for the next run (MUX-195), else the stored status. The
// store's "running" is true for both but not what an observer needs: on
// 2026-09-09 two parked workers read as stuck agents and one was stopped by
// hand mid-run.
func SpawnDisplayStatus(session string, e SpawnEntry) string {
	if e.Status != "running" || e.RunID == "" || !spawnHasResponded(session, e) {
		return e.Status
	}
	if spawnPersistent(session, e) {
		return "parked"
	}
	if idleEligible(e) {
		return "idle"
	}
	return e.Status
}

// AnnotateSpawnDisplay fills Display on every entry so the formatters
// stay pure over the entries they are handed.
func AnnotateSpawnDisplay(session string, entries []SpawnEntry) []SpawnEntry {
	for i := range entries {
		entries[i].Display = SpawnDisplayStatus(session, entries[i])
	}
	return entries
}

func displayStatus(e SpawnEntry) string {
	if e.Display != "" {
		return e.Display
	}
	return e.Status
}

// RefreshSpawnStatus checks all running spawns and updates their status.
// Returns the list of entries that transitioned from running to completed.
//
// A spawn completes two ways: its window is gone, or it answered its
// seeded task — a reply implies completion, mirroring hasReceipt. The
// second path exists because a Claude worker never exits on its own: it
// reports and idles at its prompt, so a window-gone-only lifecycle left
// every live graph spawn node running forever (MUX-117 Phase 1 stalled
// ~20min until a manual kill-window). Responded workers are reaped —
// window killed (reapSpawnWindow), then the shared completion path.
//
// Persistent exception (MUX-131 Defect B): a graph-owned worker whose run
// is still in flight is NOT reaped on respond — its iterations complete
// via per-seed delivery receipts (spawnGroupOutcome) and the next node
// re-entry reseeds the same worker.
//
// Idle pool (MUX-195): once its run is terminal or gone, a graph worker is
// not reaped at once but held as its base role's one idle worker for
// SpawnIdleSecs, so the next run can take it instead of paying a cold start.
// Of several, the most recently released is held and the rest reaped; the
// held one is reaped when its quiet window closes. A worker owed a stop is
// never held, and agent spawns keep reap-on-answer, since their owner's
// completion notice rides the reap. spawn-idle and spawn-reaped rows name
// the worker and its last owner. Reaping keeps the worktree
// dirty-preservation guard of every other completion. The pass holds the
// registry lock, so it never writes back a stale copy over an adoption.
func RefreshSpawnStatus(session string) ([]SpawnEntry, error) {
	var completed []SpawnEntry
	err := withSpawnRegistryLock(session, func() error {
		var err error
		completed, err = refreshSpawnStatusLocked(session)
		return err
	})
	return completed, err
}

func refreshSpawnStatusLocked(session string) ([]SpawnEntry, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return nil, err
	}

	var completed []SpawnEntry
	var released []int
	changed := false
	finish := func(i int) {
		entries[i].Status = "completed"
		entries[i].FinishedAt = time.Now().Unix()
		changed = true
		if err := removeSpawnWorktree(entries[i].Worktree); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: worktree cleanup failed for %s: %v\n", entries[i].SpawnRole, err)
		}
		completed = append(completed, entries[i])
	}

	for i, e := range entries {
		if e.Status != "running" {
			continue
		}

		if spawnWindowExistsFn(session, e.Window) {
			if !spawnHasResponded(session, e) || spawnPersistent(session, e) {
				if e.IdleSince != 0 {
					entries[i].IdleSince = 0 // busy or owned again: no longer idle
					changed = true
				}
				continue // busy, or held for reuse — see doc comment
			}
			if idleEligible(e) {
				released = append(released, i)
				continue
			}
			if !reapSpawnWindow(session, e) {
				continue
			}
		}
		finish(i)
	}

	var window int64
	var verdicts map[int]string
	now := time.Now().Unix()
	if len(released) > 0 {
		window = SpawnIdleSecs()
		verdicts = idleVerdicts(entries, released, now, window)
	}
	for _, i := range released {
		e, reason := entries[i], verdicts[i]
		if reason == "" {
			if e.IdleSince == 0 {
				entries[i].IdleSince = now
				changed = true
				LogLifecycle(session, "info", "spawn", "spawn-idle",
					fmt.Sprintf("%s: idle — released by run %s node %s; held up to %ds as the %s idle worker", e.SpawnRole, e.RunID, e.NodeID, window, e.Role))
			}
			continue
		}
		if !reapSpawnWindow(session, e) {
			continue
		}
		LogLifecycle(session, "info", "spawn", "spawn-reaped",
			fmt.Sprintf("%s: reaped — last owner run %s node %s, new owner none (%s)", e.SpawnRole, e.RunID, e.NodeID, reason))
		finish(i)
	}

	if changed {
		if err := WriteSpawnEntries(session, entries); err != nil {
			return completed, err
		}
	}

	return completed, nil
}

// idleVerdicts decides, for each released worker, whether it is held — "" —
// or reaped, and why. Per base role the most recently released worker is the
// one held (a first sighting counts as now; a tie goes to the newer entry),
// and only while its quiet window is open; a zero window holds nothing.
func idleVerdicts(entries []SpawnEntry, released []int, now, window int64) map[int]string {
	since := func(i int) int64 {
		if entries[i].IdleSince == 0 {
			return now
		}
		return entries[i].IdleSince
	}
	warmest := map[string]int{}
	for _, i := range released {
		if w, ok := warmest[entries[i].Role]; !ok || since(i) >= since(w) {
			warmest[entries[i].Role] = i
		}
	}
	verdicts := map[int]string{}
	for _, i := range released {
		switch w := warmest[entries[i].Role]; {
		case i != w:
			verdicts[i] = "superseded by " + entries[w].SpawnRole
		case now-since(i) >= window:
			verdicts[i] = fmt.Sprintf("quiet window of %ds closed", window)
		default:
			verdicts[i] = ""
		}
	}
	return verdicts
}

// reapSpawnWindow kills a worker's window, reporting whether it is gone. A
// failed kill leaves the entry running for the next cycle: completing anyway
// would tear down the worktree under a still-live worker.
func reapSpawnWindow(session string, e SpawnEntry) bool {
	if err := spawnKillWindowFn(session, e.Window); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: reap kill-window failed for %s: %v\n", e.SpawnRole, err)
		return false
	}
	return true
}

// SpawnIdleSecs is how long a graph worker its run has released is held idle
// for the next run before it is reaped: MUXCODE_SPAWN_IDLE_SECS from the
// environment, then the config file; default 600. 0 reaps on release, the
// behaviour before MUX-195.
func SpawnIdleSecs() int64 {
	v := os.Getenv("MUXCODE_SPAWN_IDLE_SECS")
	if v == "" {
		v = GetShellConfig("")["MUXCODE_SPAWN_IDLE_SECS"]
	}
	if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil && n >= 0 {
		return n
	}
	return 600
}

// GetSpawnResult returns the last message sent FROM a spawn role in the session log.
// The spawned agent naturally sends bus messages back to the owner — the last one
// serves as the result.
func GetSpawnResult(session, spawnRole string) (Message, bool) {
	msgs := readLogForRole(session, spawnRole, 0) // 0 = no limit

	// Find the last message FROM the spawn role
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].From == spawnRole {
			return msgs[i], true
		}
	}
	return Message{}, false
}

// CleanFinishedSpawns removes all non-running spawn entries, their inbox files,
// and any remaining worktrees. Also prunes orphaned worktree directories.
func CleanFinishedSpawns(session string) (int, error) {
	var kept []SpawnEntry
	removed := 0
	err := withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Status == "running" {
				kept = append(kept, e)
				continue
			}
			_ = os.Remove(InboxPath(session, e.SpawnRole))
			if e.Worktree != "" {
				_ = removeSpawnWorktree(e.Worktree)
			}
			removed++
		}
		return WriteSpawnEntries(session, kept)
	})
	if err != nil {
		return removed, err
	}

	// Prune orphaned worktree directories under the spawn base dir
	pruneOrphanedWorktrees(session, kept)

	return removed, nil
}

// FormatSpawnList formats spawn entries as a human-readable table.
// When showAll is false, only running entries are shown.
func FormatSpawnList(entries []SpawnEntry, showAll bool) string {
	var b strings.Builder

	var filtered []SpawnEntry
	for _, e := range entries {
		if showAll || e.Status == "running" {
			filtered = append(filtered, e)
		}
	}

	if len(filtered) == 0 {
		if showAll {
			b.WriteString("No spawns.\n")
		} else {
			b.WriteString("No running spawns. Use --all to see finished spawns.\n")
		}
		return b.String()
	}

	b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %-8s %s\n",
		"ID", "ROLE", "SPAWN-ROLE", "STATUS", "OWNER", "WORKTREE", "TASK"))
	b.WriteString(strings.Repeat("-", 120) + "\n")

	for _, e := range filtered {
		task := e.Task
		if len(task) > 40 {
			task = task[:37] + "..."
		}
		wt := "shared"
		if e.Worktree != "" {
			wt = "yes"
		}
		b.WriteString(fmt.Sprintf("%-36s %-12s %-12s %-10s %-10s %-8s %s\n",
			e.ID, e.Role, e.SpawnRole, displayStatus(e), e.Owner, wt, task))
	}

	return b.String()
}

// FormatSpawnStatus formats a single spawn entry as a detailed status report.
func FormatSpawnStatus(entry SpawnEntry) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Spawn: %s\n", entry.ID))
	b.WriteString(fmt.Sprintf("  Role:       %s\n", entry.Role))
	b.WriteString(fmt.Sprintf("  Spawn Role: %s\n", entry.SpawnRole))
	b.WriteString(fmt.Sprintf("  Status:     %s\n", displayStatus(entry)))
	switch displayStatus(entry) {
	case "parked":
		b.WriteString(fmt.Sprintf("  Parked:     awaiting the next iteration of run %s node %s — idle by design, not stuck\n", entry.RunID, entry.NodeID))
	case "idle":
		b.WriteString(fmt.Sprintf("  Idle:       released by run %s node %s — held for the next run, reaped after MUXCODE_SPAWN_IDLE_SECS\n", entry.RunID, entry.NodeID))
	}
	b.WriteString(fmt.Sprintf("  Owner:      %s\n", entry.Owner))
	b.WriteString(fmt.Sprintf("  Window:     %s\n", entry.Window))
	b.WriteString(fmt.Sprintf("  Task:       %s\n", entry.Task))
	if entry.Worktree != "" {
		b.WriteString(fmt.Sprintf("  Worktree:   %s\n", entry.Worktree))
		b.WriteString(fmt.Sprintf("  Ref:        %s\n", entry.WorktreeRef))
	} else {
		b.WriteString("  Worktree:   shared (main working directory)\n")
	}
	b.WriteString(fmt.Sprintf("  Started:    %s\n", time.Unix(entry.StartedAt, 0).Format("2006-01-02 15:04:05")))

	if entry.FinishedAt > 0 {
		b.WriteString(fmt.Sprintf("  Finished:   %s\n", time.Unix(entry.FinishedAt, 0).Format("2006-01-02 15:04:05")))
		duration := time.Duration(entry.FinishedAt-entry.StartedAt) * time.Second
		b.WriteString(fmt.Sprintf("  Duration:   %s\n", duration))
	}

	return b.String()
}

// SpawnWorktreeBase returns the base directory for spawn worktrees.
func SpawnWorktreeBase(session string) string {
	return filepath.Join(os.TempDir(), "muxcode-spawn-"+session)
}

// createSpawnWorktree creates a git worktree for a spawn agent.
// Returns (worktree path, commit SHA, error).
func createSpawnWorktree(session, spawnRole string) (string, string, error) {
	// Get current HEAD commit
	refOut, err := exec.Command("git", "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", fmt.Errorf("git rev-parse HEAD: %v", err)
	}
	ref := strings.TrimSpace(string(refOut))

	// Create worktree directory
	base := SpawnWorktreeBase(session)
	wtPath := filepath.Join(base, spawnRole)
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", "", fmt.Errorf("creating worktree base dir: %v", err)
	}

	// git worktree add --detach <path> HEAD
	cmd := exec.Command("git", "worktree", "add", "--detach", wtPath, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", fmt.Errorf("git worktree add: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	return wtPath, ref, nil
}

// worktreeRemovable reports whether the worktree may be destroyed.
// A path carrying a .git entry is judged by `git status --porcelain`:
// clean → removable; dirty or undeterminable → preserved. Refusing on
// unknown is deliberate — a status failure must not read as permission
// to delete possibly-unharvested work. A path with no .git entry is not
// a worktree and stays removable (and is never probed via a walked-up
// parent repo's status).
func worktreeRemovable(worktreePath string) bool {
	if _, err := os.Stat(filepath.Join(worktreePath, ".git")); err != nil {
		return true
	}
	out, err := exec.Command("git", "-C", worktreePath, "status", "--porcelain").Output()
	return err == nil && len(bytes.TrimSpace(out)) == 0
}

// removeSpawnWorktree cleans up a spawn's git worktree.
// Returns nil if worktreePath is empty (no worktree to remove).
//
// Dirty or undeterminable worktrees are preserved, never removed: a
// worker's reply proves it answered, not that its work reached a branch
// (MUX-120 fifth gap),
// and every removal path — reap, stop, finished-clean, orphan prune —
// funnels through here, so this is the one guard that keeps unharvested
// work off the --force/RemoveAll path. Preserved worktrees cost disk
// until a human harvests or deletes them; the disk-pressure signal
// covers the leak.
func removeSpawnWorktree(worktreePath string) error {
	if worktreePath == "" {
		return nil
	}

	if !worktreeRemovable(worktreePath) {
		// A preserved worktree keeps its build output forever; the source is
		// what must survive, not the regenerable artifacts beside it.
		if !ArtifactPurgeDisabled() {
			if res, err := PurgeBuildArtifacts(worktreePath, false); err == nil && len(res.Paths) > 0 {
				fmt.Fprintf(os.Stderr, "  reclaimed %s of build artifacts from preserved worktree %s\n",
					formatBytes(res.BytesFreed), worktreePath)
			}
		}
		fmt.Fprintf(os.Stderr, "Warning: preserving spawn worktree %s — dirty or undeterminable, unharvested work may be present\n", worktreePath)
		return nil
	}

	// Try git worktree remove --force first
	cmd := exec.Command("git", "worktree", "remove", "--force", worktreePath)
	if err := cmd.Run(); err != nil {
		// Fallback: rm -rf if git worktree remove fails
		if err2 := os.RemoveAll(worktreePath); err2 != nil {
			return fmt.Errorf("git worktree remove failed (%v) and os.RemoveAll failed (%v)", err, err2)
		}
		// Prune stale worktree references after manual removal
		_ = exec.Command("git", "worktree", "prune").Run()
	}
	return nil
}

// pruneOrphanedWorktrees removes any worktree directories under the spawn base
// that don't correspond to a running spawn entry.
func pruneOrphanedWorktrees(session string, runningEntries []SpawnEntry) {
	base := SpawnWorktreeBase(session)
	dirEntries, err := os.ReadDir(base)
	if err != nil {
		return // base dir doesn't exist or isn't readable
	}

	// Build set of active worktree paths
	active := make(map[string]bool)
	for _, e := range runningEntries {
		if e.Worktree != "" {
			active[e.Worktree] = true
		}
	}

	for _, d := range dirEntries {
		if !d.IsDir() {
			continue
		}
		dirPath := filepath.Join(base, d.Name())
		if active[dirPath] {
			continue
		}
		// Orphaned — remove
		_ = removeSpawnWorktree(dirPath)
	}

	// Prune git worktree references
	_ = exec.Command("git", "worktree", "prune").Run()
}

// findMuxcodeBinary locates the muxcode binary.
// Checks: ~/.local/bin/, PATH, then the current executable path.
func findMuxcodeBinary() (string, error) {
	home, _ := os.UserHomeDir()

	// Check common install location
	candidate := filepath.Join(home, ".local", "bin", "muxcode")
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}

	// Check PATH
	if p, err := exec.LookPath("muxcode"); err == nil {
		return p, nil
	}

	// Fall back to the current executable
	if exe, err := os.Executable(); err == nil {
		return exe, nil
	}

	return "", fmt.Errorf("muxcode binary not found in ~/.local/bin/, PATH, or as current executable")
}
