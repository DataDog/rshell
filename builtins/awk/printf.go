// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

func formatPrintf(format string, args []value) (string, error) {
	var b strings.Builder
	arg := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		start := i
		i++
		if i >= len(format) {
			return "", fmt.Errorf("unterminated printf format")
		}
		if format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		for i < len(format) && strings.ContainsRune("-+ #0", rune(format[i])) {
			i++
		}
		for i < len(format) && format[i] >= '0' && format[i] <= '9' {
			i++
		}
		if i < len(format) && format[i] == '.' {
			i++
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
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
		if arg >= len(args) {
			return "", fmt.Errorf("not enough arguments for printf")
		}
		v := args[arg]
		arg++
		switch verb {
		case 's':
			b.WriteString(fmt.Sprintf(spec, v.String()))
		case 'd', 'i':
			if verb == 'i' {
				spec = spec[:len(spec)-1] + "d"
			}
			b.WriteString(fmt.Sprintf(spec, int64(v.Number())))
		case 'u':
			b.WriteString(fmt.Sprintf(spec, uint64(v.Number())))
		case 'o', 'x', 'X':
			b.WriteString(fmt.Sprintf(spec, int64(v.Number())))
		case 'e', 'E', 'f', 'F', 'g', 'G':
			b.WriteString(fmt.Sprintf(spec, v.Number()))
		case 'c':
			b.WriteString(fmt.Sprintf(spec, printfRune(v)))
		default:
			return "", fmt.Errorf("unsupported printf format %%%c", verb)
		}
	}
	return b.String(), nil
}

func printfRune(v value) rune {
	if v.kind == valueString && v.s != "" {
		r, _ := utf8.DecodeRuneInString(v.s)
		return r
	}
	return rune(int64(v.Number()))
}
