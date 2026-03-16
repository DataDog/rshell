# Shell Features Reference

This document lists every shell feature and whether it is supported (✅) or blocked (❌).
Blocked features are rejected before execution with exit code 2.

## Builtins

- ✅ `break` — exit the innermost `for` loop
- ✅ `cat [-AbeEnstTuv] [FILE]...` — concatenate files to stdout; supports line numbering, blank squeezing, and non-printing character display
- ✅ `continue` — skip to the next iteration of the innermost `for` loop
- ✅ `cut [-b LIST|-c LIST|-f LIST] [-d DELIM] [-s] [-n] [--complement] [--output-delimiter=STRING] [FILE]...` — remove sections from each line of files
- ✅ `echo [-neE] [ARG]...` — write arguments to stdout; `-n` suppresses trailing newline, `-e` enables backslash escapes, `-E` disables them (default)
- ✅ `exit [N]` — exit the shell with status N (default 0)
- ✅ `false` — return exit code 1
- ✅ `find [-L] [PATH...] [EXPRESSION]` — search for files in a directory hierarchy; supports `-name`, `-iname`, `-path`, `-ipath`, `-type`, `-size`, `-empty`, `-newer`, `-mtime`, `-mmin`, `-maxdepth`, `-mindepth`, `-print`, `-print0`, `-prune`, `-exec cmd {} \;`, `-exec cmd {} +`, `-execdir cmd {} \;`, `-execdir cmd {} +`, logical operators (`!`, `-a`, `-o`, `()`); `-exec`/`-execdir` execute through the shell pipeline respecting allowed commands; blocks `-delete`, `-regex`, `-ok` for sandbox safety. **Note:** `-execdir` replaces `{}` with `./basename` but does not change the working directory to the file's parent directory (unlike GNU find); multiple `-exec {} +` or `-execdir {} +` actions in a single expression are not supported
- ✅ `grep [-EFGivclLnHhoqsxw] [-e PATTERN] [-m NUM] [-A NUM] [-B NUM] [-C NUM] PATTERN [FILE]...` — print lines that match patterns; uses RE2 regex engine (linear-time, no backtracking)
- ✅ `head [-n N|-c N] [-q|-v] [FILE]...` — output the first part of files (default: first 10 lines); `-z`/`--zero-terminated` and `--follow` are rejected
- ✅ `sort [-rnubfds] [-k KEYDEF] [-t SEP] [-c|-C] [FILE]...` — sort lines of text files; `-o`, `--compress-program`, and `-T` are rejected (filesystem write / exec)
- ✅ `ls [-1aAdFhlpRrSt] [--offset N] [--limit N] [FILE]...` — list directory contents; `--offset`/`--limit` are non-standard pagination flags (single-directory only, silently ignored with `-R` or multiple arguments, capped at 1,000 entries per call); offset operates on filesystem order (not sorted order) for O(n) memory
- ✅ `printf FORMAT [ARGUMENT]...` — format and print data to stdout; supports `%s`, `%b`, `%c`, `%d`, `%i`, `%o`, `%u`, `%x`, `%X`, `%e`, `%E`, `%f`, `%F`, `%g`, `%G`, `%%`; format reuse for excess arguments; `%n` rejected (security risk); `-v` rejected
- ✅ `sed [-n] [-e SCRIPT] [-E|-r] [SCRIPT] [FILE]...` — stream editor for filtering and transforming text; uses RE2 regex engine; `-i`/`-f` rejected; `e`/`w`/`W`/`r`/`R` commands blocked
- ✅ `strings [-a] [-n MIN] [-t o|d|x] [-o] [-f] [-s SEP] [FILE]...` — print printable character sequences in files (default min length 4); offsets via `-t`/`-o`; filename prefix via `-f`; custom separator via `-s`
- ✅ `tail [-n N|-c N] [-q|-v] [-z] [FILE]...` — output the last part of files (default: last 10 lines); supports `+N` offset mode; `-f`/`--follow` is rejected
- ✅ `test EXPRESSION` / `[ EXPRESSION ]` — evaluate conditional expression (file tests, string/integer comparison, logical operators)
- ✅ `tr [-cdsCt] SET1 [SET2]` — translate, squeeze, and/or delete characters from stdin
- ✅ `true` — return exit code 0
- ✅ `uniq [OPTION]... [INPUT]` — report or omit repeated lines
- ✅ `wc [-l] [-w] [-c] [-m] [-L] [FILE]...` — count lines, words, bytes, characters, or max line length
- ❌ All other commands — return exit code 127 with `<cmd>: not found` unless an ExecHandler is configured

## Variables

- ✅ Assignment: `VAR=value`
- ✅ Expansion: `$VAR`, `${VAR}`
- ✅ `$?` — last exit code (the only supported special variable)
- ✅ Inline assignment: `VAR=value command` (scoped to that command)
- ✅ Command substitution: `$(cmd)`, `` `cmd` `` — captures stdout; trailing newlines stripped; `$(<file)` shortcut reads file directly; output capped at 1 MiB
- ❌ Arithmetic expansion: `$(( expr ))`
- ❌ Array assignment: `arr=(a b c)`, `arr[0]=x`
- ❌ Append assignment: `VAR+=value`
- ❌ Parameter expansion operations: `${#var}`, `${var:-default}`, `${var:=default}`, `${var:?msg}`, `${var:+alt}`, `${var:offset}`, `${var/pattern/repl}`, `${var#prefix}`, `${var%suffix}`, `${!var}`, `${!prefix*}`, case conversion
- ❌ Positional parameters: `$1`–`$9`, `$@`, `$*`, `$#`, `$0`
- ❌ Special variables: `$!`, `$LINENO`

## Control Flow

- ✅ `for VAR in WORDS; do CMDS; done`
- ✅ `&&` — AND list (short-circuit)
- ✅ `||` — OR list (short-circuit)
- ✅ `!` — negation (inverts exit code)
- ✅ `{ CMDS; }` — brace group
- ✅ `;` and newline as command separators
- ✅ `if` / `elif` / `else`
- ✅ Subshells: `( CMDS )` — runs commands in an isolated child environment; variable changes do not propagate to the parent; exit does not terminate the parent
- ❌ `while` / `until`
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

- ✅ AllowedCommands — restricts which commands (builtins or external) may be executed; if not set, no commands are allowed
- ✅ AllowAllCommands — permits any command (testing convenience)
- ✅ AllowedPaths filesystem sandboxing — restricts all file access to specified directories
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
- ❌ No automatic inheritance from the host process
- ❌ `export`, `readonly` are blocked

## Appendix

Formating: In each category, supported features should be listed first, and the most useful ones first.
