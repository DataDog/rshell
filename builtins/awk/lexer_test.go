// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package awk

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeAwkEscapesPreservesMalformedUTF8(t *testing.T) {
	input := string([]byte{'A', 0xff, '\\', 0xfe}) + "é�\\�\\n\\141Z\\"
	want := string([]byte{'A', 0xff, 0xfe}) + "é��\naZ\\"

	assert.Equal(t, []byte(want), []byte(DecodeAwkEscapes(input)))
}

func TestLexRegexKeepsPOSIXBracketSubexpressionsNested(t *testing.T) {
	tokens, err := lex(`BEGIN { print /[[:alpha:]/]/, /[[.x.]/]/, /[[=x=]/]/ }`)
	if !assert.NoError(t, err) {
		return
	}

	var regexes []string
	for _, tok := range tokens {
		if tok.kind == tokRegex {
			regexes = append(regexes, tok.lit)
		}
	}
	assert.Equal(t, []string{"[[:alpha:]/]", "[[.x.]/]", "[[=x=]/]"}, regexes)
}
