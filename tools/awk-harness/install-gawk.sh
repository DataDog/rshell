#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/lib.sh"

install_deps() {
	if [ "${GAWK_INSTALL_DEPS:-1}" = "0" ]; then
		return 0
	fi

	case "$(uname -s)" in
		Darwin)
			if ! command_exists brew; then
				die "building GNU awk $GAWK_VERSION on macOS requires Homebrew dependencies; install Homebrew or set GAWK_INSTALL_DEPS=0 after installing deps manually"
			fi
			brew install gmp mpfr readline gettext
			local brew_prefix
			brew_prefix="$(brew --prefix)"
			export CPPFLAGS="-I$brew_prefix/opt/gmp/include -I$brew_prefix/opt/mpfr/include -I$brew_prefix/opt/readline/include -I$brew_prefix/opt/gettext/include ${CPPFLAGS:-}"
			export LDFLAGS="-L$brew_prefix/opt/gmp/lib -L$brew_prefix/opt/mpfr/lib -L$brew_prefix/opt/readline/lib -L$brew_prefix/opt/gettext/lib ${LDFLAGS:-}"
			export PKG_CONFIG_PATH="$brew_prefix/opt/gmp/lib/pkgconfig:$brew_prefix/opt/mpfr/lib/pkgconfig:$brew_prefix/opt/readline/lib/pkgconfig:$brew_prefix/opt/gettext/lib/pkgconfig:${PKG_CONFIG_PATH:-}"
			;;
		Linux)
			if command_exists apt-get; then
				sudo apt-get update
				sudo apt-get install -y build-essential ca-certificates curl tar libgmp-dev libmpfr-dev libreadline-dev gettext
			fi
			;;
	esac
}

if [ -x "$GAWK_ORACLE_BIN" ]; then
	require_gawk_version "$GAWK_ORACLE_BIN"
	log "GNU awk $GAWK_VERSION oracle already installed at $GAWK_ORACLE_BIN"
	printf '%s\n' "$GAWK_ORACLE_BIN"
	exit 0
fi

install_deps

build_root="$AWK_HARNESS_CACHE/build/gawk-$GAWK_VERSION"
tarball="$AWK_HARNESS_CACHE/downloads/gawk-$GAWK_VERSION.tar.gz"
mkdir -p "$(dirname "$tarball")" "$(dirname "$build_root")" "$GAWK_ORACLE_PREFIX"

if [ ! -f "$tarball" ]; then
	log "downloading GNU awk $GAWK_VERSION from $GAWK_RELEASE_URL"
	if command_exists curl; then
		curl -fsSL "$GAWK_RELEASE_URL" -o "$tarball"
	elif command_exists wget; then
		wget -O "$tarball" "$GAWK_RELEASE_URL"
	else
		die "curl or wget is required to download $GAWK_RELEASE_URL"
	fi
fi

rm -rf "$build_root"
mkdir -p "$build_root"
log "extracting GNU awk $GAWK_VERSION"
tar -xzf "$tarball" -C "$build_root" --strip-components 1

log "configuring GNU awk $GAWK_VERSION"
(cd "$build_root" && ./configure --prefix="$GAWK_ORACLE_PREFIX")

jobs="${GAWK_MAKE_JOBS:-}"
if [ -z "$jobs" ]; then
	if command_exists nproc; then
		jobs="$(nproc)"
	elif command_exists sysctl; then
		jobs="$(sysctl -n hw.ncpu)"
	else
		jobs=2
	fi
fi

log "building GNU awk $GAWK_VERSION"
(cd "$build_root" && make -j"$jobs")

log "installing GNU awk $GAWK_VERSION into $GAWK_ORACLE_PREFIX"
(cd "$build_root" && make install)

require_gawk_version "$GAWK_ORACLE_BIN"
"$GAWK_ORACLE_BIN" --version | sed -n '1p'
printf '%s\n' "$GAWK_ORACLE_BIN"
