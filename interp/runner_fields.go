// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"fmt"
	"iter"
	"strconv"
	"strings"
	"unicode/utf8"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

// fieldsSeq works around mvdan.cc/sh/v3's coalescing of all IFS characters.
// Non-whitespace separators must preserve leading and adjacent empty fields.
func (c *fieldCollector) fieldsSeq(word *syntax.Word) iter.Seq2[string, error] {
	ifs := c.r.ecfg.Env.Get("IFS").String()
	if strings.TrimLeft(ifs, " \t\n\r\v\f") == "" {
		return expand.FieldsSeq(c.r.ecfg, word)
	}
	return func(yield func(string, error) bool) {
		word := *word
		syntax.SplitBraces(&word)
		for w, err := range expand.BracesSeq(c.r.ecfg, &word) {
			if err != nil {
				yield("", err)
				return
			}
			fields, cfg, err := c.splitWordFields(w, ifs)
			if err != nil {
				yield("", err)
				return
			}
			for _, field := range fields {
				for value, err := range expand.FieldsSeq(cfg, field) {
					if !yield(value, err) || err != nil {
						return
					}
				}
			}
		}
	}
}

// splitFieldEnv holds already-expanded, unquoted fragments. Passing them back
// as parameters with IFS disabled preserves globbing without interpreting their
// backslashes, braces, or tildes as shell source. It never changes the runner's env.
type splitFieldEnv struct {
	expand.Environ
	values map[string]expand.Variable
}

func (e splitFieldEnv) Get(name string) expand.Variable {
	if name == "IFS" {
		return expand.Variable{Set: true, Kind: expand.String}
	}
	if value, ok := e.values[name]; ok {
		return value
	}
	return e.Environ.Get(name)
}

func (c *fieldCollector) splitWordFields(word *syntax.Word, ifs string) ([]*syntax.Word, *expand.Config, error) {
	cfg := *c.r.ecfg
	env := splitFieldEnv{Environ: cfg.Env, values: make(map[string]expand.Variable)}
	cfg.Env = env
	var fields []*syntax.Word
	var parts []syntax.WordPart
	// Whitespace immediately before a non-whitespace separator belongs to the
	// same delimiter. Keep this state across adjacent expansions, including empty ones.
	afterWhitespace := false
	flush := func() error {
		if len(fields) >= c.maxFields-len(c.fields) {
			return &expansionLimitError{message: fmt.Sprintf(
				"expansion exceeds maximum field count (%d)", c.maxFields)}
		}
		if len(parts) == 0 {
			parts = []syntax.WordPart{&syntax.SglQuoted{}}
		}
		fields = append(fields, &syntax.Word{Parts: parts})
		parts = nil
		return nil
	}
	addPart := func(part syntax.WordPart) {
		if len(parts) == 0 {
			// Only the original word's first literal may undergo tilde expansion.
			parts = append(parts, &syntax.SglQuoted{})
		}
		parts = append(parts, part)
		afterWhitespace = false
	}
	addValue := func(value string) {
		if value == "" {
			return
		}
		name := "_rshell_field_" + strconv.Itoa(len(env.values))
		env.values[name] = expand.Variable{Set: true, Kind: expand.String, Str: value}
		addPart(&syntax.ParamExp{Param: &syntax.Lit{Value: name}})
	}
	for i, part := range word.Parts {
		switch part.(type) {
		case *syntax.Lit:
			if i == 0 {
				parts = append(parts, part)
			} else {
				addPart(part)
			}
		case *syntax.SglQuoted, *syntax.DblQuoted:
			value, err := expand.Literal(c.r.ecfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, nil, err
			}
			addPart(&syntax.SglQuoted{Value: value})
		case *syntax.ParamExp, *syntax.CmdSubst:
			value, err := expand.Literal(c.r.ecfg, &syntax.Word{Parts: []syntax.WordPart{part}})
			if err != nil {
				return nil, nil, err
			}
			start := 0
			for pos := 0; pos < len(value); {
				r, size := utf8.DecodeRuneInString(value[pos:])
				if !strings.ContainsRune(ifs, r) {
					pos += size
					continue
				}
				addValue(value[start:pos])
				if strings.ContainsRune(" \t\n\r\v\f", r) {
					if len(parts) > 0 {
						if err := flush(); err != nil {
							return nil, nil, err
						}
						afterWhitespace = true
					}
				} else {
					if !afterWhitespace {
						if err := flush(); err != nil {
							return nil, nil, err
						}
					}
					afterWhitespace = false
				}
				pos += size
				start = pos
			}
			addValue(value[start:])
		default:
			return nil, nil, fmt.Errorf("unsupported word part in field splitting: %T", part)
		}
	}
	if len(parts) > 0 {
		if err := flush(); err != nil {
			return nil, nil, err
		}
	}
	return fields, &cfg, nil
}
