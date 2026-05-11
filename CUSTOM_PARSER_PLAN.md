# Custom Parser & Expander Plan

Goal: replace `mvdan.cc/sh/v3` (`syntax` + `expand`) with an in-tree parser and
word-expander tailored to the rshell-supported subset.

Motivation: **security / supply-chain audit**. `mvdan.cc/sh/v3` is the largest
external dependency on the safety-critical execution path. Owning the parser
and expander lets us:
- shrink the trusted code base (currently ~9.1 k LOC of upstream `syntax/` and
  ~2.3 k LOC of upstream `expand/` are in the trust boundary, versus the ~3 k
  LOC of grammar we actually need);
- reject unsupported constructs at the tokenizer/grammar level instead of after
  a full AST walk, removing a class of "new upstream node type slips past
  validate.go" foot-guns flagged in `interp/validate.go:124-137`;
- pin the accepted dialect to exactly what `SHELL_FEATURES.md` documents.

Compatibility target: **the subset rshell currently runs**. Anything outside
that subset — `case`, `select`, functions, arrays, arithmetic, `[[ ]]`,
`declare`/`export`/`local`, process substitution, tilde expansion, extended
globs, herestrings, non-`/dev/null` redirects, etc. — is rejected at parse
time with a clear diagnostic. Where bash and our subset overlap the behaviour
must be byte-for-byte identical (validated by `tests/scenarios/` with
`RSHELL_BASH_TEST=1`).

---

## 1. Surface area to replace

### 1.1 Parser / AST (`mvdan.cc/sh/v3/syntax`)

Node types referenced by non-test code in `interp/`:

| Category   | Node types we use (and must support)                                                                    |
|-----------|---------------------------------------------------------------------------------------------------------|
| Root       | `File`, `Stmt`, `Pos`, `Command`, `Node` (walker)                                                       |
| Commands   | `CallExpr`, `Block`, `Subshell`, `BinaryCmd` (ops: `AndStmt`, `OrStmt`, `Pipe`), `IfClause`, `ForClause` (`WordIter` only), `WhileClause` |
| Words      | `Word`, `WordPart`, `Lit`, `SglQuoted`, `DblQuoted`, `ParamExp` (simple form only), `CmdSubst`         |
| Assigns    | `Assign` (no `Append`, no `Array`, no `Index`)                                                          |
| Redirects  | `Redirect` with ops: `RdrIn`, `Hdoc`, `DashHdoc`, `RdrOut`, `AppOut`, `ClbOut`, `RdrAll`, `AppAll`, `DplOut` |

Node types we *recognise but reject* (parse must produce a diagnostic, no AST
node needed if rejection is at the lexer/parser level):

`ArithmExp`, `ArithmCmd`, `ProcSubst`, `ExtGlob`, `CaseClause`, `FuncDecl`,
`TestClause` (`[[ ]]`), `DeclClause`, `LetClause`, `TimeClause`,
`CoprocClause`, `TestDecl`, C-style `for (( ... ))`, `select`, `|&`, `<<<`,
`<&N` input fd dup, `<>`, output to non-`/dev/null` paths, `$@`/`$*`/`$1`–`$9`/`$#`/`$0`/`$!`/`$LINENO`,
tilde expansion, `${var:-default}` and friends, `${#var}`, `${var/.../.../}`,
arrays.

Entry point: `syntax.NewParser().Parse(io.Reader, name)` returning `*syntax.File`.
Only one caller (`interp.ParseScript` at `interp/api.go:639-645`) — clean cutover
boundary.

### 1.2 Word expansion (`mvdan.cc/sh/v3/expand`)

Types referenced:

| Symbol                             | Used for                                                  |
|------------------------------------|-----------------------------------------------------------|
| `Config`                           | Per-Runner expand config (`runner_expand.go:25`)          |
| `Fields`                           | Word-list expansion w/ field-splitting + globbing         |
| `Literal`                          | Single-word expansion, no field-splitting                 |
| `Document`                         | Heredoc body expansion                                    |
| `Variable`, `Environ`, `WriteEnviron`, `ListEnviron`, `KeepValue`, `String` | Variable model used in `vars.go`, `runner_exec.go`, `handler.go`, `api.go` |
| `UnsetParameterError`, `UnexpectedCommandError` | Error sentinels in `expandErr` (`runner_expand.go:211-212`) |
| `ReadDir2`                         | Glob directory listing hook (`runner_expand.go:33-45`)    |

Pieces of `expand`'s logic we genuinely depend on:

1. **Variable model**: `Variable{Set, Kind, Str, Local, Exported, ReadOnly, …}`
   + `KeepValue` sentinel for read-modify-write under `Set`. We must reproduce
   the semantics that `vars.go` keys off.
2. **Parameter expansion**: only `$VAR`, `${VAR}`, `$?` need to *succeed*; all
   other forms must error. The validator (`validateParamExp`) already rejects
   the rest at AST level, so the expander just has to look up the variable.
3. **Field splitting**: IFS-driven splitting of unquoted expansions; we set IFS
   ourselves (`api.go:485`).
4. **Globbing**: `*`, `?`, `[abc]`, `[a-z]`, `[!a]` against `ReadDir2`. No
   `globstar`, no extglob.
5. **Quote handling**: single-quoted strings literal; double-quoted strings
   allow `$VAR`/`${VAR}`/`$(...)`/`` `...` ``; backslash escapes inside
   double-quotes for `$ ` " \ \newline`.
6. **Command substitution**: invoke the `CmdSubst` callback (`r.cmdSubst`),
   strip trailing newlines, optionally field-split.

What we do *not* need from `expand`:
- Arithmetic expansion
- Brace expansion (`{a,b}` — already not in our subset, rejected by parser)
- Tilde expansion (blocked)
- Parameter expansion operations (length, default, replace, slice, case mods)
- Indirect expansion (`${!name}`, `${!prefix*}`)
- ProcSubst
- Extended globbing

This dramatically simplifies the expander vs upstream's ~2.3 k LOC.

### 1.3 Tests

About 30 non-prod files import `mvdan.cc/sh/v3` (validation, pentest, fuzz, and
scenario helpers). Most either:
- construct AST fragments to feed `Run` (rewrite to use our AST), or
- import for `syntax.NewParser()` to drive `interp.Run` (rewrite to call
  `interp.ParseScript`).

`analysis/symbols_interp.go` lists every mvdan symbol on the allowlist —
during cutover it shrinks to zero entries.

---

## 2. Design

### 2.1 New packages

- `internal/shsyntax/` — lexer + parser + AST + walker.
- `internal/shexpand/` — word expander, variable/environ model, glob.

`internal/` so the API is private. `interp` is the only intended consumer.

### 2.2 AST shape

Mirror the upstream node types we currently use, with the same field names
where possible, so the existing `interp/` code keeps working with minimal
diffs. For example:

```go
type File struct {
    Name  string
    Stmts []*Stmt
}

type Stmt struct {
    Cmd      Command
    Position Pos
    Redirs   []*Redirect
    Negated  bool
}

type CallExpr struct {
    Assigns []*Assign
    Args    []*Word
}

type Word struct {
    Parts []WordPart
}

// Parts:
type Lit struct { Value string; ValuePos, ValueEnd Pos }
type SglQuoted struct { Value string }
type DblQuoted struct { Parts []WordPart }
type ParamExp struct { Short bool; Param *Lit /* nothing else — strict subset */ }
type CmdSubst struct { Stmts []*Stmt; Backquotes bool }
```

Deliberately keep `Variable` (in `shexpand`) field-compatible with what
`vars.go` accesses (`Set`, `Kind`, `Str`, `Local`, `Exported`, `ReadOnly`,
`Declared()`, `IsSet()`, `Resolve()`).

We do *not* implement nodes for the rejected features. If the parser sees
`case`, it emits a parse error directly — no AST node is materialised. This
fulfils the "reject at grammar level" goal.

### 2.3 Lexer

Single-pass byte-oriented lexer (no full Unicode word-boundary logic — bash
treats UTF-8 outside identifier names as opaque bytes, which we match). Token
kinds we need:

```
WORD LITSTRING SQSTRING DQ_BEGIN DQ_END
LPAREN RPAREN LBRACE RBRACE
SEMICOLON NEWLINE PIPE AND_AND OR_OR AMPERSAND
LESS GREATER DGREATER AND_GREATER AND_DGREATER GREATER_PIPE DLESS DLESS_DASH
DOLLAR DOLLAR_LBRACE DOLLAR_LPAREN BACKQUOTE
IF THEN ELIF ELSE FI FOR IN WHILE UNTIL DO DONE
BANG (negation)
EOF
```

Tokens for rejected forms (`[[`, `((`, `<<<`, `|&`, `<&`, `<>`, `coproc`, etc.)
are detected by the lexer/parser and emit "X is not supported" errors —
matching the strings already produced by `validate.go` so scenario tests don't
need to change.

### 2.4 Parser

Recursive descent. POSIX grammar gives a clean structure; the subset is
small enough to fit in roughly the file shape of `parser.go` (rough estimate
1.5–2 k LOC, vs upstream's 2.9 k):

```
file       := list EOF
list       := pipeline (('&&' | '||' | ';' | '\n')+ pipeline)*
pipeline   := ['!'] command ('|' command)*
command    := simple_cmd | if_cmd | for_cmd | while_cmd | until_cmd
              | brace_group | subshell
simple_cmd := (assign | redirect)* (word (assign | redirect | word)*)?
word       := part+
part       := literal | sq_string | dq_string | param_exp | cmd_subst
```

Heredoc handling preserves the upstream model: heredoc bodies are read after
the line containing the operator is finished. The lexer surfaces a "pending
heredoc" queue.

### 2.5 Expander

Keep the `Config`-driven API so `runner_expand.go`'s integration points
(`fields`, `literal`, `document`, `expandEnv`) change in shape but not in
intent:

```go
package shexpand

type Config struct {
    Env       WriteEnviron
    CmdSubst  func(io.Writer, *shsyntax.CmdSubst) error
    ReadDir2  func(string) ([]fs.DirEntry, error)
}

func Fields(c *Config, words ...*shsyntax.Word) ([]string, error)
func Literal(c *Config, word *shsyntax.Word) (string, error)
func Document(c *Config, word *shsyntax.Word) (string, error)
```

Internals:
- `Variable`, `Environ`, `WriteEnviron`, `ListEnviron` defined here (currently
  imported from `expand`).
- Parameter expansion strictly limited to `$VAR`/`${VAR}`/`$?`. Anything else
  is a programmer error (the parser shouldn't have produced it) — convert to
  a fatal `internalErrorf` to surface the bug rather than silently mishandle.
- Glob via `path/filepath.Match`-style matcher (pre-existing helper) over
  `ReadDir2`. Match upstream's two-phase split-then-glob ordering.
- Field splitting reads `IFS` from `Env`; whitespace-IFS treats runs as one
  delimiter; non-whitespace IFS chars produce empty fields between adjacent
  delimiters (POSIX).

### 2.6 Error sentinels

Replace `expand.UnsetParameterError` and `expand.UnexpectedCommandError` with
local types of the same shape. `runner_expand.go:211-212`'s `errors.As`
checks become `errors.As(err, &shexpand.UnsetParameterError{})`.

---

## 3. Migration plan

The work is staged so each phase leaves the tree green (CI + scenario tests +
`RSHELL_BASH_TEST=1`).

### Phase 0 — Scaffolding (1 PR)

- Create empty `internal/shsyntax` and `internal/shexpand` packages.
- Add `CUSTOM_PARSER_PLAN.md` (this file).
- No code-paths changed yet.

### Phase 1 — Custom AST + Walker (1–2 PRs)

- Define every node type and `Walk` in `internal/shsyntax`.
- No parser yet. We can already write equivalence tests: take a script,
  parse with mvdan, hand-build the equivalent shsyntax tree, assert pretty-
  printed form is identical.
- Goal: shsyntax types are a drop-in replacement *shape-wise* for the
  upstream types we use.

### Phase 2 — Custom expander against mvdan AST (2–3 PRs)

Counter-intuitive ordering — but easier than parser-first because:
- the expander surface is smaller and more cleanly tested in isolation;
- we can run it side-by-side with `expand` via a build tag or env switch and
  diff outputs across the entire scenario suite (the scenarios cover field-
  splitting, quoting, IFS, globbing, `$(...)` thoroughly);
- the expander depends on the AST only for `Word`, `WordPart`, `Lit`,
  `SglQuoted`, `DblQuoted`, `ParamExp`, `CmdSubst` — a tiny adapter from
  mvdan AST to shsyntax AST is enough to drive shexpand from mvdan-parsed
  trees during the bake.

Deliverables:
- `internal/shexpand` with `Fields`, `Literal`, `Document`, `Variable`,
  `Environ`, `WriteEnviron`, `ListEnviron`, glob, IFS-splitter, error
  sentinels.
- A switch (env var or build tag) to route `runner_expand.go` through
  shexpand against the current mvdan-parsed AST via the adapter.
- Diff harness in `tests/`: for every scenario, parse with mvdan, expand
  with both `expand` and `shexpand`, compare field-by-field.

Cutover at end of phase: flip the switch by default, keep the old path
behind the build tag for one release. Remove `mvdan.cc/sh/v3/expand` imports
from production code; tests still import it for cross-checks.

### Phase 3 — Custom parser (3–4 PRs)

Bring up the lexer + parser incrementally:

1. **PR A — lexer + word grammar**: tokens, single/double quotes, comments,
   line continuation, simple words. Parse only `echo foo bar` style scripts.
   Compare AST against mvdan on every scenario tagged "simple_command",
   "comments", "line_continuation", "var_expand".
2. **PR B — control flow**: `if/elif/else/fi`, `while/until/do/done`,
   `for var in …; do …; done`, `{ … }`, `( … )`, `;`, `\n`, `&&`/`||`/`|`,
   `!`. Cover scenarios under `if_clause/`, `for_clause/`,
   `while_clause/`, `until_clause/`, `brace_group/`, `subshell/`,
   `logic_ops/`, `negation/`, `pipe/`.
3. **PR C — redirects + heredocs**: `<`, `<<`, `<<-`, `>`, `>>`, `>|`,
   `&>`, `&>>`, `>&N`. Scenarios under `redirections/`, `heredoc/`,
   `heredoc_dash/`, `blocked_redirects/`, `allowed_redirects/`.
4. **PR D — expansions & assignments**: `$VAR`, `${VAR}`, `$?`,
   `$(...)`, `` `...` ``, `VAR=value`, inline `VAR=value cmd`,
   globbing chars in literals. Scenarios under `command_substitution/`,
   `inline_var/`, `globbing/`, `var_expand/`, `field_splitting/`.

Throughout: every disallowed construct must be detected at parse time with a
diagnostic matching today's `validate.go` messages exactly. We do this by
encoding "rejected" cases directly in the parser (e.g. `case` token →
emit "case statements are not supported") so scenarios under
`shell/case_clause/`, `shell/function/`, `shell/readonly/`,
`shell/blocked_commands/` keep passing byte-for-byte.

Behind a build tag / env var (`RSHELL_PARSER=custom`) during the bake. CI
matrix runs both parsers across the full scenario suite + bash comparison.

### Phase 4 — Cutover (1 PR)

- Flip default to custom parser.
- `interp.ParseScript` no longer constructs `syntax.NewParser()`.
- Delete `validate.go`'s checks that are now redundant with parse-time
  rejection (we keep semantic checks like `$LINENO`-is-blocked since those
  are valid grammar that we reject at the validation layer).
- Update `analysis/symbols_interp.go` — every `mvdan.cc/sh/v3/...` entry is
  removed.
- Update `interp/handler.go`'s `HandlerContext.Pos` type to `shsyntax.Pos`.
- Public-API impact: `HandlerContext.Pos` and `interp.ParseScript`'s return
  type change. This is acceptable — `interp` is a library, but the only
  external Go consumer in-tree is `cmd/rshell/main.go`. Note in the PR if
  there's a downstream importer.

### Phase 5 — Remove mvdan (1 PR)

- `go mod edit -droprequire mvdan.cc/sh/v3`.
- Delete the build-tag-gated old code paths.
- Drop `expand`/`syntax` from test files (the ~30 `_test.go` files that
  import them get rewritten to use `shsyntax`/`shexpand`).
- Confirm `analysis/symbols_interp_verification_test.go` allowlist shows
  zero `mvdan.cc/sh` symbols.

---

## 4. Risks and mitigations

| Risk | Mitigation |
|------|------------|
| Parser bugs cause silent acceptance of constructs we mean to reject (e.g. parser tolerates `case` as a word). | Differential testing in phases 2–3: every scenario parsed by both parsers, ASTs diffed. Also: full scenario suite with `RSHELL_BASH_TEST=1` runs each PR — bash failing while rshell silently runs is a red flag. |
| Field-splitting / quoting corner cases diverge from bash. | The `field_splitting/`, `var_expand/`, `globbing/` scenarios already encode many edge cases; shexpand is validated against bash via the scenario harness. New cases from yash/coreutils test suites can be imported via the `improve-test-coverage` skill. |
| Heredoc handling (delimiter on next line, body queued by lexer) is the trickiest grammar bit. | Implement it last in Phase 3 PR C, with a dedicated `heredoc_*` scenario sweep. Reuse the staging approach from upstream: lexer queues pending heredocs; parser consumes them when a newline terminates the heredoc-introducing line. |
| `ParamExp` we still need (`$VAR`, `${VAR}`) shares grammar with `$(…)`/`${…var:-…}`. Misclassifying these silently allows blocked features. | Parser refuses any `${...}` whose body is not exactly `[A-Za-z_][A-Za-z0-9_]*` or `?`. Anything else (`#`, `!`, `:`, `/`, `%`, `[`, `*`, …) is rejected at parse time with the message currently produced by `validateParamExp`. |
| `Variable.Resolve()` is called in `vars.go:315` — relies on namerefs (`Kind == NameRef`) which we don't support but upstream might produce. | shexpand's `Variable` won't have a `NameRef` kind. `Resolve` becomes a trivial passthrough. Add a regression test that script-set vars never round-trip to NameRef. |
| Scenario coverage gaps: a bash-isomorphic feature we use but don't have a scenario for. | Audit: enumerate every `syntax.X` reference in `interp/` (done above) and confirm at least one scenario exercises it. Fill gaps before Phase 3 starts. |
| Cutover changes public API (`HandlerContext.Pos` type, `ParseScript` return). | Acceptable per project policy (in-tree consumers only). Communicate in the cutover PR. |
| Telemetry/span tags reference position via `syntax.Pos`. | `shsyntax.Pos` mirrors the API (`Line()`, `Col()`, `Offset()`, `IsValid()`). |

---

## 5. Out of scope (explicit non-goals)

- **Bash-compat for blocked features.** We do not parse and reject `case`,
  arrays, arithmetic, `[[ ]]`, etc. — we reject them at the token/grammar
  level. If a future PR wants to *allow* one of those features, that work
  re-opens the grammar; this plan does not pre-build infrastructure for it.
- **POSIX-mode shrink.** Bash-isms we currently support (`!` negation,
  `<<-` tab-stripping heredoc, `$(…)` command substitution) stay.
- **Pretty-printing / formatting.** Upstream's `printer.go` has no consumer
  in `interp/`; we don't reimplement it.
- **`expand.UnexpectedCommandError`-equivalent semantics outside our subset.**
  The error type exists today as a defence-in-depth check
  (`runner_expand.go:212`). We replicate it so cmdSubst-failure flows stay
  identical, but it should never trigger under the custom parser because we
  fully control which AST nodes can appear.

---

## 6. Open questions

1. **Package path**: `internal/shsyntax` vs `interp/syntax`. The latter
   couples the parser to the interpreter; the former allows future reuse
   (e.g. a linter or doc generator). Lean toward `internal/`.
2. **Position model**: upstream stores `Pos` as a packed `uint32` for memory
   efficiency. For a ≤5 MiB script (`MaxScriptBytes`) we can use a simple
   `struct { Offset, Line, Col uint32 }`. Decision: simple struct unless
   benchmarks show memory pressure.
3. **Walker API**: upstream's `Walk(node, func(Node) bool)` is convenient
   for `validate.go`. Keep the same signature so `validateNode` is a near-
   no-op rewrite.
4. **Backticks vs `$(...)`**: upstream parses them identically into
   `CmdSubst{Backquotes: true}`. Replicate.
5. **Bake duration**: how long do we keep both parsers behind the env var
   before Phase 5? Suggest: until one full release cycle with the custom
   parser as default and no regression reports.

---

## 7. Effort estimate

Rough lines-of-code and PR count, based on the upstream non-test source
shrunk by the subset filter:

| Phase                        | New code | Deleted / changed | PRs |
|------------------------------|---------:|------------------:|-----|
| 0 scaffolding                |     ~50 |                 0 | 1   |
| 1 AST + walker               |    ~800 |                 0 | 1   |
| 2 expander + diff harness    |  ~1 200 |               ~50 | 2–3 |
| 3 parser (lexer + grammar)   |  ~2 500 |              ~100 | 4   |
| 4 cutover                    |    ~150 |              ~300 | 1   |
| 5 remove mvdan + test rewrites |    ~50 |              ~600 | 1   |
| **Total**                    |  ~4 800 |            ~1 050 | 10–11 |

The 2 768 existing scenario YAMLs are the regression net — no new scenario
files are mandatory, but expect to add ~30–50 covering parse-error messages
that today come from `validate.go` and will move to the parser.

---

## 8. Success criteria

The work is done when:

1. `go.mod` has no `mvdan.cc/sh/v3` line.
2. All 2 768 scenarios pass under `go test ./tests/`.
3. `RSHELL_BASH_TEST=1 go test ./tests/ -run TestShellScenariosAgainstBash`
   passes with no new `skip_assert_against_bash: true` entries.
4. `analysis/symbols_interp_verification_test.go` reports zero
   `mvdan.cc/sh/...` symbols in the trust boundary.
5. `make fmt && go vet ./... && go test ./...` green on Linux, macOS,
   Windows.
6. CI binary size shrinks (rough expectation: 1–2 MiB smaller).
