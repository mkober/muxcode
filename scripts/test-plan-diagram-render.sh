#!/usr/bin/env bash
# Render-only integration test for the plan-agent diagram feature (Phase 2).
#
# Validates scripts/render-diagram.sh end-to-end against every committed
# fixture in docs/media/. GENERATES IMAGE FILES ONLY — it does not attach,
# upload, or embed anything (that is Phase 6). Needs no credentials/network.
#
# CI behaviour:
#   - Live-render assertions (per fixture) run only when the matching renderer
#     (drawio / mmdc) is on PATH; otherwise they are SKIPPED (not failed).
#   - The renderer-absence and arg-validation paths are ALWAYS asserted — they
#     mock the renderer via a nonexistent DRAWIO_BIN/MMDC_BIN (PATH-shadow) and
#     need no real binary.
#   - Any image the test renders into docs/media/ is removed on exit (unless it
#     was already present), so the test leaves no uncommitted churn.
#
# Mocked (always run, no binary needed): renderer absence + install hint + no
#   partial output, arg validation (unknown source type / no args).
# Requires installed renderers (skipped when absent): the 5 fixture renders and
#   the AWS-stencil-resolution assertions.
#
# Usage: bash scripts/test-plan-diagram-render.sh
#   Honors DRAWIO_BIN / MMDC_BIN overrides (same as render-diagram.sh).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
RENDER="$REPO_ROOT/scripts/render-diagram.sh"
MEDIA="$REPO_ROOT/docs/media"

DRAWIO_BIN="${DRAWIO_BIN:-drawio}"
MMDC_BIN="${MMDC_BIN:-mmdc}"

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; DIM=$'\033[2m'; NC=$'\033[0m'
PASS=0; FAIL=0; SKIP=0

pass() { PASS=$((PASS+1)); echo "  ${GREEN}✓${NC} $1"; }
fail() { FAIL=$((FAIL+1)); echo "  ${RED}✗${NC} $1"; [ -n "${2:-}" ] && echo "    ${DIM}$2${NC}"; }
skip() { SKIP=$((SKIP+1)); echo "  ${YELLOW}∼ skip${NC} $1"; }

# Clean up any images the test creates (that were not already committed).
CREATED=()
cleanup() {
  local f
  for f in "${CREATED[@]:-}"; do [ -n "$f" ] && rm -f "$f"; done
}
trap cleanup EXIT

# track_output <path> — remember an output file for cleanup iff it is new.
# Must return 0: a bare call under `set -e` would otherwise abort the script
# when the path already exists (e.g. tracking an output that a render just made).
track_output() { [ ! -e "$1" ] && CREATED+=("$1"); return 0; }

# graphic_primitives <svg> — count icon/geometry elements in an SVG
graphic_primitives() {
  grep -oE '<(path|image|use|polygon|ellipse|rect)[ />]' "$1" 2>/dev/null | wc -l | tr -d ' '
}

# --- prerequisites ---
[ -x "$RENDER" ] || { echo "render-diagram.sh not found/executable at $RENDER"; exit 1; }
[ -d "$MEDIA" ]  || { echo "media dir not found: $MEDIA"; exit 1; }

# Detect drawio the same way render-diagram.sh does: on PATH, or (for the
# default binary name) discovered in a desktop-app install location.
detect_drawio() {
  command -v "$DRAWIO_BIN" >/dev/null 2>&1 && return 0
  [ "$DRAWIO_BIN" = "drawio" ] || return 1
  local c
  for c in \
    "/Applications/draw.io.app/Contents/MacOS/draw.io" \
    "$HOME/Applications/draw.io.app/Contents/MacOS/draw.io" \
    "/opt/drawio/drawio" "/usr/bin/drawio" "/usr/local/bin/drawio"; do
    [ -x "$c" ] && return 0
  done
  return 1
}
have_drawio=0; detect_drawio && have_drawio=1
have_mmdc=0;   command -v "$MMDC_BIN" >/dev/null 2>&1 && have_mmdc=1

echo "== render-diagram integration test =="
echo "  drawio: $([ $have_drawio -eq 1 ] && echo "$DRAWIO_BIN" || echo 'absent (render asserts skipped)')"
echo "  mmdc:   $([ $have_mmdc -eq 1 ] && echo "$MMDC_BIN" || echo 'absent (render asserts skipped)')"

# render_fixture <desc> <have?> <src-basename> <min-primitives|"">
#   Renders MEDIA/<src> with the DEFAULT output path (docs/media/<base>.<ext>),
#   asserts the emitted path exists, is non-empty, lives under docs/media/, and
#   its basename matches the source. min-primitives>0 also asserts real icon
#   geometry (AWS-stencil resolution — no missing-shape/empty placeholder).
render_fixture() {
  local desc="$1" have="$2" src="$3" minprim="${4:-}"
  local src_path="$MEDIA/$src" base out
  if [ "$have" -ne 1 ]; then skip "$desc (renderer absent)"; return; fi
  [ -s "$src_path" ] || { fail "$desc" "fixture missing: $src_path"; return; }

  base="${src%.*}"
  # predict the default output for cleanup tracking (svg, or png on fallback)
  track_output "$MEDIA/$base.svg"; track_output "$MEDIA/$base.png"

  if ! out="$("$RENDER" "$src_path" 2>/dev/null)"; then
    fail "$desc" "render-diagram.sh exited non-zero"; return
  fi
  track_output "$out"

  if [ ! -s "$out" ]; then fail "$desc" "output missing/empty: $out"; return; fi
  if [ "$(cd "$(dirname "$out")" && pwd)" != "$MEDIA" ]; then
    fail "$desc" "output not under docs/media/: $out"; return; fi
  if [ "$(basename "${out%.*}")" != "$base" ]; then
    fail "$desc" "basename mismatch: $(basename "$out") vs $base"; return; fi

  if [ -n "$minprim" ]; then
    local n; n="$(graphic_primitives "$out")"
    if [ "$n" -lt "$minprim" ]; then
      fail "$desc" "only $n graphic primitives (<$minprim) — icons may have degraded to placeholders"
      return
    fi
    pass "$desc (icons resolved: $n primitives)"
  else
    pass "$desc ($(basename "$out"))"
  fi
}

echo "-- live renders (require installed renderers) --"
render_fixture "shapes (draw.io)"            "$have_drawio" "sample-shapes.drawio"          ""
render_fixture "shapes (Mermaid)"            "$have_mmdc"   "sample-shapes.mmd"             ""
render_fixture "AWS icons (draw.io)"         "$have_drawio" "sample-aws.drawio"             "5"
render_fixture "complex non-AWS (Mermaid)"   "$have_mmdc"   "sample-architecture.mmd"       ""
render_fixture "complex AWS (draw.io)"       "$have_drawio" "sample-aws-architecture.drawio" "12"

echo "-- renderer absence + arg validation (always asserted) --"

# Missing-renderer: PATH-shadow both renderers to nonexistent binaries.
# Render to a TEMP output path so this never removes or overwrites real
# docs/media assets (the fixtures live there and must be left untouched).
missing_out="$(mktemp -u "${TMPDIR:-/tmp}/plan-diagram-missing.XXXXXX").svg"
set +e
mr_out="$(DRAWIO_BIN=drawio-nonexistent-xyz MMDC_BIN=mmdc-nonexistent-xyz \
  "$RENDER" "$MEDIA/sample-shapes.drawio" "$missing_out" 2>&1)"; mr_rc=$?
set -e
[ "$mr_rc" -ne 0 ] && pass "missing renderer exits non-zero (rc=$mr_rc)" \
  || fail "missing renderer exits non-zero" "rc=$mr_rc"
printf '%s' "$mr_out" | grep -qF "brew install --cask drawio" \
  && pass "missing drawio prints install hint" \
  || fail "missing drawio prints install hint" "$mr_out"
[ ! -e "$missing_out" ] && pass "no partial output file after missing-renderer failure" \
  || fail "no partial output file after missing-renderer failure" "$missing_out exists"

set +e
mm_out="$(MMDC_BIN=mmdc-nonexistent-xyz "$RENDER" "$MEDIA/sample-shapes.mmd" 2>&1)"; mm_rc=$?
set -e
[ "$mm_rc" -ne 0 ] && pass "missing mmdc exits non-zero (rc=$mm_rc)" \
  || fail "missing mmdc exits non-zero" "rc=$mm_rc"
printf '%s' "$mm_out" | grep -qF "@mermaid-js/mermaid-cli" \
  && pass "missing mmdc prints install hint" \
  || fail "missing mmdc prints install hint" "$mm_out"

# Arg validation: unknown source type.
badtype="$(mktemp /tmp/plan-diagram-badtype.XXXXXX.txt)"; echo x > "$badtype"
set +e
bt_out="$("$RENDER" "$badtype" 2>&1)"; bt_rc=$?
set -e
rm -f "$badtype"
[ "$bt_rc" -ne 0 ] && pass "unknown source type exits non-zero (rc=$bt_rc)" \
  || fail "unknown source type exits non-zero" "rc=$bt_rc"
printf '%s' "$bt_out" | grep -qF "unknown source type" \
  && pass "unknown source type message" \
  || fail "unknown source type message" "$bt_out"

# Arg validation: no args -> usage, non-zero.
set +e
na_out="$("$RENDER" 2>&1)"; na_rc=$?
set -e
[ "$na_rc" -ne 0 ] && pass "no-args exits non-zero (rc=$na_rc)" \
  || fail "no-args exits non-zero" "rc=$na_rc"
printf '%s' "$na_out" | grep -qF "Usage: render-diagram.sh" \
  && pass "no-args prints usage" \
  || fail "no-args prints usage" "$na_out"

echo
echo "== $PASS passed, $FAIL failed, $SKIP skipped =="
[ "$FAIL" -eq 0 ] || exit 1
