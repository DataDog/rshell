// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package stat implements the stat builtin command.
//
// stat — report file system status
//
// Usage: stat -f FILE...
//
// Display file system information for each FILE. This initial implementation
// deliberately supports only GNU stat's file-system mode; ordinary per-file
// metadata and custom format strings are out of scope.
//
// Every operand is resolved through CallContext.FileSystemStat, so user-supplied
// paths remain subject to the shell's AllowedPaths restrictions.
//
// Accepted flags:
//
//	-f, --file-system
//	    Display file system status instead of file status. Required in this
//	    initial implementation.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Exit codes:
//
//	0  Every operand was reported successfully.
//	1  Invalid arguments or at least one operand failed. Processing continues
//	   after per-operand failures.
package stat

import (
	"context"
	"fmt"
	"strconv"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/flagparser"
)

// Cmd is the stat builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "stat",
	Description: "report file system status",
	MakeFlags:   makeFlags,
}

func makeFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	fileSystem := flagparser.RegisterNoArgBool(fs, "file-system", "f", "display file system status")
	help := flagparser.RegisterNoArgBool(fs, "help", "h", "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, paths []string) builtins.Result {
		if *help {
			printHelp(callCtx, fs)
			return builtins.Result{}
		}

		if !*fileSystem {
			callCtx.Errf("stat: file status mode is not supported; use 'stat -f FILE...'\n")
			return builtins.Result{Code: 1}
		}

		if len(paths) == 0 {
			callCtx.Errf("stat: missing operand\n")
			callCtx.Errf("Try 'stat --help' for more information.\n")
			return builtins.Result{Code: 1}
		}

		if callCtx.FileSystemStat == nil {
			callCtx.Errf("stat: file system status capability not available\n")
			return builtins.Result{Code: 1}
		}

		failed := false
		for _, path := range paths {
			if err := ctx.Err(); err != nil {
				return builtins.Result{Code: 1}
			}

			if path == "-" {
				callCtx.Errf("stat: using '-' to denote standard input does not work in file system mode\n")
				failed = true
				continue
			}

			info, err := callCtx.FileSystemStat(ctx, path)
			if err != nil {
				callCtx.Errf("stat: cannot read file system information for %s: %s\n", quotePath(path), portableError(callCtx, err))
				failed = true
				continue
			}

			writeFileSystemInfo(callCtx, path, info)
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

func writeFileSystemInfo(callCtx *builtins.CallContext, path string, info builtins.FileSystemInfo) {
	id := unavailableOr(info.ID, info.IDAvailable, "-", 16)
	nameMax := unavailableOr(info.NameMax, info.NameMaxAvailable, "?", 10)
	files := unavailableOr(info.Files, info.FilesAvailable, "-", 10)
	filesFree := unavailableOr(info.FilesFree, info.FilesAvailable, "-", 10)

	typeName := info.TypeName
	if typeName == "" {
		if info.TypeIDAvailable {
			typeName = fmt.Sprintf("UNKNOWN (0x%x)", info.TypeID)
		} else {
			typeName = "?"
		}
	}

	callCtx.Outf("  File: %s\n", quotePath(path))
	callCtx.Outf("    ID: %-8s Namelen: %-7s Type: %s\n", id, nameMax, typeName)
	callCtx.Outf("Block size: %-10d Fundamental block size: %d\n", info.IOBlockSize, info.FundamentalBlockSize)
	callCtx.Outf("Blocks: Total: %-10d Free: %-10d Available: %d\n", info.Blocks, info.BlocksFree, info.BlocksAvailable)
	callCtx.Outf("Inodes: Total: %-10s Free: %s\n", files, filesFree)
}

func unavailableOr(value uint64, available bool, unavailable string, base int) string {
	if !available {
		return unavailable
	}
	return strconv.FormatUint(value, base)
}

// quotePath uses Go's quoted-string form so newlines and other control
// characters in an operand cannot forge additional output or diagnostic lines.
func quotePath(path string) string {
	return strconv.Quote(path)
}

func portableError(callCtx *builtins.CallContext, err error) string {
	if callCtx.PortableErr != nil {
		return callCtx.PortableErr(err)
	}
	return err.Error()
}

func printHelp(callCtx *builtins.CallContext, fs *builtins.FlagSet) {
	callCtx.Out("Usage: stat -f FILE...\n")
	callCtx.Out("Display file system status for each FILE.\n")
	callCtx.Out("Ordinary file status mode is not supported.\n\n")

	// RegisterNoArgBool uses an unforgeable NUL sentinel for bare flags.
	// Clear it while rendering defaults so help output contains no NUL byte.
	saved := make(map[*builtins.Flag]string)
	fs.VisitAll(func(flag *builtins.Flag) {
		if flag.NoOptDefVal == flagparser.NoArgSentinel {
			saved[flag] = flag.NoOptDefVal
			flag.NoOptDefVal = ""
		}
	})
	defer func() {
		for flag, value := range saved {
			flag.NoOptDefVal = value
		}
	}()

	fs.SetOutput(callCtx.Stdout)
	fs.PrintDefaults()
}
