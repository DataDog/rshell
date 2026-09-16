// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package wineventlog

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// copyAppLog copies the host's Application.evtx into a temp dir so tests can
// run a real file query against a disposable target.
func copyAppLog(t *testing.T) string {
	t.Helper()
	const src = `C:\Windows\System32\winevt\Logs\Application.evtx`
	content, err := os.ReadFile(src)
	if err != nil {
		t.Skipf("cannot read %s: %v", src, err)
	}
	target := filepath.Join(t.TempDir(), "copy.evtx")
	if err := os.WriteFile(target, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return target
}

func fileQuery(target string) Query {
	return Query{
		Target:    target,
		IsFile:    true,
		MaxEvents: 100,
		BatchSize: 16,
		MaxBytes:  64 * 1024,
		NoMessage: true, // irrelevant here, and much faster
	}
}

// TestVerifyAfterOpenAbortsBeforeAnyEvent is the core guarantee of the --Path
// anti-TOCTOU check: when the post-open verification fails, the query is
// abandoned with the verifier's error and *zero* events reach the callback.
//
// "Zero" is the whole point. A check that only reported tampering after
// events had streamed to stdout would be detection-after-disclosure; because
// EvtQuery opens the target but does not read it, refusing here prevents the
// read entirely.
func TestVerifyAfterOpenAbortsBeforeAnyEvent(t *testing.T) {
	target := copyAppLog(t)
	sentinel := errors.New("identity changed between validation and open")

	q := fileQuery(target)
	q.VerifyAfterOpen = func() error { return sentinel }

	var emitted int
	err := Run(context.Background(), q, func(Event) error {
		emitted++
		return nil
	}, nil)

	if !errors.Is(err, sentinel) {
		t.Errorf("Run error = %v, want %v", err, sentinel)
	}
	if emitted != 0 {
		t.Errorf("%d events reached the callback; a failed post-open check must emit none", emitted)
	}
}

// TestVerifyAfterOpenPassThrough is the control: the same query with a
// verifier that approves the target must behave exactly like no verifier at
// all. Without this, the test above would also pass if VerifyAfterOpen
// unconditionally aborted every query.
func TestVerifyAfterOpenPassThrough(t *testing.T) {
	target := copyAppLog(t)

	count := func(q Query) int {
		t.Helper()
		var n int
		if err := Run(context.Background(), q, func(Event) error {
			n++
			return nil
		}, nil); err != nil {
			t.Fatalf("Run: %v", err)
		}
		return n
	}

	baseline := count(fileQuery(target))
	if baseline == 0 {
		t.Skip("copied Application.evtx yielded no events")
	}

	var called int
	q := fileQuery(target)
	q.VerifyAfterOpen = func() error { called++; return nil }
	if got := count(q); got != baseline {
		t.Errorf("with an approving verifier got %d events, want %d", got, baseline)
	}
	if called != 1 {
		t.Errorf("VerifyAfterOpen called %d times, want exactly 1 (once per query, after open)", called)
	}
}
