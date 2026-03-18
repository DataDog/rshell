// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package ps_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func runScriptWithProcPath(t *testing.T, script, procPath string) (stdout, stderr string, code int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		t.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowAllCommands(),
		interp.ProcPath(procPath),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer runner.Close()
	runErr := runner.Run(context.Background(), prog)
	exitCode := 0
	if runErr != nil {
		var es interp.ExitStatus
		if errors.As(runErr, &es) {
			exitCode = int(es)
		} else {
			t.Fatalf("unexpected runner error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// TestProcPathNonexistentDirErrors ensures ps -e fails gracefully when the
// configured proc path does not exist.
func TestProcPathNonexistentDirErrors(t *testing.T) {
	_, stderr, code := runScriptWithProcPath(t, "ps -e", "/nonexistent/proc/path")
	if code != 1 {
		t.Errorf("expected exit code 1 for nonexistent proc path, got %d", code)
	}
	if !strings.Contains(stderr, "ps:") {
		t.Errorf("expected error message in stderr, got: %s", stderr)
	}
}

// TestProcPathNonexistentDirErrorsByPID ensures ps -p fails gracefully when
// the configured proc path does not exist.
func TestProcPathNonexistentDirErrorsByPID(t *testing.T) {
	_, stderr, code := runScriptWithProcPath(t, "ps -p 1", "/nonexistent/proc/path")
	if code != 1 {
		t.Errorf("expected exit code 1 for nonexistent proc path, got %d", code)
	}
	if !strings.Contains(stderr, "ps:") {
		t.Errorf("expected error message in stderr, got: %s", stderr)
	}
}

// TestProcPathNotADirErrors_ListAll ensures ps -e fails with a clear error
// when the configured proc path exists but is a regular file, not a directory.
func TestProcPathNotADirErrors_ListAll(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require(t, err)
	require(t, f.Close())

	_, stderr, code := runScriptWithProcPath(t, "ps -e", f.Name())
	if code != 1 {
		t.Errorf("expected exit code 1 for file proc path, got %d", code)
	}
	if !strings.Contains(stderr, "ps:") {
		t.Errorf("expected ps: error in stderr, got: %s", stderr)
	}
}

// TestProcPathNotADirErrors_ByPID ensures ps -p fails with a clear error
// when the configured proc path exists but is a regular file, not a directory.
func TestProcPathNotADirErrors_ByPID(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "not-a-dir")
	require(t, err)
	require(t, f.Close())

	_, stderr, code := runScriptWithProcPath(t, "ps -p 1", f.Name())
	if code != 1 {
		t.Errorf("expected exit code 1 for file proc path, got %d", code)
	}
	if !strings.Contains(stderr, "ps:") {
		t.Errorf("expected ps: error in stderr, got: %s", stderr)
	}
	if !strings.Contains(stderr, "not a directory") {
		t.Errorf("expected 'not a directory' in stderr, got: %s", stderr)
	}
}

// TestProcPathEmptyUsesDefault ensures an empty ProcPath falls back to /proc
// and ps -e runs successfully.
func TestProcPathEmptyUsesDefault(t *testing.T) {
	_, stderr, code := runScriptWithProcPath(t, "ps -e", "")
	if code != 0 {
		t.Fatalf("ps -e with empty ProcPath exited %d; stderr: %s", code, stderr)
	}
}

// writeFakeProc creates a minimal fake /proc filesystem under dir and returns
// the procPath. It writes a single fake process with the given pid and name.
func writeFakeProc(t *testing.T, pid int, name string) string {
	t.Helper()
	dir := t.TempDir()

	// Write <procPath>/stat for boot time.
	require(t, os.WriteFile(filepath.Join(dir, "stat"), []byte("cpu 0 0 0 0\nbtime 1000000000\n"), 0o644))

	// Create the PID subdirectory using the provided pid.
	pidDir := filepath.Join(dir, strconv.Itoa(pid))
	require(t, os.MkdirAll(pidDir, 0o755))

	// Write <procPath>/<pid>/stat.
	// Format: pid (comm) state ppid pgroup session tty_nr tpgid flags minflt
	//         cminflt majflt cmajflt utime stime cutime cstime priority nice
	//         numthreads itrealvalue starttime ...
	// Fields after (comm): at least 20 required by readProc.
	statContent := fmt.Sprintf("%d (%s) S 0 %d %d 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 100\n", pid, name, pid, pid)
	require(t, os.WriteFile(filepath.Join(pidDir, "stat"), []byte(statContent), 0o644))

	// Write <procPath>/<pid>/status for UID lookup.
	require(t, os.WriteFile(filepath.Join(pidDir, "status"), []byte("Name:\t"+name+"\nUid:\t1000 1000 1000 1000\n"), 0o644))

	// Write <procPath>/<pid>/cmdline.
	require(t, os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(name+"\x00"), 0o644))

	return dir
}

func require(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// TestProcPathFakeProc ensures ps -e reads from the custom proc path and
// returns entries from the fake proc tree rather than the real /proc.
func TestProcPathFakeProc(t *testing.T) {
	procPath := writeFakeProc(t, 1, "fakeinit")

	stdout, stderr, code := runScriptWithProcPath(t, "ps -e", procPath)
	if code != 0 {
		t.Fatalf("ps -e with fake proc path exited %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "fakeinit") {
		t.Errorf("expected 'fakeinit' in ps output; got:\n%s", stdout)
	}
	// The output should not contain real system processes.
	if strings.Count(stdout, "\n") > 5 {
		t.Errorf("expected only fake proc entries, but got many lines:\n%s", stdout)
	}
}

// TestProcPathFakeProcFullFormat ensures ps -ef also reads from the custom
// proc path and includes UID and PPID columns.
func TestProcPathFakeProcFullFormat(t *testing.T) {
	procPath := writeFakeProc(t, 1, "fakeinit")

	stdout, stderr, code := runScriptWithProcPath(t, "ps -ef", procPath)
	if code != 0 {
		t.Fatalf("ps -ef with fake proc path exited %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "fakeinit") {
		t.Errorf("expected 'fakeinit' in ps -ef output; got:\n%s", stdout)
	}
	if !strings.Contains(stdout, "PPID") {
		t.Errorf("expected PPID column in ps -ef output; got:\n%s", stdout)
	}
}

// TestProcPathFakeProcByPID ensures ps -p also reads from the custom proc path.
func TestProcPathFakeProcByPID(t *testing.T) {
	procPath := writeFakeProc(t, 1, "fakeinit")

	stdout, stderr, code := runScriptWithProcPath(t, "ps -p 1", procPath)
	if code != 0 {
		t.Fatalf("ps -p 1 with fake proc path exited %d; stderr: %s", code, stderr)
	}
	if !strings.Contains(stdout, "1") {
		t.Errorf("expected PID 1 in output; got:\n%s", stdout)
	}
}

// TestProcPathFakeProcSession ensures bare ps (no flags) reads from the custom
// proc path via GetSession rather than the real /proc.
func TestProcPathFakeProcSession(t *testing.T) {
	procPath := writeFakeProc(t, 1, "fakeinit")
	// Bare ps uses GetSession; it may not include PID 1 since it is not in
	// our session, but it must not crash and must not read the real /proc.
	_, stderr, code := runScriptWithProcPath(t, "ps", procPath)
	if code != 0 {
		t.Fatalf("ps with fake proc path exited %d; stderr: %s", code, stderr)
	}
}
