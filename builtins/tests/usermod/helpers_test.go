// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package usermod_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/internal/interpoption"
	"github.com/DataDog/rshell/interp"
)

// runScript parses and runs the given shell script through interp.Runner
// with AllowAllCommands set, plus any additional options. It returns
// stdout, stderr, and the resolved exit code.
func runScript(t *testing.T, script string, opts ...interp.RunnerOption) (string, string, int) {
	t.Helper()
	parser := syntax.NewParser()
	prog, err := parser.Parse(strings.NewReader(script), "")
	if err != nil {
		t.Fatal(err)
	}
	var outBuf, errBuf bytes.Buffer
	allOpts := append([]interp.RunnerOption{
		interp.StdIO(nil, &outBuf, &errBuf),
		interpoption.AllowAllCommands().(interp.RunnerOption),
	}, opts...)
	runner, err := interp.New(allOpts...)
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
			t.Fatalf("unexpected error: %v", runErr)
		}
	}
	return outBuf.String(), errBuf.String(), exitCode
}

// usermodRun runs script with remediation mode enabled and no AllowedPaths
// configured at all. usermod never consults the AllowedPaths sandbox — see
// the package doc comment in builtins/usermod/usermod.go for why — so,
// unlike setfacl's setfaclRun helper, no writable root needs to be granted
// for the happy path.
func usermodRun(t *testing.T, script string) (string, string, int) {
	t.Helper()
	return runScript(t, script, interp.WithMode(interp.ModeRemediation))
}

// usermodRunReadOnly runs script in the default (read-only) mode, without
// enabling remediation mode.
func usermodRunReadOnly(t *testing.T, script string) (string, string, int) {
	t.Helper()
	return runScript(t, script)
}
