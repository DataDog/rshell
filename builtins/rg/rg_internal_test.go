// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package rg

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRawDisplayJoinUsesBackslashOnlyForBackslashSpelledOperandsOnWindows
// is a regression test: rawDisplayJoin must join a NEWLY inserted
// separator with '\' on Windows ONLY when dir was itself spelled with
// at least one '\' somewhere (e.g. "C:\root"), not merely because the
// process happens to be running on Windows \u2014 every existing test in
// this package asserts a bare '/' for an operand spelled with ordinary
// forward slashes (e.g. "sub", ".", "foo/bar"), on every platform
// INCLUDING Windows, and that must keep working unchanged. Before this
// fix, a directory operand spelled with native Windows separators (e.g.
// "C:\root" or "C:\root\") would produce a malformed, mixed-separator
// display path for every discovered file beneath it (e.g.
// "C:\root/child" or "C:\root\/child") instead of the consistently
// backslash-joined path real ripgrep produces on Windows; an earlier,
// over-broad version of this same fix (defaulting to the platform's
// native separator for EVERY newly-inserted separator, regardless of
// whether dir actually contained a '\') then broke every one of those
// forward-slash-spelled-operand tests on Windows CI, which is exactly
// what this test now pins against regressing again. This test only
// exercises the actual behavior on whichever platform it runs on (this
// repository's CI runs on Windows too); on a non-Windows platform,
// rawDisplayJoin's own runtime.GOOS check always takes the '/'-only
// branch regardless of what separator a test might try to force, so
// there is nothing further to assert there beyond confirming the
// existing (unaffected) forward-slash behavior still works.
func TestRawDisplayJoinUsesBackslashOnlyForBackslashSpelledOperandsOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		assert.Equal(t, "root/child", rawDisplayJoin("root", "child"))
		assert.Equal(t, "root/child", rawDisplayJoin("root/", "child"))
		return
	}

	// A dir spelled with ordinary forward slashes (or none at all) must
	// still get a '/'-joined child, exactly as on every other platform
	// -- the mere fact of running on Windows must NOT change this.
	assert.Equal(t, "sub/child", rawDisplayJoin("sub", "child"))
	assert.Equal(t, "foo/bar/child", rawDisplayJoin("foo/bar", "child"))

	// No existing trailing separator, but dir DOES contain a '\'
	// somewhere: a NEW separator must be '\', not a hardcoded '/', so a
	// '\'-spelled operand's children stay consistently '\'-joined.
	assert.Equal(t, `C:\root\child`, rawDisplayJoin(`C:\root`, "child"))

	// An existing trailing '\' is preserved verbatim, matching
	// rawDisplayJoin's own "no further cleaning applied at any level"
	// rule (see its doc comment) -- no second separator is added.
	assert.Equal(t, `C:\root\child`, rawDisplayJoin(`C:\root\`, "child"))

	// An existing trailing '/' (a forward-slash-spelled operand on
	// Windows, which Windows itself also accepts as a path separator) is
	// likewise preserved verbatim, not converted to '\'.
	assert.Equal(t, "C:/root/child", rawDisplayJoin("C:/root/", "child"))

	// A MIXED-separator dir (one earlier level joined with '\', a later
	// one spelled with '/' in the original operand) still picks '\' for
	// a newly-inserted separator, since dir contains at least one '\'
	// somewhere -- this is the recursive-propagation case: once one
	// level's result contains a '\', every deeper level built from it
	// keeps choosing '\' too.
	assert.Equal(t, `C:\root/sub\child`, rawDisplayJoin(`C:\root/sub`, "child"))
}
