#!/usr/bin/env bash
# Benchmark: ls builtin — rshell vs bash
#
# Compares rshell's built-in ls against system ls invoked via bash
# across directory sizes and flag modes.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/common.sh"

# --- Fixture setup -----------------------------------------------------------

setup_flat_dir() {
    local dir="$1" count="$2"
    mkdir -p "$dir"
    for i in $(seq -w 1 "$count"); do
        printf 'x' > "$dir/file-$i"
    done
}

setup_recursive_tree() {
    local root="$1" depth="$2" files_per_level="$3"
    local current="$root"
    for d in $(seq 1 "$depth"); do
        mkdir -p "$current"
        for i in $(seq -w 1 "$files_per_level"); do
            printf 'x' > "$current/file-$i"
        done
        if [ "$d" -lt "$depth" ]; then
            current="$current/sub"
        fi
    done
}

# --- Benchmarks ---------------------------------------------------------------

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

header "ls — default mode"

for size in small:10 medium:100 large:1000; do
    label="${size%%:*}"
    count="${size##*:}"
    dir="$TMPDIR/default_$label"
    setup_flat_dir "$dir" "$count"
    bench "ls/default/$label" \
        "rshell -s 'ls $dir' -a '$dir'" \
        "bash -c 'ls $dir'"
done

header "ls — long format (-l)"

for size in small:10 medium:100 large:1000; do
    label="${size%%:*}"
    count="${size##*:}"
    dir="$TMPDIR/long_$label"
    setup_flat_dir "$dir" "$count"
    bench "ls/long/$label" \
        "rshell -s 'ls -l $dir' -a '$dir'" \
        "bash -c 'ls -l $dir'"
done

header "ls — recursive (-R)"

for size in small:3:3 medium:3:10; do
    label="${size%%:*}"
    rest="${size#*:}"
    depth="${rest%%:*}"
    fpl="${rest##*:}"
    dir="$TMPDIR/recursive_$label"
    setup_recursive_tree "$dir" "$depth" "$fpl"
    bench "ls/recursive/$label" \
        "rshell -s 'ls -R $dir' -a '$dir'" \
        "bash -c 'ls -R $dir'"
done
