// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// awkSprintf implements awk's printf/sprintf using a hand-written conversion
// engine. We do this rather than delegating to fmt because awk's semantics
// for %d (truncate toward zero), %c (string→first char, number→Unicode rune),
// and string conversion of numbers (using OFMT) differ from Go's fmt.
//
// Supported conversions: d i o u x X c s e E f F g G %.
// Supported flags: - + space # 0.
// Width and precision: integer literal or '*' (consumes next arg).
//
// Unrecognised conversion specifiers fall back to writing the literal source
// text — this matches awk's lenient behaviour and prevents unexpected errors.
func awkSprintf(format string, values []awkValue, convFmt string) (string, error) {
	var sb strings.Builder
	argIdx := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			sb.WriteByte(c)
			// Check size after each literal byte to ensure the format string
			// itself (which may consist entirely of literal chars) cannot grow
			// sb beyond the 1 MiB cap before a %-spec is encountered.
			if sb.Len() > MaxStringBytes {
				return "", fmt.Errorf("printf output exceeds maximum string length %d", MaxStringBytes)
			}
			continue
		}
		// Find the end of the conversion specifier.
		j, spec, err := parsePrintfSpec(format, i)
		if err != nil {
			return "", err
		}
		if spec.literalPercent {
			sb.WriteByte('%')
			i = j
			continue
		}
		// Resolve width/precision if they used '*'.
		if spec.widthFromArg {
			w, err := nextInt(&argIdx, values)
			if err != nil {
				return "", err
			}
			// nextInt clamps to [math.MinInt32, MaxStringBytes], so w == math.MinInt64
			// is unreachable; plain -w is safe for all values nextInt can return.
			if w < 0 {
				spec.flagMinus = true
				w = -w
			}
			if w > MaxStringBytes {
				w = MaxStringBytes
			}
			spec.width = w
		}
		if spec.precFromArg {
			p, err := nextInt(&argIdx, values)
			if err != nil {
				return "", err
			}
			if p < 0 {
				spec.precision = -1 // negative precision is "no precision"
			} else {
				if p > MaxStringBytes {
					p = MaxStringBytes
				}
				spec.precision = p
			}
		}
		// Take the value argument (if needed).
		var val awkValue
		needsArg := spec.verb != 0
		if needsArg {
			if argIdx >= len(values) {
				val = uninitValue
			} else {
				val = values[argIdx]
				argIdx++
			}
		}
		out, err := applyPrintfSpec(spec, val, convFmt)
		if err != nil {
			return "", err
		}
		sb.WriteString(out)
		if sb.Len() > MaxStringBytes {
			return "", fmt.Errorf("printf output exceeds maximum string length %d", MaxStringBytes)
		}
		i = j
	}
	return sb.String(), nil
}

// printfSpec captures one parsed conversion specifier.
type printfSpec struct {
	flagMinus bool
	flagPlus  bool
	flagSpace bool
	flagHash  bool
	flagZero  bool

	width        int
	widthFromArg bool

	precision   int // -1 means "no precision specified"
	precFromArg bool

	verb byte

	literalPercent bool
}

// parsePrintfSpec parses a %... conversion starting at format[i] (i indexes
// the '%'). It returns (endIndex, spec). endIndex is the index of the verb
// character; the caller advances by endIndex.
func parsePrintfSpec(format string, i int) (int, printfSpec, error) {
	spec := printfSpec{precision: -1}
	j := i + 1
	if j >= len(format) {
		return j, spec, errors.New("printf: incomplete format specifier '%'")
	}
	// Flags.
flagLoop:
	for j < len(format) {
		switch format[j] {
		case '-':
			spec.flagMinus = true
		case '+':
			spec.flagPlus = true
		case ' ':
			spec.flagSpace = true
		case '#':
			spec.flagHash = true
		case '0':
			spec.flagZero = true
		default:
			break flagLoop
		}
		j++
	}
	// Width.
	if j < len(format) && format[j] == '*' {
		spec.widthFromArg = true
		j++
	} else {
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			spec.width = spec.width*10 + int(format[j]-'0')
			if spec.width > MaxStringBytes {
				return j, spec, errors.New("printf: width too large")
			}
			j++
		}
	}
	// Precision.
	if j < len(format) && format[j] == '.' {
		j++
		spec.precision = 0
		if j < len(format) && format[j] == '*' {
			spec.precFromArg = true
			j++
		} else {
			for j < len(format) && format[j] >= '0' && format[j] <= '9' {
				spec.precision = spec.precision*10 + int(format[j]-'0')
				if spec.precision > 1<<20 {
					return j, spec, errors.New("printf: precision too large")
				}
				j++
			}
		}
	}
	if j >= len(format) {
		return j, spec, errors.New("printf: incomplete format specifier")
	}
	verb := format[j]
	if verb == '%' {
		spec.literalPercent = true
		return j, spec, nil
	}
	spec.verb = verb
	return j, spec, nil
}

func nextInt(idx *int, values []awkValue) (int, error) {
	if *idx >= len(values) {
		return 0, errors.New("printf: not enough arguments for *")
	}
	v := values[*idx]
	*idx++
	// Use floatToInt64Safe to avoid implementation-defined behaviour for NaN/±Inf,
	// then clamp to [math.MinInt32, MaxStringBytes] before converting to int so
	// the cast is safe on 32-bit platforms.
	n64 := floatToInt64Safe(v.toNumber())
	if n64 > int64(MaxStringBytes) {
		n64 = int64(MaxStringBytes)
	} else if n64 < math.MinInt32 {
		n64 = math.MinInt32
	}
	return int(n64), nil
}

// applyPrintfSpec formats a single value using the parsed spec.
func applyPrintfSpec(spec printfSpec, val awkValue, convFmt string) (string, error) {
	switch spec.verb {
	case 'd', 'i':
		return formatInt(spec, floatToInt64Safe(val.toNumber())), nil
	case 'o':
		return formatUnsigned(spec, awkToUint32(val.toNumber()), 8), nil
	case 'u':
		return formatUnsigned(spec, awkToUint32(val.toNumber()), 10), nil
	case 'x':
		return formatUnsigned(spec, awkToUint32(val.toNumber()), 16), nil
	case 'X':
		s := formatUnsigned(spec, awkToUint32(val.toNumber()), 16)
		return strings.ToUpper(s), nil
	case 'c':
		return formatChar(spec, val, convFmt), nil
	case 's':
		s := val.toString(convFmt)
		if spec.precision >= 0 && spec.precision < len(s) {
			s = s[:spec.precision]
		}
		return padString(s, spec), nil
	case 'e', 'E', 'f', 'g', 'G':
		return formatFloat(spec, val.toNumber()), nil
	case 'F':
		// Go's strconv.FormatFloat does not support 'F'; fold to 'f' and uppercase.
		// However, for ±Inf and NaN, gawk/mawk output lowercase "+inf"/"-inf"/"nan"
		// even for %F — the ToUpper only applies to finite-number digits.
		f := val.toNumber()
		spec.verb = 'f'
		s := formatFloat(spec, f)
		if !math.IsInf(f, 0) && !math.IsNaN(f) {
			s = strings.ToUpper(s)
		}
		return s, nil
	}
	// Unknown verb: emit the original spec literally.
	return "%" + string(spec.verb), nil
}

func formatInt(spec printfSpec, n int64) string {
	abs := n
	negative := false
	if n < 0 {
		negative = true
		// Handle MinInt64 specially since -MinInt64 overflows.
		if n == -9223372036854775808 {
			absStr := "9223372036854775808"
			return padNumber(spec, "-", absStr)
		}
		abs = -n
	}
	digits := strconv.FormatInt(abs, 10)
	if spec.precision >= 0 {
		// Pad digits with leading zeros to meet precision (but precision 0 + value 0 = empty string).
		if spec.precision == 0 && abs == 0 {
			digits = ""
		} else if len(digits) < spec.precision {
			digits = strings.Repeat("0", spec.precision-len(digits)) + digits
		}
	}
	prefix := ""
	switch {
	case negative:
		prefix = "-"
	case spec.flagPlus:
		prefix = "+"
	case spec.flagSpace:
		prefix = " "
	}
	return padNumber(spec, prefix, digits)
}

// awkToUint32 converts a float to a uint32 value matching gawk/mawk semantics:
//   - NaN or negative values → 0
//   - Values > math.MaxUint32 → math.MaxUint32 (saturate, not wrap)
//   - Otherwise truncate toward zero to uint32
//
// This differs from a raw uint64(floatToInt64Safe(f)) cast, which would give
// 64-bit wrap-around semantics.
func awkToUint32(f float64) uint64 {
	if math.IsNaN(f) || f < 0 || math.IsInf(f, -1) {
		return 0
	}
	if f > math.MaxUint32 || math.IsInf(f, 1) {
		return math.MaxUint32
	}
	return uint64(uint32(f))
}

func formatUnsigned(spec printfSpec, n uint64, base int) string {
	digits := strconv.FormatUint(n, base)
	if spec.precision >= 0 {
		if spec.precision == 0 && n == 0 {
			digits = ""
		} else if len(digits) < spec.precision {
			digits = strings.Repeat("0", spec.precision-len(digits)) + digits
		}
	}
	prefix := ""
	if spec.flagHash {
		switch base {
		case 8:
			if !strings.HasPrefix(digits, "0") {
				digits = "0" + digits
			}
		case 16:
			prefix = "0x"
		}
	}
	return padNumber(spec, prefix, digits)
}

func formatChar(spec printfSpec, v awkValue, convFmt string) string {
	var ch string
	switch v.kind {
	case valNum:
		// POSIX awk: %c on a number emits the byte modulo 256, not a Unicode rune.
		ch = string([]byte{byte(floatToInt64Safe(v.f) & 0xFF)})
	default:
		s := v.toString(convFmt)
		if s == "" {
			ch = "\x00"
		} else {
			ch = s[:1]
		}
	}
	return padString(ch, spec)
}

func formatFloat(spec printfSpec, f float64) string {
	prec := spec.precision
	if prec < 0 {
		prec = 6
	}
	// Normalise special IEEE-754 values to match gawk/mawk output.
	// Go's strconv.FormatFloat returns "+Inf"/"-Inf"/"NaN" (capitalised);
	// gawk always outputs "+inf" for positive infinity (the "+" is always
	// present, not conditional on flagPlus), "-inf" for negative infinity,
	// and "nan" for NaN — all lowercase.
	if math.IsInf(f, 1) {
		// Positive infinity: gawk always prints "+inf", but respects the
		// space flag by prepending a space before the "+".
		if spec.flagSpace {
			return padNumber(spec, " ", "+inf")
		}
		return padNumber(spec, "", "+inf")
	}
	if math.IsInf(f, -1) {
		return padNumber(spec, "", "-inf")
	}
	if math.IsNaN(f) {
		return padNumber(spec, "", "nan")
	}
	verb := spec.verb
	digits := strconv.FormatFloat(f, byte(verb), prec, 64)
	prefix := ""
	if !strings.HasPrefix(digits, "-") {
		switch {
		case spec.flagPlus:
			prefix = "+"
		case spec.flagSpace:
			prefix = " "
		}
	}
	if strings.HasPrefix(digits, "-") {
		prefix = "-"
		digits = digits[1:]
	}
	return padNumber(spec, prefix, digits)
}

// padNumber applies width and zero/space padding to a numeric output. The
// numeric is split into prefix (sign / 0x) and digits so that '0' padding
// happens between them.
func padNumber(spec printfSpec, prefix, digits string) string {
	full := prefix + digits
	if spec.width <= len(full) {
		return full
	}
	padCount := spec.width - len(full)
	switch {
	case spec.flagMinus:
		return full + strings.Repeat(" ", padCount)
	case spec.flagZero && spec.precision < 0:
		return prefix + strings.Repeat("0", padCount) + digits
	default:
		return strings.Repeat(" ", padCount) + full
	}
}

func padString(s string, spec printfSpec) string {
	if spec.width <= len(s) {
		return s
	}
	padCount := spec.width - len(s)
	if spec.flagMinus {
		return s + strings.Repeat(" ", padCount)
	}
	return strings.Repeat(" ", padCount) + s
}
