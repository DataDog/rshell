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
		cmp     int
		unit    byte
	}{
		{"simple bytes", "10c", false, 10, 0, 'c'},
		{"plus kilobytes", "+5k", false, 5, 1, 'k'},
		{"minus megabytes", "-3M", false, 3, -1, 'M'},
		{"default 512-byte blocks", "100", false, 100, 0, 'b'},
		{"zero bytes", "0c", false, 0, 0, 'c'},
		{"gigabytes", "1G", false, 1, 0, 'G'},
		{"word units", "10w", false, 10, 0, 'w'},
		{"empty string", "", true, 0, 0, 0},
		{"just plus", "+", true, 0, 0, 0},
		{"just minus", "-", true, 0, 0, 0},
		{"just unit", "c", true, 0, 0, 0},
		{"invalid chars", "abc", true, 0, 0, 0},
		{"negative number", "-5c", false, 5, -1, 'c'},
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
		"-exec", "-execdir", "-delete", "-ok", "-okdir",
		"-fls", "-fprint", "-fprint0", "-fprintf",
		"-regex", "-iregex",
	}
	for _, pred := range blocked {
		t.Run(pred, func(t *testing.T) {
			// Blocked predicates that take an argument need one to not fail with "missing argument".
			args := []string{pred}
			if pred == "-exec" || pred == "-execdir" || pred == "-ok" || pred == "-okdir" {
				args = append(args, "cmd", ";")
			}
			_, err := parseExpression(args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "blocked")
		})
	}
}

// TestParseExpressionLimits verifies AST depth and node limits.
func TestParseExpressionLimits(t *testing.T) {
	// Build a deeply nested expression: ! ! ! ! ... -true
	args := make([]string, 0, maxExprDepth+2)
	for i := 0; i < maxExprDepth+1; i++ {
		args = append(args, "!")
	}
	args = append(args, "-true")
	_, err := parseExpression(args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "too deeply nested")
}
