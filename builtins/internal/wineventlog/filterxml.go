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

// ValidateFilterXml checks that xmlStr parses as a <QueryList> structured
// query conforming to the Windows Event Log schema, rejecting anything
// that could reach EvtQuery with unexpected structure. Runs before the
// string is handed to wevtapi so malformed or schema-violating input is
// caught in-process.
//
// Accepted shape (maximum three element levels):
//
//	<QueryList>
//	  <Query Id="N" Path="...">              (zero or more)
//	    <Select Path="...">XPATH</Select>    (zero or more)
//	    <Suppress Path="...">XPATH</Suppress> (zero or more)
//	  </Query>
//	</QueryList>
//
// Text content inside <Select> and <Suppress> is the XPath expression and
// is passed through verbatim — its syntax is wevtapi's concern. XML
// comments, processing instructions, and directives (outside CDATA) are
// allowed and ignored. No element may appear below <Select> or
// <Suppress>; that upper bound is what keeps this validator finite.
//
// Returns a nil error on success; on failure returns an error whose
// message is a short human-readable reason (no "--FilterXml:" prefix —
// callers prepend their own context).
func ValidateFilterXml(xmlStr string) error {
	dec := xml.NewDecoder(strings.NewReader(xmlStr))
	// frame tracks per-element state so we can require non-empty content
	// (a QueryList with no <Query>, or a <Query> with no <Select>/<Suppress>,
	// passes XML well-formedness but is rejected by EvtQuery with a generic
	// "specified query is invalid" / "parameter is incorrect" error — catch
	// it in-process for a clearer diagnostic).
	type frame struct {
		name     string
		sawChild bool
	}
	var stack []frame
	sawRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("malformed XML: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			switch len(stack) {
			case 0:
				if sawRoot {
					return fmt.Errorf("only one <QueryList> root is allowed")
				}
				if name != "QueryList" {
					return fmt.Errorf("expected <QueryList> root element, got <%s>", name)
				}
				sawRoot = true
			case 1:
				if name != "Query" {
					return fmt.Errorf("<QueryList> may only contain <Query> elements, got <%s>", name)
				}
			case 2:
				if name != "Select" && name != "Suppress" {
					return fmt.Errorf("<Query> may only contain <Select> or <Suppress> elements, got <%s>", name)
				}
			default:
				return fmt.Errorf("<%s> is not allowed inside <%s>", name, stack[len(stack)-1].name)
			}
			if len(stack) > 0 {
				stack[len(stack)-1].sawChild = true
			}
			stack = append(stack, frame{name: name})
		case xml.EndElement:
			if len(stack) == 0 {
				return fmt.Errorf("unexpected closing tag </%s>", t.Name.Local)
			}
			top := stack[len(stack)-1]
			if (top.name == "QueryList" || top.name == "Query") && !top.sawChild {
				if top.name == "QueryList" {
					return fmt.Errorf("<QueryList> must contain at least one <Query>")
				}
				return fmt.Errorf("<Query> must contain at least one <Select> or <Suppress>")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			// Text content inside elements is valid (XPath bodies in
			// Select/Suppress). Text outside any element — before the
			// root, between siblings at the top level, or after the
			// root closes — is not; reject anything but whitespace.
			if len(stack) == 0 && strings.TrimSpace(string(t)) != "" {
				return fmt.Errorf("unexpected text outside root element")
			}
		}
	}
	if !sawRoot {
		return fmt.Errorf("missing <QueryList> root element")
	}
	if len(stack) != 0 {
		return fmt.Errorf("unclosed element <%s>", stack[len(stack)-1].name)
	}
	return nil
}
