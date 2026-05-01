// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"testing"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func TestBenchmarkObjectiveFallsBackToQualityForOldResults(t *testing.T) {
	result := autoresearch.SuiteResult{NormalizedScore: 0.75}
	if got := benchmarkQuality(result); got != 0.75 {
		t.Fatalf("benchmarkQuality() = %v, want 0.75", got)
	}
	if got := benchmarkObjective(result); got != 0.75 {
		t.Fatalf("benchmarkObjective() = %v, want 0.75", got)
	}
}

func TestBenchmarkObjectiveUsesNewFields(t *testing.T) {
	result := autoresearch.SuiteResult{
		NormalizedScore:          0.75,
		QualityMaxScore:          100,
		QualityNormalizedScore:   0.80,
		ObjectiveMaxScore:        100,
		ObjectiveNormalizedScore: 0.90,
	}
	if got := benchmarkQuality(result); got != 0.80 {
		t.Fatalf("benchmarkQuality() = %v, want 0.80", got)
	}
	if got := benchmarkObjective(result); got != 0.90 {
		t.Fatalf("benchmarkObjective() = %v, want 0.90", got)
	}
}
