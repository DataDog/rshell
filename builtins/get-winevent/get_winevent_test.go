// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package get_winevent

import (
	"strings"
	"testing"
)

func changed(names ...string) func(string) bool {
	return func(name string) bool {
		for _, changed := range names {
			if name == changed {
				return true
			}
		}
		return false
	}
}

func TestValidateOptionsSelectorsAndOutput(t *testing.T) {
	base := options{logName: "Application", output: "tsv", maxEvents: DefaultMaxEvents}
	if got := validateOptions(&base, "TimeCreated", changed("LogName")); got != "" {
		t.Fatalf("valid channel query: %s", got)
	}

	missing := options{output: "tsv", maxEvents: DefaultMaxEvents}
	if got := validateOptions(&missing, "TimeCreated", changed()); !strings.Contains(got, "is required") {
		t.Fatalf("missing selector error = %q", got)
	}

	conflict := options{logName: "Application", path: "x.evtx", output: "tsv", maxEvents: DefaultMaxEvents}
	if got := validateOptions(&conflict, "TimeCreated", changed("LogName", "Path")); !strings.Contains(got, "only one") {
		t.Fatalf("conflicting selector error = %q", got)
	}

	jsonlColumns := options{logName: "Application", output: "jsonl", maxEvents: DefaultMaxEvents}
	if got := validateOptions(&jsonlColumns, "Message", changed("LogName", "Columns", "output")); !strings.Contains(got, "--Columns cannot") {
		t.Fatalf("JSON Lines columns error = %q", got)
	}
}

func TestValidateOptionsMaxEventsAndListingModifiers(t *testing.T) {
	tooSmall := options{logName: "Application", output: "tsv", maxEvents: 0}
	if got := validateOptions(&tooSmall, "TimeCreated", changed("LogName", "MaxEvents")); got != "--MaxEvents must be >= 1" {
		t.Fatalf("small max events error = %q", got)
	}

	clamped := options{logName: "Application", output: "tsv", maxEvents: MaxMaxEvents + 1}
	if got := validateOptions(&clamped, "TimeCreated", changed("LogName", "MaxEvents")); got != "" || clamped.maxEvents != MaxMaxEvents {
		t.Fatalf("clamp = (%q, %d), want (empty, %d)", got, clamped.maxEvents, MaxMaxEvents)
	}

	listing := options{listLog: true, output: "tsv", maxEvents: DefaultMaxEvents}
	if got := validateOptions(&listing, "TimeCreated", changed("MaxEvents")); !strings.Contains(got, "--MaxEvents cannot") {
		t.Fatalf("listing modifier error = %q", got)
	}
}

func TestParseMaxEvents(t *testing.T) {
	for _, tc := range []struct {
		input   string
		want    int64
		clamped bool
		err     string
	}{
		{input: "256", want: 256},
		{input: "-1", want: -1},
		{input: "abc", err: "--MaxEvents must be a whole number"},
		{input: "9223372036854775808", want: MaxMaxEvents, clamped: true},
		{input: strings.Repeat("9", 1_000), want: MaxMaxEvents, clamped: true},
	} {
		got, clamped, err := parseMaxEvents(tc.input)
		if tc.err != "" {
			if err == nil || err.Error() != tc.err {
				t.Errorf("parseMaxEvents(%q) error = %v, want %q", tc.input, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want || clamped != tc.clamped {
			t.Errorf("parseMaxEvents(%q) = (%d, %v, %v), want (%d, %v, nil)", tc.input, got, clamped, err, tc.want, tc.clamped)
		}
	}
}

func TestValidateOptionsPortableErrorPaths(t *testing.T) {
	validQueryList := `<QueryList><Query><Select Path="System">*</Select></Query></QueryList>`
	for _, tc := range []struct {
		name    string
		opts    options
		columns string
		changed []string
		want    string
	}{
		{
			name:    "empty LogName",
			opts:    options{output: "tsv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"LogName"},
			want:    "--LogName must not be empty",
		},
		{
			name:    "XPath too long",
			opts:    options{logName: "Application", xpath: strings.Repeat("x", MaxXPathLen+1), output: "tsv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"LogName", "FilterXPath"},
			want:    "--FilterXPath too long",
		},
		{
			name:    "FilterXml conflicts with XPath",
			opts:    options{filterXml: validQueryList, xpath: "*", output: "tsv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"FilterXml", "FilterXPath"},
			want:    "--FilterXPath cannot be combined with --FilterXml",
		},
		{
			name:    "invalid FilterXml",
			opts:    options{filterXml: `<Event/>`, output: "tsv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"FilterXml"},
			want:    "--FilterXml: expected <QueryList> root element, got <Event>",
		},
		{
			name:    "invalid output",
			opts:    options{logName: "Application", output: "csv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"LogName", "output"},
			want:    "--output must be tsv or jsonl",
		},
		{
			name:    "listing output modifier",
			opts:    options{listLog: true, output: "tsv", maxEvents: DefaultMaxEvents},
			columns: "TimeCreated",
			changed: []string{"output"},
			want:    "--output cannot be used with --ListLog or --ListProvider",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validateOptions(&tc.opts, tc.columns, changed(tc.changed...)); got != tc.want {
				t.Errorf("validateOptions() = %q, want %q", got, tc.want)
			}
		})
	}
}
