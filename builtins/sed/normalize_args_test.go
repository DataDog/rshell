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
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, normalizeArgs(c.in))
		})
	}
}
