# Shared by the muxcode Codex shims (MUX-207); sourced, never run.

# shim_exec NAME ARGS... replaces the shim with the first NAME on PATH outside
# the shim's own directory, so a shim never finds itself.
shim_exec() {
  shim_name=$1
  shift
  shim_self=$(CDPATH= cd -- "${0%/*}" && pwd -P) || exit 127
  shim_ifs=$IFS
  IFS=:
  set -f
  for shim_dir in $PATH; do
    [ -n "$shim_dir" ] || continue
    [ "$(CDPATH= cd -- "$shim_dir" 2>/dev/null && pwd -P)" = "$shim_self" ] && continue
    if [ -x "$shim_dir/$shim_name" ] && [ ! -d "$shim_dir/$shim_name" ]; then
      IFS=$shim_ifs
      set +f
      exec "$shim_dir/$shim_name" "$@"
    fi
  done
  IFS=$shim_ifs
  set +f
  echo "muxcode shim: no $shim_name on PATH outside $shim_self" >&2
  exit 127
}

# shim_has_prefix PREFIX ARGS... succeeds when an argument starts with PREFIX.
shim_has_prefix() {
  shim_prefix=$1
  shift
  for shim_arg in "$@"; do
    case $shim_arg in "$shim_prefix"*) return 0 ;; esac
  done
  return 1
}

# shim_in_codex_sandbox succeeds inside Codex's macOS Seatbelt sandbox, the
# only place the nested sandboxes these shims turn off are refused — and
# redundant, since Seatbelt already confines the process.
shim_in_codex_sandbox() {
  [ "${CODEX_SANDBOX:-}" = seatbelt ]
}
