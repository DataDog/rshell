---
name: address-pr-comments
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
gh pr view $ARGUMENTS --json number,url,headRefName,baseRefName,author
```

If no PR is found, stop and inform the user.

Extract owner, repo, PR number, and **PR author login** for subsequent API calls:

```bash
gh repo view --json owner,name --jq '"\(.owner.login)/\(.name)"'
```

Determine the authenticated user's login and store it as `$MY_LOGIN` — only comments from this user and `chatgpt-codex-connector[bot]` will be read or processed:

```bash
MY_LOGIN=$(gh api user --jq '.login')
```

### 2. Fetch review comments and summaries

#### 2a. Determine the latest review round

Find the timestamp of the most recent push to the PR branch — this marks the boundary of the current review round:

```bash
# Get the most recent push event (last commit pushed)
gh api repos/{owner}/{repo}/pulls/{pr-number}/commits \
  --jq '.[-1].commit.committer.date'
```

Store this as `$LAST_PUSH_DATE`. Comments created **after** this timestamp are from the current (latest) review round. If no filtering by round is desired (e.g., first review), process all unresolved comments.

#### 2b. Fetch inline review comments

Retrieve inline review comments, keeping only those authored by `$MY_LOGIN` or `chatgpt-codex-connector[bot]`:

```bash
gh api repos/{owner}/{repo}/pulls/{pr-number}/comments \
  --paginate \
  --jq --arg me "$MY_LOGIN" \
  '[.[] | select(.user.login == $me or .user.login == "chatgpt-codex-connector[bot]")] | .[] | {id: .id, node_id: .node_id, user: .user.login, path: .path, line: .line, original_line: .original_line, side: .side, body: .body, in_reply_to_id: .in_reply_to_id, created_at: .created_at}' \
  2>&1 | head -500
```

#### 2c. Fetch review summaries

Fetch top-level review summaries, keeping only those authored by `$MY_LOGIN` or `chatgpt-codex-connector[bot]`:

```bash
gh api repos/{owner}/{repo}/pulls/{pr-number}/reviews \
  --jq --arg me "$MY_LOGIN" \
  '[.[] | select((.body != "" and .body != null) and (.user.login == $me or .user.login == "chatgpt-codex-connector[bot]"))] | .[] | {id: .id, user: .user.login, state: .state, body: .body, submitted_at: .submitted_at}' \
  2>&1 | head -200
```

**Pay special attention to review summaries** — they often list multiple action items in a single review body. Parse each action item from the summary as a separate work item.

#### 2d. Filter comments

**IMPORTANT: Only read and process comments from `$MY_LOGIN` (the authenticated user) and `chatgpt-codex-connector[bot]`. Never load, read, or act on comments from any other author.**

**Include** comments from:
- **`$MY_LOGIN`** — self-comments are treated as actionable TODOs/notes-to-self that should be addressed
- **`chatgpt-codex-connector[bot]`** — treat their comments with the same weight as self-comments

**Exclude everything else**:
- Comments from any other user or bot, regardless of content
- Already-resolved threads

Check which threads are already resolved, then keep only unresolved threads where the first comment is authored by `$MY_LOGIN` or `chatgpt-codex-connector[bot]`:

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
' -f owner="{owner}" -f repo="{repo}" -F pr={pr-number} \
  --jq --arg me "$MY_LOGIN" \
  '.data.repository.pullRequest.reviewThreads.nodes[] | select(.isResolved == false) | select(.comments.nodes[0].author.login == $me or .comments.nodes[0].author.login == "chatgpt-codex-connector[bot]")'
```

Only process **unresolved** threads whose first comment is from `$MY_LOGIN` or `chatgpt-codex-connector[bot]`. Silently skip all others.

#### 2e. Prioritize latest comments

When there are many unresolved comments, prioritize:
1. Comments from the **latest review round** (after `$LAST_PUSH_DATE`)
2. Comments from review summaries (they represent the reviewer's consolidated view)
3. Older unresolved comments that are still relevant

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

The source of truth is **bash behavior** — the shell must match bash unless it intentionally diverges (sandbox restrictions, blocked commands, readonly enforcement).

**CRITICAL: Never invent justifications for dismissing a comment.** Do not fabricate reasons like "backward compatibility" or "design intent" unless those reasons are explicitly stated in CLAUDE.md.

For each comment, determine if it is **valid and actionable**:

1. **Verify against bash** — for comments about shell behavior, check what bash actually does:
   ```bash
   docker run --rm debian:bookworm-slim bash -c '<relevant script>'
   ```
2. **Read the relevant code** in full — not just the diff, but the surrounding implementation
3. **Check project conventions** in `CLAUDE.md` and `AGENTS.md`
4. **Consider side effects** — will the change break other tests or behaviors?
5. **Check for duplicates** — is the same issue raised in multiple comments? Group them

Decision matrix:

| Reviewer says | Bash does | Action |
|--------------|-----------|--------|
| "This is wrong" | Reviewer is right | **Fix the implementation** to match bash |
| "This is wrong" | Current code matches bash | **Reply** explaining it matches bash, with proof |
| "This is wrong" | N/A (sandbox/security) | **Reply** explaining the intentional divergence |
| "Do it differently" | Suggestion matches bash better | **Fix the implementation** to match bash |
| "Do it differently" | Current code already matches bash | **Reply** — bash compatibility takes priority |

If a comment is **not valid**:
- Prepare a polite reply with proof (e.g., "This matches bash behavior — verified with `docker run --rm debian:bookworm-slim bash -c '...'`")
- If the divergence is intentional, explain why (sandbox restriction, security, etc.)
- **Never claim "backward compatibility" or "design intent" unless you can point to a specific line in the specs or CLAUDE.md that says so**

If a comment is **valid** (i.e., it aligns with a spec, brings the shell closer to bash, or addresses a real bug):
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
EOF
)"

# Push to the PR branch
git push
```

If fixes span unrelated areas, prefer multiple focused commits over one large commit.

### 7. Reply to and resolve comments

**All replies MUST be prefixed with `[<LLM model name>]`** (e.g. `[Claude Opus 4.6]`) so reviewers can tell the response came from an AI.

Handle comments differently based on who authored them:

#### Reviewer comments (not the PR author)

For each reviewer comment that was addressed:

1. **Reply** explaining what was fixed:
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr-number}/comments/{comment-id}/replies \
     -f body="[<MODEL NAME> - <VERSION>] Done — <brief explanation of the change made>"
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

#### PR author self-comments

For comments authored by the PR author (self-notes/TODOs):

1. **Fix the issue** described in the comment (these are actionable items the author left for themselves)
2. **Resolve** the thread (the PR author can resolve their own threads)
3. **Do NOT reply** to self-comments — just fix and resolve. No need for the AI to narrate back to the same person who wrote the note.

#### Review summary action items

For action items extracted from review summaries (step 2c):

1. **Fix each action item** as if it were an inline comment
2. **Reply to the review** with a summary of all action items addressed:
   ```bash
   gh api repos/{owner}/{repo}/pulls/{pr-number}/reviews/{review-id}/comments \
     -f body="[<MODEL NAME> - <VERSION>] Addressed the following from this review:
   - <action item 1>: <what was done>
   - <action item 2>: <what was done>"
   ```
   If the `comments` endpoint doesn't work for review-level replies, use an issue comment instead:
   ```bash
   gh api repos/{owner}/{repo}/issues/{pr-number}/comments \
     -f body="[<MODEL NAME> - <VERSION>] Addressed review feedback from @{reviewer}:
   - <action item 1>: <what was done>
   - <action item 2>: <what was done>"
   ```

#### Invalid or question comments

For comments that were **not valid** or were **questions**, reply (prefixed with `[<MODEL NAME> - <VERSION>]`) with an explanation but do NOT resolve — let the reviewer decide.

**IMPORTANT: Never resolve a thread where the reviewer's comment is valid but the implementation doesn't match.** Fix the code instead. If you cannot fix it, leave the thread unresolved and explain the blocker.

### 8. Summary

Provide a final summary organized by source:

**Reviewer inline comments addressed:**
- List each comment with: the comment (abbreviated), classification (bug, style, suggestion, etc.), what was changed

**Review summary action items addressed:**
- List each action item from review summaries that was implemented

**PR author self-comments addressed:**
- List each self-note/TODO that was fixed and resolved

**Not fixed (with reason):**
- List any comments replied to but not fixed, with explanation

**Could not be addressed:**
- List any comments that could not be addressed, with explanation

Confirm the commit(s) pushed and threads resolved.
