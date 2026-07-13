// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package builtins

import (
	"bytes"
	"context"
	"testing"
)

func TestCommandRegisterEnforcesRemediationOnly(t *testing.T) {
	const name = "test-remediation-only"
	defer delete(registry, name)
	defer delete(metaRegistry, name)

	called := false
	Command{
		Name:            name,
		Description:     "test remediation enforcement",
		RemediationOnly: true,
		MakeFlags: NoFlags(func(context.Context, *CallContext, []string) Result {
			called = true
			return Result{}
		}),
	}.Register()

	handler, ok := Lookup(name)
	if !ok {
		t.Fatal("registered command was not found")
	}

	var stderr bytes.Buffer
	result := handler(context.Background(), &CallContext{Stderr: &stderr}, nil)
	if result.Code != 1 {
		t.Fatalf("read-only result code = %d, want 1", result.Code)
	}
	if called {
		t.Fatal("remediation-only handler ran in read-only mode")
	}
	if got, want := stderr.String(), name+": command requires remediation mode\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	result = handler(context.Background(), &CallContext{Stderr: &stderr, RemediationMode: true}, nil)
	if result.Code != 0 {
		t.Fatalf("remediation result code = %d, want 0", result.Code)
	}
	if !called {
		t.Fatal("remediation-only handler did not run in remediation mode")
	}
}
