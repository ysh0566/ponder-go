#!/usr/bin/env bash
# Run under the coordinator's capacity watchdog with the approved toolchain PATH.
set -euo pipefail
exec node "$(dirname "$0")/managed-finite-launch.mjs" "$@"
