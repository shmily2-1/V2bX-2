#!/usr/bin/env bash
# Configuration entrypoint adapted from the original V2bX-script flow (MPL-2.0).
# Python reads the user's stdin directly, not a here-document containing code.
set -euo pipefail
here=$(dirname -- "$(realpath -e -- "${BASH_SOURCE[0]}")")
exec python3 "$here/configure.py" "$@"
