#!/usr/bin/env bash
# probe-escape-matrix.sh — drive the MUX-163 Escape-adjacency matrix at a
# real Claude Code composer and report, per shape, whether the payload's
# first character survived and whether a parked composer submitted.
#
# Live only: it launches `claude` in an isolated tmux server (-L) inside a
# scratch directory, so it needs the CLI on PATH and a logged-in account,
# and each shape that submits costs one short model turn (the cheapest
# model, MUXCODE_ESC_PROBE_MODEL, default haiku). The bus environment is
# stripped from the scratch process so its Stop hook cannot start an inbox
# listener against a live session. Run it by hand, or from the opt-in
# section of scripts/test-prompt-mode.sh (MUXCODE_ESC_PROBE_LIVE=1).
#
#   bash scripts/probe-escape-matrix.sh [--out <file>] [--keep]
#
# Every key is its own send-keys call, exactly as the bus sends them:
#   a   Escape → text                               InjectPromptText today
#   a2  Escape → dash-leading text                  the same, MUX-104's payload
#   a3  Escape → one character                      single-character payload
#   b   Escape → 100 ms → text                      a gap alone
#   c   Escape → 100 ms → C-e → 100 ms → text       the absorber preamble
#   gap Escape → N ms → text, N swept               brackets the escape window
#   d   parked text → Escape → 50 ms → Enter        verifyEnterDelivery re-submit
#   e   parked text → Escape → 100 ms → C-e → 100 ms → Enter   re-submit on the preamble
#
# Exit 2 with a SKIP line when claude or tmux is missing or the composer
# never appears; 0 otherwise — this is a probe, the verdict is the report.
set -euo pipefail

OUT=""
KEEP=0
while [ $# -gt 0 ]; do
  case $1 in
    --out) OUT=$2; shift 2 ;;
    --keep) KEEP=1; shift ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done

command -v tmux >/dev/null 2>&1 || { echo "SKIP: tmux is required"; exit 2; }
command -v claude >/dev/null 2>&1 || { echo "SKIP: claude is required"; exit 2; }

MODEL=${MUXCODE_ESC_PROBE_MODEL:-haiku}
SOCK="mux163-$$"
T="tmux -L $SOCK"
PANE=""  # resolved after new-session — base-index may be 0 or 1
WORK=$(mktemp -d /tmp/escprobe-XXXXXX)
# The default report lives OUTSIDE $WORK: cleanup deletes the scratch dir on
# exit, so a default under it would leave a run with no surviving record (the
# matrix is the whole point of the run). This path survives; --out overrides it.
[ -n "$OUT" ] || OUT=$(mktemp /tmp/esc-matrix-XXXXXX.txt)

cleanup() {
  if [ "$KEEP" -eq 1 ]; then
    echo "kept: tmux -L $SOCK attach   (workdir $WORK)"
    return
  fi
  $T kill-server 2>/dev/null || true
  rm -rf "$WORK"
}
trap cleanup EXIT

strip_ansi() { sed 's/\x1b\[[0-9;]*[A-Za-z]//g'; }
pane() { $T capture-pane -t "$PANE" -p -J 2>/dev/null | strip_ansi; }

# composer prints the text on the live composer line — the last line
# carrying the prompt glyph, which Claude Code follows with a non-breaking
# space (U+00A0), not ASCII space — with the empty-state placeholder read
# as empty. continuation prints the row under it, so an inserted newline
# is visible as text where the box border should be.
NBSP=$'\xc2\xa0'
composer() {
  local line
  line=$(pane | grep '❯' | tail -1 || true)
  line=${line#*❯}
  line=${line#"$NBSP"}
  line=${line# }
  line=$(printf '%s' "$line" | sed 's/[[:space:]]*$//')
  case $line in 'Try "'*) line="" ;; esac
  printf '%s' "$line"
}
continuation() {
  pane | grep -A1 '❯' | tail -1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | grep -v '^[─╰╭│]*$' || true
}
busy() { pane | grep -q 'esc to interrupt'; }

wait_idle() {
  local i
  for i in $(seq 1 90); do
    if ! busy && [ -z "$(composer)" ]; then return 0; fi
    sleep 1
  done
  return 1
}

clear_composer() {
  local i
  for i in 1 2 3; do
    $T send-keys -t "$PANE" C-e C-u C-a C-k
    sleep 0.4
    [ -z "$(composer)" ] && return 0
  done
  echo "  warn: composer would not clear: '$(composer)'" >&2
  return 1
}

report() {
  printf '%s\n' "$1"
  printf '%s\n' "$1" >>"$OUT"
}

# typed_shape sends Escape, an optional preamble, then the payload, and
# reports what the composer holds. $1 label, $2 payload, $3 gap seconds
# after the Escape, $4 "absorber" to send C-e (with a trailing gap).
typed_shape() {
  local label=$1 payload=$2 gap=$3 absorber=${4:-} got verdict
  $T send-keys -t "$PANE" Escape
  [ "$gap" != 0 ] && sleep "$gap"
  if [ "$absorber" = absorber ]; then
    $T send-keys -t "$PANE" C-e
    sleep 0.1
  fi
  $T send-keys -t "$PANE" -l -- "$payload"
  sleep 0.5
  got=$(composer)
  if [ "$got" = "$payload" ]; then
    verdict="first-char KEPT"
  elif [ "$got" = "${payload:1}" ]; then
    verdict="first-char LOST"
  else
    verdict="MANGLED"
  fi
  report "$label: $verdict  composer='$got'"
  clear_composer || true
}

# resubmit_shape parks a payload, then drives the re-submit sequence and
# reports whether it submitted or left the composer occupied (a newline
# shows as a continuation row). $1 label, $2 "absorber" for the preamble.
resubmit_shape() {
  local label=$1 absorber=${2:-} payload='reply with only the word ok' got cont
  $T send-keys -t "$PANE" -l -- "$payload"
  sleep 0.4
  if [ "$(composer)" != "$payload" ]; then
    report "$label: PARK FAILED composer='$(composer)'"
    clear_composer || true
    return
  fi
  $T send-keys -t "$PANE" Escape
  if [ "$absorber" = absorber ]; then
    sleep 0.1
    $T send-keys -t "$PANE" C-e
    sleep 0.1
  else
    sleep 0.05
  fi
  $T send-keys -t "$PANE" Enter
  sleep 1.5
  got=$(composer)
  cont=$(continuation)
  if [ -z "$got" ]; then
    report "$label: SUBMITTED"
    wait_idle || echo "  warn: composer still busy after submit" >&2
  else
    report "$label: NOT SUBMITTED  composer='$got' continuation='$cont'"
    clear_composer || true
  fi
}

# fine_sweep brackets the composer's escape window: for each small gap it
# types Escape → gap → text several times and reports how many trials kept
# the first character. No shape here submits, so it costs no model turn —
# the boundary between the LOST band and the KEPT band is the window.
fine_sweep() {
  local gap kept t
  for gap in 0 0.01 0.02 0.03 0.05 0.075; do
    kept=0
    for t in 1 2 3 4 5; do
      $T send-keys -t "$PANE" Escape
      [ "$gap" != 0 ] && sleep "$gap"
      $T send-keys -t "$PANE" -l -- 'hello world'
      sleep 0.4
      [ "$(composer)" = "hello world" ] && kept=$((kept + 1))
      clear_composer || true
    done
    report "window Escape → ${gap}s → text: first-char kept ${kept}/5"
  done
}

echo "=== MUX-163 escape-adjacency matrix (claude $(claude --version 2>/dev/null | head -1), tmux $(tmux -V)) ==="
: >"$OUT"
report "# MUX-163 Phase 1 — live escape-adjacency matrix on a real Claude Code composer."
report "# Generated by scripts/probe-escape-matrix.sh; re-running overwrites this file."
report "# Records, per shape, whether a payload's first character survived after an Escape,"
report "# and (window rows) the gap band that separates the LOST band from the KEPT band."
report "# claude $(claude --version 2>/dev/null | head -1) · tmux $(tmux -V) · model $MODEL · $(date '+%Y-%m-%d %H:%M')"

cat >"$WORK/launch.sh" <<EOF
#!/usr/bin/env bash
for v in \$(compgen -e | grep -E '^(MUXCODE|BUS_|AGENT_ROLE|TMUX)'); do unset "\$v"; done
exec claude --model "$MODEL"
EOF
chmod +x "$WORK/launch.sh"
mkdir -p "$WORK/home"
$T new-session -d -s probe -x 140 -y 40 -c "$WORK/home" "$WORK/launch.sh"
PANE=$($T list-panes -t probe -F '#{session_name}:#{window_index}.#{pane_index}' | head -1)

ready=0
for i in $(seq 1 60); do
  sleep 1
  if pane | grep -q 'Yes, I trust this folder'; then
    $T send-keys -t "$PANE" Down
    sleep 0.3
    $T send-keys -t "$PANE" Enter
  elif pane | grep -q '❯'; then
    ready=1
    break
  fi
done
if [ "$ready" -ne 1 ]; then
  echo "SKIP: composer never appeared"
  pane | grep -v '^[[:space:]]*$' | tail -5 | sed 's/^/  | /'
  exit 2
fi
sleep 1

typed_shape "a  Escape → text" 'hello world' 0
typed_shape "a2 Escape → dash text" '- dash probe' 0
typed_shape "a3 Escape → one char" 'x' 0
typed_shape "b  Escape → 100ms → text" 'hello world' 0.1
typed_shape "c  Escape → 100ms → C-e → 100ms → text" 'hello world' 0.1 absorber
for gap in 0.2 0.3 0.4 0.5 0.6 0.8 1.0; do
  typed_shape "gap Escape → ${gap}s → text" 'hello world' "$gap"
done
fine_sweep
resubmit_shape "d  parked → Escape → 50ms → Enter"
resubmit_shape "e  parked → Escape → 100ms → C-e → 100ms → Enter" absorber

echo "report: $OUT"
