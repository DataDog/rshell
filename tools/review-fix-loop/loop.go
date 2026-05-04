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
	Iteration  int
	Unresolved int
	CIClean    bool
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
	fmt.Fprintf(out, "%s\n\n", bold("Logging to: "+logFile.Name()))

	// Step 1: identify the PR
	pr, err := identifyPR(cfg.WorkDir, prRef)
	if err != nil {
		return fmt.Errorf("identify PR: %w", err)
	}
	fmt.Fprintf(out, "%s\n", bold(fmt.Sprintf("PR #%d  %s", pr.Number, pr.URL)))
	fmt.Fprintf(out, "Branch: %s → %s\n", pr.Head, pr.Base)

	agent := newAgent(cfg, out, logFile)
	var results []IterationResult
	successCount := 0
	converged := false

	for iter := 1; iter <= cfg.MaxIterations; iter++ {
		fmt.Fprintf(out, "\n%s\n", boldBlue(fmt.Sprintf(
			"━━━ Iteration %d / %d   (clean streak: %d/%d) ━━━",
			iter, cfg.MaxIterations, successCount, cfg.TargetSuccess,
		)))

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

		result := IterationResult{Iteration: iter, Unresolved: unresolved, CIClean: ciClean}
		results = append(results, result)

		statusLine := fmt.Sprintf("→ unresolved=%d  ci_clean=%v", unresolved, ciClean)
		if unresolved == 0 && ciClean {
			fmt.Fprintf(out, "\n  %s\n", boldGreen(statusLine))
		} else {
			fmt.Fprintf(out, "\n  %s\n", boldRed(statusLine))
		}

		if unresolved == 0 && ciClean {
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
	fmt.Fprintf(&sb, "| # | Unresolved threads | CI |\n")
	fmt.Fprintf(&sb, "|---|--------------------|---------|\n")
	for _, r := range results {
		ci := "Passing"
		if !r.CIClean {
			ci = "Failing"
		}
		fmt.Fprintf(&sb, "| %d | %d | %s |\n", r.Iteration, r.Unresolved, ci)
	}
	return sb.String()
}
