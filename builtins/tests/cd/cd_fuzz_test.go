// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package cd_test fuzzes the cd builtin from the outside, through the
// shell interpreter. Each Fuzz* function picks a different mode of cd
// (positional path, `cd -`, `cd` with HOME) so the seed corpus is
// focused and a failing input is easy to attribute. A failure is any
// panic, hang, or exit code outside {0, 1}.

package cd_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// shellSafe filters fuzz inputs that would crash the shell parser before
// the cd builtin sees them. The parser rejects invalid UTF-8 and treats
// embedded NUL/newlines as syntax errors — neither is interesting to the
// cd-command pentest.
//
// Some C1 control characters (e.g. U+0080, encoded 0xC2 0x80) trigger a
// known mvdan.cc/sh/v3 tokenizer quirk inside single/double quotes
// ("reached EOF without closing quote"). We do *not* filter them here:
// the parser surfaces a normal exit-1 error which the harness's
// `code != 0 && code != 1` check tolerates. The seed corpus entry in
// testdata/fuzz/FuzzCdFlags/be32d37903cefe74 exists as a regression to
// confirm cd survives that parse failure without panicking.
func shellSafe(s string) bool {
	if strings.ContainsAny(s, "\x00\n") {
		return false
	}
	return utf8.ValidString(s)
}

// pathSeeds collects the corpus of path strings exercised by FuzzCdPath.
// Every entry encodes a distinct concern; the comments describe what
// each one is meant to surface so a future maintainer can extend the
// list without reintroducing duplicates.
var pathSeeds = []string{
	// --- Implementation edges ---
	"",           // empty: rejected with "no such file or directory"
	".",          // self
	"..",         // parent
	"./.",        // redundant components
	"sub",        // simple relative target (must exist for the target dir below)
	"sub/",       // trailing slash
	"./sub",      // explicit-relative
	"sub/./.",    // redundant
	"sub/../sub", // round trip
	"a/b/c",      // nested missing
	"-",          // dash means OLDPWD; covered separately too
	"--",         // pflag end-of-flags
	"-funny",     // dash-prefixed name
	" sub",       // leading whitespace
	"sub\t",      // trailing tab
	// Note: paths with embedded newlines ("\n") or NULs ("\x00") are
	// filtered by shellSafe() before reaching cd — the shell parser
	// treats them as syntax errors. They are not useful seeds.
	"sub/../../..", // upward escape attempts
	"//double",     // duplicated separator
	"/etc",         // absolute outside sandbox
	"/dev/null",    // device file (must reject as not-a-directory)

	// --- CVE-class long-input probes ---
	strings.Repeat("a", 4096),
	strings.Repeat("a", 4097),
	strings.Repeat("a", 65535), // just under maxPathBytes
	strings.Repeat("a", 65536), // exactly at maxPathBytes
	strings.Repeat("a", 65537), // just over maxPathBytes (must be rejected)

	// --- Encoding probes ---
	"\xff\xfe",                 // bad UTF-8
	"\xed\xa0\x80",             // surrogate half
	"\xfc\x80\x80\x80\x80\xaf", // overlong null
	"\x7fELF",                  // ELF magic prefix
	"PK\x03\x04",               // ZIP magic prefix

	// --- Existing test inputs ---
	"sub", "child", "real", "alias",
}

func FuzzCdPath(f *testing.F) {
	for _, s := range pathSeeds {
		f.Add(s)
	}

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, input string) {
		// Cap at 1 MiB; cd's own bound is 64 KiB but Go fuzz strings can
		// be up to 1 MiB before we even start.
		if len(input) > 1<<20 {
			return
		}
		// Filter inputs that would cause shell parse errors (the fuzzer
		// targets the cd builtin, not the parser).
		if !shellSafe(input) {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		// Pre-create a real `sub` so that a meaningful fraction of
		// inputs exercise the success path.
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		// Wrap input in single quotes; cd parses one token.
		quoted := "'" + strings.ReplaceAll(input, "'", "'\\''") + "'"
		_, _, code := cmdRunCtxFuzz(ctx, t, "cd "+quoted, dir)
		if code != 0 && code != 1 {
			t.Errorf("cd %q: unexpected exit code %d", input, code)
		}
	})
}

// FuzzCdDash exercises the `cd -` path with arbitrary OLDPWD values via
// the Env runner option. It verifies that no OLDPWD value produces a
// crash or unexpected exit code.
func FuzzCdDash(f *testing.F) {
	for _, s := range pathSeeds {
		f.Add(s)
	}
	f.Add("/")
	f.Add("/tmp")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, oldpwd string) {
		if len(oldpwd) > 1<<20 {
			return
		}
		if !shellSafe(oldpwd) {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "cd -", dir, interp.Env("OLDPWD="+oldpwd))
		if code != 0 && code != 1 {
			t.Errorf("cd - with OLDPWD=%q: unexpected exit code %d", oldpwd, code)
		}
	})
}

// FuzzCdHome exercises the bare-`cd` path with arbitrary HOME values.
func FuzzCdHome(f *testing.F) {
	for _, s := range pathSeeds {
		f.Add(s)
	}

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, home string) {
		if len(home) > 1<<20 {
			return
		}
		if !shellSafe(home) {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "cd", dir, interp.Env("HOME="+home))
		if code != 0 && code != 1 {
			t.Errorf("cd with HOME=%q: unexpected exit code %d", home, code)
		}
	})
}

// FuzzCdFlags exercises pflag's parsing of arbitrary flag-like first
// tokens. The fuzzer targets unexpected pflag interactions and must not
// produce panics or unexpected exit codes.
func FuzzCdFlags(f *testing.F) {
	flagSeeds := []string{
		"-L", "-P", "-LP", "-PL", "-LL", "-PP",
		"--logical", "--physical", "--logical=true", "--physical=false",
		"--help", "-h",
		"--", "---", "-",
		"-x", "-PLh", "-help", "--no-such-flag",
		"--LOGICAL", // case
		"-\x00",     // embedded NUL
	}
	for _, s := range flagSeeds {
		f.Add(s)
	}

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, flag string) {
		if len(flag) > 256 {
			return
		}
		if !shellSafe(flag) {
			return
		}
		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		quoted := "'" + strings.ReplaceAll(flag, "'", "'\\''") + "'"
		_, _, code := cmdRunCtxFuzz(ctx, t, "cd "+quoted+" sub", dir)
		if code != 0 && code != 1 {
			t.Errorf("cd %q sub: unexpected exit code %d", flag, code)
		}
	})
}
