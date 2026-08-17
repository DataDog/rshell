// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ntfsmft computes disk usage for a target directory on an NTFS volume
// by reading the raw $MFT.
//
// The volume I/O and scan orchestration are Windows-only (see du_windows.go);
// this file holds the pure $MFT record/attribute parser, which has no
// platform dependencies (stdlib only) so it can be unit-tested and fuzzed on
// any OS.
//
// Scan pipeline (see Scan in du_windows.go for section markers):
//
//   - Setup: open \\.\<drive>:, resolve the target and its immediate children
//     (resolveScopeIndices) to MFT indices via the Windows API (CreateFile,
//     GetFileInformationByHandle, FindFirstFile). Exclusion paths are resolved
//     to indices before the MFT walks so out-of-scope subtrees short-circuit
//     cheaply.
//
//   - Pass 1 (modeAll, one full MFT stream): build dirParent (directory →
//     parent idx), plus extSize and extParents per file base. Extension records
//     are folded into this pass so their $DATA sizes and spillover $FILE_NAME
//     parents are not rescanned. The bulk walk does not decode UTF-16 names.
//
//   - Map dirs to size accumulators (mapDirsToSizeAccumulators): assign each
//     directory to the running total pass 2 accumulates its subtree bytes into.
//     TreeDepth <= 1 (fast path) precomputes dirBucket via walkUp from target
//     and its immediate children so pass 2 attributes a file in O(1); TreeDepth
//     >= 2 (general path) retains dirParent and the in-tree anchor totals for
//     per-file chain walks in pass 2.
//
//   - Pass 2 (modeFileBaseOnly, or modeAll when TreeDepth >= 2): tally in-use
//     file base records into per-child / subtree totals; optional top-N files,
//     extension aggregation, and find predicates run inline in this callback.
//     The general path opportunistically decodes names only for dirs at
//     depth ≤ TreeDepth.
//
//   - Post-scan: assemble the optional Result.Tree; resolve top-file paths via
//     OpenFileByID (bounded, not part of the MFT stream).
//
//   - Pipelined ReadFile (double-buffered) overlaps disk I/O with parsing.
//
//   - parseMode header-only early exit skips the attribute walk on records a
//     pass cannot use (see modeAll / modeFileBaseOnly below).
//
//   - No per-file info map: pass 2 unions base + extension parents and adds
//     directly into totals. No per-file slice allocation on the hot path.
//
// Requires Administrator privileges (\\.\C: open).
package ntfsmft

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// -------------------------------------------------------------------------
// MFT record / attribute constants
// -------------------------------------------------------------------------

const (
	mftSignature = 0x454C4946 // "FILE" little-endian

	attrStandardInfo  = 0x10
	attrAttributeList = 0x20
	attrFileName      = 0x30
	attrData          = 0x80
	attrEndMarker     = 0xFFFFFFFF

	// ATTRIBUTE_RECORD_HEADER field offsets, relative to the start of the
	// attribute record. Verified field-by-field against the documented struct:
	// https://learn.microsoft.com/en-us/windows/win32/devnotes/attribute-record-header
	attrOffFormCode     = 0x08 // UCHAR  FormCode
	attrOffNameLength   = 0x09 // UCHAR  NameLength (UTF-16 chars; 0 = unnamed)
	attrOffFlags        = 0x0C // USHORT Flags
	attrOffMappingPairs = 0x20 // USHORT MappingPairsOffset (non-resident form)

	attrFormNonresident = 0x01 // NONRESIDENT_FORM (RESIDENT_FORM is 0x00)

	// attrNonresidentHeaderLen is the smallest valid non-resident attribute
	// header: through ValidDataLength at 0x38. TotalAllocated at 0x40 follows
	// only for compressed/sparse attributes. The mapping pairs array therefore
	// can never begin before this offset.
	attrNonresidentHeaderLen = 0x40

	// attrFlagsUnsupportedForMFT are the documented Flags bits that invalidate a
	// plain runlist-to-extent reading: ATTRIBUTE_FLAG_COMPRESSION_MASK (0x00FF),
	// ATTRIBUTE_FLAG_ENCRYPTED (0x4000) and ATTRIBUTE_FLAG_SPARSE (0x8000). A
	// compressed attribute's runs describe compression units and a sparse one has
	// holes, so neither maps directly onto "read these bytes as MFT records".
	attrFlagsUnsupportedForMFT = 0x00FF | 0x4000 | 0x8000

	flagInUse     = 0x01
	flagDirectory = 0x02

	nsPosix       = 0x00
	nsWin32       = 0x01
	nsDOS         = 0x02
	nsWin32AndDOS = 0x03

	// Records 0–15 are NTFS metafiles ($MFT, $MFTMirr, $LogFile, $Volume,
	// $AttrDef, root, $Bitmap, $Boot, $BadClus, $Secure, $UpCase, $Extend,
	// reserved 12–15). Real user-actionable system files (pagefile.sys,
	// hiberfil.sys, swapfile.sys) have idx >= 16.
	maxMetafileMFTIndex = 15

	// Root directory MFT index (always 5 on NTFS).
	rootDirMFTIndex = 5
)

// errBadSignature indicates the record does not start with "FILE".
var errBadSignature = errors.New("bad MFT signature")

// MFTIndex masks the lower 48 bits of an MFT file reference. The upper 16 are
// the sequence number; we don't need them for disk-usage tally because we
// always cross-reference by record index, not sequence-stamped reference.
func MFTIndex(ref uint64) uint64 {
	return ref & 0x0000FFFFFFFFFFFF
}

// -------------------------------------------------------------------------
// Parsed MFT entry — caller-buffer reuse via parseInto
// -------------------------------------------------------------------------

// hardlinkParents are the parent MFT indices from the $FILE_NAME attributes of
// a single record. A file with N hardlinks contributes N entries (one per
// $FILE_NAME, except DOS-only 8.3 aliases which we drop). For a directory or
// a single-link file this typically has 1 entry.
//
// The slice's backing array is reused across records via parseInto.

// mftEntry is the result of parsing a single MFT record. parseInto resets the
// fields on entry but preserves hardlinkParents' backing array, so the only
// allocations across many records are when hardlinkParents grows.
type mftEntry struct {
	// hardlinkParents is the list of parent MFT indices from non-DOS
	// $FILE_NAME attributes on this record. Used by the scan to attribute
	// hard-linked files to multiple buckets.
	hardlinkParents []uint64

	// primaryParent is the parent MFT index of the highest-namespace-priority
	// $FILE_NAME on this record. Falls back to hardlinkParents[0] if needed.
	primaryParent uint64

	// nameBytes is a slice into the record buffer of the raw UTF-16 little-
	// endian name from the highest-priority $FILE_NAME. Valid ONLY for the
	// duration of the streamPipelined callback that produced this entry; the
	// underlying buffer is reused on the next chunk. Callers needing the name
	// past the callback must copy it out.
	nameBytes []byte

	// sequence is the MFT record sequence number (header offset 0x10).
	// Combined with the record idx it forms the 64-bit NTFS file reference
	// used by OpenFileByID for post-scan path resolution.
	sequence uint16

	// allocatedSize / dataSize come from $DATA. fnAllocSize / fnDataSize are
	// the cached sizes from the highest-priority $FILE_NAME, used as a
	// fallback when $DATA sizes are missing (typically when $DATA is in an
	// extension record — covered by pass 2).
	allocatedSize int64
	dataSize      int64
	fnAllocSize   int64
	fnDataSize    int64

	isInUse      bool
	isDir        bool
	isSparse     bool
	isCompressed bool
}

// -------------------------------------------------------------------------
// Parser modes
// -------------------------------------------------------------------------

// parseMode lets each pass skip records it cannot use after a 0x28-byte
// header read, before the attribute walk. Header-only early exit saves
// ~25–30% wall on the file-tally pass.
type parseMode uint8

const (
	// modeAll: parse every in-use record fully. The map-building pass uses
	// this so it can see directory base records AND extension records (for
	// dir-name spillover reconciliation, $DATA size accumulation per base,
	// and cross-bucket hardlink parents from extension $FILE_NAMEs).
	modeAll parseMode = iota

	// modeFileBaseOnly: skip records with baseRef != 0 OR isDir. The
	// file-tally pass uses this. The parent-only $FILE_NAME walk still runs
	// on bases so callers can attribute hardlinks across buckets.
	modeFileBaseOnly
)

// -------------------------------------------------------------------------
// Top-level parse
// -------------------------------------------------------------------------

// parseInto parses one MFT record into the caller-provided *mftEntry,
// preserving hardlinkParents' backing array.
//
// Returns (baseRef, error). baseRef is 0 for base records and non-zero (the
// MFT index of the file the extension belongs to) for extension records.
//
// For mode != modeAll, the function may return early after the header read
// without populating any attribute-derived fields. Callers must check the
// pass-specific predicates again before use; e.g. pass 2's callback re-checks
// isDir even though modeFileBaseOnly already filtered it, because dir flag
// is a header bit set early. (In practice it's a single-bit re-check.)
func parseInto(record []byte, recordSize int, entry *mftEntry, mode parseMode) (uint64, error) {
	// Reset fields, preserve hardlinks backing array.
	hl := entry.hardlinkParents[:0]
	*entry = mftEntry{hardlinkParents: hl}

	if len(record) < recordSize {
		return 0, errBadSignature
	}
	if binary.LittleEndian.Uint32(record[0:4]) != mftSignature {
		return 0, errBadSignature
	}
	if err := applyFixups(record, recordSize); err != nil {
		return 0, err
	}

	flags := binary.LittleEndian.Uint16(record[0x16:0x18])
	firstAttrOffset := binary.LittleEndian.Uint16(record[0x14:0x16])
	entry.sequence = binary.LittleEndian.Uint16(record[0x10:0x12])

	// base_record_file_reference at offset 0x20. Non-zero = extension record.
	var baseRef uint64
	if recordSize >= 0x28 {
		baseRef = MFTIndex(binary.LittleEndian.Uint64(record[0x20:0x28]))
	}

	entry.isInUse = flags&flagInUse != 0
	entry.isDir = flags&flagDirectory != 0
	if !entry.isInUse {
		return baseRef, nil
	}

	// Pass-mode early-exit: skip the attribute walk for records the file-
	// tally pass cannot use. Most records are extensions or directories and
	// get skipped here.
	if mode == modeFileBaseOnly {
		if baseRef != 0 || entry.isDir {
			return baseRef, nil
		}
	}

	// Walk the attribute chain.
	bestNS := -1
	offset := int(firstAttrOffset)
	for offset+8 <= recordSize {
		attrType := binary.LittleEndian.Uint32(record[offset : offset+4])
		if attrType == attrEndMarker || attrType == 0 {
			break
		}
		attrLen := int(binary.LittleEndian.Uint32(record[offset+4 : offset+8]))
		if attrLen < 16 || offset+attrLen > recordSize {
			break
		}

		switch attrType {
		case attrFileName:
			if err := parseFileNameParents(record[offset:offset+attrLen], entry, &bestNS); err != nil {
				return 0, err
			}
		case attrData:
			nonResident := record[offset+8]
			if nonResident == 1 {
				if err := parseNonResidentData(record[offset:offset+attrLen], entry); err != nil {
					return 0, err
				}
			} else {
				parseResidentData(record[offset:offset+attrLen], entry)
			}
		}

		offset += attrLen
	}

	return baseRef, nil
}

// -------------------------------------------------------------------------
// Attribute parsers
// -------------------------------------------------------------------------

// parseFileNameParents extracts only what the scan needs from $FILE_NAME:
// parent MFT idx (always), and sizes from the highest-priority namespace
// (used as fallback when $DATA is missing). The UTF-16 name is NEVER
// decoded — see package-level doc.
//
// DOS-only ($FILE_NAME with namespace == nsDOS) is the 8.3 alias of an
// existing Win32 entry; we drop it to avoid double-counting parents.
func parseFileNameParents(attr []byte, entry *mftEntry, bestNS *int) error {
	// parseInto only guarantees attrLen >= 16 before dispatching here, but the
	// resident-header reads below reach attr[0x14:0x16]. When a crafted
	// attribute sits within 22 bytes of the record end its capacity drops to
	// attrLen and the read panics. Guard like parseResidentData/NonResidentData.
	if len(attr) < 0x18 {
		return nil
	}
	contentOffset := int(binary.LittleEndian.Uint16(attr[0x14:0x16]))
	contentLen := int(binary.LittleEndian.Uint32(attr[0x10:0x14]))
	if contentOffset+contentLen > len(attr) || contentLen < 0x42 {
		return nil
	}
	c := attr[contentOffset : contentOffset+contentLen]

	parentRef := MFTIndex(binary.LittleEndian.Uint64(c[0x00:0x08]))
	allocSize, ok := safeSize(binary.LittleEndian.Uint64(c[0x28:0x30]))
	if !ok {
		return errBadSize
	}
	realSize, ok := safeSize(binary.LittleEndian.Uint64(c[0x30:0x38]))
	if !ok {
		return errBadSize
	}
	namespace := int(c[0x41])

	if namespace == nsDOS {
		return nil
	}

	entry.hardlinkParents = append(entry.hardlinkParents, parentRef)

	pri := nsPriority(namespace)
	if pri > *bestNS {
		*bestNS = pri
		entry.primaryParent = parentRef
		entry.fnAllocSize = allocSize
		entry.fnDataSize = realSize
		// Capture the raw UTF-16 name bytes for callers that need basename
		// or extension. Slice points into the record buffer — valid for the
		// callback duration only.
		nameLen := int(c[0x40])
		if 0x42+nameLen*2 <= len(c) {
			entry.nameBytes = c[0x42 : 0x42+nameLen*2]
		}
	}
	return nil
}

func nsPriority(ns int) int {
	switch ns {
	case nsWin32AndDOS:
		return 4
	case nsWin32:
		return 3
	case nsPosix:
		return 2
	default:
		return 0
	}
}

// parseResidentData: $DATA is small enough to live inside the MFT record.
//
// A resident stream allocates zero clusters, so Windows' "size on disk"
// (GetCompressedFileSizeW / Explorer) reports 0 for a tiny resident file — the
// classic ~700-byte crossover where $DATA spills out to clusters. See the
// resident/non-resident attribute concept in the NTFS on-disk format docs:
// https://flatcap.github.io/linux-ntfs/ntfs/concepts/attribute_header.html
//
// We nonetheless add the content length to allocatedSize (not just dataSize):
// the bytes physically occupy the $MFT's own on-disk allocation, and the scan
// skips the $MFT metafile itself, so attributing each resident stream to its
// owning file is what keeps whole-volume totals from dropping those bytes on
// the floor. Counting them per-file also matches what a raw GetCompressedFileSizeW
// probe returns for resident data on a real volume (content length, not 0).
//
// A single MFT record can hold multiple $DATA attributes when the file has
// alternate data streams (e.g. the unnamed main stream + a Zone.Identifier
// ADS on a downloaded file). Each is its own $DATA attribute. We accumulate
// across them so the reported size matches the file's true on-disk usage.
func parseResidentData(attr []byte, entry *mftEntry) {
	if len(attr) < 0x18 {
		return
	}
	contentLen := int64(binary.LittleEndian.Uint32(attr[0x10:0x14]))
	entry.dataSize = saturatingAdd(entry.dataSize, contentLen)
	entry.allocatedSize = saturatingAdd(entry.allocatedSize, contentLen)
}

// parseNonResidentData: $DATA is in cluster runs on disk. AllocatedLength /
// FileSize are valid only when LowestVcn == 0 (per MS spec). Continuation
// fragments must be ignored; the base $DATA's sizes are authoritative.
//
// For sparse or compressed files, offset 0x40 ("Total allocated size") gives
// the actual on-disk allocation accounting for sparse holes / compression.
// Offset 0x28 is the VIRTUAL allocation including holes — useful for apparent
// size reporting but wrong for "size on disk" mode.
//
// Multiple $DATA attributes (alternate data streams) on the same record each
// contribute their own first-fragment sizes. We accumulate; sparse/compressed
// flags are sticky (any-stream).
func parseNonResidentData(attr []byte, entry *mftEntry) error {
	if len(attr) < 0x40 {
		return nil
	}
	dataFlags := binary.LittleEndian.Uint16(attr[0x0C:0x0E])
	isCompressed := dataFlags&0x0001 != 0
	isSparse := dataFlags&0x8000 != 0

	lowestVcn := binary.LittleEndian.Uint64(attr[0x10:0x18])
	if lowestVcn != 0 {
		return nil // continuation run — sizes are invalid
	}

	dataSize, ok := safeSize(binary.LittleEndian.Uint64(attr[0x30:0x38]))
	if !ok {
		return errBadSize
	}
	var allocSize int64
	if (isSparse || isCompressed) && len(attr) >= 0x48 {
		allocSize, ok = safeSize(binary.LittleEndian.Uint64(attr[0x40:0x48]))
	} else {
		allocSize, ok = safeSize(binary.LittleEndian.Uint64(attr[0x28:0x30]))
	}
	if !ok {
		return errBadSize
	}

	entry.dataSize = saturatingAdd(entry.dataSize, dataSize)
	entry.allocatedSize = saturatingAdd(entry.allocatedSize, allocSize)
	if isSparse {
		entry.isSparse = true
	}
	if isCompressed {
		entry.isCompressed = true
	}
	return nil
}

// -------------------------------------------------------------------------
// Multi-sector transfer protection (fixups)
// -------------------------------------------------------------------------

// errTornWrite is returned by applyFixups when a sector-end USN does not
// match the header USN, indicating the write that produced this record
// did not complete atomically. The record's content is in an
// indeterminate state and must not be parsed.
var errTornWrite = errors.New("torn write detected (USN mismatch)")

// errBadFixup is returned by applyFixups when the fixup (update sequence
// array) descriptor is malformed: fewer than 2 words, or an offset/count
// that places the array outside the record. Such a record cannot be
// validated or restored, so it must be rejected rather than parsed with
// unrestored (USN-corrupted) sector ends.
var errBadFixup = errors.New("malformed fixup descriptor")

// errBadSize is returned when a raw NTFS size field has its high bit set
// (value > math.MaxInt64). No real NTFS volume can hold a file or allocation
// that large (the maximum file size is far below 8 EiB), so such a value only
// occurs on a corrupted or crafted image. Casting it to int64 would wrap to a
// negative number and corrupt the scan totals, so the record is rejected.
var errBadSize = errors.New("size field exceeds int64 range")

// safeSize converts a raw uint64 NTFS size field to int64, reporting ok=false
// when the value would wrap negative (high bit set). See errBadSize.
func safeSize(raw uint64) (int64, bool) {
	if raw > math.MaxInt64 {
		return 0, false
	}
	return int64(raw), true
}

// saturatingAdd adds two non-negative int64 size values, clamping to MaxInt64
// instead of wrapping negative. safeSize rejects any single field with its high
// bit set, but summing several near-MaxInt64 fields (e.g. multiple $DATA streams
// on one record, or many records on a crafted image) would still overflow the
// accumulators and corrupt the totals. Both operands are always >= 0 here.
func saturatingAdd(a, b int64) int64 {
	if b > math.MaxInt64-a {
		return math.MaxInt64
	}
	return a + b
}

// applyFixups validates the multi-sector transfer protection on an MFT
// record and restores the original sector-end bytes in place.
//
// NTFS writes records sector-by-sector. At write time it places a USN at
// the last 2 bytes of every 512-byte sector (overwriting the real
// content) and stashes the original bytes in the update sequence array
// at the start of the record. On read we must:
//
//  1. Validate that every sector-end still equals the USN. A mismatch
//     means the write was torn (process / power interrupted mid-write)
//     and the sector contents are unreliable.
//  2. Restore the original bytes from the USA back to the sector ends,
//     so the parser sees the record's real content rather than the USN.
//
// Without step 2 the parser reads USN garbage at every 512-byte boundary;
// without step 1 we silently parse a partially-written record. Matches
// what the in-kernel NTFS driver does at the file API layer.
func applyFixups(record []byte, recordSize int) error {
	fixupOffset := binary.LittleEndian.Uint16(record[4:6])
	fixupCount := binary.LittleEndian.Uint16(record[6:8])
	// The update sequence array holds one USN plus one saved word per 512-byte
	// sector, so a valid record declares exactly recordSize/512 + 1 entries.
	// recordSize is a validated multiple of 512 (see validateNTFSLayout). A
	// smaller count (e.g. 2 on a 1024-byte record) would leave later sectors'
	// USN-corrupted end bytes unvalidated and unrestored, feeding torn-write
	// garbage to the attribute parser — so reject anything but the exact count.
	expected := recordSize/512 + 1
	if int(fixupCount) != expected || int(fixupOffset)+int(fixupCount)*2 > recordSize {
		return errBadFixup
	}

	// First word of the USA is the USN; the remaining words are the saved
	// original bytes for each sector.
	usn0 := record[int(fixupOffset)]
	usn1 := record[int(fixupOffset)+1]

	// Pass 1: USN validation. Walk every sector and confirm its trailing
	// 2 bytes still equal the header USN. If any sector fails, the write
	// did not complete atomically and we must reject the record before
	// touching its content.
	for i := uint16(1); i < fixupCount; i++ {
		sectorEnd := int(i)*512 - 2
		if sectorEnd+2 > recordSize {
			break
		}
		if record[sectorEnd] != usn0 || record[sectorEnd+1] != usn1 {
			return errTornWrite
		}
	}

	// Pass 2: restore. Copy the saved bytes from the USA back to each
	// sector end. Cannot be folded into pass 1 because step 1 must
	// complete (verify all sectors are intact) before we mutate anything.
	for i := uint16(1); i < fixupCount; i++ {
		sectorEnd := int(i)*512 - 2
		if sectorEnd+2 > recordSize {
			break
		}
		fvOff := int(fixupOffset) + int(i)*2
		if fvOff+2 > recordSize {
			break
		}
		record[sectorEnd] = record[fvOff]
		record[sectorEnd+1] = record[fvOff+1]
	}
	return nil
}

// -------------------------------------------------------------------------
// MFT extents (record 0 + $ATTRIBUTE_LIST chasing)
// -------------------------------------------------------------------------
//
// This block resolves the on-disk byte ranges of the $MFT itself. The only
// platform coupling is disk I/O, injected via the readerAt seam, so the byte
// parsing here is pure and cross-platform (unit-tested and fuzzed on any OS).

// volumeInfo carries the volume geometry needed to locate and parse the $MFT.
type volumeInfo struct {
	recordSize      int
	bytesPerCluster int64
	mftStartByte    int64
	mftValidBytes   int64
}

// extent is one contiguous on-disk byte range of the $MFT.
type extent struct {
	byteOffset int64
	byteLength int64
}

// readerAt reads len(buf) bytes from the volume at an absolute byte offset.
// On Windows it wraps ReadFile (see du_windows.go); tests inject an in-memory
// reader so getMFTExtents and its helpers run on any platform.
type readerAt func(buf []byte, offset int64) error

// maxAttrListBytes caps how much of a non-resident $ATTRIBUTE_LIST we will
// read into memory (RULES.md memory-safety: never allocate from untrusted
// on-disk sizes without a bound). Real MFT attribute lists are a few KiB even
// on heavily fragmented volumes; 4 MiB is far above any legitimate size.
const maxAttrListBytes = 4 << 20

// getMFTExtents reads MFT record 0, decodes its $DATA data runs, and chases
// $ATTRIBUTE_LIST entries to find extension records that hold additional
// $DATA runs (the $MFT itself is typically heavily fragmented).
//
// Record 0 is self-bootstrapping: whether its $ATTRIBUTE_LIST is resident or
// non-resident, the data runs needed to locate everything else fit within
// record 0. A non-resident $ATTRIBUTE_LIST is rare (it only happens on badly
// fragmented / near-full volumes — exactly this tool's target), so we follow
// its runlist by raw LCN directly (no MFT indirection) to read its content.
func getMFTExtents(read readerAt, vol *volumeInfo) ([]extent, error) {
	rec0 := make([]byte, vol.recordSize)
	if err := read(rec0, vol.mftStartByte); err != nil {
		return nil, fmt.Errorf("read record 0: %w", err)
	}
	if binary.LittleEndian.Uint32(rec0[0:4]) != mftSignature {
		return nil, errors.New("record 0 bad signature")
	}
	if err := applyFixups(rec0, vol.recordSize); err != nil {
		return nil, fmt.Errorf("record 0: %w", err)
	}

	firstAttrOff := int(binary.LittleEndian.Uint16(rec0[0x14:0x16]))
	var inline []extent
	var attrList []attrListEntry

	for off := firstAttrOff; off+8 <= vol.recordSize; {
		t := binary.LittleEndian.Uint32(rec0[off : off+4])
		if t == attrEndMarker || t == 0 {
			break
		}
		al := int(binary.LittleEndian.Uint32(rec0[off+4 : off+8]))
		if al < 16 || off+al > vol.recordSize {
			break
		}
		switch t {
		case attrData:
			if runs, ok := mftDataRuns(rec0[off : off+al]); ok {
				inline = decodeDataRuns(runs, vol.bytesPerCluster)
			}
		case attrAttributeList:
			if rec0[off+8] == 1 {
				// Non-resident $ATTRIBUTE_LIST: the entries live in disk
				// clusters, but the runlist locating them is inline in record 0
				// (raw LCNs, no MFT indirection), so we can read it directly.
				content, err := readNonResidentAttrList(read, rec0[off:off+al], vol.bytesPerCluster)
				if err != nil {
					return nil, fmt.Errorf("record 0 $ATTRIBUTE_LIST: %w", err)
				}
				attrList = parseAttributeListEntries(content)
			} else {
				attrList = parseAttributeList(rec0[off : off+al])
			}
		}
		off += al
	}

	if len(attrList) == 0 {
		if len(inline) == 0 {
			return nil, errors.New("no $DATA in record 0")
		}
		return inline, nil
	}

	all := append([]extent(nil), inline...)
	readByMFTIdx := func(mftIdx uint64) ([]byte, error) {
		bo := int64(mftIdx) * int64(vol.recordSize)
		var cum int64
		for _, ex := range all {
			if bo < cum+ex.byteLength {
				disk := ex.byteOffset + (bo - cum)
				buf := make([]byte, vol.recordSize)
				if err := read(buf, disk); err != nil {
					return nil, err
				}
				return buf, nil
			}
			cum += ex.byteLength
		}
		return nil, fmt.Errorf("MFT idx %d not in known extents", mftIdx)
	}

	seen := map[uint64]bool{0: true}
	for _, e := range attrList {
		if e.attrType != attrData || seen[e.mftRef] {
			continue
		}
		seen[e.mftRef] = true
		extRec, err := readByMFTIdx(e.mftRef)
		if err != nil {
			// Cannot locate or read this $MFT extension record. A fragmented MFT
			// can legitimately reference an extension not yet locatable in
			// processing order, so this is best-effort: skip it and return the
			// extents we have rather than failing the whole scan. Some MFT
			// extents may be missing, matching the torn/malformed skip below.
			continue
		}
		if binary.LittleEndian.Uint32(extRec[0:4]) != mftSignature {
			continue
		}
		if err := applyFixups(extRec, vol.recordSize); err != nil {
			// Torn-write or malformed extension record — skip it.
			// Some MFT extents may be missing from our list, but a wrong
			// extent list is preferable to acting on corrupt bytes.
			continue
		}

		efa := int(binary.LittleEndian.Uint16(extRec[0x14:0x16]))
		for off := efa; off+8 <= vol.recordSize; {
			t := binary.LittleEndian.Uint32(extRec[off : off+4])
			if t == attrEndMarker || t == 0 {
				break
			}
			al := int(binary.LittleEndian.Uint32(extRec[off+4 : off+8]))
			if al < 16 || off+al > vol.recordSize {
				break
			}
			if t == attrData {
				if runs, ok := mftDataRuns(extRec[off : off+al]); ok {
					all = append(all, decodeDataRuns(runs, vol.bytesPerCluster)...)
				}
			}
			off += al
		}
	}

	return all, nil
}

// mftDataRuns returns the mapping-pairs (data run) bytes of attr when attr is
// the unnamed, non-resident, uncompressed $DATA attribute that describes $MFT's
// own clusters. It reports ok=false for every other flavour of $DATA, which the
// caller must then ignore rather than guess at.
//
// Strictness matters here because the caller feeds these runs straight into
// decodeDataRuns and then reads the resulting disk offsets as MFT records, so a
// wrong runlist silently redefines where the whole scan believes $MFT lives.
// Matching on the type code alone is not enough:
//
//   - NameLength must be 0. It is "the size of the optional attribute name, in
//     characters, or 0 if there is no attribute name", so a nonzero value marks a
//     NAMED $DATA attribute — an alternate data stream with its own unrelated
//     extent chain. Splicing an ADS's clusters into the $MFT extent list would
//     make the scan parse foreign bytes as MFT records.
//   - FormCode must be NONRESIDENT_FORM; a resident $DATA holds its value inline
//     and describes no clusters at all.
//   - Flags must carry none of the compression/sparse/encrypted bits, whose run
//     semantics differ (see attrFlagsUnsupportedForMFT). $MFT itself is never
//     compressed, sparse or encrypted, so any of these indicates corruption.
//   - MappingPairsOffset must land past the header and inside the record, else we
//     would decode the header's own fields (or out-of-record bytes) as run data.
//
// Field offsets and semantics per ATTRIBUTE_RECORD_HEADER:
// https://learn.microsoft.com/en-us/windows/win32/devnotes/attribute-record-header
func mftDataRuns(attr []byte) ([]byte, bool) {
	if len(attr) < attrNonresidentHeaderLen {
		return nil, false
	}
	if attr[attrOffFormCode] != attrFormNonresident {
		return nil, false
	}
	if attr[attrOffNameLength] != 0 {
		return nil, false
	}
	flags := binary.LittleEndian.Uint16(attr[attrOffFlags : attrOffFlags+2])
	if flags&attrFlagsUnsupportedForMFT != 0 {
		return nil, false
	}
	drOff := int(binary.LittleEndian.Uint16(attr[attrOffMappingPairs : attrOffMappingPairs+2]))
	if drOff < attrNonresidentHeaderLen || drOff > len(attr) {
		return nil, false
	}
	return attr[drOff:], true
}

// readNonResidentAttrList reads the content of a non-resident $ATTRIBUTE_LIST
// attribute by following its inline data runs directly by LCN. It applies a
// fixed size bound and validates the runlist before allocating.
func readNonResidentAttrList(read readerAt, attr []byte, bytesPerCluster int64) ([]byte, error) {
	// Non-resident attribute header: MappingPairsOffset at 0x20 (2 bytes),
	// real data size at 0x30 (8 bytes); the header is at least 0x40 bytes.
	if len(attr) < 0x38 {
		return nil, errors.New("attribute too short for non-resident header")
	}
	drOff := int(binary.LittleEndian.Uint16(attr[0x20:0x22]))
	dataSize := int64(binary.LittleEndian.Uint64(attr[0x30:0x38]))
	if drOff < 0x40 || drOff > len(attr) {
		return nil, fmt.Errorf("bad data-run offset %d", drOff)
	}
	if dataSize <= 0 || dataSize > maxAttrListBytes {
		return nil, fmt.Errorf("content size %d out of range", dataSize)
	}
	runs := decodeDataRuns(attr[drOff:], bytesPerCluster)
	if len(runs) == 0 {
		return nil, errors.New("no data runs")
	}
	var allocated int64
	for _, r := range runs {
		if r.byteLength <= 0 {
			return nil, errors.New("bad run length")
		}
		allocated += r.byteLength
		if allocated > maxAttrListBytes {
			return nil, fmt.Errorf("allocated %d exceeds %d", allocated, maxAttrListBytes)
		}
	}
	if allocated < dataSize {
		return nil, fmt.Errorf("runs cover %d < content size %d", allocated, dataSize)
	}
	buf := make([]byte, allocated)
	pos := 0
	for _, r := range runs {
		if err := read(buf[pos:pos+int(r.byteLength)], r.byteOffset); err != nil {
			return nil, err
		}
		pos += int(r.byteLength)
	}
	return buf[:dataSize], nil
}

// decodeDataRuns decodes an NTFS data run list into disk extents.
func decodeDataRuns(data []byte, bytesPerCluster int64) []extent {
	var ext []extent
	var lcn int64
	pos := 0
	for pos < len(data) {
		hdr := data[pos]
		if hdr == 0 {
			break
		}
		pos++
		lenSz := int(hdr & 0x0F)
		offSz := int((hdr >> 4) & 0x0F)
		if lenSz == 0 || pos+lenSz+offSz > len(data) {
			break
		}
		var runLen int64
		for i := 0; i < lenSz; i++ {
			runLen |= int64(data[pos+i]) << (uint(i) * 8)
		}
		pos += lenSz
		if offSz == 0 {
			continue // sparse run, no offset
		}
		var runOff int64
		for i := 0; i < offSz; i++ {
			runOff |= int64(data[pos+i]) << (uint(i) * 8)
		}
		if data[pos+offSz-1]&0x80 != 0 {
			for i := offSz; i < 8; i++ {
				runOff |= int64(0xFF) << (uint(i) * 8)
			}
		}
		pos += offSz
		lcn += runOff
		// runLen and lcn are assembled from up to 8 attacker-controlled on-disk
		// bytes; guard the cluster->byte multiplications against int64 overflow
		// (which would yield negative/garbage extents) and drop the run instead.
		byteOff, ok1 := mulNoOverflow(lcn, bytesPerCluster)
		byteLen, ok2 := mulNoOverflow(runLen, bytesPerCluster)
		if !ok1 || !ok2 {
			continue
		}
		ext = append(ext, extent{byteOffset: byteOff, byteLength: byteLen})
	}
	return ext
}

// mulNoOverflow returns a*b and ok=false when the signed multiplication would
// overflow int64. b (bytesPerCluster) is always > 0 here.
func mulNoOverflow(a, b int64) (int64, bool) {
	if b == 0 {
		return 0, true
	}
	p := a * b
	if p/b != a {
		return 0, false
	}
	return p, true
}

// attrListEntry is one entry from an $ATTRIBUTE_LIST attribute.
type attrListEntry struct {
	attrType uint32
	mftRef   uint64
}

// parseAttributeList decodes the resident form of an $ATTRIBUTE_LIST. The
// non-resident form is read separately (see readNonResidentAttrList) and its
// content passed to parseAttributeListEntries.
func parseAttributeList(attr []byte) []attrListEntry {
	if len(attr) < 24 || attr[8] == 1 {
		return nil
	}
	contentOff := int(binary.LittleEndian.Uint16(attr[0x14:0x16]))
	contentLen := int(binary.LittleEndian.Uint32(attr[0x10:0x14]))
	if contentOff+contentLen > len(attr) {
		return nil
	}
	return parseAttributeListEntries(attr[contentOff : contentOff+contentLen])
}

// parseAttributeListEntries walks the raw entry list of an $ATTRIBUTE_LIST
// (the content bytes, resident or non-resident) and returns its entries.
func parseAttributeListEntries(c []byte) []attrListEntry {
	var out []attrListEntry
	pos := 0
	for pos+0x18 <= len(c) {
		entryType := binary.LittleEndian.Uint32(c[pos : pos+4])
		entryLen := int(binary.LittleEndian.Uint16(c[pos+4 : pos+6]))
		if entryLen < 0x18 || pos+entryLen > len(c) {
			break
		}
		mftRef := binary.LittleEndian.Uint64(c[pos+0x10 : pos+0x18])
		out = append(out, attrListEntry{attrType: entryType, mftRef: MFTIndex(mftRef)})
		pos += entryLen
	}
	return out
}
