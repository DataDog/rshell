// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func TestBoundedUpperScore(t *testing.T) {
	tests := []struct {
		name      string
		value     float64
		budget    float64
		hardLimit float64
		want      float64
	}{
		{name: "under budget", value: 10, budget: 20, hardLimit: 40, want: 1},
		{name: "at budget", value: 20, budget: 20, hardLimit: 40, want: 1},
		{name: "between", value: 30, budget: 20, hardLimit: 40, want: 0.5},
		{name: "at hard limit", value: 40, budget: 20, hardLimit: 40, want: 0},
		{name: "over hard limit", value: 50, budget: 20, hardLimit: 40, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := boundedUpperScore(tt.value, tt.budget, tt.hardLimit); got != tt.want {
				t.Fatalf("boundedUpperScore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestApplyObjectiveScore(t *testing.T) {
	result := autoresearch.SuiteResult{
		QualityNormalizedScore: 0.90,
		DurationScore:          0.50,
		SkillSizeScore:         1.00,
		ObjectiveConfig: autoresearch.ObjectiveConfig{
			QualityWeight:   0.85,
			DurationWeight:  0.10,
			SkillSizeWeight: 0.05,
		},
	}
	applyObjectiveScore(&result)
	want := 0.865
	if result.ObjectiveMaxScore != 100 {
		t.Fatalf("ObjectiveMaxScore = %v, want 100", result.ObjectiveMaxScore)
	}
	if diff := result.ObjectiveNormalizedScore - want; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("ObjectiveNormalizedScore = %v, want %v", result.ObjectiveNormalizedScore, want)
	}
}

func TestMeasureSkillSize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	content := "one two three four"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stats, err := measureSkillSize(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Bytes != len(content) || stats.Chars != len(content) || stats.Words != 4 || stats.EstimatedTokens != 5 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
