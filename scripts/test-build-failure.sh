#!/usr/bin/env bash
# Integration test: a Go compile error must fail `make build` and must never
# reach `make install`.
#
# This exists because the opposite was true. The `build` recipe is a single
# joined shell command with no `set -e`, so a failing `go build` was swallowed
# and the recipe's exit status came from its trailing `if/echo` — always 0.
# `make install` then ran on its "successful" prerequisite and installed the
# PREVIOUS binary, printing "Built N modules" and "Installed:" over a build
# that never compiled. Every build verdict downstream was unreliable.
#
# Also checks that install is idempotent: running it twice into the same prefix
# produces identical content.
#
# Deliberately NOT `set -e` — this test runs commands that are expected to fail.
set -uo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
BROKEN_FILE="$REPO_DIR/tools/muxcode/zz_build_guard_broken.go"
TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/muxcode-build-test-XXXXXX")"

pass_count=0
fail_count=0

cleanup() {
  rm -f "$BROKEN_FILE"
  rm -rf "$TMP_ROOT"
}
trap cleanup EXIT

ok()   { echo "  PASS: $1"; pass_count=$((pass_count + 1)); }
bad()  { echo "  FAIL: $1" >&2; fail_count=$((fail_count + 1)); }

# Install into throwaway dirs so the test never touches the real install.
hermetic_install() {
  local dest="$1"
  make -C "$REPO_DIR" install \
    PREFIX="$dest/prefix" \
    CONFIGDIR="$dest/config" \
    NVIM_CONFIGDIR="$dest/config/nvim" \
    NVIM_PLUGIN_DIR="$dest/nvim-plugin" \
    >"$dest/install.log" 2>&1
}

echo "=== 1. Baseline: clean build succeeds ==="
rm -f "$BROKEN_FILE"
if make -C "$REPO_DIR" build >"$TMP_ROOT/build-clean.log" 2>&1; then
  ok "clean build exits 0"
else
  bad "clean build failed — cannot trust the rest of this test"
  cat "$TMP_ROOT/build-clean.log" >&2
  echo "total $((pass_count + fail_count))  pass $pass_count  fail $fail_count"
  exit 1
fi

echo "=== 2. A compile error must fail 'make build' ==="
cat >"$BROKEN_FILE" <<'GOEOF'
package main

// Deliberately invalid Go, written by scripts/test-build-failure.sh.
func zzBuildGuardBroken() { this is not valid go syntax }
GOEOF

if make -C "$REPO_DIR" build >"$TMP_ROOT/build-broken.log" 2>&1; then
  bad "'make build' exited 0 despite a compile error (the original bug)"
else
  ok "'make build' exits non-zero on a compile error"
fi

if grep -q "Go build FAILED" "$TMP_ROOT/build-broken.log"; then
  ok "failure is reported explicitly"
else
  bad "no explicit failure message in build output"
fi

if grep -q "Go binary: Built" "$TMP_ROOT/build-broken.log"; then
  bad "build still printed a success count for a failed build"
else
  ok "no success count printed for a failed build"
fi

echo "=== 3. A compile error must block 'make install' ==="
mkdir -p "$TMP_ROOT/broken-install"
if hermetic_install "$TMP_ROOT/broken-install"; then
  bad "'make install' exited 0 despite a compile error"
else
  ok "'make install' exits non-zero on a compile error"
fi

if grep -q "^Installed:" "$TMP_ROOT/broken-install/install.log" 2>/dev/null; then
  bad "install reported success over a failed build"
else
  ok "install did not report success over a failed build"
fi

if [ -f "$TMP_ROOT/broken-install/prefix/bin/muxcode" ]; then
  bad "a binary was installed despite the compile error"
else
  ok "no binary installed from a failed build"
fi

echo "=== 4. Recovery: removing the error builds again ==="
rm -f "$BROKEN_FILE"
if make -C "$REPO_DIR" build >"$TMP_ROOT/build-recovered.log" 2>&1; then
  ok "build recovers after the error is removed"
else
  bad "build did not recover"
  cat "$TMP_ROOT/build-recovered.log" >&2
fi

echo "=== 5. Install is idempotent ==="
mkdir -p "$TMP_ROOT/idem"
if hermetic_install "$TMP_ROOT/idem"; then
  ok "first install succeeds"
else
  bad "first install failed"
  cat "$TMP_ROOT/idem/install.log" >&2
fi

# Snapshot everything except the install log itself.
snapshot() {
  (cd "$1" && find . -path ./install.log -prune -o -type f -print 2>/dev/null \
    | sort | while read -r f; do printf '%s %s\n' "$(shasum -a 256 "$f" | awk '{print $1}')" "$f"; done)
}
snapshot "$TMP_ROOT/idem" >"$TMP_ROOT/snap1.txt"

if hermetic_install "$TMP_ROOT/idem"; then
  ok "second install succeeds"
else
  bad "second install failed"
fi
snapshot "$TMP_ROOT/idem" >"$TMP_ROOT/snap2.txt"

if diff -u "$TMP_ROOT/snap1.txt" "$TMP_ROOT/snap2.txt" >"$TMP_ROOT/idem.diff" 2>&1; then
  ok "install is idempotent — second run produced identical content"
else
  bad "install is NOT idempotent — content changed on the second run"
  head -40 "$TMP_ROOT/idem.diff" >&2
fi

echo
echo "total $((pass_count + fail_count))  pass $pass_count  fail $fail_count"
[ "$fail_count" -eq 0 ]
