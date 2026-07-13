#!/usr/bin/env bash
# Render a draw.io / mxGraph XML or Mermaid diagram source to an image.
#
# Auto-selects the renderer from the SOURCE extension and infers the output
# format from the OUTPUT extension (SVG default, PNG supported, with automatic
# SVG->PNG fallback when SVG rendering is unavailable for the detected renderer):
#
#   .drawio / .xml    -> drawio  (bundles the AWS mxgraph.aws4.* stencils)
#   .mmd / .mermaid   -> mmdc    (mermaid-cli)
#
# Output lands under docs/media/ by default (deterministic, version-controlled,
# shared across req docs, Jira stories, and Confluence pages) with a basename
# matching the source. Renders to a temp file first, so a failed render never
# leaves a partial output file behind. Prints the final output path on stdout.
#
# Usage: render-diagram.sh <source> [output]
#   <source>  diagram source (.drawio/.xml/.mmd/.mermaid)
#   [output]  image path (.svg or .png); default docs/media/<source>.svg
#
# Env overrides:
#   DRAWIO_BIN             drawio binary (default: drawio, else auto-discovered
#                          from the macOS/Linux desktop-app install location)
#   MMDC_BIN               mmdc binary   (default: mmdc)
#   DRAWIO_ARGS           extra args appended to the drawio export command
#   MMDC_ARGS             extra args appended to the mmdc command
#   MMDC_PUPPETEER_CONFIG  puppeteer config JSON, passed to mmdc via -p
set -euo pipefail

GREEN=$'\033[0;32m'; RED=$'\033[0;31m'; YELLOW=$'\033[0;33m'; DIM=$'\033[2m'; NC=$'\033[0m'

# Log to stderr so stdout carries only the final output path (caller-consumable).
log()  { printf '%s\n' "$*" >&2; }
info() { printf '%s%s%s\n' "$DIM" "$*" "$NC" >&2; }
warn() { printf '%s%s%s\n' "$YELLOW" "$*" "$NC" >&2; }
err()  { printf '%s%s%s\n' "$RED" "$*" "$NC" >&2; }
ok()   { printf '%s%s%s\n' "$GREEN" "$*" "$NC" >&2; }

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEFAULT_MEDIA_DIR="$REPO_ROOT/docs/media"

# Track whether the caller explicitly set DRAWIO_BIN. If so we respect it
# verbatim (no app-bundle fallback) — this keeps PATH-shadow / missing-renderer
# tests deterministic. If unset, we probe common desktop-app install locations
# below, because `brew install --cask drawio` installs the binary inside the
# app bundle rather than on PATH.
if [ -n "${DRAWIO_BIN:-}" ]; then DRAWIO_BIN_EXPLICIT=1; else DRAWIO_BIN_EXPLICIT=0; fi
DRAWIO_BIN="${DRAWIO_BIN:-drawio}"
MMDC_BIN="${MMDC_BIN:-mmdc}"

usage() {
  cat >&2 <<EOF
Usage: render-diagram.sh <source> [output]

  <source>  diagram source (.drawio/.xml/.mmd/.mermaid)
  [output]  image path (.svg or .png); default: docs/media/<source>.svg

Renders <source> to an image, auto-selecting the renderer by source type:
  .drawio/.xml   -> drawio  (bundles AWS mxgraph.aws4.* stencils)
  .mmd/.mermaid  -> mmdc

Output format is inferred from the output extension (svg default; png
supported). A failed SVG render falls back to PNG automatically. The final
output path is printed on stdout.
EOF
}

# lowercase_ext <path> -> extension without the dot, lowercased
lowercase_ext() {
  local name ext
  name="$(basename -- "$1")"
  ext="${name##*.}"
  [ "$ext" = "$name" ] && ext=""   # no dot in name
  printf '%s' "$ext" | tr '[:upper:]' '[:lower:]'
}

# renderer_for_source <ext> -> "drawio" | "mmdc" | "" (unknown)
renderer_for_source() {
  case "$1" in
    drawio|xml)     printf 'drawio' ;;
    mmd|mermaid)    printf 'mmdc' ;;
    *)              printf '' ;;
  esac
}

# --- argument parsing ---
if [ "$#" -lt 1 ]; then
  usage
  exit 2
fi
case "$1" in
  -h|--help) usage; exit 0 ;;
esac

SRC="$1"
OUT="${2:-}"

if [ ! -f "$SRC" ]; then
  err "source not found: $SRC"
  exit 1
fi
if [ ! -s "$SRC" ]; then
  err "source is empty: $SRC"
  exit 1
fi

SRC_EXT="$(lowercase_ext "$SRC")"
RENDERER="$(renderer_for_source "$SRC_EXT")"
if [ -z "$RENDERER" ]; then
  err "unknown source type: '.$SRC_EXT' (expected .drawio/.xml/.mmd/.mermaid)"
  exit 1
fi

# --- resolve output path + format ---
# Default output: docs/media/<source-basename>.svg
if [ -z "$OUT" ]; then
  src_base="$(basename -- "$SRC")"
  OUT="$DEFAULT_MEDIA_DIR/${src_base%.*}.svg"
fi

OUT_EXT="$(lowercase_ext "$OUT")"
case "$OUT_EXT" in
  svg) FORMAT="svg" ;;
  png) FORMAT="png" ;;
  "")  err "output has no extension: $OUT (use .svg or .png)"; exit 1 ;;
  *)   err "unsupported output format: '.$OUT_EXT' (use .svg or .png)"; exit 1 ;;
esac

# Print the resolved plan before checking the renderer, so the output path is
# observable even when the renderer is absent.
info "source: $SRC (.$SRC_EXT -> $RENDERER)"
info "output: $OUT ($FORMAT)"

# resolve_drawio_bin — locate the drawio binary. When DRAWIO_BIN was not set
# explicitly and 'drawio' is not on PATH, probe common desktop-app install
# locations (the macOS cask installs the binary inside the app bundle, not on
# PATH). Updates DRAWIO_BIN in place. Returns non-zero if none is found.
resolve_drawio_bin() {
  command -v "$DRAWIO_BIN" >/dev/null 2>&1 && return 0
  [ "$DRAWIO_BIN_EXPLICIT" -eq 1 ] && return 1   # respect an explicit override
  local c
  for c in \
    "/Applications/draw.io.app/Contents/MacOS/draw.io" \
    "$HOME/Applications/draw.io.app/Contents/MacOS/draw.io" \
    "/opt/drawio/drawio" \
    "/usr/bin/drawio" \
    "/usr/local/bin/drawio"; do
    if [ -x "$c" ]; then DRAWIO_BIN="$c"; return 0; fi
  done
  return 1
}

# --- renderer availability ---
require_renderer() {
  case "$RENDERER" in
    drawio)
      if ! resolve_drawio_bin; then
        err "renderer not found: '$DRAWIO_BIN' is required to render $SRC_EXT sources."
        err "  install hint: brew install --cask drawio   (or set DRAWIO_BIN to the binary path)"
        exit 1
      fi ;;
    mmdc)
      if ! command -v "$MMDC_BIN" >/dev/null 2>&1; then
        err "renderer not found: '$MMDC_BIN' is required to render $SRC_EXT sources."
        err "  install hint: npm i -g @mermaid-js/mermaid-cli   (or set MMDC_BIN to the binary path)"
        exit 1
      fi ;;
  esac
}
require_renderer

# --- render (temp file first -> atomic move; no partial output on failure) ---
TMPDIR_RENDER="$(mktemp -d "${TMPDIR:-/tmp}/render-diagram.XXXXXX")"
cleanup() { rm -rf "$TMPDIR_RENDER"; }
trap cleanup EXIT

# render_once <format> <dest> -> 0 if a non-empty file was produced
render_once() {
  local fmt="$1" dest="$2"
  case "$RENDERER" in
    drawio)
      # shellcheck disable=SC2086
      "$DRAWIO_BIN" --export --format "$fmt" --output "$dest" ${DRAWIO_ARGS:-} "$SRC" >&2 2>&1 || return 1
      ;;
    mmdc)
      # mmdc infers the format from the output extension. Avoid empty-array
      # expansion (bash 3.2 / macOS treats "${arr[@]}" on an empty array as an
      # unbound variable under `set -u`), so branch on the optional -p config.
      # shellcheck disable=SC2086
      if [ -n "${MMDC_PUPPETEER_CONFIG:-}" ]; then
        "$MMDC_BIN" -i "$SRC" -o "$dest" -p "$MMDC_PUPPETEER_CONFIG" ${MMDC_ARGS:-} >&2 2>&1 || return 1
      else
        "$MMDC_BIN" -i "$SRC" -o "$dest" ${MMDC_ARGS:-} >&2 2>&1 || return 1
      fi
      ;;
  esac
  [ -s "$dest" ]
}

tmp_out="$TMPDIR_RENDER/$(basename -- "$OUT")"

if render_once "$FORMAT" "$tmp_out"; then
  final_out="$OUT"
elif [ "$FORMAT" = "svg" ]; then
  # SVG failed -> fall back to PNG (per spec: prefer SVG, fall back to PNG).
  warn "SVG render failed for $RENDERER; falling back to PNG"
  final_out="${OUT%.svg}.png"
  tmp_out="$TMPDIR_RENDER/$(basename -- "$final_out")"
  if ! render_once "png" "$tmp_out"; then
    err "render failed (SVG and PNG): $SRC"
    exit 1
  fi
else
  err "render failed: $SRC"
  exit 1
fi

mkdir -p "$(dirname -- "$final_out")"
mv -f "$tmp_out" "$final_out"
ok "rendered -> $final_out"

# stdout: final path only (caller-consumable)
printf '%s\n' "$final_out"
