// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package jq

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOrderedObjectDuplicateKeepsPositionAndLastValue(t *testing.T) {
	v, err := parseSingleJSON(context.Background(), `{"b":1,"a":2,"b":3}`)
	require.NoError(t, err)

	got, err := encodeValue(v, false, MaxOutputBytes)
	require.NoError(t, err)
	assert.Equal(t, `{"b":3,"a":2}`, got)
}

func TestJSONEncodingDoesNotEscapeHTML(t *testing.T) {
	v, err := stringValue("<tag>&")
	require.NoError(t, err)
	got, err := encodeValue(v, false, MaxOutputBytes)
	require.NoError(t, err)
	assert.Equal(t, `"<tag>&"`, got)
}

func TestNumberBoundsAndUnderflow(t *testing.T) {
	underflow, err := parseNumber("1e-400")
	require.NoError(t, err)
	assert.Equal(t, float64(0), underflow.num.float)

	_, err = parseNumber("1e400")
	assert.ErrorContains(t, err, "not finite")

	_, err = parseNumber(strings.Repeat("9", 78))
	assert.ErrorIs(t, err, errIntegerRange)
}

func TestContainerDepthIsConsistent(t *testing.T) {
	text := strings.Repeat("[", MaxNestingDepth) + "0" + strings.Repeat("]", MaxNestingDepth)
	v, err := parseSingleJSON(context.Background(), text)
	require.NoError(t, err)
	assert.Equal(t, MaxNestingDepth, v.depth)

	_, err = arrayValue([]value{v})
	assert.ErrorIs(t, err, errValueDepth)

	tooDeep := "[" + text + "]"
	_, err = parseSingleJSON(context.Background(), tooDeep)
	assert.ErrorIs(t, err, errValueDepth)
}

func TestSurrogateValidation(t *testing.T) {
	_, err := parseSingleJSON(context.Background(), `"\ud800"`)
	assert.ErrorIs(t, err, errInvalidSurrogate)

	v, err := parseSingleJSON(context.Background(), `"\ud83d\ude00"`)
	require.NoError(t, err)
	assert.Equal(t, "😀", v.str)

	_, err = parseSingleJSON(context.Background(), "\"\xff\"")
	assert.ErrorIs(t, err, errInvalidUTF8)
}

func TestFloatValueRejectsNonFinite(t *testing.T) {
	_, err := floatValue(math.Inf(1))
	assert.ErrorContains(t, err, "not finite")
	_, err = floatValue(math.NaN())
	assert.ErrorContains(t, err, "not finite")
}

func TestResultAccumulatorChecksAggregateBeforeAppend(t *testing.T) {
	large, err := stringValue(strings.Repeat("x", MaxValueBytes-2))
	require.NoError(t, err)
	results := newResultAccumulator(3)
	require.NoError(t, results.add(large))
	require.NoError(t, results.add(large))
	assert.ErrorIs(t, results.add(large), errResultLimit)
	assert.Len(t, results.values, 2)
}
