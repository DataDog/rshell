// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

const (
	defaultModel                 = "openai-codex/gpt-5.5"
	defaultLoopCount             = 1
	defaultParallelRepeats       = 3
	defaultParallelCases         = 3
	defaultStructuralInterval    = 3
	defaultRewriteInterval       = 5
	defaultExplorationCandidates = 3
)

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
	skilltrainLogPrefix                   = "skilltrain"
	skilltrainLogSeparator                = " | "
	commandOutputLimit                    = 64 * 1024
	rshellCapabilitySnapshotMaxBytes      = 12 * 1024
	researcherProgramPath                 = "program.md"
	researcherSanitizedFeedbackPath       = "sanitized-feedback.md"
	researcherSanitizedFeedbackSourcePath = "sanitized-feedback.source.json"
	researcherTools                       = "edit,write"
	sanitizedFeedbackMaxChars             = 2400
	sanitizedFeedbackLLMTimeout           = 5 * time.Minute
	iterationSkillSnapshotPath            = "SKILL.candidate.md"
	iterationPreviousSkillPath            = "SKILL.previous.md"
	iterationSkillDiffPath                = "SKILL.diff"

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
	var cfg trainConfig
	loopCount := flag.Int("loop-count", defaultLoopCount, "number of full training runs to execute; repeats all other supplied flags")
	flag.IntVar(&cfg.iterations, "iters", 3, "maximum improvement iterations")
	flag.IntVar(&cfg.structuralInterval, "structural-interval", defaultStructuralInterval, "allow larger structural skill mutations every N iterations (0 disables)")
	flag.IntVar(&cfg.rewriteInterval, "rewrite-interval", defaultRewriteInterval, "generate full-rewrite exploration candidates every N iterations (0 disables)")
	flag.IntVar(&cfg.explorationCandidates, "exploration-candidates", defaultExplorationCandidates, "number of candidates to generate and benchmark on structural/rewrite iterations")
	flag.StringVar(&cfg.casesPath, "cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "benchmark suite")
	flag.StringVar(&cfg.skillPath, "skill", "auto-improve-skills/skills/remote-host-diagnostics/SKILL.md", "skill file to improve")
	flag.StringVar(&cfg.model, "model", defaultModel, "pi model for researcher and benchmark agents")
	flag.StringVar(&cfg.feedbackModel, "feedback-model", "", "pi model for sanitized feedback generator (defaults to -model)")
	flag.StringVar(&cfg.piBinary, "pi", "pi", "pi executable")
	flag.StringVar(&cfg.runDir, "run-dir", "", "directory for this training run")
	flag.Float64Var(&cfg.minDelta, "min-delta", 0.001, "minimum normalized objective improvement to accept")
	flag.Float64Var(&cfg.qualityTolerance, "quality-tolerance", 0.01, "maximum allowed quality drop from the best seen quality")
	flag.StringVar(&cfg.holdoutCasesPath, "holdout-cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/holdout.yaml", "holdout benchmark suite used as an acceptance gate (empty disables)")
	flag.Float64Var(&cfg.holdoutQualityTolerance, "holdout-quality-tolerance", -1, "maximum allowed holdout quality drop from the best seen holdout quality; defaults to -quality-tolerance")
	flag.IntVar(&cfg.repeats, "repeats", 3, "benchmark repeats to average for each baseline and candidate")
	flag.IntVar(&cfg.parallelRepeats, "parallel-repeats", defaultParallelRepeats, "maximum benchmark repeats to run concurrently (0 = all repeats, 1 = serial)")
	flag.IntVar(&cfg.parallelCases, "parallel-cases", defaultParallelCases, "maximum cases per skillbench run to execute concurrently (0 = all selected cases, 1 = serial)")
	flag.BoolVar(&cfg.parallelSuites, "parallel-suites", true, "run independent public and holdout suites concurrently when possible")
	flag.IntVar(&cfg.limit, "limit", 0, "run at most N benchmark cases per iteration (0 = all)")
	flag.BoolVar(&cfg.judge, "judge", false, "enable skillbench LLM-as-judge scoring")
	flag.BoolVar(&cfg.push, "push", true, "push accepted skill commits to the current branch; set -push=false to keep commits local")
	flag.BoolVar(&cfg.dryRun, "dry-run", false, "run benchmark and researcher but do not commit/revert")
	flag.BoolVar(&cfg.allowDirty, "allow-dirty", false, "allow starting with unrelated uncommitted changes")
	flag.BoolVar(&cfg.feedbackLLM, "feedback-llm", true, "generate freeform sanitized researcher feedback with an LLM (default true; false writes no feedback)")
	flag.StringVar(&cfg.regenerateFeedbackRunDir, "regenerate-feedback", "", "regenerate sanitized-feedback artifacts under an existing skilltrain run directory or parent runs directory, then exit")
	flag.BoolVar(&cfg.verbose, "verbose", false, "show detailed per-step logs and stream nested skillbench output")
	flag.Parse()
	if strings.TrimSpace(cfg.feedbackModel) == "" {
		cfg.feedbackModel = cfg.model
	}

	setSkilltrainVerbose(cfg.verbose)
	if strings.TrimSpace(cfg.regenerateFeedbackRunDir) != "" {
		count, err := regenerateSanitizedFeedbackArtifacts(cfg.regenerateFeedbackRunDir, cfg.piBinary, cfg.feedbackModel, cfg.feedbackLLM)
		if err != nil {
			logError("%v", err)
			os.Exit(1)
		}
		logSuccess("regenerated sanitized feedback for %d iteration(s)", count)
		return
	}
	if err := runLoop(*loopCount, cfg); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

type trainConfig struct {
	iterations               int
	structuralInterval       int
	rewriteInterval          int
	explorationCandidates    int
	casesPath                string
	skillPath                string
	model                    string
	feedbackModel            string
	piBinary                 string
	runDir                   string
	minDelta                 float64
	qualityTolerance         float64
	holdoutCasesPath         string
	holdoutQualityTolerance  float64
	repeats                  int
	parallelRepeats          int
	parallelCases            int
	limit                    int
	judge                    bool
	parallelSuites           bool
	push                     bool
	dryRun                   bool
	allowDirty               bool
	feedbackLLM              bool
	regenerateFeedbackRunDir string
	verbose                  bool
	trainLoop                int
}

type logContext struct {
	Suite  string
	Repeat string
	Case   string
}

var (
	skilltrainLogMu      sync.Mutex
	skilltrainLogVerbose bool
)

func defaultLogContext() logContext {
	return logContext{Suite: "t", Repeat: "-", Case: "-"}
}

func suiteLogContext(suite string) logContext {
	return logContext{Suite: suite, Repeat: "-", Case: "-"}
}

func repeatLogContext(suite string, repeat, repeats int) logContext {
	ctx := suiteLogContext(suite)
	if repeat > 0 && repeats > 1 {
		ctx.Repeat = fmt.Sprintf("%d/%d", repeat, repeats)
	}
	return ctx
}

func setSkilltrainVerbose(verbose bool) {
	skilltrainLogVerbose = verbose
}

func logStep(format string, args ...any) {
	logf(os.Stdout, logSemanticInfo, defaultLogContext(), format, args...)
}

func logVerbose(format string, args ...any) {
	if !skilltrainLogVerbose {
		return
	}
	logf(os.Stdout, logSemanticInfo, defaultLogContext(), format, args...)
}

func logBenchmark(format string, args ...any) {
	logf(os.Stdout, logSemanticBenchmark, defaultLogContext(), format, args...)
}

func logBenchmarkVerbose(format string, args ...any) {
	if !skilltrainLogVerbose {
		return
	}
	logf(os.Stdout, logSemanticBenchmark, defaultLogContext(), format, args...)
}

func logBenchmarkCtx(ctx logContext, format string, args ...any) {
	logf(os.Stdout, logSemanticBenchmark, ctx, format, args...)
}

func logBenchmarkVerboseCtx(ctx logContext, format string, args ...any) {
	if !skilltrainLogVerbose {
		return
	}
	logf(os.Stdout, logSemanticBenchmark, ctx, format, args...)
}

func logSuccess(format string, args ...any) {
	logf(os.Stdout, logSemanticSuccess, defaultLogContext(), format, args...)
}

func logWarn(format string, args ...any) {
	logf(os.Stdout, logSemanticWarning, defaultLogContext(), format, args...)
}

func logError(format string, args ...any) {
	logf(os.Stderr, logSemanticError, defaultLogContext(), format, args...)
}

func logDryRun(format string, args ...any) {
	logf(os.Stdout, logSemanticDryRun, defaultLogContext(), format, args...)
}

func logDryRunVerbose(format string, args ...any) {
	if !skilltrainLogVerbose {
		return
	}
	logf(os.Stdout, logSemanticDryRun, defaultLogContext(), format, args...)
}

func logf(stream *os.File, semantic logSemantic, ctx logContext, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	skilltrainLogMu.Lock()
	defer skilltrainLogMu.Unlock()
	fmt.Fprintln(stream, formatSkilltrainLog(semantic, ctx, msg, colorEnabledForLog(stream)))
}

func printSemantic(semantic logSemantic, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	skilltrainLogMu.Lock()
	defer skilltrainLogMu.Unlock()
	fmt.Fprintln(os.Stdout, formatSkilltrainLog(semantic, defaultLogContext(), msg, colorEnabledForLog(os.Stdout)))
}

func formatSkilltrainLog(semantic logSemantic, ctx logContext, msg string, colorEnabled bool) string {
	text := msg
	if contextPrefix := formatLogContext(ctx); contextPrefix != "" {
		text = contextPrefix + " " + msg
	}
	if label := logSemanticLabel(semantic); label != "" {
		text = fmt.Sprintf("%-5s %s", label, text)
	}
	line := skilltrainLogPrefix + skilltrainLogSeparator + text
	if !colorEnabled {
		return line
	}
	prefix := ansiDim + skilltrainLogPrefix + ansiReset
	return prefix + skilltrainLogSeparator + formatSemanticText(semantic, text, true)
}

func logSemanticLabel(semantic logSemantic) string {
	switch semantic {
	case logSemanticBenchmark:
		return "bench"
	case logSemanticSuccess:
		return "ok"
	case logSemanticWarning:
		return "warn"
	case logSemanticError:
		return "err"
	case logSemanticSummary:
		return "run"
	case logSemanticDryRun:
		return "dry"
	default:
		return "step"
	}
}

func formatLogContext(ctx logContext) string {
	suite := logContextValue(ctx.Suite)
	repeat := logContextValue(ctx.Repeat)
	caseName := logContextValue(ctx.Case)
	if (suite == "" || suite == "-" || suite == "t") && (repeat == "" || repeat == "-") && (caseName == "" || caseName == "-") {
		return ""
	}
	parts := make([]string, 0, 3)
	if suite != "" && suite != "-" && (suite != "t" || repeat != "-" || caseName != "-") {
		parts = append(parts, suite)
	}
	if repeat != "" && repeat != "-" {
		parts = append(parts, repeat)
	}
	if caseName != "" && caseName != "-" {
		parts = append(parts, caseName)
	}
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func logContextValue(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	s = strings.ReplaceAll(s, "|", "_")
	s = strings.ReplaceAll(s, "\n", "_")
	s = strings.ReplaceAll(s, "\r", "_")
	return s
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

func displayPath(root, path string) string {
	if path == "" {
		return ""
	}
	cleaned := filepath.Clean(path)
	if cwd, err := os.Getwd(); err == nil {
		if rel, ok := relativeDisplayPath(cwd, cleaned); ok {
			return rel
		}
	}
	if rel, ok := relativeDisplayPath(root, cleaned); ok {
		return rel
	}
	if rel, ok := homeDisplayPath(cleaned); ok {
		return rel
	}
	return cleaned
}

func displayRunPath(runDir, path string) string {
	if rel, ok := relativeDisplayPath(runDir, path); ok {
		return rel
	}
	return displayPath("", path)
}

func relativeDisplayPath(base, path string) (string, bool) {
	if base == "" || path == "" {
		return "", false
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return "", false
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(absBase, absPath)
	if err != nil {
		return "", false
	}
	if rel == "." {
		return rel, true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

func homeDisplayPath(path string) (string, bool) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", false
	}
	rel, ok := relativeDisplayPath(home, path)
	if !ok {
		return "", false
	}
	if rel == "." {
		return "~", true
	}
	return filepath.Join("~", rel), true
}

type commandCapture struct {
	stdout *limitedBuffer
	stderr *limitedBuffer
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated int
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) <= remaining {
			_, _ = b.buf.Write(p)
		} else {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated += len(p) - remaining
		}
	} else {
		b.truncated += len(p)
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	s := b.buf.String()
	if b.truncated > 0 {
		if s != "" && !strings.HasSuffix(s, "\n") {
			s += "\n"
		}
		s += fmt.Sprintf("... truncated %d bytes", b.truncated)
	}
	return s
}

func commandWriters(verbose bool) (io.Writer, io.Writer, *commandCapture) {
	if verbose {
		return os.Stdout, os.Stderr, nil
	}
	capture := &commandCapture{
		stdout: newLimitedBuffer(commandOutputLimit),
		stderr: newLimitedBuffer(commandOutputLimit),
	}
	return capture.stdout, capture.stderr, capture
}

func commandError(action string, err error, capture *commandCapture) error {
	summary := commandOutputSummary(capture)
	if summary == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w\n%s", action, err, summary)
}

func commandOutputSummary(capture *commandCapture) string {
	if capture == nil {
		return ""
	}
	parts := make([]string, 0, 2)
	if stdout := strings.TrimSpace(capture.stdout.String()); stdout != "" {
		parts = append(parts, "stdout:\n"+stdout)
	}
	if stderr := strings.TrimSpace(capture.stderr.String()); stderr != "" {
		parts = append(parts, "stderr:\n"+stderr)
	}
	return strings.Join(parts, "\n")
}

type trainRunner func(trainConfig) error

func runLoop(loopCount int, cfg trainConfig) error {
	return runLoopWithRunner(loopCount, cfg, runConfig)
}

func runLoopWithRunner(loopCount int, cfg trainConfig, runner trainRunner) error {
	if loopCount <= 0 {
		return fmt.Errorf("-loop-count must be positive")
	}
	if runner == nil {
		return fmt.Errorf("internal error: training runner is nil")
	}
	if loopCount == 1 {
		loopCfg := cfg
		loopCfg.trainLoop = 1
		return runner(loopCfg)
	}
	for loop := 1; loop <= loopCount; loop++ {
		loopCfg := cfg
		loopCfg.trainLoop = loop
		loopCfg.runDir = loopRunDir(cfg.runDir, loop)
		printSemantic(logSemanticSummary, "loop %d/%d start", loop, loopCount)
		if err := runner(loopCfg); err != nil {
			return fmt.Errorf("loop %d/%d: %w", loop, loopCount, err)
		}
	}
	return nil
}

func loopRunDir(runDir string, loop int) string {
	if strings.TrimSpace(runDir) == "" {
		return runDir
	}
	return filepath.Join(runDir, fmt.Sprintf("loop-%03d", loop))
}

func runConfig(cfg trainConfig) error {
	setSkilltrainVerbose(cfg.verbose)
	if strings.TrimSpace(cfg.feedbackModel) == "" {
		cfg.feedbackModel = cfg.model
	}
	return run(cfg.trainLoop, cfg.iterations, cfg.structuralInterval, cfg.rewriteInterval, cfg.explorationCandidates, cfg.casesPath, cfg.skillPath, cfg.model, cfg.feedbackModel, cfg.piBinary, cfg.runDir, cfg.minDelta, cfg.qualityTolerance, cfg.holdoutCasesPath, cfg.holdoutQualityTolerance, cfg.repeats, cfg.parallelRepeats, cfg.parallelCases, cfg.limit, cfg.judge, cfg.parallelSuites, cfg.push, cfg.dryRun, cfg.allowDirty, cfg.feedbackLLM)
}

func run(trainLoop, iterations, structuralInterval, rewriteInterval, explorationCandidates int, casesPath, skillPath, model, feedbackModel, piBinary, runDir string, minDelta, qualityTolerance float64, holdoutCasesPath string, holdoutQualityTolerance float64, repeats, parallelRepeats, parallelCases, limit int, judge, parallelSuites, push, dryRun, allowDirty, feedbackLLM bool) error {
	if strings.TrimSpace(feedbackModel) == "" {
		feedbackModel = model
	}
	if qualityTolerance < 0 {
		return fmt.Errorf("-quality-tolerance must be non-negative")
	}
	if holdoutQualityTolerance < 0 {
		holdoutQualityTolerance = qualityTolerance
	}
	if structuralInterval < 0 {
		return fmt.Errorf("-structural-interval must be non-negative")
	}
	if rewriteInterval < 0 {
		return fmt.Errorf("-rewrite-interval must be non-negative")
	}
	if explorationCandidates <= 0 {
		return fmt.Errorf("-exploration-candidates must be positive")
	}
	if repeats <= 0 {
		return fmt.Errorf("-repeats must be positive")
	}
	if parallelRepeats < 0 {
		return fmt.Errorf("-parallel-repeats must be non-negative")
	}
	if parallelCases < 0 {
		return fmt.Errorf("-parallel-cases must be non-negative")
	}
	logVerbose("setup resolve repo/pi")
	root, err := autoresearch.RepoRoot()
	if err != nil {
		return err
	}
	resolvedPI, err := autoresearch.ResolvePI(piBinary)
	if err != nil {
		return err
	}
	piBinary = resolvedPI
	logVerbose("setup repo=%s", root)
	logVerbose("setup pi=%s", piBinary)

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
	logStep("run-dir %s", displayPath(root, runDir))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if !allowDirty && !dryRun {
		logVerbose("setup check clean tree")
		if dirty, status, err := gitDirty(root); err != nil {
			return err
		} else if dirty {
			return fmt.Errorf("working tree is dirty; commit or stash first, or pass -allow-dirty. Status:\n%s", status)
		}
	}
	logVerbose("setup benchmark prerequisites")
	if err := prepareBenchmarkPrerequisites(root, casesAbs, holdoutCasesAbs); err != nil {
		return err
	}

	if err := saveBaselineSkillArtifacts(skillAbs, filepath.Join(runDir, "iter-000-baseline"), holdoutCasesAbs != "", filepath.Join(runDir, "iter-000-holdout")); err != nil {
		return err
	}

	logBenchmark("baseline reps=%d reps-par=%d cases-par=%d", repeats, repeatParallelism(parallelRepeats, repeats), parallelCases)
	baseline, holdoutBaseline, haveHoldoutBaseline, err := runBaselineBenchmarks(root, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, runDir, limit, judge, repeats, parallelRepeats, parallelCases, parallelSuites)
	if err != nil {
		return err
	}
	bestObjective := benchmarkObjective(baseline)
	bestQuality := benchmarkQuality(baseline)
	qualityFloor := bestQuality - qualityTolerance
	bestPath := filepath.Join(runDir, "iter-000-baseline", "result.json")

	var holdoutBestQuality, holdoutQualityFloor float64
	var holdoutBestPath string
	acceptedIterations := 0
	rejectedIterations := 0
	if haveHoldoutBaseline {
		holdoutBestQuality = benchmarkQuality(holdoutBaseline)
		holdoutQualityFloor = holdoutBestQuality - holdoutQualityTolerance
		holdoutBestPath = filepath.Join(runDir, "iter-000-holdout", "result.json")
		printSemantic(logSemanticSummary, "baseline q=%.2f%% obj=%.2f%%; holdout q=%.2f%% floor=%.2f%%", bestQuality*100, bestObjective*100, holdoutBestQuality*100, holdoutQualityFloor*100)
	} else {
		printSemantic(logSemanticSummary, "baseline q=%.2f%% obj=%.2f%%", bestQuality*100, bestObjective*100)
	}

	feedbackSource := baseline
	for iter := 1; iter <= iterations; iter++ {
		logVerbose("iter %d/%d prepare workspace", iter, iterations)
		iterDir := filepath.Join(runDir, fmt.Sprintf("iter-%03d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return err
		}
		logVerbose("iter %d/%d snapshot skill", iter, iterations)
		previousSkill, err := os.ReadFile(skillAbs)
		if err != nil {
			return err
		}
		feedbackData := buildSanitizedFeedbackSource(feedbackSource)
		if feedbackLLM {
			if err := populateSanitizedFeedbackWithLLM(piBinary, feedbackModel, &feedbackData); err != nil {
				logWarn("iter %d/%d feedback llm failed; no sanitized feedback generated: %v", iter, iterations, err)
			}
		}
		if err := autoresearch.WriteJSON(filepath.Join(iterDir, researcherSanitizedFeedbackSourcePath), feedbackData); err != nil {
			return err
		}
		feedback := formatSanitizedResearcherFeedbackFromSource(feedbackData)
		if err := os.WriteFile(filepath.Join(iterDir, researcherSanitizedFeedbackPath), []byte(feedback), 0o644); err != nil {
			return err
		}
		plan := planIterationResearch(iter, structuralInterval, rewriteInterval, explorationCandidates)
		if plan.CandidateCount <= 1 {
			transcriptPath := filepath.Join(iterDir, "researcher.stdout.md")
			logStep("iter %d/%d edit -> %s", iter, iterations, displayRunPath(runDir, transcriptPath))
		} else {
			logStep("iter %d/%d %s exploration: generate %d candidates", iter, iterations, plan.Label, plan.CandidateCount)
		}
		restoreDryRun := func() error {
			if !dryRun {
				return nil
			}
			logDryRunVerbose("iter %d/%d restore original skill", iter, iterations)
			return os.WriteFile(skillAbs, previousSkill, 0o644)
		}
		selection, err := improveAndBenchmarkCandidates(root, runDir, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, iterDir, iter, previousSkill, feedback, plan, qualityFloor, limit, judge, repeats, parallelRepeats, parallelCases, parallelSuites)
		if err != nil {
			if restoreErr := restoreDryRun(); restoreErr != nil {
				return restoreErr
			}
			return err
		}
		candidate := selection.Result
		speculativeHoldout := selection.SpeculativeHoldout
		speculativeHoldoutRan := selection.SpeculativeHoldoutRan
		speculativeHoldoutErr := selection.SpeculativeHoldoutErr
		logVerbose("iter %d/%d evaluate", iter, iterations)
		candidatePath := selection.ResultPath
		candidateObjective := benchmarkObjective(candidate)
		candidateQuality := benchmarkQuality(candidate)
		delta := candidateObjective - bestObjective
		qualityOK := candidateQuality >= qualityFloor
		publicOK := qualityOK && delta >= minDelta
		if publicOK {
			printSemantic(logSemanticSuccess, "iter %d q=%.2f%% obj=%.2f%% delta=%+.2fpp", iter, candidateQuality*100, candidateObjective*100, delta*100)
		} else {
			printSemantic(logSemanticWarning, "iter %d q=%.2f%% obj=%.2f%% delta=%+.2fpp", iter, candidateQuality*100, candidateObjective*100, delta*100)
		}

		holdoutOK := true
		var holdoutGate *benchmarkGate
		if publicOK && holdoutCasesAbs != "" {
			var holdoutCandidate autoresearch.SuiteResult
			if speculativeHoldoutRan {
				if speculativeHoldoutErr != nil {
					if restoreErr := restoreDryRun(); restoreErr != nil {
						return restoreErr
					}
					return speculativeHoldoutErr
				}
				holdoutCandidate = speculativeHoldout
			} else {
				holdoutSuite := suiteLogLabel(holdoutCasesAbs)
				logBenchmarkCtx(suiteLogContext(holdoutSuite), "iter %d/%d holdout gate", iter, iterations)
				var err error
				holdoutCandidate, err = runBenchmark(root, holdoutCasesAbs, skillAbs, model, piBinary, filepath.Join(iterDir, "holdout"), holdoutSuite, limit, judge, repeats, parallelRepeats, parallelCases)
				if err != nil {
					if restoreErr := restoreDryRun(); restoreErr != nil {
						return restoreErr
					}
					return err
				}
			}
			holdoutPath := filepath.Join(iterDir, "holdout", "result.json")
			holdoutQuality := benchmarkQuality(holdoutCandidate)
			holdoutOK = holdoutQuality >= holdoutQualityFloor
			holdoutGate = &benchmarkGate{Result: holdoutCandidate, ResultPath: holdoutPath, QualityFloor: holdoutQualityFloor}
			if holdoutOK {
				printSemantic(logSemanticSuccess, "iter %d holdout q=%.2f%% floor=%.2f%% -> %s", iter, holdoutQuality*100, holdoutQualityFloor*100, displayRunPath(runDir, holdoutPath))
			} else {
				printSemantic(logSemanticWarning, "iter %d holdout q=%.2f%% < floor=%.2f%% -> %s", iter, holdoutQuality*100, holdoutQualityFloor*100, displayRunPath(runDir, holdoutPath))
			}
		} else if speculativeHoldoutRan && speculativeHoldoutErr != nil {
			logWarn("iter %d/%d speculative holdout failed after public reject; ignored: %v", iter, iterations, speculativeHoldoutErr)
		}
		if restoreErr := restoreDryRun(); restoreErr != nil {
			return restoreErr
		}
		feedbackSource = candidate

		if publicOK && holdoutOK {
			acceptedIterations++
			if dryRun {
				logSuccess("iter %d/%d accepted (dry-run)", iter, iterations)
				printSemantic(logSemanticDryRun, "accept iter %d; would commit %s (candidate %s)", iter, displayPath(root, skillAbs), displayRunPath(runDir, filepath.Join(iterDir, iterationSkillSnapshotPath)))
			} else {
				logSuccess("iter %d/%d accepted; commit", iter, iterations)
				if err := commitSkill(root, skillAbs, trainLoop, iter, candidate, candidatePath, holdoutGate, filepath.Join(iterDir, "researcher.stdout.md"), filepath.Join(iterDir, researcherSanitizedFeedbackPath), bestObjective, push); err != nil {
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
			rejectedIterations++
			if !qualityOK {
				printSemantic(logSemanticWarning, "iter %d reject: q=%.2f%% < floor=%.2f%%", iter, candidateQuality*100, qualityFloor*100)
			} else if !publicOK {
				printSemantic(logSemanticWarning, "iter %d reject: delta=%+.2fpp < min=%+.2fpp", iter, delta*100, minDelta*100)
			}
			if publicOK && !holdoutOK && holdoutGate != nil {
				printSemantic(logSemanticWarning, "iter %d reject: holdout q=%.2f%% < floor=%.2f%%", iter, benchmarkQuality(holdoutGate.Result)*100, holdoutGate.QualityFloor*100)
			}
			if dryRun {
				logWarn("iter %d/%d rejected (dry-run)", iter, iterations)
				printSemantic(logSemanticDryRun, "reject iter %d; would revert %s (candidate %s)", iter, displayPath(root, skillAbs), displayRunPath(runDir, filepath.Join(iterDir, iterationSkillSnapshotPath)))
			} else {
				logWarn("iter %d/%d rejected; revert", iter, iterations)
				if err := gitCheckout(root, skillAbs); err != nil {
					return err
				}
			}
		}
	}
	printSemantic(logSemanticSummary, "done iters=%d accepted=%d rejected=%d best obj=%.2f%% q=%.2f%% -> %s", iterations, acceptedIterations, rejectedIterations, bestObjective*100, bestQuality*100, displayRunPath(runDir, bestPath))
	if holdoutCasesAbs != "" {
		printSemantic(logSemanticSummary, "holdout best q=%.2f%% floor=%.2f%% -> %s", holdoutBestQuality*100, holdoutQualityFloor*100, displayRunPath(runDir, holdoutBestPath))
	}
	return nil
}

type benchmarkGate struct {
	Result       autoresearch.SuiteResult
	ResultPath   string
	QualityFloor float64
}

type iterationResearchPlan struct {
	Kind           string
	Label          string
	Guidance       string
	CandidateCount int
}

func planIterationResearch(iter, structuralInterval, rewriteInterval, explorationCandidates int) iterationResearchPlan {
	if explorationCandidates < 1 {
		explorationCandidates = 1
	}
	plan := iterationResearchPlan{
		Kind:           "incremental",
		Label:          "incremental refinement",
		Guidance:       "Default iteration: make one concise, general improvement. Keep the change focused unless a clearly better structure is needed.",
		CandidateCount: 1,
	}
	if iter <= 0 {
		return plan
	}
	if rewriteInterval > 0 && iter%rewriteInterval == 0 {
		plan.Kind = "rewrite"
		plan.Label = "full rewrite"
		plan.Guidance = "Full-rewrite exploration: create a fresh version of the complete skill from scratch, using the current skill only as reference. Preserve the YAML frontmatter and required safety behavior, but feel free to replace headings, ordering, wording, and examples wholesale. Keep the result compact, general, and anti-overfit."
		plan.CandidateCount = explorationCandidates
		return plan
	}
	if structuralInterval > 0 && iter%structuralInterval == 0 {
		plan.Kind = "structural"
		plan.Label = "structural mutation"
		plan.Guidance = "Structural exploration: make a larger coherent mutation if useful. You may reorganize sections, merge or split bullets, change headings, and replace clusters of guidance rather than only editing one line. Keep the result compact, general, and anti-overfit."
		plan.CandidateCount = explorationCandidates
	}
	return plan
}

func researcherCandidateDirective(plan iterationResearchPlan, candidateIndex int) string {
	candidateCount := plan.CandidateCount
	if candidateCount <= 0 {
		candidateCount = 1
	}
	if candidateIndex <= 0 {
		candidateIndex = 1
	}
	var b strings.Builder
	b.WriteString(plan.Guidance)
	if candidateCount > 1 {
		fmt.Fprintf(&b, "\n\nThis is candidate %d of %d for the same iteration. Produce a distinct alternative that could win on general quality, not a near-duplicate of a tiny line edit. Diversity should come from structure, emphasis, and concision, never from hidden benchmark-specific guesses.", candidateIndex, candidateCount)
	}
	return b.String()
}

type candidateSelection struct {
	Index                 int
	Count                 int
	Dir                   string
	Skill                 []byte
	Result                autoresearch.SuiteResult
	ResultPath            string
	TranscriptPath        string
	SpeculativeHoldout    autoresearch.SuiteResult
	SpeculativeHoldoutRan bool
	SpeculativeHoldoutErr error
}

func saveBaselineSkillArtifacts(skillAbs, baselineDir string, haveHoldout bool, holdoutDir string) error {
	skill, err := os.ReadFile(skillAbs)
	if err != nil {
		return err
	}
	if err := writeSkillRunArtifact(baselineDir, iterationSkillSnapshotPath, skill); err != nil {
		return err
	}
	if haveHoldout {
		return writeSkillRunArtifact(holdoutDir, iterationSkillSnapshotPath, skill)
	}
	return nil
}

func saveIterationSkillArtifacts(iterDir string, previousSkill, candidateSkill []byte) error {
	if err := writeSkillRunArtifact(iterDir, iterationPreviousSkillPath, previousSkill); err != nil {
		return err
	}
	if err := writeSkillRunArtifact(iterDir, iterationSkillSnapshotPath, candidateSkill); err != nil {
		return err
	}
	diff, err := generateSkillDiff(iterDir)
	if err != nil {
		return err
	}
	return writeSkillRunArtifact(iterDir, iterationSkillDiffPath, diff)
}

func writeSkillRunArtifact(dir, name string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o644)
}

func generateSkillDiff(iterDir string) ([]byte, error) {
	cmd := exec.Command("git", "diff", "--no-index", "--no-color", "--", iterationPreviousSkillPath, iterationSkillSnapshotPath)
	cmd.Dir = iterDir
	out, err := cmd.CombinedOutput()
	if err != nil && !isGitDiffDifferentExit(err) {
		return nil, fmt.Errorf("creating skill diff: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func isGitDiffDifferentExit(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1
}

func prepareBenchmarkPrerequisites(root string, casesPaths ...string) error {
	if err := ensureSkilltrainRShell(root); err != nil {
		return err
	}
	for _, casesPath := range casesPaths {
		if casesPath != "" && isRemoteHostDiagnosticsSuite(casesPath) {
			logBenchmarkVerbose("fixtures generate")
			return autoresearch.GenerateRemoteHostDiagnosticsFixtures(root)
		}
	}
	return nil
}

func ensureSkilltrainRShell(root string) error {
	if _, err := skilltrainRShellExecutable(root); err == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "build")
	cmd.Dir = root
	stdout, stderr, capture := commandWriters(skilltrainLogVerbose)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return commandError("building ./rshell", err, capture)
	}
	if _, err := skilltrainRShellExecutable(root); err != nil {
		return err
	}
	return nil
}

func skilltrainRShellExecutable(root string) (string, error) {
	for _, candidate := range []string{filepath.Join(root, "rshell"), filepath.Join(root, "rshell.exe")} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("./rshell not found or not executable under repo root")
}

func researcherRShellCapabilitySnapshot(root string) string {
	snapshot, err := loadRShellCapabilitySnapshot(root)
	if err != nil {
		logVerbose("setup rshell capability snapshot unavailable: %v", err)
		return "Static rshell capability snapshot unavailable. The skill should still tell agents to run rshell help in the target environment because deployments may differ."
	}
	return snapshot
}

func loadRShellCapabilitySnapshot(root string) (string, error) {
	rshellPath, err := skilltrainRShellExecutable(root)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, rshellPath, "--allow-all-commands", "--timeout", "5s", "-c", "help")
	cmd.Dir = root
	stdout := newLimitedBuffer(rshellCapabilitySnapshotMaxBytes)
	stderr := newLimitedBuffer(2 * 1024)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", commandError("collecting static ./rshell help snapshot", err, &commandCapture{stdout: stdout, stderr: stderr})
	}
	snapshot := strings.TrimSpace(stdout.String())
	if snapshot == "" {
		return "", fmt.Errorf("static ./rshell help snapshot was empty")
	}
	return snapshot, nil
}

func isRemoteHostDiagnosticsSuite(casesPath string) bool {
	return filepath.Base(filepath.Dir(casesPath)) == "remote-host-diagnostics"
}

func suiteLogLabel(casesPath string) string {
	base := strings.TrimSuffix(filepath.Base(casesPath), filepath.Ext(casesPath))
	switch base {
	case "cases":
		return "public"
	case "holdout":
		return "holdout"
	case "":
		return "suite"
	default:
		return base
	}
}

type benchmarkOutcome struct {
	Result autoresearch.SuiteResult
	Err    error
}

func runBaselineBenchmarks(root, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, runDir string, limit int, judge bool, repeats, parallelRepeats, parallelCases int, parallelSuites bool) (autoresearch.SuiteResult, autoresearch.SuiteResult, bool, error) {
	baselineDir := filepath.Join(runDir, "iter-000-baseline")
	baselineSuite := suiteLogLabel(casesAbs)
	if holdoutCasesAbs == "" {
		baseline, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, baselineDir, baselineSuite, limit, judge, repeats, parallelRepeats, parallelCases)
		return baseline, autoresearch.SuiteResult{}, false, err
	}

	holdoutDir := filepath.Join(runDir, "iter-000-holdout")
	holdoutSuite := suiteLogLabel(holdoutCasesAbs)
	if parallelSuites {
		logBenchmarkVerbose("baseline public+holdout parallel")
		baselineCh := runBenchmarkAsync(root, casesAbs, skillAbs, model, piBinary, baselineDir, baselineSuite, limit, judge, repeats, parallelRepeats, parallelCases)
		holdoutCh := runBenchmarkAsync(root, holdoutCasesAbs, skillAbs, model, piBinary, holdoutDir, holdoutSuite, limit, judge, repeats, parallelRepeats, parallelCases)
		baseline := <-baselineCh
		holdout := <-holdoutCh
		if baseline.Err != nil {
			return autoresearch.SuiteResult{}, autoresearch.SuiteResult{}, false, baseline.Err
		}
		if holdout.Err != nil {
			return autoresearch.SuiteResult{}, autoresearch.SuiteResult{}, false, holdout.Err
		}
		return baseline.Result, holdout.Result, true, nil
	}

	baseline, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, baselineDir, baselineSuite, limit, judge, repeats, parallelRepeats, parallelCases)
	if err != nil {
		return autoresearch.SuiteResult{}, autoresearch.SuiteResult{}, false, err
	}
	logBenchmarkCtx(suiteLogContext(holdoutSuite), "baseline holdout")
	holdout, err := runBenchmark(root, holdoutCasesAbs, skillAbs, model, piBinary, holdoutDir, holdoutSuite, limit, judge, repeats, parallelRepeats, parallelCases)
	if err != nil {
		return autoresearch.SuiteResult{}, autoresearch.SuiteResult{}, false, err
	}
	return baseline, holdout, true, nil
}

func runCandidateBenchmarks(root, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, iterDir string, limit int, judge bool, repeats, parallelRepeats, parallelCases int, parallelSuites bool) (autoresearch.SuiteResult, autoresearch.SuiteResult, bool, error, error) {
	candidateSuite := suiteLogLabel(casesAbs)
	if holdoutCasesAbs == "" || !parallelSuites {
		candidate, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, iterDir, candidateSuite, limit, judge, repeats, parallelRepeats, parallelCases)
		return candidate, autoresearch.SuiteResult{}, false, nil, err
	}

	holdoutSuite := suiteLogLabel(holdoutCasesAbs)
	logBenchmarkVerbose("candidate public+holdout parallel")
	candidateCh := runBenchmarkAsync(root, casesAbs, skillAbs, model, piBinary, iterDir, candidateSuite, limit, judge, repeats, parallelRepeats, parallelCases)
	holdoutCh := runBenchmarkAsync(root, holdoutCasesAbs, skillAbs, model, piBinary, filepath.Join(iterDir, "holdout"), holdoutSuite, limit, judge, repeats, parallelRepeats, parallelCases)
	candidate := <-candidateCh
	holdout := <-holdoutCh
	return candidate.Result, holdout.Result, true, holdout.Err, candidate.Err
}

func improveAndBenchmarkCandidates(root, runDir, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, iterDir string, iter int, previousSkill []byte, sanitizedFeedback string, plan iterationResearchPlan, qualityFloor float64, limit int, judge bool, repeats, parallelRepeats, parallelCases int, parallelSuites bool) (candidateSelection, error) {
	candidateCount := plan.CandidateCount
	if candidateCount <= 1 {
		if err := improveSkill(root, skillAbs, iterDir, model, piBinary, iter, sanitizedFeedback, plan, 1); err != nil {
			return candidateSelection{}, err
		}
		candidateSkill, err := os.ReadFile(skillAbs)
		if err != nil {
			return candidateSelection{}, err
		}
		if err := saveIterationSkillArtifacts(iterDir, previousSkill, candidateSkill); err != nil {
			return candidateSelection{}, err
		}
		logBenchmark("iter %d candidate", iter)
		candidate, speculativeHoldout, speculativeHoldoutRan, speculativeHoldoutErr, err := runCandidateBenchmarks(root, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, iterDir, limit, judge, repeats, parallelRepeats, parallelCases, parallelSuites)
		if err != nil {
			return candidateSelection{}, err
		}
		return candidateSelection{
			Index:                 1,
			Count:                 1,
			Dir:                   iterDir,
			Skill:                 append([]byte(nil), candidateSkill...),
			Result:                candidate,
			ResultPath:            filepath.Join(iterDir, "result.json"),
			TranscriptPath:        filepath.Join(iterDir, "researcher.stdout.md"),
			SpeculativeHoldout:    speculativeHoldout,
			SpeculativeHoldoutRan: speculativeHoldoutRan,
			SpeculativeHoldoutErr: speculativeHoldoutErr,
		}, nil
	}

	var best candidateSelection
	for candidateIndex := 1; candidateIndex <= candidateCount; candidateIndex++ {
		if err := os.WriteFile(skillAbs, previousSkill, 0o644); err != nil {
			return candidateSelection{}, err
		}
		candidateDir := filepath.Join(iterDir, fmt.Sprintf("candidate-%03d", candidateIndex))
		if err := os.MkdirAll(candidateDir, 0o755); err != nil {
			return candidateSelection{}, err
		}
		transcriptPath := filepath.Join(candidateDir, "researcher.stdout.md")
		logStep("iter %d %s candidate %d/%d edit -> %s", iter, plan.Label, candidateIndex, candidateCount, displayRunPath(runDir, transcriptPath))
		if err := improveSkill(root, skillAbs, candidateDir, model, piBinary, iter, sanitizedFeedback, plan, candidateIndex); err != nil {
			return candidateSelection{}, err
		}
		candidateSkill, err := os.ReadFile(skillAbs)
		if err != nil {
			return candidateSelection{}, err
		}
		if err := saveIterationSkillArtifacts(candidateDir, previousSkill, candidateSkill); err != nil {
			return candidateSelection{}, err
		}

		candidateSuite := fmt.Sprintf("%s-candidate-%03d", suiteLogLabel(casesAbs), candidateIndex)
		logBenchmark("iter %d %s candidate %d/%d", iter, plan.Label, candidateIndex, candidateCount)
		candidate, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, candidateDir, candidateSuite, limit, judge, repeats, parallelRepeats, parallelCases)
		if err != nil {
			return candidateSelection{}, err
		}
		selection := candidateSelection{
			Index:          candidateIndex,
			Count:          candidateCount,
			Dir:            candidateDir,
			Skill:          append([]byte(nil), candidateSkill...),
			Result:         candidate,
			ResultPath:     filepath.Join(candidateDir, "result.json"),
			TranscriptPath: transcriptPath,
		}
		if betterCandidateSelection(selection, best, qualityFloor) {
			best = selection
		}
	}
	if best.Index == 0 {
		return candidateSelection{}, fmt.Errorf("no candidate was generated")
	}
	if err := os.WriteFile(skillAbs, best.Skill, 0o644); err != nil {
		return candidateSelection{}, err
	}
	if err := saveIterationSkillArtifacts(iterDir, previousSkill, best.Skill); err != nil {
		return candidateSelection{}, err
	}
	rootResultPath := filepath.Join(iterDir, "result.json")
	if err := copyFile(best.ResultPath, rootResultPath); err != nil {
		return candidateSelection{}, err
	}
	rootTranscriptPath := filepath.Join(iterDir, "researcher.stdout.md")
	if err := copyFile(best.TranscriptPath, rootTranscriptPath); err != nil {
		return candidateSelection{}, err
	}
	selectedSummary := fmt.Sprintf("candidate-%03d\n", best.Index)
	if err := os.WriteFile(filepath.Join(iterDir, "selected-candidate.txt"), []byte(selectedSummary), 0o644); err != nil {
		return candidateSelection{}, err
	}
	best.Dir = iterDir
	best.ResultPath = rootResultPath
	best.TranscriptPath = rootTranscriptPath
	logSuccess("iter %d selected %s candidate %d/%d q=%.2f%% obj=%.2f%%", iter, plan.Label, best.Index, best.Count, benchmarkQuality(best.Result)*100, benchmarkObjective(best.Result)*100)
	return best, nil
}

func betterCandidateSelection(candidate, best candidateSelection, qualityFloor float64) bool {
	if best.Index == 0 {
		return true
	}
	candidateQualityOK := benchmarkQuality(candidate.Result) >= qualityFloor
	bestQualityOK := benchmarkQuality(best.Result) >= qualityFloor
	if candidateQualityOK != bestQualityOK {
		return candidateQualityOK
	}
	candidateObjective := benchmarkObjective(candidate.Result)
	bestObjective := benchmarkObjective(best.Result)
	if candidateObjective != bestObjective {
		return candidateObjective > bestObjective
	}
	return benchmarkQuality(candidate.Result) > benchmarkQuality(best.Result)
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func runBenchmarkAsync(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel string, limit int, judge bool, repeats, parallelRepeats, parallelCases int) <-chan benchmarkOutcome {
	ch := make(chan benchmarkOutcome, 1)
	go func() {
		result, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel, limit, judge, repeats, parallelRepeats, parallelCases)
		ch <- benchmarkOutcome{Result: result, Err: err}
	}()
	return ch
}

func runBenchmark(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel string, limit int, judge bool, repeats, parallelRepeats, parallelCases int) (autoresearch.SuiteResult, error) {
	if repeats <= 1 {
		return runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel, 1, 1, limit, judge, parallelCases)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	results := make([]autoresearch.SuiteResult, repeats)
	paths := make([]string, repeats)
	parallelism := repeatParallelism(parallelRepeats, repeats)
	if parallelism > 1 {
		logBenchmarkVerboseCtx(suiteLogContext(suiteLabel), "%dx reps-par=%d cases-par=%d", repeats, parallelism, parallelCases)
	}

	if parallelism <= 1 {
		for repeat := 1; repeat <= repeats; repeat++ {
			result, err := runBenchmarkRepeat(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel, limit, judge, parallelCases, repeat, repeats)
			if err != nil {
				return autoresearch.SuiteResult{}, err
			}
			results[repeat-1] = result
			paths[repeat-1] = filepath.Join(outDir, fmt.Sprintf("repeat-%03d", repeat), "result.json")
		}
	} else {
		jobs := make(chan int)
		errCh := make(chan error, repeats)
		var wg sync.WaitGroup
		for worker := 0; worker < parallelism; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for repeat := range jobs {
					result, err := runBenchmarkRepeat(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel, limit, judge, parallelCases, repeat, repeats)
					if err != nil {
						errCh <- err
						continue
					}
					results[repeat-1] = result
					paths[repeat-1] = filepath.Join(outDir, fmt.Sprintf("repeat-%03d", repeat), "result.json")
				}
			}()
		}
		for repeat := 1; repeat <= repeats; repeat++ {
			jobs <- repeat
		}
		close(jobs)
		wg.Wait()
		close(errCh)
		for err := range errCh {
			if err != nil {
				return autoresearch.SuiteResult{}, err
			}
		}
	}

	aggregate, err := aggregateBenchmarkRepeats(results, paths)
	if err != nil {
		return autoresearch.SuiteResult{}, err
	}
	aggregatePath := filepath.Join(outDir, "result.json")
	if err := autoresearch.WriteJSON(aggregatePath, aggregate); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	logBenchmarkCtx(suiteLogContext(suiteLabel), "done n=%d q=%.2f%% obj=%.2f%% avg=%.1fs -> %s", repeats, benchmarkQuality(aggregate)*100, benchmarkObjective(aggregate)*100, aggregate.AverageCaseDurationSeconds, displayPath(root, aggregatePath))
	return aggregate, nil
}

func runBenchmarkRepeat(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel string, limit int, judge bool, parallelCases, repeat, repeats int) (autoresearch.SuiteResult, error) {
	repeatDir := filepath.Join(outDir, fmt.Sprintf("repeat-%03d", repeat))
	logBenchmarkVerboseCtx(repeatLogContext(suiteLabel, repeat, repeats), "repeat %d/%d start", repeat, repeats)
	return runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, repeatDir, suiteLabel, repeat, repeats, limit, judge, parallelCases)
}

func repeatParallelism(configured, repeats int) int {
	if repeats <= 1 {
		return 1
	}
	if configured <= 0 || configured > repeats {
		return repeats
	}
	return configured
}

func runBenchmarkOnce(root, casesAbs, skillAbs, model, piBinary, outDir, suiteLabel string, repeat, repeats, limit int, judge bool, parallelCases int) (autoresearch.SuiteResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	logCtx := repeatLogContext(suiteLabel, repeat, repeats)
	logBenchmarkVerboseCtx(logCtx, "out %s", displayPath(root, outDir))
	goRunDir, skillbenchTarget := skillbenchGoRunTarget(root)
	args := []string{
		"run", skillbenchTarget,
		"-cases", casesAbs,
		"-skill", filepath.Dir(skillAbs),
		"-model", model,
		"-pi", piBinary,
		"-out", filepath.Join(outDir, "result.json"),
		"-raw-dir", filepath.Join(outDir, "raw"),
		"-parallel-cases", fmt.Sprint(parallelCases),
		"-log-suite", suiteLabel,
		"-log-repeat", logCtx.Repeat,
		"-ensure-rshell=false",
		"-generate-fixtures=false",
	}
	if limit > 0 {
		args = append(args, "-limit", fmt.Sprint(limit))
	}
	if judge {
		args = append(args, "-judge")
	}
	logBenchmarkVerboseCtx(logCtx, "exec skillbench")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = goRunDir
	stdout, stderr, capture := commandWriters(skilltrainLogVerbose)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return autoresearch.SuiteResult{}, commandError("skillbench failed", err, capture)
	}
	data, err := os.ReadFile(filepath.Join(outDir, "result.json"))
	if err != nil {
		return autoresearch.SuiteResult{}, err
	}
	var result autoresearch.SuiteResult
	if err := json.Unmarshal(data, &result); err != nil {
		return autoresearch.SuiteResult{}, err
	}
	if repeats <= 1 {
		logBenchmarkCtx(suiteLogContext(suiteLabel), "done q=%.2f%% obj=%.2f%% avg=%.1fs -> %s", benchmarkQuality(result)*100, benchmarkObjective(result)*100, result.AverageCaseDurationSeconds, displayPath(root, filepath.Join(outDir, "result.json")))
	}
	return result, nil
}

func skillbenchGoRunTarget(root string) (dir, target string) {
	if hasFile(filepath.Join(root, "go.mod")) && hasDir(filepath.Join(root, "cmd", "skillbench")) {
		return root, "./cmd/skillbench"
	}
	autoRoot := filepath.Join(root, "auto-improve-skills")
	if hasFile(filepath.Join(autoRoot, "go.mod")) {
		return autoRoot, "./cmd/skillbench"
	}
	return root, "./auto-improve-skills/cmd/skillbench"
}

func hasFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

func hasDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
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

type researcherWorkspace struct {
	Dir      string
	SkillRel string
}

func prepareResearcherWorkspace(root, skillAbs string) (researcherWorkspace, error) {
	workspaceDir, err := os.MkdirTemp("", "skilltrain-researcher-*")
	if err != nil {
		return researcherWorkspace{}, err
	}
	cleanupOnError := true
	defer func() {
		if cleanupOnError {
			_ = os.RemoveAll(workspaceDir)
		}
	}()

	programData, err := os.ReadFile(filepath.Join(root, "auto-improve-skills", "program.md"))
	if err != nil {
		return researcherWorkspace{}, err
	}
	if err := writeResearcherWorkspaceFile(workspaceDir, researcherProgramPath, programData); err != nil {
		return researcherWorkspace{}, err
	}

	skillData, err := os.ReadFile(skillAbs)
	if err != nil {
		return researcherWorkspace{}, err
	}
	skillRel := researcherSkillRelPath(skillAbs)
	if err := writeResearcherWorkspaceFile(workspaceDir, skillRel, skillData); err != nil {
		return researcherWorkspace{}, err
	}

	cleanupOnError = false
	return researcherWorkspace{Dir: workspaceDir, SkillRel: skillRel}, nil
}

func researcherSkillRelPath(skillAbs string) string {
	skillDir := filepath.Base(filepath.Dir(skillAbs))
	if strings.TrimSpace(skillDir) == "" || skillDir == "." || skillDir == string(filepath.Separator) {
		skillDir = "skill"
	}
	return filepath.Join("skills", skillDir, "SKILL.md")
}

func writeResearcherWorkspaceFile(workspaceDir, relPath string, data []byte) error {
	path := filepath.Join(workspaceDir, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

type sanitizedFeedbackSource struct {
	Version                   int                                  `json:"version"`
	LLMEnabled                bool                                 `json:"llm_enabled,omitempty"`
	LLMModel                  string                               `json:"llm_model,omitempty"`
	LLMError                  string                               `json:"llm_error,omitempty"`
	Feedback                  string                               `json:"feedback,omitempty"`
	RShellProcedureCategories []sanitizedFeedbackProcedureCategory `json:"rshell_procedure_feedback_categories,omitempty"`
	SafeAggregate             sanitizedFeedbackAggregate           `json:"safe_aggregate"`
}

// sanitizedFeedbackProcedureCategory is deterministic, benchmark-agnostic process
// guidance derived only from safe aggregate counts. It intentionally avoids
// command lines, flags, paths, identifiers, service names, and case facts.
type sanitizedFeedbackProcedureCategory struct {
	Category string `json:"category"`
	Guidance string `json:"guidance"`
}

type sanitizedFeedbackAggregate struct {
	CaseCount                 int                                        `json:"case_count"`
	CriteriaCount             int                                        `json:"criteria_count"`
	FailedCriteriaCount       int                                        `json:"failed_criteria_count"`
	FailureOccurrences        int                                        `json:"failure_occurrences"`
	QualityNormalizedScore    float64                                    `json:"quality_normalized_score"`
	ObjectiveNormalizedScore  float64                                    `json:"objective_normalized_score"`
	QualityMissedPoints       float64                                    `json:"quality_missed_points,omitempty"`
	CriteriaBySource          map[string]sanitizedFeedbackCriterionStats `json:"criteria_by_source,omitempty"`
	EvidenceRequiredCriteria  sanitizedFeedbackCriterionStats            `json:"evidence_required_criteria,omitempty"`
	NegativeAssertionCriteria sanitizedFeedbackCriterionStats            `json:"negative_assertion_criteria,omitempty"`
	CaseScoreBuckets          map[string]int                             `json:"case_score_buckets,omitempty"`
	AverageCommandCount       float64                                    `json:"average_command_count,omitempty"`
	AverageFailedToolCalls    float64                                    `json:"average_failed_tool_calls,omitempty"`
	AverageToolOutputKB       float64                                    `json:"average_tool_output_kb,omitempty"`
	AverageDurationSeconds    float64                                    `json:"average_duration_seconds,omitempty"`
	SafetyViolationCases      int                                        `json:"safety_violation_cases,omitempty"`
	SafetyViolationCount      int                                        `json:"safety_violation_count,omitempty"`
	ErrorCases                int                                        `json:"error_cases,omitempty"`
	Repeats                   int                                        `json:"repeats,omitempty"`
	SkillSizeEstimatedTokens  int                                        `json:"skill_size_estimated_tokens,omitempty"`
}

type sanitizedFeedbackCriterionStats struct {
	TotalCriteria      int     `json:"total_criteria"`
	FailedCriteria     int     `json:"failed_criteria"`
	FailureOccurrences int     `json:"failure_occurrences,omitempty"`
	MissedPoints       float64 `json:"missed_points,omitempty"`
}

type sanitizedFeedbackLLMRequest struct {
	Instructions              []string                             `json:"instructions"`
	SafeAggregate             sanitizedFeedbackAggregate           `json:"safe_aggregate"`
	RShellProcedureCategories []sanitizedFeedbackProcedureCategory `json:"rshell_procedure_feedback_categories,omitempty"`
	OutputSchema              string                               `json:"output_schema"`
}

func buildSanitizedFeedbackSource(result autoresearch.SuiteResult) sanitizedFeedbackSource {
	aggregate := buildSanitizedFeedbackAggregate(result)
	return sanitizedFeedbackSource{
		Version:                   4,
		RShellProcedureCategories: buildSanitizedRShellProcedureCategories(aggregate),
		SafeAggregate:             aggregate,
	}
}

func buildSanitizedFeedbackAggregate(result autoresearch.SuiteResult) sanitizedFeedbackAggregate {
	agg := sanitizedFeedbackAggregate{
		CaseCount:                len(result.Cases),
		QualityNormalizedScore:   roundFloat(benchmarkQuality(result), 4),
		ObjectiveNormalizedScore: roundFloat(benchmarkObjective(result), 4),
		CriteriaBySource:         map[string]sanitizedFeedbackCriterionStats{},
		CaseScoreBuckets:         map[string]int{},
		Repeats:                  result.Repeats,
		SkillSizeEstimatedTokens: result.SkillSizeEstimatedTokens,
	}
	qualityMax := result.QualityMaxScore
	qualityScore := result.QualityScore
	if qualityMax == 0 {
		qualityMax = result.MaxScore
		qualityScore = result.Score
	}
	if qualityMax > qualityScore {
		agg.QualityMissedPoints = roundFloat(qualityMax-qualityScore, 2)
	}

	var totalCommands, totalFailedToolCalls, totalToolOutputBytes float64
	for _, caseResult := range result.Cases {
		agg.CaseScoreBuckets[sanitizedFeedbackCaseScoreBucket(caseResult.NormalizedScore)]++
		totalCommands += float64(caseResult.CommandCount)
		totalFailedToolCalls += float64(caseResult.FailedToolCalls)
		totalToolOutputBytes += float64(caseResult.ToolOutputBytes)
		if len(caseResult.SafetyViolations) > 0 {
			agg.SafetyViolationCases++
			agg.SafetyViolationCount += len(caseResult.SafetyViolations)
		}
		if strings.TrimSpace(caseResult.Error) != "" {
			agg.ErrorCases++
		}
		for _, criterion := range caseResult.Criteria {
			agg.CriteriaCount++
			failed := !criterion.Passed
			occurrences := 0
			missed := 0.0
			if failed {
				agg.FailedCriteriaCount++
				occurrences = sanitizedFeedbackFailureOccurrences(criterion)
				agg.FailureOccurrences += occurrences
				missed = criterion.Max - criterion.Points
				if missed < 0 {
					missed = 0
				}
			}

			source := sanitizedCriterionSource(criterion.Source)
			stats := agg.CriteriaBySource[source]
			addSanitizedCriterionStat(&stats, failed, occurrences, missed)
			agg.CriteriaBySource[source] = stats
			if criterion.EvidenceRequired {
				addSanitizedCriterionStat(&agg.EvidenceRequiredCriteria, failed, occurrences, missed)
			}
			if criterion.Negative {
				addSanitizedCriterionStat(&agg.NegativeAssertionCriteria, failed, occurrences, missed)
			}
		}
	}
	if agg.CaseCount > 0 {
		count := float64(agg.CaseCount)
		agg.AverageCommandCount = roundFloat(totalCommands/count, 2)
		agg.AverageFailedToolCalls = roundFloat(totalFailedToolCalls/count, 2)
		agg.AverageToolOutputKB = roundFloat(totalToolOutputBytes/count/1024, 2)
		agg.AverageDurationSeconds = roundFloat(result.AverageCaseDurationSeconds, 2)
	}
	return agg
}

func buildSanitizedRShellProcedureCategories(aggregate sanitizedFeedbackAggregate) []sanitizedFeedbackProcedureCategory {
	categories := make([]sanitizedFeedbackProcedureCategory, 0, 5)
	add := func(category, guidance string) {
		categories = append(categories, sanitizedFeedbackProcedureCategory{Category: category, Guidance: guidance})
	}

	commandStats := aggregate.CriteriaBySource["commands"]
	finalStats := aggregate.CriteriaBySource["final"]
	toolStats := aggregate.CriteriaBySource["tool_results"]
	safetyStats := aggregate.CriteriaBySource["safety"]

	if commandStats.FailedCriteria > 0 {
		add("rshell capability discovery", "Use rshell help as the source of truth before relying on command-specific features, and adapt when a capability is unavailable.")
		add("rshell command coverage", "Make each rshell probe purposeful, read-only, and tied to a hypothesis; gather enough targeted evidence before synthesis.")
	}
	if aggregate.AverageFailedToolCalls > 0 || aggregate.ErrorCases > 0 {
		add("failed or partial rshell output", "Treat denied, unsupported, slow, partial, or empty results as evidence about uncertainty; do not retry the same failing probe unchanged.")
	}
	if aggregate.SafetyViolationCount > 0 || safetyStats.FailedCriteria > 0 || aggregate.NegativeAssertionCriteria.FailedCriteria > 0 {
		add("read-only safety", "Keep diagnostics non-mutating and narrowly scoped; recommend remediation only as safe next steps rather than executing it.")
	}
	if aggregate.EvidenceRequiredCriteria.FailedCriteria > 0 || finalStats.FailedCriteria > 0 || toolStats.FailedCriteria > 0 {
		add("evidence-grounded synthesis", "Base conclusions on observed rshell output, distinguish checked facts from hypotheses, and state uncertainty when evidence is absent or ambiguous.")
	}
	if aggregate.AverageCommandCount > 6 || aggregate.AverageToolOutputKB > 64 || aggregate.AverageDurationSeconds > 60 {
		add("boundedness and stopping", "Prefer narrow filters, limits, and recent ranges; stop once the likely cause or next safe check is well supported.")
	}
	if len(categories) == 0 && sanitizedFeedbackHasSignal(aggregate) {
		add("general rshell procedure", "Improve the balance of capability discovery, bounded read-only probes, evidence grounding, and concise final synthesis.")
	}
	return categories
}

func addSanitizedCriterionStat(stat *sanitizedFeedbackCriterionStats, failed bool, occurrences int, missed float64) {
	stat.TotalCriteria++
	if !failed {
		return
	}
	stat.FailedCriteria++
	stat.FailureOccurrences += occurrences
	stat.MissedPoints = roundFloat(stat.MissedPoints+missed, 2)
}

func sanitizedCriterionSource(source string) string {
	switch strings.TrimSpace(source) {
	case "final", "commands", "tool_results", "transcript", "safety":
		return strings.TrimSpace(source)
	case "":
		return "unknown"
	default:
		return "other"
	}
}

func sanitizedFeedbackCaseScoreBucket(score float64) string {
	switch {
	case score >= 1:
		return "full"
	case score >= 0.8:
		return "high"
	case score >= 0.5:
		return "partial"
	case score > 0:
		return "low"
	default:
		return "zero"
	}
}

func sanitizedFeedbackHasSignal(aggregate sanitizedFeedbackAggregate) bool {
	return aggregate.FailedCriteriaCount > 0 || aggregate.SafetyViolationCount > 0 || aggregate.ErrorCases > 0
}

func populateSanitizedFeedbackWithLLM(piBinary, model string, source *sanitizedFeedbackSource) error {
	source.LLMEnabled = true
	source.LLMModel = model
	source.LLMError = ""
	source.Feedback = ""
	if !sanitizedFeedbackHasSignal(source.SafeAggregate) {
		return nil
	}
	feedback, err := generateSanitizedFeedbackWithLLM(piBinary, model, *source)
	if err != nil {
		source.LLMError = err.Error()
		return err
	}
	source.Feedback = feedback
	return nil
}

func formatSanitizedResearcherFeedback(result autoresearch.SuiteResult) string {
	return formatSanitizedResearcherFeedbackFromSource(buildSanitizedFeedbackSource(result))
}

func formatSanitizedResearcherFeedbackFromSource(source sanitizedFeedbackSource) string {
	feedback := strings.TrimSpace(source.Feedback)
	if feedback == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("General hidden-task feedback (LLM-generated from sanitized aggregate metrics only; no prompts, outputs, criterion names, or task facts were disclosed):\n")
	b.WriteString(feedback)
	if !strings.HasSuffix(feedback, "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\nAnti-overfitting guardrails:\n")
	b.WriteString("- Treat this feedback as process guidance only, not as evidence about hidden tasks.\n")
	b.WriteString("- Do not add exact case facts, paths, filenames, IDs, IPs, timestamps, services, commands, root causes, line numbers, or expected-answer wording.\n")
	b.WriteString("- Prefer focused broadly useful changes unless the iteration explicitly asks for structural/rewrite exploration; ignore any feedback point that is not clearly safe and general.\n")
	return b.String()
}

func sanitizedFeedbackFailureOccurrences(criterion autoresearch.CriterionResult) int {
	var passed, seen int
	if _, err := fmt.Sscanf(criterion.Detail, "passed in %d/%d repeats", &passed, &seen); err == nil && seen > passed {
		return seen - passed
	}
	return 1
}

func generateSanitizedFeedbackWithLLM(piBinary, model string, source sanitizedFeedbackSource) (string, error) {
	request := sanitizedFeedbackLLMRequest{
		Instructions: []string{
			"Use only the safe aggregate benchmark metrics below; raw prompts, outputs, case IDs, criterion names, commands, logs, judge reasons, paths, identifiers, timestamps, services, and root causes have intentionally been omitted.",
			"Generate concise freeform process feedback for a researcher improving a diagnostic skill. Mention only generic categories inferred from aggregate metrics, such as evidence grounding, command/procedure coverage, safety, uncertainty handling, boundedness, or concision.",
			"Do not invent or include task facts, expected answers, file names, paths, identifiers, IPs, timestamps, service names, command snippets, root causes, or benchmark case details.",
			"If the aggregate signal is weak, say to make no change unless a safe general improvement is obvious.",
		},
		SafeAggregate: source.SafeAggregate,
		OutputSchema:  `{"feedback":"short freeform researcher feedback, 1-5 bullets or a short paragraph"}`,
	}
	requestJSON, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return "", err
	}
	prompt := fmt.Sprintf(`You generate sanitized freeform feedback for an autoresearch skill-improvement agent.

Safety rules:
- The JSON input contains only aggregate metrics that are safe to disclose.
- Do not mention or infer hidden task facts, exact identifiers, file names, paths, IPs, timestamps, service names, command snippets, root causes, case names, criterion names, or expected-answer text.
- Feedback must be generic process guidance that should help unseen incidents.
- Return only strict JSON with exactly this schema: {"feedback":"..."}
- The feedback string may contain Markdown bullets, but no other JSON fields are allowed.

<safe-aggregate-json>
%s
</safe-aggregate-json>
`, string(requestJSON))

	workspaceDir, err := os.MkdirTemp("", "skilltrain-feedback-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(workspaceDir)

	ctx, cancel := context.WithTimeout(context.Background(), sanitizedFeedbackLLMTimeout)
	defer cancel()
	args := []string{
		"--print",
		"--no-session",
		"--no-context-files",
		"--no-extensions",
		"--no-prompt-templates",
		"--no-skills",
		"--no-tools",
		"--model", model,
		prompt,
	}
	cmd := exec.CommandContext(ctx, piBinary, args...)
	cmd.Dir = workspaceDir
	cmd.Env = autoresearch.EnvWithExecutableDir(piBinary)
	stdout := newLimitedBuffer(commandOutputLimit)
	stderr := newLimitedBuffer(commandOutputLimit)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return "", commandError("feedback llm pi", err, &commandCapture{stdout: stdout, stderr: stderr})
	}
	return parseSanitizedFeedbackLLMOutput(stdout.String())
}

func parseSanitizedFeedbackLLMOutput(output string) (string, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return "", fmt.Errorf("feedback llm returned empty output")
	}
	var response struct {
		Feedback string `json:"feedback"`
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return "", fmt.Errorf("feedback llm output is not strict JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", fmt.Errorf("feedback llm output contains trailing data")
	}
	return validateSanitizedFeedbackText(response.Feedback)
}

var sanitizedFeedbackForbiddenPatterns = []struct {
	name string
	re   *regexp.Regexp
}{
	{name: "IP address", re: regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)},
	{name: "absolute path", re: regexp.MustCompile(`(?:^|\s)(?:/|~/|[A-Za-z]:\\)[^\s]+`)},
	{name: "relative path", re: regexp.MustCompile("(?:^|\\s|[\"'])(?:\\.\\.?/)[^\\s]+")},
	{name: "file name", re: regexp.MustCompile(`\b[\w.-]+\.(?:log|yaml|yml|json|conf|cfg|ini|txt|out|err)\b`)},
	{name: "timestamp", re: regexp.MustCompile(`\b\d{1,2}:\d{2}(?::\d{2})?(?:\s?(?:UTC|Z))?\b`)},
	{name: "date", re: regexp.MustCompile(`\b20\d{2}-\d{2}-\d{2}\b`)},
	{name: "line number", re: regexp.MustCompile(`(?i)\bline\s+\d+\b`)},
	{name: "case-like identifier", re: regexp.MustCompile(`\b[a-zA-Z]+-[a-zA-Z0-9]*\d[a-zA-Z0-9-]*\b`)},
}

func validateSanitizedFeedbackText(feedback string) (string, error) {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return "", fmt.Errorf("feedback llm returned empty feedback")
	}
	if len(feedback) > sanitizedFeedbackMaxChars {
		return "", fmt.Errorf("feedback llm returned %d bytes; maximum is %d", len(feedback), sanitizedFeedbackMaxChars)
	}
	if strings.ContainsRune(feedback, '\x00') {
		return "", fmt.Errorf("feedback llm returned control characters")
	}
	for _, forbidden := range sanitizedFeedbackForbiddenPatterns {
		if forbidden.re.MatchString(feedback) {
			return "", fmt.Errorf("feedback llm returned unsafe %s", forbidden.name)
		}
	}
	return feedback, nil
}

func roundFloat(value float64, places int) float64 {
	if places < 0 {
		return value
	}
	factor := math.Pow10(places)
	return math.Round(value*factor) / factor
}

func regenerateSanitizedFeedbackArtifacts(path, piBinary, feedbackModel string, feedbackLLM bool) (int, error) {
	runDirs, err := discoverSkilltrainRunDirs(path)
	if err != nil {
		return 0, err
	}
	resolvedPI := piBinary
	var feedbackResolveErr error
	if feedbackLLM {
		resolvedPI, feedbackResolveErr = autoresearch.ResolvePI(piBinary)
	}
	total := 0
	for _, runDir := range runDirs {
		count, err := regenerateSanitizedFeedbackArtifactsForRun(runDir, resolvedPI, feedbackModel, feedbackLLM, feedbackResolveErr)
		if err != nil {
			return total, err
		}
		total += count
	}
	return total, nil
}

func discoverSkilltrainRunDirs(path string) ([]string, error) {
	path = filepath.Clean(path)
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", path)
	}
	if fileExists(filepath.Join(path, "iter-000-baseline", "result.json")) {
		return []string{path}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	runDirs := []string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		candidate := filepath.Join(path, entry.Name())
		if fileExists(filepath.Join(candidate, "iter-000-baseline", "result.json")) {
			runDirs = append(runDirs, candidate)
		}
	}
	sort.Strings(runDirs)
	if len(runDirs) == 0 {
		return nil, fmt.Errorf("no skilltrain run directories found under %s", path)
	}
	return runDirs, nil
}

func regenerateSanitizedFeedbackArtifactsForRun(runDir, piBinary, feedbackModel string, feedbackLLM bool, feedbackResolveErr error) (int, error) {
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		iter, ok := parseIterationDir(entry.Name())
		if !ok || iter == 0 {
			continue
		}
		sourcePath := sanitizedFeedbackSourceResultPath(runDir, iter)
		if !fileExists(sourcePath) {
			continue
		}
		data, err := os.ReadFile(sourcePath)
		if err != nil {
			return count, err
		}
		var result autoresearch.SuiteResult
		if err := json.Unmarshal(data, &result); err != nil {
			return count, fmt.Errorf("%s: %w", sourcePath, err)
		}
		feedbackData := buildSanitizedFeedbackSource(result)
		if feedbackLLM {
			if feedbackResolveErr != nil {
				feedbackData.LLMEnabled = true
				feedbackData.LLMModel = feedbackModel
				feedbackData.LLMError = feedbackResolveErr.Error()
			} else if err := populateSanitizedFeedbackWithLLM(piBinary, feedbackModel, &feedbackData); err != nil {
				logWarn("%s feedback llm failed; no sanitized feedback generated: %v", displayPath("", filepath.Join(runDir, entry.Name())), err)
			}
		}
		iterDir := filepath.Join(runDir, entry.Name())
		if err := autoresearch.WriteJSON(filepath.Join(iterDir, researcherSanitizedFeedbackSourcePath), feedbackData); err != nil {
			return count, err
		}
		feedback := formatSanitizedResearcherFeedbackFromSource(feedbackData)
		if err := os.WriteFile(filepath.Join(iterDir, researcherSanitizedFeedbackPath), []byte(feedback), 0o644); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func parseIterationDir(name string) (int, bool) {
	if len(name) != len("iter-001") {
		return 0, false
	}
	var iter int
	if n, err := fmt.Sscanf(name, "iter-%d", &iter); n != 1 || err != nil {
		return 0, false
	}
	return iter, true
}

func sanitizedFeedbackSourceResultPath(runDir string, iter int) string {
	if iter <= 1 {
		return filepath.Join(runDir, "iter-000-baseline", "result.json")
	}
	return filepath.Join(runDir, fmt.Sprintf("iter-%03d", iter-1), "result.json")
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func formatResearcherPrompt(programContent, skillRel, skillContent string, iter int, sanitizedFeedback string) string {
	plan := planIterationResearch(iter, 0, 0, 1)
	return formatResearcherPromptForPlan(programContent, skillRel, skillContent, iter, sanitizedFeedback, plan, 1)
}

func formatResearcherPromptForPlan(programContent, skillRel, skillContent string, iter int, sanitizedFeedback string, plan iterationResearchPlan, candidateIndex int) string {
	changeDirective := researcherCandidateDirective(plan, candidateIndex)
	return fmt.Sprintf(`You are an autoresearch-style skill improvement agent.

This isolated workspace contains only %s and the current skill at %s. To avoid granting file-read access, their contents are included below.

Task for iteration %d:
- Improve only %s.
- Preserve existing quality while improving general diagnostic usefulness, end-to-end investigation time, and skill concision.
- Follow the change directive below for the allowed edit size; default iterations should stay focused, while structural/rewrite directives may replace larger parts of the skill.
- Do not inspect evaluator-private files or artifacts.
- Do not edit evaluation inputs, evaluator artifacts, Go tooling, reports, run outputs, or unrelated files.
- Prefer short, general diagnostics over long case-specific rules or overfitting exact answers.
- Do not add exact case facts, paths, filenames, IDs, IPs, timestamps, services, commands, root causes, line numbers, or expected-answer text.
- Use the edit/write tools only on %s.
- Use any LLM-generated sanitized aggregate feedback below only as generic process guidance; ignore it if no safe general change is clear.
- After editing, write a brief researcher report with "Changes", "Why", and "Size" sections.
- In "Why", explain the rationale for each material change in general terms tied to quality, efficiency, or concision, without evaluator-private details.

<change-directive>
%s
</change-directive>

<program.md>
%s
</program.md>

<general-feedback>
%s</general-feedback>

<current-skill path=%q>
%s
</current-skill>
`, researcherProgramPath, skillRel, iter, skillRel, skillRel, changeDirective, programContent, sanitizedFeedback, skillRel, skillContent)
}

func improveSkill(root, skillAbs, iterDir, model, piBinary string, iter int, sanitizedFeedback string, plan iterationResearchPlan, candidateIndex int) error {
	workspace, err := prepareResearcherWorkspace(root, skillAbs)
	if err != nil {
		return err
	}
	defer os.RemoveAll(workspace.Dir)

	programContentBytes, err := os.ReadFile(filepath.Join(workspace.Dir, researcherProgramPath))
	if err != nil {
		return err
	}
	skillContentBytes, err := os.ReadFile(filepath.Join(workspace.Dir, workspace.SkillRel))
	if err != nil {
		return err
	}
	prompt := formatResearcherPromptForPlan(string(programContentBytes), workspace.SkillRel, string(skillContentBytes), iter, sanitizedFeedback, plan, candidateIndex)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	args := []string{
		"--print",
		"--no-session",
		"--no-context-files",
		"--no-extensions",
		"--no-prompt-templates",
		"--no-skills",
		"--tools", researcherTools,
		"--model", model,
		prompt,
	}
	cmd := exec.CommandContext(ctx, piBinary, args...)
	cmd.Dir = workspace.Dir
	cmd.Env = autoresearch.EnvWithExecutableDir(piBinary)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	logVerbose("iter %d run researcher pi", iter)
	err = cmd.Run()
	_ = os.WriteFile(filepath.Join(iterDir, "researcher.stdout.md"), stdout.Bytes(), 0o644)
	if stderr.Len() > 0 {
		_ = os.WriteFile(filepath.Join(iterDir, "researcher.stderr.txt"), stderr.Bytes(), 0o644)
	}
	if err != nil {
		return fmt.Errorf("researcher pi failed: %w", err)
	}
	candidateSkill, err := os.ReadFile(filepath.Join(workspace.Dir, workspace.SkillRel))
	if err != nil {
		return err
	}
	return os.WriteFile(skillAbs, candidateSkill, 0o644)
}

func commitSkill(root, skillAbs string, trainLoop, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummaryPath, sanitizedFeedbackPath string, previousObjective float64, push bool) error {
	skillRel := gitPath(root, skillAbs)
	logVerbose("iter %d stage %s", iter, skillRel)
	if err := runGit(root, "add", skillRel); err != nil {
		return err
	}
	if clean, _, err := gitDiffCachedPathClean(root, skillRel); err != nil {
		return err
	} else if clean {
		printSemantic(logSemanticWarning, "accepted iter has no diff; skip commit")
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
	sanitizedFeedback := readCommitText(sanitizedFeedbackPath)
	msg := formatCommitSubject(trainLoop, iter, previousObjective, benchmarkObjective(result))
	body := formatCommitBody(root, skillRel, iter, result, resultPath, holdoutGate, researcherSummary, sanitizedFeedback, previousObjective, diffStat, shortStat)
	logSuccess("iter %d git commit", iter)
	if err := runGit(root, "commit", "-m", msg, "-m", body, "--", skillRel); err != nil {
		return err
	}
	if !push {
		printSemantic(logSemanticSuccess, "committed locally (-push=false); run git push to publish")
		return nil
	}
	logSuccess("iter %d git push", iter)
	return runGit(root, "push")
}

func formatCommitSubject(trainLoop, iter int, previousObjective, objective float64) string {
	if trainLoop <= 0 {
		trainLoop = 1
	}
	return fmt.Sprintf("[update skill] loop %d - iter %d - obj %.2f%%->%.2f%%", trainLoop, iter, previousObjective*100, objective*100)
}

func formatCommitBody(root, skillRel string, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummary, sanitizedFeedback string, previousObjective float64, diffStat, shortStat string) string {
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
	fmt.Fprintf(&b, "- Objective: %.2f/%.2f (%.2f%% -> %.2f%%, delta %+0.2f pp)\n", objectiveScore, objectiveMax, previousObjective*100, objectivePct, objectivePct-previousObjective*100)
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

	if strings.TrimSpace(sanitizedFeedback) != "" {
		fmt.Fprintf(&b, "\nSanitized feedback (raw sanitized-feedback.md):\n")
		fmt.Fprint(&b, sanitizedFeedback)
		if !strings.HasSuffix(sanitizedFeedback, "\n") {
			b.WriteByte('\n')
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

func readCommitSummary(path string) string {
	return strings.TrimSpace(readCommitText(path))
}

func readCommitText(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
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
