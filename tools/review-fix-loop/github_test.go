// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"testing"
)

func TestCIPassingFromJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    bool
		wantErr bool
	}{
		{
			name:  "empty output — no checks means passing",
			input: nil,
			want:  true,
		},
		{
			name:  "all passing",
			input: []byte(`[{"name":"test","state":"success"},{"name":"lint","state":"passing"}]`),
			want:  true,
		},
		{
			name:  "pending is non-blocking",
			input: []byte(`[{"name":"test","state":"pending"},{"name":"lint","state":"queued"}]`),
			want:  true,
		},
		{
			name:  "one failing check",
			input: []byte(`[{"name":"test","state":"success"},{"name":"lint","state":"failing"}]`),
			want:  false,
		},
		{
			name:  "failure variant",
			input: []byte(`[{"name":"test","state":"failure"}]`),
			want:  false,
		},
		{
			name:  "failed variant",
			input: []byte(`[{"name":"test","state":"failed"}]`),
			want:  false,
		},
		{
			name:  "error state",
			input: []byte(`[{"name":"test","state":"error"}]`),
			want:  false,
		},
		{
			name:  "case-insensitive matching",
			input: []byte(`[{"name":"test","state":"FAILING"}]`),
			want:  false,
		},
		{
			name:  "cancelled check is not clean",
			input: []byte(`[{"name":"test","state":"success"},{"name":"flaky","state":"cancelled"}]`),
			want:  false,
		},
		{
			name:  "cancel (gh spelling) is not clean",
			input: []byte(`[{"name":"test","state":"cancel"}]`),
			want:  false,
		},
		{
			name:  "timed_out is not clean",
			input: []byte(`[{"name":"test","state":"timed_out"}]`),
			want:  false,
		},
		{
			name:  "action_required is not clean",
			input: []byte(`[{"name":"test","state":"action_required"}]`),
			want:  false,
		},
		{
			name:  "stale is not clean",
			input: []byte(`[{"name":"test","state":"stale"}]`),
			want:  false,
		},
		{
			name:    "invalid JSON",
			input:   []byte(`not json`),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ciPassingFromJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ciPassingFromJSON() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("ciPassingFromJSON() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCountUnresolvedInPage(t *testing.T) {
	page := func(nodes string, hasNext bool, cursor string) []byte {
		next := "false"
		if hasNext {
			next = "true"
		}
		return []byte(`{"data":{"repository":{"pullRequest":{"reviewThreads":{"pageInfo":{"hasNextPage":` +
			next + `,"endCursor":"` + cursor + `"},"nodes":[` + nodes + `]}}}}}`)
	}

	thread := func(resolved bool, login string) string {
		r := "false"
		if resolved {
			r = "true"
		}
		return `{"isResolved":` + r + `,"comments":{"nodes":[{"author":{"login":"` + login + `"}}]}}`
	}

	emptyComments := `{"isResolved":false,"comments":{"nodes":[]}}`

	tests := []struct {
		name        string
		input       []byte
		myLogin     string
		wantCount   int
		wantHasNext bool
		wantCursor  string
		wantErr     bool
	}{
		{
			name:    "no threads",
			input:   page("", false, ""),
			myLogin: "alice",
		},
		{
			name:      "one unresolved from me",
			input:     page(thread(false, "alice"), false, ""),
			myLogin:   "alice",
			wantCount: 1,
		},
		{
			name:      "one unresolved from codex bot",
			input:     page(thread(false, "chatgpt-codex-connector[bot]"), false, ""),
			myLogin:   "alice",
			wantCount: 1,
		},
		{
			name:    "resolved thread is not counted",
			input:   page(thread(true, "alice"), false, ""),
			myLogin: "alice",
		},
		{
			name:    "thread from different user is not counted",
			input:   page(thread(false, "other-reviewer"), false, ""),
			myLogin: "alice",
		},
		{
			name:    "thread with no comments is not counted",
			input:   page(emptyComments, false, ""),
			myLogin: "alice",
		},
		{
			name: "mixed threads — only mine and codex counted",
			input: page(
				thread(false, "alice")+","+
					thread(true, "alice")+","+
					thread(false, "other")+","+
					thread(false, "chatgpt-codex-connector[bot]"),
				false, "",
			),
			myLogin:   "alice",
			wantCount: 2,
		},
		{
			name:        "pagination info forwarded",
			input:       page(thread(false, "alice"), true, "cursor123"),
			myLogin:     "alice",
			wantCount:   1,
			wantHasNext: true,
			wantCursor:  "cursor123",
		},
		{
			name:    "invalid JSON",
			input:   []byte(`not json`),
			myLogin: "alice",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, hasNext, cursor, err := countUnresolvedInPage(tt.input, tt.myLogin)
			if (err != nil) != tt.wantErr {
				t.Errorf("countUnresolvedInPage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
			if hasNext != tt.wantHasNext {
				t.Errorf("hasNextPage = %v, want %v", hasNext, tt.wantHasNext)
			}
			if cursor != tt.wantCursor {
				t.Errorf("endCursor = %q, want %q", cursor, tt.wantCursor)
			}
		})
	}
}
