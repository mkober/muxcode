package bus

import (
	"os"
	"path/filepath"
	"strings"
)

// Active-spec pointer reconciliation (MUX-007 Phase 3).
//
// The drafts/ → completed/ close-out move is performed by agents (git mv),
// and the pointer used to outlive the file it named: the next close-spec
// guard failed with "cannot read active spec" and verify-spec fired at a
// nonexistent path on every review. Clearing the pointer was a documented
// manual step with no owner once the move was automated. Reconciliation
// makes the pointer follow the one move shape the workflow performs, and
// makes any other disappearance loud instead of a confusing downstream
// failure.

// SpecPointer* are the ReconcileActiveSpec outcomes.
const (
	SpecPointerUnset        = "unset"        // no active spec
	SpecPointerOK           = "ok"           // pointer names an existing file
	SpecPointerUnresolvable = "unresolvable" // relative pointer, no repo dir to resolve against
	SpecPointerRepointed    = "repointed"    // followed a drafts/ → completed/ move
	SpecPointerDangling     = "dangling"     // file gone and no completed/ counterpart
)

// SpecPointerResult reports what ReconcileActiveSpec found and did.
type SpecPointerResult struct {
	Outcome string
	Path    string // the pointer as read
	NewPath string // set when repointed
}

// ReconcileActiveSpec checks that the active-spec pointer names an existing
// file. When the file has gone, the documented close-out move is followed:
// if the spec exists at the completed/ counterpart of a drafts/ path, the
// pointer is rewritten to it. Anything else missing is reported as dangling
// for the caller to surface — never silently cleared, since the pointer is
// the only record of which spec was active.
func ReconcileActiveSpec(session, repoDir string) SpecPointerResult {
	spec := ReadActiveSpec(session)
	if spec == "" {
		return SpecPointerResult{Outcome: SpecPointerUnset}
	}
	full := ResolveSpecPath(repoDir, spec)
	if full == "" {
		return SpecPointerResult{Outcome: SpecPointerUnresolvable, Path: spec}
	}
	if _, err := os.Stat(full); err == nil {
		return SpecPointerResult{Outcome: SpecPointerOK, Path: spec}
	}
	if strings.Contains(spec, "/drafts/") {
		moved := strings.Replace(spec, "/drafts/", "/completed/", 1)
		if movedFull := ResolveSpecPath(repoDir, moved); movedFull != "" {
			if _, err := os.Stat(movedFull); err == nil {
				if err := WriteActiveSpec(session, moved); err == nil {
					return SpecPointerResult{Outcome: SpecPointerRepointed, Path: spec, NewPath: moved}
				}
			}
		}
	}
	return SpecPointerResult{Outcome: SpecPointerDangling, Path: spec}
}

// ResolveSpecPath returns the absolute location a spec pointer names, or ""
// when it cannot be proven to live inside repoDir — a pointer is data an
// agent wrote, and following one outside the repo would hand the verifier
// an arbitrary file to read (review must-fix, 2026-09-01; the observed
// echo that once named the user's credentials file is exactly the read
// this refuses). Absolute pointers are accepted only when symlink-resolved
// containment holds; relative pointers resolve against repoDir under the
// same check. With no repo dir a relative pointer returns "" — the caller
// skips existence checks rather than failing them, matching
// SessionRepoDir's fail-open contract.
func ResolveSpecPath(repoDir, spec string) string {
	if strings.TrimSpace(repoDir) == "" {
		return ""
	}
	if _, ok := repoContainedRel(filepath.Clean(repoDir), spec); !ok {
		return ""
	}
	if filepath.IsAbs(spec) {
		return spec
	}
	return filepath.Join(repoDir, spec)
}
