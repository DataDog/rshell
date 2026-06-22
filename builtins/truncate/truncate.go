// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package truncate implements the truncate builtin command.
//
// truncate — shrink or extend the size of a file to a specified size
//
// Usage: truncate [OPTION]... FILE...
//
// Shrink or extend the size of each FILE to the specified size. A file that
// is larger than the specified size is truncated; a file that is smaller is
// extended (the extension reads as zero bytes). When the file does not yet
// exist it is created (mode 0666 & ~umask) unless --no-create is given.
//
// All file operations go through the AllowedPaths sandbox. Targets outside
// the sandbox are rejected with a permission error before any open syscall
// is issued. This command is only available in remediation mode.
//
// Accepted flags:
//
//	-s SIZE, --size=SIZE
//	    Set the file size to SIZE bytes. SIZE is a non-negative integer
//	    with an optional suffix.
//
//	    For K/M/G/T the leading letter is case-insensitive; for P/E the
//	    leading letter is uppercase-only (matching GNU truncate exactly).
//	    The trailing "B" and "iB" characters are always case-sensitive:
//
//	        K = k = KiB = kiB = 1024         KB = kB = 1000
//	        M = m = MiB = miB = 1024^2       MB = mB = 1000^2
//	        G = g = GiB = giB = 1024^3       GB = gB = 1000^3
//	        T = t = TiB = tiB = 1024^4       TB = tB = 1000^4
//	        P = PiB = 1024^5                 PB = 1000^5
//	        E = EiB = 1024^6                 EB = 1000^6
//
//	    Z/Y/R/Q (zetta/yotta/ronna/quetta) are rejected because their
//	    multipliers exceed int64. GNU coreutils with the standard 64-bit
//	    uintmax_t rejects these as well.
//
//	-c, --no-create
//	    Do not create files that do not already exist. Missing files are
//	    silently skipped (matching GNU truncate).
//
//	-h, --help
//	    Print this usage message to stdout and exit 0.
//
// Out of scope (not implemented; rejected as unknown flags):
//
//	-r REF, --reference=FILE   set size from a reference file
//	-o, --io-blocks            treat SIZE as a block count
//	relative size modifiers in -s (+, -, <, >, /, %)
//
// Exit codes:
//
//	0  All files processed successfully.
//	1  At least one file failed (invalid size, permission denied, missing
//	   file without -c, etc.). Processing continues across all operands so
//	   that a single failure does not abort the run; exit 1 is returned at
//	   the end if any operand failed.
//
// Memory safety:
//
//	truncate performs no I/O on file contents — only metadata. The sandbox
//	opens the file with O_WRONLY (+ O_CREATE when allowed) and calls
//	ftruncate(2) on the resulting fd. No buffers are allocated proportional
//	to user input; the only user-controlled numeric is the size argument,
//	which is validated for overflow before reaching the kernel.
package truncate

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the truncate builtin command descriptor.
var Cmd = builtins.Command{
	Name:            "truncate",
	Description:     "shrink or extend file size",
	MakeFlags:       registerFlags,
	RemediationOnly: true,
}

// errInvalidSize is returned by parseSize for any non-numeric, malformed,
// or out-of-range input.
var errInvalidSize = errors.New("invalid size")

// errRelativeSize is returned by parseSize when the size value carries a
// leading +/-/<>//%/% modifier. We surface a dedicated error so the handler
// can hint that these forms are intentionally not supported.
var errRelativeSize = errors.New("relative size operators not supported")

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	sizeStr := fs.StringP("size", "s", "", "set file size to SIZE bytes")
	noCreate := fs.BoolP("no-create", "c", false, "do not create files that do not exist")

	return func(ctx context.Context, callCtx *builtins.CallContext, files []string) builtins.Result {
		// Capability check before everything else — including --help — so that
		// truncate --help behaves the same as invoking a disallowed command:
		// it fails immediately without showing help text.
		if callCtx.Truncate == nil {
			callCtx.Errf("truncate: filesystem capability not available (remediation mode required)\n")
			return builtins.Result{Code: 1}
		}

		if *help {
			callCtx.Out("Usage: truncate [OPTION]... FILE...\n")
			callCtx.Out("Shrink or extend the size of each FILE to the specified size.\n")
			callCtx.Out("A FILE smaller than SIZE is extended with zero bytes; a FILE\n")
			callCtx.Out("larger than SIZE is truncated. Missing files are created unless\n")
			callCtx.Out("--no-create is given.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if !fs.Changed("size") {
			callCtx.Errf("truncate: you must specify --size\n")
			return builtins.Result{Code: 1}
		}

		size, err := parseSize(*sizeStr)
		if err != nil {
			if errors.Is(err, errRelativeSize) {
				callCtx.Errf("truncate: invalid size %q: %s\n", *sizeStr, err)
			} else {
				callCtx.Errf("truncate: invalid size %q\n", *sizeStr)
			}
			return builtins.Result{Code: 1}
		}

		if len(files) == 0 {
			callCtx.Errf("truncate: missing file operand\n")
			return builtins.Result{Code: 1}
		}

		var failed bool
		for _, file := range files {
			if ctx.Err() != nil {
				return builtins.Result{Code: 1}
			}
			err := callCtx.Truncate(ctx, file, size, !*noCreate)
			if err == nil {
				continue
			}
			if *noCreate && errors.Is(err, os.ErrNotExist) {
				continue
			}
			// The underlying failure can come from open (permission
			// denied, not-a-regular-file, ENOENT without -c) or from
			// ftruncate (ENOSPC, EFBIG, EINVAL); use a phase-neutral
			// message so the operator is not misled when an open
			// succeeded but the size change failed.
			callCtx.Errf("truncate: %q: %s\n", file, callCtx.PortableErr(err))
			failed = true
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

// sizeMultipliers maps suffix tokens accepted by -s to their byte
// multipliers, matching GNU coreutils:
//
//   - For K/M/G/T the leading letter is case-insensitive (K=k, M=m, G=g, T=t)
//     and lowercase-leading multi-letter forms ("kB", "kiB", ...) are accepted.
//   - For P/E the leading letter is uppercase-only (GNU rejects "1p", "1e",
//     etc.; we match that exactly).
//   - In every form, the trailing characters are case-sensitive: "B" must be
//     uppercase and "iB" must be exactly "iB".
//
// Z/Y/R/Q are intentionally NOT supported: their multipliers (1024^7+) exceed
// int64. GNU coreutils rejects these too on 64-bit systems.
//
// "" maps to 1 so a bare digit string falls through with no multiplication.
var sizeMultipliers = map[string]int64{
	"":    1,
	"K":   1 << 10,
	"k":   1 << 10,
	"KiB": 1 << 10,
	"kiB": 1 << 10,
	"KB":  1000,
	"kB":  1000,
	"M":   1 << 20,
	"m":   1 << 20,
	"MiB": 1 << 20,
	"miB": 1 << 20,
	"MB":  1000 * 1000,
	"mB":  1000 * 1000,
	"G":   1 << 30,
	"g":   1 << 30,
	"GiB": 1 << 30,
	"giB": 1 << 30,
	"GB":  1000 * 1000 * 1000,
	"gB":  1000 * 1000 * 1000,
	"T":   1 << 40,
	"t":   1 << 40,
	"TiB": 1 << 40,
	"tiB": 1 << 40,
	"TB":  1000 * 1000 * 1000 * 1000,
	"tB":  1000 * 1000 * 1000 * 1000,
	// P and E: uppercase-only leading letter, matching GNU.
	"P":   1 << 50,
	"PiB": 1 << 50,
	"PB":  1000 * 1000 * 1000 * 1000 * 1000,
	"E":   1 << 60,
	"EiB": 1 << 60,
	"EB":  1000 * 1000 * 1000 * 1000 * 1000 * 1000,
}

// parseSize parses the value of -s/--size into a non-negative byte count.
//
// The grammar matches GNU truncate:
//
//	size := digit+ suffix?
//	suffix := [Kk] | [Kk]B | [Kk]iB |
//	          [Mm] | [Mm]B | [Mm]iB |
//	          [Gg] | [Gg]B | [Gg]iB |
//	          [Tt] | [Tt]B | [Tt]iB |
//	          P    | PB    | PiB    |
//	          E    | EB    | EiB
//
// Leading +/-/<>//% modifiers (the GNU relative-size syntax) are rejected
// with errRelativeSize so the caller can surface a hint. Any other malformed
// input, or any value whose product overflows int64, returns errInvalidSize.
func parseSize(s string) (int64, error) {
	if s == "" {
		return 0, errInvalidSize
	}
	switch s[0] {
	case '+', '-', '<', '>', '/', '%':
		return 0, errRelativeSize
	}

	// Locate the digit/suffix boundary.
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0, errInvalidSize
	}
	digits, suffix := s[:i], s[i:]

	mult, ok := sizeMultipliers[suffix]
	if !ok {
		return 0, errInvalidSize
	}

	// ParseInt (not ParseUint): the leading-'+'/'-' check above already
	// rejected any relative-size prefixes, so the only way ParseInt could
	// produce a negative result is if the digit string wrapped past MaxInt64,
	// which ParseInt catches as ErrRange. ParseUint would require an extra
	// bounds check after the call; ParseInt covers both cases in one step.
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		// Only reachable on integer overflow (ErrRange); the digit string
		// is guaranteed all-ASCII digits by the loop above.
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
