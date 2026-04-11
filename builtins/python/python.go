// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package python implements the python builtin command.
//
// python — run Python 3 scripts or inline code
//
// Usage: python [-c code] [--help] [script | -] [arg ...]
//
// Execute Python source code.  Uses gpython, a pure-Go Python 3.4
// interpreter, so no CPython installation is required.
//
// Input modes (mutually exclusive; first one wins):
//
//	-c code
//	    Execute Python code given as a string.
//	    Example: python -c "print(1+2)"
//
//	script
//	    Execute a Python script file.  The file is opened via the
//	    AllowedPaths sandbox, so only files within configured allowed
//	    paths may be read.
//
//	- (or no argument)
//	    Read Python code from standard input.
//
// Additional positional arguments after the script/- are passed as
// sys.argv[1:].
//
// Accepted flags:
//
//	-c code
//	    Program passed in as string.
//
//	-h, --help
//	    Print usage to stdout and exit 0.
//
// Security restrictions (enforced by the gpython sandbox):
//
//   - os.system(), os.popen() and all OS process-spawning functions are
//     removed.  Calling them raises AttributeError.
//   - File-system mutation functions (os.remove, os.mkdir, os.makedirs,
//     os.rmdir, os.removedirs, os.rename, os.link, os.symlink, etc.) are
//     removed.
//   - The built-in open() is replaced with a read-only version that routes
//     through the shell's AllowedPaths sandbox.  Write/append modes raise
//     PermissionError.
//   - tempfile and glob modules raise ImportError when imported.
//
// Limitations (gpython vs CPython):
//
//   - Python 3.4 syntax only (no f-strings, no walrus operator, no
//     match/case, no := assignments).
//   - Very limited stdlib: math, string, sys, time, os (read-only), binascii.
//   - No subprocess, socket, threading, multiprocessing, json, re, io,
//     pathlib, hashlib, or other CPython batteries.
//
// Exit codes:
//
//	0  Python code ran successfully (or sys.exit(0)).
//	N  sys.exit(N) was called with integer N.
//	1  An unhandled Python exception occurred, a file could not be opened,
//	   or the code string / script was empty.
//
// Memory safety:
//
//	Script files and stdin input are read through bounded buffers capped
//	at 1 MiB.  open().read() calls inside Python scripts are also bounded
//	at 1 MiB per call to prevent memory exhaustion.  All context-
//	cancellation signals are respected; if the shell's execution timeout
//	fires Python is abandoned.
package python

import (
	"context"
	"io"
	"os"

	"github.com/DataDog/rshell/builtins"
	"github.com/DataDog/rshell/builtins/internal/pyruntime"
)

// Cmd is the python builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "python",
	Description: "run Python 3 scripts or inline code (gpython, Python 3.4)",
	MakeFlags:   registerFlags,
}

// maxSourceBytes is the maximum size of a script read from a file or stdin.
const maxSourceBytes = 1 << 20 // 1 MiB

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	help := fs.BoolP("help", "h", false, "print usage and exit")
	code := fs.StringP("cmd", "c", "", "program passed in as string")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: python [-c code] [-h] [script | -] [arg ...]\n\n")
			callCtx.Out("Run Python 3 source code (gpython interpreter, Python 3.4 syntax).\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			callCtx.Out("\nSecurity restrictions: os.system/write/delete blocked; open() is read-only.\n")
			callCtx.Out("Limitations: Python 3.4 syntax; very limited stdlib (math, string, sys, time, os).\n")
			return builtins.Result{}
		}

		// Determine source and source name.
		var (
			source     string
			sourceName string
			extraArgs  []string
		)

		if fs.Changed("cmd") {
			// -c mode: source is the flag value; args are extra argv.
			source = *code
			sourceName = "<string>"
			extraArgs = args
		} else if len(args) == 0 || args[0] == "-" {
			// Stdin mode.
			sourceName = "<stdin>"
			if len(args) > 0 {
				extraArgs = args[1:]
			}
			if callCtx.Stdin == nil {
				callCtx.Errf("python: no stdin available\n")
				return builtins.Result{Code: 1}
			}
			src, err := readBounded(callCtx.Stdin, maxSourceBytes)
			if err != nil {
				callCtx.Errf("python: reading stdin: %v\n", err)
				return builtins.Result{Code: 1}
			}
			source = src
		} else {
			// File mode.
			scriptPath := args[0]
			extraArgs = args[1:]
			sourceName = scriptPath

			f, err := callCtx.OpenFile(ctx, scriptPath, os.O_RDONLY, 0)
			if err != nil {
				callCtx.Errf("python: can't open file '%s': %v\n", scriptPath, callCtx.PortableErr(err))
				return builtins.Result{Code: 1}
			}
			defer f.Close()

			src, err := readBounded(f, maxSourceBytes)
			if err != nil {
				callCtx.Errf("python: reading '%s': %v\n", scriptPath, err)
				return builtins.Result{Code: 1}
			}
			source = src
		}

		exitCode := pyruntime.Run(ctx, pyruntime.RunOpts{
			Source:     source,
			SourceName: sourceName,
			Stdin:      callCtx.Stdin,
			Stdout:     callCtx.Stdout,
			Stderr:     callCtx.Stderr,
			Open:       callCtx.OpenFile,
			Args:       extraArgs,
		})

		if exitCode != 0 {
			return builtins.Result{Code: uint8(exitCode)}
		}
		return builtins.Result{}
	}
}

// readBounded reads at most maxBytes from r and returns the contents as a string.
// Returns an error if the source exceeds the limit.
func readBounded(r io.Reader, maxBytes int64) (string, error) {
	limited := io.LimitReader(r, maxBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxBytes {
		return "", &sourceTooBigError{limit: maxBytes}
	}
	return string(data), nil
}

type sourceTooBigError struct{ limit int64 }

func (e *sourceTooBigError) Error() string {
	return "source code exceeds maximum size limit"
}
