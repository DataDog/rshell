#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

source_dir="$AWK_HARNESS_CACHE/sources/onetrueawk"
commit="$(fetch_git_repo "one true awk" "$ONETRUEAWK_REPO" "$ONETRUEAWK_REF" "$source_dir")"

printf '%s\n' "$source_dir"
log "one true awk commit: $commit"
