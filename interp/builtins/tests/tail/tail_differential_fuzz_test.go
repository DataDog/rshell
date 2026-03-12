// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package tail_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func gnuCmd(name string) string {
	if runtime.GOOS == "darwin" {
		return "g" + name
	}
	return name
}

// runGNUInDir runs a GNU command with its working directory set to dir.
// args[0] is the command name (without the "g" prefix on darwin).
// args[1:] are the arguments.
func runGNUInDir(t *testing.T, dir string, args []string) (stdout string, exitCode int) {
	t.Helper()
	gnuName := gnuCmd(args[0])
	if _, err := exec.LookPath(gnuName); err != nil {
		t.Skipf("%s not found: %v", gnuName, err)
	}

	cmd := exec.Command(gnuName, args[1:]...)
	cmd.Dir = dir

	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Logf("gnu exec error: %v", err)
			return "", -1
		}
	}
	return outBuf.String(), exitCode
}

func isSandboxError(stderr string) bool {
	lower := strings.ToLower(stderr)
	return strings.Contains(lower, "permission denied") ||
		strings.Contains(lower, "not allowed") ||
		strings.Contains(lower, "sandbox")
}

// FuzzTailDifferential compares rshell tail -n N output against GNU tail.
func FuzzTailDifferential(f *testing.F) {
	if os.Getenv("RSHELL_BASH_TEST") == "" {
		f.Skip("set RSHELL_BASH_TEST=1 to run differential fuzz tests")
	}

	f.Add([]byte("line1\nline2\nline3\n"), int64(2))
	f.Add([]byte(""), int64(0))
	f.Add([]byte("no newline"), int64(1))
	f.Add([]byte("a\nb\nc\n"), int64(100))
	f.Add([]byte("\n\n\n"), int64(2))
	f.Add([]byte("a\x00b\nc\n"), int64(2))
	f.Add([]byte("single line\n"), int64(1))
	f.Add([]byte("a\nb\nc\nd\ne\n"), int64(3))
	f.Add(bytes.Repeat([]byte("line\n"), 20), int64(5))

	f.Fuzz(func(t *testing.T, input []byte, n int64) {
		if len(input) > 64*1024 {
			return
		}
		if n < 0 || n > 10000 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		nStr := fmt.Sprintf("%d", n)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rshellOut, rshellErr, rshellCode := cmdRunCtx(ctx, t, fmt.Sprintf("tail -n %s input.txt", nStr), dir)

		if isSandboxError(rshellErr) {
			t.Skip("skipping: sandbox restriction")
		}

		// Skip if rshell reports an internal limit was exceeded (ring buffer overflow etc.)
		if strings.Contains(rshellErr, "too large") || strings.Contains(rshellErr, "exceeds") {
			t.Skip("skipping: rshell internal limit exceeded")
		}

		gnuOut, gnuCode := runGNUInDir(t, dir, []string{"tail", "-n", nStr, "input.txt"})
		if gnuCode == -1 {
			return
		}

		if rshellOut != gnuOut {
			t.Errorf("tail -n %d stdout mismatch:\nrshell: %q\ngnu:    %q\ninput:  %q", n, rshellOut, gnuOut, input)
		}
		if rshellCode != gnuCode {
			t.Errorf("tail -n %d exit code mismatch: rshell=%d gnu=%d", n, rshellCode, gnuCode)
		}
	})
}
