// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package xargs implements the xargs builtin command.
//
// xargs — build and execute commands from standard input
//
// Usage: xargs [OPTION]... [COMMAND [INITIAL-ARGS]...]
//
// Reads items from standard input (or from FILE with -a), separated by
// whitespace (or by NUL with -0, or by a custom delimiter with -d) and
// executes COMMAND (default: echo) with the items appended as arguments.
//
// Items may be batched into one invocation per N items (-n), per N input
// lines (-L), or up to a maximum command-line length (-s).
//
// Accepted flags:
//
//	-0, --null
//	    Items are separated by a NUL character. Quoting and backslash
//	    escapes are not handled — every character is literal.
//
//	-a, --arg-file=FILE
//	    Read items from FILE instead of standard input. Subject to the
//	    AllowedPaths sandbox.
//
//	-d, --delimiter=DELIM
//	    Use DELIM as the single-byte item separator. Recognised escape
//	    forms: \n, \t, \r, \\, \0.
//
//	-E EOF-STR
//	    Treat an unquoted, unescaped occurrence of EOF-STR as a logical
//	    end-of-input. Has no effect if -0 or -d is in use.
//
//	-I REPLSTR
//	    Insert input as the value of REPLSTR in each COMMAND argument.
//	    Implies "one input item per command", "-L 1" semantics, and
//	    "--no-run-if-empty"; applies the same quote/backslash processing
//	    as default mode but treats each newline-terminated line as one
//	    item (matches GNU xargs).
//
//	-L NUMBER
//	    Use up to NUMBER non-empty input lines per command invocation.
//
//	-n, --max-args=N
//	    Use at most N arguments per command invocation.
//
//	-r, --no-run-if-empty
//	    Do not execute COMMAND if the input contains no items.
//
//	-s, --max-chars=N
//	    Limit a single command line to N characters.
//
//	-t, --verbose
//	    Print the resolved command line on stderr before each invocation.
//
//	-x, --exit
//	    With -n or -L, abort if -s is too small to fit a single batch.
//
//	--help
//	    Print usage to stdout and exit 0.
//
// Exit codes (POSIX):
//
//	0    success
//	1    syntax / usage error
//	123  any sub-command exited 1..125
//	124  any sub-command exited 255 (xargs stops immediately)
//	125  sub-command not invokable (RunCommand unavailable)
//	126  sub-command rejected by sandbox policy (CommandAllowed)
//	127  sub-command not found (no such builtin)
//
// Sandbox notes:
//
//	xargs only invokes other registered builtins through CallContext.RunCommand
//	and respects CallContext.CommandAllowed. There is no path to host
//	binaries; the GTFOBins shell-escape technique
//	"xargs -a /dev/null /bin/sh" is rejected because /bin/sh is not a
//	registered builtin. The -a FILE source goes through CallContext.OpenFile
//	so AllowedPaths still applies.
//
// Memory safety:
//
//	A per-token cap (MaxTokenBytes, 1 MiB) bounds any single argument and
//	prevents unbounded buffering on infinite streams. Read chunks are 64
//	KiB. ctx.Err() is checked between chunks, periodically inside per-byte
//	loops, and before each sub-command call so cancellation propagates
//	promptly even when the input stream is infinite (e.g. /dev/zero).
package xargs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Cmd is the xargs builtin command descriptor.
var Cmd = builtins.Command{
	Name:        "xargs",
	Description: "build and execute commands from standard input",
	MakeFlags:   registerFlags,
}

// Resource caps. These keep the builtin defensive against malicious or
// runaway input regardless of the user-supplied -n / -L / -s values.
const (
	// MaxTokenBytes is the largest single input item we will buffer.
	// A single argument longer than this is treated as an error.
	MaxTokenBytes = 1 << 20 // 1 MiB

	// DefaultMaxChars is the default command-line size when -s is unset.
	// Smaller than POSIX ARG_MAX (128 KiB on most Linux systems) — defensive.
	DefaultMaxChars = 128 * 1024

	// HardMaxChars is the upper clamp applied to user-provided -s values.
	HardMaxChars = 1 << 20 // 1 MiB

	// HardMaxArgs is the upper clamp on -n / -L values.
	HardMaxArgs = 1 << 20

	// readChunk is the per-Read size for bufio scanning.
	readChunk = 64 * 1024

	// ctxCheckEvery is the per-byte cadence at which token loops poll
	// context cancellation. Chosen so a 1-MiB-token DoS sees ≥ 256 polls.
	ctxCheckEvery = 4096

	// subCmdFatalCode is the sub-command exit value that triggers
	// xargs's "abort everything" code path (POSIX).
	subCmdFatalCode uint8 = 255

	// Exit codes per POSIX xargs.
	exitOK               uint8 = 0
	exitUsage            uint8 = 1
	exitSubCmdFailed     uint8 = 123
	exitSubCmd255        uint8 = 124
	exitSubCmdNotStart   uint8 = 125
	exitSubCmdNotAllowed uint8 = 126
	exitSubCmdNotFound   uint8 = 127
)

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Stop pflag from consuming flags after the first positional COMMAND.
	// GNU xargs syntax is `xargs [OPTION]... [COMMAND [INITIAL-ARGS]...]`,
	// so arguments after the command name belong to that command, not xargs.
	// Without this, `xargs echo -n` would parse -n as xargs' --max-args.
	fs.SetInterspersed(false)

	help := fs.BoolP("help", "h", false, "print usage and exit (rshell extension: -h is not recognised by GNU xargs)")
	null := fs.BoolP("null", "0", false, "input items are separated by a NUL character")
	argFile := fs.StringP("arg-file", "a", "", "read items from FILE instead of stdin")
	delim := fs.StringP("delimiter", "d", "", "use DELIM as the single-byte item separator")
	eofStr := fs.StringP("eof", "E", "", "treat EOF-STR as a logical end-of-input marker")
	var parseSeq int
	replStrVal := &orderedStringValue{seq: &parseSeq}
	fs.VarP(replStrVal, "replace", "I", "insert input as the value of REPLSTR in each argument")
	maxLinesVal := &orderedIntValue{seq: &parseSeq}
	maxArgsVal := &orderedIntValue{seq: &parseSeq}
	fs.VarP(maxLinesVal, "max-lines", "L", "use at most NUMBER non-empty input lines per command")
	fs.VarP(maxArgsVal, "max-args", "n", "use at most N arguments per command invocation")
	noRunIfEmpty := fs.BoolP("no-run-if-empty", "r", false, "do not run command if input is empty")
	maxChars := fs.IntP("max-chars", "s", 0, "limit a single command line to N characters")
	verbose := fs.BoolP("verbose", "t", false, "print the command line on stderr before running")
	exitOnSize := fs.BoolP("exit", "x", false, "abort if -s is too small to fit a -n/-L batch")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: xargs [OPTION]... [COMMAND [INITIAL-ARGS]...]\n")
			callCtx.Out("Build and execute commands from standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}

		opts, errMsg := buildOptions(fs, *null, *argFile, *delim, *eofStr, replStrVal,
			maxLinesVal, maxArgsVal, *noRunIfEmpty, *maxChars, *verbose, *exitOnSize, args)
		if errMsg != "" {
			callCtx.Errf("xargs: %s\n", errMsg)
			return builtins.Result{Code: exitUsage}
		}

		return runXargs(ctx, callCtx, opts)
	}
}

// modeKind classifies how items are tokenised from the input.
type modeKind int

const (
	modeWhitespace modeKind = iota // POSIX-style whitespace + quoting
	modeNull                       // NUL-separated, no quoting
	modeDelim                      // single custom byte, no quoting
	modeLine                       // newline-separated, with quoting (used by -I)
)

// options holds the resolved configuration for a single xargs invocation.
//
// The presence of -n / -L / -s / -I / -a / -E is encoded in the value
// itself: a non-zero int (-n/-L) or non-default mode/string indicates the
// flag was set. We avoid parallel "useX bool" fields because they are a
// repeat of the value's own zero/non-zero state.
type options struct {
	mode    modeKind
	delim   byte   // valid only when mode == modeDelim
	argFile string // empty == not set
	eofStr  string // empty == not set

	replStr    string // empty == -I not set
	maxLines   int    // 0 == -L not set
	maxArgs    int    // 0 == -n not set
	maxChars   int    // always set (defaulted to DefaultMaxChars)
	noRunEmpty bool
	verbose    bool
	exitOnSize bool

	cmdName     string
	initialArgs []string
	warnings    []string // diagnostic messages to emit before processing
}

// useReplace, useMaxLines, useMaxArgs are derived predicates kept as
// methods to make call sites read clearly without storing redundant bools.
func (o *options) useReplace() bool  { return o.replStr != "" }
func (o *options) useMaxLines() bool { return o.maxLines > 0 }
func (o *options) useMaxArgs() bool  { return o.maxArgs > 0 }

// buildOptions validates the parsed flag values and resolves the command
// to be executed. It returns a non-empty error string on validation failure.
func buildOptions(fs *builtins.FlagSet, null bool, argFile, delim, eofStr string,
	replStrVal *orderedStringValue, maxLinesVal, maxArgsVal *orderedIntValue,
	noRunEmpty bool, maxChars int, verbose, exitOnSize bool, args []string) (options, string) {

	o := options{
		mode:       modeWhitespace,
		eofStr:     eofStr,
		noRunEmpty: noRunEmpty,
		verbose:    verbose,
		exitOnSize: exitOnSize,
	}

	if null && fs.Changed("delimiter") {
		return o, "options -0 and -d are mutually exclusive"
	}
	if null {
		o.mode = modeNull
	} else if fs.Changed("delimiter") {
		b, err := decodeDelim(delim)
		if err != nil {
			return o, fmt.Sprintf("invalid delimiter: %s", err)
		}
		o.mode = modeDelim
		o.delim = b
	}

	if fs.Changed("arg-file") {
		if argFile == "" {
			return o, "argument file path is empty"
		}
		o.argFile = argFile
	}

	if maxLinesVal.changed() {
		if msg := validatePositive("L", maxLinesVal.val, HardMaxArgs); msg != "" {
			return o, msg
		}
		o.maxLines = maxLinesVal.val
		// GNU xargs documents that -L implies -x: when a logical line exceeds
		// the -s budget the command fails rather than splitting the batch.
		o.exitOnSize = true
	}

	if maxArgsVal.changed() {
		if msg := validatePositive("n", maxArgsVal.val, HardMaxArgs); msg != "" {
			return o, msg
		}
		o.maxArgs = maxArgsVal.val
		if maxLinesVal.changed() {
			// Both -n and -L were specified; last-specified wins (GNU semantics).
			if maxArgsVal.order > maxLinesVal.order {
				// -n was specified after -L → n wins, drop L.
				o.warnings = append(o.warnings,
					"warning: options --max-lines and --max-args/-n are mutually exclusive, ignoring previous --max-lines value")
				o.maxLines = 0
				// -L implied -x; now that -L is dropped, reset exitOnSize to
				// whatever the user explicitly requested (or its default, false).
				o.exitOnSize = exitOnSize
			} else {
				// -L was specified after -n → L wins, drop n.
				o.warnings = append(o.warnings,
					"warning: options --max-args and -L are mutually exclusive, ignoring previous --max-args value")
				o.maxArgs = 0
			}
		}
	}

	if fs.Changed("max-chars") {
		if maxChars <= 0 {
			return o, fmt.Sprintf("invalid -s value: %d", maxChars)
		}
		if maxChars > HardMaxChars {
			maxChars = HardMaxChars
		}
		o.maxChars = maxChars
	} else {
		o.maxChars = DefaultMaxChars
	}

	// Handle -I vs -n/-L with GNU last-specified-wins semantics. -I switches
	// tokenisation to newline-only and implies --no-run-if-empty and
	// maxArgs=maxLines=1; when -n or -L is specified after -I on the command
	// line, that flag wins instead and -I is discarded.
	if replStrVal.changed() {
		if replStrVal.val == "" {
			return o, "replace string must be non-empty"
		}
		o.replStr = replStrVal.val

		// Find the effective order of the surviving -n/-L flag (if any).
		nLOrder, nLWinner := 0, ""
		if o.maxArgs > 0 && maxArgsVal.order > nLOrder {
			nLOrder, nLWinner = maxArgsVal.order, "args"
		}
		if o.maxLines > 0 && maxLinesVal.order > nLOrder {
			nLOrder, nLWinner = maxLinesVal.order, "lines"
		}

		// Determine whether the -n/-L specified after -I should drop -I.
		// GNU special case: -n 1 after -I is treated as compatible (since
		// -I already implies one item per invocation). In that case, keep
		// replacement mode silently without a warning.
		n1AfterI := nLOrder > replStrVal.order && nLWinner == "args" && o.maxArgs == 1
		nLDropsReplace := nLOrder > replStrVal.order && !n1AfterI
		if nLDropsReplace {
			// -n or -L was specified after -I, and it is incompatible →
			// -n/-L wins, drop -I.
			o.replStr = ""
			switch nLWinner {
			case "args":
				o.warnings = append(o.warnings,
					"warning: options --replace and --max-args/-n are mutually exclusive, ignoring previous --replace value")
			case "lines":
				o.warnings = append(o.warnings,
					"warning: options --replace and -L are mutually exclusive, ignoring previous --replace value")
			}
		} else {
			// -I was specified last, or no -n/-L, or -n 1 after -I (compatible)
			// → -I wins. Warn about any incompatible earlier -n or -L, but
			// suppress the warning for -n 1 after -I (that's the compatible case).
			if o.maxArgs > 0 && !n1AfterI {
				o.warnings = append(o.warnings,
					"warning: options --max-args and --replace/-I/-i are mutually exclusive, ignoring previous --max-args value")
			} else if o.maxLines > 0 {
				o.warnings = append(o.warnings,
					"warning: options --max-lines and --replace/-I/-i are mutually exclusive, ignoring previous --max-lines value")
			}
			o.maxArgs = 1
			o.maxLines = 1
			o.noRunEmpty = true
			// Only override mode when the user did not request -0 or -d.
			if !null && !fs.Changed("delimiter") {
				o.mode = modeLine
			}
		}
	}

	if len(args) == 0 {
		o.cmdName = "echo"
	} else {
		o.cmdName = args[0]
		if len(args) > 1 {
			o.initialArgs = append([]string(nil), args[1:]...)
		}
	}

	// EOF-STR: GNU warns and clears it for -0 and -d (NUL/delimiter modes),
	// but silently preserves it for modeLine (-I), where the whole-line item
	// is compared against the marker.
	if o.mode == modeNull || o.mode == modeDelim {
		if o.eofStr != "" {
			// The trailing \n is intentional: GNU xargs emits a blank line
			// after this particular warning. callCtx.Errf("xargs: %s\n", w)
			// then adds a second \n, reproducing GNU's double-newline output.
			// Do not "fix" this to a single \n or it will diverge from GNU.
			o.warnings = append(o.warnings, "warning: the -E option has no effect if -0 or -d is used.\n")
		}
		o.eofStr = ""
	}

	return o, ""
}

// validatePositive returns "" when v is in range (0, max], else an error
// message suitable for surfacing as "xargs: <msg>".
func validatePositive(name string, v, max int) string {
	if v <= 0 || v > max {
		return fmt.Sprintf("invalid -%s value: %d", name, v)
	}
	return ""
}

// orderedIntValue is a pflag.Value for integer flags that records parse
// position via a shared sequence counter. This lets buildOptions determine
// which of -n and -L was specified last on the command line, enabling
// GNU-compatible last-specified-wins conflict resolution.
type orderedIntValue struct {
	val   int
	order int // 0 = never set; >0 = value of *seq when Set was called
	seq   *int
}

func (o *orderedIntValue) String() string { return strconv.Itoa(o.val) }
func (o *orderedIntValue) Type() string   { return "int" }
func (o *orderedIntValue) Set(s string) error {
	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("invalid integer %q", s)
	}
	o.val = v
	*o.seq++
	o.order = *o.seq
	return nil
}
func (o *orderedIntValue) changed() bool { return o.order > 0 }

// orderedStringValue is a pflag.Value for string flags that records parse
// position via a shared sequence counter, enabling last-specified-wins
// conflict resolution between -I and -n/-L.
type orderedStringValue struct {
	val   string
	order int // 0 = never set; >0 = value of *seq when Set was called
	seq   *int
}

func (o *orderedStringValue) String() string { return o.val }
func (o *orderedStringValue) Type() string   { return "string" }
func (o *orderedStringValue) Set(s string) error {
	o.val = s
	*o.seq++
	o.order = *o.seq
	return nil
}
func (o *orderedStringValue) changed() bool { return o.order > 0 }

// decodeDelim turns the -d argument into a single byte. Recognised escapes
// match the GNU xargs manual: \n, \t, \r, \\, \0. Multi-character delimiters
// other than these escapes are rejected (POSIX leaves the behaviour for
// multi-byte delimiters undefined; we choose explicit rejection).
func decodeDelim(s string) (byte, error) {
	switch s {
	case "":
		return 0, errors.New("empty delimiter")
	case `\n`:
		return '\n', nil
	case `\t`:
		return '\t', nil
	case `\r`:
		return '\r', nil
	case `\\`:
		return '\\', nil
	case `\0`:
		return 0, nil
	}
	if len(s) == 1 {
		return s[0], nil
	}
	return 0, fmt.Errorf("delimiter must be a single character: %q", s)
}

// runXargs reads items from the configured source, batches them, and invokes
// the resolved command for each batch via callCtx.RunCommand.
func runXargs(ctx context.Context, callCtx *builtins.CallContext, o options) builtins.Result {
	for _, w := range o.warnings {
		callCtx.Errf("xargs: %s\n", w)
	}
	// Upfront check: if the command itself (without any items) already
	// exceeds the -s limit, fail immediately — even on empty input. Matches
	// GNU xargs's "cannot fit single argument within argument list size limit"
	// error and must be checked before the nil-stdin early-return path.
	if commandLineLen(o, nil) > o.maxChars {
		callCtx.Errf("xargs: cannot fit single argument within argument list size limit\n")
		return builtins.Result{Code: exitUsage}
	}

	rc, err := openInput(ctx, callCtx, o)
	if err != nil {
		callCtx.Errf("xargs: %s\n", err.Error())
		return builtins.Result{Code: exitUsage}
	}
	if rc == nil {
		// No stdin available and no -a file given. POSIX xargs reads from
		// /dev/null in that case, which produces zero items.
		return finishEmpty(ctx, callCtx, o, exitOK)
	}
	defer rc.Close()

	// GNU xargs redirects child stdin to /dev/null when reading items from
	// stdin so child commands cannot consume the parent's unread input.
	// When -a is used, child stdin is left unchanged (inherits the caller's
	// stdin as GNU documents). We achieve this by setting RunCommandStdin on
	// our own CallContext; the runner's runCmd closure reads this field and
	// uses it instead of r.stdin when constructing the child context.
	if o.argFile == "" {
		callCtx.RunCommandStdin = strings.NewReader("")
	}

	tok := newTokenizer(rc, o, callCtx.Stderr)
	finalCode := exitOK
	totalItems := 0
	// tokErr is set when the tokenizer reports a fatal parsing error
	// (e.g. unmatched quote, oversize token). It suppresses the
	// no-items fallthrough so we don't run a spurious empty `echo`
	// after a failure. Any partial batch already collected is still
	// flushed to mirror GNU xargs's "best effort" behaviour.
	tokErr := false
	var batch []string
	batchLines := 0
	usedChars := commandLineLen(o, nil) // running command-line length

	flush := func() bool {
		code, stop := invokeCommand(ctx, callCtx, o, batch)
		batch = batch[:0]
		batchLines = 0
		usedChars = commandLineLen(o, nil)
		if code > finalCode {
			finalCode = code
		}
		return stop
	}

	for {
		if err := ctx.Err(); err != nil {
			return builtins.Result{Code: finalCode}
		}
		item, endedLine, more, err := tok.next(ctx)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return builtins.Result{Code: finalCode}
			}
			callCtx.Errf("xargs: %s\n", err.Error())
			if finalCode < exitUsage {
				finalCode = exitUsage
			}
			tokErr = true
			break
		}
		if !more {
			break
		}

		// Will this item still fit in the current batch's -s budget?
		// In -I mode the item expands within existing args, so compute the
		// delta from the fixed base rather than treating it as a new arg.
		var add int
		if o.useReplace() {
			add = commandLineLen(o, []string{item}) - usedChars
		} else {
			add = len(item) + 1
		}
		if usedChars+add > o.maxChars {
			if len(batch) == 0 && !o.useReplace() {
				// Single item already too large — always abort (GNU always
				// exits 1 in this case, regardless of -x). Not applicable
				// in -I mode where the batch is always empty at this point.
				callCtx.Errf("xargs: argument line too long\n")
				return builtins.Result{Code: exitUsage}
			}
			if o.exitOnSize || o.useReplace() {
				// -x / -I: abort if the current expansion won't fit within -s.
				// GNU uses "argument list too long" when a {} placeholder
				// actually expands in an arg, and "argument line too long"
				// when -I is active but no placeholder appears in initialArgs.
				hasPlaceholder := false
				for _, a := range o.initialArgs {
					if strings.Contains(a, o.replStr) {
						hasPlaceholder = true
						break
					}
				}
				if o.useReplace() && !hasPlaceholder {
					callCtx.Errf("xargs: argument line too long\n")
				} else {
					callCtx.Errf("xargs: argument list too long\n")
				}
				return builtins.Result{Code: exitUsage}
			}
			if flush() {
				return builtins.Result{Code: finalCode}
			}
			if usedChars+add > o.maxChars {
				// Item still doesn't fit even after flushing — always abort.
				callCtx.Errf("xargs: argument line too long\n")
				return builtins.Result{Code: exitUsage}
			}
		}

		batch = append(batch, item)
		usedChars += add
		if endedLine {
			batchLines++
		}
		totalItems++

		shouldFlush := (o.useMaxArgs() && len(batch) >= o.maxArgs) ||
			(o.useMaxLines() && batchLines >= o.maxLines)
		if shouldFlush {
			if flush() {
				return builtins.Result{Code: finalCode}
			}
		}
	}

	// Flush any items already collected, even if a tokenizer error fired
	// later — matches GNU xargs's behaviour of running the partial batch
	// before reporting the error.
	if len(batch) > 0 {
		if flush() {
			return builtins.Result{Code: finalCode}
		}
	}

	// Skip the "run once with no args" fallthrough on tokenizer error so
	// we don't spuriously invoke the command with empty input after a
	// parsing failure.
	if totalItems == 0 && !tokErr {
		return finishEmpty(ctx, callCtx, o, finalCode)
	}

	return builtins.Result{Code: finalCode}
}

// finishEmpty handles the "no items consumed" case at end-of-input.
func finishEmpty(ctx context.Context, callCtx *builtins.CallContext, o options, prior uint8) builtins.Result {
	if o.noRunEmpty {
		return builtins.Result{Code: prior}
	}
	code, _ := invokeCommand(ctx, callCtx, o, nil)
	if code > prior {
		prior = code
	}
	return builtins.Result{Code: prior}
}

// commandLineLen estimates the length of the command line that would be
// formed by COMMAND + INITIAL-ARGS + batch + a NUL terminator per arg.
// Matches the way GNU xargs accounts for arguments (each token counted by
// length + 1 for the separator/terminator). In -I mode the item replaces
// replStr in each initial arg rather than being appended as a new arg.
func commandLineLen(o options, batch []string) int {
	total := len(o.cmdName) + 1
	if o.useReplace() && len(batch) > 0 {
		item := batch[0]
		if len(o.initialArgs) > 0 {
			// Substitute item into each initial arg.
			for _, a := range o.initialArgs {
				total += len(strings.ReplaceAll(a, o.replStr, item)) + 1
			}
		} else {
			// No placeholder in any argument. GNU xargs still counts the
			// item towards the -s budget (verified: 5-char item at -s 6
			// passes; 6-char item fails). Add item+NUL to match GNU.
			total += len(item) + 1
		}
	} else {
		for _, a := range o.initialArgs {
			total += len(a) + 1
		}
		for _, a := range batch {
			total += len(a) + 1
		}
	}
	return total
}

// invokeCommand dispatches a single batch through callCtx.RunCommand.
// Returns the final exit code we should report and a stop flag indicating
// the caller should not invoke further batches (sub-command exit 255 or
// other fatal condition).
func invokeCommand(ctx context.Context, callCtx *builtins.CallContext, o options, batch []string) (uint8, bool) {
	if err := ctx.Err(); err != nil {
		return exitOK, true
	}

	finalCmd, finalArgs := resolveCmd(o, batch)

	if o.verbose {
		printVerbose(callCtx, finalCmd, finalArgs)
	}

	if callCtx.RunCommand == nil {
		callCtx.Errf("xargs: command execution not available\n")
		return exitSubCmdNotStart, true
	}
	if callCtx.CommandAllowed != nil && !callCtx.CommandAllowed(finalCmd) {
		callCtx.Errf("xargs: %s: command not allowed\n", finalCmd)
		return exitSubCmdNotAllowed, true
	}

	dir := ""
	if callCtx.WorkDir != nil {
		dir = callCtx.WorkDir()
	}

	exitCode, err := callCtx.RunCommand(ctx, dir, finalCmd, finalArgs)
	if err != nil {
		// The interp runner formats unknown-command / not-allowed errors
		// with a "rshell: <cmd>:" prefix. Strip it so xargs produces the
		// POSIX-style "xargs: <cmd>: <reason>" line without doubled
		// prefixes.
		msg := stripRunnerPrefix(err.Error(), finalCmd)
		callCtx.Errf("xargs: %s: %s\n", finalCmd, msg)
		// Best-effort mapping to POSIX exit codes (127 / 126 / 125)
		// based on the runner's error wording. This is brittle by
		// design — see invokeCommand_test for the contract — and a
		// future runner change will fall through to exit 125.
		switch {
		case strings.Contains(msg, "unknown command"):
			return exitSubCmdNotFound, true
		case strings.Contains(msg, "not allowed"):
			return exitSubCmdNotAllowed, true
		default:
			return exitSubCmdNotStart, true
		}
	}
	// Propagate POSIX-conventional exit codes 126/127/255 from the
	// sub-command if it reports them via a clean (non-error) return.
	switch exitCode {
	case 0:
		return exitOK, false
	case 126, 127:
		// GNU xargs continues on 126/127 from sub-command logic and returns 123.
		// Only runner-level 126/127 (CommandAllowed block, unknown command) stop.
		return exitSubCmdFailed, false
	case subCmdFatalCode:
		callCtx.Errf("xargs: %s: exited with status 255; aborting\n", finalCmd)
		return exitSubCmd255, true
	default:
		return exitSubCmdFailed, false
	}
}

// stripRunnerPrefix removes a leading "rshell: <cmd>:" prefix from an
// error message produced by interp/runner_exec.go so the eventual
// "xargs: <cmd>: <reason>" line doesn't carry a doubled prefix.
func stripRunnerPrefix(msg, cmd string) string {
	prefix := "rshell: " + cmd + ":"
	if strings.HasPrefix(msg, prefix) {
		return strings.TrimSpace(msg[len(prefix):])
	}
	return msg
}

// resolveCmd assembles the (cmdName, args) pair for a batch, applying -I
// substitution when active.
func resolveCmd(o options, batch []string) (string, []string) {
	if o.useReplace() {
		// -I forces one item per invocation; substitute the single item
		// into every occurrence of replStr in cmdName + initial args.
		var item string
		if len(batch) > 0 {
			item = batch[0]
		}
		cmd := o.cmdName // GNU: replStr is NOT substituted in the command name
		args := make([]string, len(o.initialArgs))
		for i, a := range o.initialArgs {
			args[i] = strings.ReplaceAll(a, o.replStr, item)
		}
		return cmd, args
	}
	args := make([]string, 0, len(o.initialArgs)+len(batch))
	args = append(args, o.initialArgs...)
	args = append(args, batch...)
	return o.cmdName, args
}

// printVerbose mirrors GNU xargs -t: writes the command line followed by a
// newline to stderr before running it. Arguments that contain whitespace or
// single-quote characters are shell-quoted so the output can be reproduced
// literally, matching GNU xargs -t quoting behaviour.
func printVerbose(callCtx *builtins.CallContext, name string, args []string) {
	var b strings.Builder
	b.WriteString(name)
	for _, a := range args {
		b.WriteByte(' ')
		b.WriteString(shellQuote(a))
	}
	b.WriteByte('\n')
	callCtx.Errf("%s", b.String())
}

// shellQuote returns a POSIX shell-safe representation of s. If s contains
// no characters that require quoting it is returned unchanged. Otherwise it
// is wrapped in single quotes with any embedded single-quote characters
// escaped as '\”, matching GNU xargs -t output.
func shellQuote(s string) string {
	// Fast path: safe chars only — no quoting needed.
	safe := true
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\'' || c == '"' ||
			c == '\\' || c == '`' || c == '$' || c == '!' || c == '&' ||
			c == '|' || c == ';' || c == '(' || c == ')' || c == '<' || c == '>' {
			safe = false
			break
		}
	}
	if safe {
		if len(s) == 0 {
			return "''"
		}
		return s
	}
	// Wrap in single quotes, escaping embedded single quotes as '\''.
	var b strings.Builder
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b.WriteString(`'\''`)
		} else {
			b.WriteByte(s[i])
		}
	}
	b.WriteByte('\'')
	return b.String()
}

// openInput opens the configured input source. Returns (nil, nil) if no
// stdin is available and no -a file was specified.
func openInput(ctx context.Context, callCtx *builtins.CallContext, o options) (io.ReadCloser, error) {
	if o.argFile != "" {
		f, err := callCtx.OpenFile(ctx, o.argFile, os.O_RDONLY, 0)
		if err != nil {
			return nil, err
		}
		return f, nil
	}
	if callCtx.Stdin == nil {
		return nil, nil
	}
	return io.NopCloser(callCtx.Stdin), nil
}

// tokenizer reads items from r according to opts.mode, honouring quoting
// and backslash escapes only in modeWhitespace.
type tokenizer struct {
	r           *bufio.Reader
	o           options
	buf         []byte
	eof         bool
	bytesSeen   int       // running byte count for periodic ctx.Err() polling
	stderr      io.Writer // for NUL-byte warnings; may be nil
	warnedNUL   bool      // true after the first NUL warning (GNU emits at most once)
	atLineStart bool      // true at stream-start and after any '\n'; used to gate
	// the -E EOF-marker check: GNU only suppresses the marker
	// when it is at the start of a logical line.
}

func newTokenizer(r io.Reader, o options, stderr io.Writer) *tokenizer {
	return &tokenizer{
		r:           bufio.NewReaderSize(r, readChunk),
		o:           o,
		buf:         make([]byte, 0, 256),
		stderr:      stderr,
		atLineStart: true, // stream start counts as "beginning of a line"
	}
}

// pollCtx checks ctx cancellation roughly every ctxCheckEvery bytes read.
// Cheap enough to call from the inner read loops without measurable
// throughput impact.
func (t *tokenizer) pollCtx(ctx context.Context) error {
	t.bytesSeen++
	if t.bytesSeen%ctxCheckEvery == 0 {
		return ctx.Err()
	}
	return nil
}

// pushByte appends b to t.buf, returning a "token too long" error when the
// buffer is already at the per-token cap. Centralises the cap check that
// would otherwise be duplicated at every byte append site.
func (t *tokenizer) pushByte(b byte) error {
	if len(t.buf) >= MaxTokenBytes {
		return fmt.Errorf("argument exceeds %d byte limit", MaxTokenBytes)
	}
	t.buf = append(t.buf, b)
	return nil
}

// next returns the next item, an "ended a line" flag (true if the item ends
// with or sits on a line that was terminated by '\n'), a "more" flag (false
// at EOF before producing any item), and any error.
//
// In every delimited mode (null, custom delim, line), each item is one
// logical line for -L counting purposes — the delimiter is the line
// boundary, so each token occupies exactly one line.
func (t *tokenizer) next(ctx context.Context) (item string, endedLine, more bool, err error) {
	if t.eof {
		return "", false, false, nil
	}
	switch t.o.mode {
	case modeNull:
		return t.nextDelimited(ctx, 0, false)
	case modeDelim:
		return t.nextDelimited(ctx, t.o.delim, false)
	case modeLine:
		return t.nextLineQuoted(ctx)
	default:
		return t.nextWhitespace(ctx)
	}
}

// nextDelimited reads bytes until the next occurrence of sep or EOF.
// When skipBlank is true (used by modeLine), an empty token between
// adjacent separators is silently dropped. The returned endedLine is
// always true: each delimited item counts as one logical line for -L.
func (t *tokenizer) nextDelimited(ctx context.Context, sep byte, skipBlank bool) (string, bool, bool, error) {
	t.buf = t.buf[:0]
	for {
		if err := t.pollCtx(ctx); err != nil {
			return "", false, false, err
		}
		b, err := t.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.eof = true
				if len(t.buf) == 0 || (skipBlank && isEofMarker(t.o, t.buf)) {
					return "", false, false, nil
				}
				return string(t.buf), true, true, nil
			}
			return "", false, false, err
		}
		if b == sep {
			if skipBlank && len(t.buf) == 0 {
				continue
			}
			// modeLine (-I) honors -E: a line matching eofStr terminates input.
			if skipBlank && isEofMarker(t.o, t.buf) {
				t.eof = true
				return "", false, false, nil
			}
			return string(t.buf), true, true, nil
		}
		// In modeLine (-I), GNU trims leading space/tab from each item.
		if skipBlank && len(t.buf) == 0 && (b == ' ' || b == '\t') {
			continue
		}
		if err := t.pushByte(b); err != nil {
			return "", false, false, err
		}
	}
}

// warnNUL emits the GNU xargs NUL-character warning to stderr if available.
// GNU xargs emits this warning at most once per invocation regardless of
// how many NUL bytes are present.
func (t *tokenizer) warnNUL() {
	if t.stderr != nil && !t.warnedNUL {
		t.warnedNUL = true
		fmt.Fprintf(t.stderr,
			"xargs: WARNING: a NUL character occurred in the input.  It cannot be passed through in the argument list.  Did you mean to use the --null option?\n")
	}
}

// nextLineQuoted reads the next newline-terminated input line and applies the
// same quote-stripping and backslash-escape rules as nextWhitespace, but
// treats the whole (unquoted) content of the line — including internal
// whitespace — as a single item. This matches GNU xargs -I behaviour:
//
//   - Quotes are stripped: 'hello world' → hello world
//   - Backslash escapes are processed: a\b → ab (escape keeps next byte literal)
//   - Newlines inside an unmatched quote are rejected (same as default mode)
//   - NUL bytes terminate the token and emit a warning (matching GNU)
//   - Leading space/tab are trimmed (GNU behaviour verified)
//   - Empty/blank-only lines are skipped
//
// The returned endedLine is always true (each newline-terminated item counts
// as one logical line for -L accounting).
func (t *tokenizer) nextLineQuoted(ctx context.Context) (string, bool, bool, error) {
	for {
		t.buf = t.buf[:0]
		var quote byte
		foundContent := false // true once we have a non-blank, non-leading byte
		sawLeading := false   // true once we leave the leading-whitespace region

		for {
			if err := t.pollCtx(ctx); err != nil {
				return "", false, false, err
			}
			b, err := t.r.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) {
					t.eof = true
					if quote != 0 {
						return "", false, false, unmatchedQuoteErr(quote)
					}
					if len(t.buf) == 0 {
						return "", false, false, nil
					}
					if sawLeading && isEofMarker(t.o, t.buf) {
						return "", false, false, nil
					}
					return string(t.buf), true, true, nil
				}
				return "", false, false, err
			}

			// NUL byte: warn, end this token, discard rest of line.
			if b == 0 {
				t.warnNUL()
				// If we already have content, return it; then discard to newline.
				// Discard remaining bytes on this line.
				for {
					if err := t.pollCtx(ctx); err != nil {
						return "", false, false, err
					}
					nb, nerr := t.r.ReadByte()
					if nerr != nil {
						if errors.Is(nerr, io.EOF) {
							t.eof = true
						}
						break
					}
					if nb == '\n' {
						break
					}
				}
				if len(t.buf) == 0 {
					// Blank-only before NUL: treat as empty line, skip
					break // outer loop: try next line
				}
				return string(t.buf), true, true, nil
			}

			if quote != 0 {
				if b == quote {
					quote = 0
					foundContent = true
					continue
				}
				// GNU xargs rejects newlines inside an unmatched quote
				// even in -I mode.
				if b == '\n' {
					return "", false, false, unmatchedQuoteErr(quote)
				}
				if err := t.pushByte(b); err != nil {
					return "", false, false, err
				}
				continue
			}

			// End of this input line.
			if b == '\n' {
				if isEofMarker(t.o, t.buf) {
					t.eof = true
					return "", false, false, nil
				}
				if len(t.buf) == 0 && !foundContent {
					break // empty line → skip (outer loop retries)
				}
				return string(t.buf), true, true, nil
			}

			switch b {
			case '\'':
				quote = b
				foundContent = true
				sawLeading = true
				continue
			case '"':
				quote = b
				foundContent = true
				sawLeading = true
				continue
			case '\\':
				n, errEsc := t.r.ReadByte()
				if errEsc != nil {
					if errors.Is(errEsc, io.EOF) {
						t.eof = true
						if len(t.buf) == 0 {
							return "", false, false, nil
						}
						return string(t.buf), true, true, nil
					}
					return "", false, false, errEsc
				}
				sawLeading = true
				if err := t.pushByte(n); err != nil {
					return "", false, false, err
				}
				continue
			}

			// Leading space/tab trimming (GNU xargs -I trims leading blanks).
			if !sawLeading && (b == ' ' || b == '\t') {
				continue
			}
			sawLeading = true

			if err := t.pushByte(b); err != nil {
				return "", false, false, err
			}
		}
		// Reached end of an empty/blank line; retry to get next line.
		if t.eof {
			return "", false, false, nil
		}
	}
}

func unmatchedQuoteErr(quote byte) error {
	qname := "double"
	if quote == '\'' {
		qname = "single"
	}
	return fmt.Errorf("unmatched %s quote; by default quotes are special to xargs unless you use the -0 option", qname)
}

// whitespace classification for default mode. Only space, tab, and newline
// terminate items (matches GNU xargs default tokenisation). \r, \v, \f
// are literal token bytes.
func isWhitespace(b byte) bool {
	switch b {
	case ' ', '\t', '\n':
		return true
	}
	return false
}

// nextWhitespace handles POSIX-style tokenisation with single/double quotes
// and backslash escapes. Blank lines (consecutive whitespace, including
// repeated newlines) are skipped silently and do not contribute to the
// "endedLine" line counter — only lines that actually carry an item count.
func (t *tokenizer) nextWhitespace(ctx context.Context) (string, bool, bool, error) {
	return t.nextWhitespaceOnce(ctx)
}

// nextWhitespaceOnce reads exactly one token using POSIX whitespace+quoting
// rules. A NUL byte terminates the token (with a warning) and discards the
// rest of the current "word" (bytes until the next whitespace), matching GNU
// xargs behaviour.
func (t *tokenizer) nextWhitespaceOnce(ctx context.Context) (string, bool, bool, error) {
	// outerLoop is labelled so that the NUL-inside-token path can restart
	// iteratively rather than via a recursive tail-call, preventing unbounded
	// goroutine stack growth on adversarial input (e.g. repeated quote+NUL).
outerLoop:
	t.buf = t.buf[:0]

	// Skip leading whitespace (including blank lines). NUL bytes in the
	// leading region are treated as whitespace (just skipped with a warning).
	// t.atLineStart is maintained across calls: true at stream-start or when
	// the inter-token whitespace included a '\n'. The -E EOF-marker is only
	// honoured when atLineStart is true, matching GNU xargs semantics.
	for {
		if err := t.pollCtx(ctx); err != nil {
			return "", false, false, err
		}
		b, err := t.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.eof = true
				return "", false, false, nil
			}
			return "", false, false, err
		}
		if isWhitespace(b) {
			if b == '\n' {
				t.atLineStart = true
			}
			continue
		}
		if b == 0 {
			// NUL in leading whitespace: warn and skip until next whitespace.
			t.warnNUL()
			if err2 := t.discardUntilWhitespace(ctx); err2 != nil {
				if errors.Is(err2, io.EOF) {
					return "", false, false, nil
				}
				return "", false, false, err2
			}
			continue
		}
		if err := t.r.UnreadByte(); err != nil {
			return "", false, false, err
		}
		break
	}

	// quote state: 0 = none, '\'' = single quote, '"' = double quote
	var quote byte
	endedLine := false

	for {
		if err := t.pollCtx(ctx); err != nil {
			return "", false, false, err
		}
		b, err := t.r.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				t.eof = true
				if quote != 0 {
					return "", false, false, unmatchedQuoteErr(quote)
				}
				if len(t.buf) == 0 {
					return "", false, false, nil
				}
				// GNU xargs only suppresses the EOF-marker token when it is
				// at the start of a logical line (preceded by a newline or
				// the very start of input). A same-line token like "a STOP"
				// (space before STOP, no newline) is NOT suppressed.
				if t.atLineStart && isEofMarker(t.o, t.buf) {
					t.eof = true
					return "", false, false, nil
				}
				// Token terminated by EOF — count as ending a line.
				return string(t.buf), true, true, nil
			}
			return "", false, false, err
		}

		// NUL byte: warn (once), end current token, discard rest of word.
		if b == 0 {
			t.warnNUL()
			// Discard bytes until the next whitespace (they are part of the
			// same NUL-contaminated word and cannot be passed through).
			discardErr := t.discardUntilWhitespace(ctx)
			if discardErr != nil && !errors.Is(discardErr, io.EOF) {
				return "", false, false, discardErr
			}
			if errors.Is(discardErr, io.EOF) {
				t.eof = true
			}
			// Return whatever we buffered before the NUL (may be empty if NUL
			// was the first non-whitespace byte — callers handle empty by
			// falling through to the no-items path).
			// If the token is empty, loop back to try the next non-NUL token.
			if len(t.buf) == 0 {
				if t.eof {
					return "", false, false, nil
				}
				// Restart iteratively to avoid unbounded goroutine stack growth on
				// adversarial input such as repeated quote+NUL patterns.
				goto outerLoop
			}
			return string(t.buf), endedLine, true, nil
		}

		if quote != 0 {
			if b == quote {
				quote = 0
				continue
			}
			// GNU xargs rejects newlines inside an unmatched quote.
			if b == '\n' {
				return "", false, false, unmatchedQuoteErr(quote)
			}
			if err := t.pushByte(b); err != nil {
				return "", false, false, err
			}
			continue
		}

		switch b {
		case '\'', '"':
			quote = b
			continue
		case '\\':
			n, errEsc := t.r.ReadByte()
			if errEsc != nil {
				if errors.Is(errEsc, io.EOF) {
					// GNU treats a trailing backslash as the end of the
					// last token (the backslash itself is dropped).
					t.eof = true
					if len(t.buf) == 0 {
						return "", false, false, nil
					}
					if isEofMarker(t.o, t.buf) {
						return "", false, false, nil
					}
					return string(t.buf), true, true, nil
				}
				return "", false, false, errEsc
			}
			if err := t.pushByte(n); err != nil {
				return "", false, false, err
			}
			continue
		}

		if isWhitespace(b) {
			if b == '\n' {
				endedLine = true
			}
			if isEofMarker(t.o, t.buf) {
				t.eof = true
				return "", false, false, nil
			}
			// Update atLineStart for the next token: if the terminating
			// whitespace was a newline, the next token begins a new line.
			t.atLineStart = (b == '\n')
			return string(t.buf), endedLine, true, nil
		}

		if err := t.pushByte(b); err != nil {
			return "", false, false, err
		}
	}
}

// discardUntilWhitespace reads and discards bytes until it hits whitespace or
// EOF, used after encountering a NUL byte to skip the rest of the
// NUL-contaminated word. Returns io.EOF if the stream ends before whitespace.
func (t *tokenizer) discardUntilWhitespace(ctx context.Context) error {
	for {
		if err := t.pollCtx(ctx); err != nil {
			return err
		}
		b, err := t.r.ReadByte()
		if err != nil {
			return err // io.EOF or real error
		}
		if isWhitespace(b) {
			// Push back the whitespace so the caller can see the boundary.
			_ = t.r.UnreadByte()
			return nil
		}
		// Discard non-whitespace byte (it was part of the NUL-contaminated word).
	}
}

// isEofMarker reports whether buf equals the configured EOF-STR (only when
// non-empty and we are in whitespace mode).
func isEofMarker(o options, buf []byte) bool {
	if o.eofStr == "" {
		return false
	}
	return string(buf) == o.eofStr
}
