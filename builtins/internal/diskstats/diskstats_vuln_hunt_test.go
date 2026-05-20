// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build linux

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// df-mount-enumeration.

package diskstats

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestVulnHuntSubsystemInvariantViolation_RemoteMountsClassifiedBeforeStatfs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		fsType string
	}{
		{"nfs source on generic fs", "server:/export", "auto"},
		{"sshfs source shape", "user@host:/path", "ext4"},
		{"smb unc source", "//server/share", "ext4"},
		{"nfs type", "/dev/local", "nfs4"},
		{"fuse sshfs type", "/dev/local", "fuse.sshfs"},
		{"fuse rclone type", "/dev/local", "fuse.rclone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !isRemoteSource(tc.source, tc.fsType) {
				t.Fatalf("remote mount source=%q fsType=%q classified local", tc.source, tc.fsType)
			}
		})
	}
}

func TestVulnHuntSubsystemResourceLimitBypass_ParseMountInfoCapsAndGenericErrors(t *testing.T) {
	tooLong := strings.Repeat("x", maxMountInfoLine+1) + "\n"
	mounts, err := parseMountInfo(context.Background(), strings.NewReader(tooLong))
	if !errors.Is(err, errLineTooLong) {
		t.Fatalf("overlong mountinfo line error = %v, want errLineTooLong", err)
	}
	if len(mounts) != 0 {
		t.Fatalf("overlong line returned %d mounts, want 0", len(mounts))
	}
	if strings.Contains(err.Error(), "xxx") {
		t.Fatalf("overlong error leaked raw line content: %q", err)
	}

	var b strings.Builder
	for range 5000 {
		b.WriteString("malformed without separator\n")
	}
	mounts, err = parseMountInfo(context.Background(), strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("malformed-only stream returned unexpected error: %v", err)
	}
	if len(mounts) != 0 {
		t.Fatalf("malformed-only stream returned %d mounts, want 0", len(mounts))
	}
}

func TestVulnHuntSubsystemResourceLimitBypass_UnescapeDoesNotGrow(t *testing.T) {
	for _, in := range []string{
		strings.Repeat(`\040`, 4096),
		strings.Repeat(`\999`, 4096),
		strings.Repeat(`\04`, 4096),
		strings.Repeat(`\\`, 4096),
	} {
		if got := unescapeMountField(in); len(got) > len(in) {
			t.Fatalf("unescapeMountField grew input: in=%d out=%d", len(in), len(got))
		}
	}
}

func TestVulnHuntSubsystemPanicStateCorruption_MalformedBinaryMountinfoNoPanic(t *testing.T) {
	input := strings.Join([]string{
		"36 35 98:0 / /ok rw - ext4 /dev/x rw",
		"36 35 98:0 / /nul\x00mount rw - ext4 /dev/x rw",
		"36 35 98:0 / /\xff\xfe rw - ext4 /dev/x rw",
		"\x7fELF\x02\x01\x01\x00 - ext4 src rw",
		"36 - 98:0 - / / rw - ext4 /dev/sda1 rw",
		"37 35 0:18 / /crlf rw - proc proc rw\r",
	}, "\n") + "\n"

	mounts, err := parseMountInfo(context.Background(), strings.NewReader(input))
	if err != nil {
		t.Fatalf("parseMountInfo returned error for malformed/binary mix: %v", err)
	}
	if len(mounts) == 0 {
		t.Fatal("expected at least one valid mount from mixed input")
	}
	for i, m := range mounts {
		if m.MountPoint == "" || m.FSType == "" {
			t.Fatalf("mount %d has empty required fields: %#v", i, m)
		}
	}
}

func TestVulnHuntSubsystemResourceLimitBypass_CanceledContextStopsParse(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := parseMountInfo(ctx, strings.NewReader("36 35 98:0 / / rw - ext4 /dev/x rw\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("parseMountInfo canceled error = %v, want context.Canceled", err)
	}
}
