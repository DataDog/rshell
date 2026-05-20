// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package logrotate implements a guarded logrotate command.
package logrotate

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the logrotate builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "logrotate",
	Description: "rotate one allowed log path",
	MakeFlags:   registerFlags,
}

func printUsage(callCtx *builtins.CallContext) {
	callCtx.Out("Usage: logrotate [--json] PATH\n")
	callCtx.Out("Rotate PATH using the scenario-provided logrotate wrapper.\n")
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
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
			callCtx.Errf("logrotate: expected exactly one path\n")
			return builtins.Result{Code: 1}
		}
		if callCtx.OpenExistingFileForWrite == nil {
			callCtx.Errf("logrotate: file write is not available\n")
			return builtins.Result{Code: 1}
		}
		if !builtins.HostExtraFilesSupported() {
			callCtx.Errf("logrotate: host file descriptor handoff is not supported on this platform\n")
			return builtins.Result{Code: 1}
		}
		f, err := callCtx.OpenExistingFileForWrite(ctx, args[0])
		if err != nil {
			callCtx.Errf("logrotate: %s: %s\n", args[0], callCtx.PortableErr(err))
			return builtins.Result{Code: 1}
		}
		if *jsonFlag {
			info, err := f.Stat()
			if err != nil {
				f.Close()
				callCtx.Errf("logrotate: %s: %s\n", args[0], callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			return runJSON(ctx, callCtx, args[0], info.Size(), f)
		}
		return callCtx.InvokeHostCommandWithFiles(ctx, "logrotate", []string{"--", builtins.HostExtraFilePath(0)}, []*os.File{f})
	}
}

type receipt struct {
	Path        string `json:"path"`
	RotatedPath string `json:"rotated_path"`
	BytesBefore int64  `json:"bytes_before"`
	BytesAfter  int64  `json:"bytes_after"`
	ExitCode    uint8  `json:"exit_code"`
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
}

type rotateCandidate struct {
	size    int64
	modNano int64
	id      builtins.FileID
	hasID   bool
}

func runJSON(ctx context.Context, callCtx *builtins.CallContext, path string, bytesBefore int64, f *os.File) builtins.Result {
	before := collectRotateCandidates(ctx, callCtx, path)
	host, res, ok := callCtx.CaptureHostCommandWithFiles(ctx, "logrotate", []string{"--", builtins.HostExtraFilePath(0)}, []*os.File{f})
	if !ok {
		return res
	}
	afterInfo, err := callCtx.StatFile(ctx, path)
	if err != nil {
		callCtx.Errf("logrotate: %s: %s\n", path, callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	after := collectRotateCandidates(ctx, callCtx, path)
	outRes := callCtx.OutJSON(receipt{
		Path:        path,
		RotatedPath: discoverRotatedPath(before, after),
		BytesBefore: bytesBefore,
		BytesAfter:  afterInfo.Size(),
		ExitCode:    host.Code,
		Stdout:      host.Stdout,
		Stderr:      host.Stderr,
	})
	if outRes.Code != 0 || outRes.Exiting {
		return outRes
	}
	return builtins.Result{Code: host.Code}
}

func collectRotateCandidates(ctx context.Context, callCtx *builtins.CallContext, path string) map[string]rotateCandidate {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	entries, err := callCtx.ReadDir(ctx, dir)
	if err != nil {
		return nil
	}
	out := make(map[string]rotateCandidate)
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, base+".") {
			continue
		}
		candidatePath := joinPath(dir, name)
		info, err := callCtx.StatFile(ctx, candidatePath)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidate := rotateCandidate{
			size:    info.Size(),
			modNano: info.ModTime().UnixNano(),
		}
		if callCtx.FileIdentity != nil {
			candidate.id, candidate.hasID = callCtx.FileIdentity(candidatePath, info)
		}
		out[candidatePath] = candidate
	}
	return out
}

func discoverRotatedPath(before, after map[string]rotateCandidate) string {
	names := make([]string, 0, len(after))
	for name := range after {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := before[name]; !ok {
			return name
		}
	}
	for _, name := range names {
		if !sameCandidate(before[name], after[name]) {
			return name
		}
	}
	return ""
}

func sameCandidate(a, b rotateCandidate) bool {
	if a.size != b.size || a.modNano != b.modNano {
		return false
	}
	if a.hasID != b.hasID {
		return false
	}
	return !a.hasID || a.id == b.id
}

func joinPath(dir, name string) string {
	if dir == "." {
		return name
	}
	return filepath.Join(dir, name)
}
