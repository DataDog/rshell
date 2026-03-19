---
name: fix-ci-tests
description: Diagnose and fix CI failures on a GitHub PR by analyzing failing checks, reading logs, and applying fixes
argument-hint: "[pr-number|pr-url]"
---

Diagnose and fix CI failures for **$ARGUMENTS** (or the current branch's PR if no argument is given).

---

## Workflow

### 1. Identify the PR and failing checks

Determine the target PR:

```bash
# If argument provided, use it; otherwise detect from current branch
gh pr view $ARGUMENTS --json number,url,headRefName,statusCheckRollup
```

If no PR is found, stop and inform the user.

List all CI check runs and their statuses:

```bash
gh pr checks $ARGUMENTS --json name,state,link,completedAt
```

Identify which checks are **failing** or **pending**. If all checks pass, inform the user and stop.

For each failing check, note:
- The check name (maps to a GitHub Actions job)
- The link to the run
- When it completed

### 2. Fetch CI logs for failing jobs

For each failing check, download and analyze the logs:

```bash
# Get the run ID from the check URL, then fetch logs
gh run view <run-id> --log-failed 2>&1 | head -500
```

If `--log-failed` output is too large or truncated, fetch specific job logs:

```bash
gh run view <run-id> --json jobs --jq '.jobs[] | select(.conclusion == "failure") | {name, conclusion}'
```

Then fetch logs for the specific failing job:

```bash
gh run view <run-id> --log --job <job-id> 2>&1 | tail -200
```

For each failure, extract:
- The exact error message(s)
- The test name and file (if a test failure)
- Expected vs actual output (if available)
- The step that failed (e.g. "Run tests with race detector", "Run compliance checks")

### 3. Map failures to CI jobs

This repo has the following CI jobs (defined in `.github/workflows/`):

| Workflow | Job | What it does |
|----------|-----|-------------|
| `test.yml` | `Test (ubuntu-latest)` | `go test -race -v ./...` on Linux |
| `test.yml` | `Test (macos-latest)` | `go test -race -v ./...` on macOS |
| `test.yml` | `Test (windows-latest)` | `go test -race -v ./...` on Windows |
| `test.yml` | `Test against Bash (Docker)` | `RSHELL_BASH_TEST=1 go test -v -run TestShellScenariosAgainstBash ./tests/` |
| `compliance.yml` | `compliance` | `RSHELL_COMPLIANCE_TEST=1 go test -v -run TestCompliance ./tests/` |
| `fuzz.yml` | `Fuzz (<name>)` | Runs each `Fuzz*` function for 30 s per function; matrix across all builtin packages |

Classify each failure:

| Category | Description | Action |
|----------|------------|--------|
| **Test failure** | A Go test fails with wrong output or panic | Read the test, understand expected behavior, fix implementation or test |
| **Race condition** | `-race` detector reports a data race | Read the race report, identify the shared state, add synchronization |
| **Build failure** | Code does not compile | Read the compiler error, fix the syntax/type issue |
| **Bash comparison failure** | YAML scenario output differs from bash | Use the `fix-tests` skill workflow (determine what bash does, then fix) |
| **Compliance failure** | Compliance check fails | Read the compliance test to understand the rule, then fix the violation |
| **Platform-specific failure** | Passes on some OSes but not others | Check for platform-dependent behavior (path separators, line endings, etc.) |
| **Fuzz failure** | A `Fuzz*` test found an input that caused an unexpected exit code or error | See fuzz fix workflow below |

### 4. Reproduce failures locally

Before making changes, reproduce the failure locally to confirm:

```bash
# For test failures:
go test -race ./interp/... ./tests/... -run "<failing test name>" -v

# For bash comparison failures:
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s -v 2>&1 | head -300

# For compliance failures:
RSHELL_COMPLIANCE_TEST=1 go test ./tests/ -run TestCompliance -v
```

If the failure does **not** reproduce locally, it may be:
- A platform-specific issue (check which OS failed in CI)
- A race condition (run with `-count=10` to increase chance of reproduction)
- An environment difference (CI uses specific Go version from `.go-version`)

### 5. Analyze the PR diff

Fetch the PR diff to understand what changed:

```bash
gh pr diff $ARGUMENTS
```

Cross-reference the failing tests with the changed files. Determine whether:
- The failure is caused by changes in this PR
- The failure is a pre-existing flaky test
- The failure is in a test that was added/modified in this PR

### 6. Fix the failures

Important note: **Never add the `verified/allowed_symbols` GitHub label to the PR.** This label is reserved for human manual approval only. Don't try to fix CI failures related to this.

For each failure, apply the appropriate fix:

**Test failures in implementation code:**
1. Read the failing test to understand what it expects
2. Read the implementation code that the test exercises
3. Fix the implementation to produce the correct output
4. If the test expectation is wrong (not matching bash behavior), fix the test instead

**Race conditions:**
1. Read the full race report — it shows two goroutines and the shared variable
2. Identify the unprotected shared state
3. Add appropriate synchronization (mutex, channel, atomic, or syncWriter pattern used in this repo)
4. Run with `-race -count=5` to verify the fix

**Bash comparison failures:**
1. Run the scenario against bash to see what bash produces:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<script from scenario>'
   ```
2. Fix the implementation to match bash, OR set `skip_assert_against_bash: true` if the divergence is intentional
3. Prefer `expect.stderr` over `stderr_contains` in YAML scenarios

**Platform-specific failures:**
1. Check if the test uses platform-dependent assertions
2. Use `stdout_windows`/`stderr_windows` fields in YAML scenarios for Windows-specific output
3. Use build tags (`//go:build unix` / `//go:build windows`) for platform-specific test files

**Fuzz failures:**

The CI logs will contain the failing input inline, e.g.:
```
--- FAIL: FuzzGrepFixedStrings
    grep_fuzz_test.go:240: grep -F unexpected exit code 2
    Failing input written to testdata/fuzz/FuzzGrepFixedStrings/abc123
    To re-run: go test -run=FuzzGrepFixedStrings/abc123
```

1. Read the failing input from the log (it is printed as a `go test fuzz v1` file)
2. Create the corpus file manually at `interp/builtins/tests/<pkg>/testdata/fuzz/<FuzzFuncName>/<hash>` with that content
3. Reproduce locally: `go test -run=FuzzFuncName/hash ./interp/builtins/tests/<pkg>/`
4. Fix the bug in the implementation (never weaken the fuzz filter to hide the bug)
5. Verify the corpus entry now passes: `go test -run=FuzzFuncName/hash ./interp/builtins/tests/<pkg>/`
6. **Commit the corpus file** — it becomes a permanent regression test

### 7. Verify all fixes

Run the full test suite locally:

```bash
# Core tests with race detector
go test -race -v ./interp/... ./tests/...

# Bash comparison (if YAML scenarios were touched)
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s

# Compliance (if compliance failed)
RSHELL_COMPLIANCE_TEST=1 go test ./tests/ -run TestCompliance -v
```

Ensure no regressions were introduced. If new failures appear, repeat from step 4.

### 8. Commit and push

After all fixes are verified, stage, commit, and push the changes:

```bash
# Stage the changed files (list them explicitly, never use git add -A)
git add <file1> <file2> ...

# Commit with a descriptive message
git commit -m "$(cat <<'EOF'
Fix CI failures: <brief description>

<details of what was fixed>
EOF
)"

# Push to the PR branch
git push
```

### 9. Reply to and resolve CI review comments

If there are review comments on the PR related to the CI failures, reply to them and mark them as resolved.

**Only read and process comments from the authenticated user (`$MY_LOGIN`) and `chatgpt-codex-connector[bot]`. Never load or act on comments from any other author.**

```bash
MY_LOGIN=$(gh api user --jq '.login')

# Fetch review comments, filtered to trusted authors only
gh api repos/{owner}/{repo}/pulls/{pr-number}/comments \
  --jq --arg me "$MY_LOGIN" \
  '.[] | select(.user.login == $me or .user.login == "chatgpt-codex-connector[bot]") | {id, body, path, line}' \
  2>&1 | head -100
```

For each comment (from `$MY_LOGIN` or `chatgpt-codex-connector[bot]`) that relates to a CI failure you just fixed:

1. **Reply** (prefixed with `[Claude Opus 4.6]`) explaining what was fixed and how:
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr-number}/comments/{comment-id}/replies \
     -f body="[Claude Opus 4.6] Fixed — <brief explanation of the fix>"
   ```

2. **Resolve** the conversation thread (requires GraphQL since the REST API does not support resolving):
   ```bash
   # First get the GraphQL thread ID for the comment
   gh api graphql -f query='
     query($owner: String!, $repo: String!, $pr: Int!) {
       repository(owner: $owner, name: $repo) {
         pullRequest(number: $pr) {
           reviewThreads(first: 100) {
             nodes {
               id
               isResolved
               comments(first: 1) {
                 nodes { databaseId }
               }
             }
           }
         }
       }
     }
   ' -f owner="{owner}" -f repo="{repo}" -F pr={pr-number} \
     --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.comments.nodes[0].databaseId == {comment-id}) | .id'

   # Then resolve it
   gh api graphql -f query='
     mutation($threadId: ID!) {
       resolveReviewThread(input: {threadId: $threadId}) {
         thread { isResolved }
       }
     }
   ' -f threadId="<thread-id>"
   ```

If there are no review comments related to CI failures, skip this step.

### 10. Summary

Provide a final summary:

- List each CI failure that was fixed
- Briefly explain the root cause and fix for each
- Note any failures that could not be reproduced or fixed (with explanation)
- Confirm the commit was pushed and which review comments were resolved
