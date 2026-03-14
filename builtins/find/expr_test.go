// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseDepthRejectsSignedValues verifies that -maxdepth/-mindepth reject
// +N and -N forms, matching GNU find's "positive decimal integer" requirement.
func TestParseDepthRejectsSignedValues(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
	}{
		{"maxdepth 0", []string{"-maxdepth", "0"}, false},
		{"maxdepth 1", []string{"-maxdepth", "1"}, false},
		{"maxdepth 10", []string{"-maxdepth", "10"}, false},
		{"maxdepth +1 rejected", []string{"-maxdepth", "+1"}, true},
		{"maxdepth -1 rejected", []string{"-maxdepth", "-1"}, true},
		{"maxdepth +0 rejected", []string{"-maxdepth", "+0"}, true},
		{"mindepth 0", []string{"-mindepth", "0"}, false},
		{"mindepth +1 rejected", []string{"-mindepth", "+1"}, true},
		{"mindepth -1 rejected", []string{"-mindepth", "-1"}, true},
		{"maxdepth empty rejected", []string{"-maxdepth", ""}, true},
		{"maxdepth abc rejected", []string{"-maxdepth", "abc"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseExpression(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestParseEmptyParens verifies that empty parentheses are rejected.
func TestParseEmptyParens(t *testing.T) {
	_, err := parseExpression([]string{"(", ")"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty parentheses")
}

// TestParseParensWithContent verifies that non-empty parentheses are accepted.
func TestParseParensWithContent(t *testing.T) {
	pr, err := parseExpression([]string{"(", "-true", ")"})
	require.NoError(t, err)
	assert.NotNil(t, pr.expr)
}

// TestParseSizeEdgeCases covers size parsing edge cases.
func TestParseSizeEdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		n       int64
		cmp     cmpOp
		unit    byte
	}{
		{"simple bytes", "10c", false, 10, cmpExact, 'c'},
		{"plus kilobytes", "+5k", false, 5, cmpMore, 'k'},
		{"minus megabytes", "-3M", false, 3, cmpLess, 'M'},
		{"default 512-byte blocks", "100", false, 100, cmpExact, 'b'},
		{"zero bytes", "0c", false, 0, cmpExact, 'c'},
		{"gigabytes", "1G", false, 1, cmpExact, 'G'},
		{"word units", "10w", false, 10, cmpExact, 'w'},
		{"empty string", "", true, 0, 0, 0},
		{"just plus", "+", true, 0, 0, 0},
		{"just minus", "-", true, 0, 0, 0},
		{"just unit", "c", true, 0, 0, 0},
		{"invalid chars", "abc", true, 0, 0, 0},
		{"negative number", "-5c", false, 5, cmpLess, 'c'},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			su, err := parseSize(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.n, su.n)
				assert.Equal(t, tt.cmp, su.cmp)
				assert.Equal(t, tt.unit, su.unit)
			}
		})
	}
}

// TestParseBlockedPredicates verifies all dangerous predicates are blocked.
func TestParseBlockedPredicates(t *testing.T) {
	blocked := []string{
		"-delete", "-ok", "-okdir",
		"-fls", "-fprint", "-fprint0", "-fprintf",
		"-regex", "-iregex",
	}
	for _, pred := range blocked {
		t.Run(pred, func(t *testing.T) {
			// Blocked predicates that take an argument need one to not fail with "missing argument".
			args := []string{pred}
			if pred == "-ok" || pred == "-okdir" {
				args = append(args, "cmd", ";")
			}
			_, err := parseExpression(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "blocked")
		})
	}
}

// TestParseExec verifies -exec/-execdir parsing.
func TestParseExec(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantErr     bool
		errContains string
		wantBatch   bool
	}{
		{"exec single", []string{"-exec", "echo", "{}", ";"}, false, "", false},
		{"exec batch", []string{"-exec", "echo", "{}", "+"}, false, "", true},
		{"execdir single", []string{"-execdir", "echo", "{}", ";"}, false, "", false},
		{"execdir batch", []string{"-execdir", "echo", "{}", "+"}, false, "", true},
		{"exec missing command", []string{"-exec"}, true, "missing command", false},
		{"exec missing terminator", []string{"-exec", "echo", "{}"}, true, "missing terminator", false},
		{"exec without placeholder", []string{"-exec", "echo", ";"}, false, "", false},
		{"exec empty command", []string{"-exec", ";"}, true, "missing command", false},
		{"exec with extra args", []string{"-exec", "grep", "-l", "{}", ";"}, false, "", false},
		{"exec batch multiple placeholders", []string{"-exec", "echo", "{}", "x", "{}", "+"}, true, "only one instance", false},
		{"exec batch embedded placeholder rejected", []string{"-exec", "echo", "foo{}", "{}", "+"}, true, "only one instance", false},
		{"exec batch only embedded placeholder rejected", []string{"-exec", "echo", "foo{}", "+"}, true, "missing terminator", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := parseExpression(tt.args)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, pr.expr)
				assert.Equal(t, tt.wantBatch, pr.expr.execBatch)
			}
		})
	}
}

// TestParseExpressionLimits verifies AST depth and node limits.
func TestParseExpressionLimits(t *testing.T) {
	t.Run("depth limit", func(t *testing.T) {
		// Build a deeply nested expression: ! ! ! ! ... -true
		args := make([]string, 0, maxExprDepth+2)
		for range maxExprDepth + 1 {
			args = append(args, "!")
		}
		args = append(args, "-true")
		_, err := parseExpression(args)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "too deeply nested")
	})

	t.Run("node limit", func(t *testing.T) {
		// Build a wide flat expression: -true -o -true -o -true ...
		// Each "-true -o" pair adds nodes without increasing depth.
		// We need maxExprNodes+1 leaf nodes to exceed the limit.
		count := maxExprNodes + 1
		args := make([]string, 0, count*2)
		for i := range count {
			if i > 0 {
				args = append(args, "-o")
			}
			args = append(args, "-true")
		}
		_, err := parseExpression(args)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "too many nodes")
	})
}
