// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/DataDog/rshell/auto-improve-skills/internal/autoresearch"
)

const defaultModel = "openai-codex/gpt-5.5"

func main() {
	var (
		casesPath        = flag.String("cases", "auto-improve-skills/benchmarks/remote-host-diagnostics/cases.yaml", "YAML benchmark suite")
		skillPath        = flag.String("skill", "auto-improve-skills/skills/remote-host-diagnostics", "skill directory or SKILL.md path")
		outputPath       = flag.String("out", "", "write JSON report to this path")
		rawDir           = flag.String("raw-dir", "", "directory for raw pi JSONL transcripts")
		piBinary         = flag.String("pi", "pi", "pi executable")
		model            = flag.String("model", defaultModel, "pi model for benchmark agents and optional judge")
		mode             = flag.String("mode", "live", "benchmark mode: live or prompts")
		limit            = flag.Int("limit", 0, "run at most N cases (0 = all)")
		caseFilter       = flag.String("case", "", "run one case id")
		caseTimeout      = flag.Duration("case-timeout", 10*time.Minute, "timeout per benchmark case")
		judge            = flag.Bool("judge", false, "run optional LLM-as-judge scoring pass")
		judgeWeight      = flag.Float64("judge-weight", 0.6, "when -judge is set, final score weight for judge score (0..1)")
		ensureRShell     = flag.Bool("ensure-rshell", true, "run make build if ./rshell is missing")
		generateFixtures = flag.Bool("generate-fixtures", true, "generate deterministic remote-host-diagnostics fixture logs before running")
	)
	flag.Parse()

	if err := run(*casesPath, *skillPath, *outputPath, *rawDir, *piBinary, *model, *mode, *limit, *caseFilter, *caseTimeout, *judge, *judgeWeight, *ensureRShell, *generateFixtures); err != nil {
		fmt.Fprintf(os.Stderr, "skillbench: %v\n", err)
		os.Exit(1)
	}
}

func run(casesPath, skillPath, outputPath, rawDir, piBinary, model, mode string, limit int, caseFilter string, caseTimeout time.Duration, judge bool, judgeWeight float64, ensureRShell, generateFixtures bool) error {
	if mode != "live" && mode != "prompts" {
		return fmt.Errorf("unsupported -mode %q (want live or prompts)", mode)
	}
	if judgeWeight < 0 || judgeWeight > 1 {
		return fmt.Errorf("-judge-weight must be between 0 and 1")
	}

	root, err := autoresearch.RepoRoot()
	if err != nil {
		return err
	}
	if mode == "live" {
		resolvedPI, err := autoresearch.ResolvePI(piBinary)
		if err != nil {
			return err
		}
		piBinary = resolvedPI
	}
	casesAbs := autoresearch.AbsFromRoot(root, casesPath)
	if generateFixtures && isRemoteHostDiagnosticsSuite(casesAbs) {
		if err := autoresearch.GenerateRemoteHostDiagnosticsFixtures(root); err != nil {
			return fmt.Errorf("generating deterministic fixtures: %w", err)
		}
	}
	requestedSkillAbs := autoresearch.AbsFromRoot(root, skillPath)
	if strings.HasSuffix(requestedSkillAbs, "SKILL.md") {
		requestedSkillAbs = filepath.Dir(requestedSkillAbs)
	}
	if ensureRShell && mode == "live" {
		if err := ensureLocalRShell(root); err != nil {
			return err
		}
	}

	suite, err := autoresearch.LoadSuite(casesAbs)
	if err != nil {
		return err
	}
	if suite.SkillPath != "" && skillPath == "" {
		requestedSkillAbs = autoresearch.AbsFromRoot(filepath.Dir(casesAbs), suite.SkillPath)
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	if outputPath == "" {
		outputPath = filepath.Join(root, "auto-improve-skills", "runs", "benchmark-"+stamp, "result.json")
	} else {
		outputPath = autoresearch.AbsFromRoot(root, outputPath)
	}
	if rawDir == "" {
		rawDir = filepath.Join(filepath.Dir(outputPath), "raw")
	} else {
		rawDir = autoresearch.AbsFromRoot(root, rawDir)
	}
	if err := os.MkdirAll(rawDir, 0o755); err != nil {
		return err
	}

	started := time.Now().UTC()
	vars := autoresearch.Variables(root, requestedSkillAbs)
	results := autoresearch.SuiteResult{
		SuiteName:   suite.Name,
		Description: suite.Description,
		Mode:        mode,
		Model:       model,
		SkillPath:   requestedSkillAbs,
		CasesPath:   casesAbs,
		RepoRoot:    root,
		StartedAt:   started,
	}

	runCount := 0
	for _, tc := range suite.Cases {
		if caseFilter != "" && tc.ID != caseFilter {
			continue
		}
		if limit > 0 && runCount >= limit {
			break
		}
		runCount++
		caseVars := autoresearch.MergeVariables(vars, tc.Variables)
		expanded := expandCase(tc, caseVars)
		caseResult := runCase(root, rawDir, requestedSkillAbs, piBinary, model, mode, expanded, caseTimeout)
		scoreCase(&caseResult, expanded)
		if judge && mode == "live" && strings.TrimSpace(caseResult.FinalAnswer) != "" {
			jr, err := runJudge(root, piBinary, model, expanded, caseResult, caseTimeout/2)
			if err != nil {
				caseResult.Error = strings.TrimSpace(caseResult.Error + "; judge: " + err.Error())
			} else {
				caseResult.Judge = &jr
				applyJudgeScore(&caseResult, judgeWeight)
			}
		}
		results.Cases = append(results.Cases, caseResult)
		results.Score += caseResult.Score
		results.MaxScore += caseResult.MaxScore
	}
	if runCount == 0 {
		return fmt.Errorf("no cases selected")
	}
	if results.MaxScore > 0 {
		results.NormalizedScore = results.Score / results.MaxScore
	}
	results.CompletedAt = time.Now().UTC()
	results.WallClockDuration = results.CompletedAt.Sub(started).String()

	if err := autoresearch.WriteJSON(outputPath, results); err != nil {
		return err
	}
	printSummary(results, outputPath)
	return nil
}

func isRemoteHostDiagnosticsSuite(casesPath string) bool {
	return filepath.Base(filepath.Dir(casesPath)) == "remote-host-diagnostics"
}

func ensureLocalRShell(root string) error {
	if st, err := os.Stat(filepath.Join(root, "rshell")); err == nil && st.Mode()&0o111 != 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "make", "build")
	cmd.Dir = root
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("building ./rshell: %w", err)
	}
	return nil
}

func expandCase(tc autoresearch.Case, vars map[string]string) autoresearch.Case {
	tc.Prompt = autoresearch.Expand(tc.Prompt, vars)
	tc.JudgeRubric = autoresearch.Expand(tc.JudgeRubric, vars)
	for i := range tc.Criteria {
		tc.Criteria[i].Contains = autoresearch.Expand(tc.Criteria[i].Contains, vars)
		tc.Criteria[i].Regex = autoresearch.Expand(tc.Criteria[i].Regex, vars)
	}
	return tc
}

func runCase(root, rawDir, skillPath, piBinary, model, mode string, tc autoresearch.Case, timeout time.Duration) (result autoresearch.CaseResult) {
	started := time.Now().UTC()
	result = autoresearch.CaseResult{
		ID:        tc.ID,
		Title:     tc.Title,
		Prompt:    tc.Prompt,
		StartedAt: started,
	}
	defer func() {
		result.CompletedAt = time.Now().UTC()
		result.WallClockDuration = result.CompletedAt.Sub(started).String()
	}()

	if mode == "prompts" {
		result.FinalAnswer = "PROMPT ONLY MODE"
		result.RawJSONLPath = ""
		return result
	}

	rawPath := filepath.Join(rawDir, safeFileName(tc.ID)+".jsonl")
	stderrPath := filepath.Join(rawDir, safeFileName(tc.ID)+".stderr")
	prompt := benchmarkPrompt(tc)
	args := []string{
		"--mode", "json",
		"--print",
		"--no-session",
		"--no-context-files",
		"--no-extensions",
		"--no-prompt-templates",
		"--no-skills",
		"--skill", skillPath,
		"--tools", "read,bash",
		"--model", model,
		prompt,
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, piBinary, args...)
	cmd.Dir = root
	cmd.Env = autoresearch.EnvWithExecutableDir(piBinary)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	_ = os.WriteFile(rawPath, stdout.Bytes(), 0o644)
	if stderr.Len() > 0 {
		_ = os.WriteFile(stderrPath, stderr.Bytes(), 0o644)
	}
	result.RawJSONLPath = rawPath
	parsed, parseErr := parsePiJSONL(stdout.Bytes())
	result.FinalAnswer = parsed.FinalAnswer
	result.Commands = parsed.Commands
	result.ToolCalls = parsed.ToolCalls
	if parseErr != nil {
		result.Error = appendErr(result.Error, "parse pi JSONL: "+parseErr.Error())
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Error = appendErr(result.Error, "pi timed out after "+timeout.String())
		} else {
			result.Error = appendErr(result.Error, "pi failed: "+err.Error())
		}
		if stderr.Len() > 0 {
			result.Error = appendErr(result.Error, "stderr saved to "+stderrPath)
		}
	}
	return result
}

func benchmarkPrompt(tc autoresearch.Case) string {
	return strings.TrimSpace(`You are running an automated benchmark of an Agent Skill.

You must use the loaded remote-host-diagnostics skill. Load/read the skill instructions first, then follow its workflow. This is a fake local investigation using fixture logs, so do not use host tools directly to inspect the fixture contents; run diagnostics through local ./rshell as the skill instructs. Do not modify files.

Final answer quality is the metric. Your final answer should be concise but complete, with:
- finding or likely root cause
- concrete evidence from the logs/commands
- commands you ran
- any uncertainty or safe next steps

Benchmark case:
`+tc.Prompt) + "\n"
}

type parsedPi struct {
	FinalAnswer string
	Commands    []string
	ToolCalls   []autoresearch.ToolCall
}

func parsePiJSONL(data []byte) (parsedPi, error) {
	var parsed parsedPi
	calls := map[string]int{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 20*1024*1024)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var ev struct {
			Type       string          `json:"type"`
			ToolCallID string          `json:"toolCallId"`
			ToolName   string          `json:"toolName"`
			Args       json.RawMessage `json:"args"`
			Result     json.RawMessage `json:"result"`
			IsError    bool            `json:"isError"`
			Message    json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		switch ev.Type {
		case "tool_execution_start":
			call := autoresearch.ToolCall{ID: ev.ToolCallID, Name: ev.ToolName, Args: ev.Args}
			call.Command = commandFromArgs(ev.ToolName, ev.Args)
			calls[ev.ToolCallID] = len(parsed.ToolCalls)
			parsed.ToolCalls = append(parsed.ToolCalls, call)
			if ev.ToolName == "bash" && call.Command != "" {
				parsed.Commands = append(parsed.Commands, call.Command)
			}
		case "tool_execution_end":
			idx, ok := calls[ev.ToolCallID]
			if !ok {
				continue
			}
			parsed.ToolCalls[idx].IsError = ev.IsError
			parsed.ToolCalls[idx].Result = textFromToolResult(ev.Result)
		case "message_end", "turn_end":
			if text := assistantText(ev.Message); strings.TrimSpace(text) != "" {
				parsed.FinalAnswer = text
			}
		}
	}
	return parsed, scanner.Err()
}

func commandFromArgs(tool string, raw json.RawMessage) string {
	if tool != "bash" || len(raw) == 0 {
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return ""
	}
	return args.Command
}

func textFromToolResult(raw json.RawMessage) string {
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return ""
	}
	parts := make([]string, 0, len(res.Content))
	for _, c := range res.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func assistantText(raw json.RawMessage) string {
	var msg struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil || msg.Role != "assistant" {
		return ""
	}
	parts := make([]string, 0, len(msg.Content))
	for _, c := range msg.Content {
		if c.Type == "text" {
			parts = append(parts, c.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func scoreCase(result *autoresearch.CaseResult, tc autoresearch.Case) {
	commands := strings.Join(result.Commands, "\n")
	toolResults := make([]string, 0, len(result.ToolCalls))
	for _, call := range result.ToolCalls {
		if strings.TrimSpace(call.Result) != "" {
			toolResults = append(toolResults, call.Result)
		}
	}
	texts := map[string]string{
		"final":        result.FinalAnswer,
		"commands":     commands,
		"tool_results": strings.Join(toolResults, "\n"),
	}
	texts["transcript"] = strings.Join([]string{texts["commands"], texts["tool_results"], texts["final"]}, "\n")

	for _, criterion := range tc.Criteria {
		passed, detail := matchCriterion(criterion, texts)
		cr := autoresearch.CriterionResult{Name: criterion.Name, Passed: passed, Max: criterion.Points, Detail: detail}
		if passed {
			cr.Points = criterion.Points
		}
		result.Criteria = append(result.Criteria, cr)
		result.DeterministicMaxScore += criterion.Points
		if passed {
			result.DeterministicScore += criterion.Points
		}
	}
	result.Score = result.DeterministicScore
	result.MaxScore = result.DeterministicMaxScore
	if result.MaxScore > 0 {
		result.NormalizedScore = result.Score / result.MaxScore
	}
}

func matchCriterion(c autoresearch.Criterion, texts map[string]string) (bool, string) {
	source := c.Source
	if source == "" {
		source = "final"
	}
	text := texts[source]
	if c.CaseInsensitive {
		text = strings.ToLower(text)
	}
	matched := false
	detail := ""
	if c.Contains != "" {
		needle := c.Contains
		if c.CaseInsensitive {
			needle = strings.ToLower(needle)
		}
		matched = strings.Contains(text, needle)
		detail = "contains " + strconvQuote(c.Contains)
	}
	if c.Regex != "" {
		pattern := c.Regex
		if c.CaseInsensitive && !strings.HasPrefix(pattern, "(?i)") {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return false, "invalid regex " + err.Error()
		}
		matched = re.MatchString(text)
		detail = "regex " + strconvQuote(c.Regex)
	}
	if c.Not {
		matched = !matched
		detail = "not " + detail
	}
	return matched, detail
}

func runJudge(root, piBinary, model string, tc autoresearch.Case, result autoresearch.CaseResult, timeout time.Duration) (autoresearch.JudgeResult, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	prompt := fmt.Sprintf(`You are an impartial benchmark judge. Score the assistant's FINAL ANSWER quality from 0 to 100 for the diagnostic benchmark case.

Focus on correctness, evidence, actionable explanation, uncertainty handling, and whether the answer directly addresses the user's diagnostic question. Do not reward tool-use mechanics except where they affect answer quality.

Case prompt:
%s

Rubric:
%s

Commands run:
%s

Final answer to score:
%s

Return only compact JSON with this schema: {"score": number, "reason": "short explanation"}
`, tc.Prompt, tc.JudgeRubric, strings.Join(result.Commands, "\n"), result.FinalAnswer)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	args := []string{"--print", "--no-session", "--no-tools", "--model", model, prompt}
	cmd := exec.CommandContext(ctx, piBinary, args...)
	cmd.Dir = root
	cmd.Env = autoresearch.EnvWithExecutableDir(piBinary)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if stderr.Len() > 0 {
			return autoresearch.JudgeResult{}, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return autoresearch.JudgeResult{}, err
	}
	jr, err := parseJudge(stdout.String())
	if err != nil {
		return autoresearch.JudgeResult{Raw: stdout.String()}, err
	}
	jr.Raw = stdout.String()
	if jr.Score < 0 {
		jr.Score = 0
	}
	if jr.Score > 100 {
		jr.Score = 100
	}
	return jr, nil
}

func parseJudge(s string) (autoresearch.JudgeResult, error) {
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return autoresearch.JudgeResult{}, fmt.Errorf("judge did not return JSON")
	}
	var jr autoresearch.JudgeResult
	if err := json.Unmarshal([]byte(s[start:end+1]), &jr); err != nil {
		return autoresearch.JudgeResult{}, err
	}
	if math.IsNaN(jr.Score) || math.IsInf(jr.Score, 0) {
		return autoresearch.JudgeResult{}, fmt.Errorf("invalid judge score")
	}
	return jr, nil
}

func applyJudgeScore(result *autoresearch.CaseResult, judgeWeight float64) {
	if result.Judge == nil || result.MaxScore <= 0 {
		return
	}
	deterministicPct := 100 * result.DeterministicScore / result.DeterministicMaxScore
	combined := (1-judgeWeight)*deterministicPct + judgeWeight*result.Judge.Score
	result.Score = combined
	result.MaxScore = 100
	result.NormalizedScore = combined / 100
}

func printSummary(result autoresearch.SuiteResult, outputPath string) {
	fmt.Printf("skillbench %s: %.1f/%.1f (%.1f%%)\n", result.SuiteName, result.Score, result.MaxScore, result.NormalizedScore*100)
	caseResults := append([]autoresearch.CaseResult(nil), result.Cases...)
	sort.SliceStable(caseResults, func(i, j int) bool { return caseResults[i].ID < caseResults[j].ID })
	for _, cr := range caseResults {
		status := "PASS"
		if cr.NormalizedScore < 0.85 {
			status = "WARN"
		}
		if cr.NormalizedScore < 0.65 {
			status = "FAIL"
		}
		fmt.Printf("  %-36s %5.1f/%-5.1f %5.1f%% %s\n", cr.ID, cr.Score, cr.MaxScore, cr.NormalizedScore*100, status)
		if cr.Error != "" {
			fmt.Printf("    error: %s\n", cr.Error)
		}
	}
	fmt.Printf("report: %s\n", outputPath)
}

func appendErr(existing, msg string) string {
	if strings.TrimSpace(existing) == "" {
		return msg
	}
	return existing + "; " + msg
}

func safeFileName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "case"
	}
	return b.String()
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
