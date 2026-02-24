#!/usr/bin/env bash
# muxcode-demo.sh — Record a MUXcode demo and convert to GIF
#
# Usage:
#   muxcode-demo.sh [--speed FACTOR] [--output FILE] [--scenario NAME] [--no-record]
#
# Prerequisites:
#   brew install ffmpeg gifski
#
# The script:
#   1. Starts screen recording of the tmux session
#   2. Runs the demo scenario via muxcode-agent-bus demo run
#   3. Stops recording and converts to GIF
#
set -euo pipefail

SPEED="2.0"
OUTPUT="assets/demo.gif"
SCENARIO="build-test-review"
NO_RECORD=false
TMPDIR="${TMPDIR:-/tmp}"
RECORDING="${TMPDIR}/muxcode-demo-$$.mov"
FPS=12
WIDTH=1280
MAX_SIZE_KB=5120  # 5 MB target

usage() {
  cat <<EOF
Usage: muxcode-demo.sh [OPTIONS]

Options:
  --speed FACTOR    Speed multiplier (default: 2.0)
  --output FILE     Output GIF path (default: assets/demo.gif)
  --scenario NAME   Scenario to run (default: build-test-review)
  --no-record       Run demo without recording (preview mode)
  --fps N           GIF frames per second (default: 12)
  --width N         GIF width in pixels (default: 1280)
  -h, --help        Show this help

Prerequisites:
  ffmpeg    - for video processing
  gifski    - for high-quality GIF conversion (preferred)
              Falls back to ffmpeg if gifski is not installed.

EOF
  exit 0
}

# Parse arguments
while [[ $# -gt 0 ]]; do
  case "$1" in
    --speed)
      SPEED="$2"
      shift 2
      ;;
    --output)
      OUTPUT="$2"
      shift 2
      ;;
    --scenario)
      SCENARIO="$2"
      shift 2
      ;;
    --no-record)
      NO_RECORD=true
      shift
      ;;
    --fps)
      FPS="$2"
      shift 2
      ;;
    --width)
      WIDTH="$2"
      shift 2
      ;;
    -h|--help)
      usage
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage
      ;;
  esac
done

# Check prerequisites
check_command() {
  if ! command -v "$1" &>/dev/null; then
    echo "Error: $1 is not installed." >&2
    echo "  Install with: $2" >&2
    return 1
  fi
}

check_command muxcode-agent-bus "Run ./build.sh in the muxcode repo"

if [[ "$NO_RECORD" == false ]]; then
  check_command ffmpeg "brew install ffmpeg"
fi

# Check tmux session is running
if ! tmux has-session 2>/dev/null; then
  echo "Error: No tmux session found. Start MUXcode first." >&2
  exit 1
fi

# Ensure output directory exists
output_dir="$(dirname "$OUTPUT")"
if [[ -n "$output_dir" ]] && [[ "$output_dir" != "." ]]; then
  mkdir -p "$output_dir"
fi

echo "=== MUXcode Demo Recording ==="
echo "  Scenario: ${SCENARIO}"
echo "  Speed:    ${SPEED}x"
echo "  Output:   ${OUTPUT}"
echo ""

# Preview mode — just run the demo
if [[ "$NO_RECORD" == true ]]; then
  echo "Running demo in preview mode (no recording)..."
  muxcode-agent-bus demo run "$SCENARIO" --speed "$SPEED"
  exit 0
fi

# --- Recording mode ---

# Get tmux window geometry for recording region
# On macOS, screencapture needs pixel coordinates
SESSION_NAME="$(tmux display-message -p '#S')"

cleanup() {
  # Kill recording process if still running
  if [[ -n "${RECORD_PID:-}" ]] && kill -0 "$RECORD_PID" 2>/dev/null; then
    kill "$RECORD_PID" 2>/dev/null || true
    wait "$RECORD_PID" 2>/dev/null || true
  fi
  # Clean up temp recording file
  rm -f "$RECORDING"
}
trap cleanup EXIT

echo "Starting screen recording..."
echo "  (Recording to: $RECORDING)"

# Use macOS screencapture for screen recording.
# -v = video mode, records until interrupted.
# Note: For headless/CI environments, consider using ffmpeg with x11grab instead.
if [[ "$(uname)" == "Darwin" ]]; then
  screencapture -v "$RECORDING" &
  RECORD_PID=$!
else
  # Linux fallback using ffmpeg (requires X11 or Wayland capture)
  echo "Warning: Auto-recording not supported on this platform." >&2
  echo "Use a screen recorder manually, then convert with:" >&2
  echo "  gifski --fps $FPS --width $WIDTH -o $OUTPUT <recording.mov>" >&2
  echo "" >&2
  echo "Running demo without recording..."
  muxcode-agent-bus demo run "$SCENARIO" --speed "$SPEED"
  exit 0
fi

# Brief pause to let recording stabilize
sleep 1

echo "Running demo..."
muxcode-agent-bus demo run "$SCENARIO" --speed "$SPEED"

# Brief pause after demo completes
sleep 1

echo "Stopping recording..."
kill "$RECORD_PID" 2>/dev/null || true
wait "$RECORD_PID" 2>/dev/null || true
unset RECORD_PID

# Verify recording was created
if [[ ! -f "$RECORDING" ]]; then
  echo "Error: Recording file not found at $RECORDING" >&2
  exit 1
fi

echo "Converting to GIF..."
echo "  Target: ${WIDTH}px wide, ${FPS} fps"

if command -v gifski &>/dev/null; then
  # gifski produces much better quality GIFs
  # First extract frames with ffmpeg, then assemble with gifski
  FRAMES_DIR="${TMPDIR}/muxcode-demo-frames-$$"
  mkdir -p "$FRAMES_DIR"

  ffmpeg -i "$RECORDING" -vf "fps=${FPS},scale=${WIDTH}:-1" \
    "$FRAMES_DIR/frame%04d.png" -y -loglevel warning

  gifski --fps "$FPS" --width "$WIDTH" -o "$OUTPUT" "$FRAMES_DIR"/frame*.png

  rm -rf "$FRAMES_DIR"
else
  # ffmpeg fallback — larger file, lower quality
  echo "  (gifski not found, using ffmpeg fallback — install gifski for better quality)"
  ffmpeg -i "$RECORDING" \
    -vf "fps=${FPS},scale=${WIDTH}:-1:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse" \
    -y -loglevel warning \
    "$OUTPUT"
fi

# Report result
if [[ -f "$OUTPUT" ]]; then
  size_bytes="$(wc -c < "$OUTPUT" | tr -d ' ')"
  size_kb=$((size_bytes / 1024))
  echo ""
  echo "=== Done ==="
  echo "  Output: ${OUTPUT}"
  echo "  Size:   ${size_kb} KB"
  if [[ $size_kb -gt $MAX_SIZE_KB ]]; then
    echo "  Warning: GIF exceeds ${MAX_SIZE_KB} KB target. Consider increasing --speed or reducing --width."
  fi
else
  echo "Error: GIF conversion failed." >&2
  exit 1
fi
