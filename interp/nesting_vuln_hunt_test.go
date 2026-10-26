// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Regression coverage for the unbounded-nesting-depth stack-overflow crash
// (VULN-4562 / VULN-1673) and the unbounded-pipeline-depth variant
// (VULN-4535). A sub-MaxScriptBytes but deeply nested script drove the
// recursive-descent parser, the recursive validateNode walk, or the recursive
// executor past the Go runtime's 1 GiB goroutine-stack limit, aborting the
// embedding process with an uncatchable "fatal error: stack overflow" before
// any command, path, mode, or timeout policy was consulted. The fix bounds
// nesting depth with a lexical pre-parse guard (ParseScript) and an iterative
// post-parse guard (validateNode); see MaxNestingDepth.

// deepNestingScripts returns, for a given per-level repetition count, a set of
// named scripts that each nest one construct to that depth. Parser-recursive
// shapes (brackets and substitutions) are caught by the lexical guard; the
// remaining shapes parse successfully but are caught by the AST guard.
func deepNestingScripts(n int) map[string]string {
	r := strings.Repeat
	return map[string]string{
		"subshell":           r("(", n) + "x" + r(")", n),
		"cmd_substitution":   r("$(", n) + ":" + r(")", n),
		"cmd_subst_in_quote": `"` + r("$(", n) + ":" + r(")", n) + `"`,
		"brace_group":        r("{ ", n) + "x;" + r(" }", n),
		"function_body":      r("f() { ", n) + "x;" + r(" }", n),
		"param_expansion":    "echo " + r("${a:-", n) + "b" + r("}", n),
		"arithmetic":         "echo $((" + r("(", n) + "1" + r(")", n) + "))",
		"pipeline":           r("x|", n) + "x",
		"logical_and":        r("x&&", n) + "x",
		"nested_if":          r("if x; then ", n) + "x" + r("; fi", n),
		"nested_elif":        "if x; then x;" + r("elif x; then x;", n) + "fi",
		"nested_while":       r("while x; do ", n) + "x" + r("; done", n),
		"nested_for":         r("for a in b; do ", n) + "x" + r("; done", n),
		"nested_case":        r("case a in a) ", n) + "x" + r(";; esac", n),
		"if_inside_quote":    `"$(` + r("if x; then ", n) + "x" + r("; fi", n) + `)"`,
	}
}

// TestNestingGuardRejectsDeepScriptsWithoutCrashing checks that moderately
// deep scripts — above MaxNestingDepth but far below the depth that actually
// overflows the stack, so that on unpatched code they would simply parse and
// run rather than crash the test binary — are rejected with a nesting-depth
// error through either ParseScript or Run, never accepted. This fails on main
// (the scripts are accepted) and passes with the fix.
func TestNestingGuardRejectsDeepScriptsWithoutCrashing(t *testing.T) {
	const depth = MaxNestingDepth + 1000
	for name, script := range deepNestingScripts(depth) {
		t.Run(name, func(t *testing.T) {
			prog, err := ParseScript(script, "")
			if err != nil {
				if !strings.Contains(err.Error(), "nesting exceeds maximum depth") {
					t.Fatalf("ParseScript rejected for the wrong reason: %v", err)
				}
				return // rejected at parse time by the lexical guard
			}
			// Parsed successfully: the AST guard in validateNode (reached via
			// Run) must reject it.
			var stdout, stderr bytes.Buffer
			runner, err := New(StdIO(nil, &stdout, &stderr))
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if err := runner.Run(context.Background(), prog); err == nil {
				t.Fatalf("Run accepted a script nested to depth %d (stderr=%q)", depth, stderr.String())
			}
			if !strings.Contains(stderr.String(), "nesting exceeds maximum depth") {
				t.Fatalf("Run did not report the nesting limit; stderr=%q", stderr.String())
			}
		})
	}
}

// TestNestingGuardAllowsRealisticScripts guards against false positives: scripts
// with ordinary — even unusually deep — nesting must still be accepted.
func TestNestingGuardAllowsRealisticScripts(t *testing.T) {
	cases := map[string]string{
		"flat":              "echo one\necho two\necho three\n",
		"nested_cmd_subst":  `echo "$(echo "$(echo "$(echo deep)")")"`,
		"nested_if":         "if true; then if true; then if true; then echo x; fi; fi; fi",
		"for_loop":          "for i in a b c; do echo $i; done",
		"subshell_group":    "( ( ( echo x ) ) )",
		"and_or":            "true && echo a || echo b",
		"quoted_parens":     `echo "lots of ((((((((( parens ))))))))) in a string"`,
		"comment_parens":    "echo ok # (((((((((( this is only a comment ))))))))))",
		"single_quoted":     `echo '((((((((((((((((((((((((((('`,
		"moderate_subshell": strings.Repeat("( ", 300) + "echo ok" + strings.Repeat(" )", 300),
		"long_pipeline":     "echo start" + strings.Repeat(" | cat", 800),
		// Keyword-looking words inside quotes must not be counted as compound
		// commands, even repeated across a large script.
		"keywords_in_double_quotes": strings.Repeat("echo \"waiting for work to do if ready while case\"\n", 8000),
		"keywords_in_single_quotes": strings.Repeat("echo 'for do if while until case done fi esac'\n", 8000),
		"keyword_substrings":        strings.Repeat("echo ifconfig doing format cases fortune\n", 8000),
		"brackets_in_double_quotes": strings.Repeat("echo \"json { a: [ ( nested ) ] } done\"\n", 5000),
		"real_world":                "for f in a b c; do\n  if [ -n \"$f\" ]; then\n    echo \"item $f ($(basename \"$f\"))\"\n  fi\ndone\ncase \"$1\" in start) echo go;; *) echo no;; esac\n",
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			prog, err := ParseScript(script, "")
			if err != nil {
				t.Fatalf("ParseScript falsely rejected a realistic script: %v", err)
			}
			if err := checkNestingDepth(prog); err != nil {
				t.Fatalf("AST guard falsely rejected a realistic script: %v", err)
			}
		})
	}
}

// TestCheckScriptNestingDepthQuotingContexts pins the lexical guard's handling
// of the contexts in which a bracket cannot open a nested construct.
func TestCheckScriptNestingDepthQuotingContexts(t *testing.T) {
	inert := []string{
		"echo '" + strings.Repeat("(", MaxNestingDepth+100) + "'", // single-quoted literal
		"echo " + strings.Repeat("\\(", MaxNestingDepth+100),      // backslash-escaped
		"# " + strings.Repeat("(", MaxNestingDepth+100),           // whole-line comment
		"echo ok # " + strings.Repeat("(", MaxNestingDepth+100),   // trailing comment
	}
	for i, s := range inert {
		if err := checkScriptNestingDepth(s); err != nil {
			t.Errorf("case %d: inert brackets must not count toward depth: %v", i, err)
		}
	}
	if err := checkScriptNestingDepth(strings.Repeat("(", MaxNestingDepth+1)); err == nil {
		t.Errorf("real bracket nesting past the limit must be rejected")
	}
}

// --- subprocess crash test -------------------------------------------------

const deepNestingChildEnv = "RSHELL_DEEP_NESTING_CHILD"

// TestDeepNestingDoesNotCrashProcess reproduces the real exploit depth — far
// past the stack-overflow threshold on unpatched code — in a re-exec child, so
// that an uncatchable "fatal error: stack overflow" is observed as a failed
// child process rather than aborting this test binary. On main the child dies
// with a runtime-fatal stack overflow; with the fix it returns a normal error
// and exits 0.
func TestDeepNestingDoesNotCrashProcess(t *testing.T) {
	if os.Getenv(deepNestingChildEnv) == "1" {
		deepNestingChild()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestDeepNestingDoesNotCrashProcess", "-test.v")
	cmd.Env = append(os.Environ(), deepNestingChildEnv+"=1")
	out, err := cmd.CombinedOutput()
	if bytes.Contains(out, []byte("stack overflow")) {
		t.Fatalf("deeply nested script crashed the process with a stack overflow:\n%s", out)
	}
	if err != nil {
		t.Fatalf("deeply nested script crashed the child process: %v\n%s", err, out)
	}
}

// deepNestingChild runs the documented ingestion path (ParseScript then Run) on
// genuinely crash-inducing inputs and exits 0 only if every one was handled
// without a fatal runtime throw.
func deepNestingChild() {
	const depth = 300000 // well past the stack-overflow threshold on unpatched code
	for name, script := range deepNestingScripts(depth) {
		prog, err := ParseScript(script, "")
		if err != nil {
			continue // rejected by the lexical guard before parsing — fine
		}
		var stdout, stderr bytes.Buffer
		runner, nerr := New(StdIO(nil, &stdout, &stderr))
		if nerr != nil {
			fmt.Printf("child: New failed for %s: %v\n", name, nerr)
			os.Exit(3)
		}
		// Must return an error, not crash.
		_ = runner.Run(context.Background(), prog)
	}
	fmt.Println("child: all deep scripts handled without crashing")
	os.Exit(0)
}
