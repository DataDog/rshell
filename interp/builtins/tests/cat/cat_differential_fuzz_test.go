// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build !windows

package cat_test

import (
	"bytes"
	"context"
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

// FuzzCatDifferential compares rshell cat output against GNU cat.
func FuzzCatDifferential(f *testing.F) {
	if os.Getenv("RSHELL_BASH_TEST") == "" {
		f.Skip("set RSHELL_BASH_TEST=1 to run differential fuzz tests")
	}

	f.Add([]byte("hello\nworld\n"))
	f.Add([]byte(""))
	f.Add([]byte("no newline"))
	f.Add([]byte("a\x00b\n"))
	f.Add(bytes.Repeat([]byte("x"), 4097))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte{0xff, 0xfe, 0x00, 0x01})
	f.Add([]byte("line1\nline2\nline3\n"))

	f.Fuzz(func(t *testing.T, input []byte) {
		if len(input) > 64*1024 {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "input.txt"), input, 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		rshellOut, rshellErr, rshellCode := cmdRunCtx(ctx, t, "cat input.txt", dir)

		if isSandboxError(rshellErr) {
			t.Skip("skipping: sandbox restriction")
		}

		gnuOut, gnuCode := runGNUInDir(t, dir, []string{"cat", "input.txt"})
		if gnuCode == -1 {
			return
		}

		if rshellOut != gnuOut {
			t.Errorf("stdout mismatch:\nrshell: %q\ngnu:    %q\ninput:  %q", rshellOut, gnuOut, input)
		}
		if rshellCode != gnuCode {
			t.Errorf("exit code mismatch: rshell=%d gnu=%d", rshellCode, gnuCode)
		}
	})
}
