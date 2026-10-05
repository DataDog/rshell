// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed investigator.md
var investigatorTemplate string

//go:embed findings.schema.json
var findingsSchema []byte

const (
	defaultOutputDir = ".correctness/runs"
	fingerprintLabel = "rshell-correctness-fingerprint"
)

var (
	targetPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._/-]{0,127}$`)
	refPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,255}$`)
	repoPattern   = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	markerPattern = regexp.MustCompile(`<!-- ` + fingerprintLabel + `:([a-f0-9]{64}) -->`)
)

type config struct {
	target       string
	depth        string
	ref          string
	baseRef      string
	repository   string
	outputDir    string
	codexBinary  string
	ghBinary     string
	publish      bool
	dryRun       bool
	keepWorktree bool
}

type scanResult struct {
	Summary            string        `json:"summary"`
	ComponentsExamined []string      `json:"components_examined"`
	HypothesesTested   int           `json:"hypotheses_tested"`
	Findings           []finding     `json:"findings"`
	CoverageNotes      coverageNotes `json:"coverage_notes"`
}

type finding struct {
	Classification           string       `json:"classification"`
	ContractID               string       `json:"contract_id"`
	Target                   string       `json:"target"`
	Title                    string       `json:"title"`
	Impact                   string       `json:"impact"`
	Description              string       `json:"description"`
	SourceReferences         []reference  `json:"source_references"`
	ImplementationReferences []reference  `json:"implementation_references"`
	Reproducer               reproducer   `json:"reproducer"`
	Expected                 outcome      `json:"expected"`
	Actual                   outcome      `json:"actual"`
	Verification             verification `json:"verification"`
	SuggestedTestLocation    string       `json:"suggested_test_location"`
}

type reference struct {
	Location string `json:"location"`
	Detail   string `json:"detail"`
}

type reproducer struct {
	Script       string   `json:"script"`
	FixtureSetup string   `json:"fixture_setup"`
	Commands     []string `json:"commands"`
}

type outcome struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode *int   `json:"exit_code"`
}

type verification struct {
	Runs          int    `json:"runs"`
	Reference     string `json:"reference"`
	TemporaryTest string `json:"temporary_test"`
}

type coverageNotes struct {
	FilesRead        []string `json:"files_read"`
	TestsReviewed    []string `json:"tests_reviewed"`
	CasesAttempted   []string `json:"cases_attempted"`
	AreasNotExamined []string `json:"areas_not_examined"`
}

type runMetadata struct {
	RunID       string
	Target      string
	Depth       string
	Commit      string
	BaseCommit  string
	Repository  string
	StartedAt   time.Time
	WorkflowURL string
}

type existingIssue struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	URL    string `json:"url"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

type issueLink struct {
	Action string
	URL    string
	Title  string
}

type issuePreview struct {
	Finding     finding
	Fingerprint string
	BodyPath    string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "correctness-scan:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	cfg, err := parseConfig(args, stderr)
	if err != nil {
		return err
	}

	repoRoot, err := commandOutput(ctx, "", "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("locate repository root: %w", err)
	}
	repoRoot = strings.TrimSpace(repoRoot)

	commit, err := resolveCommit(ctx, repoRoot, cfg.ref)
	if err != nil {
		return fmt.Errorf("resolve --ref %q: %w", cfg.ref, err)
	}
	baseCommit := ""
	if cfg.target == "changed" {
		baseCommit, err = resolveCommit(ctx, repoRoot, cfg.baseRef)
		if err != nil {
			return fmt.Errorf("resolve --base-ref %q: %w", cfg.baseRef, err)
		}
	}

	repository := cfg.repository
	if repository == "" {
		repository, err = repositoryFromRemote(ctx, repoRoot)
		if err != nil && cfg.publish {
			return err
		}
	}

	startedAt := time.Now().UTC()
	runID := startedAt.Format("20060102T150405.000000000Z") + "-" + commit[:12]
	outputRoot := cfg.outputDir
	if !filepath.IsAbs(outputRoot) {
		outputRoot = filepath.Join(repoRoot, outputRoot)
	}
	runDir := filepath.Join(outputRoot, runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return fmt.Errorf("create run directory: %w", err)
	}

	meta := runMetadata{
		RunID:       runID,
		Target:      cfg.target,
		Depth:       cfg.depth,
		Commit:      commit,
		BaseCommit:  baseCommit,
		Repository:  repository,
		StartedAt:   startedAt,
		WorkflowURL: workflowURL(),
	}
	prompt := buildPrompt(cfg, commit, baseCommit)
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte(prompt), 0o644); err != nil {
		return fmt.Errorf("write prompt: %w", err)
	}
	schemaPath := filepath.Join(runDir, "schema.json")
	if err := os.WriteFile(schemaPath, findingsSchema, 0o644); err != nil {
		return fmt.Errorf("write output schema: %w", err)
	}

	if cfg.dryRun {
		fmt.Fprintf(stdout, "Prepared correctness scan at %s\n", runDir)
		fmt.Fprintln(stdout, "Dry run requested; Codex and GitHub were not invoked.")
		return nil
	}

	tempRoot, err := os.MkdirTemp("", "rshell-correctness-scan-")
	if err != nil {
		return fmt.Errorf("create temporary directory: %w", err)
	}
	worktreeDir := filepath.Join(tempRoot, "repo")
	if _, err := commandOutput(ctx, repoRoot, "git", "worktree", "add", "--detach", worktreeDir, commit); err != nil {
		_ = os.RemoveAll(tempRoot)
		return fmt.Errorf("create detached worktree: %w", err)
	}
	defer cleanupWorktree(ctx, repoRoot, tempRoot, worktreeDir, cfg.keepWorktree, stderr)

	resultPath := filepath.Join(runDir, "findings.json")
	if err := invokeCodex(ctx, cfg, worktreeDir, runDir, schemaPath, resultPath, prompt); err != nil {
		return err
	}

	result, err := readResult(resultPath)
	if err != nil {
		return err
	}
	if err := validateResult(result, cfg); err != nil {
		return fmt.Errorf("validate Codex result: %w", err)
	}
	if err := writePrettyJSON(resultPath, result); err != nil {
		return err
	}

	reportPath := filepath.Join(runDir, "report.md")
	if err := os.WriteFile(reportPath, []byte(renderReport(meta, result)), 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	previews, err := prepareIssuePreviews(runDir, meta, result)
	if err != nil {
		return err
	}
	var links []issueLink
	if cfg.publish {
		links, err = publishFindings(ctx, cfg, repoRoot, meta, previews)
		if err != nil {
			_ = writeIssueLinks(filepath.Join(runDir, "issue-links.md"), links, err)
			return fmt.Errorf("publish findings: %w (report: %s)", err, reportPath)
		}
	}
	issueLinksPath := filepath.Join(runDir, "issue-links.md")
	if err := writeIssueLinks(issueLinksPath, links, nil); err != nil {
		return err
	}

	fmt.Fprintf(stdout, "Correctness scan complete: %s\n", reportPath)
	if cfg.publish {
		printIssueSummary(stdout, links)
	} else {
		fmt.Fprintln(stdout, "GitHub publication disabled by --no-publish.")
	}
	return nil
}

func parseConfig(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("correctness-scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cfg config
	var noPublish bool
	fs.StringVar(&cfg.target, "target", "changed", "builtin, shell feature, or changed")
	fs.StringVar(&cfg.depth, "depth", "quick", "scan depth: quick or standard")
	fs.StringVar(&cfg.ref, "ref", "HEAD", "committed Git ref to scan")
	fs.StringVar(&cfg.baseRef, "base-ref", "origin/main", "comparison ref used by target=changed")
	fs.StringVar(&cfg.repository, "repo", "", "GitHub owner/repository override")
	fs.StringVar(&cfg.outputDir, "output-dir", defaultOutputDir, "directory for run artifacts")
	fs.StringVar(&cfg.codexBinary, "codex-bin", "codex", "Codex CLI executable")
	fs.StringVar(&cfg.ghBinary, "gh-bin", "gh", "GitHub CLI executable")
	fs.BoolVar(&noPublish, "no-publish", false, "write issue previews without mutating GitHub")
	fs.BoolVar(&cfg.dryRun, "dry-run", false, "prepare artifacts without invoking Codex or GitHub")
	fs.BoolVar(&cfg.keepWorktree, "keep-worktree", false, "keep the temporary worktree for debugging")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	cfg.publish = !noPublish
	if !targetPattern.MatchString(cfg.target) || strings.Contains(cfg.target, "..") {
		return config{}, fmt.Errorf("invalid --target %q", cfg.target)
	}
	if cfg.depth != "quick" && cfg.depth != "standard" {
		return config{}, fmt.Errorf("invalid --depth %q: want quick or standard", cfg.depth)
	}
	if err := validateRef(cfg.ref); err != nil {
		return config{}, fmt.Errorf("invalid --ref: %w", err)
	}
	if err := validateRef(cfg.baseRef); err != nil {
		return config{}, fmt.Errorf("invalid --base-ref: %w", err)
	}
	if cfg.repository != "" && !repoPattern.MatchString(cfg.repository) {
		return config{}, fmt.Errorf("invalid --repo %q: want owner/repository", cfg.repository)
	}
	return cfg, nil
}

func validateRef(ref string) error {
	if !refPattern.MatchString(ref) || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return fmt.Errorf("unsupported Git ref %q", ref)
	}
	return nil
}

func resolveCommit(ctx context.Context, dir, ref string) (string, error) {
	out, err := commandOutput(ctx, dir, "git", "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	commit := strings.TrimSpace(out)
	if len(commit) != 40 {
		return "", fmt.Errorf("unexpected commit ID %q", commit)
	}
	return commit, nil
}

func repositoryFromRemote(ctx context.Context, repoRoot string) (string, error) {
	remote, err := commandOutput(ctx, repoRoot, "git", "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("resolve GitHub repository: %w", err)
	}
	repository, err := parseGitHubRepository(strings.TrimSpace(remote))
	if err != nil {
		return "", fmt.Errorf("resolve GitHub repository: %w", err)
	}
	return repository, nil
}

func parseGitHubRepository(remote string) (string, error) {
	var path string
	switch {
	case strings.HasPrefix(remote, "git@github.com:"):
		path = strings.TrimPrefix(remote, "git@github.com:")
	default:
		parsed, err := url.Parse(remote)
		if err != nil {
			return "", err
		}
		if parsed.Hostname() != "github.com" {
			return "", fmt.Errorf("origin is not github.com: %q", remote)
		}
		path = strings.TrimPrefix(parsed.Path, "/")
	}
	path = strings.TrimSuffix(path, ".git")
	if !repoPattern.MatchString(path) {
		return "", fmt.Errorf("cannot parse owner/repository from %q", remote)
	}
	return path, nil
}

func buildPrompt(cfg config, commit, baseCommit string) string {
	maxHypotheses := 3
	if cfg.depth == "standard" {
		maxHypotheses = 8
	}
	if baseCommit == "" {
		baseCommit = "not applicable"
	}
	prompt := strings.NewReplacer(
		"{{TARGET}}", cfg.target,
		"{{DEPTH}}", cfg.depth,
		"{{COMMIT}}", commit,
		"{{BASE_COMMIT}}", baseCommit,
		"{{MAX_HYPOTHESES}}", strconv.Itoa(maxHypotheses),
	).Replace(investigatorTemplate)
	if cfg.target == "changed" {
		prompt += fmt.Sprintf("\nFor the changed target, begin with `git diff --stat %s...%s` and map only the relevant changed paths to behavior contracts.\n", baseCommit, commit)
	}
	return prompt
}

func invokeCodex(ctx context.Context, cfg config, worktreeDir, runDir, schemaPath, resultPath, prompt string) error {
	timeout := 30 * time.Minute
	if cfg.depth == "standard" {
		timeout = 60 * time.Minute
	}
	scanCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	stdoutFile, err := os.Create(filepath.Join(runDir, "codex.stdout.log"))
	if err != nil {
		return fmt.Errorf("create Codex stdout log: %w", err)
	}
	defer stdoutFile.Close()
	stderrFile, err := os.Create(filepath.Join(runDir, "codex.stderr.log"))
	if err != nil {
		return fmt.Errorf("create Codex stderr log: %w", err)
	}
	defer stderrFile.Close()

	cmd := exec.CommandContext(scanCtx, cfg.codexBinary,
		"exec",
		"--sandbox", "workspace-write",
		"--approve-for-me",
		"--ephemeral",
		"--output-schema", schemaPath,
		"--output-last-message", resultPath,
		"-",
	)
	cmd.Dir = worktreeDir
	cmd.Env = codexEnvironment(os.Environ())
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile
	if err := cmd.Run(); err != nil {
		if errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("Codex scan exceeded %s; logs: %s", timeout, runDir)
		}
		return fmt.Errorf("Codex exited unsuccessfully: %w; logs: %s", err, runDir)
	}
	return nil
}

func readResult(path string) (scanResult, error) {
	f, err := os.Open(path)
	if err != nil {
		return scanResult{}, fmt.Errorf("open Codex result: %w", err)
	}
	defer f.Close()
	var result scanResult
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return scanResult{}, fmt.Errorf("decode Codex result: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return scanResult{}, errors.New("decode Codex result: trailing JSON data")
	}
	return result, nil
}

func validateResult(result scanResult, cfg config) error {
	maxHypotheses := 3
	if cfg.depth == "standard" {
		maxHypotheses = 8
	}
	if strings.TrimSpace(result.Summary) == "" {
		return errors.New("summary is empty")
	}
	if result.HypothesesTested < 0 || result.HypothesesTested > maxHypotheses {
		return fmt.Errorf("hypotheses_tested is %d, want 0..%d", result.HypothesesTested, maxHypotheses)
	}
	if len(result.Findings) > maxHypotheses {
		return fmt.Errorf("findings contains %d entries, exceeds hypothesis budget %d", len(result.Findings), maxHypotheses)
	}
	if len(result.Findings) > result.HypothesesTested {
		return fmt.Errorf("findings contains %d entries but only %d hypotheses were tested", len(result.Findings), result.HypothesesTested)
	}
	validContracts := map[string]bool{
		"BEH-001": true, "BEH-002": true, "BEH-003": true,
		"BEH-004": true, "BEH-005": true, "BEH-006": true,
		"BEH-007": true, "BEH-008": true, "BEH-009": true,
	}
	seenFingerprints := make(map[string]bool)
	for i, f := range result.Findings {
		if !validContracts[f.ContractID] {
			return fmt.Errorf("finding %d has invalid contract_id %q", i+1, f.ContractID)
		}
		if strings.TrimSpace(f.Target) == "" || strings.TrimSpace(f.Title) == "" || strings.TrimSpace(f.Description) == "" {
			return fmt.Errorf("finding %d is missing target, title, or description", i+1)
		}
		if f.Impact != "high" && f.Impact != "medium" && f.Impact != "low" {
			return fmt.Errorf("finding %d has invalid impact %q", i+1, f.Impact)
		}
		switch f.Classification {
		case "confirmed-defect":
			if f.Verification.Runs < 2 {
				return fmt.Errorf("finding %d confirmed-defect has %d verification runs, want at least 2", i+1, f.Verification.Runs)
			}
			if strings.TrimSpace(f.Reproducer.Script) == "" || len(f.Reproducer.Commands) == 0 {
				return fmt.Errorf("finding %d confirmed-defect lacks an executable reproducer", i+1)
			}
			if len(f.SourceReferences) == 0 || len(f.ImplementationReferences) == 0 {
				return fmt.Errorf("finding %d confirmed-defect lacks source or implementation references", i+1)
			}
			if outcomesEqual(f.Expected, f.Actual) {
				return fmt.Errorf("finding %d confirmed-defect has identical expected and actual outcomes", i+1)
			}
		case "documentation-ambiguity":
			if len(f.SourceReferences) < 2 {
				return fmt.Errorf("finding %d documentation-ambiguity needs at least two conflicting sources", i+1)
			}
		case "missing-coverage":
			if strings.TrimSpace(f.SuggestedTestLocation) == "" {
				return fmt.Errorf("finding %d missing-coverage lacks a suggested test location", i+1)
			}
		case "inconclusive":
		default:
			return fmt.Errorf("finding %d has invalid classification %q", i+1, f.Classification)
		}
		fingerprint := findingFingerprint(f)
		if seenFingerprints[fingerprint] {
			return fmt.Errorf("finding %d duplicates an earlier finding", i+1)
		}
		seenFingerprints[fingerprint] = true
	}
	return nil
}

func outcomesEqual(a, b outcome) bool {
	if a.Stdout != b.Stdout || a.Stderr != b.Stderr {
		return false
	}
	if a.ExitCode == nil || b.ExitCode == nil {
		return a.ExitCode == nil && b.ExitCode == nil
	}
	return *a.ExitCode == *b.ExitCode
}

func writePrettyJSON(path string, result scanResult) error {
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode normalized result: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write normalized result: %w", err)
	}
	return nil
}

func renderReport(meta runMetadata, result scanResult) string {
	var b strings.Builder
	fmt.Fprintln(&b, "# rshell correctness scan")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- Run: `%s`\n", meta.RunID)
	fmt.Fprintf(&b, "- Commit: `%s`\n", meta.Commit)
	if meta.BaseCommit != "" {
		fmt.Fprintf(&b, "- Comparison base: `%s`\n", meta.BaseCommit)
	}
	fmt.Fprintf(&b, "- Target: `%s`\n", meta.Target)
	fmt.Fprintf(&b, "- Depth: `%s`\n", meta.Depth)
	fmt.Fprintf(&b, "- Started: `%s`\n", meta.StartedAt.Format(time.RFC3339))
	if meta.WorkflowURL != "" {
		fmt.Fprintf(&b, "- Workflow: %s\n", meta.WorkflowURL)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "## Summary")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, result.Summary)
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Hypotheses tested: %d\n\n", result.HypothesesTested)
	writeStringList(&b, "Components examined", result.ComponentsExamined)

	classifications := []struct {
		value string
		title string
	}{
		{"confirmed-defect", "Confirmed defects"},
		{"documentation-ambiguity", "Documentation ambiguities"},
		{"missing-coverage", "Missing coverage"},
		{"inconclusive", "Inconclusive observations"},
	}
	for _, class := range classifications {
		fmt.Fprintf(&b, "## %s\n\n", class.title)
		count := 0
		for i, f := range result.Findings {
			if f.Classification != class.value {
				continue
			}
			count++
			writeFinding(&b, i+1, f)
		}
		if count == 0 {
			fmt.Fprintln(&b, "None.")
			fmt.Fprintln(&b)
		}
	}

	fmt.Fprintln(&b, "## Coverage notes")
	fmt.Fprintln(&b)
	writeStringList(&b, "Files read", result.CoverageNotes.FilesRead)
	writeStringList(&b, "Tests reviewed", result.CoverageNotes.TestsReviewed)
	writeStringList(&b, "Cases attempted", result.CoverageNotes.CasesAttempted)
	writeStringList(&b, "Areas not examined", result.CoverageNotes.AreasNotExamined)
	return b.String()
}

func writeFinding(b *strings.Builder, number int, f finding) {
	fmt.Fprintf(b, "### %d. %s\n\n", number, f.Title)
	fmt.Fprintf(b, "- Classification: `%s`\n", f.Classification)
	fmt.Fprintf(b, "- Contract: `%s`\n", f.ContractID)
	fmt.Fprintf(b, "- Target: `%s`\n", f.Target)
	fmt.Fprintf(b, "- Impact: `%s`\n\n", f.Impact)
	fmt.Fprintln(b, f.Description)
	fmt.Fprintln(b)
	writeReferences(b, "Contract sources", f.SourceReferences)
	writeReferences(b, "Implementation references", f.ImplementationReferences)
	if f.Reproducer.Script != "" || f.Reproducer.FixtureSetup != "" || len(f.Reproducer.Commands) > 0 {
		fmt.Fprintln(b, "#### Reproducer")
		fmt.Fprintln(b)
		if f.Reproducer.FixtureSetup != "" {
			fmt.Fprintln(b, "Fixture setup:")
			writeCodeBlock(b, "text", f.Reproducer.FixtureSetup)
		}
		if f.Reproducer.Script != "" {
			fmt.Fprintln(b, "Script:")
			writeCodeBlock(b, "bash", f.Reproducer.Script)
		}
		if len(f.Reproducer.Commands) > 0 {
			fmt.Fprintln(b, "Commands:")
			for _, command := range f.Reproducer.Commands {
				fmt.Fprintf(b, "- `%s`\n", strings.ReplaceAll(command, "`", "'"))
			}
			fmt.Fprintln(b)
		}
	}
	if f.Classification == "confirmed-defect" {
		writeOutcome(b, "Expected", f.Expected)
		writeOutcome(b, "Actual", f.Actual)
	}
	if f.Verification.Runs > 0 || f.Verification.Reference != "" || f.Verification.TemporaryTest != "" {
		fmt.Fprintln(b, "#### Verification")
		fmt.Fprintln(b)
		fmt.Fprintf(b, "- Runs: %d\n", f.Verification.Runs)
		fmt.Fprintf(b, "- Reference: %s\n", emptyAsNone(f.Verification.Reference))
		fmt.Fprintf(b, "- Temporary test: %s\n\n", emptyAsNone(f.Verification.TemporaryTest))
	}
	if f.SuggestedTestLocation != "" {
		fmt.Fprintf(b, "Suggested regression-test location: `%s`\n\n", f.SuggestedTestLocation)
	}
}

func writeReferences(b *strings.Builder, title string, refs []reference) {
	if len(refs) == 0 {
		return
	}
	fmt.Fprintf(b, "#### %s\n\n", title)
	for _, ref := range refs {
		fmt.Fprintf(b, "- `%s`: %s\n", ref.Location, ref.Detail)
	}
	fmt.Fprintln(b)
}

func writeOutcome(b *strings.Builder, title string, value outcome) {
	fmt.Fprintf(b, "#### %s\n\n", title)
	fmt.Fprintf(b, "Exit code: %s\n\n", formatExitCode(value.ExitCode))
	fmt.Fprintln(b, "stdout:")
	writeCodeBlock(b, "text", value.Stdout)
	fmt.Fprintln(b, "stderr:")
	writeCodeBlock(b, "text", value.Stderr)
}

func writeCodeBlock(b *strings.Builder, language, value string) {
	fmt.Fprintf(b, "```%s\n%s", language, value)
	if !strings.HasSuffix(value, "\n") {
		fmt.Fprintln(b)
	}
	fmt.Fprintln(b, "```")
	fmt.Fprintln(b)
}

func writeStringList(b *strings.Builder, title string, values []string) {
	fmt.Fprintf(b, "### %s\n\n", title)
	if len(values) == 0 {
		fmt.Fprintln(b, "None.")
		fmt.Fprintln(b)
		return
	}
	for _, value := range values {
		fmt.Fprintf(b, "- %s\n", value)
	}
	fmt.Fprintln(b)
}

func formatExitCode(code *int) string {
	if code == nil {
		return "not applicable"
	}
	return strconv.Itoa(*code)
}

func emptyAsNone(value string) string {
	if strings.TrimSpace(value) == "" {
		return "not provided"
	}
	return value
}

func prepareIssuePreviews(runDir string, meta runMetadata, result scanResult) ([]issuePreview, error) {
	var previews []issuePreview
	for _, f := range result.Findings {
		if f.Classification == "inconclusive" {
			continue
		}
		fingerprint := findingFingerprint(f)
		previews = append(previews, issuePreview{
			Finding:     f,
			Fingerprint: fingerprint,
			BodyPath:    filepath.Join(runDir, "issues", fingerprint+".md"),
		})
	}
	if len(previews) == 0 {
		return nil, nil
	}
	if err := os.MkdirAll(filepath.Join(runDir, "issues"), 0o755); err != nil {
		return nil, fmt.Errorf("create issue previews directory: %w", err)
	}
	for _, preview := range previews {
		body := renderIssueBody(meta, preview.Finding, preview.Fingerprint)
		if err := os.WriteFile(preview.BodyPath, []byte(body), 0o644); err != nil {
			return nil, fmt.Errorf("write issue preview: %w", err)
		}
	}
	return previews, nil
}

func publishFindings(ctx context.Context, cfg config, repoRoot string, meta runMetadata, previews []issuePreview) ([]issueLink, error) {
	if len(previews) == 0 {
		return nil, nil
	}
	if meta.Repository == "" {
		return nil, errors.New("GitHub repository is unknown; pass --repo owner/repository")
	}
	if _, err := commandOutput(ctx, repoRoot, cfg.ghBinary, "auth", "status", "--hostname", "github.com"); err != nil {
		return nil, fmt.Errorf("GitHub CLI authentication failed: %w; use --no-publish to keep the report local", err)
	}
	existing, err := loadExistingIssues(ctx, cfg, repoRoot, meta.Repository)
	if err != nil {
		return nil, err
	}
	byFingerprint := make(map[string]existingIssue)
	for _, issue := range existing {
		if match := markerPattern.FindStringSubmatch(issue.Body); len(match) == 2 {
			byFingerprint[match[1]] = issue
		}
	}

	links := make([]issueLink, 0, len(previews))
	for _, preview := range previews {
		f := preview.Finding
		if issue, ok := byFingerprint[preview.Fingerprint]; ok {
			action := "updated"
			if strings.EqualFold(issue.State, "closed") {
				if _, err := commandOutput(ctx, repoRoot, cfg.ghBinary, "issue", "reopen", strconv.Itoa(issue.Number), "--repo", meta.Repository); err != nil {
					return links, fmt.Errorf("reopen issue %d: %w", issue.Number, err)
				}
				action = "reopened"
			}
			if _, err := commandOutput(ctx, repoRoot, cfg.ghBinary, "issue", "comment", strconv.Itoa(issue.Number), "--repo", meta.Repository, "--body-file", preview.BodyPath); err != nil {
				return links, fmt.Errorf("comment on issue %d: %w", issue.Number, err)
			}
			links = append(links, issueLink{Action: action, URL: issue.URL, Title: f.Title})
			continue
		}

		title := issueTitle(f)
		issueURL, err := commandOutput(ctx, repoRoot, cfg.ghBinary, "issue", "create", "--repo", meta.Repository, "--title", title, "--body-file", preview.BodyPath)
		if err != nil {
			return links, fmt.Errorf("create issue %q: %w", title, err)
		}
		issueURL = strings.TrimSpace(issueURL)
		links = append(links, issueLink{Action: "created", URL: issueURL, Title: f.Title})
		byFingerprint[preview.Fingerprint] = existingIssue{State: "OPEN", URL: issueURL, Title: title}
	}
	return links, nil
}

func loadExistingIssues(ctx context.Context, cfg config, repoRoot, repository string) ([]existingIssue, error) {
	out, err := commandOutput(ctx, repoRoot, cfg.ghBinary, "issue", "list", "--repo", repository, "--state", "all", "--limit", "1000", "--json", "number,state,url,title,body")
	if err != nil {
		return nil, fmt.Errorf("list existing correctness issues: %w", err)
	}
	var issues []existingIssue
	if err := json.Unmarshal([]byte(out), &issues); err != nil {
		return nil, fmt.Errorf("decode existing issues: %w", err)
	}
	return issues, nil
}

func findingFingerprint(f finding) string {
	parts := []string{
		strings.ToLower(strings.TrimSpace(f.Classification)),
		strings.ToUpper(strings.TrimSpace(f.ContractID)),
		strings.ToLower(strings.TrimSpace(f.Target)),
		normalizeFingerprintText(f.Title),
		normalizeFingerprintText(f.Reproducer.Script),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

func normalizeFingerprintText(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(value)), " ")
}

func issueTitle(f finding) string {
	title := fmt.Sprintf("[correctness][%s][%s] %s", f.Classification, f.Target, strings.TrimSpace(f.Title))
	runes := []rune(title)
	if len(runes) > 240 {
		title = string(runes[:240])
	}
	return title
}

func renderIssueBody(meta runMetadata, f finding, fingerprint string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- %s:%s -->\n", fingerprintLabel, fingerprint)
	fmt.Fprintln(&b, "Generated by the rshell behavior correctness scanner.")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- Contract: `%s`\n", f.ContractID)
	fmt.Fprintf(&b, "- Classification: `%s`\n", f.Classification)
	fmt.Fprintf(&b, "- Impact: `%s`\n", f.Impact)
	fmt.Fprintf(&b, "- Target: `%s`\n", f.Target)
	fmt.Fprintf(&b, "- Scanned commit: `%s`\n", meta.Commit)
	if meta.WorkflowURL != "" {
		fmt.Fprintf(&b, "- Workflow run: %s\n", meta.WorkflowURL)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, f.Description)
	fmt.Fprintln(&b)
	writeReferences(&b, "Contract sources", f.SourceReferences)
	writeReferences(&b, "Implementation references", f.ImplementationReferences)
	if f.Reproducer.FixtureSetup != "" {
		fmt.Fprintln(&b, "## Fixture setup")
		fmt.Fprintln(&b)
		writeCodeBlock(&b, "text", f.Reproducer.FixtureSetup)
	}
	if f.Reproducer.Script != "" {
		fmt.Fprintln(&b, "## Reproducer")
		fmt.Fprintln(&b)
		writeCodeBlock(&b, "bash", f.Reproducer.Script)
	}
	if len(f.Reproducer.Commands) > 0 {
		fmt.Fprintln(&b, "Commands used:")
		fmt.Fprintln(&b)
		for _, command := range f.Reproducer.Commands {
			fmt.Fprintf(&b, "- `%s`\n", strings.ReplaceAll(command, "`", "'"))
		}
		fmt.Fprintln(&b)
	}
	if f.Classification == "confirmed-defect" {
		writeOutcome(&b, "Expected", f.Expected)
		writeOutcome(&b, "Actual", f.Actual)
	}
	fmt.Fprintln(&b, "## Verification")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "- Reproduction runs: %d\n", f.Verification.Runs)
	fmt.Fprintf(&b, "- Reference: %s\n", emptyAsNone(f.Verification.Reference))
	fmt.Fprintf(&b, "- Temporary test: %s\n", emptyAsNone(f.Verification.TemporaryTest))
	if f.SuggestedTestLocation != "" {
		fmt.Fprintf(&b, "- Suggested regression-test location: `%s`\n", f.SuggestedTestLocation)
	}
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Fingerprint: `%s`\n", fingerprint)
	return b.String()
}

func writeIssueLinks(path string, links []issueLink, publishErr error) error {
	var b strings.Builder
	fmt.Fprintln(&b, "# Correctness scan issues")
	fmt.Fprintln(&b)
	if len(links) == 0 {
		fmt.Fprintln(&b, "No issues were created or updated.")
	} else {
		sort.SliceStable(links, func(i, j int) bool {
			if links[i].Action == links[j].Action {
				return links[i].Title < links[j].Title
			}
			return links[i].Action < links[j].Action
		})
		current := ""
		for _, link := range links {
			if link.Action != current {
				current = link.Action
				fmt.Fprintf(&b, "## %s\n\n", strings.ToUpper(current[:1])+current[1:])
			}
			fmt.Fprintf(&b, "- [%s](%s)\n", link.Title, link.URL)
		}
	}
	if publishErr != nil {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "## Publication error")
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, publishErr)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return fmt.Errorf("write issue links: %w", err)
	}
	return nil
}

func printIssueSummary(w io.Writer, links []issueLink) {
	if len(links) == 0 {
		fmt.Fprintln(w, "No publishable findings; no GitHub issues were changed.")
		return
	}
	fmt.Fprintln(w, "GitHub issues:")
	for _, link := range links {
		fmt.Fprintf(w, "- %s: %s (%s)\n", link.Action, link.Title, link.URL)
	}
}

func workflowURL() string {
	server := os.Getenv("GITHUB_SERVER_URL")
	repository := os.Getenv("GITHUB_REPOSITORY")
	runID := os.Getenv("GITHUB_RUN_ID")
	if server == "" || repository == "" || runID == "" {
		return ""
	}
	return strings.TrimSuffix(server, "/") + "/" + repository + "/actions/runs/" + runID
}

func codexEnvironment(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "GITHUB_") || key == "GH_TOKEN" || key == "GH_ENTERPRISE_TOKEN" ||
			key == "ACTIONS_ID_TOKEN_REQUEST_TOKEN" || key == "ACTIONS_RUNTIME_TOKEN" {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

func cleanupWorktree(ctx context.Context, repoRoot, tempRoot, worktreeDir string, keep bool, stderr io.Writer) {
	if keep {
		fmt.Fprintf(stderr, "correctness-scan: kept worktree at %s\n", worktreeDir)
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if _, err := commandOutput(cleanupCtx, repoRoot, "git", "worktree", "remove", "--force", worktreeDir); err != nil {
		fmt.Fprintf(stderr, "correctness-scan: remove worktree: %v\n", err)
		return
	}
	if err := os.RemoveAll(tempRoot); err != nil {
		fmt.Fprintf(stderr, "correctness-scan: remove temporary directory: %v\n", err)
	}
}

func commandOutput(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message != "" {
			return "", fmt.Errorf("%w: %s", err, message)
		}
		return "", err
	}
	return stdout.String(), nil
}
