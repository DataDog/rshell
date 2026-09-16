// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

//go:build windows

// This file uses a narrow unsafe exception: unsafe.Pointer is used solely to
// pass buffer addresses and handle arrays to wevtapi.dll through LazyProc.Call.
// No pointer arithmetic is performed after a call; all returned data is
// interpreted via stdlib helpers (windows.UTF16ToString) or parsed as XML.
//
// References: mirrors the EvtQuery / EvtNext / EvtRender wrapper style from
// C:\dd\datadog-agent-main\pkg\util\winutil\eventlog\api\windows\wevtapi.go
// (Apache-2.0, same company). Channel and publisher enumeration are written
// from MSDN documentation — that reference package does not implement them.
package wineventlog

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
)

// wevtapi function handles, loaded lazily on first use.
var (
	modwevtapi = windows.NewLazySystemDLL("wevtapi.dll")

	procEvtQuery                 = modwevtapi.NewProc("EvtQuery")
	procEvtNext                  = modwevtapi.NewProc("EvtNext")
	procEvtRender                = modwevtapi.NewProc("EvtRender")
	procEvtClose                 = modwevtapi.NewProc("EvtClose")
	procEvtOpenChannelEnum       = modwevtapi.NewProc("EvtOpenChannelEnum")
	procEvtNextChannelPath       = modwevtapi.NewProc("EvtNextChannelPath")
	procEvtOpenPublisherEnum     = modwevtapi.NewProc("EvtOpenPublisherEnum")
	procEvtNextPublisherID       = modwevtapi.NewProc("EvtNextPublisherId")
	procEvtOpenPublisherMetadata = modwevtapi.NewProc("EvtOpenPublisherMetadata")
	procEvtFormatMessage         = modwevtapi.NewProc("EvtFormatMessage")
)

// EVT_QUERY_FLAGS values.
const (
	evtQueryChannelPath      = 0x1
	evtQueryFilePath         = 0x2
	evtQueryForwardDirection = 0x100
	evtQueryReverseDirection = 0x200
)

// EVT_RENDER_FLAGS values.
const (
	evtRenderEventXml = 1
)

// EVT_FORMAT_MESSAGE_FLAGS values. We only use the "full formatted event
// message" variant.
const (
	evtFormatMessageEvent = 1
)

// Sentinel error returned by the per-batch helpers when EvtNext signals
// ERROR_NO_MORE_ITEMS. Callers translate this into clean EOF.
var errNoMoreItems = errors.New("wineventlog: ERROR_NO_MORE_ITEMS")

// Handle enumeration / resource ceilings. These are defensive — an attacker
// who can feed an unbounded number of channel names or render gigantic
// events should not be able to exhaust memory through this package.
const (
	maxEnumEntries     = 16384
	maxPathBufferChars = 8192
	// maxPubCacheEntries bounds the per-query publisher-metadata cache.
	// Provider names come from the event XML, so a crafted .evtx can
	// present an unbounded number of distinct ones; without a cap the map
	// (and, for names that do resolve, the open Win32 handles) would grow
	// with event cardinality. Once the cap is reached, message formatting
	// degrades to the EventData fallback instead of opening more handles —
	// the same behaviour as a provider whose metadata cannot be resolved.
	// A real host has a few thousand registered providers, so this is far
	// above any legitimate query.
	maxPubCacheEntries = 4096
)

// evtClose closes a wevtapi handle. Null handles are a no-op.
func evtClose(h uintptr) {
	if h == 0 {
		return
	}
	_, _, _ = procEvtClose.Call(h)
}

// utf16Ptr converts a Go string to a *uint16 or returns nil for empty input.
// wevtapi treats NULL as "no query" / "no publisher" depending on context.
func utf16Ptr(s string) (*uint16, error) {
	if s == "" {
		return nil, nil
	}
	return windows.UTF16PtrFromString(s)
}

// Run executes Query q and invokes onEvent for each rendered event up to
// q.MaxEvents. Returns the first fatal error, or nil on clean completion
// (including when the log is empty).
//
// runtime.LockOSThread is held for the duration of the query so that the
// EvtQuery result-set handle is only manipulated from one OS thread — the
// Datadog Agent package documents this as required to avoid handle
// corruption under contention.
func Run(ctx context.Context, q Query, onEvent OnEvent, onWarn OnWarn) error {
	if q.BatchSize < 1 || q.BatchSize > 1024 {
		return fmt.Errorf("wineventlog: invalid BatchSize %d", q.BatchSize)
	}
	if q.MaxEvents < 1 {
		return fmt.Errorf("wineventlog: invalid MaxEvents %d", q.MaxEvents)
	}
	if q.MaxBytes < 1024 {
		return fmt.Errorf("wineventlog: invalid MaxBytes %d", q.MaxBytes)
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	// For structured queries Path is NULL and the <QueryList> XML goes in
	// Query. For channel/file queries Path is the channel/file and Query
	// is the (optional) XPath filter.
	var pathPtr, queryPtr *uint16
	var flags uintptr
	if q.IsStructured {
		if q.XPath != "" {
			return fmt.Errorf("wineventlog: XPath cannot be combined with a structured query")
		}
		pathPtr = nil
		qp, err := utf16Ptr(q.Target)
		if err != nil {
			return fmt.Errorf("wineventlog: convert structured query: %w", err)
		}
		queryPtr = qp
		// Pin structured queries to channel semantics explicitly. The
		// Path attributes inside a <QueryList> name channels, and with
		// EvtQueryChannelPath set, a Path naming a file is rejected by the
		// service ("The specified channel path is invalid") rather than
		// opened. That matters for the sandbox: --FilterXml content is
		// user-supplied, so if the service resolved those Path attributes
		// as filesystem paths it would be a read primitive that bypasses
		// AllowedPaths. Windows already defaults to channel semantics when
		// no path-kind flag is passed, but relying on an undocumented
		// default for a security property is fragile — EVT_QUERY_FLAGS
		// specifies exactly one path-kind flag, so state it.
		flags |= evtQueryChannelPath
	} else {
		tp, err := utf16Ptr(q.Target)
		if err != nil {
			return fmt.Errorf("wineventlog: convert target: %w", err)
		}
		xp, err := utf16Ptr(q.XPath)
		if err != nil {
			return fmt.Errorf("wineventlog: convert xpath: %w", err)
		}
		pathPtr = tp
		queryPtr = xp
		if q.IsFile {
			flags |= evtQueryFilePath
		} else {
			flags |= evtQueryChannelPath
		}
	}
	if q.Oldest {
		flags |= evtQueryForwardDirection
	} else {
		flags |= evtQueryReverseDirection
	}

	hQuery, _, callErr := procEvtQuery.Call(
		0,
		uintptr(unsafe.Pointer(pathPtr)),
		uintptr(unsafe.Pointer(queryPtr)),
		flags,
	)
	if hQuery == 0 {
		return fmt.Errorf("wineventlog: EvtQuery: %w", callErr)
	}
	defer evtClose(hQuery)

	// The target is now open and pinned by the service. Give the caller a
	// chance to confirm it is the object they validated, before any event
	// is read. See Query.VerifyAfterOpen.
	if q.VerifyAfterOpen != nil {
		if err := q.VerifyAfterOpen(); err != nil {
			return err
		}
	}

	// Publisher-metadata handle cache. Opening a metadata handle is
	// expensive; keep one per provider for the life of the query and
	// close them all in a single defer. A nil entry in the map means
	// the provider has no resolvable metadata (common for transient or
	// uninstalled providers) — we remember that so we don't retry.
	pubCache := make(map[string]uintptr)
	defer func() {
		for _, h := range pubCache {
			evtClose(h)
		}
	}()

	handles := make([]uintptr, q.BatchSize)
	var emitted int64
	for {
		if ctx.Err() != nil {
			// Close any still-open event handles from a partial batch.
			return ctx.Err()
		}
		need := q.MaxEvents - emitted
		if need <= 0 {
			return nil
		}
		batch := int64(q.BatchSize)
		if batch > need {
			batch = need
		}

		got, err := evtNextBatch(hQuery, handles[:batch])
		if err != nil {
			if errors.Is(err, errNoMoreItems) {
				return nil
			}
			return err
		}
		if got == 0 {
			return nil
		}

		for i := 0; i < got; i++ {
			h := handles[i]
			if ctx.Err() != nil {
				// Close remaining handles.
				for j := i; j < got; j++ {
					evtClose(handles[j])
				}
				return ctx.Err()
			}
			ev, err := renderAndParse(h, q.MaxBytes, pubCache, q.NoMessage)
			evtClose(h)
			if err != nil {
				if onWarn != nil {
					onWarn(fmt.Sprintf("render failed: %v", err))
				}
				continue
			}
			emittedEvent, err := onEvent(ev)
			if err != nil {
				// Close the unprocessed remainder of the batch so a
				// downstream failure (e.g. broken pipe) doesn't leak
				// wevtapi handles. Mirrors the ctx-cancellation cleanup
				// above; the current handle h is already closed.
				for j := i + 1; j < got; j++ {
					evtClose(handles[j])
				}
				return err
			}
			if !emittedEvent {
				continue
			}
			emitted++
			if emitted >= q.MaxEvents {
				return nil
			}
		}
	}
}

// evtNextBatch calls EvtNext and returns the number of handles populated.
// Returns errNoMoreItems when the query is exhausted. On any other failure,
// an error is returned and no handles are populated.
//
// Timeout is INFINITE, matching the Datadog Agent's wevtapi wrapper
// (pkg/util/winutil/eventlog/{api/windows/wevtapi.go, bookmark/bookmark.go}).
// Per MS docs, EvtNext on a finite snapshot result set returns at end-of-log
// rather than blocking, so INFINITE does not actually wait forever for our
// query shape (no live subscription). The only safe way to bound a hung
// syscall from Go (orphaning the goroutine) is unsafe given the &handles[0]
// / &returned pointers we pass — see
// open-telemetry/opentelemetry-collector-contrib#47576 for that discussion.
// ctx cancellation is checked between batches instead.
func evtNextBatch(hQuery uintptr, handles []uintptr) (int, error) {
	var returned uint32
	r1, _, callErr := procEvtNext.Call(
		hQuery,
		uintptr(uint32(len(handles))),
		uintptr(unsafe.Pointer(&handles[0])),
		uintptr(windows.INFINITE),
		0,
		uintptr(unsafe.Pointer(&returned)),
	)
	if r1 == 0 {
		if errno, ok := callErr.(syscall.Errno); ok && errno == windows.ERROR_NO_MORE_ITEMS {
			return 0, errNoMoreItems
		}
		return 0, fmt.Errorf("wineventlog: EvtNext: %w", callErr)
	}
	if int(returned) > len(handles) {
		returned = uint32(len(handles))
	}
	return int(returned), nil
}

// renderAndParse renders event handle h as XML, extracts the fields we
// report, and attempts to resolve the formatted message text via
// EvtFormatMessage using publisher metadata. pubCache is consulted and
// populated to amortise EvtOpenPublisherMetadata across events from the
// same provider (a nil entry is cached for providers with no resolvable
// metadata so we do not retry every event). When message formatting fails
// — a common case for uninstalled providers or messages requiring
// locale-specific resources — the flattened EventData text is used
// instead. An error is returned only when the XML render itself fails
// (the fields are mandatory).
//
// noMessage skips the metadata/format step altogether, leaving Message as
// the EventData fallback. That is the caller's opt-out from the dominant
// per-event cost; see Query.NoMessage.
func renderAndParse(hEvent uintptr, maxBytes int, pubCache map[string]uintptr, noMessage bool) (Event, error) {
	xmlStr, err := renderEventXML(hEvent, maxBytes)
	if err != nil {
		return Event{}, err
	}
	ev, err := parseEventXML(xmlStr)
	if err != nil {
		return Event{}, err
	}
	ev.Raw = xmlStr
	// Fall-back message from EventData is already populated by parseEventXML;
	// try to overlay a formatted message if the publisher metadata resolves.
	if !noMessage {
		if formatted := formatEventMessage(hEvent, ev.ProviderName, maxBytes, pubCache); formatted != "" {
			ev.Message = formatted
		}
	}
	return ev, nil
}

// formatEventMessage resolves the publisher's formatted message for hEvent.
// Returns an empty string (no message, caller keeps the EventData fallback)
// on any failure — metadata missing, locale missing, format error. Never
// fatal. pubCache is consulted and updated.
func formatEventMessage(hEvent uintptr, provider string, maxBytes int, pubCache map[string]uintptr) string {
	if provider == "" {
		return ""
	}
	hMeta, ok := pubCache[provider]
	if !ok {
		// Cache full: degrade to the EventData fallback rather than
		// opening another handle we would have to hold for the rest of
		// the query. See maxPubCacheEntries.
		if len(pubCache) >= maxPubCacheEntries {
			return ""
		}
		hMeta = openPublisherMetadata(provider)
		pubCache[provider] = hMeta
	}
	if hMeta == 0 {
		return ""
	}
	return formatMessage(hMeta, hEvent, maxBytes)
}

// openPublisherMetadata opens a publisher metadata handle for the given
// provider name. Returns 0 on any error (metadata not installed, permission
// denied, etc.) — the caller uses 0 as a cache sentinel meaning "no
// resolvable metadata".
func openPublisherMetadata(provider string) uintptr {
	namePtr, err := windows.UTF16PtrFromString(provider)
	if err != nil {
		return 0
	}
	// EvtOpenPublisherMetadata(session, publisherId, logFilePath, locale, flags)
	// - session=0 (local)
	// - logFilePath=NULL (use live publishers)
	// - locale=0 (caller thread locale)
	// - flags=0
	h, _, _ := procEvtOpenPublisherMetadata.Call(
		0,
		uintptr(unsafe.Pointer(namePtr)),
		0,
		0,
		0,
	)
	return h
}

// formatMessage calls EvtFormatMessage with the standard size-probe / grow
// pattern. Returns an empty string on any failure.
//
// Quirk (observed in the Windows SDK and the Datadog Agent wrapper): the
// second call to EvtFormatMessage may write one extra null terminator past
// the reported BufferUsed value. We allocate BufferUsed+1 uint16 slots but
// pass only BufferUsed to the API to avoid a buffer-overrun write.
func formatMessage(hMeta, hEvent uintptr, maxBytes int) string {
	var used uint32
	// Size probe. A NULL buffer + size 0 is supported; we still pass a
	// pointer to used so wevtapi can report the required size.
	_, _, callErr := procEvtFormatMessage.Call(
		hMeta,
		hEvent,
		0,
		0,
		0,
		uintptr(evtFormatMessageEvent),
		0,
		0,
		uintptr(unsafe.Pointer(&used)),
	)
	if errno, ok := callErr.(syscall.Errno); !ok || errno != windows.ERROR_INSUFFICIENT_BUFFER {
		return ""
	}
	if used == 0 {
		return ""
	}
	// used is in uint16 code units (including trailing NUL), not bytes.
	// Cap defensively against maxBytes (interpreted as a byte ceiling).
	if int(used)*2 > maxBytes {
		return ""
	}
	buf := make([]uint16, int(used)+1) // +1: SDK quirk (see doc above)
	r1, _, _ := procEvtFormatMessage.Call(
		hMeta,
		hEvent,
		0,
		0,
		0,
		uintptr(evtFormatMessageEvent),
		uintptr(used),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used)),
	)
	if r1 == 0 {
		return ""
	}
	return sanitizeMessage(windows.UTF16ToString(buf))
}

// sanitizeMessage strips U+200E (LEFT-TO-RIGHT MARK) from the formatted
// message and trims trailing whitespace. EvtFormatMessage embeds invisible
// LRM markers around inserted parameters in message templates so bidi
// rendering works in mixed RTL/LTR contexts; faithful round-trip is the
// problem because agents matching against rendered messages then fail on
// the invisible-char mismatch. The Datadog Agent strips this same
// character at comp/checks/windowseventlog/impl/check/message_filter.go.
//
// Other transforms (CR/LF/TAB handling, control chars) are deferred to
// the TSV emission path via tsvEscape so the JSONL path can carry the
// raw byte stream and let encoding/json produce the standard \uXXXX
// escapes.
func sanitizeMessage(s string) string {
	if !strings.ContainsRune(s, '‎') {
		return strings.TrimRight(s, " \t\r\n")
	}
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r == '‎' {
			continue
		}
		out = utf8.AppendRune(out, r)
	}
	return strings.TrimRight(string(out), " \t\r\n")
}

// renderEventXML calls EvtRender in EvtRenderEventXml mode using the standard
// two-pass pattern: first call with a null buffer to learn the required size,
// then allocate and call again. Returns a UTF-8 string.
func renderEventXML(hEvent uintptr, maxBytes int) (string, error) {
	var used, propCount uint32
	// First pass: expect ERROR_INSUFFICIENT_BUFFER.
	_, _, callErr := procEvtRender.Call(
		0,
		hEvent,
		uintptr(evtRenderEventXml),
		0,
		0,
		uintptr(unsafe.Pointer(&used)),
		uintptr(unsafe.Pointer(&propCount)),
	)
	if errno, ok := callErr.(syscall.Errno); !ok || errno != windows.ERROR_INSUFFICIENT_BUFFER {
		return "", fmt.Errorf("EvtRender size probe: %w", callErr)
	}
	if used == 0 {
		return "", fmt.Errorf("EvtRender reported zero-byte event")
	}
	// wevtapi returns UTF-16, which is always even-byte-aligned. An odd
	// count would cause make([]uint16, used/2) to allocate a buffer one
	// byte shorter than uintptr(used) below, exposing a 1-byte OOB write
	// on the second EvtRender call. Defensive check only — not observed
	// in practice.
	if used%2 != 0 {
		return "", fmt.Errorf("EvtRender reported odd byte count %d (expected UTF-16 even count)", used)
	}
	if int(used) > maxBytes {
		return "", fmt.Errorf("event exceeds %d-byte render cap (needed %d)", maxBytes, used)
	}
	// used is in bytes; UTF-16 means used/2 code units.
	buf := make([]uint16, used/2)
	r1, _, callErr := procEvtRender.Call(
		0,
		hEvent,
		uintptr(evtRenderEventXml),
		uintptr(used),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&used)),
		uintptr(unsafe.Pointer(&propCount)),
	)
	if r1 == 0 {
		return "", fmt.Errorf("EvtRender: %w", callErr)
	}
	return windows.UTF16ToString(buf), nil
}

// rawEvent matches the subset of the rendered event XML we care about.
type rawEvent struct {
	XMLName xml.Name `xml:"Event"`
	System  struct {
		Provider struct {
			Name string `xml:"Name,attr"`
		} `xml:"Provider"`
		EventID     uint32 `xml:"EventID"`
		Level       uint8  `xml:"Level"`
		TimeCreated struct {
			SystemTime string `xml:"SystemTime,attr"`
		} `xml:"TimeCreated"`
		EventRecordID uint64 `xml:"EventRecordID"`
		Channel       string `xml:"Channel"`
		Computer      string `xml:"Computer"`
		Task          uint32 `xml:"Task"`
		Opcode        uint32 `xml:"Opcode"`
		Keywords      string `xml:"Keywords"`
		Version       uint32 `xml:"Version"`
		Security      struct {
			UserID string `xml:"UserID,attr"`
		} `xml:"Security"`
		Execution struct {
			ProcessID uint32 `xml:"ProcessID,attr"`
			ThreadID  uint32 `xml:"ThreadID,attr"`
		} `xml:"Execution"`
		Correlation struct {
			ActivityID string `xml:"ActivityID,attr"`
		} `xml:"Correlation"`
	} `xml:"System"`
	EventData struct {
		Data []struct {
			Name  string `xml:"Name,attr"`
			Value string `xml:",chardata"`
		} `xml:"Data"`
	} `xml:"EventData"`
	UserData struct {
		InnerXML string `xml:",innerxml"`
	} `xml:"UserData"`
}

// parseEventXML extracts our output fields from the rendered event XML.
func parseEventXML(s string) (Event, error) {
	var r rawEvent
	if err := xml.Unmarshal([]byte(s), &r); err != nil {
		return Event{}, fmt.Errorf("parse event xml: %w", err)
	}
	data := flattenEventData(r)
	ev := Event{
		TimeCreated:  normalizeTime(r.System.TimeCreated.SystemTime),
		Level:        levelName(r.System.Level),
		EventID:      r.System.EventID,
		RecordID:     r.System.EventRecordID,
		ProviderName: r.System.Provider.Name,
		Message:      data,
		LogName:      r.System.Channel,
		MachineName:  r.System.Computer,
		UserID:       r.System.Security.UserID,
		ProcessID:    r.System.Execution.ProcessID,
		ThreadID:     r.System.Execution.ThreadID,
		Task:         r.System.Task,
		Opcode:       r.System.Opcode,
		Keywords:     strings.TrimSpace(r.System.Keywords),
		Version:      r.System.Version,
		ActivityID:   r.System.Correlation.ActivityID,
		EventData:    data,
	}
	return ev, nil
}

// normalizeTime converts the ISO-8601 SystemTime attribute into a compact
// "2006-01-02T15:04:05Z" form. Invalid or empty inputs are returned as-is so
// the user still sees something useful.
func normalizeTime(s string) string {
	if s == "" {
		return ""
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC().Format("2006-01-02T15:04:05Z")
	}
	return s
}

// levelName maps Windows event level codes to their canonical names.
func levelName(level uint8) string {
	switch level {
	case 0:
		return "LogAlways"
	case 1:
		return "Critical"
	case 2:
		return "Error"
	case 3:
		return "Warning"
	case 4:
		return "Information"
	case 5:
		return "Verbose"
	default:
		return strconv.Itoa(int(level))
	}
}

// flattenEventData joins EventData/Data children into a single spaced
// string. Used as a fallback Message when EvtFormatMessage cannot resolve
// the publisher metadata. Per-character escaping of control bytes is
// deferred to the TSV emission path (tsvEscape) so the JSONL path can
// carry the raw text and let encoding/json escape it.
func flattenEventData(r rawEvent) string {
	var parts []string
	for _, d := range r.EventData.Data {
		if d.Value == "" {
			continue
		}
		parts = append(parts, d.Value)
	}
	return strings.Join(parts, " ")
}

// ListChannels enumerates event log channels via EvtOpenChannelEnum /
// EvtNextChannelPath. Honors ctx cancellation between entries. Capped at
// maxEnumEntries entries defensively.
func ListChannels(ctx context.Context) ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hEnum, _, callErr := procEvtOpenChannelEnum.Call(0, 0)
	if hEnum == 0 {
		return nil, fmt.Errorf("wineventlog: EvtOpenChannelEnum: %w", callErr)
	}
	defer evtClose(hEnum)
	return enumStrings(ctx, hEnum, procEvtNextChannelPath)
}

// ListProviders enumerates event publishers via EvtOpenPublisherEnum /
// EvtNextPublisherId.
func ListProviders(ctx context.Context) ([]string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hEnum, _, callErr := procEvtOpenPublisherEnum.Call(0, 0)
	if hEnum == 0 {
		return nil, fmt.Errorf("wineventlog: EvtOpenPublisherEnum: %w", callErr)
	}
	defer evtClose(hEnum)
	return enumStrings(ctx, hEnum, procEvtNextPublisherID)
}

// enumStrings drives a wevtapi enumeration using the standard two-pass
// buffer-sizing pattern. nextProc must be either EvtNextChannelPath or
// EvtNextPublisherId — both share the same signature.
func enumStrings(ctx context.Context, hEnum uintptr, nextProc *windows.LazyProc) ([]string, error) {
	var out []string
	buf := make([]uint16, 256)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if len(out) >= maxEnumEntries {
			break
		}
		var used uint32
		r1, _, callErr := nextProc.Call(
			hEnum,
			uintptr(uint32(len(buf))),
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&used)),
		)
		if r1 == 0 {
			errno, ok := callErr.(syscall.Errno)
			if ok && errno == windows.ERROR_NO_MORE_ITEMS {
				break
			}
			if ok && errno == windows.ERROR_INSUFFICIENT_BUFFER {
				if used == 0 || int(used) > maxPathBufferChars {
					return nil, fmt.Errorf("wineventlog: enum buffer size %d out of range", used)
				}
				buf = make([]uint16, used)
				continue
			}
			return nil, fmt.Errorf("wineventlog: enum next: %w", callErr)
		}
		if used == 0 {
			continue
		}
		// used counts uint16 code units including the null terminator.
		end := int(used)
		if end > len(buf) {
			end = len(buf)
		}
		out = append(out, windows.UTF16ToString(buf[:end]))
	}
	return out, nil
}
