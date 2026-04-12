// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package python

import (
	"bufio"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ---- Module registry ----

type moduleFactory func(opts *RunOpts) *PyModule

var moduleRegistry map[string]moduleFactory

func init() {
	moduleRegistry = map[string]moduleFactory{
		"sys":      makeSysModule,
		"math":     makeMathModule,
		"os":       makeOsModule,
		"binascii": makeBinasciModule,
		"string":   makeStringModule,
		// Blocked modules
		"tempfile":        makeBlockedModule("tempfile"),
		"glob":            makeBlockedModule("glob"),
		"subprocess":      makeBlockedModule("subprocess"),
		"socket":          makeBlockedModule("socket"),
		"ctypes":          makeBlockedModule("ctypes"),
		"multiprocessing": makeBlockedModule("multiprocessing"),
		"threading":       makeBlockedModule("threading"),
		"asyncio":         makeBlockedModule("asyncio"),
	}
}

func makeBlockedModule(name string) moduleFactory {
	return func(_ *RunOpts) *PyModule {
		panic(exceptionSignal{exc: newExceptionf(ExcImportError, "module %q is not available in this shell", name)})
	}
}

// loadModule returns (module, found). Panics with ImportError if found but blocked.
func loadModule(name string, opts *RunOpts) (*PyModule, bool) {
	factory, ok := moduleRegistry[name]
	if !ok {
		return nil, false
	}
	mod := factory(opts) // may panic for blocked modules
	return mod, true
}

// goosToSysPlatform converts a runtime.GOOS value to the string that Python's
// sys.platform reports on each OS. This matches CPython behaviour.
func goosToSysPlatform(goos string) string {
	switch goos {
	case "darwin":
		return "darwin"
	case "windows":
		return "win32"
	default:
		return "linux"
	}
}

// ---- sys module ----

func makeSysModule(opts *RunOpts) *PyModule {
	argv := make([]Object, 0, 1+len(opts.Args))
	argv = append(argv, pyStr(opts.SourceName))
	for _, a := range opts.Args {
		argv = append(argv, pyStr(a))
	}

	sysMod := &PyModule{Name: "sys", Dict: map[string]Object{
		"argv":         pyList(argv),
		"stdout":       &PyFile{w: opts.Stdout, name: "<stdout>"},
		"stderr":       &PyFile{w: opts.Stderr, name: "<stderr>"},
		"stdin":        nil, // set below
		"version":      pyStr("3.12.0 (rshell custom interpreter)"),
		"version_info": pyTuple([]Object{pyInt(3), pyInt(12), pyInt(0), pyStr("final"), pyInt(0)}),
		"platform":     pyStr(goosToSysPlatform(runtime.GOOS)),
		"path":         pyList([]Object{}),
		"modules":      pyDict(),
		"maxsize":      pyInt(int64(^uint(0) >> 1)),
		"exit":         nil, // set below
		"__name__":     pyStr("sys"),
	}}

	// stdin
	if opts.Stdin != nil {
		sysMod.Dict["stdin"] = &PyFile{r: bufio.NewReader(opts.Stdin), name: "<stdin>"}
	} else {
		sysMod.Dict["stdin"] = &PyFile{r: bufio.NewReader(strings.NewReader("")), name: "<stdin>"}
	}

	// sys.exit
	sysMod.Dict["exit"] = makeBuiltin("exit", func(args []Object, kwargs map[string]Object) Object {
		code := 0
		if len(args) > 0 {
			switch v := args[0].(type) {
			case *PyInt:
				if n, ok := v.int64(); ok {
					code = int(n)
				} else {
					code = 1
				}
			case *PyNone:
				code = 0
			case *PyBool:
				if v.v {
					code = 1
				}
			default:
				// Print message to stderr and exit 1
				fmt.Fprint(opts.Stderr, args[0].pyStr()+"\n")
				code = 1
			}
		}
		panic(controlSignal{kind: ctrlSysExit, value: pyInt(int64(code))})
	})

	return sysMod
}

// ---- math module ----

func makeMathModule(_ *RunOpts) *PyModule {
	wrapF := func(name string, fn func(float64) float64) *PyBuiltin {
		return makeBuiltin(name, func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("%s() takes exactly 1 argument (%d given)", name, len(args))
			}
			return pyFloat(fn(toFloat(args[0])))
		})
	}

	wrapF2 := func(name string, fn func(float64, float64) float64) *PyBuiltin {
		return makeBuiltin(name, func(args []Object, _ map[string]Object) Object {
			if len(args) != 2 {
				raiseTypeError("%s() takes exactly 2 arguments (%d given)", name, len(args))
			}
			return pyFloat(fn(toFloat(args[0]), toFloat(args[1])))
		})
	}

	return &PyModule{Name: "math", Dict: map[string]Object{
		"floor": makeBuiltin("floor", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("floor() takes exactly 1 argument")
			}
			return pyInt(int64(math.Floor(toFloat(args[0]))))
		}),
		"ceil": makeBuiltin("ceil", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("ceil() takes exactly 1 argument")
			}
			return pyInt(int64(math.Ceil(toFloat(args[0]))))
		}),
		"sqrt":  wrapF("sqrt", math.Sqrt),
		"log":   makeBuiltin("log", mathLog),
		"log2":  wrapF("log2", math.Log2),
		"log10": wrapF("log10", math.Log10),
		"sin":   wrapF("sin", math.Sin),
		"cos":   wrapF("cos", math.Cos),
		"tan":   wrapF("tan", math.Tan),
		"asin":  wrapF("asin", math.Asin),
		"acos":  wrapF("acos", math.Acos),
		"atan":  wrapF("atan", math.Atan),
		"atan2": wrapF2("atan2", math.Atan2),
		"exp":   wrapF("exp", math.Exp),
		"pow":   wrapF2("pow", math.Pow),
		"fabs":  wrapF("fabs", math.Abs),
		"isnan": makeBuiltin("isnan", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("isnan() takes exactly 1 argument")
			}
			return pyBool(math.IsNaN(toFloat(args[0])))
		}),
		"isinf": makeBuiltin("isinf", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("isinf() takes exactly 1 argument")
			}
			return pyBool(math.IsInf(toFloat(args[0]), 0))
		}),
		"isfinite": makeBuiltin("isfinite", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("isfinite() takes exactly 1 argument")
			}
			f := toFloat(args[0])
			return pyBool(!math.IsNaN(f) && !math.IsInf(f, 0))
		}),
		"trunc": makeBuiltin("trunc", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("trunc() takes exactly 1 argument")
			}
			return pyInt(int64(math.Trunc(toFloat(args[0]))))
		}),
		"gcd":       makeBuiltin("gcd", mathGcd),
		"factorial": makeBuiltin("factorial", mathFactorial),
		"hypot":     wrapF2("hypot", math.Hypot),
		"degrees": wrapF("degrees", func(r float64) float64 {
			return r * 180 / math.Pi
		}),
		"radians": wrapF("radians", func(d float64) float64 {
			return d * math.Pi / 180
		}),
		"pi":  pyFloat(math.Pi),
		"e":   pyFloat(math.E),
		"tau": pyFloat(2 * math.Pi),
		"inf": pyFloat(math.Inf(1)),
		"nan": pyFloat(math.NaN()),
		"fsum": makeBuiltin("fsum", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("fsum() takes exactly 1 argument")
			}
			items := collectIterable(args[0])
			sum := 0.0
			for _, item := range items {
				sum += toFloat(item)
			}
			return pyFloat(sum)
		}),
		"comb": makeBuiltin("comb", func(args []Object, _ map[string]Object) Object {
			if len(args) != 2 {
				raiseTypeError("comb() takes exactly 2 arguments")
			}
			n := toIntVal(args[0])
			k := toIntVal(args[1])
			if k < 0 || k > n {
				return pyInt(0)
			}
			// C(n, k) = n! / (k! * (n-k)!)
			result := big.NewInt(1)
			for i := int64(0); i < k; i++ {
				result.Mul(result, big.NewInt(n-i))
				result.Div(result, big.NewInt(i+1))
			}
			return pyIntBig(result)
		}),
		"perm": makeBuiltin("perm", func(args []Object, _ map[string]Object) Object {
			if len(args) < 1 || len(args) > 2 {
				raiseTypeError("perm() takes 1 or 2 arguments")
			}
			n := toIntVal(args[0])
			k := n
			if len(args) == 2 && args[1] != pyNone {
				k = toIntVal(args[1])
			}
			if k < 0 || k > n {
				return pyInt(0)
			}
			result := big.NewInt(1)
			for i := int64(0); i < k; i++ {
				result.Mul(result, big.NewInt(n-i))
			}
			return pyIntBig(result)
		}),
	}}
}

func mathLog(args []Object, _ map[string]Object) Object {
	if len(args) < 1 || len(args) > 2 {
		raiseTypeError("log() takes 1 or 2 arguments (%d given)", len(args))
	}
	x := toFloat(args[0])
	if len(args) == 1 {
		return pyFloat(math.Log(x))
	}
	base := toFloat(args[1])
	return pyFloat(math.Log(x) / math.Log(base))
}

func mathGcd(args []Object, _ map[string]Object) Object {
	if len(args) < 2 {
		raiseTypeError("gcd() takes at least 2 arguments")
	}
	a := new(big.Int).Abs(toIntValObj(args[0]))
	for _, arg := range args[1:] {
		b := new(big.Int).Abs(toIntValObj(arg))
		a.GCD(nil, nil, a, b)
	}
	return pyIntBig(a)
}

func mathFactorial(args []Object, _ map[string]Object) Object {
	if len(args) != 1 {
		raiseTypeError("factorial() takes exactly 1 argument")
	}
	n := toIntVal(args[0])
	if n < 0 {
		raiseValueError("factorial() not defined for negative values")
	}
	if n > 10000 {
		raiseValueError("factorial() argument is too large")
	}
	result := big.NewInt(1)
	for i := int64(2); i <= n; i++ {
		result.Mul(result, big.NewInt(i))
	}
	return pyIntBig(result)
}

// ---- os module ----

func makeOsModule(opts *RunOpts) *PyModule {
	osPath := makeOsPathModule(opts)

	linesep := "\n"
	osName := "posix"
	if runtime.GOOS == "windows" {
		linesep = "\r\n"
		osName = "nt"
	}

	return &PyModule{Name: "os", Dict: map[string]Object{
		"path":    osPath,
		"environ": pyDict(), // empty — Python must not access the host process environment
		"getenv": makeBuiltin("getenv", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("getenv() missing required argument: 'key'")
			}
			// Always return the default — Python must not access the host process environment.
			if len(args) >= 2 {
				return args[1]
			}
			return pyNone
		}),
		"listdir": makeBuiltin("listdir", func(args []Object, _ map[string]Object) Object {
			dir := "."
			if len(args) > 0 {
				dir = mustStr(args[0], "listdir")
			}
			entries, err := opts.ReadDir(opts.Ctx, dir)
			if err != nil {
				raiseOSError(err.Error())
			}
			items := make([]Object, len(entries))
			for i, e := range entries {
				items[i] = pyStr(e.Name())
			}
			return pyList(items)
		}),
		"sep":     pyStr(string(filepath.Separator)),
		"linesep": pyStr(linesep),
		"curdir":  pyStr("."),
		"pardir":  pyStr(".."),
		"name":    pyStr(osName),
		"devnull": pyStr(os.DevNull),
		"error":   ExcOSError,
		// Dangerous functions intentionally absent
	}}
}

func makeOsPathModule(opts *RunOpts) *PyModule {
	return &PyModule{Name: "os.path", Dict: map[string]Object{
		"join":     makeBuiltin("join", osPathJoin),
		"exists":   makeBuiltin("exists", makeOsPathExists(opts)),
		"isfile":   makeBuiltin("isfile", makeOsPathIsFile(opts)),
		"isdir":    makeBuiltin("isdir", makeOsPathIsDir(opts)),
		"dirname":  makeBuiltin("dirname", osPathDirname),
		"basename": makeBuiltin("basename", osPathBasename),
		"splitext": makeBuiltin("splitext", osPathSplitext),
		"split":    makeBuiltin("split", osPathSplit),
		"sep":      pyStr(string(filepath.Separator)),
		"curdir":   pyStr("."),
		"pardir":   pyStr(".."),
		"extsep":   pyStr("."),
		"pathsep":  pyStr(string(filepath.ListSeparator)),
		"normpath": makeBuiltin("normpath", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("normpath() takes exactly 1 argument")
			}
			return pyStr(filepath.Clean(mustStr(args[0], "normpath")))
		}),
		// abspath and realpath are intentionally absent: both call filepath.Abs
		// which reads the host process CWD via os.Getwd, leaking the host path.
		// This matches the policy that blocked os.getcwd() (commit f5235f88).
	}}
}

func osPathJoin(args []Object, _ map[string]Object) Object {
	if len(args) == 0 {
		raiseTypeError("join() requires at least 1 argument")
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = mustStr(arg, "join")
	}
	return pyStr(filepath.Join(parts...))
}

func makeOsPathExists(opts *RunOpts) func([]Object, map[string]Object) Object {
	return func(args []Object, _ map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("exists() takes exactly 1 argument")
		}
		path := mustStr(args[0], "exists")
		_, err := opts.Stat(opts.Ctx, path)
		return pyBool(err == nil)
	}
}

func makeOsPathIsFile(opts *RunOpts) func([]Object, map[string]Object) Object {
	return func(args []Object, _ map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("isfile() takes exactly 1 argument")
		}
		path := mustStr(args[0], "isfile")
		info, err := opts.Stat(opts.Ctx, path)
		if err != nil {
			return pyFalse
		}
		return pyBool(!info.IsDir())
	}
}

func makeOsPathIsDir(opts *RunOpts) func([]Object, map[string]Object) Object {
	return func(args []Object, _ map[string]Object) Object {
		if len(args) != 1 {
			raiseTypeError("isdir() takes exactly 1 argument")
		}
		path := mustStr(args[0], "isdir")
		info, err := opts.Stat(opts.Ctx, path)
		if err != nil {
			return pyFalse
		}
		return pyBool(info.IsDir())
	}
}

func osPathDirname(args []Object, _ map[string]Object) Object {
	if len(args) != 1 {
		raiseTypeError("dirname() takes exactly 1 argument")
	}
	return pyStr(filepath.Dir(mustStr(args[0], "dirname")))
}

func osPathBasename(args []Object, _ map[string]Object) Object {
	if len(args) != 1 {
		raiseTypeError("basename() takes exactly 1 argument")
	}
	return pyStr(filepath.Base(mustStr(args[0], "basename")))
}

func osPathSplitext(args []Object, _ map[string]Object) Object {
	if len(args) != 1 {
		raiseTypeError("splitext() takes exactly 1 argument")
	}
	p := mustStr(args[0], "splitext")
	ext := filepath.Ext(p)
	base := p[:len(p)-len(ext)]
	return pyTuple([]Object{pyStr(base), pyStr(ext)})
}

func osPathSplit(args []Object, _ map[string]Object) Object {
	if len(args) != 1 {
		raiseTypeError("split() takes exactly 1 argument")
	}
	p := mustStr(args[0], "split")
	dir := filepath.Dir(p)
	base := filepath.Base(p)
	return pyTuple([]Object{pyStr(dir), pyStr(base)})
}

// ---- binascii module ----

func makeBinasciModule(_ *RunOpts) *PyModule {
	return &PyModule{Name: "binascii", Dict: map[string]Object{
		"hexlify": makeBuiltin("hexlify", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("hexlify() takes exactly 1 argument")
			}
			b := mustBytes(args[0], "hexlify")
			return pyBytes([]byte(hex.EncodeToString(b)))
		}),
		"unhexlify": makeBuiltin("unhexlify", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("unhexlify() takes exactly 1 argument")
			}
			var s string
			switch v := args[0].(type) {
			case *PyStr:
				s = v.v
			case *PyBytes:
				s = string(v.v)
			default:
				raiseTypeError("unhexlify() argument must be str or bytes-like object")
			}
			b, err := hex.DecodeString(s)
			if err != nil {
				raiseValueError("Non-hexadecimal digit found")
			}
			return pyBytes(b)
		}),
		"b2a_base64": makeBuiltin("b2a_base64", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("b2a_base64() takes exactly 1 argument")
			}
			b := mustBytes(args[0], "b2a_base64")
			encoded := base64.StdEncoding.EncodeToString(b) + "\n"
			return pyBytes([]byte(encoded))
		}),
		"a2b_base64": makeBuiltin("a2b_base64", func(args []Object, _ map[string]Object) Object {
			if len(args) != 1 {
				raiseTypeError("a2b_base64() takes exactly 1 argument")
			}
			var s string
			switch v := args[0].(type) {
			case *PyStr:
				s = v.v
			case *PyBytes:
				s = string(v.v)
			default:
				raiseTypeError("a2b_base64() argument must be str or bytes-like object")
			}
			s = strings.TrimSpace(s)
			b, err := base64.StdEncoding.DecodeString(s)
			if err != nil {
				b, err = base64.RawStdEncoding.DecodeString(s)
				if err != nil {
					raiseValueError("Invalid base64-encoded string: %v", err)
				}
			}
			return pyBytes(b)
		}),
		"crc32": makeBuiltin("crc32", func(args []Object, _ map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("crc32() takes at least 1 argument")
			}
			b := mustBytes(args[0], "crc32")
			var init uint32
			if len(args) > 1 {
				init = uint32(toIntVal(args[1]))
			}
			checksum := crc32.Update(init, crc32.IEEETable, b)
			return pyInt(int64(checksum))
		}),
		"Error": ExcOSError, // binascii.Error = OSError
	}}
}

// b2a_hex and a2b_hex are aliases
func init() {
	// These aliases need the registry to exist, so we add them in init after makeBinasciModule is available
}

// ---- string module ----

func makeStringModule(_ *RunOpts) *PyModule {
	printable := ""
	for i := 32; i < 127; i++ {
		printable += string(rune(i))
	}

	return &PyModule{Name: "string", Dict: map[string]Object{
		"ascii_letters":   pyStr("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"),
		"ascii_lowercase": pyStr("abcdefghijklmnopqrstuvwxyz"),
		"ascii_uppercase": pyStr("ABCDEFGHIJKLMNOPQRSTUVWXYZ"),
		"digits":          pyStr("0123456789"),
		"hexdigits":       pyStr("0123456789abcdefABCDEF"),
		"octdigits":       pyStr("01234567"),
		"punctuation":     pyStr(`!"#$%&'()*+,-./:;<=>?@[\]^_` + "`{|}~"),
		"whitespace":      pyStr(" \t\n\r\x0b\x0c"),
		"printable":       pyStr(printable),
		"Formatter": makeBuiltin("Formatter", func(args []Object, kwargs map[string]Object) Object {
			raiseTypeError("string.Formatter is not implemented in this shell")
			return nil
		}),
		"Template": makeBuiltin("Template", func(args []Object, kwargs map[string]Object) Object {
			raiseTypeError("string.Template is not implemented in this shell")
			return nil
		}),
		"capwords": makeBuiltin("capwords", func(args []Object, kwargs map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("capwords() requires at least 1 argument")
			}
			s := mustStr(args[0], "capwords")
			sep := " "
			if len(args) > 1 && args[1] != pyNone {
				sep = mustStr(args[1], "capwords")
			}
			words := strings.Split(s, sep)
			for i, w := range words {
				if len(w) > 0 {
					words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
				}
			}
			return pyStr(strings.Join(words, sep))
		}),
	}}
}

// ---- Helper functions ----

// toFloat converts an Object to a float64.
func toFloat(obj Object) float64 {
	switch v := obj.(type) {
	case *PyFloat:
		return v.v
	case *PyInt:
		if n, ok := v.int64(); ok {
			return float64(n)
		}
		f, _ := new(big.Float).SetInt(v.big).Float64()
		return f
	case *PyBool:
		if v.v {
			return 1
		}
		return 0
	}
	raiseTypeError("must be real number, not '%s'", obj.pyType().Name)
	return 0
}

// mustBytes extracts bytes from an Object or raises TypeError.
func mustBytes(obj Object, fnName string) []byte {
	switch v := obj.(type) {
	case *PyBytes:
		return v.v
	}
	raiseTypeError("%s() argument must be bytes-like object, not '%s'", fnName, obj.pyType().Name)
	return nil
}

// raiseOSError panics with an OSError.
func raiseOSError(msg string) {
	panic(exceptionSignal{exc: newExceptionf(ExcOSError, "%s", msg)})
}

// ---- re module (stub) ----

func makeReModule(_ *RunOpts) *PyModule {
	return &PyModule{Name: "re", Dict: map[string]Object{
		"compile": makeBuiltin("compile", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("re module is not implemented in this shell")
			return nil
		}),
		"match": makeBuiltin("match", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("re module is not implemented in this shell")
			return nil
		}),
		"search": makeBuiltin("search", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("re module is not implemented in this shell")
			return nil
		}),
		"findall": makeBuiltin("findall", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("re module is not implemented in this shell")
			return nil
		}),
		"sub": makeBuiltin("sub", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("re module is not implemented in this shell")
			return nil
		}),
	}}
}

// ---- json module (stub) ----

func makeJsonModule(_ *RunOpts) *PyModule {
	return &PyModule{Name: "json", Dict: map[string]Object{
		"dumps": makeBuiltin("dumps", func(args []Object, _ map[string]Object) Object {
			if len(args) < 1 {
				raiseTypeError("dumps() requires at least 1 argument")
			}
			return pyStr(jsonDumps(args[0]))
		}),
		"loads": makeBuiltin("loads", func(args []Object, _ map[string]Object) Object {
			raiseTypeError("json.loads() is not implemented in this shell")
			return nil
		}),
	}}
}

// jsonDumps converts a Python object to a JSON string.
func jsonDumps(obj Object) string {
	switch v := obj.(type) {
	case *PyNone:
		return "null"
	case *PyBool:
		if v.v {
			return "true"
		}
		return "false"
	case *PyInt:
		return v.pyRepr()
	case *PyFloat:
		return v.pyRepr()
	case *PyStr:
		// Basic JSON string escaping
		var b strings.Builder
		b.WriteByte('"')
		for _, r := range v.v {
			switch r {
			case '"':
				b.WriteString(`\"`)
			case '\\':
				b.WriteString(`\\`)
			case '\n':
				b.WriteString(`\n`)
			case '\r':
				b.WriteString(`\r`)
			case '\t':
				b.WriteString(`\t`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
		return b.String()
	case *PyList:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			parts[i] = jsonDumps(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *PyTuple:
		parts := make([]string, len(v.items))
		for i, item := range v.items {
			parts[i] = jsonDumps(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *PyDict:
		parts := make([]string, len(v.keys))
		for i, k := range v.keys {
			parts[i] = jsonDumps(k) + ": " + jsonDumps(v.vals[i])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return "null"
}

func init() {
	// Add extra modules to the registry
	moduleRegistry["re"] = makeReModule
	moduleRegistry["json"] = makeJsonModule
	moduleRegistry["collections"] = makeCollectionsModule
}

func makeCollectionsModule(_ *RunOpts) *PyModule {
	return &PyModule{Name: "collections", Dict: map[string]Object{
		"OrderedDict": makeBuiltin("OrderedDict", func(args []Object, kwargs map[string]Object) Object {
			// OrderedDict is essentially a regular dict (which is already ordered in Python 3.7+)
			d := pyDict()
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
			return d
		}),
		"defaultdict": makeBuiltin("defaultdict", func(args []Object, kwargs map[string]Object) Object {
			// Simplified: return a regular dict, ignoring the default_factory
			return pyDict()
		}),
		"namedtuple": makeBuiltin("namedtuple", func(args []Object, kwargs map[string]Object) Object {
			raiseTypeError("collections.namedtuple is not implemented in this shell")
			return nil
		}),
		"Counter": makeBuiltin("Counter", func(args []Object, kwargs map[string]Object) Object {
			d := pyDict()
			if len(args) > 0 {
				items := collectIterable(args[0])
				for _, item := range items {
					existing, ok := d.get(item)
					if !ok {
						d.set(item, pyInt(1))
					} else if n, ok2 := existing.(*PyInt); ok2 {
						val, _ := n.int64()
						d.set(item, pyInt(val+1))
					}
				}
			}
			return d
		}),
		"deque": makeBuiltin("deque", func(args []Object, kwargs map[string]Object) Object {
			// Simplified: return a regular list
			if len(args) > 0 {
				items := collectIterable(args[0])
				return pyList(items)
			}
			return pyList(nil)
		}),
	}}
}

// ---- Formatting helpers for fmt.Fprintf using %v ----

func init() {
	// Ensure the fmt package is used
	_ = fmt.Sprintf
}
