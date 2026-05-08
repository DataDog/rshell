#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

oracle="$(resolve_gawk_oracle)"
export GAWK_ORACLE="$oracle"
export RSHELL_GAWK_TEST=1

log "running shell scenarios marked oracle: gawk"
log "using GNU awk oracle: $GAWK_ORACLE ($("$GAWK_ORACLE" --version | sed -n '1p'))"

(cd "$REPO_ROOT" && go test -v ./tests -run TestShellScenariosAgainstGawk -count=1)
