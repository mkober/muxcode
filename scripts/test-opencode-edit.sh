#!/usr/bin/env bash
# test-opencode-edit.sh — Integration test for OpenCode edit agent config generation
#
# Verifies that MUXCODE_EDIT_CLI=opencode produces correct .opencode/agents/edit.md
# with proper permissions, deny rules, model, and adapted body.
#
# Usage: bash scripts/test-opencode-edit.sh
#
# Does NOT require a running muxcode session — runs standalone.

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
NC='\033[0m'

pass=0
fail=0
total=0

assert() {
  local desc="$1"
  local result="$2"
  total=$((total + 1))
  if [ "$result" = "0" ]; then
    echo -e "  ${GREEN}PASS${NC} $desc"
    pass=$((pass + 1))
  else
    echo -e "  ${RED}FAIL${NC} $desc"
    fail=$((fail + 1))
  fi
}

# Work in a temp directory to avoid polluting the project
WORK_DIR=$(mktemp -d /tmp/muxcode-test-opencode-edit-XXXXXX)
cleanup() { rm -rf "$WORK_DIR"; }
trap cleanup EXIT

# Copy minimal project structure so agent resolution works
PROJECT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cp -r "$PROJECT_DIR/agents" "$WORK_DIR/agents" 2>/dev/null || true
mkdir -p "$WORK_DIR/.opencode/agents"

cd "$WORK_DIR"

echo "=== OpenCode Edit Agent Config Test ==="
echo ""

# Generate the config via the Go binary
export MUXCODE_EDIT_CLI=opencode
export MUXCODE_EDIT_MODEL=""  # use default

# Use writeOpenCodeAgentConfig via the agent config subcommand
if ! muxcode agent config edit 2>/dev/null; then
  echo -e "${RED}FAIL${NC}: Could not generate .opencode/agents/edit.md"
  echo "  'muxcode agent config edit' failed"
  exit 1
fi

CONFIG=".opencode/agents/edit.md"

if [ ! -f "$CONFIG" ]; then
  echo -e "${RED}FAIL${NC}: $CONFIG not found"
  exit 1
fi

echo "Generated: $CONFIG"
echo ""

# --- Permission checks ---

echo "--- Permissions ---"

# Write/Edit allows
grep -q 'edit: allow' "$CONFIG"
assert "edit: allow (Write/Edit permission)" "$?"

# Bash allow patterns
grep -q '"muxcode \*": allow' "$CONFIG"
assert "muxcode allow pattern" "$?"

grep -q '"tree \*": allow' "$CONFIG"
assert "tree allow pattern" "$?"

# External directory
grep -q 'external_directory: allow' "$CONFIG"
assert "external_directory: allow" "$?"

echo ""
echo "--- Deny Rules ---"

# Git write operations
grep -q '"git commit\*": deny' "$CONFIG"
assert "deny: git commit" "$?"

grep -q '"git push\*": deny' "$CONFIG"
assert "deny: git push" "$?"

grep -q '"git checkout\*": deny' "$CONFIG"
assert "deny: git checkout" "$?"

# GitHub CLI
grep -q '"gh \*": deny' "$CONFIG"
assert "deny: gh (GitHub CLI)" "$?"

# Build commands
grep -q '"./build.sh\*": deny' "$CONFIG"
assert "deny: ./build.sh" "$?"

grep -q '"make\*": deny' "$CONFIG"
assert "deny: make" "$?"

# Test commands
grep -q '"go test\*": deny' "$CONFIG"
assert "deny: go test" "$?"

grep -q '"pytest\*": deny' "$CONFIG"
assert "deny: pytest" "$?"

# Deploy commands
grep -q '"cdk synth\*": deny' "$CONFIG"
assert "deny: cdk synth" "$?"

# AWS operations
grep -q '"aws lambda\*": deny' "$CONFIG"
assert "deny: aws lambda" "$?"

grep -q '"aws s3\*": deny' "$CONFIG"
assert "deny: aws s3" "$?"

# curl
grep -q '"curl\*": deny' "$CONFIG"
assert "deny: curl" "$?"

echo ""
echo "--- Model ---"

grep -q 'model: opencode-go/deepseek-v4-pro' "$CONFIG"
assert "model: opencode-go/deepseek-v4-pro" "$?"

echo ""
echo "--- Body Adaptation ---"

# Should NOT contain original hook guard reference
if grep -q 'enforces this at the tool level' "$CONFIG"; then
  assert "hook guard reference removed" "1"
else
  assert "hook guard reference removed" "0"
fi

# Should contain self-enforcement instruction
grep -q 'MUST self-enforce delegation rules' "$CONFIG"
assert "self-enforcement instruction present" "$?"

# Should contain manual orchestration
grep -q 'MUST manually orchestrate' "$CONFIG"
assert "manual orchestration instruction present" "$?"

echo ""
echo "--- SharedPrompt ---"

# Should contain manual bus messaging section
grep -q 'Manual Bus Messaging' "$CONFIG"
assert "manual bus messaging section" "$?"

# Should contain orchestration commands
grep -q 'muxcode send build build' "$CONFIG"
assert "build orchestration command" "$?"

# Should NOT contain /compact (Claude Code specific)
if grep -q '/compact' "$CONFIG"; then
  assert "no /compact reference (OpenCode)" "1"
else
  assert "no /compact reference (OpenCode)" "0"
fi

# Should contain auto-compaction
grep -q 'auto-compaction' "$CONFIG"
assert "auto-compaction reference" "$?"

# Should contain startup context
grep -q 'muxcode memory context' "$CONFIG"
assert "startup memory context instruction" "$?"

echo ""
echo "=== Results: $pass/$total passed, $fail failed ==="

if [ "$fail" -gt 0 ]; then
  exit 1
fi
