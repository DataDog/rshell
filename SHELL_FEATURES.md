# Shell Features Reference

This document lists every shell feature and whether it is supported (✅) or blocked (❌).
Blocked features are rejected before execution with exit code 2.

The in-shell `help` command mirrors these feature categories: run `help` for a concise supported/unsupported summary plus commands, or `help <feature|command>` for details about a specific feature or command.

## Builtins

- ✅ `break` — exit the innermost `for` loop
- ✅ `cat [-AbeEnstTuv] [FILE]...` — concatenate files to stdout; supports line numbering, blank squeezing, and non-printing character display
- ✅ `cd [-LP] [-|DIRECTORY]` — change the shell's working directory; targets must lie inside `AllowedPaths`; with no operand changes to `$HOME`, with `-` swaps to `$OLDPWD` (printing the new path); `-L` (default) preserves symlinks, `-P` resolves them; `-e`, `-@`, `CDPATH`, `~user` not supported
- ✅ `continue` — skip to the next iteration of the innermost `for` loop
- ✅ `cut [-b LIST|-c LIST|-f LIST] [-d DELIM] [-s] [-n] [--complement] [--output-delimiter=STRING] [FILE]...` — remove sections from each line of files
- ✅ `df [-hHkPTialx] [-t TYPE] [-x TYPE] [--total] [--no-sync]` — report file system disk space usage (Linux/macOS only; on Windows `df` exits 1 with `df: not supported on this platform` because mount enumeration goes through `/proc/self/mountinfo` on Linux and `getfsstat(2)` on macOS, neither of which has a Windows equivalent); Linux reads `/proc/self/mountinfo` directly via `os.Open`, bypassing `AllowedPaths`; positional `FILE` operands and `--sync`, `-B`, `--output` are not supported; mount table capped at 100 000 entries
- ✅ `du [-asScSLP0bhkm] [-d N] [--apparent-size|--si] [FILE]...` — estimate file space usage; recursion capped at depth 256 and hardlink-dedup tracking capped at 2²⁰ entries; `--files0-from`, `--exclude-from`/`-X`, `--exclude` are rejected (data-exfiltration / file-driven control); `-B`/`--block-size`, `-t`/`--threshold`, `-x`/`--one-file-system`, `--inodes`, `--time`, `-l`/`--count-links` are not implemented
- ✅ `echo [-neE] [ARG]...` — write arguments to stdout; `-n` suppresses trailing newline, `-e` enables backslash escapes, `-E` disables them (default)
- ✅ `exit [N]` — exit the shell with status N (default 0)
- ✅ `false` — return exit code 1
- ✅ `find [-L] [-P] [PATH...] [EXPRESSION]` — search for files in a directory hierarchy; supports `--help`, `-name`, `-iname`, `-path`, `-ipath`, `-type` (b,c,d,f,l,p,s), `-size`, `-empty`, `-newer`, `-mtime`, `-mmin`, `-perm`, `-maxdepth`, `-mindepth`, `-print`, `-print0`, `-exec CMD {} \;`, `-execdir CMD {} \;`, `-prune`, `-quit`, logical operators (`!`, `-a`, `-o`, `()`); blocks `-delete`, `-regex` for sandbox safety
- ✅ `free [-h]` — report host memory and swap usage; a narrow read-only investigation snapshot, not a remediation or repeated-sampling command (see planned `vmstat`); Linux only, reads `/proc/meminfo` directly via `os.Open`, bypassing `AllowedPaths` (on macOS/Windows `free` exits 1 with `free: not supported on this platform` because neither exposes the buffers/cache/shared breakdown through a syscall this shell can call without cgo); `-h`/`--human` prints IEC binary sizes (e.g. `1.5Gi`); `-b`/`-k`/`-m`/`-g`/`--si`/`-w`/`-t`/`-s`/`-c` (repeated sampling) are not supported
- ✅ `grep [-EFGivclLnHhoqsxw] [-e PATTERN] [-m NUM] [-A NUM] [-B NUM] [-C NUM] PATTERN [FILE]...` — print lines that match patterns; uses RE2 regex engine (linear-time, no backtracking)
- ✅ `head [-n N|-c N] [-q|-v] [FILE]...` — output the first part of files (default: first 10 lines); `-z`/`--zero-terminated` and `--follow` are rejected
- ✅ `help [--all] [feature|command]` — display rshell features, a concise unsupported-feature summary, available commands, and the configured `AllowedPaths` sandbox roots grouped by read-only and read-write access (or a notice that no paths are configured); with a topic, show detailed help for that feature or command
- ✅ `ip [-o|-4|-6|--brief] addr|link [show] [dev IFNAME]` — show network interface addresses and link-layer info (read-only); write ops (`add`, `del`, `flush`, `set`), namespace ops (`netns`, `-n`), and batch mode (`-b`/`-B`/`--force`) are blocked
- ✅ `ip route [show|list]` — show IPv4 routing table (Linux only; reads `/proc/net/route` directly via `os.Open`, bypassing `AllowedPaths`); at most 10 000 entries loaded; lines longer than 1 MiB abort parsing with an error (exit 1)
- ✅ `ip route get ADDRESS` — show the route selected by longest-prefix-match for ADDRESS (Linux only); write ops (`add`, `del`, `flush`, `replace`, `change`, `save`, `restore`) are blocked; `-6` (IPv6 routing) is not supported
- ✅ `journalctl (-u SERVICE...|-k) [-b] [-n COUNT] [--since TIME] [-o short|cat]` — bounded journal investigation for exact system services or current-boot kernel messages; defaults to 100 and caps at 1,000 entries, 32 service scopes, and 64 KiB per selected field; `short` sanitizes controls, non-graphic Unicode, and invalid UTF-8 and labels unidentified kernel entries `kernel`, while `cat` matches host `journalctl` within the field bound by writing each `MESSAGE` value unchanged and appending one newline; service reads require exact `SERVICE:read` grants and kernel reads require `systemd-journald.service:read`; also supports `--disk-usage` with `systemd-journald.service:read`, plus remediation-only `--rotate`, `--vacuum-size SIZE`, `--vacuum-time AGE`, and `--dry-run` with `systemd-journald.service:clean`; time-only vacuum removes every eligible archive at least `AGE` old, while size vacuum requires `AGE` as a minimum deletion age and never deletes newer archives merely to reach its target; rotation may precede vacuuming, while `--dry-run` cannot rotate; bare/unrestricted reads, globbed services, arbitrary matches, follow mode, raw structured output, alternate sources, cursors, machines, namespaces, and arbitrary boot selection are unavailable; log reads use a bounded pure-Go parser for regular/compact and XZ/LZ4/Zstandard journal files with no host `journalctl`, cgo, or libsystemd dependency; rotation requires Linux with procfs descriptor links at `/proc/self/fd`, and mounted-file usage/vacuum operations support Linux/macOS
- ✅ `logrotate (-s SIZE|-f) [-n] [-v] FILE...` — remediation-mode helper that truncates log files to zero bytes through `AllowedPaths`; `--size SIZE` truncates only files at least SIZE bytes, `--force` explicitly truncates without a threshold, `--dry-run` reports without modifying files, and `--verbose` reports per-file actions. Symlinked write targets are rejected with `symlinks are not supported as write targets`; pass the real log path instead. This is not a full `logrotate(8)` replacement: no config parsing, rename-based rotation, retained copies, compression, state files, or rotate scripts.
- ✅ `sort [-rnhubfds] [-k KEYDEF] [-t SEP] [-c|-C] [FILE]...` — sort lines of text files; `-h`/`--human-numeric-sort` orders by SI suffix (none < K/k < M < G < T < P < E < Z < Y < R < Q) then by numeric value (single-letter suffixes only — `Ki`, `Mi`, etc. are not recognised); `-o`, `--compress-program`, and `-T` are rejected (filesystem write / exec)
- ✅ `ss [-tuaxlans4689Hoehs] [OPTION]...` — display network socket statistics; reads kernel socket state directly via `os.Open` (bypassing `AllowedPaths`) from: Linux: `/proc/net/`; macOS: sysctl; Windows: iphlpapi.dll; `-F`/`--filter` (GTFOBins file-read), `-p`/`--processes` (PID disclosure), `-K`/`--kill`, `-E`/`--events`, and `-N`/`--net` are rejected
- ✅ `systemctl [--system] [--no-pager] COMMAND [OPTION]... [UNIT]...` — remediation-mode-only bounded Linux system-manager inspection and control for exact, fully suffixed unit names; every invocation, including `--help`, bare/list/status inspection, and mutations, fails before grant lookup or manager access in read-only mode; once enabled, a bare invocation is restricted `list-units`, which supports `--all`, `--type`, `--state`, and `--no-legend` but considers only units with exact `read` grants; by default it returns already-loaded units that are active, failed, or carrying a job, while `--all` may load valid read-granted candidates and includes inactive units (nonexistent names may be omitted); the supported command set is exactly `list-units`, `status`, `start`, `stop`, `reload`, `restart`, `enable`, and `disable`; listing and status require exact `read` grants, and each mutation requires its matching exact action grant for every operand before any effect; runtime jobs use fixed `replace` mode, process multiple anchors sequentially in operand order, and may act on dependency-related units through normal systemd transaction semantics; enable/disable may follow `[Install] Alias=`, `Also=`, and template `DefaultInstance=` into auxiliary unit-file changes, then globally reload the manager, which may run generators and pick up unrelated host changes; granted unit payloads, install metadata, aliases, and dependency graphs are operator-trusted, so dedicated lifecycle verbs being absent does not make lifecycle effects impossible through a granted unit; every manager-backend operation has a fixed 30-second cap in addition to the runner deadline; accepts `.service`, `.timer`, `.socket`, and other valid unit types, with at most 32 operands and 64 KiB per returned field; `status` omits process command lines, logs, arbitrary properties, unit-file paths, and D-Bus object paths, and every human-readable field is sanitized; `show`, the `is-*` predicates, conditional restart variants, `reset-failed`, `--now`, arbitrary enumeration/properties, globs, implicit `.service`, user/machine/root/image targets, `clean`, standalone `daemon-reload`, `kill`, unit-file editing/linking/masking/presets, dedicated power-management verbs, asynchronous jobs, arbitrary job modes, and explicit dependency-expansion switches are unavailable; requires procfs descriptor links at `/proc/self/fd` and uses the configured public system D-Bus socket without executing host `systemctl`
- ✅ `ls [-1aAdFhlpRrSt] [--offset N] [--limit N] [FILE]...` — list directory contents; `--offset`/`--limit` are non-standard pagination flags (single-directory only, silently ignored with `-R` or multiple arguments, capped at 1,000 entries per call); offset operates on filesystem order (not sorted order) for O(n) memory
- ✅ `ping [-c N] [-W DURATION] [-i DURATION] [-q] [-4|-6] [-h] HOST` — send ICMP echo requests to a network host and report round-trip statistics; `-f` (flood), `-b` (broadcast), `-s` (packet size), `-I` (interface), `-p` (pattern), and `-R` (record route) are blocked; count/wait/interval are clamped to safe ranges with a warning; multicast, unspecified (`0.0.0.0`/`::`), and broadcast addresses (IPv4 last-octet `.255`) are rejected — note: directed broadcasts on non-standard subnets (e.g. `.127` on a `/25`) are not blocked without subnet-mask knowledge
- ✅ `ps [-e|-A] [-f] [-p PIDLIST]` — report process status; default shows current-session processes; `-e`/`-A` shows all; `-f` adds UID/PPID/STIME columns; `-p` selects by PID list; `CMD` shows only the process comm/executable name, never argv
- ✅ `printf FORMAT [ARGUMENT]...` — format and print data to stdout; supports `%s`, `%b`, `%c`, `%d`, `%i`, `%o`, `%u`, `%x`, `%X`, `%e`, `%E`, `%f`, `%F`, `%g`, `%G`, `%%`; format reuse for excess arguments; `%n` rejected (security risk); `-v` rejected
- ✅ `pwd [-LP]` — print the absolute pathname of the current working directory; `-L` (default) prints the shell's tracked logical path, `-P` resolves all symlinks; `-P` is best-effort within the sandbox (path components above `AllowedPaths` pass through unresolved); `--version` rejected
- ✅ `read [-r] [-p PROMPT] [-d DELIM] [-n N] [-N N] [-t SECS] [NAME...]` — read one delimited chunk from stdin and assign each IFS-split field to a shell variable (defaulting to `REPLY` when no NAME is given); `-n`/`-N` are capped at 1 MiB; non-raw mode treats `\<newline>` as a line continuation (both characters are dropped) and `\<X>` for any other `X` (including the active custom delimiter under `-d`) as a literal `X` with the backslash removed — e.g. `printf 'a\,b,c' | read -d , x` assigns `x="a,b"`; `-p` is suppressed unless stdin is a terminal (matches bash); `-a` (array), `-s` (silent), `-u` (read from FD), `-e` (readline), and `-i` (initial text) are not implemented
- ✅ `sed [-n] [-e SCRIPT] [-E|-r] [SCRIPT] [FILE]...` — stream editor for filtering and transforming text; uses RE2 regex engine; `-i`/`-f` rejected; `e`/`w`/`W`/`r`/`R` commands blocked
- ✅ `strings [-a] [-n MIN] [-t o|d|x] [-o] [-f] [-s SEP] [FILE]...` — print printable character sequences in files (default min length 4); offsets via `-t`/`-o`; filename prefix via `-f`; custom separator via `-s`
- ✅ `tail [-n N|-c N] [-q|-v] [-z] [FILE]...` — output the last part of files (default: last 10 lines); supports `+N` offset mode; `-f`/`--follow` is rejected
- ✅ `test EXPRESSION` / `[ EXPRESSION ]` — evaluate conditional expression (file tests, string/integer comparison, logical operators)
- ✅ `tr [-cdsCt] SET1 [SET2]` — translate, squeeze, and/or delete characters from stdin
- ✅ `truncate -s SIZE [-c] [FILE]...` — shrink or extend file size; **remediation mode only**, target must be within a `:rw` `AllowedPaths` root; SIZE supports GNU suffix grammar (K/k/KiB/kiB=1024, KB/kB=1000, M/G/T similarly, P/E uppercase-only); relative-size modifiers and `--reference`/`--io-blocks` are rejected
- ✅ `true` — return exit code 0
- ✅ `uname [-asnrvm]` — print system information (Linux only; reads from `/proc/sys/kernel/`, respects `--proc-path`)
- ✅ `uniq [OPTION]... [INPUT]` — report or omit repeated lines
- ✅ `wc [-l] [-w] [-c] [-m] [-L] [FILE]...` — count lines, words, bytes, characters, or max line length
- ✅ `xargs [-0] [-a FILE] [-d DELIM] [-E EOF-STR] [-I REPLSTR] [-L N] [-n N] [-r] [-s N] [-t] [-x] [COMMAND [INITIAL-ARGS]...]` — build and execute commands from standard input; only invokes other registered builtins (subject to `CommandAllowed`), so the GTFOBins shell-escape `xargs … /bin/sh` is rejected; flags outside the supported set above (e.g. `-p` interactive, `-P` parallel, `-o`/`--open-tty`, `--show-limits`) are rejected as unknown
- ❌ All other commands — return exit code 127 with `<cmd>: not found` unless an ExecHandler is configured

## Variables

- ✅ Assignment: `VAR=value`
- ✅ Expansion: `$VAR`, `${VAR}`
- ✅ `$?` — last exit code (the only supported special variable)
- ✅ Inline assignment: `VAR=value command` (scoped to that command)
- ✅ Command substitution: `$(cmd)`, `` `cmd` `` — captures stdout; trailing newlines stripped; `$(<file)` shortcut reads file directly (gated on `cat` being in the command allowlist); output capped at 1 MiB
- ❌ Arithmetic expansion: `$(( expr ))`
- ❌ Array assignment: `arr=(a b c)`, `arr[0]=x`
- ❌ Append assignment: `VAR+=value`
- ❌ Parameter expansion operations: `${#var}`, `${var:-default}`, `${var:=default}`, `${var:?msg}`, `${var:+alt}`, `${var:offset}`, `${var/pattern/repl}`, `${var#prefix}`, `${var%suffix}`, `${!var}`, `${!prefix*}`, case conversion
- ❌ Positional parameters: `$1`–`$9`, `$@`, `$*`, `$#`, `$0`
- ❌ Special variables: `$!`, `$LINENO`

## Control Flow

- ✅ `for VAR in WORDS; do CMDS; done`
- ✅ `while CONDITION; do CMDS; done` — runs CMDS while the last command of CONDITION exits 0
- ✅ `until CONDITION; do CMDS; done` — runs CMDS while the last command of CONDITION exits non-zero
- ✅ `&&` — AND list (short-circuit)
- ✅ `||` — OR list (short-circuit)
- ✅ `!` — negation (inverts exit code)
- ✅ `{ CMDS; }` — brace group
- ✅ `;` and newline as command separators
- ✅ `if` / `elif` / `else`
- ✅ Subshells: `( CMDS )` — runs commands in an isolated child environment; variable changes do not propagate to the parent; exit does not terminate the parent
- ❌ `case`
- ❌ `select`
- ❌ C-style for loop: `for (( i=0; i<N; i++ ))`
- ❌ Functions: `fname() { ... }`

## Pipes and Redirections

### Always supported (both modes)

- ✅ `|` — pipe stdout
- ✅ `<` — input redirection (read-only, within AllowedPaths)
- ✅ `<<DELIM` — heredoc
- ✅ `<<-DELIM` — heredoc with tab stripping
- ✅ `2>&1`, `>&2` — file descriptor duplication between stdout (1) and stderr (2)
- ✅ `> FILE`, `>> FILE` — write/truncate or append to a regular file; **remediation mode only**, target must be within a `:rw` `AllowedPaths` root (exit 1 otherwise)
- ✅ `2> FILE`, `2>> FILE` — same rules applied to the stderr stream; **remediation mode only**
- ✅ `&> FILE`, `&>> FILE` — redirect both stdout and stderr to the same file; **remediation mode only**, same `AllowedPaths` enforcement
- ❌ `|&` — pipe stdout and stderr (bash extension)
- ❌ `<<<` — herestring (bash extension)
- ❌ `<>` — read-write open (blocked in all modes)
- ❌ `<&N` — input file descriptor duplication

### Output redirections (mode-dependent)

| Redirect | read-only mode | remediation mode |
|----------|---------------|-----------------|
| `>/dev/null`, `2>/dev/null`, `&>/dev/null` | ✅ always accepted (discards output) | ✅ always accepted |
| `>>/dev/null`, `&>>/dev/null` | ✅ always accepted (same effect as truncate) | ✅ always accepted |
| `> FILE`, `>| FILE` | ❌ exit 2 (parse-time rejection) | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |
| `>> FILE` | ❌ exit 2 | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |
| `2> FILE` | ❌ exit 2 | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |
| `2>> FILE` | ❌ exit 2 | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |
| `&> FILE` | ❌ exit 2 | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |
| `&>> FILE` | ❌ exit 2 | ✅ within `:rw` `AllowedPaths`; exit 1 outside or in read-only roots |

## Quoting and Expansion

- ✅ Single quotes: `'literal'`
- ✅ Double quotes: `"with $expansion"`
- ✅ Globbing: `*`, `?`, `[abc]`, `[a-z]`, `[!a]`
- ✅ Line continuation: `\` at end of line
- ✅ Comments: `# text`
- ❌ Extended globbing: `@(pat)`, `*(pat)`, etc.
- ❌ Tilde expansion: `~`, `~/path`, `~user`
- ❌ Process substitution: `<(cmd)`, `>(cmd)`

## Execution

- ✅ AllowedCommands — restricts which commands (builtins or external) may be executed; commands require the `rshell:` namespace prefix (e.g. `rshell:cat`); if not set, no commands are allowed
- ✅ AllowedSystemServices policy — one shared default-deny capability map for exact unit names with `read`, `clean`, `start`, `stop`, `reload`, `restart`, `enable`, and `disable` actions; actionless grants are ignored, invalid names/actions are skipped, names are case-sensitive, are not normalized, and cannot contain `:` or globs; all valid unit types are accepted; `read` remains usable by bounded `journalctl` queries in read-only mode, every non-read action requires remediation mode, and the entire `systemctl` builtin separately requires remediation mode while `list-units` sees only exact `read` grants; allowing all commands does not bypass this policy; configure grants through `interp.AllowedSystemServices` or CLI `--allowed-services UNIT:ACTION[+ACTION...]`
- ✅ SystemdTargetConfig — systemd-aware builtins use standard local paths by default; explicit `JournalDirs`, `MachineIDPath`, `JournalControlSocket`, and `ManagerBusSocket` paths support container mount layouts without falling back to local endpoints; target paths are trusted configuration and bypass `AllowedPaths`; explicit mounts must all refer to the same host; manager operations use the public system D-Bus socket (default `/run/dbus/system_bus_socket`), pin its inode, authenticate, and verify the systemd manager peer's machine ID, while the private `/run/systemd/private` endpoint is unsupported
- ✅ AllowedPaths filesystem sandboxing — restricts all file access (read and write) to specified directories. Entries may end with `:ro` or `:rw` to indicate read-only and read-write permissions, respectively; entries without a suffix default to read-only. In remediation mode, write operations are accepted only inside the most-specific matching `:rw` root. Cross-root symlink fallback is read-only to avoid TOCTOU on writes; on Unix, symlink components in write targets are rejected with `symlinks are not supported as write targets` via a no-follow `openat` walk
- ✅ Whole-run execution timeout — callers can bound a `Run()` call via `context.Context`, `interp.MaxExecutionTime`, or the CLI `--timeout` flag; the deadline applies to the entire script, not each individual command
- ✅ ProcPath — overrides the proc filesystem path used by `ps` (default `/proc`; Linux-only; useful for testing/container environments); `ps` does not read `/proc/<pid>/cmdline`
- ✅ RemediationMode — opt-in mode (`interp.WithMode(interp.ModeRemediation)` / `--mode remediation`) that enables file-target output redirections (`>`, `>>`, `2>`, `&>`, `&>>`) within `:rw` `AllowedPaths` and remediation-only builtins such as `truncate`, `logrotate`, and the entire restricted `systemctl` surface; targets outside the allowlist or inside read-only roots fail with `permission denied` (exit 1); symlinked write targets fail with `symlinks are not supported as write targets`; `/dev/null` always accepted; `<>` remains blocked
- ❌ External commands — blocked by default; requires an ExecHandler to be configured and the binary to be within AllowedPaths
- ❌ Background execution: `cmd &`
- ❌ Coprocesses: `coproc`
- ❌ `time`
- ❌ `[[ ... ]]` extended test expressions (bash extension)
- ❌ `(( ... ))` arithmetic commands
- ❌ `declare`, `export`, `local`, `readonly`, `let`

## Environment

- ✅ Empty by default — no parent environment variables are inherited
- ✅ Caller-provided variables via the `Env` option
- ✅ `IFS` is set to space/tab/newline by default
- ✅ `ALLOWED_PATHS` — when `AllowedPaths` is configured, set to a `filepath.ListSeparator`-delimited list of resolved allowed directories (`:` on Unix, `;` on Windows)
- ❌ No automatic inheritance from the host process
- ❌ `export`, `readonly` are blocked

## Intentional Divergences from Bash

- **Time reference for `find -mmin`/`-mtime` and `ls -l`**: rshell captures `time.Now()` once at the start of each `Run()` call and shares it across all builtins in that run. Bash evaluates each command against its own invocation time. In practice this only matters for long-running scripts (e.g. `sleep 61; find . -mmin -1`) where the reference time drifts from the actual command start. Short-lived AI agent scripts are unaffected.

## Appendix

Formatting: In each category, supported features should be listed first, and the most useful ones first.
