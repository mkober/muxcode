#!/usr/bin/env bash
# Merge muxcode's Claude Code hooks and permissions into an existing settings
# file, idempotently.
#
#   merge-claude-settings.sh [--upgrade] <muxcode-settings> <claude-settings>
#
# Shared by ./install.sh and `make install` — which ./build.sh and
# `muxcode upgrade` run — so a hook added upstream reaches an existing install
# on every road. Before this, only install.sh merged, and an upgraded install
# never received PostToolUseFailure (MUX-204, PR 160).
#
# Never creates <claude-settings>: a missing file is skipped. --upgrade also
# skips a file that carries no muxcode hook, so a build never opts in a user
# who declined Claude at install. The file is rewritten only on a semantic
# change, after a backup to <claude-settings>.pre-muxcode, keeping its mode.
#
# Concurrent writers: a merge that would change the file takes a lock and
# re-reads the file under it, so builds racing with different templates add
# their entries in turn rather than the last rename discarding the first. The
# lock, <claude-settings>.muxcode-lock, is a symlink naming its holder's pid,
# created in one step and removed only by that holder's exit trap (which runs
# on SIGINT and SIGTERM too). It is never broken automatically — any check-
# then-delete can remove a new holder's lock — so a holder that has exited
# (a SIGKILL or crash inside the locked tenth of a second) fails the merge at
# once, naming the lock to remove; a live holder is waited on for 10 s. Claude
# Code ignores the lock, so the replace also happens only while the file still
# matches the snapshot the merge read, re-merging otherwise (3 attempts): only
# an edit of Claude's landing between that compare and the rename can be lost.
# A merge that changes nothing never takes the lock, so a sandbox that cannot
# write beside the file still reports it current.
#
# jq runs the merge; python3 is the fallback and must apply the same rules —
# scripts/test-claude-settings-merge.sh pins the two to identical results.
# With neither, the merge is skipped with a note.
#
# Exit: 0 merged, already current, or skipped; 1 the merge or write failed;
# 2 usage.
set -uo pipefail

upgrade=false
if [ "${1:-}" = --upgrade ]; then
  upgrade=true
  shift
fi
if [ $# -ne 2 ]; then
  echo "usage: merge-claude-settings.sh [--upgrade] <muxcode-settings> <claude-settings>" >&2
  exit 2
fi
template=$1
target=$2

case "$target" in
  "${HOME:-/nonexistent}"/*) display="~${target#"$HOME"}" ;;
  *) display=$target ;;
esac
note() { echo "Claude settings: $*"; }
die()  { echo "Claude settings: $* — $display left unchanged" >&2; exit 1; }

[ -f "$template" ] || die "template $template not found"
if [ ! -f "$target" ]; then
  note "no $display — skipped (this merge never creates one)"
  exit 0
fi

snapshot=$(mktemp "${TMPDIR:-/tmp}/muxcode-claude-settings.XXXXXX") || die "cannot create a scratch file"
merged=$(mktemp "${TMPDIR:-/tmp}/muxcode-claude-settings.XXXXXX") || die "cannot create a scratch file"
staged=""
lock="$target.muxcode-lock"
locked=false
cleanup() {
  rm -f "$snapshot" "$merged" ${staged:+"$staged"}
  if $locked && [ "$(readlink "$lock" 2>/dev/null)" = "$$" ]; then rm -f "$lock"; fi
}
trap cleanup EXIT

HAS_MUXCODE_JQ='[.hooks? // {} | .. | objects | .command? | strings | select(startswith("muxcode"))] | length > 0'

MERGE_JQ='
  def add_hook($phase; $matcher; $hook):
    if (.hooks[$phase] // [] | map(select(.matcher == $matcher)) | length) > 0 then
      .hooks[$phase] |= map(
        if .matcher == $matcher and (.hooks | map(.command) | index($hook.command) | not) then
          .hooks += [$hook]
        else . end
      )
    else
      .hooks[$phase] = ((.hooks[$phase] // []) + [{"matcher": $matcher, "hooks": [$hook]}])
    end;

  .hooks = (.hooks // {}) |
  .permissions = (.permissions // {}) |
  .permissions.allow = (.permissions.allow // []) |

  # PostToolUseFailure carries every non-zero Bash exit to hook bash (MUX-204).
  reduce ("PreToolUse", "PostToolUse", "PostToolUseFailure") as $phase (.;
    reduce ($mc[0].hooks[$phase] // [] | .[] | . as $entry | $entry.hooks[] | {m: $entry.matcher, h: .}) as $x (
      .; add_hook($phase; $x.m; $x.h)
    )
  ) |

  # Stop has no matcher: append a group per command not already under .hooks.Stop.
  reduce ($mc[0].hooks.Stop // [] | .[] | .hooks[]) as $h (
    .;
    if ((.hooks.Stop // []) | [.[].hooks[]?.command] | index($h.command)) then .
    else .hooks.Stop = ((.hooks.Stop // []) + [{"hooks": [$h]}]) end
  ) |

  # Prune rules Claude Code rejects at startup — the additive union below never would.
  .permissions.allow = (.permissions.allow - ["Write(/tmp/muxcode-*)", "Write(/private/tmp/muxcode-*)"]) |
  .permissions.deny = ((.permissions.deny // []) - ["Bash(rm -rf /)*"]) |

  .permissions.allow = (.permissions.allow + ($mc[0].permissions.allow // []) | unique) |
  .permissions.deny = ((.permissions.deny // []) + ($mc[0].permissions.deny // []) | unique)
'

# Merge $snapshot, never the live target. Prints not-installed, unchanged or
# changed; on changed, $merged holds the result.
merge_with_jq() {
  jq empty "$template" "$snapshot" || return 1
  if $upgrade && ! jq -e "$HAS_MUXCODE_JQ" "$snapshot" >/dev/null; then
    echo not-installed
    return 0
  fi
  jq --slurpfile mc "$template" "$MERGE_JQ" "$snapshot" >"$merged" || return 1
  if jq -e -n --slurpfile a "$snapshot" --slurpfile b "$merged" '$a[0] == $b[0]' >/dev/null; then
    echo unchanged
  else
    echo changed
  fi
}

merge_with_python() {
  python3 - "$template" "$snapshot" "$merged" "$upgrade" <<'PY'
import json, sys

template, snapshot, out, upgrade = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4] == "true"
with open(template) as f:
    mc = json.load(f)
with open(snapshot) as f:
    s = json.load(f)
original = json.loads(json.dumps(s))


def commands(node):
    if isinstance(node, dict):
        if isinstance(node.get("command"), str):
            yield node["command"]
        for v in node.values():
            yield from commands(v)
    elif isinstance(node, list):
        for v in node:
            yield from commands(v)


if upgrade and not any(c.startswith("muxcode") for c in commands(s.get("hooks") or {})):
    print("not-installed")
    sys.exit(0)

s["hooks"] = hooks = s.get("hooks") or {}
s["permissions"] = perms = s.get("permissions") or {}
perms["allow"] = perms.get("allow") or []


def add_hook(phase, matcher, hook):
    groups = hooks.get(phase) or []
    if any(g.get("matcher") == matcher for g in groups):
        for g in groups:
            present = [h.get("command") for h in g.get("hooks") or []]
            if g.get("matcher") == matcher and hook.get("command") not in present:
                g["hooks"] = (g.get("hooks") or []) + [hook]
        hooks[phase] = groups
    else:
        hooks[phase] = groups + [{"matcher": matcher, "hooks": [hook]}]


mc_hooks = mc.get("hooks") or {}
for phase in ("PreToolUse", "PostToolUse", "PostToolUseFailure"):
    for entry in mc_hooks.get(phase) or []:
        for hook in entry.get("hooks") or []:
            add_hook(phase, entry.get("matcher"), hook)

for entry in mc_hooks.get("Stop") or []:
    for hook in entry.get("hooks") or []:
        present = [h.get("command") for g in hooks.get("Stop") or [] for h in g.get("hooks") or []]
        if hook.get("command") not in present:
            hooks["Stop"] = (hooks.get("Stop") or []) + [{"hooks": [hook]}]

mc_perms = mc.get("permissions") or {}
rejected = ("Write(/tmp/muxcode-*)", "Write(/private/tmp/muxcode-*)")
perms["allow"] = [a for a in perms["allow"] if a not in rejected]
perms["deny"] = [d for d in perms.get("deny") or [] if d != "Bash(rm -rf /)*"]
perms["allow"] = sorted(set(perms["allow"] + (mc_perms.get("allow") or [])))
perms["deny"] = sorted(set(perms["deny"] + (mc_perms.get("deny") or [])))

if s == original:
    print("unchanged")
    sys.exit(0)
with open(out, "w") as f:
    json.dump(s, f, indent=2, ensure_ascii=False)
    f.write("\n")
print("changed")
PY
}

if command -v jq >/dev/null 2>&1; then
  tool=jq merge=merge_with_jq
elif command -v python3 >/dev/null 2>&1; then
  tool=python3 merge=merge_with_python
else
  note "neither jq nor python3 found — merge skipped; install one and re-run ./install.sh"
  exit 0
fi

merge_once() {
  cp "$target" "$snapshot" || die "cannot read $display"
  status=$($merge) || die "$tool merge failed"
}

# Exits on not-installed or unchanged; returns only when the merge changed something.
exit_unless_changed() {
  case "$status" in
    not-installed)
      note "$display carries no muxcode hooks — left alone (./install.sh adds them)"
      exit 0
      ;;
    unchanged)
      note "$display already up to date"
      exit 0
      ;;
    changed) ;;
    *) die "unexpected merge status '$status'" ;;
  esac
}

acquire_lock() {
  local tries=0 holder=""
  until ln -sn "$$" "$lock" 2>/dev/null; do
    if holder=$(readlink "$lock" 2>/dev/null); then
      kill -0 "$holder" 2>/dev/null \
        || die "$display.muxcode-lock is held by pid $holder, which has exited — remove it if no build is running"
    elif [ ! -L "$lock" ] && [ ! -w "$(dirname "$target")" ]; then
      die "cannot write beside $display"
    fi
    tries=$((tries + 1))
    [ "$tries" -le 100 ] || die "could not take $display.muxcode-lock within 10 s (held by pid ${holder:-?})"
    sleep 0.1
  done
  locked=true
}

merge_once
exit_unless_changed
acquire_lock
for attempt in 1 2 3; do
  merge_once
  exit_unless_changed
  staged=$(mktemp "$target.muxcode-XXXXXX") || die "cannot write beside $display"
  cp -p "$target" "$staged" && cat "$merged" >"$staged" || die "cannot write beside $display"
  if cmp -s "$snapshot" "$target"; then
    cp -p "$target" "$target.pre-muxcode" || die "cannot back up $display"
    mv "$staged" "$target" || die "cannot replace $display"
    note "updated $display (backup: $(basename "$target").pre-muxcode)"
    exit 0
  fi
  rm -f "$staged"
done
die "$display kept changing during $attempt merge attempts"
