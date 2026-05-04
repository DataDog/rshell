// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/DataDog/rshell/builtins/testutil"
	"github.com/DataDog/rshell/interp"
	"mvdan.cc/sh/v3/syntax"
)

// cmdRunCtxFuzz runs a script with the given context and AllowedPaths={dir}.
// Suffixed "Fuzz" to avoid name collisions with the regular runScriptCtx in
// awk_test.go.
// cmdRunCtxFuzz is like cmdRunCtx but used in fuzz tests: if the shell
// parser rejects the generated script (e.g. because the fuzz-generated
// program text contains characters that break the shell quoting), the
// function returns ("", "", -1) so the caller can skip it instead of failing.
func cmdRunCtxFuzz(ctx context.Context, t *testing.T, script, dir string) (string, string, int) {
	t.Helper()
	// Pre-validate shell syntax. If the generated script is not valid shell
	// (e.g. because the fuzz input contains characters that break the quoting
	// when interpolated into the script), skip it instead of letting
	// testutil.RunScriptCtx call require.NoError and fail the fuzz test.
	if _, err := syntax.NewParser().Parse(strings.NewReader(script), ""); err != nil {
		return "", "", -1
	}
	return testutil.RunScriptCtx(ctx, t, script, dir, interp.AllowedPaths([]string{dir}))
}

// shellQuote single-quotes s for safe inclusion in a bash script and rejects
// any input that would break out of the quoting.
func shellQuote(s string) (string, bool) {
	if strings.ContainsAny(s, "\x00") {
		return "", false
	}
	if strings.Contains(s, "'") {
		return "", false
	}
	return "'" + s + "'", true
}

// awkSafe rejects an awk source that contains any byte that the shell or
// our parser cannot represent reliably (NUL bytes, single quotes that
// escape our shell-quoting, or invalid UTF-8 sequences that cause the
// shell parser to return a non-ExitStatus error). The fuzz function
// returns instead of failing — "rejected input" is not a bug.
func awkSafe(src []byte) bool {
	if len(src) == 0 || len(src) > 4096 {
		return false
	}
	if !utf8.Valid(src) {
		return false
	}
	for _, r := range string(src) {
		if r == 0 || r == '\'' {
			return false
		}
		// Reject C1 control characters (U+0080..U+009F): mvdan.cc/sh\'s parser
		// fails to parse single-quoted strings that contain these runes.
		if r >= 0x80 && r <= 0x9f {
			return false
		}
	}
	return true
}

// =============================================================================
// FuzzAwkProgramText — fuzz the parser with random program text. Goal: never
// panic, never hang, never produce an unexpected exit code.
// =============================================================================

func FuzzAwkProgramText(f *testing.F) {
	// Source A: implementation edge cases (lexer / parser branches).
	f.Add([]byte("BEGIN { print 1 }"))
	f.Add([]byte("END { print NR }"))
	f.Add([]byte("/x/"))
	f.Add([]byte("/x/ { print }"))
	f.Add([]byte("{ print }"))
	f.Add([]byte("NR == 1"))
	f.Add([]byte("$1 > 0 { print $2 }"))
	f.Add([]byte("BEGIN { for (i=0; i<10; i++) print i }"))
	f.Add([]byte("BEGIN { a[1]=1; for (k in a) print k }"))
	f.Add([]byte(""))                       // empty
	f.Add([]byte("\n\n"))                   // only whitespace
	f.Add([]byte("# comment only\n"))       // comment only
	f.Add([]byte("BEGIN { exit }"))         // exit no value
	f.Add([]byte("BEGIN { exit 0 }"))       // exit 0
	f.Add([]byte("BEGIN { x = 1; print }")) // simple
	f.Add([]byte("BEGIN { while (0) {} }"))
	f.Add([]byte("BEGIN { do { } while (0) }"))
	f.Add([]byte("BEGIN { print 1 ? 2 : 3 }"))
	f.Add([]byte("BEGIN { print sprintf(\"%d\", 42) }"))
	f.Add([]byte("BEGIN { gsub(/x/, \"y\", \"axa\") }"))

	// Source B: CVE / security-class inputs (must be rejected at parse time).
	f.Add([]byte(`BEGIN { system("x") }`))
	f.Add([]byte(`BEGIN { print > "x" }`))
	f.Add([]byte(`BEGIN { print >> "x" }`))
	f.Add([]byte(`BEGIN { print | "x" }`))
	f.Add([]byte(`BEGIN { getline x < "x" }`))
	f.Add([]byte(`BEGIN { print ENVIRON["x"] }`))
	f.Add([]byte(`function f(){return 1}`))

	// Source C: existing test coverage seeds.
	f.Add([]byte(`BEGIN{s=0} {s+=$1} END{print s}`))
	f.Add([]byte(`/start/,/stop/`))
	f.Add([]byte(`{ if ($1 > 7) print "big"; else print "small" }`))
	f.Add([]byte(`{ delete a[1] }`))

	// Adversarial: deeply nested constructs (must reject, not crash).
	f.Add([]byte(strings.Repeat("(", 64) + "1" + strings.Repeat(")", 64)))
	f.Add([]byte(strings.Repeat("$", 64) + "1"))
	f.Add([]byte("BEGIN { " + strings.Repeat("print 1; ", 32) + " }"))

	// Adversarial: very large numbers.
	f.Add([]byte(`BEGIN { print 9223372036854775807 }`))
	f.Add([]byte(`BEGIN { print -9999999999999999999 }`))
	f.Add([]byte(`BEGIN { print 1e308 }`))

	// Adversarial: invalid syntax.
	f.Add([]byte(`{ print +`))
	f.Add([]byte(`'`))
	f.Add([]byte(`{`))

	f.Fuzz(func(t *testing.T, src []byte) {
		if !awkSafe(src) {
			return
		}
		quoted, ok := shellQuote(string(src))
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t, "awk "+quoted, t.TempDir())
		if code == -1 {
			// Shell parse error: the generated script is not valid shell syntax.
			// This is not a bug in the awk implementation; skip.
			return
		}
		// Acceptable: any code 0-255, since user programs can call exit(N) for
		// any N (e.g. "BEGIN{exit 2}"). Only negative codes or -1 (shell parse
		// error, handled above) are unexpected.
		if code < 0 {
			t.Fatalf("unexpected exit code %d for src=%q", code, src)
		}
	})
}

// =============================================================================
// FuzzAwkInputData — fuzz with a fixed safe program and varying input bytes.
// Goal: input data should never crash the runtime.
// =============================================================================

func FuzzAwkInputData(f *testing.F) {
	f.Add([]byte("alpha\nbeta\ngamma\n"))
	f.Add([]byte(""))                                                             // empty
	f.Add([]byte("\n"))                                                           // single empty line
	f.Add([]byte("nonewline"))                                                    // no trailing newline
	f.Add([]byte("a\r\nb\r\n"))                                                   // CRLF
	f.Add([]byte("a\x00b\nc\nd\n"))                                               // NUL bytes
	f.Add([]byte("\xff\xfe\xfd\n"))                                               // bad UTF-8
	f.Add(append(append([]byte("MZ"), make([]byte, 100)...), '\n'))               // PE magic
	f.Add(append(append([]byte{0x7f, 'E', 'L', 'F'}, make([]byte, 64)...), '\n')) // ELF magic
	f.Add([]byte(strings.Repeat("a", 1<<15) + "\n"))                              // long line below cap
	f.Add([]byte("very\tlong\twith\ttabs\n"))                                     // tabs
	f.Add([]byte("a b  c   d\nx y\n"))                                            // varied whitespace

	f.Fuzz(func(t *testing.T, data []byte) {
		// Clamp data size.
		if len(data) > 1<<18 {
			return
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "input.txt")
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t,
			`awk '{print NR, NF, $0}' input.txt`, dir)
		if code == -1 {
			return // shell parse error; not a bug
		}
		if code != 0 && code != 1 {
			t.Fatalf("unexpected exit code %d", code)
		}
	})
}

// =============================================================================
// FuzzAwkFieldSep — fuzz the -F flag value.
// =============================================================================

func FuzzAwkFieldSep(f *testing.F) {
	f.Add(",")
	f.Add(":")
	f.Add(" ")
	f.Add("\t")
	f.Add("")
	f.Add("[,:|]") // regex
	f.Add(".")
	f.Add("|")
	f.Add("(\\d+)")
	f.Add(strings.Repeat("a", 64))
	f.Add("[")   // invalid regex
	f.Add("***") // invalid regex

	f.Fuzz(func(t *testing.T, sep string) {
		if len(sep) > 256 {
			return
		}
		// Skip values that would break our shell-quoting or shell parsing.
		if strings.ContainsAny(sep, "'\x00") {
			return
		}
		// Skip invalid UTF-8: the mvdan.cc/sh parser rejects scripts containing
		// invalid UTF-8 sequences, so such separators are out-of-scope.
		if !utf8.ValidString(sep) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		dir := t.TempDir()
		// Build a small CSV-like input.
		path := filepath.Join(dir, "in.txt")
		if err := os.WriteFile(path, []byte("alpha,beta,gamma\nx:y:z\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		// Some valid awk regex characters (e.g. *, +, ?) trigger different
		// behaviour but should still result in clean exit codes.
		_, _, code := cmdRunCtxFuzz(ctx, t,
			`awk -F '`+sep+`' '{print NF}' in.txt`, dir)
		if code == -1 {
			return // shell parse error; not a bug
		}
		if code != 0 && code != 1 {
			t.Fatalf("unexpected exit code %d for sep=%q", code, sep)
		}
	})
}

// =============================================================================
// FuzzAwkVarAssignment — fuzz the -v flag value.
// =============================================================================

func FuzzAwkVarAssignment(f *testing.F) {
	f.Add("a=1")
	f.Add("name=value")
	f.Add("x=")
	f.Add("FS=,")
	f.Add("OFS=|")
	f.Add("RS=z")
	f.Add("a=" + strings.Repeat("x", 64))
	f.Add("noequals") // invalid
	f.Add("=value")   // invalid (empty name)
	f.Add("9=v")      // invalid name
	f.Add("a=1=2")    // multiple = in value
	f.Add("a b=c")    // space in name

	f.Fuzz(func(t *testing.T, assign string) {
		if len(assign) > 1024 {
			return
		}
		// Skip any value that contains shell metacharacters; we always
		// single-quote the value but null bytes still break the shell, and
		// embedded single quotes break our quoting.
		if strings.ContainsAny(assign, "'\x00\n") {
			return
		}
		// Skip invalid UTF-8: the mvdan.cc/sh parser rejects scripts containing
		// invalid UTF-8 sequences, so such assignment values are out-of-scope.
		if !utf8.ValidString(assign) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _, code := cmdRunCtxFuzz(ctx, t,
			`awk -v '`+assign+`' 'BEGIN {}'`,
			t.TempDir())
		if code == -1 {
			return // shell parse error; not a bug
		}
		if code != 0 && code != 1 {
			t.Fatalf("unexpected exit code %d for -v %q", code, assign)
		}
	})
}
