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
		iterations       = flag.Int("iters", 3, "maximum improvement iterations")
		casesPath        = flag.String("cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "benchmark suite")
		skillPath        = flag.String("skill", "auto-improve-skills/skills/remote-host-diagnostics/SKILL.md", "skill file to improve")
		model            = flag.String("model", defaultModel, "pi model for researcher and benchmark agents")
		piBinary         = flag.String("pi", "pi", "pi executable")
		runDir           = flag.String("run-dir", "", "directory for this training run")
		minDelta         = flag.Float64("min-delta", 0.005, "minimum normalized objective improvement to accept")
		qualityTolerance = flag.Float64("quality-tolerance", 0.01, "maximum allowed quality drop from the best seen quality")
		limit            = flag.Int("limit", 0, "run at most N benchmark cases per iteration (0 = all)")
		judge            = flag.Bool("judge", false, "enable skillbench LLM-as-judge scoring")
		push             = flag.Bool("push", true, "push accepted skill commits to the current branch; set -push=false to keep commits local")
		dryRun           = flag.Bool("dry-run", false, "run benchmark and researcher but do not commit/revert")
		allowDirty       = flag.Bool("allow-dirty", false, "allow starting with unrelated uncommitted changes")
	)
	flag.Parse()

	if err := run(*iterations, *casesPath, *skillPath, *model, *piBinary, *runDir, *minDelta, *qualityTolerance, *limit, *judge, *push, *dryRun, *allowDirty); err != nil {
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

func run(iterations int, casesPath, skillPath, model, piBinary, runDir string, minDelta, qualityTolerance float64, limit int, judge, push, dryRun, allowDirty bool) error {
	if qualityTolerance < 0 {
		return fmt.Errorf("-quality-tolerance must be non-negative")
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
	baseline, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, filepath.Join(runDir, "iter-000-baseline"), limit, judge)
	if err != nil {
		return err
	}
	bestObjective := benchmarkObjective(baseline)
	bestQuality := benchmarkQuality(baseline)
	qualityFloor := bestQuality - qualityTolerance
	bestPath := filepath.Join(runDir, "iter-000-baseline", "result.json")
	printSemantic(logSemanticSummary, "baseline quality: %.2f%% objective: %.2f%% (%s)", bestQuality*100, bestObjective*100, bestPath)

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
		candidate, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, iterDir, limit, judge)
		if dryRun {
			logDryRun("iteration %d/%d: restoring original skill after dry-run benchmark", iter, iterations)
			if restoreErr := os.WriteFile(skillAbs, original, 0o644); restoreErr != nil && err == nil {
				err = restoreErr
			}
		}
		if err != nil {
			return err
		}
		logStep("iteration %d/%d: evaluating candidate", iter, iterations)
		candidatePath := filepath.Join(iterDir, "result.json")
		candidateObjective := benchmarkObjective(candidate)
		candidateQuality := benchmarkQuality(candidate)
		delta := candidateObjective - bestObjective
		qualityOK := candidateQuality >= qualityFloor
		if qualityOK && delta >= minDelta {
			printSemantic(logSemanticSuccess, "iteration %d quality: %.2f%% objective: %.2f%% (delta %.2f%%)", iter, candidateQuality*100, candidateObjective*100, delta*100)
		} else {
			printSemantic(logSemanticWarning, "iteration %d quality: %.2f%% objective: %.2f%% (delta %.2f%%)", iter, candidateQuality*100, candidateObjective*100, delta*100)
		}
		if qualityOK && delta >= minDelta {
			if dryRun {
				logSuccess("iteration %d/%d: accepted in dry-run", iter, iterations)
				printSemantic(logSemanticDryRun, "dry-run: would accept iteration %d and commit %s (candidate saved in %s)", iter, skillAbs, filepath.Join(iterDir, "candidate.SKILL.md"))
			} else {
				logSuccess("iteration %d/%d: accepted; committing skill change", iter, iterations)
				if err := commitSkill(root, skillAbs, iter, candidate, candidatePath, filepath.Join(iterDir, "researcher.stdout.md"), delta, push); err != nil {
					return err
				}
			}
			bestObjective = candidateObjective
			if candidateQuality > bestQuality {
				bestQuality = candidateQuality
				qualityFloor = bestQuality - qualityTolerance
			}
			bestPath = candidatePath
		} else {
			if !qualityOK {
				printSemantic(logSemanticWarning, "iteration %d rejected: quality %.2f%% is below floor %.2f%%", iter, candidateQuality*100, qualityFloor*100)
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
	return nil
}

func runBenchmark(root, casesAbs, skillAbs, model, piBinary, outDir string, limit int, judge bool) (autoresearch.SuiteResult, error) {
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

func commitSkill(root, skillAbs string, iter int, result autoresearch.SuiteResult, resultPath, researcherSummaryPath string, delta float64, push bool) error {
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
	body := formatCommitBody(root, skillRel, iter, result, resultPath, researcherSummary, delta, diffStat, shortStat)
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

func formatCommitBody(root, skillRel string, iter int, result autoresearch.SuiteResult, resultPath, researcherSummary string, delta float64, diffStat, shortStat string) string {
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
	cfg := result.ObjectiveConfig
	if cfg.QualityWeight+cfg.DurationWeight+cfg.SkillSizeWeight > 0 {
		fmt.Fprintf(&b, "- Objective config: quality=%.2f duration=%.2f skill_size=%.2f; duration budget/hard=%.0fs/%.0fs; skill-size target/hard=%d/%d tokens\n",
			cfg.QualityWeight, cfg.DurationWeight, cfg.SkillSizeWeight, cfg.DurationBudgetSeconds, cfg.DurationHardLimitSeconds, cfg.SkillSizeTargetTokens, cfg.SkillSizeHardLimitTokens)
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
		failed = append(failed, fmt.Sprintf("%s: 0/%.1f", detail, criterion.Max))
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
