// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package truncate_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// FuzzTruncateSize fuzzes the -s SIZE parser by routing arbitrary
// byte-strings through the runner. The corpus seeds every distinct
// numeric / suffix / modifier shape parseSize is expected to handle, and
// the fuzz body asserts only that exit code is 0 or 1 (no panic, no
// hang, no other status).
func FuzzTruncateSize(f *testing.F) {
	// Bare digits — boundaries.
	f.Add("0")
	f.Add("1")
	f.Add("9223372036854775807")  // exact MaxInt64.
	f.Add("9223372036854775808")  // one above.
	f.Add("99999999999999999999") // far past int64.
	f.Add("00000000000000000000")

	// Every accepted suffix.
	for _, s := range []string{
		"K", "k", "KB", "KiB",
		"M", "m", "MB", "MiB",
		"G", "g", "GB", "GiB",
		"T", "t", "TB", "TiB",
	} {
		f.Add("0" + s)
		f.Add("1" + s)
		f.Add("123" + s)
	}

	// Suffix-overflow neighbours.
	f.Add("8388607T") // largest accepted T-suffix.
	f.Add("8388608T") // overflow.
	f.Add("9007199254740992K")

	// GNU relative-size modifiers — must always reject with errRelativeSize.
	for _, p := range []string{"+", "-", "<", ">", "/", "%"} {
		f.Add(p)
		f.Add(p + "0")
		f.Add(p + "10")
		f.Add(p + "10K")
	}

	// Malformed inputs.
	f.Add("")
	f.Add(" ")
	f.Add("   ")
	f.Add("abc")
	f.Add("1.5")
	f.Add("0x10")
	f.Add("0b10")
	f.Add("1KIB")
	f.Add("1kB")
	f.Add("1MiB1")
	f.Add("K")
	f.Add("KB")
	f.Add("--size=0")

	// CVE-class adversarial inputs: huge integer string, embedded null
	// bytes, control characters. The shell parser may strip some of
	// these before they reach truncate; we rely on the runner to model
	// realistic shell behaviour.
	f.Add(strings.Repeat("9", 100))
	f.Add("0\x00")
	f.Add("\x00")
	f.Add("\x1b[0m")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, sizeArg string) {
		if t.Context().Err() != nil {
			return
		}
		// Cap size string length to keep iterations cheap; an attacker-
		// controlled size larger than 1 KiB is not a realistic scenario.
		if len(sizeArg) > 1024 {
			return
		}
		// Reject size strings containing shell metacharacters that would
		// alter parsing — we want to exercise parseSize, not the shell.
		if strings.ContainsAny(sizeArg, "'\"\\$`\n\r") {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		script := "truncate -s '" + sizeArg + "' f.txt"
		_, _, code := runScriptCtxFuzz(ctx, t, script, dir)
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for size %q", code, sizeArg)
		}
	})
}

// FuzzTruncateFlags fuzzes the flag parser by submitting arbitrary
// argument strings before a fixed --size 0 and a single file operand.
// Confirms that no flag combination crashes the runner — only exit codes
// 0 and 1 are valid outcomes.
func FuzzTruncateFlags(f *testing.F) {
	// Known flags and combinations.
	f.Add("")
	f.Add("-c")
	f.Add("--no-create")
	f.Add("--help")
	f.Add("-h")
	f.Add("-cs")   // missing value for -s; expected reject.
	f.Add("-c -s") // separated, missing value.
	f.Add("--size=0 -c")
	f.Add("-c -c")             // duplicate boolean.
	f.Add("--size=0 --size=1") // duplicate value flag.
	// Unknown / deferred flags — must be rejected.
	f.Add("-r ref.txt")
	f.Add("--reference=ref.txt")
	f.Add("-o")
	f.Add("--io-blocks")
	f.Add("--unknown")
	f.Add("-X")
	// Double-dash separator games.
	f.Add("--")
	f.Add("-- -file")
	f.Add("-- --size=0")
	// Numeric-looking flags (which would matter if NormalizeArgs were ever
	// added later — currently truncate has no normaliser, so -5 is just an
	// unknown short flag).
	f.Add("-5")
	f.Add("--size -5")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, flagArgs string) {
		if t.Context().Err() != nil {
			return
		}
		if len(flagArgs) > 256 {
			return
		}
		if strings.ContainsAny(flagArgs, "'\"\\$`\n\r;|&><()") {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()
		if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		script := "truncate " + flagArgs + " f.txt"
		_, _, code := runScriptCtxFuzz(ctx, t, script, dir)
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for flags %q", code, flagArgs)
		}
	})
}

// FuzzTruncateFilenames fuzzes the operand path. Exit codes other than 0
// or 1 indicate a panic or unhandled error path. AllowedPaths is set to
// dir, so paths outside dir must be rejected by the sandbox cleanly.
func FuzzTruncateFilenames(f *testing.F) {
	// Plain names.
	f.Add("file.txt")
	f.Add("a")
	f.Add("dir/file")
	// Path-traversal payloads.
	f.Add("../escape")
	f.Add("../../etc/hosts")
	f.Add("./././file")
	f.Add("//double//slash//file")
	// Absolute paths outside the sandbox.
	f.Add("/etc/hosts")
	f.Add("/dev/null")
	f.Add("/tmp/escape")
	// Special characters and unicode.
	f.Add("file with spaces.txt")
	f.Add("file\twith\ttabs.txt")
	f.Add("é-unicode.txt")
	f.Add("漢字.txt")
	// Long names.
	f.Add(strings.Repeat("a", 200) + ".txt")
	// Names that look like flags (will be passed positional, not as flag).
	f.Add("-dashfile")
	f.Add("--dashed-file")
	f.Add("-c")

	baseDir := f.TempDir()
	var counter atomic.Int64

	f.Fuzz(func(t *testing.T, filename string) {
		if t.Context().Err() != nil {
			return
		}
		if len(filename) == 0 || len(filename) > 512 {
			return
		}
		// Filter shell-special chars; we want to exercise the sandbox /
		// open path, not shell parsing.
		if strings.ContainsAny(filename, "'\"\\$`\n\r;|&><()") {
			return
		}
		// Reject strings with embedded NULs which the OS rejects on most
		// filesystems before reaching truncate's logic.
		if strings.ContainsRune(filename, 0) {
			return
		}

		dir, cleanup := testutil.FuzzIterDir(t, baseDir, &counter)
		defer cleanup()

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		script := "truncate -s 0 -- '" + filename + "'"
		_, _, code := runScriptCtxFuzz(ctx, t, script, dir)
		if t.Context().Err() != nil {
			return
		}
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for filename %q", code, filename)
		}
	})
}

// runScriptCtxFuzz wraps runScriptCtx with AllowedPaths set to dir so the
// sandbox boundary matches the temp directory each fuzz iteration uses.
// The name is distinct from runScriptCtx to avoid future collisions if
// the integration-test helper grows additional positional parameters.
func runScriptCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return runScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}
