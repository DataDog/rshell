// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package write_file implements an explicit guarded file-write command.
package write_file

import (
	"context"
	"io"

	"github.com/DataDog/rshell/builtins"
)

// MaxWriteFileBytes caps stdin buffered by write_file.
const MaxWriteFileBytes = 10 << 20 // 10 MiB

// Cmd is the write_file builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "write_file",
	Description: "write stdin to one allowed file",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: write_file [--mode overwrite|append] [--json] FILE\n")
	callCtx.Out("Write stdin to FILE, overwriting by default or appending with --mode append.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	modeFlag := fs.String("mode", "overwrite", "write mode: overwrite or append")
	appendFlag := fs.BoolP("append", "a", false, "append to FILE instead of overwriting")
	jsonFlag := fs.Bool("json", false, "print a structured remediation receipt")
	helpFlag := fs.BoolP("help", "h", false, "print usage and exit")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *helpFlag {
			printUsage(callCtx)
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if len(args) != 1 {
			callCtx.Errf("write_file: expected exactly one file\n")
			return builtins.Result{Code: 1}
		}
		if callCtx.OpenFileForWrite == nil {
			callCtx.Errf("write_file: file write is not available\n")
			return builtins.Result{Code: 1}
		}
		mode := *modeFlag
		if *appendFlag {
			mode = "append"
		}
		appendMode := false
		switch mode {
		case "overwrite":
		case "append":
			appendMode = true
		default:
			callCtx.Errf("write_file: unsupported mode: %s\n", mode)
			return builtins.Result{Code: 1}
		}
		data, res, ok := readInput(callCtx)
		if !ok {
			return res
		}
		path := args[0]
		created := true
		if callCtx.StatFile != nil {
			if _, err := callCtx.StatFile(ctx, path); err == nil {
				created = false
			}
		}
		f, err := callCtx.OpenFileForWrite(ctx, path, appendMode)
		if err != nil {
			callCtx.Errf("write_file: %s: %s\n", path, callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if _, err := f.Write(data); err != nil {
			f.Close()
			callCtx.Errf("write_file: %s: %s\n", path, callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if err := f.Close(); err != nil {
			callCtx.Errf("write_file: %s: %s\n", path, callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if *jsonFlag {
			return printReceipt(ctx, callCtx, path, mode, len(data), created)
		}
		return builtins.Result{}
	}
}

type receipt struct {
	Path         string `json:"path"`
	Mode         string `json:"mode"`
	BytesWritten int    `json:"bytes_written"`
	BytesAfter   int64  `json:"bytes_after"`
	Created      bool   `json:"created"`
	ExitCode     uint8  `json:"exit_code"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
}

func readInput(callCtx *builtins.CallContext) ([]byte, builtins.Result, bool) {
	if callCtx.Stdin == nil {
		return nil, builtins.Result{}, true
	}
	data, err := io.ReadAll(io.LimitReader(callCtx.Stdin, MaxWriteFileBytes+1))
	if err != nil {
		callCtx.Errf("write_file: reading stdin: %s\n", err)
		return nil, builtins.Result{Code: 1}, false
	}
	if len(data) > MaxWriteFileBytes {
		callCtx.Errf("write_file: input exceeds maximum of %d bytes\n", MaxWriteFileBytes)
		return nil, builtins.Result{Code: 1}, false
	}
	return data, builtins.Result{}, true
}

func printReceipt(ctx context.Context, callCtx *builtins.CallContext, path, mode string, bytesWritten int, created bool) builtins.Result {
	var bytesAfter int64
	if callCtx.StatFile != nil {
		info, err := callCtx.StatFile(ctx, path)
		if err != nil {
			callCtx.Errf("write_file: %s: %s\n", path, callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		bytesAfter = info.Size()
	}
	return callCtx.OutJSON(receipt{
		Path:         path,
		Mode:         mode,
		BytesWritten: bytesWritten,
		BytesAfter:   bytesAfter,
		Created:      created,
		ExitCode:     0,
		Stdout:       "",
		Stderr:       "",
	})
}
