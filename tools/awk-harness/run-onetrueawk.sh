#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

source_dir="$AWK_HARNESS_CACHE/sources/onetrueawk"
result_dir="$AWK_HARNESS_RESULTS/onetrueawk"
mkdir -p "$result_dir"

commit="$(fetch_git_repo "one true awk" "$ONETRUEAWK_REPO" "$ONETRUEAWK_REF" "$source_dir")"
testdir="$source_dir/testdir"

if [ ! -d "$testdir" ]; then
	die "one true awk testdir not found at $testdir"
fi

count_matching() {
	local pattern="$1"
	find "$testdir" -maxdepth 1 -type f -name "$pattern" | wc -l | tr -d ' '
}

t_count="$(count_matching 't.*')"
p_count="$(count_matching 'p.*')"
T_count="$(count_matching 'T.*')"
tt_count="$(count_matching 'tt.*')"
total_count=$((t_count + p_count + T_count))

if [ -n "$AWK_HARNESS_BOOTSTRAP" ]; then
	log "bootstrap mode: fetched one true awk tests only"
	log "test counts: t=$t_count p=$p_count T=$T_count tt=$tt_count"
	write_json_summary "$result_dir/summary.json" "onetrueawk" "$ONETRUEAWK_REPO" "$ONETRUEAWK_REF" "$commit" "$total_count" 0 0 "$total_count"
	exit 0
fi

awk_bin="$(resolve_awk_under_test)"
reference_awk="$(resolve_gawk_oracle)"
reference_version="$($reference_awk --version | sed -n '1p')"
suite_spec="${ONETRUEAWK_SUITE:-core}"
log_file="$result_dir/run.log"
: >"$log_file"
log "using GNU awk oracle: $reference_awk ($reference_version)"

expand_suites() {
	case "$suite_spec" in
		core) printf '%s\n' t p T ;;
		all) printf '%s\n' t p T tt ;;
		regress) printf '%s\n' regress ;;
		*) printf '%s\n' "$suite_spec" | tr ',' '\n' ;;
	esac
}

run_suite() {
	local suite="$1"
	local suite_log="$result_dir/$suite.log"

	log "running one true awk suite '$suite' against GNU awk oracle"
	case "$suite" in
		t)
			(cd "$testdir" && oldawk="$reference_awk" awk="$awk_bin" sh Compare.t t.*) >"$suite_log" 2>&1
			;;
		p)
			(cd "$testdir" && oldawk="$reference_awk" awk="$awk_bin" sh Compare.p p.? p.??*) >"$suite_log" 2>&1
			;;
		T)
			(cd "$testdir" && oldawk="$reference_awk" awk="$awk_bin" sh Compare.T1) >"$suite_log" 2>&1
			;;
		tt)
			(cd "$testdir" && oldawk="$reference_awk" awk="$awk_bin" sh Compare.tt tt.*) >"$suite_log" 2>&1
			;;
		regress)
			(cd "$testdir" && oldawk="$reference_awk" awk="$awk_bin" sh REGRESS) >"$suite_log" 2>&1
			;;
		*)
			die "unknown one true awk suite: $suite"
			;;
	esac

	{
		printf '== %s ==\n' "$suite"
		cat "$suite_log"
		printf '\n'
	} >>"$log_file"

	grep -aEc 'BAD|FAIL' "$suite_log" || true
}

total=0
failed=0
while IFS= read -r suite; do
	[ -n "$suite" ] || continue
	case "$suite" in
		t) total=$((total + t_count)) ;;
		p) total=$((total + p_count)) ;;
		T) total=$((total + T_count)) ;;
		tt) total=$((total + tt_count)) ;;
		regress) total=$((total + t_count + p_count + T_count + tt_count)) ;;
	esac
	suite_failures="$(run_suite "$suite")"
	failed=$((failed + suite_failures))
done < <(expand_suites)

passed=$((total - failed))
write_json_summary "$result_dir/summary.json" "onetrueawk" "$ONETRUEAWK_REPO" "$ONETRUEAWK_REF" "$commit" "$total" "$passed" "$failed" 0

log "one true awk summary: total=$total passed=$passed failed=$failed"
log "full log: $log_file"

if [ "$failed" -ne 0 ]; then
	exit 1
fi
