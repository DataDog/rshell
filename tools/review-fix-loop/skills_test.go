package main

import (
	"testing"
)

func TestStripFrontmatter(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "no frontmatter",
			input: "just content\nno frontmatter",
			want:  "just content\nno frontmatter",
		},
		{
			name:  "standard frontmatter",
			input: "---\nname: foo\ndescription: bar\n---\nactual content",
			want:  "actual content",
		},
		{
			name:  "frontmatter with trailing whitespace trimmed",
			input: "---\nname: foo\n---\n\n\ncontent here\n",
			want:  "content here",
		},
		{
			name:  "opening delimiter only — treated as no frontmatter",
			input: "---\nname: foo\nno closing delimiter",
			want:  "---\nname: foo\nno closing delimiter",
		},
		{
			name:  "empty content after frontmatter",
			input: "---\nkey: value\n---\n",
			want:  "",
		},
		{
			name:  "multi-line frontmatter",
			input: "---\nname: code-review\ndescription: \"comprehensive review\"\nargument-hint: \"[pr]\"\n---\nSkill body starts here.",
			want:  "Skill body starts here.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripFrontmatter(tt.input)
			if got != tt.want {
				t.Errorf("stripFrontmatter() = %q, want %q", got, tt.want)
			}
		})
	}
}
