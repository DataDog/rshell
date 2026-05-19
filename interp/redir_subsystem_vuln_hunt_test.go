// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tests added by vuln-hunt campaign 2026-05-18-initial-audit /
// interp-redirect-handling to pin the runner's redirect-implementation
// invariants at the subsystem level (the implementation behind the
// already-audited `redirections`, `allowed_redirects`, `blocked_redirects`
// shell-feature scenarios).

package interp

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingOpenHandler is a test double that records every call made to
// openHandler so the test can assert which redirect target paths the
// interpreter routed through the sandbox.
type recordingOpenHandler struct {
	calls []openCall
	root  string
}

type openCall struct {
	path string
	flag int
}

func (h *recordingOpenHandler) handle(_ context.Context, path string, flag int, _ os.FileMode) (io.ReadWriteCloser, error) {
	h.calls = append(h.calls, openCall{path: path, flag: flag})
	// Open the underlying file relative to the recorded root, so cat reads
	// real contents and the test exercises the post-open code path too.
	full := path
	if !filepath.IsAbs(path) {
		full = filepath.Join(h.root, path)
	}
	return os.OpenFile(full, flag, 0)
}

// TestVulnHuntSubsystemInterpRedirectHandling_InputRedirectGoesThroughOpenHandler
// asserts that an input redirect target (`cat <file`) reaches the runner's
// openHandler with the expected path and a read-only flag. If a future
// regression in runner_redir.go bypassed openHandler with a direct os.Open,
// the recorded calls slice would be empty and this test would fail — the
// AllowedPaths sandbox is built on top of openHandler, so any bypass would
// also bypass AllowedPaths.
func TestVulnHuntSubsystemInterpRedirectHandling_InputRedirectGoesThroughOpenHandler(t *testing.T) {
	dir := t.TempDir()
	const fileName = "input.txt"
	const fileBody = "redirect-payload\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, fileName), []byte(fileBody), 0o600))

	var stdout, stderr bytes.Buffer
	r, err := New(
		allowAllCommandsOpt(),
		StdIO(nil, &stdout, &stderr),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	r.Reset()

	rec := &recordingOpenHandler{root: dir}
	r.openHandler = rec.handle
	r.Dir = dir

	err = r.Run(context.Background(), parseScript(t, "cat <"+fileName))
	require.NoError(t, err, "stderr=%q", stderr.String())

	require.Len(t, rec.calls, 1, "expected exactly one open call routed through openHandler")
	assert.Equal(t, fileName, rec.calls[0].path,
		"openHandler must receive the redirect target path verbatim, got: %v", rec.calls)
	assert.Equal(t, os.O_RDONLY, rec.calls[0].flag,
		"input redirect must request read-only access; a writable flag would mean the redirect path widens the sandbox")
	assert.Equal(t, fileBody, stdout.String(),
		"cat output should match file body — proves the recorded handler also returned a working file")
}
