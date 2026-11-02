#!/usr/bin/env bash
# probe-xcode-sandbox.sh — MUX-207 Phase 1: measure what Swift and Xcode
# commands need under the Codex sandbox exactly as muxcode launches the build
# and test roles, before any sandbox fix is chosen.
#
# Each cell runs one command in a fresh copy of a scratch fixture — a SwiftPM
# package nested at packages/ProbeKit and an XcodeGen iOS app at apps/ios that
# depends on it, tibo's layout — through `codex sandbox`, Codex's own Seatbelt
# runner, with the role's real launch policy. The policy is rebuilt from
# codexSandboxArgs' inputs and proven identical by hashing it the way
# LaunchSandboxFingerprint does and comparing with the installed binary's
# `muxcode agent sandbox-fingerprint`; a mismatch exits 1, because a matrix run
# under a different policy measures nothing.
#
#   bash scripts/probe-xcode-sandbox.sh [--role build|test]... [--only <cmd>]...
#                                       [--denials] [--keep] [--out <file>]
#                                       [--login]
#
# Since Phase 4 the launch policy carries the swift/xcodebuild shims (BASH_ENV
# → <install>/codex-shim/bash_env) and the SwiftPM cache root, so `base`
# measures the fixed policy once that binary is installed. --login runs every
# cell as `/bin/bash -lc`, the shape Codex itself uses, whose login profile may
# reset PATH; without it cells run `bash -c`, as Phase 1 measured.
#
# Codex agents launch with no sandbox by default since 2026-11-02, so the
# probe pins the MUXCODE_*_CODEX_SANDBOX=workspace-write opt-out it measures.
#
# Commands — build: swift-build swift-run xcodegen xcodebuild simctl
#            test:  swift-test simctl
# Variants, cheapest candidate first; `host` is the unsandboxed control, so a
# command failing there too is an environment fact, not a sandbox one:
#   base         the role's launch sandbox, unchanged
#   reloc        + caches relocated by env (CLANG_MODULE_CACHE_PATH,
#                  SWIFTPM_MODULECACHE_OVERRIDE, SWIFTPM_TESTS_MODULECACHE)
#                  into /tmp, the codexPackageCacheEnv shape
#   roots        + writable roots: DARWIN_USER_CACHE_DIR and the SwiftPM and
#                  Xcode home caches that exist
#   nosbx        + SwiftPM --disable-sandbox / xcodebuild
#                  -IDEPackageSupportDisable{Manifest,PluginExecution}Sandbox=YES
#   nosbx+reloc, nosbx+roots   the combinations
#   host         no sandbox
# xcodegen and simctl take no sandbox flag, so their nosbx* cells are not
# applicable — identical to the cell without the flag, whether that passed or
# failed — and are not run (shown `=`).
#
# Writes outside the scratch directory (removed unless --keep): the roots
# cells are granted, and the host cells run with, the real home caches —
# ~/Library/Caches/org.swift.swiftpm, ~/Library/org.swift.swiftpm, ~/.swiftpm,
# DARWIN_USER_CACHE_DIR (clang module cache), Xcode DerivedData and
# ~/Library/Logs/CoreSimulator — and may populate or modify them. Fresh fixture
# copies isolate only in-repo state (.build, the in-repo -derivedDataPath);
# home caches carry across cells in run order and from earlier runs, so a cell
# may read entries another cell wrote. The matrix measures access, not cold-
# cache behaviour. Output is PII-safe: no environment is printed, and $HOME,
# the user name and the per-user /var/folders directory are masked in every
# failure line.
# Exit 2 with a SKIP line when codex, swift, xcodebuild or xcodegen is
# missing; 1 on a fingerprint mismatch; 0 otherwise — the report is the verdict.
set -euo pipefail

ROLES=()
ONLY=()
DENIALS=0
KEEP=0
SHELL_FLAG=-c
OUT=""
while [ $# -gt 0 ]; do
  case $1 in
    --role) ROLES+=("$2"); shift 2 ;;
    --only) ONLY+=("$2"); shift 2 ;;
    --denials) DENIALS=1; shift ;;
    --keep) KEEP=1; shift ;;
    --login) SHELL_FLAG=-lc; shift ;;
    --out) OUT=$2; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 1 ;;
  esac
done
[ ${#ROLES[@]} -gt 0 ] || ROLES=(build test)
export MUXCODE_BUILD_CODEX_SANDBOX=workspace-write MUXCODE_TEST_CODEX_SANDBOX=workspace-write

for tool in codex swift xcodebuild xcodegen muxcode; do
  command -v "$tool" >/dev/null 2>&1 || { echo "SKIP: $tool is required"; exit 2; }
done

TIMEOUT=${MUXCODE_XCODE_PROBE_TIMEOUT:-300}
WORK=$(mktemp -d /tmp/xcode-probe-XXXXXX)
WORK=$(cd "$WORK" && pwd -P)
[ -n "$OUT" ] || OUT=$(mktemp /tmp/xcode-probe-report-XXXXXX)
DARWIN_CACHE=$(getconf DARWIN_USER_CACHE_DIR 2>/dev/null || true)
DARWIN_CACHE=${DARWIN_CACHE%/}
DARWIN_BASE=$(dirname "${DARWIN_CACHE:-/nonexistent/x}")

cleanup() {
  if [ "$KEEP" -eq 1 ]; then echo "kept: $WORK"; else rm -rf "$WORK"; fi
}
trap cleanup EXIT

say() { echo "$*" | tee -a "$OUT"; }

mask() {
  sed -E -e "s#(/private)?${DARWIN_BASE}#\$DARWIN_DIR#g" -e "s#${HOME}#~#g" -e "s#${USER:-__nouser__}#<user>#g" -e "s#${WORK}#\$WORK#g"
}

# physical_dir prints a directory's symlink-free path, or nothing when it does
# not exist — resolveWritableRoots' EvalSymlinks, without its mkdir.
physical_dir() { (cd "$1" 2>/dev/null && pwd -P) || true; }

# role_roots mirrors codexWritableRoots: state roots, build's install roots,
# the Go caches, the SwiftPM user cache; resolved and deduped in order.
role_roots() {
  local role=$1 prefix d r
  local -a raw=("${MUXCODE_LIFECYCLE_LOG_DIR:-$HOME/.config/muxcode/logs}" "$HOME/.config/muxcode/memory")
  if [ "$role" = build ]; then
    prefix=${PREFIX:-$HOME/.local}
    raw+=("${BINDIR:-$prefix/bin}" "${CONFIGDIR:-$HOME/.config/muxcode}" "$HOME/.claude/commands")
  fi
  if [ "$role" = build ] || [ "$role" = test ]; then
    while IFS= read -r d; do [ -n "$d" ] && raw+=("$d"); done < <(go env GOCACHE GOMODCACHE 2>/dev/null || true)
    raw+=("$HOME/Library/Caches/org.swift.swiftpm")
  fi
  local seen=" "
  for d in "${raw[@]}"; do
    r=$(physical_dir "$d")
    [ -n "$r" ] || continue
    case $seen in *" $r "*) continue ;; esac
    seen="$seen$r "
    echo "$r"
  done
}

# launch_args mirrors codexSandboxArgs for build and test, one arg per line.
launch_args() {
  local role=$1 root cache="/tmp/muxcode-cache-$(id -u)" kv
  printf '%s\n' -a never -s workspace-write
  while IFS= read -r root; do printf '%s\n' --add-dir "$root"; done < <(role_roots "$role")
  printf '%s\n' -c sandbox_workspace_write.network_access=true
  for kv in npm_config_cache=npm PIP_CACHE_DIR=pip UV_CACHE_DIR=uv POETRY_CACHE_DIR=pypoetry YARN_CACHE_FOLDER=yarn; do
    printf '%s\n' -c "shell_environment_policy.set.${kv%%=*}=\"$cache/${kv#*=}\""
  done
  local install
  install=$(install_dir)
  [ -z "$install" ] || printf '%s\n' -c "shell_environment_policy.set.BASH_ENV=\"$install/codex-shim/bash_env\""
}

# install_dir mirrors resolveInstallDir: MUXCODE_INSTALL_DIR, else
# ~/.config/muxcode when muxcode is installed in a .local/bin, else nothing.
install_dir() {
  if [ -n "${MUXCODE_INSTALL_DIR:-}" ]; then echo "$MUXCODE_INSTALL_DIR"; return; fi
  case $(dirname "$(command -v muxcode)") in */.local/bin) echo "$HOME/.config/muxcode" ;; esac
}

fingerprint() {
  local first=1 a
  while IFS= read -r a; do
    if [ $first -eq 1 ]; then printf '%s' "$a"; first=0; else printf '\0%s' "$a"; fi
  done | shasum -a 256 | cut -d' ' -f1
}

# sandbox_config translates launch args into `codex sandbox` config: -s is
# sandbox_mode, every --add-dir joins writable_roots with any extra roots, -c
# passes through, and -a (an approval policy, meaningless without a model) is
# dropped.
sandbox_config() {
  local role=$1; shift
  local -a args=() roots=("$@")
  local a prev=""
  while IFS= read -r a; do
    case $prev in
      -s) args+=(-c "sandbox_mode=\"$a\"") ;;
      --add-dir) roots+=("$a") ;;
      -c) args+=(-c "$a") ;;
    esac
    prev=$a
  done < <(launch_args "$role")
  local list="" r
  for r in "${roots[@]+"${roots[@]}"}"; do list="$list${list:+,}\"$r\""; done
  args+=(-c "sandbox_workspace_write.writable_roots=[$list]")
  printf '%s\n' "${args[@]}"
}

EXTRA_ROOTS=()
for d in "$DARWIN_CACHE" "$HOME/Library/Caches/org.swift.swiftpm" "$HOME/Library/org.swift.swiftpm" \
         "$HOME/.swiftpm" "$HOME/Library/Developer/Xcode/DerivedData"; do
  r=$(physical_dir "$d")
  [ -n "$r" ] && EXTRA_ROOTS+=("$r")
done

write_fixture() {
  local t=$1
  mkdir -p "$t/packages/ProbeKit/Sources/ProbeKit" "$t/packages/ProbeKit/Sources/ProbeCLI" \
           "$t/packages/ProbeKit/Tests/ProbeKitTests" "$t/apps/ios/Sources"
  cat > "$t/packages/ProbeKit/Package.swift" <<'EOF'
// swift-tools-version:5.9
import PackageDescription

let package = Package(
  name: "ProbeKit",
  platforms: [.macOS(.v13), .iOS(.v16)],
  products: [
    .library(name: "ProbeKit", targets: ["ProbeKit"]),
    .executable(name: "probe", targets: ["ProbeCLI"]),
  ],
  targets: [
    .target(name: "ProbeKit"),
    .executableTarget(name: "ProbeCLI", dependencies: ["ProbeKit"]),
    .testTarget(name: "ProbeKitTests", dependencies: ["ProbeKit"]),
  ]
)
EOF
  echo 'public func probeGreeting() -> String { "probe-ok" }' > "$t/packages/ProbeKit/Sources/ProbeKit/ProbeKit.swift"
  printf 'import ProbeKit\nprint(probeGreeting())\n' > "$t/packages/ProbeKit/Sources/ProbeCLI/main.swift"
  cat > "$t/packages/ProbeKit/Tests/ProbeKitTests/ProbeKitTests.swift" <<'EOF'
import XCTest
@testable import ProbeKit

final class ProbeKitTests: XCTestCase {
  func testGreeting() { XCTAssertEqual(probeGreeting(), "probe-ok") }
}
EOF
  cat > "$t/apps/ios/project.yml" <<'EOF'
name: ProbeApp
options:
  bundleIdPrefix: dev.muxcode.probe
  deploymentTarget:
    iOS: "16.0"
packages:
  ProbeKit:
    path: ../../packages/ProbeKit
targets:
  ProbeApp:
    type: application
    platform: iOS
    sources: [Sources]
    dependencies:
      - package: ProbeKit
    settings:
      base:
        GENERATE_INFOPLIST_FILE: YES
        CODE_SIGNING_ALLOWED: NO
        PRODUCT_BUNDLE_IDENTIFIER: dev.muxcode.probe.app
schemes:
  ProbeApp:
    build:
      targets:
        ProbeApp: all
EOF
  cat > "$t/apps/ios/Sources/ProbeApp.swift" <<'EOF'
import SwiftUI
import ProbeKit

@main
struct ProbeApp: App {
  var body: some Scene { WindowGroup { Text(probeGreeting()) } }
}
EOF
}

# cell_cmd prints the shell line a cell runs from the fixture root, with the
# variant's in-command flags.
cell_cmd() {
  local cmd=$1 variant=$2 spm="" xc=""
  case $variant in nosbx*)
    spm="--disable-sandbox"
    xc="-IDEPackageSupportDisableManifestSandbox=YES -IDEPackageSupportDisablePluginExecutionSandbox=YES" ;;
  esac
  case $cmd in
    swift-build) echo "cd packages/ProbeKit && swift build $spm" ;;
    swift-test)  echo "cd packages/ProbeKit && swift test $spm" ;;
    swift-run)   echo "cd packages/ProbeKit && swift run $spm probe" ;;
    xcodegen)    echo "cd apps/ios && xcodegen generate" ;;
    xcodebuild)  echo "cd apps/ios && xcodebuild -project ProbeApp.xcodeproj -scheme ProbeApp -destination 'generic/platform=iOS Simulator' -derivedDataPath build/DerivedData $xc CODE_SIGNING_ALLOWED=NO build" ;;
    simctl)      echo "xcrun simctl list devices" ;;
  esac
}

takes_flags() { case $1 in swift-*|xcodebuild) return 0 ;; esac; return 1; }

# with_timeout runs a command in its own process group and, past $TIMEOUT,
# kills the whole group — codex sandbox, bash and every Swift/Xcode child —
# then waits until no member is left before returning 142, so a timed-out cell
# cannot keep compiling while later cells run or cleanup removes its fixture.
with_timeout() {
  perl -e '
    my $t = shift @ARGV;
    my $pid = fork // die "fork: $!\n";
    if ($pid == 0) { setpgrp(0, 0); exec @ARGV or die "exec: $!\n"; }
    setpgrp($pid, $pid);
    $SIG{ALRM} = sub {
      kill "TERM", -$pid; sleep 2; kill "KILL", -$pid;
      waitpid($pid, 0);
      for (1 .. 100) { last unless kill 0, -$pid; select(undef, undef, undef, 0.1); }
      exit 142;
    };
    alarm $t;
    waitpid($pid, 0);
    my $st = $?;
    alarm 0;
    exit($st & 127 ? 128 + ($st & 127) : $st >> 8);
  ' "$TIMEOUT" "$@"
}

SIGNATURE='sandbox_apply|sandbox-exec|Operation not permitted|not permitted|ModuleCache|Invalid manifest|CoreSimulator|is not installed|Unable to find a destination|denied|error:'

# run_cell runs one cell and prints OK, FAIL, TIMEOUT or =, logging the
# failure's first signature line.
run_cell() {
  local role=$1 cmd=$2 variant=$3
  if [[ $variant == nosbx* ]] && ! takes_flags "$cmd"; then echo "="; return; fi
  local dir="$WORK/cells/$role-$cmd-$variant" line rc=0
  mkdir -p "$dir"
  cp -R "$WORK/template" "$dir/proj"
  line=$(cell_cmd "$cmd" "$variant")
  local -a extra=() cfg=() env=()
  case $variant in *roots*) extra=("${EXTRA_ROOTS[@]+"${EXTRA_ROOTS[@]}"}") ;; esac
  case $variant in *reloc*)
    mkdir -p "$dir/cache"
    env=(CLANG_MODULE_CACHE_PATH="$dir/cache/clang" SWIFTPM_MODULECACHE_OVERRIDE="$dir/cache/swiftpm-modules"
         SWIFTPM_TESTS_MODULECACHE="$dir/cache/swiftpm-tests") ;;
  esac
  if [ "$variant" = host ]; then
    (cd "$dir/proj" && with_timeout /bin/bash "$SHELL_FLAG" "$line") > "$dir/log" 2>&1 || rc=$?
  else
    while IFS= read -r a; do cfg+=("$a"); done < <(sandbox_config "$role" "${extra[@]+"${extra[@]}"}")
    local kv
    for kv in "${env[@]+"${env[@]}"}"; do cfg+=(-c "shell_environment_policy.set.${kv%%=*}=\"${kv#*=}\""); done
    [ "$DENIALS" -eq 1 ] && cfg=(--log-denials "${cfg[@]}")
    (cd "$dir/proj" && with_timeout codex sandbox "${cfg[@]}" -- /bin/bash "$SHELL_FLAG" "$line") > "$dir/log" 2>&1 || rc=$?
  fi
  local verdict=OK
  if [ "$rc" -eq 142 ]; then verdict=TIMEOUT
  elif [ "$rc" -ne 0 ]; then verdict=FAIL
  elif [ "$cmd" = swift-run ] && ! grep -q probe-ok "$dir/log"; then verdict=FAIL
  fi
  if [ "$verdict" != OK ]; then
    local sig
    sig=$(grep -E -m1 "$SIGNATURE" "$dir/log" | mask | cut -c1-200 || true)
    [ -n "$sig" ] || sig=$(tail -n1 "$dir/log" | mask | cut -c1-200)
    echo "$role/$cmd/$variant rc=$rc: $sig" >> "$WORK/failures"
    if [ "$DENIALS" -eq 1 ]; then
      sed -n '/=== Sandbox denials ===/,$p' "$dir/log" | sed 1d | mask | sort -u | head -8 | sed "s#^#    $role/$cmd/$variant denial: #" >> "$WORK/failures"
    fi
  fi
  echo "$verdict"
}

: > "$OUT"
say "=== MUX-207 Xcode/Swift Codex sandbox probe ==="
say "codex: $(codex --version 2>&1 | head -1)   xcode: $(xcodebuild -version 2>/dev/null | head -1)   swift: $(swift --version 2>&1 | grep -o 'Swift version [0-9.]*' | head -1)"
say "xcodegen: $(xcodegen --version 2>/dev/null)   simulator runtimes: $(xcrun simctl list runtimes 2>/dev/null | grep -c -i -E '^(iOS|tvOS|watchOS|visionOS|xrOS) ' || true)"
say "extra roots (roots variants): $(printf '%s ' "${EXTRA_ROOTS[@]+"${EXTRA_ROOTS[@]}"}" | mask)"

for role in "${ROLES[@]}"; do
  want=$(muxcode agent sandbox-fingerprint "$role" | cut -f2)
  got=$(launch_args "$role" | fingerprint)
  if [ -z "$want" ]; then
    say "sandbox $role: role does not resolve to codex here — args rebuilt from code defaults, UNVERIFIED"
  elif [ "$want" = "$got" ]; then
    say "sandbox $role: matches installed muxcode fingerprint ${want:0:12}"
  else
    say "sandbox $role: MISMATCH installed=${want:0:12} rebuilt=${got:0:12} — rebuilt args:"
    launch_args "$role" | mask | sed 's/^/    /' | tee -a "$OUT"
    exit 1
  fi
done

write_fixture "$WORK/template"
if (cd "$WORK/template/apps/ios" && xcodegen generate) > "$WORK/setup.log" 2>&1; then
  say "setup: host xcodegen generate OK (the xcodebuild cells use this project)"
else
  say "setup: host xcodegen generate FAILED — xcodebuild cells will fail on a missing project"
fi

VARIANTS=(base reloc roots nosbx nosbx+reloc nosbx+roots host)
: > "$WORK/failures"
say ""
say "$(printf '%-6s %-12s' role command)$(printf ' %-12s' "${VARIANTS[@]}")  needs"
for role in "${ROLES[@]}"; do
  case $role in
    build) cmds=(swift-build swift-run xcodegen xcodebuild simctl) ;;
    test)  cmds=(swift-test simctl) ;;
    *) echo "unknown role: $role (build|test)" >&2; exit 1 ;;
  esac
  for cmd in "${cmds[@]}"; do
    if [ ${#ONLY[@]} -gt 0 ] && [[ " ${ONLY[*]} " != *" $cmd "* ]]; then continue; fi
    row=$(printf '%-6s %-12s' "$role" "$cmd")
    needs=""
    verdicts=()
    for v in "${VARIANTS[@]}"; do
      verdicts+=("$(run_cell "$role" "$cmd" "$v")")
      row="$row $(printf ' %-12s' "${verdicts[${#verdicts[@]}-1]}")"
    done
    if [ "${verdicts[${#VARIANTS[@]}-1]}" != OK ]; then
      needs="ENV: fails unsandboxed too"
    else
      for i in 0 1 2 3 4 5; do
        if [ "${verdicts[$i]}" = OK ]; then needs=${VARIANTS[$i]}; break; fi
      done
      [ -n "$needs" ] || needs="NOTHING tried works"
    fi
    say "$row  $needs"
  done
done

say ""
say "=== failure signatures ==="
if [ -s "$WORK/failures" ]; then tee -a "$OUT" < "$WORK/failures"; else say "(none)"; fi
say ""
say "report: $OUT"
