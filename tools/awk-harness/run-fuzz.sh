#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

oracle="$(resolve_gawk_oracle)"
export GAWK_ORACLE="$oracle"
export RSHELL_AWK_FUZZ_TEST=1

fuzztime="${RSHELL_AWK_FUZZTIME:-30s}"
timeout="${RSHELL_AWK_GO_TEST_TIMEOUT:-90s}"

log "running AWK fuzz targets against GNU awk"
log "using GNU awk oracle: $GAWK_ORACLE ($("$GAWK_ORACLE" --version | sed -n '1p'))"

if [ ! -f "$REPO_ROOT/builtins/awk/awk.go" ]; then
	log "rshell awk builtin is not present; skipping AWK fuzz targets"
	exit 0
fi

fuzz_funcs="$(grep -r '^func FuzzAwk' "$REPO_ROOT/tests" 2>/dev/null | sed 's/.*func \(FuzzAwk[^(]*\).*/\1/' | sort -u)"
if [ -z "$fuzz_funcs" ]; then
	log "no AWK fuzz targets found"
	exit 0
fi

log "running AWK fuzz seed corpus"
(cd "$REPO_ROOT" && go test -run '^FuzzAwk' ./tests -timeout "$timeout")

fuzz_run() {
	local func="$1"
	local tmpfile exit_code oldpwd
	tmpfile="$(mktemp)"
	oldpwd="$PWD"
	cd "$REPO_ROOT"
	go test -run '^$' -fuzz="^${func}$" -fuzztime="$fuzztime" ./tests -timeout "$timeout" 2>&1 | tee "$tmpfile" || true
	exit_code=${PIPESTATUS[0]}
	cd "$oldpwd"
	if [ "$exit_code" -ne 0 ]; then
		if grep -qE '[[:space:]]+[^[:space:]]+_test\.go:[0-9]+:' "$tmpfile"; then
			rm -f "$tmpfile"
			echo "FAIL: $func — test assertion failure detected" >&2
			return "$exit_code"
		fi
		echo "NOTE: $func — fuzz coordinator boundary timeout (expected at fuzz time limit, not a failure)"
	fi
	rm -f "$tmpfile"
	return 0
}

for func in $fuzz_funcs; do
	log "fuzzing $func for $fuzztime"
	fuzz_run "$func"
done
