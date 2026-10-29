#!/usr/bin/env bash
# Run one Go fuzz target for a fixed duration, treating only Go's -fuzztime
# boundary timeout as a pass.
#
# Usage: bash scripts/fuzz-run.sh <package> <FuzzFunc> [fuzztime] [timeout]
#
# When -fuzztime expires, Go's fuzz coordinator cancels in-flight inputs and
# can fail the run with a bare "context deadline exceeded" and no failing
# input. That is not a finding. A real finding (a panic, t.Fatal, a hang the
# worker reports, ...) always makes Go write the input to testdata/fuzz and
# print "Failing input written to", so any failure that printed it, or that
# lacks the bare timeout message, still fails.
set -uo pipefail

if [ $# -lt 2 ] || [ $# -gt 4 ]; then
  echo "usage: $0 <package> <FuzzFunc> [fuzztime] [timeout]" >&2
  exit 2
fi

pkg="$1"
func="$2"
fuzztime="${3:-30s}"
timeout="${4:-90s}"

tmpfile=$(mktemp)
trap 'rm -f "$tmpfile"' EXIT

# tee streams progress to the CI log in real time; PIPESTATUS keeps go test's
# exit code rather than tee's.
go test -fuzz="^${func}\$" -fuzztime="$fuzztime" "$pkg" -timeout "$timeout" 2>&1 | tee "$tmpfile"
exit_code=${PIPESTATUS[0]}

if [ "$exit_code" -eq 0 ]; then
  exit 0
fi

if grep -q 'context deadline exceeded' "$tmpfile" && ! grep -q 'Failing input written to' "$tmpfile"; then
  echo "NOTE: $func hit Go's fuzz coordinator timeout at the -fuzztime limit; not a failure"
  exit 0
fi

echo "FAIL: $func" >&2
exit "$exit_code"
