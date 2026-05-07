#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

gawk_source="${GAWK_SOURCE_DIR:-$AWK_HARNESS_CACHE/sources/gawk}"
onetrueawk_source="${ONETRUEAWK_SOURCE_DIR:-$AWK_HARNESS_CACHE/sources/onetrueawk}"

if [ ! -d "$gawk_source/test" ]; then
	die "gawk test directory not found at $gawk_source/test; run tools/awk-harness/run.sh fetch first"
fi
if [ ! -d "$onetrueawk_source/testdir" ]; then
	die "One True Awk testdir not found at $onetrueawk_source/testdir; run tools/awk-harness/run.sh fetch first"
fi

{
	for path in "$gawk_source"/test/*.awk "$gawk_source"/test/*.sh; do
		[ -e "$path" ] || continue
		printf 'gawk\t%s\t%s\n' "test/$(basename "$path")" "$GAWK_REF"
	done
	for path in "$gawk_source"/test/*.ok; do
		[ -e "$path" ] || continue
		base="${path%.ok}"
		if [ ! -f "$base.awk" ] && [ ! -f "$base.sh" ]; then
			printf 'gawk\t%s\t%s\n' "test/$(basename "$path")" "$GAWK_REF"
		fi
	done
	for path in "$onetrueawk_source"/testdir/t.* "$onetrueawk_source"/testdir/p.* "$onetrueawk_source"/testdir/T.* "$onetrueawk_source"/testdir/tt.*; do
		[ -e "$path" ] || continue
		printf 'onetrueawk\t%s\t%s\n' "testdir/$(basename "$path")" "$ONETRUEAWK_REF"
	done
} | LC_ALL=C sort -u
