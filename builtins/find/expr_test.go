// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"runtime"
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

// TestParsePathPredicateUsesParsePathPredicate verifies that -path, -ipath,
// -newer, -wholename, and -iwholename are routed through parsePathPredicate
// (which applies filepath.ToSlash). On Unix filepath.ToSlash is a no-op so
// we can only verify correct parsing here; actual backslash→slash conversion
// is exercised on Windows CI.
func TestParsePathPredicateUsesParsePathPredicate(t *testing.T) {
	tests := []struct {
		name string
		args []string
		kind exprKind
		want string
	}{
		{"path", []string{"-path", "dir/file"}, exprPath, "dir/file"},
		{"ipath", []string{"-ipath", "dir/file"}, exprIPath, "dir/file"},
		{"newer", []string{"-newer", "dir/ref.txt"}, exprNewer, "dir/ref.txt"},
		{"wholename alias", []string{"-wholename", "dir/file"}, exprPath, "dir/file"},
		{"iwholename alias", []string{"-iwholename", "dir/file"}, exprIPath, "dir/file"},
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

	// Verify -name and -iname do NOT go through parsePathPredicate
	// (they match basenames only, no path separators to normalize).
	t.Run("name is not path-normalized", func(t *testing.T) {
		pr, err := parseExpression([]string{"-name", "*.txt"})
		require.NoError(t, err)
		assert.Equal(t, exprName, pr.expr.kind)
		assert.Equal(t, "*.txt", pr.expr.strVal)
	})

	// On Windows, filepath.ToSlash converts '\' to '/'. Verify that
	// parsePathPredicate actually normalizes backslashes. This subtest
	// is skipped on Unix where '\' is a valid filename character and
	// filepath.ToSlash is a no-op.
	if runtime.GOOS == "windows" {
		windowsTests := []struct {
			name string
			args []string
			kind exprKind
			want string
		}{
			{"path backslash", []string{"-path", `dir\sub\*.go`}, exprPath, "dir/sub/*.go"},
			{"ipath backslash", []string{"-ipath", `Dir\Sub\*.Go`}, exprIPath, "Dir/Sub/*.Go"},
			{"newer backslash", []string{"-newer", `dir\ref.txt`}, exprNewer, "dir/ref.txt"},
			{"wholename backslash", []string{"-wholename", `src\main.go`}, exprPath, "src/main.go"},
			{"iwholename backslash", []string{"-iwholename", `Src\Main.go`}, exprIPath, "Src/Main.go"},
			{"mixed separators", []string{"-path", `dir/sub\file.go`}, exprPath, "dir/sub/file.go"},
			{"multiple backslashes", []string{"-path", `a\b\c\d`}, exprPath, "a/b/c/d"},
		}

		for _, tt := range windowsTests {
			t.Run("windows/"+tt.name, func(t *testing.T) {
				pr, err := parseExpression(tt.args)
				require.NoError(t, err)
				require.NotNil(t, pr.expr)
				assert.Equal(t, tt.kind, pr.expr.kind)
				assert.Equal(t, tt.want, pr.expr.strVal)
			})
		}

		// -name should NOT normalize backslashes even on Windows
		// (basenames never contain path separators).
		t.Run("windows/name not normalized", func(t *testing.T) {
			pr, err := parseExpression([]string{"-name", `file\name`})
			require.NoError(t, err)
			assert.Equal(t, `file\name`, pr.expr.strVal)
		})
	}
}

// TestParseTypePredicate validates the parser accepts b and c type characters.
func TestParseTypePredicate(t *testing.T) {
	tests := []struct {
		name    string
		arg     string
		wantErr bool
	}{
		{"b valid", "b", false},
		{"c valid", "c", false},
		{"b,c valid", "b,c", false},
		{"f,b,c valid", "f,b,c", false},
		{"all types", "b,c,f,d,l,p,s", false},
		{"x invalid", "x", true},
		{"trailing comma", "b,", true},
		{"leading comma", ",c", true},
		{"no comma separator", "bc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseExpression([]string{"-type", tt.arg})
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
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

// TestParsePermPredicate verifies -perm parsing for octal and symbolic modes.
func TestParsePermPredicate(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		permVal uint32
		permCmp byte
	}{
		{"exact octal 644", []string{"-perm", "644"}, false, 0o644, '='},
		{"exact octal 0755", []string{"-perm", "0755"}, false, 0o755, '='},
		{"all bits 0111", []string{"-perm", "-0111"}, false, 0o111, '-'},
		{"any bit 0222", []string{"-perm", "/0222"}, false, 0o222, '/'},
		{"exact 0", []string{"-perm", "0"}, false, 0, '='},
		{"symbolic u=rwx", []string{"-perm", "u=rwx"}, false, 0o700, '='},
		{"symbolic a=r", []string{"-perm", "a=r"}, false, 0o444, '='},
		{"symbolic u=rw,g=r,o=r", []string{"-perm", "u=rw,g=r,o=r"}, false, 0o644, '='},
		{"symbolic = overwrites", []string{"-perm", "u=rw,u=x"}, false, 0o100, '='},
		{"symbolic + adds", []string{"-perm", "u=rw,u+x"}, false, 0o700, '='},
		{"all bits symbolic", []string{"-perm", "-u=x"}, false, 0o100, '-'},
		{"symbolic setuid", []string{"-perm", "-u=s"}, false, 0o4000, '-'},
		{"symbolic setgid", []string{"-perm", "-g=s"}, false, 0o2000, '-'},
		{"symbolic sticky", []string{"-perm", "-o=t"}, false, 0o1000, '-'},
		{"symbolic setuid+exec", []string{"-perm", "u=xs"}, false, 0o4100, '='},
		{"symbolic = clears special", []string{"-perm", "u=s,u=rwx"}, false, 0o700, '='},
		// Copy-bits: basic
		{"copy g=u from empty", []string{"-perm", "g=u"}, false, 0o000, '='},
		{"copy u=rwx,g=u", []string{"-perm", "u=rwx,g=u"}, false, 0o770, '='},
		{"copy u=rw,o=u", []string{"-perm", "u=rw,o=u"}, false, 0o606, '='},
		{"copy o=r,g=o", []string{"-perm", "o=r,g=o"}, false, 0o044, '='},
		{"copy u=rwx,g=u,o=g cascade", []string{"-perm", "u=rwx,g=u,o=g"}, false, 0o777, '='},
		{"copy g=o from empty", []string{"-perm", "g=o"}, false, 0o000, '='},
		{"copy u=g", []string{"-perm", "g=rx,u=g"}, false, 0o550, '='},
		{"copy o=g", []string{"-perm", "g=rx,o=g"}, false, 0o055, '='},
		// Copy-bits: with + and - operators
		{"copy g+u adds", []string{"-perm", "u=rwx,g=r,g+u"}, false, 0o770, '='},
		{"copy g-u removes", []string{"-perm", "u=rx,g=rwx,g-u"}, false, 0o520, '='},
		// Copy-bits: special bits NOT copied
		{"copy g=u does not copy setuid", []string{"-perm", "u=s,g=u"}, false, 0o4000, '='},
		// Copy-bits: invalid mixing
		{"copy g=ur invalid", []string{"-perm", "g=ur"}, true, 0, 0},
		{"copy g=uo invalid", []string{"-perm", "g=uo"}, true, 0, 0},
		// Conditional execute X
		{"X from zero no-op", []string{"-perm", "a+X"}, false, 0o000, '='},
		{"X with existing x", []string{"-perm", "u=x,a+X"}, false, 0o111, '='},
		{"X with = from zero", []string{"-perm", "u=X"}, false, 0o000, '='},
		{"X with = and existing x", []string{"-perm", "u=rx,g=X"}, false, 0o510, '='},
		{"a=rX from zero", []string{"-perm", "a=rX"}, false, 0o444, '='},
		{"u=rwx,a=rX", []string{"-perm", "u=rwx,a=rX"}, false, 0o555, '='},
		{"a+rX from zero", []string{"-perm", "a+rX"}, false, 0o444, '='},
		{"u=x,a+rX", []string{"-perm", "u=x,a+rX"}, false, 0o555, '='},
		// Sticky bit respects who mask (t only applies when who includes o/a)
		{"sticky u+t is no-op", []string{"-perm", "u+t"}, false, 0o000, '='},
		{"sticky g=t is no-op", []string{"-perm", "g=t"}, false, 0o000, '='},
		{"sticky ug+t is no-op", []string{"-perm", "ug+t"}, false, 0o000, '='},
		{"sticky o+t sets", []string{"-perm", "o+t"}, false, 0o1000, '='},
		{"sticky a=t sets", []string{"-perm", "a=t"}, false, 0o1000, '='},
		{"sticky ug+st sets suid sgid only", []string{"-perm", "ug+st"}, false, 0o6000, '='},
		// Errors
		{"missing arg", []string{"-perm"}, true, 0, 0},
		{"invalid octal", []string{"-perm", "xyz"}, true, 0, 0},
		{"invalid mode 99999", []string{"-perm", "99999"}, true, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, err := parseExpression(tt.args)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				require.NotNil(t, pr.expr)
				assert.Equal(t, exprPerm, pr.expr.kind)
				assert.Equal(t, tt.permVal, pr.expr.permVal)
				assert.Equal(t, tt.permCmp, pr.expr.permCmp)
			}
		})
	}
}

// TestParseNewPredicates verifies the new predicates parse correctly.
func TestParseNewPredicates(t *testing.T) {
	// No-arg predicates.
	noArgTests := []struct {
		arg  string
		kind exprKind
	}{
		{"-quit", exprQuit},
	}
	for _, tt := range noArgTests {
		t.Run(tt.arg, func(t *testing.T) {
			pr, err := parseExpression([]string{tt.arg})
			require.NoError(t, err)
			require.NotNil(t, pr.expr)
			assert.Equal(t, tt.kind, pr.expr.kind)
		})
	}

}

// TestParseHelpRequested verifies that --help as a standalone predicate
// returns errHelpRequested, and that -name --help consumes it as a pattern.
func TestParseHelpRequested(t *testing.T) {
	t.Run("standalone --help", func(t *testing.T) {
		_, err := parseExpression([]string{"--help"})
		assert.ErrorIs(t, err, errHelpRequested)
	})
	t.Run("--help after other predicate", func(t *testing.T) {
		_, err := parseExpression([]string{"-true", "--help"})
		assert.ErrorIs(t, err, errHelpRequested)
	})
	t.Run("-name consumes --help as pattern", func(t *testing.T) {
		pr, err := parseExpression([]string{"-name", "--help"})
		require.NoError(t, err)
		require.NotNil(t, pr.expr)
		assert.Equal(t, exprName, pr.expr.kind)
		assert.Equal(t, "--help", pr.expr.strVal)
	})
}

// TestParseExpressionLimits verifies AST depth and node limits.
func TestParseExpressionLimits(t *testing.T) {
	t.Run("depth limit", func(t *testing.T) {
		// Build a deeply nested expression: ! ! ! ! ... -true
		args := make([]string, 0, maxExprDepth+2)
		for i := 0; i < maxExprDepth+1; i++ {
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
		for i := 0; i < count; i++ {
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
