package pyruntime

import (
	"testing"
)

func TestParseListComp(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{"basic_nl", "[x * x for x in range(5)]\n"},
		{"basic", "[x * x for x in range(5)]"},
		{"semi", "squares = [x * x for x in range(5)]; print(squares)"},
		{"semi_nl", "squares = [x * x for x in range(5)]; print(squares)\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.src, "<test>")
			if err != nil {
				t.Logf("parse error: %v", err)
			} else {
				t.Logf("ok")
			}
		})
	}
}
