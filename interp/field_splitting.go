// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// fieldsSeq compensates for mvdan's coalescing of non-whitespace IFS
// delimiters. Keep its expansion path when IFS contains only whitespace.
func (r *Runner) fieldsSeq(word *syntax.Word) iter.Seq2[string, error] {
	ifs := r.ecfg.Env.Get("IFS").String()
	if strings.Trim(ifs, " \t\n") == "" {
		return expand.FieldsSeq(r.ecfg, word)
	}
	return func(yield func(string, error) bool) {
		word := *word
		syntax.SplitBraces(&word)
		for w, err := range expand.BracesSeq(r.ecfg, &word) {
			if err != nil {
				yield("", err)
				return
			}
			if !r.splitWordFields(w, ifs, yield) {
				return
			}
		}
	}
}

func (r *Runner) splitWordFields(word *syntax.Word, ifs string, yield func(string, error) bool) bool {
	// Evaluate every expansion once, in source order, before pathname
	// expansion. A nil part marks unquoted data subject to field splitting.
	type expandedPart struct {
		part  syntax.WordPart
		value string
	}
	parts := make([]expandedPart, 0, len(word.Parts))
	for _, part := range word.Parts {
		if r.stop(r.ectx) {
			return false
		}
		switch part.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.SglQuoted, *syntax.DblQuoted:
			value, err := expand.Literal(r.ecfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				yield("", err)
				return false
			}
			switch part.(type) {
			case *syntax.SglQuoted, *syntax.DblQuoted:
				parts = append(parts, expandedPart{part: &syntax.SglQuoted{Value: value}})
			default:
				parts = append(parts, expandedPart{value: value})
			}
		default:
			parts = append(parts, expandedPart{part: part})
		}
	}

	// Feed already-split unquoted data back as parameters with IFS disabled.
	// Unlike synthetic literals, parameters cannot reinterpret backslashes,
	// tildes or braces in expansion results. All original parameters and
	// quoted substitutions were evaluated above using the real environment.
	env := &overlayEnviron{parent: r.ecfg.Env, values: make(map[string]expand.Variable)}
	cfg := *r.ecfg
	cfg.Env = env
	var field syntax.Word
	reset := func() {
		clear(env.values)
		env.values["IFS"] = expand.Variable{Set: true, Kind: expand.String}
		// This anchor preserves explicit empty fields and prevents a literal
		// following a split expansion from becoming a tilde expansion.
		field.Parts = []syntax.WordPart{&syntax.SglQuoted{}}
	}
	reset()
	flush := func(force bool) bool {
		if !force && len(field.Parts) == 1 {
			return true
		}
		for value, err := range expand.FieldsSeq(&cfg, &field) {
			if !yield(value, err) || err != nil {
				return false
			}
		}
		reset()
		return true
	}
	addValue := func(value string) {
		name := "field" + strconv.Itoa(len(field.Parts))
		env.values[name] = expand.Variable{Set: true, Kind: expand.String, Str: value}
		field.Parts = append(field.Parts, &syntax.ParamExp{Param: &syntax.Lit{Value: name}})
	}

	// Defer whitespace separators so an adjacent non-whitespace delimiter
	// (even in the next expansion) can join them into a single separator.
	whitespace := false
	for _, part := range parts {
		if part.part != nil {
			if whitespace && !flush(false) {
				return false
			}
			whitespace = false
			field.Parts = append(field.Parts, part.part)
			continue
		}
		start := 0
		for i, ch := range part.value {
			if !strings.ContainsRune(ifs, ch) {
				if whitespace && !flush(false) {
					return false
				}
				whitespace = false
				continue
			}
			if i > start {
				addValue(part.value[start:i])
			}
			_, size := utf8.DecodeRuneInString(part.value[i:])
			start = i + size
			if ch == ' ' || ch == '\t' || ch == '\n' {
				whitespace = true
			} else {
				if !flush(true) {
					return false
				}
				whitespace = false
			}
		}
		if start < len(part.value) {
			addValue(part.value[start:])
		}
	}
	return flush(false)
}
