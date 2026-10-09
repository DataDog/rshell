// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"strings"
	"testing"
)

func TestParseEventXML(t *testing.T) {
	got, err := parseEventXML(`<Event><System>
		<Provider Name="provider"/><EventID>42</EventID><Level>3</Level>
		<TimeCreated SystemTime="2026-01-02T03:04:05.123Z"/>
		<EventRecordID>99</EventRecordID><Channel>Application</Channel>
		<Computer>host</Computer><Task>7</Task><Opcode>8</Opcode>
		<Keywords> 0x8000 </Keywords><Version>2</Version>
		<Security UserID="S-1-5-18"/><Execution ProcessID="123" ThreadID="456"/>
		<Correlation ActivityID="{activity}"/>
	</System><EventData><Data></Data><Data Name="first">one</Data><Data Name="second">two</Data></EventData></Event>`)
	if err != nil {
		t.Fatalf("parseEventXML: %v", err)
	}
	want := Event{
		TimeCreated:  "2026-01-02T03:04:05Z",
		Level:        "Warning",
		EventID:      42,
		RecordID:     99,
		ProviderName: "provider",
		Message:      "one two",
		LogName:      "Application",
		MachineName:  "host",
		UserID:       "S-1-5-18",
		ProcessID:    123,
		ThreadID:     456,
		Task:         7,
		Opcode:       8,
		Keywords:     "0x8000",
		Version:      2,
		ActivityID:   "{activity}",
		EventData:    "one two",
	}
	if got != want {
		t.Errorf("parseEventXML() = %#v, want %#v", got, want)
	}
}

func TestNormalizeTime(t *testing.T) {
	for _, tc := range []struct {
		in, want string
	}{
		{"", ""},
		{"not-a-time", "not-a-time"},
		{"2026-01-02T03:04:05.1234567Z", "2026-01-02T03:04:05Z"},
		{"2026-01-02T03:04:05+02:00", "2026-01-02T01:04:05Z"},
	} {
		if got := normalizeTime(tc.in); got != tc.want {
			t.Errorf("normalizeTime(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLevelName(t *testing.T) {
	for level, want := range map[uint8]string{
		0: "LogAlways", 1: "Critical", 2: "Error", 3: "Warning",
		4: "Information", 5: "Verbose", 99: "99",
	} {
		if got := levelName(level); got != want {
			t.Errorf("levelName(%d) = %q, want %q", level, got, want)
		}
	}
}

func TestParseEventXMLRejectsMalformedAndInvalidNumericFields(t *testing.T) {
	for _, in := range []string{
		`<Event><System>`,
		`<Event><System><EventID>not-a-number</EventID></System></Event>`,
	} {
		_, err := parseEventXML(in)
		if err == nil || !strings.Contains(err.Error(), "parse event xml:") {
			t.Errorf("parseEventXML(%q) error = %v, want parse error", in, err)
		}
	}
}
