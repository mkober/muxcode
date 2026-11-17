#!/usr/bin/env bash
# Integration test for MUX-223 — project memory resolves to one root, never the
# caller's working directory, and stranded memory is merged as the user decided.
#
# Before the fix MemoryDir() was the relative `.muxcode/memory`, so a
# `session compact` run from a subdirectory wrote a second memory tree there
# that restore never read: tools/muxcode/.muxcode/memory and
# tools/muxcode/bus/.muxcode/memory held real session summaries.
#
# Hermetic: muxcode built from THIS tree, scratch HOME, XDG_STATE_HOME, global
# memory and git checkout, no daemon and no tmux. The pre-fix build is this tree
# with MemoryDir's relative default restored in a scratch copy.
#
# Sections:
#   A  `session compact` from sub/a and `memory write` from sub/b/c land in one
#      memory directory, at the checkout root; `memory context` and
#      `session resume` from sub/d read both
#   B  a git worktree's write lands in the main checkout's memory
#   C  control — an absolute BUS_MEMORY_DIR receives the write and nothing in
#      the checkout changes
#   D  a relative BUS_MEMORY_DIR is refused naming the variable, from two
#      subdirectories, creating nothing
#   E  outside any checkout a write fails naming BUS_MEMORY_DIR and creates
#      nothing; `memory context` returns global memory only
#   F  a seeded stranded directory is listed (the root's own memory never is),
#      merged entry by entry with the root backed up, kept byte for byte under
#      .muxcode/stranded-memory, and a rescan finds nothing
#   G  control — the pre-fix build strands a subdirectory's write beside it
# A coverage floor keeps a skipped section from reporting green.
# Requires go, git.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=25
for t in go git; do
  command -v "$t" >/dev/null 2>&1 || { echo "SKIP: $t is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d /tmp/memory-dir-root-XXXXXX)
WORK=$(cd "$WORK" && pwd -P)
trap 'rm -rf "$WORK"' EXIT

unset AGENT_ROLE BUS_ROLE TMUX MUXCODE MUXCODE_CONFIG BUS_MEMORY_DIR MUXCODE_BUS_BASE MUXCODE_BUS_DIR
export HOME="$WORK/home"
export XDG_STATE_HOME="$WORK/state"
export BUS_GLOBAL_MEMORY_DIR="$WORK/global-memory"
export MUXCODE_LIFECYCLE_LOG_DIR="$WORK/lifecycle"
export BUS_SESSION="memroot-$$"
export AGENT_ROLE=edit
export GIT_CEILING_DIRECTORIES="$WORK"
mkdir -p "$HOME" "$XDG_STATE_HOME" "$MUXCODE_LIFECYCLE_LOG_DIR" "$WORK/bin" "$WORK/bin-pre"

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }
git_q() { git -c user.name=Test -c user.email=test@example.com -c commit.gpgsign=false -c core.hooksPath= "$@" >/dev/null 2>&1; }

(cd "$REPO/tools/muxcode" && go build -buildvcs=false -o "$WORK/bin/muxcode" .) \
  || { echo "SKIP: go build muxcode failed"; exit 2; }
MUX="$WORK/bin/muxcode"
ok "scratch muxcode built from this tree"

mkdir -p "$WORK/pre"
cp -R "$REPO/tools/muxcode" "$WORK/pre/"
perl -0pi -e 's/func MemoryDir\(\) string \{\n/func MemoryDir() string {\n\tif os.Getenv(MemoryDirEnv) == "" {\n\t\treturn filepath.Join(".muxcode", "memory")\n\t}\n/' "$WORK/pre/muxcode/bus/config.go"
if grep -q 'return filepath.Join(".muxcode", "memory")' "$WORK/pre/muxcode/bus/config.go" \
  && (cd "$WORK/pre/muxcode" && go build -buildvcs=false -o "$WORK/bin-pre/muxcode" .); then
  ok "pre-fix muxcode built: MemoryDir's relative default restored"
else
  fail "pre-fix build: the MemoryDir patch did not apply, or the copy failed to build"
fi
MUX_PRE="$WORK/bin-pre/muxcode"

# new_project NAME — a git checkout with one commit and sub/a, sub/b/c, sub/d;
# sets PROJ.
new_project() {
  PROJ="$WORK/$1"
  mkdir -p "$PROJ/sub/a" "$PROJ/sub/b/c" "$PROJ/sub/d"
  git_q -C "$PROJ" init
  git_q -C "$PROJ" commit --allow-empty -m init
}
memory_dirs() { (cd "$1" && find . -path ./.git -prune -o -type d -name memory -path '*/.muxcode/memory' -print | sort); }
in_dir() { local d=$1; shift; (cd "$d" && "$@"); }

echo "=== MUX-223 project memory root ==="

# ── A: two subdirectories, one memory directory at the root ────────
echo "-- A: compact and write from subdirectories"
new_project a
in_dir "$PROJ/sub/d" "$MUX" init >/dev/null 2>&1
in_dir "$PROJ/sub/a" "$MUX" session compact "Summary A-COMPACT from sub/a" >/dev/null 2>&1 \
  && ok "A: session compact from sub/a succeeded" || fail "A: session compact from sub/a failed"
in_dir "$PROJ/sub/b/c" "$MUX" memory write "Note" "Entry B-WRITE from sub/b/c" >/dev/null 2>&1 \
  && ok "A: memory write from sub/b/c succeeded" || fail "A: memory write from sub/b/c failed"
dirs=$(memory_dirs "$PROJ")
[ "$dirs" = "./.muxcode/memory" ] && ok "A: exactly one memory directory, at the checkout root" \
  || fail "A: memory directories: $(echo "$dirs" | tr '\n' ' ')"
grep -q A-COMPACT "$PROJ/.muxcode/memory/edit.md" 2>/dev/null && grep -q B-WRITE "$PROJ/.muxcode/memory/edit.md" \
  && ok "A: both entries are in the root's edit.md" || fail "A: root edit.md: $(cat "$PROJ/.muxcode/memory/edit.md" 2>&1)"
ctx=$(in_dir "$PROJ/sub/d" "$MUX" memory context --no-global 2>&1)
echo "$ctx" | grep -q A-COMPACT && echo "$ctx" | grep -q B-WRITE \
  && ok "A: memory context from sub/d returns both entries" || fail "A: memory context from sub/d: $ctx"
res=$(in_dir "$PROJ/sub/d" "$MUX" session resume 2>&1)
echo "$res" | grep -q A-COMPACT && ok "A: session resume from sub/d restores the compact summary" \
  || fail "A: session resume from sub/d: $res"

# ── B: a worktree shares the main checkout's memory ────────────────
echo "-- B: a git worktree"
git_q -C "$PROJ" worktree add --detach "$WORK/a-wt"
in_dir "$WORK/a-wt" "$MUX" memory write "Note" "Entry WORKTREE-WRITE" >/dev/null 2>&1 \
  && grep -q WORKTREE-WRITE "$PROJ/.muxcode/memory/edit.md" && [ ! -e "$WORK/a-wt/.muxcode" ] \
  && ok "B: the worktree's write landed in the main checkout's memory" \
  || fail "B: worktree write — root: $(grep -c WORKTREE-WRITE "$PROJ/.muxcode/memory/edit.md" 2>&1), worktree .muxcode: $(ls -d "$WORK/a-wt/.muxcode" 2>&1)"

# ── C: control — an absolute BUS_MEMORY_DIR wins ───────────────────
echo "-- C: control — absolute BUS_MEMORY_DIR"
before=$(cksum <"$PROJ/.muxcode/memory/edit.md")
in_dir "$PROJ/sub/a" env BUS_MEMORY_DIR="$WORK/explicit-memory" "$MUX" memory write "Note" "Entry EXPLICIT-WRITE" >/dev/null 2>&1
grep -q EXPLICIT-WRITE "$WORK/explicit-memory/edit.md" 2>/dev/null \
  && ok "C: the write went to the explicit BUS_MEMORY_DIR" || fail "C: explicit memory: $(cat "$WORK/explicit-memory/edit.md" 2>&1)"
[ "$(cksum <"$PROJ/.muxcode/memory/edit.md")" = "$before" ] && [ "$(memory_dirs "$PROJ")" = "./.muxcode/memory" ] \
  && ok "C: nothing in the checkout changed" || fail "C: the checkout's memory changed: $(memory_dirs "$PROJ" | tr '\n' ' ')"

# ── D: a relative BUS_MEMORY_DIR is refused ────────────────────────
echo "-- D: relative BUS_MEMORY_DIR"
for d in sub/a sub/d; do
  out=$(in_dir "$PROJ/$d" env BUS_MEMORY_DIR=custom-memory "$MUX" memory write "Note" "RELATIVE" 2>&1)
  rc=$?
  [ "$rc" -ne 0 ] && echo "$out" | grep -q 'BUS_MEMORY_DIR must be an absolute path' && [ ! -e "$PROJ/$d/custom-memory" ] \
    && ok "D: refused from $d, naming BUS_MEMORY_DIR, creating nothing" || fail "D: from $d exit $rc: $out"
done

# ── E: outside any checkout ────────────────────────────────────────
echo "-- E: outside a checkout"
mkdir -p "$WORK/outside"
in_dir "$WORK/outside" "$MUX" memory write-global "Note" "GLOBAL-ONLY" >/dev/null 2>&1
out=$(in_dir "$WORK/outside" "$MUX" memory write "Note" "NOWHERE" 2>&1)
rc=$?
[ "$rc" -ne 0 ] && echo "$out" | grep -q BUS_MEMORY_DIR \
  && ok "E: a write outside a checkout fails naming BUS_MEMORY_DIR" || fail "E: exit $rc: $out"
[ -z "$(ls -A "$WORK/outside")" ] && ok "E: nothing was created in the working directory" \
  || fail "E: created: $(ls -A "$WORK/outside")"
ctx=$(in_dir "$WORK/outside" "$MUX" memory context 2>&1)
echo "$ctx" | grep -q GLOBAL-ONLY && ! echo "$ctx" | grep -q '# edit Memory' \
  && ok "E: memory context returns global memory only" || fail "E: memory context: $ctx"

# ── F: stranded memory, merged as decided ──────────────────────────
echo "-- F: a seeded stranded directory"
STR="$PROJ/sub/b/.muxcode/memory"
mkdir -p "$STR/edit"
printf '\n## Session Summary\n_2026-11-13 08:00_\n\nSTRANDED-EARLY\n' >"$STR/edit.md"
printf '\n## Old\n_2026-11-12 12:00_\n\nSTRANDED-ARCHIVE\n' >"$STR/edit/2026-11-12.md"
cp -R "$STR" "$WORK/stranded-copy"
cp "$PROJ/.muxcode/memory/edit.md" "$WORK/root-before.md"

list=$(in_dir "$PROJ/sub/a" "$MUX" memory stranded 2>&1)
echo "$list" | grep -q 'stranded: sub/b/.muxcode/memory' \
  && ok "F: the stranded directory is listed" || fail "F: listing: $list"
! echo "$list" | grep -qE 'stranded: \.muxcode/memory|stranded-memory' \
  && ok "F: the root's own memory is never listed (negative control)" || fail "F: listing names the root: $list"
merge=$(in_dir "$PROJ/sub/a" "$MUX" memory stranded --merge 2>&1)
rc=$?
[ "$rc" -eq 0 ] && grep -q STRANDED-EARLY "$PROJ/.muxcode/memory/edit.md" \
  && grep -q STRANDED-ARCHIVE "$PROJ/.muxcode/memory/edit/2026-11-12.md" 2>/dev/null \
  && ok "F: both stranded files were merged into the root" || fail "F: merge exit $rc: $merge"
first=$(grep -n -m1 STRANDED-EARLY "$PROJ/.muxcode/memory/edit.md" | cut -d: -f1)
second=$(grep -n -m1 A-COMPACT "$PROJ/.muxcode/memory/edit.md" | cut -d: -f1)
[ -n "$first" ] && [ -n "$second" ] && [ "$first" -lt "$second" ] \
  && ok "F: the merged entries are in time order" || fail "F: STRANDED-EARLY at line $first, A-COMPACT at $second"
backup=$(echo "$merge" | sed -n 's/.*sub\/b\/.muxcode\/memory\/edit.md: .*backup \(.*\)$/\1/p')
[ -n "$backup" ] && cmp -s "$backup" "$WORK/root-before.md" \
  && ok "F: the root file was backed up byte for byte before the merge" || fail "F: backup '$backup' — merge output: $merge"
KEPT="$PROJ/.muxcode/stranded-memory/sub/b/.muxcode/memory"
diff -r "$WORK/stranded-copy" "$KEPT" >/dev/null 2>&1 && [ ! -e "$STR" ] \
  && ok "F: the original was kept byte for byte under .muxcode/stranded-memory and moved out of place" \
  || fail "F: kept original differs or the stranded directory remains"
in_dir "$PROJ/sub/a" "$MUX" memory stranded 2>&1 | grep -q 'No stranded memory directories' \
  && ok "F: a rescan finds nothing stranded" || fail "F: rescan: $(in_dir "$PROJ/sub/a" "$MUX" memory stranded 2>&1)"

# ── G: control — the pre-fix build strands the write ───────────────
echo "-- G: control — the pre-fix build"
new_project g
in_dir "$PROJ/sub/a" "$MUX_PRE" memory write "Note" "PRE-FIX-WRITE" >/dev/null 2>&1
grep -q PRE-FIX-WRITE "$PROJ/sub/a/.muxcode/memory/edit.md" 2>/dev/null \
  && ok "G: the pre-fix build wrote beside the caller, in sub/a/.muxcode/memory — the 2026-11-13 stranding" \
  || fail "G: the pre-fix build did not strand the write: $(memory_dirs "$PROJ" | tr '\n' ' ')"
[ ! -e "$PROJ/.muxcode/memory/edit.md" ] && ok "G: and the root's memory never received it" \
  || fail "G: the pre-fix write reached the root"

# ── Coverage floor ─────────────────────────────────────────────────
echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
[ "$FAIL" -eq 0 ] || exit 1
exit 0
