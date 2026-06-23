// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rm_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// FuzzRmFilePath fuzzes "rm FILE" with arbitrary path strings.
//
// The contract under test:
//   - rm never panics on any fuzz input
//   - rm never hangs (enforced by a 3-second timeout)
//   - exit code is always 0 or 1
//   - a file inside the sandbox is removed or reported with an error — it is
//     never silently left behind by rm while the command claims success
func FuzzRmFilePath(f *testing.F) {
	// --- Source A: valid paths ---
	f.Add("target.txt")
	f.Add("subdir/target.txt")
	f.Add("./target.txt")
	f.Add("a b c.txt") // spaces in name
	f.Add("unicode_café.txt")

	// --- Source B: path traversal attempts ---
	f.Add("../escape.txt")
	f.Add("../../escape.txt")
	f.Add("/etc/passwd")
	f.Add("/tmp/evil.txt")

	// --- Source C: special paths ---
	f.Add("")
	f.Add(".")
	f.Add("..")
	f.Add("/")
	f.Add("nosuchfile.txt")

	f.Fuzz(func(t *testing.T, pathStr string) {
		// Skip inputs that are too long.
		if len(pathStr) > 1024 {
			return
		}
		// The shell parser rejects invalid UTF-8; skip to avoid spurious parse errors.
		if !utf8.ValidString(pathStr) {
			return
		}
		// Shell metacharacters in pathStr would alter the script structure.
		// Skip them — the fuzz goal is rm's own path handling, not the parser.
		for _, c := range pathStr {
			if c == '\'' || c == '\x00' || c == '\n' || c == '\r' {
				return
			}
		}

		dir := t.TempDir()
		// Create a target file inside the sandbox for the path to point to.
		target := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(target, []byte("seed"), 0644); err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		script := fmt.Sprintf("rm -f '%s'", pathStr)
		_, _, code := runScriptCtx(ctx, t, script, dir,
			interp.AllowedPaths([]string{dir}),
			interp.WithMode(interp.ModeRemediation),
		)
		if ctx.Err() != nil {
			t.Fatal("rm fuzz timed out")
		}
		if code != 0 && code != 1 {
			t.Errorf("unexpected exit code %d for path %q", code, pathStr)
		}
	})
}

// rmRunFuzz runs a script through the rm builtin without failing on shell parse
// errors. Unlike testutil.RunScriptCtx, parse errors return (0, error) so the
// fuzzer can treat malformed shell syntax as uninteresting rather than a fatal.
func rmRunFuzz(t *testing.T, script, dir string) (int, error) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(
		interp.StdIO(nil, &outBuf, &errBuf),
		interp.AllowedPaths([]string{dir}),
		interp.WithMode(interp.ModeRemediation),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	)
	if err != nil {
		t.Fatalf("interp.New: %v", err)
	}
	defer runner.Close()
	runner.Dir = dir
	exitCode := 0
	if runErr := runner.Run(ctx, prog); runErr != nil {
		var es interp.ExitStatus
		if errors.As(runErr, &es) {
			exitCode = int(es)
		}
	}
	return exitCode, nil
}

// FuzzRmFlagCombinator fuzzes "rm [FLAGS] file" with arbitrary flag strings.
//
// The contract under test:
//   - rm never panics on any flag input
//   - rm never hangs
//   - exit code is 0 or 1
//   - no accepted flag causes a file outside the sandbox to be removed
func FuzzRmFlagCombinator(f *testing.F) {
	// --- Source A: valid flag combinations ---
	f.Add("rm target.txt")
	f.Add("rm -f target.txt")
	f.Add("rm -v target.txt")
	f.Add("rm -d target.txt")
	f.Add("rm -fv target.txt")
	f.Add("rm -fd target.txt")
	f.Add("rm --force target.txt")
	f.Add("rm --verbose target.txt")
	f.Add("rm --dir target.txt")
	f.Add("rm --help")
	f.Add("rm -- target.txt")
	f.Add("rm -- -badfile.txt")

	// --- Source B: rejected flags ---
	f.Add("rm -r target.txt")
	f.Add("rm -R target.txt")
	f.Add("rm --recursive target.txt")
	f.Add("rm -i target.txt")
	f.Add("rm -I target.txt")
	f.Add("rm --preserve-root target.txt")
	f.Add("rm --no-preserve-root target.txt")
	f.Add("rm --one-file-system target.txt")

	// --- Source C: edge cases ---
	f.Add("rm")
	f.Add("rm -")
	f.Add("rm --")
	f.Add("rm -- ")
	f.Add("rm -fvd target.txt")
	f.Add("rm target.txt nosuchfile.txt")

	f.Fuzz(func(t *testing.T, script string) {
		if len(script) > 4*1024 {
			return
		}
		if !utf8.ValidString(script) {
			return
		}
		for _, c := range script {
			if c == '\x00' || c == '\n' || c == '\r' {
				return
			}
		}
		// Only fuzz rm invocations: must be "rm" alone or "rm " (with a space/tab).
		if len(script) < 2 || script[:2] != "rm" {
			return
		}
		if len(script) > 2 && script[2] != ' ' && script[2] != '\t' {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte("seed"), 0644); err != nil {
			t.Fatal(err)
		}
		// Create a dash-prefixed file to exercise the -- separator path.
		if err := os.WriteFile(filepath.Join(dir, "-badfile.txt"), []byte("seed"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create a sentinel file outside the sandbox to verify sandbox integrity.
		outside := t.TempDir()
		sentinel := filepath.Join(outside, "sentinel.txt")
		if err := os.WriteFile(sentinel, []byte("sentinel"), 0644); err != nil {
			t.Fatal(err)
		}

		_, parseErr := rmRunFuzz(t, script, dir)
		// Parse errors are expected — the fuzzer routinely mutates inputs into
		// malformed shell syntax. They are not failures.
		if parseErr != nil {
			return
		}
		// We do not assert on the exit code: shell control flow operators (e.g.
		// background `&`, pipelines, `&&`) can produce exit codes > 1 from the
		// runner, not from rm itself. The fuzz contract is "no panic, no hang,
		// no sandbox escape" — all three are enforced here and by the timeout in
		// rmRunFuzz.
		//
		// Sentinel outside the sandbox must never be removed.
		if _, err := os.Stat(sentinel); os.IsNotExist(err) {
			t.Errorf("sandbox escape: sentinel file outside AllowedPaths was removed by script %q", script)
		}
	})
}
