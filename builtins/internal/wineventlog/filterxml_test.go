// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package wineventlog

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFilterXmlAccepted(t *testing.T) {
	cases := []struct {
		name string
		xml  string
	}{
		{"single Select", `<QueryList><Query Id="0" Path="System"><Select Path="System">*</Select></Query></QueryList>`},
		{"multiple Queries", `<QueryList>
			<Query Id="0" Path="System"><Select Path="System">*</Select></Query>
			<Query Id="1" Path="Application"><Select Path="Application">*</Select></Query>
		</QueryList>`},
		{"Select + Suppress", `<QueryList><Query Id="0">
			<Select Path="Application">*[System[EventID=1033]]</Select>
			<Suppress Path="Application">*[EventData/Data[4]='0']</Suppress>
		</Query></QueryList>`},
		{"XML declaration allowed", `<?xml version="1.0" encoding="utf-8"?><QueryList><Query Id="0"><Select Path="System">*</Select></Query></QueryList>`},
		{"comments and whitespace", `<QueryList>
			<!-- a comment -->
			<Query Id="0"><Select Path="System">*</Select></Query>
		</QueryList>`},
		{"CDATA in XPath body", `<QueryList><Query Id="0"><Select Path="System"><![CDATA[*[System[Provider[@Name='X']]]]]></Select></Query></QueryList>`},
		{"entity-escaped XPath body", `<QueryList><Query Id="0"><Select Path="System">*[System[Provider[@Name=&apos;EventLog&apos;]]]</Select></Query></QueryList>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, ValidateFilterXml(tc.xml))
		})
	}
}

func TestValidateFilterXmlRejected(t *testing.T) {
	cases := []struct {
		name   string
		xml    string
		reason string
	}{
		{"empty string", "", "missing <QueryList> root"},
		{"whitespace only", "   \n", "missing <QueryList> root"},
		{"wrong root element", `<Foo/>`, "expected <QueryList> root"},
		{"disallowed child of QueryList", `<QueryList><Foo/></QueryList>`, "<QueryList> may only contain <Query>"},
		{"disallowed child of Query", `<QueryList><Query><Foo/></Query></QueryList>`, "<Query> may only contain <Select> or <Suppress>"},
		{"nested element inside Select", `<QueryList><Query><Select Path="X"><Nested/></Select></Query></QueryList>`, "is not allowed inside <Select>"},
		{"nested element inside Suppress", `<QueryList><Query><Suppress Path="X"><Nested/></Suppress></Query></QueryList>`, "is not allowed inside <Suppress>"},
		{"two root elements",
			`<QueryList><Query><Select Path="X">*</Select></Query></QueryList>` +
				`<QueryList><Query><Select Path="X">*</Select></Query></QueryList>`,
			"only one <QueryList> root"},
		{"unclosed element", `<QueryList><Query>`, "malformed XML"},
		{"unbalanced tags", `<QueryList></Query></QueryList>`, "malformed XML"},
		{"junk before root", `hello<QueryList/>`, "text outside root"},
		{"junk after root",
			`<QueryList><Query><Select Path="X">*</Select></Query></QueryList>trailing`,
			"text outside root"},
		{"not XML", `not even xml`, "text outside root"},
		{"empty QueryList self-closing", `<QueryList/>`, "<QueryList> must contain at least one <Query>"},
		{"empty QueryList long form", `<QueryList></QueryList>`, "<QueryList> must contain at least one <Query>"},
		{"empty Query", `<QueryList><Query Id="0"/></QueryList>`, "<Query> must contain at least one <Select> or <Suppress>"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFilterXml(tc.xml)
			require.Error(t, err, "expected rejection for %q", tc.xml)
			assert.Contains(t, err.Error(), tc.reason,
				"expected reason %q in %q", tc.reason, err.Error())
		})
	}
}

// TestValidateFilterXmlDeepNestingRejected proves the schema cap holds
// even for pathological inputs that try to exceed the three-level depth.
func TestValidateFilterXmlDeepNestingRejected(t *testing.T) {
	// Inside <Select>, any nested element is rejected — however deep.
	const open = `<QueryList><Query Id="0"><Select Path="System">`
	const close = `</Select></Query></QueryList>`
	deep := open + strings.Repeat("<x>", 1000) + strings.Repeat("</x>", 1000) + close
	err := ValidateFilterXml(deep)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not allowed inside <Select>")
}

// TestValidateFilterXmlLargeAllowedInput ensures a large but schema-valid
// query (many Queries at depth 2) is accepted in reasonable time.
func TestValidateFilterXmlLargeAllowedInput(t *testing.T) {
	var b strings.Builder
	b.WriteString("<QueryList>")
	for i := 0; i < 1000; i++ {
		b.WriteString(`<Query Id="`)
		b.WriteString("x")
		b.WriteString(`"><Select Path="System">*</Select></Query>`)
	}
	b.WriteString("</QueryList>")
	require.NoError(t, ValidateFilterXml(b.String()))
}
