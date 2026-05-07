// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type awkScenario struct {
	Description string              `yaml:"description"`
	Upstream    awkUpstreamMetadata `yaml:"upstream"`
	Covers      []string            `yaml:"covers"`
	Skip        string              `yaml:"skip"`
	Setup       setup               `yaml:"setup"`
	Input       awkInput            `yaml:"input"`
	Expect      awkExpected         `yaml:"expect"`
}

type awkUpstreamMetadata struct {
	Suite string `yaml:"suite"`
	ID    string `yaml:"id"`
	Ref   string `yaml:"ref"`
	Notes string `yaml:"notes"`
}

type awkInput struct {
	AwkArgs     []string          `yaml:"awk_args"`
	Program     string            `yaml:"program"`
	ProgramFile string            `yaml:"program_file"`
	Args        []string          `yaml:"args"`
	Stdin       string            `yaml:"stdin"`
	Envs        map[string]string `yaml:"envs"`
}

type awkExpected struct {
	Stdout         string   `yaml:"stdout"`
	StdoutContains []string `yaml:"stdout_contains"`
	Stderr         string   `yaml:"stderr"`
	StderrContains []string `yaml:"stderr_contains"`
	ExitCode       int      `yaml:"exit_code"`
}

type awkResult struct {
	stdout   string
	stderr   string
	exitCode int
}

type awkUpstreamMap struct {
	Entries []awkUpstreamMapEntry `yaml:"entries"`
}

type awkUpstreamMapEntry struct {
	Suite  string   `yaml:"suite"`
	ID     string   `yaml:"id"`
	Ref    string   `yaml:"ref"`
	Status string   `yaml:"status"`
	Tests  []string `yaml:"tests"`
	Covers []string `yaml:"covers"`
	Reason string   `yaml:"reason"`
}

type awkUpstreamInventoryItem struct {
	suite string
	id    string
}

func TestAwkScenarioMetadata(t *testing.T) {
	scenariosDir := filepath.Join("awk_scenarios")
	enabledPaths := loadEnabledAwkScenarios(t, filepath.Join(scenariosDir, "enabled.txt"), scenariosDir)
	mapEntries := loadAwkUpstreamMap(t, filepath.Join(scenariosDir, "upstream-map.yaml"), scenariosDir)

	mappedTests := map[string]bool{}
	for _, entry := range mapEntries {
		for _, testPath := range entry.Tests {
			mappedTests[filepath.Clean(filepath.FromSlash(testPath))] = true
		}
	}
	for _, enabledPath := range enabledPaths {
		require.True(t, mappedTests[enabledPath], "enabled awk scenario %s is missing from upstream-map.yaml", enabledPath)
		loadAwkScenario(t, filepath.Join(scenariosDir, enabledPath))
	}
}

func TestAwkUpstreamMapCompleteness(t *testing.T) {
	if os.Getenv("RSHELL_AWK_UPSTREAM_MAP_TEST") == "" {
		t.Skip("skipping awk upstream map completeness test (set RSHELL_AWK_UPSTREAM_MAP_TEST=1 to enable)")
	}

	scenariosDir := filepath.Join("awk_scenarios")
	mapEntries := loadAwkUpstreamMap(t, filepath.Join(scenariosDir, "upstream-map.yaml"), scenariosDir)

	var inventory []awkUpstreamInventoryItem
	inventory = append(inventory, discoverGawkUpstreamTests(t, requiredSourceDir(t, "GAWK_SOURCE_DIR"))...)
	inventory = append(inventory, discoverOneTrueAwkUpstreamTests(t, requiredSourceDir(t, "ONETRUEAWK_SOURCE_DIR"))...)
	require.NotEmpty(t, inventory, "upstream inventory is empty")

	inventorySet := map[string]bool{}
	for _, item := range inventory {
		inventorySet[awkUpstreamKey(item.suite, item.id)] = true
	}

	mappedSet := map[string]bool{}
	var duplicateMapped []string
	for _, entry := range mapEntries {
		key := awkUpstreamKey(entry.Suite, entry.ID)
		if mappedSet[key] {
			duplicateMapped = append(duplicateMapped, entry.Suite+"\t"+entry.ID)
		}
		mappedSet[key] = true
	}

	var missing []string
	for _, item := range inventory {
		key := awkUpstreamKey(item.suite, item.id)
		if !mappedSet[key] {
			missing = append(missing, item.suite+"\t"+item.id)
		}
	}

	var stale []string
	for _, entry := range mapEntries {
		key := awkUpstreamKey(entry.Suite, entry.ID)
		if !inventorySet[key] {
			stale = append(stale, entry.Suite+"\t"+entry.ID)
		}
	}

	assert.Empty(t, duplicateMapped, "duplicate upstream-map.yaml entries")
	assert.Empty(t, missing, "upstream tests missing from upstream-map.yaml")
	assert.Empty(t, stale, "upstream-map.yaml entries not present in fetched upstream inventory")
}

func TestAwkScenarios(t *testing.T) {
	if os.Getenv("RSHELL_AWK_TEST") == "" {
		t.Skip("skipping awk scenario tests (set RSHELL_AWK_TEST=1 to enable)")
	}

	scenariosDir := filepath.Join("awk_scenarios")
	enabledPaths := loadEnabledAwkScenarios(t, filepath.Join(scenariosDir, "enabled.txt"), scenariosDir)

	candidate := os.Getenv("AWK_UNDER_TEST")
	oracle := os.Getenv("GAWK_ORACLE")
	if candidate == "" {
		candidate = oracle
	}
	if candidate == "" {
		t.Fatal("AWK_UNDER_TEST or GAWK_ORACLE must point to the awk binary under test")
	}

	candidate = resolveAwkExecutable(t, candidate)
	if oracle != "" {
		oracle = resolveAwkExecutable(t, oracle)
	}
	timeout := awkScenarioTimeout(t)

	groups := groupAwkScenarioPaths(enabledPaths)
	for _, group := range sortedMapKeys(groups) {
		paths := groups[group]
		t.Run(group, func(t *testing.T) {
			for _, scenarioPath := range paths {
				path := filepath.Join(scenariosDir, scenarioPath)
				sc := loadAwkScenario(t, path)
				name := strings.TrimSuffix(filepath.Base(scenarioPath), filepath.Ext(scenarioPath))
				t.Run(name, func(t *testing.T) {
					if sc.Skip != "" {
						t.Skip(sc.Skip)
					}

					got := runAwkScenario(t, candidate, sc, timeout)
					assertAwkExpectations(t, sc, got)

					if oracle != "" && candidate != oracle {
						want := runAwkScenario(t, oracle, sc, timeout)
						assert.Equal(t, want.exitCode, got.exitCode, "exit code mismatch against GNU awk oracle")
						assert.Equal(t, want.stdout, got.stdout, "stdout mismatch against GNU awk oracle")
						assert.Equal(t, want.stderr, got.stderr, "stderr mismatch against GNU awk oracle")
					}
				})
			}
		})
	}
}

func loadAwkUpstreamMap(t *testing.T, path, scenariosDir string) []awkUpstreamMapEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read awk upstream map %s", path)

	var upstreamMap awkUpstreamMap
	err = yaml.Unmarshal(data, &upstreamMap)
	require.NoError(t, err, "failed to parse awk upstream map %s", path)
	require.NotEmpty(t, upstreamMap.Entries, "awk upstream map %s must contain entries", path)

	for index, entry := range upstreamMap.Entries {
		require.NotEmpty(t, entry.Suite, "awk upstream map entry %d must identify a suite", index)
		require.NotEmpty(t, entry.ID, "awk upstream map entry %d must identify an upstream id", index)
		require.NotEmpty(t, entry.Ref, "awk upstream map entry %d must identify an upstream ref", index)
		require.NotEmpty(t, entry.Status, "awk upstream map entry %d must identify a status", index)
		if entry.Status == "rewritten" || entry.Status == "policy" {
			require.NotEmpty(t, entry.Tests, "awk upstream map entry %d must list local tests", index)
			require.NotEmpty(t, entry.Covers, "awk upstream map entry %d must describe covered behavior", index)
		}
		if entry.Status == "deferred" {
			require.NotEmpty(t, entry.Reason, "awk upstream map entry %d must explain deferral", index)
		}
		if entry.Status == "todo" {
			require.NotEmpty(t, entry.Reason, "awk upstream map entry %d must explain pending rewrite work", index)
		}
		for _, testPath := range entry.Tests {
			require.False(t, filepath.IsAbs(testPath), "awk upstream map entry %d test path must be relative: %s", index, testPath)
			cleaned := filepath.Clean(filepath.FromSlash(testPath))
			require.False(t, cleaned == "." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) || cleaned == "..", "awk upstream map entry %d test path escapes scenarios dir: %s", index, testPath)
			if entry.Status == "rewritten" {
				require.FileExists(t, filepath.Join(scenariosDir, cleaned), "awk upstream map entry %d test path does not exist: %s", index, testPath)
			}
		}
	}
	return upstreamMap.Entries
}

func discoverGawkUpstreamTests(t *testing.T, sourceDir string) []awkUpstreamInventoryItem {
	t.Helper()

	testDir := filepath.Join(sourceDir, "test")
	require.DirExists(t, testDir, "gawk source must contain a test directory")

	seen := map[string]bool{}
	add := func(name string) {
		seen["test/"+name] = true
	}

	entries, err := os.ReadDir(testDir)
	require.NoError(t, err, "failed to read gawk test directory")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		switch ext {
		case ".awk", ".sh":
			add(name)
		case ".ok":
			base := strings.TrimSuffix(name, ".ok")
			if !fileExists(filepath.Join(testDir, base+".awk")) && !fileExists(filepath.Join(testDir, base+".sh")) {
				add(name)
			}
		}
	}

	return sortedAwkInventory("gawk", seen)
}

func discoverOneTrueAwkUpstreamTests(t *testing.T, sourceDir string) []awkUpstreamInventoryItem {
	t.Helper()

	testDir := filepath.Join(sourceDir, "testdir")
	require.DirExists(t, testDir, "One True Awk source must contain a testdir directory")

	seen := map[string]bool{}
	entries, err := os.ReadDir(testDir)
	require.NoError(t, err, "failed to read One True Awk testdir")
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, "t.") || strings.HasPrefix(name, "p.") || strings.HasPrefix(name, "T.") || strings.HasPrefix(name, "tt.") {
			seen["testdir/"+name] = true
		}
	}

	return sortedAwkInventory("onetrueawk", seen)
}

func sortedAwkInventory(suite string, ids map[string]bool) []awkUpstreamInventoryItem {
	keys := make([]string, 0, len(ids))
	for id := range ids {
		keys = append(keys, id)
	}
	sort.Strings(keys)

	items := make([]awkUpstreamInventoryItem, 0, len(keys))
	for _, id := range keys {
		items = append(items, awkUpstreamInventoryItem{suite: suite, id: id})
	}
	return items
}

func requiredSourceDir(t *testing.T, envName string) string {
	t.Helper()
	sourceDir := os.Getenv(envName)
	require.NotEmpty(t, sourceDir, "%s must point to a fetched upstream source tree", envName)
	require.DirExists(t, sourceDir, "%s must point to a fetched upstream source tree", envName)
	return sourceDir
}

func awkUpstreamKey(suite, id string) string {
	return suite + "\x00" + id
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func loadAwkScenario(t *testing.T, path string) awkScenario {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err, "failed to read awk scenario file %s", path)

	var sc awkScenario
	err = yaml.Unmarshal(data, &sc)
	require.NoError(t, err, "failed to parse awk scenario file %s", path)
	require.NotEmpty(t, sc.Description, "awk scenario %s must have a description", path)
	require.NotEmpty(t, sc.Upstream.Suite, "awk scenario %s must identify an upstream suite", path)
	require.NotEmpty(t, sc.Upstream.ID, "awk scenario %s must identify an upstream test id or coverage id", path)
	require.NotEmpty(t, sc.Covers, "awk scenario %s must describe the behavior it covers", path)
	return sc
}

func loadEnabledAwkScenarios(t *testing.T, enabledPath, scenariosDir string) []string {
	t.Helper()

	data, err := os.ReadFile(enabledPath)
	require.NoError(t, err, "failed to read enabled awk scenario list %s", enabledPath)

	seen := map[string]int{}
	var paths []string
	for lineNumber, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		require.False(t, filepath.IsAbs(line), "enabled awk scenario %s:%d must be relative", enabledPath, lineNumber+1)
		cleaned := filepath.Clean(filepath.FromSlash(line))
		require.False(t, cleaned == "." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) || cleaned == "..", "enabled awk scenario %s:%d escapes scenarios dir: %s", enabledPath, lineNumber+1, line)
		require.Contains(t, []string{".yaml", ".yml"}, filepath.Ext(cleaned), "enabled awk scenario %s:%d must point to a YAML file", enabledPath, lineNumber+1)
		if previous, ok := seen[cleaned]; ok {
			t.Fatalf("enabled awk scenario %s:%d duplicates line %d: %s", enabledPath, lineNumber+1, previous, line)
		}
		seen[cleaned] = lineNumber + 1
		require.FileExists(t, filepath.Join(scenariosDir, cleaned), "enabled awk scenario %s:%d does not exist", enabledPath, lineNumber+1)
		paths = append(paths, cleaned)
	}
	require.NotEmpty(t, paths, "enabled awk scenario list %s is empty", enabledPath)
	return paths
}

func groupAwkScenarioPaths(paths []string) map[string][]string {
	groups := make(map[string][]string)
	for _, path := range paths {
		group := filepath.ToSlash(filepath.Dir(path))
		groups[group] = append(groups[group], path)
	}
	for _, paths := range groups {
		sort.Strings(paths)
	}
	return groups
}

func runAwkScenario(t *testing.T, awkBin string, sc awkScenario, timeout time.Duration) awkResult {
	t.Helper()

	dir := setupTestDir(t, scenario{Setup: sc.Setup})
	args := append([]string{}, sc.Input.AwkArgs...)
	if sc.Input.ProgramFile != "" {
		if sc.Input.Program != "" {
			programPath := filepath.Join(dir, sc.Input.ProgramFile)
			require.NoError(t, os.MkdirAll(filepath.Dir(programPath), 0755), "failed to create directories for %s", sc.Input.ProgramFile)
			require.NoError(t, os.WriteFile(programPath, []byte(sc.Input.Program), 0644), "failed to write awk program %s", sc.Input.ProgramFile)
		}
		args = append(args, "-f", sc.Input.ProgramFile)
	} else {
		require.NotEmpty(t, sc.Input.Program, "awk scenario must provide program or program_file")
		args = append(args, sc.Input.Program)
	}
	args = append(args, sc.Input.Args...)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, awkBin, args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(sc.Input.Stdin)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	for k, v := range sc.Input.Envs {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("awk scenario timed out after %s", timeout)
	}

	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("failed to run awk candidate %s: %v", awkBin, err)
		}
	}

	return awkResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: exitCode,
	}
}

func assertAwkExpectations(t *testing.T, sc awkScenario, got awkResult) {
	t.Helper()

	assert.Equal(t, sc.Expect.ExitCode, got.exitCode, "exit code mismatch")
	if len(sc.Expect.StdoutContains) > 0 {
		for _, substr := range sc.Expect.StdoutContains {
			assert.Contains(t, got.stdout, substr, "stdout should contain %q", substr)
		}
	} else {
		assert.Equal(t, sc.Expect.Stdout, got.stdout, "stdout mismatch")
	}

	if len(sc.Expect.StderrContains) > 0 {
		for _, substr := range sc.Expect.StderrContains {
			assert.Contains(t, got.stderr, substr, "stderr should contain %q", substr)
		}
	} else {
		assert.Equal(t, sc.Expect.Stderr, got.stderr, "stderr mismatch")
	}
}

func resolveAwkExecutable(t *testing.T, value string) string {
	t.Helper()

	if filepath.IsAbs(value) {
		require.FileExists(t, value, "awk executable does not exist")
		return value
	}

	if strings.ContainsRune(value, os.PathSeparator) {
		root := repoRoot(t)
		candidate := filepath.Join(root, value)
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		wd, err := os.Getwd()
		require.NoError(t, err)
		return filepath.Join(wd, value)
	}

	resolved, err := exec.LookPath(value)
	require.NoError(t, err, "awk executable %q not found on PATH", value)
	return resolved
}

func awkScenarioTimeout(t *testing.T) time.Duration {
	t.Helper()

	value := os.Getenv("RSHELL_AWK_SCENARIO_TIMEOUT")
	if value == "" {
		return 10 * time.Second
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}
	timeout, err := time.ParseDuration(value)
	require.NoError(t, err, "invalid RSHELL_AWK_SCENARIO_TIMEOUT")
	return timeout
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
