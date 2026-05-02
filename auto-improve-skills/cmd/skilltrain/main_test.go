// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func TestFormatSkilltrainLogUsesSemanticColors(t *testing.T) {
	ctx := defaultLogContext()
	plain := formatSkilltrainLog(logSemanticSuccess, ctx, "accepted skill change", false)
	if plain != "skilltrain | ok    accepted skill change" {
		t.Fatalf("plain log line = %q", plain)
	}

	colored := formatSkilltrainLog(logSemanticSuccess, ctx, "accepted skill change", true)
	want := ansiDim + "skilltrain" + ansiReset + " | " + ansiGreen + "ok    accepted skill change" + ansiReset
	if colored != want {
		t.Fatalf("colored log line = %q, want %q", colored, want)
	}
}

func TestFormatSkilltrainLogAddsUsefulContextOnlyWhenPresent(t *testing.T) {
	plain := formatSkilltrainLog(logSemanticBenchmark, repeatLogContext("public", 2, 3), "benchmark repeat", false)
	if plain != "skilltrain | bench [public 2/3] benchmark repeat" {
		t.Fatalf("plain log line = %q", plain)
	}

	withoutRepeat := formatSkilltrainLog(logSemanticBenchmark, repeatLogContext("holdout", 1, 1), "benchmark result", false)
	if withoutRepeat != "skilltrain | bench [holdout] benchmark result" {
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

func TestSkillbenchGoRunTargetUsesNestedModule(t *testing.T) {
	root := t.TempDir()
	autoRoot := filepath.Join(root, "auto-improve-skills")
	if err := os.MkdirAll(filepath.Join(autoRoot, "cmd", "skillbench"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(autoRoot, "go.mod"), []byte("module example/auto\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	dir, target := skillbenchGoRunTarget(root)
	if dir != autoRoot || target != "./cmd/skillbench" {
		t.Fatalf("skillbenchGoRunTarget() = %q, %q; want %q, %q", dir, target, autoRoot, "./cmd/skillbench")
	}
}

func TestSkillbenchGoRunTargetKeepsLegacyLayout(t *testing.T) {
	root := t.TempDir()
	dir, target := skillbenchGoRunTarget(root)
	if dir != root || target != "./auto-improve-skills/cmd/skillbench" {
		t.Fatalf("skillbenchGoRunTarget() = %q, %q; want %q, %q", dir, target, root, "./auto-improve-skills/cmd/skillbench")
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

func TestFormatResearcherPromptDoesNotPassBenchmarkArtifacts(t *testing.T) {
	skillRel := filepath.Join("skills", "remote-host-diagnostics", "SKILL.md")
	prompt := formatResearcherPrompt("program content", skillRel, "skill content", 2, "General hidden-task feedback.\n")
	for _, want := range []string{
		"program.md",
		skillRel,
		"Improve only",
		"Do not inspect evaluator-private",
		"general-feedback",
		"General hidden-task feedback",
		"\"Changes\", \"Why\", and \"Size\" sections",
		"explain the rationale for each material change",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("researcher prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"/repo",
		"auto-improve-skills",
		"cases.yaml",
		"holdout.yaml",
		"benchmark suite",
		"best benchmark result",
		"researcher-feedback",
		"generated-fixtures",
		"raw/",
		"raw transcripts",
		"result JSON",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("researcher prompt leaked %q:\n%s", forbidden, prompt)
		}
	}
}

func TestBuildSanitizedFeedbackSourceIncludesOnlySafeAggregateMetrics(t *testing.T) {
	result := autoresearch.SuiteResult{
		QualityScore:               70,
		QualityMaxScore:            100,
		ObjectiveNormalizedScore:   0.68,
		AverageCaseDurationSeconds: 12.345,
		Repeats:                    3,
		SkillSizeEstimatedTokens:   1234,
		Cases: []autoresearch.CaseResult{
			{
				ID:              "case-alpha",
				NormalizedScore: 0.4,
				CommandCount:    4,
				FailedToolCalls: 1,
				ToolOutputBytes: 2048,
				SafetyViolations: []string{
					"fixture log rshell command missing --allowed-paths",
				},
				Criteria: []autoresearch.CriterionResult{
					{
						Name:             "contains 198.51.100.23 in auth.log",
						Source:           "final",
						Passed:           false,
						Max:              10,
						Detail:           "passed in 1/3 repeats; private detail /tmp/secret",
						EvidenceRequired: true,
					},
					{
						Name:   "commands use --allowed-paths {{LOG_ROOT}}",
						Source: "commands",
						Passed: false,
						Max:    5,
					},
					{
						Name:     "final avoids compromise claim",
						Source:   "final",
						Passed:   false,
						Max:      2,
						Negative: true,
					},
				},
			},
			{
				ID:              "case-beta",
				NormalizedScore: 1,
				CommandCount:    2,
				ToolOutputBytes: 1024,
				Criteria: []autoresearch.CriterionResult{{
					Name:             "passing private service evidence",
					Source:           "final",
					Passed:           true,
					Points:           1,
					Max:              1,
					EvidenceRequired: true,
				}},
			},
		},
	}

	source := buildSanitizedFeedbackSource(result)
	if source.Version != 3 {
		t.Fatalf("source version = %d, want 3", source.Version)
	}
	agg := source.SafeAggregate
	if agg.CaseCount != 2 || agg.CriteriaCount != 4 || agg.FailedCriteriaCount != 3 || agg.FailureOccurrences != 4 {
		t.Fatalf("aggregate counts = cases:%d criteria:%d failed:%d occurrences:%d", agg.CaseCount, agg.CriteriaCount, agg.FailedCriteriaCount, agg.FailureOccurrences)
	}
	if agg.CriteriaBySource["final"].TotalCriteria != 3 || agg.CriteriaBySource["final"].FailedCriteria != 2 || agg.CriteriaBySource["final"].FailureOccurrences != 3 {
		t.Fatalf("final source stats = %#v", agg.CriteriaBySource["final"])
	}
	if agg.CriteriaBySource["commands"].TotalCriteria != 1 || agg.CriteriaBySource["commands"].FailedCriteria != 1 {
		t.Fatalf("commands source stats = %#v", agg.CriteriaBySource["commands"])
	}
	if agg.EvidenceRequiredCriteria.TotalCriteria != 2 || agg.EvidenceRequiredCriteria.FailedCriteria != 1 || agg.EvidenceRequiredCriteria.FailureOccurrences != 2 {
		t.Fatalf("evidence stats = %#v", agg.EvidenceRequiredCriteria)
	}
	if agg.NegativeAssertionCriteria.TotalCriteria != 1 || agg.NegativeAssertionCriteria.FailedCriteria != 1 {
		t.Fatalf("negative stats = %#v", agg.NegativeAssertionCriteria)
	}
	if agg.SafetyViolationCases != 1 || agg.SafetyViolationCount != 1 {
		t.Fatalf("safety stats = cases:%d count:%d", agg.SafetyViolationCases, agg.SafetyViolationCount)
	}
	if agg.CaseScoreBuckets["low"] != 1 || agg.CaseScoreBuckets["full"] != 1 {
		t.Fatalf("case score buckets = %#v", agg.CaseScoreBuckets)
	}

	data, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	serialized := string(data)
	for _, forbidden := range []string{"case-alpha", "case-beta", "198.51.100.23", "auth.log", "--allowed-paths", "LOG_ROOT", "private service", "/tmp/secret"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("sanitized source leaked %q: %s", forbidden, serialized)
		}
	}
}

func TestFormatSanitizedResearcherFeedbackRendersLLMProseAndGuardrails(t *testing.T) {
	source := sanitizedFeedbackSource{Feedback: "- Focus final answers on nearby evidence and calibrated uncertainty.\n- Keep probes bounded before synthesizing."}
	feedback := formatSanitizedResearcherFeedbackFromSource(source)
	for _, want := range []string{
		"LLM-generated from sanitized aggregate metrics only",
		"Focus final answers",
		"Anti-overfitting guardrails",
		"Do not add exact case facts",
	} {
		if !strings.Contains(feedback, want) {
			t.Fatalf("feedback missing %q:\n%s", want, feedback)
		}
	}

	if got := formatSanitizedResearcherFeedbackFromSource(sanitizedFeedbackSource{}); got != "" {
		t.Fatalf("empty feedback should render no researcher feedback, got %q", got)
	}
}

func TestParseSanitizedFeedbackLLMOutputRequiresStrictSchema(t *testing.T) {
	feedback, err := parseSanitizedFeedbackLLMOutput(`{"feedback":"- Improve evidence grounding in final answers."}`)
	if err != nil {
		t.Fatalf("valid JSON failed: %v", err)
	}
	if !strings.Contains(feedback, "evidence grounding") {
		t.Fatalf("feedback = %q", feedback)
	}
	for name, output := range map[string]string{
		"extra field":    `{"feedback":"generic", "comment":"leak"}`,
		"markdown fence": "```json\n{\"feedback\":\"generic\"}\n```",
		"trailing text":  `{"feedback":"generic"} more`,
		"empty feedback": `{"feedback":"   "}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSanitizedFeedbackLLMOutput(output); err == nil {
				t.Fatalf("expected strict parse to reject %q", output)
			}
		})
	}
}

func TestValidateSanitizedFeedbackTextRejectsOverfitArtifacts(t *testing.T) {
	if _, err := validateSanitizedFeedbackText("Prefer bounded evidence summaries and calibrated uncertainty."); err != nil {
		t.Fatalf("generic feedback should be allowed: %v", err)
	}
	for name, feedback := range map[string]string{
		"ip":         "Mention 198.51.100.23 explicitly.",
		"path":       "Check /tmp/generated-fixtures/logs first.",
		"file":       "Always cite agent.log.",
		"timestamp":  "Focus on events around 10:12 UTC.",
		"identifier": "Handle rc-8831 carefully.",
		"line":       "Look for line 42.",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateSanitizedFeedbackText(feedback); err == nil {
				t.Fatalf("expected feedback %q to be rejected", feedback)
			}
		})
	}
}

func TestSanitizedFeedbackSourceResultPathUsesPreviousIteration(t *testing.T) {
	runDir := filepath.Join("runs", "train")
	if got := sanitizedFeedbackSourceResultPath(runDir, 1); got != filepath.Join(runDir, "iter-000-baseline", "result.json") {
		t.Fatalf("iter 1 source = %q", got)
	}
	if got := sanitizedFeedbackSourceResultPath(runDir, 3); got != filepath.Join(runDir, "iter-002", "result.json") {
		t.Fatalf("iter 3 source = %q", got)
	}
	if iter, ok := parseIterationDir("iter-003"); !ok || iter != 3 {
		t.Fatalf("parse iter-003 = %d, %v", iter, ok)
	}
	if _, ok := parseIterationDir("iter-003-holdout"); ok {
		t.Fatalf("holdout-style directory should not parse as iteration")
	}
}

func TestPrepareResearcherWorkspaceCopiesOnlyResearcherFiles(t *testing.T) {
	root := t.TempDir()
	programPath := filepath.Join(root, "auto-improve-skills", "program.md")
	skillPath := filepath.Join(root, "auto-improve-skills", "skills", "remote-host-diagnostics", "SKILL.md")
	secretPath := filepath.Join(root, "auto-improve-skills", "benchmarks", "remote-host-diagnostics", "cases.yaml")
	if err := os.MkdirAll(filepath.Dir(programPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(programPath, []byte("program"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("skill"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(secretPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	workspace, err := prepareResearcherWorkspace(root, skillPath)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(workspace.Dir)
	if strings.HasPrefix(workspace.Dir, root) {
		t.Fatalf("researcher workspace %q should not be inside repo root %q", workspace.Dir, root)
	}
	if workspace.SkillRel != filepath.Join("skills", "remote-host-diagnostics", "SKILL.md") {
		t.Fatalf("SkillRel = %q", workspace.SkillRel)
	}
	programData, err := os.ReadFile(filepath.Join(workspace.Dir, researcherProgramPath))
	if err != nil || string(programData) != "program" {
		t.Fatalf("workspace program = %q, %v", string(programData), err)
	}
	skillData, err := os.ReadFile(filepath.Join(workspace.Dir, workspace.SkillRel))
	if err != nil || string(skillData) != "skill" {
		t.Fatalf("workspace skill = %q, %v", string(skillData), err)
	}

	files := map[string]bool{}
	if err := filepath.WalkDir(workspace.Dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(workspace.Dir, path)
		if err != nil {
			return err
		}
		files[rel] = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{researcherProgramPath, filepath.Join("skills", "remote-host-diagnostics", "SKILL.md")}
	if len(files) != len(wantFiles) {
		t.Fatalf("workspace files = %#v", files)
	}
	for _, want := range wantFiles {
		if !files[want] {
			t.Fatalf("workspace missing %q in %#v", want, files)
		}
	}
}

func TestSaveIterationSkillArtifactsWritesSnapshotAndDiff(t *testing.T) {
	iterDir := t.TempDir()
	previous := []byte("# Skill\n\nOld guidance.\n")
	candidate := []byte("# Skill\n\nNew guidance.\n")

	if err := saveIterationSkillArtifacts(iterDir, previous, candidate); err != nil {
		t.Fatal(err)
	}

	previousData, err := os.ReadFile(filepath.Join(iterDir, iterationPreviousSkillPath))
	if err != nil || string(previousData) != string(previous) {
		t.Fatalf("previous skill artifact = %q, %v", string(previousData), err)
	}
	snapshotData, err := os.ReadFile(filepath.Join(iterDir, iterationSkillSnapshotPath))
	if err != nil || string(snapshotData) != string(candidate) {
		t.Fatalf("skill snapshot artifact = %q, %v", string(snapshotData), err)
	}
	diffData, err := os.ReadFile(filepath.Join(iterDir, iterationSkillDiffPath))
	if err != nil {
		t.Fatal(err)
	}
	diff := string(diffData)
	for _, want := range []string{iterationPreviousSkillPath, iterationSkillSnapshotPath, "-Old guidance.", "+New guidance."} {
		if !strings.Contains(diff, want) {
			t.Fatalf("skill diff missing %q:\n%s", want, diff)
		}
	}
}

func TestSaveBaselineSkillArtifactsWritesPublicAndHoldoutSnapshots(t *testing.T) {
	skillPath := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("baseline skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runDir := t.TempDir()
	baselineDir := filepath.Join(runDir, "iter-000-baseline")
	holdoutDir := filepath.Join(runDir, "iter-000-holdout")

	if err := saveBaselineSkillArtifacts(skillPath, baselineDir, true, holdoutDir); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{baselineDir, holdoutDir} {
		data, err := os.ReadFile(filepath.Join(dir, iterationSkillSnapshotPath))
		if err != nil || string(data) != "baseline skill\n" {
			t.Fatalf("baseline snapshot in %s = %q, %v", dir, string(data), err)
		}
	}
}

func TestResearcherToolsExcludeReadAndBash(t *testing.T) {
	if researcherTools != "edit,write" {
		t.Fatalf("researcherTools = %q, want edit,write", researcherTools)
	}
	for _, forbidden := range []string{"bash", "read"} {
		if strings.Contains(researcherTools, forbidden) {
			t.Fatalf("researcherTools must not include %s: %q", forbidden, researcherTools)
		}
	}
}

func TestFormatCommitSubjectIncludesTrainLoopIterationAndObjective(t *testing.T) {
	got := formatCommitSubject(3, 7, 0.84567)
	want := "[update skill] loop 3 - iter 7 - obj 84.57%"
	if got != want {
		t.Fatalf("formatCommitSubject() = %q, want %q", got, want)
	}
}

func TestFormatCommitSubjectDefaultsTrainLoopToOne(t *testing.T) {
	got := formatCommitSubject(0, 2, 0.2)
	want := "[update skill] loop 1 - iter 2 - obj 20.00%"
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
