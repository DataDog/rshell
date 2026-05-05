// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"math"
	"strconv"
	"strings"
)

// valueKind classifies awk values. Awk's data type is dynamic: a value is
// either a string, a number, or a "string-number" (a string that came from
// input or a -v assignment that looks numeric and may be coerced either way).
type valueKind uint8

const (
	valUninit valueKind = iota // never assigned: numeric 0, string ""
	valNum                     // pure number
	valStr                     // pure string (e.g. literal)
	valStrNum                  // string that looks numeric (input field, -v)
)

// awkValue is the runtime representation of an awk value. We carry both the
// string and float forms when relevant so that conversions are cheap and the
// numeric-ness of a string can be cached.
type awkValue struct {
	kind valueKind
	s    string
	f    float64
}

// numValue creates a numeric value.
func numValue(f float64) awkValue {
	return awkValue{kind: valNum, f: f}
}

// strValue creates a pure string value.
func strValue(s string) awkValue {
	return awkValue{kind: valStr, s: s}
}

// strNumValue creates a string-number value (string that may be coerced to
// number per POSIX rules).
func strNumValue(s string) awkValue {
	return awkValue{kind: valStrNum, s: s}
}

// uninitValue is the zero awkValue (kind=valUninit) — implicitly ""/0.
var uninitValue = awkValue{}

// isTrue reports whether v is "true" in awk's boolean sense:
//   - Numbers: non-zero is true.
//   - Pure strings: non-empty is true.
//   - String-numbers: numeric value non-zero (per POSIX).
//   - Uninitialised: false.
func (v awkValue) isTrue() bool {
	switch v.kind {
	case valNum:
		return v.f != 0
	case valStr:
		return v.s != ""
	case valStrNum:
		if looksNumeric(v.s) {
			return v.toNumber() != 0
		}
		return v.s != ""
	}
	return false
}

// toNumber converts the value to a float64 per POSIX rules.
func (v awkValue) toNumber() float64 {
	switch v.kind {
	case valNum:
		return v.f
	case valStr, valStrNum:
		return parseAwkNumber(v.s)
	}
	return 0
}

// toString converts the value to a string, using convFmt for non-integer
// numbers (mirrors awk's CONVFMT behaviour). For integer values, the integer
// representation is used regardless of convFmt.
func (v awkValue) toString(convFmt string) string {
	switch v.kind {
	case valStr, valStrNum:
		return v.s
	case valNum:
		return formatNumber(v.f, convFmt)
	}
	return ""
}

// parseAwkNumber implements awk's lenient string-to-number conversion:
// optional whitespace, optional sign, an optional integer part, an optional
// fractional part, an optional exponent, and any trailing junk yields the
// number parsed up to that point. Empty / non-numeric strings yield 0.
//
// We deliberately avoid hex/octal — POSIX awk treats numbers as decimal only.
func parseAwkNumber(s string) float64 {
	s = strings.TrimLeft(s, " \t\n\r\f\v")
	if s == "" {
		return 0
	}
	i := 0
	// Optional sign.
	sign := 1.0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		if s[i] == '-' {
			sign = -1.0
		}
		i++
	}
	// Detect inf/nan (case-insensitive) — gawk treats these as numeric.
	lower := strings.ToLower(s[i:])
	if strings.HasPrefix(lower, "inf") {
		return math.Inf(int(sign))
	}
	if strings.HasPrefix(lower, "nan") {
		return math.NaN()
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i == start || (i == start+1 && s[start] == '.') {
		// No digits parsed yet (only sign and/or dot).
		return 0
	}
	// Optional exponent.
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		expStart := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		if j > expStart {
			i = j
		}
	}
	f, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		// Overflow returns +/-Inf (acceptable in awk).
		if errIsRange(err) {
			return f
		}
		return 0
	}
	return f
}

// looksNumeric reports whether a string is a "numeric string" in awk's
// terminology: the entire trimmed value is a valid number.
// Note: IEEE-754 special values ("nan", "inf", etc.) are excluded because
// mawk (the reference implementation on bookworm-slim) does not classify them
// as numeric strings for comparison purposes.
func looksNumeric(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	// Exclude nan/inf: our implementation uses string comparison for these
	// special tokens rather than converting them to IEEE-754 NaN/±Infinity.
	// Note: both gawk and mawk do treat "inf"/"nan" as numeric strings, so
	// this is an intentional divergence. The practical impact is that
	// "nan" == "nan" returns 1 here (string equality) whereas gawk/mawk return
	// 0 (NaN ≠ NaN by IEEE-754). The divergence is documented in SHELL_FEATURES.md.
	lower := strings.ToLower(t)
	if lower == "nan" || lower == "inf" || lower == "+inf" || lower == "-inf" {
		return false
	}
	_, err := strconv.ParseFloat(t, 64)
	return err == nil || errIsRange(err)
}

// errIsRange reports whether err is strconv.ErrRange.
func errIsRange(err error) bool {
	if err == nil {
		return false
	}
	ne, ok := err.(*strconv.NumError)
	return ok && ne.Err == strconv.ErrRange
}

// formatNumber formats a float using awk's rule: integral values within
// int64 range render without a decimal point; otherwise, format with the
// supplied CONVFMT/OFMT directive. NaN and ±Inf get awk-style names.
func formatNumber(f float64, fmt string) string {
	if math.IsNaN(f) {
		return "nan"
	}
	if math.IsInf(f, 1) {
		return "inf"
	}
	if math.IsInf(f, -1) {
		return "-inf"
	}
	if f == math.Trunc(f) && !math.IsInf(f, 0) && f >= -9007199254740992 && f <= 9007199254740992 {
		// Render as integer.
		return strconv.FormatInt(int64(f), 10)
	}
	// Honour the limited subset of fmt directives that awk uses (e.g. "%.6g").
	out, ok := formatFloatWithFmt(f, fmt)
	if ok {
		return out
	}
	return strconv.FormatFloat(f, 'g', 6, 64)
}

// formatFloatWithFmt parses a small subset of printf conversion specifiers
// commonly seen in CONVFMT/OFMT and applies it to f. Returns (s, true) if the
// directive was understood, (zero, false) otherwise. Unsupported directives
// fall back to "%.6g" by the caller.
//
// Supported:
//
//	%[.prec][gGfeEd]   no flags / width
//
// We accept "%.6g" (default) and a few near-equivalents to keep behaviour
// stable without bringing the full printf engine into the hot path.
func formatFloatWithFmt(f float64, spec string) (string, bool) {
	// Cheap fast path for the default.
	if spec == "" || spec == "%.6g" {
		return strconv.FormatFloat(f, 'g', 6, 64), true
	}
	if len(spec) < 2 || spec[0] != '%' {
		return "", false
	}
	i := 1
	prec := -1
	if i < len(spec) && spec[i] == '.' {
		i++
		prec = 0
		for i < len(spec) && spec[i] >= '0' && spec[i] <= '9' {
			prec = prec*10 + int(spec[i]-'0')
			i++
			if prec > 64 { // Sanity cap.
				return "", false
			}
		}
	}
	if i != len(spec)-1 {
		return "", false
	}
	verb := spec[i]
	switch verb {
	case 'g', 'G', 'f', 'F', 'e', 'E':
		if prec < 0 {
			prec = 6
		}
		return strconv.FormatFloat(f, byte(verb), prec, 64), true
	case 'd':
		return strconv.FormatInt(int64(f), 10), true
	}
	return "", false
}
