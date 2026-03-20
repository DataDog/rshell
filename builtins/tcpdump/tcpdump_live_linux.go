// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

package tcpdump

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/google/gopacket"
	"github.com/google/gopacket/layers"
	"golang.org/x/sys/unix"
)

// linuxLiveHandle captures live packets on Linux using an AF_PACKET raw socket.
// It uses non-blocking I/O with a 100 ms poll timeout so the capture loop can
// check context cancellation frequently.
type linuxLiveHandle struct {
	fd  int
	buf []byte
}

// openLiveInterface opens a raw AF_PACKET socket bound to iface and returns a
// packetReader. Requires CAP_NET_RAW or root.
func openLiveInterface(_ context.Context, iface string, snaplen int) (packetReader, error) {
	bufSize := snaplen
	if bufSize <= 0 {
		bufSize = MaxPacketBytes
	}
	if bufSize > MaxPacketBytes {
		bufSize = MaxPacketBytes
	}

	// Create a raw Ethernet socket that receives all frames.
	// htons(ETH_P_ALL) = 0x0300 on little-endian hardware.
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("cannot open live capture socket (requires CAP_NET_RAW or root): %w", err)
	}

	// Resolve the interface.
	nif, err := net.InterfaceByName(iface)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("interface %q: %w", iface, err)
	}

	// Bind to the specific interface so we only see its traffic.
	sll := unix.SockaddrLinklayer{
		Protocol: htons(unix.ETH_P_ALL),
		Ifindex:  nif.Index,
	}
	if err := unix.Bind(fd, &sll); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("cannot bind to interface %q: %w", iface, err)
	}

	// Switch to non-blocking so Poll controls read timeouts.
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("cannot set non-blocking mode: %w", err)
	}

	return &linuxLiveHandle{
		fd:  fd,
		buf: make([]byte, bufSize),
	}, nil
}

// ReadPacketData reads the next packet from the AF_PACKET socket.
// It polls for up to 100 ms; if no data arrives it returns errReadTimeout.
func (h *linuxLiveHandle) ReadPacketData() ([]byte, gopacket.CaptureInfo, error) {
	// Poll with a 100 ms timeout so the caller can check ctx.Err() periodically.
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

	data := make([]byte, nr)
	copy(data, h.buf[:nr])

	ci := gopacket.CaptureInfo{
		Timestamp:     time.Now(),
		CaptureLength: nr,
		Length:        nr,
	}
	return data, ci, nil
}

// LinkType returns the link type for an Ethernet AF_PACKET socket.
func (h *linuxLiveHandle) LinkType() layers.LinkType {
	return layers.LinkTypeEthernet
}

// Close releases the underlying file descriptor.
func (h *linuxLiveHandle) Close() error {
	return unix.Close(h.fd)
}

// htons converts a uint16 from host byte order to network byte order.
// All currently supported Linux hardware is little-endian (x86, ARM),
// so this always swaps the two bytes.
func htons(v uint16) uint16 {
	return (v >> 8) | (v << 8)
}
