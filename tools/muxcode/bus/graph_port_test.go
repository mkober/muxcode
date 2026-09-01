package bus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- MUX-131 Defect A: spawn-output harvest (uncommitted landing) ---

// initPortRepo creates a hermetic git repo (no global/system config, no
// template hooks) with one committed file, checked out on its default
// branch — the stand-in for the session's main checkout.
func initPortRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	repo := t.TempDir()
	if err := exec.Command("git", "init", "-q", repo).Run(); err != nil {
		t.Skipf("git init unavailable: %v", err)
	}
	portRepoGit(t, repo, "config", "user.email", "muxcode-test@localhost")
	portRepoGit(t, repo, "config", "user.name", "muxcode test")
	portRepoGit(t, repo, "config", "commit.gpgsign", "false")
	writePortFile(t, repo, "base.txt", "base\n")
	portRepoGit(t, repo, "add", "-A")
	portRepoGit(t, repo, "commit", "-q", "-m", "base")
	return repo
}

func portRepoGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := portGit(dir, args...)
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return out
}

func writePortFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// addPortWorktree creates a detached worktree at repo HEAD — the same
// shape createSpawnWorktree produces.
func addPortWorktree(t *testing.T, repo string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), "wt")
	portRepoGit(t, repo, "worktree", "add", "--detach", wt, "HEAD")
	return wt
}

func portEntry(role, wt string) SpawnEntry {
	return SpawnEntry{ID: role, SpawnRole: role, Window: role, Owner: "daemon",
		Status: "running", Worktree: wt, RunID: "run-pt", NodeID: "implement"}
}

// commitOnBranch is the tests' stand-in for the gated commit node — the
// only legitimate commit creator.
func commitOnBranch(t *testing.T, repo, msg string) string {
	t.Helper()
	portRepoGit(t, repo, "add", "-A")
	portRepoGit(t, repo, "commit", "-q", "-m", msg)
	return portRepoGit(t, repo, "rev-parse", "HEAD")
}

func TestPortSpawnWorktreeLandsChangesUncommitted(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	base := portRepoGit(t, wt, "rev-parse", "HEAD")
	headBefore := portRepoGit(t, repo, "rev-parse", "HEAD")
	writePortFile(t, wt, "feature.go", "package x\n")
	writePortFile(t, wt, "base.txt", "changed\n")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil {
		t.Fatalf("port: %v", err)
	}
	if !ported {
		t.Fatal("dirty worktree must report ported")
	}
	// The checkout's WORKING TREE carries the work — that is what
	// downstream build/test actually read.
	if got, rerr := os.ReadFile(filepath.Join(repo, "feature.go")); rerr != nil || string(got) != "package x\n" {
		t.Fatalf("new file missing from checkout: %v %q", rerr, got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "base.txt")); string(got) != "changed\n" {
		t.Fatalf("modified file not landed: %q", got)
	}
	// The daemon never creates a commit: HEAD identical, work uncommitted.
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("port must not move HEAD: %s vs %s", head, headBefore)
	}
	st := portRepoGit(t, repo, "status", "--porcelain")
	if !strings.Contains(st, "base.txt") || !strings.Contains(st, "feature.go") {
		t.Fatalf("landed work must be uncommitted in the checkout, status %q", st)
	}
	// The worktree keeps the changes — the durability copy until the
	// gated commit ships them — and is NOT advanced (reseed's job).
	if s := portRepoGit(t, wt, "status", "--porcelain"); s == "" {
		t.Fatal("worktree must stay dirty after the land — it is the durability copy")
	}
	if got, _ := os.ReadFile(filepath.Join(wt, "feature.go")); string(got) != "package x\n" {
		t.Fatalf("worktree copy of the work must survive the port, got %q", got)
	}
	if head := portRepoGit(t, wt, "rev-parse", "HEAD"); head != base {
		t.Fatalf("worktree HEAD must stay at its base after a port: %s vs %s", head, base)
	}
}

// TestPortSpawnFixLoopSecondHarvestSucceedsOverOwnPort is the Phase 3
// fix-loop control: iteration 1 ports, the loop re-enters without a
// commit, the fix pass reworks the same file AND reverts a file it had
// added — and the second harvest succeeds over the run's own port
// instead of being clobber-refused. The port record's blob hashes prove
// the checkout state is ours; the previous patch is reversed and the
// new one applied, so the reverted file disappears rather than
// lingering from iteration 1.
func TestPortSpawnFixLoopSecondHarvestSucceedsOverOwnPort(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	headBefore := portRepoGit(t, repo, "rev-parse", "HEAD")
	writePortFile(t, wt, "base.txt", "spawn version\n")
	writePortFile(t, wt, "extra.go", "package extra\n")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || !ported {
		t.Fatalf("first port must land: ported=%v err=%v", ported, err)
	}

	// Fix-loop pass: rework base.txt, revert the extra file, no commit.
	writePortFile(t, wt, "base.txt", "spawn version 2\n")
	if rerr := os.Remove(filepath.Join(wt, "extra.go")); rerr != nil {
		t.Fatal(rerr)
	}
	ported, err = portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || !ported {
		t.Fatalf("second harvest must succeed over the run's own port: ported=%v err=%v", ported, err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "base.txt")); string(got) != "spawn version 2\n" {
		t.Fatalf("fix pass content must replace the earlier port, got %q", got)
	}
	if _, serr := os.Stat(filepath.Join(repo, "extra.go")); !os.IsNotExist(serr) {
		t.Fatalf("file the fix pass reverted must not linger from iteration 1 (stat err: %v)", serr)
	}
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("re-port must not move HEAD: %s vs %s", head, headBefore)
	}
	if s := portRepoGit(t, wt, "status", "--porcelain"); s == "" {
		t.Fatal("worktree must stay dirty after the re-port — it is the durability copy")
	}
}

// TestPortSelfPortGuardRefusesTamperedPort keeps the lifted limitation
// from going inert: a ported file a human has since edited no longer
// hashes to the port record, so the re-port still refuses naming it —
// the record authorizes overwriting OUR content, never theirs.
func TestPortSelfPortGuardRefusesTamperedPort(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	writePortFile(t, wt, "base.txt", "spawn version\n")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || !ported {
		t.Fatalf("first port must land: ported=%v err=%v", ported, err)
	}

	// A human edits the ported file before the fix pass re-ports.
	writePortFile(t, repo, "base.txt", "human tampered\n")
	writePortFile(t, wt, "base.txt", "spawn version 2\n")
	ported, err = portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err == nil || ported {
		t.Fatalf("tampered port must refuse, got ported=%v err=%v", ported, err)
	}
	if !strings.Contains(err.Error(), "port refused") || !strings.Contains(err.Error(), "base.txt") {
		t.Fatalf("refusal must name the path, got: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "base.txt")); string(got) != "human tampered\n" {
		t.Fatalf("the human's edit must never be clobbered, got %q", got)
	}
}

// TestPortDurabilityWorkHeldInTwoPlaces is the Phase 3 durability
// control: between a successful port and the gated commit the work
// exists in BOTH the checkout working tree and the worktree — the
// worktree copy must not be discarded, or the checkout's uncommitted
// state becomes the sole copy of the iteration.
func TestPortDurabilityWorkHeldInTwoPlaces(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	writePortFile(t, wt, "feature.go", "package x\n")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || !ported {
		t.Fatalf("port must land: ported=%v err=%v", ported, err)
	}
	for _, place := range []string{repo, wt} {
		if got, rerr := os.ReadFile(filepath.Join(place, "feature.go")); rerr != nil || string(got) != "package x\n" {
			t.Fatalf("work missing from %s: %v %q", place, rerr, got)
		}
	}
	if s := portRepoGit(t, wt, "status", "--porcelain"); s == "" {
		t.Fatal("worktree copy discarded — durability requires the second copy until the commit ships")
	}
}

func TestPortSpawnWorktreeBranchMovedCleanApply(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	writePortFile(t, wt, "feature.go", "package x\n")
	// Branch moves on a non-overlapping file while the spawn ran — not a
	// conflict, the patch still applies.
	writePortFile(t, repo, "other.txt", "other\n")
	tip := commitOnBranch(t, repo, "branch moved")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || !ported {
		t.Fatalf("non-overlapping branch move must land cleanly, got ported=%v err=%v", ported, err)
	}
	if got, rerr := os.ReadFile(filepath.Join(repo, "feature.go")); rerr != nil || string(got) != "package x\n" {
		t.Fatalf("harvested file missing: %v %q", rerr, got)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "other.txt")); string(got) != "other\n" {
		t.Fatalf("branch's own change lost: %q", got)
	}
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("port must not move HEAD: %s vs %s", head, tip)
	}
}

func TestPortSpawnWorktreeConflictFailsNamingPaths(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	writePortFile(t, wt, "base.txt", "spawn version\n")
	writePortFile(t, repo, "base.txt", "branch version\n")
	movedTip := commitOnBranch(t, repo, "conflicting branch move")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err == nil || ported {
		t.Fatalf("overlapping branch move must fail the port, got ported=%v err=%v", ported, err)
	}
	if !strings.Contains(err.Error(), "base.txt") {
		t.Fatalf("conflict must name the conflicting path, got: %v", err)
	}
	// Neither side auto-resolved: branch untouched, checkout clean,
	// HEAD unmoved.
	if got, _ := os.ReadFile(filepath.Join(repo, "base.txt")); string(got) != "branch version\n" {
		t.Fatalf("branch side must be untouched, got %q", got)
	}
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != movedTip {
		t.Fatalf("failed port must not move HEAD: %s vs %s", head, movedTip)
	}
	if st := portRepoGit(t, repo, "status", "--porcelain"); st != "" {
		t.Fatalf("checkout must be untouched after a failed apply, got %q", st)
	}
	// Worktree intact and dirty, so the preservation guard keeps it.
	if got, _ := os.ReadFile(filepath.Join(wt, "base.txt")); string(got) != "spawn version\n" {
		t.Fatalf("worktree side must be preserved, got %q", got)
	}
	if st := portRepoGit(t, wt, "status", "--porcelain"); st == "" {
		t.Fatal("worktree must stay dirty so the preservation guard keeps it")
	}
}

func TestPortSpawnWorktreeDirtyCheckoutRefusalNamesPaths(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	headBefore := portRepoGit(t, repo, "rev-parse", "HEAD")
	writePortFile(t, wt, "base.txt", "spawn version\n")
	// A human mid-edit in the checkout, uncommitted, on the same file.
	writePortFile(t, repo, "base.txt", "human edit\n")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err == nil || ported {
		t.Fatalf("dirty affected path must refuse the port, got ported=%v err=%v", ported, err)
	}
	if !strings.Contains(err.Error(), "port refused") || !strings.Contains(err.Error(), "base.txt") {
		t.Fatalf("refusal must name the path, got: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(repo, "base.txt")); string(got) != "human edit\n" {
		t.Fatalf("human edit must never be clobbered, got %q", got)
	}
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("refused port must not move HEAD: %s vs %s", head, headBefore)
	}
	if st := portRepoGit(t, wt, "status", "--porcelain"); st == "" {
		t.Fatal("worktree must stay dirty so the preservation guard keeps it")
	}
}

func TestPortSpawnWorktreeNoOpSucceedsWithoutAdvance(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	base := portRepoGit(t, wt, "rev-parse", "HEAD")
	// Verify-only iteration: nothing produced, though the branch moved.
	writePortFile(t, repo, "other.txt", "other\n")
	tip := commitOnBranch(t, repo, "branch moved")

	ported, err := portSpawnWorktree("port-test", repo, portEntry("spawn-pt", wt))
	if err != nil || ported {
		t.Fatalf("empty diff must be a successful no-op, got ported=%v err=%v", ported, err)
	}
	// The port never advances the worktree — that is reseed's job.
	if head := portRepoGit(t, wt, "rev-parse", "HEAD"); head != base || head == tip {
		t.Fatalf("no-op port must leave the worktree at its base: %s (base %s, tip %s)", head, base, tip)
	}
}

// TestPortSpawnGroupLandsMembersSequentially pins the map fan-out
// mechanics: members land one at a time in completion order, uncommitted,
// with no ref movement between or after members.
func TestPortSpawnGroupLandsMembersSequentially(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wtA := addPortWorktree(t, repo)
	wtB := addPortWorktree(t, repo)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	headBefore := portRepoGit(t, repo, "rev-parse", "HEAD")
	writePortFile(t, wtA, "member-a.go", "package a\n")
	writePortFile(t, wtB, "member-b.go", "package b\n")

	a := portEntry("spawn-pa", wtA)
	a.FinishedAt = 200
	b := portEntry("spawn-pb", wtB)
	b.FinishedAt = 100
	if err := os.MkdirAll(filepath.Dir(SpawnPath(runTestSession)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{a, b}); err != nil {
		t.Fatal(err)
	}

	summary, err := portSpawnGroup(runTestSession, "spawn-pa,spawn-pb")
	if err != nil {
		t.Fatalf("group port: %v", err)
	}
	// b finished first, so it lands first.
	if summary != "ported spawn-pb, spawn-pa" {
		t.Fatalf("summary must reflect completion order, got %q", summary)
	}
	for _, f := range []string{"member-a.go", "member-b.go"} {
		if _, err := os.Stat(filepath.Join(repo, f)); err != nil {
			t.Fatalf("member output %s missing from checkout: %v", f, err)
		}
	}
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("group port must not move HEAD: %s vs %s", head, headBefore)
	}
}

// TestReseedSpawnAdvancesWorktreeToBranchTip pins the amended advance
// site across the worktree's real lifecycle: a clean worktree advances
// outright; a dirty worktree whose content equals the tip (the gated
// commit landed the port) advances under tip containment; a dirty
// worktree holding unshipped content is refused conservatively.
func TestReseedSpawnAdvancesWorktreeToBranchTip(t *testing.T) {
	useTempBusDir(t)
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	base := portRepoGit(t, wt, "rev-parse", "HEAD")
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)
	origWake := graphSpawnWakeFn
	graphSpawnWakeFn = func(string, string) {}
	t.Cleanup(func() { graphSpawnWakeFn = origWake })

	e := portEntry("spawn-rs", wt)
	if err := os.MkdirAll(DeliveryDir(runTestSession), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(SpawnPath(runTestSession)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpawnEntries(runTestSession, []SpawnEntry{e}); err != nil {
		t.Fatal(err)
	}

	// Scenario 1: clean worktree, branch tip moved — advances outright.
	writePortFile(t, repo, "shipped.txt", "phase 1\n")
	tip := commitOnBranch(t, repo, "phase 1")
	if tip == base {
		t.Fatal("fixture: branch tip must move")
	}
	if _, err := ReseedSpawn(runTestSession, e, "implement phase 2"); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if head := portRepoGit(t, wt, "rev-parse", "HEAD"); head != tip {
		t.Fatalf("clean worktree must advance to the branch tip at reseed: %s vs %s", head, tip)
	}

	// Scenario 2: tip containment — the ported-then-committed flow. The
	// worktree is dirty with the phase's work, and the gated commit
	// shipped exactly that work, so content equals the tip and the
	// advance loses nothing.
	writePortFile(t, wt, "feature.go", "package x\n")
	writePortFile(t, repo, "feature.go", "package x\n")
	tip2 := commitOnBranch(t, repo, "phase 2")
	if _, err := ReseedSpawn(runTestSession, e, "implement phase 3"); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if head := portRepoGit(t, wt, "rev-parse", "HEAD"); head != tip2 {
		t.Fatalf("tip-contained dirty worktree must advance: %s vs %s", head, tip2)
	}
	if s := portRepoGit(t, wt, "status", "--porcelain"); s != "" {
		t.Fatalf("advanced worktree must read clean, got %q", s)
	}

	// Scenario 3: conservative refusal — dirty with unshipped content.
	writePortFile(t, wt, "unharvested.go", "package x\n")
	writePortFile(t, repo, "shipped2.txt", "phase 3\n")
	commitOnBranch(t, repo, "phase 3")
	if _, err := ReseedSpawn(runTestSession, e, "implement phase 4"); err != nil {
		t.Fatalf("reseed: %v", err)
	}
	if head := portRepoGit(t, wt, "rev-parse", "HEAD"); head != tip2 {
		t.Fatalf("unshipped dirty worktree must not be advanced: %s vs %s", head, tip2)
	}
	if _, err := os.Stat(filepath.Join(wt, "unharvested.go")); err != nil {
		t.Fatalf("unshipped work destroyed by the reseed advance: %v", err)
	}
}

// spawnPortGraph is the two-node fixture the executor harvest tests
// share: a spawn implement node feeding a build send node.
func spawnPortGraph() *Graph {
	return &Graph{Name: "spawn-port", Start: "w",
		Nodes: []Node{
			{ID: "w", Type: NodeSpawn, Role: "edit", Message: "implement"},
			{ID: "b", Type: NodeSend, Role: "build", Action: "build", Message: "build it"},
		},
		Edges: []Edge{{From: "w", To: "b"}}}
}

// attachPortWorktree points a fake worker's entry at a real worktree.
func attachPortWorktree(t *testing.T, worker, wt string) {
	t.Helper()
	if err := UpdateSpawnEntry(runTestSession, worker, func(e *SpawnEntry) { e.Worktree = wt }); err != nil {
		t.Fatal(err)
	}
}

// TestExecSpawnHarvestLandsOutputBeforeBuild pins the Defect A criterion
// end to end: the spawn node's success carries a completed harvest, so
// build is dispatched against a checkout that already holds the work —
// uncommitted, with HEAD unmoved.
func TestExecSpawnHarvestLandsOutputBeforeBuild(t *testing.T) {
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	headBefore := portRepoGit(t, repo, "rev-parse", "HEAD")
	run := createTestRun(t, spawnPortGraph())
	f := fakeLiveSpawns(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	if worker == "" || f.fresh != 1 {
		t.Fatalf("fresh worker expected: task %q, %d starts", worker, f.fresh)
	}
	attachPortWorktree(t, worker, wt)
	writePortFile(t, wt, "feature.go", "package x\n")
	answerSpawn(t, runTestSession, worker)

	step(t, runTestSession, run.ID)

	st, _ = ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeDone || st.Outcome != OutcomeSuccess {
		t.Fatalf("spawn node must complete after harvest: %q %q (%s)", st.State, st.Outcome, st.Output)
	}
	if !strings.Contains(st.Output, "ported "+worker) {
		t.Fatalf("node output must record the port, got %q", st.Output)
	}
	if got, err := os.ReadFile(filepath.Join(repo, "feature.go")); err != nil || string(got) != "package x\n" {
		t.Fatalf("checkout missing harvested work at build dispatch: %v %q", err, got)
	}
	// Uncommitted, HEAD unmoved: the gated commit node is still the only
	// commit creator.
	if head := portRepoGit(t, repo, "rev-parse", "HEAD"); head != headBefore {
		t.Fatalf("harvest must not move HEAD: %s vs %s", head, headBefore)
	}
	if s := portRepoGit(t, repo, "status", "--porcelain"); !strings.Contains(s, "feature.go") {
		t.Fatalf("harvested work must be uncommitted in the checkout, status %q", s)
	}
	if s := portRepoGit(t, wt, "status", "--porcelain"); s == "" {
		t.Fatal("worktree must stay dirty after the harvest — it is the durability copy")
	}
	if s := nodeState(t, runTestSession, run.ID, "b"); s != GraphNodeRunning {
		t.Fatalf("b state %q, want running", s)
	}
	if msgs, _ := Peek(runTestSession, "build"); len(msgs) != 1 {
		t.Fatalf("build must have been dispatched once: %+v", msgs)
	}
}

// TestExecSpawnHarvestConflictFailsNodeBeforeBuild pins the other half:
// stranded output is a spawn-node failure naming the conflicting paths —
// the guard at commit is no longer the first thing to notice, because
// build never runs at all.
func TestExecSpawnHarvestConflictFailsNodeBeforeBuild(t *testing.T) {
	repo := initPortRepo(t)
	wt := addPortWorktree(t, repo)
	run := createTestRun(t, spawnPortGraph())
	_ = fakeLiveSpawns(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", repo)

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	attachPortWorktree(t, worker, wt)
	writePortFile(t, wt, "base.txt", "spawn version\n")
	writePortFile(t, repo, "base.txt", "branch version\n")
	commitOnBranch(t, repo, "conflicting branch move")
	answerSpawn(t, runTestSession, worker)

	step(t, runTestSession, run.ID)

	st, _ = ReadNodeStatus(runTestSession, run.ID, "w")
	if st.State != GraphNodeFailed || st.Outcome != OutcomeFailure {
		t.Fatalf("stranded output must fail the spawn node, got %q %q (%s)", st.State, st.Outcome, st.Output)
	}
	if !strings.Contains(st.Output, "harvest:") || !strings.Contains(st.Output, "base.txt") {
		t.Fatalf("failure must name the conflicting path, got %q", st.Output)
	}
	if msgs, _ := Peek(runTestSession, "build"); len(msgs) != 0 {
		t.Fatalf("build must never be dispatched on a failed harvest: %+v", msgs)
	}
	if r, _ := ReadGraphRun(runTestSession, run.ID); r.State != GraphRunFailed {
		t.Fatalf("run state %q, want failed — the failure happens before build, not at commit", r.State)
	}
}

// TestExecSpawnHarvestPostponesWhenRepoDirUnknown mirrors the guards'
// transient rule: an unresolvable repo dir postpones the harvest tick,
// it neither fails the node nor lets success through unported.
func TestExecSpawnHarvestPostponesWhenRepoDirUnknown(t *testing.T) {
	run := createTestRun(t, spawnPortGraph())
	_ = fakeLiveSpawns(t)
	t.Setenv("MUXCODE_SESSION_REPO_DIR", "")

	step(t, runTestSession, run.ID)
	st, _ := ReadNodeStatus(runTestSession, run.ID, "w")
	worker := st.TaskID
	attachPortWorktree(t, worker, t.TempDir())
	answerSpawn(t, runTestSession, worker)

	step(t, runTestSession, run.ID)

	if s := nodeState(t, runTestSession, run.ID, "w"); s != GraphNodeRunning {
		t.Fatalf("w state %q, want running — an unresolvable repo dir is transient, not a verdict", s)
	}
	if msgs, _ := Peek(runTestSession, "build"); len(msgs) != 0 {
		t.Fatalf("build must not be dispatched while the harvest is postponed: %+v", msgs)
	}
}
