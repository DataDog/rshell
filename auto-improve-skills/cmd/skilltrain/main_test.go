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
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

func TestFormatSkilltrainLogUsesSemanticColors(t *testing.T) {
	ctx := defaultLogContext()
	at := time.Date(2026, 5, 4, 12, 34, 56, 789000000, time.UTC)
	plain := formatSkilltrainLogAt(logSemanticSuccess, ctx, "accepted skill change", false, at)
	if plain != "2026-05-04T12:34:56.789Z | ok    accepted skill change" {
		t.Fatalf("plain log line = %q", plain)
	}

	colored := formatSkilltrainLogAt(logSemanticSuccess, ctx, "accepted skill change", true, at)
	want := ansiDim + "2026-05-04T12:34:56.789Z" + ansiReset + " | " + ansiGreen + "ok    accepted skill change" + ansiReset
	if colored != want {
		t.Fatalf("colored log line = %q, want %q", colored, want)
	}
}

func TestFormatSkilltrainLogAddsUsefulContextOnlyWhenPresent(t *testing.T) {
	at := time.Date(2026, 5, 4, 12, 34, 56, 789000000, time.UTC)
	plain := formatSkilltrainLogAt(logSemanticBenchmark, suiteLogContext("public"), "benchmark result", false, at)
	if plain != "2026-05-04T12:34:56.789Z | bench [public] benchmark result" {
		t.Fatalf("plain log line = %q", plain)
	}

	withoutSuite := formatSkilltrainLogAt(logSemanticBenchmark, suiteLogContext(""), "benchmark result", false, at)
	if withoutSuite != "2026-05-04T12:34:56.789Z | bench benchmark result" {
		t.Fatalf("no-suite log line = %q", withoutSuite)
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

func TestDefaultTrainingSettings(t *testing.T) {
	if defaultIterations != 5 {
		t.Fatalf("defaultIterations = %d, want 5", defaultIterations)
	}
	if defaultLoopCount != 1 {
		t.Fatalf("defaultLoopCount = %d, want 1", defaultLoopCount)
	}
	if defaultParallelCases != 10 {
		t.Fatalf("defaultParallelCases = %d, want 10", defaultParallelCases)
	}
}

func TestRunLoopWithRunnerPreservesProvidedConfig(t *testing.T) {
	cfg := trainConfig{
		iterations:       3,
		model:            "test/model",
		runDir:           filepath.Join("auto-improve-skills", "runs", "trainloop"),
		judge:            true,
		parallelSuites:   true,
		push:             false,
		allowDirty:       true,
		verbose:          true,
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
		if call.iterations != cfg.iterations || call.model != cfg.model || call.judge != cfg.judge || call.parallelCases != cfg.parallelCases || call.qualityTolerance != cfg.qualityTolerance || call.verbose != cfg.verbose {
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

func TestSaveBaselineSkillArtifactsWritesSkillSnapshots(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	skill := []byte("# Skill\n\nUse rshell.\n")
	if err := os.WriteFile(skillPath, skill, 0o644); err != nil {
		t.Fatal(err)
	}

	baselineDir := filepath.Join(dir, "runs", "iter-000-baseline")
	holdoutDir := filepath.Join(dir, "runs", "iter-000-holdout")
	if err := saveBaselineSkillArtifacts(skillPath, baselineDir, true, holdoutDir); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join(baselineDir, iterationSkillSnapshotPath),
		filepath.Join(holdoutDir, iterationSkillSnapshotPath),
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(skill) {
			t.Fatalf("%s = %q, want %q", path, got, skill)
		}
	}
}

func TestSaveIterationSkillArtifactsWritesSnapshotsAndRenamedDiff(t *testing.T) {
	dir := t.TempDir()
	previous := []byte("# Skill\n\nUse the old workflow.\n")
	candidate := []byte("# Skill\n\nUse the new workflow.\n")

	if err := saveIterationSkillArtifacts(dir, previous, candidate); err != nil {
		t.Fatal(err)
	}

	previousPath := filepath.Join(dir, iterationPreviousSkillPath)
	candidatePath := filepath.Join(dir, iterationSkillSnapshotPath)
	diffPath := filepath.Join(dir, iterationSkillDiffPath)
	for path, want := range map[string][]byte{
		previousPath:  previous,
		candidatePath: candidate,
	} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}

	diff, err := os.ReadFile(diffPath)
	if err != nil {
		t.Fatalf("reading %s: %v", diffPath, err)
	}
	for _, want := range []string{"SKILL.previous.md", "SKILL.candidate.md", "-Use the old workflow.", "+Use the new workflow."} {
		if !strings.Contains(string(diff), want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "SKILL.diff")); !os.IsNotExist(err) {
		t.Fatalf("legacy SKILL.diff should not be written, stat error: %v", err)
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

func TestFormatResearcherPromptIncludesPublicArtifactsAndForbidsHoldout(t *testing.T) {
	skillRel := filepath.Join("auto-improve-skills", "skills", "remote-host-diagnostics", "SKILL.md")
	casesPath := filepath.Join("auto-improve-skills", "benchmarks", "remote-host-diagnostics", "cases.yaml")
	runDir := filepath.Join("auto-improve-skills", "runs", "train")
	bestResultPath := filepath.Join(runDir, "iter-000-baseline", "result.json")
	prompt := formatResearcherPrompt(skillRel, casesPath, runDir, bestResultPath, 2, 0.01, "")
	for _, want := range []string{
		"program.md",
		skillRel,
		casesPath,
		runDir,
		bestResultPath,
		"best public benchmark result",
		"current training run artifact directory",
		"previous public iterations",
		"report.md",
		"raw outputs",
		"researcher.stderr.txt",
		"researcher.stdout.md",
		"result.json",
		"SKILL.candidate.md",
		"SKILL.md.diff",
		"SKILL.previous.md",
		"Do not read, list, grep, inspect, or edit holdout-related",
		"holdout.yaml",
		"generated-fixtures/holdout",
		"Improve only",
		"Treat public benchmark data as samples, not targets",
		"rshell-capability-snapshot",
		"Static rshell capability snapshot unavailable",
		"Production deployments may restrict",
		"\"Changes\" and \"Why\" sections",
		"explain the rationale for each material change",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("researcher prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, forbidden := range []string{
		"smallest useful",
		"one focused",
		"keeping the skill concise",
		"\"Size\" sections",
		"concision",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("researcher prompt should not contain %q:\n%s", forbidden, prompt)
		}
	}
}

func TestFormatIterationReportPromptAllowsReportsAndHidesPrivateDetails(t *testing.T) {
	runDir := filepath.Join("auto-improve-skills", "runs", "train")
	iterDir := filepath.Join(runDir, "iter-002")
	ctx := iterationReportContext{
		Root:                   "/repo",
		SkillPath:              filepath.Join("auto-improve-skills", "skills", "remote-host-diagnostics", "SKILL.md"),
		CasesPath:              filepath.Join("auto-improve-skills", "benchmarks", "remote-host-diagnostics", "cases.yaml"),
		RunDir:                 runDir,
		IterDir:                iterDir,
		Iter:                   2,
		ResultPath:             filepath.Join(iterDir, "result.json"),
		PreviousBestResultPath: filepath.Join(runDir, "iter-001", "result.json"),
		CurrentBestResultPath:  filepath.Join(iterDir, "result.json"),
		PreviousObjective:      0.91,
		CandidateObjective:     0.93,
		PreviousBestQuality:    0.95,
		CandidateQuality:       0.96,
		PreviousQualityFloor:   0.94,
		MinDelta:               0.001,
		QualityOK:              true,
		PublicOK:               true,
		FinalAccepted:          true,
		FinalReason:            iterationDecisionReason(true, true, true, true, true, false),
		PrivateGateConfigured:  true,
		PrivateGateEvaluated:   true,
	}

	prompt := formatIterationReportPrompt(ctx, filepath.Join(iterDir, iterationReportPath))
	for _, want := range []string{
		"report.md",
		"raw outputs",
		"researcher.stderr.txt",
		"researcher.stdout.md",
		"SKILL.candidate.md",
		"SKILL.md.diff",
		"SKILL.previous.md",
		"Do not include the word \"holdout\" in the report body",
		"Private gate configured=true, evaluated=true",
		"Summary",
		"Case-Level Findings",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("iteration report prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestIterationDecisionReasonAvoidsHoldoutSpecifics(t *testing.T) {
	reason := iterationDecisionReason(false, true, true, true, true, false)
	if strings.Contains(strings.ToLower(reason), "holdout") {
		t.Fatalf("decision reason leaked holdout wording: %q", reason)
	}
	if !strings.Contains(reason, "private acceptance gate") {
		t.Fatalf("decision reason = %q, want private gate wording", reason)
	}
}

func TestValidateIterationReportRejectsHoldoutReferences(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, iterationReportPath)
	if err := os.WriteFile(path, []byte("# Report\n\nPrivate gate details omitted.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateIterationReport(path); err != nil {
		t.Fatalf("validateIterationReport() unexpected error: %v", err)
	}

	if err := os.WriteFile(path, []byte("# Report\n\nholdout score leaked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateIterationReport(path); err == nil {
		t.Fatal("validateIterationReport() succeeded for forbidden holdout reference")
	}
}

func TestResearcherCodexArgsUseFastXHighWorkspaceWrite(t *testing.T) {
	args := strings.Join(autoresearch.CodexExecTextArgs("gpt-5.5", autoresearch.CodexWorkspaceWriteSandbox, "researcher.stdout.md"), "\n")
	for _, want := range []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--sandbox\nworkspace-write",
		`service_tier="fast"`,
		`model_reasoning_effort="xhigh"`,
		"--output-last-message\nresearcher.stdout.md",
		"-m\ngpt-5.5",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("Codex args missing %q:\n%s", want, args)
		}
	}
}

func TestFormatCommitSubjectIncludesTrainLoopIterationAndObjectiveTransition(t *testing.T) {
	got := formatCommitSubject(3, 7, 0.8027, 0.84567)
	want := "[update skill] loop 3 - iter 7 - obj 80.27%->84.57%"
	if got != want {
		t.Fatalf("formatCommitSubject() = %q, want %q", got, want)
	}
}

func TestFormatCommitSubjectDefaultsTrainLoopToOne(t *testing.T) {
	got := formatCommitSubject(0, 2, 0.1, 0.2)
	want := "[update skill] loop 1 - iter 2 - obj 10.00%->20.00%"
	if got != want {
		t.Fatalf("formatCommitSubject() = %q, want %q", got, want)
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
		"Researcher summary:",
		"Tightened the workflow",
		"Change summary:",
		"1 file changed, 6 insertions(+), 6 deletions(-)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("commit body missing %q:\n%s", want, body)
		}
	}
	for _, forbidden := range []string{"Per-case scores:", "datadog-agent-config-regression", "auth-bruteforce-summary", "Failed criteria:"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("commit body should not contain %q:\n%s", forbidden, body)
		}
	}
}
