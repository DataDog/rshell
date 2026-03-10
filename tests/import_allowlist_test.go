// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// builtinAllowedSymbols maps each builtin command (keyed by its subdirectory
// name under interp/builtins/) to the set of "importpath.Symbol" references it
// may use. Internal shared packages use "internal/<pkg>" as their key.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside a sandboxed builtin (e.g. pure function, constant, interface,
// no filesystem/network/exec side effects).
//
// To use a new symbol, add it to the relevant command's list with its safety
// justification.
//
// Permanently banned (cannot be added):
//   - reflect  — reflection defeats static safety analysis
//   - unsafe   — bypasses Go's type and memory safety guarantees
//
// All packages not listed here are implicitly banned, including all
// third-party packages and other internal module packages.
var builtinAllowedSymbols = map[string][]string{
	"break": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"cat": {
		"bufio.NewScanner", // line-by-line input reading; no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
	},
	"continue": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"echo": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
		"strings.Builder", // efficient string concatenation; pure in-memory buffer, no I/O.
	},
	"exit": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
		"strconv.Atoi",    // string-to-int conversion; pure function, no I/O.
	},
	"false": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"head": {
		"bufio.NewScanner", // line-by-line input reading; no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"io.Reader",        // interface type; no side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
		"strconv.ParseInt", // string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"internal/loopctl": {
		"strconv.Atoi", // string-to-int conversion; pure function, no I/O.
	},
	"ls": {
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.New",       // creates a simple error value; pure function, no I/O.
		"fmt.Sprintf",      // string formatting; pure function, no I/O.
		"io/fs.FileInfo",   // interface type for file information; no side effects.
		"io/fs.ModeDir",    // file mode bit constant for directories; pure constant.
		"io/fs.ModeNamedPipe", // file mode bit constant for named pipes; pure constant.
		"io/fs.ModeSetgid", // file mode bit constant for setgid; pure constant.
		"io/fs.ModeSetuid", // file mode bit constant for setuid; pure constant.
		"io/fs.ModeSocket", // file mode bit constant for sockets; pure constant.
		"io/fs.ModeSticky", // file mode bit constant for sticky bit; pure constant.
		"io/fs.ModeSymlink", // file mode bit constant for symlinks; pure constant.
		"slices.Reverse",   // reverses a slice in-place; pure function, no I/O.
		"slices.SortFunc",  // sorts a slice with a comparison function; pure function, no I/O.
		"time.Time",        // time value type; pure data, no side effects.
	},
	"tail": {
		"bufio.NewScanner", // line-by-line input reading; no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"errors.New",       // creates a simple error value; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"io.Reader",        // interface type; no side effects.
		"os.FileInfo",      // file metadata interface returned by Stat; no I/O side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
		"strconv.ParseInt", // string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"testcmd": {
		"context.Context",    // deadline/cancellation plumbing; pure interface, no side effects.
		"io/fs.FileInfo",     // interface type for file information; no side effects.
		"io/fs.ModeNamedPipe", // file mode bit constant for named pipes; pure constant.
		"io/fs.ModeSymlink",  // file mode bit constant for symlinks; pure constant.
		"math.MaxInt64",      // integer constant; no side effects.
		"math.MinInt64",      // integer constant; no side effects.
		"strconv.ErrRange",   // sentinel error value for overflow; pure constant.
		"strconv.NumError",   // error type for numeric conversion failures; pure type.
		"strconv.ParseInt",   // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strings.TrimSpace",  // removes leading/trailing whitespace; pure function.
	},
	"true": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"uniq": {
		"bufio.NewScanner", // line-by-line input reading; no write or exec capability.
		"bufio.SplitFunc",  // type for custom scanner split functions; pure type, no I/O.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"io.Reader",        // interface type; no side effects.
		"io.WriteString",   // writes a string to a writer; no filesystem access, delegates to Write.
		"io.Writer",        // interface type for writing; no side effects.
		"math.MaxInt64",    // integer constant; no side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
		"strconv.ErrRange", // sentinel error value for overflow; pure constant.
		"strconv.FormatInt", // int-to-string conversion; pure function, no I/O.
		"strconv.NumError",  // error type for numeric conversion failures; pure type.
		"strconv.ParseInt",  // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strings.HasPrefix", // pure function for prefix matching; no I/O.
	},
	"wc": {
		"context.Context",       // deadline/cancellation plumbing; pure interface, no side effects.
		"io.EOF",                // sentinel error value; pure constant.
		"io.NopCloser",          // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",         // interface type; no side effects.
		"io.Reader",             // interface type; no side effects.
		"os.O_RDONLY",           // read-only file flag constant; cannot open files by itself.
		"strconv.FormatInt",     // int-to-string conversion; pure function, no I/O.
		"unicode.Cc",            // control character category range table; pure data, no I/O.
		"unicode.Cf",            // format character category range table; pure data, no I/O.
		"unicode.Is",            // checks if rune belongs to a range table; pure function, no I/O.
		"unicode.Me",            // enclosing mark category range table; pure data, no I/O.
		"unicode.Mn",            // nonspacing mark category range table; pure data, no I/O.
		"unicode.Range16",       // struct type for 16-bit Unicode ranges; pure data.
		"unicode.Range32",       // struct type for 32-bit Unicode ranges; pure data.
		"unicode.RangeTable",    // struct type for Unicode range tables; pure data.
		"unicode/utf8.DecodeRune", // decodes first UTF-8 rune from a byte slice; pure function, no I/O.
		"unicode/utf8.RuneCount",  // counts UTF-8 runes in a byte slice; pure function, no I/O.
		"unicode/utf8.UTFMax",     // maximum number of bytes in a UTF-8 encoding; constant, no I/O.
		"unicode/utf8.Valid",      // checks if a byte slice is valid UTF-8; pure function, no I/O.
	},
}

// permanentlyBanned lists packages that may never be imported by builtin
// command implementations, regardless of what symbols they export.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
}

// TestBuiltinImportAllowlist enforces per-command symbol-level import
// restrictions on command implementation files in interp/builtins/.
// builtins.go is exempt as the package framework. Every other file's imports
// and pkg.Symbol references must be explicitly listed in the command's entry
// in builtinAllowedSymbols. The test also verifies that no command lists
// symbols it does not actually use.
func TestBuiltinImportAllowlist(t *testing.T) {
	root := repoRoot(t)
	builtinsDir := filepath.Join(root, "interp", "builtins")

	// Collect all .go files in builtin sub-packages, grouped by command key.
	// Command key is the first path component (e.g. "cat", "internal/loopctl").
	type fileEntry struct {
		absPath string
		rel     string // relative to builtinsDir
	}
	commandFiles := make(map[string][]fileEntry)

	err := filepath.Walk(builtinsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == "testutil" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".go") || strings.HasSuffix(info.Name(), "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(builtinsDir, path)
		if rel == "builtins.go" {
			return nil
		}
		if !strings.Contains(rel, string(filepath.Separator)) {
			return nil
		}

		// Determine command key from relative path.
		// e.g. "cat/cat.go" → "cat", "internal/loopctl/loopctl.go" → "internal/loopctl"
		parts := strings.Split(rel, string(filepath.Separator))
		var cmdKey string
		if parts[0] == "internal" && len(parts) >= 3 {
			cmdKey = parts[0] + "/" + parts[1]
		} else {
			cmdKey = parts[0]
		}

		commandFiles[cmdKey] = append(commandFiles[cmdKey], fileEntry{absPath: path, rel: rel})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(commandFiles) == 0 {
		t.Fatal("no command implementation files found in interp/builtins/ sub-packages")
	}

	// Verify every discovered command has an allowlist entry.
	for cmdKey := range commandFiles {
		if _, ok := builtinAllowedSymbols[cmdKey]; !ok {
			t.Errorf("command %q has no entry in builtinAllowedSymbols", cmdKey)
		}
	}

	// Verify every allowlist entry corresponds to a real command.
	for cmdKey := range builtinAllowedSymbols {
		if _, ok := commandFiles[cmdKey]; !ok {
			t.Errorf("builtinAllowedSymbols has entry for %q but no matching command directory exists", cmdKey)
		}
	}

	fset := token.NewFileSet()

	for cmdKey, files := range commandFiles {
		allowedList, ok := builtinAllowedSymbols[cmdKey]
		if !ok {
			continue // already reported above
		}

		// Build per-command lookup sets.
		allowedSymbols := make(map[string]bool, len(allowedList))
		allowedPackages := make(map[string]bool)
		for _, entry := range allowedList {
			dot := strings.LastIndexByte(entry, '.')
			if dot <= 0 {
				t.Fatalf("%s: malformed allowlist entry (no dot): %q", cmdKey, entry)
			}
			allowedSymbols[entry] = true
			allowedPackages[entry[:dot]] = true
		}

		// Track which allowed symbols are actually used.
		usedSymbols := make(map[string]bool)

		for _, fe := range files {
			f, err := parser.ParseFile(fset, fe.absPath, nil, 0)
			if err != nil {
				t.Errorf("%s: parse error: %v", fe.rel, err)
				continue
			}

			localToPath := make(map[string]string)
			for _, imp := range f.Imports {
				importPath := strings.Trim(imp.Path.Value, `"`)

				if reason, banned := permanentlyBanned[importPath]; banned {
					t.Errorf("%s: import of %q is permanently banned (%s)", fe.rel, importPath, reason)
					continue
				}

				if importPath == "github.com/DataDog/rshell/interp/builtins" ||
					strings.HasPrefix(importPath, "github.com/DataDog/rshell/interp/builtins/internal/") {
					continue
				}

				var localName string
				if imp.Name != nil {
					localName = imp.Name.Name
				} else {
					parts := strings.Split(importPath, "/")
					localName = parts[len(parts)-1]
				}

				if localName == "_" || localName == "." {
					t.Errorf("%s: blank/dot import of %q is not allowed", fe.rel, importPath)
					continue
				}

				if !allowedPackages[importPath] {
					t.Errorf("%s: import of %q is not in the %s allowlist", fe.rel, importPath, cmdKey)
					continue
				}

				localToPath[localName] = importPath
			}

			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				ident, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				importPath, ok := localToPath[ident.Name]
				if !ok {
					return true
				}
				key := importPath + "." + sel.Sel.Name
				usedSymbols[key] = true
				if !allowedSymbols[key] {
					pos := fset.Position(sel.Pos())
					t.Errorf("%s:%d: %s is not in the %s allowlist", fe.rel, pos.Line, key, cmdKey)
				}
				return true
			})
		}

		// Verify no unused symbols in the allowlist.
		for _, entry := range allowedList {
			if !usedSymbols[entry] {
				t.Errorf("%s: allowlist contains %q but it is not used by any source file", cmdKey, entry)
			}
		}
	}
}

// TestBuiltinAllowlistSorted verifies that each command's allowlist entries
// are in sorted order, making it easier to maintain and review.
func TestBuiltinAllowlistSorted(t *testing.T) {
	for cmdKey, symbols := range builtinAllowedSymbols {
		if !sort.StringsAreSorted(symbols) {
			t.Errorf("%s: allowlist entries are not sorted", cmdKey)
		}
	}
}
