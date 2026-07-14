//go:build linux || darwin

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package systemd

import (
	"context"
	"fmt"
	"math"
	"os"
	"syscall"

	"github.com/DataDog/rshell/builtins"
)

const journalBlockSize = 512

// JournalDiskUsage reports allocated blocks, matching journalctl's disk-usage
// semantics more closely than summing logical file lengths.
func (c *Client) JournalDiskUsage(ctx context.Context) (builtins.JournalUsage, error) {
	_, files, err := c.journalFiles()
	if err != nil {
		return builtins.JournalUsage{}, err
	}
	usage := builtins.JournalUsage{Files: len(files)}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return builtins.JournalUsage{}, err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return builtins.JournalUsage{}, fmt.Errorf("inspect journal file %q: %w", path, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return builtins.JournalUsage{}, fmt.Errorf("journal file %q changed during usage scan", path)
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Blocks < 0 {
			return builtins.JournalUsage{}, fmt.Errorf("journal file %q has unavailable allocation metadata", path)
		}
		blocks := uint64(stat.Blocks)
		if blocks > math.MaxUint64/journalBlockSize {
			return builtins.JournalUsage{}, fmt.Errorf("journal allocation overflow for %q", path)
		}
		allocated := blocks * journalBlockSize
		if usage.Bytes > math.MaxUint64-allocated {
			return builtins.JournalUsage{}, fmt.Errorf("journal allocation total overflow")
		}
		usage.Bytes += allocated
	}
	return usage, nil
}
