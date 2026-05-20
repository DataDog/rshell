// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package testcmd_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

// Campaign: 2026-05-20-gpt-5.5-cyber-3

func TestVulnHuntBuiltinFlagDrivenExploit_FlagLookingOperandsStayData(t *testing.T) {
	stdout, stderr, code := runScript(t, `test --no-such-flag; echo unknown=$?
test --; echo dashdash=$?
test -h; echo lone_h=$?
test --help >/dev/null; echo test_help=$?
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "unknown=0\ndashdash=0\nlone_h=0\ntest_help=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinFlagDrivenExploit_BracketHelpRequiresExactSingleArg(t *testing.T) {
	stdout, stderr, code := runScript(t, `[ --help ]; echo data_help=$?
[ --not-help; echo missing=$?
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "data_help=0\nmissing=2\n", stdout)
	assert.Equal(t, "[: missing `]'\n", stderr)
}

func TestVulnHuntBuiltinResourceExhaustion_RepeatedNegationDepthCapped(t *testing.T) {
	script := "test " + strings.Repeat("! ", 200) + `""`
	mustNotHang(t, func() {
		_, stderr, code := runScript(t, script, "")
		assert.Equal(t, 2, code)
		assert.Contains(t, stderr, "expression too deeply nested")
	})
}

func TestVulnHuntBuiltinResourceExhaustion_LongLogicalChainDoesNotHang(t *testing.T) {
	var b strings.Builder
	b.WriteString(`test "x"`)
	for i := 0; i < 2000; i++ {
		b.WriteString(` -a "x"`)
	}

	mustNotHang(t, func() {
		_, stderr, code := runScript(t, b.String(), "")
		assert.Equal(t, 0, code)
		assert.Empty(t, stderr)
	})
}

func TestVulnHuntBuiltinDeclaredVsImplemented_SyntaxDiagnosticsAndExitCodes(t *testing.T) {
	stdout, stderr, code := runScript(t, `test ""; echo false=$?
test "x"; echo true=$?
test 1 -eq x; echo badint=$?
test '(' ')'; echo empty_group=$?
test a b c d e; echo extra=$?
[ 1 -eq 1; echo missing_bracket=$?
`, "")

	assert.Equal(t, 0, code)
	assert.Equal(t, "false=1\ntrue=0\nbadint=2\nempty_group=2\nextra=2\nmissing_bracket=2\n", stdout)
	assert.Contains(t, stderr, "test: x: integer expression expected")
	assert.Contains(t, stderr, "test: missing argument")
	assert.Contains(t, stderr, "test: too many arguments")
	assert.Contains(t, stderr, "[: missing `]'")
}

func TestVulnHuntBuiltinFileAccessBypass_SymlinkEscapePredicatesStaySandboxed(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	assert.NoError(t, os.Mkdir(allowed, 0755))
	assert.NoError(t, os.Mkdir(secret, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(allowed, "inside.txt"), []byte("inside"), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(secret, "hidden.txt"), []byte("secret"), 0644))
	if err := os.Symlink("../secret/hidden.txt", filepath.Join(allowed, "escape")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	stdout, stderr, code := runScript(t, `test -e escape; echo exists=$?
test -f escape; echo regular=$?
test -h escape; echo symlink_h=$?
test -L escape; echo symlink_L=$?
test -e inside.txt; echo inside=$?
`, allowed, interp.AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "exists=1\nregular=1\nsymlink_h=0\nsymlink_L=0\ninside=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinFileAccessBypass_FileCompareOutsideSandboxHasNoExistenceOracle(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	secret := filepath.Join(root, "secret")
	assert.NoError(t, os.Mkdir(allowed, 0755))
	assert.NoError(t, os.Mkdir(secret, 0755))
	assert.NoError(t, os.WriteFile(filepath.Join(allowed, "newer.txt"), []byte("new"), 0644))
	assert.NoError(t, os.WriteFile(filepath.Join(secret, "older.txt"), []byte("old"), 0644))

	stdout, stderr, code := runScript(t, `test newer.txt -nt ../secret/older.txt; echo right_existing=$?
test newer.txt -nt ../secret/missing.txt; echo right_missing=$?
test ../secret/older.txt -nt newer.txt; echo left_existing=$?
test ../secret/missing.txt -nt newer.txt; echo left_missing=$?
test ../secret/older.txt -ot newer.txt; echo ot_left_existing=$?
test ../secret/missing.txt -ot newer.txt; echo ot_left_missing=$?
`, allowed, interp.AllowedPaths([]string{allowed}))

	assert.Equal(t, 0, code)
	assert.Equal(t, "right_existing=0\nright_missing=0\nleft_existing=1\nleft_missing=1\not_left_existing=0\not_left_missing=0\n", stdout)
	assert.Empty(t, stderr)
}

func TestVulnHuntBuiltinDeclaredVsImplemented_StaticSurfaceHasNoContentReadExecNetworkOrProcParsers(t *testing.T) {
	srcBytes, err := os.ReadFile("testcmd.go")
	assert.NoError(t, err)
	src := string(srcBytes)

	assert.NotContains(t, src, "OpenFile(")
	assert.NotContains(t, src, "RunCommand")
	assert.NotContains(t, src, "Stdin")
	assert.NotContains(t, src, "os/exec")
	assert.NotContains(t, src, "net/")
	assert.NotContains(t, src, "builtins/internal/proc")
	assert.NotContains(t, src, "builtins/internal/diskstats")
}
