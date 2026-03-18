# allowedsymbols

Static enforcement of symbol-level import restrictions for the rshell interpreter.

## Purpose

rshell is a sandboxed shell. Any Go package it imports is a potential escape vector. This package maintains **explicit allowlists** of every `importpath.Symbol` that each subsystem is permitted to use, and enforces those lists via Go AST analysis run as tests.

If a symbol is not on the list, the code cannot compile it through CI — adding new capabilities requires a deliberate, reviewed allowlist entry.

## Permanently Banned Packages

Some packages may never be imported regardless of the symbol, declared in `symbols_common.go`:

## Allowlists

Each subsystem has its own allowlist file:

| File | Governs |
|------|---------|
| `symbols_builtins.go` | `builtins/` — builtin command implementations |
| `symbols_interp.go` | `interp/` — interpreter core |
| `symbols_allowedpaths.go` | `allowedpaths/` — filesystem sandbox |
| `symbols_internal.go` | `builtins/internal/` — shared internal helpers |

### Two-layer system for builtins

Builtins use two complementary lists:

- **`builtinAllowedSymbols`** — global ceiling: every symbol any builtin may use.
- **`builtinPerCommandSymbols`** — per-command sublists: each builtin directory (`cat/`, `grep/`, …) declares only the symbols it actually needs.

Every symbol in a per-command list must be present in the global ceiling. Every symbol in the global ceiling must appear in at least one per-command list. This keeps each builtin's surface area minimal and auditable in isolation.

The same two-layer pattern applies to `builtins/internal/` via `internalAllowedSymbols` and `internalPerPackageSymbols`.

## Safety Legend

Each allowlist entry carries an inline comment prefixed with a safety emoji:

| Emoji | Meaning | Examples |
|-------|---------|---------|
| 🟢 | **Pure** — no side effects (pure functions, constants, types, interfaces) | `strings.Split`, `fmt.Sprintf`, `io.Reader`, AST types |
| 🟠 | **Read-only I/O** — reads from filesystem, OS state, or kernel; or delegates writes to an `io.Writer` | `os.Open`, `os.ReadFile`, `time.Now`, `net.Interfaces`, `syscall.Getsid` |
| 🔴 | **Privileged** — network I/O, unsafe memory, or native code loading | `net.DefaultResolver`, `pro-bing.NewPinger`, `unsafe.Pointer`, `syscall.MustLoadDLL` |

## Enforcement

The tests in this package use Go's `go/parser` and `go/ast` to walk source files and verify:

1. No permanently banned package is imported.
2. Every imported package is in the allowlist.
3. Every `pkg.Symbol` reference is explicitly listed.
4. Every symbol in an allowlist is actually used (no dead entries).
5. Every builtin subdirectory has a per-command entry.

Verification tests additionally inject banned imports or unlisted symbols into a temporary copy of the repo and assert the checker catches them.

## Adding a New Symbol

1. Add a line to the appropriate allowlist (and the per-command sublist if it's a builtin).
2. Prefix the comment with the correct safety emoji.
3. Run `go test ./allowedsymbols/` to verify the entry is valid and used.
