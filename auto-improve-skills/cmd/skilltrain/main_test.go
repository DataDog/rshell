// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"strings"
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

func TestFormatCommitBodyIncludesChangeAndScoreDetails(t *testing.T) {
	result := autoresearch.SuiteResult{
		SuiteName:                  "remote-host-diagnostics-quality",
		Model:                      "test/model",
		QualityScore:               195,
		QualityMaxScore:            200,
		QualityNormalizedScore:     0.975,
		ObjectiveScore:             94.25,
		ObjectiveMaxScore:          100,
		ObjectiveNormalizedScore:   0.9425,
		AverageCaseDurationSeconds: 82.3,
		DurationScore:              0.91,
		SkillSizeEstimatedTokens:   2100,
		SkillSizeBytes:             8400,
		SkillSizeScore:             0.93,
		ObjectiveConfig: autoresearch.ObjectiveConfig{
			QualityWeight:            0.85,
			DurationWeight:           0.10,
			SkillSizeWeight:          0.05,
			DurationBudgetSeconds:    120,
			DurationHardLimitSeconds: 300,
			SkillSizeTargetTokens:    2000,
			SkillSizeHardLimitTokens: 3500,
		},
		Cases: []autoresearch.CaseResult{
			{
				ID: "datadog-agent-config-regression", Score: 100, MaxScore: 100, NormalizedScore: 1, DurationSeconds: 71.2, CommandCount: 5,
				Criteria: []autoresearch.CriterionResult{{Name: "root cause", Passed: true, Points: 25, Max: 25}},
			},
			{
				ID: "auth-bruteforce-summary", Score: 95, MaxScore: 100, NormalizedScore: 0.95, DurationSeconds: 93.4, CommandCount: 6, FailedToolCalls: 1,
				Criteria: []autoresearch.CriterionResult{{Name: "count near 96", Passed: false, Max: 5, Detail: "regex count"}},
			},
		},
	}
	body := formatCommitBody(
		"/repo",
		"auto-improve-skills/skills/remote-host-diagnostics/SKILL.md",
		2,
		result,
		"/repo/auto-improve-skills/runs/train/iter-002/result.json",
		"Tightened the workflow and removed duplicated guidance.",
		0.0123,
		" auto-improve-skills/skills/remote-host-diagnostics/SKILL.md | 12 ++++++------\n 1 file changed, 6 insertions(+), 6 deletions(-)\n",
		" 1 file changed, 6 insertions(+), 6 deletions(-)\n",
	)
	for _, want := range []string{
		"Training iteration: 2",
		"Benchmark report: auto-improve-skills/runs/train/iter-002/result.json",
		"Quality: 195.00/200.00 (97.50%)",
		"Objective: 94.25/100.00 (94.25%, delta +1.23 pp)",
		"Average case duration: 82.3s",
		"Skill size: 2100 estimated tokens, 8400 bytes",
		"Objective config: quality=0.85 duration=0.10 skill_size=0.05",
		"datadog-agent-config-regression: 100.0/100.0 (100.0%)",
		"auth-bruteforce-summary: 95.0/100.0 (95.0%)",
		"Criteria: all deterministic checks passed",
		"Failed criteria:",
		"count near 96 (regex count): 0/5.0",
		"Researcher summary:",
		"Tightened the workflow",
		"Change summary:",
		"1 file changed, 6 insertions(+), 6 deletions(-)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("commit body missing %q:\n%s", want, body)
		}
	}
}
