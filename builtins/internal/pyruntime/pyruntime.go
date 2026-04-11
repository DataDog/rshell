// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package pyruntime wraps gpython so the python builtin can run sandboxed
// Python 3.4 code.  This package lives under builtins/internal/ and is
// therefore exempt from the builtinAllowedSymbols static-analysis check,
// which lets us freely use the gpython third-party library and blank imports.
//
// # Security sandbox
//
// Every Context created here is stripped of dangerous capabilities before any
// user code runs:
//
//   - os.system, os.popen and all file-system mutation helpers (os.remove,
//     os.mkdir, os.makedirs, os.rmdir, os.removedirs, os.rename, os.link,
//     os.symlink) are deleted from the os module's globals.
//   - The built-in open() is replaced with a read-only version that routes
//     file access through the caller-supplied OpenFile callback (which enforces
//     the AllowedPaths sandbox).  Write and append modes raise PermissionError.
//   - tempfile and glob are blocked at import time: importing them raises
//     ImportError.
//   - sys.stdout and sys.stderr are redirected to the caller-supplied
//     io.Writers so that output is captured by the shell executor.
//   - sys.stdin is redirected to the caller-supplied io.Reader (or set to a
//     no-op reader if nil).
//
// # Context cancellation
//
// Run executes Python in a goroutine and selects on ctx.Done().  If the
// context is cancelled before Python finishes the goroutine is abandoned (it
// will eventually terminate when the process exits or the 30-second executor
// timeout fires).  The abandoned goroutine holds no OS resources after the
// context is cancelled because gpython is pure-Go.
//
// # Memory limits
//
// File reads performed by the sandboxed open() are capped at maxReadBytes
// (1 MiB) to prevent memory exhaustion.  Output written by Python print()
// statements is forwarded to the caller-supplied Stdout without an additional
// cap (the shell executor's 1 MiB output limit applies at a higher level).
package pyruntime

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/go-python/gpython/py"

	// stdlib registers py.NewContext and py.Compile, plus all built-in Python
	// modules (os, sys, math, string, time, tempfile, glob, binascii, marshal).
	// The blank import is required; named symbols are not used here.
	_ "github.com/go-python/gpython/stdlib"
)

// maxReadBytes caps a single open().read() call to prevent memory exhaustion.
const maxReadBytes = 1 << 20 // 1 MiB

// RunOpts configures a single Python execution.
type RunOpts struct {
	// Source is the Python source code to execute.
	Source string

	// SourceName is the name shown in tracebacks (e.g. "<string>", "script.py").
	SourceName string

	// Stdin is Python's sys.stdin reader.  If nil, stdin returns EOF immediately.
	Stdin io.Reader

	// Stdout receives all output from Python print() statements.
	Stdout io.Writer

	// Stderr receives Python tracebacks and error messages.
	Stderr io.Writer

	// Open opens a file for reading within the shell's AllowedPaths sandbox.
	// It must never be nil; the sandbox open() implementation calls it.
	Open func(ctx context.Context, path string, flags int, mode os.FileMode) (io.ReadWriteCloser, error)

	// Args are additional arguments appended to sys.argv after SourceName.
	Args []string
}

// Run executes Python source code in a sandboxed gpython context.
// It blocks until execution completes or ctx is cancelled.
// Returns the Python exit code (0 = success, 1 = unhandled exception,
// N = sys.exit(N)).
func Run(ctx context.Context, opts RunOpts) int {
	type result struct{ code int }
	ch := make(chan result, 1)

	go func() {
		ch <- result{code: runInternal(opts)}
	}()

	select {
	case r := <-ch:
		return r.code
	case <-ctx.Done():
		return 1
	}
}

// runInternal is the synchronous implementation of Run.
func runInternal(opts RunOpts) int {
	pyCtx := py.NewContext(py.ContextOpts{
		SysArgs:  buildArgv(opts.SourceName, opts.Args),
		SysPaths: []string{}, // no module search paths
	})
	defer pyCtx.Close()

	// sysExitCode is set by the sys.exit() override before returning any error.
	// This avoids relying on error type-checking, since gpython wraps Go errors
	// returned from Python builtins inside a SystemError exception.
	var sysExitCode *int

	// Redirect sys streams.
	if err := redirectStreams(pyCtx, opts, &sysExitCode); err != nil {
		fmt.Fprintf(opts.Stderr, "python: failed to redirect streams: %v\n", err)
		return 1
	}

	// Pre-load the os module so we can sandbox it before user code runs.
	// After the first import, gpython caches the module in the context's store,
	// so subsequent "import os" calls in user code return the modified version.
	_ = py.Import(pyCtx, "os")
	if err := sandboxOsModule(pyCtx); err != nil {
		fmt.Fprintf(opts.Stderr, "python: failed to apply os sandbox: %v\n", err)
		return 1
	}

	// Override builtins.open.
	if err := sandboxOpen(pyCtx, opts); err != nil {
		fmt.Fprintf(opts.Stderr, "python: failed to sandbox open(): %v\n", err)
		return 1
	}

	// Block dangerous modules at import time.
	blockModules(pyCtx)

	// Compile and run.  Use ExecMode (not SingleMode) so the VM does not
	// attempt to repr-print intermediate results, which triggers a gpython
	// panic when sys.exit() raises SystemExit with an integer argument.
	code, compileErr := py.Compile(opts.Source+"\n", opts.SourceName, py.ExecMode, 0, true)
	if compileErr != nil {
		return handleRunError(compileErr, opts.Stderr)
	}
	_, runErr := py.RunCode(pyCtx, code, opts.SourceName, nil)
	if runErr == nil {
		return 0
	}

	// sys.exit() sets sysExitCode before returning any error to stop the VM.
	if sysExitCode != nil {
		return *sysExitCode
	}

	return handleRunError(runErr, opts.Stderr)
}

// handleRunError interprets a gpython error and returns an exit code.
func handleRunError(err error, stderr io.Writer) int {
	excInfo, ok := err.(py.ExceptionInfo)
	if !ok {
		fmt.Fprintf(stderr, "python: %v\n", err)
		return 1
	}

	// sys.exit(N) raises SystemExit — handle the gpython native path as well.
	if py.IsException(py.SystemExit, excInfo) {
		return systemExitCode(excInfo)
	}

	// Real Python exception: print the traceback.
	excInfo.TracebackDump(stderr)
	return 1
}

// systemExitCode extracts the integer exit code from a SystemExit exception.
func systemExitCode(excInfo py.ExceptionInfo) int {
	exc, ok := excInfo.Value.(*py.Exception)
	if !ok {
		return 0
	}
	args, ok := exc.Args.(py.Tuple)
	if !ok || len(args) == 0 {
		return 0
	}
	switch v := args[0].(type) {
	case py.Int:
		n, _ := v.GoInt64()
		if n < 0 || n > 255 {
			return 1
		}
		return int(n)
	case py.NoneType:
		return 0
	default:
		// Any non-integer, non-None arg means sys.exit("message") → exit 1.
		return 1
	}
}

// buildArgv constructs sys.argv: [sourceName] + extra args.
func buildArgv(sourceName string, extra []string) []string {
	argv := make([]string, 0, 1+len(extra))
	argv = append(argv, sourceName)
	argv = append(argv, extra...)
	return argv
}

// ---- Stream redirection -----

// redirectStreams replaces sys.stdout, sys.stderr, and sys.stdin in the
// given context with Go-backed Python file objects.  It also overrides
// sys.exit() so the exit code is reliably propagated back to runInternal.
//
// exitCodePtr is a pointer to a *int in runInternal.  The sys.exit() closure
// sets *exitCodePtr before returning an error to stop the VM.  runInternal
// checks *exitCodePtr after py.RunCode returns to recover the exit code
// before the gpython VM can wrap the Go error into a SystemError exception.
func redirectStreams(pyCtx py.Context, opts RunOpts, exitCodePtr **int) error {
	sysMod, err := pyCtx.GetModule("sys")
	if err != nil {
		return err
	}
	sysMod.Globals["stdout"] = &goWriter{w: opts.Stdout}
	sysMod.Globals["__stdout__"] = sysMod.Globals["stdout"]
	sysMod.Globals["stderr"] = &goWriter{w: opts.Stderr}
	sysMod.Globals["__stderr__"] = sysMod.Globals["stderr"]

	var stdin io.Reader = strings.NewReader("") // default: empty stdin
	if opts.Stdin != nil {
		stdin = opts.Stdin
	}
	sysMod.Globals["stdin"] = &goReader{r: bufio.NewReader(stdin)}
	sysMod.Globals["__stdin__"] = sysMod.Globals["stdin"]

	// Override sys.exit() because gpython's built-in sys_exit returns the
	// exception as a Python value rather than raising it, so it never reaches
	// our error handler.  We set *exitCodePtr before returning any error so
	// that runInternal can recover the exit code even after gpython wraps the
	// error into a SystemError exception.
	sysMod.Globals["exit"] = py.MustNewMethod("exit", func(self py.Object, args py.Tuple) (py.Object, error) {
		code := 0
		if len(args) > 0 {
			switch v := args[0].(type) {
			case py.Int:
				n, _ := v.GoInt64()
				code = int(n)
			case py.NoneType:
				code = 0
			default:
				// Any non-integer non-None argument means failure.
				code = 1
			}
		}
		c := code
		*exitCodePtr = &c // store before any error wrapping occurs
		return nil, fmt.Errorf("sys.exit(%d)", code)
	}, 0, "exit(code=0)\n\nExit the interpreter by raising SystemExit(status).")

	return nil
}

// ---- os module sandbox -----

// dangerousOsFuncs are os module functions that must be removed.
var dangerousOsFuncs = []string{
	"system",
	"popen",
	"remove",
	"unlink",
	"mkdir",
	"makedirs",
	"rmdir",
	"removedirs",
	"rename",
	"renames",
	"replace",
	"link",
	"symlink",
	"chmod",
	"chown",
	"chroot",
	"execl",
	"execle",
	"execlp",
	"execlpe",
	"execv",
	"execve",
	"execvp",
	"execvpe",
	"_exit",
	"fork",
	"forkpty",
	"kill",
	"killpg",
	"popen2",
	"popen3",
	"popen4",
	"spawnl",
	"spawnle",
	"spawnlp",
	"spawnlpe",
	"spawnv",
	"spawnve",
	"spawnvp",
	"spawnvpe",
	"startfile",
	"truncate",
	"write",
	"putenv",
	"unsetenv",
}

func sandboxOsModule(pyCtx py.Context) error {
	osMod, err := pyCtx.GetModule("os")
	if err != nil {
		// os module may not be loaded yet; that is fine — it will be blocked
		// at import time by blockModules if needed.
		return nil
	}
	for _, name := range dangerousOsFuncs {
		delete(osMod.Globals, name)
	}
	return nil
}

// ---- open() sandbox -----

// sandboxOpen replaces builtins.open with a read-only version that routes
// file access through the AllowedPaths-aware OpenFile callback.
func sandboxOpen(pyCtx py.Context, opts RunOpts) error {
	builtinsMod, err := pyCtx.GetModule("builtins")
	if err != nil {
		return err
	}
	openFn := makeOpenFunc(opts)
	builtinsMod.Globals["open"] = py.MustNewMethod("open", openFn, 0, sandboxOpenDoc)
	return nil
}

const sandboxOpenDoc = `open(file, mode='r') -> file

Open a file for reading.  Write and append modes are not permitted.`

// makeOpenFunc returns a Python-callable open() implementation.
func makeOpenFunc(opts RunOpts) func(py.Object, py.Tuple, py.StringDict) (py.Object, error) {
	return func(self py.Object, args py.Tuple, kwargs py.StringDict) (py.Object, error) {
		var (
			pyPath py.Object
			pyMode py.Object = py.String("r")
		)
		err := py.ParseTupleAndKeywords(args, kwargs, "O|O:open",
			[]string{"file", "mode"},
			&pyPath, &pyMode)
		if err != nil {
			return nil, err
		}

		path, ok := pyPath.(py.String)
		if !ok {
			return nil, py.ExceptionNewf(py.TypeError, "open() argument 1 must be str, not %s", pyPath.Type().Name)
		}

		mode := "r"
		if pyMode != py.None {
			modeStr, ok := pyMode.(py.String)
			if !ok {
				return nil, py.ExceptionNewf(py.TypeError, "open() mode must be str, not %s", pyMode.Type().Name)
			}
			mode = string(modeStr)
		}

		// Reject any write/append/create modes.
		for _, ch := range mode {
			switch ch {
			case 'w', 'a', 'x', '+':
				return nil, py.ExceptionNewf(py.PermissionError, "open() in write mode is not permitted in this shell")
			}
		}

		// Determine if binary or text mode.
		binary := strings.ContainsRune(mode, 'b')

		// Use a background context for file open — the shell's context
		// cancellation is handled at the Run() level.
		rc, err := opts.Open(context.Background(), string(path), os.O_RDONLY, 0)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, py.ExceptionNewf(py.FileNotFoundError, "%s: No such file or directory", string(path))
			}
			return nil, py.ExceptionNewf(py.OSError, "cannot open %q: %v", string(path), err)
		}

		return &goFile{rc: rc, name: string(path), binary: binary}, nil
	}
}

// ---- blocked modules -----

// blockModules installs stub module impls that raise ImportError when loaded.
func blockModules(pyCtx py.Context) {
	for _, name := range []string{"tempfile", "glob"} {
		blockModule(pyCtx, name)
	}
}

func blockModule(pyCtx py.Context, name string) {
	modName := name // capture for closure
	store := pyCtx.Store()
	impl := &py.ModuleImpl{
		Info: py.ModuleInfo{
			Name: modName,
			Doc:  modName + " is not available in this shell",
		},
		Methods: []*py.Method{},
		Globals: py.StringDict{},
	}
	// Pre-load a broken version: if the module is already in the store under
	// this name, replace it with a version that raises on any attribute access.
	// The simplest approach is to use Python source that raises ImportError.
	impl.CodeSrc = fmt.Sprintf(
		"raise ImportError('module %q is not available in this shell')\n",
		modName,
	)
	// Ignore errors — if the module isn't importable at all, that is also fine.
	_ = store
	pyCtx.ModuleInit(impl) //nolint:errcheck
}

// ---- Python type: GoWriter -----

// goWriterType is the Python type for Go io.Writer-backed file objects.
var goWriterType = py.NewType("GoWriter", "Go io.Writer backed file")

func init() {
	goWriterType.Dict["write"] = py.MustNewMethod("write", func(self py.Object, args py.Tuple) (py.Object, error) {
		gw := self.(*goWriter)
		if len(args) != 1 {
			return nil, py.ExceptionNewf(py.TypeError, "write() takes exactly 1 argument (%d given)", len(args))
		}
		var b []byte
		switch v := args[0].(type) {
		case py.Bytes:
			b = []byte(v)
		case py.String:
			b = []byte(v)
		default:
			return nil, py.ExceptionNewf(py.TypeError, "write() argument must be str or bytes, not %s", args[0].Type().Name)
		}
		n, werr := gw.w.Write(b)
		if werr != nil {
			return nil, py.ExceptionNewf(py.OSError, "write error: %v", werr)
		}
		return py.Int(n), nil
	}, 0, "write(s) -> int\n\nWrite string s to the stream.")

	goWriterType.Dict["flush"] = py.MustNewMethod("flush", func(self py.Object) (py.Object, error) {
		return py.None, nil
	}, 0, "flush()\n\nNo-op flush.")

	goWriterType.Dict["fileno"] = py.MustNewMethod("fileno", func(self py.Object) (py.Object, error) {
		return nil, py.ExceptionNewf(py.NotImplementedError, "fileno() not supported")
	}, 0, "fileno() -> not supported")
}

// goWriter wraps an io.Writer as a Python file object.
type goWriter struct {
	w io.Writer
}

func (g *goWriter) Type() *py.Type { return goWriterType }

// ---- Python type: GoReader -----

// goReaderType is the Python type for Go io.Reader-backed file objects.
var goReaderType = py.NewType("GoReader", "Go io.Reader backed file")

func init() {
	goReaderType.Dict["read"] = py.MustNewMethod("read", func(self py.Object, args py.Tuple) (py.Object, error) {
		gr := self.(*goReader)
		var sizeObj py.Object = py.Int(-1)
		if len(args) > 0 {
			sizeObj = args[0]
		}
		n := -1
		if sz, ok := sizeObj.(py.Int); ok {
			v, _ := sz.GoInt64()
			if v >= 0 {
				n = int(v)
			}
		}
		return gr.read(n)
	}, 0, "read([size]) -> str\n\nRead up to size bytes from stdin.")

	goReaderType.Dict["readline"] = py.MustNewMethod("readline", func(self py.Object, args py.Tuple) (py.Object, error) {
		gr := self.(*goReader)
		line, err := gr.r.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, py.ExceptionNewf(py.OSError, "readline error: %v", err)
		}
		return py.String(line), nil
	}, 0, "readline() -> str\n\nRead one line from stdin.")

	goReaderType.Dict["flush"] = py.MustNewMethod("flush", func(self py.Object) (py.Object, error) {
		return py.None, nil
	}, 0, "flush()\n\nNo-op flush.")
}

// goReader wraps a bufio.Reader as a Python stdin object.
type goReader struct {
	r *bufio.Reader
}

func (g *goReader) Type() *py.Type { return goReaderType }

func (g *goReader) read(n int) (py.Object, error) {
	var buf []byte
	var err error
	if n < 0 {
		buf, err = io.ReadAll(io.LimitReader(g.r, maxReadBytes+1))
		if len(buf) > maxReadBytes {
			return nil, py.ExceptionNewf(py.MemoryError, "stdin input exceeds %d byte limit", maxReadBytes)
		}
	} else {
		if n > maxReadBytes {
			n = maxReadBytes
		}
		buf = make([]byte, n)
		var total int
		for total < n {
			nr, re := g.r.Read(buf[total:])
			total += nr
			if re != nil {
				if errors.Is(re, io.EOF) {
					break
				}
				err = re
				break
			}
		}
		buf = buf[:total]
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, py.ExceptionNewf(py.OSError, "read error: %v", err)
	}
	return py.String(buf), nil
}

// ---- Python type: GoFile (sandboxed read-only file) -----

// goFileType is the Python type for sandboxed read-only file objects returned
// by the overridden open().
var goFileType = py.NewType("GoFile", "sandboxed read-only file object")

func init() {
	goFileType.Dict["read"] = py.MustNewMethod("read", func(self py.Object, args py.Tuple) (py.Object, error) {
		gf := self.(*goFile)
		if gf.closed {
			return nil, py.ExceptionNewf(py.ValueError, "I/O operation on closed file")
		}
		var sizeObj py.Object = py.Int(-1)
		if len(args) > 0 {
			sizeObj = args[0]
		}
		n := -1
		if sz, ok := sizeObj.(py.Int); ok {
			v, _ := sz.GoInt64()
			if v >= 0 {
				n = int(v)
			}
		}
		return gf.read(n)
	}, 0, "read([size]) -> str or bytes")

	goFileType.Dict["readline"] = py.MustNewMethod("readline", func(self py.Object, args py.Tuple) (py.Object, error) {
		gf := self.(*goFile)
		if gf.closed {
			return nil, py.ExceptionNewf(py.ValueError, "I/O operation on closed file")
		}
		if gf.scanner == nil {
			gf.scanner = bufio.NewScanner(gf.rc)
		}
		if gf.scanner.Scan() {
			line := gf.scanner.Text() + "\n"
			if gf.binary {
				return py.Bytes(line), nil
			}
			return py.String(line), nil
		}
		if err := gf.scanner.Err(); err != nil {
			return nil, py.ExceptionNewf(py.OSError, "readline error: %v", err)
		}
		if gf.binary {
			return py.Bytes{}, nil
		}
		return py.String(""), nil
	}, 0, "readline() -> str or bytes")

	goFileType.Dict["readlines"] = py.MustNewMethod("readlines", func(self py.Object, args py.Tuple) (py.Object, error) {
		gf := self.(*goFile)
		if gf.closed {
			return nil, py.ExceptionNewf(py.ValueError, "I/O operation on closed file")
		}
		data, err := io.ReadAll(io.LimitReader(gf.rc, maxReadBytes+1))
		if int64(len(data)) > maxReadBytes {
			return nil, py.ExceptionNewf(py.MemoryError, "file content exceeds %d byte limit", maxReadBytes)
		}
		if err != nil {
			return nil, py.ExceptionNewf(py.OSError, "readlines error: %v", err)
		}
		lines := bytes.SplitAfter(data, []byte("\n"))
		items := make(py.Tuple, 0, len(lines))
		for _, l := range lines {
			if len(l) == 0 {
				continue
			}
			if gf.binary {
				items = append(items, py.Bytes(l))
			} else {
				items = append(items, py.String(l))
			}
		}
		return &py.List{Items: items}, nil
	}, 0, "readlines() -> list")

	goFileType.Dict["close"] = py.MustNewMethod("close", func(self py.Object) (py.Object, error) {
		gf := self.(*goFile)
		if !gf.closed {
			_ = gf.rc.Close()
			gf.closed = true
		}
		return py.None, nil
	}, 0, "close()")

	goFileType.Dict["__enter__"] = py.MustNewMethod("__enter__", func(self py.Object) (py.Object, error) {
		return self, nil
	}, 0, "__enter__()")

	goFileType.Dict["__exit__"] = py.MustNewMethod("__exit__", func(self py.Object, args py.Tuple) (py.Object, error) {
		gf := self.(*goFile)
		if !gf.closed {
			_ = gf.rc.Close()
			gf.closed = true
		}
		return py.False, nil
	}, 0, "__exit__(exc_type, exc_val, exc_tb)")

	goFileType.Dict["name"] = py.MustNewMethod("name", func(self py.Object) (py.Object, error) {
		gf := self.(*goFile)
		return py.String(gf.name), nil
	}, 0, "name of the file")
}

// goFile is a sandboxed read-only file object.
type goFile struct {
	rc      io.ReadWriteCloser
	name    string
	binary  bool
	closed  bool
	buf     []byte // accumulated data for read()
	bufDone bool   // true after all data has been read into buf
	scanner *bufio.Scanner
}

func (g *goFile) Type() *py.Type { return goFileType }

func (g *goFile) read(n int) (py.Object, error) {
	// Lazily read all data into a bounded buffer.
	if !g.bufDone {
		data, err := io.ReadAll(io.LimitReader(g.rc, maxReadBytes+1))
		g.bufDone = true
		if int64(len(data)) > maxReadBytes {
			return nil, py.ExceptionNewf(py.MemoryError, "file content exceeds %d byte limit", maxReadBytes)
		}
		if err != nil {
			return nil, py.ExceptionNewf(py.OSError, "read error: %v", err)
		}
		g.buf = data
	}

	var chunk []byte
	if n < 0 {
		chunk = g.buf
		g.buf = nil
	} else {
		if n > len(g.buf) {
			n = len(g.buf)
		}
		chunk = g.buf[:n]
		g.buf = g.buf[n:]
	}

	if g.binary {
		return py.Bytes(chunk), nil
	}
	return py.String(chunk), nil
}
