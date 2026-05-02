// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func TestFormatSkilltrainLogUsesSemanticColors(t *testing.T) {
	ctx := defaultLogContext()
	plain := formatSkilltrainLog(logSemanticSuccess, ctx, "accepted skill change", false)
	if plain != "skilltrain: accepted skill change" {
		t.Fatalf("plain log line = %q", plain)
	}

	colored := formatSkilltrainLog(logSemanticSuccess, ctx, "accepted skill change", true)
	want := ansiDim + "skilltrain:" + ansiReset + " " + ansiGreen + "accepted skill change" + ansiReset
	if colored != want {
		t.Fatalf("colored log line = %q, want %q", colored, want)
	}
}

func TestFormatSkilltrainLogAddsUsefulContextOnlyWhenPresent(t *testing.T) {
	plain := formatSkilltrainLog(logSemanticBenchmark, repeatLogContext("public", 2, 3), "benchmark repeat", false)
	if plain != "skilltrain: [public 2/3] benchmark repeat" {
		t.Fatalf("plain log line = %q", plain)
	}

	withoutRepeat := formatSkilltrainLog(logSemanticBenchmark, repeatLogContext("holdout", 1, 1), "benchmark result", false)
	if withoutRepeat != "skilltrain: [holdout] benchmark result" {
		t.Fatalf("single-repeat log line = %q", withoutRepeat)
	}
}

func TestCommandErrorIncludesCapturedOutput(t *testing.T) {
	_, _, capture := commandWriters(false)
	_, _ = capture.stdout.Write([]byte("public progress\n"))
	_, _ = capture.stderr.Write([]byte("failure details\n"))

	err := commandError("skillbench failed", errTestCommand, capture)
	for _, want := range []string{"skillbench failed", "stdout:\npublic progress", "stderr:\nfailure details"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

var errTestCommand = &testCommandError{}

type testCommandError struct{}

func (e *testCommandError) Error() string { return "exit status 1" }

func TestLogSemanticStyleMapsStatusesToColors(t *testing.T) {
	cases := []struct {
		name     string
		semantic logSemantic
		want     string
	}{
		{name: "success", semantic: logSemanticSuccess, want: ansiGreen},
		{name: "warning", semantic: logSemanticWarning, want: ansiYellow},
		{name: "dry run", semantic: logSemanticDryRun, want: ansiYellow},
		{name: "error", semantic: logSemanticError, want: ansiRed},
		{name: "benchmark", semantic: logSemanticBenchmark, want: ansiMagenta},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logSemanticStyle(tc.semantic); got != tc.want {
				t.Fatalf("logSemanticStyle(%s) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestDefaultParallelSettings(t *testing.T) {
	if defaultLoopCount != 1 {
		t.Fatalf("defaultLoopCount = %d, want 1", defaultLoopCount)
	}
	if defaultParallelRepeats != 3 {
		t.Fatalf("defaultParallelRepeats = %d, want 3", defaultParallelRepeats)
	}
	if defaultParallelCases != 3 {
		t.Fatalf("defaultParallelCases = %d, want 3", defaultParallelCases)
	}
}

func TestRunLoopWithRunnerRepeatsProvidedConfig(t *testing.T) {
	cfg := trainConfig{
		iterations:       3,
		model:            "test/model",
		runDir:           filepath.Join("auto-improve-skills", "runs", "trainloop"),
		judge:            true,
		parallelSuites:   true,
		push:             false,
		allowDirty:       true,
		verbose:          true,
		parallelRepeats:  2,
		parallelCases:    4,
		qualityTolerance: 0.02,
	}
	var calls []trainConfig
	err := runLoopWithRunner(3, cfg, func(call trainConfig) error {
		calls = append(calls, call)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3 {
		t.Fatalf("runner calls = %d, want 3", len(calls))
	}
	wantRunDirs := []string{"loop-001", "loop-002", "loop-003"}
	for i, call := range calls {
		wantRunDir := filepath.Join(cfg.runDir, wantRunDirs[i])
		if call.runDir != wantRunDir {
			t.Fatalf("call %d runDir = %q, want %q", i+1, call.runDir, wantRunDir)
		}
		if call.trainLoop != i+1 {
			t.Fatalf("call %d trainLoop = %d, want %d", i+1, call.trainLoop, i+1)
		}
		if call.iterations != cfg.iterations || call.model != cfg.model || call.judge != cfg.judge || call.parallelRepeats != cfg.parallelRepeats || call.parallelCases != cfg.parallelCases || call.qualityTolerance != cfg.qualityTolerance || call.verbose != cfg.verbose {
			t.Fatalf("call %d did not preserve supplied flags: %+v", i+1, call)
		}
	}
}

func TestRunLoopWithRunnerLeavesDefaultRunDirEmpty(t *testing.T) {
	var calls []trainConfig
	err := runLoopWithRunner(2, trainConfig{iterations: 3, judge: true}, func(call trainConfig) error {
		calls = append(calls, call)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for i, call := range calls {
		if call.runDir != "" {
			t.Fatalf("call %d runDir = %q, want empty default", i+1, call.runDir)
		}
		if call.trainLoop != i+1 {
			t.Fatalf("call %d trainLoop = %d, want %d", i+1, call.trainLoop, i+1)
		}
	}
}

func TestRunLoopWithRunnerSetsTrainLoopForSingleRun(t *testing.T) {
	var got trainConfig
	err := runLoopWithRunner(1, trainConfig{iterations: 3}, func(call trainConfig) error {
		got = call
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.trainLoop != 1 {
		t.Fatalf("trainLoop = %d, want 1", got.trainLoop)
	}
}

func TestRunLoopWithRunnerRejectsNonPositiveLoopCount(t *testing.T) {
	called := false
	err := runLoopWithRunner(0, trainConfig{}, func(trainConfig) error {
		called = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "-loop-count must be positive") {
		t.Fatalf("runLoopWithRunner error = %v, want loop-count validation", err)
	}
	if called {
		t.Fatal("runner was called after loop-count validation failed")
	}
}

func TestRepeatParallelismZeroMeansAllRepeats(t *testing.T) {
	if got := repeatParallelism(0, 3); got != 3 {
		t.Fatalf("repeatParallelism(0, 3) = %d, want 3", got)
	}
	if got := repeatParallelism(2, 3); got != 2 {
		t.Fatalf("repeatParallelism(2, 3) = %d, want 2", got)
	}
	if got := repeatParallelism(99, 3); got != 3 {
		t.Fatalf("repeatParallelism(99, 3) = %d, want 3", got)
	}
	if got := repeatParallelism(1, 3); got != 1 {
		t.Fatalf("repeatParallelism(1, 3) = %d, want 1", got)
	}
}

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

func TestFormatCommitSubjectIncludesTrainLoopIterationAndObjectiveChange(t *testing.T) {
	got := formatCommitSubject(3, 7, 0.81234, 0.84567)
	want := "[update skill] train loop 3|iter 7|obj 81.23%->84.57%"
	if got != want {
		t.Fatalf("formatCommitSubject() = %q, want %q", got, want)
	}
}

func TestFormatCommitSubjectDefaultsTrainLoopToOne(t *testing.T) {
	got := formatCommitSubject(0, 2, 0.1, 0.2)
	want := "[update skill] train loop 1|iter 2|obj 10.00%->20.00%"
	if got != want {
		t.Fatalf("formatCommitSubject() = %q, want %q", got, want)
	}
}

func TestAggregateBenchmarkRepeatsAveragesScoresAndCases(t *testing.T) {
	results := []autoresearch.SuiteResult{
		{
			SuiteName:                  "suite",
			Score:                      90,
			MaxScore:                   100,
			QualityScore:               90,
			QualityMaxScore:            100,
			ObjectiveScore:             91,
			ObjectiveMaxScore:          100,
			AverageCaseDurationSeconds: 80,
			DurationScore:              1,
			Cases: []autoresearch.CaseResult{{
				ID: "case-a", Score: 90, MaxScore: 100, NormalizedScore: 0.90, CommandCount: 4,
				Criteria: []autoresearch.CriterionResult{{Name: "finding", Passed: true, Points: 10, Max: 10}},
			}},
		},
		{
			SuiteName:                  "suite",
			Score:                      96,
			MaxScore:                   100,
			QualityScore:               96,
			QualityMaxScore:            100,
			ObjectiveScore:             97,
			ObjectiveMaxScore:          100,
			AverageCaseDurationSeconds: 100,
			DurationScore:              0.8,
			Cases: []autoresearch.CaseResult{{
				ID: "case-a", Score: 96, MaxScore: 100, NormalizedScore: 0.96, CommandCount: 6,
				Criteria: []autoresearch.CriterionResult{{Name: "finding", Passed: true, Points: 10, Max: 10}},
			}},
		},
	}
	aggregate, err := aggregateBenchmarkRepeats(results, []string{"repeat-001/result.json", "repeat-002/result.json"})
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Repeats != 2 || aggregate.Score != 93 {
		t.Fatalf("unexpected aggregate scores: %+v", aggregate)
	}
	if diff := aggregate.QualityNormalizedScore - 0.93; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("QualityNormalizedScore = %v, want 0.93", aggregate.QualityNormalizedScore)
	}
	if diff := aggregate.ObjectiveNormalizedScore - 0.94; diff < -1e-9 || diff > 1e-9 {
		t.Fatalf("ObjectiveNormalizedScore = %v, want 0.94", aggregate.ObjectiveNormalizedScore)
	}
	if len(aggregate.Cases) != 1 || aggregate.Cases[0].CommandCount != 5 || aggregate.Cases[0].Criteria[0].Detail != "passed in 2/2 repeats" {
		t.Fatalf("unexpected aggregate cases: %+v", aggregate.Cases)
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
		nil,
		"Tightened the workflow and removed duplicated guidance.",
		0.9302,
		" auto-improve-skills/skills/remote-host-diagnostics/SKILL.md | 12 ++++++------\n 1 file changed, 6 insertions(+), 6 deletions(-)\n",
		" 1 file changed, 6 insertions(+), 6 deletions(-)\n",
	)
	for _, want := range []string{
		"Training iteration: 2",
		"Benchmark report: auto-improve-skills/runs/train/iter-002/result.json",
		"Quality: 195.00/200.00 (97.50%)",
		"Objective: 94.25/100.00 (93.02% -> 94.25%, delta +1.23 pp)",
		"Average case duration: 82.3s",
		"Skill size: 2100 estimated tokens, 8400 bytes",
		"Objective config: quality=0.85 duration=0.10 skill_size=0.05",
		"datadog-agent-config-regression: 100.0/100.0 (100.0%)",
		"auth-bruteforce-summary: 95.0/100.0 (95.0%)",
		"Criteria: all deterministic checks passed",
		"Failed criteria:",
		"count near 96 (regex count): 0.0/5.0",
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
