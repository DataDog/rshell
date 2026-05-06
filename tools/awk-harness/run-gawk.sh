#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

source_dir="$AWK_HARNESS_CACHE/sources/gawk"
result_dir="$AWK_HARNESS_RESULTS/gawk"
mkdir -p "$result_dir"

oracle="$(resolve_gawk_oracle)"
oracle_version="$($oracle --version | sed -n '1p')"
gawk_ref="$GAWK_REF"
log "using GNU awk oracle: $oracle ($oracle_version)"

commit="$(fetch_git_repo "gawk" "$GAWK_REPO" "$gawk_ref" "$source_dir")"
testdir="$source_dir/test"

if [ ! -d "$testdir" ]; then
	die "gawk test directory not found at $testdir"
fi

list_file="$result_dir/gawk-tests.txt"
find "$testdir" -maxdepth 1 -type f -name '*.awk' | sort >"$list_file"
total_awk="$(wc -l <"$list_file" | tr -d ' ')"
runnable=0
while IFS= read -r awk_file; do
	[ -f "${awk_file%.awk}.ok" ] && runnable=$((runnable + 1))
done <"$list_file"

if [ -n "$AWK_HARNESS_BOOTSTRAP" ]; then
	log "bootstrap mode: fetched gawk tests only"
	log "test counts: awk_files=$total_awk runnable_triplets=$runnable"
	write_json_summary "$result_dir/summary.json" "gawk" "$GAWK_REPO" "$gawk_ref" "$commit" "$runnable" 0 0 "$runnable"
	exit 0
fi

awk_bin="$(resolve_awk_under_test)"
mode="${GAWK_TEST_MODE:-triples}"

run_awk_program() {
	local bin="$1"
	local awk_file="$2"
	local in_file="$3"
	local out_file="$4"
	local err_file="$5"

	if [ -n "$AWK_HARNESS_TIMEOUT" ] && command_exists timeout; then
		if [ -f "$in_file" ]; then
			timeout "$AWK_HARNESS_TIMEOUT" "$bin" -f "$awk_file" "$in_file" >"$out_file" 2>"$err_file"
		else
			timeout "$AWK_HARNESS_TIMEOUT" "$bin" -f "$awk_file" >"$out_file" 2>"$err_file"
		fi
	else
		if [ -f "$in_file" ]; then
			"$bin" -f "$awk_file" "$in_file" >"$out_file" 2>"$err_file"
		else
			"$bin" -f "$awk_file" >"$out_file" 2>"$err_file"
		fi
	fi
}

run_triples() {
	local filter="${GAWK_TEST_FILTER:-}"
	local limit="${GAWK_TEST_LIMIT:-0}"
	local full_log="$result_dir/triples.log"
	local failures_log="$result_dir/failures.log"
	local total=0
	local passed=0
	local failed=0
	local skipped=0
	local ref_dir="$result_dir/reference"
	local candidate_dir="$result_dir/candidate"

	mkdir -p "$ref_dir" "$candidate_dir"
	: >"$full_log"
	: >"$failures_log"

	while IFS= read -r awk_file; do
		base="${awk_file%.awk}"
		name="$(basename "$base")"
		ok_file="$base.ok"
		in_file="$base.in"
		ref_out="$ref_dir/$name.out"
		ref_err="$ref_dir/$name.stderr"
		out_file="$candidate_dir/$name.out"
		stderr_file="$candidate_dir/$name.stderr"

		if [ -n "$filter" ]; then
			case "$name" in
				*"$filter"*) ;;
				*) skipped=$((skipped + 1)); continue ;;
			esac
		fi

		if [ ! -f "$ok_file" ]; then
			skipped=$((skipped + 1))
			continue
		fi

		if [ "$limit" -gt 0 ] && [ "$total" -ge "$limit" ]; then
			skipped=$((skipped + 1))
			continue
		fi

		total=$((total + 1))
		log "running gawk triplet $name against GNU awk oracle"

		set +e
		run_awk_program "$oracle" "$awk_file" "$in_file" "$ref_out" "$ref_err"
		ref_exit=$?
		run_awk_program "$awk_bin" "$awk_file" "$in_file" "$out_file" "$stderr_file"
		exit_code=$?
		set -e

		stdout_ok=0
		stderr_ok=0
		exit_ok=0
		cmp -s "$ref_out" "$out_file" && stdout_ok=1
		cmp -s "$ref_err" "$stderr_file" && stderr_ok=1
		[ "$ref_exit" -eq "$exit_code" ] && exit_ok=1

		{
			printf '== %s ==\n' "$name"
			printf 'oracle_exit=%s candidate_exit=%s exit_ok=%s stdout_ok=%s stderr_ok=%s\n' "$ref_exit" "$exit_code" "$exit_ok" "$stdout_ok" "$stderr_ok"
		} >>"$full_log"

		if [ "$exit_ok" -eq 1 ] && [ "$stdout_ok" -eq 1 ] && [ "$stderr_ok" -eq 1 ]; then
			passed=$((passed + 1))
		else
			failed=$((failed + 1))
			{
				printf '== %s ==\n' "$name"
				printf 'awk=%s\ninput=%s\noracle_out=%s\ncandidate_out=%s\noracle_stderr=%s\ncandidate_stderr=%s\noracle_exit=%s\ncandidate_exit=%s\n' \
					"$awk_file" "$in_file" "$ref_out" "$out_file" "$ref_err" "$stderr_file" "$ref_exit" "$exit_code"
				printf '\n'
			} >>"$failures_log"
		fi
	done <"$list_file"

	write_json_summary "$result_dir/summary.json" "gawk-triples" "$GAWK_REPO" "$gawk_ref" "$commit" "$total" "$passed" "$failed" "$skipped"
	log "gawk triples summary: total=$total passed=$passed failed=$failed skipped=$skipped"
	log "full log: $full_log"
	log "failures log: $failures_log"

	if [ "$failed" -ne 0 ]; then
		return 1
	fi
}

run_make_check() {
	if [ ! -x "$source_dir/configure" ]; then
		if [ ! -x "$source_dir/bootstrap.sh" ]; then
			die "gawk source tree has neither configure nor bootstrap.sh"
		fi
		(cd "$source_dir" && ./bootstrap.sh)
	fi
	(cd "$source_dir" && ./configure)
	(cd "$source_dir" && make -j"${GAWK_MAKE_JOBS:-2}")
	(cd "$source_dir/test" && make check AWKPROG="$awk_bin" AWK="$awk_bin")
}

case "$mode" in
	triples) run_triples ;;
	make-check) run_make_check ;;
	*) die "unknown GAWK_TEST_MODE: $mode" ;;
esac
