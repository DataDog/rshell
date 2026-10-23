# rshell Behavior Contracts

These contracts define the user-visible behavior examined by the correctness
scanner. They are compatibility contracts, not security invariants. The scan
must not investigate authorization bypasses, sandbox escapes, secret exposure,
resource-exhaustion attacks, privileged execution, or other threat-model
concerns covered by the repository's security tooling.

## Sources of truth

Apply these sources in order:

1. Explicit accepted design decisions and intentional divergences.
2. `SHELL_FEATURES.md` for the supported rshell surface.
3. User-visible flag, help, and output requirements in `docs/RULES.md`.
4. Bash and GNU behavior for functionality rshell claims to support without a
   documented divergence.
5. Builtin help and command descriptions.
6. Existing tests as evidence of intent, but not as final authority.

The current implementation is the subject under examination and is never an
authority merely because the checked-in tests agree with it. When authoritative
sources disagree, report a documentation ambiguity instead of choosing one.

## Contracts

### BEH-001: Supported shell semantics

For supported syntax and ordinary bounded inputs, parsing, expansion,
execution order, and exit-status propagation match Bash unless an intentional
divergence is documented.

### BEH-002: Builtin output

For supported flags and operands, deterministic builtins produce the expected
stdout bytes, including separators, headers, ordering, line endings, and
trailing-newline behavior.

### BEH-003: Errors and status

Ordinary invalid usage produces the documented stderr and exit status. This
contract covers user-visible behavior only; whether blocked functionality can
cross a security boundary is outside the scan.

### BEH-004: Input sources

Standard input, `-`, file operands, multiple files, pipes, and command
substitutions interact as documented and, where applicable, like the reference
command.

### BEH-005: Flag interactions

Supported short, long, combined, repeated, and reordered flags retain their
documented meaning. Attached values, separate values, and `--` termination
follow the documented or reference behavior.

### BEH-006: Shell state

Variables, working directories, `$?`, loops, and subshell isolation have the
documented lifetime and propagation behavior. Inline assignments remain scoped
correctly, and subshell changes do not escape to their parent.

### BEH-007: Formatting and ordering

Deterministic formatting, numeric rendering, sorting, labeling, and record
boundaries match the selected oracle for supported inputs.

### BEH-008: Intentional differences

Documented rshell-specific behavior matches the documentation even when Bash
or a GNU command behaves differently. A reference mismatch is not a defect
when the divergence is explicit and the implementation follows it.

### BEH-009: Surface consistency

Builtin registration, command descriptions, `--help`, `README.md`, and
`SHELL_FEATURES.md` agree about the supported user-visible command and flag
surface.

## Initial scan scope

The first scanner version focuses on deterministic text-processing builtins
and positive shell semantics:

- `cat`, `cut`, `echo`, `grep`, `head`, `printf`, `sed`, `sort`, `tail`,
  `test`, `tr`, `uniq`, and `wc`.
- Variables, quoting, expansion, pipes, substitutions, AND/OR lists, loops,
  conditionals, and subshells.

Other targets may be requested, but a scan must mark a claim inconclusive when
the target depends on live host state, privileged access, or a security-policy
decision rather than a stable behavioral oracle.

## Excluded security concerns

The correctness scanner must not report findings about:

- `AllowedCommands` or `AllowedPaths` bypasses.
- Remediation or system-service authorization.
- Privileged-helper behavior.
- Secret, environment, process, or telemetry exposure.
- Host-observation trust boundaries.
- Resource-exhaustion attacks or security limits.
- Cross-tenant isolation.
- Side effects occurring before authorization.

Those concerns belong to the security threat pack and its dedicated review
workflows.
