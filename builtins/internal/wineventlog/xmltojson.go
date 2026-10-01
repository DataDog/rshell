// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// EventTree converts a rendered Event XML string into a generic
// JSON-compatible tree built from map[string]any / []any / string.
//
// The returned map preserves the XML root (i.e. has a single top-level
// "Event" key whose value is the decoded <Event> element). Callers can
// add sibling top-level fields (e.g. Message) to the returned map before
// encoding.
//
// Encoding conventions match the Datadog Agent's windowsevent package:
//
//   - Attributes and child elements share the same key namespace. No "@"
//     prefix. <Provider Name="X"/> becomes {"Provider": {"Name": "X"}}.
//   - An element containing only text decodes to that text as a plain
//     string. Elements with mixed text + children/attributes place their
//     text content under the "value" key (the Agent uses "#text" during
//     decoding and renames to "value" on JSON output; we rename in-place).
//   - Repeated child elements with the same tag collapse into a []any in
//     document order (this matches <EventData><Data/><Data/></EventData>).
//   - Whitespace-only text nodes are discarded. Leading/trailing
//     whitespace on text nodes is trimmed.
//
// Two post-decode transforms mirror the Agent:
//
//   - normalizeEventID: splits Event.System.EventID = {value: N,
//     Qualifiers: Q} into EventID: N and EventIDQualifier: Q sibling
//     fields, so Event.System.EventID is always a plain scalar.
//   - formatEventDataField: if every Event.EventData.Data entry has a
//     Name attribute, collapses them into a single {Name: value, ...}
//     map (the common shape for named EventData fields).
func EventTree(xmlStr string) (map[string]any, error) {
	dec := xml.NewDecoder(strings.NewReader(xmlStr))
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("event xml: no root element")
		}
		if err != nil {
			return nil, fmt.Errorf("event xml: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		val, err := decodeElement(dec, start, 1)
		if err != nil {
			return nil, err
		}
		root := map[string]any{start.Name.Local: val}
		normalizeEventID(root)
		formatEventDataField(root)
		return root, nil
	}
}

// eventTreeMaxDepth caps XML nesting in EventTree. Windows Event XML has
// a fixed schema with ~5 nesting levels in practice (Event > RenderingInfo
// > Keywords > Keyword is the deepest common path); 32 is a comfortable
// ceiling that rejects only pathological input without constraining any
// legitimate event shape.
const eventTreeMaxDepth = 32

// decodeElement consumes tokens from dec until start's matching
// EndElement, returning the element's decoded value. depth is the
// 1-based nesting level of start (the root element is decoded at
// depth 1) and is checked against eventTreeMaxDepth to bound
// recursion on adversarial input.
func decodeElement(dec *xml.Decoder, start xml.StartElement, depth int) (any, error) {
	if depth > eventTreeMaxDepth {
		return nil, fmt.Errorf("event xml: depth limit %d exceeded", eventTreeMaxDepth)
	}
	obj := map[string]any{}
	for _, a := range start.Attr {
		obj[a.Name.Local] = a.Value
	}
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("event xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeElement(dec, t, depth+1)
			if err != nil {
				return nil, err
			}
			addChild(obj, t.Name.Local, child)
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			s := strings.TrimSpace(text.String())
			if len(obj) == 0 {
				if s == "" {
					return map[string]any{}, nil
				}
				return s, nil
			}
			if s != "" {
				obj["value"] = s
			}
			return obj, nil
		}
	}
}

// addChild sets obj[key]=v, promoting to a []any if key is already set so
// repeated child elements collapse into an array in document order.
func addChild(obj map[string]any, key string, v any) {
	existing, ok := obj[key]
	if !ok {
		obj[key] = v
		return
	}
	if arr, ok := existing.([]any); ok {
		obj[key] = append(arr, v)
		return
	}
	obj[key] = []any{existing, v}
}

// normalizeEventID splits Event.System.EventID when it carries both a
// Qualifiers attribute and text content, so the resulting JSON is
// consistent regardless of whether the source XML had the attribute.
//
// Before: Event.System.EventID = {"value": "7036", "Qualifiers": "16384"}
// After:  Event.System.EventID = "7036"
//
//	Event.System.EventIDQualifier = "16384"
//
// Format reference:
// https://learn.microsoft.com/en-us/windows/win32/wes/eventschema-systempropertiestype-complextype
func normalizeEventID(root map[string]any) {
	system := nestedMap(root, "Event", "System")
	if system == nil {
		return
	}
	idVal, ok := system["EventID"].(map[string]any)
	if !ok {
		return
	}
	text, foundText := idVal["value"]
	qualifier, foundQualifier := idVal["Qualifiers"]
	if !foundText || !foundQualifier {
		return
	}
	system["EventID"] = text
	system["EventIDQualifier"] = qualifier
}

// formatEventDataField collapses <Data Name='K'>V</Data> sequences at
// Event.EventData.Data into a flat {K: V, ...} map. The Name attribute
// is optional in the schema; if any Data entry lacks a Name, the array
// form is left untouched.
//
// Format reference:
// https://learn.microsoft.com/en-us/windows/win32/wes/eventschema-datafieldtype-complextype
func formatEventDataField(root map[string]any) {
	eventData := nestedMap(root, "Event", "EventData")
	if eventData == nil {
		return
	}
	raw, ok := eventData["Data"]
	if !ok {
		return
	}
	entries, ok := raw.([]any)
	if !ok {
		// Single Data element; nothing to flatten.
		return
	}
	named := make(map[string]any, len(entries))
	for _, e := range entries {
		m, ok := e.(map[string]any)
		if !ok {
			return
		}
		name, ok := m["Name"].(string)
		if !ok {
			return
		}
		// Prefer the text body ("value") as the field content; if the
		// Data element has no body, use an empty string.
		v, present := m["value"]
		if !present {
			v = ""
		}
		named[name] = v
	}
	eventData["Data"] = named
}

// nestedMap walks obj[keys[0]][keys[1]]... returning the final map, or
// nil if any hop is missing or not a map.
func nestedMap(obj map[string]any, keys ...string) map[string]any {
	cur := obj
	for _, k := range keys {
		next, ok := cur[k].(map[string]any)
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}
