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
//	\0NNN     octal byte value (0 + 1-3 digits)
//	\xHH     hexadecimal byte value (1-2 digits)
//	\uHHHH   Unicode code point (1-4 hex digits)
//	\UHHHHHHHH Unicode code point (1-8 hex digits)
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
	"math/big"
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
// The flags parameter is the parsed format flags string, used to determine
// whether the + sign should be preserved for positive infinity.
func bashFloat(s string, flags string) string {
	s = strings.ReplaceAll(s, "NaN", "nan")
	if strings.ContainsRune(flags, '+') {
		s = strings.ReplaceAll(s, "+Inf", "+inf")
	} else if strings.ContainsRune(flags, ' ') {
		s = strings.ReplaceAll(s, "+Inf", " inf")
	} else {
		s = strings.ReplaceAll(s, "+Inf", "inf")
	}
	s = strings.ReplaceAll(s, "-Inf", "-inf")
	s = strings.ReplaceAll(s, "Inf", "inf")
	return s
}

// bashFloatUpper fixes Go's NaN/Inf casing to match bash's uppercase output
// for uppercase format verbs (F, E, G). Go outputs "NaN" and "+Inf"/"-Inf"
// but bash outputs "NAN", "INF", "-INF".
// The flags parameter is the parsed format flags string, used to determine
// whether the + sign should be preserved for positive infinity.
func bashFloatUpper(s string, flags string) string {
	s = strings.ReplaceAll(s, "NaN", "NAN")
	if strings.ContainsRune(flags, '+') {
		s = strings.ReplaceAll(s, "+Inf", "+INF")
	} else if strings.ContainsRune(flags, ' ') {
		s = strings.ReplaceAll(s, "+Inf", " INF")
	} else {
		s = strings.ReplaceAll(s, "+Inf", "INF")
	}
	s = strings.ReplaceAll(s, "-Inf", "-INF")
	s = strings.ReplaceAll(s, "Inf", "INF")
	return s
}

// maxWidthOrPrec caps width/precision values to prevent huge allocations.
const maxWidthOrPrec = 10_000

// stripSignFlags removes '+' and ' ' from a flag string.
// Bash ignores these flags for unsigned conversions (%o, %u, %x, %X).
func stripSignFlags(flags string) string {
	var b strings.Builder
	for i := 0; i < len(flags); i++ {
		if flags[i] != '+' && flags[i] != ' ' {
			b.WriteByte(flags[i])
		}
	}
	return b.String()
}

func run(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
	// Manual flag handling: only --help, -v, and -- are recognised.
	// Any other flag starting with - is rejected (bash compat).
	if len(args) > 0 {
		switch {
		case args[0] == "--help":
			callCtx.Errf("printf: usage: printf [-v var] format [arguments]\n")
			return builtins.Result{Code: 2}
		case args[0] == "-v":
			callCtx.Errf("printf: -v: not supported in restricted shell\n")
			return builtins.Result{Code: 1}
		case args[0] == "--":
			args = args[1:] // skip --
		case len(args[0]) > 1 && args[0][0] == '-' && args[0][1] != '-':
			// Unknown single-dash flag (e.g. -h, -f, -z).
			// Bash rejects these with "invalid option" and exit 2.
			callCtx.Errf("printf: %c%c: invalid option\n", args[0][0], args[0][1])
			callCtx.Errf("printf: usage: printf [-v var] format [arguments]\n")
			return builtins.Result{Code: 2}
		case len(args[0]) > 2 && args[0][0] == '-' && args[0][1] == '-':
			// Unknown long flag (e.g. --follow, --foo).
			// Bash rejects these with "--: invalid option" and exit 2.
			callCtx.Errf("printf: --: invalid option\n")
			callCtx.Errf("printf: usage: printf [-v var] format [arguments]\n")
			return builtins.Result{Code: 2}
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
			s, advance, errMsg := processFormatEscape(format[i:])
			callCtx.Out(s)
			if errMsg != "" {
				callCtx.Errf("%s", errMsg)
			}
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
// Returns the replacement string, the number of bytes consumed from s, and an optional
// error message to emit to stderr (empty string if no error).
func processFormatEscape(s string) (string, int, string) {
	if len(s) < 2 {
		return "\\", 1, ""
	}
	switch s[1] {
	case '\\':
		return "\\", 2, ""
	case 'a':
		return "\a", 2, ""
	case 'b':
		return "\b", 2, ""
	case 'f':
		return "\f", 2, ""
	case 'n':
		return "\n", 2, ""
	case 'r':
		return "\r", 2, ""
	case 't':
		return "\t", 2, ""
	case 'v':
		return "\v", 2, ""
	case '"':
		return "\"", 2, ""
	case '0':
		// \0NN — octal (0 counts as first digit, up to 2 more).
		// Bash treats the leading 0 as the first of 3 octal digits,
		// so \0123 = \012 (newline) + literal '3'.
		val, consumed := parseOctal(s[2:], 2)
		return string([]byte{byte(val)}), 2 + consumed, ""
	case 'x':
		// \xHH — hex (up to 2 digits)
		val, consumed := parseHex(s[2:], 2)
		if consumed == 0 {
			return "\\x", 2, ""
		}
		return string([]byte{byte(val)}), 2 + consumed, ""
	case 'u':
		// \uHHHH — 4-digit Unicode code point
		val, consumed := parseHex(s[2:], 4)
		if consumed == 0 {
			return "\\u", 2, "printf: missing unicode digit for \\u\n"
		}
		return string(rune(val)), 2 + consumed, ""
	case 'U':
		// \UHHHHHHHH — 8-digit Unicode code point
		val, consumed := parseHex(s[2:], 8)
		if consumed == 0 {
			return "\\U", 2, "printf: missing unicode digit for \\U\n"
		}
		// Clamp to max valid Unicode code point.
		if val > 0x10FFFF {
			val = 0xFFFD // Unicode replacement character
		}
		return string(rune(val)), 2 + consumed, ""

	default:
		if s[1] >= '1' && s[1] <= '7' {
			// \NNN — octal without leading 0 (1-3 digits)
			val, consumed := parseOctal(s[1:], 3)
			return string([]byte{byte(val)}), 1 + consumed, ""
		}
		// Unknown escape: output backslash and character.
		return string([]byte{'\\', s[1]}), 2, ""
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
	// For unsigned verbs (o, u, x, X), strip '+' and ' ' sign flags
	// because bash ignores them for unsigned conversions.
	flagStr := flags.String()
	if verb == 'o' || verb == 'u' || verb == 'x' || verb == 'X' {
		flagStr = stripSignFlags(flagStr)
	}
	var goFmt strings.Builder
	goFmt.WriteByte('%')
	goFmt.WriteString(flagStr)
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
		processed, stop, warns := processBEscapes(arg)
		if warns != "" {
			callCtx.Errf("%s", warns)
		}
		// Apply width/precision formatting to the processed string.
		goFmt.WriteByte('s')
		callCtx.Out(fmt.Sprintf(goFmt.String(), processed))
		if stop {
			return true, i, hadError
		}

	case 'c':
		arg := getStringArg(args, argIdx)
		// %c prints the first byte of the argument as a raw byte.
		// We use %s with a single-byte string instead of Go's %c, because
		// Go's %c treats the byte as a rune and UTF-8 encodes values >= 0x80.
		// Empty arg produces a NUL byte (bash behavior).
		// Bash ignores precision for %c — always emits exactly one byte.
		var charStr string
		if len(arg) > 0 {
			charStr = string([]byte{arg[0]})
		} else {
			charStr = "\x00"
		}
		// Build a format without precision — bash ignores precision for %c.
		var cFmt strings.Builder
		cFmt.WriteByte('%')
		cFmt.WriteString(flagStr)
		cFmt.WriteString(width)
		cFmt.WriteByte('s')
		callCtx.Out(fmt.Sprintf(cFmt.String(), charStr))

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
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			goFmt.WriteByte('e')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), 0.0), flagStr))
			return false, i, true
		}
		goFmt.WriteByte('e')
		callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), fa.f), flagStr))

	case 'E':
		arg := getStringArg(args, argIdx)
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			goFmt.WriteByte('E')
			callCtx.Out(bashFloatUpper(fmt.Sprintf(goFmt.String(), 0.0), flagStr))
			return false, i, true
		}
		goFmt.WriteByte('E')
		callCtx.Out(bashFloatUpper(fmt.Sprintf(goFmt.String(), fa.f), flagStr))

	case 'f':
		arg := getStringArg(args, argIdx)
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			goFmt.WriteByte('f')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), 0.0), flagStr))
			return false, i, true
		}
		if fa.exact != nil {
			// Use big.Int for exact integer formatting to avoid float64 precision loss.
			prec := -1 // default
			if hasPrecision {
				prec, _ = strconv.Atoi(precision)
			}
			callCtx.Out(formatBigIntAsFloat(fa.exact, flagStr, width, prec))
		} else {
			goFmt.WriteByte('f')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), fa.f), flagStr))
		}

	case 'F':
		arg := getStringArg(args, argIdx)
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			fa = floatArg{}
		}
		if fa.exact != nil {
			// Use big.Int for exact integer formatting.
			prec := -1
			if hasPrecision {
				prec, _ = strconv.Atoi(precision)
			}
			callCtx.Out(formatBigIntAsFloat(fa.exact, flagStr, width, prec))
		} else {
			// Go doesn't have %F; use %f and fix Inf/NaN casing to match bash.
			goFmt.WriteByte('f')
			out := bashFloatUpper(fmt.Sprintf(goFmt.String(), fa.f), flagStr)
			callCtx.Out(out)
		}
		if err != nil && arg != "" {
			return false, i, true
		}

	case 'g':
		arg := getStringArg(args, argIdx)
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			goFmt.WriteByte('g')
			callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), 0.0), flagStr))
			return false, i, true
		}
		goFmt.WriteByte('g')
		callCtx.Out(bashFloat(fmt.Sprintf(goFmt.String(), fa.f), flagStr))

	case 'G':
		arg := getStringArg(args, argIdx)
		fa, err := parseFloatArg(arg)
		if err != nil && arg != "" {
			callCtx.Errf("printf: '%s': invalid number\n", arg)
			goFmt.WriteByte('G')
			callCtx.Out(bashFloatUpper(fmt.Sprintf(goFmt.String(), 0.0), flagStr))
			return false, i, true
		}
		goFmt.WriteByte('G')
		callCtx.Out(bashFloatUpper(fmt.Sprintf(goFmt.String(), fa.f), flagStr))

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
		// Unknown specifier — bash treats this as an error and stops processing
		// the rest of the format string.
		callCtx.Errf("printf: %%%c: invalid format character\n", verb)
		return true, i, true
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
// Like bash, it accepts decimal, octal (0-prefix), hex (0x-prefix), and
// character constants ('X or "X).
// The second return value is true if parsing failed.
func getIntArg(args []string, idx *int, callCtx *builtins.CallContext) (int, bool) {
	s := getStringArg(args, idx)
	if s == "" {
		return 0, false
	}
	// Character constant: 'X or "X — bare quote with no following char yields 0.
	if s[0] == '\'' || s[0] == '"' {
		if len(s) >= 2 {
			return int(s[1]), false
		}
		return 0, false
	}
	v, err := strconv.ParseInt(s, 0, strconv.IntSize)
	if err != nil {
		callCtx.Errf("printf: '%s': invalid number\n", s)
		return 0, true
	}
	return int(v), false
}

// parseIntArg parses a string as a signed integer, supporting decimal, octal (0-prefix),
// hex (0x-prefix), and character constants ('X or "X).
func parseIntArg(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	// Character constant: 'X or "X — bare quote with no following char yields 0.
	if s[0] == '\'' || s[0] == '"' {
		if len(s) >= 2 {
			return int64(s[1]), nil
		}
		return 0, nil
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

	// Character constant: 'X or "X — bare quote with no following char yields 0.
	if s[0] == '\'' || s[0] == '"' {
		if len(s) >= 2 {
			return uint64(s[1]), nil
		}
		return 0, nil
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

// floatArg holds the result of parsing a float argument. For integer inputs,
// exact holds the exact big.Int value to avoid float64 precision loss when
// formatting with %f/%F.
type floatArg struct {
	f     float64
	exact *big.Int // non-nil when the input was an exact integer
}

// parseFloatArg parses a string as a float64, supporting hex/octal integer prefixes
// and character constants. When the input is a pure integer, exact is set to preserve
// full precision for %f/%F formatting (float64 only has 53 bits of mantissa).
func parseFloatArg(s string) (floatArg, error) {
	if s == "" {
		return floatArg{}, nil
	}

	// Character constant: 'X or "X — bare quote with no following char yields 0.
	if s[0] == '\'' || s[0] == '"' {
		if len(s) >= 2 {
			v := int64(s[1])
			return floatArg{f: float64(v), exact: big.NewInt(v)}, nil
		}
		return floatArg{}, nil
	}

	// Handle hex/octal integers used as float args (0xff, -0xff, 0755, etc).
	// Bash accepts these for %f/%e/%g and converts them to float.
	prefix := s
	isNeg := false
	if len(prefix) > 0 && (prefix[0] == '-' || prefix[0] == '+') {
		isNeg = prefix[0] == '-'
		prefix = prefix[1:]
	}
	if len(prefix) > 1 && prefix[0] == '0' && (prefix[1] == 'x' || prefix[1] == 'X' || (prefix[1] >= '0' && prefix[1] <= '7')) {
		if isNeg {
			val, err := strconv.ParseInt(s, 0, 64)
			if err != nil {
				return floatArg{}, err
			}
			return floatArg{f: float64(val), exact: big.NewInt(val)}, nil
		}
		// Try unsigned first to handle values > math.MaxInt64 (e.g. 0xffffffffffffffff).
		uval, err := strconv.ParseUint(prefix, 0, 64)
		if err != nil {
			val, serr := strconv.ParseInt(s, 0, 64)
			if serr != nil {
				return floatArg{}, err
			}
			return floatArg{f: float64(val), exact: big.NewInt(val)}, nil
		}
		bi := new(big.Int).SetUint64(uval)
		return floatArg{f: float64(uval), exact: bi}, nil
	}

	// Handle infinity and NaN.
	lower := strings.ToLower(s)
	if lower == "inf" || lower == "infinity" || lower == "+inf" || lower == "+infinity" {
		return floatArg{f: math.Inf(1)}, nil
	}
	if lower == "-inf" || lower == "-infinity" {
		return floatArg{f: math.Inf(-1)}, nil
	}

	// Try parsing as a plain decimal integer for exact precision.
	if isDecimalInteger(s) {
		bi, ok := new(big.Int).SetString(s, 10)
		if ok {
			val, _ := strconv.ParseFloat(s, 64)
			return floatArg{f: val, exact: bi}, nil
		}
	}

	val, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return floatArg{}, err
	}
	return floatArg{f: val}, nil
}

// isDecimalInteger returns true if s is a plain decimal integer (optional leading sign, all digits).
func isDecimalInteger(s string) bool {
	if len(s) == 0 {
		return false
	}
	start := 0
	if s[0] == '-' || s[0] == '+' {
		start = 1
	}
	if start >= len(s) {
		return false
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// formatBigIntAsFloat formats a big.Int as a decimal float string with the given
// precision (number of decimal places). This preserves full integer precision
// that would be lost with float64 formatting.
func formatBigIntAsFloat(bi *big.Int, flags string, width string, prec int) string {
	intStr := bi.String()
	// Build decimal part.
	var decPart string
	if prec > 0 {
		decPart = "." + strings.Repeat("0", prec)
	} else if prec == 0 {
		// No decimal point unless # flag.
		if strings.ContainsRune(flags, '#') {
			decPart = "."
		}
	} else {
		// Default precision is 6.
		decPart = ".000000"
	}

	// Handle sign/flags.
	sign := ""
	num := intStr
	if num[0] == '-' {
		sign = "-"
		num = num[1:]
	} else if strings.ContainsRune(flags, '+') {
		sign = "+"
	} else if strings.ContainsRune(flags, ' ') {
		sign = " "
	}

	result := sign + num + decPart

	// Handle width.
	if width != "" {
		w, err := strconv.Atoi(width)
		if err == nil && len(result) < abs(w) {
			pad := abs(w) - len(result)
			if strings.ContainsRune(flags, '-') {
				// Left-aligned.
				result = result + strings.Repeat(" ", pad)
			} else if strings.ContainsRune(flags, '0') {
				// Zero-padded (pad between sign and digits).
				result = sign + strings.Repeat("0", pad) + num + decPart
			} else {
				result = strings.Repeat(" ", pad) + result
			}
		}
	}

	return result
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// processBEscapes handles backslash escapes for %b (like echo -e).
// Returns the processed string, whether \c was seen (stop all output),
// and any warning messages to emit to stderr.
func processBEscapes(s string) (string, bool, string) {
	var b strings.Builder
	var warns strings.Builder
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
			return b.String(), true, warns.String()
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
		case 'u':
			// Unicode: \uHHHH (up to 4 hex digits)
			i++
			val, consumed := parseHex(s[i:], 4)
			if consumed == 0 {
				b.WriteByte('\\')
				b.WriteByte('u')
				warns.WriteString("printf: missing unicode digit for \\u\n")
				continue
			}
			i += consumed
			b.WriteString(string(rune(val)))
			continue
		case 'U':
			// Unicode: \UHHHHHHHH (up to 8 hex digits)
			i++
			val, consumed := parseHex(s[i:], 8)
			if consumed == 0 {
				b.WriteByte('\\')
				b.WriteByte('U')
				warns.WriteString("printf: missing unicode digit for \\U\n")
				continue
			}
			i += consumed
			if val > 0x10FFFF {
				val = 0xFFFD // Unicode replacement character
			}
			b.WriteString(string(rune(val)))
			continue
		default:
			if s[i] >= '1' && s[i] <= '7' {
				// \NNN — octal without leading 0 (1-3 digits).
				// Bash %b supports both \0NNN and \NNN.
				val, consumed := parseOctal(s[i:], 3)
				i += consumed
				b.WriteByte(byte(val))
				continue
			}
			// Unrecognized: output backslash and character.
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
		i++
	}
	return b.String(), false, warns.String()
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
