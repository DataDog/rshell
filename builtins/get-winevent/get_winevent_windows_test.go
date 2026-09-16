// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package get_winevent

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/builtins"
)

func TestPathValidationUsesShellWorkDir(t *testing.T) {
	workDir := t.TempDir()
	var opened string
	var stderr bytes.Buffer
	callCtx := &builtins.CallContext{
		Stderr:  &stderr,
		WorkDir: func() string { return workDir },
		OpenFile: func(_ context.Context, path string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			opened = path
			return nil, errors.New("denied by test sandbox")
		},
		PortableErr: func(err error) string { return err.Error() },
	}

	result := runQueryFile(context.Background(), callCtx, options{path: `logs\copy.evtx`})
	if result.Code != 1 {
		t.Errorf("runQueryFile code = %d, want 1", result.Code)
	}
	if want := filepath.Join(workDir, `logs\copy.evtx`); opened != want {
		t.Errorf("sandbox OpenFile path = %q, want %q", opened, want)
	}
	if !strings.Contains(stderr.String(), "denied by test sandbox") {
		t.Errorf("stderr = %q, want sandbox denial", stderr.String())
	}
}
