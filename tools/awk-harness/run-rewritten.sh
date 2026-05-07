#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

oracle="$(resolve_gawk_oracle)"
if [ -z "${AWK_UNDER_TEST:-}" ]; then
	die "AWK_UNDER_TEST must point to the awk binary under test; for rshell use RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk"
fi
AWK_UNDER_TEST="$(resolve_awk_under_test)"

export GAWK_ORACLE="$oracle"
export AWK_UNDER_TEST
export RSHELL_AWK_TEST=1

log "running rewritten AWK scenarios"
log "using candidate: $AWK_UNDER_TEST"
log "using GNU awk oracle: $GAWK_ORACLE ($("$GAWK_ORACLE" --version | sed -n '1p'))"

(cd "$REPO_ROOT" && go test -v ./tests -run TestAwkScenarios -count=1)
