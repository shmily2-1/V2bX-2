#!/usr/bin/env bash
# Configuration entrypoint adapted from the original V2bX-script flow (MPL-2.0).
# Python reads the user's stdin directly, not a here-document containing code.
set -euo pipefail
here=$(dirname -- "$(realpath -e -- "${BASH_SOURCE[0]}")")
if [[ " $* " == *" --root "* || " $* " == *" --offline "* ]]; then
  args=()
  for arg in "$@"; do [[ $arg == --offline ]] || args+=("$arg"); done
  exec python3 "$here/configure.py" "${args[@]}"
fi
exec python3 "$here/provision.py" "$@"
