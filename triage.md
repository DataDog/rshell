# Command Triage

## `top`

Add a restricted `top` builtin for read-only process investigation.

`top` is a familiar first stop when diagnosing host pressure, slow applications, and memory growth. rshell already has `ps`, but its current output intentionally omits RSS, VSZ, `%MEM`, sorting, and repeated snapshots, so it cannot directly answer "which process is consuming memory right now?" without lower-level `/proc` commands.

The rshell version should be non-interactive, plain-text, and investigation-focused: show process names only, expose memory-oriented columns, support safe sorting/filtering, and avoid full argv disclosure. Linux-only support is acceptable initially because the most useful implementation maps naturally to `/proc`.
