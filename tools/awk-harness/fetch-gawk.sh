#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

source_dir="$AWK_HARNESS_CACHE/sources/gawk"
commit="$(fetch_git_repo "gawk" "$GAWK_REPO" "$GAWK_REF" "$source_dir")"

printf '%s\n' "$source_dir"
log "gawk commit: $commit"
