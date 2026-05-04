// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// IterationResult records the outcome of one loop iteration.
type IterationResult struct {
	Iteration      int
	ReviewFindings int // new threads opened by the code-review step
	Unresolved     int // unresolved threads at end of iteration
	CIClean        bool
}

// run is the main entry point after flag parsing.
func run(ctx context.Context, cfg Config, prRef string) error {
	// Create a temp log file so the user can tail -f it for full details.
	logFile, err := os.CreateTemp("", "review-fix-loop-*.log")
	if err != nil {
		return fmt.Errorf("create log file: %w", err)
	}
	defer logFile.Close()

	out := io.MultiWriter(os.Stdout, logFile)
	fmt.Fprintf(out, "%s\n", bold("Logging to: "+logFile.Name()))
	fmt.Fprintf(out, "Model:      %s\n\n", cfg.Model)

	// Step 1: identify the PR
	pr, err := identifyPR(cfg.WorkDir, prRef)
	if err != nil {
		return fmt.Errorf("identify PR: %w", err)
	}
	fmt.Fprintf(out, "%s\n", bold(fmt.Sprintf("PR #%d  %s", pr.Number, pr.URL)))
	fmt.Fprintf(out, "Branch: %s → %s\n", pr.Head, pr.Base)

	// Ensure we are on the PR head branch before making any changes. Fetch the
	// branch from origin first so that the local ref is up-to-date even when
	// started from a different branch (e.g. from main with a PR number arg).
	if err := ensurePRHead(cfg.WorkDir, pr.Head); err != nil {
		return fmt.Errorf("checkout PR head: %w", err)
	}
	fmt.Fprintf(out, "Checked out branch: %s\n", pr.Head)

	agent := newAgent(cfg, out, logFile, os.Stdout)
	var results []IterationResult
	successCount := 0
	converged := false

	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		fmt.Fprintf(out, "\n%s\n", boldBlue(fmt.Sprintf(
			"━━━ Iteration %d / %d   (clean streak: %d/%d) ━━━",
			iter, cfg.MaxIterations, successCount, cfg.TargetSuccess,
		)))

		// Count threads before review so we can detect new findings below.
		beforeReview, beforeErr := countUnresolvedThreads(cfg.WorkDir, pr)
		if beforeErr != nil {
			log.Printf("[pre-review threads] warning: %v", beforeErr)
		}

		// 2A1 (self-review) + 2A2 (trigger codex) in parallel
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			userMsg := fmt.Sprintf("Review PR #%d. Focus on the diff vs the base branch.", pr.Number)
			if err := agent.Run(ctx, "code-review", loadSkill(cfg.WorkDir, "code_review", fmt.Sprintf("#%d", pr.Number)), userMsg); err != nil {
				log.Printf("[code-review] error: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			triggerCodex(cfg.WorkDir, pr.Number)
		}()
		wg.Wait()

		// Count threads after review to measure new findings.
		afterReview, afterErr := countUnresolvedThreads(cfg.WorkDir, pr)
		if afterErr != nil {
			log.Printf("[post-review threads] warning: %v", afterErr)
		}
		reviewFindings := afterReview - beforeReview
		if reviewFindings < 0 {
			reviewFindings = 0
		}

		// Post a brief iteration comment so the outcome is visible on the PR
		postComment(cfg.WorkDir, pr.Number,
			fmt.Sprintf("[AI Generated] Self-review iteration %d complete — see inline review comments for findings.", iter))

		// Pull latest before applying fixes to avoid working on stale code
		if err := gitPullRebase(cfg.WorkDir, pr.Head); err != nil {
			log.Printf("[pre-fix pull] warning: %v", err)
		}

		// 2B: address PR review comments
		addrMsg := fmt.Sprintf(
			"Address all unresolved review comments on PR #%d. Prefix every commit message with \"[iter %d]\".",
			pr.Number, iter,
		)
		if err := agent.Run(ctx, "address-pr-comments",
			loadSkill(cfg.WorkDir, "address_pr_comments", fmt.Sprintf("#%d", pr.Number)), addrMsg); err != nil {
			log.Printf("[address-pr-comments] error: %v", err)
		}

		// 2C: fix CI failures
		ciMsg := fmt.Sprintf(
			"Fix any CI failures on PR #%d. Prefix every commit message with \"[iter %d]\".",
			pr.Number, iter,
		)
		if err := agent.Run(ctx, "fix-ci-tests",
			loadSkill(cfg.WorkDir, "fix_ci_tests", fmt.Sprintf("#%d", pr.Number)), ciMsg); err != nil {
			log.Printf("[fix-ci-tests] error: %v", err)
		}

		// 2D: sync branch
		if err := gitPullRebase(cfg.WorkDir, pr.Head); err != nil {
			log.Printf("[post-fix pull] warning: %v", err)
		}
		pushIfNeeded(cfg.WorkDir, pr.Head)

		// 2E: decide — purely structural signals, no comment body text
		unresolved, unresolvedErr := countUnresolvedThreads(cfg.WorkDir, pr)
		if unresolvedErr != nil {
			log.Printf("[unresolved threads] warning: %v", unresolvedErr)
		}
		ciClean, ciErr := allCIPassing(cfg.WorkDir, pr.Number)
		if ciErr != nil {
			log.Printf("[CI status] warning: %v", ciErr)
		}

		result := IterationResult{Iteration: iter, ReviewFindings: reviewFindings, Unresolved: unresolved, CIClean: ciClean}
		results = append(results, result)

		statusLine := fmt.Sprintf("→ findings=%d  unresolved=%d  ci_clean=%v", reviewFindings, unresolved, ciClean)
		// A clean iteration requires: no new review findings, no unresolved review
		// threads (from any prior iteration), and all CI checks passing.
		// If the thread-count or CI-status lookup failed, treat it as not clean
		// to avoid advancing the streak on unknown state.
		iterClean := reviewFindings == 0 && unresolved == 0 && ciClean && unresolvedErr == nil && ciErr == nil
		if iterClean {
			fmt.Fprintf(out, "\n  %s\n", boldGreen(statusLine))
		} else {
			fmt.Fprintf(out, "\n  %s\n", boldRed(statusLine))
		}

		if iterClean {
			successCount++
			fmt.Fprintf(out, "  %s\n", boldGreen(fmt.Sprintf("✓ clean (streak %d/%d)", successCount, cfg.TargetSuccess)))
			if successCount >= cfg.TargetSuccess {
				converged = true
				break
			}
		} else {
			successCount = 0
		}
	}

	// Step 4: final summary
	summary := buildSummary(pr, results, converged)
	fmt.Fprint(out, summary)
	postComment(cfg.WorkDir, pr.Number, summary)
	return nil
}

func triggerCodex(workDir string, prNumber int) {
	cmd := exec.Command("gh", "pr", "comment", fmt.Sprintf("%d", prNumber),
		"--body", "@codex review this PR")
	cmd.Dir = workDir
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[codex trigger] %s: %v", strings.TrimSpace(string(out)), err)
	}
}

func postComment(workDir string, prNumber int, body string) {
	cmd := exec.Command("gh", "pr", "comment", fmt.Sprintf("%d", prNumber), "--body", body)
	cmd.Dir = workDir
	_ = cmd.Run()
}

// ensurePRHead fetches and checks out the named branch from origin so that
// the tool always operates on the correct PR branch, even when invoked from
// an unrelated local branch.
func ensurePRHead(workDir, branch string) error {
	// Fetch the branch ref from origin.
	fetchCmd := exec.Command("git", "fetch", "origin", branch)
	fetchCmd.Dir = workDir
	if out, err := fetchCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch origin %s: %s: %w", branch, strings.TrimSpace(string(out)), err)
	}
	// Check out the branch (creates it tracking origin if it doesn't exist locally).
	checkoutCmd := exec.Command("git", "checkout", branch)
	checkoutCmd.Dir = workDir
	if out, err := checkoutCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git checkout %s: %s: %w", branch, strings.TrimSpace(string(out)), err)
	}
	// Reset to match origin to avoid stale local state.
	resetCmd := exec.Command("git", "reset", "--hard", "origin/"+branch)
	resetCmd.Dir = workDir
	if out, err := resetCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard origin/%s: %s: %w", branch, strings.TrimSpace(string(out)), err)
	}
	return nil
}

func gitPullRebase(workDir, branch string) error {
	cmd := exec.Command("git", "pull", "--rebase", "origin", branch)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull --rebase: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

func pushIfNeeded(workDir, branch string) {
	// Check for unpushed commits
	checkCmd := exec.Command("git", "log", "--oneline", "origin/"+branch+"..HEAD")
	checkCmd.Dir = workDir
	out, _ := checkCmd.Output()
	if len(strings.TrimSpace(string(out))) == 0 {
		return
	}
	pushCmd := exec.Command("git", "push", "origin", branch)
	pushCmd.Dir = workDir
	if pushOut, err := pushCmd.CombinedOutput(); err != nil {
		log.Printf("[push] %s: %v", strings.TrimSpace(string(pushOut)), err)
	}
}

func buildSummary(pr PRInfo, results []IterationResult, converged bool) string {
	status := "CLEAN"
	if !converged {
		status = "ITERATION_LIMIT_REACHED"
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "\n## Review-Fix Loop Summary\n\n")
	fmt.Fprintf(&sb, "- **PR**: #%d (%s)\n", pr.Number, pr.URL)
	fmt.Fprintf(&sb, "- **Iterations completed**: %d\n", len(results))
	fmt.Fprintf(&sb, "- **Final status**: %s\n\n", status)
	fmt.Fprintf(&sb, "### Iteration log\n\n")
	fmt.Fprintf(&sb, "| # | Review findings | Unresolved threads | CI |\n")
	fmt.Fprintf(&sb, "|---|-----------------|--------------------|---------|\n")
	for _, r := range results {
		ci := "Passing"
		if !r.CIClean {
			ci = "Failing"
		}
		fmt.Fprintf(&sb, "| %d | %d | %d | %s |\n", r.Iteration, r.ReviewFindings, r.Unresolved, ci)
	}
	return sb.String()
}
