// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"unicode/utf8"
)

const (
	MaxPrintfWidth     = 1 << 20
	MaxPrintfPrecision = 1 << 20
	MaxPrintfOutput    = 1 << 20

	minInt64Float          = -9223372036854775808.0
	maxInt64ExclusiveFloat = 9223372036854775808.0
	maxUint64Exclusive     = 18446744073709551616.0
)

func formatPrintf(format string, args []value) (string, error) {
	var b strings.Builder
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			if err := appendPrintfByte(&b, format[i]); err != nil {
				return "", err
			}
			continue
		}
		start := i
		i++
		if i >= len(format) {
			return "", fmt.Errorf("unterminated printf format")
		}
		if format[i] == '%' {
			if err := appendPrintfByte(&b, '%'); err != nil {
				return "", err
			}
			continue
		}
		for i < len(format) && strings.ContainsRune("-+ #0", rune(format[i])) {
			i++
		}
		flagsEnd := i - start
		if err := consumePrintfBound(format, &i, MaxPrintfWidth, "width"); err != nil {
			return "", err
		}
		if i < len(format) && format[i] == '.' {
			i++
			if err := consumePrintfBound(format, &i, MaxPrintfPrecision, "precision"); err != nil {
				return "", err
			}
		}
		if i >= len(format) {
			return "", fmt.Errorf("unterminated printf format")
		}
		verb := format[i]
		if verb == '*' {
			return "", fmt.Errorf("dynamic printf width is not supported")
		}
		spec := format[start : i+1]
		if verb == 's' || verb == 'c' {
			spec = strings.ReplaceAll(spec[:flagsEnd], "0", "") + spec[flagsEnd:]
		}
		if arg >= len(args) {
			return "", fmt.Errorf("not enough arguments for printf")
		}
		v := args[arg]
		arg++
		var out string
		switch verb {
		case 's':
			out = fmt.Sprintf(spec, v.String())
		case 'd', 'i':
			if verb == 'i' {
				spec = spec[:len(spec)-1] + "d"
			}
			out = fmt.Sprintf(spec, printfSigned(v))
		case 'u', 'o', 'x', 'X':
			if fallback, ok := formatPrintfUnsignedFallback(spec, v.Number()); ok {
				out = fallback
				break
			}
			if verb == 'u' {
				spec = spec[:len(spec)-1] + "d"
			}
			out = fmt.Sprintf(spec, printfUnsigned(v))
		case 'e', 'E', 'f', 'F', 'g', 'G':
			out = fmt.Sprintf(spec, v.Number())
		case 'c':
			out = formatPrintfChar(spec, v)
		default:
			return "", fmt.Errorf("unsupported printf format %%%c", verb)
		}
		if err := appendPrintfString(&b, out); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func formatPrintfUnsignedFallback(spec string, n float64) (string, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n >= minInt64Float && n < maxUint64Exclusive {
		return "", false
	}
	return fmt.Sprintf(spec[:len(spec)-1]+"g", n), true
}

func appendPrintfByte(b *strings.Builder, c byte) error {
	if b.Len() >= MaxPrintfOutput {
		return fmt.Errorf("printf output exceeds %d bytes", MaxPrintfOutput)
	}
	b.WriteByte(c)
	return nil
}

func appendPrintfString(b *strings.Builder, s string) error {
	if len(s) > MaxPrintfOutput-b.Len() {
		return fmt.Errorf("printf output exceeds %d bytes", MaxPrintfOutput)
	}
	b.WriteString(s)
	return nil
}

func consumePrintfBound(format string, idx *int, max int, name string) error {
	n := 0
	for *idx < len(format) && format[*idx] >= '0' && format[*idx] <= '9' {
		digit := int(format[*idx] - '0')
		if n > (max-digit)/10 {
			return fmt.Errorf("printf %s exceeds %d", name, max)
		}
		n = n*10 + digit
		(*idx)++
	}
	if n > max {
		return fmt.Errorf("printf %s exceeds %d", name, max)
	}
	return nil
}

func printfSigned(v value) any {
	n := v.Number()
	if n >= minInt64Float && n < maxInt64ExclusiveFloat {
		return int64(n)
	}
	return printfBigInt(n)
}

func printfUnsigned(v value) any {
	n := v.Number()
	if n >= 0 && n < maxUint64Exclusive {
		return uint64(n)
	}
	if n >= minInt64Float && n < 0 {
		return uint64(int64(n))
	}
	return printfBigInt(n)
}

func printfBigInt(n float64) *big.Int {
	if math.IsNaN(n) {
		return big.NewInt(0)
	}
	if math.IsInf(n, 1) {
		return new(big.Int).SetUint64(^uint64(0))
	}
	if math.IsInf(n, -1) {
		return big.NewInt(-9223372036854775807 - 1)
	}
	f := new(big.Float).SetPrec(64).SetFloat64(n)
	i, _ := f.Int(nil)
	if i == nil {
		return big.NewInt(0)
	}
	return i
}

func formatPrintfChar(spec string, v value) string {
	if v.kind == valueString && v.s != "" {
		r, size := utf8.DecodeRuneInString(v.s)
		if r == utf8.RuneError && size == 1 {
			return formatPrintfByte(spec, v.s[0])
		}
		return fmt.Sprintf(spec, r)
	}
	n := int64(v.Number())
	r := rune(n)
	if r < 0 || r > 0x10ffff || r >= 0xd800 && r <= 0xdfff {
		return formatPrintfByte(spec, byte(n))
	}
	return fmt.Sprintf(spec, r)
}

func formatPrintfByte(spec string, b byte) string {
	// Format a one-rune marker first so %c width and precision stay unchanged.
	formatted := fmt.Sprintf(spec, rune(0))
	return strings.ReplaceAll(formatted, "\x00", string([]byte{b}))
}
