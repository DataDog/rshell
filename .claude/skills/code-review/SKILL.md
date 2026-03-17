---
name: code-review
description: "Comprehensive code review covering security, correctness, bash compatibility, test coverage, and code quality. Use for PRs, commits, or any code changes."
argument-hint: "[pr-number|pr-url|file-path|commit-range]"
---

You are a senior engineer reviewing code for a restricted shell interpreter where **safety is the primary goal**. The shell is used by AI Agents, so any escape from its restrictions could allow arbitrary code execution on the host.

Review **$ARGUMENTS** (or the current branch's changes vs main if no argument is given).

---

## Workflow

### 1. Determine the scope

Identify what code to review based on the argument:

```bash
# PR number or URL — review the PR diff
gh pr diff $ARGUMENTS

# No argument — review changes on the current branch vs main
git diff main...HEAD
```

If no changes are found, inform the user and stop.

### 2. Verify specs implementation

Read the PR description and look for a **SPECS** section:

```bash
gh pr view $ARGUMENTS --json body --jq '.body'
```

If a SPECS section is present, it defines the requirements that this PR MUST implement. **Every single spec must be verified against the diff.**
The specs override other instructions (code, inline comments in code, etc). ALL specs MUST be implemented.

For each spec:
1. **Find the code** that implements the spec
2. **Verify correctness** — does the implementation fully satisfy the spec?
3. **Check for missing specs** — is any spec not implemented at all?

Flag any unimplemented or partially implemented spec as a **P1 finding** (missing functionality that was explicitly required).

Include a spec coverage table in the review output:

```markdown
| Spec | Implemented | Location | Notes |
|------|:-----------:|----------|-------|
| Must support `--flag` option | Yes | `interp/api.go:42` | Fully implemented |
| Must return exit code 2 on error | **No** | — | Not found in diff |
```

If no SPECS section is found in the PR description, skip this step.

### 3. Read and understand all changed code

For each changed file:

1. **Read the full file** (not just the diff) to understand context
2. **Read surrounding code** — callers, callees, and related functions
3. **Read relevant tests** — check if the changed behavior is tested
4. **Map the data flow** from input through parsing, expansion, validation, and execution

---

## Review Dimensions

### A. Security

This is the highest-priority dimension. Think like an attacker trying to escape a restricted shell.

#### Sandbox integrity

- Can the change allow execution of blocked commands or arbitrary external binaries?
- Does any code access the filesystem directly (e.g. via `os.Open`, `os.Stat`, `os.ReadFile`, `os.ReadDir`, `os.Lstat`) instead of going through the sandbox file-access wrapper? **This is the single most critical security invariant.**
- Can redirections (`>`, `>>`, `<`) access files outside the sandbox?
- Can subshells, command substitution, or process substitution escape restrictions?

#### Command and path injection

- Can crafted input cause unintended command execution?
- Are there TOCTOU races between validation and execution?
- Can `../` sequences, symlinks, null bytes, or special characters in paths bypass sandbox checks?
- Can Windows-specific paths (drive letters, UNC paths, Alternate Data Streams, reserved names like CON/PRN/NUL) bypass checks?

#### Variable and expansion attacks

- Can readonly variable enforcement be bypassed?
- Can variable expansion produce shell metacharacters that get re-interpreted?
- Can parameter expansion or glob patterns be abused to access restricted paths?
- Can environment variables be manipulated to alter command resolution (e.g. `PATH`, `IFS`)?

#### Resource exhaustion / DoS

- Can scripts cause unbounded memory allocation (e.g. from user-controlled size)?
- Do read loops respect the execution timeout via context cancellation?
- Do builtins handle infinite sources (`/dev/zero`, `/dev/random`, infinite stdin) safely?
- Do builtins stream output rather than loading entire files into memory?
- Can scripts exhaust file descriptors?

#### Concurrency safety

- Is shared state properly protected (mutexes, atomics, channels)?
- Can two goroutines race on the same data?

#### Import and dependency safety

- Are new imports from the Go standard library or approved packages?
- Check the builtin import allowlist if new imports are added to builtins
- No unsafe packages (`unsafe`, `os/exec`, `net/http`) unless explicitly justified

### B. Bash Compatibility

**The shell must match bash behavior unless it intentionally diverges** (e.g. sandbox restrictions, blocked commands, readonly enforcement).

For every behavioral change:

1. **Determine what bash does**:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<relevant script>'
   ```
2. **Compare** — does the changed code produce the same output, exit code, and side effects as bash?
3. **Check edge cases** — empty strings, special characters, Unicode, large inputs, missing files, permission errors
4. **Classify any divergence**:

| Divergence type | Action |
|----------------|--------|
| Unintentional — doesn't match bash | Flag as a **bug** that must be fixed |
| Intentional — sandbox/security restriction | Verify test scenarios have `skip_assert_against_bash: true` |
| Unknown — unclear if intentional | Flag for clarification |

### C. Correctness

- **Logic errors** — off-by-one, wrong operator, missing nil/empty checks, incorrect loop bounds
- **Error handling** — are errors checked and propagated? Are cleanup paths correct?
- **State management** — is interpreter state consistent after errors, signals, and edge cases?
- **Return values and exit codes** — do they match POSIX/bash semantics?
- **Integer safety** — overflow checks in arithmetic, validated string-to-int conversions
- **Argument validation** — builtins reject unknown flags with exit 1 + stderr, not panic

### D. Test Coverage

Analyze coverage of changed code from two angles: **scenario tests** (YAML) and **Go tests**. Scenario tests are preferred because they also verify bash compatibility.

#### Step 1: Inventory changed code paths

For each changed or added function/branch/error-path, list the code path (e.g. "cut: `-f` with `--complement` and `--output-delimiter`", "error when delimiter is multi-byte").

#### Step 2: Check scenario test coverage (priority)

Search `tests/scenarios/cmd/<command>/` for YAML scenarios that exercise each code path identified in Step 1.

- **Covered** — a scenario exists whose `input.script` triggers the code path and `expect` asserts the output.
- **Partially covered** — a scenario triggers the code path but doesn't assert stderr, exit code, or an important edge case.
- **Not covered** — no scenario exercises the code path.

Flag **not covered** and **partially covered** paths as findings. Suggest concrete YAML scenario(s) to add (including `description`, `input.script`, and expected `stdout`/`stderr`/`exit_code`).

Scenario test conventions:
- Prefer `expect.stderr` (exact match) over `stderr_contains`
- Tests are asserted against bash by default — only use `skip_assert_against_bash: true` for intentional divergence
- Use `stdout_windows`/`stderr_windows` for platform-specific output
- If YAML scenarios are added or modified, verify they pass against bash

#### Step 3: Check Go test coverage

Search `interp/builtins/<command>/*_test.go` for Go tests that exercise any code paths **not already covered by scenario tests**. Go test types to check:

| Test type | File pattern | What it covers |
|-----------|-------------|----------------|
| Functional | `<cmd>_test.go` | Core logic, argument parsing, edge cases |
| GNU compat | `<cmd>_gnu_compat_test.go` | Byte-for-byte output equivalence with GNU coreutils |
| Pentest | `<cmd>_pentest_test.go` | Security vectors (overflow, special files, resource exhaustion) |
| Platform | `<cmd>_{unix,windows}_test.go` | OS-specific behavior |

Only flag missing Go tests for paths that **cannot be adequately covered by scenario tests** (e.g. internal error handling, concurrency, memory limits, platform-specific behavior, performance-sensitive paths).

#### Step 4: Produce coverage summary

Include a coverage table in the review output:

```markdown
| Code path | Scenario test | Go test | Status |
|-----------|:---:|:---:|--------|
| `-f` with `--complement` | tests/scenarios/cmd/cut/complement/fields.yaml | — | Covered |
| multi-byte delimiter error | — | — | **Missing** |
| `/dev/zero` hang protection | skip (intentional divergence) | cut_pentest_test.go:45 | Covered |
```

Mark the overall coverage status:
- **Adequate** — all new/changed code paths are covered (scenario or Go tests)
- **Gaps found** — list missing coverage as P2 or P3 findings

### E. Code Quality

- **Naming** — clear, consistent with existing codebase conventions
- **Complexity** — is the change more complex than necessary? Could it be simplified?
- **Duplication** — does it duplicate existing functionality?
- **Documentation** — user-facing behavior changes must be reflected in project documentation

### F. Platform Compatibility

- Does the change work on Linux, Windows, and macOS?
- Path separators, line endings, OS-specific APIs?
- Platform-aware path handling (not string concatenation)?
- Are platform-specific test assertions using the correct fields?

---

## Pentest Checklist (for builtin changes)

When the review includes a new or modified builtin, run through these attack vectors:

| Category | Test vectors |
|----------|-------------|
| **Integer edge cases** | `0`, `1`, `MaxInt64`, `MaxInt64+1`, `99999999999999999999`, `-1`, `-9999999999`, `''`, `'   '` |
| **Special files** | `/dev/zero`, `/dev/random`, `/dev/null`, `/proc` or `/sys` files, directories as file args |
| **Resource exhaustion** | Large count args on small files, many file args (FD leak), very large files, very long lines (>1MB) |
| **Path edge cases** | `../` traversal, `//double//slashes`, `/etc/././hosts`, non-existent file, empty filename, `-`-prefixed filenames, symlinks pointing outside sandbox |
| **Flag injection** | Unknown flags, flag values via expansion, `--` end-of-flags, multiple `-` (stdin) args |

---

## Finding Severity

Use the P0–P3 priority scale. Each finding MUST be prefixed with the corresponding shields.io badge image in all inline comments and the summary table.

### Priority definitions and badge images

| Priority | Badge markdown | Criteria |
|----------|---------------|----------|
| P0 | `<sub><sub>![P0 Badge](https://img.shields.io/badge/P0-red?style=flat)</sub></sub>` | Drop everything to fix. Exploitable vulnerability with high impact (RCE, sandbox bypass, data breach). Blocking merge. |
| P1 | `<sub><sub>![P1 Badge](https://img.shields.io/badge/P1-orange?style=flat)</sub></sub>` | Urgent. Likely exploitable or high-risk pattern — correctness bugs that produce wrong output vs bash, data races, panics. |
| P2 | `<sub><sub>![P2 Badge](https://img.shields.io/badge/P2-yellow?style=flat)</sub></sub>` | Normal. Potential vulnerability, unintentional bash divergence, missing test coverage, missing documentation updates. |
| P3 | `<sub><sub>![P3 Badge](https://img.shields.io/badge/P3-blue?style=flat)</sub></sub>` | Low / nice-to-have. Style inconsistency, minor simplification, hardening suggestion, nice-to-have edge case test. |

Every inline comment body MUST start with the badge image followed by the finding title. For example:

```
<sub><sub>![P1 Badge](https://img.shields.io/badge/P1-orange?style=flat)</sub></sub> **Command injection via unsanitized user input**
```

---

## Output Format

### Review Summary
- Brief overview of what was reviewed
- Overall assessment: **safe to merge**, **needs fixes**, or **needs major rework**
- Summary table of findings with badges:

```markdown
| # | Priority | File | Finding |
|---|----------|------|---------|
| 1 | <sub><sub>![P0 Badge](https://img.shields.io/badge/P0-red?style=flat)</sub></sub> | `path/to/file.go:42` | Brief description |
| 2 | <sub><sub>![P2 Badge](https://img.shields.io/badge/P2-yellow?style=flat)</sub></sub> | `path/to/other.go:15` | Brief description |
```

### Findings

Organize by severity (P0 first, then P1, P2, P3).

For each finding, include:
1. **Title** — clear, descriptive name (prefixed with priority badge)
2. **Severity** — P0–P3 (and category, e.g. Security, Bash Compat, Correctness)
3. **Location** — file path and line number(s)
4. **Description** — what the issue is and why it matters
5. **Evidence** — the specific code or a proof-of-concept shell script demonstrating the issue
6. **Remediation** — concrete, actionable fix

For security findings, also include:
- **Impact** — what an attacker (malicious script) could achieve
- **References** — relevant CWE IDs or OWASP references

### Positive Observations
- Note security measures and good patterns already in place — this helps the team understand what's working well

---

## PR Review Submission

When the argument is a PR number or URL, submit findings as a GitHub review with inline comments.

### 0. Signal review in progress

React to the PR body with an **eyes** emoji to indicate the review is underway:

```bash
gh api repos/{owner}/{repo}/issues/{pr_number}/reactions \
  --method POST \
  --field content=eyes \
  --jq '.id'
```

Store the returned reaction ID so it can be removed later.

### 1. Determine review event

Based on findings:
- **No P0/P1 findings** → `COMMENT`
- **Any P0 or P1 finding** → `REQUEST_CHANGES`
- **No findings at all** → `APPROVE`

### 2. Submit the review

```bash
gh api repos/{owner}/{repo}/pulls/{pr_number}/reviews \
  --method POST \
  --input - \
  --jq '{id: .id, state: .state, html_url: .html_url}' <<'EOF'
{
  "commit_id": "<head commit SHA>",
  "event": "<APPROVE|COMMENT|REQUEST_CHANGES>",
  "body": "<review summary>",
  "comments": [
    {
      "path": "relative/path/to/file.go",
      "line": 42,
      "side": "RIGHT",
      "body": "<sub><sub>![P1 Badge](https://img.shields.io/badge/P1-orange?style=flat)</sub></sub> **Title**\n\nExplanation.\n\n```suggestion\n// fix\n```"
    }
  ]
}
EOF
```

**Payload rules:**
- `commit_id`: exact HEAD SHA of the PR branch.
- `line`: must fall within a diff hunk for that file. For multi-line comments, use both `start_line` and `line`.
- `side`: use `"RIGHT"` for comments on the new code.
- Include GitHub suggestion blocks (```` ```suggestion ````) where a concrete fix is straightforward.
- If the API returns an error about an invalid line position, adjust the `line` to fall within the diff hunk and retry.

### 3. Post-review emoji reactions

After successfully submitting the review, update the PR body reactions based on the outcome:

- **If `APPROVE`**: React with a **thumbs up** (`+1`) emoji, then remove the eyes reaction.
  ```bash
  gh api repos/{owner}/{repo}/issues/{pr_number}/reactions \
    --method POST --field content='+1'
  gh api repos/{owner}/{repo}/issues/{pr_number}/reactions/{eyes_reaction_id} \
    --method DELETE
  ```

- **If `REQUEST_CHANGES`**: Remove **all** reactions added by the current authenticated user from the PR body.
  ```bash
  GITHUB_USER=$(gh api user --jq '.login')
  gh api repos/{owner}/{repo}/issues/{pr_number}/reactions --paginate \
    --jq --arg user "$GITHUB_USER" '.[] | select(.user.login == $user) | .id' | while read -r reaction_id; do
    gh api repos/{owner}/{repo}/issues/{pr_number}/reactions/$reaction_id --method DELETE
  done
  ```

- **If `COMMENT`** (default): Remove the eyes reaction.
  ```bash
  gh api repos/{owner}/{repo}/issues/{pr_number}/reactions/{eyes_reaction_id} \
    --method DELETE
  ```

---

## Operational Guidelines

- **Be thorough but precise** — every finding must be backed by evidence from the actual code. Do not fabricate or speculate.
- **No false positives** — if unsure, clearly state uncertainty and the conditions under which it would be exploitable.
- **Prioritize sandbox escapes** — any way to execute arbitrary commands or access arbitrary files is the highest priority.
- **Read before judging** — trace data flow before flagging issues.
- **Test your findings** — include proof-of-concept shell scripts when possible.
- **Do not make changes to the code** — your role is to identify and report, not to fix.
