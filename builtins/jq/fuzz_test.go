// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq

import (
	"context"
	"testing"
)

func FuzzJQFilterParser(f *testing.F) {
	for _, seed := range []string{
		`.`, `.items[]?`, `{a: .x | . + 1}`, `(1,2) + (10,20)`,
		`[.[] | select(. >= 2)]`, `$x // empty`, `1 < 2 < 3`, `"\ud800"`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, filter string) {
		if len(filter) > MaxFilterBytes {
			return
		}
		_, _ = parseFilter(filter)
	})
}

func FuzzJQJSONDecoder(f *testing.F) {
	for _, seed := range []string{
		`null`, `123456789012345678901234567890`, `1e-400`, `{"b":1,"a":2,"b":3}`,
		`[true,false,null,"text"]`, `"\ud83d\ude00"`, `"\ud800"`, `{`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		if len(input) > 1<<20 {
			return
		}
		_, _ = parseSingleJSON(context.Background(), input)
	})
}
