// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package df

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/rshell/builtins/internal/diskstats"
)

func TestHumanBytes_1024(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "0"},
		{1, "1"},
		{1023, "1023"},
		{1024, "1.0K"},
		{2047, "2.0K"}, // 2047/1024 = 1.999 → 2.0
		{2048, "2.0K"},
		{10 * 1024, "10K"}, // ≥10 drops decimal
		{1024 * 1024, "1.0M"},
		{500 * 1024 * 1024, "500M"},
		{1 << 30, "1.0G"},
		{1<<40 + 1<<39, "1.5T"},
		{1 << 50, "1.0P"},
		{1 << 60, "1.0E"},
		{^uint64(0), "16E"},
		// GNU df rounds non-integer remainders up so "Used" never
		// under-reports. 1,576,960 bytes is 385 × 4 KiB blocks; GNU
		// emits "1.6M" rather than the round-to-nearest "1.5M".
		{1_576_960, "1.6M"},
		// 2 KiB + 1 byte → just over 2.0K, must round up to 2.1K.
		{2*1024 + 1, "2.1K"},
		// Just under 1 MiB: rounds up at K-level to 1024K which is
		// awkward output, so promote to the next suffix and emit
		// "1.0M". Matches GNU df.
		{1<<20 - 1, "1.0M"},
		// Same promotion at every higher boundary.
		{1<<30 - 1, "1.0G"},
		{1<<40 - 1, "1.0T"},
		// Above 10K we drop the decimal — make sure the promotion
		// path through the >=10 branch also works. 9.7M = 10172724
		// rounds-up to 10M (no decimal). 10239*1024 = 10484736 is
		// just under 10M and rounds to 10M too, but 10240*1024 - 1
		// = 10485759 (just under 10M) similarly rounds to 10M.
		{10485759, "10M"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanBytes(c.v, 1024), "v=%d", c.v)
	}
}

func TestHumanBytes_1000(t *testing.T) {
	cases := []struct {
		v    uint64
		want string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{1_000_000, "1.0M"},
		{1_000_000_000, "1.0G"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, humanBytes(c.v, 1000), "v=%d", c.v)
	}
}

func TestPercentUsed(t *testing.T) {
	cases := []struct {
		used, free uint64
		want       string
	}{
		{0, 0, "-"},
		{0, 100, "0%"},
		{1, 99, "1%"},
		{50, 50, "50%"},
		{99, 1, "99%"},
		{1, 0, "100%"},
		// Round-up semantics: any non-zero remainder bumps the
		// percentage up.
		{1, 999, "1%"},
		{1, 9_999_999, "1%"},
		// Overflow guard: used * 100 would wrap; the implementation
		// right-shifts both sides until the multiplication fits.
		// used == free → 50%, even at extreme magnitudes.
		{^uint64(0) / 50, ^uint64(0) / 50, "50%"},
		// Used > free at extreme magnitudes → 100%.
		{^uint64(0), 1, "100%"},
		{^uint64(0) / 2, ^uint64(0) / 4, "67%"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, percentUsed(c.used, c.free), "u=%d f=%d", c.used, c.free)
	}
}

func TestSaturatingAdd(t *testing.T) {
	maxU := ^uint64(0)
	assert.Equal(t, uint64(3), saturatingAdd(1, 2))
	assert.Equal(t, maxU, saturatingAdd(maxU, 0))
	assert.Equal(t, maxU, saturatingAdd(0, maxU))
	assert.Equal(t, maxU, saturatingAdd(maxU, 1))
	assert.Equal(t, maxU, saturatingAdd(maxU, maxU))
	assert.Equal(t, maxU-1, saturatingAdd(maxU/2, maxU/2))
}

func TestFormatCount(t *testing.T) {
	// Inode mode in fixed-block (POSIX / -k) units → raw integers.
	assert.Equal(t, "12345", formatCount(12345, unitsK, true))

	// `df -ih` and `df -iH`: GNU scales inode counts through the same
	// suffix machinery as block counts, so 4M inodes renders as "4.0M",
	// not "4194304".
	assert.Equal(t, "4.0M", formatCount(4*1024*1024, unitsHuman1024, true))
	assert.Equal(t, "1.0G", formatCount(1_000_000_000, unitsHuman1000, true))

	// 1K block mode: rounds up to the next 1024 boundary.
	assert.Equal(t, "0", formatCount(0, unitsK, false))
	assert.Equal(t, "1", formatCount(1, unitsK, false))
	assert.Equal(t, "1", formatCount(1024, unitsK, false))
	assert.Equal(t, "2", formatCount(1025, unitsK, false))

	// Saturated max value (e.g. an overflowed grand total) must not
	// wrap to 0 — must remain a sane integer count of 1K blocks.
	assert.Equal(t, "18014398509481984", formatCount(^uint64(0), unitsK, false))

	// Human modes delegate to humanBytes.
	assert.Equal(t, "1.0K", formatCount(1024, unitsHuman1024, false))
	assert.Equal(t, "1.0K", formatCount(1000, unitsHuman1000, false))
}

// TestPercentUsed_NoDivByZero — every combination of zero inputs and
// extreme magnitudes must produce a finite percentage string and never
// panic. percentUsed is called from a hot per-mount loop, so a panic
// would crash the entire df invocation.
func TestPercentUsed_NoDivByZero(t *testing.T) {
	maxU := ^uint64(0)
	cases := []struct{ used, free uint64 }{
		{0, 0},
		{0, maxU},
		{maxU, 0},
		{maxU, maxU},
		{maxU, 1},
		{1, maxU},
		{maxU - 1, 1},
		{maxU / 2, maxU / 2},
		{maxU / 200, maxU / 200},
	}
	for _, c := range cases {
		// Wrap in a func so a panic in any case fails the whole test
		// with a useful message instead of taking down the suite.
		assert.NotPanics(t, func() { _ = percentUsed(c.used, c.free) },
			"u=%d f=%d", c.used, c.free)
	}
}

func TestStringSet(t *testing.T) {
	assert.Nil(t, stringSet(nil))
	assert.Nil(t, stringSet([]string{}))
	got := stringSet([]string{"ext4"})
	assert.Equal(t, map[string]struct{}{"ext4": {}}, got)
	got = stringSet([]string{"ext4", "tmpfs"})
	assert.Contains(t, got, "ext4")
	assert.Contains(t, got, "tmpfs")
	// Comma-separated values are split apart (matches GNU df).
	got = stringSet([]string{"ext4,tmpfs", "xfs"})
	assert.Contains(t, got, "ext4")
	assert.Contains(t, got, "tmpfs")
	assert.Contains(t, got, "xfs")
	// Empty fragments are silently dropped.
	got = stringSet([]string{",,ext4,,"})
	assert.Equal(t, map[string]struct{}{"ext4": {}}, got)
}

// keep is a small helper that runs makePreStatFilter against a fixture
// slice and returns the survivors. Mirrors what diskstats.List does
// internally between mountinfo parsing and statfs.
func keep(in []diskstats.Mount, f *flags) []diskstats.Mount {
	pred := makePreStatFilter(f)
	out := make([]diskstats.Mount, 0, len(in))
	for _, m := range in {
		if pred(m) {
			out = append(out, m)
		}
	}
	return out
}

func TestPreStatFilter_DefaultDropsPseudo(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/", FSType: "ext4", Local: true},
		{MountPoint: "/proc", FSType: "proc", Pseudo: true},
		{MountPoint: "/dev", FSType: "devtmpfs", Pseudo: true},
		{MountPoint: "/mnt/nfs", FSType: "nfs", Local: false},
	}
	out := keep(in, &flags{
		all:          ptrBool(false),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string(nil)),
		excludeTypes: ptrSlice([]string(nil)),
	})
	assert.Len(t, out, 2)
	assert.Equal(t, "/", out[0].MountPoint)
	assert.Equal(t, "/mnt/nfs", out[1].MountPoint)
}

func TestPreStatFilter_AllIncludesPseudo(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/", FSType: "ext4", Local: true},
		{MountPoint: "/proc", FSType: "proc", Pseudo: true},
	}
	out := keep(in, &flags{
		all:          ptrBool(true),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string(nil)),
		excludeTypes: ptrSlice([]string(nil)),
	})
	assert.Len(t, out, 2)
}

func TestPreStatFilter_LocalDropsRemote(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/", FSType: "ext4", Local: true},
		{MountPoint: "/mnt/nfs", FSType: "nfs", Local: false},
	}
	out := keep(in, &flags{
		all:          ptrBool(true),
		local:        ptrBool(true),
		includeTypes: ptrSlice([]string(nil)),
		excludeTypes: ptrSlice([]string(nil)),
	})
	assert.Len(t, out, 1)
	assert.Equal(t, "/", out[0].MountPoint)
}

// An explicit -t TYPE filter must override the default pseudo-FS
// suppression so scripts running `df -t tmpfs` see tmpfs mounts even
// without -a. Matches GNU df behaviour.
func TestPreStatFilter_TypeIncludeOverridesPseudoSuppression(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/", FSType: "ext4", Local: true},
		{MountPoint: "/dev/shm", FSType: "tmpfs", Pseudo: true, Local: true},
		{MountPoint: "/run", FSType: "tmpfs", Pseudo: true, Local: true},
	}
	out := keep(in, &flags{
		all:          ptrBool(false),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string{"tmpfs"}),
		excludeTypes: ptrSlice([]string(nil)),
	})
	assert.Len(t, out, 2)
	for _, m := range out {
		assert.Equal(t, "tmpfs", m.FSType)
	}
}

func TestPreStatFilter_TypeExcludeWinsOverIncludeOnPseudo(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/dev/shm", FSType: "tmpfs", Pseudo: true},
	}
	out := keep(in, &flags{
		all:          ptrBool(false),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string{"tmpfs"}),
		excludeTypes: ptrSlice([]string{"tmpfs"}),
	})
	assert.Empty(t, out)
}

func TestPreStatFilter_TypeIncludeAndExclude(t *testing.T) {
	in := []diskstats.Mount{
		{MountPoint: "/a", FSType: "ext4", Local: true},
		{MountPoint: "/b", FSType: "ext4", Local: true},
		{MountPoint: "/c", FSType: "btrfs", Local: true},
		{MountPoint: "/d", FSType: "xfs", Local: true},
	}
	out := keep(in, &flags{
		all:          ptrBool(true),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string{"ext4", "xfs"}),
		excludeTypes: ptrSlice([]string(nil)),
	})
	assert.Len(t, out, 3) // both ext4 + xfs

	out = keep(in, &flags{
		all:          ptrBool(true),
		local:        ptrBool(false),
		includeTypes: ptrSlice([]string{"ext4", "xfs"}),
		excludeTypes: ptrSlice([]string{"ext4"}),
	})
	assert.Len(t, out, 1)
	assert.Equal(t, "xfs", out[0].FSType)
}

// filterMounts now only handles dedup. With -a, every mount is kept;
// without -a, mounts sharing a Source are collapsed to the first.
func TestFilterMounts_DedupBySourceWithoutAll(t *testing.T) {
	in := []diskstats.Mount{
		{Source: "overlay", MountPoint: "/etc/hosts", FSType: "overlay"},
		{Source: "overlay", MountPoint: "/etc/hostname", FSType: "overlay"},
		{Source: "overlay", MountPoint: "/etc/resolv.conf", FSType: "overlay"},
		{Source: "/dev/sda1", MountPoint: "/", FSType: "ext4"},
	}
	out := filterMounts(append([]diskstats.Mount(nil), in...), &flags{
		all: ptrBool(false),
	})
	assert.Len(t, out, 2, "duplicate overlay mounts collapsed to one")
	assert.Equal(t, "/etc/hosts", out[0].MountPoint)
	assert.Equal(t, "/", out[1].MountPoint)
}

// With -a, dedup is disabled (matches GNU df --all).
func TestFilterMounts_AllPreservesDuplicates(t *testing.T) {
	in := []diskstats.Mount{
		{Source: "overlay", MountPoint: "/etc/hosts", FSType: "overlay"},
		{Source: "overlay", MountPoint: "/etc/hostname", FSType: "overlay"},
	}
	out := filterMounts(append([]diskstats.Mount(nil), in...), &flags{
		all: ptrBool(true),
	})
	assert.Len(t, out, 2, "-a must preserve duplicates")
}

// Empty Source is unusual but possible for some pseudo filesystems;
// dedup should not collapse mounts with empty Source onto each other.
func TestFilterMounts_EmptySourceNotDeduped(t *testing.T) {
	in := []diskstats.Mount{
		{Source: "", MountPoint: "/a", FSType: "tmpfs"},
		{Source: "", MountPoint: "/b", FSType: "tmpfs"},
	}
	out := filterMounts(append([]diskstats.Mount(nil), in...), &flags{
		all: ptrBool(false),
	})
	assert.Len(t, out, 2)
}

func TestBuildHeader(t *testing.T) {
	// Default: Filesystem 1K-blocks Used Available Use% Mounted on
	h := buildHeader(false, false, false, unitsK)
	assert.Equal(t, []string{"Filesystem", "1K-blocks", "Used", "Available", "Use%", "Mounted on"}, h)

	// -P: Filesystem 1024-blocks Used Available Capacity Mounted on
	h = buildHeader(true, false, false, unitsK)
	assert.Equal(t, []string{"Filesystem", "1024-blocks", "Used", "Available", "Capacity", "Mounted on"}, h)

	// -h: column 1 is "Size" instead of blocks
	h = buildHeader(false, false, false, unitsHuman1024)
	assert.Contains(t, h, "Size")

	// -T: inserts Type column
	h = buildHeader(false, true, false, unitsK)
	assert.Equal(t, "Type", h[1])

	// -i: inode columns
	h = buildHeader(false, false, true, unitsK)
	assert.Equal(t, []string{"Filesystem", "Inodes", "IUsed", "IFree", "IUse%", "Mounted on"}, h)

	// -i -P: inode columns but POSIX renames the percentage column
	h = buildHeader(true, false, true, unitsK)
	assert.Contains(t, h, "Capacity")

	// -i -T: inode columns + Type column inserted after Filesystem.
	h = buildHeader(false, true, true, unitsK)
	assert.Equal(t, []string{"Filesystem", "Type", "Inodes", "IUsed", "IFree", "IUse%", "Mounted on"}, h)

	// -P -h: human suffix overrides the fixed-block POSIX label, so
	// "Size" appears even when -P is set. Matches GNU `df -P -h`.
	h = buildHeader(true, false, false, unitsHuman1024)
	assert.Equal(t, "Size", h[1])
	assert.NotContains(t, h, "1024-blocks")

	// -P -H: same for SI mode.
	h = buildHeader(true, false, false, unitsHuman1000)
	assert.Equal(t, "Size", h[1])
}

func TestSelectColumns(t *testing.T) {
	m := diskstats.Mount{
		Total: 1000, Used: 200, Free: 800,
		Inodes: 50, InodesUsed: 10, InodesFree: 40,
	}
	// block mode
	a, b, c := selectColumns(m, false)
	assert.Equal(t, uint64(1000), a)
	assert.Equal(t, uint64(200), b)
	assert.Equal(t, uint64(800), c)

	// inode mode
	a, b, c = selectColumns(m, true)
	assert.Equal(t, uint64(50), a)
	assert.Equal(t, uint64(10), b)
	assert.Equal(t, uint64(40), c)
}

func ptrBool(v bool) *bool          { return &v }
func ptrSlice(v []string) *[]string { return &v }
