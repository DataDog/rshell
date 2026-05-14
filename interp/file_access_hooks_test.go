// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type capturedFileAccessEvent struct {
	phase string
	event FileAccessEvent
}

func runWithFileAccessHooks(t *testing.T, dir string, script string, hooks FileAccessHooks) error {
	t.Helper()
	prog, err := ParseScript(script, "")
	require.NoError(t, err)
	r, err := New(
		StdIO(nil, io.Discard, io.Discard),
		AllowedPaths([]string{dir}),
		allowAllCommandsOpt(),
		WithFileAccessHooks(hooks),
	)
	require.NoError(t, err)
	t.Cleanup(func() { r.Close() })
	return r.Run(context.Background(), prog)
}

func TestFileAccessHooksOpenFileBeforeAfter(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	require.NoError(t, os.WriteFile(input, []byte("hello\n"), 0o644))

	var events []capturedFileAccessEvent
	hooks := FileAccessHooks{
		Before: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "before", event: event})
		},
		After: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "after", event: event})
		},
	}

	require.NoError(t, runWithFileAccessHooks(t, dir, "cat input.txt", hooks))
	require.Len(t, events, 2)

	before := events[0].event
	after := events[1].event
	assert.Equal(t, "before", events[0].phase)
	assert.Equal(t, "after", events[1].phase)
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, "cat", before.Command)
	assert.Equal(t, FileAccessSourceBuiltin, before.Source)
	assert.Equal(t, FileAccessOpOpen, before.Op)
	assert.Equal(t, "input.txt", before.RequestedPath)
	assert.Equal(t, input, before.AbsPath)
	assert.Equal(t, input, before.ResolvedPath)
	assert.Equal(t, dir, before.CWD)
	require.NotNil(t, before.PreMetadata)
	assert.True(t, before.PreMetadata.IsRegular)
	assert.Equal(t, int64(len("hello\n")), before.PreMetadata.Size)

	assert.Equal(t, FileAccessResultSuccess, after.Result)
	assert.Empty(t, after.Err)
	require.NotNil(t, after.PostMetadata)
	assert.True(t, after.PostMetadata.IsRegular)
	assert.Equal(t, int64(len("hello\n")), after.PostMetadata.Size)
}

func TestFileAccessHooksAttributeGlobToCommand(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "one.txt"), []byte("one\n"), 0o644))

	var events []capturedFileAccessEvent
	hooks := FileAccessHooks{
		Before: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "before", event: event})
		},
		After: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "after", event: event})
		},
	}

	require.NoError(t, runWithFileAccessHooks(t, dir, "echo *.txt", hooks))
	require.Len(t, events, 2)

	before := events[0].event
	after := events[1].event
	assert.Equal(t, FileAccessSourceGlob, before.Source)
	assert.Equal(t, FileAccessOpReadDir, before.Op)
	assert.Equal(t, "echo", before.Command)
	assert.Equal(t, dir, before.RequestedPath)
	assert.Equal(t, dir, before.AbsPath)
	require.NotNil(t, before.PreMetadata)
	assert.True(t, before.PreMetadata.IsDir)
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, FileAccessResultSuccess, after.Result)
	require.NotNil(t, after.PostMetadata)
	assert.True(t, after.PostMetadata.IsDir)
}

func TestFileAccessHooksAttributeCommandSubstitutionShortcut(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input.txt")
	require.NoError(t, os.WriteFile(input, []byte("needle\n"), 0o644))

	var events []capturedFileAccessEvent
	hooks := FileAccessHooks{
		Before: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "before", event: event})
		},
		After: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "after", event: event})
		},
	}

	require.NoError(t, runWithFileAccessHooks(t, dir, "echo $(<input.txt)", hooks))
	require.Len(t, events, 2)

	before := events[0].event
	after := events[1].event
	assert.Equal(t, FileAccessSourceCommandSubstitute, before.Source)
	assert.Equal(t, FileAccessOpOpen, before.Op)
	assert.Equal(t, "echo", before.Command)
	assert.Equal(t, "input.txt", before.RequestedPath)
	assert.Equal(t, input, before.AbsPath)
	require.NotNil(t, before.PreMetadata)
	assert.True(t, before.PreMetadata.IsRegular)
	assert.Equal(t, before.ID, after.ID)
	assert.Equal(t, FileAccessResultSuccess, after.Result)
	require.NotNil(t, after.PostMetadata)
	assert.True(t, after.PostMetadata.IsRegular)
}

func TestFileAccessHooksFireAfterOnOpenError(t *testing.T) {
	dir := t.TempDir()

	var events []capturedFileAccessEvent
	hooks := FileAccessHooks{
		Before: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "before", event: event})
		},
		After: func(_ context.Context, event FileAccessEvent) {
			events = append(events, capturedFileAccessEvent{phase: "after", event: event})
		},
	}

	err := runWithFileAccessHooks(t, dir, "cat missing.txt", hooks)
	require.Error(t, err)
	require.Len(t, events, 2)

	before := events[0].event
	after := events[1].event
	assert.Equal(t, before.ID, after.ID)
	assert.Nil(t, before.PreMetadata)
	assert.NotEmpty(t, before.PreMetadataErr)
	assert.Equal(t, FileAccessResultError, after.Result)
	assert.Contains(t, after.Err, "no such file or directory")
	assert.Nil(t, after.PostMetadata)
}

func TestFileAccessHookPanicDoesNotAffectRun(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "input.txt"), []byte("ok\n"), 0o644))

	hooks := FileAccessHooks{
		Before: func(context.Context, FileAccessEvent) {
			panic("before panic")
		},
		After: func(context.Context, FileAccessEvent) {
			panic("after panic")
		},
	}

	require.NoError(t, runWithFileAccessHooks(t, dir, "cat input.txt", hooks))
}
