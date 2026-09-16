// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"strings"
	"testing"
)

// sampleEvent is a fully-populated event so every column extractor has a
// distinguishable value.
func sampleEvent() Event {
	return Event{
		TimeCreated:  "2026-07-31T16:00:00Z",
		Level:        "Information",
		EventID:      6013,
		RecordID:     30820,
		ProviderName: "EventLog",
		Message:      "The system uptime is 37204 seconds.",
		LogName:      "System",
		MachineName:  "winbuild",
		UserID:       "S-1-5-18",
		ProcessID:    2568,
		ThreadID:     4444,
		Task:         12,
		Opcode:       3,
		Keywords:     "0x8080000000000000",
		Version:      1,
		ActivityID:   "{fad94239-7c94-47aa-b45b-43da2b74104a}",
		EventData:    "37204 60 300 Eastern Standard Time",
	}
}

func TestParseColumnsDefaultMatchesLegacyLayout(t *testing.T) {
	cols, err := ParseColumns(strings.Join(DefaultColumns, ","))
	if err != nil {
		t.Fatalf("ParseColumns(default): %v", err)
	}
	got := FormatRow(sampleEvent(), cols)
	// This is the exact TSV layout get-winevent emitted before --Columns
	// existed. It must not drift: the default output is a compatibility
	// surface.
	want := "2026-07-31T16:00:00Z\tInformation\t6013\t30820\tEventLog\tThe system uptime is 37204 seconds.\n"
	if got != want {
		t.Errorf("default row\n got: %q\nwant: %q", got, want)
	}
}

func TestParseColumnsCaseInsensitiveAndOrdered(t *testing.T) {
	cols, err := ParseColumns("message,ID,TIMECREATED")
	if err != nil {
		t.Fatalf("ParseColumns: %v", err)
	}
	if len(cols) != 3 {
		t.Fatalf("got %d columns, want 3", len(cols))
	}
	// Canonical casing is reported regardless of how the user typed it.
	for i, want := range []string{"Message", "Id", "TimeCreated"} {
		if cols[i].Name != want {
			t.Errorf("cols[%d].Name = %q, want %q", i, cols[i].Name, want)
		}
	}
	got := FormatRow(sampleEvent(), cols)
	want := "The system uptime is 37204 seconds.\t6013\t2026-07-31T16:00:00Z\n"
	if got != want {
		t.Errorf("row\n got: %q\nwant: %q", got, want)
	}
}

func TestParseColumnsAllowsWhitespaceAndDuplicates(t *testing.T) {
	cols, err := ParseColumns(" Id , Id ")
	if err != nil {
		t.Fatalf("ParseColumns: %v", err)
	}
	if got, want := FormatRow(sampleEvent(), cols), "6013\t6013\n"; got != want {
		t.Errorf("row = %q, want %q", got, want)
	}
}

func TestParseColumnsErrors(t *testing.T) {
	tests := []struct {
		name    string
		spec    string
		wantSub string
	}{
		{"unknown", "Nope", `unknown column "Nope"`},
		{"empty entry", "Id,,Level", "empty column name"},
		{"empty spec", "", "empty column name"},
		{"whitespace only", "   ", "empty column name"},
		{"trailing comma", "Id,", "empty column name"},
		{"too many", strings.TrimSuffix(strings.Repeat("Id,", MaxColumns+1), ","), "too many columns"},
		// The length cap must fire before the split allocates, so a
		// pathological spec is rejected on size rather than entry count.
		{"spec too long", strings.Repeat(",", MaxColumnsSpecLen+1), "column list too long"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseColumns(tc.spec)
			if err == nil {
				t.Fatalf("ParseColumns(%q) = nil error, want error", tc.spec)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not contain %q", err, tc.wantSub)
			}
		})
	}
}

// Every column must be selectable by its own canonical name — a registry
// entry whose name does not round-trip through ParseColumns would appear in
// --help but be unusable.
func TestEveryAdvertisedColumnParses(t *testing.T) {
	for _, name := range ColumnNames() {
		cols, err := ParseColumns(name)
		if err != nil {
			t.Errorf("ParseColumns(%q): %v", name, err)
			continue
		}
		if cols[0].Name != name {
			t.Errorf("ParseColumns(%q) resolved to %q", name, cols[0].Name)
		}
		if cols[0].Value == nil {
			t.Errorf("column %q has a nil extractor", name)
		}
	}
}

// Selecting the whole registry must produce one field per column, with no
// field silently empty for our fully-populated sample. This catches an
// Event field added to types.go but never wired to its extractor.
func TestAllColumnsPopulatedForSampleEvent(t *testing.T) {
	cols, err := ParseColumns(strings.Join(ColumnNames(), ","))
	if err != nil {
		t.Fatalf("ParseColumns(all): %v", err)
	}
	row := strings.TrimSuffix(FormatRow(sampleEvent(), cols), "\n")
	fields := strings.Split(row, "\t")
	if len(fields) != len(cols) {
		t.Fatalf("got %d fields, want %d", len(fields), len(cols))
	}
	for i, f := range fields {
		if f == "" {
			t.Errorf("column %q produced an empty field", cols[i].Name)
		}
	}
}

// FormatRow must escape every field, not just the message, so a tab or
// newline in any column cannot forge a column or row boundary.
func TestFormatRowEscapesAllFields(t *testing.T) {
	ev := sampleEvent()
	ev.ProviderName = "a\tb"
	ev.MachineName = "x\ny"
	cols, err := ParseColumns("ProviderName,MachineName,Id")
	if err != nil {
		t.Fatalf("ParseColumns: %v", err)
	}
	got := FormatRow(ev, cols)
	if want := "a\\tb\tx\\ny\t6013\n"; got != want {
		t.Errorf("row\n got: %q\nwant: %q", got, want)
	}
	if strings.Count(got, "\t") != 2 {
		t.Errorf("row has %d tabs, want exactly 2 separators: %q", strings.Count(got, "\t"), got)
	}
	if strings.Count(got, "\n") != 1 {
		t.Errorf("row has %d newlines, want exactly 1 terminator: %q", strings.Count(got, "\n"), got)
	}
}

func TestNeedsMessage(t *testing.T) {
	tests := []struct {
		spec string
		want bool
	}{
		{"TimeCreated,Id", false},
		{"TimeCreated,Message", true},
		{"Message", true},
		// EventData is derived from the XML for free; it does not require
		// the EvtFormatMessage round-trip that Message does.
		{"EventData", false},
		{strings.Join(DefaultColumns, ","), true},
	}
	for _, tc := range tests {
		cols, err := ParseColumns(tc.spec)
		if err != nil {
			t.Fatalf("ParseColumns(%q): %v", tc.spec, err)
		}
		if got := NeedsMessage(cols); got != tc.want {
			t.Errorf("NeedsMessage(%q) = %v, want %v", tc.spec, got, tc.want)
		}
	}
}

// AllColumns and ColumnNames must stay in sync, and names must be unique
// (a duplicate would make one entry unreachable through columnIndex).
func TestColumnRegistryIsConsistent(t *testing.T) {
	all := AllColumns()
	names := ColumnNames()
	if len(all) != len(names) {
		t.Fatalf("AllColumns has %d entries, ColumnNames has %d", len(all), len(names))
	}
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		lower := strings.ToLower(n)
		if seen[lower] {
			t.Errorf("duplicate column name %q (case-insensitively)", n)
		}
		seen[lower] = true
		if strings.ContainsAny(n, " ,\t") {
			t.Errorf("column name %q contains a separator character", n)
		}
	}
	for _, c := range all {
		if c.Desc == "" {
			t.Errorf("column %q has no description for --help", c.Name)
		}
	}
}
