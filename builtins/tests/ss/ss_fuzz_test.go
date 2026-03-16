// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Fuzz tests for the ss builtin (black-box, all platforms).
//
// ss reads kernel socket state rather than user-supplied files, so this fuzz
// test focuses on flag parsing — ensuring arbitrary flag input never panics
// and always returns exit code 0 or 1.
//
// Seed corpus is built from:
//
//	A. All flag names implemented and rejected (from ss.go comments).
//	B. CVE-class inputs: empty, whitespace, oversized, special chars.
//	C. All flag inputs exercised in existing unit and pentest tests.
package ss_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins/testutil"
)

// cmdRunCtxFuzzSS runs an ss command with no AllowedPaths restriction.
// ss reads kernel state directly, not sandboxed files.
func cmdRunCtxFuzzSS(ctx context.Context, t *testing.T, script string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, "")
}

// FuzzSSFlags fuzzes the ss flag argument string to ensure no flag combination
// ever panics or returns an unexpected exit code.
//
// Only exit codes 0 and 1 are acceptable; any other code is a bug.
func FuzzSSFlags(f *testing.F) {
	// Source A: implemented flags (should succeed: exit 0)
	f.Add("-t")
	f.Add("-u")
	f.Add("-x")
	f.Add("-l")
	f.Add("-a")
	f.Add("-n")
	f.Add("-4")
	f.Add("-6")
	f.Add("-s")
	f.Add("-H")
	f.Add("-o")
	f.Add("-e")
	f.Add("-h")
	f.Add("--help")
	// Combinations from existing tests
	f.Add("-tan")
	f.Add("-lH")
	f.Add("-4tn")
	f.Add("-6un")
	f.Add("-ans")
	f.Add("-taH")
	f.Add("-tln")

	// Source A: rejected flags (must exit 1, not panic)
	f.Add("-F")
	f.Add("-p")
	f.Add("-K")
	f.Add("-E")
	f.Add("-N")
	f.Add("-r")
	f.Add("-b")
	f.Add("--filter")
	f.Add("--processes")
	f.Add("--kill")
	f.Add("--events")
	f.Add("--net")

	// Source B: CVE-class / edge case inputs
	f.Add("")                       // empty flags
	f.Add("   ")                    // whitespace only
	f.Add("--no-such-flag")         // unknown long flag
	f.Add("-Z")                     // unknown short flag
	f.Add("--")                     // end-of-flags only
	f.Add("-- -H")                  // end-of-flags + positional
	f.Add("-\x00")                  // null byte in flag
	f.Add("--\x00flag")             // null byte in long flag name
	f.Add("-ttttttttttttttttttttt") // repeated flags
	f.Add("--tcp --udp --all --numeric --summary")

	// Source C: existing unit and pentest test flag inputs
	f.Add("-F /dev/null")
	f.Add("-K")
	f.Add("-E")
	f.Add("-N ns0")
	f.Add("-an extra_arg_1 extra_arg_2")

	f.Fuzz(func(t *testing.T, flags string) {
		// Cap input length to prevent shell-parse errors from excessively long strings.
		if len(flags) > 256 {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtxFuzzSS(ctx, t, fmt.Sprintf("ss %s", flags))
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for flags %q", code, flags)
		}
	})
}
