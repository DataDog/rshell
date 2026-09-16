// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package get_winevent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/wineventlog"
)

func mustColumns(t *testing.T, spec string) []wineventlog.Column {
	t.Helper()
	cols, err := wineventlog.ParseColumns(spec)
	if err != nil {
		t.Fatal(err)
	}
	return cols
}

func TestSerializeEventTSVEscapesRepeatedColumns(t *testing.T) {
	opts := options{
		output:  "tsv",
		columns: mustColumns(t, "Message,Message,ProviderName"),
	}
	row, err := serializeEvent(wineventlog.Event{
		Message:      "a\tb\nc",
		ProviderName: "provider",
	}, opts)
	if err != nil {
		t.Fatalf("serializeEvent: %v", err)
	}
	if got, want := string(row), "a\\tb\\nc\ta\\tb\\nc\tprovider\n"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

func TestSerializeEventJSONLIsOneCompleteLine(t *testing.T) {
	opts := options{output: "jsonl"}
	row, err := serializeEvent(wineventlog.Event{
		Raw:     `<Event><System><EventID>42</EventID></System></Event>`,
		Message: "a\tb\nc",
	}, opts)
	if err != nil {
		t.Fatalf("serializeEvent: %v", err)
	}
	if !strings.HasSuffix(string(row), "\n") || strings.Count(string(row), "\n") != 1 {
		t.Errorf("JSON Lines row must have exactly one trailing newline: %q", row)
	}
	var decoded map[string]any
	if err := json.Unmarshal(row, &decoded); err != nil {
		t.Fatalf("JSON Lines row is not valid JSON: %v", err)
	}
	if got := decoded["Message"]; got != "a\tb\nc" {
		t.Errorf("Message = %#v, want escaped message value", got)
	}
}

func TestSerializeEventRejectsOversizedFinalRecords(t *testing.T) {
	large := strings.Repeat("x", MaxRenderBytes)
	tests := []struct {
		name string
		ev   wineventlog.Event
		opts options
	}{
		{
			name: "tsv",
			ev:   wineventlog.Event{Message: large},
			opts: options{output: "tsv", columns: mustColumns(t, "Message")},
		},
		{
			name: "jsonl",
			ev: wineventlog.Event{
				Raw:     `<Event><System><EventID>42</EventID></System></Event>`,
				Message: large,
			},
			opts: options{output: "jsonl"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := serializeEvent(tc.ev, tc.opts)
			if err == nil {
				t.Fatalf("serializeEvent returned row of %d bytes, want size error", len(row))
			}
			if row != nil {
				t.Errorf("serializeEvent returned %d bytes with an error; callers must have no partial record to write", len(row))
			}
		})
	}
}

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
