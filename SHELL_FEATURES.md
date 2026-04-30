# Shell Features Reference

This document lists every shell feature and whether it is supported (✅) or blocked (❌).
Blocked features are rejected before execution with exit code 2.

The in-shell `help` command mirrors these feature categories: run `help` for a concise supported/unsupported summary plus commands, or `help <feature|command>` for details about a specific feature or command.

## Builtins

- ✅ `break` — exit the innermost `for` loop
- ✅ `cat [-AbeEnstTuv] [FILE]...` — concatenate files to stdout; supports line numbering, blank squeezing, and non-printing character display
- ✅ `continue` — skip to the next iteration of the innermost `for` loop
- ✅ `cut [-b LIST|-c LIST|-f LIST] [-d DELIM] [-s] [-n] [--complement] [--output-delimiter=STRING] [FILE]...` — remove sections from each line of files
- ✅ `df [-hHkPTialx] [-t TYPE] [-x TYPE] [--total] [--no-sync]` — report file system disk space usage (Linux/macOS only; on Windows `df` exits 1 with `df: not supported on this platform` because mount enumeration goes through `/proc/self/mountinfo` on Linux and `getfsstat(2)` on macOS, neither of which has a Windows equivalent); Linux reads `/proc/self/mountinfo` directly via `os.Open`, bypassing `AllowedPaths`; positional `FILE` operands and `--sync`, `-B`, `--output` are not supported; mount table capped at 100 000 entries
- ✅ `du [-asScSLP0bhkm] [-d N] [--apparent-size|--si] [FILE]...` — estimate file space usage; recursion capped at depth 256 and hardlink-dedup tracking capped at 2²⁰ entries; `--files0-from`, `--exclude-from`/`-X`, `--exclude` are rejected (data-exfiltration / file-driven control); `-B`/`--block-size`, `-t`/`--threshold`, `-x`/`--one-file-system`, `--inodes`, `--time`, `-l`/`--count-links` are not implemented
- ✅ `echo [-neE] [ARG]...` — write arguments to stdout; `-n` suppresses trailing newline, `-e` enables backslash escapes, `-E` disables them (default)
- ✅ `exit [N]` — exit the shell with status N (default 0)
- ✅ `false` — return exit code 1
- ✅ `find [-L] [-P] [PATH...] [EXPRESSION]` — search for files in a directory hierarchy; supports `--help`, `-name`, `-iname`, `-path`, `-ipath`, `-type` (b,c,d,f,l,p,s), `-size`, `-empty`, `-newer`, `-mtime`, `-mmin`, `-perm`, `-maxdepth`, `-mindepth`, `-print`, `-print0`, `-exec CMD {} \;`, `-execdir CMD {} \;`, `-prune`, `-quit`, logical operators (`!`, `-a`, `-o`, `()`); blocks `-delete`, `-regex` for sandbox safety
- ✅ `grep [-EFGivclLnHhoqsxw] [-e PATTERN] [-m NUM] [-A NUM] [-B NUM] [-C NUM] PATTERN [FILE]...` — print lines that match patterns; uses RE2 regex engine (linear-time, no backtracking)
- ✅ `head [-n N|-c N] [-q|-v] [FILE]...` — output the first part of files (default: first 10 lines); `-z`/`--zero-terminated` and `--follow` are rejected
- ✅ `help [--all] [feature|command]` — display rshell features, a concise unsupported-feature summary, and available commands; with a topic, show detailed help for that feature or command
- ✅ `ip [-o|-4|-6|--brief] addr|link [show] [dev IFNAME]` — show network interface addresses and link-layer info (read-only); write ops (`add`, `del`, `flush`, `set`), namespace ops (`netns`, `-n`), and batch mode (`-b`/`-B`/`--force`) are blocked
- ✅ `ip route [show|list]` — show IPv4 routing table (Linux only; reads `/proc/net/route` directly via `os.Open`, bypassing `AllowedPaths`); at most 10 000 entries loaded; lines longer than 1 MiB abort parsing with an error (exit 1)
- ✅ `ip route get ADDRESS` — show the route selected by longest-prefix-match for ADDRESS (Linux only); write ops (`add`, `del`, `flush`, `replace`, `change`, `save`, `restore`) are blocked; `-6` (IPv6 routing) is not supported
- ✅ `sort [-rnhubfds] [-k KEYDEF] [-t SEP] [-c|-C] [FILE]...` — sort lines of text files; `-h`/`--human-numeric-sort` orders by SI suffix (none < K/k < M < G < T < P < E < Z < Y < R < Q) then by numeric value (single-letter suffixes only — `Ki`, `Mi`, etc. are not recognised); `-o`, `--compress-program`, and `-T` are rejected (filesystem write / exec)
- ✅ `ss [-tuaxlans4689Hoehs] [OPTION]...` — display network socket statistics; reads kernel socket state directly via `os.Open` (bypassing `AllowedPaths`) from: Linux: `/proc/net/`; macOS: sysctl; Windows: iphlpapi.dll; `-F`/`--filter` (GTFOBins file-read), `-p`/`--processes` (PID disclosure), `-K`/`--kill`, `-E`/`--events`, and `-N`/`--net` are rejected
- ✅ `ls [-1aAdFhlpRrSt] [--offset N] [--limit N] [FILE]...` — list directory contents; `--offset`/`--limit` are non-standard pagination flags (single-directory only, silently ignored with `-R` or multiple arguments, capped at 1,000 entries per call); offset operates on filesystem order (not sorted order) for O(n) memory
- ✅ `ping [-c N] [-W DURATION] [-i DURATION] [-q] [-4|-6] [-h] HOST` — send ICMP echo requests to a network host and report round-trip statistics; `-f` (flood), `-b` (broadcast), `-s` (packet size), `-I` (interface), `-p` (pattern), and `-R` (record route) are blocked; count/wait/interval are clamped to safe ranges with a warning; multicast, unspecified (`0.0.0.0`/`::`), and broadcast addresses (IPv4 last-octet `.255`) are rejected — note: directed broadcasts on non-standard subnets (e.g. `.127` on a `/25`) are not blocked without subnet-mask knowledge
- ✅ `ps [-e|-A] [-f] [-p PIDLIST]` — report process status; default shows current-session processes; `-e`/`-A` shows all; `-f` adds UID/PPID/STIME columns; `-p` selects by PID list
- ✅ `printf FORMAT [ARGUMENT]...` — format and print data to stdout; supports `%s`, `%b`, `%c`, `%d`, `%i`, `%o`, `%u`, `%x`, `%X`, `%e`, `%E`, `%f`, `%F`, `%g`, `%G`, `%%`; format reuse for excess arguments; `%n` rejected (security risk); `-v` rejected
- ✅ `pwd [-LP]` — print the absolute pathname of the current working directory; `-L` (default) prints the shell's tracked logical path, `-P` resolves all symlinks; `-P` is best-effort within the sandbox (path components above `AllowedPaths` pass through unresolved); `--version` rejected
- ✅ `read [-r] [-p PROMPT] [-d DELIM] [-n N] [-N N] [-t SECS] [NAME...]` — read one delimited chunk from stdin and assign each IFS-split field to a shell variable (defaulting to `REPLY` when no NAME is given); `-n`/`-N` are capped at 1 MiB; non-raw mode treats `\<newline>` as a line continuation (both characters are dropped) and `\<X>` for any other `X` (including the active custom delimiter under `-d`) as a literal `X` with the backslash removed — e.g. `printf 'a\,b,c' | read -d , x` assigns `x="a,b"`; `-p` is suppressed unless stdin is a terminal (matches bash); `-a` (array), `-s` (silent), `-u` (read from FD), `-e` (readline), and `-i` (initial text) are not implemented
- ✅ `sed [-n] [-e SCRIPT] [-E|-r] [SCRIPT] [FILE]...` — stream editor for filtering and transforming text; uses RE2 regex engine; `-i`/`-f` rejected; `e`/`w`/`W`/`r`/`R` commands blocked
- ✅ `strings [-a] [-n MIN] [-t o|d|x] [-o] [-f] [-s SEP] [FILE]...` — print printable character sequences in files (default min length 4); offsets via `-t`/`-o`; filename prefix via `-f`; custom separator via `-s`
- ✅ `tail [-n N|-c N] [-q|-v] [-z] [FILE]...` — output the last part of files (default: last 10 lines); supports `+N` offset mode; `-f`/`--follow` is rejected
- ✅ `test EXPRESSION` / `[ EXPRESSION ]` — evaluate conditional expression (file tests, string/integer comparison, logical operators)
- ✅ `tr [-cdsCt] SET1 [SET2]` — translate, squeeze, and/or delete characters from stdin
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

- ✅ `|` — pipe stdout
- ✅ `<` — input redirection (read-only, within AllowedPaths)
- ✅ `<<DELIM` — heredoc
- ✅ `<<-DELIM` — heredoc with tab stripping
- ✅ `>/dev/null`, `2>/dev/null` — redirect stdout or stderr to /dev/null (output is discarded; only `/dev/null` is allowed as target)
- ✅ `&>/dev/null` — redirect both stdout and stderr to /dev/null
- ✅ `>>/dev/null`, `&>>/dev/null` — append redirect to /dev/null (same effect as truncate)
- ✅ `2>&1`, `>&2` — file descriptor duplication between stdout (1) and stderr (2)
- ❌ `|&` — pipe stdout and stderr (bash extension)
- ❌ `<<<` — herestring (bash extension)
- ❌ `> FILE` — write/truncate to any file other than /dev/null
- ❌ `>> FILE` — append to any file other than /dev/null
- ❌ `&> FILE` — redirect all to any file other than /dev/null
- ❌ `&>> FILE` — append all to any file other than /dev/null
- ❌ `<>` — read-write
- ❌ `<&N` — input file descriptor duplication

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
- ✅ AllowedPaths filesystem sandboxing — restricts all file access (read and write) to specified directories; cross-root symlink fallback is read-only to avoid TOCTOU on writes
- ✅ Whole-run execution timeout — callers can bound a `Run()` call via `context.Context`, `interp.MaxExecutionTime`, or the CLI `--timeout` flag; the deadline applies to the entire script, not each individual command
- ✅ ProcPath — overrides the proc filesystem path used by `ps` (default `/proc`; Linux-only; useful for testing/container environments)
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
