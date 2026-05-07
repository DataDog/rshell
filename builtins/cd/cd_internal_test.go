// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package cd

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// absRoot is an absolute root suitable for tests on the host platform:
// "/" on Unix and `C:\` on Windows.
func absRoot() string {
	if runtime.GOOS == "windows" {
		return `C:\`
	}
	return string(filepath.Separator)
}

// --- rootPrefix ---

func TestRootPrefixUnixRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only path semantics")
	}
	assert.Equal(t, "/", rootPrefix("/"))
	assert.Equal(t, "/", rootPrefix("/a/b"))
	assert.Equal(t, "/", rootPrefix("/a"))
}

func TestRootPrefixRelative(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only path semantics")
	}
	assert.Equal(t, "/", rootPrefix("a/b"))
}

// --- parentDir ---

func TestParentDirUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix-only path semantics")
	}
	assert.Equal(t, "/", parentDir("/"), "root has no parent — stays at root")
	assert.Equal(t, "/", parentDir("/a"))
	assert.Equal(t, "/a", parentDir("/a/b"))
	assert.Equal(t, "/a/b", parentDir("/a/b/c"))
}

// --- joinPath ---

func TestJoinPathHandlesTrailingSeparator(t *testing.T) {
	root := absRoot()
	assert.Equal(t, root+"a", joinPath(root, "a"), "no double separator after root")
	assert.Equal(t, filepath.Join(root, "a")+string(filepath.Separator)+"b",
		joinPath(filepath.Join(root, "a"), "b"))
}

func TestJoinPathEmptyDir(t *testing.T) {
	assert.Equal(t, "comp", joinPath("", "comp"))
}

// --- boolSeqFlag ---

func TestBoolSeqFlagTypeIsBool(t *testing.T) {
	seq := 0
	f := newBoolSeqFlag(&seq)
	assert.Equal(t, "bool", f.Type())
}

func TestBoolSeqFlagStringIsFalse(t *testing.T) {
	seq := 0
	f := newBoolSeqFlag(&seq)
	assert.Equal(t, "false", f.String())
}

func TestBoolSeqFlagSetSentinelOnly(t *testing.T) {
	seq := 0
	f := newBoolSeqFlag(&seq)
	assert.NoError(t, f.Set(boolSeqSentinel))
	assert.Equal(t, 1, f.pos)

	// A second value increments.
	g := newBoolSeqFlag(&seq)
	assert.NoError(t, g.Set(boolSeqSentinel))
	assert.Equal(t, 2, g.pos)
}

func TestBoolSeqFlagRejectsExplicitValue(t *testing.T) {
	seq := 0
	f := newBoolSeqFlag(&seq)
	err := f.Set("true")
	assert.Error(t, err)
	err = f.Set("false")
	assert.Error(t, err)
	err = f.Set("")
	assert.Error(t, err)
}
