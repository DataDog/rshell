// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tests for the $MFT extent-resolution code (getMFTExtents and its pure
// helpers: decodeDataRuns, parseAttributeList / parseAttributeListEntries,
// readNonResidentAttrList). This code parses raw on-disk bytes and only needs
// disk I/O, which is injected via the readerAt seam, so it runs on any OS.
//
// The interesting path is a fragmented $MFT whose record 0 carries an
// $ATTRIBUTE_LIST pointing at extension records that hold more $DATA runs. The
// rare-but-real non-resident $ATTRIBUTE_LIST variant (only seen on badly
// fragmented / near-full volumes — exactly this tool's target) cannot be
// exercised on a CI NTFS volume, so these synthetic tests are its only
// automated coverage.
package ntfsmft

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const (
	testExtRecordSize = 1024
	testBytesPerClus  = 4096
)

// memReader returns a readerAt over an in-memory disk image. Out-of-range
// reads error (never panic), matching the real ReadFile wrapper's short-read
// behavior.
func memReader(disk []byte) readerAt {
	return func(buf []byte, off int64) error {
		if off < 0 || off+int64(len(buf)) > int64(len(disk)) {
			return fmt.Errorf("read [%d,%d) out of range (disk %d)", off, off+int64(len(buf)), len(disk))
		}
		copy(buf, disk[off:off+int64(len(buf))])
		return nil
	}
}

// singleRun encodes one NTFS data run with a 1-byte length and 1-byte signed
// LCN delta, followed by the 0x00 terminator.
func singleRun(runLen, lcnDelta byte) []byte {
	return []byte{0x11, runLen, lcnDelta, 0x00}
}

// writeRecordHeader stamps a valid "FILE" header with a 3-word fixup array
// (1 USN + 2 sectors) whose sector-end USNs match, so applyFixups accepts the
// record. firstAttrOff is where the attribute chain begins.
func writeRecordHeader(rec []byte, firstAttrOff int) {
	binary := leWriter{rec}
	binary.u32(0, mftSignature)
	binary.u16(4, 0x30) // fixup (USA) offset
	binary.u16(6, 3)    // fixup count: 1 USN + 2 sectors
	binary.u16(0x14, uint16(firstAttrOff))
	binary.u16(0x16, flagInUse)
	binary.u16(0x30, 0xCAFE)  // USN
	binary.u16(0x1FE, 0xCAFE) // sector 1 end
	binary.u16(0x3FE, 0xCAFE) // sector 2 end
}

// nonResidentAttr builds a non-resident attribute: MappingPairsOffset at 0x20
// points at drOff (0x40, just past the header), real size at 0x30, and the
// data runs copied in at 0x40.
func nonResidentAttr(attrType uint32, dataSize int64, runs []byte) []byte {
	const drOff = 0x40
	attr := make([]byte, drOff+len(runs))
	w := leWriter{attr}
	w.u32(0, attrType)
	w.u32(4, uint32(len(attr)))
	attr[8] = 1 // non-resident
	w.u16(0x20, drOff)
	w.u64(0x30, uint64(dataSize))
	copy(attr[drOff:], runs)
	return attr
}

// residentAttr builds a resident attribute: content length at 0x10, content
// offset at 0x14 (0x18, just past the resident header), content copied in.
func residentAttr(attrType uint32, content []byte) []byte {
	const contentOff = 0x18
	attr := make([]byte, contentOff+len(content))
	w := leWriter{attr}
	w.u32(0, attrType)
	w.u32(4, uint32(len(attr)))
	attr[8] = 0 // resident
	w.u32(0x10, uint32(len(content)))
	w.u16(0x14, contentOff)
	copy(attr[contentOff:], content)
	return attr
}

// attrListEntryBytes builds one $ATTRIBUTE_LIST entry (0x18 bytes): type at
// 0x00, entry length at 0x04, referenced MFT file reference at 0x10.
func attrListEntryBytes(attrType uint32, mftRef uint64) []byte {
	e := make([]byte, 0x18)
	w := leWriter{e}
	w.u32(0, attrType)
	w.u16(4, 0x18)
	w.u64(0x10, mftRef)
	return e
}

// assembleRecord builds a full record: valid header, attributes packed from
// 0x38, terminated with the end marker.
func assembleRecord(attrs ...[]byte) []byte {
	rec := make([]byte, testExtRecordSize)
	writeRecordHeader(rec, 0x38)
	off := 0x38
	for _, a := range attrs {
		copy(rec[off:], a)
		off += len(a)
	}
	leWriter{rec}.u32(off, attrEndMarker)
	return rec
}

// leWriter is a tiny little-endian writer used only by these test builders.
type leWriter struct{ b []byte }

func (w leWriter) u16(off int, v uint16) {
	w.b[off] = byte(v)
	w.b[off+1] = byte(v >> 8)
}
func (w leWriter) u32(off int, v uint32) {
	for i := 0; i < 4; i++ {
		w.b[off+i] = byte(v >> (uint(i) * 8))
	}
}
func (w leWriter) u64(off int, v uint64) {
	for i := 0; i < 8; i++ {
		w.b[off+i] = byte(v >> (uint(i) * 8))
	}
}

func testVol() *volumeInfo {
	return &volumeInfo{
		recordSize:      testExtRecordSize,
		bytesPerCluster: testBytesPerClus,
		mftStartByte:    testBytesPerClus, // record 0 at LCN 1
		mftValidBytes:   testBytesPerClus * 4,
	}
}

func extentsEqual(a, b []extent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// -------------------------------------------------------------------------
// getMFTExtents
// -------------------------------------------------------------------------

func TestGetMFTExtents_InlineOnly(t *testing.T) {
	// Record 0 with a single non-resident $DATA run and no $ATTRIBUTE_LIST:
	// the inline extents are returned as-is.
	disk := make([]byte, 8192)
	copy(disk[testBytesPerClus:], assembleRecord(nonResidentAttr(attrData, 0, singleRun(2, 1))))

	got, err := getMFTExtents(memReader(disk), testVol())
	if err != nil {
		t.Fatalf("getMFTExtents: %v", err)
	}
	want := []extent{{byteOffset: 1 * testBytesPerClus, byteLength: 2 * testBytesPerClus}}
	if !extentsEqual(got, want) {
		t.Errorf("extents = %+v, want %+v", got, want)
	}
}

func TestGetMFTExtents_ResidentAttrListChasesExtension(t *testing.T) {
	// Record 0's inline $DATA covers records 0-7 (2 clusters at LCN 1). Its
	// resident $ATTRIBUTE_LIST points at record 1 (a $DATA extension), whose
	// own run must be appended to the result.
	disk := make([]byte, 8192)
	rec0 := assembleRecord(
		nonResidentAttr(attrData, 0, singleRun(2, 1)),
		residentAttr(attrAttributeList, attrListEntryBytes(attrData, 1)),
	)
	rec1 := assembleRecord(nonResidentAttr(attrData, 0, singleRun(5, 50)))
	copy(disk[testBytesPerClus:], rec0)                   // record 0 at 4096
	copy(disk[testBytesPerClus+testExtRecordSize:], rec1) // record 1 at 5120
	got, err := getMFTExtents(memReader(disk), testVol())
	if err != nil {
		t.Fatalf("getMFTExtents: %v", err)
	}
	want := []extent{
		{byteOffset: 1 * testBytesPerClus, byteLength: 2 * testBytesPerClus},
		{byteOffset: 50 * testBytesPerClus, byteLength: 5 * testBytesPerClus},
	}
	if !extentsEqual(got, want) {
		t.Errorf("extents = %+v, want %+v", got, want)
	}
}

func TestGetMFTExtents_NonResidentAttrListChasesExtension(t *testing.T) {
	// The rare case: record 0's $ATTRIBUTE_LIST is non-resident. Its content
	// (one entry pointing at record 1) lives in a disk cluster located by the
	// attr-list's own inline runs — read directly by LCN, no MFT indirection.
	disk := make([]byte, 16384)
	rec0 := assembleRecord(
		nonResidentAttr(attrData, 0, singleRun(2, 1)),             // MFT self-location
		nonResidentAttr(attrAttributeList, 0x18, singleRun(1, 3)), // content at LCN 3
	)
	rec1 := assembleRecord(nonResidentAttr(attrData, 0, singleRun(5, 50)))
	copy(disk[testBytesPerClus:], rec0)                              // record 0 at 4096
	copy(disk[testBytesPerClus+testExtRecordSize:], rec1)            // record 1 at 5120
	copy(disk[3*testBytesPerClus:], attrListEntryBytes(attrData, 1)) // content at 12288

	got, err := getMFTExtents(memReader(disk), testVol())
	if err != nil {
		t.Fatalf("getMFTExtents: %v", err)
	}
	want := []extent{
		{byteOffset: 1 * testBytesPerClus, byteLength: 2 * testBytesPerClus},
		{byteOffset: 50 * testBytesPerClus, byteLength: 5 * testBytesPerClus},
	}
	if !extentsEqual(got, want) {
		t.Errorf("extents = %+v, want %+v", got, want)
	}
}

func TestGetMFTExtents_UnresolvableEntrySkipped(t *testing.T) {
	// An attr-list entry whose MFT index falls outside the known inline extents
	// cannot be read; getMFTExtents skips it and still returns the inline runs
	// rather than failing the whole scan.
	disk := make([]byte, 8192)
	rec0 := assembleRecord(
		nonResidentAttr(attrData, 0, singleRun(2, 1)),
		residentAttr(attrAttributeList, attrListEntryBytes(attrData, 100000)),
	)
	copy(disk[testBytesPerClus:], rec0)

	got, err := getMFTExtents(memReader(disk), testVol())
	if err != nil {
		t.Fatalf("getMFTExtents: %v", err)
	}
	want := []extent{{byteOffset: 1 * testBytesPerClus, byteLength: 2 * testBytesPerClus}}
	if !extentsEqual(got, want) {
		t.Errorf("extents = %+v, want %+v", got, want)
	}
}

func TestGetMFTExtents_BadSignature(t *testing.T) {
	disk := make([]byte, 8192) // record 0 region is all zero: no signature
	if _, err := getMFTExtents(memReader(disk), testVol()); err == nil {
		t.Fatal("expected error on missing record 0 signature")
	}
}

func TestGetMFTExtents_NoData(t *testing.T) {
	// Record 0 with a valid header but no $DATA and no $ATTRIBUTE_LIST.
	disk := make([]byte, 8192)
	copy(disk[testBytesPerClus:], assembleRecord())
	_, err := getMFTExtents(memReader(disk), testVol())
	if err == nil || !strings.Contains(err.Error(), "no $DATA") {
		t.Fatalf("err = %v, want no-$DATA error", err)
	}
}

func TestGetMFTExtents_ReadError(t *testing.T) {
	// mftStartByte points past the end of the disk: the record 0 read fails.
	vol := testVol()
	if _, err := getMFTExtents(memReader(make([]byte, 1024)), vol); err == nil {
		t.Fatal("expected read error when record 0 is out of range")
	}
}

func TestGetMFTExtents_NonResidentAttrListTooLargeRejected(t *testing.T) {
	// A non-resident $ATTRIBUTE_LIST claiming a content size above the memory
	// bound must fail loudly rather than allocate unbounded memory.
	disk := make([]byte, 16384)
	rec0 := assembleRecord(
		nonResidentAttr(attrData, 0, singleRun(2, 1)),
		nonResidentAttr(attrAttributeList, maxAttrListBytes+1, singleRun(1, 3)),
	)
	copy(disk[testBytesPerClus:], rec0)
	if _, err := getMFTExtents(memReader(disk), testVol()); err == nil {
		t.Fatal("expected error on oversized non-resident $ATTRIBUTE_LIST")
	}
}

// -------------------------------------------------------------------------
// readNonResidentAttrList
// -------------------------------------------------------------------------

func TestReadNonResidentAttrList_Valid(t *testing.T) {
	disk := make([]byte, 16384)
	content := append(attrListEntryBytes(attrData, 7), attrListEntryBytes(attrData, 9)...)
	copy(disk[3*testBytesPerClus:], content) // at LCN 3
	attr := nonResidentAttr(attrAttributeList, int64(len(content)), singleRun(1, 3))

	got, err := readNonResidentAttrList(memReader(disk), attr, testBytesPerClus)
	if err != nil {
		t.Fatalf("readNonResidentAttrList: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch:\n got %x\nwant %x", got, content)
	}
}

func TestReadNonResidentAttrList_Errors(t *testing.T) {
	disk := make([]byte, 16384)
	cases := []struct {
		name string
		attr []byte
		want string
	}{
		{"too short", make([]byte, 0x20), "too short"},
		{"bad drOff", func() []byte {
			a := nonResidentAttr(attrAttributeList, 0x18, singleRun(1, 3))
			leWriter{a}.u16(0x20, 0x10) // drOff below the 0x40 header floor
			return a
		}(), "data-run offset"},
		{"zero size", nonResidentAttr(attrAttributeList, 0, singleRun(1, 3)), "out of range"},
		{"oversize", nonResidentAttr(attrAttributeList, maxAttrListBytes+1, singleRun(1, 3)), "out of range"},
		{"no runs", nonResidentAttr(attrAttributeList, 0x18, []byte{0x00}), "no data runs"},
		{"runs too small", nonResidentAttr(attrAttributeList, 2*testBytesPerClus, singleRun(1, 3)), "content size"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := readNonResidentAttrList(memReader(disk), c.attr, testBytesPerClus)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want containing %q", err, c.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// decodeDataRuns
// -------------------------------------------------------------------------

func TestDecodeDataRuns(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want []extent
	}{
		{"empty", nil, nil},
		{"terminator only", []byte{0x00}, nil},
		{"single run", singleRun(2, 1), []extent{{byteOffset: 4096, byteLength: 8192}}},
		{
			"two runs, second relative",
			// run1: len 2, lcn +1 → {4096, 8192}; run2: len 3, lcn +2 (=3) → {12288, 12288}
			[]byte{0x11, 0x02, 0x01, 0x11, 0x03, 0x02, 0x00},
			[]extent{{byteOffset: 4096, byteLength: 8192}, {byteOffset: 12288, byteLength: 12288}},
		},
		{
			"sparse run skipped",
			// sparse run (offSz 0): len 4, no offset → skipped; then len 1 at lcn +5
			[]byte{0x01, 0x04, 0x11, 0x01, 0x05, 0x00},
			[]extent{{byteOffset: 5 * 4096, byteLength: 4096}},
		},
		{
			"negative lcn delta (sign extension)",
			// run1: len 1 at lcn +10 → {40960,4096}; run2: len 1 at lcn -1 (0xFF) → lcn 9 → {36864,4096}
			[]byte{0x11, 0x01, 0x0A, 0x11, 0x01, 0xFF, 0x00},
			[]extent{{byteOffset: 10 * 4096, byteLength: 4096}, {byteOffset: 9 * 4096, byteLength: 4096}},
		},
		{"truncated run bytes", []byte{0x11, 0x02}, nil}, // header says 1+1 bytes follow but they don't
		{"zero length size", []byte{0x10, 0x05}, nil},    // lenSz 0 → break
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := decodeDataRuns(c.data, testBytesPerClus)
			if !extentsEqual(got, c.want) {
				t.Errorf("decodeDataRuns(%x) = %+v, want %+v", c.data, got, c.want)
			}
		})
	}
}

// -------------------------------------------------------------------------
// parseAttributeList / parseAttributeListEntries
// -------------------------------------------------------------------------

func TestParseAttributeList_Resident(t *testing.T) {
	content := append(attrListEntryBytes(attrData, 3), attrListEntryBytes(attrFileName, 4)...)
	attr := residentAttr(attrAttributeList, content)
	got := parseAttributeList(attr)
	want := []attrListEntry{{attrType: attrData, mftRef: 3}, {attrType: attrFileName, mftRef: 4}}
	if len(got) != len(want) {
		t.Fatalf("got %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseAttributeList_RejectsNonResident(t *testing.T) {
	attr := nonResidentAttr(attrAttributeList, 0x18, singleRun(1, 3))
	if got := parseAttributeList(attr); got != nil {
		t.Errorf("parseAttributeList on non-resident attr = %+v, want nil", got)
	}
}

func TestParseAttributeList_TooShort(t *testing.T) {
	if got := parseAttributeList(make([]byte, 16)); got != nil {
		t.Errorf("parseAttributeList(short) = %+v, want nil", got)
	}
}

func TestParseAttributeList_ContentOutOfBounds(t *testing.T) {
	attr := residentAttr(attrAttributeList, attrListEntryBytes(attrData, 1))
	// Claim a content length far beyond the attribute buffer.
	leWriter{attr}.u32(0x10, 0xFFFF)
	if got := parseAttributeList(attr); got != nil {
		t.Errorf("parseAttributeList(oob content) = %+v, want nil", got)
	}
}

func TestParseAttributeListEntries(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := parseAttributeListEntries(nil); got != nil {
			t.Errorf("got %+v, want nil", got)
		}
	})
	t.Run("mftRef masked to 48 bits", func(t *testing.T) {
		e := attrListEntryBytes(attrData, 0x0007_0000_0000_0005)
		got := parseAttributeListEntries(e)
		if len(got) != 1 || got[0].mftRef != 5 {
			t.Errorf("got %+v, want one entry with mftRef 5", got)
		}
	})
	t.Run("undersized entry length stops walk", func(t *testing.T) {
		e := attrListEntryBytes(attrData, 1)
		leWriter{e}.u16(4, 0x10) // entryLen < 0x18
		if got := parseAttributeListEntries(e); got != nil {
			t.Errorf("got %+v, want nil (walk should stop)", got)
		}
	})
	t.Run("entry length past content stops walk", func(t *testing.T) {
		e := attrListEntryBytes(attrData, 1)
		leWriter{e}.u16(4, 0xFF) // entryLen beyond the buffer
		if got := parseAttributeListEntries(e); got != nil {
			t.Errorf("got %+v, want nil (walk should stop)", got)
		}
	})
}
