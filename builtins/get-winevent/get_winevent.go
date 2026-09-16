// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package get_winevent implements the get-winevent builtin command.
//
// get-winevent — query Windows event logs and .evtx files
//
// Usage: get-winevent [OPTION]...
//
// Read-only, local-only subset of the PowerShell Get-WinEvent cmdlet. Queries
// the live Windows event log service or a .evtx file via wevtapi.dll. Remote
// targets (Get-WinEvent -ComputerName / -Credential) are deliberately
// unsupported: this builtin never opens a network session and the associated
// flags are rejected as unknown.
//
// On non-Windows platforms the command prints a clear error and exits 1.
//
// Accepted flags:
//
//	--LogName <name>
//	    Query the named event log channel (e.g. System, Application). See
//	    --ListLog for the set of valid channel names. Names returned by
//	    --ListProvider are providers, not channels, and cannot be passed
//	    here — use --FilterXml with a <QueryList> selecting on
//	    Provider/@Name to filter by provider.
//	    Mutually exclusive with --Path, --FilterXml, --ListLog, --ListProvider.
//
//	--Path <file>
//	    Read events from a local .evtx file. The path is validated against
//	    the shell's allowed-paths sandbox before handing it to EvtQuery.
//
// Sandbox note for live-log operations:
//
//	--LogName, --ListLog, --ListProvider, and --FilterXml read through
//	wevtapi, which delegates the underlying file I/O to the Windows
//	Event Log service. The backing .evtx files are resolved by the
//	service from system configuration, not from user input to this
//	builtin, so AllowedPaths does not gate these operations — same
//	rationale as ss / ip route reading /proc/net/*. Per-channel
//	granularity (e.g. allow Application but deny Security) could be
//	layered on top of --LogName / --FilterXml parsing if a customer
//	requests it, but is not implemented today. --Path is the
//	exception: the user-supplied .evtx path is validated through
//	callCtx.OpenFile because it is user-controllable.
//
//	--FilterXml <xml>
//	    Full Windows Event Log <QueryList> XML (equivalent to
//	    Get-WinEvent -FilterXml). Selects arbitrary channels and filters
//	    within one query; required for provider-based queries since the
//	    channel must be specified inside the QueryList. Mutually exclusive
//	    with --LogName, --Path, --FilterXPath, --ListLog, --ListProvider.
//	    Capped at 64 KiB.
//
//	--MaxEvents <N>
//	    Maximum number of events to return. Must be >= 1; clamped to 2^31-1.
//	    Defaults to 256 (PowerShell's default is unbounded; we cap for DoS
//	    safety).
//
//	--Oldest
//	    Emit events oldest-first. Required for .etl and some forwarded
//	    logs; default order is newest-first.
//
//	--ListLog
//	    List available event log channel names, one per line, then exit.
//
//	--ListProvider
//	    List available event provider names, one per line, then exit.
//
//	--FilterXPath <expr>
//	    XPath 1.0 expression passed to EvtQuery. Capped at 4 KiB.
//
//	--Columns <list>
//	    Comma-separated list of TSV columns to emit, in the order given.
//	    Names are case-insensitive and may repeat; see
//	    wineventlog.AllColumns for the registry and --help for the
//	    per-column descriptions. Defaults to the historical fixed layout
//	    (TimeCreated,Level,Id,RecordId,ProviderName,Message). Every
//	    selectable column comes out of the same rendered XML the default
//	    columns do, so widening the selection costs nothing extra at query
//	    time. No header row is emitted — the caller already knows the
//	    order it asked for. TSV only: rejected with --jsonl, which always
//	    carries every field.
//
//	--NoMessage
//	    Skip publisher-metadata resolution and EvtFormatMessage. Those are
//	    the dominant per-event cost of a query, so this is the flag to
//	    reach for on large --MaxEvents scans. Message then carries the
//	    flattened EventData text — the same fallback used when a
//	    provider's message resources cannot be resolved. Selecting a
//	    column set that omits Message has the same effect automatically.
//
//	--jsonl
//	    Emit one JSON object per event (JSON Lines / NDJSON) instead of
//	    TSV. Each line has an "Event" field holding the full event XML
//	    converted to a JSON-compatible tree (attributes and child
//	    elements share a key namespace; mixed text+attr elements expose
//	    their body under "value"; repeated same-named children collapse
//	    into an array), and a "Message" field holding the rendered
//	    message. Not allowed with --ListLog / --ListProvider.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Rejected flags (pflag unknown-flag, exit 1):
//
//	-ComputerName, -Credential — remote targeting not supported.
//	-FilterHashtable — PowerShell hashtable input not supported; use
//	  --FilterXml for equivalent expressiveness.
//	-Force, -ProviderName — not implemented. Use --FilterXml with a
//	  <QueryList> selecting on Provider/@Name for provider-based queries.
//
// Output format:
//
//	Events (default):  TimeCreated\tLevel\tId\tRecordId\tProviderName\tMessage
//	                   (override with --Columns)
//	Events (--jsonl):  one JSON object per line (JSON Lines), shape
//	                   {"Event": <xml-as-json>, "Message": <rendered>}
//	--ListLog:         one channel name per line.
//	--ListProvider:    one provider name per line.
//
// Exit codes:
//
//	0  Success (including empty result set).
//	1  Unknown flag, invalid argument, missing/conflicting target, sandbox
//	   denial, wevtapi.dll error, or context cancellation.
//
// Memory safety:
//
//   - EvtNext batch size = 16; total capped by --MaxEvents (<= 2^31-1).
//   - Per-event render buffer capped at 64 KiB; events exceeding that are
//     dropped with a warning to stderr, not fatal.
//   - --FilterXPath capped at 4096 bytes before reaching the Win32 API.
//   - --FilterXml capped at 64 KiB before reaching the Win32 API.
//   - ctx.Err() is checked at the top of every EvtNext iteration.
package get_winevent

import (
	"context"
	"strings"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/wineventlog"
)

// Cmd is the get-winevent builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "get-winevent",
	Description: "query Windows event logs and .evtx files",
	MakeFlags:   registerFlags,
}

// Defaults / caps. Exposed as constants for tests.
const (
	// DefaultMaxEvents is the cap applied when --MaxEvents is not provided.
	DefaultMaxEvents int64 = 256
	// MaxMaxEvents is the ceiling on --MaxEvents values; larger values
	// are clamped to this. Bounded at math.MaxInt32 because EvtNext's
	// Count parameter is a DWORD (uint32) and we never want to need
	// more than one int32-sized count's worth of events.
	MaxMaxEvents int64 = 1<<31 - 1
	// MaxXPathLen is the ceiling on --FilterXPath string length.
	MaxXPathLen = 4096
	// MaxFilterXmlLen is the ceiling on --FilterXml string length. A
	// full QueryList can legitimately be larger than a bare XPath since
	// it may enumerate many channels and filters, but it is still a
	// human-authored query; 64 KiB is comfortably above realistic use.
	MaxFilterXmlLen = 64 * 1024
	// MaxLogNameLen is the ceiling on --LogName / --Path string length.
	MaxLogNameLen = 512
	// EvtNextBatchSize is the number of event handles requested per EvtNext
	// call. Small batches keep context-cancellation latency low.
	EvtNextBatchSize = 16
	// MaxRenderBytes is the hard cap on a single event's rendered XML.
	MaxRenderBytes = 64 * 1024
)

// options holds the resolved flag values after pflag parsing.
type options struct {
	logName      string
	path         string
	xpath        string
	filterXml    string
	maxEvents    int64
	oldest       bool
	listLog      bool
	listProvider bool
	jsonl        bool
	noMessage    bool

	// columns is the resolved TSV column set, populated by validateOptions
	// from --Columns (or DefaultColumns when the flag is absent). Unused in
	// --jsonl mode.
	columns []wineventlog.Column
}

// registerFlags registers all get-winevent flags on the framework-provided
// FlagSet and returns the bound handler.
func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	logName := fs.String("LogName", "", "event log channel name to query")
	path := fs.String("Path", "", "path to a local .evtx file")
	maxEvents := fs.Int64("MaxEvents", DefaultMaxEvents, "maximum number of events to return")
	oldest := fs.Bool("Oldest", false, "return oldest events first")
	listLog := fs.Bool("ListLog", false, "list available event log channels and exit")
	listProvider := fs.Bool("ListProvider", false, "list available event providers and exit")
	xpath := fs.String("FilterXPath", "", "XPath 1.0 expression to filter events")
	filterXml := fs.String("FilterXml", "", "Windows Event Log <QueryList> XML (full structured query)")
	jsonl := fs.Bool("jsonl", false, "emit one JSON object per event (JSON Lines) instead of TSV")
	cols := fs.String("Columns", strings.Join(wineventlog.DefaultColumns, ","), "comma-separated TSV columns to emit (see Columns below)")
	noMessage := fs.Bool("NoMessage", false, "skip formatted-message lookup; Message falls back to EventData")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: get-winevent [OPTION]...\n")
			callCtx.Out("Query Windows event logs and .evtx files (Windows only).\n\n")
			// Group selector flags by PowerShell Get-WinEvent parameter
			// set. Exactly one selector flag (group header in parens) must
			// be supplied; the bracketed flags in each block compose with
			// that selector. Mirrors the Syntax section of the Get-WinEvent
			// cmdlet reference, adapted to the flags this builtin actually
			// supports.
			callCtx.Out("Parameter sets (pick exactly one selector):\n\n")
			callCtx.Out("  Channel query (default):\n")
			callCtx.Out("    get-winevent --LogName NAME [--MaxEvents N] [--Oldest]\n")
			callCtx.Out("                 [--FilterXPath EXPR] [--jsonl]\n\n")
			callCtx.Out("  File query:\n")
			callCtx.Out("    get-winevent --Path FILE [--MaxEvents N] [--Oldest]\n")
			callCtx.Out("                 [--FilterXPath EXPR] [--jsonl]\n\n")
			callCtx.Out("  Structured XML query:\n")
			callCtx.Out("    get-winevent --FilterXml XML [--MaxEvents N] [--Oldest] [--jsonl]\n\n")
			callCtx.Out("  List channels:   get-winevent --ListLog\n")
			callCtx.Out("  List providers:  get-winevent --ListProvider\n\n")
			callCtx.Out("Output:\n")
			callCtx.Out("  Default TSV columns are second-precision UTC; control bytes,\n")
			callCtx.Out("  backslashes, and U+200E are backslash-escaped per column.\n")
			callCtx.Out("  --jsonl preserves full 100ns precision via\n")
			callCtx.Out("  Event.System.TimeCreated.SystemTime, and carries message text\n")
			callCtx.Out("  without TSV escaping (U+200E is still stripped and trailing\n")
			callCtx.Out("  whitespace trimmed, as in TSV).\n\n")
			callCtx.Out("Columns (--Columns, TSV only; case-insensitive, comma-separated):\n")
			for _, c := range wineventlog.AllColumns() {
				callCtx.Outf("  %-13s %s\n", c.Name, c.Desc)
			}
			callCtx.Outf("  Default: %s\n", strings.Join(wineventlog.DefaultColumns, ","))
			callCtx.Out("  No header row is emitted; columns appear in the order requested.\n\n")
			callCtx.Out("Flags:\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		if len(args) > 0 {
			callCtx.Errf("get-winevent: unexpected positional argument: %s\n", args[0])
			return builtins.Result{Code: 1}
		}

		opts := options{
			logName:      *logName,
			path:         *path,
			xpath:        *xpath,
			filterXml:    *filterXml,
			maxEvents:    *maxEvents,
			oldest:       *oldest,
			listLog:      *listLog,
			listProvider: *listProvider,
			jsonl:        *jsonl,
			noMessage:    *noMessage,
		}

		if msg := validateOptions(&opts, *cols, fs.Changed); msg != "" {
			callCtx.Errf("get-winevent: %s\n", msg)
			return builtins.Result{Code: 1}
		}

		return run(ctx, callCtx, opts)
	}
}

// validateOptions checks that a valid selector is set, rejects conflicting
// selectors, and clamps numeric/string inputs. It mutates opts in place to
// apply clamps.
//
// isSet reports whether a flag was explicitly passed (typically
// fs.Changed). Querying pflag directly avoids parallel "haveX" booleans
// on options — pflag stays the single source of truth for "was this
// flag passed". Empty-explicit values (--LogName "") still trip the
// "must not be empty" diagnostic.
func validateOptions(opts *options, colSpec string, isSet func(string) bool) string {
	selectors := 0
	if isSet("LogName") {
		selectors++
	}
	if isSet("Path") {
		selectors++
	}
	if isSet("FilterXml") {
		selectors++
	}
	if opts.listLog {
		selectors++
	}
	if opts.listProvider {
		selectors++
	}
	if selectors == 0 {
		return "one of --LogName, --Path, --FilterXml, --ListLog, --ListProvider is required"
	}
	if selectors > 1 {
		return "only one of --LogName, --Path, --FilterXml, --ListLog, --ListProvider may be used"
	}

	// Reject empty explicit values.
	if isSet("LogName") && strings.TrimSpace(opts.logName) == "" {
		return "--LogName must not be empty"
	}
	if isSet("Path") && strings.TrimSpace(opts.path) == "" {
		return "--Path must not be empty"
	}
	if isSet("FilterXml") && strings.TrimSpace(opts.filterXml) == "" {
		return "--FilterXml must not be empty"
	}

	// Length caps.
	if len(opts.logName) > MaxLogNameLen {
		return "--LogName too long"
	}
	if len(opts.path) > MaxLogNameLen {
		return "--Path too long"
	}
	if len(opts.xpath) > MaxXPathLen {
		return "--FilterXPath too long"
	}
	if len(opts.filterXml) > MaxFilterXmlLen {
		return "--FilterXml too long"
	}

	// --FilterXml is a complete structured query; --FilterXPath must be
	// embedded inside the QueryList rather than stacked on top.
	if isSet("FilterXml") && isSet("FilterXPath") {
		return "--FilterXPath cannot be combined with --FilterXml"
	}

	// Reject FilterXml that doesn't match the QueryList/Query/{Select,Suppress}
	// schema before handing it to wevtapi. Keeps the validator error close to
	// the user-visible flag and ensures malformed input never reaches the
	// Win32 XML parser.
	if isSet("FilterXml") {
		if err := wineventlog.ValidateFilterXml(opts.filterXml); err != nil {
			return "--FilterXml: " + err.Error()
		}
	}

	// MaxEvents: reject <1; clamp over MaxMaxEvents.
	if opts.maxEvents < 1 {
		return "--MaxEvents must be >= 1"
	}
	if opts.maxEvents > MaxMaxEvents {
		opts.maxEvents = MaxMaxEvents
	}

	// --Columns selects TSV fields, so it is meaningless in --jsonl mode:
	// every field is already present in the JSON object. Rejecting the
	// combination is better than silently ignoring the flag.
	if isSet("Columns") && opts.jsonl {
		return "--Columns cannot be used with --jsonl (JSON Lines output always carries every field)"
	}

	// Resolve columns even when --Columns was not passed, so the emitter
	// always has a column set and the default layout goes through exactly
	// the same code path as an explicit selection.
	cols, err := wineventlog.ParseColumns(colSpec)
	if err != nil {
		return "--Columns: " + err.Error()
	}
	opts.columns = cols

	// --FilterXPath, --Oldest, --jsonl, --Columns, and --NoMessage only
	// compose with query selectors.
	if opts.listLog || opts.listProvider {
		if isSet("FilterXPath") {
			return "--FilterXPath cannot be used with --ListLog or --ListProvider"
		}
		if opts.oldest {
			return "--Oldest cannot be used with --ListLog or --ListProvider"
		}
		if opts.jsonl {
			return "--jsonl cannot be used with --ListLog or --ListProvider"
		}
		if isSet("Columns") {
			return "--Columns cannot be used with --ListLog or --ListProvider"
		}
		if opts.noMessage {
			return "--NoMessage cannot be used with --ListLog or --ListProvider"
		}
	}
	return ""
}
