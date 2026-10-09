#!/usr/bin/env bash
# Integration test for scripts/merge-claude-settings.sh — the hook merge shared
# by ./install.sh and `make install` (which ./build.sh and `muxcode upgrade`
# run). PR 160 review: an install predating MUX-204 never received the
# PostToolUseFailure hook on the upgrade road, because only install.sh merged.
#
# Hermetic: scratch settings files, and `make install` under a scratch HOME
# with every install directory overridden; the user's ~/.claude is never read
# or written. The fixture is config/settings.json minus PostToolUseFailure —
# an install from before MUX-204 — plus the user's own hook, key and rules.
#   upgrade road — --upgrade adds the failure hook, keeps the user's entries,
#                  prunes rejected rules, backs up the original, keeps its mode
#   idempotence  — a second run changes nothing, not even the backup
#   opt-in gate  — no file is skipped and never created; a file with no
#                  muxcode hook is left alone under --upgrade, merged without it
#   python3      — with jq hidden the fallback matches jq's result exactly and
#                  honours the same gate; with neither tool the merge is skipped
#   write failure — an unwritable directory exits 1 and leaves the file intact
#   concurrency  — an edit landing while a merge holds the lock (a jq stand-in
#                  on PATH rewrites the file once, as Claude would) survives;
#                  six simultaneous merges converge with no staging file left;
#                  two merges with different templates, a slowed cmp holding
#                  the race open, keep both additions; a dead holder's lock is
#                  never broken — the merge fails at once naming it — while a
#                  live holder (the control) is waited on for 10 s; a merge
#                  sent SIGTERM under the lock still releases it
#   make install — merges into $HOME/.claude/settings.json by default, passes
#                  --upgrade, and only warns when it cannot write
# Coverage floor: the exact pass count.
set -uo pipefail

PASS=0
FAIL=0
EXPECTED_PASS=21

for tool in jq python3 go make; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done

REPO=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
HELPER="$REPO/scripts/merge-claude-settings.sh"
TEMPLATE="$REPO/config/settings.json"
BASH_BIN=$(command -v bash)
WORK=$(mktemp -d /tmp/claude-settings-merge-XXXXXX)
unset CLAUDE_SETTINGS

cleanup() { chmod -R u+w "$WORK" 2>/dev/null; rm -rf "$WORK"; }
trap cleanup EXIT

ok()   { PASS=$((PASS + 1)); echo "  ok: $1"; }
fail() { FAIL=$((FAIL + 1)); echo "  FAIL: $1"; }

merge() { "$BASH_BIN" "$HELPER" "$@" 2>&1; }
same_json() { jq -e -n --slurpfile a "$1" --slurpfile b "$2" '$a[0] == $b[0]' >/dev/null 2>&1; }
mode_of() { ls -l "$1" | cut -c1-10; }
has_failure_hook() {
  jq -e '.hooks.PostToolUseFailure // [] | any(.matcher == "Bash" and any(.hooks[]; .command == "muxcode hook bash"))' "$1" >/dev/null 2>&1
}

# A PATH holding only what the helper runs, so jq (or both tools) can be hidden.
tool_dir() {
  local dir="$WORK/bin-$1"; shift
  mkdir -p "$dir"
  for t in mktemp cp mv cat cmp rm basename dirname ln readlink sleep "$@"; do ln -sf "$(command -v "$t")" "$dir/$t"; done
  echo "$dir"
}
NOJQ_PATH=$(tool_dir nojq python3)
NOTOOL_PATH=$(tool_dir none)

write_fixture() {
  jq '
    del(.hooks.PostToolUseFailure)
    | .model = "opus"
    | .hooks.PreToolUse += [{"matcher": "Read", "hooks": [{"type": "command", "command": "my-own-hook"}]}]
    | .permissions.allow += ["Bash(my-tool *)", "Write(/tmp/muxcode-*)"]
  ' "$TEMPLATE" >"$1"
  chmod 640 "$1"
}
write_foreign() {
  printf '%s\n' '{"model": "opus", "hooks": {"PreToolUse": [{"matcher": "Read", "hooks": [{"type": "command", "command": "my-own-hook"}]}]}}' >"$1"
}

echo "=== 1. upgrade road (jq): a pre-MUX-204 install gains the failure hook ==="
mkdir -p "$WORK/jq"
S="$WORK/jq/settings.json"
write_fixture "$S"
cp -p "$S" "$WORK/original.json"
out=$(merge --upgrade "$TEMPLATE" "$S"); rc=$?
if [ "$rc" -eq 0 ] && has_failure_hook "$S" && [[ "$out" == *updated* ]]; then
  ok "--upgrade adds PostToolUseFailure Bash → muxcode hook bash"
else
  fail "upgrade merge: rc=$rc out=$out"
fi
if jq -e '.model == "opus"
    and any(.hooks.PreToolUse[]; .matcher == "Read" and any(.hooks[]; .command == "my-own-hook"))
    and (.permissions.allow | index("Bash(my-tool *)"))
    and (.permissions.allow | index("Write(/tmp/muxcode-*)") | not)' "$S" >/dev/null; then
  ok "the user's hook, key and rule are kept; the rule Claude rejects is pruned"
else
  fail "user entries not preserved, or rejected rule kept"
fi
if cmp -s "$S.pre-muxcode" "$WORK/original.json" && [ "$(mode_of "$S")" = "$(mode_of "$WORK/original.json")" ]; then
  ok "the original is backed up byte for byte and the file keeps its mode ($(mode_of "$S"))"
else
  fail "backup differs from the original, or mode changed: $(mode_of "$S")"
fi
cp "$S" "$WORK/jq-merged.json"

echo "=== 2. idempotence ==="
out=$(merge --upgrade "$TEMPLATE" "$S"); rc=$?
if [ "$rc" -eq 0 ] && [[ "$out" == *"already up to date"* ]] && cmp -s "$S" "$WORK/jq-merged.json" \
    && cmp -s "$S.pre-muxcode" "$WORK/original.json"; then
  ok "a second run reports up to date and rewrites neither the file nor the backup"
else
  fail "second run: rc=$rc out=$out"
fi

echo "=== 3. opt-in gate ==="
out=$(merge --upgrade "$TEMPLATE" "$WORK/absent/settings.json"); rc=$?
if [ "$rc" -eq 0 ] && [[ "$out" == *skipped* ]] && [ ! -e "$WORK/absent" ]; then
  ok "no settings file: skipped, and nothing is created"
else
  fail "missing file: rc=$rc out=$out"
fi
mkdir -p "$WORK/foreign"
F="$WORK/foreign/settings.json"
write_foreign "$F"
cp "$F" "$WORK/foreign.json"
out=$(merge --upgrade "$TEMPLATE" "$F"); rc=$?
if [ "$rc" -eq 0 ] && cmp -s "$F" "$WORK/foreign.json" && [ ! -e "$F.pre-muxcode" ]; then
  ok "--upgrade leaves a file with no muxcode hook alone — a build never opts a user in"
else
  fail "--upgrade touched a foreign file: rc=$rc out=$out"
fi
out=$(merge "$TEMPLATE" "$F"); rc=$?
if [ "$rc" -eq 0 ] && has_failure_hook "$F" \
    && jq -e 'any(.hooks.PreToolUse[]; .matcher == "Read" and any(.hooks[]; .command == "my-own-hook"))' "$F" >/dev/null; then
  ok "control: the install road (no --upgrade) merges into that same file"
else
  fail "install road did not merge: rc=$rc out=$out"
fi

echo "=== 4. python3 fallback (jq hidden) ==="
mkdir -p "$WORK/py"
P="$WORK/py/settings.json"
write_fixture "$P"
out=$(PATH="$NOJQ_PATH" merge --upgrade "$TEMPLATE" "$P"); rc=$?
if [ "$rc" -eq 0 ] && same_json "$P" "$WORK/jq-merged.json"; then
  ok "python3's merge equals jq's on the same fixture"
else
  fail "python3 and jq disagree: rc=$rc out=$out diff: $(diff <(jq -S . "$P") <(jq -S . "$WORK/jq-merged.json") | head -5)"
fi
cp "$P" "$WORK/py-merged.json"
out=$(PATH="$NOJQ_PATH" merge --upgrade "$TEMPLATE" "$P"); rc=$?
if [ "$rc" -eq 0 ] && [[ "$out" == *"already up to date"* ]] && cmp -s "$P" "$WORK/py-merged.json"; then
  ok "python3: a second run reports up to date and leaves the file byte-identical"
else
  fail "python3 second run: rc=$rc out=$out"
fi
write_foreign "$F"
out=$(PATH="$NOJQ_PATH" merge --upgrade "$TEMPLATE" "$F"); rc=$?
if [ "$rc" -eq 0 ] && cmp -s "$F" "$WORK/foreign.json"; then
  ok "python3 honours the --upgrade gate"
else
  fail "python3 --upgrade touched a foreign file: rc=$rc out=$out"
fi

echo "=== 5. neither tool ==="
write_fixture "$P"
cp "$P" "$WORK/py-before.json"
out=$(PATH="$NOTOOL_PATH" merge --upgrade "$TEMPLATE" "$P"); rc=$?
if [ "$rc" -eq 0 ] && [[ "$out" == *"neither jq nor python3"* ]] && cmp -s "$P" "$WORK/py-before.json"; then
  ok "with neither jq nor python3 the merge is skipped with a note, file untouched"
else
  fail "no-tool road: rc=$rc out=$out"
fi

echo "=== 6. write failure ==="
mkdir -p "$WORK/ro"
R="$WORK/ro/settings.json"
write_fixture "$R"
cp "$R" "$WORK/ro-before.json"
chmod 555 "$WORK/ro"
out=$(merge --upgrade "$TEMPLATE" "$R"); rc=$?
chmod 755 "$WORK/ro"
if [ "$rc" -eq 1 ] && cmp -s "$R" "$WORK/ro-before.json" && [ ! -e "$R.pre-muxcode" ] && [[ "$out" == *"left unchanged"* ]]; then
  ok "an unwritable directory exits 1 and leaves the file intact"
else
  fail "write failure: rc=$rc out=$out"
fi

echo "=== 7. concurrent writers ==="
REAL_JQ=$(command -v jq)
RACE_BIN="$WORK/bin-racejq"
mkdir -p "$RACE_BIN" "$WORK/race"
C="$WORK/race/settings.json"
write_fixture "$C"
cat >"$RACE_BIN/jq" <<EOF
#!/usr/bin/env bash
"$REAL_JQ" "\$@"; rc=\$?
case " \$* " in
  *" --slurpfile mc "*)
    if [ -L "$C.muxcode-lock" ] && [ ! -e "$WORK/race/fired" ]; then
      : >"$WORK/race/fired"
      "$REAL_JQ" '.concurrentEdit = true' "$C" >"$C.edit" && mv "$C.edit" "$C"
    fi ;;
esac
exit \$rc
EOF
chmod +x "$RACE_BIN/jq"
out=$(PATH="$RACE_BIN:$PATH" merge --upgrade "$TEMPLATE" "$C"); rc=$?
if [ "$rc" -eq 0 ] && [ -e "$WORK/race/fired" ] && has_failure_hook "$C" \
    && jq -e '.concurrentEdit == true' "$C" >/dev/null 2>&1; then
  ok "an edit landing under the lock (Claude ignores it) survives — the stale snapshot is re-merged, never written over it"
else
  fail "mid-merge edit: rc=$rc fired=$([ -e "$WORK/race/fired" ] && echo yes || echo no) out=$out"
fi

mkdir -p "$WORK/many"
M="$WORK/many/settings.json"
write_fixture "$M"
pids=()
for i in 1 2 3 4 5 6; do
  merge --upgrade "$TEMPLATE" "$M" >"$WORK/many/out.$i" &
  pids+=($!)
done
all_exited_0=true
for p in "${pids[@]}"; do wait "$p" || all_exited_0=false; done
leftovers=$(ls "$WORK/many" | grep -c 'muxcode-' || true)
if $all_exited_0 && same_json "$M" "$WORK/jq-merged.json" && [ "$leftovers" -eq 0 ]; then
  ok "six concurrent merges all exit 0, converge on the single-run result, and leave no staging file"
else
  fail "concurrent merges: all_exited_0=$all_exited_0 leftovers=$leftovers $(cat "$WORK"/many/out.* | sort | uniq -c | tr '\n' ' ')"
fi

TEMPLATE_B="$WORK/template-b.json"
printf '%s\n' '{"hooks": {"PostToolUse": [{"matcher": "Glob", "hooks": [{"type": "command", "command": "muxcode-extra-b"}]}]}, "permissions": {"allow": ["Bash(extra-b *)"]}}' >"$TEMPLATE_B"
REAL_CMP=$(command -v cmp)
SLOW_BIN="$WORK/bin-slowcmp"
mkdir -p "$SLOW_BIN" "$WORK/two"
cat >"$SLOW_BIN/cmp" <<EOF
#!/usr/bin/env bash
"$REAL_CMP" "\$@"; rc=\$?
sleep 0.5
exit \$rc
EOF
chmod +x "$SLOW_BIN/cmp"
T="$WORK/two/settings.json"
write_fixture "$T"
PATH="$SLOW_BIN:$PATH" merge --upgrade "$TEMPLATE" "$T" >"$WORK/two/out.a" & pa=$!
PATH="$SLOW_BIN:$PATH" merge --upgrade "$TEMPLATE_B" "$T" >"$WORK/two/out.b" & pb=$!
wait "$pa"; ra=$?
wait "$pb"; rb=$?
if [ "$ra" -eq 0 ] && [ "$rb" -eq 0 ] && has_failure_hook "$T" \
    && jq -e 'any(.hooks.PostToolUse[]; .matcher == "Glob" and any(.hooks[]; .command == "muxcode-extra-b"))
              and (.permissions.allow | index("Bash(extra-b *)"))' "$T" >/dev/null 2>&1; then
  ok "two merges racing with different templates keep both additions (cmp slowed to hold the race open)"
else
  fail "different-template race: ra=$ra rb=$rb a=$(cat "$WORK/two/out.a") b=$(cat "$WORK/two/out.b") failure_hook=$(has_failure_hook "$T" && echo yes || echo no) extra_b=$(jq -c '[.hooks.PostToolUse[] | select(.matcher == "Glob")]' "$T")"
fi

mkdir -p "$WORK/locks"
LD="$WORK/locks/dead.json"
write_fixture "$LD"
cp "$LD" "$WORK/locks/dead-before.json"
bash -c 'exit 0' & dead=$!
wait "$dead"
ln -s "$dead" "$LD.muxcode-lock"
started=$SECONDS
out=$(merge --upgrade "$TEMPLATE" "$LD"); rc=$?
if [ "$rc" -eq 1 ] && [[ "$out" == *"which has exited"* ]] && [ $((SECONDS - started)) -lt 5 ] \
    && cmp -s "$LD" "$WORK/locks/dead-before.json" && [ "$(readlink "$LD.muxcode-lock")" = "$dead" ]; then
  ok "a dead holder's lock is never broken — the merge fails at once naming it, file and lock untouched"
else
  fail "dead-holder lock: rc=$rc out=$out lock=$(readlink "$LD.muxcode-lock" 2>/dev/null || echo gone)"
fi
rm -f "$LD.muxcode-lock"

LL="$WORK/locks/live.json"
write_fixture "$LL"
cp "$LL" "$WORK/locks/live-before.json"
ln -s $$ "$LL.muxcode-lock"
out=$(merge --upgrade "$TEMPLATE" "$LL"); rc=$?
if [ "$rc" -eq 1 ] && [[ "$out" == *"could not take"* ]] && cmp -s "$LL" "$WORK/locks/live-before.json" \
    && [ "$(readlink "$LL.muxcode-lock")" = "$$" ]; then
  ok "control: a live holder is waited on for 10 s, then the merge fails leaving file and lock alone"
else
  fail "live-holder lock: rc=$rc out=$out"
fi
rm -f "$LL.muxcode-lock"

LT="$WORK/locks/term.json"
write_fixture "$LT"
PATH="$SLOW_BIN:$PATH" "$BASH_BIN" "$HELPER" --upgrade "$TEMPLATE" "$LT" >"$WORK/locks/term.out" 2>&1 &
pt=$!
for _ in $(seq 50); do
  [ -L "$LT.muxcode-lock" ] && break
  sleep 0.1
done
saw_lock=$([ -L "$LT.muxcode-lock" ] && echo yes || echo no)
kill -TERM "$pt" 2>/dev/null
wait "$pt"; rt=$?
if [ "$saw_lock" = yes ] && [ "$rt" -ne 0 ] && [ ! -L "$LT.muxcode-lock" ] && jq empty "$LT" 2>/dev/null; then
  ok "a merge sent SIGTERM while holding the lock releases it — only SIGKILL or a crash leaves one"
else
  fail "SIGTERM release: saw_lock=$saw_lock rt=$rt lock=$(readlink "$LT.muxcode-lock" 2>/dev/null || echo gone)"
fi

echo "=== 8. make install (scratch HOME) ==="
GOCACHE_DIR=$(go env GOCACHE)
GOPATH_DIR=$(go env GOPATH)
GOENV_FILE=$(go env GOENV)
make_install() {
  local home=$1
  HOME="$home" GOCACHE="$GOCACHE_DIR" GOPATH="$GOPATH_DIR" GOENV="$GOENV_FILE" \
    make -C "$REPO" install \
      PREFIX="$home/.local" \
      CONFIGDIR="$home/.config/muxcode" \
      NVIM_CONFIGDIR="$home/.config/muxcode/nvim" \
      NVIM_PLUGIN_DIR="$home/nvim-plugin" \
      >"$home/install.log" 2>&1
}

H="$WORK/home-upgrade"
mkdir -p "$H/.claude"
write_fixture "$H/.claude/settings.json"
if make_install "$H" && has_failure_hook "$H/.claude/settings.json"; then
  ok "make install merges the failure hook into \$HOME/.claude/settings.json"
else
  fail "make install did not merge: $(tail -5 "$H/install.log")"
fi

H="$WORK/home-foreign"
mkdir -p "$H/.claude"
write_foreign "$H/.claude/settings.json"
if make_install "$H" && cmp -s "$H/.claude/settings.json" "$WORK/foreign.json"; then
  ok "make install passes --upgrade: a file with no muxcode hook is untouched"
else
  fail "make install touched a foreign file: $(tail -5 "$H/install.log")"
fi

H="$WORK/home-readonly"
mkdir -p "$H/.claude/commands"
write_fixture "$H/.claude/settings.json"
cp "$H/.claude/settings.json" "$WORK/readonly-before.json"
chmod 555 "$H/.claude"
make_install "$H"; rc=$?
chmod 755 "$H/.claude"
if [ "$rc" -eq 0 ] && grep -q "warning: muxcode hooks not merged" "$H/install.log" \
    && cmp -s "$H/.claude/settings.json" "$WORK/readonly-before.json"; then
  ok "an unwritable ~/.claude only warns: make install still exits 0"
else
  fail "read-only ~/.claude: rc=$rc $(tail -5 "$H/install.log")"
fi

echo
echo "=== results: $PASS passed, $FAIL failed (floor $EXPECTED_PASS) ==="
if [ "$FAIL" -gt 0 ]; then
  exit 1
fi
if [ "$PASS" -ne "$EXPECTED_PASS" ]; then
  echo "  FAIL  coverage floor: expected exactly $EXPECTED_PASS passes, got $PASS — a section was skipped or double-counted"
  exit 1
fi
exit 0
