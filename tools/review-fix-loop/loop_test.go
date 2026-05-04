// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"strings"
	"testing"
)

func TestBuildSummary(t *testing.T) {
	pr := PRInfo{
		Number: 42,
		URL:    "https://github.com/org/repo/pull/42",
	}

	t.Run("converged with clean iterations", func(t *testing.T) {
		results := []IterationResult{
			{Iteration: 1, Unresolved: 3, CIClean: false},
			{Iteration: 2, Unresolved: 1, CIClean: true},
			{Iteration: 3, Unresolved: 0, CIClean: true},
		}
		got := buildSummary(pr, results, true)

		assertContains(t, got, "CLEAN")
		assertNotContains(t, got, "ITERATION_LIMIT_REACHED")
		assertContains(t, got, "#42")
		assertContains(t, got, "https://github.com/org/repo/pull/42")
		assertContains(t, got, "Iterations completed**: 3")
		assertContains(t, got, "| 1 | 3 | Failing |")
		assertContains(t, got, "| 2 | 1 | Passing |")
		assertContains(t, got, "| 3 | 0 | Passing |")
	})

	t.Run("hit iteration limit", func(t *testing.T) {
		results := []IterationResult{
			{Iteration: 1, Unresolved: 2, CIClean: true},
		}
		got := buildSummary(pr, results, false)

		assertContains(t, got, "ITERATION_LIMIT_REACHED")
		assertNotContains(t, got, "CLEAN\n") // not the bare CLEAN status
	})

	t.Run("empty results", func(t *testing.T) {
		got := buildSummary(pr, nil, false)
		assertContains(t, got, "Iterations completed**: 0")
		assertContains(t, got, "ITERATION_LIMIT_REACHED")
	})

	t.Run("CI failing row", func(t *testing.T) {
		results := []IterationResult{
			{Iteration: 1, Unresolved: 0, CIClean: false},
		}
		got := buildSummary(pr, results, false)
		assertContains(t, got, "| 1 | 0 | Failing |")
	})
}

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q\ngot:\n%s", substr, s)
	}
}

func assertNotContains(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected output NOT to contain %q\ngot:\n%s", substr, s)
	}
}
