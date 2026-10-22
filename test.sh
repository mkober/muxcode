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
# Exits 0 only when every module passed; 1 on any failure, or when tools/ holds
# no module at all.
set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")" && pwd)"

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
  (cd "$REPO_DIR/tools/$name" && go "$@")
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
    failed=$((failed + 1))
  elif ! go_step "$name" test -v ./...; then
    summary+=("$name: failed (go test)")
    failed=$((failed + 1))
  else
    summary+=("$name: passed")
    passed=$((passed + 1))
  fi
done

print_summary
[ "$failed" -eq 0 ] || exit 1
