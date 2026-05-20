package find

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVulnHuntBuiltinFindBlockedPredicatesBehindBooleanParsing(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"or delete", []string{"-true", "-o", "-delete"}},
		{"not ok", []string{"!", "-ok", "echo", ";"}},
		{"group fprintf", []string{"(", "-true", ")", "-fprintf", "/tmp/out", "%p"}},
		{"and regex", []string{"-name", "*.txt", "-a", "-regex", ".*"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseExpression(tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "blocked")
		})
	}
}

func TestVulnHuntBuiltinFindExecCommandNameSubstitutionStillUsesPolicy(t *testing.T) {
	tests := []struct {
		name     string
		execKind exprKind
		relPath  string
		print    string
		wantCmd  string
	}{
		{
			name:     "execdir basename replacement",
			execKind: exprExecDir,
			relPath:  "echo",
			wantCmd:  "./echo",
		},
		{
			name:     "exec full path replacement",
			execKind: exprExec,
			relPath:  "dir/echo",
			print:    "dir/echo",
			wantCmd:  "dir/echo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			runCalled := false
			callCtx := newPentestCallCtx(&stdout, &stderr)
			callCtx.CommandAllowed = func(name string) bool {
				return name == "echo"
			}
			callCtx.RunCommand = func(_ context.Context, _ string, _ string, _ []string) (uint8, error) {
				runCalled = true
				return 0, nil
			}

			ec := &evalContext{
				callCtx: callCtx,
				ctx:     context.Background(),
				relPath: tt.relPath,
				info:    &mockFileInfo{},
			}
			if tt.print != "" {
				ec.printPath = tt.print
			}

			e := &expr{kind: tt.execKind, execCmd: "{}", execArgs: []string{"arg"}}
			var result evalResult
			if tt.execKind == exprExecDir {
				result = evalExecDir(ec, e)
			} else {
				result = evalExec(ec, e)
			}

			assert.False(t, result.matched)
			assert.True(t, ec.failed)
			assert.False(t, runCalled)
			assert.Contains(t, stderr.String(), tt.wantCmd)
			assert.Contains(t, stderr.String(), "not allowed")
		})
	}
}
