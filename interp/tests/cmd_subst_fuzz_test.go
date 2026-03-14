// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/interp"
)

func fuzzSubstRun(ctx context.Context, t *testing.T, script string) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		return "", "", -1 // parse error, skip
	}
	var outBuf, errBuf bytes.Buffer
	runner, err := interp.New(interp.StdIO(nil, &outBuf, &errBuf))
	if err != nil {
		t.Fatalf("unexpected runner error: %v", err)
	}
	defer runner.Close()
	err = runner.Run(ctx, prog)
	exitCode := 0
	if err != nil {
		var es interp.ExitStatus
		if errors.As(err, &es) {
			exitCode = int(es)
		} else if ctx.Err() != nil {
			return outBuf.String(), errBuf.String(), -2 // timeout
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// FuzzCmdSubstContent fuzzes the content inside command substitution.
func FuzzCmdSubstContent(f *testing.F) {
	// Seed corpus from existing test cases
	f.Add("hello")
	f.Add("hello world")
	f.Add("")
	f.Add("line1\nline2\nline3")
	f.Add("a b c")
	f.Add("hello\n\n\n")
	f.Add("special chars: $VAR 'quotes' \"double\"")
	f.Add(strings.Repeat("x", 1024))
	f.Add(strings.Repeat("long line ", 100))
	f.Add("null\x00byte")
	f.Add("tab\there")
	f.Add("cr\rhere")
	f.Add("crlf\r\nhere")

	f.Fuzz(func(t *testing.T, content string) {
		if len(content) > 1<<16 { // cap at 64KB
			return
		}
		// Avoid shell metacharacters that would cause parse errors
		if strings.ContainsAny(content, "`$(){}[]|;&<>\\'\"\n\r\t") {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		script := `echo "$(echo ` + content + `)"` //nolint: gocritic
		_, _, code := fuzzSubstRun(ctx, t, script)
		if code != -1 && code != -2 && code != 0 && code != 1 && code != 2 {
			t.Errorf("unexpected exit code %d for content %q", code, content)
		}
	})
}

// FuzzSubshellScript fuzzes scripts inside subshells.
func FuzzSubshellScript(f *testing.F) {
	f.Add("echo hello")
	f.Add("true")
	f.Add("false")
	f.Add("exit 0")
	f.Add("exit 1")
	f.Add("echo a; echo b")
	f.Add("echo hello | cat")

	f.Fuzz(func(t *testing.T, script string) {
		if len(script) > 1<<16 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		fullScript := "(" + script + ")"
		_, _, code := fuzzSubstRun(ctx, t, fullScript)
		if code != -1 && code != -2 && code != 0 && code != 1 && code != 2 {
			t.Errorf("unexpected exit code %d", code)
		}
	})
}
