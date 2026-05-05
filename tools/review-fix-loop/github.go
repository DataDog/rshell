// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// PRInfo holds the resolved PR metadata.
type PRInfo struct {
	Number int
	URL    string
	Head   string
	Base   string
	Owner  string
	Repo   string
}

func identifyPR(workDir, prRef string) (PRInfo, error) {
	args := []string{"pr", "view", "--json", "number,url,headRefName,baseRefName"}
	if prRef != "" {
		args = append(args, prRef)
	}
	cmd := exec.Command("gh", args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh pr view: %s: %w", strings.TrimSpace(string(out)), err)
	}
	var pr struct {
		Number      int    `json:"number"`
		URL         string `json:"url"`
		HeadRefName string `json:"headRefName"`
		BaseRefName string `json:"baseRefName"`
	}
	if err := json.Unmarshal(out, &pr); err != nil {
		return PRInfo{}, fmt.Errorf("parse PR JSON: %w", err)
	}

	repoCmd := exec.Command("gh", "repo", "view", "--json", "owner,name")
	repoCmd.Dir = workDir
	repoOut, err := repoCmd.Output()
	if err != nil {
		return PRInfo{}, fmt.Errorf("gh repo view: %w", err)
	}
	var repo struct {
		Owner struct{ Login string } `json:"owner"`
		Name  string                 `json:"name"`
	}
	if err := json.Unmarshal(repoOut, &repo); err != nil {
		return PRInfo{}, fmt.Errorf("parse repo JSON: %w", err)
	}

	return PRInfo{
		Number: pr.Number,
		URL:    pr.URL,
		Head:   pr.HeadRefName,
		Base:   pr.BaseRefName,
		Owner:  repo.Owner.Login,
		Repo:   repo.Name,
	}, nil
}

// countUnresolvedThreads returns the number of unresolved review threads whose
// first comment was posted by $MY_LOGIN or chatgpt-codex-connector[bot] (or the
// bare "chatgpt-codex-connector" login that GitHub's GraphQL API returns).
// Only the thread count is used for loop control — comment bodies are never read.
func countUnresolvedThreads(workDir string, pr PRInfo) (int, error) {
	myLogin, err := getMyLogin(workDir)
	if err != nil {
		return 0, err
	}

	const query = `
query($owner: String!, $repo: String!, $pr: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          isResolved
          comments(first: 1) {
            nodes { author { login } }
          }
        }
      }
    }
  }
}`

	total := 0
	cursor := ""
	for {
		// Omit the "after" argument on the first request (empty string is not a
		// valid GraphQL cursor; GitHub returns an error for after:"").
		cursorArgs := []string{
			"-f", "query=" + query,
			"-f", "owner=" + pr.Owner,
			"-f", "repo=" + pr.Repo,
			"-F", fmt.Sprintf("pr=%d", pr.Number),
		}
		if cursor != "" {
			cursorArgs = append(cursorArgs, "-f", "after="+cursor)
		}
		cmd := exec.Command("gh", "api", "graphql")
		cmd.Args = append(cmd.Args, cursorArgs...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			return 0, fmt.Errorf("graphql query: %w", err)
		}

		n, hasNext, next, err := countUnresolvedInPage(out, myLogin)
		if err != nil {
			return 0, err
		}
		total += n
		if !hasNext {
			break
		}
		cursor = next
	}
	return total, nil
}

// countUnresolvedInPage parses one page of the reviewThreads GraphQL response and
// returns the count of unresolved threads from myLogin or chatgpt-codex-connector[bot]
// (or the bare login without [bot] suffix that GraphQL sometimes returns),
// plus pagination info.
func countUnresolvedInPage(out []byte, myLogin string) (count int, hasNextPage bool, endCursor string, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return 0, false, "", fmt.Errorf("parse graphql response: %w", err)
	}

	threads := resp.Data.Repository.PullRequest.ReviewThreads
	for _, node := range threads.Nodes {
		if node.IsResolved || len(node.Comments.Nodes) == 0 {
			continue
		}
		author := node.Comments.Nodes[0].Author.Login
		if author == myLogin || author == "chatgpt-codex-connector[bot]" || author == "chatgpt-codex-connector" {
			count++
		}
	}
	return count, threads.PageInfo.HasNextPage, threads.PageInfo.EndCursor, nil
}

// codexHasHighPriorityFindings returns true if any unresolved thread from
// chatgpt-codex-connector[bot] (or the bare login) contains a P0 or P1 badge
// pattern in its first comment body. On API error it returns (true, err) so
// that callers treat unknown state conservatively.
func codexHasHighPriorityFindings(workDir string, pr PRInfo) (bool, error) {
	const query = `
query($owner: String!, $repo: String!, $pr: Int!, $after: String) {
  repository(owner: $owner, name: $repo) {
    pullRequest(number: $pr) {
      reviewThreads(first: 100, after: $after) {
        pageInfo { hasNextPage endCursor }
        nodes {
          isResolved
          comments(first: 1) {
            nodes {
              body
              author { login }
            }
          }
        }
      }
    }
  }
}`
	cursor := ""
	for {
		cursorArgs := []string{
			"-f", "query=" + query,
			"-f", "owner=" + pr.Owner,
			"-f", "repo=" + pr.Repo,
			"-F", fmt.Sprintf("pr=%d", pr.Number),
		}
		if cursor != "" {
			cursorArgs = append(cursorArgs, "-f", "after="+cursor)
		}
		cmd := exec.Command("gh", "api", "graphql")
		cmd.Args = append(cmd.Args, cursorArgs...)
		cmd.Dir = workDir
		out, err := cmd.Output()
		if err != nil {
			return true, fmt.Errorf("graphql query: %w", err)
		}

		found, hasNext, next, err := codexHighPriorityInPage(out)
		if err != nil {
			return true, err
		}
		if found {
			return true, nil
		}
		if !hasNext {
			break
		}
		cursor = next
	}
	return false, nil
}

// codexHighPriorityInPage checks one page of reviewThreads for unresolved
// chatgpt-codex-connector threads whose first comment body contains a P0 or P1 badge.
func codexHighPriorityInPage(out []byte) (found bool, hasNextPage bool, endCursor string, err error) {
	var resp struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						PageInfo struct {
							HasNextPage bool   `json:"hasNextPage"`
							EndCursor   string `json:"endCursor"`
						} `json:"pageInfo"`
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									Body   string `json:"body"`
									Author struct {
										Login string `json:"login"`
									} `json:"author"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		return false, false, "", fmt.Errorf("parse graphql response: %w", err)
	}

	threads := resp.Data.Repository.PullRequest.ReviewThreads
	for _, node := range threads.Nodes {
		if node.IsResolved || len(node.Comments.Nodes) == 0 {
			continue
		}
		c := node.Comments.Nodes[0]
		if c.Author.Login != "chatgpt-codex-connector[bot]" && c.Author.Login != "chatgpt-codex-connector" {
			continue
		}
		if strings.Contains(c.Body, "/badge/P0-") || strings.Contains(c.Body, "/badge/P1-") {
			return true, threads.PageInfo.HasNextPage, threads.PageInfo.EndCursor, nil
		}
	}
	return false, threads.PageInfo.HasNextPage, threads.PageInfo.EndCursor, nil
}

// allCIPassing returns true if no CI checks are in a failing state.
// Pending/queued checks are treated as non-blocking per the skill spec.
func allCIPassing(workDir string, prNumber int) (bool, error) {
	cmd := exec.Command("gh", "pr", "checks", fmt.Sprintf("%d", prNumber), "--json", "name,state")
	cmd.Dir = workDir
	// gh returns exit code 8 when checks are pending, non-zero on auth/network
	// errors too. Use CombinedOutput to capture stderr for diagnostics, but
	// keep the raw stdout for JSON parsing via cmd.Output semantics.
	out, err := cmd.Output()
	if err != nil {
		// If stdout is non-empty we still got partial JSON — try to parse it.
		// If stdout is empty the command truly failed (auth/network/missing PR)
		// and we must not treat that as "CI passing".
		if len(out) == 0 {
			return false, fmt.Errorf("gh pr checks: %w", err)
		}
	}
	return ciPassingFromJSON(out)
}

// ciPassingFromJSON parses the JSON emitted by `gh pr checks --json name,state`
// and returns false if any check is in a failing or non-clean terminal state.
// Empty/nil input means no checks were found, which is treated as passing
// (a PR with no CI configured is not failing).  Callers that obtain output via
// an external command must propagate command errors before calling this function
// to avoid masking auth/network failures.
func ciPassingFromJSON(out []byte) (bool, error) {
	if len(out) == 0 {
		return true, nil
	}

	var checks []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(out, &checks); err != nil {
		return false, fmt.Errorf("parse checks JSON: %w", err)
	}

	for _, c := range checks {
		s := strings.ToLower(c.State)
		switch s {
		case "failing", "failure", "failed", "error",
			"cancelled", "cancel", "timed_out", "action_required", "stale",
			"startup_failure":
			return false, nil
		}
	}
	return true, nil
}

func getMyLogin(workDir string) (string, error) {
	cmd := exec.Command("gh", "api", "user", "--jq", ".login")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh api user: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// currentGitBranch returns the name of the currently checked-out branch in workDir.
func currentGitBranch(workDir string) (string, error) {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
