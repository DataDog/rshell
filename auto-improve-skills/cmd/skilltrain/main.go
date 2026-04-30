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
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

const defaultModel = "openai-codex/gpt-5.5"

func main() {
	var (
		iterations = flag.Int("iters", 3, "maximum improvement iterations")
		casesPath  = flag.String("cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "benchmark suite")
		skillPath  = flag.String("skill", "auto-improve-skills/skills/remote-host-diagnostics/SKILL.md", "skill file to improve")
		model      = flag.String("model", defaultModel, "pi model for researcher and benchmark agents")
		piBinary   = flag.String("pi", "pi", "pi executable")
		runDir     = flag.String("run-dir", "", "directory for this training run")
		minDelta   = flag.Float64("min-delta", 0.01, "minimum normalized-score improvement to accept")
		limit      = flag.Int("limit", 0, "run at most N benchmark cases per iteration (0 = all)")
		judge      = flag.Bool("judge", false, "enable skillbench LLM-as-judge scoring")
		dryRun     = flag.Bool("dry-run", false, "run benchmark and researcher but do not commit/revert")
		allowDirty = flag.Bool("allow-dirty", false, "allow starting with unrelated uncommitted changes")
	)
	flag.Parse()

	if err := run(*iterations, *casesPath, *skillPath, *model, *piBinary, *runDir, *minDelta, *limit, *judge, *dryRun, *allowDirty); err != nil {
		fmt.Fprintf(os.Stderr, "skilltrain: %v\n", err)
		os.Exit(1)
	}
}

func run(iterations int, casesPath, skillPath, model, piBinary, runDir string, minDelta float64, limit int, judge, dryRun, allowDirty bool) error {
	root, err := autoresearch.RepoRoot()
	if err != nil {
		return err
	}
	resolvedPI, err := autoresearch.ResolvePI(piBinary)
	if err != nil {
		return err
	}
	piBinary = resolvedPI
	casesAbs := autoresearch.AbsFromRoot(root, casesPath)
	skillAbs := autoresearch.AbsFromRoot(root, skillPath)
	if runDir == "" {
		runDir = filepath.Join(root, "auto-improve-skills", "runs", "train-"+time.Now().UTC().Format("20060102T150405Z"))
	} else {
		runDir = autoresearch.AbsFromRoot(root, runDir)
	}
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return err
	}
	if !allowDirty && !dryRun {
		if dirty, status, err := gitDirty(root); err != nil {
			return err
		} else if dirty {
			return fmt.Errorf("working tree is dirty; commit or stash first, or pass -allow-dirty. Status:\n%s", status)
		}
	}

	fmt.Printf("skilltrain run dir: %s\n", runDir)
	baseline, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, filepath.Join(runDir, "iter-000-baseline"), limit, judge)
	if err != nil {
		return err
	}
	bestScore := baseline.NormalizedScore
	bestPath := filepath.Join(runDir, "iter-000-baseline", "result.json")
	fmt.Printf("baseline score: %.2f%% (%s)\n", bestScore*100, bestPath)

	for iter := 1; iter <= iterations; iter++ {
		iterDir := filepath.Join(runDir, fmt.Sprintf("iter-%03d", iter))
		if err := os.MkdirAll(iterDir, 0o755); err != nil {
			return err
		}
		var original []byte
		if dryRun {
			var err error
			original, err = os.ReadFile(skillAbs)
			if err != nil {
				return err
			}
		}
		if err := improveSkill(root, skillAbs, casesAbs, bestPath, iterDir, model, piBinary, iter); err != nil {
			return err
		}
		if dryRun {
			if candidateSkill, err := os.ReadFile(skillAbs); err == nil {
				_ = os.WriteFile(filepath.Join(iterDir, "candidate.SKILL.md"), candidateSkill, 0o644)
			}
		}
		candidate, err := runBenchmark(root, casesAbs, skillAbs, model, piBinary, iterDir, limit, judge)
		if dryRun {
			if restoreErr := os.WriteFile(skillAbs, original, 0o644); restoreErr != nil && err == nil {
				err = restoreErr
			}
		}
		if err != nil {
			return err
		}
		candidatePath := filepath.Join(iterDir, "result.json")
		delta := candidate.NormalizedScore - bestScore
		fmt.Printf("iteration %d score: %.2f%% (delta %.2f%%)\n", iter, candidate.NormalizedScore*100, delta*100)
		if delta >= minDelta {
			if dryRun {
				fmt.Printf("dry-run: would accept iteration %d and commit %s (candidate saved in %s)\n", iter, skillAbs, filepath.Join(iterDir, "candidate.SKILL.md"))
			} else {
				if err := commitSkill(root, skillAbs, iter, candidate.NormalizedScore, delta); err != nil {
					return err
				}
			}
			bestScore = candidate.NormalizedScore
			bestPath = candidatePath
		} else {
			if dryRun {
				fmt.Printf("dry-run: would reject iteration %d and revert %s (candidate saved in %s)\n", iter, skillAbs, filepath.Join(iterDir, "candidate.SKILL.md"))
			} else if err := gitCheckout(root, skillAbs); err != nil {
				return err
			}
		}
	}
	fmt.Printf("best score: %.2f%% (%s)\n", bestScore*100, bestPath)
	return nil
}

func runBenchmark(root, casesAbs, skillAbs, model, piBinary, outDir string, limit int, judge bool) (autoresearch.SuiteResult, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return autoresearch.SuiteResult{}, err
	}
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

func improveSkill(root, skillAbs, casesAbs, bestResultPath, iterDir, model, piBinary string, iter int) error {
	prompt := fmt.Sprintf(`You are an autoresearch-style skill improvement agent.

Read auto-improve-skills/program.md, the current skill at %s, the benchmark suite at %s, and the best benchmark result at %s.

Task for iteration %d:
- Improve only %s.
- Optimize final answer quality on the benchmark cases.
- Keep the skill safe and local: it must use ./rshell through bash and must not use Datadog remote-action tools.
- Do not edit benchmark cases, fake logs, Go tooling, or reports.
- Prefer clear diagnostic workflow instructions over overfitting exact answers.
- After editing, briefly summarize what you changed.
`, skillAbs, casesAbs, bestResultPath, iter, skillAbs)
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

func commitSkill(root, skillAbs string, iter int, score, delta float64) error {
	if err := runGit(root, "add", skillAbs); err != nil {
		return err
	}
	if clean, _, err := gitDiffCachedClean(root); err != nil {
		return err
	} else if clean {
		fmt.Println("accepted iteration had no staged diff; skipping commit")
		return nil
	}
	msg := fmt.Sprintf("auto-improve remote-host-diagnostics iter %d", iter)
	body := fmt.Sprintf("Score: %.2f%%\nDelta: %.2f%%", score*100, delta*100)
	return runGit(root, "commit", "-m", msg, "-m", body)
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
