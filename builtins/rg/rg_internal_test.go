// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rg

import (
	"context"
	"io"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestRawDisplayJoinUsesBackslashOnlyForBackslashSpelledOperandsOnWindows
// is a regression test: rawDisplayJoin must join a NEWLY inserted
// separator with '\' on Windows ONLY when dir was itself spelled with
// at least one '\' somewhere (e.g. "C:\root"), not merely because the
// process happens to be running on Windows \u2014 every existing test in
// this package asserts a bare '/' for an operand spelled with ordinary
// forward slashes (e.g. "sub", ".", "foo/bar"), on every platform
// INCLUDING Windows, and that must keep working unchanged. Before this
// fix, a directory operand spelled with native Windows separators (e.g.
// "C:\root" or "C:\root\") would produce a malformed, mixed-separator
// display path for every discovered file beneath it (e.g.
// "C:\root/child" or "C:\root\/child") instead of the consistently
// backslash-joined path real ripgrep produces on Windows; an earlier,
// over-broad version of this same fix (defaulting to the platform's
// native separator for EVERY newly-inserted separator, regardless of
// whether dir actually contained a '\') then broke every one of those
// forward-slash-spelled-operand tests on Windows CI, which is exactly
// what this test now pins against regressing again. This test only
// exercises the actual behavior on whichever platform it runs on (this
// repository's CI runs on Windows too); on a non-Windows platform,
// rawDisplayJoin's own runtime.GOOS check always takes the '/'-only
// branch regardless of what separator a test might try to force, so
// there is nothing further to assert there beyond confirming the
// existing (unaffected) forward-slash behavior still works.
func TestRawDisplayJoinUsesBackslashOnlyForBackslashSpelledOperandsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		assert.Equal(t, "root/child", rawDisplayJoin("root", "child"))
		assert.Equal(t, "root/child", rawDisplayJoin("root/", "child"))
		return
	}

	// A dir spelled with ordinary forward slashes (or none at all) must
	// still get a '/'-joined child, exactly as on every other platform
	// -- the mere fact of running on Windows must NOT change this.
	assert.Equal(t, "sub/child", rawDisplayJoin("sub", "child"))
	assert.Equal(t, "foo/bar/child", rawDisplayJoin("foo/bar", "child"))

	// No existing trailing separator, but dir DOES contain a '\'
	// somewhere: a NEW separator must be '\', not a hardcoded '/', so a
	// '\'-spelled operand's children stay consistently '\'-joined.
	assert.Equal(t, `C:\root\child`, rawDisplayJoin(`C:\root`, "child"))

	// An existing trailing '\' is preserved verbatim, matching
	// rawDisplayJoin's own "no further cleaning applied at any level"
	// rule (see its doc comment) -- no second separator is added.
	assert.Equal(t, `C:\root\child`, rawDisplayJoin(`C:\root\`, "child"))

	// An existing trailing '/' (a forward-slash-spelled operand on
	// Windows, which Windows itself also accepts as a path separator) is
	// likewise preserved verbatim, not converted to '\'.
	assert.Equal(t, "C:/root/child", rawDisplayJoin("C:/root/", "child"))

	// A MIXED-separator dir (one earlier level joined with '\', a later
	// one spelled with '/' in the original operand) still picks '\' for
	// a newly-inserted separator, since dir contains at least one '\'
	// somewhere -- this is the recursive-propagation case: once one
	// level's result contains a '\', every deeper level built from it
	// keeps choosing '\' too.
	assert.Equal(t, `C:\root/sub\child`, rawDisplayJoin(`C:\root/sub`, "child"))
}

// blockingArbitraryReader never returns any data (and never errors)
// until closed via its done channel, simulating a regular file backed
// by a stalled network/FUSE filesystem (or a custom embedder's own
// blocking io.ReadCloser substituted for OpenRegularFile) — used by
// TestCancellableReaderForAppliesToAnyReaderNotJustStdin below. Unlike
// an *os.File, it implements only io.Reader, forcing
// cancellableReaderFor onto its goroutine-based fallback path rather
// than a kernel-level SetReadDeadline, which is exactly the scenario
// this test needs to cover: a reader with NO deadline support at all.
type blockingArbitraryReader struct {
	done chan struct{}
}

func (b *blockingArbitraryReader) Read(p []byte) (int, error) {
	<-b.done
	return 0, io.EOF
}

// TestCancellableReaderForAppliesToAnyReaderNotJustStdin is a
// regression test: cancellableReaderFor must interrupt a blocked Read
// against ANY reader, not merely one backed by stdin — verified
// directly here with a plain io.Reader implementing no deadline
// support, standing in for a regular file opened via OpenRegularFile
// against a stalled network/FUSE filesystem (see the per-reader-type
// doc comment in searchFile for the full rationale: Close() alone, as
// a context-close backstop would apply, is not a portable cancellation
// mechanism for a truly blocking kernel read).
func TestCancellableReaderForAppliesToAnyReaderNotJustStdin(t *testing.T) {
	br := &blockingArbitraryReader{done: make(chan struct{})}
	defer close(br.done)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	wrapped, cleanup := cancellableReaderFor(ctx, br)
	defer cleanup()

	start := time.Now()
	buf := make([]byte, 10)
	_, err := wrapped.Read(buf)
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Less(t, elapsed, 2*time.Second, "cancellableReaderFor must interrupt a blocked Read against an arbitrary (non-stdin) reader once ctx expires, not hang indefinitely")
}

// TestCancellableReaderForPrefersKernelDeadlineWhenAvailable is a
// regression test: openReader's own stdin branch must preserve the
// UNDERLYING reader's concrete type (e.g. the real *os.File backing
// stdin when stdin is a pipe) all the way into
// cancellableReaderFor's own readDeadlineSetter type assertion, rather
// than hiding it behind an io.NopCloser wrapper first — verified
// directly: wrapping an *os.File in io.NopCloser produces a value with
// no SetReadDeadline method at all (confirmed via a separate throwaway
// script, not itself committed here), which would otherwise ALWAYS
// force the slower, goroutine-leaking fallback path even when the real
// underlying reader supports a cheap kernel-level deadline directly.
// cancellableReaderFor returns r UNMODIFIED (not a new wrapper value)
// whenever the kernel-deadline path is taken — only installing a
// deadline as a side effect — so asserting the returned reader IS the
// exact same value passed in is a precise, direct way to confirm which
// path was taken. Skipped on Windows: os.Pipe's returned *os.File there
// does not support SetReadDeadline at all (confirmed directly against
// Go's own os package documentation — only POLLABLE files support a
// deadline on Windows, which an os.Pipe handle there is not), so
// cancellableReaderFor's own readDeadlineSetter type assertion
// genuinely fails and the goroutine fallback is correctly taken
// instead — TestCancellableReaderForAppliesToAnyReaderNotJustStdin
// already covers that fallback path's own functional correctness on
// every platform, including this one.
func TestCancellableReaderForPrefersKernelDeadlineWhenAvailable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Pipe's *os.File does not support SetReadDeadline on Windows; see doc comment")
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	wrapped, cleanup := cancellableReaderFor(ctx, r)
	defer cleanup()

	assert.Same(t, io.Reader(r), wrapped, "cancellableReaderFor should return the *os.File unmodified (kernel-deadline path) when passed directly, not wrapped behind an io.NopCloser that would hide SetReadDeadline and force the goroutine fallback instead")
}
