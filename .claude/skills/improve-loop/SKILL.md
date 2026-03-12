---
name: improve-loop
description: "Systematically review and improve every shell feature and builtin command. Iterates through each feature/command, runs code-review, fixes issues, and re-reviews until clean."
argument-hint: "[pr-number|pr-url]"
---

Systematically review and improve every shell feature and builtin command on **$ARGUMENTS** (or the current branch's PR if no argument is given), iterating until all issues are resolved.

---

## STOP — READ THIS BEFORE DOING ANYTHING ELSE

You MUST follow this execution protocol. Skipping steps or running them out of order has caused regressions and wasted iterations in every prior run of this skill.

### 1. Create the full task list FIRST

Your very first action — before reading ANY files, before running ANY commands — is to call TaskCreate for each step below. Use these exact subjects:

1. "Step 1: Identify PR and enumerate review targets"
2. "Step 2: Run the improve loop"
3. "Step 2A: Pick next review target"
4. "Step 2B: Focused review of target"
5. "Step 2C: Fix issues found"
6. "Step 2D: Run tests"
7. "Step 2E: Commit and push fixes"
8. "Step 2F: Post iteration summary as PR comment"
9. "Step 2G: Decide whether to continue"
10. "Step 3: Full sweep re-review"
11. "Step 4: Final summary"

**Note on sub-steps 2A–2G:** These are created once and reused across loop iterations. At the start of each iteration, reset all sub-steps to `pending`, then execute them in order.

### 2. Execution order and gating

Steps run strictly in this order:

```
Step 1 → Step 2 (loop: 2A → 2B → 2C → 2D → 2E → 2F → 2G) → Step 3 → Step 4
                   ↑                                      ↓
                   └──────────────── repeat ──────────────┘
```

**Top-level steps** are sequential: before starting step N, call TaskList and verify step N-1 is `completed`. Set step N to `in_progress`.

### 3. Never skip steps

- Do NOT skip the review (Step 2B) because you think the code is fine
- Do NOT skip tests (Step 2D) because fixes seem trivial
- Do NOT skip the full sweep (Step 3) because individual reviews were clean
- Do NOT mark a step completed until every sub-bullet in that step is satisfied

If you catch yourself wanting to skip a step, STOP and do the step anyway.

---

## Step 1: Identify PR and enumerate review targets

**Set this step to `in_progress` immediately after creating all tasks.**

### 1A. Identify the PR

```bash
# If argument provided, use it; otherwise detect from current branch
gh pr view $ARGUMENTS --json number,url,headRefName,baseRefName
```

If `$ARGUMENTS` is empty, this automatically falls back to the PR associated with the current branch. If no PR is found, stop and inform the user.

Store the PR number, head branch, and base branch for all subsequent steps.

```bash
gh repo view --json owner,name --jq '"\(.owner.login)/\(.name)"'
```

Store the owner and repo name.

### 1B. Enumerate review targets

Build the review target list by combining **builtin commands** and **shell features**, then **shuffle the combined list into a random order**. Each target is reviewed independently.

**Builtin commands** — list all directories under `interp/builtins/` (excluding `internal`, `tests`, `testutil`):
```bash
ls -d interp/builtins/*/ | grep -v -E '(internal|tests|testutil)' | xargs -I{} basename {}
```

**Shell features** — list all directories under `tests/scenarios/shell/`:
```bash
ls -d tests/scenarios/shell/*/  | xargs -I{} basename {}
```

**Randomize the order** — combine both lists and shuffle:
```bash
{ ls -d interp/builtins/*/ | grep -v -E '(internal|tests|testutil)' | xargs -I{} basename {}; ls -d tests/scenarios/shell/*/ | xargs -I{} basename {}; } | sort -R
```

The randomized order ensures that each run of the improve loop covers targets in a different sequence, avoiding systematic bias toward alphabetically early targets.

Create a checklist of all targets in the shuffled order. Example:

```
REVIEW TARGETS:
Commands: break, cat, continue, cut, echo, exit, false, grep, head, ls, printf, sed, strings_cmd, tail, testcmd, tr, true, uniq, wc
Shell features: allowed_paths, allowed_redirects, blocked_commands, blocked_redirects, brace_group, case_clause, cmd_separator, comments, empty_script, environment, errors, field_splitting, for_clause, function, globbing, heredoc, heredoc_dash, if_clause, inline_var, input_processing, line_continuation, logic_ops, negation, pipe, readonly, redirections, simple_command, until_clause, var_expand, while_clause
```

Mark each target as `pending`. This list will be tracked as you work through them.

**Post the plan as a PR comment** so reviewers can see the full scope upfront:

```bash
gh pr comment <pr-number> --body "$(cat <<'EOF'
### Improve Loop — Review Plan

Starting systematic review of **<total>** targets in randomized order.

#### Review order
| # | Target | Type |
|---|--------|------|
| 1 | <target> | command/feature |
| 2 | <target> | command/feature |
| ... | ... | ... |

Each target will be reviewed for security, bash compatibility, correctness, test coverage, and platform compatibility. Progress updates will be posted after each iteration.
EOF
)"
```

**Completion check:** You have the PR details, the full target list, and the plan comment is posted. Mark Step 1 as `completed`.

---

## Step 2: Run the improve loop

**GATE CHECK**: Call TaskList. Step 1 must be `completed`. Set Step 2 to `in_progress`.

Set `iteration = 1`. Maximum iterations: **50**. Repeat sub-steps A through G:

---

### Sub-step 2A — Pick next review target

Select the next `pending` target from the randomized list (in the order established in Step 1B).

If all targets are `done`, proceed to Step 3.

Mark the selected target as `in_progress`.

---

### Sub-step 2B — Focused review of target

Perform a deep review of the selected target. This is NOT a generic code review — it is a focused analysis of one specific command or feature.

#### For builtin commands:

1. **Read the full implementation:**
   ```bash
   # Read all Go files for the command
   find interp/builtins/<command>/ -name '*.go' -not -name '*_test.go'
   ```

2. **Read all existing tests:**
   - Scenario tests: `tests/scenarios/cmd/<command>/`
   - Go tests: `interp/builtins/<command>/*_test.go`

3. **Review dimensions** (check ALL of these):

   **Security:**
   - Does it use the sandbox file-access wrapper for all filesystem access? (NOT `os.Open`, `os.Stat`, `os.ReadFile`, `os.ReadDir`, `os.Lstat` directly)
   - Can crafted input cause path traversal or sandbox escape?
   - Does it handle `/dev/zero`, `/dev/random`, infinite stdin safely?
   - Does it stream output or buffer everything in memory?
   - Are there integer overflow risks in argument parsing?

   **Bash compatibility:**
   - Compare behavior against bash for edge cases:
     ```bash
     docker run --rm debian:bookworm-slim bash -c '<edge case script>'
     ```
   - Check: empty args, special characters, Unicode, large inputs, missing files, permission errors

   **Correctness:**
   - Error handling — are errors checked and propagated?
   - Exit codes — do they match bash semantics?
   - Flag parsing — does it reject unknown flags properly?

   **Test coverage:**
   - Are all flags/options tested in scenario tests?
   - Are error paths tested?
   - Are edge cases covered (empty input, special chars, large files)?
   - Missing tests = findings that must be fixed

   **Platform compatibility:**
   - Does it work on Linux, Windows, and macOS?
   - Path handling uses proper abstractions?

#### For shell features:

1. **Read the implementation** — find the relevant code in `interp/` that handles this feature
2. **Read all scenario tests** in `tests/scenarios/shell/<feature>/`
3. **Review** for correctness vs bash, edge cases, and test coverage

#### Output format

For each target, produce a findings list:

```
TARGET: <name>
FINDINGS:
  1. [P1] <title> — <file>:<line> — <description>
  2. [P2] <title> — <file>:<line> — <description>
  ...
STATUS: <CLEAN | HAS_ISSUES>
```

If `CLEAN` (no findings), mark the target as `done` and proceed to 2A.
If `HAS_ISSUES`, proceed to 2C.

---

### Sub-step 2C — Fix issues found

For each finding from 2B, implement the fix:

1. **Fix the shell implementation** to match bash behavior (NEVER change tests to match broken behavior)
2. **Add missing test scenarios** in `tests/scenarios/` (preferred over Go tests)
3. **Add missing pentest/security tests** in Go test files where scenario tests are insufficient
4. **Update documentation** (`SHELL_FEATURES.md`, `README.md`) if behavior changed

**Commit message format:** All commits MUST be prefixed with the target name:
```
[<target>] Fix null byte handling in path argument
[cat] Add missing -b flag scenario tests
[heredoc] Fix tab stripping with mixed indentation
```

---

### Sub-step 2D — Run tests

Run the test suite to verify fixes don't break anything:

```bash
# Run scenario tests for the specific command/feature
go test ./tests/ -run "TestShellScenarios" -timeout 120s

# Run unit tests for the specific builtin (if applicable)
go test ./interp/builtins/<command>/... -timeout 60s

# Run bash comparison tests (if Docker is available)
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
```

- If tests **pass** → proceed to 2E
- If tests **fail** → fix the failures (prioritize fixing the implementation, not the tests), then re-run. Maximum 3 fix attempts per test failure. If still failing after 3 attempts, log the failure and proceed.

---

### Sub-step 2E — Commit and push fixes

```bash
gofmt -w .
git add -A
git status
```

If there are changes:
```bash
git commit -m "[improve] <target>: <brief summary of fixes>"
git push origin <head-branch>
```

If no changes (target was clean), skip.

**Completion check:** Working tree is clean, branch is pushed. Proceed.

---

### Sub-step 2F — Post iteration summary as PR comment

Post a concise summary of this iteration's results as a GitHub PR comment so that progress is visible to reviewers.

```bash
gh pr comment <pr-number> --body "$(cat <<'EOF'
### Improve Loop — Iteration <iteration>: `<target>`

- **Type**: <command|feature>
- **Status**: <CLEAN (no issues found) | FIXED (N issues found and fixed)>
- **Findings**: <N> (breakdown by priority if any, e.g. 1xP1, 2xP2)
- **Fixes applied**: <brief list of fixes, or "None — target was clean">
- **Tests**: <PASS | FAIL (details)>
- **Progress**: <done>/<total> targets reviewed
EOF
)"
```

**Completion check:** PR comment posted. Proceed.

---

### Sub-step 2G — Decide whether to continue

Mark the current target as `done`.

Check progress:
- How many targets remain `pending`?
- How many targets had issues vs were clean?
- Has the iteration limit been reached?

**Decision:**

| Pending targets | Iteration | Action |
|----------------|-----------|--------|
| > 0 | <= limit | **Continue** → go back to Sub-step 2A |
| 0 | Any | **All targets reviewed** → proceed to Step 3 |
| Any | > 50 | **STOP — iteration limit reached** → proceed to Step 3 |

Log the progress:
```
PROGRESS: <done>/<total> targets reviewed, <issues_found> issues found, <issues_fixed> fixed
Current target: <name> — <CLEAN|FIXED>
```

---

**Step 2 completion check:** All targets reviewed or iteration limit reached. Mark Step 2 as `completed`.

---

## Step 3: Full sweep re-review

**GATE CHECK**: Call TaskList. Step 2 must be `completed`. Set Step 3 to `in_progress`.

Run a full sweep to catch any cross-cutting issues or regressions introduced by the individual fixes.

### 3A. Run the full test suite

```bash
go test ./... -timeout 300s
```

If any tests fail, fix them (implementation fixes, not test changes).

### 3B. Run bash comparison tests

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
```

If any bash comparison failures, fix the implementation to match bash.

### 3C. Run gofmt check

```bash
gofmt -l .
```

If any files listed, run `gofmt -w .` and commit.

### 3D. Self-review the full diff

Review the complete diff of all changes made during this session:

```bash
git diff main...HEAD
```

Look for:
- Regressions introduced by fixes
- Inconsistencies between commands (e.g., one command handles an edge case but a similar command doesn't)
- Security issues introduced by changes
- Missing documentation updates

If issues found, fix them and re-run tests.

### 3E. Push final changes

```bash
git status
git push origin <head-branch>
```

**Completion check:** All tests pass, bash comparison clean, gofmt clean, no regressions found. Mark Step 3 as `completed`.

---

## Step 4: Final summary

**GATE CHECK**: Call TaskList. Step 3 must be `completed`. Set Step 4 to `in_progress`.

Provide a summary in this exact format:

```markdown
## Improve Loop Summary

- **PR**: #<number> (<url>)
- **Targets reviewed**: <N>/<total>
- **Final status**: <CLEAN | ISSUES_REMAINING>

### Target results

| # | Target | Type | Findings | Fixes | Status |
|---|--------|------|----------|-------|--------|
| 1 | cat | command | 3 (1xP1, 2xP2) | 3 fixed | CLEAN |
| 2 | heredoc | feature | 0 | — | CLEAN |
| 3 | grep | command | 1 (1xP2) | 1 fixed | CLEAN |
| ... | ... | ... | ... | ... | ... |

### Changes made

- **Commits**: <N> commits
- **Files changed**: <list key files>
- **Tests added**: <N> new scenario tests, <N> new Go tests

### Remaining issues (if any)

- <list any unresolved findings or test failures>
```

**Post the summary as a GitHub PR comment:**
```bash
gh pr comment <pr-number> --body "<the summary markdown above>"
```

**Completion check:** Summary is output to the user AND posted as a PR comment. Mark Step 4 as `completed`.

---

## Important rules

- **ALWAYS fix the shell implementation to match bash** — never change tests to match broken behavior.
- **Prefer scenario tests over Go tests** — scenario tests are automatically validated against bash.
- **Run tests after every fix** — don't accumulate fixes without testing.
- **One target at a time** — complete the full review-fix cycle for each target before moving to the next.
- **Use gate checks** — always call TaskList and verify prerequisites before starting a step.
- **Respect the iteration limit** — hard stop at 50 iterations to prevent infinite loops.
- **Format code** — run `gofmt -w .` before every commit.
- **Stream, don't buffer** — when fixing builtins, ensure they stream output for large inputs.
- **Sandbox first** — all filesystem access must go through the sandbox wrapper, never direct `os.*` calls.
