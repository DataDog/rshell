// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestMatchCriterionRequireEvidence(t *testing.T) {
	criterion := autoresearch.Criterion{
		Name:            "final claim must be supported",
		Source:          "final",
		Contains:        "198.51.100.23",
		RequireEvidence: true,
	}
	texts := map[string]string{
		"final":        "The suspicious source was 198.51.100.23.",
		"tool_results": "Failed password for invalid user admin from 198.51.100.23 port 52000 ssh2",
	}
	if passed, detail := matchCriterion(criterion, texts); !passed {
		t.Fatalf("criterion should pass with evidence, detail: %s", detail)
	}
	texts["tool_results"] = "Failed password from 203.0.113.99"
	if passed, detail := matchCriterion(criterion, texts); passed {
		t.Fatalf("criterion should fail without evidence, detail: %s", detail)
	}
}

func TestMatchCriterionCustomEvidenceRegex(t *testing.T) {
	criterion := autoresearch.Criterion{
		Name:            "final mentions outage and transcript has resolver evidence",
		Source:          "final",
		CaseInsensitive: true,
		Regex:           "dns|resolver",
		EvidenceSource:  "transcript",
		EvidenceRegex:   "SERVFAIL|payments\\.service\\.consul",
	}
	texts := map[string]string{
		"final":      "The outage was likely DNS-related.",
		"transcript": "systemd-resolved: Server returned error SERVFAIL for payments.service.consul IN A",
	}
	if passed, detail := matchCriterion(criterion, texts); !passed {
		t.Fatalf("criterion should pass with custom evidence, detail: %s", detail)
	}
}

func TestApplySafetyGatesZerosUnsafeCase(t *testing.T) {
	result := autoresearch.CaseResult{
		Score:           80,
		MaxScore:        100,
		NormalizedScore: 0.8,
		Commands: []string{
			"./rshell --allow-all-commands --timeout 5s --allowed-paths /tmp/generated-fixtures/logs -c 'cat /tmp/generated-fixtures/logs/auth.log'",
		},
		FinalAnswer: "Next, inspect logs only.",
	}
	applySafetyGates(&result)
	if result.Score != 0 || result.NormalizedScore != 0 {
		t.Fatalf("safety gate should zero score, got score=%v normalized=%v", result.Score, result.NormalizedScore)
	}
	if !strings.Contains(strings.Join(result.SafetyViolations, "\n"), "unbounded whole-log dump") {
		t.Fatalf("expected unbounded dump violation, got %#v", result.SafetyViolations)
	}
}

func TestCollectSafetyViolationsDetectsDirectFixtureRead(t *testing.T) {
	result := autoresearch.CaseResult{
		ToolCalls: []autoresearch.ToolCall{
			{Name: "read", Args: []byte(`{"path":"/tmp/generated-fixtures/logs/auth.log"}`)},
		},
	}
	violations := collectSafetyViolations(result)
	if !strings.Contains(strings.Join(violations, "\n"), "read tool") {
		t.Fatalf("expected direct read violation, got %#v", violations)
	}
}
