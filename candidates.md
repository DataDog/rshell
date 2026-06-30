# Command Candidates

## Entry Format

Each command entry should explain whether the command is a good fit for rshell and why. Use 🟢 for reasons to support it and 🔴 for reasons to reject, defer, or narrow the scope, so the decision is easy to scan.

Each entry should also state whether the use case is already covered by existing rshell commands. If coverage is partial, name the existing command and the specific missing capability.

## `stat`

🟢 Fit: consider a restricted `stat -f PATH...` builtin for filesystem-level inode investigation.

The inode exhaustion runbook uses `stat -f /var/spool/` to confirm total and free inodes for the filesystem backing a specific path. rshell already supports `df -i` / `df -ih`, which covers the same filesystem inode totals at the mount-table level, but `df` currently does not accept `FILE` operands. That leaves no direct way to ask "which filesystem backs this path, and how many inodes are free there?"

Coverage: partially covered by `df -i` / `df -ih`, but not for path-targeted filesystem stats.

🔴 Scope: this should not start as a full GNU/BSD `stat` implementation. A narrow read-only subset for `stat -f` is enough for this workflow. User-supplied paths must go through `AllowedPaths`; unlike `df` mount enumeration, these paths are operator input rather than hardcoded kernel pseudo-files.

## `top`

🟢 Fit: add a restricted `top` builtin for read-only process investigation.

`top` is a familiar first stop when diagnosing host pressure, slow applications, and memory growth. rshell already has `ps`, but its current output intentionally omits RSS, VSZ, `%MEM`, sorting, and repeated snapshots, so it cannot directly answer "which process is consuming memory right now?" without lower-level `/proc` commands.

Coverage: partially covered by `ps`, but not for memory-oriented columns, sorting, or repeated snapshots.

The rshell version should be non-interactive, plain-text, and investigation-focused: show process names only, expose memory-oriented columns, support safe sorting/filtering, and avoid full argv disclosure. Linux-only support is acceptable initially because the most useful implementation maps naturally to `/proc`.
