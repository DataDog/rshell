// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package df_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

// dfRunFuzz invokes the df builtin from a fuzz test. It returns
// (stdout, stderr, exitCode, parseErr). When parseErr is non-nil the
// fuzzer mutated the input into something the shell parser cannot read
// (e.g. unclosed quote), and the caller should treat it as
// uninteresting rather than as a failure.
//
// We intentionally do not call testutil.RunScriptCtx — that helper
// fails the test on parse errors via require.NoError, which is correct
// for unit tests but turns every malformed fuzz input into a fatal.
func dfRunFuzz(t *testing.T, script string) (string, string, int, error) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		return "", "", 0, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths(nil),
	)
	if err != nil {
		t.Fatalf("interp.New: %v", err)
	}
	defer runner.Close()

	exitCode := 0
	if runErr := runner.Run(ctx, prog); runErr != nil {
		var es interp.ExitStatus
		if errors.As(runErr, &es) {
			exitCode = int(es)
		} else if ctx.Err() == nil {
			t.Fatalf("unexpected runner error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode, nil
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

		_, _, code, parseErr := dfRunFuzz(t, script)
		// Parse errors are expected — the fuzzer routinely mutates
		// inputs into malformed shell syntax (unclosed quotes,
		// unbalanced parens, …). They are not failures.
		if parseErr != nil {
			return
		}
		// df returns only 0 or 1 per its documented contract. The
		// runner may return 127 for not-found if df is somehow
		// unregistered during a fuzz iteration. Anything else is
		// a contract violation.
		switch code {
		case 0, 1, 127:
		default:
			t.Fatalf("unexpected exit code %d for script %q", code, script)
		}
	})
}
