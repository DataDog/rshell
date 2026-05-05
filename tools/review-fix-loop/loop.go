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
	Iteration            int
	ReviewFindings       int  // new threads opened by the code-review step (informational)
	HighPriorityFindings bool // true if any P0/P1 found in self-review or Codex
	Unresolved           int  // total unresolved threads at end of iteration (informational)
	CIClean              bool
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

	// Verify that the local working tree is on the PR branch before running any
	// fix agents. If the user invoked the tool with a different PR while another
	// branch is checked out, edits and commits would land on the wrong branch.
	currentBranch, branchErr := currentGitBranch(cfg.WorkDir)
	if branchErr != nil {
		return fmt.Errorf("cannot determine current git branch: %w", branchErr)
	}
	if currentBranch != pr.Head {
		return fmt.Errorf(
			"current branch %q does not match PR branch %q — "+
				"please run `gh pr checkout %d` before starting the loop",
			currentBranch, pr.Head, pr.Number,
		)
	}
	fmt.Fprintf(out, "Working on local branch: %s\n", pr.Head)

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

		// 2A1 (self-review) + 2A2 (trigger codex) in parallel.
		// Only the first goroutine writes to the shared agent writers; triggerCodex
		// runs an external subprocess and does not touch agent state, so there is
		// no data race on the shared writers today.
		var (
			wg             sync.WaitGroup
			reviewAgentErr error
			reviewText     string
		)
		wg.Add(2)
		go func() {
			defer wg.Done()
			userMsg := fmt.Sprintf("Review PR #%d. Focus on the diff vs the base branch.", pr.Number)
			var err error
			reviewText, err = agent.Run(ctx, "code-review", loadSkill(cfg.WorkDir, "code_review", fmt.Sprintf("#%d", pr.Number)), userMsg)
			if err != nil {
				log.Printf("[code-review] error: %v", err)
				reviewAgentErr = err
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

		// 2B: address PR review comments
		addrMsg := fmt.Sprintf(
			"Address all unresolved review comments on PR #%d. Prefix every commit message with \"[iter %d]\".",
			pr.Number, iter,
		)
		var addrAgentErr error
		if _, err := agent.Run(ctx, "address-pr-comments",
			loadSkill(cfg.WorkDir, "address_pr_comments", fmt.Sprintf("#%d", pr.Number)), addrMsg); err != nil {
			log.Printf("[address-pr-comments] error: %v", err)
			addrAgentErr = err
		}

		// 2C: fix CI failures
		ciMsg := fmt.Sprintf(
			"Fix any CI failures on PR #%d. Prefix every commit message with \"[iter %d]\".",
			pr.Number, iter,
		)
		var fixCIAgentErr error
		if _, err := agent.Run(ctx, "fix-ci-tests",
			loadSkill(cfg.WorkDir, "fix_ci_tests", fmt.Sprintf("#%d", pr.Number)), ciMsg); err != nil {
			log.Printf("[fix-ci-tests] error: %v", err)
			fixCIAgentErr = err
		}

		// 2D: decide — P0/P1 badge detection for both self-review and Codex.
		// Only P0/P1 findings block the clean streak; P2/P3 are addressed but
		// do not reset it. Unresolved thread count is informational only.
		selfHighPriority := reviewHasHighPriorityFindings(reviewText)
		codexHighPriority, codexErr := codexHasHighPriorityFindings(cfg.WorkDir, pr)
		if codexErr != nil {
			log.Printf("[codex high-priority] warning: %v", codexErr)
		}
		highPriority := selfHighPriority || codexHighPriority

		unresolved, _ := countUnresolvedThreads(cfg.WorkDir, pr)
		ciClean, ciErr := allCIPassing(cfg.WorkDir, pr.Number)
		if ciErr != nil {
			log.Printf("[CI status] warning: %v", ciErr)
		}

		result := IterationResult{
			Iteration:            iter,
			ReviewFindings:       reviewFindings,
			HighPriorityFindings: highPriority,
			Unresolved:           unresolved,
			CIClean:              ciClean,
		}
		results = append(results, result)

		statusLine := fmt.Sprintf("→ high_priority=%v  findings=%d  unresolved=%d  ci_clean=%v",
			highPriority, reviewFindings, unresolved, ciClean)
		// A clean iteration requires no P0/P1 findings (self-review or Codex) and CI passing.
		// Agent errors are treated as non-clean: a failed review agent cannot be trusted.
		agentErr := reviewAgentErr != nil || addrAgentErr != nil || fixCIAgentErr != nil
		iterClean := !highPriority && ciClean && ciErr == nil && !agentErr
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
	fmt.Fprintf(&sb, "| # | P0/P1 findings | Total findings | Unresolved threads | CI |\n")
	fmt.Fprintf(&sb, "|---|----------------|----------------|--------------------|---------|\n")
	for _, r := range results {
		ci := "Passing"
		if !r.CIClean {
			ci = "Failing"
		}
		hp := "none"
		if r.HighPriorityFindings {
			hp = "YES"
		}
		fmt.Fprintf(&sb, "| %d | %s | %d | %d | %s |\n", r.Iteration, hp, r.ReviewFindings, r.Unresolved, ci)
	}
	return sb.String()
}

// reviewHasHighPriorityFindings returns true if the self-review agent output
// contains P0 or P1 badge markers. P2/P3 findings do not block the streak.
func reviewHasHighPriorityFindings(reviewText string) bool {
	return strings.Contains(reviewText, "/badge/P0-") || strings.Contains(reviewText, "/badge/P1-")
}
