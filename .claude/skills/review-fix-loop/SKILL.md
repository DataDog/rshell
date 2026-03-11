---
name: review-fix-loop
description: "Self-review a PR, fix all issues, and re-review in a loop until clean. Coordinates code-review, address-pr-comments, and fix-ci-tests skills."
argument-hint: "[pr-number|pr-url]"
---

Self-review and iteratively fix **$ARGUMENTS** (or the current branch's PR if no argument is given) until the review is clean.

---

## ⛔ STOP — READ THIS BEFORE DOING ANYTHING ELSE ⛔

You MUST follow this execution protocol. Skipping steps or running them out of order has caused regressions and wasted iterations in every prior run of this skill.

### 1. Create the full task list FIRST

Your very first action — before reading ANY files, before running ANY commands — is to call TaskCreate exactly 9 times, once for each step/sub-step below. Use these exact subjects:

1. "Step 1: Identify the PR"
2. "Step 2: Run the review-fix loop"
3. "Step 2A: Review the PR (self-review ∥ external reviews)" ← **parallel Step 2A and Step 2B**
4. "Step 2B: Fix review findings and CI failures (address-pr-comments ∥ fix-ci-tests)" ← **parallel Step 2A and Step 2B**
5. "Step 2C: Verify push and resolve conflicts"
6. "Step 2D: Check CI status"
7. "Step 2E: Decide whether to continue"
8. "Step 3: Verify clean state"
9. "Step 4: Final summary"

**Note on sub-steps 2A–2E:** These are created once and reused across loop iterations. At the start of each iteration, reset 2A–2E to `pending`, then execute them in order. Sub-steps marked **parallel** launch multiple agents concurrently within that sub-step.

### 2. Execution order and gating

Steps run strictly in this order:

```
Step 1 → Step 2 (loop: 2A → 2B → 2C → 2D → 2E) → Step 3 → Step 4
                    ↑                          ↓
                    └──────── repeat ───────────┘
```

**Top-level steps** are sequential: before starting step N, call TaskList and verify step N-1 is `completed`. Set step N to `in_progress`.

**Sub-steps within Step 2** are also sequential (2A → 2B → 2C → 2D → 2E), but **within** certain sub-steps, multiple agents run in parallel:

| Sub-step | Internal parallelism |
|----------|---------------------|
| **2A** | Self-review agent **∥** external review comment — run in parallel |
| **2B** | address-pr-comments agent **∥** fix-ci-tests agent — run in parallel |
| **2C** | Sequential (verify & resolve conflicts) |
| **2D** | Sequential (check CI, optionally fix) |
| **2E** | Sequential (evaluate & decide) |

### 3. Never skip steps

- Do NOT skip the review (Step 2A) because you think the code is fine
- Do NOT skip verification (Step 3) because tests passed during fixes
- Do NOT skip the external review trigger — @datadog and @codex reviews catch issues the self-review misses
- Do NOT mark a step completed until every sub-bullet in that step is satisfied

If you catch yourself wanting to skip a step, STOP and do the step anyway.

---

## Step 1: Identify the PR

**Set this step to `in_progress` immediately after creating all tasks.**

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

**Completion check:** You have the PR number, URL, owner, repo, head branch, and base branch. Mark Step 1 as `completed`.

---

## Step 2: Run the review-fix loop

**GATE CHECK**: Call TaskList. Step 1 must be `completed`. Set Step 2 to `in_progress`.

Set `iteration = 1`. Maximum iterations: **10**. Repeat sub-steps A through E while `iteration <= 10`:

---

### Sub-step A — Review the PR (parallel launch)

Launch **both** of these in parallel using the Agent tool:

1. **Self-review** — Run the **code-review** skill on the PR:
   ```
   /code-review <pr-number>
   ```
   This analyzes the full diff against main, posts findings as a GitHub PR review with inline comments, and classifies findings by severity (P0–P3).

2. **Request external reviews** — Post a comment to trigger @datadog and @codex reviews:
   ```bash
   gh pr comment <pr-number> --body "@datadog @codex make a comprehensive code and security reviews"
   ```

Wait for the **self-review agent** to complete before proceeding. The external reviews arrive asynchronously — their comments will be picked up by **address-pr-comments** in Sub-step B.

**Record the self-review outcome:**
- If the review result is **APPROVE** (no findings) → skip to **Sub-step D (CI check)**
- If there are findings → continue to **Sub-step B**

---

### Sub-step B — Fix review findings and CI failures (parallel)

**Pre-check:** Before launching fixes, ensure the working tree is clean and up to date:

```bash
git status
git pull --rebase origin <head-branch>
```

Launch **both** of these in parallel using the Agent tool:

1. **Address PR comments** — Run the **address-pr-comments** skill:
   ```
   /address-pr-comments <pr-number>
   ```
   This reads all unresolved review comments, evaluates validity, implements fixes, commits, pushes, and replies/resolves threads.

2. **Fix CI failures** — Run the **fix-ci-tests** skill:
   ```
   /fix-ci-tests <pr-number>
   ```
   This checks for failing CI jobs, downloads logs, reproduces failures locally, fixes them, and pushes.

Wait for **both agents** to complete before proceeding.

---

### Sub-step C — Verify push and resolve conflicts

After both parallel tasks complete, verify the branch state:

```bash
git fetch origin <head-branch>
git status
git log --oneline -5
```

**Conflict resolution:** If the two parallel fix streams produced divergent commits (e.g., both modified the same file), resolve the conflict:

1. Pull the latest remote state:
   ```bash
   git pull --rebase origin <head-branch>
   ```
2. If rebase conflicts occur, resolve them manually, then:
   ```bash
   git rebase --continue
   git push --force-with-lease
   ```
3. If no conflicts, confirm the branch is up to date with the remote.

**Completion check:** `git status` shows a clean working tree and the branch is pushed. Only then proceed.

---

### Sub-step D — Check CI status

```bash
gh pr checks <pr-number> --json name,state
```

- If any checks are **failing** → run the **fix-ci-tests** skill one more time:
  ```
  /fix-ci-tests <pr-number>
  ```
  Wait for it to complete, then re-check CI status. If still failing after this second attempt, log the failure and continue to Sub-step E.

- If all checks are **passing** or **pending** → continue to Sub-step E.

---

### Sub-step E — Decide whether to continue

Increment `iteration`.

Check **all three** review sources for remaining issues:

1. **Self-review** — Was the latest `/code-review` result **APPROVE** (no findings)?

2. **External reviews** — Are there unresolved PR comment threads from @datadog or @codex?
   ```bash
   gh api graphql -f query='
     query($owner: String!, $repo: String!, $pr: Int!) {
       repository(owner: $owner, name: $repo) {
         pullRequest(number: $pr) {
           reviewThreads(first: 100) {
             nodes {
               isResolved
               comments(first: 1) {
                 nodes { author { login } body }
               }
             }
           }
         }
       }
     }
   ' -f owner="{owner}" -f repo="{repo}" -F pr={pr-number} \
     --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false)'
   ```

3. **CI** — Are all checks passing?
   ```bash
   gh pr checks <pr-number> --json name,state
   ```

**Decision matrix:**

| Self-review | External comments | CI | Action |
|------------|-------------------|-----|--------|
| APPROVE | None unresolved | Passing | **STOP — PR is clean** |
| Any findings | Any | Any | **Continue** → go back to Sub-step A |
| APPROVE | Unresolved threads | Any | **Continue** → go back to Sub-step A (address-pr-comments will handle them) |
| APPROVE | None unresolved | Failing | **Continue** → go back to Sub-step A (fix-ci-tests will handle it) |
| — | — | — | If `iteration > 10` → **STOP — iteration limit reached** |

Log the iteration result before continuing or stopping:
- Iteration number
- Self-review result (APPROVE / COMMENT / REQUEST_CHANGES)
- Number of findings by severity
- Number of fixes applied
- CI status

---

**Step 2 completion check:** The loop exited because either (a) all three conditions are met (clean), or (b) the iteration limit was reached. Mark Step 2 as `completed`.

---

## Step 3: Verify clean state

**GATE CHECK**: Call TaskList. Step 2 must be `completed`. Set Step 3 to `in_progress`.

Run a final verification regardless of how the loop exited:

1. **Confirm branch is pushed:**
   ```bash
   git status
   git log --oneline origin/<head-branch>..HEAD
   ```
   If there are unpushed commits, push them.

2. **Confirm CI status:**
   ```bash
   gh pr checks <pr-number> --json name,state
   ```

3. **Confirm no unresolved threads:**
   ```bash
   gh api graphql -f query='
     query($owner: String!, $repo: String!, $pr: Int!) {
       repository(owner: $owner, name: $repo) {
         pullRequest(number: $pr) {
           reviewThreads(first: 100) {
             nodes {
               isResolved
               comments(first: 1) {
                 nodes { author { login } body }
               }
             }
           }
         }
       }
     }
   ' -f owner="{owner}" -f repo="{repo}" -F pr={pr-number} \
     --jq '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false) | .comments.nodes[0].body' \
     2>&1 | head -50
   ```

Record the final state of each dimension (self-review, external reviews, CI).

**Completion check:** All three verifications ran. Mark Step 3 as `completed`.

---

## Step 4: Final summary

**GATE CHECK**: Call TaskList. Step 3 must be `completed`. Set Step 4 to `in_progress`.

Provide a summary in this exact format:

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

### Final state

- **Self-review**: APPROVE / REQUEST_CHANGES / COMMENT
- **Unresolved external comments**: <count> (list authors)
- **CI**: Passing / Failing (list failing checks)

### Remaining issues (if any)

- <list any unresolved findings, external comments, or CI failures>
```

**Completion check:** Summary is output. Mark Step 4 as `completed`.

---

## Important rules

- **Never skip the review step** — always re-review after fixes to catch regressions or new issues introduced by the fixes themselves.
- **Always submit reviews to GitHub** — each iteration's review must be posted as PR comments so there's a visible trail.
- **Parallelise fix-ci-tests and address-pr-comments** — they work on independent concerns (CI failures vs review comments) and can run simultaneously.
- **Pull before fixing** — always `git pull --rebase` before launching parallel fix agents to avoid working on stale code.
- **Resolve merge conflicts** — if the parallel fix streams conflict, resolve before re-reviewing.
- **Stop early on APPROVE + CI green + no unresolved threads** — don't waste iterations if the PR is already clean.
- **Respect the iteration limit** — hard stop at 10 to prevent infinite loops. If issues persist after 10 iterations, report what's left for the user to handle.
- **Use gate checks** — always call TaskList and verify prerequisites before starting a step. This prevents out-of-order execution.
