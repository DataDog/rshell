# Shell Features Reference

This document lists every shell feature and whether it is supported (✅) or blocked (❌).
Blocked features are rejected before execution with exit code 2.

The in-shell `help` command mirrors these feature categories: run `help` for a concise supported/unsupported summary plus commands, or `help <feature|command>` for details about a specific feature or command.

## Builtins

- ✅ `awk [-F SEP] [-v NAME=VALUE] ['PROGRAM'|-f PROGRAM-FILE] [FILE]...` — practical POSIX-oriented text processing with BEGIN/main/range/END rules, fields, scalars, associative arrays, POSIX-oriented regex, control flow, user functions, `print`/`printf`, and common string builtins. Input files honor `AllowedPaths`; evaluated expressions, strings, records, rules, statements, loop iterations, function calls/depth, regex work, substitution metadata, and stdout are bounded. awk programs cannot execute commands: `system()`, every form of `getline`, `close()`, and command pipes are rejected, as are file-output redirection and GNU-only features such as `gensub`, `asort`/`asorti`, `strtonum`, `IGNORECASE`, the third `match` argument, GNU boundary escapes, malformed-UTF-8 byte matching, and nondecimal source literals. Exact cross-implementation `printf`/numeric edge compatibility, including NaN/infinity spellings and uncommon flag combinations, is also outside the profile. Run `awk --help` for the exact profile.
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
- ✅ `help [--all] [feature|command]` — display rshell features, a concise unsupported-feature summary, available commands, the configured `AllowedPaths` sandbox roots grouped by read-only and read-write access, and the effective `AllowedSystemServices` unit/action grants in `UNIT:ACTION[+ACTION...]` form (with explicit default-deny notices when either policy is empty); in read-only mode configured non-read grants remain visible but are marked inactive; with a topic, show detailed help for that feature or command
- ✅ `ip [-o|-4|-6|--brief] addr|link [show] [dev IFNAME]` — show network interface addresses and link-layer info (read-only); write ops (`add`, `del`, `flush`, `set`), namespace ops (`netns`, `-n`), and batch mode (`-b`/`-B`/`--force`) are blocked
- ✅ `ip route [show|list]` — show IPv4 routing table (Linux only; reads `/proc/net/route` directly via `os.Open`, bypassing `AllowedPaths`); at most 10 000 entries loaded; lines longer than 1 MiB abort parsing with an error (exit 1)
- ✅ `ip route get ADDRESS` — show the route selected by longest-prefix-match for ADDRESS (Linux only); write ops (`add`, `del`, `flush`, `replace`, `change`, `save`, `restore`) are blocked; `-6` (IPv6 routing) is not supported
- ✅ `journalctl (-u SERVICE...|-k) [-b] [-n COUNT] [--since TIME] [-o short|cat]` — bounded journal investigation for exact system services or current-boot kernel messages; defaults to 100 and caps at 1,000 entries, 32 service scopes, and 64 KiB per selected field; `short` sanitizes controls, non-graphic Unicode, and invalid UTF-8 and labels unidentified kernel entries `kernel`, while `cat` matches host `journalctl` within the field bound by writing each `MESSAGE` value unchanged and appending one newline; service reads require exact `SERVICE:read` grants and kernel reads require `systemd-journald.service:read`; also supports `--disk-usage` with `systemd-journald.service:read`, plus remediation-only `--rotate`, `--vacuum-size SIZE`, `--vacuum-time AGE`, and `--dry-run` with `systemd-journald.service:clean`; time-only vacuum removes every eligible archive at least `AGE` old, while size vacuum targets the total allocated journal storage reported by `--disk-usage` (active plus archived, matching host `journalctl --vacuum-size`), requires `AGE` as a minimum deletion age, never deletes active journals or newer archives merely to reach its target, and reports the remaining allocated bytes when the target is still exceeded; rotation may precede vacuuming, while `--dry-run` cannot rotate; bare/unrestricted reads, globbed services, arbitrary matches, follow mode, raw structured output, alternate sources, cursors, machines, namespaces, and arbitrary boot selection are unavailable; log reads use a bounded pure-Go parser for regular/compact and XZ/LZ4/Zstandard journal files with no host `journalctl`, cgo, or libsystemd dependency; rotation requires Linux with procfs descriptor links at `/proc/self/fd`, and mounted-file usage/vacuum operations support Linux/macOS
- ✅ `jq [-hcernsR] [--arg NAME VALUE] [--argjson NAME JSON] [FILTER] [FILE]...` — self-contained jq 1.8 subset supporting fields, indexes, iteration, `?`, pipes, generators, literals, constructors, variables, comparisons, boolean operators, `//`, arithmetic, and `select`, `map`, `length`, `keys`, `has`, `type`, `not`, and `empty`; an omitted filter is identity. Up to 64 operands are accepted as identity-verified regular files through `AllowedPaths`; special files and descriptor portals are rejected. Limits include 64 KiB/4,096 nodes per filter, 8 MiB cumulative input, 1 MiB raw lines, 64-level/65,536-node/4-MiB values, 100,000 evaluation steps, 10,000 results, 131,072 retained nodes/8 MiB of evaluator state, 1 MiB output, and 64 variable bindings/1 MiB of binding text. Evaluation budgets are cumulative and fail hard. Assignments, user-defined functions, reduce/foreach, try/catch, regex, recursion, modules, environment access, file-based filters/variables, streaming, dates, formatters, `as`, conditionals, slices, array-index-by-array, and `$__loc__` are unavailable; number and per-value-limit differences are documented below
- ✅ `logrotate (-s SIZE|-f) [-n] [-v] FILE...` — remediation-mode helper that truncates log files to zero bytes through `AllowedPaths`; `--size SIZE` truncates only files at least SIZE bytes, `--force` explicitly truncates without a threshold, `--dry-run` reports without modifying files, and `--verbose` reports per-file actions. Symlinked write targets are rejected with `symlinks are not supported as write targets`, and, on Unix, hard-linked targets (link count > 1) with `hard links are not supported as write targets` — not enforced on Windows, where no link count is available from an open handle; pass the real, single-linked log path instead. This is not a full `logrotate(8)` replacement: no config parsing, rename-based rotation, retained copies, compression, state files, or rotate scripts.
- ✅ `lsof [-p PIDLIST] [-c NAME] [-u UIDLIST] [-a]` — list open files, with emphasis on files still open after being deleted (Linux only; on macOS/Windows `lsof` exits 1 with `lsof: not supported on this platform`); reads `/proc/<pid>/fd/*` metadata (COMMAND/PID/USER/FD/TYPE/DEVICE/SIZE/NODE) directly via `os.Readlink`/`unix.Stat`, bypassing `AllowedPaths` like `ss`/`df`/`free`, but the resolved NAME path is checked against `AllowedPaths` and replaced with `(restricted)` (or `(restricted) (deleted)`) when outside every configured root: a deliberate divergence, since NAME can point anywhere on the host filesystem; `-p` selects by PID list, `-c` by command-name prefix (literal), `-u` by numeric UID list, `-a` ANDs the selectors instead of the default OR; network detail (`-i`/`-U`/`-s`/`-T`/`-n`/`-P`), directory-tree scans (`+d`/`+D`), repeat mode (`+r`/`-r`), device-cache files (`-D`/`-f`/`+f`), and login-name UID resolution are not supported
- ✅ `sort [-rnhubfds] [-k KEYDEF] [-t SEP] [-c|-C] [FILE]...` — sort lines of text files; `-h`/`--human-numeric-sort` orders by SI suffix (none < K/k < M < G < T < P < E < Z < Y < R < Q) then by numeric value (single-letter suffixes only — `Ki`, `Mi`, etc. are not recognised); `-o`, `--compress-program`, and `-T` are rejected (filesystem write / exec)
- ✅ `ss [-tuaxlans4689Hoehs] [OPTION]...` — display network socket statistics; reads kernel socket state directly via `os.Open` (bypassing `AllowedPaths`) from: Linux: `/proc/net/`; macOS: sysctl; Windows: iphlpapi.dll; `-F`/`--filter` (GTFOBins file-read), `-p`/`--processes` (PID disclosure), `-K`/`--kill`, `-E`/`--events`, and `-N`/`--net` are rejected
- ✅ `stat -f FILE...` — report filesystem status for each operand through `AllowedPaths`: filesystem ID/type, name-length limit, block sizes, total/free/available blocks, and inode totals; supported on Linux, macOS, and Windows, with unavailable platform fields rendered honestly (`Namelen: ?` on macOS and inode totals/free as `-` on Windows); ordinary file-status mode, custom formats, terse output, and cache controls are not implemented
- ✅ `systemctl [--system] [--no-pager] COMMAND [OPTION]... [UNIT]...` — remediation-mode-only bounded Linux system-manager inspection and control for exact, fully suffixed unit names; every invocation, including `--help`, bare/list/status inspection, and mutations, fails before grant lookup or manager access in read-only mode; once enabled, a bare invocation is restricted `list-units`, which supports `--all`, `--type`, `--state`, and `--no-legend` but considers only units with exact `read` grants; by default it returns already-loaded units that are active, failed, or carrying a job, while `--all` may load valid read-granted candidates and includes inactive units (nonexistent names may be omitted); the supported command set is exactly `list-units`, `status`, `start`, `stop`, `reload`, `restart`, `enable`, and `disable`; listing and status require exact `read` grants, and each mutation requires its matching exact action grant for every operand before any effect; runtime jobs use fixed `replace` mode, process multiple anchors sequentially in operand order, and may act on dependency-related units through normal systemd transaction semantics; enable/disable may follow `[Install] Alias=`, `Also=`, and template `DefaultInstance=` into auxiliary unit-file changes, then globally reload the manager, which may run generators and pick up unrelated host changes; granted unit payloads, install metadata, aliases, and dependency graphs are operator-trusted, so dedicated lifecycle verbs being absent does not make lifecycle effects impossible through a granted unit; every manager-backend operation has a fixed 30-second cap in addition to the runner deadline; accepts `.service`, `.timer`, `.socket`, and other valid unit types, with at most 32 operands and 64 KiB per returned field; `status` omits process command lines, logs, arbitrary properties, unit-file paths, and D-Bus object paths, and every human-readable field is sanitized; `show`, the `is-*` predicates, conditional restart variants, `reset-failed`, `--now`, arbitrary enumeration/properties, globs, implicit `.service`, user/machine/root/image targets, `clean`, standalone `daemon-reload`, `kill`, unit-file editing/linking/masking/presets, dedicated power-management verbs, asynchronous jobs, arbitrary job modes, and explicit dependency-expansion switches are unavailable; requires procfs descriptor links at `/proc/self/fd` and uses the configured public system D-Bus socket without executing host `systemctl`
- ✅ `ls [-1aAdFhlpRrSt] [--offset N] [--limit N] [FILE]...` — list directory contents; `--offset`/`--limit` are non-standard pagination flags (single-directory only, silently ignored with `-R` or multiple arguments, capped at 1,000 entries per call); offset operates on filesystem order (not sorted order) for O(n) memory
- ✅ `ping [-c N] [-W DURATION] [-i DURATION] [-q] [-4|-6] [-h] HOST` — send ICMP echo requests to a network host and report round-trip statistics; `-f` (flood), `-b` (broadcast), `-s` (packet size), `-I` (interface), `-p` (pattern), and `-R` (record route) are blocked; count/wait/interval are clamped to safe ranges with a warning; multicast, unspecified (`0.0.0.0`/`::`), and broadcast addresses (IPv4 last-octet `.255`) are rejected — note: directed broadcasts on non-standard subnets (e.g. `.127` on a `/25`) are not blocked without subnet-mask knowledge
- ✅ `pmap [-x] [-h|--help] PID...` — report per-process virtual memory mappings (start address, size, permission mode, mapping label); Linux reads `<ProcPath>/<pid>/maps` (or `smaps` for `-x`) directly via `os.Open`, bypassing `AllowedPaths` (the configured proc root is trusted and the remaining path is derived only from the numeric PID); Windows enumerates committed regions via `VirtualQueryEx` and labels each by its `MEMORY_BASIC_INFORMATION` type rather than a resolved file path (`-x` exits 1 with `pmap: -x is not supported on this platform`, since Windows has no per-region RSS/Dirty breakdown without a working-set walk); macOS enumerates regions via the `proc_pidinfo(PROC_PIDREGIONINFO)` kernel call reached through the raw `syscall.SYS_PROC_INFO` trap (`-x` also exits 1 with `pmap: -x is not supported on this platform`, since `proc_regioninfo` reports whole-shadow-chain resident/dirty counts rather than the private Rss/Dirty split pmap's extended columns expect); the process header shows only the comm/executable name, never argv; full-path, device, range, raw-kernel-name, rc-file read/write, extra-extended, quiet, and version flags are not supported
- ✅ `ps [-e|-A] [-f] [-p PIDLIST] [-o FORMAT] [--sort SPEC]` — report process status; default shows current-session processes; `-e`/`-A` shows all; `-f` adds UID/PPID/STIME columns; `-p` selects by PID list; repeatable `-o`/`--format` accepts the canonical fields `pid`, `ppid`, `uid`, `state`, `tty`, `stime`, `time`, `comm`, `rss`, `vsz`, `pmem`, `pcpu`, and `etime`, with `%cpu` and `%mem` as aliases for `pcpu` and `pmem`; `--sort` accepts the same fields and aliases in a comma- or space-separated list with optional `+` (ascending) or `-` (descending) prefixes; `pcpu` is the lifetime average since process start, not an interval measurement; unavailable requested metrics render as `-`; process names are comm-only, argv/environment/executable-path selectors are rejected, and process argv and environment are never read; on macOS, `stime`/`etime` remain available while `rss`/`vsz`/`pmem`/`time`/`pcpu` may be unavailable for processes the caller cannot inspect; on Windows, `rss`/`vsz` use the compatibility-sensitive `SystemProcessInformation` snapshot API and may be unavailable if it fails (`pmem` is then unavailable too)
- ✅ `printf FORMAT [ARGUMENT]...` — format and print data to stdout; supports `%s`, `%b`, `%c`, `%d`, `%i`, `%o`, `%u`, `%x`, `%X`, `%e`, `%E`, `%f`, `%F`, `%g`, `%G`, `%%`; format reuse for excess arguments; `%n` rejected (security risk); `-v` rejected
- ✅ `pwd [-LP]` — print the absolute pathname of the current working directory; `-L` (default) prints the shell's tracked logical path, `-P` resolves all symlinks; `-P` is best-effort within the sandbox (path components above `AllowedPaths` pass through unresolved); `--version` rejected
- ✅ `read [-r] [-p PROMPT] [-d DELIM] [-n N] [-N N] [-t SECS] [NAME...]` — read one delimited chunk from stdin and assign each IFS-split field to a shell variable (defaulting to `REPLY` when no NAME is given); `-n`/`-N` are capped at 1 MiB; non-raw mode treats `\<newline>` as a line continuation (both characters are dropped) and `\<X>` for any other `X` (including the active custom delimiter under `-d`) as a literal `X` with the backslash removed — e.g. `printf 'a\,b,c' | read -d , x` assigns `x="a,b"`; `-p` is suppressed unless stdin is a terminal (matches bash); `-a` (array), `-s` (silent), `-u` (read from FD), `-e` (readline), and `-i` (initial text) are not implemented
- ✅ `rm [-v] FILE...` — remove files; **remediation mode only**, targets must be within a `:rw` `AllowedPaths` root; directories are always rejected, even empty ones (there is no recursive or `-d`/`--dir` mode) — any other non-directory entry (regular file, symlink, FIFO, socket, device node) may be removed; a symlink argument removes the link itself, never its referent; a hard link is removable (unlinking one name leaves the inode and its other names intact), unlike the write builtins which reject hard-linked targets; at most 10 files per invocation, checked before any file is removed, **and** at most 100 files in total per script run — the run-wide budget is shared across every `rm` invocation, loop iteration, subshell, and pipeline stage, so `for f in *; do rm "$f"; done` and `find … | xargs -n1 rm` are bounded too; only successful removals are charged, and each new run starts with a fresh budget; `-v`/`--verbose` prints `removed 'FILE'` per file; `-r`/`-R`/`--recursive`, `-f`/`--force`, `-i`/`-I`/`--interactive`, `-d`/`--dir`, `--preserve-root`, `--no-preserve-root`, and `--one-file-system` are rejected as unknown flags
- ✅ `sed [-n] [-e SCRIPT] [-E|-r] [SCRIPT] [FILE]...` — stream editor for filtering and transforming text; uses RE2 regex engine; `-i`/`-f` rejected; `e`/`w`/`W`/`r`/`R` commands blocked
- ✅ `strings [-a] [-n MIN] [-t o|d|x] [-o] [-f] [-s SEP] [FILE]...` — print printable character sequences in files (default min length 4); offsets via `-t`/`-o`; filename prefix via `-f`; custom separator via `-s`
- ✅ `tail [-n N|-c N] [-q|-v] [-z] [FILE]...` — output the last part of files (default: last 10 lines); supports `+N` offset mode; `-f`/`--follow` is rejected
- ✅ `test EXPRESSION` / `[ EXPRESSION ]` — evaluate conditional expression (file tests, string/integer comparison, logical operators)
- ✅ `tr [-cdsCt] SET1 [SET2]` — translate, squeeze, and/or delete characters from stdin
- ✅ `truncate -s SIZE [-c] [FILE]...` — shrink or extend file size; **remediation mode only**, target must be within a `:rw` `AllowedPaths` root; SIZE supports GNU suffix grammar (K/k/KiB/kiB=1024, KB/kB=1000, M/G/T similarly, P/E uppercase-only); relative-size modifiers and `--reference`/`--io-blocks` are rejected; hard-linked targets (link count > 1) are rejected with `hard links are not supported as write targets` on Unix
- ✅ `true` — return exit code 0
- ✅ `uname [-asnrvm]` — print system information (Linux only; reads from `/proc/sys/kernel/`, respects `--proc-path`)
- ✅ `uniq [OPTION]... [INPUT]` — report or omit repeated lines
- ✅ `uptime [-ps]` — tell how long the system has been running; shows current time, uptime duration, and load averages (1/5/15 min); user count omitted (privacy); `-p` prints human-readable duration; `-s` prints boot time as `YYYY-MM-DD HH:MM:SS`; load average unavailable on Windows
- ✅ `vmstat [-a] [-w] [-S k|K|m|M] [-s] [delay count]` — report virtual memory, swap, IO, and CPU pressure statistics; no arguments prints a since-boot snapshot, positive `delay count` samples for at most 29 seconds of total wait time, and `-s`/`--stats` prints a full counter summary; `-S` scales memory and swap-rate columns; reads `/proc/*` (Linux) or `sysctl(3)` (macOS) directly, bypassing `AllowedPaths` (same exception as `df`/`ss`/`ip route`); macOS lacks several counters (Mach-only), shown as `-` rather than fabricated; `-d`, `-p`, `-f`, `-m`, `-t`, `-n`, `-V` are not implemented; not supported on Windows
- ✅ `wc [-l] [-w] [-c] [-m] [-L] [FILE]...` — count lines, words, bytes, characters, or max line length
- ✅ `xargs [-0] [-a FILE] [-d DELIM] [-E EOF-STR] [-I REPLSTR] [-L N] [-n N] [-r] [-s N] [-t] [-x] [COMMAND [INITIAL-ARGS]...]` — build and execute commands from standard input; only invokes other registered builtins (subject to `CommandAllowed`), so the GTFOBins shell-escape `xargs … /bin/sh` is rejected; flags outside the supported set above (e.g. `-p` interactive, `-P` parallel, `-o`/`--open-tty`, `--show-limits`) are rejected as unknown
- ❌ All other commands — rejected with exit code 127; the public API does not expose an external-command executor

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

The `exit 2` rejections above apply to a **literal** target, which the validator
can resolve before the script runs. A target that is not a plain literal —
`> "$F"`, `> $F`, `> "/dev/null"` — is not resolved at validation time (the
validator never expands user input); it is checked at run time against the
expanded value instead. In read-only mode the policy is identical (only
`/dev/null` is accepted), but the failure mode is gentler: that single command
fails with exit 1 and the script continues, rather than the whole program being
rejected with exit 2. So `F=/dev/null; echo x > "$F"` succeeds, and
`F=out.txt; echo x > "$F"` prints
`> out.txt: file redirection is only supported for /dev/null` and fails just
that command. A command substitution in the target (`> $(cmd)`) is the one
dynamic form still rejected with exit 2, so that a blocked redirect never runs
the substituted command.

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

- ✅ AllowedCommands — allowlists names in the `rshell:` namespace (e.g. `rshell:cat`); registered commands may execute, while allowlisted unknown names still fail with exit code 127; if not set, no commands are allowed
- ✅ AllowedSystemServices policy — one shared default-deny capability map for exact unit names with `read`, `clean`, `start`, `stop`, `reload`, `restart`, `enable`, and `disable` actions; the `*` action wildcard grants every action supported by the running and future rshell versions for its exact unit and is expanded in `help`; actionless grants are ignored, invalid names/actions are skipped, names are case-sensitive, are not normalized, and cannot contain `:` or globs; all valid unit types are accepted; `read` remains usable by bounded `journalctl` queries in read-only mode, every non-read action requires remediation mode, and the entire `systemctl` builtin separately requires remediation mode while `list-units` sees only exact `read` grants; allowing all commands does not bypass this policy; a `read` grant on a `.slice` unit is the one selector whose journal results are not bounded by the granted name, because `journalctl -u SLICE` matches `_SYSTEMD_SLICE` and therefore returns every entry logged by every unit in that slice (see the trusted systemd target exception in `docs/RULES.md`); configure grants through `interp.AllowedSystemServices` or CLI `--allowed-services UNIT:ACTION[+ACTION...]`
- ✅ SystemdTargetConfig — systemd-aware builtins use standard local paths by default; explicit `JournalDirs`, `MachineIDPath`, `JournalControlSocket`, and `ManagerBusSocket` paths support container mount layouts without falling back to local endpoints; target paths are trusted configuration and bypass `AllowedPaths`; explicit mounts must all refer to the same host; manager operations use the public system D-Bus socket (default `/run/dbus/system_bus_socket`), pin its inode, authenticate, and verify the systemd manager peer's machine ID, while the private `/run/systemd/private` endpoint is unsupported
- ✅ AllowedPaths filesystem sandboxing — restricts all file access (read and write) to specified directories. Entries may end with `:ro` or `:rw` to indicate read-only and read-write permissions, respectively; entries without a suffix default to read-only. In remediation mode, write operations are accepted only inside the most-specific matching `:rw` root. Cross-root symlink fallback is read-only to avoid TOCTOU on writes; on Unix, symlink components in write targets are rejected with `symlinks are not supported as write targets` via a no-follow `openat` walk. Containment is path-based and cannot see hard links, so on Unix every content-mutating write target is additionally `fstat`ed after open and rejected with `hard links are not supported as write targets` when its link count exceeds one; `rm` is deliberately exempt (unlinking one of several names changes no content). Not enforced on Windows, where no link count is available from an open handle
- ✅ Whole-run execution timeout — callers can bound a `Run()` call via `context.Context`, `interp.MaxExecutionTime`, or the CLI `--timeout` flag; the deadline applies to the whole script. Cancellation is checked between bounded operations; a caller-owned stream blocked in `Read` or `Write` can delay return because rshell does not close borrowed streams
- ✅ ProcPath — overrides the proc filesystem path used by `ps` (default `/proc`; Linux-only; useful for testing/container environments); `ps` reads only bounded process metadata and counters, never `/proc/<pid>/cmdline` or process environment data
- ✅ RemediationMode — opt-in mode (`interp.WithMode(interp.ModeRemediation)` / `--mode remediation`) that enables file-target output redirections (`>`, `>>`, `2>`, `&>`, `&>>`) within `:rw` `AllowedPaths` and remediation-only builtins such as `truncate`, `logrotate`, `rm`, and the entire restricted `systemctl` surface; targets outside the allowlist or inside read-only roots fail with `permission denied` (exit 1); symlinked write targets fail with `symlinks are not supported as write targets`; hard-linked write targets fail with `hard links are not supported as write targets` on Unix; `/dev/null` always accepted; `<>` remains blocked
- ❌ External commands — unavailable through the stock runner because it exposes no external-command executor; allowlisting an unknown name does not make it executable
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

## Intentional Divergences

- **Time reference for `find -mmin`/`-mtime` and `ls -l`**: rshell captures `time.Now()` once at the start of each `Run()` call and shares it across all builtins in that run. Bash evaluates each command against its own invocation time. In practice this only matters for long-running scripts (e.g. `sleep 61; find . -mmin -1`) where the reference time drifts from the actual command start. Short-lived AI agent scripts are unaffected.
- **`jq` number model**: integer literals and integer-only `+`, `-`, `*`, `%`, and exact division remain exact up to 256 bits; larger values are reported and skipped. Decimal/exponent literals, mixed arithmetic, and non-integral division use `float64`, so integer arithmetic does not preserve signed zero. The integer bound prevents unbounded `big.Int` growth under chained multiplication.
- **`jq` floating-point rendering**: decimal/exponent values are stored as `float64`, so output is normalized instead of preserving source spelling. Overflow saturates to `±1.7976931348623157e+308`; underflow becomes `0`.
- **Strict JSON input in `jq`**: leading-zero numbers and raw NUL in strings are rejected instead of reproducing jq's permissive or chunk-dependent behavior. An unpaired low surrogate (`"\udc00"`) decodes to U+FFFD; an unpaired high surrogate is an error.

## Appendix

Formatting: In each category, supported features should be listed first, and the most useful ones first.
