// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyDir recursively copies src to dst. Only regular files and directories
// are handled.
func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyFile(path, target)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// repoRoot returns the repo root by going one level up from the test's working
// directory (_allowedsymbols/).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(dir)
}

// builtinsCfg returns an allowedSymbolsConfig for the builtins directory
// rooted at tempRoot, collecting errors into errs.
func builtinsCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   builtinAllowedSymbols,
		TargetDir: "builtins",
		CollectFiles: func(dir string) ([]string, error) {
			return collectSubdirGoFiles(dir, map[string]bool{"testutil": true}, func(rel string) bool {
				return rel == "builtins.go"
			})
		},
		ExemptImport: func(importPath string) bool {
			return importPath == "github.com/DataDog/rshell/builtins" ||
				strings.HasPrefix(importPath, "github.com/DataDog/rshell/builtins/internal/")
		},
		ListName:         "builtinAllowedSymbols",
		MinFiles:         1,
		RepoRootOverride: tempRoot,
		Errors:           errs,
	}
}

// appendToFile appends text to the end of the given file.
func appendToFile(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// findFirstGoFile returns the path to the first non-test .go file inside a
// subdirectory of dir.
func findFirstGoFile(t *testing.T, dir string) string {
	t.Helper()
	var found string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if found != "" {
			return filepath.SkipAll
		}
		if info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(dir, path)
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		// Must be inside a subdirectory (not top-level).
		if strings.Contains(rel, string(filepath.Separator)) {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatal("no .go file found in builtins subdirectories")
	}
	return found
}

// errContains returns true if any error in errs contains substr.
func errContains(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func TestVerificationCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationBannedPackageExact(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstGoFile(t, filepath.Join(tmp, "builtins"))

	// Read existing file and inject os/exec import + usage.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	content = strings.Replace(content, "import (", "import (\n\t\"os/exec\"", 1)
	content += "\nvar _ = exec.Command\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for os/exec, got: %v", errs)
	}
}

func TestVerificationBannedPackagePrefix(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstGoFile(t, filepath.Join(tmp, "builtins"))

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	content = strings.Replace(content, "import (", "import (\n\t\"net/http\"", 1)
	content += "\nvar _ = http.Get\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for net/http, got: %v", errs)
	}
}

func TestVerificationUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstGoFile(t, filepath.Join(tmp, "builtins"))

	// os is an allowed package (os.FileInfo, os.O_RDONLY), but os.Setenv is not listed.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	// Ensure os is imported.
	if !strings.Contains(content, `"os"`) {
		content = strings.Replace(content, "import (", "import (\n\t\"os\"", 1)
	}
	content += "\nvar _ = os.Setenv\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "os.Setenv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os.Setenv, got: %v", errs)
	}
}

func TestVerificationUnlistedPackage(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstGoFile(t, filepath.Join(tmp, "builtins"))

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	content = strings.Replace(content, "import (", "import (\n\t\"crypto/rand\"", 1)
	content += "\nvar _ = rand.Read\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for crypto/rand, got: %v", errs)
	}
}

func TestVerificationBlankImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstGoFile(t, filepath.Join(tmp, "builtins"))

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	content = strings.Replace(content, "import (", "import (\n\t_ \"encoding/json\"", 1)
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "blank/dot import") {
		t.Errorf("expected 'blank/dot import' error, got: %v", errs)
	}
}
