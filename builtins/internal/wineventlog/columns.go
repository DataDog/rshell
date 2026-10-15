// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"fmt"
	"strconv"
	"strings"
)

// Column is a single selectable TSV output field.
type Column struct {
	// Name is the canonical, case-sensitive display name. It matches the
	// PowerShell EventLogRecord property name so a Get-WinEvent user can
	// reuse what they already know.
	Name string
	// Desc is a one-line description used in --help output.
	Desc string
	// Value extracts the column's text from a rendered event.
	Value func(Event) string
}

// columns is the canonical column registry, in the order --help lists them:
// the default columns first, then the extras. Lookup is by lowercased name
// via columnIndex.
var columns = []Column{
	{"TimeCreated", "event time, second-precision UTC", func(e Event) string { return e.TimeCreated }},
	{"Level", "severity name (Error, Warning, Information, ...)", func(e Event) string { return e.Level }},
	{"Id", "event ID", func(e Event) string { return strconv.FormatUint(uint64(e.EventID), 10) }},
	{"RecordId", "per-log record number", func(e Event) string { return strconv.FormatUint(e.RecordID, 10) }},
	{"ProviderName", "publishing provider name", func(e Event) string { return e.ProviderName }},
	{"Message", "formatted message, or EventData when unresolvable", func(e Event) string { return e.Message }},
	{"LogName", "channel the event was written to", func(e Event) string { return e.LogName }},
	{"MachineName", "computer that logged the event", func(e Event) string { return e.MachineName }},
	{"UserId", "SID of the associated user, if recorded", func(e Event) string { return e.UserID }},
	{"ProcessId", "process ID that raised the event", func(e Event) string { return strconv.FormatUint(uint64(e.ProcessID), 10) }},
	{"ThreadId", "thread ID that raised the event", func(e Event) string { return strconv.FormatUint(uint64(e.ThreadID), 10) }},
	{"Task", "provider-defined task number", func(e Event) string { return strconv.FormatUint(uint64(e.Task), 10) }},
	{"Opcode", "provider-defined opcode number", func(e Event) string { return strconv.FormatUint(uint64(e.Opcode), 10) }},
	{"Keywords", "keyword bitmask, verbatim from the XML", func(e Event) string { return e.Keywords }},
	{"Version", "provider-defined event version", func(e Event) string { return strconv.FormatUint(uint64(e.Version), 10) }},
	{"ActivityId", "correlation activity GUID, if recorded", func(e Event) string { return e.ActivityID }},
	{"EventData", "raw insertion strings, space-joined", func(e Event) string { return e.EventData }},
}

// columnIndex maps a lowercased column name to its entry in columns.
// Lookups are case-insensitive: PowerShell property access is
// case-insensitive and an agent typing --Columns time,id,message should not
// have to guess our capitalisation. The canonical casing in columns is what
// --help and error messages report.
var columnIndex = func() map[string]*Column {
	m := make(map[string]*Column, len(columns))
	for i := range columns {
		m[strings.ToLower(columns[i].Name)] = &columns[i]
	}
	return m
}()

// DefaultColumns is the column set emitted when --Columns is not given. It
// is the historical fixed TSV layout, kept byte-for-byte identical so adding
// --Columns is not a breaking output change.
var DefaultColumns = []string{"TimeCreated", "Level", "Id", "RecordId", "ProviderName", "Message"}

// MaxColumns bounds a --Columns list. Duplicates are allowed (a caller may
// legitimately want a field twice), so the cap is on total entries rather
// than distinct ones; it exists only to keep a pathological argument from
// producing an enormous line per event.
const MaxColumns = 32

// MaxColumnsSpecLen bounds the raw --Columns string before it is split, so a
// pathological argument cannot make strings.Split allocate a huge slice
// before the MaxColumns check runs. The longest legitimate spec is every
// column once (17 names, longest 12 bytes, plus separators) — well under
// this, which leaves generous room for whitespace padding.
const MaxColumnsSpecLen = 1024

// AllColumns returns the column registry for --help rendering.
func AllColumns() []Column { return columns }

// ParseColumns resolves a comma-separated column list into extractors.
// Whitespace around each name is ignored; empty names and unknown names are
// errors. The returned slice preserves the caller's order, including
// duplicates.
func ParseColumns(spec string) ([]Column, error) {
	// Bound the input before splitting: the length check has to precede the
	// allocation it protects.
	if len(spec) > MaxColumnsSpecLen {
		return nil, fmt.Errorf("column list too long (max %d bytes)", MaxColumnsSpecLen)
	}
	parts := strings.Split(spec, ",")
	if len(parts) > MaxColumns {
		return nil, fmt.Errorf("too many columns (max %d)", MaxColumns)
	}
	out := make([]Column, 0, len(parts))
	for _, p := range parts {
		name := strings.TrimSpace(p)
		if name == "" {
			return nil, fmt.Errorf("empty column name")
		}
		col, ok := columnIndex[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("unknown column %q (valid: %s)", name, strings.Join(ColumnNames(), ", "))
		}
		out = append(out, *col)
	}
	return out, nil
}

// ColumnNames returns every canonical column name in registry order.
func ColumnNames() []string {
	out := make([]string, 0, len(columns))
	for _, c := range columns {
		out = append(out, c.Name)
	}
	return out
}

// FormatRow renders one TSV line (including the trailing newline) for ev
// using cols. Every field is passed through TSVEscape so a tab or newline in
// message text cannot forge a column or row boundary.
func FormatRow(ev Event, cols []Column) string {
	var b strings.Builder
	for i, c := range cols {
		if i > 0 {
			b.WriteByte('\t')
		}
		b.WriteString(TSVEscape(c.Value(ev)))
	}
	b.WriteByte('\n')
	return b.String()
}

// NeedsMessage reports whether any column in cols reads the formatted
// message. When it returns false the caller can skip EvtFormatMessage
// entirely — the value would never be observed — which is the single
// biggest per-event cost in a query.
func NeedsMessage(cols []Column) bool {
	for _, c := range cols {
		if c.Name == "Message" {
			return true
		}
	}
	return false
}
