// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// vuln-hunt 2026-05-20-gpt-5.5-cyber-3 (target: tr)

package tr_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/interp"
)

func TestVulnHuntBuiltinTrFlagDrivenExploit_DangerousFlagsRejected(t *testing.T) {
	for _, script := range []string{
		"tr --output=/tmp/out a b",
		"tr --reference=/etc/passwd a b",
		"tr -w a b",
	} {
		t.Run(script, func(t *testing.T) {
			stdout, stderr, code := runScript(t, script, t.TempDir())
			assert.Equal(t, 1, code)
			assert.Empty(t, stdout)
			assert.Contains(t, stderr, "tr:")
			assert.Contains(t, stderr, "Try 'tr --help' for more information.")
		})
	}
}

func TestVulnHuntBuiltinTrFlagDrivenExploit_FlagShapedSetsStayData(t *testing.T) {
	stdout, stderr, code := trRun(t, "-d-d", "-- -d XY")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "XYXY", stdout)

	stdout, stderr, code = trRun(t, "az", "a -Z")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "-z", stdout)
}

func TestVulnHuntBuiltinTrIntegerOverflow_RepeatCountsClampedOrRejected(t *testing.T) {
	stdout, stderr, code := trRun(t, "a", "a '[b*9223372036854775807]'")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "b", stdout)

	_, stderr, code = trRun(t, "a", "a '[b*9223372036854775808]'")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid repeat count '9223372036854775808'")

	_, stderr, code = trRun(t, "a", "a '[b*-0]'")
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr, "invalid repeat count '-0'")
}

func TestVulnHuntBuiltinTrIntegerOverflow_FillRepeatTailDoesNotUnderflow(t *testing.T) {
	stdout, stderr, code := trRun(t, "a", "a '[x*]YZ'")
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, "Y", stdout)
}

func TestVulnHuntBuiltinTrResourceExhaustion_LargeFiniteInputStreams(t *testing.T) {
	dir := t.TempDir()
	input := strings.Repeat("abcXYZ\n", 80_000)
	writeFile(t, dir, "in.txt", input)

	stdout, stderr, code := runScript(t, "cat in.txt | tr a-z A-Z", dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, strings.ToUpper(input), stdout)
}

func TestVulnHuntBuiltinTrDeclaredVsImplemented_BinaryBytesRemainBytes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "in.txt", string([]byte{0x00, 'a', '\r', '\n', 0xff, 'a'}))

	stdout, stderr, code := runScript(t, `tr '\000a\377' 'XyZ' < in.txt`, dir, interp.AllowedPaths([]string{dir}))
	assert.Equal(t, 0, code)
	assert.Empty(t, stderr)
	assert.Equal(t, string([]byte{'X', 'y', '\r', '\n', 'Z', 'y'}), stdout)
}
