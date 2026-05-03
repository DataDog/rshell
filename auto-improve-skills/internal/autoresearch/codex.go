// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package autoresearch

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	CodexFastServiceTier       = "fast"
	CodexReasoningEffort       = "xhigh"
	CodexDefaultApprovalPolicy = "never"
	CodexReadOnlySandbox       = "read-only"
	CodexWorkspaceWriteSandbox = "workspace-write"
)

// CodexExecJSONArgs returns common non-interactive Codex args for JSONL output.
func CodexExecJSONArgs(model, sandbox string) []string {
	args := codexExecBaseArgs(model, sandbox)
	return insertArgs(args, 1, "--json")
}

// CodexExecTextArgs returns common non-interactive Codex args for text output.
func CodexExecTextArgs(model, sandbox, outputLastMessagePath string) []string {
	args := codexExecBaseArgs(model, sandbox)
	if outputLastMessagePath != "" {
		args = insertArgs(args, len(args)-1, "--output-last-message", outputLastMessagePath)
	}
	return args
}

func insertArgs(args []string, idx int, values ...string) []string {
	out := make([]string, 0, len(args)+len(values))
	out = append(out, args[:idx]...)
	out = append(out, values...)
	out = append(out, args[idx:]...)
	return out
}

func codexExecBaseArgs(model, sandbox string) []string {
	return []string{
		"exec",
		"--ephemeral",
		"--ignore-user-config",
		"--ignore-rules",
		"--skip-git-repo-check",
		"--sandbox", sandbox,
		"-c", `approval_policy="` + CodexDefaultApprovalPolicy + `"`,
		"-c", `service_tier="` + CodexFastServiceTier + `"`,
		"-c", `model_reasoning_effort="` + CodexReasoningEffort + `"`,
		"-m", model,
		"-",
	}
}

// ResolveCodex resolves the codex executable. It first respects an explicit
// -codex value, then CODEX_BIN, PATH, and common Homebrew/npm/nvm installation
// locations. The nvm fallback matters when Go is launched from a shell that did
// not source nvm, so "codex" is installed but not on PATH.
func ResolveCodex(codex string) (string, error) {
	codex = strings.TrimSpace(codex)
	if codex == "" {
		codex = "codex"
	}

	if hasPathSeparator(codex) || filepath.IsAbs(codex) {
		return resolveExecutablePath(codex)
	}

	if codex != "codex" {
		if path, err := exec.LookPath(codex); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("%q executable not found in PATH", codex)
	}

	if env := strings.TrimSpace(os.Getenv("CODEX_BIN")); env != "" {
		return resolveExecutablePath(env)
	}

	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}

	for _, candidate := range codexCandidates() {
		if path, err := resolveExecutablePath(candidate); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("codex executable not found. Install Codex CLI, pass -codex /path/to/codex, or set CODEX_BIN=/path/to/codex. Current PATH=%q", os.Getenv("PATH"))
}

// EnvWithExecutableDir returns an environment that prepends the executable's
// directory to PATH. This is important for npm/nvm-installed codex scripts
// whose shebang uses /usr/bin/env node; node usually lives next to codex.
func EnvWithExecutableDir(executable string) []string {
	env := os.Environ()
	if executable == "" || !hasPathSeparator(executable) && !filepath.IsAbs(executable) {
		return env
	}
	dir := filepath.Dir(executable)
	if dir == "." || dir == string(filepath.Separator) {
		return env
	}
	pathValue := os.Getenv("PATH")
	newPath := dir
	if pathValue != "" {
		newPath += string(os.PathListSeparator) + pathValue
	}
	for i, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			env[i] = "PATH=" + newPath
			return env
		}
	}
	return append(env, "PATH="+newPath)
}

func resolveExecutablePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("empty executable path")
	}
	if !filepath.IsAbs(path) && hasPathSeparator(path) {
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
	}
	for _, candidate := range executableVariants(path) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		if runtime.GOOS == "windows" || info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("executable not found or not executable: %s", path)
}

func executableVariants(path string) []string {
	if runtime.GOOS != "windows" || filepath.Ext(path) != "" {
		return []string{path}
	}
	return []string{path, path + ".cmd", path + ".exe", path + ".bat"}
}

func codexCandidates() []string {
	var candidates []string
	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "codex"),
			filepath.Join(home, ".npm-global", "bin", "codex"),
		)
		if matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "codex")); err == nil {
			// Prefer newest-looking versions by trying lexicographically later paths first.
			for i := len(matches) - 1; i >= 0; i-- {
				candidates = append(candidates, matches[i])
			}
		}
	}
	candidates = append(candidates,
		filepath.Join("/opt", "homebrew", "bin", "codex"),
		filepath.Join("/usr", "local", "bin", "codex"),
	)
	if npmPrefix := npmGlobalPrefix(); npmPrefix != "" {
		candidates = append([]string{filepath.Join(npmPrefix, "bin", "codex")}, candidates...)
	}
	return dedupe(candidates)
}

func npmGlobalPrefix() string {
	npm, err := exec.LookPath("npm")
	if err != nil {
		return ""
	}
	cmd := exec.Command(npm, "prefix", "-g")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return ""
	}
	return strings.TrimSpace(out.String())
}

func dedupe(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func hasPathSeparator(path string) bool {
	return strings.ContainsRune(path, os.PathSeparator) || os.PathSeparator == '\\' && strings.ContainsRune(path, '/')
}
