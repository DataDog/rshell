---
name: review-fix-loop
description: "Self-review a PR, fix all issues, and re-review in a loop until clean. Coordinates code-review, address-pr-comments, and fix-ci-tests skills."
argument-hint: "[pr-number|pr-url]"
---

Self-review and iteratively fix **$ARGUMENTS** (or the current branch's PR if no argument is given) until the review is clean.

---

## Overview

This skill orchestrates a review-fix loop:

1. **Review** the PR (posts findings as GitHub PR comments)
2. **Fix** all reported issues and CI failures (in parallel)
3. **Re-review** the updated PR
4. **Repeat** until the review is clean or the iteration limit is reached

Maximum iterations: **10**

---

## Workflow

### 0. Identify the PR

```bash
# If argument provided, use it; otherwise detect from current branch
gh pr view $ARGUMENTS --json number,url,headRefName,baseRefName
```

If `$ARGUMENTS` is empty, this automatically falls back to the PR associated with the current branch. If no PR is found, stop and inform the user. Store the PR number and owner/repo for all subsequent steps.

```bash
gh repo view --json owner,name --jq '"\(.owner.login)/\(.name)"'
```

---

### 1. Start the review-fix loop

Set `iteration = 1`. Repeat the following steps while `iteration <= 10`:

---

#### Step A — Review the PR (in parallel)

Launch **both** of these in parallel:

1. **Self-review** — Run the **code-review** skill on the PR:
   ```
   /code-review <pr-number>
   ```
   This will analyze the full diff against main, post findings as a GitHub PR review with inline comments, and classify findings by severity (P0–P3).

2. **Request external reviews** — Post a comment to trigger @datadog and @codex reviews:
   ```bash
   gh pr comment <pr-number> --body "@datadog @codex make a comprehensive code and security review"
   ```

Wait for the **self-review** to complete before proceeding. The external reviews will arrive asynchronously and their comments will be picked up by **address-pr-comments** in Step B.

**Record the self-review outcome:**
- If the review result is **APPROVE** (no findings) → go to **Step D (CI check)**
- If there are findings → continue to **Step B**

---

#### Step B — Fix review findings and CI failures (in parallel)

Launch **both** of these in parallel:

1. **Address PR comments** — Run the **address-pr-comments** skill:
   ```
   /address-pr-comments <pr-number>
   ```
   This will read all unresolved review comments, evaluate validity, implement fixes, commit, push, and reply/resolve threads.

2. **Fix CI failures** — Run the **fix-ci-tests** skill:
   ```
   /fix-ci-tests <pr-number>
   ```
   This will check for failing CI jobs, download logs, reproduce failures locally, fix them, and push.

Wait for **both** to complete before proceeding.

---

#### Step C — Verify push succeeded

After both parallel tasks complete, confirm the branch is up to date:

```bash
git status
git log --oneline -3
```

If there were merge conflicts between the two parallel fix streams, resolve them and push.

---

#### Step D — Check CI status

Check if CI is passing:

```bash
gh pr checks <pr-number> --json name,state
```

- If any checks are **failing** → run **fix-ci-tests** one more time, then continue to re-review
- If all checks are **passing** or **pending** → continue to re-review

---

#### Step E — Decide whether to continue

Increment `iteration`.

- If the previous review was **APPROVE** and CI is passing → **stop, the PR is clean**
- If `iteration > 10` → **stop, iteration limit reached**
- Otherwise → **go back to Step A** for the next iteration

---

### 2. Final summary

After the loop ends, provide a summary:

```markdown
## Review-Fix Loop Summary

- **PR**: #<number> (<url>)
- **Iterations completed**: <N>
- **Final status**: <CLEAN | ITERATION_LIMIT_REACHED>

### Iteration log

| # | Review result | Findings | Fixes applied | CI status |
|---|--------------|----------|---------------|-----------|
| 1 | REQUEST_CHANGES | 3 (1×P1, 2×P2) | 3 fixed | Passing |
| 2 | COMMENT | 1 (1×P3) | 1 fixed | Passing |
| 3 | APPROVE | 0 | — | Passing |

### Remaining issues (if any)
- <list any unresolved findings or CI failures>
```

---

## Important rules

- **Never skip the review step** — always re-review after fixes to catch regressions or new issues introduced by the fixes themselves.
- **Always submit reviews to GitHub** — each iteration's review must be posted as PR comments so there's a visible trail.
- **Parallelise fix-ci-tests and address-pr-comments** — they work on independent concerns (CI failures vs review comments) and can run simultaneously.
- **Resolve merge conflicts** — if the parallel fix streams conflict, resolve before re-reviewing.
- **Stop early on APPROVE + CI green** — don't waste iterations if the PR is already clean.
- **Respect the iteration limit** — hard stop at 10 to prevent infinite loops. If issues persist after 10 iterations, report what's left for the user to handle.
