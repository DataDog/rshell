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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// errContains returns true if any error in errs contains substr.
func errContains(errs []string, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

// injectImport reads a Go source file and inserts an import line into
// the first import block. It also optionally appends extra code after the file.
func injectImport(t *testing.T, path, importLine, appendCode string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	content = strings.Replace(content, "import (", "import (\n\t"+importLine, 1)
	if appendCode != "" {
		content += "\n" + appendCode + "\n"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeGoFile creates a minimal Go source file at path with the given package
// name, imports, and body lines.
func writeGoFile(t *testing.T, path, pkg string, imports []string, body string) {
	t.Helper()
	var b strings.Builder
	b.WriteString("package " + pkg + "\n\n")
	if len(imports) > 0 {
		b.WriteString("import (\n")
		for _, imp := range imports {
			b.WriteString("\t" + imp + "\n")
		}
		b.WriteString(")\n\n")
	}
	b.WriteString(body)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// findFirstSubdirGoFile returns the path to the first non-test .go file inside
// a subdirectory of dir (not at the top level).
func findFirstSubdirGoFile(t *testing.T, dir string) string {
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
		if strings.Contains(rel, string(filepath.Separator)) {
			found = path
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == "" {
		t.Fatalf("no .go file found in subdirectories of %s", dir)
	}
	return found
}

// findFirstFlatGoFile returns the path to the first non-test .go file directly
// in dir (not in subdirectories).
func findFirstFlatGoFile(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".go") && !strings.HasSuffix(e.Name(), "_test.go") {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no .go file found in %s", dir)
	return ""
}

// ---------------------------------------------------------------------------
// Config builders
// ---------------------------------------------------------------------------

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

func interpCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   interpAllowedSymbols,
		TargetDir: "interp",
		CollectFiles: func(dir string) ([]string, error) {
			return collectFlatGoFiles(dir)
		},
		ExemptImport: func(importPath string) bool {
			return strings.HasPrefix(importPath, "github.com/DataDog/rshell/")
		},
		ListName:         "interpAllowedSymbols",
		MinFiles:         1,
		RepoRootOverride: tempRoot,
		Errors:           errs,
	}
}

func allowedpathsCfg(tempRoot string, errs *[]string) allowedSymbolsConfig {
	return allowedSymbolsConfig{
		Symbols:   allowedpathsAllowedSymbols,
		TargetDir: "allowedpaths",
		CollectFiles: func(dir string) ([]string, error) {
			return collectFlatGoFiles(dir)
		},
		ExemptImport: func(importPath string) bool {
			return strings.HasPrefix(importPath, "github.com/DataDog/rshell/")
		},
		ListName:         "allowedpathsAllowedSymbols",
		MinFiles:         1,
		RepoRootOverride: tempRoot,
		Errors:           errs,
	}
}

// ---------------------------------------------------------------------------
// Builtins verification tests
// ---------------------------------------------------------------------------

func TestVerificationBuiltinsCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationBuiltinsBannedPackageExact(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"os/exec"`, "var _ = exec.Command")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for os/exec, got: %v", errs)
	}
}

func TestVerificationBuiltinsBannedPackagePrefix(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"net/http"`, "var _ = http.Get")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "permanently banned") {
		t.Errorf("expected 'permanently banned' error for net/http, got: %v", errs)
	}
}

func TestVerificationBuiltinsUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))

	// os is an allowed package (os.FileInfo, os.O_RDONLY), but os.Setenv is not.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
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

func TestVerificationBuiltinsUnlistedPackage(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `"crypto/rand"`, "var _ = rand.Read")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for crypto/rand, got: %v", errs)
	}
}

func TestVerificationBuiltinsBlankImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	injectImport(t, target, `_ "encoding/json"`, "")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if !errContains(errs, "blank/dot import") {
		t.Errorf("expected 'blank/dot import' error, got: %v", errs)
	}
}

func TestVerificationBuiltinsExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	target := findFirstSubdirGoFile(t, filepath.Join(tmp, "builtins"))
	// builtins/internal/* imports are exempt — should not trigger an error.
	injectImport(t, target, `internalfoo "github.com/DataDog/rshell/builtins/internal/fakepkg"`, "var _ = internalfoo.Foo")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/builtins/internal/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}

func TestVerificationBuiltinsSkipsTopLevel(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Inject a banned import into builtins.go (which is skipped by skipTopLevel).
	target := filepath.Join(tmp, "builtins", "builtins.go")
	injectImport(t, target, `"os/exec"`, "var _ = exec.Command")

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("builtins.go should be skipped, but got error: %v", errs)
	}
}

func TestVerificationBuiltinsSkipsTestutilDir(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "builtins"), filepath.Join(tmp, "builtins"))

	// Create a violating file inside testutil/ (which is skipped by skipDirs).
	writeGoFile(t,
		filepath.Join(tmp, "builtins", "testutil", "bad.go"),
		"testutil",
		[]string{`"os/exec"`},
		"var _ = exec.Command\n",
	)

	var errs []string
	checkAllowedSymbols(t, builtinsCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("testutil/ should be skipped, but got error: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Interp verification tests
// ---------------------------------------------------------------------------

func TestVerificationInterpCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	var errs []string
	checkAllowedSymbols(t, interpCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationInterpUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "interp"))

	// os is an allowed package (os.File, os.Getwd, etc.), but os.Setenv is not.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"os"`) {
		content = strings.Replace(content, "import (", "import (\n\t\"os\"", 1)
	}
	content += "\nvar _ = os.Setenv\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, interpCfg(tmp, &errs))

	if !errContains(errs, "os.Setenv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os.Setenv, got: %v", errs)
	}
}

func TestVerificationInterpExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "interp"))
	// Internal module imports (github.com/DataDog/rshell/*) are exempt.
	injectImport(t, target, `fakepkg "github.com/DataDog/rshell/fakepkg"`, "var _ = fakepkg.Foo")

	var errs []string
	checkAllowedSymbols(t, interpCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}

func TestVerificationInterpIgnoresSubdirs(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "interp"), filepath.Join(tmp, "interp"))

	// collectFlatGoFiles only checks top-level files. A violating file in a
	// subdirectory should be ignored.
	writeGoFile(t,
		filepath.Join(tmp, "interp", "subdir", "bad.go"),
		"subdir",
		[]string{`"os/exec"`},
		"var _ = exec.Command\n",
	)

	var errs []string
	checkAllowedSymbols(t, interpCfg(tmp, &errs))

	if errContains(errs, "os/exec") {
		t.Errorf("subdirectory files should be ignored by collectFlatGoFiles, but got error: %v", errs)
	}
}

// ---------------------------------------------------------------------------
// Allowedpaths verification tests
// ---------------------------------------------------------------------------

func TestVerificationAllowedpathsCleanPass(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "allowedpaths"), filepath.Join(tmp, "allowedpaths"))

	var errs []string
	checkAllowedSymbols(t, allowedpathsCfg(tmp, &errs))

	if len(errs) > 0 {
		t.Errorf("expected no errors on clean copy, got:\n%s", strings.Join(errs, "\n"))
	}
}

func TestVerificationAllowedpathsUnlistedSymbol(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "allowedpaths"), filepath.Join(tmp, "allowedpaths"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "allowedpaths"))

	// os is an allowed package (os.Stat, os.Root, etc.), but os.Setenv is not.
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)
	if !strings.Contains(content, `"os"`) {
		content = strings.Replace(content, "import (", "import (\n\t\"os\"", 1)
	}
	content += "\nvar _ = os.Setenv\n"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var errs []string
	checkAllowedSymbols(t, allowedpathsCfg(tmp, &errs))

	if !errContains(errs, "os.Setenv") || !errContains(errs, "not in the allowlist") {
		t.Errorf("expected 'not in the allowlist' error for os.Setenv, got: %v", errs)
	}
}

func TestVerificationAllowedpathsExemptImport(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	copyDir(t, filepath.Join(root, "allowedpaths"), filepath.Join(tmp, "allowedpaths"))

	target := findFirstFlatGoFile(t, filepath.Join(tmp, "allowedpaths"))
	// Internal module imports (github.com/DataDog/rshell/*) are exempt.
	injectImport(t, target, `fakepkg "github.com/DataDog/rshell/fakepkg"`, "var _ = fakepkg.Foo")

	var errs []string
	checkAllowedSymbols(t, allowedpathsCfg(tmp, &errs))

	if errContains(errs, "github.com/DataDog/rshell/fakepkg") {
		t.Errorf("exempt import should not be flagged, got: %v", errs)
	}
}
