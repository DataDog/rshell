#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

gawk_source="$("$SCRIPT_DIR/fetch-gawk.sh")"
onetrueawk_source="$("$SCRIPT_DIR/fetch-onetrueawk.sh")"

export RSHELL_AWK_UPSTREAM_MAP_TEST=1
export GAWK_SOURCE_DIR="$gawk_source"
export ONETRUEAWK_SOURCE_DIR="$onetrueawk_source"

(cd "$REPO_ROOT" && go test -v ./tests -run TestAwkUpstreamMapCompleteness -count=1)
