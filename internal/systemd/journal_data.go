// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

const (
	journalDataRegularHeaderSize = 64
	journalDataCompactHeaderSize = 72

	maxJournalDecodeWindow = 8 * 1024 * 1024
	maxJournalEncodedRead  = 8 * 1024 * 1024
	maxJournalLZ4DataSize  = 8 * 1024 * 1024
	maxJournalPayloadRead  = maxJournalFieldSize + 512
)

type journalDataObject struct {
	journalObject
	hash                       uint64
	nextHashOffset             uint64
	nextFieldOffset            uint64
	entryOffset                uint64
	entryArrayOffset           uint64
	nEntries                   uint64
	tailEntryArrayOffset       uint32
	tailEntryArrayNEntries     uint32
	hasTailEntryArrayReference bool
	payloadOffset              uint64
	payloadSize                uint64
}

func (f *journalFileView) dataObjectAt(offset uint64) (journalDataObject, error) {
	object, err := f.objectAt(offset, journalObjectData)
	if err != nil {
		return journalDataObject{}, err
	}

	headerSize := uint64(journalDataRegularHeaderSize)
	if f.header.compact() {
		headerSize = journalDataCompactHeaderSize
	}
	if object.size < headerSize {
		return journalDataObject{}, journalCorrupt(f.name, offset, "DATA object size %d is smaller than its %d-byte header", object.size, headerSize)
	}

	var raw [journalDataCompactHeaderSize]byte
	if err := readJournalAt(f.name, f.reader, f.size, offset, raw[:headerSize]); err != nil {
		return journalDataObject{}, err
	}
	data := journalDataObject{
		journalObject:    object,
		hash:             binary.LittleEndian.Uint64(raw[16:24]),
		nextHashOffset:   binary.LittleEndian.Uint64(raw[24:32]),
		nextFieldOffset:  binary.LittleEndian.Uint64(raw[32:40]),
		entryOffset:      binary.LittleEndian.Uint64(raw[40:48]),
		entryArrayOffset: binary.LittleEndian.Uint64(raw[48:56]),
		nEntries:         binary.LittleEndian.Uint64(raw[56:64]),
		payloadOffset:    offset + headerSize,
		payloadSize:      object.size - headerSize,
	}
	if f.header.compact() {
		data.tailEntryArrayOffset = binary.LittleEndian.Uint32(raw[64:68])
		data.tailEntryArrayNEntries = binary.LittleEndian.Uint32(raw[68:72])
		data.hasTailEntryArrayReference = true
	}

	for _, reference := range []struct {
		offset uint64
		label  string
	}{
		{offset: data.nextHashOffset, label: "next DATA hash"},
		{offset: data.nextFieldOffset, label: "next DATA field"},
		{offset: data.entryOffset, label: "first DATA entry"},
		{offset: data.entryArrayOffset, label: "DATA entry array"},
	} {
		if reference.offset == 0 {
			continue
		}
		if err := validateJournalObjectOffset(f.name, f.header, reference.offset, reference.label); err != nil {
			return journalDataObject{}, err
		}
	}
	if data.hasTailEntryArrayReference {
		if (data.tailEntryArrayOffset == 0) != (data.tailEntryArrayNEntries == 0) {
			return journalDataObject{}, journalCorrupt(f.name, uint64(data.tailEntryArrayOffset), "DATA tail entry array offset and count are inconsistent")
		}
		if data.tailEntryArrayOffset != 0 {
			tailOffset := uint64(data.tailEntryArrayOffset)
			if err := validateJournalObjectOffset(f.name, f.header, tailOffset, "DATA tail entry array"); err != nil {
				return journalDataObject{}, err
			}
			if data.entryArrayOffset == 0 || data.entryArrayOffset > tailOffset {
				return journalDataObject{}, journalCorrupt(f.name, tailOffset, "DATA tail entry array precedes its entry array chain")
			}
		}
	}

	return data, nil
}

func (f *journalFileView) readDataPayload(data journalDataObject, limit int) ([]byte, bool, error) {
	if limit <= 0 {
		return nil, false, fmt.Errorf("journal DATA payload limit must be positive")
	}
	if limit == math.MaxInt {
		return nil, false, fmt.Errorf("journal DATA payload limit is too large")
	}
	if limit > maxJournalPayloadRead {
		return nil, false, fmt.Errorf("journal DATA payload limit %d exceeds maximum %d", limit, maxJournalPayloadRead)
	}

	switch data.flags {
	case 0:
		return f.readUncompressedDataPayload(data, limit)
	case journalObjectCompressedXZ:
		decoder, err := (xz.ReaderConfig{
			DictCap:      maxJournalDecodeWindow,
			SingleStream: true,
		}).NewReader(f.compressedPayloadReader(data))
		if err != nil {
			if errors.Is(err, errJournalLimit) {
				return nil, false, err
			}
			return nil, false, journalCorrupt(f.name, data.payloadOffset, "decode XZ DATA payload: %v", err)
		}
		return readDecodedJournalPayload(f.name, data.payloadOffset, "XZ", decoder, limit)
	case journalObjectCompressedLZ4:
		return f.readLZ4DataPayload(data, limit)
	case journalObjectCompressedZSTD:
		decoder, err := zstd.NewReader(
			f.compressedPayloadReader(data),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderLowmem(true),
			zstd.WithDecoderMaxMemory(maxJournalDecodeWindow),
			zstd.WithDecoderMaxWindow(maxJournalDecodeWindow),
		)
		if err != nil {
			if errors.Is(err, errJournalLimit) {
				return nil, false, err
			}
			return nil, false, journalCorrupt(f.name, data.payloadOffset, "decode ZSTD DATA payload: %v", err)
		}
		defer decoder.Close()
		return readDecodedJournalPayload(f.name, data.payloadOffset, "ZSTD", decoder, limit)
	default:
		return nil, false, journalUnsupported(f.name, "DATA object at offset %d uses compression flags 0x%02x", data.offset, data.flags)
	}
}

func (f *journalFileView) compressedPayloadReader(data journalDataObject) io.Reader {
	section := io.NewSectionReader(f.reader, int64(data.payloadOffset), int64(data.payloadSize))
	if data.payloadSize <= maxJournalEncodedRead {
		return section
	}
	return &journalCompressedReader{
		reader:    section,
		remaining: maxJournalEncodedRead,
		name:      f.name,
		offset:    data.payloadOffset,
	}
}

type journalCompressedReader struct {
	reader    io.Reader
	remaining int
	name      string
	offset    uint64
}

func (r *journalCompressedReader) Read(destination []byte) (int, error) {
	if len(destination) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		return 0, journalLimit(r.name, r.offset, "compressed DATA input exceeds %d bytes", maxJournalEncodedRead)
	}
	if len(destination) > r.remaining {
		destination = destination[:r.remaining]
	}
	n, err := r.reader.Read(destination)
	r.remaining -= n
	return n, err
}

func (f *journalFileView) readUncompressedDataPayload(data journalDataObject, limit int) ([]byte, bool, error) {
	readSize := data.payloadSize
	if readSize > uint64(limit)+1 {
		readSize = uint64(limit) + 1
	}
	payload := make([]byte, int(readSize))
	if err := readJournalAt(f.name, f.reader, f.size, data.payloadOffset, payload); err != nil {
		return nil, false, err
	}
	if len(payload) > limit {
		return payload[:limit], true, nil
	}
	return payload, false, nil
}

func (f *journalFileView) readLZ4DataPayload(data journalDataObject, limit int) ([]byte, bool, error) {
	if data.payloadSize <= 8 {
		return nil, false, journalCorrupt(f.name, data.payloadOffset, "LZ4 DATA payload is too small")
	}

	var sizeBytes [8]byte
	if err := readJournalAt(f.name, f.reader, f.size, data.payloadOffset, sizeBytes[:]); err != nil {
		return nil, false, err
	}
	decodedSize := binary.LittleEndian.Uint64(sizeBytes[:])
	if decodedSize == 0 {
		return nil, false, journalCorrupt(f.name, data.payloadOffset, "LZ4 DATA payload has an empty decoded size")
	}
	if decodedSize > maxJournalLZ4DataSize {
		return nil, false, journalLimit(f.name, data.payloadOffset, "LZ4 DATA payload expands to %d bytes; maximum is %d", decodedSize, maxJournalLZ4DataSize)
	}
	if data.payloadSize-8 > maxJournalEncodedRead {
		return nil, false, journalLimit(f.name, data.payloadOffset, "compressed LZ4 DATA input exceeds %d bytes", maxJournalEncodedRead)
	}
	if decodedSize > uint64(math.MaxInt) || data.payloadSize-8 > uint64(math.MaxInt) {
		return nil, false, journalLimit(f.name, data.payloadOffset, "LZ4 DATA payload exceeds the platform allocation range")
	}

	compressed := make([]byte, int(data.payloadSize-8))
	if err := readJournalAt(f.name, f.reader, f.size, data.payloadOffset+8, compressed); err != nil {
		return nil, false, err
	}
	decoded := make([]byte, int(decodedSize))
	n, err := lz4.UncompressBlock(compressed, decoded)
	if err != nil {
		return nil, false, journalCorrupt(f.name, data.payloadOffset, "decode LZ4 DATA payload: %v", err)
	}
	if n != len(decoded) {
		return nil, false, journalCorrupt(f.name, data.payloadOffset, "decode LZ4 DATA payload: got %d of %d bytes", n, len(decoded))
	}
	if len(decoded) > limit {
		return decoded[:limit], true, nil
	}
	return decoded, false, nil
}

func readDecodedJournalPayload(name string, offset uint64, algorithm string, reader io.Reader, limit int) ([]byte, bool, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		if errors.Is(err, errJournalLimit) {
			return nil, false, err
		}
		return nil, false, journalCorrupt(name, offset, "decode %s DATA payload: %v", algorithm, err)
	}
	if len(payload) > limit {
		return payload[:limit], true, nil
	}
	return payload, false, nil
}

func journalLimit(name string, offset uint64, format string, arguments ...any) error {
	return fmt.Errorf("%w %q at offset %d: %s", errJournalLimit, name, offset, fmt.Sprintf(format, arguments...))
}
