package bus

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Spawn-output harvest — MUX-131 Defect A.
//
// A spawn worker runs in an isolated git worktree, and nothing used to
// land that worktree's output for the run: every downstream node
// operated on a tree without the work, and test evidence produced inside
// the stale worktree read as confidently green (the A2 failure mode).
// The executor harvests at iteration completion, exactly where
// spawnGroupOutcome derives success, so a stranded-output run fails at
// the spawn node itself — before build, not at commit after a human gate.
//
// Landing model (spec mechanism 5, AMENDED 2026-09-01 on authority
// grounds): the work lands UNCOMMITTED. The daemon never creates a
// commit — CheckCommitAuthority is called only from the message paths,
// so daemon-side `git commit`/`merge`/branch-ref advance would run where
// the check does not exist, the exact laundering shape the authority
// model prevents. Instead the worktree's diff is applied as a patch into
// the checkout's working tree; build, test, review and the guards all
// read the working tree, so Defect A is still fixed, and the gated
// `commit` node remains the only thing that ever creates a commit.
// `git apply` is all-or-nothing: a patch that does not fit fails without
// touching anything, and its error names the paths — that is the
// conflict semantics.
//
// Uncommitted landing restores the clobber hazard the withdrawn wording
// avoided, so the apply is fenced by an explicit guard: when the
// checkout already has local state in any affected path (a human
// mid-edit, or an earlier map member's port), the node fails naming
// those paths and nothing is applied. After a successful port the
// worktree stays DIRTY — until the gated commit ships the phase, the
// checkout's uncommitted state is otherwise the sole copy of the work,
// and a `git checkout -- .` there would lose the iteration; the dirty
// worktree is the durability backup. The worktree advances to the
// branch tip at RESEED time (see advanceSpawnWorktree), not after a
// port: a port moves no ref, so there is nothing to advance to until
// the gated commit ships the phase — and advancing a dirty worktree is
// allowed exactly when its content equals the tip, the proof the commit
// landed it.

// errPortTransient marks a tick where the session repo dir could not be
// resolved: the node stays running and the harvest retries next tick,
// mirroring the guards' transient-repo-dir postpone.
var errPortTransient = errors.New("harvest: session repo dir unresolvable this tick")

// portSpawnGroup lands each member worktree's output into the checkout,
// sequentially in completion order. An earlier member's uncommitted port
// makes the checkout dirty in its paths, so a later member touching the
// same paths is refused by the clobber guard naming them — parallel map
// collisions surface as an explicit failure instead of racing a shared
// tree. Members without a worktree (shared-CWD fallback) or whose
// worktree is gone (clean at reap — dirty trees are always preserved)
// have nothing to port; the repo dir is resolved only when a member
// actually needs landing, so worktree-less groups never depend on tmux.
// The first port failure aborts the group: earlier members' landings
// stand, later members' work stays preserved in their worktrees.
func portSpawnGroup(session, taskIDs string) (string, error) {
	entries, err := ReadSpawnEntries(session)
	if err != nil {
		return "", fmt.Errorf("reading spawn entries: %v", err)
	}
	byRole := make(map[string]SpawnEntry, len(entries))
	for _, e := range entries {
		byRole[e.SpawnRole] = e
	}
	var members []SpawnEntry
	for _, id := range strings.Split(taskIDs, ",") {
		if e, ok := byRole[id]; ok {
			members = append(members, e)
		}
	}
	// Persistent workers are still "running" (FinishedAt 0) — they land
	// last, stable by dispatch order.
	sort.SliceStable(members, func(i, j int) bool {
		fi, fj := members[i].FinishedAt, members[j].FinishedAt
		const open = int64(1) << 62
		if fi == 0 {
			fi = open
		}
		if fj == 0 {
			fj = open
		}
		return fi < fj
	})

	repoDir := ""
	var landed []string
	for _, e := range members {
		if e.Worktree == "" {
			continue
		}
		if _, statErr := os.Stat(e.Worktree); statErr != nil {
			continue
		}
		if repoDir == "" {
			if repoDir = SessionRepoDir(session); repoDir == "" {
				return "", errPortTransient
			}
		}
		ported, perr := portSpawnWorktree(session, repoDir, e)
		if perr != nil {
			return "", perr
		}
		if ported {
			landed = append(landed, e.SpawnRole)
		}
	}
	if len(landed) == 0 {
		return "nothing to port", nil
	}
	return "ported " + strings.Join(landed, ", "), nil
}

// portSpawnWorktree lands one worker's uncommitted worktree changes into
// the checkout working tree, without creating a commit — see the file
// doc comment for the model. An empty diff is a successful no-op
// (verify-only iteration — "nothing to port" must never become a
// failure); a vacuous staging (dirty status, empty content diff) resets
// only when the content also equals the branch tip. Any error is a node
// failure at the caller: a spawn that produced changes but failed to
// port them must not report success. Ported or failed, the worktree
// keeps its dirty state — the durability copy the file doc comment
// describes, and what the reap path's dirty-preservation guard protects.
func portSpawnWorktree(session, repoDir string, e SpawnEntry) (bool, error) {
	status, err := portGit(e.Worktree, "status", "--porcelain")
	if err != nil {
		return false, fmt.Errorf("%s: cannot verify worktree state: %v: %s", e.SpawnRole, err, clampPortDetail(status))
	}
	if status == "" {
		return false, nil
	}

	// Staging touches only the worktree's own index — it is how untracked
	// files enter the diff, not a step toward a commit.
	if out, aerr := portGit(e.Worktree, "add", "-A"); aerr != nil {
		return false, fmt.Errorf("%s: staging worktree changes failed: %v: %s", e.SpawnRole, aerr, clampPortDetail(out))
	}
	patch, err := portGitRaw(e.Worktree, "diff", "--cached", "--binary")
	if err != nil {
		return false, fmt.Errorf("%s: cannot diff worktree: %v", e.SpawnRole, err)
	}
	if len(bytes.TrimSpace(patch)) == 0 {
		if tip, terr := portGit(repoDir, "rev-parse", "HEAD"); terr == nil && worktreeContentEqualsTip(e.Worktree, tip) {
			resetPortWorktree(session, e, tip)
		}
		return false, nil
	}
	affectedOut, err := portGit(e.Worktree, "diff", "--cached", "--name-only")
	if err != nil {
		return false, fmt.Errorf("%s: cannot list affected paths: %v", e.SpawnRole, err)
	}
	affected := strings.Split(affectedOut, "\n")

	blocked, err := checkoutDirtyIn(repoDir, affected)
	if err != nil {
		return false, fmt.Errorf("%s: %v", e.SpawnRole, err)
	}
	if len(blocked) > 0 {
		return false, fmt.Errorf("%s: port refused — checkout has local changes in affected paths: %s",
			e.SpawnRole, strings.Join(blocked, ", "))
	}

	tmp, terr := os.CreateTemp("", "muxcode-harvest-*.patch")
	if terr != nil {
		return false, fmt.Errorf("%s: cannot stage patch: %v", e.SpawnRole, terr)
	}
	defer os.Remove(tmp.Name())
	if _, werr := tmp.Write(patch); werr != nil {
		tmp.Close()
		return false, fmt.Errorf("%s: cannot write patch: %v", e.SpawnRole, werr)
	}
	tmp.Close()

	if out, perr := portGit(repoDir, "apply", "--whitespace=nowarn", tmp.Name()); perr != nil {
		return false, fmt.Errorf("%s: port conflict — patch does not apply to the checkout: %s",
			e.SpawnRole, clampPortDetail(out))
	}
	// The worktree deliberately stays dirty — see the file doc comment.
	return true, nil
}

// worktreeContentEqualsTip reports whether the worktree's staged content
// is byte-identical to the branch tip — the proof that the gated commit
// has landed everything the worktree holds, so resetting to the tip
// loses nothing. Callers must have staged the worktree (`add -A`) first.
// Any error reads as "not equal": unknown must never authorize a reset.
func worktreeContentEqualsTip(worktree, tip string) bool {
	if tip == "" {
		return false
	}
	diff, err := portGitRaw(worktree, "diff", "--cached", tip)
	return err == nil && len(bytes.TrimSpace(diff)) == 0
}

// resetPortWorktree hard-resets a worktree to the branch tip. Only
// reachable behind worktreeContentEqualsTip, so it can never destroy
// unshipped work. A failed reset is logged, not fatal — the worktree
// merely stays dirty until the next pass.
func resetPortWorktree(session string, e SpawnEntry, tip string) {
	if out, err := portGit(e.Worktree, "reset", "--hard", tip); err != nil {
		LogLifecycle(session, "warn", "daemon", "graph-harvest-sync-failed",
			fmt.Sprintf("%s: worktree reset to %s failed: %v: %s", e.SpawnRole, tip, err, clampPortDetail(out)))
	}
}

// checkoutDirtyIn returns the affected paths the checkout's working tree
// already has local state for — modifications, staged changes, or
// untracked files. Applying over any of them would overwrite a human's
// in-progress edit, so the caller fails the node naming them instead of
// applying.
func checkoutDirtyIn(repoDir string, affected []string) ([]string, error) {
	raw, err := portGitRaw(repoDir, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("cannot read checkout state: %v", err)
	}
	var dirty []string
	for _, line := range strings.Split(string(raw), "\n") {
		if len(line) < 4 {
			continue
		}
		for _, p := range strings.Split(line[3:], " -> ") {
			if p = strings.TrimSpace(p); p != "" {
				dirty = append(dirty, p)
			}
		}
	}
	var blocked []string
	for _, a := range affected {
		if a == "" {
			continue
		}
		for _, d := range dirty {
			if a == d || (strings.HasSuffix(d, "/") && strings.HasPrefix(a, d)) {
				blocked = append(blocked, a)
				break
			}
		}
	}
	return blocked, nil
}

// advanceSpawnWorktree moves a persistent worktree to the current branch
// tip at reseed time, so a reused worker starts each iteration from what
// the branch now holds — the gated commit node may have shipped the
// previous phase between iterations, and a port moves no ref, so reseed
// is the first moment there can be anything to advance to. A clean
// worktree advances outright. A DIRTY worktree — the normal state after
// a port, since the ported copy is kept as the durability backup —
// advances only under tip containment: staged content byte-identical to
// the tip proves the commit landed it, so the reset loses nothing.
// Anything else refuses conservatively: unshipped work outlives a failed
// iteration, and every other failure degrades to starting from the old
// base — the reseeded task still runs, it just sees an older tree.
func advanceSpawnWorktree(session string, e SpawnEntry) {
	if e.Worktree == "" {
		return
	}
	if _, err := os.Stat(e.Worktree); err != nil {
		return
	}
	repoDir := SessionRepoDir(session)
	if repoDir == "" {
		return
	}
	tip, err := portGit(repoDir, "rev-parse", "HEAD")
	if err != nil || tip == "" {
		return
	}
	status, err := portGit(e.Worktree, "status", "--porcelain")
	if err != nil {
		return
	}
	if status == "" {
		if base, berr := portGit(e.Worktree, "rev-parse", "HEAD"); berr != nil || base == tip {
			return
		}
	} else {
		if _, aerr := portGit(e.Worktree, "add", "-A"); aerr != nil {
			return
		}
		if !worktreeContentEqualsTip(e.Worktree, tip) {
			return // conservative refusal — unshipped content stays put
		}
	}
	if out, aerr := portGit(e.Worktree, "reset", "--hard", tip); aerr != nil {
		LogLifecycle(session, "warn", "daemon", "graph-worktree-advance-failed",
			fmt.Sprintf("%s: advance to %s failed: %v: %s", e.SpawnRole, tip, aerr, clampPortDetail(out)))
	}
}

// portGit runs one git command for the harvest, returning trimmed
// combined output — apply refusals arrive on stderr, and the paths they
// name are the node-failure detail the spec requires.
func portGit(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// portGitRaw runs one git command returning raw stdout: patch text must
// keep its trailing newline for git apply, and porcelain status lines
// must keep their leading column bytes for parsing.
func portGitRaw(dir string, args ...string) ([]byte, error) {
	return exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
}

// clampPortDetail bounds git output embedded in node failure detail so
// the persisted node status stays readable.
func clampPortDetail(s string) string {
	const limit = 1200
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "… (truncated)"
}
