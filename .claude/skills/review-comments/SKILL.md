---
name: review-comments
description: Read PR review comments, evaluate validity, implement fixes, push changes, and reply/resolve threads
argument-hint: "[pr-number|pr-url]"
---

Address code review comments on **$ARGUMENTS** (or the current branch's PR if no argument is given).

---

## Workflow

### 1. Identify the PR

Determine the target PR:

```bash
# If argument provided, use it; otherwise detect from current branch
gh pr view $ARGUMENTS --json number,url,headRefName,baseRefName
```

If no PR is found, stop and inform the user.

Extract owner, repo, and PR number for subsequent API calls:

```bash
gh repo view --json owner,name --jq '"\(.owner.login)/\(.name)"'
```

### 2. Fetch all review comments

Retrieve all review comments (inline code comments) on the PR:

```bash
gh api repos/{owner}/{repo}/pulls/{pr-number}/comments \
  --paginate \
  --jq '.[] | {id: .id, node_id: .node_id, user: .user.login, path: .path, line: .line, original_line: .original_line, side: .side, body: .body, in_reply_to_id: .in_reply_to_id, created_at: .created_at}' \
  2>&1 | head -500
```

Also fetch top-level review comments (review bodies):

```bash
gh api repos/{owner}/{repo}/pulls/{pr-number}/reviews \
  --jq '.[] | select(.body != "" and .body != null) | {id: .id, user: .user.login, state: .state, body: .body}' \
  2>&1 | head -200
```

Filter out:
- Comments authored by the PR author (self-comments, unless they contain a TODO/action item)
- Already-resolved threads
- Bot comments that are purely informational

Check which threads are already resolved:

```bash
gh api graphql -f query='
  query($owner: String!, $repo: String!, $pr: Int!) {
    repository(owner: $owner, name: $repo) {
      pullRequest(number: $pr) {
        reviewThreads(first: 100) {
          nodes {
            id
            isResolved
            comments(first: 10) {
              nodes {
                databaseId
                body
                author { login }
              }
            }
          }
        }
      }
    }
  }
' -f owner="{owner}" -f repo="{repo}" -F pr={pr-number}
```

Only process **unresolved** threads with actionable comments.

### 3. Understand each comment

For each unresolved review comment:

1. **Read the file and surrounding context** at the line referenced by the comment
2. **Read the PR diff** to understand what changed:
   ```bash
   gh pr diff $ARGUMENTS -- <path>
   ```
3. **Classify the comment** into one of these categories:

| Category | Description | Action |
|----------|------------|--------|
| **Bug/correctness** | Reviewer identified a real bug or incorrect behavior | Fix the code |
| **Style/convention** | Naming, formatting, or project convention issue | Fix to match convention |
| **Suggestion/improvement** | A better approach or simplification | Evaluate and implement if it improves the code |
| **Question** | Reviewer asking for clarification | Reply with an explanation, no code change needed |
| **Nitpick** | Minor optional suggestion | Evaluate — fix if trivial, otherwise reply explaining the tradeoff |
| **Invalid/outdated** | Comment doesn't apply or is based on a misunderstanding | Reply politely explaining why |

### 4. Evaluate validity — bash behavior is the source of truth

**The shell must match bash behavior unless it intentionally diverges** (e.g., sandbox restrictions, blocked commands, readonly enforcement). This principle overrides reviewer suggestions.

For each comment, determine if it is **valid and actionable**:

1. **Verify against bash** — always check what bash actually does:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<relevant script>'
   ```
2. **Read the relevant code** in full — not just the diff, but the surrounding implementation
3. **Check project conventions** in `CLAUDE.md` and `AGENTS.md`
4. **Consider side effects** — will the change break other tests or behaviors?
5. **Check for duplicates** — is the same issue raised in multiple comments? Group them

Decision matrix:

| Reviewer says | Bash does | Shell intentionally diverges? | Action |
|--------------|-----------|-------------------------------|--------|
| "This is wrong" | Reviewer is right | No | **Fix the implementation** to match bash |
| "This is wrong" | Current code matches bash | No | **Reply** explaining it matches bash, with proof |
| "This is wrong" | N/A | Yes (sandbox/security) | **Reply** explaining the intentional divergence |
| "Do it differently" | Suggestion matches bash better | No | **Fix the implementation** to match bash |
| "Do it differently" | Current code already matches bash | No | **Reply** — bash compatibility takes priority |

If a comment is **not valid**:
- Prepare a polite reply with proof (e.g., "This matches bash behavior — verified with `docker run --rm debian:bookworm-slim bash -c '...'`")
- If the divergence is intentional, explain why (sandbox restriction, security, etc.)

If a comment is **valid** (i.e., fixing it brings the shell closer to bash, or addresses a real bug):
- Proceed to step 5

### 5. Implement fixes

For each valid comment, apply the fix. **Always prefer fixing the shell implementation over adjusting tests or expectations**, unless the shell intentionally diverges from bash.

1. **Read the file** being modified
2. **Determine what bash does** if not already verified:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<relevant script>'
   ```
3. **Fix the implementation** to match bash behavior — do NOT adjust test expectations to match broken implementation
4. **Check for related issues** — if the comment reveals a pattern, fix all occurrences (not just the one the reviewer flagged)
5. **Run relevant tests** to verify:
   ```bash
   # Run tests for the affected package
   go test -race -v ./interp/... ./tests/... -run "<relevant test>" -timeout 60s

   # If YAML scenarios were touched, run bash comparison
   RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
   ```
6. If tests fail, iterate on the **implementation fix** (not the test) until they pass
7. Only set `skip_assert_against_bash: true` when the behavior intentionally diverges from bash (sandbox restrictions, blocked commands, readonly enforcement)

Group related comment fixes into a single logical commit when possible.

### 6. Commit and push

After all fixes are verified:

```bash
# Stage the changed files explicitly
git add <file1> <file2> ...

# Commit with a descriptive message
git commit -m "$(cat <<'EOF'
Address review comments: <brief description>

<details of what was changed and why>

Co-Authored-By: Claude Opus 4.6 <noreply@anthropic.com>
EOF
)"

# Push to the PR branch
git push
```

If fixes span unrelated areas, prefer multiple focused commits over one large commit.

### 7. Reply to and resolve comments

For each comment that was addressed:

1. **Reply** explaining what was fixed:
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr-number}/comments/{comment-id}/replies \
     -f body="Done — <brief explanation of the change made>"
   ```

2. **Resolve** the thread:
   ```bash
   # Get the GraphQL thread ID
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

   # Resolve the thread
   gh api graphql -f query='
     mutation($threadId: ID!) {
       resolveReviewThread(input: {threadId: $threadId}) {
         thread { isResolved }
       }
     }
   ' -f threadId="<thread-id>"
   ```

For comments that were **not valid** or were **questions**, reply with an explanation but do NOT resolve — let the reviewer decide.

### 8. Summary

Provide a final summary:

- List each review comment that was addressed with:
  - The comment (abbreviated)
  - The classification (bug, style, suggestion, etc.)
  - What was changed
- List any comments that were replied to but not fixed (with reason)
- List any comments that could not be addressed (with explanation)
- Confirm the commit(s) pushed and threads resolved
