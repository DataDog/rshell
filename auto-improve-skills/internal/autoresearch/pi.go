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

// ResolvePI resolves the pi executable. It first respects an explicit -pi value,
// then PI_BIN, PATH, and common npm/nvm installation locations. The nvm fallback
// matters when Go is launched from a shell that did not source nvm, so "pi" is
// installed but not on PATH.
func ResolvePI(pi string) (string, error) {
	pi = strings.TrimSpace(pi)
	if pi == "" {
		pi = "pi"
	}

	if hasPathSeparator(pi) || filepath.IsAbs(pi) {
		return resolveExecutablePath(pi)
	}

	if pi != "pi" {
		if path, err := exec.LookPath(pi); err == nil {
			return path, nil
		}
		return "", fmt.Errorf("%q executable not found in PATH", pi)
	}

	if env := strings.TrimSpace(os.Getenv("PI_BIN")); env != "" {
		return resolveExecutablePath(env)
	}

	if path, err := exec.LookPath("pi"); err == nil {
		return path, nil
	}

	for _, candidate := range piCandidates() {
		if path, err := resolveExecutablePath(candidate); err == nil {
			return path, nil
		}
	}

	return "", fmt.Errorf("pi executable not found. Install pi, pass -pi /path/to/pi, or set PI_BIN=/path/to/pi. Current PATH=%q", os.Getenv("PATH"))
}

// EnvWithExecutableDir returns an environment that prepends the executable's
// directory to PATH. This is important for npm/nvm-installed pi scripts whose
// shebang uses /usr/bin/env node; node usually lives next to pi.
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

func piCandidates() []string {
	var candidates []string
	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".local", "bin", "pi"),
			filepath.Join(home, ".npm-global", "bin", "pi"),
		)
		if matches, err := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "*", "bin", "pi")); err == nil {
			// Prefer newest-looking versions by trying lexicographically later paths first.
			for i := len(matches) - 1; i >= 0; i-- {
				candidates = append(candidates, matches[i])
			}
		}
	}
	candidates = append(candidates,
		filepath.Join("/opt", "homebrew", "bin", "pi"),
		filepath.Join("/usr", "local", "bin", "pi"),
	)
	if npmPrefix := npmGlobalPrefix(); npmPrefix != "" {
		candidates = append([]string{filepath.Join(npmPrefix, "bin", "pi")}, candidates...)
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
