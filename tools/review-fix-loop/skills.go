// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// skillDirName maps internal skill keys to their .claude/skills/ directory names.
var skillDirName = map[string]string{
	"code_review":         "code-review",
	"address_pr_comments": "address-pr-comments",
	"fix_ci_tests":        "fix-ci-tests",
}

// loadSkill reads .claude/skills/<dir>/SKILL.md from the repo root, strips
// frontmatter, and replaces $ARGUMENTS with prRef.
func loadSkill(workDir, name, prRef string) string {
	dir, ok := skillDirName[name]
	if !ok {
		panic(fmt.Sprintf("unknown skill: %s", name))
	}
	path := filepath.Join(workDir, ".claude", "skills", dir, "SKILL.md")
	data, err := os.ReadFile(path)
	if err != nil {
		panic(fmt.Sprintf("read skill %s from %s: %v\nhint: run the tool from the repo root with .claude/skills/ present", name, path, err))
	}
	content := stripFrontmatter(string(data))
	return strings.ReplaceAll(content, "$ARGUMENTS", prRef)
}

// stripFrontmatter removes YAML frontmatter delimited by "---\n" from the start.
func stripFrontmatter(s string) string {
	if !strings.HasPrefix(s, "---\n") {
		return s
	}
	rest := s[4:]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return s
	}
	return strings.TrimSpace(rest[end+5:])
}
