You are running a behavior-only correctness scan of the rshell repository.

Read `docs/BEHAVIOR_CONTRACTS.md` completely before investigating. Treat it,
`SHELL_FEATURES.md`, command help, and the applicable user-visible portions of
`docs/RULES.md` as the contract. Bash or a GNU command is authoritative only
for supported behavior without a documented rshell divergence. Existing tests
are evidence, not ground truth, and current implementation output is never
ground truth merely because a test expects it.

## Scope

- Target: {{TARGET}}
- Depth: {{DEPTH}}
- Scanned commit: {{COMMIT}}
- Comparison base commit: {{BASE_COMMIT}}
- Maximum hypotheses: {{MAX_HYPOTHESES}}

Focus only on the requested target and directly related tests and
documentation. Prefer a few carefully verified hypotheses over a broad review.

This is not a security review. Do not investigate or report authorization
bypasses, sandbox escapes, remediation policy, privileged execution, secret or
telemetry exposure, resource-exhaustion attacks, or cross-tenant isolation.

## Workflow

1. Read the relevant implementation, documentation, and existing tests.
2. Identify behavior within the supported surface that is missing meaningful
   coverage or appears inconsistent with its oracle.
3. Prefer temporary scenario YAML files under `tests/scenarios/`. Always use
   YAML `|+` block scalars for scripts and exact stdout/stderr values. Use a
   temporary Go test only when the scenario framework cannot express the case.
4. Prefix every temporary file with `zz_correctness_scan_`. Do not modify or
   delete existing files.
5. Run focused rshell tests. For eligible Bash-compatible behavior, run the
   existing Bash comparison with `RSHELL_BASH_TEST=1`. Do not use Bash as the
   oracle for AWK or an intentional divergence.
6. Reproduce a claimed implementation defect at least twice from clean test
   fixtures. Capture exact stdout, stderr, and exit status.
7. Return the structured result required by the supplied JSON schema.

Do not modify production code or checked-in expectations. Do not commit, push,
create GitHub issues, or access production systems. Leave temporary test files
in place; the parent process destroys this worktree after collecting results.

Classify findings as follows:

- `confirmed-defect`: deterministic mismatch against an authoritative
  behavioral oracle, reproduced at least twice.
- `documentation-ambiguity`: authoritative user-visible sources conflict and
  the conflict is actionable.
- `missing-coverage`: a meaningful contract lacks a regression test, but no
  implementation defect was demonstrated.
- `inconclusive`: the claim lacks a stable oracle or could not be reproduced.

For a confirmed defect, include the exact script and fixture setup, commands
used, expected and actual outputs, reproduction count, source references, and
implementation references. If any required evidence is unavailable, classify
the observation as inconclusive instead.
