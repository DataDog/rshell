// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package acl decodes and encodes the glibc/libacl binary xattr format used
// to store POSIX ACLs in the system.posix_acl_access and
// system.posix_acl_default extended attributes on Linux.
//
// This package is pure encoding/decoding logic: it never touches the
// filesystem or an xattr syscall itself. It lives under builtins/internal/
// and is therefore exempt from the builtinAllowedSymbols allowlist check,
// but it is deliberately portable (no build tag) since the format itself is
// just a byte layout, not an OS API — only allowedpaths' Sandbox.GetACL /
// Sandbox.SetACL (which do call into the kernel) are Linux-only.
//
// # Format
//
// The xattr value is a 4-byte little-endian version header
// (ACL_EA_VERSION, currently 0x0002) followed by zero or more 8-byte
// entries, each little-endian {tag uint16, perm uint16, id uint32}. Entries
// are conventionally sorted by tag, then by id within ACL_USER/ACL_GROUP
// entries. See acl_ea_entry in <sys/acl.h> / linux's
// include/uapi/linux/acl.h for the canonical struct layout this mirrors.
package acl

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sort"
)

// Tag identifies which kind of ACL entry a given Entry represents.
const (
	TagUserObj  uint16 = 0x01
	TagUser     uint16 = 0x02
	TagGroupObj uint16 = 0x04
	TagGroup    uint16 = 0x08
	TagMask     uint16 = 0x10
	TagOther    uint16 = 0x20
)

// Perm bits, ORed together to form an Entry's Perm field.
const (
	PermRead    uint16 = 0x04
	PermWrite   uint16 = 0x02
	PermExecute uint16 = 0x01
)

// EAVersion is the only version of the acl_ea_entry format this package
// understands (the format has not changed since Linux 2.5.something).
const EAVersion uint32 = 0x0002

// UndefinedID is the ID value used for entries that don't carry a
// user/group ID (ACL_USER_OBJ, ACL_GROUP_OBJ, ACL_MASK, ACL_OTHER).
const UndefinedID uint32 = 0xffffffff

const (
	eaVersionSize = 4
	entrySize     = 8
	// maxEntries bounds decode against a pathological/corrupt xattr value;
	// real ACLs have at most a handful of named-user/group entries.
	maxEntries = 1024
)

// ErrInvalidVersion is returned by DecodeACL when the 4-byte header does not
// match EAVersion.
var ErrInvalidVersion = errors.New("acl: unsupported or invalid ACL version header")

// ErrTruncated is returned by DecodeACL when the input length is not
// eaVersionSize plus a whole number of entrySize-byte entries, or exceeds
// maxEntries.
var ErrTruncated = errors.New("acl: truncated or malformed ACL data")

// Entry is one decoded acl_ea_entry: a tag (which kind of entry), a
// permission bitmask, and (for ACL_USER/ACL_GROUP) the numeric ID it names.
type Entry struct {
	Tag  uint16
	Perm uint16
	ID   uint32
}

// DecodeACL parses the raw bytes of a system.posix_acl_access or
// system.posix_acl_default xattr value into a slice of Entry.
func DecodeACL(data []byte) ([]Entry, error) {
	if len(data) < eaVersionSize {
		return nil, ErrTruncated
	}
	version := binary.LittleEndian.Uint32(data[:eaVersionSize])
	if version != EAVersion {
		return nil, fmt.Errorf("%w: got 0x%08x", ErrInvalidVersion, version)
	}
	rest := data[eaVersionSize:]
	if len(rest)%entrySize != 0 {
		return nil, ErrTruncated
	}
	count := len(rest) / entrySize
	if count > maxEntries {
		return nil, ErrTruncated
	}
	entries := make([]Entry, count)
	for i := 0; i < count; i++ {
		b := rest[i*entrySize : (i+1)*entrySize]
		entries[i] = Entry{
			Tag:  binary.LittleEndian.Uint16(b[0:2]),
			Perm: binary.LittleEndian.Uint16(b[2:4]),
			ID:   binary.LittleEndian.Uint32(b[4:8]),
		}
	}
	return entries, nil
}

// EncodeACL serializes entries back into the raw xattr byte format,
// including the version header. Entries are written in the order given;
// callers that need canonical ordering should sort first (UpsertGroupEntry
// and MinimalACLFromMode already produce canonically-sorted output).
func EncodeACL(entries []Entry) []byte {
	out := make([]byte, eaVersionSize+len(entries)*entrySize)
	binary.LittleEndian.PutUint32(out[:eaVersionSize], EAVersion)
	for i, e := range entries {
		b := out[eaVersionSize+i*entrySize : eaVersionSize+(i+1)*entrySize]
		binary.LittleEndian.PutUint16(b[0:2], e.Tag)
		binary.LittleEndian.PutUint16(b[2:4], e.Perm)
		binary.LittleEndian.PutUint32(b[4:8], e.ID)
	}
	return out
}

// entrySortKey orders entries the way libacl / setfacl canonicalize them:
// by tag first (in the fixed ACL_USER_OBJ..ACL_OTHER order given by the tag
// bit values themselves), then by ID for ACL_USER/ACL_GROUP entries.
func entrySortKey(e Entry) (uint16, uint32) {
	return e.Tag, e.ID
}

func sortEntries(entries []Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		ti, idi := entrySortKey(entries[i])
		tj, idj := entrySortKey(entries[j])
		if ti != tj {
			return ti < tj
		}
		return idi < idj
	})
}

// recomputeMask recomputes (or inserts) the ACL_MASK entry as the union of
// the permission bits of every ACL_USER, ACL_GROUP_OBJ, and ACL_GROUP entry
// — the same rule `setfacl`/libacl applies whenever a named-user or
// named-group entry is present, matching acl_calc_mask(3).
//
// If, after this pass, there is no ACL_USER or ACL_GROUP entry at all, the
// mask entry is removed entirely: a minimal 3-entry ACL (user_obj,
// group_obj, other) has no mask.
func recomputeMask(entries []Entry) []Entry {
	var (
		union    uint16
		hasNamed bool
		filtered = make([]Entry, 0, len(entries))
	)
	for _, e := range entries {
		switch e.Tag {
		case TagMask:
			continue // drop existing mask; recomputed below
		case TagUser, TagGroup:
			hasNamed = true
			union |= e.Perm
			filtered = append(filtered, e)
		case TagGroupObj:
			union |= e.Perm
			filtered = append(filtered, e)
		default:
			filtered = append(filtered, e)
		}
	}
	if !hasNamed {
		// No named entries: mask stays absent (minimal ACL).
		return filtered
	}
	filtered = append(filtered, Entry{Tag: TagMask, Perm: union, ID: UndefinedID})
	return filtered
}

// UpsertGroupEntry returns a new entry slice with an ACL_GROUP entry for
// gid set to perm: replacing an existing ACL_GROUP entry for the same gid
// if one exists, or appending a new one otherwise. The ACL_MASK entry is
// recomputed to cover the result (per acl_calc_mask(3) semantics), and the
// returned slice is canonically sorted.
//
// entries is not modified in place.
func UpsertGroupEntry(entries []Entry, gid uint32, perm uint16) []Entry {
	out := make([]Entry, 0, len(entries)+2)
	replaced := false
	for _, e := range entries {
		if e.Tag == TagGroup && e.ID == gid {
			out = append(out, Entry{Tag: TagGroup, Perm: perm, ID: gid})
			replaced = true
			continue
		}
		out = append(out, e)
	}
	if !replaced {
		out = append(out, Entry{Tag: TagGroup, Perm: perm, ID: gid})
	}
	out = recomputeMask(out)
	sortEntries(out)
	return out
}

// MinimalACLFromMode builds the minimal 3-entry ACL (ACL_USER_OBJ,
// ACL_GROUP_OBJ, ACL_OTHER) equivalent to the traditional rwx permission
// bits in mode. This is the ACL setfacl synthesizes as a starting point for
// a file that has no extended ACL yet (mirroring libacl's
// acl_get_file-returns-minimal-ACL fallback), before UpsertGroupEntry adds
// a named-group entry to it.
func MinimalACLFromMode(mode os.FileMode) []Entry {
	perm := mode.Perm()
	toPerm := func(bits os.FileMode) uint16 {
		var p uint16
		if bits&0o4 != 0 {
			p |= PermRead
		}
		if bits&0o2 != 0 {
			p |= PermWrite
		}
		if bits&0o1 != 0 {
			p |= PermExecute
		}
		return p
	}
	userPerm := toPerm((perm >> 6) & 0o7)
	groupPerm := toPerm((perm >> 3) & 0o7)
	otherPerm := toPerm(perm & 0o7)
	return []Entry{
		{Tag: TagUserObj, Perm: userPerm, ID: UndefinedID},
		{Tag: TagGroupObj, Perm: groupPerm, ID: UndefinedID},
		{Tag: TagOther, Perm: otherPerm, ID: UndefinedID},
	}
}
