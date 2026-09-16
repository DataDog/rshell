// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package wineventlog wraps a minimal subset of the Windows Event Log API
// (wevtapi.dll) needed by the get-winevent builtin. It exposes pull-style
// queries (EvtQuery / EvtNext / EvtRender) and channel / publisher
// enumeration. No subscription, no remoting, no writes.
//
// On non-Windows platforms the package compiles to stubs that return an
// "not supported" error; the get-winevent builtin short-circuits before
// reaching them.
package wineventlog

// Query describes a single read-only event log query. It supports three
// shapes, selected by the Is* flags:
//
//   - Channel query (default): Target is a channel name (e.g. "System"),
//     XPath is an optional filter evaluated against that channel.
//   - File query (IsFile): Target is an absolute filesystem path to a
//     .evtx/.etl file. The caller is responsible for having validated
//     the path against any sandbox before calling Run — this package
//     opens the file via wevtapi, not via any sandbox wrapper.
//   - Structured query (IsStructured): Target is a <QueryList> XML
//     string as defined by the Windows Event Log schema. XPath must be
//     empty (the XPath is embedded inside the QueryList). EvtQuery is
//     called with a NULL path, so one structured query can span
//     multiple channels and filter by provider, event ID, keywords,
//     etc. See the FilterXml field of Get-WinEvent for the same
//     feature in PowerShell.
//
// IsFile and IsStructured are mutually exclusive.
type Query struct {
	Target       string
	IsFile       bool
	IsStructured bool
	XPath        string
	MaxEvents    int64
	Oldest       bool
	BatchSize    int
	MaxBytes     int

	// NoMessage skips publisher-metadata resolution and EvtFormatMessage
	// for every event. Those two calls dominate query cost — the metadata
	// handle is cached per provider, but EvtFormatMessage runs per event
	// and has to load and expand the publisher's message-template
	// resources. With NoMessage set, Event.Message carries the flattened
	// EventData text instead, which is already available from the rendered
	// XML at no extra cost. This is exactly the fallback used when a
	// provider's metadata cannot be resolved, so the output shape does not
	// change.
	NoMessage bool

	// VerifyAfterOpen, when non-nil, is invoked once after EvtQuery has
	// opened the target but before any event is read. A non-nil error
	// aborts the query with zero events delivered to OnEvent.
	//
	// This is the hook that makes a file query's sandbox check effectively
	// atomic. EvtQuery takes a path string, so the Event Log service
	// re-resolves the name; the caller cannot hand it an already-validated
	// handle. But EvtQuery opens the file eagerly and holds it in a share
	// mode that denies both rename and delete of that name, so once
	// EvtQuery has returned, the name is pinned to the object the service
	// opened. Re-checking the file's identity at that point therefore
	// reveals whether the object the service has open is the same one the
	// caller validated — and because nothing was read yet, a mismatch can
	// still be refused instead of merely reported.
	VerifyAfterOpen func() error
}

// Event is a rendered event record in the shape consumed by get-winevent's
// output formatter. Fields are plain strings / ints so callers do not need to
// touch any windows-only type.
type Event struct {
	// TimeCreated is the event time normalized to second-precision UTC
	// ("2006-01-02T15:04:05Z"). Sub-second precision is intentionally
	// dropped here for column-friendly TSV display. Consumers that need
	// 100 ns precision should read Event.System.TimeCreated.SystemTime
	// from the EventTree-decoded JSONL output, which preserves the raw
	// XML attribute verbatim.
	TimeCreated  string
	Level        string
	EventID      uint32
	RecordID     uint64
	ProviderName string
	Message      string

	// Fields below back the non-default --Columns selections. All are
	// extracted from the same rendered XML as the fields above, so
	// requesting them costs nothing extra at query time. Names mirror the
	// PowerShell EventLogRecord property they correspond to, which is not
	// always the XML element name (LogName is <Channel>, MachineName is
	// <Computer>).
	LogName     string // System/Channel
	MachineName string // System/Computer
	UserID      string // System/Security/@UserID (SID, may be empty)
	ProcessID   uint32 // System/Execution/@ProcessID
	ThreadID    uint32 // System/Execution/@ThreadID
	Task        uint32 // System/Task
	Opcode      uint32 // System/Opcode
	Keywords    string // System/Keywords, verbatim (e.g. "0x8080000000000000")
	Version     uint32 // System/Version
	ActivityID  string // System/Correlation/@ActivityID (GUID, may be empty)

	// EventData is the flattened <EventData>/<Data> text, space-joined.
	// This is what Message falls back to when the publisher's formatted
	// message cannot be resolved (or when NoMessage is set); exposing it
	// as its own column lets callers read the raw insertion strings even
	// when a formatted Message is available.
	EventData string

	// Raw is the verbatim EvtRender EventXml string for this event, or
	// empty if the caller did not request it. Populated by default so
	// --jsonl consumers can convert it via EventTree; cheap because the
	// string is already allocated in the render path. Bounded by
	// Query.MaxBytes.
	Raw string
}

// OnEvent is invoked for each rendered event. Returning a non-nil error
// aborts the query; ctx cancellation should surface as ctx.Err().
type OnEvent func(Event) error

// OnWarn is invoked for per-event render failures that do not abort the
// query (e.g. an oversized event). May be nil to discard.
type OnWarn func(string)
