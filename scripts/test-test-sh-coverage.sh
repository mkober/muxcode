#!/usr/bin/env bash
# Integration test for ./test.sh's module coverage and run summary (MUX-174).
#
# Runs the repo's real test.sh in place — its own module discovery against the
# real tools/ tree — with a stub `go` first on PATH, so nothing compiles and the
# suite stays the test node's job. The stub records each `<module> <step>` call
# and fails the steps named in GO_STUB_FAIL ("<module>:vet" / "<module>:test").
#
# The module list is pinned in EXPECTED_MODULES: a module added to or dropped
# from tools/ fails section 1 until the pin is updated, and every summary
# assertion checks the pinned list, so a module dropping out of the run is a
# failure rather than a shorter green report. Section 5 is the negative
# control: the summary assertion must reject a summary missing a module.
#
# Hermetic: no daemon, no tmux, no muxcode binary; writes only to a temp dir.
# Coverage floor: the exact pass count, tied to the two pinned modules.
set -uo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
EXPECTED_MODULES="muxcode muxcode-llm-harness"
EXPECTED_COUNT=2
EXPECTED_PASS=23

pass=0; fail=0
ok()  { echo "  PASS  $*"; pass=$((pass + 1)); }
bad() { echo "  FAIL  $*"; fail=$((fail + 1)); }
check() { if [ "$2" -eq 0 ]; then ok "$1"; else bad "$1"; fi; }

WORK="$(mktemp -d "${TMPDIR:-/tmp}/test-test-sh-coverage.XXXXXX")"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"
cat > "$WORK/bin/go" <<'EOF'
#!/usr/bin/env bash
mod=$(basename "$PWD")
echo "$mod $1" >> "$GO_STUB_LOG"
case " ${GO_STUB_FAIL:-} " in
  *" $mod:$1 "*) echo "go-stub: $mod go $1 forced failure" >&2; exit 2 ;;
esac
echo "go-stub: $mod go $1 ok"
EOF
chmod +x "$WORK/bin/go"

# run_test_sh <test.sh> <fail-spec> sets OUT (combined output), RC and CALLS
# (the stub's `<module> <step>` lines, in call order).
run_test_sh() {
  : > "$WORK/go.log"
  OUT=$(GO_STUB_LOG="$WORK/go.log" GO_STUB_FAIL="$2" PATH="$WORK/bin:$PATH" bash "$1" 2>&1)
  RC=$?
  CALLS=$(cat "$WORK/go.log")
}

# summary_lists_all <output> succeeds only when the last `=== Summary ===`
# section has exactly one status line for every pinned module and a totals
# line counting all of them.
summary_lists_all() {
  local section mod
  section=$(printf '%s\n' "$1" | awk '/^=== Summary ===$/ { buf = ""; s = 1; next } s { buf = buf $0 "\n" } END { printf "%s", buf }')
  for mod in $EXPECTED_MODULES; do
    [ "$(printf '%s\n' "$section" | grep -cE "^$mod: (passed|failed \(go vet\)|failed \(go test\)|never executed)$")" -eq 1 ] || return 1
  done
  printf '%s\n' "$section" | grep -qE "^modules=$EXPECTED_COUNT ran=[0-9]+ passed=[0-9]+ failed=[0-9]+ never-executed=[0-9]+$"
}

has_line() { printf '%s\n' "$OUT" | grep -qxF "$1"; }

echo "=== test.sh coverage integration test ==="

# --- 1. The pinned module list matches tools/ --------------------------------
discovered=$(for d in "$REPO"/tools/*/; do [ -f "$d/go.mod" ] && basename "$d"; done | LC_ALL=C sort | tr '\n' ' ')
pinned=$(printf '%s\n' $EXPECTED_MODULES | LC_ALL=C sort | tr '\n' ' ')
check "tools/ modules equal the pin (found: $discovered)" "$([ "$discovered" = "$pinned" ]; echo $?)"

# --- 2. Every module runs and appears in the summary -------------------------
run_test_sh "$REPO/test.sh" ""
check "all passing: exit 0 (got $RC)" "$([ "$RC" -eq 0 ]; echo $?)"
check "all passing: summary lists every pinned module" "$(summary_lists_all "$OUT"; echo $?)"
for mod in $EXPECTED_MODULES; do
  check "all passing: '$mod: passed'" "$(has_line "$mod: passed"; echo $?)"
done
totals="modules=$EXPECTED_COUNT ran=$EXPECTED_COUNT passed=$EXPECTED_COUNT failed=0 never-executed=0"
check "all passing: totals line" "$(has_line "$totals"; echo $?)"
check "all passing: totals line is the last line" "$([ "$(printf '%s\n' "$OUT" | tail -1)" = "$totals" ]; echo $?)"
calls_ok=0
[ "$(printf '%s\n' "$CALLS" | grep -c .)" -eq $((EXPECTED_COUNT * 2)) ] || calls_ok=1
for mod in $EXPECTED_MODULES; do
  printf '%s\n' "$CALLS" | grep -qxF "$mod vet" || calls_ok=1
  printf '%s\n' "$CALLS" | grep -qxF "$mod test" || calls_ok=1
done
check "all passing: go vet and go test ran once in every module" "$calls_ok"

FIRST=$(printf '%s\n' "$CALLS" | awk 'NR == 1 { print $1 }')
OTHERS=$(for mod in $EXPECTED_MODULES; do [ "$mod" = "$FIRST" ] || echo "$mod"; done)
echo "  (test.sh visits '$FIRST' first)"

# --- 3. A vet failure in the first module reports the rest as never executed --
run_test_sh "$REPO/test.sh" "$FIRST:vet"
check "first fails vet: exit 1 (got $RC)" "$([ "$RC" -eq 1 ]; echo $?)"
check "first fails vet: '$FIRST: failed (go vet)'" "$(has_line "$FIRST: failed (go vet)"; echo $?)"
for mod in $OTHERS; do
  check "first fails vet: '$mod: never executed'" "$(has_line "$mod: never executed"; echo $?)"
  check "first fails vet: '$mod' not reported passed" "$(has_line "$mod: passed"; [ $? -ne 0 ]; echo $?)"
done
check "first fails vet: summary still lists every pinned module" "$(summary_lists_all "$OUT"; echo $?)"
check "first fails vet: totals line" "$(has_line "modules=$EXPECTED_COUNT ran=1 passed=0 failed=1 never-executed=$((EXPECTED_COUNT - 1))"; echo $?)"
check "first fails vet: only '$FIRST vet' invoked (got: $(echo $CALLS))" "$([ "$CALLS" = "$FIRST vet" ]; echo $?)"

# --- 4. A test failure in the first module stops the run the same way --------
run_test_sh "$REPO/test.sh" "$FIRST:test"
check "first fails test: exit 1 (got $RC)" "$([ "$RC" -eq 1 ]; echo $?)"
check "first fails test: '$FIRST: failed (go test)'" "$(has_line "$FIRST: failed (go test)"; echo $?)"
for mod in $OTHERS; do
  check "first fails test: '$mod: never executed'" "$(has_line "$mod: never executed"; echo $?)"
done
check "first fails test: totals line" "$(has_line "modules=$EXPECTED_COUNT ran=1 passed=0 failed=1 never-executed=$((EXPECTED_COUNT - 1))"; echo $?)"
check "first fails test: only '$FIRST' invoked" "$([ "$CALLS" = "$(printf '%s vet\n%s test' "$FIRST" "$FIRST")" ]; echo $?)"

# --- 5. Negative control: a summary missing a module must be rejected --------
run_test_sh "$REPO/test.sh" ""
doctored=$(printf '%s\n' "$OUT" | grep -vxF "$FIRST: passed")
check "doctored summary without '$FIRST' is rejected" "$(summary_lists_all "$doctored"; [ $? -ne 0 ]; echo $?)"

drop="$WORK/dropout"
mkdir -p "$drop/tools"
cp "$REPO/test.sh" "$drop/test.sh"
for mod in $EXPECTED_MODULES; do mkdir -p "$drop/tools/$mod"; done
echo "module $FIRST" > "$drop/tools/$FIRST/go.mod"
run_test_sh "$drop/test.sh" ""
check "dropped-out module: test.sh alone still exits 0 (the shorter green report)" "$([ "$RC" -eq 0 ]; echo $?)"
check "dropped-out module: summary assertion rejects the run" "$(summary_lists_all "$OUT"; [ $? -ne 0 ]; echo $?)"

# --- Coverage floor --------------------------------------------------------
echo ""
echo "=== results: $pass passed, $fail failed (floor $EXPECTED_PASS) ==="
if [ "$pass" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $pass — a section was skipped or double-counted"
  exit 1
fi
[ "$fail" -eq 0 ]
