# Shell Commands

Reference for shell commands.

Each command is documented with:

- **Description** — what the command does and its default behavior.
- **Usage** — synopsis showing arguments and options (omitted for commands that take no arguments).
- **Options** — list of supported flags and options (omitted when none are available).

## `true`

Always exit with status `0` (success). Accepts no arguments or options. Commonly used in shell control flow such as `while true; do ...; done`.

## `false`

Always exit with status `1` (failure). Accepts no arguments or options. Commonly used in shell control flow and conditional expressions.

## `echo`

Print arguments to standard output, separated by spaces, followed by a newline.

**Usage:** `echo [ARG ...]`

## `cat`

Concatenate and print file contents to standard output. With no arguments or when `-` is given, read from standard input.

**Usage:** `cat [FILE ...]`

**Options:**

- `-` — read from stdin

## `head`

Print the first 10 lines of each file. With no file or when file is `-`, read from standard input. When multiple files are given, precede each with a filename header.

**Usage:** `head [OPTION]... [FILE ...]`

**Options:**

- `-n N`, `--lines=N` — output the first N lines (default: 10)
- `-c N`, `--bytes=N` — output the first N bytes instead of lines
- `-q`, `--quiet`, `--silent` — never print filename headers
- `-v`, `--verbose` — always print filename headers
- `-h`, `--help` — print usage and exit

## `exit`

Exit the shell with status N. If N is omitted, the exit status is that of the last command executed.

**Usage:** `exit [N]`

## `break`

Break out of the innermost enclosing `for`, `while`, or `until` loop. If N is specified, break out of N enclosing loops.

**Usage:** `break [N]`

## `continue`

Skip to the next iteration of the innermost enclosing `for`, `while`, or `until` loop. If N is specified, resume at the Nth enclosing loop.

**Usage:** `continue [N]`
