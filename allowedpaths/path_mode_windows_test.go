// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package allowedpaths

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolveAllowedPathModeStripsWindowsSuffix(t *testing.T) {
	base := filepath.Join(t.TempDir(), "policy")

	path, mode := resolveAllowedPathMode(base + ":rw")
	assert.Equal(t, base, path)
	assert.Equal(t, pathModeReadWrite, mode)

	path, mode = resolveAllowedPathMode(base + ":ro")
	assert.Equal(t, base, path)
	assert.Equal(t, pathModeReadOnly, mode)
}

func TestRelWithinWindowsIsCaseInsensitive(t *testing.T) {
	rel, ok := relWithin(`C:\Allowed\Root`, `c:\allowed\root\Child\File.txt`)
	assert.True(t, ok)
	assert.Equal(t, `Child\File.txt`, rel)

	rel, ok = relWithin(`C:\Allowed\Root`, `C:\Allowed\Root`)
	assert.True(t, ok)
	assert.Equal(t, `.`, rel)

	_, ok = relWithin(`C:\Allowed\Root`, `C:\Allowed\Rooted\File.txt`)
	assert.False(t, ok)
}

func TestWindowsAlternateDataStreamDetection(t *testing.T) {
	assert.False(t, hasWindowsAlternateDataStream(`C:\tmp\file.txt`))
	assert.False(t, hasWindowsAlternateDataStream(`\\server\share\file.txt`))
	assert.False(t, hasWindowsAlternateDataStream(`\\?\C:\tmp\file.txt`))

	assert.True(t, hasWindowsAlternateDataStream(`C:\tmp\file.txt:stream`))
	assert.True(t, hasWindowsAlternateDataStream(`\\?\C:\tmp\file.txt:stream`))
	assert.True(t, hasWindowsAlternateDataStream(`relative:name`))
	assert.True(t, hasWindowsAlternateDataStream(`C:relative`))
}

func TestNormalizeWindowsAbsoluteReparseTarget(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{name: "nt drive", in: `\??\C:\allowed\target`, want: `C:\allowed\target`, ok: true},
		{name: "nt unc", in: `\??\UNC\server\share\target`, want: `\\server\share\target`, ok: true},
		{name: "win32 drive", in: `\\?\C:\allowed\target`, want: `C:\allowed\target`, ok: true},
		{name: "volume guid", in: `\??\Volume{11111111-2222-3333-4444-555555555555}\target`, want: `\\?\Volume{11111111-2222-3333-4444-555555555555}\target`, ok: true},
		{name: "relative", in: `target\child`, ok: false},
		{name: "drive relative", in: `C:target\child`, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := normalizeWindowsAbsoluteReparseTarget(tt.in)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
