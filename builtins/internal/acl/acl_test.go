// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package acl

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinimalACLFromMode(t *testing.T) {
	tests := []struct {
		name string
		mode os.FileMode
		want []Entry
	}{
		{
			name: "0755",
			mode: 0o755,
			want: []Entry{
				{Tag: TagUserObj, Perm: PermRead | PermWrite | PermExecute, ID: UndefinedID},
				{Tag: TagGroupObj, Perm: PermRead | PermExecute, ID: UndefinedID},
				{Tag: TagOther, Perm: PermRead | PermExecute, ID: UndefinedID},
			},
		},
		{
			name: "0640",
			mode: 0o640,
			want: []Entry{
				{Tag: TagUserObj, Perm: PermRead | PermWrite, ID: UndefinedID},
				{Tag: TagGroupObj, Perm: PermRead, ID: UndefinedID},
				{Tag: TagOther, Perm: 0, ID: UndefinedID},
			},
		},
		{
			name: "0000",
			mode: 0o000,
			want: []Entry{
				{Tag: TagUserObj, Perm: 0, ID: UndefinedID},
				{Tag: TagGroupObj, Perm: 0, ID: UndefinedID},
				{Tag: TagOther, Perm: 0, ID: UndefinedID},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MinimalACLFromMode(tc.mode)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	entries := []Entry{
		{Tag: TagUserObj, Perm: PermRead | PermWrite | PermExecute, ID: UndefinedID},
		{Tag: TagGroupObj, Perm: PermRead | PermExecute, ID: UndefinedID},
		{Tag: TagGroup, Perm: PermRead, ID: 998},
		{Tag: TagMask, Perm: PermRead | PermExecute, ID: UndefinedID},
		{Tag: TagOther, Perm: PermRead, ID: UndefinedID},
	}
	data := EncodeACL(entries)
	// version header + 5 entries * 8 bytes
	require.Len(t, data, 4+5*8)

	decoded, err := DecodeACL(data)
	require.NoError(t, err)
	assert.Equal(t, entries, decoded)
}

func TestDecodeACL_EmptyACL(t *testing.T) {
	data := EncodeACL(nil)
	decoded, err := DecodeACL(data)
	require.NoError(t, err)
	assert.Empty(t, decoded)
}

func TestDecodeACL_InvalidVersion(t *testing.T) {
	data := []byte{0x01, 0x00, 0x00, 0x00} // version 0x0001, not 0x0002
	_, err := DecodeACL(data)
	assert.ErrorIs(t, err, ErrInvalidVersion)
}

func TestDecodeACL_TooShortForHeader(t *testing.T) {
	_, err := DecodeACL([]byte{0x02, 0x00})
	assert.ErrorIs(t, err, ErrTruncated)
}

func TestDecodeACL_MisalignedEntries(t *testing.T) {
	data := EncodeACL(MinimalACLFromMode(0o755))
	// Chop off the last 3 bytes of the last entry: no longer a multiple of
	// entrySize past the header.
	truncated := data[:len(data)-3]
	_, err := DecodeACL(truncated)
	assert.ErrorIs(t, err, ErrTruncated)
}

func TestDecodeACL_TooManyEntries(t *testing.T) {
	data := make([]byte, eaVersionSize+(maxEntries+1)*entrySize)
	// version header defaults to zero bytes; set it explicitly.
	data[0] = 0x02
	_, err := DecodeACL(data)
	assert.ErrorIs(t, err, ErrTruncated)
}

func TestUpsertGroupEntry_FromMinimalACL_AddsMaskAndSorts(t *testing.T) {
	minimal := MinimalACLFromMode(0o755)
	got := UpsertGroupEntry(minimal, 998, PermRead|PermExecute)

	want := []Entry{
		{Tag: TagUserObj, Perm: PermRead | PermWrite | PermExecute, ID: UndefinedID},
		{Tag: TagGroupObj, Perm: PermRead | PermExecute, ID: UndefinedID},
		{Tag: TagGroup, Perm: PermRead | PermExecute, ID: 998},
		{Tag: TagMask, Perm: PermRead | PermExecute, ID: UndefinedID},
		{Tag: TagOther, Perm: PermRead | PermExecute, ID: UndefinedID},
	}
	assert.Equal(t, want, got)
}

func TestUpsertGroupEntry_ReplacesExistingEntryForSameGID(t *testing.T) {
	minimal := MinimalACLFromMode(0o750)
	withGroup := UpsertGroupEntry(minimal, 998, PermRead)
	// Now widen the same gid's permission to rwx; the old rw entry must be
	// replaced, not duplicated, and the mask must widen to match.
	got := UpsertGroupEntry(withGroup, 998, PermRead|PermWrite|PermExecute)

	groupEntries := 0
	for _, e := range got {
		if e.Tag == TagGroup && e.ID == 998 {
			groupEntries++
			assert.Equal(t, PermRead|PermWrite|PermExecute, e.Perm)
		}
	}
	assert.Equal(t, 1, groupEntries, "must not duplicate the ACL_GROUP entry for the same gid")

	var mask Entry
	found := false
	for _, e := range got {
		if e.Tag == TagMask {
			mask = e
			found = true
		}
	}
	require.True(t, found)
	assert.Equal(t, PermRead|PermWrite|PermExecute, mask.Perm)
}

func TestUpsertGroupEntry_PreExistingMaskIsRecomputedNotJustKept(t *testing.T) {
	// An ACL that already has a named-group entry and a (now-stale) mask
	// from a prior operation; adding a wider-permission group entry must
	// widen the mask rather than leaving the old value in place.
	existing := []Entry{
		{Tag: TagUserObj, Perm: PermRead | PermWrite | PermExecute, ID: UndefinedID},
		{Tag: TagGroupObj, Perm: PermRead, ID: UndefinedID},
		{Tag: TagGroup, Perm: PermRead, ID: 500},
		{Tag: TagMask, Perm: PermRead, ID: UndefinedID},
		{Tag: TagOther, Perm: 0, ID: UndefinedID},
	}
	got := UpsertGroupEntry(existing, 998, PermRead|PermWrite|PermExecute)

	var mask Entry
	found := false
	for _, e := range got {
		if e.Tag == TagMask {
			mask = e
			found = true
		}
	}
	require.True(t, found)
	// Mask must be the union of group_obj(r) | group:500(r) | group:998(rwx)
	assert.Equal(t, PermRead|PermWrite|PermExecute, mask.Perm)

	// Exactly one mask entry, and canonical tag ordering preserved.
	maskCount := 0
	for _, e := range got {
		if e.Tag == TagMask {
			maskCount++
		}
	}
	assert.Equal(t, 1, maskCount)
	for i := 1; i < len(got); i++ {
		assert.LessOrEqual(t, got[i-1].Tag, got[i].Tag, "entries must be sorted by tag")
	}
}

func TestUpsertGroupEntry_DoesNotMutateInput(t *testing.T) {
	minimal := MinimalACLFromMode(0o755)
	original := append([]Entry(nil), minimal...)
	_ = UpsertGroupEntry(minimal, 998, PermRead)
	assert.Equal(t, original, minimal, "UpsertGroupEntry must not mutate its input slice")
}
