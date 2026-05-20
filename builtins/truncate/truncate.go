// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package truncate implements a guarded truncate command.
package truncate

import (
	"context"
	"os"
	"strconv"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the truncate builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "truncate",
	Description: "shrink an existing regular file to a byte size",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: truncate -s SIZE [--json] FILE\n")
	callCtx.Out("Shrink FILE to SIZE bytes.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	sizeFlag := fs.StringP("size", "s", "", "target byte size")
	jsonFlag := fs.Bool("json", false, "print a structured remediation receipt")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if *sizeFlag == "" {
			callCtx.Errf("truncate: missing -s SIZE\n")
			return builtins.Result{Code: 1}
		}
		if !isDecimalSize(*sizeFlag) {
			callCtx.Errf("truncate: invalid size: %s\n", *sizeFlag)
			return builtins.Result{Code: 1}
		}
		size, err := strconv.ParseInt(*sizeFlag, 10, 64)
		if err != nil {
			callCtx.Errf("truncate: invalid size: %s\n", *sizeFlag)
			return builtins.Result{Code: 1}
		}
		if len(args) != 1 {
			callCtx.Errf("truncate: expected exactly one file\n")
			return builtins.Result{Code: 1}
		}
		if callCtx.OpenExistingFileForWrite == nil {
			callCtx.Errf("truncate: file write is not available\n")
			return builtins.Result{Code: 1}
		}
		if !builtins.HostExtraFilesSupported() {
			callCtx.Errf("truncate: host file descriptor handoff is not supported on this platform\n")
			return builtins.Result{Code: 1}
		}
		f, err := callCtx.OpenExistingFileForWrite(ctx, args[0])
		if err != nil {
			callCtx.Errf("truncate: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			callCtx.Errf("truncate: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if !info.Mode().IsRegular() {
			f.Close()
			callCtx.Errf("truncate: %s: not a regular file\n", args[0])
			return builtins.Result{Code: 1}
		}
		if size > info.Size() {
			f.Close()
			callCtx.Errf("truncate: cannot grow file\n")
			return builtins.Result{Code: 1}
		}
		hostArgs := []string{"-s", strconv.FormatInt(size, 10), "--", builtins.HostExtraFilePath(0)}
		if *jsonFlag {
			return runJSON(ctx, callCtx, args[0], size, info.Size(), hostArgs, f)
		}
		return callCtx.InvokeHostCommandWithFiles(ctx, "truncate", hostArgs, []*os.File{f})
	}
}

type receipt struct {
	Path        string `json:"path"`
	BytesBefore int64  `json:"bytes_before"`
	BytesAfter  int64  `json:"bytes_after"`
	SizeBytes   int64  `json:"size_bytes"`
	ExitCode    uint8  `json:"exit_code"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

func runJSON(ctx context.Context, callCtx *builtins.CallContext, path string, size int64, bytesBefore int64, hostArgs []string, f *os.File) builtins.Result {
	host, res, ok := callCtx.CaptureHostCommandWithFiles(ctx, "truncate", hostArgs, []*os.File{f})
	if !ok {
		return res
	}
	afterInfo, err := callCtx.StatFile(ctx, path)
	if err != nil {
		callCtx.Errf("truncate: %s: %s\n", path, callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	outRes := callCtx.OutJSON(receipt{
		Path:        path,
		BytesBefore: bytesBefore,
		BytesAfter:  afterInfo.Size(),
		SizeBytes:   size,
		ExitCode:    host.Code,
		Stdout:      host.Stdout,
		Stderr:      host.Stderr,
	})
	if outRes.Code != 0 || outRes.Exiting {
		return outRes
	}
	return builtins.Result{Code: host.Code}
}

func isDecimalSize(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
