#!/usr/bin/env python3
"""Open a PR on DataDog/datadog-agent that bumps the pinned rshell version.

Invoked by the `bump_datadog_agent` GitLab CI job after a new rshell tag is
detected. Expects:
  - sys.argv[1]: the rshell tag (e.g. "v0.0.11")
  - env GITHUB_TOKEN: a short-lived dd-octo-sts token scoped to
    DataDog/datadog-agent with contents:write + pull-requests:write.

Uses the Python standard library only (urllib.request for the GitHub REST API)
so the script runs on any datadog-agent buildimage without pip-install steps.
"""

from __future__ import annotations

import json
import os
import re
import secrets
import subprocess
import sys
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path
from typing import Any

TARGET_REPO = "DataDog/datadog-agent"
TARGET_BASE = "main"
RSHELL_MODULE = "github.com/DataDog/rshell"
REVIEW_TEAM = "action-platform"
PR_LABELS = ["changelog/no-changelog", "ask-review"]
GIT_USER_NAME = "github-actions[bot]"
GIT_USER_EMAIL = "github-actions[bot]@users.noreply.github.com"


def log(msg: str) -> None:
    """Emit a progress line to stdout. Call sites must never pass secrets."""
    print(f"[bump] {msg}", flush=True)


def run(cmd: list[str], cwd: Path | None = None, check: bool = True) -> subprocess.CompletedProcess[str]:
    return subprocess.run(cmd, cwd=cwd, check=check, text=True, capture_output=True)


def configure_credentials(workdir: Path, token: str) -> Path:
    """Store the GitHub token in a local git credentials file under .git/.

    Keeps the token out of process argv (visible to `ps`), command-line logs,
    and subprocess exception tracebacks. The file is mode 0600 and lives inside
    the ephemeral clone directory, which is discarded when the runner exits.
    """
    creds_path = workdir / ".git" / "ci-credentials"
    creds_path.write_text(f"https://x-access-token:{token}@github.com\n")
    creds_path.chmod(0o600)
    run(["git", "config", "credential.helper", f"store --file={creds_path}"], cwd=workdir)
    return creds_path


def strip_rshell_replace(go_mod: Path) -> None:
    """Drop any `replace github.com/DataDog/rshell => ...` line (one-time v0.0.11 transition)."""
    original = go_mod.read_text()
    pattern = re.compile(rf"^\s*replace\s+{re.escape(RSHELL_MODULE)}\s+=>.*$\n?", re.MULTILINE)
    updated = pattern.sub("", original)
    if updated != original:
        go_mod.write_text(updated)
        log(f"stripped `replace {RSHELL_MODULE} =>` directive from go.mod")


def current_rshell_version(go_mod: Path) -> str | None:
    """Return the rshell version pinned in a `require` declaration, ignoring `replace` lines."""
    pattern = re.compile(
        rf"^\s*(?:require\s+)?{re.escape(RSHELL_MODULE)}\s+(v\S+)(?!\s*=>)",
        re.MULTILINE,
    )
    m = pattern.search(go_mod.read_text())
    return m.group(1) if m else None


def write_release_note(repo_root: Path, version: str) -> Path:
    suffix = secrets.token_hex(8)
    note = repo_root / "releasenotes" / "notes" / f"bump-rshell-{version}-{suffix}.yaml"
    note.write_text(
        "---\n"
        "enhancements:\n"
        "  - |\n"
        f"    Bump ``rshell`` to {version} for the Private Action Runner.\n"
    )
    return note


class GitHub:
    """Minimal GitHub REST client — only the four endpoints this script needs."""

    def __init__(self, token: str, repo: str):
        self._token = token
        self._base = f"https://api.github.com/repos/{repo}"

    def _request(self, method: str, path: str, body: dict | None = None) -> Any:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(
            f"{self._base}{path}",
            data=data,
            method=method,
            headers={
                "Authorization": f"Bearer {self._token}",
                "Accept": "application/vnd.github+json",
                "X-GitHub-Api-Version": "2022-11-28",
                "Content-Type": "application/json",
            },
        )
        try:
            with urllib.request.urlopen(req) as resp:
                return json.loads(resp.read() or b"null")
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")
            raise RuntimeError(f"GitHub {method} {path} -> {e.code}: {detail}") from None

    def list_open_prs(self, head: str) -> list[dict]:
        q = urllib.parse.urlencode({"state": "open", "head": head})
        return self._request("GET", f"/pulls?{q}")

    def create_pull(self, *, title: str, body: str, base: str, head: str, draft: bool = True) -> dict:
        return self._request("POST", "/pulls", {
            "title": title, "body": body, "base": base, "head": head, "draft": draft,
        })

    def add_labels(self, pr_number: int, labels: list[str]) -> None:
        self._request("POST", f"/issues/{pr_number}/labels", {"labels": labels})

    def request_team_review(self, pr_number: int, team: str) -> None:
        self._request("POST", f"/pulls/{pr_number}/requested_reviewers", {"team_reviewers": [team]})


def main() -> int:
    if len(sys.argv) != 2:
        print("usage: bump_datadog_agent.py <tag>", file=sys.stderr)
        return 2
    version = sys.argv[1]
    if not re.fullmatch(r"v\d+\.\d+\.\d+", version):
        print(f"invalid version {version!r}; expected vX.Y.Z", file=sys.stderr)
        return 2

    token = os.environ.get("GITHUB_TOKEN")
    if not token:
        print("GITHUB_TOKEN is not set; dd-octo-sts exchange failed upstream", file=sys.stderr)
        return 1

    log(f"preparing bump of {RSHELL_MODULE} to {version}")
    gh = GitHub(token, TARGET_REPO)
    branch = f"bump-rshell-{version}"

    log(f"checking {TARGET_REPO} for existing PR with head={branch}")
    existing = gh.list_open_prs(head=f"{TARGET_REPO.split('/')[0]}:{branch}")
    if existing:
        log(f"PR already exists: {existing[0]['html_url']}; nothing to do")
        return 0

    workdir = Path("/tmp/datadog-agent")
    clone_url = f"https://github.com/{TARGET_REPO}.git"
    log(f"cloning {TARGET_REPO}@{TARGET_BASE} into {workdir}")
    run(["git", "clone", "--depth=1", "--branch", TARGET_BASE, clone_url, str(workdir)])
    log("configuring git credentials (token stored in .git/ci-credentials, not argv)")
    configure_credentials(workdir, token)
    run(["git", "config", "user.name", GIT_USER_NAME], cwd=workdir)
    run(["git", "config", "user.email", GIT_USER_EMAIL], cwd=workdir)
    log(f"creating branch {branch}")
    run(["git", "checkout", "-b", branch], cwd=workdir)

    go_mod = workdir / "go.mod"
    previous_version = current_rshell_version(go_mod)
    log(f"current pinned version in go.mod: {previous_version or '<none>'}")
    strip_rshell_replace(go_mod)
    log(f"running: go get {RSHELL_MODULE}@{version}")
    run(["go", "get", f"{RSHELL_MODULE}@{version}"], cwd=workdir)
    log("running: dda inv tidy")
    run(["dda", "inv", "tidy"], cwd=workdir)
    note = write_release_note(workdir, version)
    log(f"wrote release note: {note.relative_to(workdir)}")

    run(["git", "add", "-A"], cwd=workdir)
    diff = subprocess.run(["git", "diff", "--cached", "--quiet"], cwd=workdir)
    if diff.returncode == 0:
        log(f"no staged changes; datadog-agent already at rshell {version}")
        return 0

    commit_msg = (
        f"Bump rshell dependency from {previous_version} to {version}"
        if previous_version
        else f"Bump rshell dependency to {version}"
    )
    log(f"committing: {commit_msg}")
    run(["git", "commit", "-m", commit_msg], cwd=workdir)
    log(f"pushing branch {branch} to origin")
    run(["git", "push", "origin", branch], cwd=workdir)

    log("opening draft PR")
    pr = gh.create_pull(
        title=f"[automated] Bump rshell to {version}",
        body=(
            f"Automated bump of `{RSHELL_MODULE}` to "
            f"[{version}](https://github.com/DataDog/rshell/releases/tag/{version}).\n"
        ),
        base=TARGET_BASE,
        head=branch,
        draft=True,
    )
    pr_number = pr["number"]
    log(f"opened draft PR: {pr['html_url']}")

    try:
        gh.add_labels(pr_number, PR_LABELS)
        log(f"added labels: {', '.join(PR_LABELS)}")
    except Exception as e:
        log(f"warning: failed to add labels {PR_LABELS}: {e}")

    try:
        gh.request_team_review(pr_number, REVIEW_TEAM)
        log(f"requested review from @DataDog/{REVIEW_TEAM}")
    except Exception as e:
        log(f"warning: failed to request review from @DataDog/{REVIEW_TEAM}: {e}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
