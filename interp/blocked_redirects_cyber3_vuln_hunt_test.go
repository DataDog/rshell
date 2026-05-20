// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt campaign: 2026-05-20-gpt-5.5-cyber-3
// Target: blocked_redirects (shell-feature)

package interp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func runBlockedRedirectsCyber3(t *testing.T, script string, opts ...RunnerOption) (string, string, int, error) {
	t.Helper()

	prog, err := ParseScript(script, "blocked_redirects_cyber3_vuln_hunt.sh")
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

func TestVulnHuntShellFeatureParserConfusion_BlockedRedirectsRejectBeforeExecution(t *testing.T) {
	tests := map[string]struct {
		script string
		want   string
	}{
		"write_truncate":       {"echo data > out.txt\n", "> file redirection is not supported"},
		"write_clobber":        {"echo data >| out.txt\n", "> file redirection is not supported"},
		"append":               {"echo data >> out.txt\n", ">> file redirection is not supported"},
		"write_all":            {"echo data &> out.txt\n", "&> file redirection is not supported"},
		"append_all":           {"echo data &>> out.txt\n", "&>> file redirection is not supported"},
		"read_write":           {"cat <> out.txt\n", "<> file redirection is not supported"},
		"herestring":           {"cat <<< hello\n", "<<< (herestring) is not supported"},
		"input_dup":            {"echo data <&0\n", "<&N fd duplication is not supported"},
		"output_dup_close":     {"echo data >&-\n", ">&N fd duplication is not supported"},
		"output_dup_bad_src":   {"echo data 3>&1\n", ">&N fd duplication is not supported"},
		"output_dup_bad_dst":   {"echo data >&3\n", ">&N fd duplication is not supported"},
		"input_fd3":            {"echo data 3< input.txt\n", "3< input fd redirection is not supported"},
		"dynamic_output":       {"TARGET=/dev/null\necho data > \"$TARGET\"\n", "> file redirection is not supported"},
		"fd0_output_to_devnul": {"echo data 0>/dev/null\n", "> file redirection is not supported"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr, code, err := runBlockedRedirectsCyber3(t,
				"echo before\n"+tc.script+"echo after\n",
				AllowedPaths([]string{dir}),
			)

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout, "whole-file validation must reject before any statement executes")
			assert.Contains(t, stderr, tc.want)
			assert.NotContains(t, stdout+stderr, "before")
			assert.NotContains(t, stdout+stderr, "after")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}
}

func TestVulnHuntShellFeatureExpansionChain_DynamicNullTargetsStayBlocked(t *testing.T) {
	for _, script := range []string{
		"TARGET=/dev/null\necho hi > $TARGET\n",
		"echo hi > \"$(printf /dev/null)\"\n",
		"echo hi > '/dev/null'\n",
		"echo hi > /dev//null\n",
		"echo hi > /dev/./null\n",
		"echo hi > /dev/null/\n",
		"echo hi > /dev/null/../null\n",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code, err := runBlockedRedirectsCyber3(t, "echo before\n"+script+"echo after\n")

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "file redirection is not supported")
			assert.NotContains(t, stdout+stderr, "before")
			assert.NotContains(t, stdout+stderr, "after")
		})
	}
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_SupportedNullRedirectsStillWork(t *testing.T) {
	stdout, stderr, code, err := runBlockedRedirectsCyber3(t, `echo hidden >/dev/null
echo visible
no_such_command 2>/dev/null
echo status=$?
echo both &>/dev/null
echo append >>/dev/null
echo append_all &>>/dev/null
echo err >&2
`)

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, "visible\nstatus=127\n", stdout)
	assert.Equal(t, "err\n", stderr)
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_BlockedRedirectsDoNotOpenTargets(t *testing.T) {
	for _, script := range []string{
		"echo hi > fifo\n",
		"cat <> fifo\n",
		"echo hi 3< fifo\n",
		"echo hi >> fifo\n",
		"cat <<< data\n",
	} {
		t.Run(script, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
			require.NoError(t, err)
			t.Cleanup(func() { _ = r.Close() })
			r.openHandler = func(context.Context, string, int, os.FileMode) (io.ReadWriteCloser, error) {
				t.Fatalf("blocked redirect unexpectedly reached openHandler for script %q", script)
				return nil, nil
			}

			err = r.Run(context.Background(), parseScript(t, script))
			var status ExitStatus
			require.ErrorAs(t, err, &status)
			assert.Equal(t, ExitStatus(2), status)
			assert.Empty(t, stdout.String())
			assert.NotEmpty(t, stderr.String())
		})
	}
}

func TestVulnHuntShellFeatureSubshellIsolation_BlockedRedirectInCompoundRejectsGlobally(t *testing.T) {
	for _, script := range []string{
		"(echo hidden > out.txt)\necho after\n",
		"{ echo hidden > out.txt; }\necho after\n",
		"echo $(echo hidden > out.txt)\necho after\n",
		"echo left | echo hidden > out.txt\necho after\n",
	} {
		t.Run(script, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr, code, err := runBlockedRedirectsCyber3(t, script, AllowedPaths([]string{dir}))

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "> file redirection is not supported")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}
}

func TestVulnHuntShellFeatureRedirectionChain_MixedAllowedBlockedRedirectsFailClosed(t *testing.T) {
	for _, script := range []string{
		"echo before\necho hidden >/dev/null > out.txt\necho after\n",
		"echo before\necho hidden 2>&1 > out.txt\necho after\n",
		"echo before\necho hidden &>/dev/null > out.txt\necho after\n",
	} {
		t.Run(script, func(t *testing.T) {
			dir := t.TempDir()
			stdout, stderr, code, err := runBlockedRedirectsCyber3(t, script, AllowedPaths([]string{dir}))

			require.NoError(t, err)
			assert.Equal(t, 2, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "> file redirection is not supported")
			assert.NoFileExists(t, filepath.Join(dir, "out.txt"))
		})
	}
}

func TestVulnHuntShellFeatureCompositionAttack_InvalidRedirectPreventsStateChanges(t *testing.T) {
	var stdout, stderr bytes.Buffer
	r, err := New(StdIO(nil, &stdout, &stderr), allowAllCommandsOpt())
	require.NoError(t, err)
	t.Cleanup(func() { _ = r.Close() })

	err = r.Run(context.Background(), parseScript(t, "X=original\n"))
	require.NoError(t, err)

	err = r.Run(context.Background(), parseScript(t, "X=changed\necho hi > out.txt\n"))
	var status ExitStatus
	require.ErrorAs(t, err, &status)
	assert.Equal(t, ExitStatus(2), status)

	stdout.Reset()
	stderr.Reset()
	err = r.Run(context.Background(), parseScript(t, "echo $X\n"))
	require.NoError(t, err)
	assert.Equal(t, "original\n", stdout.String())
	assert.Empty(t, stderr.String())
}

func TestVulnHuntShellFeatureDeclaredVsImplemented_MaxScriptBytesRejectsHugeBlockedRedirects(t *testing.T) {
	line := "echo hi > out.txt\n"
	script := strings.Repeat(line, MaxScriptBytes/len(line)+1)
	require.Greater(t, len(script), MaxScriptBytes)

	_, err := ParseScript(script, "blocked_redirects_oversized.sh")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum")
	assert.Contains(t, err.Error(), "5 MiB")
}
