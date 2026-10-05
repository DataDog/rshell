// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseConfigPublishesByDefault(t *testing.T) {
	cfg, err := parseConfig([]string{"--target", "cat"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.publish {
		t.Fatal("publish should be enabled by default")
	}

	cfg, err = parseConfig([]string{"--target", "cat", "--no-publish"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.publish {
		t.Fatal("--no-publish should disable publication")
	}
}

func TestParseConfigRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"--target", "../cat"},
		{"--depth", "exhaustive"},
		{"--ref", "HEAD@{1}"},
		{"--repo", "rshell"},
	} {
		if _, err := parseConfig(args, io.Discard); err == nil {
			t.Fatalf("parseConfig(%q) succeeded", args)
		}
	}
}

func TestParseGitHubRepository(t *testing.T) {
	tests := map[string]string{
		"git@github.com:DataDog/rshell.git":     "DataDog/rshell",
		"https://github.com/DataDog/rshell.git": "DataDog/rshell",
		"ssh://git@github.com/DataDog/rshell":   "DataDog/rshell",
	}
	for remote, want := range tests {
		got, err := parseGitHubRepository(remote)
		if err != nil {
			t.Fatalf("parseGitHubRepository(%q): %v", remote, err)
		}
		if got != want {
			t.Fatalf("parseGitHubRepository(%q) = %q, want %q", remote, got, want)
		}
	}
	if _, err := parseGitHubRepository("https://example.com/DataDog/rshell.git"); err == nil {
		t.Fatal("non-GitHub remote succeeded")
	}
}

func TestBuildPrompt(t *testing.T) {
	cfg := config{target: "changed", depth: "standard"}
	prompt := buildPrompt(cfg, strings.Repeat("a", 40), strings.Repeat("b", 40))
	for _, want := range []string{"Target: changed", "Depth: standard", "Maximum hypotheses: 8", "git diff --stat"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt does not contain %q", want)
		}
	}
	if strings.Contains(prompt, "{{") {
		t.Fatal("prompt contains an unreplaced template variable")
	}
}

func TestValidateResult(t *testing.T) {
	wantCode, gotCode := 0, 1
	valid := scanResult{
		Summary:          "Found one mismatch.",
		HypothesesTested: 1,
		Findings: []finding{{
			Classification: "confirmed-defect",
			ContractID:     "BEH-003",
			Target:         "cat",
			Title:          "status differs",
			Impact:         "medium",
			Description:    "The exit status differs from the documented behavior.",
			SourceReferences: []reference{{
				Location: "SHELL_FEATURES.md:1",
				Detail:   "documents the behavior",
			}},
			ImplementationReferences: []reference{{
				Location: "builtins/cat/cat.go:1",
				Detail:   "returns the observed status",
			}},
			Reproducer: reproducer{
				Script:   "cat missing\n",
				Commands: []string{"go test ./tests"},
			},
			Expected:     outcome{ExitCode: &wantCode},
			Actual:       outcome{ExitCode: &gotCode},
			Verification: verification{Runs: 2},
		}},
	}
	if err := validateResult(valid, config{depth: "quick"}); err != nil {
		t.Fatal(err)
	}

	invalid := valid
	invalid.Findings = append([]finding(nil), valid.Findings...)
	invalid.Findings[0].Verification.Runs = 1
	if err := validateResult(invalid, config{depth: "quick"}); err == nil {
		t.Fatal("confirmed defect with one verification run succeeded")
	}
}

func TestFindingFingerprintNormalizesPresentation(t *testing.T) {
	a := finding{Classification: "missing-coverage", ContractID: "BEH-004", Target: "cat", Title: "Missing stdin case", Reproducer: reproducer{Script: "cat -\n"}}
	b := a
	b.Title = "  MISSING   stdin CASE "
	b.Reproducer.Script = " CAT   - "
	if findingFingerprint(a) != findingFingerprint(b) {
		t.Fatal("presentation-only changes altered the fingerprint")
	}
}

func TestPrepareIssuePreviewsSkipsInconclusive(t *testing.T) {
	runDir := t.TempDir()
	result := scanResult{Findings: []finding{
		{Classification: "missing-coverage", ContractID: "BEH-004", Target: "cat", Title: "stdin lacks coverage", Impact: "low", Description: "No scenario covers stdin.", SuggestedTestLocation: "tests/scenarios/cat.yaml"},
		{Classification: "inconclusive", ContractID: "BEH-002", Target: "cat", Title: "unstable output", Impact: "low", Description: "Could not reproduce."},
	}}
	previews, err := prepareIssuePreviews(runDir, runMetadata{Commit: strings.Repeat("a", 40)}, result)
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 {
		t.Fatalf("got %d previews, want 1", len(previews))
	}
	body, err := os.ReadFile(previews[0].BodyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "<!-- "+fingerprintLabel+":"+previews[0].Fingerprint+" -->") {
		t.Fatal("preview does not contain its fingerprint marker")
	}
	if filepath.Dir(previews[0].BodyPath) != filepath.Join(runDir, "issues") {
		t.Fatalf("preview written outside issues directory: %s", previews[0].BodyPath)
	}
}

func TestIssueTitleTruncatesOnRuneBoundary(t *testing.T) {
	title := issueTitle(finding{Classification: "missing-coverage", Target: "cat", Title: strings.Repeat("é", 300)})
	if !utf8.ValidString(title) {
		t.Fatal("truncated title is invalid UTF-8")
	}
	if utf8.RuneCountInString(title) != 240 {
		t.Fatalf("title contains %d runes, want 240", utf8.RuneCountInString(title))
	}
}

func TestCodexEnvironmentExcludesGitHubCredentials(t *testing.T) {
	env := codexEnvironment([]string{
		"PATH=/bin",
		"OPENAI_API_KEY=keep",
		"GH_TOKEN=drop",
		"GITHUB_TOKEN=drop",
		"GITHUB_REPOSITORY=drop",
		"ACTIONS_ID_TOKEN_REQUEST_TOKEN=drop",
		"ACTIONS_RUNTIME_TOKEN=drop",
	})
	got := strings.Join(env, "\n")
	if got != "PATH=/bin\nOPENAI_API_KEY=keep" {
		t.Fatalf("unexpected environment:\n%s", got)
	}
}

func TestPublishFindingsCreatesOrReopensIssue(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test fixture uses a POSIX shell")
	}
	ghBinary := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(ghBinary, []byte(`#!/bin/sh
printf '%s\n' "$*" >> "$FAKE_GH_LOG"
if [ "$1 $2" = "issue list" ]; then
  printf '%s\n' "$FAKE_GH_ISSUES"
fi
if [ "$1 $2" = "issue create" ]; then
  printf '%s\n' "https://github.com/DataDog/rshell/issues/42"
fi
`), 0o755); err != nil {
		t.Fatal(err)
	}
	f := finding{
		Classification:        "missing-coverage",
		ContractID:            "BEH-004",
		Target:                "cat",
		Title:                 "stdin lacks coverage",
		Impact:                "low",
		Description:           "No scenario covers stdin.",
		SuggestedTestLocation: "tests/scenarios/cat.yaml",
	}
	meta := runMetadata{Repository: "DataDog/rshell", Commit: strings.Repeat("a", 40)}
	cfg := config{ghBinary: ghBinary}

	t.Run("create", func(t *testing.T) {
		runDir := t.TempDir()
		logPath := filepath.Join(runDir, "gh.log")
		t.Setenv("FAKE_GH_LOG", logPath)
		t.Setenv("FAKE_GH_ISSUES", "[]")
		previews, err := prepareIssuePreviews(runDir, meta, scanResult{Findings: []finding{f}})
		if err != nil {
			t.Fatal(err)
		}
		links, err := publishFindings(context.Background(), cfg, runDir, meta, previews)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].Action != "created" {
			t.Fatalf("unexpected links: %#v", links)
		}
		assertCommandLogContains(t, logPath, "issue create")
	})

	t.Run("reopen", func(t *testing.T) {
		runDir := t.TempDir()
		logPath := filepath.Join(runDir, "gh.log")
		t.Setenv("FAKE_GH_LOG", logPath)
		fingerprint := findingFingerprint(f)
		issues, err := json.Marshal([]existingIssue{{
			Number: 7,
			State:  "CLOSED",
			URL:    "https://github.com/DataDog/rshell/issues/7",
			Body:   "<!-- " + fingerprintLabel + ":" + fingerprint + " -->",
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_GH_ISSUES", string(issues))
		previews, err := prepareIssuePreviews(runDir, meta, scanResult{Findings: []finding{f}})
		if err != nil {
			t.Fatal(err)
		}
		links, err := publishFindings(context.Background(), cfg, runDir, meta, previews)
		if err != nil {
			t.Fatal(err)
		}
		if len(links) != 1 || links[0].Action != "reopened" {
			t.Fatalf("unexpected links: %#v", links)
		}
		assertCommandLogContains(t, logPath, "issue reopen 7")
		assertCommandLogContains(t, logPath, "issue comment 7")
		assertCommandLogExcludes(t, logPath, "issue create")
	})
}

func assertCommandLogContains(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("command log does not contain %q:\n%s", want, data)
	}
}

func assertCommandLogExcludes(t *testing.T, path, unwanted string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), unwanted) {
		t.Fatalf("command log contains %q:\n%s", unwanted, data)
	}
}
