// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package sed

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestNormalizeArgsSplitsAttachedInPlaceSuffix is a regression test for the
// P1 finding: any character attached to -i within a short-option cluster
// must be treated as an (unsupported) backup suffix, never as more combined
// flags, even when that character happens to also be a registered
// shorthand letter (E, n, etc.). See splitAttachedInPlaceSuffix's doc for
// the exact GNU sed 4.9 behavior this mirrors.
func TestNormalizeArgsSplitsAttachedInPlaceSuffix(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{
			name: "bare -i is untouched",
			in:   []string{"-i", "s/a/b/", "file.txt"},
			want: []string{"-i", "s/a/b/", "file.txt"},
		},
		{
			name: "-iE splits into a rejectable -i=E",
			in:   []string{"-iE", "s/a/b/", "file.txt"},
			want: []string{"-i=E", "s/a/b/", "file.txt"},
		},
		{
			name: "-niE splits the prefix and the rejectable suffix",
			in:   []string{"-niE", "s/a/b/", "file.txt"},
			want: []string{"-n", "-i=E", "s/a/b/", "file.txt"},
		},
		{
			name: "-i.bak splits into a rejectable -i=.bak",
			in:   []string{"-i.bak", "s/a/b/", "file.txt"},
			want: []string{"-i=.bak", "s/a/b/", "file.txt"},
		},
		{
			name: "-Ei is untouched: i is the cluster's last character",
			in:   []string{"-Ei", "s/a/b/", "file.txt"},
			want: []string{"-Ei", "s/a/b/", "file.txt"},
		},
		{
			name: "-ni is untouched: i is the cluster's last character",
			in:   []string{"-ni", "s/a/b/p", "file.txt"},
			want: []string{"-ni", "s/a/b/p", "file.txt"},
		},
		{
			name: "-i=true is left to pflag's existing explicit-value rejection",
			in:   []string{"-i=true", "s/a/b/", "file.txt"},
			want: []string{"-i=true", "s/a/b/", "file.txt"},
		},
		{
			name: "--in-place (long form) is untouched",
			in:   []string{"--in-place", "s/a/b/", "file.txt"},
			want: []string{"--in-place", "s/a/b/", "file.txt"},
		},
		{
			name: "separate -n -i tokens are untouched",
			in:   []string{"-n", "-i", "s/a/b/", "file.txt"},
			want: []string{"-n", "-i", "s/a/b/", "file.txt"},
		},
		{
			name: "a positional after -- is never rewritten even if it looks like a cluster",
			in:   []string{"s/a/b/", "--", "-iE"},
			want: []string{"s/a/b/", "--", "-iE"},
		},
		{
			// -e is the one other short flag sed registers that takes a
			// value; an attached 'i' inside that value must not be
			// mistaken for -i. Verified against real GNU sed 4.9:
			// `sed -es/input/output/ file` applies the substitution
			// normally.
			name: "-e with an attached value containing 'i' is untouched",
			in:   []string{"-es/input/output/", "file.txt"},
			want: []string{"-es/input/output/", "file.txt"},
		},
		{
			// When 'i' appears before 'e' in the same cluster, 'i' wins:
			// everything after it (including the 'e') is -i's own attached
			// suffix, not a separate -e flag. Verified against real GNU
			// sed 4.9: `sed -ie ...` creates a backup file literally named
			// "file.txte".
			name: "-ie: i comes first, so e is i's suffix (rejectable)",
			in:   []string{"-ie", "s/a/b/", "file.txt"},
			want: []string{"-i=e", "s/a/b/", "file.txt"},
		},
		{
			name: "-nie: n, then i before e, so e is i's suffix (rejectable)",
			in:   []string{"-nie", "s/a/b/", "file.txt"},
			want: []string{"-n", "-i=e", "s/a/b/", "file.txt"},
		},
		{
			// Verified against real GNU sed 4.9: `sed -nei 's/a/b/p' file`
			// fails trying to parse "i" as a script (-e's value), matching
			// "untouched" here rather than being rewritten as -i.
			name: "-nei: n, then e (whose value is 'i'), untouched",
			in:   []string{"-nei", "s/a/b/", "file.txt"},
			want: []string{"-nei", "s/a/b/", "file.txt"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, normalizeArgs(c.in))
		})
	}
}
