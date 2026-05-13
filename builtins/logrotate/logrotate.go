// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package logrotate implements a demo-grade logrotate builtin.
//
// logrotate — rotate log files by truncating them in place
//
// Usage: logrotate [OPTION]... FILE...
//
// This is a deliberately minimal subset of the real GNU/util-linux logrotate
// tool, intended for agent-driven host remediation (e.g. "free up disk by
// emptying a runaway log file"). It only truncates each FILE through the
// AllowedPaths sandbox; no rename-based rotation, compression, or config
// parsing is performed.
//
// Threshold safety: the size check used by -s and the ftruncate share a
// single open fd via callCtx.TruncateIfLarger, so an attacker with write
// access to the directory cannot swap a small file under the same path
// between a separate stat and truncate to fool the threshold gate.
//
// Accepted flags:
//
//	-s SIZE, --size=SIZE
//	    Only rotate files whose current size is at least SIZE bytes. SIZE
//	    is a non-negative integer with an optional K/M/G/T binary suffix
//	    (1024-based; leading letter case-insensitive) or KB/MB/GB/TB
//	    decimal suffix (1000-based). Files smaller than SIZE are skipped.
//
//	-k N, --keep=N
//	    Informational. Real logrotate keeps N rotated copies; this builtin
//	    has no sandbox capability to rename files, so the flag is recorded
//	    and printed by -v but does not actually retain prior copies. N
//	    must be a non-negative integer.
//
//	-v, --verbose
//	    Print a line per file describing what happened (truncated /
//	    skipped) along with the pre-rotation size and keep count.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  Bad flag value, missing operand, or at least one per-file failure.
//	   Processing continues across operands so a single failure does not
//	   abort the run.
package logrotate

import (
	"context"
	"errors"
	"math"
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the logrotate builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "logrotate",
	Description: "rotate log files by truncating them (demo)",
	MakeFlags:   registerFlags,
}

var errInvalidSize = errors.New("invalid size")

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	sizeStr := fs.StringP("size", "s", "", "only rotate files larger than SIZE bytes")
	keep := fs.IntP("keep", "k", 0, "informational: number of rotated copies to keep")
	verbose := fs.BoolP("verbose", "v", false, "print each rotated or skipped file")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: logrotate [OPTION]... FILE...\n")
			callCtx.Out("Rotate each FILE by truncating it to zero bytes through the\n")
			callCtx.Out("AllowedPaths sandbox. -k is recorded but not enforced because the\n")
			callCtx.Out("sandbox does not currently expose a rename capability.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if callCtx.TruncateIfLarger == nil {
			callCtx.Errf("logrotate: filesystem capability not available\n")
			return builtins.Result{Code: 1}
		}

		var threshold int64
		if fs.Changed("size") {
			n, err := parseSize(*sizeStr)
			if err != nil {
				callCtx.Errf("logrotate: invalid size %q\n", *sizeStr)
				return builtins.Result{Code: 1}
			}
			threshold = n
		}

		if *keep < 0 {
			callCtx.Errf("logrotate: --keep must be >= 0\n")
			return builtins.Result{Code: 1}
		}

		if len(files) == 0 {
			callCtx.Errf("logrotate: missing file operand\n")
			return builtins.Result{Code: 1}
		}

		var failed bool
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}

			// One sandbox call: open, fstat, conditionally ftruncate, all
			// on the same fd. This closes the path-stat to path-truncate
			// TOCTOU window — the size used for the threshold decision is
			// the size of the inode that will actually be truncated.
			sizeBefore, truncated, err := callCtx.TruncateIfLarger(ctx, file, threshold, 0, false)
			if err != nil {
				callCtx.Errf("logrotate: %q: %s\n", file, callCtx.PortableErr(err))
				failed = true
				continue
			}

			if !truncated {
				if *verbose {
					callCtx.Outf("logrotate: %s: %d bytes below threshold %d, skipping\n", file, sizeBefore, threshold)
				}
				continue
			}

			if *verbose {
				callCtx.Outf("logrotate: %s: truncated %d bytes (keep=%d)\n", file, sizeBefore, *keep)
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// parseSize parses a non-negative byte count with an optional K/M/G/T binary
// suffix (1024-based; leading letter case-insensitive) or KB/MB/GB/TB decimal
// suffix (1000-based). Demo-grade: a strict subset of truncate's grammar that
// is enough for the size-threshold use case. Relative-size modifiers and the
// P/E suffixes are intentionally not supported.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, errInvalidSize
	}
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, errInvalidSize
	}
	digits, suffix := s[:i], s[i:]

	var mult int64
	switch suffix {
	case "":
		mult = 1
	case "K", "k", "KiB", "kiB":
		mult = 1 << 10
	case "M", "m", "MiB", "miB":
		mult = 1 << 20
	case "G", "g", "GiB", "giB":
		mult = 1 << 30
	case "T", "t", "TiB", "tiB":
		mult = 1 << 40
	case "KB", "kB":
		mult = 1000
	case "MB", "mB":
		mult = 1000 * 1000
	case "GB", "gB":
		mult = 1000 * 1000 * 1000
	case "TB", "tB":
		mult = 1000 * 1000 * 1000 * 1000
	default:
		return 0, errInvalidSize
	}

	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, errInvalidSize
	}
	if mult == 1 {
		return n, nil
	}
	if n > math.MaxInt64/mult {
		return 0, errInvalidSize
	}
	return n * mult, nil
}
