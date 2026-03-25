// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// interpAllowedSymbols lists every "importpath.Symbol" that may be used by
// non-test Go files in interp/. Each entry must be in "importpath.Symbol"
// form, where importpath is the full Go import path.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside the interpreter core.
//
// Internal module imports (github.com/DataDog/rshell/*) are auto-allowed
// and do not appear here.
//
// The permanently banned packages (reflect, unsafe) apply here too.
var interpAllowedSymbols = []string{
	"bytes.Buffer",         // 🟢 in-memory byte buffer; pure data structure, no I/O.
	"context.CancelFunc",   // 🟢 function type returned by WithTimeout/WithCancel; pure function type, no side effects.
	"context.Context",      // 🟢 deadline/cancellation plumbing; pure interface, no side effects.
	"context.WithTimeout",  // 🟢 derives a context with a deadline; needed for execution timeout support.
	"context.WithValue",    // 🟢 derives a context carrying a key-value pair; pure function.
	"errors.As",            // 🟢 error type assertion; pure function, no I/O.
	"fmt.Errorf",           // 🟢 formatted error creation; pure function, no I/O.
	"fmt.Fprintf",          // 🟠 formatted write to an io.Writer; delegates to Write, no filesystem access.
	"fmt.Fprintln",         // 🟠 writes to an io.Writer with newline; delegates to Write, no filesystem access.
	"fmt.Sprintf",          // 🟢 string formatting; pure function, no I/O.
	"io.Closer",            // 🟢 interface type for closing; no side effects.
	"io.Copy",              // 🟠 copies from Reader to Writer; no filesystem access, delegates to Read/Write.
	"io.Discard",           // 🟢 write sink that discards all data; no side effects.
	"io.LimitReader",       // 🟢 wraps a Reader with a byte cap; pure function, no I/O.
	"io.Reader",            // 🟢 interface type for reading; no side effects.
	"io.ReadWriteCloser",   // 🟢 combined interface type; no side effects.
	"io.Writer",            // 🟢 interface type for writing; no side effects.
	"io/fs.DirEntry",       // 🟢 interface type for directory entries; no side effects.
	"io/fs.FileInfo",       // 🟢 interface type for file metadata; no side effects.
	"io/fs.ReadDirFile",    // 🟢 read-only directory handle interface; no write capability.
	"maps.Insert",          // 🟢 inserts all key-value pairs from one map into another; pure function.
	"os.DirEntry",          // 🟢 type alias for fs.DirEntry; no side effects.
	"os.File",              // 🟠 file handle type; interpreter needs file I/O for redirects and pipes.
	"os.FileMode",          // 🟢 file permission bits type; pure type.
	"os.Getwd",             // 🟠 returns current working directory; read-only.
	"os.O_RDONLY",          // 🟢 read-only file flag constant; pure constant.
	"os.PathError",         // 🟢 error type wrapping path and operation; pure type.
	"os.Pipe",              // 🟠 creates an OS pipe pair; needed for shell pipelines.
	"path/filepath.IsAbs",  // 🟢 checks if path is absolute; pure function, no I/O.
	"path/filepath.Join",   // 🟢 joins path elements; pure function, no I/O.
	"runtime.GOOS",         // 🟢 current OS name constant; pure constant, no I/O.
	"strconv.Itoa",         // 🟢 int-to-string conversion; pure function, no I/O.
	"strings.Builder",      // 🟢 efficient string concatenation; pure in-memory buffer, no I/O.
	"strings.ContainsRune", // 🟢 checks if a rune is in a string; pure function, no I/O.
	"strings.Index",        // 🟢 finds substring index; pure function, no I/O.
	"strings.HasPrefix",    // 🟢 pure function for prefix matching; no I/O.
	"strings.HasSuffix",    // 🟢 pure function for suffix matching; no I/O.
	"strings.Split",        // 🟢 splits a string by separator; pure function, no I/O.
	"strings.ToUpper",      // 🟢 converts string to uppercase; pure function, no I/O.
	"strings.TrimLeft",     // 🟢 trims leading characters; pure function, no I/O.
	"sync.Mutex",           // 🟢 mutual exclusion lock; concurrency primitive, no I/O.
	"sync.Once",            // 🟢 ensures a function runs exactly once; concurrency primitive, no I/O.
	"sync.WaitGroup",       // 🟢 waits for goroutines to finish; concurrency primitive, no I/O.
	"sync/atomic.Int64",    // 🟢 atomic int64 counter; concurrency primitive, no I/O.
	"time.Duration",        // 🟢 numeric duration type; pure type, no side effects.
	"time.Now",             // 🟠 returns current time; read-only, no mutation.
	"time.Time",            // 🟢 time value type; pure data, no side effects.

	// --- mvdan.cc/sh/v3/expand --- (shell word expansion library)

	"mvdan.cc/sh/v3/expand.Config",                 // 🟢 configuration for word expansion; pure type.
	"mvdan.cc/sh/v3/expand.Document",               // 🟢 expands a here-document; pure function.
	"mvdan.cc/sh/v3/expand.Environ",                // 🟢 interface for environment variable access; pure interface.
	"mvdan.cc/sh/v3/expand.Fields",                 // 🟢 expands words into fields (splitting, globbing); core expansion.
	"mvdan.cc/sh/v3/expand.KeepValue",              // 🟢 sentinel for variable expansion; pure constant.
	"mvdan.cc/sh/v3/expand.ListEnviron",            // 🟢 converts string slice to Environ; pure function.
	"mvdan.cc/sh/v3/expand.Literal",                // 🟢 expands a word to a single literal string; pure function.
	"mvdan.cc/sh/v3/expand.String",                 // 🟢 expands a word to a string; pure function.
	"mvdan.cc/sh/v3/expand.UnexpectedCommandError", // 🟢 error for unexpected command substitution in restricted mode; pure type.
	"mvdan.cc/sh/v3/expand.UnsetParameterError",    // 🟢 error for unset parameter expansion; pure type.
	"mvdan.cc/sh/v3/expand.Variable",               // 🟢 represents a shell variable; pure type.
	"mvdan.cc/sh/v3/expand.WriteEnviron",           // 🟢 interface for setting environment variables; pure interface.

	// --- mvdan.cc/sh/v3/syntax --- (shell AST types and utilities)

	"mvdan.cc/sh/v3/syntax.AndStmt",      // 🟢 AST node for && operator; pure type.
	"mvdan.cc/sh/v3/syntax.AppAll",       // 🟢 redirect operator constant (&>>); pure constant.
	"mvdan.cc/sh/v3/syntax.AppOut",       // 🟢 redirect operator constant (>>); pure constant.
	"mvdan.cc/sh/v3/syntax.ArithmCmd",    // 🟢 AST node for (( )) arithmetic command; pure type.
	"mvdan.cc/sh/v3/syntax.ArithmExp",    // 🟢 AST node for $(( )) arithmetic expansion; pure type.
	"mvdan.cc/sh/v3/syntax.ArithmExpr",   // 🟢 AST interface for arithmetic expressions; pure interface.
	"mvdan.cc/sh/v3/syntax.Assign",       // 🟢 AST node for variable assignment; pure type.
	"mvdan.cc/sh/v3/syntax.BinaryCmd",    // 🟢 AST node for binary command (&&, ||, |); pure type.
	"mvdan.cc/sh/v3/syntax.Block",        // 🟢 AST node for { } command group; pure type.
	"mvdan.cc/sh/v3/syntax.CallExpr",     // 🟢 AST node for simple command call; pure type.
	"mvdan.cc/sh/v3/syntax.CaseClause",   // 🟢 AST node for case statement; pure type.
	"mvdan.cc/sh/v3/syntax.ClbOut",       // 🟢 redirect operator constant (>|); pure constant.
	"mvdan.cc/sh/v3/syntax.CmdSubst",     // 🟢 AST node for $() command substitution; pure type.
	"mvdan.cc/sh/v3/syntax.Command",      // 🟢 AST interface for all command types; pure interface.
	"mvdan.cc/sh/v3/syntax.CoprocClause", // 🟢 AST node for coproc command; pure type.
	"mvdan.cc/sh/v3/syntax.DashHdoc",     // 🟢 here-doc operator constant (<<-); pure constant.
	"mvdan.cc/sh/v3/syntax.DblQuoted",    // 🟢 AST node for double-quoted string; pure type.
	"mvdan.cc/sh/v3/syntax.DeclClause",   // 🟢 AST node for declare/local/export; pure type.
	"mvdan.cc/sh/v3/syntax.DplIn",        // 🟢 redirect operator constant (<&); pure constant.
	"mvdan.cc/sh/v3/syntax.DplOut",       // 🟢 redirect operator constant (>&); pure constant.
	"mvdan.cc/sh/v3/syntax.ExtGlob",      // 🟢 AST node for extended glob pattern; pure type.
	"mvdan.cc/sh/v3/syntax.File",         // 🟢 AST root node for a parsed shell script; pure type.
	"mvdan.cc/sh/v3/syntax.ForClause",    // 🟢 AST node for for loop; pure type.
	"mvdan.cc/sh/v3/syntax.FuncDecl",     // 🟢 AST node for function declaration; pure type.
	"mvdan.cc/sh/v3/syntax.Hdoc",         // 🟢 here-doc operator constant (<<); pure constant.
	"mvdan.cc/sh/v3/syntax.IfClause",     // 🟢 AST node for if statement; pure type.
	"mvdan.cc/sh/v3/syntax.LetClause",    // 🟢 AST node for let command; pure type.
	"mvdan.cc/sh/v3/syntax.Lit",          // 🟢 AST node for literal string; pure type.
	"mvdan.cc/sh/v3/syntax.Node",         // 🟢 AST interface for all nodes; pure interface.
	"mvdan.cc/sh/v3/syntax.OrStmt",       // 🟢 AST node for || operator; pure type.
	"mvdan.cc/sh/v3/syntax.ParamExp",     // 🟢 AST node for ${} parameter expansion; pure type.
	"mvdan.cc/sh/v3/syntax.Pipe",         // 🟢 AST node for pipeline; pure type.
	"mvdan.cc/sh/v3/syntax.PipeAll",      // 🟢 redirect operator constant (|&); pure constant.
	"mvdan.cc/sh/v3/syntax.Pos",          // 🟢 source position type; pure type.
	"mvdan.cc/sh/v3/syntax.ProcSubst",    // 🟢 AST node for process substitution; pure type.
	"mvdan.cc/sh/v3/syntax.RdrAll",       // 🟢 redirect operator constant (&>); pure constant.
	"mvdan.cc/sh/v3/syntax.RdrIn",        // 🟢 redirect operator constant (<); pure constant.
	"mvdan.cc/sh/v3/syntax.RdrInOut",     // 🟢 redirect operator constant (<>); pure constant.
	"mvdan.cc/sh/v3/syntax.RdrOut",       // 🟢 redirect operator constant (>); pure constant.
	"mvdan.cc/sh/v3/syntax.Redirect",     // 🟢 AST node for I/O redirection; pure type.
	"mvdan.cc/sh/v3/syntax.SglQuoted",    // 🟢 AST node for single-quoted string; pure type.
	"mvdan.cc/sh/v3/syntax.Stmt",         // 🟢 AST node for a complete statement; pure type.
	"mvdan.cc/sh/v3/syntax.Subshell",     // 🟢 AST node for ( ) subshell; pure type.
	"mvdan.cc/sh/v3/syntax.TestClause",   // 🟢 AST node for [[ ]] test command; pure type.
	"mvdan.cc/sh/v3/syntax.TestDecl",     // 🟢 AST node for test declaration; pure type.
	"mvdan.cc/sh/v3/syntax.TimeClause",   // 🟢 AST node for time command; pure type.
	"mvdan.cc/sh/v3/syntax.Walk",         // 🟢 traverses the AST; pure function, no I/O.
	"mvdan.cc/sh/v3/syntax.WhileClause",  // 🟢 AST node for while/until loop; pure type.
	"mvdan.cc/sh/v3/syntax.Word",         // 🟢 AST node for a shell word; pure type.
	"mvdan.cc/sh/v3/syntax.WordHdoc",     // 🟢 redirect operator constant (<<<); pure constant.
	"mvdan.cc/sh/v3/syntax.WordIter",     // 🟢 AST node for word iteration (for-in); pure type.
	"mvdan.cc/sh/v3/syntax.WordPart",     // 🟢 AST interface for word components; pure interface.
}
