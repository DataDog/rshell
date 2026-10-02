// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcpasswd

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const samplePasswdFile = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1:daemon:/usr/sbin:/usr/sbin/nologin
dd-agent:x:998:998::/opt/datadog-agent:/bin/false
# a comment line

malformed-line-no-colon
`

func TestParsePasswdFile_HappyPath(t *testing.T) {
	found, err := parsePasswdFile(strings.NewReader(samplePasswdFile), "dd-agent")
	assert.NoError(t, err)
	assert.True(t, found)
}

func TestParsePasswdFile_RootUser(t *testing.T) {
	found, err := parsePasswdFile(strings.NewReader(samplePasswdFile), "root")
	assert.NoError(t, err)
	assert.True(t, found)
}

func TestParsePasswdFile_NotFound(t *testing.T) {
	found, err := parsePasswdFile(strings.NewReader(samplePasswdFile), "nosuchuser")
	assert.NoError(t, err)
	assert.False(t, found)
}

func TestParsePasswdFile_SkipsCommentsAndBlankAndMalformedLines(t *testing.T) {
	found, err := parsePasswdFile(strings.NewReader(samplePasswdFile), "malformed-line-no-colon")
	assert.NoError(t, err)
	assert.False(t, found, "a line with no colon at all never matches any account name")
}

func TestParsePasswdFile_EmptyInput(t *testing.T) {
	found, err := parsePasswdFile(strings.NewReader(""), "root")
	assert.NoError(t, err)
	assert.False(t, found)
}

func TestParsePasswdFile_LineTooLong(t *testing.T) {
	huge := "hugeuser:x:1:1:" + strings.Repeat("a", maxPasswdLine*2) + ":/home/huge:/bin/bash\n"
	_, err := parsePasswdFile(strings.NewReader(huge), "hugeuser")
	assert.Error(t, err)
}

func TestUserExists_RootAlwaysPresentOnLinux(t *testing.T) {
	// Sanity check that the Linux backend is actually wired (i.e. this test
	// file is compiled): the well-known "root" account must exist in the
	// real /etc/passwd on every Linux host.
	found, err := UserExists("root")
	assert.NoError(t, err)
	assert.True(t, found)
}

func TestUserExists_NotFound(t *testing.T) {
	found, err := UserExists("no-such-user-rshell-test-marker")
	assert.NoError(t, err)
	assert.False(t, found)
}
