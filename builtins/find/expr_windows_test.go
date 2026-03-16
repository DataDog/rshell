// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package find

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParsePathPredicateNormalizesBackslashesWindows verifies that on Windows,
// parsePathPredicate converts backslash path separators to forward slashes.
// This test only runs on Windows (go:build windows) because filepath.ToSlash
// is a no-op on Unix where '\' is a valid filename character.
func TestParsePathPredicateNormalizesBackslashesWindows(t *testing.T) {
	tests := []struct {
		name  string
		args  []string
		kind  exprKind
		want  string
	}{
		{"path backslash", []string{"-path", `dir\sub\*.go`}, exprPath, "dir/sub/*.go"},
		{"ipath backslash", []string{"-ipath", `Dir\Sub\*.Go`}, exprIPath, "Dir/Sub/*.Go"},
		{"newer backslash", []string{"-newer", `dir\ref.txt`}, exprNewer, "dir/ref.txt"},
		{"wholename backslash", []string{"-wholename", `src\main.go`}, exprPath, "src/main.go"},
		{"iwholename backslash", []string{"-iwholename", `Src\Main.go`}, exprIPath, "Src/Main.go"},
		{"mixed separators", []string{"-path", `dir/sub\file.go`}, exprPath, "dir/sub/file.go"},
		{"multiple backslashes", []string{"-path", `a\b\c\d`}, exprPath, "a/b/c/d"},
		{"forward slashes unchanged", []string{"-path", "dir/file"}, exprPath, "dir/file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := parseExpression(tt.args)
			require.NoError(t, err)
			require.NotNil(t, pr.expr)
			assert.Equal(t, tt.kind, pr.expr.kind)
			assert.Equal(t, tt.want, pr.expr.strVal)
		})
	}
}

// TestRunNormalizesStartPathBackslashesWindows verifies that start paths
// passed to find on Windows have backslashes converted to forward slashes.
// This ensures baseName and joinPath (which only handle '/') work correctly.
func TestRunNormalizesStartPathBackslashesWindows(t *testing.T) {
	// Verify via the parser that -name/-iname do NOT get normalized
	// (they match basenames which never contain path separators).
	pr, err := parseExpression([]string{"-name", `file\name`})
	require.NoError(t, err)
	assert.Equal(t, `file\name`, pr.expr.strVal, "-name should NOT normalize backslashes")
}
