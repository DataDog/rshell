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
	"io/fs"
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

func TestPathIdentityMismatchAbortsBeforeWritingEvents(t *testing.T) {
	contents, err := os.ReadFile(`C:\Windows\System32\winevt\Logs\Application.evtx`)
	if err != nil {
		t.Skipf("cannot read Application.evtx: %v", err)
	}
	path := filepath.Join(t.TempDir(), "copy.evtx")
	if err := os.WriteFile(path, contents, 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	identityCalls := 0
	callCtx := &builtins.CallContext{
		Stdout:  &stdout,
		Stderr:  &stderr,
		WorkDir: func() string { return filepath.Dir(path) },
		OpenFile: func(_ context.Context, name string, _ int, _ os.FileMode) (io.ReadWriteCloser, error) {
			return os.Open(name)
		},
		StatFile: func(_ context.Context, name string) (fs.FileInfo, error) {
			return os.Stat(name)
		},
		FileIdentity: func(_ string, _ fs.FileInfo) (builtins.FileID, bool) {
			identityCalls++
			// Model the path having been replaced after EvtQuery opened it.
			return builtins.FileID{Ino: uint64(identityCalls)}, true
		},
		PortableErr: func(err error) string { return err.Error() },
	}

	result := runQueryFile(context.Background(), callCtx, options{
		path:      path,
		output:    "tsv",
		maxEvents: 1,
		columns:   nil,
	})
	if result.Code != 1 {
		t.Errorf("runQueryFile code = %d, want 1", result.Code)
	}
	if identityCalls != 2 {
		t.Errorf("FileIdentity called %d times, want validation plus post-open verification", identityCalls)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want no event rows after identity mismatch", stdout.String())
	}
	if !strings.Contains(stderr.String(), "changed identity") {
		t.Errorf("stderr = %q, want identity mismatch", stderr.String())
	}
}
