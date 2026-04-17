"""Tests for tools/bump_datadog_agent/bump.py.

Runs with stdlib only; PyGithub is stubbed before the script is imported, so
the suite executes anywhere Python 3.10+ is installed. From the repo root:

    python3 -m unittest discover -s tools/bump_datadog_agent/tests -v
"""

from __future__ import annotations

import os
import sys
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

_github_stub = MagicMock()
_github_stub.GithubException = type("GithubException", (Exception,), {})
sys.modules["github"] = _github_stub

# Put the script's directory on sys.path so we can `import bump` regardless of
# where the test runner is invoked from.
sys.path.insert(0, str(Path(__file__).resolve().parent.parent))
import bump  # noqa: E402


class TestConfigureCredentials(unittest.TestCase):
    def test_writes_credentials_file_and_invokes_git_config(self):
        with tempfile.TemporaryDirectory() as td:
            workdir = Path(td)
            (workdir / ".git").mkdir()
            with patch("bump.subprocess.run") as mock_run:
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

    def test_removes_single_line_versioned_replace(self):
        original = textwrap.dedent(
            """\
            module m
            go 1.25

            replace github.com/DataDog/rshell v0.0.10 => /local/path

            require github.com/DataDog/rshell v0.0.10
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("replace github.com/DataDog/rshell", updated)
        self.assertIn("require github.com/DataDog/rshell v0.0.10", updated)

    def test_removes_block_form_unversioned_entry(self):
        original = textwrap.dedent(
            """\
            module m
            go 1.25

            replace (
                github.com/DataDog/rshell => /local/path
            )
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("DataDog/rshell", updated)

    def test_removes_block_form_versioned_entry(self):
        original = textwrap.dedent(
            """\
            module m
            go 1.25

            replace (
                github.com/DataDog/rshell v0.0.10 => /local/path
            )
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("DataDog/rshell", updated)

    def test_strips_only_rshell_entries_from_mixed_block(self):
        original = textwrap.dedent(
            """\
            replace (
                github.com/other => /a
                github.com/DataDog/rshell => /rshell-local
                github.com/DataDog/rshell v0.0.10 => /rshell-pinned
                github.com/another => /b
            )
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            updated = go_mod.read_text()
        self.assertNotIn("DataDog/rshell", updated)
        self.assertIn("github.com/other => /a", updated)
        self.assertIn("github.com/another => /b", updated)

    def test_does_not_touch_require_lines(self):
        # Lines in a require block look similar but have no `=>`; must not be
        # stripped.
        original = textwrap.dedent(
            """\
            require (
                github.com/DataDog/rshell v0.0.10
            )
            """
        )
        with tempfile.TemporaryDirectory() as td:
            go_mod = Path(td) / "go.mod"
            go_mod.write_text(original)
            bump.strip_rshell_replace(go_mod)
            self.assertEqual(go_mod.read_text(), original)


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

    def test_filename_is_deterministic_for_same_version(self):
        with tempfile.TemporaryDirectory() as td:
            repo_root = Path(td)
            (repo_root / "releasenotes" / "notes").mkdir(parents=True)
            a = bump.write_release_note(repo_root, "v0.0.11")
            b = bump.write_release_note(repo_root, "v0.0.11")
            self.assertEqual(a.name, b.name)

    def test_filename_differs_between_versions(self):
        with tempfile.TemporaryDirectory() as td:
            repo_root = Path(td)
            (repo_root / "releasenotes" / "notes").mkdir(parents=True)
            a = bump.write_release_note(repo_root, "v0.0.11")
            b = bump.write_release_note(repo_root, "v0.0.12")
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
        existing_pr = MagicMock()
        existing_pr.html_url = "https://github.com/DataDog/datadog-agent/pull/999"
        mock_repo = MagicMock()
        mock_repo.get_pulls.return_value = [existing_pr]
        _github_stub.Github.reset_mock()
        _github_stub.Github.return_value.get_repo.return_value = mock_repo

        with patch.dict(os.environ, {"GITHUB_TOKEN": "fake-token"}):
            with patch.object(sys, "argv", ["bump_datadog_agent.py", "v0.0.99"]):
                # subprocess.run should never be reached on the early-exit path
                with patch("bump.subprocess.run") as mock_run:
                    result = bump.main()
                    mock_run.assert_not_called()

        self.assertEqual(result, 0)
        mock_repo.get_pulls.assert_called_once()
        call_kwargs = mock_repo.get_pulls.call_args.kwargs
        self.assertEqual(call_kwargs["state"], "open")
        self.assertEqual(call_kwargs["head"], "DataDog:bump-rshell-v0.0.99")


class TestTokenScrubbing(unittest.TestCase):
    def test_github_token_removed_from_environ_before_subprocess_calls(self):
        # Use the "PR already exists" path because it goes just far enough to
        # create the GitHub client — which is where the token should get
        # scrubbed — without needing to mock clone/go/dda.
        existing_pr = MagicMock()
        existing_pr.html_url = "https://github.com/DataDog/datadog-agent/pull/1"
        mock_repo = MagicMock()
        mock_repo.get_pulls.return_value = [existing_pr]
        _github_stub.Github.reset_mock()
        _github_stub.Github.return_value.get_repo.return_value = mock_repo

        with patch.dict(os.environ, {"GITHUB_TOKEN": "fake-token"}, clear=False):
            with patch.object(sys, "argv", ["bump.py", "v0.0.99"]):
                bump.main()
                # After main(), the token must no longer be readable from env.
                self.assertNotIn("GITHUB_TOKEN", os.environ)


if __name__ == "__main__":
    unittest.main()
