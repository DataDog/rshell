// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tee

import (
	"bytes"
	"errors"
	"testing"

	"github.com/DataDog/rshell/builtins"
)

// fakeCloser is an io.Closer whose Close call is scripted for the test,
// modeling a delayed write-back failure surfaced only at Close time (e.g.
// ENOSPC, EIO, or a quota error on some filesystems, including several
// network filesystems) — a real os.File cannot be made to fail Close on
// demand portably, so this is a Go-level unit test of closeAllDests rather
// than an end-to-end scenario.
type fakeCloser struct {
	err error
}

func (f *fakeCloser) Close() error { return f.err }

func TestCloseAllDestsReportsFailure(t *testing.T) {
	var errBuf bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr:      &errBuf,
		PortableErr: func(err error) string { return err.Error() },
	}
	closeErr := errors.New("write-back failed: no space left on device")
	dests := []dest{
		{name: "standard output", w: nil, closer: nil}, // never closed
		{name: "good.txt", w: nil, closer: &fakeCloser{err: nil}},
		{name: "bad.txt", w: nil, closer: &fakeCloser{err: closeErr}},
	}

	ok := closeAllDests(callCtx, dests)

	if ok {
		t.Fatal("expected closeAllDests to report failure")
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("bad.txt")) {
		t.Errorf("stderr missing failing destination name: %q", errBuf.String())
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("no space left on device")) {
		t.Errorf("stderr missing close error text: %q", errBuf.String())
	}
	if bytes.Contains(errBuf.Bytes(), []byte("good.txt")) {
		t.Errorf("stderr must not report the successfully closed destination: %q", errBuf.String())
	}
}

func TestCloseAllDestsAllSucceed(t *testing.T) {
	var errBuf bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr:      &errBuf,
		PortableErr: func(err error) string { return err.Error() },
	}
	dests := []dest{
		{name: "standard output", w: nil, closer: nil},
		{name: "a.txt", w: nil, closer: &fakeCloser{err: nil}},
		{name: "b.txt", w: nil, closer: &fakeCloser{err: nil}},
	}

	ok := closeAllDests(callCtx, dests)

	if !ok {
		t.Fatal("expected closeAllDests to report success")
	}
	if errBuf.Len() != 0 {
		t.Errorf("expected no stderr output, got %q", errBuf.String())
	}
}

// TestCloseAllDestsClosesEveryDestinationDespiteEarlierFailure verifies that
// one destination's close failure does not skip closing the rest — every
// closer must still be invoked exactly once.
func TestCloseAllDestsClosesEveryDestinationDespiteEarlierFailure(t *testing.T) {
	var errBuf bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr:      &errBuf,
		PortableErr: func(err error) string { return err.Error() },
	}
	closed := make([]bool, 3)
	makeCloser := func(i int, err error) *trackedCloser {
		return &trackedCloser{err: err, onClose: func() { closed[i] = true }}
	}
	dests := []dest{
		{name: "a.txt", w: nil, closer: makeCloser(0, errors.New("boom"))},
		{name: "b.txt", w: nil, closer: makeCloser(1, nil)},
		{name: "c.txt", w: nil, closer: makeCloser(2, errors.New("boom2"))},
	}

	closeAllDests(callCtx, dests)

	for i, c := range closed {
		if !c {
			t.Errorf("destination %d was not closed", i)
		}
	}
}

type trackedCloser struct {
	err     error
	onClose func()
}

func (t *trackedCloser) Close() error {
	t.onClose()
	return t.err
}
