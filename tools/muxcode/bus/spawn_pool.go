package bus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// errSpawnCap: one more worker of a base role would pass SpawnMaxWorkers.
var errSpawnCap = errors.New("spawn cap reached")

// SpawnMaxWorkers is the most live workers of one base role a session may
// hold (MUX-195): MUXCODE_SPAWN_MAX_WORKERS from the environment, then the
// config file; default 3 — one idle beside a graph run's worker and an
// agent's (the user's call, 2026-10-06). 0 lifts the cap. Only a fresh launch
// is refused: reuse and adoption add no worker, so lowering the cap never
// stops one already live.
func SpawnMaxWorkers() int {
	v := os.Getenv("MUXCODE_SPAWN_MAX_WORKERS")
	if v == "" {
		v = GetShellConfig("")["MUXCODE_SPAWN_MAX_WORKERS"]
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
		return n
	}
	return 3
}

// spawnCapError refuses one more worker of role when entries already hold
// SpawnMaxWorkers of it live or reserved (starting), naming them, wrapped in
// errSpawnCap.
func spawnCapError(entries []SpawnEntry, role string) error {
	limit := SpawnMaxWorkers()
	if limit == 0 {
		return nil
	}
	var live []string
	for _, e := range entries {
		if (e.Status == "running" || e.Status == spawnStarting) && e.Role == role {
			live = append(live, e.SpawnRole)
		}
	}
	if len(live) < limit {
		return nil
	}
	return fmt.Errorf("%w: %d live %s workers already (MUXCODE_SPAWN_MAX_WORKERS=%d): %s",
		errSpawnCap, len(live), role, limit, strings.Join(live, ", "))
}

// workerOwner is who a worker serves: a graph run's node, or an agent (RunID
// empty).
type workerOwner struct {
	RunID, NodeID, Owner string
}

func (o workerOwner) String() string {
	return ownerLabel(SpawnEntry{RunID: o.RunID, NodeID: o.NodeID, Owner: o.Owner})
}

// adoptWorkerFor hands cand, an idle worker found earlier, to a new owner —
// a graph run's node or an agent — and reports the worker it seeded. The
// claim comes before every side effect: ownership and the new seed id move in
// one locked write (claimIdleWorker) that re-checks cand is still idle, so of
// two claimants that found the same worker the loser fails here having
// touched nothing — it never clears or seeds a worker the winner now owns.
// Context follows Decision 1: kept while the active spec is unchanged and the
// owner stays the same kind, /clear-ed under the claim when the spec changed
// since the worker's last seed or it switches between serving a run and an
// agent. The previous owner's unconsumed seeds are dropped so its stale work
// cannot reach the new one. A failed clear releases the claim (releaseClaim;
// the worker is stopped if even that fails), and a seed that cannot be sent
// stops the claimed worker, so no failure leaves a worker owned but untasked.
// rows prefixes the lifecycle rows: <rows>ed on adoption, <rows>-failed
// otherwise. seedID, when set, is the id the seed is sent under.
func adoptWorkerFor(session string, cand SpawnEntry, to workerOwner, task, seedID, rows string) (string, bool) {
	spec := ReadActiveSpec(session)
	contextPolicy := "kept"
	switch {
	case cand.RunID == "" && to.RunID != "":
		contextPolicy = "cleared: it last served an agent"
	case cand.RunID != "" && to.RunID == "":
		contextPolicy = "cleared: it last served a graph run"
	case cand.Spec != spec:
		contextPolicy = fmt.Sprintf("cleared: active spec changed from %q", cand.Spec)
	}

	msg := NewMessage(to.Owner, cand.SpawnRole, "request", "spawn-task", adoptionNotice(cand, to)+task, "")
	if seedID != "" {
		msg.ID = seedID
	}
	claimed, err := claimIdleWorker(session, cand, func(e *SpawnEntry) {
		e.RunID, e.NodeID, e.Owner = to.RunID, to.NodeID, to.Owner
		e.IdleSince = 0
		e.SeedMsgID, e.Task, e.Spec = msg.ID, task, spec
		oweNotice(e, msg.ID)
	})
	if err != nil {
		LogLifecycle(session, "info", "daemon", rows+"-failed",
			fmt.Sprintf("%s did not adopt %s (%v) — starting fresh", to, cand.SpawnRole, err))
		return "", false
	}
	if contextPolicy != "kept" {
		if err := spawnClearFn(session, claimed.SpawnRole); err != nil {
			cleanup := "claim released"
			if rerr := releaseClaim(session, cand, msg.ID); rerr != nil {
				_, _ = stopSpawnRole(session, claimed.SpawnRole)
				cleanup = fmt.Sprintf("claim release failed (%v), worker stopped", rerr)
			}
			LogLifecycle(session, "warn", "daemon", rows+"-failed",
				fmt.Sprintf("%s could not clear idle worker %s (%v) — %s, starting fresh", to, cand.SpawnRole, err, cleanup))
			return "", false
		}
	}
	dropStaleSeeds(session, claimed.SpawnRole)
	if _, err := sendSpawnSeed(session, claimed, msg); err != nil {
		_, _ = stopSpawnRole(session, claimed.SpawnRole)
		LogLifecycle(session, "warn", "daemon", rows+"-failed",
			fmt.Sprintf("%s adopted %s but its seed failed (%v) — worker stopped, starting fresh", to, claimed.SpawnRole, err))
		return "", false
	}
	LogLifecycle(session, "info", "daemon", rows+"ed",
		fmt.Sprintf("%s adopted idle worker %s — old owner %s, new owner %s; context %s",
			to, cand.SpawnRole, ownerLabel(cand), to, contextPolicy))
	return claimed.SpawnRole, true
}

// adoptionNotice opens an adopted worker's seed: it names the new owner
// even on a graph whose preamble names none, and closes the previous
// owner's work, which the worker may still hold in context.
func adoptionNotice(prev SpawnEntry, to workerOwner) string {
	serve := "agent " + to.Owner
	if to.RunID != "" {
		serve = fmt.Sprintf("graph run %s · node %s", to.RunID, to.NodeID)
	}
	return fmt.Sprintf("[worker handed over: you now serve %s. Your work for %s is finished — "+
		"do not resume it; do only the task below.]\n\n", serve, ownerLabel(prev))
}

// spawnClearFn issues /clear to a worker's pane before an adoption that
// must not carry context over; a seam so adoption runs under test without
// tmux.
var spawnClearFn = func(session, spawnRole string) error {
	return ClearAgent(session, spawnRole, "daemon", "spawn-adopt")
}

// How an agent's spawn start got its worker (AgentSpawn.How).
const (
	SpawnStarted = "started"
	SpawnReused  = "reused"
	SpawnQueued  = "queued"
	SpawnAdopted = "adopted"
)

// AgentSpawn is the worker a spawn start was given and how it was got.
type AgentSpawn struct {
	Entry SpawnEntry
	How   string
}

// AcquireAgentWorker is `muxcode spawn start` on the one road to a worker
// (MUX-195): an agent holds one worker per base role and worktree kind. In
// order:
//
//  1. its own live worker is reseeded when idle or, while busy, handed the
//     task queued behind its current one (Decision 3) — never a second
//     worker;
//  2. else the session's idle worker of the role and kind is adopted;
//  3. else a fresh worker is launched within SpawnMaxWorkers — a refusal
//     wraps errSpawnCap and writes a spawn-cap-refused row.
//
// The whole decision runs under the agent's spawn lock (withAgentSpawnLock),
// so two starts from one agent cannot both find no worker and both launch,
// and the second always finds the first's worker. Unserialized, concurrent
// first starts launched two workers, and a queued task whose seed check lost
// to a concurrent queue fell through to a fresh launch beside the agent's own
// worker. A seed that still changed under the lock — a dedup adoption — is
// retried against the agent's worker before anything else is tried.
func AcquireAgentWorker(session, role, task, owner string, useWorktree bool) (AgentSpawn, error) {
	var got AgentSpawn
	err := withAgentSpawnLock(session, owner, role, useWorktree, func() error {
		var err error
		got, err = acquireAgentWorkerLocked(session, role, task, owner, useWorktree)
		return err
	})
	return got, err
}

// ownWorkerAttempts bounds the retries of an agent's own worker whose seed
// changed between the find and the claim.
const ownWorkerAttempts = 3

func acquireAgentWorkerLocked(session, role, task, owner string, useWorktree bool) (AgentSpawn, error) {
	for range ownWorkerAttempts {
		own, ok := findOwnerWorker(session, owner, role, useWorktree)
		if !ok {
			break
		}
		if got, ok, err := giveOwnWorker(session, own, task); ok || err != nil {
			return got, err
		}
	}
	if cand, ok := findIdleWorker(session, role, useWorktree); ok {
		if id, ok := adoptWorkerFor(session, cand, workerOwner{Owner: owner}, task, "", "spawn-adopt"); ok {
			e, _ := findSpawnByRole(session, id)
			return AgentSpawn{Entry: e, How: SpawnAdopted}, nil
		}
	}
	e, err := StartSpawn(session, role, task, owner, useWorktree)
	if errors.Is(err, errSpawnCap) {
		LogLifecycle(session, "warn", "spawn", "spawn-cap-refused", fmt.Sprintf("agent %s: %v", owner, err))
	}
	return AgentSpawn{Entry: e, How: SpawnStarted}, err
}

// giveOwnWorker hands task to an agent's own worker: reseeded when idle, its
// worktree advanced; queued behind the current task when busy, with no
// worktree advance (it would stage in-progress edits) and no wake
// (postSpawnSeed). The claim comes before every side effect: the seed id
// moves in one locked write that re-checks the worker is still the agent's on
// the seed it was found with, and decides there whether it is idle. A caller
// whose find went stale — a run adopted the worker, or another task reached
// it first — gets ok false having touched nothing, the worktree included;
// advanced before the claim, a stale caller ran git add and reset in a tree
// another task had already started in. The worker reads busy until the last
// queued task is answered; each task is owed its own completion notice.
func giveOwnWorker(session string, own SpawnEntry, task string) (AgentSpawn, bool, error) {
	how := SpawnQueued
	msg := NewMessage(own.Owner, own.SpawnRole, "request", "spawn-task", task, "")
	spec := ReadActiveSpec(session)
	var claimed SpawnEntry
	err := withSpawnRegistryLock(session, func() error {
		entries, err := ReadSpawnEntries(session)
		if err != nil {
			return err
		}
		for i, e := range entries {
			if e.ID != own.ID {
				continue
			}
			if e.Status != "running" || e.RunID != "" || e.Owner != own.Owner || e.SeedMsgID != own.SeedMsgID {
				return errOwnWorkerTaken
			}
			if spawnHasResponded(session, e) {
				how = SpawnReused
			}
			entries[i].SeedMsgID, entries[i].Task, entries[i].Spec = msg.ID, task, spec
			oweNotice(&entries[i], msg.ID)
			claimed = entries[i]
			return WriteSpawnEntries(session, entries)
		}
		return errOwnWorkerTaken
	})
	if errors.Is(err, errOwnWorkerTaken) {
		return AgentSpawn{}, false, nil
	}
	if err != nil {
		return AgentSpawn{}, false, err
	}
	if how == SpawnReused {
		spawnAdvanceWorktreeFn(session, claimed)
	}
	send := sendSpawnSeed
	if how == SpawnQueued {
		send = postSpawnSeed
	}
	if _, err := send(session, claimed, msg); err != nil {
		return AgentSpawn{}, false, err
	}
	LogLifecycle(session, "info", "spawn", "spawn-"+how,
		fmt.Sprintf("%s: %s for agent %s", own.SpawnRole, how, own.Owner))
	e, err := GetSpawnEntry(session, own.ID)
	return AgentSpawn{Entry: e, How: how}, true, err
}

// errOwnWorkerTaken: the agent's worker changed hands or seeds after it was
// found.
var errOwnWorkerTaken = errors.New("own worker taken")

// withAgentSpawnLock runs fn holding an exclusive flock on the lock file of
// one agent's workers of a base role and worktree kind, serializing that
// agent's spawn starts across processes while other agents' run in
// parallel. Taken outside the registry lock, never inside it.
func withAgentSpawnLock(session, owner, role string, worktree bool, fn func() error) error {
	safe := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
				return r
			}
			return '_'
		}, s)
	}
	path := fmt.Sprintf("%s.agent-%s-%s-%t.lock", SpawnPath(session), safe(owner), safe(role), worktree)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0644)
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

// findOwnerWorker returns an agent's own newest live worker of a base role
// and worktree kind — one an earlier spawn start of the agent's made.
func findOwnerWorker(session, owner, role string, worktree bool) (SpawnEntry, bool) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return SpawnEntry{}, false
	}
	for i := len(entries) - 1; i >= 0; i-- {
		e := entries[i]
		if e.Status == "running" && e.RunID == "" && e.Owner == owner && e.Role == role &&
			(e.Worktree != "") == worktree && e.StopPending == "" && spawnWindowExistsFn(session, e.Window) {
			return e, true
		}
	}
	return SpawnEntry{}, false
}
