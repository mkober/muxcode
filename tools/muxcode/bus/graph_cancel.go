package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// SpawnSurvivor is a graph worker a cancel tried and failed to stop.
type SpawnSurvivor struct {
	ID        string // spawn entry id — what `muxcode spawn stop` takes
	SpawnRole string // bus role and window name
	Err       error
}

// CancelIncompleteError reports a cancel that could not verify its work
// stopped: a worker survived its stop, or a cleanup step failed (the
// spawn registry could not be read, or a delegation could not be
// retracted). The run is left GraphRunCanceling, never GraphRunCanceled,
// and re-running the cancel retries every step.
type CancelIncompleteError struct {
	RunID     string
	Survivors []SpawnSurvivor
	Cleanup   []error
}

func (e *CancelIncompleteError) Error() string {
	var parts []string
	for _, s := range e.Survivors {
		parts = append(parts, fmt.Sprintf("worker %s survived (%v) — stop with: muxcode spawn stop %s", s.SpawnRole, s.Err, s.ID))
	}
	for _, err := range e.Cleanup {
		parts = append(parts, "cleanup failed: "+err.Error())
	}
	return fmt.Sprintf("run %s NOT canceled: %s; then re-run: muxcode graph cancel %s",
		e.RunID, strings.Join(parts, "; "), e.RunID)
}

// CancelGraphRun cancels a run and verifies its work has stopped.
//
// A canceled run once kept its spawn worker editing the shared checkout
// for 8m45s (MUX-182; reproduced on this repo 2026-09-23, 60s past the
// cancel): running nodes were left to finish and no worker was stopped.
// Now, in order:
//
//  1. The run moves to GraphRunCanceling, which halts dispatch and
//     replaceLostWorkers — no worker is started or replaced after this.
//  2. Every worker the run owns is stopped (runSpawnRoles, stopSpawnRole),
//     each stop logged as graph-cancel-spawn-stopped or -survived.
//  3. What the workers already delegated is retracted: their in-flight
//     tasks time out and their unconsumed requests leave every inbox, so
//     a run or plan agent does not act on a dead run's behalf. A request
//     already consumed cannot be recalled.
//  4. Unstarted nodes are skipped, and running spawn/map nodes whose workers
//     all stopped are skipped. A running send node's request is withdrawn if
//     its agent has not read it yet; one its agent is already working on
//     cannot be recalled, so it fails the cancel until the agent answers
//     (see stopSendNode). Each send node's task is expired so the stall
//     watchdog cannot re-drive it (2026-08-27, a canceled loop's edit node
//     re-driven).
//  5. Session artifacts are purged only once no worker survived and every
//     cleanup step succeeded — live work may still be writing them.
//
// The whole cancel holds the run lock (lockGraphRun), so no executor tick
// can be mid-spawn while the registry is read.
//
// Only when no worker survived and every cleanup step succeeded does the
// run become GraphRunCanceled. Otherwise it fails closed: the run stays
// canceling, the survivors' nodes stay running, and a
// *CancelIncompleteError names each survivor with the command that stops
// it, and each failed step — reporting canceled while a worker ran was the
// incident. An unreadable or partly malformed spawn registry is such a
// failure, never an empty worker set.
//
// Before any of that, the caller must pass CheckCancelAuthority, resolved
// here rather than by each caller because the CLI and the TUI both cancel;
// a refusal logs graph-cancel-refused and touches nothing. The actor is
// named on graph-run-canceled, as graph-gate-approved names its approver.
// Authority is decided on the run as read under the lock and held through
// the mutation: it depends on state, and a canceled run read before the
// lock may be running again by a RetryGraphRun before the cancel acts.
func CancelGraphRun(session, runID string) error {
	unlock, err := lockExistingGraphRun(session, runID, "canceled")
	if err != nil {
		return err
	}
	defer unlock()

	run, err := ReadGraphRun(session, runID)
	if err != nil {
		return fmt.Errorf("unknown run: %w", err)
	}
	actor := BusActorVerified()
	if deny := CheckCancelAuthority(actor, run); deny != "" {
		LogLifecycle(session, "warn", actor, "graph-cancel-refused", fmt.Sprintf("Graph run %s: %s", runID, deny))
		return errors.New(deny)
	}
	runStopAuthorizedHook()

	if err := UpdateGraphRunState(session, runID, GraphRunCanceling); err != nil {
		return err
	}
	statuses, err := ReadAllNodeStatuses(session, runID)
	if err != nil {
		return err
	}

	roles, err := runSpawnRoles(session, runID, statuses)
	if err != nil {
		cerr := &CancelIncompleteError{RunID: runID, Cleanup: []error{fmt.Errorf("read spawn registry: %w", err)}}
		LogLifecycle(session, "error", "daemon", "graph-cancel-incomplete", cerr.Error())
		return cerr
	}
	survivors := stopRunWorkers(session, runID, roles)
	cleanup := retractSpawnDelegations(session, runID, roles)
	sendNodes, err := runSendNodes(session, runID)
	if err != nil {
		cleanup = append(cleanup, fmt.Errorf("read run graph: %w", err))
	}

	owned := make(map[string]bool, len(roles))
	for _, r := range roles {
		owned[r] = true
	}
	survived := make(map[string]bool, len(survivors))
	for _, s := range survivors {
		survived[s.SpawnRole] = true
	}
	for id, st := range statuses {
		role, isSend := sendNodes[id]
		switch {
		case st.State == GraphNodePending || st.State == GraphNodeReady || st.State == GraphNodeWaiting:
			_ = TransitionGraphNode(session, runID, id, GraphNodeSkipped, nil)
		case st.State == GraphNodeRunning && isSend && st.TaskID != "":
			if err := stopSendNode(session, runID, id, role, st.TaskID); err != nil {
				cleanup = append(cleanup, err)
			}
			continue
		case st.State == GraphNodeRunning:
			if taskIDNamesAny(st.TaskID, owned) && !taskIDNamesAny(st.TaskID, survived) {
				_ = TransitionGraphNode(session, runID, id, GraphNodeSkipped, func(s *GraphNodeStatus) {
					s.Output = "canceled while running"
				})
			}
		}
		if isSend && st.TaskID != "" {
			if err := expireTask(session, st.TaskID); err != nil {
				cleanup = append(cleanup, fmt.Errorf("expire node %s task %s: %w", id, st.TaskID, err))
			}
		}
	}
	if len(survivors) == 0 && len(cleanup) == 0 {
		cancelPurgeFn(session, "graph run "+runID+" canceled")
	}

	if len(survivors) > 0 || len(cleanup) > 0 {
		cerr := &CancelIncompleteError{RunID: runID, Survivors: survivors, Cleanup: cleanup}
		LogLifecycle(session, "error", "daemon", "graph-cancel-incomplete", cerr.Error())
		return cerr
	}
	if err := UpdateGraphRunState(session, runID, GraphRunCanceled); err != nil {
		return err
	}
	LogLifecycle(session, "info", actor, "graph-run-canceled", fmt.Sprintf("%s canceled by %s", runID, actor))
	return nil
}

// runSpawnRoles returns, sorted, every spawn role the run owns: entries
// stamped with the run at birth, plus any role a node's TaskID names —
// spawn and map nodes store worker roles there, not task ids. A node naming a
// spawn role the registry does not hold is an error while that role's window
// is live, whatever the node's state — a completed node keeps a parked
// worker: the registry is missing or incomplete, and reading the gap as "no
// worker" would let the run reach canceled with the worker alive (Copilot on
// PR #91). A window absent from a tmux listing that succeeded is the proof of
// a stopped worker, and it is what lets a run whose finished workers were
// pruned by CleanFinishedSpawns still cancel; a failed listing proves nothing
// and fails the cancel too.
func runSpawnRoles(session, runID string, statuses map[string]*GraphNodeStatus) ([]string, error) {
	entries, malformed, err := scanSpawnEntries(session)
	if err != nil {
		return nil, err
	}
	if malformed > 0 {
		return nil, fmt.Errorf("%d malformed spawn registry line(s) — a live worker of this run could be among them", malformed)
	}
	known := make(map[string]bool, len(entries))
	set := map[string]bool{}
	for _, e := range entries {
		known[e.SpawnRole] = true
		if e.RunID == runID {
			set[e.SpawnRole] = true
		}
	}
	for id, st := range statuses {
		if st == nil {
			continue
		}
		for _, tok := range strings.Split(st.TaskID, ",") {
			switch {
			case known[tok]:
				set[tok] = true
			case IsSpawnRole(tok):
				live, lerr := spawnWindowLookupFn(session, tok)
				if lerr != nil {
					return nil, fmt.Errorf("node %s names worker %s, absent from the spawn registry, and its window could not be checked: %w", id, tok, lerr)
				}
				if live {
					return nil, fmt.Errorf("node %s names worker %s, absent from the spawn registry with its window still live", id, tok)
				}
			}
		}
	}
	roles := make([]string, 0, len(set))
	for r := range set {
		roles = append(roles, r)
	}
	sort.Strings(roles)
	return roles, nil
}

// runSendNodes maps each of the run's send nodes to its role — the only
// nodes whose TaskID is a task id. Spawn and map nodes store worker roles
// there, and a large map's joined list is too long to be a filename, so
// passing it to expireTask fails the cancel on every retry (review must-fix,
// 2026-09-24).
func runSendNodes(session, runID string) (map[string]string, error) {
	g, err := ReadGraphRunGraph(session, runID)
	if err != nil {
		return nil, err
	}
	send := map[string]string{}
	for _, n := range g.Nodes {
		if n.Type == NodeSend {
			send[n.ID] = n.Role
		}
	}
	return send, nil
}

// stopSendNode stops a running send node's work, or reports that it cannot.
// A send node has no worker of its own: its request goes to a shared agent.
// If the request is still unread in an inbox it is withdrawn, the task
// expired and the node skipped — the agent never starts. Otherwise the agent
// may have read it, and while its pane is not idle it may be working on a dead
// run's behalf; nothing can recall that, so the cancel fails closed until it
// answers or goes idle rather than report canceled while it acts (Copilot on
// PR #89). Neither a timed-out task nor a missing receipt is evidence: the
// daemon times tasks out while agents keep working, and Receive drains the
// inbox before its best-effort receipt write. Only a request found and
// withdrawn — now, or by an earlier cancel (delivery marked expired) — is
// proven unread. An idle agent is working on nothing: the task is expired so
// the stall watchdog cannot re-drive it (2026-08-27, a canceled loop's node
// re-driven). An answered request needs nothing.
func stopSendNode(session, runID, nodeID, role, taskID string) error {
	inboxes, err := inboxRoles(session)
	if err != nil {
		return fmt.Errorf("send node %s: list inboxes: %w", nodeID, err)
	}
	for _, inbox := range inboxes {
		msgs, err := receiveMatching(session, inbox, "", func(m Message) bool { return m.ID == taskID })
		if err != nil {
			return fmt.Errorf("send node %s: withdraw from %s inbox: %w", nodeID, inbox, err)
		}
		if len(msgs) == 0 {
			continue
		}
		markDeliveryExpired(session, taskID)
		if err := expireTask(session, taskID); err != nil {
			return fmt.Errorf("expire node %s task %s: %w", nodeID, taskID, err)
		}
		_ = TransitionGraphNode(session, runID, nodeID, GraphNodeSkipped, func(s *GraphNodeStatus) {
			s.Output = "canceled before its agent read the request"
		})
		return nil
	}
	task, err := ReadTask(session, taskID)
	if err != nil {
		return fmt.Errorf("send node %s: read task %s: %w", nodeID, taskID, err)
	}
	if task.Status == TaskCompleted {
		return nil
	}
	ds, derr := ReadDeliveryStatus(session, taskID)
	withdrawn := derr == nil && ds.Status == StatusExpired
	if !withdrawn && !graphAgentIdleFn(session, role) {
		return fmt.Errorf("send node %s: its %s agent is still working on the request (task %s) — cancel again once it answers or goes idle", nodeID, role, taskID)
	}
	if err := expireTask(session, taskID); err != nil {
		return fmt.Errorf("expire node %s task %s: %w", nodeID, taskID, err)
	}
	_ = TransitionGraphNode(session, runID, nodeID, GraphNodeSkipped, func(s *GraphNodeStatus) {
		s.Output = "canceled with no agent working on the request"
	})
	return nil
}

// stopRunWorkers stops each role's worker and returns those that survived.
func stopRunWorkers(session, runID string, roles []string) []SpawnSurvivor {
	var survivors []SpawnSurvivor
	for _, role := range roles {
		stopped, err := stopSpawnRole(session, role)
		if err != nil {
			id := role
			if e, ok := findSpawnByRole(session, role); ok {
				id = e.ID
			}
			survivors = append(survivors, SpawnSurvivor{ID: id, SpawnRole: role, Err: err})
			LogLifecycle(session, "error", "daemon", "graph-cancel-spawn-survived",
				fmt.Sprintf("%s: worker %s (%s) survived its stop: %v", runID, role, id, err))
			continue
		}
		if stopped {
			LogLifecycle(session, "info", "daemon", "graph-cancel-spawn-stopped",
				fmt.Sprintf("%s: worker %s stopped", runID, role))
		}
	}
	return survivors
}

// stopSpawnRole stops the newest worker holding spawnRole and reports
// whether anything was live to stop. StopSpawn keys on the entry ID, so
// a caller holding a role (what graph nodes record) must come through
// here. An entry no longer running is stopped already unless its window
// is somehow still live, in which case the window is killed and must be
// gone afterwards.
func stopSpawnRole(session, spawnRole string) (bool, error) {
	e, ok := findSpawnByRole(session, spawnRole)
	if !ok {
		return false, fmt.Errorf("spawn not found: %s", spawnRole)
	}
	if e.Status == "running" {
		return true, StopSpawn(session, e.ID)
	}
	if !spawnWindowExistsFn(session, e.Window) {
		return false, nil
	}
	if err := spawnKillWindowFn(session, e.Window); err != nil && spawnWindowExistsFn(session, e.Window) {
		return true, fmt.Errorf("spawn %s (%s): kill-window failed with the window still live: %v", e.ID, e.Status, err)
	}
	return true, nil
}

// retractSpawnDelegations times out the in-flight tasks the roles sent
// and removes their unconsumed requests from every inbox, marking each
// removed row expired rather than received. It retracts everything it
// can and returns every failure, so the caller never reads a partial
// retraction as a complete one. A task file that cannot be read is a
// failure too: its sender is unknown, so it may be one of the roles'.
func retractSpawnDelegations(session, runID string, roles []string) []error {
	if len(roles) == 0 {
		return nil
	}
	from := make(map[string]bool, len(roles))
	for _, r := range roles {
		from[r] = true
	}

	var errs []error
	expired := 0
	tasks, unreadable, err := scanTasks(session, TaskInFlight)
	if err != nil {
		errs = append(errs, fmt.Errorf("list in-flight tasks: %w", err))
	}
	errs = append(errs, unreadable...)
	for _, t := range tasks {
		if !from[t.From] {
			continue
		}
		if err := expireTask(session, t.ID); err != nil {
			errs = append(errs, fmt.Errorf("expire task %s: %w", t.ID, err))
			continue
		}
		expired++
	}

	var retracted []string
	inboxes, err := inboxRoles(session)
	if err != nil {
		errs = append(errs, fmt.Errorf("list inboxes: %w", err))
	}
	for _, inbox := range inboxes {
		msgs, err := receiveMatching(session, inbox, "", func(m Message) bool {
			return m.Type == "request" && from[m.From]
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("withdraw from %s inbox: %w", inbox, err))
		}
		for _, m := range msgs {
			markDeliveryExpired(session, m.ID)
			retracted = append(retracted, fmt.Sprintf("%s->%s:%s", m.From, inbox, m.Action))
		}
	}

	if expired > 0 || len(retracted) > 0 {
		LogLifecycle(session, "info", "daemon", "graph-cancel-retracted",
			fmt.Sprintf("%s: %d in-flight task(s) expired, %d request(s) withdrawn %v", runID, expired, len(retracted), retracted))
	}
	return errs
}

// inboxRoles lists every role holding an inbox file in the session. A
// session with no inbox directory has no inboxes, not an error.
func inboxRoles(session string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(BusDir(session), "inbox"))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var roles []string
	for _, e := range entries {
		if name := e.Name(); !e.IsDir() && strings.HasSuffix(name, ".jsonl") {
			roles = append(roles, strings.TrimSuffix(name, ".jsonl"))
		}
	}
	return roles, nil
}

// graphRunLockWait bounds how long a cancel waits out an executor tick. A
// variable so tests of a held lock need not wait it out.
var graphRunLockWait = 30 * time.Second

// cancelPurgeFn purges session artifacts for a completed cancel. A test seam.
var cancelPurgeFn = PurgeSessionArtifacts

// errGraphRunBusy reports the run lock held past the caller's wait.
var errGraphRunBusy = errors.New("graph run lock held")

// lockGraphRun takes the run's cross-process lock, retrying until wait
// elapses (wait 0 is a single try), and returns its release. It is what
// serializes a cancel with worker creation: StepGraphRun holds it for the
// whole tick — every dispatch, reseed and replacement happens inside one —
// and CancelGraphRun for the whole cancel, so a tick either registers its
// spawn before the cancel reads the registry or sees the run canceling and
// spawns nothing. A state re-read alone left the check-to-spawn window
// open (review must-fix, 2026-09-23).
func lockGraphRun(session, runID string, wait time.Duration) (func(), error) {
	path := filepath.Join(GraphRunDir(session, runID), "run.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || !time.Now().Before(deadline) {
			_ = f.Close()
			if errors.Is(err, syscall.EWOULDBLOCK) {
				return nil, errGraphRunBusy
			}
			return nil, err
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// lockExistingGraphRun is lockGraphRun at graphRunLockWait for a caller
// that must then read and change the run — cancel, retry, and an authorized
// spawn stop. A missing run reports as unknown rather than as a lock failure.
func lockExistingGraphRun(session, runID, verb string) (func(), error) {
	unlock, err := lockGraphRun(session, runID, graphRunLockWait)
	if err == nil {
		return unlock, nil
	}
	if _, rerr := ReadGraphRun(session, runID); rerr != nil {
		return nil, fmt.Errorf("unknown run: %w", rerr)
	}
	return nil, fmt.Errorf("run %s NOT %s: cannot serialize with the executor: %w", runID, verb, err)
}

// runStopAuthorizedHook runs after a stop is authorized, under the run lock
// and before the mutation. Tests use it to land a retry in that window.
var runStopAuthorizedHook = func() {}

// markDeliveryExpired records a message withdrawn before anyone read it.
func markDeliveryExpired(session, msgID string) {
	ds, err := ReadDeliveryStatus(session, msgID)
	if err != nil {
		ds = DeliveryStatus{ID: msgID}
	}
	ds.Status = StatusExpired
	_ = writeDeliveryStatus(session, ds)
}

// taskIDNamesAny reports whether a node's comma-separated TaskID names
// any role in set.
func taskIDNamesAny(taskID string, set map[string]bool) bool {
	for _, tok := range strings.Split(taskID, ",") {
		if set[tok] {
			return true
		}
	}
	return false
}
