// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package ls_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/interp"
	"github.com/DataDog/rshell/interp/builtins/testutil"
)

func cmdRunCtx(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// FuzzLsFlags fuzzes ls with various flag combinations on directories with random filenames.
func FuzzLsFlags(f *testing.F) {
	f.Add("file1.txt", true, false, false, false, false)
	f.Add(".hidden", false, true, false, false, false)
	f.Add("file.txt", false, false, true, false, false)
	f.Add("file.txt", false, false, false, true, false)
	f.Add("file.txt", false, false, false, false, true)

	f.Fuzz(func(t *testing.T, filename string, flagL, flagA, flagR, flagS, flagF bool) {
		if len(filename) == 0 || len(filename) > 100 {
			return
		}
		if !utf8.ValidString(filename) {
			return
		}
		// Skip filenames with characters problematic for shell or filesystem.
		for _, c := range filename {
			if c == '\'' || c == '\x00' || c == '\n' || c == '/' || c == '\\' || c == '"' || c == '`' || c == '$' {
				return
			}
		}
		// Skip filenames starting with - (would be treated as flags).
		if filename[0] == '-' {
			return
		}

		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, filename), []byte("content"), 0644); err != nil {
			// Some filenames may be invalid on the OS.
			return
		}

		flags := ""
		if flagL {
			flags += " -l"
		}
		if flagA {
			flags += " -a"
		}
		if flagR {
			flags += " -r"
		}
		if flagS {
			flags += " -S"
		}
		if flagF {
			flags += " -F"
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "ls"+flags, dir)
		if code != 0 && code != 1 {
			t.Errorf("ls%s unexpected exit code %d", flags, code)
		}
	})
}

// FuzzLsRecursive fuzzes ls -R on nested directories.
func FuzzLsRecursive(f *testing.F) {
	f.Add(int64(1))
	f.Add(int64(3))
	f.Add(int64(5))

	f.Fuzz(func(t *testing.T, depth int64) {
		if depth < 0 || depth > 10 {
			return
		}

		dir := t.TempDir()
		current := dir
		for i := int64(0); i < depth; i++ {
			subdir := filepath.Join(current, "sub")
			if err := os.Mkdir(subdir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(current, "file.txt"), []byte("x"), 0644); err != nil {
				t.Fatal(err)
			}
			current = subdir
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, _, code := cmdRunCtx(ctx, t, "ls -R", dir)
		if code != 0 && code != 1 {
			t.Errorf("ls -R unexpected exit code %d", code)
		}
	})
}
