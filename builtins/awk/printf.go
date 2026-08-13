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
			flags := strings.ReplaceAll(spec[:flagsEnd], "0", "")
			spec = flags + spec[flagsEnd:]
			flagsEnd = len(flags)
		}
		if arg >= len(args) {
			return "", fmt.Errorf("not enough arguments for printf")
		}
		v := args[arg]
		arg++
		var out string
		special, hasSpecial := formatPrintfSpecialNumber(spec, verb, v.Number())
		switch {
		case hasSpecial:
			out = special
		case verb == 's':
			out = fmt.Sprintf(spec, v.String())
		case verb == 'd' || verb == 'i':
			if verb == 'i' {
				spec = spec[:len(spec)-1] + "d"
			}
			out = fmt.Sprintf(spec, printfSigned(v))
		case strings.ContainsRune("uoxX", rune(verb)):
			n := v.Number()
			if fallback, ok := formatPrintfUnsignedFallback(spec, n); ok {
				out = fallback
				break
			}
			spec, flagsEnd = normalizePrintfUnsignedFlags(spec, flagsEnd)
			u := printfUnsigned(v)
			octalHasZeroPrecision := verb == 'o' && normalizePrintfZeroPrecision(spec, flagsEnd) != spec
			if n != 0 && printfUnsignedIsZero(u) {
				spec = normalizePrintfZeroPrecision(spec, flagsEnd)
			}
			if verb == 'u' {
				spec = spec[:len(spec)-1] + "d"
			} else if verb == 'x' || verb == 'X' {
				if n == 0 {
					spec = normalizePrintfHexZero(spec, flagsEnd)
				} else {
					spec = normalizePrintfHexWidth(spec, flagsEnd)
				}
			} else if verb == 'o' {
				if n == 0 {
					spec = normalizePrintfOctalZero(spec, flagsEnd)
				} else if !octalHasZeroPrecision {
					spec = normalizePrintfOctalPrecision(spec, flagsEnd, printfUnsignedIsZero(u))
				}
			}
			out = fmt.Sprintf(spec, u)
		case strings.ContainsRune("eEfFgG", rune(verb)):
			if verb == 'g' || verb == 'G' {
				spec = normalizePrintfGeneralPrecision(spec)
			}
			out = fmt.Sprintf(spec, v.Number())
		case verb == 'c':
			out = formatPrintfChar(spec, flagsEnd, v)
		default:
			return "", fmt.Errorf("unsupported printf format %%%c", verb)
		}
		if err := appendPrintfString(&b, out); err != nil {
			return "", err
		}
	}
	return b.String(), nil
}

func formatPrintfSpecialNumber(spec string, verb byte, n float64) (string, bool) {
	if !strings.ContainsRune("diuoxXeEfFgG", rune(verb)) {
		return "", false
	}
	return formatAwkSpecialNumber(n, strings.ContainsRune("XEFG", rune(verb)))
}

func formatPrintfUnsignedFallback(spec string, n float64) (string, bool) {
	if math.IsNaN(n) || math.IsInf(n, 0) || n >= minInt64Float && n < maxUint64Exclusive {
		return "", false
	}
	spec = spec[:len(spec)-1] + "g"
	return fmt.Sprintf(normalizePrintfGeneralPrecision(spec), n), true
}

func normalizePrintfGeneralPrecision(spec string) string {
	if strings.Contains(spec[:len(spec)-1], ".") {
		return spec
	}
	return spec[:len(spec)-1] + ".6" + spec[len(spec)-1:]
}

func normalizePrintfUnsignedFlags(spec string, flagsEnd int) (string, int) {
	flags := strings.ReplaceAll(spec[:flagsEnd], "+", "")
	flags = strings.ReplaceAll(flags, " ", "")
	return flags + spec[flagsEnd:], len(flags)
}

func normalizePrintfHexZero(spec string, flagsEnd int) string {
	if strings.Contains(spec[1:flagsEnd], "#") {
		spec = normalizePrintfZeroPrecision(spec, flagsEnd)
	}
	return strings.ReplaceAll(spec[:flagsEnd], "#", "") + spec[flagsEnd:]
}

func normalizePrintfHexWidth(spec string, flagsEnd int) string {
	flags := spec[1:flagsEnd]
	if !strings.Contains(flags, "#") || !strings.Contains(flags, "0") || strings.Contains(flags, "-") {
		return spec
	}
	width := 0
	widthEnd := flagsEnd
	for widthEnd < len(spec)-1 && spec[widthEnd] >= '0' && spec[widthEnd] <= '9' {
		width = width*10 + int(spec[widthEnd]-'0')
		widthEnd++
	}
	if widthEnd == flagsEnd || spec[widthEnd] == '.' {
		return spec
	}
	if width <= 2 {
		return spec[:flagsEnd] + spec[widthEnd:]
	}
	return fmt.Sprintf("%s%d%s", spec[:flagsEnd], width-2, spec[widthEnd:])
}

func normalizePrintfOctalZero(spec string, flagsEnd int) string {
	if !strings.Contains(spec[1:flagsEnd], "#") {
		return spec
	}
	return normalizePrintfZeroPrecision(spec, flagsEnd)
}

func normalizePrintfOctalPrecision(spec string, flagsEnd int, convertedZero bool) string {
	if !strings.Contains(spec[1:flagsEnd], "#") {
		return spec
	}
	precisionStart := strings.IndexByte(spec[flagsEnd:len(spec)-1], '.')
	if precisionStart < 0 {
		if !convertedZero {
			return spec
		}
		return spec[:len(spec)-1] + ".2" + spec[len(spec)-1:]
	}
	precisionStart += flagsEnd + 1
	precisionEnd := len(spec) - 1
	precision := 0
	for i := precisionStart; i < precisionEnd; i++ {
		precision = precision*10 + int(spec[i]-'0')
	}
	return fmt.Sprintf("%s%d%s", spec[:precisionStart], precision+1, spec[precisionEnd:])
}

func normalizePrintfZeroPrecision(spec string, flagsEnd int) string {
	precisionStart := strings.IndexByte(spec[flagsEnd:len(spec)-1], '.')
	if precisionStart < 0 {
		return spec
	}
	precisionStart += flagsEnd + 1
	precisionEnd := len(spec) - 1
	for i := precisionStart; i < precisionEnd; i++ {
		if spec[i] != '0' {
			return spec
		}
	}
	return spec[:precisionStart] + "1" + spec[precisionEnd:]
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

func printfUnsignedIsZero(v any) bool {
	switch n := v.(type) {
	case uint64:
		return n == 0
	case *big.Int:
		return n.Sign() == 0
	default:
		return false
	}
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

func formatPrintfChar(spec string, flagsEnd int, v value) string {
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
	return fmt.Sprintf(normalizePrintfCharWidth(spec, flagsEnd, len(string(r))), r)
}

func normalizePrintfCharWidth(spec string, flagsEnd, runeBytes int) string {
	if runeBytes <= 1 {
		return spec
	}
	width := 0
	widthEnd := flagsEnd
	for widthEnd < len(spec)-1 && spec[widthEnd] >= '0' && spec[widthEnd] <= '9' {
		width = width*10 + int(spec[widthEnd]-'0')
		widthEnd++
	}
	if widthEnd == flagsEnd {
		return spec
	}
	width -= runeBytes - 1
	if width < 1 {
		width = 1
	}
	return fmt.Sprintf("%s%d%s", spec[:flagsEnd], width, spec[widthEnd:])
}

func formatPrintfByte(spec string, b byte) string {
	// Format a one-rune marker first so %c width and precision stay unchanged.
	formatted := fmt.Sprintf(spec, rune(0))
	return strings.ReplaceAll(formatted, "\x00", string([]byte{b}))
}
