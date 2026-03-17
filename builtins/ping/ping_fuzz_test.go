// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ping_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"
)

// FuzzPingFlags fuzzes the flag parsing of the ping command.
// Since ping makes network calls, we use a very short timeout so that
// valid flag combinations exit quickly (either via ICMP error or timeout).
func FuzzPingFlags(f *testing.F) {
	// Seed corpus from existing tests.
	f.Add("-c 1 localhost")
	f.Add("-c 0 localhost")
	f.Add("-c -1 localhost")
	f.Add("-c 99999999999999999999 localhost")
	f.Add("-W 0 localhost")
	f.Add("-W -1 localhost")
	f.Add("-i 0 localhost")
	f.Add("-i -1 localhost")
	f.Add("-i 0.5 localhost")
	f.Add("-s 0 localhost")
	f.Add("-s -1 localhost")
	f.Add("-s 24 localhost")
	f.Add("-s 56 localhost")
	f.Add("-s 65507 localhost")
	f.Add("-s 65508 localhost")
	f.Add("-s abc localhost")
	f.Add("-s 99999999999999999999 localhost")
	f.Add("-4 localhost")
	f.Add("-6 localhost")
	f.Add("-4 -6 localhost")
	// Integer overflow / CVE-class inputs for -c and -W.
	f.Add("-c 2147483647 localhost")          // MaxInt32
	f.Add("-c 9223372036854775807 localhost") // MaxInt64
	f.Add("-W 2147483647 localhost")          // MaxInt32 timeout
	// Long-form flags with new options.
	f.Add("--size 24 localhost")
	f.Add("--size 65507 localhost")
	f.Add("--size -1 localhost")
	// Combined valid flags.
	f.Add("-c 1 -s 56 -4 localhost")
	f.Add("-c 1 -s 56 -6 localhost")
	f.Add("--help")
	f.Add("-h")
	f.Add("-f localhost")
	f.Add("--follow localhost")
	f.Add("")
	f.Add("host1 host2")
	f.Add("-c abc localhost")
	f.Add("-W abc localhost")
	f.Add("--count 0 localhost")
	f.Add("--timeout 0 localhost")
	f.Add("--interval 0 localhost")
	f.Add("--size 0 localhost")
	f.Add("-c")

	f.Fuzz(func(t *testing.T, args string) {
		// Use a short context to prevent actual network calls from blocking.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		script := fmt.Sprintf("ping %s", args)

		// Skip inputs that the shell parser cannot parse (unmatched quotes,
		// special characters that create multi-command scripts, etc.).
		// This ensures we fuzz ping flag handling, not the shell parser.
		parser := syntax.NewParser()
		file, err := parser.Parse(strings.NewReader(script), "")
		if err != nil {
			return
		}

		// Skip inputs that produce anything other than a single simple
		// command. Pipes (e.g. "ping |0"), background jobs ("ping &cmd"),
		// and multi-statement scripts are valid shell syntax but test
		// the shell runtime, not ping's flag parsing.
		if len(file.Stmts) != 1 {
			return
		}
		stmt := file.Stmts[0]
		if stmt.Background || stmt.Coprocess || stmt.Negated {
			return
		}
		if _, ok := stmt.Cmd.(*syntax.CallExpr); !ok {
			return
		}
		if len(stmt.Redirs) > 0 {
			return
		}

		_, _, code := runScriptCtx(ctx, t, script, t.TempDir())

		// Exit codes 0, 1, and 2 are acceptable.
		// Code 2 can occur when the shell rejects a syntactically valid
		// but unsupported construct at runtime (e.g., background operator &).
		if code != 0 && code != 1 && code != 2 {
			t.Errorf("unexpected exit code %d for args %q", code, args)
		}
	})
}
