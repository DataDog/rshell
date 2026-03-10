---
name: code-auditor
description: Audit code changes for security issues, bash compatibility, correctness, and adherence to project conventions
argument-hint: "[pr-number|pr-url|file-path|commit-range]"
---

Audit code for security issues, bash compatibility, correctness, and project convention adherence in **$ARGUMENTS** (or the current branch's uncommitted/staged changes if no argument is given).

---

## Workflow

### 1. Determine the scope of the audit

Identify what code to audit based on the argument:

```bash
# PR number or URL — audit the PR diff
gh pr diff $ARGUMENTS

# No argument — audit uncommitted + staged changes on the current branch
git diff HEAD

# If on a feature branch, also compare against main
git diff main...HEAD
```

If no changes are found, inform the user and stop.

### 2. Read and understand all changed code

For each changed file:

1. **Read the full file** (not just the diff) to understand context
2. **Read surrounding code** — callers, callees, and related functions
3. **Read relevant tests** — check if the changed behavior is tested

### 3. Security audit

This is a restricted shell interpreter where **safety is the primary goal**. Audit for:

#### 3a. Shell escape / sandbox bypass

- Can the change allow execution of blocked commands?
- Can it bypass filesystem restrictions (e.g. redirect to paths outside the sandbox)?
- Can it escape the restricted shell environment?
- Does it introduce new ways to execute arbitrary code?

#### 3b. Input handling

- Are shell arguments, variables, and expansions handled safely?
- Can malicious input cause unexpected behavior (e.g. glob injection, variable injection)?
- Are error paths handled — do they leak information or leave state inconsistent?

#### 3c. Concurrency safety

- Is shared state properly protected (mutexes, atomics, channels)?
- Run a mental model of concurrent access — can two goroutines race on the same data?
- Check if the `-race` detector would catch any issues

#### 3d. Import and dependency safety

- Are new imports from the Go standard library or approved packages?
- Check the builtin import allowlist if new imports are added to builtins
- No unsafe packages (`unsafe`, `os/exec`, `net/http`) unless explicitly justified

### 4. Bash compatibility audit

**The shell must match bash behavior unless it intentionally diverges.** For every behavioral change:

1. **Determine what bash does**:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<relevant script>'
   ```

2. **Compare with the implementation** — does the changed code produce the same output, exit code, and side effects as bash?

3. **Check edge cases** — empty strings, special characters, Unicode, large inputs, missing files, permission errors

4. **Classify any divergence**:

| Divergence type | Action |
|----------------|--------|
| Unintentional — implementation doesn't match bash | Flag as a **bug** that must be fixed |
| Intentional — sandbox restriction, blocked command, readonly enforcement | Verify `skip_assert_against_bash: true` is set in relevant test scenarios |
| Unknown — unclear if intentional | Flag for clarification |

### 5. Correctness audit

- **Logic errors** — off-by-one, wrong operator, missing nil/empty checks, incorrect loop bounds
- **Error handling** — are errors checked and propagated correctly? Are cleanup paths correct?
- **State management** — does the change leave interpreterstate consistent after errors, signals, and edge cases?
- **Return values and exit codes** — do they match expected POSIX/bash semantics?

### 6. Test coverage audit

- **Are new behaviors tested?** Every new code path should have a corresponding test
- **Are edge cases tested?** Empty input, boundary values, error conditions
- **YAML scenario conventions**:
  - Prefer `expect.stderr` over `stderr_contains`
  - Tests are asserted against bash by default — only set `skip_assert_against_bash: true` for intentional divergences
  - Use `stdout_windows`/`stderr_windows` for platform-specific output
- **Bash comparison**: If YAML scenarios are added or modified, verify they pass:
  ```bash
  RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
  ```

### 7. Code quality audit

- **Naming** — clear, consistent with existing codebase conventions
- **Complexity** — is the change more complex than necessary? Could it be simplified?
- **Duplication** — does it duplicate existing functionality?
- **Documentation** — `README.md` and `SHELL_FEATURES.md` must be updated if user-facing behavior changes

### 8. Platform compatibility

- Does the change work on Linux, Windows, and macOS?
- Path separators, line endings, OS-specific APIs?
- Are platform-specific test assertions using the correct fields (`stdout_windows`, etc.)?

### 9. Produce the audit report

Organize findings by severity:

#### Critical
Issues that **must** be fixed before merging:
- Security vulnerabilities (sandbox bypass, shell escape, unsafe input handling)
- Correctness bugs that produce wrong output vs bash
- Data races or concurrency issues
- Missing error handling that could cause panics

#### Warning
Issues that **should** be fixed:
- Unintentional bash compatibility divergences
- Missing test coverage for new behavior
- Documentation not updated for user-facing changes
- Code that works but is fragile or hard to maintain

#### Info
Suggestions for improvement (optional to address):
- Style or naming inconsistencies
- Minor simplifications
- Additional edge case tests that would be nice to have

For each finding, include:
1. **File and line** — exact location
2. **Description** — what the issue is
3. **Bash behavior** — what bash does (if applicable), with the command to verify
4. **Suggested fix** — concrete code or approach to resolve it

### 10. Summary

Provide a summary with:
- Total findings by severity (critical / warning / info)
- Overall assessment — safe to merge, needs fixes, or needs major rework
- The most important issue to address first
