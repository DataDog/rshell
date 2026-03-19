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

Your very first action — before reading ANY files, before running ANY commands — is to call TaskCreate exactly 11 times, once for each step/sub-step below. Use these exact subjects:

1. "Step 1: Identify the PR"
2. "Step 2: Run the review-fix loop" ← **Update subject with iteration number each loop** (e.g. "Step 2: Run the review-fix loop (iteration 1)")
3. "Step 2A1: Self-review (code-review)" ← **parallel with 2A2**
4. "Step 2A2: Request external reviews (@codex)" ← **parallel with 2A1**
5. "Step 2B: Address PR comments (address-pr-comments)"
6. "Step 2C: Fix CI failures (fix-ci-tests)"
7. "Step 2D: Verify push and resolve conflicts"
8. "Step 2E: Check CI status"
9. "Step 2F: Decide whether to continue"
10. "Step 3: Verify clean state"
11. "Step 4: Final summary"

**Note on sub-steps 2A–2F:** These are created once and reused across loop iterations. At the start of each iteration, reset all sub-steps to `pending`, then execute them in order. Sub-steps marked **parallel** are launched concurrently and must both complete before proceeding to the next group.

### 2. Execution order and gating

Steps run strictly in this order:

```
Step 1 → Step 2 (loop: [2A1 ∥ 2A2] → 2B → 2C → 2D → 2E → 2F) → Step 3 → Step 4
                    ↑                                          ↓
                    └──────────────── repeat ───────────────────┘
```

**Top-level steps** are sequential: before starting step N, call TaskList and verify step N-1 is `completed`. Set step N to `in_progress`.

**Sub-steps within Step 2** follow this execution order:

| Phase | Sub-steps | Execution |
|-------|-----------|-----------|
| Review | **2A1** ∥ **2A2** | **Parallel** — launch both, wait for both |
| Fix comments | **2B** | Sequential |
| Fix CI | **2C** | Sequential — run after 2B completes |
| Verify | **2D** | Sequential |
| CI check | **2E** | Sequential |
| Decide | **2F** | Sequential |

### 3. Never skip steps

- Do NOT skip the review (Step 2A1) because you think the code is fine
- Do NOT skip verification (Step 3) because tests passed during fixes
- Do NOT skip the external review trigger — @codex reviews catch issues the self-review misses
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

Set `iteration = 1`. Maximum iterations: **30**. Repeat sub-steps A through E while `iteration <= 30`.

**At the start of each iteration**, update the Step 2 task subject to include the current iteration number using TaskUpdate, e.g. `"Step 2: Run the review-fix loop (iteration 3)"`.

---

### Sub-step 2A1 — Self-review ← **parallel with 2A2**

Run the **code-review** skill on the PR:
```
/code-review <pr-number>
```
This analyzes the full diff against main, posts findings as a GitHub PR review with inline comments, and classifies findings by severity (P0–P3).

### Sub-step 2A2 — Request external reviews ← **parallel with 2A1**

Post a comment to trigger @codex reviews:
```bash
gh pr comment <pr-number> --body "@codex review this PR"
```
The external reviews arrive asynchronously — their comments will be picked up by **address-pr-comments** in Sub-step 2B1.

### After 2A1 ∥ 2A2 complete

Wait for **both** to complete before proceeding.

**Post the self-review outcome (from 2A1) as a GitHub PR comment** so it is always visible on the PR:
```bash
gh pr comment <pr-number> --body "<iteration N self-review result: APPROVE/COMMENT/REQUEST_CHANGES, number of findings by severity, and a brief summary>"
```

**Record the self-review outcome:**
- If the review result is **APPROVE** (no findings) → skip to **Sub-step 2E (CI check)**
- If there are findings → continue to **Sub-step 2B**

---

### Pre-check before 2B

Before launching fixes, ensure the working tree is clean and up to date:

```bash
git status
git pull --rebase origin <head-branch>
```

### Sub-step 2B — Address PR comments

Run the **address-pr-comments** skill:
```
/address-pr-comments <pr-number>
```
This reads all unresolved review comments, evaluates validity, implements fixes, commits, pushes, and replies/resolves threads.

**Commit message prefix:** All commits created in this sub-step MUST be prefixed with the current loop iteration number, e.g. `[iter 3] Fix null check in parser`.

Wait for completion before proceeding to 2C.

### Sub-step 2C — Fix CI failures

Run the **fix-ci-tests** skill:
```
/fix-ci-tests <pr-number>
```
This checks for failing CI jobs, downloads logs, reproduces failures locally, fixes them, and pushes.

**Commit message prefix:** All commits created in this sub-step MUST be prefixed with the current loop iteration number, e.g. `[iter 3] Fix flaky test timeout`.

Wait for completion before proceeding to 2D.

---

### Sub-step 2D — Verify push and sync

After 2B and 2C complete, verify the branch state:

```bash
git fetch origin <head-branch>
git status
git log --oneline -5
```

1. If there are unpushed commits, push them.
2. Pull the latest remote state to stay in sync:
   ```bash
   git pull --rebase origin <head-branch>
   ```
3. Confirm the branch is up to date with the remote.

**Completion check:** `git status` shows a clean working tree and the branch is pushed. Only then proceed.

---

### Sub-step 2E — Check CI status

```bash
gh pr checks <pr-number> --json name,state
```

- If any checks are **failing** → run the **fix-ci-tests** skill one more time:
  ```
  /fix-ci-tests <pr-number>
  ```
  Wait for it to complete, then re-check CI status. If still failing after this second attempt, log the failure and continue to Sub-step 2F.

- If all checks are **passing** or **pending** → continue to Sub-step 2F.

---

### Sub-step 2F — Decide whether to continue

Increment `iteration`.

Check **all three** review sources for remaining issues:

1. **Self-review** — Was the latest `/code-review` result **APPROVE** (no findings)?

2. **External reviews** — Are there unresolved PR comment threads from @codex or @chatgpt-codex-connector[bot]?
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
| Any findings | Any | Any | **Continue** → go back to Sub-step 2A1 ∥ 2A2 |
| APPROVE | Unresolved threads | Any | **Continue** → go back to Sub-step 2A1 ∥ 2A2 (address-pr-comments will handle them) |
| APPROVE | None unresolved | Failing | **Continue** → go back to Sub-step 2A1 ∥ 2A2 (fix-ci-tests will handle it) |
| — | — | — | If `iteration > 30` → **STOP — iteration limit reached** |

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

4. **Confirm Codex has replied to the LATEST review request (with polling):**

   The review request comment posted in Step 2A2 triggers Codex asynchronously. The bot may respond as either `codex` or `chatgpt-codex-connector[bot]` (the GitHub App). It can take **15+ minutes** to respond. You must verify that the bot has actually responded to **the most recent** request, not a previous iteration's request. Replies from earlier iterations do NOT count.

   **How to check:**
   - Find the timestamp of the **last** Codex review request comment (the one posted in Step 2A2 of the final iteration). You can identify it by looking for comments authored by the current user containing "@codex" in the body:
     ```bash
     gh api repos/{owner}/{repo}/issues/{pr-number}/comments --paginate --jq '
       [.[] | select(.body | test("@codex")) | select(.user.login != "codex") | select(.user.login != "chatgpt-codex-connector[bot]")] | last | .created_at'
     ```
   - Then check whether the codex bot has posted a review **after** that timestamp. Check both possible bot logins (`codex` and `chatgpt-codex-connector[bot]`):
     ```bash
     gh api repos/{owner}/{repo}/pulls/{pr-number}/reviews --paginate --jq '
       [.[] | select(.user.login == "codex" or .user.login == "chatgpt-codex-connector[bot]")] | last | {submitted_at, state, user: .user.login}'
     ```
   - Also check issue comments (the bot may reply as a comment instead of a review):
     ```bash
     gh api repos/{owner}/{repo}/issues/{pr-number}/comments --paginate --jq '
       [.[] | select(.user.login == "codex" or .user.login == "chatgpt-codex-connector[bot]")] | last | {created_at, user: .user.login}'
     ```
   - Compare timestamps. If the bot's latest review `submitted_at` (or comment `created_at`) is **after** the latest request's `created_at`, the bot has replied — **verification passes**. Use whichever response (review or comment) has the most recent timestamp.

   **Polling wait if the bot hasn't replied yet:**

   Do NOT immediately fail. Instead, poll and wait:
   - **Poll interval:** 1 minute (use `sleep 60` between checks)
   - **Maximum wait:** 10 minutes (up to 10 poll attempts)
   - On each poll iteration, re-run the `gh api` commands above and compare timestamps
   - Log each poll attempt: `"Waiting for Codex reply... (attempt N/10, elapsed Xm)"`

   **Only fail this verification** if the bot has still not replied after the full 10-minute wait. Then go back to **Step 2: Run the review-fix loop**.

   **If the bot has no reviews or comments at all** after the 10-minute wait, the verification also fails.

Record the final state of each dimension (self-review, external reviews, CI, Codex response).

Track how many times Step 3 has **succeeded** (all four verifications passed) across the entire run.

**If any verification fails** (CI failing, unresolved threads remain, unpushed commits that can't be pushed, or Codex hasn't responded to the latest review request), reset the success counter to 0, reset Step 2 and all its sub-steps to `pending`, and go back to **Step 2: Run the review-fix loop** for another iteration.

**If all verifications pass**, increment the success counter. If this is the **5th consecutive success** of Step 3 → proceed to **Step 4**. Otherwise → reset Step 2 and all its sub-steps to `pending`, and go back to **Step 2: Run the review-fix loop** for another iteration to re-confirm stability.

**Completion check:** Step 3 has succeeded 5 consecutive times. Mark Step 3 as `completed`.

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

**Post the summary as a GitHub PR comment** so it is visible on the PR itself:
```bash
gh pr comment <pr-number> --body "<the summary markdown above>"
```

**Completion check:** Summary is output to the user AND posted as a PR comment. Mark Step 4 as `completed`.

---

## Important rules

- **Never skip the review step** — always re-review after fixes to catch regressions or new issues introduced by the fixes themselves.
- **Always submit reviews to GitHub** — each iteration's review must be posted as PR comments so there's a visible trail.
- **Run address-pr-comments before fix-ci-tests** — 2B then 2C, sequentially, so CI fixes run on code that already incorporates review feedback.
- **Pull before fixing** — always `git pull --rebase` before launching fix agents to avoid working on stale code.
- **Stop early on APPROVE + CI green + no unresolved threads** — don't waste iterations if the PR is already clean.
- **Respect the iteration limit** — hard stop at 30 to prevent infinite loops. If issues persist after 30 iterations, report what's left for the user to handle.
- **Use gate checks** — always call TaskList and verify prerequisites before starting a step. This prevents out-of-order execution.
