# AWK Implementation Plan

This document is the shared plan for implementing the rshell `awk` builtin
across multiple sessions. It complements the external harness in
`tools/awk-harness/` and the repo-local `.claude/skills/implement-awk` skill.

The goal is not to implement every GNU awk feature in one pass. The goal is to
define a useful, safe rshell awk profile, use GNU awk as the compatibility
oracle for features we intentionally support, and reject or defer features that
conflict with rshell's safety model.

## Compatibility Model

- The reference behavior for supported features is pinned GNU awk, as installed
  by `tools/awk-harness/run.sh install-gawk`.
- GNU awk is an oracle for supported behavior, not a requirement to implement
  every extension.
- One True Awk remains a supporting regression suite, but GNU awk wins when
  behavior differs.
- Upstream gawk and One True Awk tests, logs, and generated outputs are external
  data. Do not vendor them or copy their test bodies into this repository.
- Write original rshell tests for every behavior that lands in the builtin.

## Parser Strategy

Build one long-lived parser and extend it over time.

The first implementation should not be a throwaway parser or a collection of
ad hoc string splits. It should be a real, intentionally small awk parser with:

- a hand-written lexer,
- a recursive-descent parser for programs, rules, and statements,
- a Pratt parser for expressions and operator precedence,
- a small AST shared by parser tests and the interpreter,
- clear syntax errors for unsupported grammar.

The parser should initially accept only the Practical awk subset below, but its
shape should be ready for later additions such as arrays, control flow,
functions, `printf`, and string builtins.

Important lexer/parser detail: `/.../` is context-sensitive in awk. It can be a
regular expression literal where an expression or pattern can begin, or division
after an operand. Handle this through parser-aware lexing or parser state rather
than a global "slash always starts regex" rule.

## Phase 1: Practical Awk

This is the recommended first implementation PR, ideally on a branch based on
the harness branch, for example `codex/awk-phase-1`.

### CLI And Program Loading

Support:

- `awk 'program' [FILE]...`
- `awk -f program.awk [FILE]...`
- multiple `-f` program files, read in order through `callCtx.OpenFile`
- `awk -F SEP 'program' [FILE]...`
- `awk -v name=value 'program' [FILE]...`
- `awk --help`
- `-` as an input operand for stdin
- implicit stdin when no input files are given

Reject malformed CLI input with an exit code of 1 and a clear `awk:` error.

### Program Structure

Support:

- `BEGIN { ... }`
- record rules: `pattern { ... }`, `{ ... }`, and pattern-only rules
- `END { ... }`
- multiple rules, executed in source order
- semicolons and newlines as statement separators where awk accepts them

Default actions:

- pattern-only rule: print `$0`
- omitted pattern with action: run action for every record
- omitted action with omitted pattern is invalid

### Records, Fields, And Built-In Variables

Support:

- `$0`
- `$1`, `$2`, ... and expression-based field references such as `$NF`
- `NF`
- `NR`
- `FNR`
- `FILENAME`
- `FS`
- `OFS`
- `ORS`

Initial defaults:

- `FS = " "`
- `OFS = " "`
- `ORS = "\n"`

For Phase 1, prioritize correct field splitting for `FS = " "` and
single-character literal separators supplied by `-F` or `FS = value`. The
single-character separator should be interpreted after awk string escape
decoding, so tab is supported via values such as `-F '\t'` or `FS = "\t"`.
Reject empty separators, multi-character separators, and regex-looking
separators with a clear unsupported-feature error.

Defer regex `FS` support to Phase 2. In awk, `FS` is normally a regular
expression, so values like `-F'[,:]'` or `FS = "[[:space:]]+"` split on regex
matches rather than a literal separator. That is useful, but Phase 1 only needs
the core extractor behavior: default whitespace splitting and one-character
literal separators such as `:`, `,`, and tab. Regex `FS` should land after the
record runtime and regex matcher are stable.

Defer field mutation and `$0` rebuilding to Phase 2. Phase 1 may read `$0`,
`$1`, `$NF`, and `NF`, but assignments such as `$1 = "new"`, `$0 = value`, or
`NF = 2` should be rejected with a clear unsupported-feature error. Correct awk
field mutation requires tracking whether `$0` or the fields are authoritative,
rebuilding `$0` with `OFS`, and recomputing fields when `$0` changes; that state
machine deserves its own focused PR.

### Statements

Support:

- `print`
- expression statements
- scalar assignment: `name = expr`
- scalar compound assignment: `+=`, `-=`, `*=`, `/=`, `%=`
- scalar increment/decrement: `name++`, `name--`, `++name`, `--name`

Defer:

- `printf`
- `if`, `while`, `for`
- `break`, `continue`, `next`, `nextfile`
- arrays and `delete`
- user-defined functions

### Expressions

Support:

- numeric literals
- string literals with common awk escapes
- variable references
- field references
- parenthesized expressions
- unary `+`, unary `-`, and `!`
- arithmetic: `+`, `-`, `*`, `/`, `%`
- comparisons: `==`, `!=`, `<`, `<=`, `>`, `>=`
- boolean operators: `&&`, `||`
- regex match operators: `~`, `!~`
- regex literals in patterns and match expressions
- string concatenation by adjacent expressions, such as `"user=" $1` and
  `$1 ":" $2`

Use awk-style truthiness:

- numeric zero is false,
- empty string is false,
- nonzero numbers and non-empty strings are true.

String concatenation is part of Phase 1 even though it has no explicit operator
in awk. Model adjacency as an implicit binary operator in the Pratt parser, with
tests around ambiguous cases. Without concatenation, common formatting one-liners
feel broken.

### Resource Limits

Use explicit caps so malformed or adversarial awk programs cannot grow parser or
runtime memory without bound:

- `MaxProgramBytes = 256 KiB`: maximum combined source size from inline programs
  and all `-f` files. This is much larger than normal one-liners and small awk
  scripts while keeping parse-time memory predictable.
- `MaxRecordBytes = 1 MiB`: maximum input record size. This matches the existing
  line-oriented builtin pattern and should be enforced before a record is stored
  or split into fields.
- `MaxFields = 16,384`: maximum fields per record. This is generous for logs and
  tabular data but prevents separator-heavy records from creating unbounded field
  slices.
- `MaxVariableBytes = 1 MiB`: maximum aggregate storage for awk scalar variable
  string values. This mirrors rshell's shell variable storage cap. Built-in
  record/field storage is governed by the record and field caps above; later
  array support should share this aggregate variable-storage budget or introduce
  an equally explicit array budget.

Also reject a single scalar assignment whose string value exceeds
`MaxVariableBytes`, even if the aggregate budget is otherwise empty.

### Patterns

Support:

- empty pattern for "all records"
- expression patterns such as `$2 > 10`
- regex patterns such as `/error/`, tested against `$0`
- `BEGIN` and `END`

Defer:

- range patterns: `pat1, pat2`

### Regular Expressions

Use Go's `regexp` package for linear-time matching. Document and test any
intentional difference from GNU awk regex behavior that comes from rshell's
safety or implementation constraints.

Avoid backtracking engines.

Regex field separators are deferred even though regex patterns and `~`/`!~` are
in Phase 1. This keeps the initial field-splitting semantics small and
reviewable.

### ENVIRON

Do not populate `ENVIRON` from the host process environment.

When `ENVIRON` is implemented, populate it from rshell's script-visible
environment only: caller-provided `interp.Env(...)` values, current shell
variables, and interpreter-provided variables such as `PWD`, `IFS`, `OPTIND`,
`RSHELL_VERSION`, and `ALLOWED_PATHS` when present. This matches rshell's
documented environment model: no host environment is inherited by default.

Defer `ENVIRON` until array/indexing support lands. In awk, `ENVIRON` is an
associative array (`ENVIRON["NAME"]`), so supporting it in Phase 1 would require
a one-off special array before the language supports arrays generally. The
reason to defer is implementation shape, not safety; the source of truth is
clear and should be rshell's environment snapshot.

Do not add a `builtins.CallContext` environment iterator in Phase 1 solely for
future `ENVIRON` support. Phase 1 has no feature that needs access to rshell's
environment beyond explicit awk variables supplied by `-v`. When `ENVIRON`
lands with arrays, add the necessary builtin capability then, either as a
read-only environment iterator/snapshot on `CallContext` or an equivalent
narrow API.

## Safety Policy

The builtin must preserve rshell's no-write, no-host-exec safety model.

Reject or defer:

- `system()`
- command pipes: `print | "cmd"` and `"cmd" | getline`
- coprocesses
- output redirection to files: `print > "file"` and `print >> "file"`
- `getline` in all forms for Phase 1
- dynamic extension loading
- network special files
- any feature that executes host commands
- any feature that writes, creates, modifies, or deletes files

All file reads must go through `callCtx.OpenFile`.

## Implementation Files

Expected production files:

- `builtins/awk/awk.go`: command registration, flags, help, top-level runner
- `builtins/awk/lexer.go`: tokenization
- `builtins/awk/parser.go`: program, rules, statements, expression parser
- `builtins/awk/ast.go`: AST node definitions
- `builtins/awk/runtime.go`: records, fields, variables, built-in variables
- `builtins/awk/eval.go`: statement and expression evaluation
- `builtins/awk/regex.go`: regex compilation and matching helpers

Expected integration edits:

- `interp/register_builtins.go`
- `SHELL_FEATURES.md`
- `README.md`, if the builtin list or examples need updating
- `analysis/symbols_builtins.go`, with narrow symbol allowlist entries and
  safety comments for any stdlib symbols used by `builtins/awk`

## Testing Plan

Prefer original scenario tests for externally visible behavior.

Expected test locations:

- `tests/scenarios/cmd/awk/`
- `builtins/tests/awk/`
- parser and runtime unit tests under `builtins/awk/` where scenario tests are
  too coarse

Scenario coverage should include:

- help output
- inline program argument
- `-f` program loading
- `-F`
- `-v`
- stdin and `-`
- multiple input files, including `FNR`, `NR`, and `FILENAME`
- `BEGIN`, main rules, and `END`
- field extraction with `$0`, `$1`, `$NF`, and `NF`
- `print` with `OFS` and `ORS`
- numeric comparisons and arithmetic
- aggregation with `sum += $2`
- regex patterns and `~` / `!~`
- missing files and malformed programs
- rejected unsafe features

For upstream-derived failures, write new original rshell tests. Do not copy
gawk test data, fixture bodies, comments, helper scripts, expected output, or
generated files into this repository.

## Verification Loop

After each coherent implementation step:

```bash
make fmt
go test ./...
```

When `./rshell` is needed and does not exist:

```bash
make build
```

For AWK compatibility work, run focused harness filters first, then expand:

```bash
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk GAWK_TEST_FILTER=<filter> tools/awk-harness/run.sh gawk
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk ONETRUEAWK_SUITE=t tools/awk-harness/run.sh onetrueawk
```

Before considering an implementation PR complete, run the full required awk
sequence from `.claude/skills/implement-awk/SKILL.md`:

```bash
make fmt
go test ./...
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh gawk
RSHELL_BIN=./rshell AWK_UNDER_TEST=tools/awk-harness/rshell-awk tools/awk-harness/run.sh onetrueawk
```

If scenarios or builtin implementations changed, also run the bash comparison
suite when Docker is available:

```bash
RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash -timeout 120s
```

## Later Phases

Phase 2 candidates:

- `printf`
- `if`
- `next`
- range patterns
- regex `FS`
- field assignment and `$0` rebuilding
- common string builtins: `length`, `substr`, `index`, `split`, `tolower`,
  `toupper`, `int`

Phase 3 candidates:

- arrays, including `count[$1]++`
- `ENVIRON`, populated from the rshell environment snapshot
- `in`
- `delete`
- `for (k in array)`
- `for` and `while`
- `break` and `continue`

Phase 4 candidates:

- user-defined functions
- additional POSIX awk builtins
- carefully restricted `getline`, only if a safe design is approved
- safe GNU awk compatibility extensions that do not violate rshell policy

## Open Design Questions

There are no unresolved Phase 1 scope questions at the time of writing. Add new
questions here if implementation uncovers behavior that needs explicit product
or safety direction before proceeding.
