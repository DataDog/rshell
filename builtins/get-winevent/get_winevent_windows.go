// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

package get_winevent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/wineventlog"
)

// run is the Windows implementation. It dispatches to the appropriate
// wineventlog operation based on the selector flag.
func run(ctx context.Context, callCtx *builtins.CallContext, opts options) builtins.Result {
	if ctx.Err() != nil {
		return builtins.Result{Code: 1}
	}

	// validateOptions guarantees exactly one selector is set with a
	// non-empty value, so empty-string checks are sufficient here.
	switch {
	case opts.listLog:
		return runListLog(ctx, callCtx)
	case opts.listProvider:
		return runListProvider(ctx, callCtx)
	case opts.path != "":
		return runQueryFile(ctx, callCtx, opts)
	case opts.filterXml != "":
		return runQueryXml(ctx, callCtx, opts)
	case opts.logName != "":
		return runQueryChannel(ctx, callCtx, opts)
	}
	// Unreachable: validateOptions guarantees one selector.
	callCtx.Errf("get-winevent: no target selected\n")
	return builtins.Result{Code: 1}
}

func runListLog(ctx context.Context, callCtx *builtins.CallContext) builtins.Result {
	names, err := wineventlog.ListChannels(ctx)
	if err != nil {
		callCtx.Errf("get-winevent: %v\n", err)
		return builtins.Result{Code: 1}
	}
	for _, n := range names {
		if ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}
		callCtx.Outf("%s\n", n)
	}
	return builtins.Result{}
}

func runListProvider(ctx context.Context, callCtx *builtins.CallContext) builtins.Result {
	names, err := wineventlog.ListProviders(ctx)
	if err != nil {
		callCtx.Errf("get-winevent: %v\n", err)
		return builtins.Result{Code: 1}
	}
	for _, n := range names {
		if ctx.Err() != nil {
			return builtins.Result{Code: 1}
		}
		callCtx.Outf("%s\n", n)
	}
	return builtins.Result{}
}

func runQueryChannel(ctx context.Context, callCtx *builtins.CallContext, opts options) builtins.Result {
	q := wineventlog.Query{
		Target:    opts.logName,
		IsFile:    false,
		XPath:     opts.xpath,
		MaxEvents: opts.maxEvents,
		Oldest:    opts.oldest,
		BatchSize: EvtNextBatchSize,
		MaxBytes:  MaxRenderBytes,
	}
	return runQuery(ctx, callCtx, q, opts)
}

func runQueryFile(ctx context.Context, callCtx *builtins.CallContext, opts options) builtins.Result {
	// Resolve relative paths against the shell's tracked WorkDir before
	// validating or querying. Without this, OpenFile (which resolves
	// against runner.Dir) and EvtQuery (which resolves against the
	// process CWD) can disagree — see TestPathRelativeUsesShellWorkDir.
	absPath := opts.path
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(callCtx.WorkDir(), absPath)
	}

	// Validate the path against the shell's AllowedPaths sandbox by opening
	// it read-only and immediately closing.
	//
	// A single validation here cannot be atomic with the read: EvtQuery
	// takes a path *string*, not a handle, so the Event Log service
	// re-resolves the name itself. Two things make that gap real rather
	// than theoretical:
	//
	//  1. The two resolutions use different symlink semantics. os.Root
	//     walks component-by-component with FILE_OPEN_REPARSE_POINT and
	//     refuses to traverse a symlink or junction at any component, which
	//     is what makes it a sandbox rather than a string comparison. The
	//     Win32 resolver EvtQuery uses does follow reparse points.
	//  2. Holding this handle open would not help. os.Root opens with
	//     FILE_SHARE_READ|WRITE|DELETE (os/root_windows.go), so a held
	//     handle blocks neither rename nor delete — verified empirically.
	//     Plain os.Open (share R|W, no DELETE) would block both, but it
	//     bypasses AllowedPaths entirely and is forbidden by docs/RULES.md.
	//
	// So instead of trying to hold the object, we confirm afterwards that
	// the service opened the object we validated: capture its identity now,
	// and re-check it in VerifyAfterOpen once EvtQuery has opened and
	// pinned the target but before any event is read (see the
	// Query.VerifyAfterOpen contract and TestEvtQueryPinsTargetFile). A
	// mismatch refuses the query with zero events emitted, so a substituted
	// path is prevented rather than merely reported.
	f, err := callCtx.OpenFile(ctx, absPath, os.O_RDONLY, 0)
	if err != nil {
		callCtx.Errf("get-winevent: %s\n", callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}
	_ = f.Close()

	// Identity of the object we just validated, taken through the sandbox
	// (FileIdentity opens via os.Root and reads the volume serial + file
	// index off the handle, so this is the identity of a real object, not a
	// name).
	wantID, err := sandboxFileID(ctx, callCtx, absPath)
	if err != nil {
		callCtx.Errf("get-winevent: %s\n", callCtx.PortableErr(err))
		return builtins.Result{Code: 1}
	}

	q := wineventlog.Query{
		Target:    absPath,
		IsFile:    true,
		XPath:     opts.xpath,
		MaxEvents: opts.maxEvents,
		Oldest:    opts.oldest,
		BatchSize: EvtNextBatchSize,
		MaxBytes:  MaxRenderBytes,
		VerifyAfterOpen: func() error {
			gotID, err := sandboxFileID(ctx, callCtx, absPath)
			if err != nil {
				return fmt.Errorf("--Path could not be re-validated after open: %w", err)
			}
			if gotID != wantID {
				return errors.New("--Path changed identity between validation and open; refusing to read (possible path substitution)")
			}
			return nil
		},
	}
	return runQuery(ctx, callCtx, q, opts)
}

// sandboxFileID returns the canonical identity (volume serial + file index)
// of absPath, resolved through the AllowedPaths sandbox. An error means the
// path is outside the sandbox, unreadable, or that identity could not be
// determined — all of which are treated as validation failures by callers.
func sandboxFileID(ctx context.Context, callCtx *builtins.CallContext, absPath string) (builtins.FileID, error) {
	info, err := callCtx.StatFile(ctx, absPath)
	if err != nil {
		return builtins.FileID{}, err
	}
	id, ok := callCtx.FileIdentity(absPath, info)
	if !ok {
		return builtins.FileID{}, errors.New("could not determine file identity")
	}
	return id, nil
}

func runQueryXml(ctx context.Context, callCtx *builtins.CallContext, opts options) builtins.Result {
	q := wineventlog.Query{
		Target:       opts.filterXml,
		IsStructured: true,
		MaxEvents:    opts.maxEvents,
		Oldest:       opts.oldest,
		BatchSize:    EvtNextBatchSize,
		MaxBytes:     MaxRenderBytes,
	}
	return runQuery(ctx, callCtx, q, opts)
}

func runQuery(ctx context.Context, callCtx *builtins.CallContext, q wineventlog.Query, opts options) builtins.Result {
	// Resolving the publisher-formatted message is the dominant per-event
	// cost. Skip it when the user asked us to, and also when the selected
	// TSV columns cannot observe it — in that case the value would be
	// computed and thrown away, so skipping is a pure win with no output
	// change. --jsonl always emits Message, so it always needs it.
	q.NoMessage = opts.noMessage || (opts.output == "tsv" && !wineventlog.NeedsMessage(opts.columns))

	warn := func(warn string) {
		callCtx.Errf("get-winevent: %s\n", warn)
	}
	err := wineventlog.Run(ctx, q, func(ev wineventlog.Event) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		row, err := serializeEvent(ev, opts)
		if err != nil {
			// Rendering failures are local to this event. Returning nil keeps
			// wineventlog.Run scanning and means omitted records do not consume
			// its successful-emission MaxEvents budget.
			warn(err.Error())
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		callCtx.Out(string(row))
		return nil
	}, warn)
	if err != nil {
		callCtx.Errf("get-winevent: %v\n", err)
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// serializeEvent constructs a complete output record before it reaches
// stdout. The final-byte check is deliberately here, after TSV/JSON escaping
// and repeated selected columns, rather than relying on the adapter's XML
// render cap.
func serializeEvent(ev wineventlog.Event, opts options) ([]byte, error) {
	if opts.output == "tsv" {
		row := []byte(wineventlog.FormatRow(ev, opts.columns))
		if len(row) > MaxRenderBytes {
			return nil, fmt.Errorf("rendered TSV record exceeds %d bytes", MaxRenderBytes)
		}
		return row, nil
	}
	tree, err := wineventlog.EventTree(ev.Raw)
	if err != nil {
		return nil, err
	}
	tree["Message"] = ev.Message
	row, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("serialize JSON Lines record: %w", err)
	}
	row = append(row, '\n')
	if len(row) > MaxRenderBytes {
		return nil, fmt.Errorf("rendered JSON Lines record exceeds %d bytes", MaxRenderBytes)
	}
	return row, nil
}
