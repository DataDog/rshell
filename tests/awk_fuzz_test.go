// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func FuzzAwkPrintRecords(f *testing.F) {
	addAwkFuzzSeeds(f)
	oracle := requireAwkFuzzOracle(f)

	f.Fuzz(func(t *testing.T, input string) {
		if !validAwkFuzzText(input) {
			return
		}
		compareAwkFuzzProgram(t, oracle, "{ print }", nil, input)
	})
}

func FuzzAwkPrintFieldCount(f *testing.F) {
	addAwkFuzzSeeds(f)
	oracle := requireAwkFuzzOracle(f)

	f.Fuzz(func(t *testing.T, input string) {
		if !validAwkFuzzText(input) {
			return
		}
		compareAwkFuzzProgram(t, oracle, "{ print NF }", nil, input)
	})
}

func FuzzAwkCommaSeparatedFields(f *testing.F) {
	addAwkFuzzSeeds(f)
	oracle := requireAwkFuzzOracle(f)

	f.Fuzz(func(t *testing.T, input string) {
		if !validAwkFuzzText(input) {
			return
		}
		compareAwkFuzzProgram(t, oracle, "{ print NF }", []string{"-F", ","}, input)
	})
}

func FuzzAwkRegexPattern(f *testing.F) {
	addAwkFuzzSeeds(f)
	oracle := requireAwkFuzzOracle(f)

	f.Fuzz(func(t *testing.T, input string) {
		if !validAwkFuzzText(input) {
			return
		}
		compareAwkFuzzProgram(t, oracle, "/a/ { print }", nil, input)
	})
}

func addAwkFuzzSeeds(f *testing.F) {
	f.Helper()
	f.Add("")
	f.Add("alpha beta\ncharlie delta\n")
	f.Add(" leading  and  repeated   spaces \n\n")
	f.Add("a,b,c\nx,,z\n")
	f.Add("no trailing newline")
	f.Add("tab\tseparated\tfields\n")
	f.Add("carriage\r\nreturn\r\n")
}

func requireAwkFuzzOracle(f *testing.F) string {
	f.Helper()

	if os.Getenv("RSHELL_AWK_FUZZ_TEST") == "" {
		f.Skip("skipping awk fuzz tests (set RSHELL_AWK_FUZZ_TEST=1 to enable)")
	}
	if _, err := os.Stat(filepath.Join(awkFuzzRepoRoot(f), "builtins", "awk", "awk.go")); err != nil {
		f.Skip("skipping awk fuzz tests because the rshell awk builtin is not present")
	}

	gawkOracle := os.Getenv("GAWK_ORACLE")
	if gawkOracle == "" {
		f.Fatal("GAWK_ORACLE must point to the pinned GNU awk oracle")
	}
	resolved, err := exec.LookPath(gawkOracle)
	if err != nil {
		f.Fatalf("GAWK_ORACLE must point to an executable: %v", err)
	}
	version := os.Getenv("GAWK_VERSION")
	if version == "" {
		version = defaultGawkVersion
	}
	out, err := exec.Command(resolved, "--version").Output()
	if err != nil {
		f.Fatalf("failed to run %s --version: %v", resolved, err)
	}
	firstLine := strings.SplitN(string(out), "\n", 2)[0]
	if !strings.Contains(firstLine, "GNU Awk "+version) {
		f.Fatalf("GAWK_ORACLE must be GNU awk %s, got %q", version, firstLine)
	}
	return resolved
}

func awkFuzzRepoRoot(t testing.TB) string {
	t.Helper()

	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(dir)
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("could not locate repo root (expected go.mod at %s): %v", root, err)
	}
	return root
}

func validAwkFuzzText(input string) bool {
	if len(input) > 4096 {
		return false
	}
	for _, b := range []byte(input) {
		if b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

func compareAwkFuzzProgram(t *testing.T, oracle, program string, awkArgs []string, input string) {
	t.Helper()

	sc := awkScenario{
		Setup: setup{
			Files: []setupFile{
				{
					Path:    "input.txt",
					Content: input,
				},
			},
		},
		Input: awkInput{
			AwkArgs: awkArgs,
			Program: program,
			Args:    []string{"input.txt"},
		},
	}

	timeout := awkFuzzTimeout(t)
	want := runAwkScenario(t, oracle, sc, timeout)
	got := runAwkFuzzScenarioWithRshell(t, sc)

	if got.exitCode != want.exitCode {
		t.Fatalf("exit code mismatch for %q: rshell=%d gawk=%d input=%q", program, got.exitCode, want.exitCode, input)
	}
	if got.stdout != want.stdout {
		t.Fatalf("stdout mismatch for %q:\nrshell: %q\ngawk:   %q\ninput:  %q", program, got.stdout, want.stdout, input)
	}
	if got.stderr != want.stderr {
		t.Fatalf("stderr mismatch for %q:\nrshell: %q\ngawk:   %q\ninput:  %q", program, got.stderr, want.stderr, input)
	}
}

func runAwkFuzzScenarioWithRshell(t *testing.T, sc awkScenario) awkResult {
	t.Helper()

	var parts []string
	parts = append(parts, "awk")
	for _, arg := range sc.Input.AwkArgs {
		parts = append(parts, shellQuote(arg))
	}
	parts = append(parts, shellQuote(sc.Input.Program))
	for _, arg := range sc.Input.Args {
		parts = append(parts, shellQuote(arg))
	}

	allowAllCommands := true
	result := executeScenario(t, scenario{
		Setup: sc.Setup,
		Input: input{
			Script:           strings.Join(parts, " "),
			AllowedPaths:     []string{"$DIR"},
			AllowAllCommands: &allowAllCommands,
		},
	})

	return awkResult{
		stdout:   result.stdout,
		stderr:   result.stderr,
		exitCode: result.exitCode,
	}
}

func awkFuzzTimeout(t *testing.T) time.Duration {
	t.Helper()

	value := os.Getenv("RSHELL_AWK_FUZZ_TIMEOUT")
	if value == "" {
		return 2 * time.Second
	}
	timeout, err := time.ParseDuration(value)
	if err != nil {
		t.Fatalf("invalid RSHELL_AWK_FUZZ_TIMEOUT: %v", err)
	}
	return timeout
}
