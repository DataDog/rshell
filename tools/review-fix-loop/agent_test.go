package main

import (
	"bytes"
	"testing"
)

func TestLineWriter(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		writes []string
		want   string
	}{
		{
			name:   "single line no trailing newline",
			prefix: "[a] ",
			writes: []string{"hello"},
			want:   "[a] hello",
		},
		{
			name:   "single line with trailing newline",
			prefix: "[a] ",
			writes: []string{"hello\n"},
			want:   "[a] hello\n",
		},
		{
			name:   "multiple lines in one write",
			prefix: "[a] ",
			writes: []string{"line1\nline2\nline3\n"},
			want:   "[a] line1\n[a] line2\n[a] line3\n",
		},
		{
			name:   "split across multiple writes mid-line",
			prefix: "[x] ",
			writes: []string{"hel", "lo\nwor", "ld\n"},
			want:   "[x] hello\n[x] world\n",
		},
		{
			name:   "newline-only write",
			prefix: "[a] ",
			writes: []string{"\n"},
			want:   "[a] \n",
		},
		{
			name:   "empty write is a no-op",
			prefix: "[a] ",
			writes: []string{"first\n", "", "second\n"},
			want:   "[a] first\n[a] second\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			lw := newLineWriter(&buf, tt.prefix)
			for _, w := range tt.writes {
				lw.write(w)
			}
			if got := buf.String(); got != tt.want {
				t.Errorf("lineWriter output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLineWriterFlush(t *testing.T) {
	t.Run("flush adds newline when mid-line", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLineWriter(&buf, "[p] ")
		lw.write("no newline here")
		lw.flush()
		want := "[p] no newline here\n"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("flush is no-op when already at line start", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLineWriter(&buf, "[p] ")
		lw.write("line\n")
		lw.flush()
		want := "[p] line\n"
		if got := buf.String(); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("flush on fresh writer is no-op", func(t *testing.T) {
		var buf bytes.Buffer
		lw := newLineWriter(&buf, "[p] ")
		lw.flush()
		if got := buf.String(); got != "" {
			t.Errorf("expected empty, got %q", got)
		}
	})
}
