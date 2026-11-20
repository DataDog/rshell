// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"encoding/json"
	"strings"
	"testing"
)

// maxFuzzXMLBytes matches get-winevent's MaxFilterXmlLen and MaxRenderBytes.
// The production command rejects larger user-controlled XML before it reaches
// these helpers, so larger fuzz inputs would exercise an unrealistic surface.
const maxFuzzXMLBytes = 64 * 1024

const fuzzEventXML = `<Event><System><Provider Name="provider"/><EventID>42</EventID><Level>4</Level><TimeCreated SystemTime="2026-01-02T03:04:05.123Z"/><EventRecordID>99</EventRecordID><Channel>Application</Channel><Computer>host</Computer></System><EventData><Data Name="first">one</Data><Data Name="second">two</Data></EventData></Event>`

// FuzzEventTree fuzzes the XML-to-JSON conversion used by get-winevent's
// JSON Lines output. Successful trees must remain JSON encodable after the
// conversion's map/slice normalization.
func FuzzEventTree(f *testing.F) {
	for _, seed := range []string{
		fuzzEventXML,
		`<Event><System><EventID Qualifiers="16384">6013</EventID></System></Event>`,
		`<Event><EventData><Data Name="first">one</Data><Data>two</Data></EventData></Event>`,
		`<Event><a><a><a><a><a><a><a><a><a><a>leaf</a></a></a></a></a></a></a></a></a></a></Event>`,
		`<Event><System>`,
		`<Event><System><EventID>&invalid;</EventID></System></Event>`,
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxFuzzXMLBytes {
			return
		}
		tree, err := EventTree(input)
		if err != nil {
			return
		}
		if _, err := json.Marshal(tree); err != nil {
			t.Fatalf("EventTree result is not JSON encodable: %v", err)
		}
	})
}

// FuzzParseEventXML fuzzes the typed rendered-event extractor. Errors from
// malformed XML and invalid numeric fields are expected; successful events
// must still be safe to render as TSV without leaking record delimiters.
func FuzzParseEventXML(f *testing.F) {
	for _, seed := range []string{
		fuzzEventXML,
		`<Event><System><EventID>not-a-number</EventID></System></Event>`,
		`<Event><System><Provider Name="a&#x9;b"/></System></Event>`,
		`<Event><System>`,
		"\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxFuzzXMLBytes {
			return
		}
		ev, err := parseEventXML(input)
		if err != nil {
			return
		}
		row := FormatRow(ev, columns)
		if strings.Count(row, "\n") != 1 {
			t.Fatalf("FormatRow produced an invalid TSV record: %q", row)
		}
		if got, want := strings.Count(row, "\t"), len(columns)-1; got != want {
			t.Fatalf("FormatRow produced %d field separators, want %d: %q", got, want, row)
		}
	})
}

// FuzzValidateFilterXml fuzzes the structured-query validator before any
// input could reach wevtapi. Rejection is expected for malformed XML and for
// XML outside the intentionally small QueryList schema.
func FuzzValidateFilterXml(f *testing.F) {
	for _, seed := range []string{
		`<QueryList><Query Id="0" Path="System"><Select Path="System">*</Select></Query></QueryList>`,
		`<?xml version="1.0"?><QueryList><Query><Select><![CDATA[*[System[EventID=1]]]]></Select><Suppress>*[System[Level=4]]</Suppress></Query></QueryList>`,
		`<QueryList/>`,
		`<QueryList><Query><Select><Nested/></Select></Query></QueryList>`,
		`<Event/>`,
		`<QueryList><Query>`,
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxFuzzXMLBytes {
			return
		}
		_ = ValidateFilterXml(input)
	})
}

// FuzzTSVEscape verifies every escaped value remains a single TSV field.
func FuzzTSVEscape(f *testing.F) {
	for _, seed := range []string{"", "plain text", "a\tb\nc\rd", "\\", "\x00\x1f\x7f", "Foo‎Bar", "\xff"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > maxFuzzXMLBytes {
			return
		}
		got := TSVEscape(input)
		if strings.ContainsRune(got, '‎') || strings.ContainsAny(got, "\t\r\n") {
			t.Fatalf("TSVEscape leaked a structural character: %q", got)
		}
		for _, r := range got {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("TSVEscape leaked control character %U in %q", r, got)
			}
		}
	})
}

// FuzzParseColumns checks the bounded user-supplied column selector and the
// resulting TSV layout together. The parser's errors are ordinary invalid
// user input; a successful parse must produce exactly one record.
func FuzzParseColumns(f *testing.F) {
	for _, seed := range []string{
		strings.Join(DefaultColumns, ","),
		"message,ID,TIMECREATED",
		"Id,,Level",
		strings.TrimSuffix(strings.Repeat("Id,", MaxColumns+1), ","),
		strings.Repeat(",", MaxColumnsSpecLen+1),
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, spec string) {
		cols, err := ParseColumns(spec)
		if err != nil {
			return
		}
		if len(cols) == 0 || len(cols) > MaxColumns {
			t.Fatalf("ParseColumns returned %d columns", len(cols))
		}
		row := FormatRow(sampleEvent(), cols)
		if !strings.HasSuffix(row, "\n") || strings.Count(row, "\n") != 1 {
			t.Fatalf("FormatRow did not produce one TSV record: %q", row)
		}
		if got, want := strings.Count(row, "\t"), len(cols)-1; got != want {
			t.Fatalf("FormatRow produced %d field separators, want %d: %q", got, want, row)
		}
	})
}
