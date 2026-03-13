---
name: improve-test-coverage
description: Improve test coverage for shell features and commands using reference test suites from yash, GNU coreutils, and uutils/coreutils
argument-hint: "[command-name|shell-feature|all]"
---

Improve test coverage for **$ARGUMENTS** by mining reference test suites from yash, GNU coreutils, and uutils/coreutils for gaps in our scenario tests.

---

## ⛔ STOP — READ THIS BEFORE DOING ANYTHING ELSE ⛔

You MUST follow this execution protocol. Skipping steps causes missed coverage gaps or broken tests.

### 1. Create the full task list FIRST

Your very first action — before reading ANY files, before writing ANY code — is to call TaskCreate exactly 11 times, once for each step below (Steps 1–11). Use these exact subjects:

1. "Step 1: Determine scope"
2. "Step 2: Download and read reference test suites"
3. "Step 3: Audit existing coverage"
4. "Step 4: Identify coverage gaps"
5. "Step 5: Write new scenario tests"
6. "Step 6: Check for duplicate tests"
7. "Step 7: Review skip_assert_against_bash flags"
8. "Step 8: Review unnecessary Windows-specific assertions"
9. "Step 9: Verify all tests pass"
10. "Step 10: Run bash comparison tests"
11. "Step 11: Post report as PR comment"

### 2. Execution order

Steps run strictly sequentially: 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11.

Before starting step N, call TaskList and verify step N-1 is `completed`. Set step N to `in_progress`.

Before marking any step as `completed`:
- Re-read the step description and verify every sub-bullet is satisfied
- If any sub-bullet is not done, keep working — do NOT mark it completed

---

## Context

The safe shell interpreter (`interp/`) implements all commands as Go builtins — it never executes host binaries. Test scenarios are YAML files in `tests/scenarios/` that are automatically validated against both the shell and bash (via Docker).

### Reference test suites

Three external test suites serve as coverage references:

1. **yash** — a POSIX-compliant shell with thorough tests for shell language features (control flow, expansion, quoting, redirections, etc.)
   - Repository: https://github.com/magicant/yash/tree/trunk/tests
   - License: GPL v2; use for test **design** reference only, not verbatim copy
   - Strengths: POSIX shell language edge cases, quoting, expansion, control flow

2. **GNU coreutils** — reference implementation tests for command-line utilities
   - Repository: https://github.com/coreutils/coreutils/tree/master/tests
   - License: GPL v3; use for test **design** reference only
   - Offline: `resources/gnu-coreutils-tests/`
   - Strengths: flag combinations, edge cases, error handling, multi-file behavior

3. **uutils/coreutils** — Rust rewrite of coreutils with MIT-licensed tests
   - Repository: https://github.com/uutils/coreutils/tree/main/tests
   - License: MIT; test logic can be freely adapted
   - Offline: `resources/uutils-tests/`
   - Strengths: integer edge cases, Unicode, binary input, overflow guards, cross-platform

### How to decide which suite to consult

| Target | Primary suite | Secondary suite |
|--------|--------------|-----------------|
| Shell language features (control flow, expansion, quoting, redirections, etc.) | yash | — |
| Builtin commands (cat, head, grep, etc.) | GNU coreutils + uutils | yash (for piping/integration) |
| Both | All three | — |

---

## Step 1: Determine scope

Based on the argument (`$ARGUMENTS`), determine what to improve coverage for:

- **A specific command** (e.g. `cat`, `head`, `grep`): Focus on that command's scenario tests in `tests/scenarios/cmd/<command>/`
- **A shell feature** (e.g. `var_expand`, `globbing`, `pipe`): Focus on that feature's scenario tests in `tests/scenarios/shell/<feature>/`
- **`all`**: Scan all commands and shell features, prioritize the ones with the fewest tests relative to their complexity

If `$ARGUMENTS` is a command, verify it exists as an implemented builtin by checking `interp/builtins/`. If it is a shell feature, verify the directory exists in `tests/scenarios/shell/`.

List the current test count for the target and note which subdirectories/categories exist.

## Step 2: Download and read reference test suites

### For builtin commands

First check if offline resources exist:

```bash
ls resources/gnu-coreutils-tests/ 2>/dev/null | head -5
ls resources/uutils-tests/ 2>/dev/null | head -5
```

If offline resources are available, use them. Otherwise download:

```bash
# GNU coreutils tests
curl -sL https://github.com/coreutils/coreutils/archive/refs/heads/master.tar.gz | tar -xz -C /tmp
# Tests are at: /tmp/coreutils-master/tests/<command>/

# uutils tests
curl -sL https://github.com/uutils/coreutils/archive/refs/heads/main.tar.gz | tar -xz -C /tmp
# Tests are at: /tmp/coreutils-main/tests/by-util/test_<command>.rs
```

Read the relevant test files for the target command. For each test suite:
- List all test files/functions
- Note what each test covers (flags, edge cases, error conditions)
- Pay special attention to tests that exercise flag combinations, boundary conditions, and error paths

### For shell features

Download the yash test suite:

```bash
curl -sL https://github.com/magicant/yash/archive/refs/heads/trunk.tar.gz | tar -xz -C /tmp
# Tests are at: /tmp/yash-trunk/tests/
```

Read the relevant yash test files. yash tests are shell scripts organized by feature (e.g. `if-y.tst`, `for-y.tst`, `quote-y.tst`, `param-y.tst`, `expand-y.tst`). For each test:
- Note the test description and what behavior it validates
- Pay attention to POSIX edge cases and corner cases in expansion, quoting, and control flow

### For commands: also check yash for integration patterns

Even when targeting a command, skim yash tests for integration patterns — e.g. how commands interact with pipes, redirections, subshells, and variable expansion.

## Step 3: Audit existing coverage

Read all existing scenario tests for the target:

```bash
# For a command:
find tests/scenarios/cmd/<command>/ -name "*.yaml" | sort

# For a shell feature:
find tests/scenarios/shell/<feature>/ -name "*.yaml" | sort
```

Read each YAML file and build a coverage matrix. For each test, note:
- What flag/feature/behavior it exercises
- Whether it covers happy path, edge cases, or error cases
- Whether it has `skip_assert_against_bash: true`

Also read the Go test files if they exist:

```bash
# For a command:
find interp/builtins/ -path "*<command>*_test.go" | sort
find interp/ -name "builtin_<command>*_test.go" | sort
```

Build a summary table of what is currently tested.

## Step 4: Identify coverage gaps

Cross-reference the reference test suites (Step 2) against existing coverage (Step 3) to find gaps.

### Gap categories to look for

**For commands:**

| Category | What to check |
|----------|--------------|
| **Untested flags** | Flags that are implemented but have no scenario test exercising them |
| **Flag combinations** | Pairs/triples of flags used together (reference suites often test these) |
| **Edge case inputs** | Empty file, single-line file, no trailing newline, binary input, very long lines |
| **Error conditions** | Missing file, directory as argument, permission denied, invalid flag values |
| **stdin behavior** | Command reading from pipe vs file, `-` as filename, interactive vs non-interactive |
| **Multi-file behavior** | Multiple file arguments, mix of valid and invalid files, header formatting |
| **Numeric boundaries** | Zero, one, large values, negative values (where applicable) |
| **Special characters** | Filenames with spaces, newlines, Unicode, glob characters |

**For shell features:**

| Category | What to check |
|----------|--------------|
| **Quoting edge cases** | Nested quotes, escaped characters in different quoting contexts |
| **Expansion edge cases** | Unset variables, empty variables, special parameters ($?, $#, $@, $*) |
| **Control flow edge cases** | Empty bodies, nested loops, break/continue with counts |
| **Redirection edge cases** | Multiple redirections, fd duplication, here-documents with expansion |
| **Error handling** | Syntax errors, failed commands in pipelines, exit codes from control structures |
| **Word splitting** | IFS variations, empty fields, splitting with special characters |
| **Globbing** | No matches, dot files, special patterns, escaped glob characters |

### Filtering

Skip reference tests that:
- Test flags we intentionally do not implement (check the builtin's doc comment or `--help` output)
- Test write/execute operations that our sandbox blocks
- Test platform-specific kernel features (`/proc`, `/sys`, inotify)
- Test GNU-specific extensions beyond POSIX that we don't support
- Rely on external commands we don't implement

### Priority

Rank gaps by importance:
1. **P1 — Missing basic coverage**: A flag or feature has zero scenario tests
2. **P2 — Missing edge cases**: Basic behavior is tested but edge cases from reference suites are not
3. **P3 — Missing error paths**: Error conditions referenced in test suites are not covered
4. **P4 — Missing combinations**: Flag combinations or integration scenarios are not covered

Present the gap analysis to the user as a summary table before proceeding to write tests. Include:
- The gap description
- The reference test it was derived from (suite + test name/function)
- The priority level
- Whether it needs `skip_assert_against_bash: true`

## Step 5: Write new scenario tests

For each identified gap, create a YAML scenario test file. Follow the project conventions:

### File organization

```
tests/scenarios/cmd/<command>/<category>/<test_name>.yaml
tests/scenarios/shell/<feature>/<category>/<test_name>.yaml
```

Group into subdirectories by concern:
- `basic/` — core functionality, default behavior
- `flags/` — individual flag behavior
- `combinations/` — flag combinations
- `edge_cases/` — boundary conditions, special inputs
- `errors/` — error conditions, invalid inputs
- `stdin/` — pipe and stdin behavior
- `multifile/` — multi-file argument behavior
- `integration/` — interactions with other shell features

### YAML format

```yaml
# Derived from <suite> <test-name/function>
description: One sentence describing what this scenario tests.
setup:
  files:
    - path: relative/path/in/tempdir
      content: "file content here"
      chmod: 0644           # optional
      symlink: target/path  # optional
input:
  allowed_paths: ["$DIR"]   # "$DIR" resolves to the temp dir
  script: |+
    command arguments
expect:
  stdout: "expected output\n"
  stderr: ""
  exit_code: 0
```

### Rules

- **Source attribution**: Include a comment at the top of each YAML file noting which reference test it was derived from (e.g. `# Derived from GNU coreutils head/head-elide-tail.sh` or `# Derived from uutils test_head.rs::test_head_big_n` or `# Derived from yash if-y.tst case 3`)
- **`stdout_contains` and `stderr_contains` must be YAML lists**, not scalar strings
- **Prefer `expect.stderr`** (exact match) over `stderr_contains` unless the error message is platform-specific
- **Prefer `expect.stdout`** (exact match) over `stdout_contains` whenever possible
- Tests are asserted against bash by default — only set `skip_assert_against_bash: true` for features that intentionally diverge from bash
- Use `stdout_windows`/`stderr_windows` for platform-specific output differences
- Use `|+` for multi-line content to preserve trailing newlines
- Do **not** use `echo -n` — the echo builtin does not support `-n` and will emit `-n ` literally. Use `printf` instead for newline-free output
- When testing error messages from our builtins, the error format is typically `<command>: <message>` — verify by running the command in the shell first

### Determining expected output

For each new test, determine the correct expected output:

**Method A — Run in our shell first:**
```bash
go run . -c '<script>'
```

**Method B — Run in bash (Docker):**
```bash
docker run --rm debian:bookworm-slim bash -c '<script>'
```

**Method C — Run locally with bash:**
```bash
bash -c '<script>'
```

Always verify that our shell output matches bash for tests without `skip_assert_against_bash: true`.

### Batch size

Write tests in batches of 10-15 files, then run verification (Step 9) before writing more. This catches format errors early.

## Step 6: Check for duplicate tests

Scan all scenario tests for the target (including newly written ones) and identify duplicates — tests that exercise the same behavior with the same or nearly identical scripts and expected output.

### How to detect duplicates

1. **Exact duplicates**: Two YAML files with identical `input.script` and `expect` sections (possibly different descriptions or filenames)
2. **Near duplicates**: Tests with functionally equivalent scripts that test the same behavior (e.g. same command with same flags, just different variable names or file content that doesn't change the behavior being tested)
3. **Subset duplicates**: A test that is a strict subset of another test — everything it validates is already covered by a more comprehensive test

### Process

```bash
# For each pair of YAML files, compare the script and expected output
# Look for files with identical or near-identical scripts
find tests/scenarios/cmd/<command>/ -name "*.yaml" -exec grep -l "script:" {} \; | sort
```

For each duplicate found:
- Note which files are duplicates and why
- Recommend which one to keep (prefer the one with better description, more complete assertions, or better file organization)
- Remove the duplicate file

If no duplicates are found, note that and move on.

## Step 7: Review skip_assert_against_bash flags

Scan all scenario tests for the target that have `skip_assert_against_bash: true` and evaluate whether the flag is still needed.

```bash
grep -rl "skip_assert_against_bash: true" tests/scenarios/cmd/<command>/ 2>/dev/null
grep -rl "skip_assert_against_bash: true" tests/scenarios/shell/<feature>/ 2>/dev/null
```

For each flagged test:

1. **Read the test** to understand what behavior it tests
2. **Determine if the divergence is still intentional**: Run the script in bash to see what bash produces
   ```bash
   bash -c '<script from test>'
   # or
   docker run --rm debian:bookworm-slim bash -c '<script>'
   ```
3. **Classify the result**:

| Result | Action |
|--------|--------|
| Our shell now matches bash | Remove `skip_assert_against_bash: true` |
| Divergence is a bug in our shell | Note the bug, keep the flag for now, add to findings |
| Divergence is intentional (sandbox, blocked commands, readonly) | Keep the flag, verify the comment explains why |
| Test scenario is wrong (neither matches bash nor tests intentional divergence) | Fix the test expectations |

For each test where the flag is removed, verify it passes against bash:

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run "TestShellScenariosAgainstBash/<scenario_name>" -timeout 120s -v
```

## Step 8: Review unnecessary Windows-specific assertions

Scan all scenario tests for the target that use Windows-specific assertion fields and evaluate whether they are actually needed.

```bash
grep -rl "stdout_windows\|stderr_windows\|stdout_contains_windows\|stderr_contains_windows" tests/scenarios/cmd/<command>/ 2>/dev/null
grep -rl "stdout_windows\|stderr_windows\|stdout_contains_windows\|stderr_contains_windows" tests/scenarios/shell/<feature>/ 2>/dev/null
```

For each test with Windows-specific assertions:

1. **Read the test** and compare the Windows-specific field value against the non-Windows field value
2. **Evaluate whether the difference is genuine**: Windows-specific assertions are only needed when output genuinely differs on Windows — typically:
   - Path separators (`\` vs `/`)
   - Line endings (`\r\n` vs `\n`)
   - OS-specific error messages
   - Windows reserved filenames (CON, PRN, NUL, etc.)
3. **Classify the result**:

| Result | Action |
|--------|--------|
| Windows value is identical to non-Windows value | Remove the Windows-specific field (redundant) |
| Windows value differs only due to path separators or line endings | Keep — this is a valid platform difference |
| Windows value differs for unclear reasons | Investigate further; if no genuine platform difference exists, remove |
| Windows field exists but non-Windows field is missing | Check if both should have the same value and consolidate |

For each unnecessary Windows-specific assertion removed, the non-Windows assertion serves as the fallback and will be used on all platforms.

## Step 9: Verify all tests pass

After writing each batch of tests, run:

```bash
# Run all scenario tests
go test ./tests/ -run TestShellScenarios -timeout 120s -v 2>&1 | tail -50
```

If any test fails:
1. Read the failure output carefully
2. Determine if the issue is in the test expectation or the shell implementation
3. **Default assumption: fix the test to match actual bash behavior** (for coverage improvement, we're adding tests for existing behavior, not finding bugs)
4. If you discover a genuine shell bug, note it but still write the test with the correct (bash) expected output — the test will fail and serve as a regression target

Fix all failures before proceeding to the next batch or to Step 10.

Also run Go tests to ensure no regressions:

```bash
go test -race ./interp/... -timeout 120s
```

## Step 10: Run bash comparison tests

After all new tests are written and passing, run the full bash comparison suite:

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
```

For any failures:
1. Check what bash actually produces
2. If our shell diverges from bash:
   - If the divergence is intentional (sandbox restriction), add `skip_assert_against_bash: true`
   - If the divergence is a bug, note it as a finding and either fix the test expectation to match bash or leave it as a failing test that documents the bug
3. Re-run until all tests pass

## Step 11: Post report as PR comment

After all tests pass, produce a summary report and post it as a comment on the current PR.

### 1. Determine the PR number

```bash
gh pr view --json number --jq '.number'
```

If no PR exists for the current branch, skip this step and print the report to the console instead.

### 2. Build the report

Compose the report in the following format:

```markdown
## Coverage Improvement Summary

**Target**: <command or feature>
**Reference suites consulted**: <list>

### New tests added
| File | Category | Derived from | Description |
|------|----------|-------------|-------------|
| ... | ... | ... | ... |

### Coverage before/after
- Before: N scenario tests
- After: M scenario tests (+X new)
- New categories covered: <list>

### Cleanup
- Duplicate tests removed: <count>
- `skip_assert_against_bash` flags removed: <count>
- Unnecessary Windows-specific assertions removed: <count>

### Findings
- <any shell bugs discovered>
- <any intentional divergences noted>

---
🤖 Generated with [Claude Code](https://claude.com/claude-code)
```

### 3. Post the comment

```bash
gh pr comment <PR_NUMBER> --body "$(cat <<'EOF'
<report content here>
EOF
)"
```

If posting fails (e.g. permissions), print the report to the console as a fallback.
