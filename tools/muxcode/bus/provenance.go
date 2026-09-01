package bus

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Changed-files provenance and the verify-movement gate (MUX-007 Phase 3).
//
// Verify-spec requests used to forward raw file-write paths — live fires
// named /tmp handoffs, spawn-worktree paths, and once the user's
// credentials file. Two controls replace the receiving agent's judgement:
// RepoScopedFiles proves containment before a path is presented, and
// VerifyMovementFingerprint separates echoes from progress by state
// movement (non-docs tree + graph run/node states) rather than filename
// shape.

// RepoScopedFiles filters raw changed-file paths down to the ones provably
// inside repoDir and returns them repo-relative, deduped, in input order.
//
// Containment keys on location, not name: an absolute path is accepted only
// when it resolves under repoDir, so a spawn-worktree copy of a file the
// repo also contains is rejected while the repo's own copy passes (census
// finding B1 — a verifier must not check off a phase against code absent
// from the branch). Relative paths are treated as repo-relative candidates
// and rejected if they escape the root after cleaning. With no resolvable
// repoDir nothing can be proven, so nothing is presented.
func RepoScopedFiles(repoDir string, files []string) []string {
	if strings.TrimSpace(repoDir) == "" {
		return nil
	}
	root := filepath.Clean(repoDir)
	seen := make(map[string]bool, len(files))
	var scoped []string
	for _, f := range files {
		p := strings.TrimSpace(f)
		if p == "" {
			continue
		}
		rel, ok := repoContainedRel(root, p)
		if !ok {
			continue
		}
		if !seen[rel] {
			seen[rel] = true
			scoped = append(scoped, rel)
		}
	}
	return scoped
}

// repoContainedRel reports the repo-relative form of p (absolute, or
// relative meaning repo-relative) when it provably resolves inside root
// AFTER symlink resolution. A lexical check alone is not containment: a
// path that traverses a symlink pointing outside the repo cleans as
// contained while its content is external (review must-fix, 2026-09-01
// — the boundary this package exists to enforce was itself bypassable).
// Non-existent paths (deleted files, possibly with their whole
// directory) resolve via the longest EXISTING ancestor: only components
// that exist can traverse a symlink, so a resolved-contained ancestor
// plus a non-escaping remainder proves containment of the full path —
// the walk terminates because the filesystem root always resolves. Any
// resolution failure reads as not contained: the boundary never passes
// what it cannot prove.
func repoContainedRel(root, p string) (string, bool) {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", false
	}
	abs := p
	if !filepath.IsAbs(p) {
		abs = filepath.Join(root, p)
	}
	abs = filepath.Clean(abs)
	// Longest-existing-ancestor walk — see doc comment.
	prefix, remainder := abs, ""
	var resolved string
	for {
		if r, rerr := filepath.EvalSymlinks(prefix); rerr == nil {
			resolved = r
			break
		}
		parent := filepath.Dir(prefix)
		if parent == prefix {
			return "", false
		}
		remainder = filepath.Join(filepath.Base(prefix), remainder)
		prefix = parent
	}
	rel, err := filepath.Rel(resolvedRoot, filepath.Join(resolved, remainder))
	if err != nil {
		return "", false
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return rel, true
}

// VerifyMovementFingerprint hashes the state a verify-spec fire is evidence
// of: the repo's non-docs working tree (HEAD plus each dirty path's size and
// mtime) and every graph run's state including node states. Two fires with
// the same fingerprint mean nothing moved between them — the echo shape.
//
// docs/ paths are excluded because the verifier's own spec edit is the one
// write every census echo carried; a fingerprint that counted it would
// re-arm the self-feeding loop it exists to break. Graph run state is
// included because the census's one genuine doc-only fire (fire 11) moved
// only there — a working-tree fingerprint alone would have suppressed it.
// Returns "" when neither source yields evidence: suppression needs positive
// proof that nothing moved, so no evidence must read as movement, never as
// stasis.
func VerifyMovementFingerprint(session, repoDir string) string {
	var parts []string
	if strings.TrimSpace(repoDir) != "" {
		if head := gitOutputIn(repoDir, "rev-parse", "HEAD"); head != "" {
			parts = append(parts, "head:"+head)
			for _, line := range gitStatusLines(repoDir) {
				if len(line) < 4 {
					continue
				}
				path := strings.TrimSpace(line[3:])
				if i := strings.Index(path, " -> "); i >= 0 {
					path = path[i+4:]
				}
				path = strings.Trim(path, `"`)
				if strings.HasPrefix(path, "docs/") {
					continue
				}
				if info, err := os.Stat(filepath.Join(repoDir, path)); err == nil {
					parts = append(parts, fmt.Sprintf("dirty:%s:%d:%d", path, info.Size(), info.ModTime().UnixNano()))
				} else {
					parts = append(parts, "dirty:"+path+":gone")
				}
			}
		}
	}
	runs, _ := ListGraphRuns(session)
	for _, r := range runs {
		parts = append(parts, "run:"+r.ID+":"+r.State)
		statuses, err := ReadAllNodeStatuses(session, r.ID)
		if err != nil {
			continue
		}
		ids := make([]string, 0, len(statuses))
		for id := range statuses {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			parts = append(parts, "node:"+r.ID+":"+id+":"+statuses[id].State+":"+statuses[id].Outcome)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:])
}

// gitStatusLines returns the raw porcelain status lines for repoDir.
// gitOutputIn is not usable here: it trims the whole output, which eats the
// leading space of an unstaged-modification line (" M path") and shifts the
// path column — the docs exclusion then never matches the first entry.
func gitStatusLines(repoDir string) []string {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		return nil
	}
	return strings.Split(strings.TrimRight(string(out), "\n"), "\n")
}

// VerifyMovementMarkerPath returns the file recording the movement
// fingerprint captured by the last verify-spec fire.
func VerifyMovementMarkerPath(session string) string {
	return filepath.Join(BusDir(session), "verify-movement.last")
}

// ReadVerifyMovementMarker returns the fingerprint recorded at the last
// verify-spec fire, or "" if none has fired with evidence yet.
func ReadVerifyMovementMarker(session string) string {
	data, err := os.ReadFile(VerifyMovementMarkerPath(session))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteVerifyMovementMarker records fp as the state observed by the fire
// that just happened. Atomic for the same reason as WriteReviewedMarker,
// but with the opposite error contract: a failed write here must NOT
// withhold the fire — losing the fingerprint costs one redundant
// verify-spec later, while withholding drops a genuine verification.
func WriteVerifyMovementMarker(session, fp string) error {
	return atomicWriteFile(VerifyMovementMarkerPath(session), []byte(fp+"\n"))
}
