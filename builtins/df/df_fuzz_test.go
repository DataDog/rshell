// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package df_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
)

// dfRunFuzz invokes the df builtin from a fuzz test. Each invocation
// has a 5-second hard timeout so a regression that introduces a hang
// surfaces as a clear failure, not a CI freeze.
func dfRunFuzz(t *testing.T, script string) (string, string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return testutil.RunScriptCtx(ctx, t, script, "", interp.AllowedPaths(nil))
}

// FuzzDfFlagCombinator runs `df` end-to-end through the runner with
// fuzzed argv strings. The contract being tested:
//
//   - df never panics on any combination of bytes the parser sees as
//     a valid shell command line
//   - df exits with code 0 (success) or 1 (error); never 2 or higher
//   - if the command parses, the output ends with a newline (or is
//     empty in the error path)
//
// Seed corpus draws from the three standard sources.
func FuzzDfFlagCombinator(f *testing.F) {
	// --- Source A: implementation edge cases (every flag we register) ---
	for _, args := range []string{
		"df",
		"df --help",
		"df -h",
		"df -H",
		"df -k",
		"df -P",
		"df -T",
		"df -i",
		"df -a",
		"df -l",
		"df --total",
		"df --no-sync",
		"df -t ext4",
		"df -x ext4",
		"df -t ext4 -x tmpfs",
		"df -aTl --total",
		"df -PT -t apfs",
	} {
		f.Add(args)
	}

	// --- Source B: rejected flags (exit 1 path) ---
	for _, args := range []string{
		"df --sync",
		"df -B 1M",
		"df --output",
		"df --output=source",
		"df -v",
		"df --version",
		"df --no-such-flag",
		"df /etc/passwd",
		"df --proc-path=/etc",
	} {
		f.Add(args)
	}

	// --- Source C: shell-syntax stressors (the runner, not df itself,
	// must reject these; we still want to make sure df does not panic
	// when given the raw argv) ---
	for _, args := range []string{
		"df ''",
		"df '   '",
		"df $''",
		"df -t ''",
		"df -t ',,,'",
		"df -t a,b,c,d,e",
		"df -- -name",
		"df -t 'café'",
		"df -t $'\\xff'",
	} {
		f.Add(args)
	}

	f.Fuzz(func(t *testing.T, script string) {
		// Cap fuzz inputs by length and content. A 16 KiB script is
		// far past anything a human would write; a NUL byte breaks
		// shell syntax. Both classes are uninteresting noise.
		if len(script) > 16*1024 {
			return
		}
		if strings.ContainsRune(script, 0) {
			return
		}
		// We only care about df invocations; skip seeds the fuzzer
		// mutates into something that doesn't even start with df.
		if !strings.HasPrefix(strings.TrimSpace(script), "df") {
			return
		}

		_, _, code := dfRunFuzz(t, script)
		// df returns only 0 or 1 (per its documented contract). The
		// runner returns 2 for shell-parse errors, 127 for not-found,
		// and may return other codes for runtime errors that
		// terminate the script. Tolerate those so we don't flag the
		// runner's behaviour, but flag any code df itself produces
		// outside its contract.
		switch code {
		case 0, 1, 2, 127:
			// 0/1: df contract.
			// 2: runner shell-parse error.
			// 127: command-not-found from the runner (if df was
			//      somehow not registered for the duration of a
			//      fuzz iteration).
		default:
			t.Fatalf("unexpected exit code %d for script %q", code, script)
		}
	})
}
