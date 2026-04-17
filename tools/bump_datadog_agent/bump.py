#!/usr/bin/env python3
"""Open a PR on DataDog/datadog-agent that bumps the pinned rshell version.

Invoked by the `bump_datadog_agent` GitLab CI job after a new rshell tag is
detected. Expects:
  - sys.argv[1]: the rshell tag (e.g. "v0.0.11")
  - env GITHUB_TOKEN: a short-lived dd-octo-sts token scoped to
    DataDog/datadog-agent with contents:write + pull-requests:write.
"""

from __future__ import annotations

import hashlib
import os
import re
import subprocess
import sys
import tempfile
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


def log(msg: str) -> None:
    """Emit a progress line to stdout. Call sites must never pass secrets."""
    print(f"[bump] {msg}", flush=True)


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


_RSHELL_REPLACE_RE = re.compile(
    rf"^[ \t]*(?:replace\s+)?{re.escape(RSHELL_MODULE)}(?:\s+v\S+)?\s+=>\s+[^\n]*$\n?",
    re.MULTILINE,
)


def strip_rshell_replace(go_mod: Path) -> None:
    """Remove rshell replace directives from go.mod in every valid Go form.

    Handles:
      - single-line unversioned:  replace github.com/DataDog/rshell => /path
      - single-line versioned:    replace github.com/DataDog/rshell v0.0.10 => /path
      - block-form entries (no leading `replace` keyword on the line itself)

    Any leftover empty `replace ( )` block is normalized away by `dda inv tidy`
    downstream.
    """
    original = go_mod.read_text()
    updated = _RSHELL_REPLACE_RE.sub("", original)
    if updated != original:
        go_mod.write_text(updated)
        log(f"stripped replace directive(s) for {RSHELL_MODULE} from go.mod")


def current_rshell_version(go_mod: Path) -> str | None:
    """Return the rshell version pinned in a `require` declaration, ignoring `replace` lines."""
    pattern = re.compile(
        rf"^\s*(?:require\s+)?{re.escape(RSHELL_MODULE)}\s+(v\S+)(?!\s*=>)",
        re.MULTILINE,
    )
    m = pattern.search(go_mod.read_text())
    return m.group(1) if m else None


def write_release_note(repo_root: Path, version: str) -> Path:
    # Deterministic per (module, version) so retries produce the identical file.
    suffix = hashlib.sha256(f"{RSHELL_MODULE}@{version}".encode()).hexdigest()[:16]
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
        print("usage: bump.py <tag>", file=sys.stderr)
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
    from github import Auth, Github, GithubException

    gh = Github(auth=Auth.Token(token), per_page=100)
    repo = gh.get_repo(TARGET_REPO)
    branch = f"bump-rshell-{version}"

    # Scrub the token from the process environment now that PyGithub has
    # internalized it. Subsequent subprocess calls (git, go, dda inv tidy,
    # which executes code from the freshly cloned repo) inherit os.environ by
    # default; leaving the write-scoped GITHUB_TOKEN in that environment would
    # let any of them exfiltrate it. PyGithub still holds the token in its
    # auth object, and git push authenticates via the on-disk credential-helper
    # file rather than the env var.
    os.environ.pop("GITHUB_TOKEN", None)

    # Include closed + merged PRs too, so we can distinguish:
    #   - open:             stop (human owns the cycle)
    #   - merged:           log and let the go.mod/go.sum diff decide
    #   - closed unmerged:  reopen it at create-pull time instead of failing
    log(f"checking {TARGET_REPO} for existing PRs with head={branch}")
    all_existing = list(
        repo.get_pulls(state="all", head=f"{TARGET_REPO.split('/')[0]}:{branch}")
    )
    open_existing = [p for p in all_existing if p.state == "open"]
    if open_existing:
        log(f"open PR already exists: {open_existing[0].html_url}; nothing to do")
        return 0

    closed_unmerged = [p for p in all_existing if p.state == "closed" and not p.merged]
    merged_existing = [p for p in all_existing if p.merged]
    if merged_existing:
        log(
            f"prior PR merged ({merged_existing[0].html_url}); will no-op unless "
            "go.mod/go.sum shows a genuine diff"
        )

    # Use a fresh, unique tempdir each run. Auto-cleanup on exit means no stale
    # state leaks between runs; starting empty means `git clone` never fails
    # with exit 128 because a prior directory already existed.
    with tempfile.TemporaryDirectory(prefix="bump-datadog-agent-") as td:
        workdir = Path(td) / "datadog-agent"
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

        if previous_version == version:
            log(f"datadog-agent already pins rshell at {version}; nothing to do")
            return 0

        strip_rshell_replace(go_mod)
        log(f"running: go get {RSHELL_MODULE}@{version}")
        run(["go", "get", f"{RSHELL_MODULE}@{version}"], cwd=workdir)
        log("running: dda inv tidy")
        run(["dda", "inv", "tidy"], cwd=workdir)

        run(["git", "add", "-A"], cwd=workdir)
        diff = subprocess.run(["git", "diff", "--cached", "--quiet"], cwd=workdir)
        if diff.returncode == 0:
            log(f"no changes to go.mod/go.sum; datadog-agent already at rshell {version}")
            return 0

        note = write_release_note(workdir, version)
        log(f"wrote release note: {note.relative_to(workdir)}")
        run(["git", "add", str(note)], cwd=workdir)

        commit_msg = (
            f"Bump rshell dependency from {previous_version} to {version}"
            if previous_version
            else f"Bump rshell dependency to {version}"
        )
        log(f"committing: {commit_msg}")
        run(["git", "commit", "-m", commit_msg], cwd=workdir)
        log(f"pushing branch {branch} to origin (force)")
        # Force push is safe: this branch is only ever written by this script, and
        # the force handles retries after a prior failure (deterministic tree,
        # non-deterministic commit timestamp).
        run(["git", "push", "--force", "origin", branch], cwd=workdir)

    pr_title = f"[automated] Bump rshell to {version}"
    pr_body = (
        f"Automated bump of `{RSHELL_MODULE}` to "
        f"[{version}](https://github.com/DataDog/rshell/releases/tag/{version}).\n"
    )
    if closed_unmerged:
        # Reusing a previously-closed PR avoids GitHub's duplicate-PR error and
        # keeps the review history in one thread.
        pr = closed_unmerged[0]
        log(f"reopening prior closed PR: {pr.html_url}")
        pr.edit(state="open", title=pr_title, body=pr_body)
    else:
        log("opening draft PR")
        pr = repo.create_pull(
            title=pr_title,
            body=pr_body,
            base=TARGET_BASE,
            head=branch,
            draft=True,
        )
        log(f"opened draft PR: {pr.html_url}")

    try:
        pr.add_to_labels(*PR_LABELS)
        log(f"added labels: {', '.join(PR_LABELS)}")
    except GithubException as e:
        log(f"warning: failed to add labels {PR_LABELS}: {e}")

    try:
        pr.create_review_request(team_reviewers=[REVIEW_TEAM])
        log(f"requested review from @DataDog/{REVIEW_TEAM}")
    except GithubException as e:
        log(f"warning: failed to request review from @DataDog/{REVIEW_TEAM}: {e}")

    return 0


if __name__ == "__main__":
    sys.exit(main())
