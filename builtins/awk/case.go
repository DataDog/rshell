// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"strings"
	"unicode/utf8"
)

func mapAwkCase(s string, transform func(string) string) string {
	if utf8.ValidString(s) {
		return transform(s)
	}

	var b strings.Builder
	b.Grow(len(s))
	validStart := 0
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError || size != 1 {
			i += size
			continue
		}
		b.WriteString(transform(s[validStart:i]))
		b.WriteByte(s[i])
		i++
		validStart = i
	}
	b.WriteString(transform(s[validStart:]))
	return b.String()
}
