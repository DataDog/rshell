"""Tests for bump_datadog_agent.py.

Runs with stdlib only — the script and its tests have no third-party
dependencies:

    cd .gitlab/scripts && python3 -m unittest test_bump_datadog_agent -v
"""

from __future__ import annotations

import os
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).parent))
import bump_datadog_agent as bump  # noqa: E402


class TestConfigureCredentials(unittest.TestCase):
    def test_writes_credentials_file_and_invokes_git_config(self):
        with tempfile.TemporaryDirectory() as td:
            workdir = Path(td)
            (workdir / ".git").mkdir()
            with patch("bump_datadog_agent.subprocess.run") as mock_run:
                mock_run.return_value.returncode = 0
                path = bump.configure_credentials(workdir, "ghs_SECRET123")

            self.assertEqual(path, workdir / ".git" / "ci-credentials")
            self.assertTrue(path.exists())
            self.assertIn("ghs_SECRET123", path.read_text())
            self.assertIn("x-access-token", path.read_text())
            # file permissions are 0o600
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

            # git config was invoked with the path, not the token
            self.assertEqual(mock_run.call_count, 1)
            called_cmd = mock_run.call_args.args[0]
            self.assertEqual(called_cmd[:3], ["git", "config", "credential.helper"])
            self.assertNotIn("ghs_SECRET123", " ".join(called_cmd))
            self.assertIn(str(path), called_cmd[3])


class TestStripRshellReplace(unittest.TestCase):
    def test_removes_replace_directive(self):
        original = textwrap.dedent(
            """\
            module m
            go 1.25

            replace github.com/DataDog/rshell => /local/path

            require (
                github.com/DataDog/rshell v0.0.10
            )
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("replace github.com/DataDog/rshell", updated)
        self.assertIn("require (", updated)
        self.assertIn("github.com/DataDog/rshell v0.0.10", updated)

    def test_noop_when_no_replace(self):
        original = "module m\ngo 1.25\n\nrequire github.com/DataDog/rshell v0.0.10\n"
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            self.assertEqual(go_mod.read_text(), original)

    def test_preserves_other_replaces(self):
        original = textwrap.dedent(
            """\
            module m
            go 1.25

            replace github.com/other/mod => /a
            replace github.com/DataDog/rshell => /local/path
            replace github.com/yet/another => /b
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("DataDog/rshell", updated)
        self.assertIn("github.com/other/mod", updated)
        self.assertIn("github.com/yet/another", updated)


class TestCurrentRshellVersion(unittest.TestCase):
    def _write(self, content: str) -> Path:
        td = tempfile.TemporaryDirectory()
        self.addCleanup(td.cleanup)
        go_mod = Path(td.name) / "go.mod"
        go_mod.write_text(content)
        return go_mod

    def test_finds_version_in_require_block(self):
        go_mod = self._write(
            textwrap.dedent(
                """\
                require (
                    github.com/DataDog/rshell v0.0.10
                    github.com/DataDog/other v1.2.3
                )
                """
            )
        )
        self.assertEqual(bump.current_rshell_version(go_mod), "v0.0.10")

    def test_finds_version_in_single_require(self):
        go_mod = self._write("require github.com/DataDog/rshell v0.0.11\n")
        self.assertEqual(bump.current_rshell_version(go_mod), "v0.0.11")

    def test_ignores_replace_line_version(self):
        go_mod = self._write(
            textwrap.dedent(
                """\
                require github.com/DataDog/rshell v0.0.10
                replace github.com/DataDog/rshell v0.0.10 => /local
                """
            )
        )
        self.assertEqual(bump.current_rshell_version(go_mod), "v0.0.10")

    def test_returns_none_when_absent(self):
        go_mod = self._write("module m\ngo 1.25\n")
        self.assertIsNone(bump.current_rshell_version(go_mod))


class TestWriteReleaseNote(unittest.TestCase):
    def test_writes_correct_format_and_path(self):
        with tempfile.TemporaryDirectory() as td:
            repo_root = Path(td)
            (repo_root / "releasenotes" / "notes").mkdir(parents=True)
            note = bump.write_release_note(repo_root, "v0.0.11")
            self.assertTrue(note.exists())
            self.assertEqual(note.parent, repo_root / "releasenotes" / "notes")
            self.assertTrue(note.name.startswith("bump-rshell-v0.0.11-"))
            self.assertTrue(note.name.endswith(".yaml"))
            content = note.read_text()
            self.assertIn("enhancements:", content)
            self.assertIn("Bump ``rshell`` to v0.0.11", content)

    def test_filenames_are_unique(self):
        with tempfile.TemporaryDirectory() as td:
            repo_root = Path(td)
            (repo_root / "releasenotes" / "notes").mkdir(parents=True)
            a = bump.write_release_note(repo_root, "v0.0.11")
            b = bump.write_release_note(repo_root, "v0.0.11")
            self.assertNotEqual(a.name, b.name)


class TestMainInputValidation(unittest.TestCase):
    def test_missing_argv_returns_2(self):
        with patch.object(sys, "argv", ["bump_datadog_agent.py"]):
            self.assertEqual(bump.main(), 2)

    def test_extra_argv_returns_2(self):
        with patch.object(sys, "argv", ["bump_datadog_agent.py", "v0.0.11", "extra"]):
            self.assertEqual(bump.main(), 2)

    def test_rejects_version_without_v_prefix(self):
        with patch.object(sys, "argv", ["bump_datadog_agent.py", "0.0.11"]):
            self.assertEqual(bump.main(), 2)

    def test_rejects_non_semver(self):
        for bad in ("v0.0", "v1.2.3-alpha", "vX.Y.Z", "v1.2.3.4"):
            with self.subTest(bad=bad):
                with patch.object(sys, "argv", ["bump_datadog_agent.py", bad]):
                    self.assertEqual(bump.main(), 2)

    def test_missing_github_token_returns_1(self):
        with patch.dict(os.environ, {}, clear=True):
            with patch.object(sys, "argv", ["bump_datadog_agent.py", "v0.0.11"]):
                self.assertEqual(bump.main(), 1)


class TestMainIdempotency(unittest.TestCase):
    def test_exits_zero_when_pr_already_exists(self):
        existing_pr = {"html_url": "https://github.com/DataDog/datadog-agent/pull/999"}

        with patch.dict(os.environ, {"GITHUB_TOKEN": "fake-token"}):
            with patch.object(sys, "argv", ["bump_datadog_agent.py", "v0.0.99"]):
                with patch.object(bump.GitHub, "list_open_prs", return_value=[existing_pr]) as mock_list:
                    # subprocess.run should never be reached on the early-exit path
                    with patch("bump_datadog_agent.subprocess.run") as mock_run:
                        result = bump.main()
                        mock_run.assert_not_called()

        self.assertEqual(result, 0)
        mock_list.assert_called_once_with(head="DataDog:bump-rshell-v0.0.99")


class TestGitHubClient(unittest.TestCase):
    def test_list_open_prs_builds_correct_url(self):
        with patch.object(bump.GitHub, "_request", return_value=[]) as mock_req:
            bump.GitHub("tok", "DataDog/datadog-agent").list_open_prs(head="DataDog:my-branch")
        mock_req.assert_called_once()
        method, path = mock_req.call_args.args[:2]
        self.assertEqual(method, "GET")
        self.assertIn("state=open", path)
        self.assertIn("head=DataDog%3Amy-branch", path)

    def test_create_pull_sends_draft_true_and_body(self):
        with patch.object(bump.GitHub, "_request", return_value={"number": 1, "html_url": "x"}) as mock_req:
            bump.GitHub("tok", "DataDog/datadog-agent").create_pull(
                title="t", body="b", base="main", head="bump-rshell-v0.0.11", draft=True
            )
        method, path, body = mock_req.call_args.args
        self.assertEqual(method, "POST")
        self.assertEqual(path, "/pulls")
        self.assertEqual(body["draft"], True)
        self.assertEqual(body["title"], "t")
        self.assertEqual(body["head"], "bump-rshell-v0.0.11")

    def test_add_labels_hits_issues_endpoint(self):
        with patch.object(bump.GitHub, "_request", return_value=None) as mock_req:
            bump.GitHub("tok", "DataDog/datadog-agent").add_labels(42, ["foo", "bar"])
        method, path, body = mock_req.call_args.args
        self.assertEqual(method, "POST")
        self.assertEqual(path, "/issues/42/labels")
        self.assertEqual(body, {"labels": ["foo", "bar"]})

    def test_request_team_review_hits_pulls_endpoint(self):
        with patch.object(bump.GitHub, "_request", return_value=None) as mock_req:
            bump.GitHub("tok", "DataDog/datadog-agent").request_team_review(42, "action-platform")
        method, path, body = mock_req.call_args.args
        self.assertEqual(method, "POST")
        self.assertEqual(path, "/pulls/42/requested_reviewers")
        self.assertEqual(body, {"team_reviewers": ["action-platform"]})


if __name__ == "__main__":
    unittest.main()
