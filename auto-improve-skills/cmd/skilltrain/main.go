// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

const defaultModel = "openai-codex/gpt-5.5"

type logSemantic int

const (
	logSemanticInfo logSemantic = iota
	logSemanticBenchmark
	logSemanticSuccess
	logSemanticWarning
	logSemanticError
	logSemanticSummary
	logSemanticDryRun
)

const (
	ansiReset   = "\x1b[0m"
	ansiBold    = "\x1b[1m"
	ansiDim     = "\x1b[2m"
	ansiRed     = "\x1b[31m"
	ansiGreen   = "\x1b[32m"
	ansiYellow  = "\x1b[33m"
	ansiMagenta = "\x1b[35m"
	ansiCyan    = "\x1b[36m"
)

func main() {
	var (
		iterations              = flag.Int("iters", 3, "maximum improvement iterations")
		casesPath               = flag.String("cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "benchmark suite")
		skillPath               = flag.String("skill", "auto-improve-skills/skills/remote-host-diagnostics/SKILL.md", "skill file to improve")
		model                   = flag.String("model", defaultModel, "pi model for researcher and benchmark agents")
		piBinary                = flag.String("pi", "pi", "pi executable")
		runDir                  = flag.String("run-dir", "", "directory for this training run")
		minDelta                = flag.Float64("min-delta", 0.001, "minimum normalized objective improvement to accept")
		qualityTolerance        = flag.Float64("quality-tolerance", 0.01, "maximum allowed quality drop from the best seen quality")
		holdoutCasesPath        = flag.String("holdout-cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/holdout.yaml", "holdout benchmark suite used as an acceptance gate (empty disables)")
		holdoutQualityTolerance = flag.Float64("holdout-quality-tolerance", -1, "maximum allowed holdout quality drop from the best seen holdout quality; defaults to -quality-tolerance")
		repeats                 = flag.Int("repeats", 3, "benchmark repeats to average for each baseline and candidate")
		limit                   = flag.Int("limit", 0, "run at most N benchmark cases per iteration (0 = all)")
		judge                   = flag.Bool("judge", false, "enable skillbench LLM-as-judge scoring")
		push                    = flag.Bool("push", true, "push accepted skill commits to the current branch; set -push=false to keep commits local")
		dryRun                  = flag.Bool("dry-run", false, "run benchmark and researcher but do not commit/revert")
		allowDirty              = flag.Bool("allow-dirty", false, "allow starting with unrelated uncommitted changes")
	)
	flag.Parse()

	if err := run(*iterations, *casesPath, *skillPath, *model, *piBinary, *runDir, *minDelta, *qualityTolerance, *holdoutCasesPath, *holdoutQualityTolerance, *repeats, *limit, *judge, *push, *dryRun, *allowDirty); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

func logStep(format string, args ...any) {
	logf(os.Stdout, logSemanticInfo, format, args...)
}

func logBenchmark(format string, args ...any) {
	logf(os.Stdout, logSemanticBenchmark, format, args...)
}

func logSuccess(format string, args ...any) {
	logf(os.Stdout, logSemanticSuccess, format, args...)
}

func logWarn(format string, args ...any) {
	logf(os.Stdout, logSemanticWarning, format, args...)
}

func logError(format string, args ...any) {
	logf(os.Stderr, logSemanticError, format, args...)
}

func logDryRun(format string, args ...any) {
	logf(os.Stdout, logSemanticDryRun, format, args...)
}

func logf(stream *os.File, semantic logSemantic, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(stream, formatSkilltrainLog(semantic, msg, colorEnabledForLog(stream)))
}

func printSemantic(semantic logSemantic, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stdout, formatSemanticText(semantic, msg, colorEnabledForLog(os.Stdout)))
}

func formatSkilltrainLog(semantic logSemantic, msg string, colorEnabled bool) string {
	line := "skilltrain: " + msg
	if !colorEnabled {
		return line
	}
	prefix := ansiDim + "skilltrain:" + ansiReset
	return prefix + " " + formatSemanticText(semantic, msg, true)
}

func formatSemanticText(semantic logSemantic, msg string, colorEnabled bool) string {
	if !colorEnabled {
		return msg
	}
	style := logSemanticStyle(semantic)
	if style == "" {
		return msg
	}
	return style + msg + ansiReset
}

func logSemanticStyle(semantic logSemantic) string {
	switch semantic {
	case logSemanticBenchmark:
		return ansiMagenta
	case logSemanticSuccess:
		return ansiGreen
	case logSemanticWarning, logSemanticDryRun:
		return ansiYellow
	case logSemanticError:
		return ansiRed
	case logSemanticSummary:
		return ansiBold + ansiCyan
	default:
		return ansiCyan
	}
}

func colorEnabledForLog(stream *os.File) bool {
	if stream == nil || os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("FORCE_COLOR") != "" || os.Getenv("CLICOLOR_FORCE") != "" {
		return true
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}
	info, err := stream.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func run(iterations int, casesPath, skillPath, model, piBinary, runDir string, minDelta, qualityTolerance float64, holdoutCasesPath string, holdoutQualityTolerance float64, repeats, limit int, judge, push, dryRun, allowDirty bool) error {
	if qualityTolerance < 0 {
		return fmt.Errorf("-quality-tolerance must be non-negative")
	}
	if holdoutQualityTolerance < 0 {
		holdoutQualityTolerance = qualityTolerance
	}
	if repeats <= 0 {
		return fmt.Errorf("-repeats must be positive")
	}
	logStep("resolving repository root and pi binary")
	root, err := autoresearch.RepoRoot()
	if err != nil {
		return err
	}
	resolvedPI, err := autoresearch.ResolvePI(piBinary)
	if err != nil {
		return err
	}
	piBinary = resolvedPI
	logStep("using repo root: %s", root)
	logStep("using pi binary: %s", piBinary)

	casesAbs := autoresearch.AbsFromRoot(root, casesPath)
	skillAbs := autoresearch.AbsFromRoot(root, skillPath)
	holdoutCasesAbs := ""
	if strings.TrimSpace(holdoutCasesPath) != "" {
		holdoutCasesAbs = autoresearch.AbsFromRoot(root, holdoutCasesPath)
	}
	if runDir == "" {
		runDir = filepath.Join(root, "auto-improve-skills", "runs", "train-"+time.Now().UTC().Format("20060102T150405Z"))
	} else {
		runDir = autoresearch.AbsFromRoot(root, runDir)
	}
	logStep("preparing run directory: %s", runDir)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if !allowDirty && !dryRun {
		logStep("checking working tree cleanliness")
		if dirty, status, err := gitDirty(root); err != nil {
			return err
		} else if dirty {
			return fmt.Errorf("working tree is dirty; commit or stash first, or pass -allow-dirty. Status:\n%s", status)
		}
	}

	printSemantic(logSemanticSummary, "skilltrain run dir: %s", runDir)
	logBenchmark("running baseline benchmark")
	baseline, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, filepath.Join(runDir, "iter-000-baseline"), limit, judge, repeats)
	if err != nil {
		return err
	}
	bestObjective := benchmarkObjective(baseline)
	bestQuality := benchmarkQuality(baseline)
	qualityFloor := bestQuality - qualityTolerance
	bestPath := filepath.Join(runDir, "iter-000-baseline", "result.json")
	printSemantic(logSemanticSummary, "baseline quality: %.2f%% objective: %.2f%% (%s)", bestQuality*100, bestObjective*100, bestPath)

	var holdoutBestQuality, holdoutQualityFloor float64
	var holdoutBestPath string
	if holdoutCasesAbs != "" {
		logBenchmark("running holdout baseline benchmark")
		holdoutBaseline, err := runBenchmark(root, holdoutCasesAbs, skillAbs, model, piBinary, filepath.Join(runDir, "iter-000-holdout"), limit, judge, repeats)
		if err != nil {
			return err
		}
		holdoutBestQuality = benchmarkQuality(holdoutBaseline)
		holdoutQualityFloor = holdoutBestQuality - holdoutQualityTolerance
		holdoutBestPath = filepath.Join(runDir, "iter-000-holdout", "result.json")
		printSemantic(logSemanticSummary, "holdout baseline quality: %.2f%% floor: %.2f%% (%s)", holdoutBestQuality*100, holdoutQualityFloor*100, holdoutBestPath)
	}

	for iter := 1; iter <= iterations; iter++ {
		logStep("iteration %d/%d: preparing workspace", iter, iterations)
		iterDir := filepath.Join(runDir, fmt.Sprintf("iter-%03d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return err
		}
		var original []byte
		if dryRun {
			logDryRun("iteration %d/%d: snapshotting skill for dry-run restore", iter, iterations)
			var err error
			original, err = os.ReadFile(skillAbs)
			if err != nil {
				return err
			}
		}
		logStep("iteration %d/%d: invoking researcher to edit skill", iter, iterations)
		if err := improveSkill(root, skillAbs, casesAbs, bestPath, iterDir, model, piBinary, iter, qualityTolerance); err != nil {
			return err
		}
		if dryRun {
			logDryRun("iteration %d/%d: saving candidate skill copy", iter, iterations)
			if candidateSkill, err := os.ReadFile(skillAbs); err == nil {
				_ = os.WriteFile(filepath.Join(iterDir, "candidate.SKILL.md"), candidateSkill, 0o644)
			}
		}
		logBenchmark("iteration %d/%d: running candidate benchmark", iter, iterations)
		candidate, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, iterDir, limit, judge, repeats)
		restoreDryRun := func() error {
			if !dryRun {
				return nil
			}
			logDryRun("iteration %d/%d: restoring original skill after dry-run benchmarks", iter, iterations)
			return os.WriteFile(skillAbs, original, 0o644)
		}
		if err != nil {
			if restoreErr := restoreDryRun(); restoreErr != nil {
				return restoreErr
			}
			return err
		}
		logStep("iteration %d/%d: evaluating candidate", iter, iterations)
		candidatePath := filepath.Join(iterDir, "result.json")
		candidateObjective := benchmarkObjective(candidate)
		candidateQuality := benchmarkQuality(candidate)
		delta := candidateObjective - bestObjective
		qualityOK := candidateQuality >= qualityFloor
		publicOK := qualityOK && delta >= minDelta
		if publicOK {
			printSemantic(logSemanticSuccess, "iteration %d quality: %.2f%% objective: %.2f%% (delta %.2f%%)", iter, candidateQuality*100, candidateObjective*100, delta*100)
		} else {
			printSemantic(logSemanticWarning, "iteration %d quality: %.2f%% objective: %.2f%% (delta %.2f%%)", iter, candidateQuality*100, candidateObjective*100, delta*100)
		}

		holdoutOK := true
		var holdoutGate *benchmarkGate
		if publicOK && holdoutCasesAbs != "" {
			logBenchmark("iteration %d/%d: running holdout gate benchmark", iter, iterations)
			holdoutCandidate, err := runBenchmark(root, holdoutCasesAbs, skillAbs, model, piBinary, filepath.Join(iterDir, "holdout"), limit, judge, repeats)
			if err != nil {
				if restoreErr := restoreDryRun(); restoreErr != nil {
					return restoreErr
				}
				return err
			}
			holdoutPath := filepath.Join(iterDir, "holdout", "result.json")
			holdoutQuality := benchmarkQuality(holdoutCandidate)
			holdoutOK = holdoutQuality >= holdoutQualityFloor
			holdoutGate = &benchmarkGate{Result: holdoutCandidate, ResultPath: holdoutPath, QualityFloor: holdoutQualityFloor}
			if holdoutOK {
				printSemantic(logSemanticSuccess, "iteration %d holdout quality: %.2f%% floor: %.2f%% (%s)", iter, holdoutQuality*100, holdoutQualityFloor*100, holdoutPath)
			} else {
				printSemantic(logSemanticWarning, "iteration %d holdout quality: %.2f%% below floor %.2f%% (%s)", iter, holdoutQuality*100, holdoutQualityFloor*100, holdoutPath)
			}
		}
		if restoreErr := restoreDryRun(); restoreErr != nil {
			return restoreErr
		}

		if publicOK && holdoutOK {
			if dryRun {
				logSuccess("iteration %d/%d: accepted in dry-run", iter, iterations)
				printSemantic(logSemanticDryRun, "dry-run: would accept iteration %d and commit %s (candidate saved in %s)", iter, skillAbs, filepath.Join(iterDir, "candidate.SKILL.md"))
			} else {
				logSuccess("iteration %d/%d: accepted; committing skill change", iter, iterations)
				if err := commitSkill(root, skillAbs, iter, candidate, candidatePath, holdoutGate, filepath.Join(iterDir, "researcher.stdout.md"), delta, push); err != nil {
					return err
				}
			}
			bestObjective = candidateObjective
			if candidateQuality > bestQuality {
				bestQuality = candidateQuality
				qualityFloor = bestQuality - qualityTolerance
			}
			bestPath = candidatePath
			if holdoutGate != nil && benchmarkQuality(holdoutGate.Result) > holdoutBestQuality {
				holdoutBestQuality = benchmarkQuality(holdoutGate.Result)
				holdoutQualityFloor = holdoutBestQuality - holdoutQualityTolerance
				holdoutBestPath = holdoutGate.ResultPath
			}
		} else {
			if !qualityOK {
				printSemantic(logSemanticWarning, "iteration %d rejected: quality %.2f%% is below floor %.2f%%", iter, candidateQuality*100, qualityFloor*100)
			}
			if publicOK && !holdoutOK && holdoutGate != nil {
				printSemantic(logSemanticWarning, "iteration %d rejected: holdout quality %.2f%% is below floor %.2f%%", iter, benchmarkQuality(holdoutGate.Result)*100, holdoutGate.QualityFloor*100)
			}
			if dryRun {
				logWarn("iteration %d/%d: rejected in dry-run", iter, iterations)
				printSemantic(logSemanticDryRun, "dry-run: would reject iteration %d and revert %s (candidate saved in %s)", iter, skillAbs, filepath.Join(iterDir, "candidate.SKILL.md"))
			} else {
				logWarn("iteration %d/%d: rejected; reverting skill change", iter, iterations)
				if err := gitCheckout(root, skillAbs); err != nil {
					return err
				}
			}
		}
	}
	printSemantic(logSemanticSummary, "best objective: %.2f%%; best quality seen: %.2f%% (%s)", bestObjective*100, bestQuality*100, bestPath)
	if holdoutCasesAbs != "" {
		printSemantic(logSemanticSummary, "best holdout quality seen: %.2f%% (floor %.2f%%; %s)", holdoutBestQuality*100, holdoutQualityFloor*100, holdoutBestPath)
	}
	return nil
}

type benchmarkGate struct {
	Result       autoresearch.SuiteResult
	ResultPath   string
	QualityFloor float64
}

func runBenchmark(root, casesAbs, skillAbs, model, piBinary, outDir string, limit int, judge bool, repeats int) (autoresearch.SuiteResult, error) {
	if repeats <= 1 {
		return runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, outDir, limit, judge)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	results := make([]autoresearch.SuiteResult, 0, repeats)
	paths := make([]string, 0, repeats)
	for repeat := 1; repeat <= repeats; repeat++ {
		repeatDir := filepath.Join(outDir, fmt.Sprintf("repeat-%03d", repeat))
		logBenchmark("benchmark: repeat %d/%d", repeat, repeats)
		result, err := runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, repeatDir, limit, judge)
		if err != nil {
			return autoresearch.SuiteResult{}, err
		}
		results = append(results, result)
		paths = append(paths, filepath.Join(repeatDir, "result.json"))
	}
	aggregate, err := aggregateBenchmarkRepeats(results, paths)
	if err != nil {
		return autoresearch.SuiteResult{}, err
	}
	aggregatePath := filepath.Join(outDir, "result.json")
	if err := autoresearch.WriteJSON(aggregatePath, aggregate); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	printSemantic(logSemanticSummary, "benchmark aggregate (%d repeats): quality %.2f%% objective %.2f%% (%s)", repeats, benchmarkQuality(aggregate)*100, benchmarkObjective(aggregate)*100, aggregatePath)
	return aggregate, nil
}

func runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, outDir string, limit int, judge bool) (autoresearch.SuiteResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	logBenchmark("benchmark: writing results under %s", outDir)
	args := []string{
		"run", "./auto-improve-skills/cmd/skillbench",
		"-cases", casesAbs,
		"-skill", filepath.Dir(skillAbs),
		"-model", model,
		"-pi", piBinary,
		"-out", filepath.Join(outDir, "result.json"),
		"-raw-dir", filepath.Join(outDir, "raw"),
	}
	if limit > 0 {
		args = append(args, "-limit", fmt.Sprint(limit))
	}
	if judge {
		args = append(args, "-judge")
	}
	logBenchmark("benchmark: executing skillbench")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	data, err := os.ReadFile(filepath.Join(outDir, "result.json"))
	if err != nil {
		return autoresearch.SuiteResult{}, err
	}
	var result autoresearch.SuiteResult
	if err := json.Unmarshal(data, &result); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	return result, nil
}

func aggregateBenchmarkRepeats(results []autoresearch.SuiteResult, paths []string) (autoresearch.SuiteResult, error) {
	if len(results) == 0 {
		return autoresearch.SuiteResult{}, fmt.Errorf("no benchmark repeats to aggregate")
	}
	aggregate := results[0]
	aggregate.Repeats = len(results)
	aggregate.RepeatResultPaths = append([]string(nil), paths...)
	aggregate.Cases = aggregateCaseRepeats(results)
	aggregate.Score = 0
	aggregate.MaxScore = 0
	aggregate.QualityScore = 0
	aggregate.QualityMaxScore = 0
	aggregate.ObjectiveScore = 0
	aggregate.AverageCaseDurationSeconds = 0
	aggregate.DurationScore = 0

	for _, result := range results {
		aggregate.Score += result.Score
		aggregate.MaxScore += result.MaxScore
		aggregate.QualityScore += result.QualityScore
		aggregate.QualityMaxScore += result.QualityMaxScore
		aggregate.ObjectiveScore += result.ObjectiveScore
		aggregate.AverageCaseDurationSeconds += result.AverageCaseDurationSeconds
		aggregate.DurationScore += result.DurationScore
	}
	count := float64(len(results))
	aggregate.Score /= count
	aggregate.MaxScore /= count
	aggregate.QualityScore /= count
	aggregate.QualityMaxScore /= count
	aggregate.ObjectiveScore /= count
	aggregate.AverageCaseDurationSeconds /= count
	aggregate.DurationScore /= count
	if aggregate.MaxScore > 0 {
		aggregate.NormalizedScore = aggregate.Score / aggregate.MaxScore
	}
	if aggregate.QualityMaxScore > 0 {
		aggregate.QualityNormalizedScore = aggregate.QualityScore / aggregate.QualityMaxScore
	}
	if aggregate.ObjectiveMaxScore > 0 {
		aggregate.ObjectiveNormalizedScore = aggregate.ObjectiveScore / aggregate.ObjectiveMaxScore
	}
	aggregate.StartedAt = results[0].StartedAt
	aggregate.CompletedAt = results[len(results)-1].CompletedAt
	if !aggregate.StartedAt.IsZero() && !aggregate.CompletedAt.IsZero() {
		aggregate.WallClockDuration = aggregate.CompletedAt.Sub(aggregate.StartedAt).String()
	}
	return aggregate, nil
}

func aggregateCaseRepeats(results []autoresearch.SuiteResult) []autoresearch.CaseResult {
	type accum struct {
		caseResult              autoresearch.CaseResult
		count                   int
		score                   float64
		maxScore                float64
		normalizedScore         float64
		deterministicScore      float64
		deterministicMaxScore   float64
		commandCount            int
		toolOutputBytes         int
		failedToolCalls         int
		durationSeconds         float64
		durationScore           float64
		criteriaPointsByName    map[string]float64
		criteriaPassCountByName map[string]int
		criteriaSeenCountByName map[string]int
		criteriaOrder           []string
		criteriaTemplateByName  map[string]autoresearch.CriterionResult
	}
	accums := map[string]*accum{}
	order := make([]string, 0)
	for _, result := range results {
		for _, caseResult := range result.Cases {
			acc := accums[caseResult.ID]
			if acc == nil {
				copyCase := caseResult
				copyCase.FinalAnswer = ""
				copyCase.Commands = nil
				copyCase.ToolCalls = nil
				copyCase.Criteria = nil
				copyCase.Judge = nil
				copyCase.RawJSONLPath = ""
				acc = &accum{
					caseResult:              copyCase,
					criteriaPointsByName:    map[string]float64{},
					criteriaPassCountByName: map[string]int{},
					criteriaSeenCountByName: map[string]int{},
					criteriaTemplateByName:  map[string]autoresearch.CriterionResult{},
				}
				accums[caseResult.ID] = acc
				order = append(order, caseResult.ID)
			}
			acc.count++
			acc.score += caseResult.Score
			acc.maxScore += caseResult.MaxScore
			acc.normalizedScore += caseResult.NormalizedScore
			acc.deterministicScore += caseResult.DeterministicScore
			acc.deterministicMaxScore += caseResult.DeterministicMaxScore
			acc.commandCount += caseResult.CommandCount
			acc.toolOutputBytes += caseResult.ToolOutputBytes
			acc.failedToolCalls += caseResult.FailedToolCalls
			acc.durationSeconds += caseResult.DurationSeconds
			acc.durationScore += caseResult.DurationScore
			if caseResult.Error != "" {
				acc.caseResult.Error = appendErr(acc.caseResult.Error, caseResult.Error)
			}
			for _, criterion := range caseResult.Criteria {
				if _, ok := acc.criteriaTemplateByName[criterion.Name]; !ok {
					acc.criteriaTemplateByName[criterion.Name] = criterion
					acc.criteriaOrder = append(acc.criteriaOrder, criterion.Name)
				}
				acc.criteriaPointsByName[criterion.Name] += criterion.Points
				acc.criteriaSeenCountByName[criterion.Name]++
				if criterion.Passed {
					acc.criteriaPassCountByName[criterion.Name]++
				}
			}
		}
	}

	cases := make([]autoresearch.CaseResult, 0, len(order))
	for _, id := range order {
		acc := accums[id]
		count := float64(acc.count)
		caseResult := acc.caseResult
		caseResult.Score = acc.score / count
		caseResult.MaxScore = acc.maxScore / count
		caseResult.NormalizedScore = acc.normalizedScore / count
		caseResult.DeterministicScore = acc.deterministicScore / count
		caseResult.DeterministicMaxScore = acc.deterministicMaxScore / count
		caseResult.CommandCount = roundedAverage(acc.commandCount, acc.count)
		caseResult.ToolOutputBytes = roundedAverage(acc.toolOutputBytes, acc.count)
		caseResult.FailedToolCalls = roundedAverage(acc.failedToolCalls, acc.count)
		caseResult.DurationSeconds = acc.durationSeconds / count
		caseResult.DurationScore = acc.durationScore / count
		caseResult.WallClockDuration = fmt.Sprintf("average of %d repeats", acc.count)
		caseResult.Criteria = aggregateCriteria(acc.criteriaOrder, acc.criteriaTemplateByName, acc.criteriaPointsByName, acc.criteriaPassCountByName, acc.criteriaSeenCountByName)
		cases = append(cases, caseResult)
	}
	return cases
}

func aggregateCriteria(order []string, templates map[string]autoresearch.CriterionResult, points map[string]float64, passCounts, seenCounts map[string]int) []autoresearch.CriterionResult {
	criteria := make([]autoresearch.CriterionResult, 0, len(order))
	for _, name := range order {
		template := templates[name]
		seen := seenCounts[name]
		if seen == 0 {
			continue
		}
		template.Points = points[name] / float64(seen)
		template.Passed = passCounts[name] == seen
		template.Detail = fmt.Sprintf("passed in %d/%d repeats", passCounts[name], seen)
		criteria = append(criteria, template)
	}
	return criteria
}

func roundedAverage(sum, count int) int {
	if count <= 0 {
		return 0
	}
	return (sum + count/2) / count
}

func improveSkill(root, skillAbs, casesAbs, bestResultPath, iterDir, model, piBinary string, iter int, qualityTolerance float64) error {
	prompt := fmt.Sprintf(`You are an autoresearch-style skill improvement agent.

Read auto-improve-skills/program.md, the current skill at %s, the benchmark suite at %s, and the best benchmark result at %s.

Task for iteration %d:
- Improve only %s.
- Optimize final answer quality first. The trainer allows at most a %.1f percentage point quality drop from the best seen quality.
- Also improve the simple composite objective by reducing end-to-end investigation time and keeping the skill concise.
- Keep the skill safe and local: it must use ./rshell through bash and must not use Datadog remote-action tools.
- Do not edit benchmark cases, fake logs, Go tooling, or reports.
- Prefer short, general diagnostic workflow instructions over long case-specific rules or overfitting exact answers.
- After editing, briefly summarize what you changed and whether the skill became shorter.
`, skillAbs, casesAbs, bestResultPath, iter, skillAbs, qualityTolerance*100)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := []string{
		"--print",
		"--no-session",
		"--no-extensions",
		"--no-prompt-templates",
		"--no-skills",
		"--tools", "read,bash,edit,write",
		"--model", model,
		prompt,
	}
	cmd := exec.CommandContext(ctx, piBinary, args...)
	cmd.Dir = root
	cmd.Env = autoresearch.EnvWithExecutableDir(piBinary)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	logStep("iteration %d: running researcher pi; transcript will be saved under %s", iter, iterDir)
	err := cmd.Run()
	_ = os.WriteFile(filepath.Join(iterDir, "researcher.stdout.md"), stdout.Bytes(), 0o644)
	if stderr.Len() > 0 {
		_ = os.WriteFile(filepath.Join(iterDir, "researcher.stderr.txt"), stderr.Bytes(), 0o644)
	}
	if err != nil {
		return fmt.Errorf("researcher pi failed: %w", err)
	}
	return nil
}

func commitSkill(root, skillAbs string, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummaryPath string, delta float64, push bool) error {
	skillRel := gitPath(root, skillAbs)
	logStep("iteration %d: staging %s", iter, skillRel)
	if err := runGit(root, "add", skillRel); err != nil {
		return err
	}
	if clean, _, err := gitDiffCachedPathClean(root, skillRel); err != nil {
		return err
	} else if clean {
		printSemantic(logSemanticWarning, "accepted iteration had no staged diff; skipping commit")
		return nil
	}
	diffStat, err := gitOutput(root, "diff", "--cached", "--stat", "--", skillRel)
	if err != nil {
		return err
	}
	shortStat, err := gitOutput(root, "diff", "--cached", "--shortstat", "--", skillRel)
	if err != nil {
		return err
	}
	researcherSummary := readCommitSummary(researcherSummaryPath)
	msg := fmt.Sprintf("auto-improve remote-host-diagnostics iter %d", iter)
	body := formatCommitBody(root, skillRel, iter, result, resultPath, holdoutGate, researcherSummary, delta, diffStat, shortStat)
	logSuccess("iteration %d: creating git commit", iter)
	if err := runGit(root, "commit", "-m", msg, "-m", body, "--", skillRel); err != nil {
		return err
	}
	if !push {
		printSemantic(logSemanticSuccess, "accepted iteration committed locally because -push=false; run git push manually to publish it")
		return nil
	}
	logSuccess("iteration %d: pushing accepted commit", iter)
	return runGit(root, "push")
}

func formatCommitBody(root, skillRel string, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummary string, delta float64, diffStat, shortStat string) string {
	qualityScore, qualityMax, qualityPct := result.QualityScore, result.QualityMaxScore, benchmarkQuality(result)*100
	if qualityMax == 0 {
		qualityScore, qualityMax, qualityPct = result.Score, result.MaxScore, result.NormalizedScore*100
	}
	objectiveScore, objectiveMax, objectivePct := result.ObjectiveScore, result.ObjectiveMaxScore, benchmarkObjective(result)*100
	if objectiveMax == 0 {
		objectiveScore, objectiveMax = objectivePct, 100
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Training iteration: %d\n", iter)
	fmt.Fprintf(&b, "Changed file: %s\n", skillRel)
	if resultPath != "" {
		fmt.Fprintf(&b, "Benchmark report: %s\n", gitPath(root, resultPath))
	}
	if result.SuiteName != "" {
		fmt.Fprintf(&b, "Benchmark suite: %s\n", result.SuiteName)
	}
	if result.Model != "" {
		fmt.Fprintf(&b, "Model: %s\n", result.Model)
	}

	fmt.Fprintf(&b, "\nScore summary:\n")
	fmt.Fprintf(&b, "- Quality: %.2f/%.2f (%.2f%%)\n", qualityScore, qualityMax, qualityPct)
	fmt.Fprintf(&b, "- Objective: %.2f/%.2f (%.2f%%, delta %+0.2f pp)\n", objectiveScore, objectiveMax, objectivePct, delta*100)
	fmt.Fprintf(&b, "- Average case duration: %.1fs (score %.2f%%)\n", result.AverageCaseDurationSeconds, result.DurationScore*100)
	fmt.Fprintf(&b, "- Skill size: %d estimated tokens, %d bytes (score %.2f%%)\n", result.SkillSizeEstimatedTokens, result.SkillSizeBytes, result.SkillSizeScore*100)
	if result.Repeats > 1 {
		fmt.Fprintf(&b, "- Repeats averaged: %d\n", result.Repeats)
	}
	cfg := result.ObjectiveConfig
	if cfg.QualityWeight+cfg.DurationWeight+cfg.SkillSizeWeight > 0 {
		fmt.Fprintf(&b, "- Objective config: quality=%.2f duration=%.2f skill_size=%.2f; duration budget/hard=%.0fs/%.0fs; skill-size target/hard=%d/%d tokens\n",
			cfg.QualityWeight, cfg.DurationWeight, cfg.SkillSizeWeight, cfg.DurationBudgetSeconds, cfg.DurationHardLimitSeconds, cfg.SkillSizeTargetTokens, cfg.SkillSizeHardLimitTokens)
	}

	if holdoutGate != nil {
		holdoutQualityScore, holdoutQualityMax, holdoutQualityPct := holdoutGate.Result.QualityScore, holdoutGate.Result.QualityMaxScore, benchmarkQuality(holdoutGate.Result)*100
		if holdoutQualityMax == 0 {
			holdoutQualityScore, holdoutQualityMax, holdoutQualityPct = holdoutGate.Result.Score, holdoutGate.Result.MaxScore, holdoutGate.Result.NormalizedScore*100
		}
		fmt.Fprintf(&b, "\nHoldout gate:\n")
		fmt.Fprintf(&b, "- Report: %s\n", gitPath(root, holdoutGate.ResultPath))
		fmt.Fprintf(&b, "- Quality: %.2f/%.2f (%.2f%%; floor %.2f%%)\n", holdoutQualityScore, holdoutQualityMax, holdoutQualityPct, holdoutGate.QualityFloor*100)
		fmt.Fprintf(&b, "- Objective: %.2f%%\n", benchmarkObjective(holdoutGate.Result)*100)
		if holdoutGate.Result.Repeats > 1 {
			fmt.Fprintf(&b, "- Repeats averaged: %d\n", holdoutGate.Result.Repeats)
		}
	}

	fmt.Fprintf(&b, "\nPer-case scores:\n")
	if len(result.Cases) == 0 {
		fmt.Fprintf(&b, "- none recorded\n")
	}
	for _, cr := range result.Cases {
		fmt.Fprintf(&b, "- %s: %.1f/%.1f (%.1f%%), duration %.1fs, commands %d, failed tool calls %d",
			cr.ID, cr.Score, cr.MaxScore, cr.NormalizedScore*100, cr.DurationSeconds, cr.CommandCount, cr.FailedToolCalls)
		if cr.Judge != nil {
			fmt.Fprintf(&b, ", judge %.1f", cr.Judge.Score)
		}
		if cr.Error != "" {
			fmt.Fprintf(&b, ", error: %s", truncateOneLine(cr.Error, 160))
		}
		b.WriteByte('\n')
		if criteria := criteriaSummary(cr); criteria != "" {
			fmt.Fprintf(&b, "%s\n", indentLines(criteria, "  "))
		}
	}

	if strings.TrimSpace(researcherSummary) != "" {
		fmt.Fprintf(&b, "\nResearcher summary:\n%s\n", indentLines(truncateText(strings.TrimSpace(researcherSummary), 2000), "  "))
	}

	fmt.Fprintf(&b, "\nChange summary:\n")
	if strings.TrimSpace(diffStat) == "" {
		fmt.Fprintf(&b, "- no diff stat captured\n")
	} else {
		fmt.Fprint(&b, strings.TrimRight(diffStat, "\n"), "\n")
	}
	if strings.TrimSpace(shortStat) != "" && !strings.Contains(diffStat, strings.TrimSpace(shortStat)) {
		fmt.Fprint(&b, strings.TrimRight(shortStat, "\n"), "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func criteriaSummary(cr autoresearch.CaseResult) string {
	if len(cr.Criteria) == 0 {
		return ""
	}
	failed := make([]string, 0)
	for _, criterion := range cr.Criteria {
		if criterion.Passed {
			continue
		}
		detail := criterion.Name
		if criterion.Detail != "" {
			detail += " (" + criterion.Detail + ")"
		}
		failed = append(failed, fmt.Sprintf("%s: %.1f/%.1f", detail, criterion.Points, criterion.Max))
	}
	if len(failed) == 0 {
		return "Criteria: all deterministic checks passed"
	}
	const maxFailedCriteria = 5
	if len(failed) > maxFailedCriteria {
		failed = append(failed[:maxFailedCriteria], fmt.Sprintf("... and %d more failed criteria", len(failed)-maxFailedCriteria))
	}
	return "Failed criteria:\n- " + strings.Join(failed, "\n- ")
}

func readCommitSummary(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func gitPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		return filepath.ToSlash(rel)
	}
	return path
}

func gitDiffCachedPathClean(root, path string) (bool, string, error) {
	out, err := gitOutput(root, "diff", "--cached", "--name-only", "--", path)
	if err != nil {
		return false, "", err
	}
	return len(bytes.TrimSpace([]byte(out))) == 0, out, nil
}

func gitOutput(root string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func appendErr(existing, msg string) string {
	if strings.TrimSpace(msg) == "" {
		return existing
	}
	if strings.TrimSpace(existing) == "" {
		return msg
	}
	return existing + "; " + msg
}

func indentLines(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func truncateOneLine(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	return truncateText(s, limit)
}

func truncateText(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= len("...") {
		return s[:limit]
	}
	return s[:limit-len("...")] + "..."
}

func benchmarkQuality(result autoresearch.SuiteResult) float64 {
	if result.QualityMaxScore > 0 {
		return result.QualityNormalizedScore
	}
	return result.NormalizedScore
}

func benchmarkObjective(result autoresearch.SuiteResult) float64 {
	if result.ObjectiveMaxScore > 0 {
		return result.ObjectiveNormalizedScore
	}
	return result.NormalizedScore
}

func gitDirty(root string) (bool, string, error) {
	cmd := exec.Command("git", "status", "--short")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false, "", err
	}
	return len(bytes.TrimSpace(out)) > 0, string(out), nil
}

func gitDiffCachedClean(root string) (bool, string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return false, "", err
	}
	return len(bytes.TrimSpace(out)) == 0, string(out), nil
}

func gitCheckout(root, path string) error {
	return runGit(root, "checkout", "--", path)
}

func runGit(root string, args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
