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
