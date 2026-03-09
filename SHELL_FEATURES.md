# Shell Features Reference

This document lists every shell feature and whether it is supported (✅) or blocked (❌).
Blocked features are rejected before execution with exit code 2.

## Builtins

| Command | Options | Short description |
| --- | --- | --- |
| `true` | none | Exit with status `0`. |
| `false` | none | Exit with status `1`. |
| `echo [ARG ...]` | none | Print arguments separated by spaces, then newline. |
| `cat [FILE ...]` | `-` (read stdin) | Print files; with no args, read stdin. |
| `head [FILE ...]` | `-n N` (lines), `-c N` (bytes), `-q`/`--quiet`/`--silent` (no headers), `-v` (force headers) | Print first 10 lines of each FILE; with no FILE or `-`, read stdin. |
| `test EXPR` / `[ EXPR ]` | `-e`/`-f`/`-d`/`-s`/`-r`/`-w`/`-x`/`-L` (file tests), `-n`/`-z`/`=`/`!=` (strings), `-eq`/`-ne`/`-lt`/`-gt`/`-le`/`-ge` (integers), `-nt`/`-ot`/`-ef` (file comparison), `!`/`-a`/`-o` (logic) | Evaluate conditional expression; exit 0 (true) or 1 (false). |
| `exit [N]` | `N` (status code) | Exit the shell with `N` (default: last status). |
| `break [N]` | `N` (loop levels) | Break current loop, or `N` enclosing loops. |
| `continue [N]` | `N` (loop levels) | Continue current loop, or `N` enclosing loops. |

All other commands return exit code 127 with `<cmd>: not found` unless an ExecHandler is configured.

## Variables

- ✅ Assignment: `VAR=value`
- ✅ Expansion: `$VAR`, `${VAR}`
- ✅ `$?` — last exit code (the only supported special variable)
- ✅ Inline assignment: `VAR=value command` (scoped to that command)
- ❌ Command substitution: `$(cmd)`, `` `cmd` ``
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
- ❌ `if` / `elif` / `else`
- ❌ `while` / `until`
- ❌ `case`
- ❌ `select`
- ❌ C-style for loop: `for (( i=0; i<N; i++ ))`
- ❌ Functions: `fname() { ... }`
- ❌ Subshells: `( CMDS )`

## Pipes and Redirections

- ✅ `|` — pipe stdout
- ✅ `<` — input redirection (read-only, within AllowedPaths)
- ✅ `<<DELIM` — heredoc
- ✅ `<<-DELIM` — heredoc with tab stripping
- ❌ `|&` — pipe stdout and stderr (bash extension)
- ❌ `<<<` — herestring (bash extension)
- ❌ `>` — write/truncate
- ❌ `>>` — append
- ❌ `&>` — redirect all
- ❌ `&>>` — append all
- ❌ `<>` — read-write
- ❌ `>&N` / `<&N` — file descriptor duplication

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

- ✅ AllowedPaths filesystem sandboxing — restricts all file access to specified directories
- ❌ External commands — blocked by default; requires an ExecHandler to be configured and the binary to be within AllowedPaths
- ❌ Background execution: `cmd &`
- ❌ Coprocesses: `coproc`
- ❌ `time`
- ❌ `[[ ... ]]` test expressions
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
