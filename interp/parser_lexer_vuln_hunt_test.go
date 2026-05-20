// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: parser-lexer (subsystem)

package interp

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runParserLexerVulnHuntScript(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "parser_lexer_vuln_hunt.sh")
	if err != nil {
		return "", err.Error() + "\n", 2, nil
	}

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{
		StdIO(nil, &stdout, &stderr),
		allowAllCommandsOpt(),
	}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	exitCode := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			exitCode = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

func TestVulnHuntSubsystemInvariantViolation_ControlBytesAndLineEndingsDeterministic(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"crlf_separates_statements": {
			script: "echo one\r\necho two\r\n",
			want:   "one\ntwo\n",
		},
		"nul_matches_bash_ignored_byte_model": {
			script: "echo before\x00echo after\n",
			want:   "beforeecho after\n",
		},
		"comment_before_crlf_stays_comment": {
			script: "echo one # ignored\r\necho two\n",
			want:   "one\ntwo\n",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runParserLexerVulnHuntScript(t, tc.script)

			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, tc.want, stdout)
			assert.Empty(t, stderr)
		})
	}

	_, err := ParseScript("echo ok\xff\n", "invalid_utf8.sh")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid UTF-8 encoding")
}

func TestVulnHuntSubsystemInvariantViolation_UnsupportedSyntaxValidationPrecedesExecution(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"case_clause": {
			script: "echo before\ncase x in x) echo hidden;; esac\necho after\n",
			want:   "case statements are not supported",
		},
		"function_decl": {
			script: "echo before\nf() { echo hidden; }\necho after\n",
			want:   "function declarations are not supported",
		},
		"process_substitution": {
			script: "echo before\ncat <(echo hidden)\necho after\n",
			want:   "process substitution is not supported",
		},
		"background_execution": {
			script: "echo before\necho hidden & echo after\n",
			want:   "background execution",
		},
		"herestring": {
			script: "echo before\ncat <<< hidden\necho after\n",
			want:   "<<< (herestring) is not supported",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			stdout, stderr, code, err := runParserLexerVulnHuntScript(t, tc.script)

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout, "whole-file validation must reject before earlier statements execute")
			assert.Contains(t, stderr, tc.want)
		})
	}
}

func TestVulnHuntSubsystemResourceLimitBypass_MaxScriptBytesRejectsBeforeParser(t *testing.T) {
	script := strings.Repeat("echo parser-lexer\n", MaxScriptBytes/len("echo parser-lexer\n")+1)
	require.Greater(t, len(script), MaxScriptBytes)

	_, err := ParseScript(script, "oversized_parser_lexer.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")
}

func TestVulnHuntSubsystemThreatModelCoverage_ProductionParsingUsesParseScript(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	repoRoot := filepath.Dir(filepath.Dir(file))

	runtimeDirs := map[string]bool{
		"allowedpaths": true,
		"builtins":     true,
		"cmd":          true,
		"interp":       true,
		"internal":     true,
	}
	allowedDirectParserFiles := map[string]bool{
		filepath.Join("interp", "api.go"): true,
	}

	var violations []string
	for dir := range runtimeDirs {
		root := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			require.NoError(t, err)
			if d.IsDir() {
				switch d.Name() {
				case ".git", "vendor":
					return filepath.SkipDir
				}
				relDir, err := filepath.Rel(repoRoot, path)
				require.NoError(t, err)
				switch relDir {
				case filepath.Join("builtins", "testutil"), filepath.Join("builtins", "tests"):
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			rel, err := filepath.Rel(repoRoot, path)
			require.NoError(t, err)
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			if strings.Contains(string(data), "syntax.NewParser") && !allowedDirectParserFiles[rel] {
				violations = append(violations, rel)
			}
			return nil
		})
		require.NoError(t, err)
	}

	assert.Empty(t, violations, "production parser use must go through interp.ParseScript for MaxScriptBytes enforcement")
}

func TestVulnHuntSubsystemPanicStateCorruption_ValidationErrorDoesNotPoisonRunnerReuse(t *testing.T) {
	blocked, err := ParseScript("case x in x) echo hidden;; esac\n", "blocked_case.sh")
	require.NoError(t, err)
	valid, err := ParseScript("echo ok\n", "valid_after_blocked.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	runErr := r.Run(context.Background(), blocked)
	var status ExitStatus
	require.ErrorAs(t, runErr, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "case statements are not supported")

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, r.Run(context.Background(), valid))
	assert.Equal(t, "ok\n", stdout.String())
	assert.Empty(t, stderr.String())
}
