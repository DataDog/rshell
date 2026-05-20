// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Tripwire tests added by vuln-hunt campaign 2026-05-20-gpt-5.5-cyber-3 /
// df-mount-enumeration.

package df

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/diskstats"
)

func TestVulnHuntSubsystemInvariantViolation_ControlMountCellsCannotForgeRows(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"src\nforged", "src?forged"},
		{"src\tforged", "src?forged"},
		{"src\rforged", "src?forged"},
		{"src\x00forged", "src?forged"},
		{"src\x7fforged", "src?forged"},
	} {
		if got := replaceUnprintable(tc.in); got != tc.want {
			t.Fatalf("replaceUnprintable(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVulnHuntSubsystemPanicStateCorruption_WriteOutputAdversarialRows(t *testing.T) {
	var stdout, stderr bytes.Buffer
	flags := &flags{
		posix:     boolPtrVH(true),
		printType: boolPtrVH(true),
		inodes:    boolPtrVH(false),
		total:     boolPtrVH(true),
	}
	mounts := []diskstats.Mount{
		{
			Source: "src\nFORGED_DF_ROW=1", DevID: "8:1", MountPoint: "/mnt\tbad", FSType: "ext4",
			Total: ^uint64(0), Used: ^uint64(0), Free: ^uint64(0),
		},
		{
			Source: "dup", DevID: "8:1", MountPoint: "/longer/duplicate", FSType: "ext4",
			Total: 1, Used: 1, Free: 0,
		},
	}

	writeOutput(&builtins.CallContext{Stdout: &stdout, Stderr: &stderr}, mounts, flags, unitsK)
	if stderr.Len() != 0 {
		t.Fatalf("writeOutput wrote stderr: %q", stderr.String())
	}
	out := stdout.String()
	if strings.Contains(out, "\nFORGED_DF_ROW") {
		t.Fatalf("control byte in mount source forged a row:\n%s", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if got, want := len(lines), 4; got != want {
		t.Fatalf("output line count = %d, want header + 2 rows + total; output:\n%s", got, out)
	}
}

func TestVulnHuntSubsystemResourceLimitBypass_DfArithmeticSaturates(t *testing.T) {
	maxU := ^uint64(0)
	if got := saturatingAdd(maxU, 1); got != maxU {
		t.Fatalf("saturatingAdd(max, 1) = %d, want max", got)
	}
	if got := percentUsed(maxU, maxU); got != "50%" {
		t.Fatalf("percentUsed(max, max) = %q, want 50%%", got)
	}
	if got := percentUsed(1, maxU); got != "1%" {
		t.Fatalf("percentUsed(1, max) = %q, want 1%%", got)
	}
	if got := formatCount(maxU, unitsK, false); got == "0" || got == "" {
		t.Fatalf("formatCount(max, unitsK) wrapped or emptied: %q", got)
	}
}

func TestVulnHuntSubsystemRaceToctou_DedupKeepsShortestAndEmptyDevID(t *testing.T) {
	in := []diskstats.Mount{
		{Source: "bind", DevID: "0:25", MountPoint: "/etc/resolv.conf"},
		{Source: "bind", DevID: "0:25", MountPoint: "/etc/hosts"},
		{Source: "nodev-a", DevID: "", MountPoint: "/a"},
		{Source: "nodev-b", DevID: "", MountPoint: "/b"},
	}
	out := filterMounts(append([]diskstats.Mount(nil), in...), &flags{all: boolPtrVH(false)})
	if len(out) != 3 {
		t.Fatalf("filterMounts kept %d rows, want duplicate collapsed plus two empty-DevID rows", len(out))
	}
	var sawHosts, sawA, sawB bool
	for _, m := range out {
		sawHosts = sawHosts || m.MountPoint == "/etc/hosts"
		sawA = sawA || m.MountPoint == "/a"
		sawB = sawB || m.MountPoint == "/b"
	}
	if !sawHosts || !sawA || !sawB {
		t.Fatalf("dedup result lost required rows: %#v", out)
	}
}

func boolPtrVH(v bool) *bool { return &v }
