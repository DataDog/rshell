---
name: fix-local-tests
description: Fix failing tests by prioritising shell implementation fixes to match bash behaviour
argument-hint: "[test filter or description of failure]"
allowedTools:
  - Read
  - Write
  - Edit
  - Glob
  - Grep
  - Agent
  - "Bash(go test *)"
  - "Bash(git *)"
  - "Bash(docker *)"
  - "Bash(bash -c *)"
  - "Bash(RSHELL_BASH_TEST=1 go test *)"
---

Fix failing tests. **The implementation is more likely wrong than the test.** Always try to fix the shell implementation to match bash behaviour before touching the test expectations.

---

## Workflow

### 1. Reproduce the failures

Run the relevant tests to capture the actual failures:

```bash
# If a specific test filter was given, use it:
go test -race ./interp/... ./tests/... -run "$ARGUMENTS" -v 2>&1 | head -200

# Otherwise run the full suite:
go test -race ./interp/... ./tests/... -v 2>&1 | head -200
```

If the failure involves YAML scenario tests, also run the bash comparison tests to see what bash actually produces:

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s -v 2>&1 | head -300
```

Collect every distinct failure. For each one, note:
- The test name and file
- Expected vs actual output
- The exit code difference (if any)

### 2. Determine what bash does

For **every** failure, determine the correct bash behaviour before making any changes. Use one or more of these methods:

**Method A — bash comparison test output.** If the `TestShellScenariosAgainstBash` output is available from step 1, it already shows what bash produces. Use that.

**Method B — run in Docker.** For cases not covered by comparison tests or when you need to experiment:

```bash
docker run --rm debian:bookworm-slim bash -c '<the script from the failing test>'
```

**Method C — run locally with bash.** For quick checks on macOS/Linux:

```bash
bash -c '<script>'
```

**Method D — GNU coreutils reference.** For builtin command behaviour, check `resources/gnu-coreutils-tests/` or `resources/uutils-tests/` for relevant test cases.

Record what bash produces for each failure — this is the ground truth.

### 3. Classify each failure

For each failure, classify it as one of:

| Category | Action |
|----------|--------|
| **Implementation bug** — rshell produces different output than bash | Fix the implementation in `interp/` to match bash |
| **Test expectation wrong** — test expects something different from what bash does | Fix the test to match bash behaviour |
| **Intentional divergence** — rshell behaviour deliberately differs from bash (e.g. sandbox restrictions, blocked commands) | Fix the test and set `skip_assert_against_bash: true` in YAML scenarios |

**Default assumption: the implementation is wrong.** Only classify as "test expectation wrong" or "intentional divergence" if you have clear evidence.

### 4. Fix implementation bugs (priority)

For each failure classified as an implementation bug:

1. Read the relevant implementation file in `interp/builtins/` or `interp/`
2. Understand what the code currently does vs what bash does
3. Fix the implementation to match bash behaviour
4. Run the failing test to verify the fix:
   ```bash
   go test -race ./interp/... ./tests/... -run "<test name>" -v
   ```

### 5. Fix test expectations (if needed)

For failures where the test expectation is wrong (not matching bash):

1. Update the expected output in the test to match what bash actually produces
2. For YAML scenarios, prefer `expect.stderr` over `stderr_contains` when possible
3. Ensure the test still passes against bash:
   ```bash
   RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash/<scenario> -timeout 120s -v
   ```

### 6. Fix fuzz failures

If a `Fuzz*` test is failing (either a fuzzer-discovered corpus entry or a seed):

1. Run it to see the error: `go test -v -run FuzzFuncName/corpushash ./interp/builtins/tests/<pkg>/`
2. Fix the **implementation** — never weaken the fuzz input filter to hide the bug
3. If the fix is to the input filter (e.g. the input is legitimately unsupported), that is also acceptable, but the reason must be clear from a comment
4. **Always commit the failing corpus file** at `testdata/fuzz/<FuzzFuncName>/<hash>` — it becomes a permanent regression test

To reproduce a fuzzer-found crash from a log message, create the corpus file manually:
```
go test fuzz v1
[]byte("...")
string("...")
```
Place it at `interp/builtins/tests/<pkg>/testdata/fuzz/<FuzzFuncName>/<hash>` and re-run.

### 7. Verify all fixes

After all fixes are applied, run the full test suite:

```bash
go test -race ./interp/... ./tests/... -v
```

Ensure no regressions were introduced. If new failures appear, repeat from step 1 for those failures.

### 7. Run bash comparison tests

If any YAML scenarios were touched or any builtin implementation was changed, run the bash comparison tests:

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
```

All scenarios without `skip_assert_against_bash: true` must pass.
