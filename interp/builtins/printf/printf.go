// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package printf implements the printf builtin command.
//
// printf — format and print data
//
// Usage: printf FORMAT [ARGUMENT]...
//
// Write formatted output to standard output. FORMAT is a string that
// contains literal text and format specifiers (introduced by %). Each
// format specifier consumes the next ARGUMENT and formats it.
//
// If there are more ARGUMENTs than format specifiers, the FORMAT string
// is reused from the beginning until all arguments are consumed (bounded
// to 10,000 iterations to prevent runaway loops).
//
// Missing arguments default to "" for string specifiers and 0 for
// numeric specifiers.
//
// Accepted flags:
//
//	--help
//	    Print a usage message to stderr and exit 2.
//
// Rejected flags:
//
//	-v varname
//	    Bash extension to assign output to a variable. Not supported
//	    in the restricted shell.
//
// Format specifiers:
//
//	%s     String.
//	%b     String with backslash escape interpretation (like echo -e).
//	       \c in %b stops all further output.
//	%c     First character of the argument.
//	%d, %i Signed decimal integer.
//	%o     Unsigned octal integer.
//	%u     Unsigned decimal integer.
//	%x, %X Unsigned hexadecimal integer (lower/upper).
//	%e, %E Scientific notation float.
//	%f, %F Decimal float.
//	%g, %G Shortest float representation.
//	%%     Literal percent sign.
//
// Width and precision modifiers are supported (e.g. %10s, %-10s, %.5f,
// %010d). Flag characters: - (left-align), + (sign), ' ' (space),
// 0 (zero-pad), # (alternate form).
//
// Escape sequences in FORMAT string:
//
//	\\    backslash
//	\a    alert (BEL)
//	\b    backspace
//	\f    form feed
//	\n    newline
//	\r    carriage return
//	\t    horizontal tab
//	\v    vertical tab
//	\"    double quote
//	\NNN  octal byte value (1-3 digits)
//	\0NNN octal byte value (0 + 1-3 digits)
//	\xHH  hexadecimal byte value (1-2 digits)
//
// Numeric argument extensions:
//
//	Arguments for numeric specifiers may be:
//	- Decimal integers: 42, -7, +3
//	- Octal: 0755
//	- Hexadecimal: 0xff, 0XFF
//	- Character constants: "'A" or '"A' gives the ASCII value of A
//
// Not implemented (rejected):
//
//	%n     Byte count write (security risk). Produces an error.
//	%q     Shell-quoting (bash extension, not POSIX).
//	%a, %A Hexadecimal float (deferred).
//
// Exit codes:
//
//	0  Successful completion (conversion warnings may still be emitted).
//	1  Format error (invalid number, unknown specifier, incomplete specifier).
//	2  Usage error (no format string provided).
//
// Memory safety:
//
//	printf does not read files or stdin. All output is generated from
//	the format string and arguments. The format reuse loop is bounded
//	to maxFormatIterations (10,000) and checks ctx.Err() on each
//	iteration to honour the shell's execution timeout.
package printf

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/interp/builtins"
)

// Cmd is the printf builtin command descriptor.
// printf uses NoFlags because its arguments (format string and data) can look
// like flags (e.g. printf "%d" -42). Manual pre-parsing handles --help and -v.
var Cmd = builtins.Command{Name: "printf", MakeFlags: builtins.NoFlags(run)}

// maxFormatIterations bounds the format-reuse loop to prevent runaway output.
const maxFormatIterations = 10_000

// bashFloat fixes Go's NaN/Inf casing to match bash's lowercase output
// for lowercase format verbs (f, e, g). Go outputs "NaN" and "+Inf"/"-Inf"
// but bash outputs "nan", "inf", "-inf".
func bashFloat(s string) string {
	s = strings.ReplaceAll(s, "NaN", "nan")
	s = strings.ReplaceAll(s, "+Inf", "inf")
	s = strings.ReplaceAll(s, "-Inf", "-inf")
	s = strings.ReplaceAll(s, "Inf", "inf")
	return s
}

// maxWidthOrPrec caps width/precision values to prevent huge allocations.
const maxWidthOrPrec = 10_000

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	// Manual flag handling: only --help/-h is accepted; -v is rejected.
	// -- terminates options (allows format strings starting with -).
	if len(args) > 0 {
		switch args[0] {
		case "--help":
			callCtx.Errf("printf: usage: printf [-v var] format [arguments]\n")
			return builtins.Result{Code: 2}
		case "-v":
			callCtx.Errf("printf: -v: not supported in restricted shell\n")
			return builtins.Result{Code: 1}
		case "--":
			args = args[1:] // skip --
		}
	}

	if len(args) == 0 {
		callCtx.Errf("printf: usage: printf [-v var] format [arguments]\n")
		return builtins.Result{Code: 2}
	}

	format := args[0]
	fmtArgs := args[1:]

	argIdx := 0
	hadError := false
	iterations := 0

	for {
		if ctx.Err() != nil {
			break
		}
		if iterations >= maxFormatIterations {
			break
		}
		iterations++

		startArgIdx := argIdx
		stop, err := processFormat(callCtx, format, fmtArgs, &argIdx, &hadError)
		if err {
			hadError = true
		}
		if stop {
			// \c in %b — stop all output immediately.
			break
		}

		// If no args were consumed in this pass, or we've consumed all args, stop.
		if argIdx <= startArgIdx || argIdx >= len(fmtArgs) {
			break
		}
		// More args remain — reuse the format string.
	}

	if hadError {
		return builtins.Result{Code: 1}
	}
	return builtins.Result{}
}

// processFormat walks the format string once, outputting literal text and
// processing format specifiers. It returns (stop, hadError).
// stop is true if \c was encountered in a %b argument.
func processFormat(callCtx *builtins.CallContext, format string, args []string, argIdx *int, hadError *bool) (bool, bool) {
	i := 0
	for i < len(format) {
		ch := format[i]

		if ch == '\\' {
			// Process escape sequence in format string.
			s, advance := processFormatEscape(format[i:])
			callCtx.Out(s)
			i += advance
			continue
		}

		if ch == '%' {
			if i+1 < len(format) && format[i+1] == '%' {
				callCtx.Out("%")
				i += 2
				continue
			}
			stop, advance, err := processSpecifier(callCtx, format[i:], args, argIdx)
			if err {
				*hadError = true
			}
			if stop {
				return true, *hadError
			}
			i += advance
			continue
		}

		// Batch consecutive literal characters into a single write.
		start := i
		for i < len(format) && format[i] != '\\' && format[i] != '%' {
			i++
		}
		callCtx.Out(format[start:i])
	}
	return false, *hadError
}

// processFormatEscape handles a backslash escape in the format string (not in %b arguments).
// Returns the replacement string and the number of bytes consumed from s.
func processFormatEscape(s string) (string, int) {
	if len(s) < 2 {
		return "\\", 1
	}
	switch s[1] {
	case '\\':
		return "\\", 2
	case 'a':
		return "\a", 2
	case 'b':
		return "\b", 2
	case 'f':
		return "\f", 2
	case 'n':
		return "\n", 2
	case 'r':
		return "\r", 2
	case 't':
		return "\t", 2
	case 'v':
		return "\v", 2
	case '"':
		return "\"", 2
	case '0':
		// \0NNN — octal (0 + up to 3 digits)
		val, consumed := parseOctal(s[2:], 3)
		return string(rune(val)), 2 + consumed
	case 'x':
		// \xHH — hex (up to 2 digits)
		val, consumed := parseHex(s[2:], 2)
		if consumed == 0 {
			return "\\x", 2
		}
		return string(rune(val)), 2 + consumed
	default:
		if s[1] >= '1' && s[1] <= '7' {
			// \NNN — octal without leading 0 (1-3 digits)
			val, consumed := parseOctal(s[1:], 3)
			return string(rune(val)), 1 + consumed
		}
		// Unknown escape: output backslash and character.
		return string([]byte{'\\', s[1]}), 2
	}
}

// processSpecifier handles a single % format specifier starting at s[0]=='%'.
// Returns (stop, bytesConsumed, hadError).
func processSpecifier(callCtx *builtins.CallContext, s string, args []string, argIdx *int) (bool, int, bool) {
	i := 1 // skip '%'
	hadError := false

	// Parse flags: -, +, ' ', 0, #
	var flags strings.Builder
	for i < len(s) {
		switch s[i] {
		case '-', '+', ' ', '0', '#':
			flags.WriteByte(s[i])
			i++
			continue
		}
		break
	}

	// Parse width (digits or *)
	var width string
	if i < len(s) && s[i] == '*' {
		// Width from argument.
		w, err := getIntArg(args, argIdx, callCtx)
		if err {
			hadError = true
		}
		width = strconv.Itoa(w)
		i++
	} else {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		width = s[start:i]
	}

	// Parse precision
	var precision string
	hasPrecision := false
	if i < len(s) && s[i] == '.' {
		hasPrecision = true
		i++ // skip '.'
		if i < len(s) && s[i] == '*' {
			p, err := getIntArg(args, argIdx, callCtx)
			if err {
				hadError = true
			}
			if p < 0 {
				// Negative precision from * means "no precision specified" in bash.
				hasPrecision = false
			} else {
				precision = strconv.Itoa(p)
			}
			i++
		} else {
			start := i
			for i < len(s) && s[i] >= '0' && s[i] <= '9' {
				i++
			}
			precision = s[start:i]
		}
	}

	// Clamp width/precision for safety.
	if w, err := strconv.Atoi(width); err == nil && (w > maxWidthOrPrec || w < -maxWidthOrPrec) {
		if w > 0 {
			width = strconv.Itoa(maxWidthOrPrec)
		} else {
			width = strconv.Itoa(-maxWidthOrPrec)
		}
	}
	if p, err := strconv.Atoi(precision); err == nil && p > maxWidthOrPrec {
		precision = strconv.Itoa(maxWidthOrPrec)
	}

	if i >= len(s) {
		// Incomplete specifier — bash errors on this.
		callCtx.Errf("printf: `%s': missing format character\n", s[:i])
		return false, i, true
	}

	verb := s[i]
	i++ // consume verb

	// Build Go format string.
	var goFmt strings.Builder
	goFmt.WriteByte('%')
	goFmt.WriteString(flags.String())
	goFmt.WriteString(width)
	if hasPrecision {
		goFmt.WriteByte('.')
		goFmt.WriteString(precision)
	}

	switch verb {
	case 's':
		arg := getStringArg(args, argIdx)
		goFmt.WriteByte('s')
		callCtx.Out(fmt.Sprintf(goFmt.String(), arg))

	case 'b':
		arg := getStringArg(args, argIdx)
		processed, stop := processBEscapes(arg)
		// Apply width/precision formatting to the processed string.
		goFmt.WriteByte('s')
		callCtx.Out(fmt.Sprintf(goFmt.String(), processed))
		if stop {
			return true, i, hadError
		}

	case 'c':
		arg := getStringArg(args, argIdx)
		if len(arg) > 0 {
			// %c prints the first character (byte).
			goFmt.WriteByte('c')
			callCtx.Out(fmt.Sprintf(goFmt.String(), arg[0]))
		} else {
			// Empty argument produces a NUL byte (bash behavior).
			callCtx.Out("\x00")
		}

	case 'd', 'i':
		arg := getStringArg(args, argIdx)
		val, err := parseIntArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			// Bash continues with value 0 and sets exit code.
			val = 0
			goFmt.WriteByte('d')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('d')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'o':
		arg := getStringArg(args, argIdx)
		val, err := parseUintArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('o')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('o')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'u':
		arg := getStringArg(args, argIdx)
		val, err := parseUintArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('d')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('d')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'x':
		arg := getStringArg(args, argIdx)
		val, err := parseUintArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('x')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('x')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'X':
		arg := getStringArg(args, argIdx)
		val, err := parseUintArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('X')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('X')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'e':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('e')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))
			return false, i, true
		}
		goFmt.WriteByte('e')
		callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))

	case 'E':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('E')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('E')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'f':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('f')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))
			return false, i, true
		}
		goFmt.WriteByte('f')
		callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))

	case 'F':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
		}
		// Go doesn't have %F; use %f and uppercase manually.
		goFmt.WriteByte('f')
		out := fmt.Sprintf(goFmt.String(), val)
		out = strings.ToUpper(out)
		callCtx.Out(out)
		if err != nil && arg != "" {
			return false, i, true
		}

	case 'g':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('g')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))
			return false, i, true
		}
		goFmt.WriteByte('g')
		callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), val)))

	case 'G':
		arg := getStringArg(args, argIdx)
		val, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			val = 0
			goFmt.WriteByte('G')
			callCtx.Out(fmt.Sprintf(goFmt.String(), val))
			return false, i, true
		}
		goFmt.WriteByte('G')
		callCtx.Out(fmt.Sprintf(goFmt.String(), val))

	case 'n':
		callCtx.Errf("printf: %%n: not supported (security risk)\n")
		_ = getStringArg(args, argIdx) // consume arg
		return false, i, true

	case 'q':
		callCtx.Errf("printf: %%q: not supported\n")
		_ = getStringArg(args, argIdx)
		return false, i, true

	case 'a', 'A':
		callCtx.Errf("printf: %%%c: not supported\n", verb)
		_ = getStringArg(args, argIdx)
		return false, i, true

	default:
		// Unknown specifier — bash treats this as an error.
		callCtx.Errf("printf: %%%c: invalid format character\n", verb)
		return false, i, true
	}

	return false, i, hadError
}

// getStringArg returns the next argument, or "" if exhausted.
func getStringArg(args []string, idx *int) string {
	if *idx >= len(args) {
		return ""
	}
	s := args[*idx]
	*idx++
	return s
}

// getIntArg returns the next argument parsed as an int (for * width/precision), or 0.
// The second return value is true if parsing failed.
func getIntArg(args []string, idx *int, callCtx *builtins.CallContext) (int, bool) {
	s := getStringArg(args, idx)
	if s == "" {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		callCtx.Errf("printf: '%s': invalid number\n", s)
		return 0, true
	}
	return v, false
}

// parseIntArg parses a string as a signed integer, supporting decimal, octal (0-prefix),
// hex (0x-prefix), and character constants ('X or "X).
func parseIntArg(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	// Character constant: 'X or "X
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') {
		return int64(s[1]), nil
	}

	// Try parsing with automatic base detection.
	val, err := strconv.ParseInt(s, 0, 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}

// parseUintArg parses a string as an unsigned integer.
func parseUintArg(s string) (uint64, error) {
	if s == "" {
		return 0, nil
	}

	// Character constant: 'X or "X
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') {
		return uint64(s[1]), nil
	}

	// Handle negative numbers: parse as signed, then interpret as unsigned.
	if len(s) > 0 && s[0] == '-' {
		val, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, err
		}
		// Bash wraps negatives as unsigned.
		return uint64(val), nil
	}

	val, err := strconv.ParseUint(s, 0, 64)
	if err != nil {
		// Try signed parse for large hex values that may be negative in two's complement.
		sval, serr := strconv.ParseInt(s, 0, 64)
		if serr != nil {
			return 0, err
		}
		return uint64(sval), nil
	}
	return val, nil
}

// parseFloatArg parses a string as a float64, supporting hex/octal integer prefixes
// and character constants.
func parseFloatArg(s string) (float64, error) {
	if s == "" {
		return 0, nil
	}

	// Character constant.
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') {
		return float64(s[1]), nil
	}

	// Handle hex integers used as float args (0xff etc).
	if len(s) > 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		val, err := strconv.ParseInt(s, 0, 64)
		if err != nil {
			return 0, err
		}
		return float64(val), nil
	}

	// Handle infinity and NaN.
	lower := strings.ToLower(s)
	if lower == "inf" || lower == "infinity" || lower == "+inf" || lower == "+infinity" {
		return math.Inf(1), nil
	}
	if lower == "-inf" || lower == "-infinity" {
		return math.Inf(-1), nil
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return val, nil
}

// processBEscapes handles backslash escapes for %b (like echo -e).
// Returns the processed string and whether \c was seen (stop all output).
func processBEscapes(s string) (string, bool) {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			i++
			continue
		}
		i++ // skip '\'
		switch s[i] {
		case '\\':
			b.WriteByte('\\')
		case 'a':
			b.WriteByte('\a')
		case 'b':
			b.WriteByte('\b')
		case 'c':
			return b.String(), true
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte('\v')
		case '0':
			// Octal: \0nnn (up to 3 digits after '0')
			i++
			val, consumed := parseOctal(s[i:], 3)
			i += consumed
			b.WriteByte(byte(val))
			continue
		case 'x':
			// Hex: \xHH (up to 2 digits)
			i++
			val, consumed := parseHex(s[i:], 2)
			if consumed == 0 {
				b.WriteByte('\\')
				b.WriteByte('x')
				continue
			}
			i += consumed
			b.WriteByte(byte(val))
			continue
		default:
			// Unrecognized: output backslash and character.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String(), false
}

// parseOctal reads up to maxDigits octal digits from s and returns the
// accumulated value and the number of bytes consumed.
func parseOctal(s string, maxDigits int) (int, int) {
	val := 0
	n := 0
	for n < maxDigits && n < len(s) && s[n] >= '0' && s[n] <= '7' {
		val = val*8 + int(s[n]-'0')
		n++
	}
	return val, n
}

// parseHex reads up to maxDigits hexadecimal digits from s and returns
// the accumulated value and the number of bytes consumed.
func parseHex(s string, maxDigits int) (int, int) {
	val := 0
	n := 0
	for n < maxDigits && n < len(s) {
		ch := s[n]
		switch {
		case ch >= '0' && ch <= '9':
			val = val*16 + int(ch-'0')
		case ch >= 'a' && ch <= 'f':
			val = val*16 + int(ch-'a') + 10
		case ch >= 'A' && ch <= 'F':
			val = val*16 + int(ch-'A') + 10
		default:
			return val, n
		}
		n++
	}
	return val, n
}
