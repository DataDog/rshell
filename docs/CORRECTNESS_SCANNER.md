# Correctness scanner

The correctness scanner is a local, AI-assisted review tool for rshell's
user-visible behavior. It uses Codex to inspect one bounded target, generate
temporary tests in a detached worktree, and validate suspected mismatches with
the repository's existing scenario and Bash-comparison infrastructure.

It is intentionally separate from security review. The scanner reads
`docs/BEHAVIOR_CONTRACTS.md` and does not investigate sandbox escapes,
authorization bypasses, secret exposure, or other threat-model concerns.

## Prerequisites

- A clean, committed Git ref to scan. The current checkout may contain
  unrelated untracked files; the scanner always examines the committed ref.
- Go and Git.
- Codex CLI authenticated for local use.
- Docker when a Bash comparison is required.
- GitHub CLI authenticated for issue publication.

The scanner uses the Codex CLI's configured default model. Model selection is
not exposed by V0.

## Run locally

Scan changed behavior relative to `origin/main`:

```bash
make correctness-scan TARGET=changed DEPTH=quick
```

Scan one builtin:

```bash
make correctness-scan TARGET=cat DEPTH=standard
```

Issue publication is enabled by default. For a calibration or offline run:

```bash
make correctness-scan TARGET=cat CORRECTNESS_SCAN_ARGS=--no-publish
```

The underlying command also accepts `--ref`, `--base-ref`, `--repo`, and
`--output-dir`:

```bash
go run ./tools/correctness-scan \
  --target shell:variables \
  --depth quick \
  --ref HEAD \
  --no-publish
```

## Artifacts

Each run writes to the gitignored `.correctness/runs/<run-id>/` directory:

- `prompt.md` — the exact investigator prompt.
- `schema.json` — the structured output contract passed to Codex.
- `codex.stdout.log` and `codex.stderr.log` — Codex process output.
- `findings.json` — the structured final response.
- `report.md` — the human-readable scan report.
- `issues/*.md` — one publication preview per publishable finding, including
  when `--no-publish` is used.
- `issue-links.md` — issues created, updated, or reopened by the run.

The temporary Git worktree is removed after the scan unless
`--keep-worktree` is supplied for debugging.

## Publishing rules

The scanner publishes one issue per finding for these classifications:

- Confirmed correctness defect.
- Actionable documentation ambiguity.
- Actionable missing coverage.

Inconclusive observations remain in the local report. A stable fingerprint in
each issue body prevents duplicate issues. A repeat finding comments on the
existing issue; if the issue was closed, the scanner reopens it first.

The planned V1 automation will invoke this same command and publication path
on an Actions runner. Only scheduling and credential sourcing move into the
workflow, so local and automated scans use identical finding classifications,
validation, fingerprints, and issue bodies.

Use `--no-publish` whenever a run should not mutate GitHub. Publishing uses the
developer's existing `gh` authentication and never passes GitHub credentials
to Codex or generated test programs.

## Isolation and evidence

Codex receives workspace-write access only to the detached temporary worktree.
It may add temporary scenario or Go tests, but it must not modify production
code, commit, push, or create issues. Issue publication happens afterward in
the parent scanner process.

A confirmed defect must include an exact reproducer, expected and actual
stdout/stderr/status, at least two successful reproductions, source references,
and implementation references. Claims without that evidence are rejected or
reported as inconclusive.
