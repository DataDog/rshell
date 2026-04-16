#!/usr/bin/env python3
"""Open a PR on DataDog/datadog-agent that bumps the pinned rshell version.

Invoked by the `bump_datadog_agent` GitLab CI job after a new rshell tag is
detected. Expects:
  - sys.argv[1]: the rshell tag (e.g. "v0.0.11")
  - env GITHUB_TOKEN: a short-lived dd-octo-sts token scoped to
    DataDog/datadog-agent with contents:write + pull-requests:write.
"""

from __future__ import annotations

import os
import re
import secrets
import subprocess
import sys
from pathlib import Path

TARGET_REPO = "DataDog/datadog-agent"
TARGET_BASE = "main"
RSHELL_MODULE = "github.com/DataDog/rshell"
REVIEW_TEAM = "action-platform"
PR_LABELS = ["changelog/no-changelog", "ask-review"]
GIT_USER_NAME = "github-actions[bot]"
GIT_USER_EMAIL = "github-actions[bot]@users.noreply.github.com"


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

    from github import Auth, Github, GithubException

    gh = Github(auth=Auth.Token(token), per_page=100)
    repo = gh.get_repo(TARGET_REPO)
    branch = f"bump-rshell-{version}"

    existing = list(repo.get_pulls(state="open", head=f"{TARGET_REPO.split('/')[0]}:{branch}"))
    if existing:
        return 0

    workdir = Path("/tmp/datadog-agent")
    clone_url = f"https://github.com/{TARGET_REPO}.git"
    run(["git", "clone", "--depth=1", "--branch", TARGET_BASE, clone_url, str(workdir)])
    configure_credentials(workdir, token)
    run(["git", "config", "user.name", GIT_USER_NAME], cwd=workdir)
    run(["git", "config", "user.email", GIT_USER_EMAIL], cwd=workdir)
    run(["git", "checkout", "-b", branch], cwd=workdir)

    go_mod = workdir / "go.mod"
    previous_version = current_rshell_version(go_mod)
    strip_rshell_replace(go_mod)
    run(["go", "get", f"{RSHELL_MODULE}@{version}"], cwd=workdir)
    run(["dda", "inv", "tidy"], cwd=workdir)
    write_release_note(workdir, version)

    run(["git", "add", "-A"], cwd=workdir)
    diff = subprocess.run(["git", "diff", "--cached", "--quiet"], cwd=workdir)
    if diff.returncode == 0:
        return 0

    commit_msg = (
        f"Bump rshell dependency from {previous_version} to {version}"
        if previous_version
        else f"Bump rshell dependency to {version}"
    )
    run(["git", "commit", "-m", commit_msg], cwd=workdir)
    run(["git", "push", "origin", branch], cwd=workdir)

    pr = repo.create_pull(
        title=f"[automated] Bump rshell to {version}",
        body=(
            f"Automated bump of `{RSHELL_MODULE}` to "
            f"[{version}](https://github.com/DataDog/rshell/releases/tag/{version}).\n"
        ),
        base=TARGET_BASE,
        head=branch,
        draft=True,
    )

    try:
        pr.add_to_labels(*PR_LABELS)
    except GithubException:
        pass

    try:
        pr.create_review_request(team_reviewers=[REVIEW_TEAM])
    except GithubException:
        pass

    return 0


if __name__ == "__main__":
    sys.exit(main())
