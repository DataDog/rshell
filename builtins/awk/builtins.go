// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// resolveRegexArg returns a compiled *regexp.Regexp for an argument used as a
// regex. If the argument is a regex literal, its pre-compiled form is returned;
// otherwise the argument is evaluated as a string and compiled on the fly.
func (r *runtime) resolveRegexArg(e expr) (*regexp.Regexp, error) {
	if reLit, ok := e.(*regexExpr); ok {
		return reLit.re, nil
	}
	v, err := r.evalExpr(e)
	if err != nil {
		return nil, err
	}
	compiled, cerr := compileERE(v.toString(r.convFmt))
	if cerr != nil {
		return nil, fmt.Errorf("invalid regex: %v", cerr)
	}
	return compiled, nil
}

// evalCall dispatches a builtin function call.
func (r *runtime) evalCall(c *callExpr) (awkValue, error) {
	switch c.name {
	case "length":
		return r.bLength(c.args)
	case "substr":
		return r.bSubstr(c.args)
	case "index":
		return r.bIndex(c.args)
	case "split":
		return r.bSplit(c.args)
	case "sub":
		return r.bSub(c.args, false)
	case "gsub":
		return r.bSub(c.args, true)
	case "match":
		return r.bMatch(c.args)
	case "sprintf":
		return r.bSprintf(c.args)
	case "tolower":
		return r.bCase(c.args, strings.ToLower)
	case "toupper":
		return r.bCase(c.args, strings.ToUpper)
	case "int":
		return r.bInt(c.args)
	case "sqrt":
		return r.bMath1(c.args, math.Sqrt)
	case "exp":
		return r.bMath1(c.args, math.Exp)
	case "log":
		return r.bMath1(c.args, math.Log)
	case "sin":
		return r.bMath1(c.args, math.Sin)
	case "cos":
		return r.bMath1(c.args, math.Cos)
	case "atan2":
		return r.bAtan2(c.args)
	case "rand":
		return r.bRand()
	case "srand":
		return r.bSrand(c.args)
	}
	return uninitValue, fmt.Errorf("unknown function %q", c.name)
}

func (r *runtime) bLength(args []expr) (awkValue, error) {
	if len(args) == 0 {
		return numValue(float64(len(r.record))), nil
	}
	// length(arr) returns the number of array elements (gawk-extension).
	if id, ok := args[0].(*identExpr); ok {
		if arr, exists := r.arrays[id.name]; exists {
			return numValue(float64(len(arr))), nil
		}
	}
	v, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	return numValue(float64(len(v.toString(r.convFmt)))), nil
}

// bSubstr implements substr(s, m[, n]). Positions and lengths are byte-based,
// not character-based. Multi-byte UTF-8 sequences are counted as multiple
// positions, consistent with mawk but differing from gawk in a UTF-8 locale.
func (r *runtime) bSubstr(args []expr) (awkValue, error) {
	sv, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	s := sv.toString(r.convFmt)
	mv, err := r.evalExpr(args[1])
	if err != nil {
		return uninitValue, err
	}
	m64 := floatToInt64Safe(mv.toNumber())
	if m64 > int64(len(s))+1 {
		return strValue(""), nil
	}
	if m64 < math.MinInt32 {
		m64 = math.MinInt32
	}
	m := int(m64)
	// Awk substr is 1-based.
	end := len(s) + 1
	if len(args) == 3 {
		nv, err := r.evalExpr(args[2])
		if err != nil {
			return uninitValue, err
		}
		n64 := floatToInt64Safe(nv.toNumber())
		if n64 > int64(len(s)) {
			n64 = int64(len(s)) + 1
		}
		if n64 < math.MinInt32 {
			n64 = math.MinInt32
		}
		n := int(n64)
		if n < 0 {
			return strValue(""), nil
		}
		if m < 1 {
			// When start position is <= 0, awk/mawk semantics: the conceptual
			// range still extends n characters from the original m. So the
			// effective end (1-based exclusive) = n - m + 1. The start is
			// clamped to 1. Example: substr("hello",-1,3) => s[0:4] = "hell".
			end = n - m + 1
			m = 1
		} else {
			end = m + n
		}
		if end <= m {
			return strValue(""), nil
		}
	} else if m < 1 {
		m = 1
	}
	if m > len(s)+1 {
		return strValue(""), nil
	}
	if end > len(s)+1 {
		end = len(s) + 1
	}
	if end <= m {
		return strValue(""), nil
	}
	return strValue(s[m-1 : end-1]), nil
}

// bIndex implements index(s, t). Returns a byte position, not a character
// position. Multi-byte UTF-8 input will produce byte offsets, consistent with
// mawk but differing from gawk in a UTF-8 locale.
func (r *runtime) bIndex(args []expr) (awkValue, error) {
	sv, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	tv, err := r.evalExpr(args[1])
	if err != nil {
		return uninitValue, err
	}
	s := sv.toString(r.convFmt)
	t := tv.toString(r.convFmt)
	if t == "" {
		return numValue(1), nil
	}
	idx := strings.Index(s, t)
	if idx < 0 {
		return numValue(0), nil
	}
	return numValue(float64(idx + 1)), nil
}

func (r *runtime) bSplit(args []expr) (awkValue, error) {
	sv, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	s := sv.toString(r.convFmt)
	id, ok := args[1].(*identExpr)
	if !ok {
		return uninitValue, errors.New("split: second argument must be an array name")
	}
	// Reset the destination array.
	delete(r.arrays, id.name)
	if s == "" {
		return numValue(0), nil
	}
	sep := r.fs
	var sepRe = r.fsRe
	if len(args) == 3 {
		// split with explicit separator: regex literal or string.
		if reLit, ok := args[2].(*regexExpr); ok {
			sepRe = reLit.re
			sep = ""
		} else {
			vv, err := r.evalExpr(args[2])
			if err != nil {
				return uninitValue, err
			}
			sep = vv.toString(r.convFmt)
			sepRe = nil
			if len(sep) > 1 {
				re, cerr := compileERE(sep)
				if cerr != nil {
					return uninitValue, fmt.Errorf("split: invalid regex: %v", cerr)
				}
				sepRe = re
			}
		}
	}
	var parts []string
	switch {
	case sepRe != nil:
		parts = sepRe.Split(s, -1)
	case sep == " ":
		parts = strings.Fields(s)
	case sep == "":
		parts = make([]string, 0, len(s))
		for _, ch := range s {
			parts = append(parts, string(ch))
		}
	default:
		parts = strings.Split(s, sep)
	}
	if len(parts) > MaxArrayEntries {
		return uninitValue, fmt.Errorf("split: result exceeds maximum array entries %d", MaxArrayEntries)
	}
	arr := make(map[string]awkValue, len(parts))
	for i, p := range parts {
		arr[itoa(i+1)] = strNumValue(p)
	}
	r.arrays[id.name] = arr
	return numValue(float64(len(parts))), nil
}

func (r *runtime) bSub(args []expr, global bool) (awkValue, error) {
	compiled, err := r.resolveRegexArg(args[0])
	if err != nil {
		return uninitValue, fmt.Errorf("sub/gsub: %v", err)
	}
	repl, err := r.evalExpr(args[1])
	if err != nil {
		return uninitValue, err
	}
	replStr := repl.toString(r.convFmt)

	var target expr = &fieldExpr{index: &numExpr{val: 0}}
	if len(args) == 3 {
		target = args[2]
	}
	if !isLValue(target) {
		return uninitValue, errors.New("sub/gsub: third argument must be assignable")
	}
	tv, err := r.evalExpr(target)
	if err != nil {
		return uninitValue, err
	}
	s := tv.toString(r.convFmt)

	count := 0
	var newStr string
	if global {
		// Substitute all matches; track count.
		// We can't use ReplaceAllStringFunc because we need to expand & in the
		// replacement text; do it manually.
		var sb strings.Builder
		last := 0
		for _, m := range compiled.FindAllStringSubmatchIndex(s, -1) {
			sb.WriteString(s[last:m[0]])
			expanded, expErr := expandAwkReplacement(replStr, s[m[0]:m[1]])
			if expErr != nil {
				return uninitValue, expErr
			}
			sb.WriteString(expanded)
			last = m[1]
			count++
			if sb.Len() > MaxStringBytes {
				return uninitValue, fmt.Errorf("gsub: result exceeds maximum string length %d", MaxStringBytes)
			}
		}
		sb.WriteString(s[last:])
		newStr = sb.String()
	} else {
		loc := compiled.FindStringIndex(s)
		if loc == nil {
			newStr = s
		} else {
			matched := s[loc[0]:loc[1]]
			expanded, expErr := expandAwkReplacement(replStr, matched)
			if expErr != nil {
				return uninitValue, expErr
			}
			newStr = s[:loc[0]] + expanded + s[loc[1]:]
			count = 1
		}
	}
	if len(newStr) > MaxStringBytes {
		return uninitValue, fmt.Errorf("sub/gsub: result exceeds maximum string length %d", MaxStringBytes)
	}
	// Only write back to the lvalue when a substitution was actually made.
	// Assigning $1 even with the same value rebuilds $0 with OFS, mutating the
	// record for a no-op sub — which is wrong.
	if count > 0 {
		if _, err := r.assignLValue(target, strValue(newStr)); err != nil {
			return uninitValue, err
		}
	}
	return numValue(float64(count)), nil
}

// expandAwkReplacement implements awk's & substitution and \& literal-amp.
// Returns an error if the expanded replacement exceeds MaxStringBytes to prevent OOM.
func expandAwkReplacement(repl, matched string) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(repl); i++ {
		c := repl[i]
		if c == '\\' && i+1 < len(repl) {
			n := repl[i+1]
			switch n {
			case '&':
				sb.WriteByte('&')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(n)
			}
			i++
		} else if c == '&' {
			sb.WriteString(matched)
		} else {
			sb.WriteByte(c)
		}
		if sb.Len() > MaxStringBytes {
			return "", fmt.Errorf("sub/gsub replacement exceeds maximum string length %d", MaxStringBytes)
		}
	}
	return sb.String(), nil
}

// bMatch implements match(s, re). RSTART and RLENGTH are set to byte offsets
// and lengths, not character positions. Multi-byte UTF-8 input will produce
// byte-based values, consistent with mawk but differing from gawk in a UTF-8 locale.
func (r *runtime) bMatch(args []expr) (awkValue, error) {
	sv, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	s := sv.toString(r.convFmt)
	compiled, err := r.resolveRegexArg(args[1])
	if err != nil {
		return uninitValue, fmt.Errorf("match: %v", err)
	}
	loc := compiled.FindStringIndex(s)
	if loc == nil {
		r.rstart = 0
		r.rlength = -1
		return numValue(0), nil
	}
	r.rstart = int64(loc[0] + 1)
	r.rlength = int64(loc[1] - loc[0])
	return numValue(float64(r.rstart)), nil
}

func (r *runtime) bSprintf(args []expr) (awkValue, error) {
	if len(args) == 0 {
		return uninitValue, errors.New("sprintf: no format")
	}
	fv, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	values := make([]awkValue, len(args)-1)
	for i, a := range args[1:] {
		v, e := r.evalExpr(a)
		if e != nil {
			return uninitValue, e
		}
		values[i] = v
	}
	out, err := awkSprintf(fv.toString(r.convFmt), values, r.convFmt)
	if err != nil {
		return uninitValue, err
	}
	if len(out) > MaxStringBytes {
		return uninitValue, fmt.Errorf("sprintf: output exceeds maximum string length %d", MaxStringBytes)
	}
	return strValue(out), nil
}

func (r *runtime) bCase(args []expr, fn func(string) string) (awkValue, error) {
	v, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	out := fn(v.toString(r.convFmt))
	if len(out) > MaxStringBytes {
		return uninitValue, fmt.Errorf("output exceeds maximum string length %d", MaxStringBytes)
	}
	return strValue(out), nil
}

func (r *runtime) bInt(args []expr) (awkValue, error) {
	v, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	return numValue(math.Trunc(v.toNumber())), nil
}

func (r *runtime) bMath1(args []expr, fn func(float64) float64) (awkValue, error) {
	v, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	return numValue(fn(v.toNumber())), nil
}

func (r *runtime) bAtan2(args []expr) (awkValue, error) {
	y, err := r.evalExpr(args[0])
	if err != nil {
		return uninitValue, err
	}
	x, err := r.evalExpr(args[1])
	if err != nil {
		return uninitValue, err
	}
	return numValue(math.Atan2(y.toNumber(), x.toNumber())), nil
}

func (r *runtime) bRand() (awkValue, error) {
	return numValue(r.rng.next()), nil
}

func (r *runtime) bSrand(args []expr) (awkValue, error) {
	prev := r.rng.seed
	if len(args) == 0 {
		r.rng.setSeed(0)
	} else {
		v, err := r.evalExpr(args[0])
		if err != nil {
			return uninitValue, err
		}
		r.rng.setSeed(int64(v.toNumber()))
	}
	return numValue(float64(prev)), nil
}
