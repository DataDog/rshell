// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"strings"
	"unicode"
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

func compareAwkStringsIgnoreCase(left, right string) int {
	if !utf8.ValidString(left) || !utf8.ValidString(right) {
		return strings.Compare(
			mapAwkCase(left, strings.ToLower),
			mapAwkCase(right, strings.ToLower),
		)
	}

	// GNU awk bounds multibyte case folding by the shorter UTF-8 byte length.
	limit := min(len(left), len(right))
	leftOffset, rightOffset := 0, 0
	for leftOffset < limit && rightOffset < limit {
		leftRune, leftSize := decodeAwkComparisonRune(left, leftOffset, limit)
		rightRune, rightSize := decodeAwkComparisonRune(right, rightOffset, limit)
		if leftRune >= 0 {
			leftRune = unicode.ToLower(leftRune)
		}
		if rightRune >= 0 {
			rightRune = unicode.ToLower(rightRune)
		}
		if leftRune < rightRune {
			return -1
		}
		if leftRune > rightRune {
			return 1
		}
		leftOffset += leftSize
		rightOffset += rightSize
	}

	return len(left) - len(right)
}

func decodeAwkComparisonRune(s string, offset, limit int) (rune, int) {
	r, size := utf8.DecodeRuneInString(s[offset:])
	if (r == utf8.RuneError && size == 1) || offset+size > limit {
		return -1, 1
	}
	return r, size
}
