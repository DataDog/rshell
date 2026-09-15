// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package etcgroup

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const sampleGroupFile = `root:x:0:
daemon:x:1:
dd-agent:x:998:datadog-agent
docker:x:999:alice,bob
# a comment line

malformed-line-no-colons
`

func TestParseGroupFile_HappyPath(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "dd-agent")
	assert.NoError(t, err)
	assert.Equal(t, uint32(998), gid)
}

func TestParseGroupFile_RootGroup(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "root")
	assert.NoError(t, err)
	assert.Equal(t, uint32(0), gid)
}

func TestParseGroupFile_NotFound(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader(sampleGroupFile), "nosuchgroup")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_SkipsCommentsAndBlankAndMalformedLines(t *testing.T) {
	gid, err := parseGroupFile(strings.NewReader(sampleGroupFile), "docker")
	assert.NoError(t, err)
	assert.Equal(t, uint32(999), gid)
}

func TestParseGroupFile_EmptyInput(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader(""), "root")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_NonNumericGID(t *testing.T) {
	input := "broken:x:notanumber:\nreal:x:42:\n"
	gid, err := parseGroupFile(strings.NewReader(input), "broken")
	assert.ErrorIs(t, err, ErrGroupNotFound, "a malformed GID field is skipped, not fatal")
	gid, err = parseGroupFile(strings.NewReader(input), "real")
	assert.NoError(t, err)
	assert.Equal(t, uint32(42), gid)
}

func TestParseGroupFile_MissingGIDField(t *testing.T) {
	_, err := parseGroupFile(strings.NewReader("nogid:x\n"), "nogid")
	assert.ErrorIs(t, err, ErrGroupNotFound)
}

func TestParseGroupFile_LineTooLong(t *testing.T) {
	huge := "hugegroup:x:1:" + strings.Repeat("a,", maxGroupLine*2) + "\n"
	_, err := parseGroupFile(strings.NewReader(huge), "hugegroup")
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrGroupNotFound))
}

func TestLookupGID_NotSupportedNeverOnLinux(t *testing.T) {
	// Sanity check that the Linux backend is actually wired (i.e. this test
	// file is compiled): looking up the well-known "root" group against the
	// real /etc/group must succeed with GID 0 on every Linux host.
	gid, err := LookupGID("root")
	assert.NoError(t, err)
	assert.Equal(t, uint32(0), gid)
}
