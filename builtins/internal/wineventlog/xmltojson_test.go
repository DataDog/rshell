// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEventTreeSimple covers the core encoding conventions on a minimal
// event: attributes and children share a key namespace, leaf elements
// decode to their text content, and xmlns is preserved as an attribute.
func TestEventTreeSimple(t *testing.T) {
	const in = `<Event xmlns="http://example.com/ns">
		<System>
			<Provider Name="P1" Guid="{abc}"/>
			<Channel>System</Channel>
		</System>
	</Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	event, ok := tree["Event"].(map[string]any)
	require.True(t, ok, "expected Event key with map value: %#v", tree)
	assert.Equal(t, "http://example.com/ns", event["xmlns"])

	system, ok := event["System"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "System", system["Channel"])

	provider, ok := system["Provider"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "P1", provider["Name"])
	assert.Equal(t, "{abc}", provider["Guid"])
}

// TestEventTreeNormalizeEventID verifies the EventID/Qualifiers split
// applied to <EventID Qualifiers="Q">N</EventID>: the mixed-content map
// is replaced with two sibling scalar fields, matching the Datadog
// Agent's windowsevent.normalizeEventID transform.
func TestEventTreeNormalizeEventID(t *testing.T) {
	const in = `<Event><System><EventID Qualifiers="16384">6013</EventID></System></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	system := tree["Event"].(map[string]any)["System"].(map[string]any)
	assert.Equal(t, "6013", system["EventID"])
	assert.Equal(t, "16384", system["EventIDQualifier"])
}

// TestEventTreeEventIDScalar verifies that a plain scalar EventID (no
// Qualifiers attribute) decodes to a string without any transform.
func TestEventTreeEventIDScalar(t *testing.T) {
	const in = `<Event><System><EventID>1014</EventID></System></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	system := tree["Event"].(map[string]any)["System"].(map[string]any)
	assert.Equal(t, "1014", system["EventID"])
	_, hasQualifier := system["EventIDQualifier"]
	assert.False(t, hasQualifier)
}

// TestEventTreeFormatEventData verifies that <Data Name="K">V</Data>
// sequences collapse into a flat {K: V, ...} map at
// Event.EventData.Data when every entry has a Name attribute.
func TestEventTreeFormatEventData(t *testing.T) {
	const in = `<Event><EventData>
		<Data Name="QueryName">api.example.com</Data>
		<Data Name="QueryType">1</Data>
	</EventData></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	eventData := tree["Event"].(map[string]any)["EventData"].(map[string]any)
	data, ok := eventData["Data"].(map[string]any)
	require.True(t, ok, "expected Data as flat map, got %T", eventData["Data"])
	assert.Equal(t, "api.example.com", data["QueryName"])
	assert.Equal(t, "1", data["QueryType"])
}

// TestEventTreeEventDataPositional verifies that Data entries without
// Name attributes stay as an array (the flatten transform bails out).
func TestEventTreeEventDataPositional(t *testing.T) {
	const in = `<Event><EventData>
		<Data>first</Data>
		<Data>second</Data>
		<Data>third</Data>
	</EventData></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	eventData := tree["Event"].(map[string]any)["EventData"].(map[string]any)
	arr, ok := eventData["Data"].([]any)
	require.True(t, ok, "expected Data as array, got %T", eventData["Data"])
	assert.Equal(t, []any{"first", "second", "third"}, arr)
}

// TestEventTreeMixedDataArrayStays verifies that when only some Data
// entries have a Name attribute, the array form is preserved rather
// than losing the unnamed entries via a lossy flatten.
func TestEventTreeMixedDataArrayStays(t *testing.T) {
	const in = `<Event><EventData>
		<Data Name="First">1</Data>
		<Data>bare</Data>
	</EventData></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	eventData := tree["Event"].(map[string]any)["EventData"].(map[string]any)
	_, isArr := eventData["Data"].([]any)
	assert.True(t, isArr, "mixed Name/no-Name entries must not be flattened")
}

// TestEventTreeMixedTextAndAttrs verifies that an element carrying both
// a text body and an attribute places the text under the "value" key,
// matching the Datadog Agent's #text -> value rename.
func TestEventTreeMixedTextAndAttrs(t *testing.T) {
	const in = `<Event><System><Level note="info">4</Level></System></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	level := tree["Event"].(map[string]any)["System"].(map[string]any)["Level"].(map[string]any)
	assert.Equal(t, "info", level["note"])
	assert.Equal(t, "4", level["value"])
}

// TestEventTreeEmptyElement verifies that a self-closing or empty
// element decodes to an empty map (preserves the fact that it existed
// without inventing a string value).
func TestEventTreeEmptyElement(t *testing.T) {
	const in = `<Event><System><Correlation/></System></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)

	correlation, ok := tree["Event"].(map[string]any)["System"].(map[string]any)["Correlation"].(map[string]any)
	require.True(t, ok)
	assert.Empty(t, correlation)
}

// TestEventTreeJSONRoundTrip verifies the tree marshals through
// encoding/json without error and produces the expected envelope shape
// when a Message field is added as a sibling.
func TestEventTreeJSONRoundTrip(t *testing.T) {
	const in = `<Event><System><EventID>1014</EventID></System></Event>`
	tree, err := EventTree(in)
	require.NoError(t, err)
	tree["Message"] = "example"

	buf, err := json.Marshal(tree)
	require.NoError(t, err)

	var round map[string]any
	require.NoError(t, json.Unmarshal(buf, &round))
	assert.Equal(t, "example", round["Message"])
	assert.Equal(t, "1014",
		round["Event"].(map[string]any)["System"].(map[string]any)["EventID"])
}

// TestEventTreeMalformed surfaces a clear error on truncated XML
// instead of returning a partial tree.
func TestEventTreeMalformed(t *testing.T) {
	_, err := EventTree(`<Event><System><EventID>1014`)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event xml")
}

// TestEventTreeModerateDepthAccepted confirms that depth well within
// the cap (deeper than any real-world wevtapi output) still decodes
// cleanly — the cap only fires on pathological input.
func TestEventTreeModerateDepthAccepted(t *testing.T) {
	// 10 nested elements: Event > a0 > a1 > ... > a9.
	const levels = 10
	var open, close string
	for i := 0; i < levels; i++ {
		open += "<a" + itoa(i) + ">"
	}
	for i := levels - 1; i >= 0; i-- {
		close += "</a" + itoa(i) + ">"
	}
	in := "<Event>" + open + "leaf" + close + "</Event>"
	_, err := EventTree(in)
	require.NoError(t, err)
}

// TestEventTreeDepthLimitRejected proves the recursion cap holds on
// pathological deeply-nested input (far beyond any legitimate event
// schema), bounding stack use regardless of MaxBytes.
func TestEventTreeDepthLimitRejected(t *testing.T) {
	const levels = 100
	deep := strings.Repeat("<a>", levels) + strings.Repeat("</a>", levels)
	_, err := EventTree(deep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depth limit")
}

// itoa is a tiny local helper to avoid pulling in strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
