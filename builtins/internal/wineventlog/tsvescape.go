// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import "strings"

// TSVEscape returns s with characters that would corrupt or hide content
// in tab-separated output rewritten as backslash escapes. The escape
// alphabet is:
//
//	\\           → \\\\
//	\t           → \\t
//	\n           → \\n
//	\r           → \\r
//	other < 0x20 → \\xHH (two lowercase hex digits)
//	0x7F         → \\x7f
//	U+200E       → \\u200e (defensive — sanitizeMessage already strips
//	              this from EvtFormatMessage output, but apply here too
//	              in case it appears in a non-message column)
//
// Everything else, including printable non-ASCII Unicode, passes through
// unchanged. The output is single-line and contains no tab or NUL bytes,
// so naive consumers (`cut -f`, `awk -F'\t'`, `IFS=$'\t' read`) split
// columns reliably regardless of the message body.
//
// The escape set is round-trippable via Go's strconv.Unquote on a
// double-quoted form of the result; bash printf %b handles the
// `\\`/`\t`/`\n`/`\r` subset but not `\xHH` or `\uXXXX`, so consumers
// needing the original bytes should round-trip via a JSON / Go
// string-literal decoder rather than POSIX printf.
func TSVEscape(s string) string {
	if !needsTSVEscape(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '‎':
			b.WriteString("\\u200e")
		case r < 0x20 || r == 0x7F:
			const hex = "0123456789abcdef"
			b.WriteString(`\x`)
			b.WriteByte(hex[byte(r)>>4])
			b.WriteByte(hex[byte(r)&0x0f])
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// needsTSVEscape returns true if s contains any byte or rune that
// TSVEscape would rewrite. Used as a fast path so unaffected strings are
// returned without copying.
func needsTSVEscape(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7F || c == '\\' {
			return true
		}
	}
	// Slow path: U+200E is multi-byte (E2 80 8E). Only check if the lead
	// byte 0xE2 was present.
	if strings.IndexByte(s, 0xe2) >= 0 && strings.ContainsRune(s, '‎') {
		return true
	}
	return false
}
