// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package traceroute_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/DataDog/rshell/builtins/testutil"
)

func cmdRunCtxFuzz(ctx context.Context, t testing.TB, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir)
}

// FuzzTracerouteValidation fuzzes traceroute flag parsing and validation.
// All seeds are designed to fail validation (exit 1) so no actual network
// calls are made. The test verifies the command never panics or returns
// unexpected exit codes.
func FuzzTracerouteValidation(f *testing.F) {
	// Invalid max-hops
	f.Add("-m 0 127.0.0.1")
	f.Add("-m -1 127.0.0.1")
	f.Add("-m 256 127.0.0.1")
	f.Add("-m 999999999 127.0.0.1")
	f.Add("-m 2147483648 127.0.0.1")
	f.Add("-m 9999999999999999999 127.0.0.1")

	// Invalid port
	f.Add("-p 0 127.0.0.1")
	f.Add("-p 65536 127.0.0.1")
	f.Add("-p -1 127.0.0.1")
	f.Add("-p 999999999 127.0.0.1")

	// Invalid queries
	f.Add("-q 0 127.0.0.1")
	f.Add("-q 101 127.0.0.1")
	f.Add("-q -1 127.0.0.1")

	// Invalid wait
	f.Add("-w 0 127.0.0.1")
	f.Add("-w 301 127.0.0.1")
	f.Add("-w -1 127.0.0.1")

	// Invalid first-ttl
	f.Add("-f 0 127.0.0.1")
	f.Add("-f -1 127.0.0.1")

	// Invalid delay
	f.Add("--delay -1 127.0.0.1")
	f.Add("--delay 60001 127.0.0.1")

	// Invalid e2e-queries
	f.Add("--e2e-queries -1 127.0.0.1")
	f.Add("--e2e-queries 1001 127.0.0.1")

	// Invalid protocol
	f.Add("--protocol invalid 127.0.0.1")
	f.Add("--protocol '' 127.0.0.1")

	// Invalid TCP method
	f.Add("--tcp-method bogus 127.0.0.1")
	f.Add("--tcp-method '' 127.0.0.1")

	// Unknown flags
	f.Add("--follow 127.0.0.1")
	f.Add("-x 127.0.0.1")
	f.Add("--exec 127.0.0.1")
	f.Add("-F 127.0.0.1")

	// Missing/excess args
	f.Add("")
	f.Add("host1 host2")
	f.Add("host1 host2 host3")

	// Help (exits 0)
	f.Add("--help")
	f.Add("-h")

	// first-ttl > max-hops
	f.Add("-f 10 -m 5 127.0.0.1")

	f.Fuzz(func(t *testing.T, args string) {
		if len(args) > 1024 {
			return
		}
		dir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		script := fmt.Sprintf("traceroute %s", args)
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for args %q", code, args)
		}
	})
}

// FuzzTracerouteHostname fuzzes the hostname argument with validation-only
// parameters to minimize network calls. Uses invalid port (0) to ensure
// the command fails on validation before reaching the network.
func FuzzTracerouteHostname(f *testing.F) {
	f.Add("")
	f.Add("a b") // two args → too many arguments
	f.Add("a b c")

	f.Fuzz(func(t *testing.T, hostname string) {
		if len(hostname) > 1024 {
			return
		}
		// Filter out shell-special characters
		for _, c := range hostname {
			if c == '\'' || c == '"' || c == '\\' || c == '$' || c == '`' ||
				c == '(' || c == ')' || c == '{' || c == '}' ||
				c == ';' || c == '|' || c == '&' || c == '<' || c == '>' ||
				c == '\n' || c == '\r' || c == '#' {
				return
			}
		}
		dir := t.TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		// Use -p 0 to fail on port validation, not network
		script := fmt.Sprintf("traceroute -p 0 -- %s", hostname)
		_, _, code := cmdRunCtxFuzz(ctx, t, script, dir)
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for hostname %q", code, hostname)
		}
	})
}
