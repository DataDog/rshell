// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ping_test

import (
	"context"
	"fmt"
	"testing"
	"time"
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
	f.Add("-c")

	f.Fuzz(func(t *testing.T, args string) {
		// Use a short context to prevent actual network calls from blocking.
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()

		script := fmt.Sprintf("ping %s", args)

		// Filter out shell-unparseable inputs.
		// The shell parser will reject some inputs, which is fine.
		_, _, code := runScriptCtx(ctx, t, script, t.TempDir())

		// Exit codes 0 and 1 are acceptable.
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for args %q", code, args)
		}
	})
}
