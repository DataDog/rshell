// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// RunOpts configures a single Python execution.
type RunOpts struct {
	// Source is the Python source code to execute.
	Source string

	// SourceName is the name shown in tracebacks (e.g. "<string>", "script.py").
	SourceName string

	// Ctx is the execution context. Sandbox I/O calls (Open, Stat, ReadDir) use
	// this context so they respect the shell's cancellation deadline. Set by
	// runInternal; callers should leave it nil (it will be populated automatically).
	Ctx context.Context

	// Stdin is Python's sys.stdin reader. If nil, stdin returns EOF immediately.
	Stdin io.Reader

	// Stdout receives all output from Python print() statements.
	Stdout io.Writer

	// Stderr receives Python tracebacks and error messages.
	Stderr io.Writer

	// Open opens a file for reading within the shell's AllowedPaths sandbox.
	Open func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error)

	// Stat returns file metadata within the shell's AllowedPaths sandbox (follows symlinks).
	Stat func(ctx context.Context, path string) (fs.FileInfo, error)

	// ReadDir lists a directory within the shell's AllowedPaths sandbox.
	ReadDir func(ctx context.Context, path string) ([]fs.DirEntry, error)

	// Args are additional arguments appended to sys.argv after SourceName.
	Args []string

	// stdinReader is a single persistent bufio.Reader wrapping Stdin, shared
	// across all input() calls so that read-ahead bytes are not lost between calls.
	// Initialised by runInternal once Stdin has been wrapped in its LimitReader.
	stdinReader *bufio.Reader
}

// ---- Control flow signals ----

// controlKind identifies the kind of non-exception control signal.
type controlKind int

const (
	ctrlReturn controlKind = iota
	ctrlBreak
	ctrlContinue
	ctrlSysExit
	ctrlGeneratorExit
)

// controlSignal is panicked for return/break/continue/sys.exit.
type controlSignal struct {
	kind  controlKind
	value Object // return value or sys.exit code
}

// exceptionSignal is panicked for Python exceptions.
type exceptionSignal struct {
	exc *PyException
}

// ---- Object interface ----

// Object is the universal Python value.
type Object interface {
	pyType() *PyType
	pyRepr() string
	pyStr() string
}

// ---- PyType ----

// PyType represents a Python type object.
type PyType struct {
	Name  string
	Bases []*PyType // for isinstance checks on built-in types
}

func (t *PyType) pyType() *PyType { return typeType }
func (t *PyType) pyRepr() string  { return "<class '" + t.Name + "'>" }
func (t *PyType) pyStr() string   { return t.pyRepr() }

// Built-in type objects.
var (
	typeType          = &PyType{Name: "type"}
	typeNone          = &PyType{Name: "NoneType"}
	typeBool          = &PyType{Name: "bool"}
	typeInt           = &PyType{Name: "int"}
	typeFloat         = &PyType{Name: "float"}
	typeStr           = &PyType{Name: "str"}
	typeBytes         = &PyType{Name: "bytes"}
	typeList          = &PyType{Name: "list"}
	typeTuple         = &PyType{Name: "tuple"}
	typeDict          = &PyType{Name: "dict"}
	typeSet           = &PyType{Name: "set"}
	typeFrozenSet     = &PyType{Name: "frozenset"}
	typeFunction      = &PyType{Name: "function"}
	typeBuiltin       = &PyType{Name: "builtin_function_or_method"}
	typeModule        = &PyType{Name: "module"}
	typeRange         = &PyType{Name: "range"}
	typeSlice         = &PyType{Name: "slice"}
	typeClass         = &PyType{Name: "type"} // user-defined class type
	typeBoundMethod   = &PyType{Name: "method"}
	typeGenerator     = &PyType{Name: "generator"}
	typeMapIter       = &PyType{Name: "map"}
	typeFilterIter    = &PyType{Name: "filter"}
	typeZipIter       = &PyType{Name: "zip"}
	typeEnumerateIter = &PyType{Name: "enumerate"}
	typeReversedIter  = &PyType{Name: "list_reverseiterator"}
	typeFile          = &PyType{Name: "TextIOWrapper"}
)

// ---- Singletons ----

var (
	pyNone  = &PyNone{}
	pyTrue  = &PyBool{v: true}
	pyFalse = &PyBool{v: false}
)

// PyNone is the Python None singleton.
type PyNone struct{}

func (n *PyNone) pyType() *PyType { return typeNone }
func (n *PyNone) pyRepr() string  { return "None" }
func (n *PyNone) pyStr() string   { return "None" }

// PyBool is the Python bool type.
type PyBool struct{ v bool }

func (b *PyBool) pyType() *PyType { return typeBool }
func (b *PyBool) pyRepr() string {
	if b.v {
		return "True"
	}
	return "False"
}
func (b *PyBool) pyStr() string { return b.pyRepr() }

func pyBool(v bool) *PyBool {
	if v {
		return pyTrue
	}
	return pyFalse
}

// ---- PyInt ----

// Small int cache (-5 to 256)
var smallInts [262]*PyInt

func init() {
	for i := 0; i < 262; i++ {
		smallInts[i] = &PyInt{small: int64(i - 5)}
	}
}

// PyInt is the Python int type, backed by int64 or *big.Int for large values.
type PyInt struct {
	small int64    // used when big == nil
	big   *big.Int // non-nil for large values
}

func pyInt(n int64) *PyInt {
	if n >= -5 && n <= 256 {
		return smallInts[n+5]
	}
	return &PyInt{small: n}
}

func pyIntBig(n *big.Int) *PyInt {
	if n.IsInt64() {
		return pyInt(n.Int64())
	}
	return &PyInt{big: new(big.Int).Set(n)}
}

func (i *PyInt) int64() (int64, bool) {
	if i.big == nil {
		return i.small, true
	}
	if i.big.IsInt64() {
		return i.big.Int64(), true
	}
	return 0, false
}

func (i *PyInt) toBigInt() *big.Int {
	if i.big != nil {
		return new(big.Int).Set(i.big)
	}
	return big.NewInt(i.small)
}

func (i *PyInt) pyType() *PyType { return typeInt }
func (i *PyInt) pyRepr() string {
	if i.big != nil {
		return i.big.String()
	}
	return strconv.FormatInt(i.small, 10)
}
func (i *PyInt) pyStr() string { return i.pyRepr() }

// ---- PyFloat ----

// PyFloat is the Python float type.
type PyFloat struct{ v float64 }

func pyFloat(v float64) *PyFloat   { return &PyFloat{v: v} }
func (f *PyFloat) pyType() *PyType { return typeFloat }
func (f *PyFloat) pyRepr() string {
	// Match Python's float repr: use shortest decimal that round-trips
	s := strconv.FormatFloat(f.v, 'g', -1, 64)
	// If there's no decimal point and no exponent, add .0
	if !strings.ContainsAny(s, ".eEn") && s != "inf" && s != "-inf" {
		s += ".0"
	}
	return s
}
func (f *PyFloat) pyStr() string { return f.pyRepr() }

// ---- PyStr ----

// PyStr is the Python str type.
type PyStr struct{ v string }

func pyStr(s string) *PyStr      { return &PyStr{v: s} }
func (s *PyStr) pyType() *PyType { return typeStr }
func (s *PyStr) pyRepr() string {
	// Single-quoted, escaped
	var b strings.Builder
	b.WriteByte('\'')
	for _, r := range s.v {
		switch r {
		case '\'':
			b.WriteString("\\'")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 32 || r == 127 {
				fmt.Fprintf(&b, "\\x%02x", r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}
func (s *PyStr) pyStr() string { return s.v }

// strGetAttr returns a bound method builtin for string attribute access.
func strGetAttr(s *PyStr, name string) (Object, bool) {
	switch name {
	case "upper":
		return makeBuiltin("upper", func(args []Object, kwargs map[string]Object) Object {
			return pyStr(strings.ToUpper(s.v))
		}), true
	case "lower":
		return makeBuiltin("lower", func(args []Object, kwargs map[string]Object) Object {
			return pyStr(strings.ToLower(s.v))
		}), true
	case "strip":
		return makeBuiltin("strip", func(args []Object, kwargs map[string]Object) Object {
			if len(args) == 0 || args[0] == pyNone {
				return pyStr(strings.TrimSpace(s.v))
			}
			chars := mustStr(args[0], "strip")
			return pyStr(strings.Trim(s.v, chars))
		}), true
	case "lstrip":
		return makeBuiltin("lstrip", func(args []Object, kwargs map[string]Object) Object {
			if len(args) == 0 || args[0] == pyNone {
				return pyStr(strings.TrimLeftFunc(s.v, func(r rune) bool { return strings.ContainsRune(" \t\n\r\x0b\x0c", r) }))
			}
			chars := mustStr(args[0], "lstrip")
			return pyStr(strings.TrimLeft(s.v, chars))
		}), true
	case "rstrip":
		return makeBuiltin("rstrip", func(args []Object, kwargs map[string]Object) Object {
			if len(args) == 0 || args[0] == pyNone {
				return pyStr(strings.TrimRightFunc(s.v, func(r rune) bool { return strings.ContainsRune(" \t\n\r\x0b\x0c", r) }))
			}
			chars := mustStr(args[0], "rstrip")
			return pyStr(strings.TrimRight(s.v, chars))
		}), true
	case "split":
		return makeBuiltin("split", func(args []Object, kwargs map[string]Object) Object {
			sep := ""
			maxsplit := -1
			if len(args) > 0 && args[0] != pyNone {
				sep = mustStr(args[0], "split")
			}
			if len(args) > 1 {
				if n, ok := args[1].(*PyInt); ok {
					if v, ok2 := n.int64(); ok2 {
						maxsplit = int(v)
					}
				}
			}
			var parts []string
			if sep == "" {
				// Split on whitespace, removing empty strings
				fields := strings.Fields(s.v)
				if maxsplit >= 0 && len(fields) > maxsplit+1 {
					// rejoin the rest
					parts = fields[:maxsplit]
					rest := strings.Join(fields[maxsplit:], " ")
					parts = append(parts, rest)
				} else {
					parts = fields
				}
			} else {
				if maxsplit < 0 {
					parts = strings.Split(s.v, sep)
				} else {
					parts = strings.SplitN(s.v, sep, maxsplit+1)
				}
			}
			items := make([]Object, len(parts))
			for i, p := range parts {
				items[i] = pyStr(p)
			}
			return pyList(items)
		}), true
	case "rsplit":
		return makeBuiltin("rsplit", func(args []Object, kwargs map[string]Object) Object {
			sep := ""
			maxsplit := -1
			if len(args) > 0 && args[0] != pyNone {
				sep = mustStr(args[0], "rsplit")
			}
			if len(args) > 1 {
				if n, ok := args[1].(*PyInt); ok {
					if v, ok2 := n.int64(); ok2 {
						maxsplit = int(v)
					}
				}
			}
			var parts []string
			if sep == "" {
				fields := strings.Fields(s.v)
				if maxsplit >= 0 && len(fields) > maxsplit+1 {
					split := len(fields) - maxsplit
					rest := strings.Join(fields[:split], " ")
					parts = append([]string{rest}, fields[split:]...)
				} else {
					parts = fields
				}
			} else {
				if maxsplit < 0 {
					parts = strings.Split(s.v, sep)
				} else {
					// SplitN from right
					parts = strRSplitN(s.v, sep, maxsplit+1)
				}
			}
			items := make([]Object, len(parts))
			for i, p := range parts {
				items[i] = pyStr(p)
			}
			return pyList(items)
		}), true
	case "join":
		return makeBuiltin("join", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("join() takes exactly 1 argument")
			}
			items := iterToStrings(args[0], "join")
			return pyStr(strings.Join(items, s.v))
		}), true
	case "startswith":
		return makeBuiltin("startswith", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("startswith() requires at least 1 argument")
			}
			prefix := mustStr(args[0], "startswith")
			return pyBool(strings.HasPrefix(s.v, prefix))
		}), true
	case "endswith":
		return makeBuiltin("endswith", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("endswith() requires at least 1 argument")
			}
			suffix := mustStr(args[0], "endswith")
			return pyBool(strings.HasSuffix(s.v, suffix))
		}), true
	case "replace":
		return makeBuiltin("replace", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 2 {
				raiseTypeError("replace() requires at least 2 arguments")
			}
			old := mustStr(args[0], "replace")
			new_ := mustStr(args[1], "replace")
			n := -1
			if len(args) > 2 {
				if v, ok := args[2].(*PyInt); ok {
					if i, ok2 := v.int64(); ok2 {
						n = int(i)
					}
				}
			}
			return pyStr(strings.Replace(s.v, old, new_, n))
		}), true
	case "find":
		return makeBuiltin("find", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("find() requires at least 1 argument")
			}
			sub := mustStr(args[0], "find")
			idx := strings.Index(s.v, sub)
			return pyInt(int64(idx))
		}), true
	case "rfind":
		return makeBuiltin("rfind", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("rfind() requires at least 1 argument")
			}
			sub := mustStr(args[0], "rfind")
			idx := strings.LastIndex(s.v, sub)
			return pyInt(int64(idx))
		}), true
	case "index":
		return makeBuiltin("index", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("index() requires at least 1 argument")
			}
			sub := mustStr(args[0], "index")
			idx := strings.Index(s.v, sub)
			if idx < 0 {
				raiseValueError("substring not found")
			}
			return pyInt(int64(idx))
		}), true
	case "rindex":
		return makeBuiltin("rindex", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("rindex() requires at least 1 argument")
			}
			sub := mustStr(args[0], "rindex")
			idx := strings.LastIndex(s.v, sub)
			if idx < 0 {
				raiseValueError("substring not found")
			}
			return pyInt(int64(idx))
		}), true
	case "count":
		return makeBuiltin("count", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("count() requires at least 1 argument")
			}
			sub := mustStr(args[0], "count")
			return pyInt(int64(strings.Count(s.v, sub)))
		}), true
	case "encode":
		return makeBuiltin("encode", func(args []Object, kwargs map[string]Object) Object {
			// Default: UTF-8
			return pyBytes([]byte(s.v))
		}), true
	case "isdigit":
		return makeBuiltin("isdigit", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			for _, r := range s.v {
				if r < '0' || r > '9' {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	case "isalpha":
		return makeBuiltin("isalpha", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			for _, r := range s.v {
				if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	case "isalnum":
		return makeBuiltin("isalnum", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			for _, r := range s.v {
				if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	case "isspace":
		return makeBuiltin("isspace", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			for _, r := range s.v {
				if !strings.ContainsRune(" \t\n\r\x0b\x0c", r) {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	case "isupper":
		return makeBuiltin("isupper", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			hasUpper := false
			for _, r := range s.v {
				if r >= 'a' && r <= 'z' {
					return pyFalse
				}
				if r >= 'A' && r <= 'Z' {
					hasUpper = true
				}
			}
			return pyBool(hasUpper)
		}), true
	case "islower":
		return makeBuiltin("islower", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyFalse
			}
			hasLower := false
			for _, r := range s.v {
				if r >= 'A' && r <= 'Z' {
					return pyFalse
				}
				if r >= 'a' && r <= 'z' {
					hasLower = true
				}
			}
			return pyBool(hasLower)
		}), true
	case "zfill":
		return makeBuiltin("zfill", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("zfill() requires 1 argument")
			}
			w := int(toIntVal(args[0]))
			return pyStr(strZfill(s.v, w))
		}), true
	case "center":
		return makeBuiltin("center", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("center() requires 1 argument")
			}
			w := int(toIntVal(args[0]))
			fill := " "
			if len(args) > 1 {
				fill = mustStr(args[1], "center")
			}
			return pyStr(strCenter(s.v, w, fill))
		}), true
	case "ljust":
		return makeBuiltin("ljust", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("ljust() requires 1 argument")
			}
			w := int(toIntVal(args[0]))
			fill := " "
			if len(args) > 1 {
				fill = mustStr(args[1], "ljust")
			}
			return pyStr(strLjust(s.v, w, fill))
		}), true
	case "rjust":
		return makeBuiltin("rjust", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("rjust() requires 1 argument")
			}
			w := int(toIntVal(args[0]))
			fill := " "
			if len(args) > 1 {
				fill = mustStr(args[1], "rjust")
			}
			return pyStr(strRjust(s.v, w, fill))
		}), true
	case "title":
		return makeBuiltin("title", func(args []Object, kwargs map[string]Object) Object {
			return pyStr(strings.Title(s.v)) //nolint:staticcheck
		}), true
	case "capitalize":
		return makeBuiltin("capitalize", func(args []Object, kwargs map[string]Object) Object {
			if len(s.v) == 0 {
				return pyStr("")
			}
			return pyStr(strings.ToUpper(s.v[:1]) + strings.ToLower(s.v[1:]))
		}), true
	case "format":
		return makeBuiltin("format", func(args []Object, kwargs map[string]Object) Object {
			return pyStr(strFormat(s.v, args, kwargs))
		}), true
	case "format_map":
		return makeBuiltin("format_map", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("format_map() requires exactly 1 argument")
			}
			d, ok := args[0].(*PyDict)
			if !ok {
				raiseTypeError("format_map() argument must be a dict")
			}
			mapping := make(map[string]Object)
			for i, k := range d.keys {
				if ks, ok2 := k.(*PyStr); ok2 {
					mapping[ks.v] = d.vals[i]
				}
			}
			return pyStr(strFormat(s.v, nil, mapping))
		}), true
	case "expandtabs":
		return makeBuiltin("expandtabs", func(args []Object, kwargs map[string]Object) Object {
			tabsize := 8
			if len(args) > 0 {
				tabsize = int(toIntVal(args[0]))
			}
			return pyStr(strings.ReplaceAll(s.v, "\t", strings.Repeat(" ", tabsize)))
		}), true
	case "splitlines":
		return makeBuiltin("splitlines", func(args []Object, kwargs map[string]Object) Object {
			keepends := false
			if len(args) > 0 {
				keepends = pyTruth(args[0])
			}
			lines := splitlines(s.v, keepends)
			items := make([]Object, len(lines))
			for i, l := range lines {
				items[i] = pyStr(l)
			}
			return pyList(items)
		}), true
	case "partition":
		return makeBuiltin("partition", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("partition() requires 1 argument")
			}
			sep := mustStr(args[0], "partition")
			idx := strings.Index(s.v, sep)
			if idx < 0 {
				return pyTuple([]Object{pyStr(s.v), pyStr(""), pyStr("")})
			}
			return pyTuple([]Object{pyStr(s.v[:idx]), pyStr(sep), pyStr(s.v[idx+len(sep):])})
		}), true
	case "rpartition":
		return makeBuiltin("rpartition", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("rpartition() requires 1 argument")
			}
			sep := mustStr(args[0], "rpartition")
			idx := strings.LastIndex(s.v, sep)
			if idx < 0 {
				return pyTuple([]Object{pyStr(""), pyStr(""), pyStr(s.v)})
			}
			return pyTuple([]Object{pyStr(s.v[:idx]), pyStr(sep), pyStr(s.v[idx+len(sep):])})
		}), true
	case "translate":
		return makeBuiltin("translate", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("translate() requires 1 argument")
			}
			// table is a dict mapping ordinals to ordinals/strings/None
			table, ok := args[0].(*PyDict)
			if !ok {
				raiseTypeError("translate() argument must be a dict")
			}
			var b strings.Builder
			for _, r := range s.v {
				key := pyInt(int64(r))
				k, _ := hashKey(key)
				if idx, found := table.index[k]; found {
					v := table.vals[idx]
					if v == pyNone {
						// delete
					} else if vs, ok2 := v.(*PyStr); ok2 {
						b.WriteString(vs.v)
					} else if vi, ok2 := v.(*PyInt); ok2 {
						if n, ok3 := vi.int64(); ok3 {
							b.WriteRune(rune(n))
						}
					}
				} else {
					b.WriteRune(r)
				}
			}
			return pyStr(b.String())
		}), true
	}
	return nil, false
}

// iterToStrings collects strings from an iterable for join().
func iterToStrings(obj Object, fnName string) []string {
	items := collectIterable(obj)
	result := make([]string, len(items))
	for i, item := range items {
		s, ok := item.(*PyStr)
		if !ok {
			raiseTypeError("sequence item %d: expected str instance, %s found", i, item.pyType().Name)
		}
		result[i] = s.v
	}
	return result
}

// strRSplitN splits s by sep from the right, at most n parts from the right.
func strRSplitN(s, sep string, n int) []string {
	if n == 1 {
		return []string{s}
	}
	parts := []string{}
	for len(parts) < n-1 {
		idx := strings.LastIndex(s, sep)
		if idx < 0 {
			break
		}
		parts = append([]string{s[idx+len(sep):]}, parts...)
		s = s[:idx]
	}
	return append([]string{s}, parts...)
}

// splitlines splits a string by line endings.
func splitlines(s string, keepends bool) []string {
	var lines []string
	for len(s) > 0 {
		idx := strings.IndexAny(s, "\n\r\x0b\x0c\x1c\x1d\x1e\x85")
		if idx < 0 {
			lines = append(lines, s)
			break
		}
		end := idx + 1
		if s[idx] == '\r' && idx+1 < len(s) && s[idx+1] == '\n' {
			end = idx + 2
		}
		if keepends {
			lines = append(lines, s[:end])
		} else {
			lines = append(lines, s[:idx])
		}
		s = s[end:]
	}
	return lines
}

func strZfill(s string, w int) string {
	runes := []rune(s)
	pad := w - len(runes)
	if pad <= 0 {
		return s
	}
	sign := ""
	if len(runes) > 0 && (runes[0] == '+' || runes[0] == '-') {
		sign = string(runes[0])
		runes = runes[1:]
	}
	return sign + strings.Repeat("0", pad) + string(runes)
}

func strCenter(s string, w int, fill string) string {
	runes := []rune(s)
	pad := w - len(runes)
	if pad <= 0 {
		return s
	}
	fillRune := []rune(fill)
	if len(fillRune) == 0 {
		return s
	}
	leftPad := pad / 2
	rightPad := pad - leftPad
	return strings.Repeat(fill, leftPad/len(fillRune)+1)[:leftPad] + s + strings.Repeat(fill, rightPad/len(fillRune)+1)[:rightPad]
}

func strLjust(s string, w int, fill string) string {
	runes := []rune(s)
	pad := w - len(runes)
	if pad <= 0 {
		return s
	}
	return s + strings.Repeat(fill, pad)
}

func strRjust(s string, w int, fill string) string {
	runes := []rune(s)
	pad := w - len(runes)
	if pad <= 0 {
		return s
	}
	return strings.Repeat(fill, pad) + s
}

// strFormat implements str.format().
func strFormat(tmpl string, args []Object, kwargs map[string]Object) string {
	var b strings.Builder
	autoIdx := 0
	i := 0
	for i < len(tmpl) {
		if tmpl[i] == '{' {
			if i+1 < len(tmpl) && tmpl[i+1] == '{' {
				b.WriteByte('{')
				i += 2
				continue
			}
			end := strings.Index(tmpl[i:], "}")
			if end < 0 {
				b.WriteByte('{')
				i++
				continue
			}
			field := tmpl[i+1 : i+end]
			i += end + 1

			// Parse field: [field_name][!conversion][:format_spec]
			conv := ""
			spec := ""
			if ci := strings.Index(field, "!"); ci >= 0 {
				conv = field[ci+1:]
				field = field[:ci]
				if ci2 := strings.Index(conv, ":"); ci2 >= 0 {
					spec = conv[ci2+1:]
					conv = conv[:ci2]
				}
			} else if ci := strings.Index(field, ":"); ci >= 0 {
				spec = field[ci+1:]
				field = field[:ci]
			}

			var val Object
			if field == "" {
				// Auto-numbered
				if autoIdx < len(args) {
					val = args[autoIdx]
				} else {
					val = pyNone
				}
				autoIdx++
			} else if n, err := strconv.Atoi(field); err == nil {
				if n < len(args) {
					val = args[n]
				} else {
					val = pyNone
				}
			} else {
				// Named
				if kwargs != nil {
					val = kwargs[field]
				}
				if val == nil {
					val = pyNone
				}
			}

			// Apply conversion
			var s string
			switch conv {
			case "r":
				s = val.pyRepr()
			case "s":
				s = val.pyStr()
			case "a":
				s = val.pyRepr() // simplified
			default:
				s = val.pyStr()
			}

			// Apply format spec
			if spec != "" {
				s = applyFormatSpec(s, val, spec)
			}
			b.WriteString(s)
		} else if tmpl[i] == '}' && i+1 < len(tmpl) && tmpl[i+1] == '}' {
			b.WriteByte('}')
			i += 2
		} else {
			b.WriteByte(tmpl[i])
			i++
		}
	}
	return b.String()
}

func applyFormatSpec(s string, val Object, spec string) string {
	if spec == "" {
		return s
	}
	// Very simple format spec: just handle d, f, s, r, x, o, b, e, g
	switch spec[len(spec)-1] {
	case 'd':
		if n, ok := val.(*PyInt); ok {
			v, _ := n.int64()
			s = strconv.FormatInt(v, 10)
		}
	case 'f':
		var f float64
		switch v := val.(type) {
		case *PyFloat:
			f = v.v
		case *PyInt:
			if n, ok := v.int64(); ok {
				f = float64(n)
			}
		}
		prec := 6
		if len(spec) > 1 {
			if dotIdx := strings.Index(spec, "."); dotIdx >= 0 {
				p, err := strconv.Atoi(spec[dotIdx+1 : len(spec)-1])
				if err == nil {
					prec = p
				}
			}
		}
		s = strconv.FormatFloat(f, 'f', prec, 64)
	case 'x':
		if n, ok := val.(*PyInt); ok {
			v, _ := n.int64()
			s = strconv.FormatInt(v, 16)
		}
	case 'o':
		if n, ok := val.(*PyInt); ok {
			v, _ := n.int64()
			s = strconv.FormatInt(v, 8)
		}
	case 'b':
		if n, ok := val.(*PyInt); ok {
			v, _ := n.int64()
			s = strconv.FormatInt(v, 2)
		}
	}
	return s
}

// strPercent implements % formatting.
func strPercent(tmpl string, args Object) string {
	// Collect args into a slice
	var argList []Object
	switch v := args.(type) {
	case *PyTuple:
		argList = v.items
	default:
		argList = []Object{args}
	}

	var b strings.Builder
	argIdx := 0
	i := 0
	for i < len(tmpl) {
		if tmpl[i] != '%' {
			b.WriteByte(tmpl[i])
			i++
			continue
		}
		i++
		if i >= len(tmpl) {
			break
		}
		if tmpl[i] == '%' {
			b.WriteByte('%')
			i++
			continue
		}
		// Parse optional flags
		for i < len(tmpl) && (tmpl[i] == '-' || tmpl[i] == '+' || tmpl[i] == ' ' || tmpl[i] == '0' || tmpl[i] == '#') {
			i++
		}
		// Parse optional width (integer)
		for i < len(tmpl) && tmpl[i] >= '0' && tmpl[i] <= '9' {
			i++
		}
		// Parse optional precision: .digits
		prec := -1
		if i < len(tmpl) && tmpl[i] == '.' {
			i++
			prec = 0
			for i < len(tmpl) && tmpl[i] >= '0' && tmpl[i] <= '9' {
				prec = prec*10 + int(tmpl[i]-'0')
				i++
			}
		}
		if i >= len(tmpl) {
			break
		}
		// Consume arg only once per format spec.
		var arg Object
		if argIdx < len(argList) {
			arg = argList[argIdx]
			argIdx++
		} else {
			arg = pyNone
		}
		switch tmpl[i] {
		case 's':
			b.WriteString(arg.pyStr())
		case 'r':
			b.WriteString(arg.pyRepr())
		case 'd':
			switch v := arg.(type) {
			case *PyInt:
				n, _ := v.int64()
				b.WriteString(strconv.FormatInt(n, 10))
			case *PyFloat:
				b.WriteString(strconv.FormatInt(int64(v.v), 10))
			case *PyBool:
				if v.v {
					b.WriteString("1")
				} else {
					b.WriteString("0")
				}
			default:
				b.WriteString("0")
			}
		case 'f':
			var f float64
			switch v := arg.(type) {
			case *PyFloat:
				f = v.v
			case *PyInt:
				n, _ := v.int64()
				f = float64(n)
			}
			digits := 6
			if prec >= 0 {
				digits = prec
			}
			b.WriteString(strconv.FormatFloat(f, 'f', digits, 64))
		case 'e', 'E':
			var f float64
			switch v := arg.(type) {
			case *PyFloat:
				f = v.v
			case *PyInt:
				n, _ := v.int64()
				f = float64(n)
			}
			digits := 6
			if prec >= 0 {
				digits = prec
			}
			s := strconv.FormatFloat(f, 'e', digits, 64)
			if tmpl[i] == 'E' {
				s = strings.ToUpper(s)
			}
			b.WriteString(s)
		case 'g', 'G':
			var f float64
			switch v := arg.(type) {
			case *PyFloat:
				f = v.v
			case *PyInt:
				n, _ := v.int64()
				f = float64(n)
			}
			digits := -1
			if prec >= 0 {
				digits = prec
			}
			s := strconv.FormatFloat(f, 'g', digits, 64)
			if tmpl[i] == 'G' {
				s = strings.ToUpper(s)
			}
			b.WriteString(s)
		case 'x':
			if v, ok := arg.(*PyInt); ok {
				n, _ := v.int64()
				b.WriteString(strconv.FormatInt(n, 16))
			}
		case 'X':
			if v, ok := arg.(*PyInt); ok {
				n, _ := v.int64()
				b.WriteString(strings.ToUpper(strconv.FormatInt(n, 16)))
			}
		case 'o':
			if v, ok := arg.(*PyInt); ok {
				n, _ := v.int64()
				b.WriteString(strconv.FormatInt(n, 8))
			}
		case 'b':
			if v, ok := arg.(*PyInt); ok {
				n, _ := v.int64()
				b.WriteString(strconv.FormatInt(n, 2))
			}
		case 'c':
			switch v := arg.(type) {
			case *PyInt:
				n, _ := v.int64()
				b.WriteRune(rune(n))
			case *PyStr:
				if len(v.v) > 0 {
					r, _ := utf8.DecodeRuneInString(v.v)
					b.WriteRune(r)
				}
			}
		default:
			b.WriteByte('%')
			b.WriteByte(tmpl[i])
		}
		i++
	}
	return b.String()
}

// ---- PyBytes ----

// PyBytes is the Python bytes type.
type PyBytes struct{ v []byte }

func pyBytes(b []byte) *PyBytes    { return &PyBytes{v: b} }
func (b *PyBytes) pyType() *PyType { return typeBytes }
func (b *PyBytes) pyRepr() string {
	var sb strings.Builder
	sb.WriteString("b'")
	for _, c := range b.v {
		switch c {
		case '\'':
			sb.WriteString("\\'")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			if c < 32 || c >= 127 {
				fmt.Fprintf(&sb, "\\x%02x", c)
			} else {
				sb.WriteByte(c)
			}
		}
	}
	sb.WriteByte('\'')
	return sb.String()
}
func (b *PyBytes) pyStr() string { return b.pyRepr() }

func bytesGetAttr(b *PyBytes, name string) (Object, bool) {
	switch name {
	case "hex":
		return makeBuiltin("hex", func(args []Object, kwargs map[string]Object) Object {
			result := make([]byte, len(b.v)*2)
			const hexChars = "0123456789abcdef"
			for i, c := range b.v {
				result[i*2] = hexChars[c>>4]
				result[i*2+1] = hexChars[c&0xf]
			}
			return pyStr(string(result))
		}), true
	case "decode":
		return makeBuiltin("decode", func(args []Object, kwargs map[string]Object) Object {
			// Default: UTF-8
			s := string(b.v)
			if !utf8.ValidString(s) {
				panic(exceptionSignal{exc: newExceptionf(ExcUnicodeDecodeError, "invalid utf-8 sequence")})
			}
			return pyStr(s)
		}), true
	}
	return nil, false
}

// ---- PyList ----

// PyList is the Python list type.
type PyList struct{ items []Object }

func pyList(items []Object) *PyList {
	if items == nil {
		items = []Object{}
	}
	return &PyList{items: items}
}
func (l *PyList) pyType() *PyType { return typeList }
func (l *PyList) pyRepr() string {
	parts := make([]string, len(l.items))
	for i, item := range l.items {
		parts[i] = item.pyRepr()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
func (l *PyList) pyStr() string { return l.pyRepr() }

func listGetAttr(l *PyList, name string) (Object, bool) {
	switch name {
	case "append":
		return makeBuiltin("append", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("append() takes exactly 1 argument")
			}
			l.items = append(l.items, args[0])
			return pyNone
		}), true
	case "extend":
		return makeBuiltin("extend", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("extend() takes exactly 1 argument")
			}
			items := collectIterable(args[0])
			l.items = append(l.items, items...)
			return pyNone
		}), true
	case "insert":
		return makeBuiltin("insert", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 2 {
				raiseTypeError("insert() takes exactly 2 arguments")
			}
			idx := int(toIntVal(args[0]))
			if idx < 0 {
				idx = len(l.items) + idx
			}
			if idx < 0 {
				idx = 0
			}
			if idx > len(l.items) {
				idx = len(l.items)
			}
			l.items = append(l.items, nil)
			copy(l.items[idx+1:], l.items[idx:])
			l.items[idx] = args[1]
			return pyNone
		}), true
	case "remove":
		return makeBuiltin("remove", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("remove() takes exactly 1 argument")
			}
			for i, item := range l.items {
				if pyEq(item, args[0]) {
					l.items = append(l.items[:i], l.items[i+1:]...)
					return pyNone
				}
			}
			raiseValueError("list.remove(x): x not in list")
			return nil
		}), true
	case "pop":
		return makeBuiltin("pop", func(args []Object, kwargs map[string]Object) Object {
			if len(l.items) == 0 {
				raiseIndexError("pop from empty list")
			}
			idx := len(l.items) - 1
			if len(args) > 0 {
				idx = int(toIntVal(args[0]))
			}
			if idx < 0 {
				idx = len(l.items) + idx
			}
			if idx < 0 || idx >= len(l.items) {
				raiseIndexError("pop index out of range")
			}
			val := l.items[idx]
			l.items = append(l.items[:idx], l.items[idx+1:]...)
			return val
		}), true
	case "index":
		return makeBuiltin("index", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("index() requires at least 1 argument")
			}
			for i, item := range l.items {
				if pyEq(item, args[0]) {
					return pyInt(int64(i))
				}
			}
			raiseValueError("%s is not in list", args[0].pyRepr())
			return nil
		}), true
	case "count":
		return makeBuiltin("count", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("count() takes exactly 1 argument")
			}
			n := 0
			for _, item := range l.items {
				if pyEq(item, args[0]) {
					n++
				}
			}
			return pyInt(int64(n))
		}), true
	case "sort":
		return makeBuiltin("sort", func(args []Object, kwargs map[string]Object) Object {
			reverse := false
			var keyFn Object
			if v, ok := kwargs["reverse"]; ok {
				reverse = pyTruth(v)
			}
			if v, ok := kwargs["key"]; ok && v != pyNone {
				keyFn = v
			}
			sortList(l.items, keyFn, reverse)
			return pyNone
		}), true
	case "reverse":
		return makeBuiltin("reverse", func(args []Object, kwargs map[string]Object) Object {
			for i, j := 0, len(l.items)-1; i < j; i, j = i+1, j-1 {
				l.items[i], l.items[j] = l.items[j], l.items[i]
			}
			return pyNone
		}), true
	case "copy":
		return makeBuiltin("copy", func(args []Object, kwargs map[string]Object) Object {
			items := make([]Object, len(l.items))
			copy(items, l.items)
			return pyList(items)
		}), true
	case "clear":
		return makeBuiltin("clear", func(args []Object, kwargs map[string]Object) Object {
			l.items = []Object{}
			return pyNone
		}), true
	}
	return nil, false
}

// ---- PyTuple ----

// PyTuple is the Python tuple type.
type PyTuple struct{ items []Object }

func pyTuple(items []Object) *PyTuple {
	if items == nil {
		items = []Object{}
	}
	return &PyTuple{items: items}
}
func (t *PyTuple) pyType() *PyType { return typeTuple }
func (t *PyTuple) pyRepr() string {
	if len(t.items) == 0 {
		return "()"
	}
	parts := make([]string, len(t.items))
	for i, item := range t.items {
		parts[i] = item.pyRepr()
	}
	if len(t.items) == 1 {
		return "(" + parts[0] + ",)"
	}
	return "(" + strings.Join(parts, ", ") + ")"
}
func (t *PyTuple) pyStr() string { return t.pyRepr() }

// ---- PyDict ----

// PyDict is the Python dict type, preserving insertion order.
type PyDict struct {
	keys  []Object
	vals  []Object
	index map[any]int
}

func pyDict() *PyDict {
	return &PyDict{index: make(map[any]int)}
}

func pyDictFromPairs(pairs [][2]Object) *PyDict {
	d := pyDict()
	for _, p := range pairs {
		d.set(p[0], p[1])
	}
	return d
}

func (d *PyDict) get(key Object) (Object, bool) {
	k, err := hashKey(key)
	if err != nil {
		panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "unhashable type: '%s'", key.pyType().Name)})
	}
	if idx, ok := d.index[k]; ok {
		return d.vals[idx], true
	}
	return nil, false
}

func (d *PyDict) set(key Object, val Object) {
	k, err := hashKey(key)
	if err != nil {
		panic(exceptionSignal{exc: newExceptionf(ExcTypeError, "unhashable type: '%s'", key.pyType().Name)})
	}
	if idx, ok := d.index[k]; ok {
		d.vals[idx] = val
		return
	}
	d.index[k] = len(d.keys)
	d.keys = append(d.keys, key)
	d.vals = append(d.vals, val)
}

func (d *PyDict) del(key Object) bool {
	k, err := hashKey(key)
	if err != nil {
		return false
	}
	idx, ok := d.index[k]
	if !ok {
		return false
	}
	// Remove from slice
	d.keys = append(d.keys[:idx], d.keys[idx+1:]...)
	d.vals = append(d.vals[:idx], d.vals[idx+1:]...)
	// Rebuild index
	delete(d.index, k)
	for i := idx; i < len(d.keys); i++ {
		k2, _ := hashKey(d.keys[i])
		d.index[k2] = i
	}
	return true
}

func (d *PyDict) pyType() *PyType { return typeDict }
func (d *PyDict) pyRepr() string {
	if len(d.keys) == 0 {
		return "{}"
	}
	parts := make([]string, len(d.keys))
	for i := range d.keys {
		parts[i] = d.keys[i].pyRepr() + ": " + d.vals[i].pyRepr()
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func (d *PyDict) pyStr() string { return d.pyRepr() }

func dictGetAttr(d *PyDict, name string) (Object, bool) {
	switch name {
	case "get":
		return makeBuiltin("get", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("get() requires at least 1 argument")
			}
			k, err := hashKey(args[0])
			if err != nil {
				raiseTypeError("unhashable type: '%s'", args[0].pyType().Name)
			}
			if idx, ok := d.index[k]; ok {
				return d.vals[idx]
			}
			if len(args) > 1 {
				return args[1]
			}
			return pyNone
		}), true
	case "keys":
		return makeBuiltin("keys", func(args []Object, kwargs map[string]Object) Object {
			items := make([]Object, len(d.keys))
			copy(items, d.keys)
			return pyList(items)
		}), true
	case "values":
		return makeBuiltin("values", func(args []Object, kwargs map[string]Object) Object {
			items := make([]Object, len(d.vals))
			copy(items, d.vals)
			return pyList(items)
		}), true
	case "items":
		return makeBuiltin("items", func(args []Object, kwargs map[string]Object) Object {
			items := make([]Object, len(d.keys))
			for i := range d.keys {
				items[i] = pyTuple([]Object{d.keys[i], d.vals[i]})
			}
			return pyList(items)
		}), true
	case "update":
		return makeBuiltin("update", func(args []Object, kwargs map[string]Object) Object {
			if len(args) > 0 {
				if other, ok := args[0].(*PyDict); ok {
					for i, k := range other.keys {
						d.set(k, other.vals[i])
					}
				}
			}
			for k, v := range kwargs {
				d.set(pyStr(k), v)
			}
			return pyNone
		}), true
	case "pop":
		return makeBuiltin("pop", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("pop() requires at least 1 argument")
			}
			val, ok := d.get(args[0])
			if !ok {
				if len(args) > 1 {
					return args[1]
				}
				raiseKeyError(args[0])
			}
			d.del(args[0])
			return val
		}), true
	case "setdefault":
		return makeBuiltin("setdefault", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("setdefault() requires at least 1 argument")
			}
			val, ok := d.get(args[0])
			if ok {
				return val
			}
			def := Object(pyNone)
			if len(args) > 1 {
				def = args[1]
			}
			d.set(args[0], def)
			return def
		}), true
	case "copy":
		return makeBuiltin("copy", func(args []Object, kwargs map[string]Object) Object {
			newD := pyDict()
			for i, k := range d.keys {
				newD.set(k, d.vals[i])
			}
			return newD
		}), true
	case "clear":
		return makeBuiltin("clear", func(args []Object, kwargs map[string]Object) Object {
			d.keys = nil
			d.vals = nil
			d.index = make(map[any]int)
			return pyNone
		}), true
	}
	return nil, false
}

// ---- PySet ----

// PySet is the Python set type.
type PySet struct {
	items map[any]Object
}

func pySet(items []Object) (*PySet, error) {
	s := &PySet{items: make(map[any]Object)}
	for _, item := range items {
		k, err := hashKey(item)
		if err != nil {
			return nil, err
		}
		s.items[k] = item
	}
	return s, nil
}

func (s *PySet) pyType() *PyType { return typeSet }
func (s *PySet) pyRepr() string {
	if len(s.items) == 0 {
		return "set()"
	}
	parts := make([]string, 0, len(s.items))
	for _, v := range s.items {
		parts = append(parts, v.pyRepr())
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
func (s *PySet) pyStr() string { return s.pyRepr() }

func setGetAttr(s *PySet, name string) (Object, bool) {
	switch name {
	case "add":
		return makeBuiltin("add", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("add() takes exactly 1 argument")
			}
			k, err := hashKey(args[0])
			if err != nil {
				raiseTypeError("unhashable type: '%s'", args[0].pyType().Name)
			}
			s.items[k] = args[0]
			return pyNone
		}), true
	case "discard":
		return makeBuiltin("discard", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("discard() takes exactly 1 argument")
			}
			k, err := hashKey(args[0])
			if err == nil {
				delete(s.items, k)
			}
			return pyNone
		}), true
	case "remove":
		return makeBuiltin("remove", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("remove() takes exactly 1 argument")
			}
			k, err := hashKey(args[0])
			if err != nil {
				raiseTypeError("unhashable type: '%s'", args[0].pyType().Name)
			}
			if _, ok := s.items[k]; !ok {
				raiseKeyError(args[0])
			}
			delete(s.items, k)
			return pyNone
		}), true
	case "pop":
		return makeBuiltin("pop", func(args []Object, kwargs map[string]Object) Object {
			for k, v := range s.items {
				delete(s.items, k)
				return v
			}
			raiseKeyError(pyStr("pop from an empty set"))
			return nil
		}), true
	case "union":
		return makeBuiltin("union", func(args []Object, kwargs map[string]Object) Object {
			result := &PySet{items: make(map[any]Object)}
			for k, v := range s.items {
				result.items[k] = v
			}
			for _, arg := range args {
				items := collectIterable(arg)
				for _, item := range items {
					k, err := hashKey(item)
					if err != nil {
						raiseTypeError("unhashable type: '%s'", item.pyType().Name)
					}
					result.items[k] = item
				}
			}
			return result
		}), true
	case "intersection":
		return makeBuiltin("intersection", func(args []Object, kwargs map[string]Object) Object {
			result := &PySet{items: make(map[any]Object)}
			if len(args) == 0 {
				return result
			}
			other := args[0]
			otherItems := collectIterable(other)
			otherSet := make(map[any]bool)
			for _, item := range otherItems {
				k, err := hashKey(item)
				if err == nil {
					otherSet[k] = true
				}
			}
			for k, v := range s.items {
				if otherSet[k] {
					result.items[k] = v
				}
			}
			return result
		}), true
	case "difference":
		return makeBuiltin("difference", func(args []Object, kwargs map[string]Object) Object {
			result := &PySet{items: make(map[any]Object)}
			for k, v := range s.items {
				result.items[k] = v
			}
			for _, arg := range args {
				items := collectIterable(arg)
				for _, item := range items {
					k, err := hashKey(item)
					if err == nil {
						delete(result.items, k)
					}
				}
			}
			return result
		}), true
	case "issubset":
		return makeBuiltin("issubset", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("issubset() takes exactly 1 argument")
			}
			otherItems := collectIterable(args[0])
			otherSet := make(map[any]bool)
			for _, item := range otherItems {
				k, err := hashKey(item)
				if err == nil {
					otherSet[k] = true
				}
			}
			for k := range s.items {
				if !otherSet[k] {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	case "issuperset":
		return makeBuiltin("issuperset", func(args []Object, kwargs map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("issuperset() takes exactly 1 argument")
			}
			otherItems := collectIterable(args[0])
			for _, item := range otherItems {
				k, err := hashKey(item)
				if err != nil {
					raiseTypeError("unhashable type: '%s'", item.pyType().Name)
				}
				if _, ok := s.items[k]; !ok {
					return pyFalse
				}
			}
			return pyTrue
		}), true
	}
	return nil, false
}

// PyFrozenSet is the Python frozenset type.
type PyFrozenSet struct {
	items map[any]Object
}

func (s *PyFrozenSet) pyType() *PyType { return typeFrozenSet }
func (s *PyFrozenSet) pyRepr() string {
	if len(s.items) == 0 {
		return "frozenset()"
	}
	parts := make([]string, 0, len(s.items))
	for _, v := range s.items {
		parts = append(parts, v.pyRepr())
	}
	return "frozenset({" + strings.Join(parts, ", ") + "})"
}
func (s *PyFrozenSet) pyStr() string { return s.pyRepr() }

// ---- PyFunction ----

// PyFunction represents a user-defined Python function.
type PyFunction struct {
	Name       string
	Args       *Arguments
	Body       []Stmt
	Closure    *Scope
	Globals    map[string]Object
	Defaults   []Object
	KwDefaults map[string]Object
	IsGen      bool
}

func (f *PyFunction) pyType() *PyType { return typeFunction }
func (f *PyFunction) pyRepr() string  { return "<function " + f.Name + ">" }
func (f *PyFunction) pyStr() string   { return f.pyRepr() }

// ---- PyBuiltin ----

// PyBuiltin is a built-in function or method.
type PyBuiltin struct {
	Name string
	Fn   func(args []Object, kwargs map[string]Object) Object
}

func (b *PyBuiltin) pyType() *PyType { return typeBuiltin }
func (b *PyBuiltin) pyRepr() string  { return "<built-in function " + b.Name + ">" }
func (b *PyBuiltin) pyStr() string   { return b.pyRepr() }

func makeBuiltin(name string, fn func([]Object, map[string]Object) Object) *PyBuiltin {
	return &PyBuiltin{Name: name, Fn: fn}
}

// ---- PyClass and PyInstance ----

// PyClass represents a Python class (user-defined or built-in exception class).
type PyClass struct {
	Name  string
	Bases []*PyClass
	MRO   []*PyClass
	Dict  map[string]Object
}

func (c *PyClass) pyType() *PyType { return typeClass }
func (c *PyClass) pyRepr() string  { return "<class '" + c.Name + "'>" }
func (c *PyClass) pyStr() string   { return c.pyRepr() }

// computeMRO computes the C3 linearization of a class hierarchy.
func computeMRO(cls *PyClass) []*PyClass {
	if len(cls.Bases) == 0 {
		return []*PyClass{cls}
	}
	// Simple: cls + flatten bases
	seen := map[*PyClass]bool{cls: true}
	result := []*PyClass{cls}
	var walk func(c *PyClass)
	walk = func(c *PyClass) {
		for _, base := range c.Bases {
			if !seen[base] {
				seen[base] = true
				result = append(result, base)
				walk(base)
			}
		}
	}
	walk(cls)
	return result
}

// PyInstance represents a Python object instance.
type PyInstance struct {
	Class *PyClass
	Dict  map[string]Object
}

func (i *PyInstance) pyType() *PyType { return typeClass }
func (i *PyInstance) pyRepr() string {
	if reprFn, ok := i.lookupMethod("__repr__"); ok {
		result := callObject(reprFn, []Object{i}, nil)
		if s, ok := result.(*PyStr); ok {
			return s.v
		}
	}
	return "<" + i.Class.Name + " object>"
}
func (i *PyInstance) pyStr() string {
	if strFn, ok := i.lookupMethod("__str__"); ok {
		result := callObject(strFn, []Object{i}, nil)
		if s, ok := result.(*PyStr); ok {
			return s.v
		}
	}
	return i.pyRepr()
}

func (i *PyInstance) lookupMethod(name string) (Object, bool) {
	if v, ok := i.Dict[name]; ok {
		return v, true
	}
	for _, cls := range i.Class.MRO {
		if v, ok := cls.Dict[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// goroutineCallFns maps goroutine ID → the active evaluator's callObject for that goroutine.
// Each Python execution registers its callObject before running and deregisters on return,
// so concurrent executions never share a function pointer.
var goroutineCallFns sync.Map // map[int64]func(Object, []Object, map[string]Object) Object

// goroutineID returns the current goroutine's numeric ID by inspecting the stack header.
// Format: "goroutine N [..."
//
// Parsing runtime.Stack output is fragile — the format is undocumented. Returns (0, false)
// if the ID cannot be parsed so callers can degrade gracefully rather than crashing.
func goroutineID() (int64, bool) {
	var buf [64]byte
	runtime.Stack(buf[:], false)
	var id int64
	for i := 10; i < len(buf); i++ { // skip "goroutine "
		c := buf[i]
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + int64(c-'0')
	}
	if id == 0 {
		return 0, false
	}
	return id, true
}

// callObject dispatches a call through the evaluator registered for the current goroutine.
func callObject(fn Object, args []Object, kwargs map[string]Object) Object {
	gid, ok := goroutineID()
	if !ok {
		panic(exceptionSignal{exc: newExceptionf(ExcRuntimeError, "could not determine goroutine ID (runtime.Stack format changed)")})
	}
	v, ok := goroutineCallFns.Load(gid)
	if !ok {
		panic("callObject invoked outside Python evaluation context")
	}
	return v.(func(Object, []Object, map[string]Object) Object)(fn, args, kwargs)
}

// ---- PyModule ----

// PyModule represents a Python module.
type PyModule struct {
	Name string
	Dict map[string]Object
}

func (m *PyModule) pyType() *PyType { return typeModule }
func (m *PyModule) pyRepr() string  { return "<module '" + m.Name + "'>" }
func (m *PyModule) pyStr() string   { return m.pyRepr() }

// ---- PyRange ----

// PyRange represents a Python range object.
type PyRange struct {
	start, stop, step int64
}

func (r *PyRange) pyType() *PyType { return typeRange }
func (r *PyRange) pyRepr() string {
	if r.step == 1 {
		return fmt.Sprintf("range(%d, %d)", r.start, r.stop)
	}
	return fmt.Sprintf("range(%d, %d, %d)", r.start, r.stop, r.step)
}
func (r *PyRange) pyStr() string { return r.pyRepr() }

func (r *PyRange) length() int64 {
	if r.step > 0 {
		if r.stop <= r.start {
			return 0
		}
		return (r.stop - r.start + r.step - 1) / r.step
	}
	if r.step < 0 {
		if r.start <= r.stop {
			return 0
		}
		return (r.start - r.stop - r.step - 1) / (-r.step)
	}
	return 0
}

// rangeIter is the iterator for PyRange.
type rangeIter struct {
	r   *PyRange
	cur int64
}

func (ri *rangeIter) next() (Object, bool) {
	if ri.r.step > 0 && ri.cur >= ri.r.stop {
		return nil, false
	}
	if ri.r.step < 0 && ri.cur <= ri.r.stop {
		return nil, false
	}
	val := ri.cur
	ri.cur += ri.r.step
	return pyInt(val), true
}

func (ri *rangeIter) pyType() *PyType { return typeRange }
func (ri *rangeIter) pyRepr() string  { return "<range_iterator>" }
func (ri *rangeIter) pyStr() string   { return ri.pyRepr() }

// ---- PyGenerator ----

// PyGenerator implements Python generators via goroutines.
type PyGenerator struct {
	name         string
	sendCh       chan Object // caller → generator
	yieldCh      chan Object // generator → caller
	done         bool
	awaitingSend bool              // true after a value has been received from yieldCh; the generator is blocked waiting for sendCh
	excCh        chan *PyException // generator sends exception at close
	ctx          context.Context   // execution context; used by drainGenerator to respect cancellation
}

func (g *PyGenerator) pyType() *PyType { return typeGenerator }
func (g *PyGenerator) pyRepr() string  { return "<generator object " + g.name + ">" }
func (g *PyGenerator) pyStr() string   { return g.pyRepr() }

// ---- PyException ----

// TraceFrame is a single frame in a traceback.
type TraceFrame struct {
	File string
	Line int
	Name string
}

// PyException represents a Python exception instance.
type PyException struct {
	ExcClass  *PyClass
	Args      []Object
	Cause     *PyException
	Context   *PyException
	Traceback []TraceFrame
	Dict      map[string]Object
}

func (e *PyException) pyType() *PyType { return typeClass }
func (e *PyException) pyRepr() string {
	if len(e.Args) == 0 {
		return e.ExcClass.Name + "()"
	}
	if len(e.Args) == 1 {
		return e.ExcClass.Name + "(" + e.Args[0].pyRepr() + ")"
	}
	parts := make([]string, len(e.Args))
	for i, a := range e.Args {
		parts[i] = a.pyRepr()
	}
	return e.ExcClass.Name + "(" + strings.Join(parts, ", ") + ")"
}
func (e *PyException) pyStr() string {
	if len(e.Args) == 0 {
		return ""
	}
	if len(e.Args) == 1 {
		return e.Args[0].pyStr()
	}
	parts := make([]string, len(e.Args))
	for i, a := range e.Args {
		parts[i] = a.pyStr()
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// Exception class singletons.
var (
	ExcBaseException       = &PyClass{Name: "BaseException"}
	ExcException           = &PyClass{Name: "Exception", Bases: []*PyClass{ExcBaseException}}
	ExcArithmeticError     = &PyClass{Name: "ArithmeticError", Bases: []*PyClass{ExcException}}
	ExcLookupError         = &PyClass{Name: "LookupError", Bases: []*PyClass{ExcException}}
	ExcValueError          = &PyClass{Name: "ValueError", Bases: []*PyClass{ExcException}}
	ExcTypeError           = &PyClass{Name: "TypeError", Bases: []*PyClass{ExcException}}
	ExcAttributeError      = &PyClass{Name: "AttributeError", Bases: []*PyClass{ExcException}}
	ExcNameError           = &PyClass{Name: "NameError", Bases: []*PyClass{ExcException}}
	ExcImportError         = &PyClass{Name: "ImportError", Bases: []*PyClass{ExcException}}
	ExcIndexError          = &PyClass{Name: "IndexError", Bases: []*PyClass{ExcLookupError}}
	ExcKeyError            = &PyClass{Name: "KeyError", Bases: []*PyClass{ExcLookupError}}
	ExcStopIteration       = &PyClass{Name: "StopIteration", Bases: []*PyClass{ExcException}}
	ExcGeneratorExit       = &PyClass{Name: "GeneratorExit", Bases: []*PyClass{ExcBaseException}}
	ExcRuntimeError        = &PyClass{Name: "RuntimeError", Bases: []*PyClass{ExcException}}
	ExcNotImplementedError = &PyClass{Name: "NotImplementedError", Bases: []*PyClass{ExcRuntimeError}}
	ExcOSError             = &PyClass{Name: "OSError", Bases: []*PyClass{ExcException}}
	ExcFileNotFoundError   = &PyClass{Name: "FileNotFoundError", Bases: []*PyClass{ExcOSError}}
	ExcPermissionError     = &PyClass{Name: "PermissionError", Bases: []*PyClass{ExcOSError}}
	ExcZeroDivisionError   = &PyClass{Name: "ZeroDivisionError", Bases: []*PyClass{ExcArithmeticError}}
	ExcOverflowError       = &PyClass{Name: "OverflowError", Bases: []*PyClass{ExcArithmeticError}}
	ExcMemoryError         = &PyClass{Name: "MemoryError", Bases: []*PyClass{ExcException}}
	ExcKeyboardInterrupt   = &PyClass{Name: "KeyboardInterrupt", Bases: []*PyClass{ExcBaseException}}
	ExcSystemExit          = &PyClass{Name: "SystemExit", Bases: []*PyClass{ExcBaseException}}
	ExcAssertionError      = &PyClass{Name: "AssertionError", Bases: []*PyClass{ExcException}}
	ExcUnboundLocalError   = &PyClass{Name: "UnboundLocalError", Bases: []*PyClass{ExcNameError}}
	ExcRecursionError      = &PyClass{Name: "RecursionError", Bases: []*PyClass{ExcRuntimeError}}
	ExcUnicodeError        = &PyClass{Name: "UnicodeError", Bases: []*PyClass{ExcValueError}}
	ExcUnicodeDecodeError  = &PyClass{Name: "UnicodeDecodeError", Bases: []*PyClass{ExcUnicodeError}}
	ExcUnicodeEncodeError  = &PyClass{Name: "UnicodeEncodeError", Bases: []*PyClass{ExcUnicodeError}}
	ExcIOError             = ExcOSError // alias
)

func init() {
	allExcClasses := []*PyClass{
		ExcBaseException, ExcException, ExcArithmeticError, ExcLookupError,
		ExcValueError, ExcTypeError, ExcAttributeError, ExcNameError,
		ExcImportError, ExcIndexError, ExcKeyError, ExcStopIteration,
		ExcGeneratorExit, ExcRuntimeError, ExcNotImplementedError, ExcOSError,
		ExcFileNotFoundError, ExcPermissionError, ExcZeroDivisionError,
		ExcOverflowError, ExcMemoryError, ExcKeyboardInterrupt, ExcSystemExit,
		ExcAssertionError, ExcUnboundLocalError, ExcRecursionError, ExcUnicodeError,
		ExcUnicodeDecodeError, ExcUnicodeEncodeError,
	}
	for _, c := range allExcClasses {
		c.MRO = computeMRO(c)
		if c.Dict == nil {
			c.Dict = make(map[string]Object)
		}
	}
}

// newException creates a new PyException for the given class with message args.
func newException(cls *PyClass, args ...Object) *PyException {
	return &PyException{
		ExcClass: cls,
		Args:     args,
		Dict:     make(map[string]Object),
	}
}

// newExceptionf creates a PyException with a formatted message string.
func newExceptionf(cls *PyClass, format string, a ...interface{}) *PyException {
	msg := fmt.Sprintf(format, a...)
	return &PyException{
		ExcClass: cls,
		Args:     []Object{pyStr(msg)},
		Dict:     make(map[string]Object),
	}
}

// isInstance checks if obj is an instance of cls (walks MRO).
func isInstance(obj Object, cls *PyClass) bool {
	switch v := obj.(type) {
	case *PyException:
		return exceptionMatchesClass(v, cls)
	case *PyInstance:
		for _, c := range v.Class.MRO {
			if c == cls {
				return true
			}
		}
		return false
	case *PyNone:
		return cls.Name == "NoneType"
	case *PyBool:
		return cls.Name == "bool" || cls.Name == "int"
	case *PyInt:
		return cls.Name == "int"
	case *PyFloat:
		return cls.Name == "float"
	case *PyStr:
		return cls.Name == "str"
	case *PyBytes:
		return cls.Name == "bytes"
	case *PyList:
		return cls.Name == "list"
	case *PyTuple:
		return cls.Name == "tuple"
	case *PyDict:
		return cls.Name == "dict"
	case *PySet:
		return cls.Name == "set"
	}
	return false
}

// exceptionMatchesClass checks if a PyException matches a class (by MRO walk).
func exceptionMatchesClass(exc *PyException, cls *PyClass) bool {
	for _, c := range exc.ExcClass.MRO {
		if c == cls {
			return true
		}
	}
	return false
}

// ---- PyFile ----

const maxFileReadBytes = 1 << 20 // 1 MiB

// PyFile represents a Python file object.
type PyFile struct {
	rc      io.ReadWriteCloser
	w       io.Writer
	r       *bufio.Reader
	name    string
	binary  bool
	closed  bool
	buf     []byte
	bufDone bool
}

func (f *PyFile) pyType() *PyType { return typeFile }
func (f *PyFile) pyRepr() string {
	mode := "r"
	if f.binary {
		mode = "rb"
	}
	return fmt.Sprintf("<_io.TextIOWrapper name='%s' mode='%s' encoding='UTF-8'>", f.name, mode)
}
func (f *PyFile) pyStr() string { return f.pyRepr() }

func fileGetAttr(f *PyFile, name string) (Object, bool) {
	switch name {
	case "read":
		return makeBuiltin("read", func(args []Object, kwargs map[string]Object) Object {
			if f.closed {
				panic(exceptionSignal{exc: newExceptionf(ExcValueError, "I/O operation on closed file.")})
			}
			n := -1
			if len(args) > 0 && args[0] != pyNone {
				if v, ok := args[0].(*PyInt); ok {
					if i, ok2 := v.int64(); ok2 {
						n = int(i)
					}
				}
			}
			return f.read(n)
		}), true
	case "readline":
		return makeBuiltin("readline", func(args []Object, kwargs map[string]Object) Object {
			if f.closed {
				panic(exceptionSignal{exc: newExceptionf(ExcValueError, "I/O operation on closed file.")})
			}
			if f.r != nil {
				// f.r is a bufio.Reader already wrapping a LimitReader (set up in
				// runInternal), so we reuse it directly rather than wrapping again.
				// Creating a fresh LimitReader per call would give each readline()
				// call its own independent 1 MiB budget and would also discard
				// buffered bytes from f.r's internal buffer.
				line, err := f.r.ReadString('\n')
				if err != nil && err != io.EOF {
					panic(exceptionSignal{exc: newExceptionf(ExcOSError, "readline error: %v", err)})
				}
				if f.binary {
					return pyBytes([]byte(line))
				}
				return pyStr(line)
			}
			// For rc-based files
			if !f.bufDone {
				f.loadBuf()
			}
			idx := -1
			for i, b := range f.buf {
				if b == '\n' {
					idx = i
					break
				}
			}
			var line []byte
			if idx < 0 {
				line = f.buf
				f.buf = nil
			} else {
				line = f.buf[:idx+1]
				f.buf = f.buf[idx+1:]
			}
			if f.binary {
				return pyBytes(line)
			}
			return pyStr(string(line))
		}), true
	case "readlines":
		return makeBuiltin("readlines", func(args []Object, kwargs map[string]Object) Object {
			if f.closed {
				panic(exceptionSignal{exc: newExceptionf(ExcValueError, "I/O operation on closed file.")})
			}
			if !f.bufDone {
				f.loadBuf()
			}
			lines := splitBytesLines(f.buf)
			f.buf = nil
			items := make([]Object, len(lines))
			for i, l := range lines {
				if f.binary {
					items[i] = pyBytes(l)
				} else {
					items[i] = pyStr(string(l))
				}
			}
			return pyList(items)
		}), true
	case "write":
		return makeBuiltin("write", func(args []Object, kwargs map[string]Object) Object {
			if f.closed {
				panic(exceptionSignal{exc: newExceptionf(ExcValueError, "I/O operation on closed file.")})
			}
			if len(args) != 1 {
				raiseTypeError("write() takes exactly 1 argument")
			}
			var data []byte
			switch v := args[0].(type) {
			case *PyStr:
				data = []byte(v.v)
			case *PyBytes:
				data = v.v
			default:
				raiseTypeError("write() argument must be str or bytes")
			}
			var err error
			if f.w != nil {
				_, err = f.w.Write(data)
			} else if f.rc != nil {
				// Files opened via open() are always read-only; block writes at the
				// application layer rather than relying solely on OS rejection.
				panic(exceptionSignal{exc: newExceptionf(ExcPermissionError, "write() is not permitted on a file opened in read mode")})
			}
			if err != nil {
				panic(exceptionSignal{exc: newExceptionf(ExcOSError, "write error: %v", err)})
			}
			return pyInt(int64(len(data)))
		}), true
	case "close":
		return makeBuiltin("close", func(args []Object, kwargs map[string]Object) Object {
			if !f.closed && f.rc != nil {
				_ = f.rc.Close()
				f.closed = true
			}
			return pyNone
		}), true
	case "__enter__":
		return makeBuiltin("__enter__", func(args []Object, kwargs map[string]Object) Object {
			return f
		}), true
	case "__exit__":
		return makeBuiltin("__exit__", func(args []Object, kwargs map[string]Object) Object {
			if !f.closed && f.rc != nil {
				_ = f.rc.Close()
				f.closed = true
			}
			return pyFalse
		}), true
	case "name":
		return pyStr(f.name), true
	case "closed":
		return pyBool(f.closed), true
	case "flush":
		return makeBuiltin("flush", func(args []Object, kwargs map[string]Object) Object {
			return pyNone
		}), true
	}
	return nil, false
}

func (f *PyFile) loadBuf() {
	if f.bufDone {
		return
	}
	f.bufDone = true
	if f.rc != nil {
		data, _ := io.ReadAll(io.LimitReader(f.rc, maxFileReadBytes+1))
		if len(data) > maxFileReadBytes {
			panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "file content exceeds %d byte limit", maxFileReadBytes)})
		}
		f.buf = data
	}
}

func (f *PyFile) read(n int) Object {
	if f.r != nil {
		// stdin-like reader
		if n < 0 {
			data, _ := io.ReadAll(io.LimitReader(f.r, maxFileReadBytes+1))
			if len(data) > maxFileReadBytes {
				panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "stdin content exceeds %d byte limit", maxFileReadBytes)})
			}
			if f.binary {
				return pyBytes(data)
			}
			return pyStr(string(data))
		}
		// Cap n to the per-file read limit to prevent OOM via large allocations.
		if n > maxFileReadBytes {
			n = maxFileReadBytes
		}
		buf := make([]byte, n)
		total := 0
		for total < n {
			nr, err := f.r.Read(buf[total:])
			total += nr
			if err != nil {
				break
			}
		}
		if f.binary {
			return pyBytes(buf[:total])
		}
		return pyStr(string(buf[:total]))
	}
	// rc-based file
	if !f.bufDone {
		f.loadBuf()
	}
	var chunk []byte
	if n < 0 {
		chunk = f.buf
		f.buf = nil
	} else {
		if n > len(f.buf) {
			n = len(f.buf)
		}
		chunk = f.buf[:n]
		f.buf = f.buf[n:]
	}
	if f.binary {
		return pyBytes(chunk)
	}
	return pyStr(string(chunk))
}

func splitBytesLines(b []byte) [][]byte {
	var lines [][]byte
	for len(b) > 0 {
		idx := -1
		for i, c := range b {
			if c == '\n' {
				idx = i
				break
			}
		}
		if idx < 0 {
			lines = append(lines, b)
			break
		}
		lines = append(lines, b[:idx+1])
		b = b[idx+1:]
	}
	return lines
}

// ---- PyBoundMethod ----

// PyBoundMethod binds a method to its self object.
type PyBoundMethod struct {
	Self Object
	Func *PyFunction
}

func (m *PyBoundMethod) pyType() *PyType { return typeBoundMethod }
func (m *PyBoundMethod) pyRepr() string {
	return "<bound method " + m.Func.Name + " of " + m.Self.pyRepr() + ">"
}
func (m *PyBoundMethod) pyStr() string { return m.pyRepr() }

// ---- Scope ----

// Scope represents a variable scope (function frame or module level).
type Scope struct {
	vars          map[string]Object
	parent        *Scope
	globals       map[string]Object
	globalNames   map[string]bool
	nonlocalNames map[string]bool
	class         *PyClass
	funcName      string
	file          string
	line          int
}

func newModuleScope(globals map[string]Object) *Scope {
	return &Scope{
		vars:    globals,
		globals: globals,
	}
}

func newFunctionScope(parent *Scope, globals map[string]Object, funcName string) *Scope {
	return &Scope{
		vars:     make(map[string]Object),
		parent:   parent,
		globals:  globals,
		funcName: funcName,
	}
}

func (s *Scope) get(name string) (Object, bool) {
	// Check globals declaration
	if s.globalNames != nil && s.globalNames[name] {
		if v, ok := s.globals[name]; ok {
			return v, true
		}
		return nil, false
	}
	// Check nonlocal
	if s.nonlocalNames != nil && s.nonlocalNames[name] {
		p := s.parent
		for p != nil && p.globals != nil {
			if v, ok := p.vars[name]; ok {
				return v, true
			}
			p = p.parent
		}
		return nil, false
	}
	// Local first
	if v, ok := s.vars[name]; ok {
		return v, true
	}
	// Walk up to globals (but not through sibling scopes)
	if s.parent != nil && !s.isGlobalScope() {
		return s.parent.get(name)
	}
	return nil, false
}

func (s *Scope) set(name string, val Object) {
	if s.globalNames != nil && s.globalNames[name] {
		s.globals[name] = val
		return
	}
	if s.nonlocalNames != nil && s.nonlocalNames[name] {
		p := s.parent
		for p != nil && !p.isGlobalScope() {
			if _, ok := p.vars[name]; ok {
				p.vars[name] = val
				return
			}
			p = p.parent
		}
		// If not found, set in parent
		if s.parent != nil {
			s.parent.vars[name] = val
		}
		return
	}
	s.vars[name] = val
}

// isGlobalScope returns true if this scope is the module/global scope.
func (s *Scope) isGlobalScope() bool {
	return s.parent == nil
}

func (s *Scope) delete(name string) bool {
	if _, ok := s.vars[name]; ok {
		delete(s.vars, name)
		return true
	}
	return false
}

// ---- Utility functions ----

// pyTruth returns the Python truth value of obj.
func pyTruth(obj Object) bool {
	if obj == nil || obj == pyNone {
		return false
	}
	switch v := obj.(type) {
	case *PyBool:
		return v.v
	case *PyInt:
		if v.big != nil {
			return v.big.Sign() != 0
		}
		return v.small != 0
	case *PyFloat:
		return v.v != 0
	case *PyStr:
		return len(v.v) > 0
	case *PyBytes:
		return len(v.v) > 0
	case *PyList:
		return len(v.items) > 0
	case *PyTuple:
		return len(v.items) > 0
	case *PyDict:
		return len(v.keys) > 0
	case *PySet:
		return len(v.items) > 0
	case *PyFrozenSet:
		return len(v.items) > 0
	case *PyRange:
		return v.length() > 0
	}
	return true
}

// pyEq returns true if a == b (Python equality).
func pyEq(a, b Object) bool {
	if a == b {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	switch av := a.(type) {
	case *PyNone:
		_, ok := b.(*PyNone)
		return ok
	case *PyBool:
		switch bv := b.(type) {
		case *PyBool:
			return av.v == bv.v
		case *PyInt:
			var ai int64
			if av.v {
				ai = 1
			}
			if n, ok := bv.int64(); ok {
				return ai == n
			}
		}
	case *PyInt:
		switch bv := b.(type) {
		case *PyInt:
			if av.big == nil && bv.big == nil {
				return av.small == bv.small
			}
			return av.toBigInt().Cmp(bv.toBigInt()) == 0
		case *PyBool:
			var bi int64
			if bv.v {
				bi = 1
			}
			if n, ok := av.int64(); ok {
				return n == bi
			}
		case *PyFloat:
			if n, ok := av.int64(); ok {
				return float64(n) == bv.v
			}
		}
	case *PyFloat:
		switch bv := b.(type) {
		case *PyFloat:
			return av.v == bv.v
		case *PyInt:
			if n, ok := bv.int64(); ok {
				return av.v == float64(n)
			}
		}
	case *PyStr:
		if bv, ok := b.(*PyStr); ok {
			return av.v == bv.v
		}
	case *PyBytes:
		if bv, ok := b.(*PyBytes); ok {
			if len(av.v) != len(bv.v) {
				return false
			}
			for i := range av.v {
				if av.v[i] != bv.v[i] {
					return false
				}
			}
			return true
		}
	case *PyList:
		if bv, ok := b.(*PyList); ok {
			if len(av.items) != len(bv.items) {
				return false
			}
			for i := range av.items {
				if !pyEq(av.items[i], bv.items[i]) {
					return false
				}
			}
			return true
		}
	case *PyTuple:
		if bv, ok := b.(*PyTuple); ok {
			if len(av.items) != len(bv.items) {
				return false
			}
			for i := range av.items {
				if !pyEq(av.items[i], bv.items[i]) {
					return false
				}
			}
			return true
		}
	}
	return false
}

// pyCompare returns -1, 0, +1 for a < b, a == b, a > b.
func pyCompare(a, b Object) int {
	if pyEq(a, b) {
		return 0
	}
	switch av := a.(type) {
	case *PyInt:
		switch bv := b.(type) {
		case *PyInt:
			return av.toBigInt().Cmp(bv.toBigInt())
		case *PyFloat:
			if n, ok := av.int64(); ok {
				f := float64(n)
				if f < bv.v {
					return -1
				} else if f > bv.v {
					return 1
				}
				return 0
			}
		case *PyBool:
			var bi int64
			if bv.v {
				bi = 1
			}
			if n, ok := av.int64(); ok {
				if n < bi {
					return -1
				} else if n > bi {
					return 1
				}
				return 0
			}
		}
	case *PyFloat:
		switch bv := b.(type) {
		case *PyFloat:
			if av.v < bv.v {
				return -1
			}
			return 1
		case *PyInt:
			if n, ok := bv.int64(); ok {
				f := float64(n)
				if av.v < f {
					return -1
				} else if av.v > f {
					return 1
				}
				return 0
			}
		}
	case *PyStr:
		if bv, ok := b.(*PyStr); ok {
			if av.v < bv.v {
				return -1
			}
			return 1
		}
	case *PyBool:
		var ai int64
		if av.v {
			ai = 1
		}
		switch bv := b.(type) {
		case *PyBool:
			var bi int64
			if bv.v {
				bi = 1
			}
			if ai < bi {
				return -1
			} else if ai > bi {
				return 1
			}
			return 0
		case *PyInt:
			if n, ok := bv.int64(); ok {
				if ai < n {
					return -1
				} else if ai > n {
					return 1
				}
				return 0
			}
		}
	case *PyList:
		if bv, ok := b.(*PyList); ok {
			minLen := len(av.items)
			if len(bv.items) < minLen {
				minLen = len(bv.items)
			}
			for i := 0; i < minLen; i++ {
				c := pyCompare(av.items[i], bv.items[i])
				if c != 0 {
					return c
				}
			}
			if len(av.items) < len(bv.items) {
				return -1
			} else if len(av.items) > len(bv.items) {
				return 1
			}
			return 0
		}
	case *PyTuple:
		if bv, ok := b.(*PyTuple); ok {
			minLen := len(av.items)
			if len(bv.items) < minLen {
				minLen = len(bv.items)
			}
			for i := 0; i < minLen; i++ {
				c := pyCompare(av.items[i], bv.items[i])
				if c != 0 {
					return c
				}
			}
			if len(av.items) < len(bv.items) {
				return -1
			} else if len(av.items) > len(bv.items) {
				return 1
			}
			return 0
		}
	case *PyBytes:
		if bv, ok := b.(*PyBytes); ok {
			for i := 0; i < len(av.v) && i < len(bv.v); i++ {
				if av.v[i] < bv.v[i] {
					return -1
				}
				if av.v[i] > bv.v[i] {
					return 1
				}
			}
			if len(av.v) < len(bv.v) {
				return -1
			} else if len(av.v) > len(bv.v) {
				return 1
			}
			return 0
		}
	}
	raiseTypeError("'%s' not supported between instances of '%s' and '%s'",
		"<", a.pyType().Name, b.pyType().Name)
	return 0
}

// hashKey returns a comparable Go value for dict/set operations.
func hashKey(obj Object) (any, error) {
	switch v := obj.(type) {
	case *PyNone:
		return nil, nil
	case *PyBool:
		if v.v {
			return int64(1), nil
		}
		return int64(0), nil
	case *PyInt:
		if v.big == nil {
			return v.small, nil
		}
		return v.big.String(), nil
	case *PyFloat:
		// If float is integer-valued, use the int key for consistency
		if v.v == float64(int64(v.v)) {
			return int64(v.v), nil
		}
		return v.v, nil
	case *PyStr:
		return v.v, nil
	case *PyBytes:
		return string(v.v), nil
	case *PyTuple:
		// Use a string encoding for tuples
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			k, err := hashKey(item)
			if err != nil {
				return nil, err
			}
			parts[i] = fmt.Sprintf("%T:%v", k, k)
		}
		return "tuple:" + strings.Join(parts, ","), nil
	case *PyList:
		return nil, fmt.Errorf("unhashable type: 'list'")
	case *PyDict:
		return nil, fmt.Errorf("unhashable type: 'dict'")
	case *PySet:
		return nil, fmt.Errorf("unhashable type: 'set'")
	case *PyClass:
		return fmt.Sprintf("class:%p", v), nil
	case *PyInstance:
		return fmt.Sprintf("instance:%p", v), nil
	case *PyFunction:
		return fmt.Sprintf("function:%p", v), nil
	case *PyBuiltin:
		return fmt.Sprintf("builtin:%p", v), nil
	}
	return fmt.Sprintf("obj:%p", obj), nil
}

// raiseTypeError panics with a TypeError.
func raiseTypeError(msg string, a ...interface{}) {
	panic(exceptionSignal{exc: newExceptionf(ExcTypeError, msg, a...)})
}

// raiseValueError panics with a ValueError.
func raiseValueError(msg string, a ...interface{}) {
	panic(exceptionSignal{exc: newExceptionf(ExcValueError, msg, a...)})
}

// raiseAttributeError panics with AttributeError.
func raiseAttributeError(typeName, attr string) {
	panic(exceptionSignal{exc: newExceptionf(ExcAttributeError, "'%s' object has no attribute '%s'", typeName, attr)})
}

// raiseIndexError panics with IndexError.
func raiseIndexError(msg string) {
	panic(exceptionSignal{exc: newExceptionf(ExcIndexError, "%s", msg)})
}

// raiseKeyError panics with KeyError for a key object.
func raiseKeyError(key Object) {
	panic(exceptionSignal{exc: newException(ExcKeyError, key)})
}

// raiseNameError panics with NameError.
func raiseNameError(name string) {
	panic(exceptionSignal{exc: newExceptionf(ExcNameError, "name '%s' is not defined", name)})
}

// normalizeIndex handles Python's negative indexing.
func normalizeIndex(i, length int) int {
	if i < 0 {
		i += length
	}
	return i
}

// toNumber converts obj to a numeric type for arithmetic.
func toNumber(obj Object) (Object, bool) {
	switch obj.(type) {
	case *PyInt, *PyFloat, *PyBool:
		return obj, true
	}
	return nil, false
}

// toIntVal extracts an int64 from a PyInt or PyBool.
// If the value is a big integer that does not fit in int64 it raises
// IndexError (matching CPython's "cannot fit 'int' into an index-sized integer").
func toIntVal(obj Object) int64 {
	switch v := obj.(type) {
	case *PyInt:
		if n, ok := v.int64(); ok {
			return n
		}
		panic(exceptionSignal{exc: newExceptionf(ExcIndexError, "cannot fit 'int' into an index-sized integer")})
	case *PyBool:
		if v.v {
			return 1
		}
		return 0
	case *PyFloat:
		return int64(v.v)
	}
	raiseTypeError("expected int, got %s", obj.pyType().Name)
	return 0
}

// toIntValBig extracts a *big.Int from a PyInt, PyBool, or PyFloat.
// Unlike toIntVal it never truncates big integers.
func toIntValBig(obj Object) *big.Int {
	switch v := obj.(type) {
	case *PyInt:
		return v.toBigInt()
	case *PyBool:
		if v.v {
			return big.NewInt(1)
		}
		return big.NewInt(0)
	case *PyFloat:
		return new(big.Int).SetInt64(int64(v.v))
	}
	raiseTypeError("expected int, got %s", obj.pyType().Name)
	return nil
}

// collectIterable collects all items from an iterable into a slice.
func collectIterable(obj Object) []Object {
	switch v := obj.(type) {
	case *PyList:
		result := make([]Object, len(v.items))
		copy(result, v.items)
		return result
	case *PyTuple:
		result := make([]Object, len(v.items))
		copy(result, v.items)
		return result
	case *PyStr:
		runes := []rune(v.v)
		result := make([]Object, len(runes))
		for i, r := range runes {
			result[i] = pyStr(string(r))
		}
		return result
	case *PyBytes:
		result := make([]Object, len(v.v))
		for i, b := range v.v {
			result[i] = pyInt(int64(b))
		}
		return result
	case *PyRange:
		n := v.length()
		// Guard against huge range lengths (e.g. list(range(0, 1<<62))) that
		// would cause make([]Object, n) to panic with "makeslice: len out of range".
		if n > maxGeneratorItems {
			panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "range too large to materialize (length %d exceeds limit %d)", n, maxGeneratorItems)})
		}
		result := make([]Object, n)
		cur := v.start
		for i := int64(0); i < n; i++ {
			result[i] = pyInt(cur)
			cur += v.step
		}
		return result
	case *PyDict:
		result := make([]Object, len(v.keys))
		copy(result, v.keys)
		return result
	case *PySet:
		result := make([]Object, 0, len(v.items))
		for _, item := range v.items {
			result = append(result, item)
		}
		return result
	case *PyFrozenSet:
		result := make([]Object, 0, len(v.items))
		for _, item := range v.items {
			result = append(result, item)
		}
		return result
	case *PyGenerator:
		return drainGenerator(v)
	case *rangeIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyMapIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyFilterIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyZipIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyEnumerateIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	case *PyReversedIter:
		var result []Object
		for {
			item, ok := v.next()
			if !ok {
				break
			}
			result = append(result, item)
		}
		return result
	}
	raiseTypeError("'%s' object is not iterable", obj.pyType().Name)
	return nil
}

// maxGeneratorItems is the maximum number of items drainGenerator will collect
// from an infinite generator before raising MemoryError (~128k items at 8 bytes each = 1 MiB).
const maxGeneratorItems = 1 << 17 // 128k items

// drainGenerator collects all values from a generator, respecting context
// cancellation and capping the result at maxGeneratorItems to prevent OOM.
func drainGenerator(g *PyGenerator) []Object {
	var result []Object
	ctx := g.ctx
	for !g.done {
		if g.awaitingSend {
			select {
			case g.sendCh <- pyNone:
			case <-ctx.Done():
				g.done = true
				// The generator may be blocked on yieldCh <- val; drain it so the
				// goroutine can exit rather than leaking.
				select {
				case <-g.yieldCh:
				default:
				}
				panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "")})
			}
			g.awaitingSend = false
		}
		select {
		case val, ok := <-g.yieldCh:
			if !ok {
				g.done = true
				return result
			}
			g.awaitingSend = true
			result = append(result, val)
			if len(result) > maxGeneratorItems {
				g.done = true
				panic(exceptionSignal{exc: newExceptionf(ExcMemoryError, "generator produced too many items (limit %d)", maxGeneratorItems)})
			}
		case <-ctx.Done():
			g.done = true
			// Non-blocking drain: if the generator goroutine is blocked on
			// yieldCh <- val, receive that value so the goroutine can observe
			// g.done == true (or ctx.Done()) and exit rather than hanging forever.
			select {
			case <-g.yieldCh:
			default:
			}
			panic(exceptionSignal{exc: newExceptionf(ExcKeyboardInterrupt, "")})
		}
	}
	return result
}

// nextFromIterable returns the next item from an iterable object.
// Returns (val, true) or (nil, false) at exhaustion.
func nextFromIterable(obj Object) (Object, bool) {
	switch v := obj.(type) {
	case *rangeIter:
		return v.next()
	case *PyMapIter:
		return v.next()
	case *PyFilterIter:
		return v.next()
	case *PyZipIter:
		return v.next()
	case *PyEnumerateIter:
		return v.next()
	case *PyReversedIter:
		return v.next()
	case *PyGenerator:
		if v.done {
			return nil, false
		}
		if v.awaitingSend {
			v.sendCh <- pyNone
			v.awaitingSend = false
		}
		val, ok := <-v.yieldCh
		if !ok {
			v.done = true
			// Check if the generator exited due to a non-StopIteration exception
			// and propagate it to the caller.
			if v.excCh != nil {
				select {
				case exc := <-v.excCh:
					panic(exceptionSignal{exc: exc})
				default:
				}
			}
			return nil, false
		}
		v.awaitingSend = true
		return val, true
	case *PyList:
		// Not really an iterator, but handle via index
		return nil, false
	}
	return nil, false
}

// sortList sorts a list in place using an optional key function.
func sortList(items []Object, keyFn Object, reverse bool) {
	// Simple insertion sort (stable, correct for small lists)
	getKey := func(item Object) Object {
		if keyFn == nil {
			return item
		}
		return callObject(keyFn, []Object{item}, nil)
	}
	for i := 1; i < len(items); i++ {
		cur := items[i] // save before inner loop shifts elements
		key := getKey(cur)
		j := i
		for j > 0 && func() bool {
			c := pyCompare(getKey(items[j-1]), key)
			if reverse {
				return c < 0
			}
			return c > 0
		}() {
			items[j] = items[j-1]
			j--
		}
		items[j] = cur
	}
}

// mustStr extracts a string from an Object or raises TypeError.
func mustStr(obj Object, fnName string) string {
	switch v := obj.(type) {
	case *PyStr:
		return v.v
	}
	raiseTypeError("%s() argument must be str, not '%s'", fnName, obj.pyType().Name)
	return ""
}

// ---- Lazy iterator types ----

// PyMapIter is a lazy map() iterator.
type PyMapIter struct {
	fn    Object
	iters []Object // underlying iterators (as list slices for simplicity)
	idx   int
	items [][]Object // pre-collected for each iterable
}

func (m *PyMapIter) pyType() *PyType { return typeMapIter }
func (m *PyMapIter) pyRepr() string  { return "<map object>" }
func (m *PyMapIter) pyStr() string   { return m.pyRepr() }

func (m *PyMapIter) next() (Object, bool) {
	if m.idx >= len(m.items[0]) {
		return nil, false
	}
	args := make([]Object, len(m.items))
	for i, items := range m.items {
		if m.idx >= len(items) {
			return nil, false
		}
		args[i] = items[m.idx]
	}
	m.idx++
	result := callObject(m.fn, args, nil)
	return result, true
}

// PyFilterIter is a lazy filter() iterator.
type PyFilterIter struct {
	fn    Object // nil means filter by truth
	items []Object
	idx   int
}

func (f *PyFilterIter) pyType() *PyType { return typeFilterIter }
func (f *PyFilterIter) pyRepr() string  { return "<filter object>" }
func (f *PyFilterIter) pyStr() string   { return f.pyRepr() }

func (f *PyFilterIter) next() (Object, bool) {
	for f.idx < len(f.items) {
		item := f.items[f.idx]
		f.idx++
		if f.fn == nil || f.fn == pyNone {
			if pyTruth(item) {
				return item, true
			}
		} else {
			result := callObject(f.fn, []Object{item}, nil)
			if pyTruth(result) {
				return item, true
			}
		}
	}
	return nil, false
}

// PyZipIter is a lazy zip() iterator.
type PyZipIter struct {
	items [][]Object
	idx   int
}

func (z *PyZipIter) pyType() *PyType { return typeZipIter }
func (z *PyZipIter) pyRepr() string  { return "<zip object>" }
func (z *PyZipIter) pyStr() string   { return z.pyRepr() }

func (z *PyZipIter) next() (Object, bool) {
	if len(z.items) == 0 {
		return nil, false
	}
	for _, items := range z.items {
		if z.idx >= len(items) {
			return nil, false
		}
	}
	tuple := make([]Object, len(z.items))
	for i, items := range z.items {
		tuple[i] = items[z.idx]
	}
	z.idx++
	return pyTuple(tuple), true
}

// PyEnumerateIter is a lazy enumerate() iterator.
type PyEnumerateIter struct {
	items   []Object
	idx     int
	counter int64
}

func (e *PyEnumerateIter) pyType() *PyType { return typeEnumerateIter }
func (e *PyEnumerateIter) pyRepr() string  { return "<enumerate object>" }
func (e *PyEnumerateIter) pyStr() string   { return e.pyRepr() }

func (e *PyEnumerateIter) next() (Object, bool) {
	if e.idx >= len(e.items) {
		return nil, false
	}
	val := pyTuple([]Object{pyInt(e.counter), e.items[e.idx]})
	e.idx++
	e.counter++
	return val, true
}

// PyReversedIter is a reversed iterator.
type PyReversedIter struct {
	items []Object
	idx   int
}

func (r *PyReversedIter) pyType() *PyType { return typeReversedIter }
func (r *PyReversedIter) pyRepr() string  { return "<list_reverseiterator object>" }
func (r *PyReversedIter) pyStr() string   { return r.pyRepr() }

func (r *PyReversedIter) next() (Object, bool) {
	if r.idx < 0 {
		return nil, false
	}
	val := r.items[r.idx]
	r.idx--
	return val, true
}

// PyListIter is a forward list iterator.
type PyListIter struct {
	items []Object
	idx   int
}

func (r *PyListIter) pyType() *PyType { return typeList }
func (r *PyListIter) pyRepr() string  { return "<list_iterator object>" }
func (r *PyListIter) pyStr() string   { return r.pyRepr() }

func (r *PyListIter) next() (Object, bool) {
	if r.idx >= len(r.items) {
		return nil, false
	}
	val := r.items[r.idx]
	r.idx++
	return val, true
}

// PyDictKeyIter iterates over dict keys.
type PyDictKeyIter struct {
	keys []Object
	idx  int
}

func (d *PyDictKeyIter) pyType() *PyType { return typeDict }
func (d *PyDictKeyIter) pyRepr() string  { return "<dict_keyiterator object>" }
func (d *PyDictKeyIter) pyStr() string   { return d.pyRepr() }

func (d *PyDictKeyIter) next() (Object, bool) {
	if d.idx >= len(d.keys) {
		return nil, false
	}
	val := d.keys[d.idx]
	d.idx++
	return val, true
}
