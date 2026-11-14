#!/usr/bin/env bash
# Runs `go vet ./...` then `go test -v ./...` in every Go module under tools/,
# in name order, stopping at the first failure. It always ends with a summary,
# so a truncated run reads as truncated rather than as a shorter green one: on
# 2026-09-10 a vet failure in one module silently ended the run (MUX-174, the
# MUX-152 shape).
#
# The summary is a contract that scripts and agents parse:
#   === Summary ===
#   <module>: passed | failed (go vet) | failed (go test) | never executed
#   modules=<n> ran=<n> passed=<n> failed=<n> never-executed=<n>
# A failed module's line is followed by indented detail lines — "  log: <path>"
# and, for a test failure, "  FAIL <package>: <tests>" (at most four), for a vet
# failure "  vet <file:line: finding>" (at most three) — so the failure is named
# inside the summary, the part a history row's output tail keeps (MUX-215:
# twice on 2026-11-13 a failure passed on rerun and nothing named the test).
# Each module's full vet and test output is saved to <log dir>/<module>.log;
# MUXCODE_TEST_LOG_DIR sets the log root (default /tmp/muxcode-test-logs).
# Exits 0 only when every module passed; 1 on any failure, or when tools/ holds
# no module at all.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
LOG_DIR="${MUXCODE_TEST_LOG_DIR:-/tmp/muxcode-test-logs}/$(date +%Y%m%d-%H%M%S)-$$"
mkdir -p "$LOG_DIR" 2>/dev/null || LOG_DIR=""

modules=()
for moddir in "$REPO_DIR"/tools/*/; do
  [ -f "$moddir/go.mod" ] || continue
  modules+=("$(basename "$moddir")")
done

summary=()
ran=0 passed=0 failed=0 never=0

# print_summary writes the closing contract above. The length guard is
# load-bearing: an empty "${summary[@]}" is unbound under set -u on bash < 4.4.
print_summary() {
  echo "=== Summary ==="
  [ "${#summary[@]}" -eq 0 ] || printf '%s\n' "${summary[@]}"
  echo "modules=${#modules[@]} ran=$ran passed=$passed failed=$failed never-executed=$never"
}

if [ "${#modules[@]}" -eq 0 ]; then
  echo "test.sh: no Go module under $REPO_DIR/tools" >&2
  print_summary
  exit 1
fi

go_step() {
  local name=$1
  shift
  echo "=== $name: go $1 ==="
  if [ -n "$LOG_DIR" ]; then
    (cd "$REPO_DIR/tools/$name" && go "$@") 2>&1 | tee -a "$LOG_DIR/$name.log"
  else
    (cd "$REPO_DIR/tools/$name" && go "$@")
  fi
}

# failure_details <module> <vet|test> — the indented summary lines that name a
# failure: the log path, then the failing packages and tests (or vet findings)
# read from the module's log. Short and few, so the summary stays a summary.
failure_details() {
  local log="$LOG_DIR/$1.log" modpath
  [ -n "$LOG_DIR" ] && [ -f "$log" ] || return 0
  echo "  log: $log"
  if [ "$2" = vet ]; then
    grep -E '\.go:[0-9]+(:[0-9]+)?: ' "$log" | head -3 | cut -c1-120 | sed 's/^/  vet /'
    return 0
  fi
  modpath=$(awk '$1 == "module" { print $2; exit }' "$REPO_DIR/tools/$1/go.mod" 2>/dev/null || true)
  awk -v mod="$modpath" '
    /^[[:space:]]*--- FAIL: / { tests = tests ? tests ", " $3 : $3; next }
    /^FAIL[ \t]/ && NF >= 2 {
      pkg = $2
      if (mod != "" && index(pkg, mod) == 1) { pkg = substr(pkg, length(mod) + 2); if (pkg == "") pkg = "." }
      line = "  FAIL " pkg (($3 ~ /^\[/) ? " " $3 " " $4 : "") (tests ? ": " tests : "")
      if (length(line) > 120) line = substr(line, 1, 117) "..."
      out[++k] = line
      tests = ""
      next
    }
    $1 == "ok" { tests = "" }
    END {
      for (i = 1; i <= k && i <= 4; i++) print out[i]
      if (k > 4) print "  ... " (k - 4) " more failing package(s), see the log"
    }' "$log"
}

for name in "${modules[@]}"; do
  if [ "$failed" -gt 0 ]; then
    summary+=("$name: never executed")
    never=$((never + 1))
    continue
  fi
  ran=$((ran + 1))
  if ! go_step "$name" vet ./...; then
    summary+=("$name: failed (go vet)")
    while IFS= read -r line; do summary+=("$line"); done < <(failure_details "$name" vet)
    failed=$((failed + 1))
  elif ! go_step "$name" test -v ./...; then
    summary+=("$name: failed (go test)")
    while IFS= read -r line; do summary+=("$line"); done < <(failure_details "$name" test)
    failed=$((failed + 1))
  else
    summary+=("$name: passed")
    passed=$((passed + 1))
  fi
done

print_summary
[ "$failed" -eq 0 ] || exit 1
