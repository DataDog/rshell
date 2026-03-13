#!/usr/bin/env bash
# Common helpers for benchmark scripts.
#
# Sourced by each bench_*.sh script. Provides:
#   header  — print a section header
#   bench   — run a hyperfine comparison (rshell vs bash)
#
# Environment variables:
#   WARMUP  — number of warmup runs (default: 3)
#   RUNS    — number of timed runs  (default: 10)
#   EXPORT  — if set, directory to write JSON results (e.g. EXPORT=/tmp/results)

set -euo pipefail

WARMUP="${WARMUP:-10}"
RUNS="${RUNS:-50}"

header() {
    printf '\n══════════════════════════════════════════════════════════════\n'
    printf '  %s\n' "$1"
    printf '══════════════════════════════════════════════════════════════\n\n'
}

bench() {
    local name="$1" rshell_cmd="$2" bash_cmd="$3"

    local export_args=()
    if [ -n "${EXPORT:-}" ]; then
        mkdir -p "$EXPORT"
        local safe_name="${name//\//_}"
        export_args=(--export-json "$EXPORT/${safe_name}.json")
    fi

    hyperfine \
        --shell=none \
        --warmup "$WARMUP" \
        --runs "$RUNS" \
        --command-name "rshell: $name" "$rshell_cmd" \
        --command-name "bash:   $name" "$bash_cmd" \
        "${export_args[@]}"
}
