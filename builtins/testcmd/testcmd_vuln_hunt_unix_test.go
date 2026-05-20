// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build unix

package testcmd_test

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntBuiltinSpecialFiles_AccessPredicatesOnFifoDoNotBlock(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, "fifo"), 0644); err != nil {
		t.Skipf("cannot create FIFO: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	stdout, stderr, code := runScriptCtx(ctx, t, `test -r fifo; echo read=$?
test -w fifo; echo write=$?
test -x fifo; echo exec=$?
test -p fifo; echo pipe=$?
`, dir, interp.AllowedPaths([]string{dir}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "read=0\nwrite=0\nexec=1\npipe=0\n", stdout)
	assert.Empty(t, stderr)
}
