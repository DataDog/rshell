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
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

const (
	defaultModel           = "openai-codex/gpt-5.5"
	defaultLoopCount       = 1
	defaultParallelRepeats = 3
	defaultParallelCases   = 3
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
	skilltrainLogPrefix    = "skilltrain"
	skilltrainLogSeparator = " | "
	commandOutputLimit     = 64 * 1024

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
	flag.StringVar(&cfg.casesPath, "cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "benchmark suite")
	flag.StringVar(&cfg.skillPath, "skill", "auto-improve-skills/skills/remote-host-diagnostics/SKILL.md", "skill file to improve")
	flag.StringVar(&cfg.model, "model", defaultModel, "pi model for researcher and benchmark agents")
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
	flag.BoolVar(&cfg.verbose, "verbose", false, "show detailed per-step logs and stream nested skillbench output")
	flag.Parse()

	setSkilltrainVerbose(cfg.verbose)
	if err := runLoop(*loopCount, cfg); err != nil {
		logError("%v", err)
		os.Exit(1)
	}
}

type trainConfig struct {
	iterations              int
	casesPath               string
	skillPath               string
	model                   string
	piBinary                string
	runDir                  string
	minDelta                float64
	qualityTolerance        float64
	holdoutCasesPath        string
	holdoutQualityTolerance float64
	repeats                 int
	parallelRepeats         int
	parallelCases           int
	limit                   int
	judge                   bool
	parallelSuites          bool
	push                    bool
	dryRun                  bool
	allowDirty              bool
	verbose                 bool
	trainLoop               int
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
	return run(cfg.trainLoop, cfg.iterations, cfg.casesPath, cfg.skillPath, cfg.model, cfg.piBinary, cfg.runDir, cfg.minDelta, cfg.qualityTolerance, cfg.holdoutCasesPath, cfg.holdoutQualityTolerance, cfg.repeats, cfg.parallelRepeats, cfg.parallelCases, cfg.limit, cfg.judge, cfg.parallelSuites, cfg.push, cfg.dryRun, cfg.allowDirty)
}

func run(trainLoop, iterations int, casesPath, skillPath, model, piBinary, runDir string, minDelta, qualityTolerance float64, holdoutCasesPath string, holdoutQualityTolerance float64, repeats, parallelRepeats, parallelCases, limit int, judge, parallelSuites, push, dryRun, allowDirty bool) error {
	if qualityTolerance < 0 {
		return fmt.Errorf("-quality-tolerance must be non-negative")
	}
	if holdoutQualityTolerance < 0 {
		holdoutQualityTolerance = qualityTolerance
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

	logBenchmark("baseline reps=%d rpar=%d cpar=%d", repeats, repeatParallelism(parallelRepeats, repeats), parallelCases)
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

	for iter := 1; iter <= iterations; iter++ {
		logVerbose("iter %d/%d prepare workspace", iter, iterations)
		iterDir := filepath.Join(runDir, fmt.Sprintf("iter-%03d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return err
		}
		var original []byte
		if dryRun {
			logDryRunVerbose("iter %d/%d snapshot skill", iter, iterations)
			var err error
			original, err = os.ReadFile(skillAbs)
			if err != nil {
				return err
			}
		}
		transcriptPath := filepath.Join(iterDir, "researcher.stdout.md")
		logStep("iter %d/%d edit -> %s", iter, iterations, displayRunPath(runDir, transcriptPath))
		if err := improveSkill(root, skillAbs, casesAbs, bestPath, iterDir, model, piBinary, iter, qualityTolerance); err != nil {
			return err
		}
		if dryRun {
			logDryRunVerbose("iter %d/%d save candidate", iter, iterations)
			if candidateSkill, err := os.ReadFile(skillAbs); err == nil {
				_ = os.WriteFile(filepath.Join(iterDir, "candidate.SKILL.md"), candidateSkill, 0o644)
			}
		}
		restoreDryRun := func() error {
			if !dryRun {
				return nil
			}
			logDryRunVerbose("iter %d/%d restore original skill", iter, iterations)
			return os.WriteFile(skillAbs, original, 0o644)
		}
		logBenchmark("iter %d/%d candidate", iter, iterations)
		candidate, speculativeHoldout, speculativeHoldoutRan, speculativeHoldoutErr, err := runCandidateBenchmarks(root, casesAbs, holdoutCasesAbs, skillAbs, model, piBinary, iterDir, limit, judge, repeats, parallelRepeats, parallelCases, parallelSuites)
		if err != nil {
			if restoreErr := restoreDryRun(); restoreErr != nil {
				return restoreErr
			}
			return err
		}
		logVerbose("iter %d/%d evaluate", iter, iterations)
		candidatePath := filepath.Join(iterDir, "result.json")
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

		if publicOK && holdoutOK {
			acceptedIterations++
			if dryRun {
				logSuccess("iter %d/%d accepted (dry-run)", iter, iterations)
				printSemantic(logSemanticDryRun, "accept iter %d; would commit %s (candidate %s)", iter, displayPath(root, skillAbs), displayRunPath(runDir, filepath.Join(iterDir, "candidate.SKILL.md")))
			} else {
				logSuccess("iter %d/%d accepted; commit", iter, iterations)
				if err := commitSkill(root, skillAbs, trainLoop, iter, candidate, candidatePath, holdoutGate, filepath.Join(iterDir, "researcher.stdout.md"), bestObjective, push); err != nil {
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
				printSemantic(logSemanticDryRun, "reject iter %d; would revert %s (candidate %s)", iter, displayPath(root, skillAbs), displayRunPath(runDir, filepath.Join(iterDir, "candidate.SKILL.md")))
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
	if st, err := os.Stat(filepath.Join(root, "rshell")); err == nil && st.Mode()&0o111 != 0 {
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
	return nil
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
		logBenchmarkVerboseCtx(suiteLogContext(suiteLabel), "%dx rpar=%d", repeats, parallelism)
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
	args := []string{
		"run", "./auto-improve-skills/cmd/skillbench",
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
	cmd.Dir = root
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
- Do not edit benchmark cases, fake logs, Go tooling, or reports.
- Prefer short, general diagnostic over long case-specific rules or overfitting exact answers.
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
	logVerbose("iter %d run researcher pi", iter)
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

func commitSkill(root, skillAbs string, trainLoop, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummaryPath string, previousObjective float64, push bool) error {
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
	msg := formatCommitSubject(trainLoop, iter, previousObjective, benchmarkObjective(result))
	body := formatCommitBody(root, skillRel, iter, result, resultPath, holdoutGate, researcherSummary, previousObjective, diffStat, shortStat)
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

func formatCommitSubject(trainLoop, iter int, previousObjective, newObjective float64) string {
	if trainLoop <= 0 {
		trainLoop = 1
	}
	return fmt.Sprintf("[update skill] train loop %d|iter %d|obj %.2f%%->%.2f%%", trainLoop, iter, previousObjective*100, newObjective*100)
}

func formatCommitBody(root, skillRel string, iter int, result autoresearch.SuiteResult, resultPath string, holdoutGate *benchmarkGate, researcherSummary string, previousObjective float64, diffStat, shortStat string) string {
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
