// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: for_clause (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runForClauseCyber3(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "for_clause_cyber3_vuln_hunt.sh")
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	allOpts := append([]RunnerOption{StdIO(nil, &stdout, &stderr), allowAllCommandsOpt()}, opts...)
	r, err := New(allOpts...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), prog)
	code := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			code = int(status)
			err = nil
		}
	}
	return stdout.String(), stderr.String(), code, err
}

func TestVulnHuntShellFeatureExpansionChain_ForItemsRemainData(t *testing.T) {
	stdout, stderr, code, err := runForClauseCyber3(t, `for item in "$(printf 'echo HACKED')" 'semi;echo HACKED' '$(echo nope)' 'x > out.txt'; do
  echo "item=[$item]"
done
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "item=[echo HACKED]\nitem=[semi;echo HACKED]\nitem=[$(echo nope)]\nitem=[x > out.txt]\n", stdout)
	assert.Empty(t, stderr)
	assert.NotContains(t, stdout, "\nHACKED\n")
}

func TestVulnHuntShellFeatureExpansionChain_ForItemCatShortcutPolicyPrecedesBody(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("top secret\n"), 0o644))

	prog := parseScript(t, `for item in $(<secret.txt); do echo "body=$item"; done
echo after
`)
	var stdout, stderr bytes.Buffer
	r, err := New(
		StdIO(nil, &stdout, &stderr),
		AllowedPaths([]string{dir}),
		AllowedCommands([]string{"rshell:echo"}),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })
	err = r.Run(context.Background(), prog)
	code := 0
	if err != nil {
		var status ExitStatus
		if errors.As(err, &status) {
			code = int(status)
			err = nil
		}
	}

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "after\n", stdout.String())
	assert.Contains(t, stderr.String(), "file read not permitted")
	assert.Contains(t, stderr.String(), "cat not in allowed commands")
	assert.NotContains(t, stdout.String()+stderr.String(), "top secret")
	assert.NotContains(t, stdout.String(), "body=")
}

func TestVulnHuntShellFeatureParserConfusion_ForValidationPrecedesExecution(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"c_style": {
			script: "X=changed\nfor (( i=0; i<1; i++ )); do echo body; done\n",
			want:   "c-style for loops are not supported",
		},
		"select": {
			script: "X=changed\nselect item in a b; do echo body; done\n",
			want:   "select statements are not supported",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })

			require.NoError(t, r.Run(context.Background(), parseScript(t, "X=original\n")))
			err = r.Run(context.Background(), parseScript(t, tc.script))
			var status ExitStatus
			require.ErrorAs(t, err, &status)
			assert.Equal(t, ExitStatus(2), status)
			assert.Contains(t, stderr.String(), tc.want)

			stdout.Reset()
			stderr.Reset()
			require.NoError(t, r.Run(context.Background(), parseScript(t, "echo $X\n")))
			assert.Equal(t, "original\n", stdout.String())
			assert.Empty(t, stderr.String())
		})
	}
}

func TestVulnHuntShellFeatureParserConfusion_NoInLoopHasNoImplicitPositionalParams(t *testing.T) {
	stdout, stderr, code, err := runForClauseCyber3(t, `for item; do echo "[$item]"; done
echo done=$?
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "done=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureSubshellIsolation_ForLoopDoesNotLeakState(t *testing.T) {
	stdout, stderr, code, err := runForClauseCyber3(t, `OUT=outer
( for OUT in a b; do INNER=$OUT; done; echo "sub=$OUT/$INNER" )
echo "parent=$OUT/$INNER"
for item in p q; do PIPE=$item; echo "$PIPE"; done | cat >/dev/null
echo "after_pipe=$PIPE/$item"
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "sub=b/b\nparent=outer/\nafter_pipe=/\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_ForLoopControlDoesNotLeakCounters(t *testing.T) {
	stdout, stderr, code, err := runForClauseCyber3(t, `for i in 1; do break 99; done
for j in a b; do echo "$j"; done
for k in 1 2; do continue 99; echo bad; done
echo after
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "a\nb\nafter\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsHugeForLoop(t *testing.T) {
	line := "for i in a b c; do echo $i; done\n"
	script := strings.Repeat(line, MaxScriptBytes/len(line)+1)
	require.Greater(t, len(script), MaxScriptBytes)

	_, err := ParseScript(script, "for_clause_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")
}

func TestVulnHuntShellFeatureCompositionAttack_GlobbedForItemsRemainData(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"$(echo hacked).txt", "semi;echo hacked.txt", "redir>out.txt", "plain.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
	}

	stdout, stderr, code, err := runForClauseCyber3(t,
		`for item in *; do echo "[$item]"; done
`,
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout, "[$(echo hacked).txt]\n")
	assert.Contains(t, stdout, "[semi;echo hacked.txt]\n")
	assert.Contains(t, stdout, "[redir>out.txt]\n")
	assert.Contains(t, stdout, "[plain.txt]\n")
	assert.Empty(t, stderr)
	assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
}

func TestVulnHuntShellFeatureCompositionAttack_ForGlobReadDirCapIsShared(t *testing.T) {
	dir := t.TempDir()
	args := make([]string, MaxGlobReadDirCalls+1)
	for i := range args {
		args[i] = fmt.Sprintf("nomatch_%d_*", i)
	}

	stdout, stderr, code, err := runForClauseCyber3(t,
		"for item in "+strings.Join(args, " ")+"; do :; done\n",
		AllowedPaths([]string{dir}),
	)

	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr, "glob expansion exceeded maximum number of directory reads")
}

func TestVulnHuntShellFeatureRedirectionChain_ForBlockedRedirectRejectsGlobally(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	require.NoError(t, r.Run(context.Background(), parseScript(t, "X=original\n")))
	err = r.Run(context.Background(), parseScript(t, "X=changed\nfor item in a; do echo body > out.txt; done\necho after\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "> file redirection is not supported")

	stdout.Reset()
	stderr.Reset()
	require.NoError(t, r.Run(context.Background(), parseScript(t, "echo $X\n")))
	assert.Equal(t, "original\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureRedirectionChain_ForInputRedirectUsesLoopVariableSandbox(t *testing.T) {
	base := t.TempDir()
	allowed := filepath.Join(base, "allowed")
	secret := filepath.Join(base, "secret")
	require.NoError(t, os.Mkdir(allowed, 0o755))
	require.NoError(t, os.Mkdir(secret, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "data.txt"), []byte("safe\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(secret, "secret.txt"), []byte("top-secret-data\n"), 0o644))

	stdout, stderr, code, err := runForClauseCyber3(t, `for file in data.txt ../secret/secret.txt data.txt; do
  cat < "$file"
  echo "status=$?"
done
cat <<EOF
restored
EOF
`,
		AllowedPaths([]string{allowed}),
	)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "safe\nstatus=0\nstatus=1\nsafe\nstatus=0\nrestored\n", stdout)
	assert.Contains(t, stderr, "permission denied")
	assert.NotContains(t, stdout+stderr, "top-secret-data")
}

func TestVulnHuntShellFeatureSignalContext_ForLoopChecksCancellation(t *testing.T) {
	r := newTimeoutRunner(t, MaxExecutionTime(40*time.Millisecond))
	r.execHandler = func(ctx context.Context, _ []string) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(15 * time.Millisecond):
			return nil
		}
	}

	err := r.Run(context.Background(), parseScript(t, "for item in 1 2 3 4 5 6 7 8 9 10; do slowcmd; done\n"))

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
