package bus

import (
	"fmt"
	"strings"
	"sync"
)

// Dead graph workers (MUX-139 Phase 3). A worker whose agent process exits
// keeps its tmux window, and RefreshSpawnStatus reads window presence, so the
// entry stays "running" while its node waits on a seed nobody is working — and
// with that seed already consumed no redrive fires either. Run
// 1788365614-spec-to-pr-64c5fe4b stalled indefinitely that way on 2026-09-02.
//
// resumeDeadWorkers finds such a worker and resumes it into its conversation,
// or fails its node loudly. The session id comes from the pane's exit banner
// only: graph workers run in the session checkout (no worktree, MUX-178), a
// cwd TranscriptIDForCwd always declines as shared. A resumed worker gets no
// work until verifyResumedWorkers has seen it come back with its definition.

// deadWorkerConfirmSecs is how long a worker must read dead with no exit
// banner before its node is failed: IsAlive is a pane reading, and one bad
// capture must not fail a run. A banner is proof of exit and needs no wait.
const deadWorkerConfirmSecs int64 = 30

// resumeVerifySecs bounds how long a resumed worker may stay unverified —
// not yet alive, or its pane unreadable — before its node fails, so a resume
// that never lands fails loudly instead of re-creating the stall it fixes.
const resumeVerifySecs int64 = 60

// resumeVerifyLines is the scrollback read for the definition-less banner,
// which prints once at session start (definition watchdog's depth).
const resumeVerifyLines = 200

// Seams: tests drive worker liveness, the banner scrape, the launch keys and
// the post-resume capture without tmux.
var (
	spawnWorkerAliveFn = func(session string, e SpawnEntry) bool {
		return IsHarnessActive(session, e.SpawnRole) || ResolveProvider(e.Role).IsAlive(session, e.SpawnRole)
	}
	spawnWorkerResumeIDFn = func(session string, e SpawnEntry) (string, bool) {
		content, err := captureResumePane(PaneTargetForWindow(session, e.Window, PaneTagAgent))
		if err != nil {
			return "", false
		}
		id, _ := restartResumeTarget(content)
		return id, id != ""
	}
	spawnWorkerLaunchFn  = launchResumedWorker
	spawnWorkerCaptureFn = func(session string, e SpawnEntry) (string, error) {
		return TmuxOutput("capture-pane", "-t", PaneTargetForWindow(session, e.Window, PaneTagAgent),
			"-p", "-J", "-S", fmt.Sprintf("-%d", resumeVerifyLines))
	}
)

// deadWorkerSince records each worker's first dead sighting without a banner.
var (
	deadWorkerMu    sync.Mutex
	deadWorkerSince = map[string]int64{}
)

// deadSpawnWorkers lists a node's workers that are running in the registry
// with a live window and an unanswered seed, but whose agent pane shows the
// agent gone.
func deadSpawnWorkers(session, taskIDs string) (dead []SpawnEntry, alive []string) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return nil, nil
	}
	byRole := make(map[string]SpawnEntry, len(entries))
	for _, e := range entries {
		byRole[e.SpawnRole] = e
	}
	for _, id := range strings.Split(taskIDs, ",") {
		e, ok := byRole[id]
		if !ok || e.Status != "running" || e.SeedMsgID == "" || spawnHasResponded(session, e) {
			continue
		}
		if !spawnWindowExistsFn(session, e.Window) || spawnWorkerAliveFn(session, e) {
			alive = append(alive, id)
			continue
		}
		dead = append(dead, e)
	}
	return dead, alive
}

// resumeDeadWorkers resumes a spawn or map node's dead workers and reports
// whether it acted. A Claude worker whose pane offers a session is relaunched
// with `--resume <id>` and its full launch line, recorded as pending
// verification first (identity before the act it identifies), and held until
// verifyResumedWorkers reseeds it. A worker that cannot be resumed — no banner
// after deadWorkerConfirmSecs, a provider with no resume, or resumes exhausted
// — fails the node and is stopped, so the run fails loudly and a retry starts
// fresh (reserveRunWorker never reuses a stopped worker). Resumes share the
// node's redrive cap with replaceLostWorkers. MUXCODE_AUTO_RESUME_DISABLE=1
// returns before any of it, leaving the executor's previous behaviour intact.
//
// A resume relaunches the worker in its own window under its own entry and
// never creates one, so it cannot give the run a second worker (MUX-195); it
// runs only when replaceLostWorkers did not act this tick, and the two act on
// disjoint workers — resume on running entries, replacement on ended ones.
func resumeDeadWorkers(session string, run *GraphRun, n *Node, st *GraphNodeStatus, now int64) bool {
	if AutoResumeDisabled() {
		return false
	}
	if verifyResumedWorkers(session, run, n, st.TaskID, now) {
		return true
	}
	dead, alive := deadSpawnWorkers(session, st.TaskID)
	clearDeadWorkerSince(alive...)
	for _, e := range dead {
		claude := IsClaudeTUI(ResolveProvider(e.Role))
		id, ok := "", false
		if claude {
			id, ok = spawnWorkerResumeIDFn(session, e)
		}
		switch {
		case ok && st.Redrives >= graphRedriveMax:
			return failDeadWorker(session, run, n, e, fmt.Sprintf("%d resumes exhausted", graphRedriveMax))
		case ok:
			if err := UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) {
				s.ResumeID, s.ResumedAt = id, now
			}); err != nil {
				return failDeadWorker(session, run, n, e, "recording the resume: "+err.Error())
			}
			if err := spawnWorkerLaunchFn(session, e, id); err != nil {
				return failDeadWorker(session, run, n, e, "resume failed: "+err.Error())
			}
			clearDeadWorkerSince(e.SpawnRole)
			_ = MutateNodeStatus(session, run.ID, n.ID, func(s *GraphNodeStatus) {
				s.Redrives++
				s.LastRedrive = now
			})
			st.Redrives++
			st.LastRedrive = now
			LogLifecycle(session, "info", "daemon", "agent-resume", fmt.Sprintf("%s: id=%s source=%s", e.SpawnRole, id, ResumeSourcePane))
			LogLifecycle(session, "warn", "daemon", "graph-spawn-resumed",
				fmt.Sprintf("%s: %s worker %s exited before answering — resumed session %s (%d/%d)",
					run.ID, n.ID, e.SpawnRole, id, st.Redrives, graphRedriveMax))
			return true
		}
		if now-markDeadWorkerSince(e.SpawnRole, now) < deadWorkerConfirmSecs {
			continue
		}
		reason := "no resumable session in its pane"
		if !claude {
			reason = "provider " + ResolveProviderCLI(e.Role) + " cannot resume"
		}
		return failDeadWorker(session, run, n, e, reason)
	}
	return false
}

// resumeSettleSecs is how long a resumed session must stay clean after its
// prompt first draws before it is trusted with work.
const resumeSettleSecs int64 = 5

// verifyResumedWorkers holds a node while any worker it resumed is unverified,
// and reports whether it held: no task is delivered and no completion read
// until the resumed session has positively finished starting — its Claude
// composer drawn in the live pane (resumedSessionReady) — and stayed
// free of the definition-less banner for resumeSettleSecs since. Claude Code
// prints that banner even when --agent/--agents were on the argv (MUX-136), so
// a live process, launcher text, or a blank pane mid-startup proves nothing,
// and nothing else watches a spawn — checkAgentHealth and the definition
// watchdog cover KnownRoles only. The banner at any point stops the worker and
// fails the node; a failed capture or a session not yet settled stays pending,
// failing the node only once resumeVerifySecs pass without a verdict.
func verifyResumedWorkers(session string, run *GraphRun, n *Node, taskIDs string, now int64) bool {
	held := false
	for _, e := range resumePendingWorkers(session, taskIDs) {
		held = true
		content, err := spawnWorkerCaptureFn(session, e)
		if err == nil && PaneShowsDefinitionlessAgent(SinceLastAgentExit(content)) {
			return failDeadWorker(session, run, n, e, "resumed without its agent definition — default tools, no role restrictions")
		}
		ready := err == nil && resumedSessionReady(content) && spawnWorkerAliveFn(session, e)
		switch {
		case ready && e.ReadyAt == 0:
			_ = UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) { s.ReadyAt = now })
			continue
		case ready && now-e.ReadyAt >= resumeSettleSecs:
			if err := reseedResumedWorker(session, e); err != nil {
				return failDeadWorker(session, run, n, e, "reseeding the resumed worker: "+err.Error())
			}
			LogLifecycle(session, "info", "daemon", "graph-spawn-resume-verified",
				fmt.Sprintf("%s: %s worker %s came back with its definition — reseeded", run.ID, n.ID, e.SpawnRole))
			continue
		case ready:
			continue
		}
		if now-e.ResumedAt < resumeVerifySecs {
			continue
		}
		why := "it never finished starting"
		if err != nil {
			why = "its pane could not be read: " + err.Error()
		}
		return failDeadWorker(session, run, n, e, fmt.Sprintf("resume unverified after %ds: %s", resumeVerifySecs, why))
	}
	return held
}

// resumedSessionReady reports whether content shows the resumed Claude
// session's own composer: a ❯ line in the live bottom of the pane, below the
// last exit banner, that is not the typed launch line — matched by
// `AGENT_ROLE=` as well as `agent launch`, since a ❯-drawn shell prompt that
// wraps the line keeps only its head on the ❯ row. It anchors on
// neither the launch text nor the banner surviving — Claude may redraw the
// screen over both, and a narrow pane wraps the launch line — only on what the
// new session drew last: the dead session's composer sits above the banner
// (SinceLastAgentExit), an older one in scrollback above the live tail, and a
// session that has exited again leaves a fresh banner with no composer below.
func resumedSessionReady(content string) bool {
	for _, line := range strings.Split(paneLiveTail(SinceLastAgentExit(content)), "\n") {
		trimmed := strings.TrimSpace(line)
		composer := trimmed == idlePromptChar || strings.HasPrefix(trimmed, idlePromptChar+" ")
		if composer && !strings.Contains(trimmed, "agent launch") && !strings.Contains(trimmed, "AGENT_ROLE=") {
			return true
		}
	}
	return false
}

// resumePendingWorkers lists the running workers among taskIDs awaiting
// resume verification.
func resumePendingWorkers(session, taskIDs string) []SpawnEntry {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return nil
	}
	ids := map[string]bool{}
	for _, id := range strings.Split(taskIDs, ",") {
		ids[id] = true
	}
	var pending []SpawnEntry
	for _, e := range entries {
		if ids[e.SpawnRole] && e.Status == "running" && e.ResumeID != "" {
			pending = append(pending, e)
		}
	}
	return pending
}

// reseedResumedWorker clears the pending mark, then hands the worker its task
// under the resume preamble. Clearing first means a reseed failure fails the
// node rather than retrying into a duplicate task.
func reseedResumedWorker(session string, e SpawnEntry) error {
	if err := UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) {
		s.ResumeID, s.ResumedAt, s.ReadyAt = "", 0, 0
	}); err != nil {
		return err
	}
	_, err := ReseedSpawn(session, e, spawnResumePreamble(e.ResumeID)+e.Task)
	return err
}

// failDeadWorker stops the worker, then fails the node naming it and why it
// was not resumed. Stop comes first and the failure is persisted on the entry
// (StopPending) before it: a node finished ahead of a stop that fails would
// leave the worker — possibly running without its definition — with no tick
// left to retry the stop, and reusable by a retry. While the stop is
// unconfirmed the node stays running and retryPendingStops owns it.
func failDeadWorker(session string, run *GraphRun, n *Node, e SpawnEntry, reason string) bool {
	clearDeadWorkerSince(e.SpawnRole)
	LogLifecycle(session, "error", "daemon", "graph-spawn-dead",
		fmt.Sprintf("%s: %s worker %s exited before answering and was not resumed: %s", run.ID, n.ID, e.SpawnRole, reason))
	output := fmt.Sprintf("worker %s exited before answering and could not be resumed: %s", e.SpawnRole, reason)
	if err := UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) { s.StopPending = output }); err != nil {
		LogLifecycle(session, "error", "daemon", "graph-spawn-stop-failed",
			fmt.Sprintf("%s: recording the pending stop: %v", e.SpawnRole, err))
	}
	if _, err := stopSpawnRole(session, e.SpawnRole); err != nil {
		LogLifecycle(session, "error", "daemon", "graph-spawn-stop-failed", fmt.Sprintf(
			"%s: %s worker %s could not be stopped and may still be running: %v — node held, stop retried every tick",
			run.ID, n.ID, e.SpawnRole, err))
		return true
	}
	_ = UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) { s.StopPending = "" })
	finishNode(session, run, n, OutcomeFailure, output)
	return true
}

// retryPendingStops retries the stop of every worker of the node whose stop
// failed (StopPending), and reports whether the node is held on one. A
// confirmed stop finishes the node with the failure recorded at the time;
// until then the node stays running, so nothing harvests or reuses the
// worker. harvestRunningNode calls it first for spawn and map nodes — ahead
// of the node timeout, replaceLostWorkers and the auto-resume opt-out: a stop
// already owed is owed whatever else the tick would do. Registry status is
// ignored: the daemon's checkSpawns refresh runs before the graph tick and
// marks a worker whose window vanished "completed", and a manual stop marks
// it "stopped", both with the stop still owed. stopSpawnRole confirms either
// (window gone, or killed), so the saved failure is never lost to a
// replacement or a timeout. The mark is cleared once the stop is confirmed.
func retryPendingStops(session string, run *GraphRun, n *Node, taskIDs string) bool {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return false
	}
	ids := map[string]bool{}
	for _, id := range strings.Split(taskIDs, ",") {
		ids[id] = true
	}
	for _, e := range entries {
		if !ids[e.SpawnRole] || e.StopPending == "" {
			continue
		}
		if _, err := stopSpawnRole(session, e.SpawnRole); err != nil {
			return true
		}
		if err := UpdateSpawnEntry(session, e.ID, func(s *SpawnEntry) { s.StopPending = "" }); err != nil {
			LogLifecycle(session, "warn", "daemon", "graph-spawn-stopped",
				fmt.Sprintf("%s: clearing the pending stop: %v", e.SpawnRole, err))
		}
		LogLifecycle(session, "info", "daemon", "graph-spawn-stopped",
			fmt.Sprintf("%s: %s worker %s stopped on retry (status was %s)", run.ID, n.ID, e.SpawnRole, e.Status))
		finishNode(session, run, n, OutcomeFailure, e.StopPending)
		return true
	}
	return false
}

// launchResumedWorker types the worker's own launch line plus `--resume id`
// into its agent pane — same AGENT_ROLE, role, launcher and cwd, so the
// definition and tools are the ones it was spawned with. It delivers no task:
// the reseed that wakes the agent and moves SeedMsgID waits on verification
// (verifyResumedWorkers).
func launchResumedWorker(session string, e SpawnEntry, id string) error {
	launcher, err := findMuxcodeBinary()
	if err != nil {
		return fmt.Errorf("finding muxcode binary: %v", err)
	}
	target := PaneTargetForWindow(session, e.Window, PaneTagAgent)
	if err := sendKeysThenEnter(target, spawnLaunchCommand(e.Worktree, e.SpawnRole, launcher, e.Role)+" --resume "+id); err != nil {
		return fmt.Errorf("relaunching %s: %v", e.SpawnRole, err)
	}
	return nil
}

// spawnResumePreamble heads the reseeded task of a resumed worker.
func spawnResumePreamble(id string) string {
	return fmt.Sprintf("[resumed] Your agent process exited before you answered this task and was resumed into its conversation (session %s). Check what you already did, finish the task, and reply to THIS message.\n\n", id)
}

func markDeadWorkerSince(role string, now int64) int64 {
	deadWorkerMu.Lock()
	defer deadWorkerMu.Unlock()
	if first, ok := deadWorkerSince[role]; ok {
		return first
	}
	deadWorkerSince[role] = now
	return now
}

func clearDeadWorkerSince(roles ...string) {
	deadWorkerMu.Lock()
	defer deadWorkerMu.Unlock()
	for _, r := range roles {
		delete(deadWorkerSince, r)
	}
}
