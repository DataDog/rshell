#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

awk_harness_default_platform() {
	local kernel
	local machine
	kernel="$(uname -s | tr '[:upper:]' '[:lower:]')"
	machine="$(uname -m | tr '[:upper:]' '[:lower:]')"
	printf '%s-%s\n' "$kernel" "$machine"
}

AWK_HARNESS_PLATFORM="${AWK_HARNESS_PLATFORM:-$(awk_harness_default_platform)}"
AWK_HARNESS_CACHE="${AWK_HARNESS_CACHE:-$REPO_ROOT/.superset/awk-harness/$AWK_HARNESS_PLATFORM}"
AWK_HARNESS_TIMEOUT="${AWK_HARNESS_TIMEOUT:-}"

GAWK_VERSION="${GAWK_VERSION:-5.4.0}"
GAWK_RELEASE_URL="${GAWK_RELEASE_URL:-https://ftp.gnu.org/gnu/gawk/gawk-$GAWK_VERSION.tar.gz}"
GAWK_ORACLE_PREFIX="${GAWK_ORACLE_PREFIX:-$AWK_HARNESS_CACHE/oracle/gawk-$GAWK_VERSION}"
GAWK_ORACLE_BIN="$GAWK_ORACLE_PREFIX/bin/gawk"

mkdir -p "$AWK_HARNESS_CACHE"

log() {
	printf '[awk-harness] %s\n' "$*" >&2
}

die() {
	printf '[awk-harness] error: %s\n' "$*" >&2
	exit 1
}

command_exists() {
	command -v "$1" >/dev/null 2>&1
}

abs_path() {
	case "$1" in
		/*) printf '%s\n' "$1" ;;
		*) printf '%s/%s\n' "$PWD" "$1" ;;
	esac
}

resolve_awk_under_test() {
	if [ -z "${AWK_UNDER_TEST:-}" ]; then
		die "AWK_UNDER_TEST must point to the awk binary under test"
	fi

	case "$AWK_UNDER_TEST" in
		*/*)
			if [ ! -x "$AWK_UNDER_TEST" ]; then
				die "AWK_UNDER_TEST is not executable: $AWK_UNDER_TEST"
			fi
			abs_path "$AWK_UNDER_TEST"
			;;
		*)
			if ! command_exists "$AWK_UNDER_TEST"; then
				die "AWK_UNDER_TEST is not on PATH: $AWK_UNDER_TEST"
			fi
			command -v "$AWK_UNDER_TEST"
			;;
	esac
}

resolve_command() {
	local value="$1"
	local label="$2"

	case "$value" in
		*/*)
			if [ ! -x "$value" ]; then
				die "$label is not executable: $value"
			fi
			abs_path "$value"
			;;
		*)
			if ! command_exists "$value"; then
				die "$label is not on PATH: $value"
			fi
			command -v "$value"
			;;
	esac
}

gawk_version() {
	local oracle="$1"
	"$oracle" --version | sed -n '1s/^GNU Awk \([^, ]*\).*/\1/p'
}

require_gawk_version() {
	local oracle="$1"
	local version
	version="$(gawk_version "$oracle")"
	if [ "$version" != "$GAWK_VERSION" ]; then
		die "$oracle is GNU awk $version, but this harness requires GNU awk $GAWK_VERSION; run tools/awk-harness/run.sh install-gawk or set GAWK_ORACLE to a matching binary"
	fi
}

resolve_gawk_oracle() {
	local candidate="${GAWK_ORACLE:-}"
	if [ -n "$candidate" ]; then
		candidate="$(resolve_command "$candidate" "GAWK_ORACLE")"
		require_gawk_version "$candidate"
		printf '%s\n' "$candidate"
		return 0
	fi

	if [ -x "$GAWK_ORACLE_BIN" ]; then
		require_gawk_version "$GAWK_ORACLE_BIN"
		printf '%s\n' "$GAWK_ORACLE_BIN"
		return 0
	fi

	if command_exists gawk; then
		candidate="$(command -v gawk)"
		require_gawk_version "$candidate"
		printf '%s\n' "$candidate"
		return 0
	fi

	die "GNU awk $GAWK_VERSION is required; run tools/awk-harness/run.sh install-gawk or set GAWK_ORACLE=/path/to/gawk-$GAWK_VERSION"
}
