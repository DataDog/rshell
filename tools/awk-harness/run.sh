#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
	cat <<'EOF'
Usage: tools/awk-harness/run.sh TARGET

Targets:
  rewritten    Run rshell-owned AWK scenario rewrites.
  inventory    List upstream tests that need rewrite-map coverage.
  sync-rewrite-map Add todo entries for fetched upstream tests missing from upstream-map.yaml.
  check-rewrite-map Verify upstream-map.yaml covers every fetched upstream test.
  onetrueawk   Fetch and run One True Awk tests against the GNU awk oracle.
  gawk         Fetch and run GNU awk tests against the GNU awk oracle.
  all          Run gawk, then onetrueawk.
  fetch        Fetch both upstream repositories without running candidate tests.
  install-gawk Build/install the pinned GNU awk oracle into the harness cache.

Required for test runs:
  AWK_UNDER_TEST=/path/to/awk-like-binary
  For rshell, use: RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk

Oracle:
  The harness compares candidate behavior to GNU awk, not mawk or system awk.
  Run install-gawk first, or set GAWK_ORACLE=/path/to/gawk with the pinned version.

Useful environment variables:
  AWK_HARNESS_BOOTSTRAP=1       Fetch and summarize upstream tests only.
  AWK_HARNESS_CACHE=DIR         Cache external repos and results.
  RSHELL_AWK_SCENARIO_TIMEOUT=D Duration or seconds for local rewritten tests.
  ONETRUEAWK_REF=REF            One True Awk commit, tag, or branch.
  ONETRUEAWK_SUITE=core|all|... One True Awk suites to run.
  GAWK_ORACLE=/path/to/gawk     Trusted GNU awk binary used as oracle.
  GAWK_VERSION=VERSION          Pinned GNU awk oracle version (default: 5.4.0).
  GAWK_REF=REF                  gawk source tag or branch (default: gawk-$GAWK_VERSION).
  GAWK_TEST_MODE=triples        Run .awk/.in/.ok triplets (default).
  GAWK_TEST_FILTER=SUBSTRING    Run matching gawk triplet names only.
  GAWK_TEST_LIMIT=N             Cap gawk triplets for smoke tests.
EOF
}

target="${1:-}"
if [ -z "$target" ] || [ "$target" = "-h" ] || [ "$target" = "--help" ]; then
	usage
	exit 0
fi

case "$target" in
	rewritten)
		exec "$SCRIPT_DIR/run-rewritten.sh"
		;;
	inventory)
		exec "$SCRIPT_DIR/list-rewrite-inventory.sh"
		;;
	sync-rewrite-map)
		exec "$SCRIPT_DIR/sync-rewrite-map.sh"
		;;
	check-rewrite-map)
		exec "$SCRIPT_DIR/check-rewrite-map.sh"
		;;
	onetrueawk)
		exec "$SCRIPT_DIR/run-onetrueawk.sh"
		;;
	gawk)
		exec "$SCRIPT_DIR/run-gawk.sh"
		;;
	install-gawk)
		exec "$SCRIPT_DIR/install-gawk.sh"
		;;
	fetch)
		"$SCRIPT_DIR/fetch-onetrueawk.sh" >/dev/null
		"$SCRIPT_DIR/fetch-gawk.sh" >/dev/null
		;;
	all)
		status=0
		"$SCRIPT_DIR/run-gawk.sh" || status=$?
		"$SCRIPT_DIR/run-onetrueawk.sh" || status=$?
		exit "$status"
		;;
	*)
		usage >&2
		exit 2
		;;
esac
