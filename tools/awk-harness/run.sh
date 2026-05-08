#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

usage() {
	cat <<'EOF'
Usage: tools/awk-harness/run.sh TARGET

Targets:
  fuzz         Run AWK fuzz targets against GNU awk.
  scenarios    Run shell scenarios marked oracle: gawk against GNU awk.
  rewritten    Run rshell-owned AWK scenario rewrites.
  install-gawk Build/install the pinned GNU awk oracle into the harness cache.

Required for test runs:
  AWK_UNDER_TEST=/path/to/awk-like-binary
  For rshell, use: RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk

Oracle:
  The harness compares candidate behavior to GNU awk, not mawk or system awk.
  Run install-gawk first, or set GAWK_ORACLE=/path/to/gawk with the pinned version.

Useful environment variables:
  AWK_HARNESS_CACHE=DIR         Cache oracle builds and scratch files.
  RSHELL_AWK_FUZZTIME=D         Duration for each AWK fuzz target.
  RSHELL_AWK_FUZZ_TIMEOUT=D     Per-process timeout for fuzzed AWK executions.
  RSHELL_AWK_GO_TEST_TIMEOUT=D  Overall go test timeout for each AWK fuzz run.
  RSHELL_AWK_SCENARIO_TIMEOUT=D Duration or seconds for local rewritten tests.
  GAWK_ORACLE=/path/to/gawk     Trusted GNU awk binary used as oracle.
  GAWK_VERSION=VERSION          Pinned GNU awk oracle version (default: 5.4.0).
EOF
}

target="${1:-}"
if [ -z "$target" ] || [ "$target" = "-h" ] || [ "$target" = "--help" ]; then
	usage
	exit 0
fi

case "$target" in
	fuzz)
		exec "$SCRIPT_DIR/run-fuzz.sh"
		;;
	scenarios)
		exec "$SCRIPT_DIR/run-scenarios.sh"
		;;
	rewritten)
		exec "$SCRIPT_DIR/run-rewritten.sh"
		;;
	install-gawk)
		exec "$SCRIPT_DIR/install-gawk.sh"
		;;
	*)
		usage >&2
		exit 2
		;;
esac
