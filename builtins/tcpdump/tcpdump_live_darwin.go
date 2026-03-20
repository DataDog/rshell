// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build darwin

package tcpdump

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"golang.org/x/sys/unix"
)

// darwinLiveHandle captures live packets on macOS using BPF (Berkeley Packet
// Filter) character devices (/dev/bpf0, /dev/bpf1, …). It uses non-blocking
// I/O with a 100 ms poll timeout so the capture loop can check context
// cancellation frequently.
//
// BPF packet format (from <net/bpf.h>):
//
//	struct bpf_hdr {
//	    struct timeval bh_tstamp;   // 8 bytes: int32 sec + int32 usec (Timeval32)
//	    uint32_t       bh_caplen;   // 4 bytes
//	    uint32_t       bh_datalen;  // 4 bytes
//	    uint16_t       bh_hdrlen;   // 2 bytes
//	};
//	// Total struct size: 18 bytes. bh_hdrlen is set by the kernel to the actual
//	// struct size (18), which is the byte offset to the packet data. Successive
//	// packets are BPF_WORDALIGN(hdrlen + caplen)-aligned to 4-byte boundaries.
type darwinLiveHandle struct {
	fd      int
	buf     []byte
	pending []byte // unprocessed bytes from the last BPF read
}

// bpfHdrFixedSize is the minimum number of bytes in a BPF packet header on
// Darwin. The kernel sets bh_hdrlen to 18 (the actual struct size), which is
// also the byte offset at which packet data begins.
const bpfHdrFixedSize = 18

// ifreqSize is the size of struct ifreq on Darwin (name[16] + union[16] = 32).
const ifreqSize = 32

// bpfWordAlign rounds n up to the next 4-byte boundary (BPF_WORDALIGN).
func bpfWordAlign(n int) int {
	return (n + 3) &^ 3
}

// openLiveInterface opens a BPF device, binds it to iface, and returns a
// packetReader. Requires root on macOS.
//
// Note: /dev/bpf* are system character devices used for network capture.
// They are opened with unix.Open (not callCtx.OpenFile) because they are not
// user-data files subject to the AllowedPaths sandbox — analogous to the raw
// network sockets used by the ping builtin.
func openLiveInterface(_ context.Context, iface string, snaplen int) (packetReader, error) {
	// Find the first available BPF device.
	fd := -1
	var openErr error
	for i := 0; i < 10; i++ {
		path := fmt.Sprintf("/dev/bpf%d", i)
		fd, openErr = unix.Open(path, unix.O_RDONLY, 0)
		if openErr == nil {
			break
		}
	}
	if fd < 0 {
		return nil, fmt.Errorf("cannot open BPF device (requires root): %w", openErr)
	}

	// Bind BPF to the named interface.
	// BIOCSETIF expects a struct ifreq (32 bytes on Darwin): the first 16 bytes
	// are the interface name (ifr_name[IFNAMSIZ]), the remainder is a union.
	// We pad the name to ifreqSize bytes so the kernel copyin reads valid memory.
	ifnameBuf := make([]byte, ifreqSize)
	copy(ifnameBuf, []byte(iface))
	if err := unix.IoctlSetString(fd, unix.BIOCSETIF, string(ifnameBuf)); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("cannot bind to interface %q: %w", iface, err)
	}

	// Enable immediate mode: return packets as they arrive rather than
	// buffering until the BPF buffer is full.
	if err := unix.IoctlSetPointerInt(fd, unix.BIOCIMMEDIATE, 1); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("BIOCIMMEDIATE: %w", err)
	}

	// Query the kernel buffer length.
	bufLen, err := unix.IoctlGetInt(fd, unix.BIOCGBLEN)
	if err != nil || bufLen <= 0 {
		bufLen = 65536
	}

	// Switch to non-blocking so Poll controls read timeouts.
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("cannot set non-blocking mode: %w", err)
	}

	_ = snaplen // snaplen is enforced by runCapture; not applied at the BPF layer.
	return &darwinLiveHandle{
		fd:  fd,
		buf: make([]byte, bufLen),
	}, nil
}

// ReadPacketData returns the next captured packet.
// It drains any packets buffered from the previous read before issuing a new
// one. If the pending buffer is empty it polls for up to 100 ms.
func (h *darwinLiveHandle) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	// Return a buffered packet if one is available.
	if len(h.pending) >= bpfHdrFixedSize {
		return h.nextFromPending()
	}

	// Poll for new data with a 100 ms timeout.
	fds := [1]unix.PollFd{{Fd: int32(h.fd), Events: unix.POLLIN}}
	n, err := unix.Poll(fds[:], 100)
	if err != nil {
		return nil, gopacket.CaptureInfo{}, err
	}
	if n == 0 {
		return nil, gopacket.CaptureInfo{}, errReadTimeout
	}

	nr, err := unix.Read(h.fd, h.buf)
	if err != nil {
		return nil, gopacket.CaptureInfo{}, err
	}
	if nr == 0 {
		return nil, gopacket.CaptureInfo{}, io.EOF
	}

	h.pending = h.buf[:nr]
	return h.nextFromPending()
}

// nextFromPending extracts the next packet from h.pending.
// BPF header layout (all little-endian on Darwin/x86-64 and Darwin/arm64):
//
//	[0:4]  bh_tstamp.tv_sec  (int32 — Timeval32.Sec)
//	[4:8]  bh_tstamp.tv_usec (int32 — Timeval32.Usec)
//	[8:12] bh_caplen         (uint32)
//	[12:16] bh_datalen       (uint32)
//	[16:18] bh_hdrlen        (uint16) — equals 18 on Darwin; packet data follows immediately
func (h *darwinLiveHandle) nextFromPending() ([]byte, gopacket.CaptureInfo, error) {
	if len(h.pending) < bpfHdrFixedSize {
		h.pending = nil
		return nil, gopacket.CaptureInfo{}, io.EOF
	}

	sec := int32(h.pending[0]) | int32(h.pending[1])<<8 | int32(h.pending[2])<<16 | int32(h.pending[3])<<24
	usec := int32(h.pending[4]) | int32(h.pending[5])<<8 | int32(h.pending[6])<<16 | int32(h.pending[7])<<24
	caplen := int(h.pending[8]) | int(h.pending[9])<<8 | int(h.pending[10])<<16 | int(h.pending[11])<<24
	datalen := int(h.pending[12]) | int(h.pending[13])<<8 | int(h.pending[14])<<16 | int(h.pending[15])<<24
	hdrlen := int(h.pending[16]) | int(h.pending[17])<<8

	if hdrlen < bpfHdrFixedSize || hdrlen > len(h.pending) {
		h.pending = nil
		return nil, gopacket.CaptureInfo{}, io.EOF
	}
	if caplen < 0 || hdrlen+caplen > len(h.pending) {
		h.pending = nil
		return nil, gopacket.CaptureInfo{}, io.EOF
	}

	// Remember the original captured length for the BPF_WORDALIGN advance: we
	// need to skip the full caplen bytes in the pending buffer even if we only
	// copy a smaller slice for display.
	origCaplen := caplen

	// Cap to MaxPacketBytes before copying.
	if caplen > MaxPacketBytes {
		caplen = MaxPacketBytes
	}

	data := make([]byte, caplen)
	copy(data, h.pending[hdrlen:hdrlen+caplen])

	// Advance pending past this packet (BPF_WORDALIGN-aligned), using the
	// original captured length so that subsequent packets are not misaligned.
	advance := bpfWordAlign(hdrlen + origCaplen)
	if advance >= len(h.pending) {
		h.pending = nil
	} else {
		h.pending = h.pending[advance:]
	}

	ci := gopacket.CaptureInfo{
		Timestamp:     time.Unix(int64(sec), int64(usec)*1000),
		CaptureLength: caplen,
		Length:        datalen,
	}
	return data, ci, nil
}

// LinkType returns the link type for an Ethernet BPF device.
func (h *darwinLiveHandle) LinkType() layers.LinkType {
	return layers.LinkTypeEthernet
}

// Close releases the underlying BPF file descriptor.
func (h *darwinLiveHandle) Close() error {
	return unix.Close(h.fd)
}
