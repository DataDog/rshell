// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package jq implements a bounded, read-only subset of the jq JSON processor.
//
// Supported filters include identity, field and index access, iteration,
// optional expressions, pipes, comma expressions, literals, array and object
// constructors, comparisons, boolean and alternative operators, arithmetic,
// variables, and select/map/length/keys/has/type/empty. The implementation is
// intentionally self-contained: it neither executes an external jq binary nor
// loads jq programs, modules, environment variables, or auxiliary files.
package jq

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/DataDog/rshell/builtins"
)

// Additional invocation limits bound resources that are not part of a JSON
// value itself.
const (
	MaxFileOperands       = 64
	MaxVariableBindings   = 64
	MaxVariableBytes      = 1 << 20
	MaxVariableNodes      = MaxValueNodes
	MaxVariableValueBytes = MaxValueBytes

	inputReadChunk = 32 << 10
	variableSep    = "\x00"
)

var (
	errInputLimit = errors.New("input exceeds the total size limit")
	errNoProgress = errors.New("input reader made no progress")
)

const (
	exitGeneric = 1
	exitSystem  = 2
	exitCompile = 3
	exitNoValue = 4
	exitRuntime = 5
)

// Cmd is the jq builtin command descriptor.
var Cmd = builtins.Command{
	Name:          "jq",
	Description:   "process JSON with bounded read-only filters",
	MakeFlags:     registerFlags,
	NormalizeArgs: normalizeVariableArgs,
}

type variableBinding struct {
	name   string
	text   string
	isJSON bool
}

type invalidArgJSONError struct {
	name string
	err  error
}

func (e *invalidArgJSONError) Error() string {
	return fmt.Sprintf("invalid JSON passed to --argjson %s: %v", e.name, e.err)
}

func (e *invalidArgJSONError) Unwrap() error { return e.err }

type bindingCollector struct {
	bindings []variableBinding
	bytes    int
}

func (c *bindingCollector) add(encoded string, isJSON bool) error {
	name, text, ok := strings.Cut(encoded, variableSep)
	if !ok {
		return errors.New("requires NAME and VALUE")
	}
	if len(c.bindings) >= MaxVariableBindings {
		return fmt.Errorf("too many variable bindings (maximum %d)", MaxVariableBindings)
	}
	added := len(name) + len(text)
	if added > MaxVariableBytes-c.bytes {
		return fmt.Errorf("variable bindings exceed the %d-byte limit", MaxVariableBytes)
	}
	c.bytes += added
	c.bindings = append(c.bindings, variableBinding{name: name, text: text, isJSON: isJSON})
	return nil
}

type bindingFlag struct {
	collector *bindingCollector
	isJSON    bool
}

func (f *bindingFlag) String() string { return "" }
func (f *bindingFlag) Type() string   { return "NAME VALUE" }
func (f *bindingFlag) Set(text string) error {
	return f.collector.add(text, f.isJSON)
}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	compact := fs.BoolP("compact-output", "c", false, "produce compact JSON output")
	rawOutput := fs.BoolP("raw-output", "r", false, "output strings without JSON quoting")
	exitStatus := fs.BoolP("exit-status", "e", false, "set status from the last output value")
	nullInput := fs.BoolP("null-input", "n", false, "use null as one input without reading files")
	slurp := fs.BoolP("slurp", "s", false, "read all inputs into an array")
	rawInput := fs.BoolP("raw-input", "R", false, "read each input line as a string")
	help := fs.BoolP("help", "h", false, "print usage and exit")
	collector := &bindingCollector{}
	fs.Var(&bindingFlag{collector: collector}, "arg", "bind $NAME to string VALUE")
	fs.Var(&bindingFlag{collector: collector, isJSON: true}, "argjson", "bind $NAME to JSON VALUE")

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		if *help {
			callCtx.Out("Usage: jq [OPTION]... FILTER [FILE]...\n")
			callCtx.Out("Process JSON using a bounded, read-only jq filter subset.\n")
			callCtx.Out("With no FILE, or when FILE is -, read standard input.\n\n")
			fs.SetOutput(callCtx.Stdout)
			fs.PrintDefaults()
			return builtins.Result{}
		}
		if err := validateArgJSONBindings(ctx, collector.bindings); err != nil {
			callCtx.Errf("jq: %s\n", err)
			return builtins.Result{Code: variableErrorCode(err)}
		}
		if len(args) == 0 {
			callCtx.Errf("jq: missing filter\nTry 'jq --help' for more information.\n")
			return builtins.Result{Code: 1}
		}
		filter := args[0]
		files := args[1:]
		if len(files) > MaxFileOperands {
			callCtx.Errf("jq: too many file operands (maximum %d)\n", MaxFileOperands)
			return builtins.Result{Code: 1}
		}
		root, err := parseFilter(filter)
		if err != nil {
			callCtx.Errf("jq: compile error: %s\n", err)
			return builtins.Result{Code: exitCompile}
		}
		referencedVariables := filterVariables(root)
		variables, err := makeVariables(ctx, collector.bindings, referencedVariables)
		if err != nil {
			callCtx.Errf("jq: %s\n", err)
			return builtins.Result{Code: variableErrorCode(err)}
		}
		if err := validateFilterVariables(root, variables); err != nil {
			callCtx.Errf("jq: compile error: %s\n", err)
			return builtins.Result{Code: exitCompile}
		}

		eval := newEvaluator(ctx, variables)
		emitter := &outputEmitter{
			ctx:     ctx,
			writer:  callCtx.Stdout,
			compact: *compact,
			raw:     *rawOutput,
		}
		run := func(input value) error {
			results, err := eval.evaluate(input, root)
			for _, result := range results {
				if emitErr := emitter.emit(result); emitErr != nil {
					return emitErr
				}
			}
			return err
		}

		hadOpenFailure := false
		if *nullInput {
			err = run(null())
		} else {
			if len(files) == 0 {
				files = []string{"-"}
			}
			hadOpenFailure, err = processInputs(ctx, callCtx, files, *rawInput, *slurp, run)
		}
		if err != nil {
			if builtins.IsBrokenPipe(err) {
				return builtins.Result{}
			}
			callCtx.Errf("jq: %s\n", formatError(callCtx, err))
			if hadOpenFailure {
				return builtins.Result{Code: exitSystem}
			}
			var runtimeErr *runtimeError
			if errors.As(err, &runtimeErr) {
				return builtins.Result{Code: exitRuntime}
			}
			var parseErr *parseInputError
			if errors.As(err, &parseErr) {
				return builtins.Result{Code: exitRuntime}
			}
			return builtins.Result{Code: exitGeneric}
		}
		if hadOpenFailure {
			return builtins.Result{Code: exitSystem}
		}
		if *exitStatus && !emitter.wrote {
			return builtins.Result{Code: exitNoValue}
		}
		if *exitStatus && !truthy(emitter.last) {
			return builtins.Result{Code: exitGeneric}
		}
		return builtins.Result{}
	}
}

func variableErrorCode(err error) uint8 {
	var invalidJSON *invalidArgJSONError
	if errors.As(err, &invalidJSON) {
		return exitSystem
	}
	return exitGeneric
}

func normalizeVariableArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return append(out, args[i:]...)
		}
		if arg == "--arg" || arg == "--argjson" {
			if i+2 < len(args) {
				out = append(out, arg+"="+args[i+1]+variableSep+args[i+2])
				i += 2
				continue
			}
		}
		if strings.HasPrefix(arg, "--arg=") || strings.HasPrefix(arg, "--argjson=") {
			if i+1 < len(args) {
				out = append(out, arg+variableSep+args[i+1])
				i++
				continue
			}
		}
		out = append(out, arg)
	}
	for i, arg := range out {
		if arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			break
		}
		if isRegisteredOption(arg) {
			continue
		}
		if isImplicitNegativeFilter(arg) {
			withSeparator := make([]string, 0, len(out)+1)
			withSeparator = append(withSeparator, out[:i]...)
			withSeparator = append(withSeparator, "--")
			withSeparator = append(withSeparator, out[i:]...)
			return withSeparator
		}
	}
	return out
}

func isImplicitNegativeFilter(arg string) bool {
	if len(arg) < 2 || arg[0] != '-' {
		return false
	}
	if isASCIIDigit(arg[1]) || isFilterSpace(arg[1]) {
		return true
	}
	switch arg[1] {
	case '.', '$', '(', '[', '{', '"':
		return true
	default:
		return false
	}
}

func isRegisteredOption(arg string) bool {
	switch arg {
	case "--compact-output", "--raw-output", "--exit-status", "--null-input", "--slurp", "--raw-input", "--help":
		return true
	}
	if strings.HasPrefix(arg, "--arg=") || strings.HasPrefix(arg, "--argjson=") {
		return true
	}
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return false
	}
	for _, shorthand := range arg[1:] {
		if !strings.ContainsRune("crensRh", shorthand) {
			return false
		}
	}
	return true
}

func makeVariables(ctx context.Context, bindings []variableBinding, referenced map[string]struct{}) (map[string]value, error) {
	variables := make(map[string]value, len(referenced))
	seen := make(map[string]struct{}, len(bindings))
	retainedNodes, retainedBytes := 0, 0
	for _, binding := range bindings {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !validVariableName(binding.name) {
			return nil, fmt.Errorf("invalid variable name %q", builtins.SafeOperand(binding.name))
		}
		if _, exists := seen[binding.name]; exists {
			continue
		}
		seen[binding.name] = struct{}{}
		var (
			v   value
			err error
		)
		if binding.isJSON {
			v, err = parseArgJSON(ctx, binding)
		} else {
			v, err = stringValue(binding.text)
			if err != nil {
				return nil, fmt.Errorf("--arg %s: %w", binding.name, err)
			}
		}
		if err != nil {
			return nil, err
		}
		if _, used := referenced[binding.name]; !used {
			continue
		}
		if v.nodes > MaxVariableNodes-retainedNodes || v.bytes > MaxVariableValueBytes-retainedBytes {
			return nil, errors.New("referenced variable values exceed the aggregate size limit")
		}
		retainedNodes += v.nodes
		retainedBytes += v.bytes
		variables[binding.name] = v
	}
	return variables, nil
}

func validateArgJSONBindings(ctx context.Context, bindings []variableBinding) error {
	seen := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validVariableName(binding.name) {
			continue
		}
		if _, exists := seen[binding.name]; exists {
			continue
		}
		seen[binding.name] = struct{}{}
		if !binding.isJSON {
			continue
		}
		if _, err := parseArgJSON(ctx, binding); err != nil {
			return err
		}
	}
	return nil
}

func parseArgJSON(ctx context.Context, binding variableBinding) (value, error) {
	v, err := parseSingleJSON(ctx, binding.text)
	if err == nil {
		return v, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return value{}, ctxErr
	}
	return value{}, &invalidArgJSONError{name: binding.name, err: err}
}

func filterVariables(root *node) map[string]struct{} {
	variables := make(map[string]struct{})
	var visit func(*node)
	visit = func(current *node) {
		if current == nil {
			return
		}
		if current.kind == nodeVariable {
			variables[current.name] = struct{}{}
		}
		visit(current.left)
		visit(current.right)
		visit(current.child)
		for _, member := range current.members {
			visit(member.key)
			visit(member.value)
		}
	}
	visit(root)
	return variables
}

func validateFilterVariables(root *node, variables map[string]value) error {
	if root == nil {
		return nil
	}
	if root.kind == nodeVariable {
		if _, ok := variables[root.name]; !ok {
			return fmt.Errorf("variable $%s is not defined", root.name)
		}
	}
	if err := validateFilterVariables(root.left, variables); err != nil {
		return err
	}
	if err := validateFilterVariables(root.right, variables); err != nil {
		return err
	}
	if err := validateFilterVariables(root.child, variables); err != nil {
		return err
	}
	for _, member := range root.members {
		if err := validateFilterVariables(member.key, variables); err != nil {
			return err
		}
		if err := validateFilterVariables(member.value, variables); err != nil {
			return err
		}
	}
	return nil
}

func validVariableName(name string) bool {
	if name == "" || !isIdentifierStart(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isIdentifierContinue(name[i]) {
			return false
		}
	}
	return true
}

type inputBudget struct{ used int }

type budgetReader struct {
	ctx        context.Context
	reader     io.Reader
	budget     *inputBudget
	emptyReads int
}

func (r *budgetReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	remaining := MaxTotalInputBytes - r.budget.used
	if remaining < 0 {
		return 0, errInputLimit
	}
	if len(p) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := r.reader.Read(p)
	if n > remaining {
		r.budget.used += n
		return 0, errInputLimit
	}
	r.budget.used += n
	if n == 0 && err == nil {
		r.emptyReads++
		if r.emptyReads >= 100 {
			return 0, errNoProgress
		}
	} else {
		r.emptyReads = 0
	}
	return n, err
}

func processInputs(
	ctx context.Context,
	callCtx *builtins.CallContext,
	files []string,
	raw bool,
	slurp bool,
	consume func(value) error,
) (bool, error) {
	budget := &inputBudget{}
	stdin := callCtx.Stdin
	source := &sequentialInput{ctx: ctx, callCtx: callCtx, stdin: stdin, files: files}
	defer source.Close()
	reader := &budgetReader{ctx: ctx, reader: source, budget: budget}
	if raw && slurp {
		var text strings.Builder
		if err := appendRawInput(ctx, reader, &text); err != nil {
			return source.hadOpenFailure, err
		}
		v, err := stringValue(text.String())
		if err != nil {
			return source.hadOpenFailure, err
		}
		return source.hadOpenFailure, consume(v)
	}
	if slurp {
		items := make([]value, 0)
		nodes, size := 1, 2
		err := processJSON(ctx, &surrogateValidator{reader: reader}, func(v value) error {
			separator := 0
			if len(items) > 0 {
				separator = 1
			}
			if err := addAggregate(&nodes, &size, v, separator, MaxValueNodes, MaxValueBytes); err != nil {
				return err
			}
			items = append(items, v)
			return nil
		})
		if err != nil {
			return source.hadOpenFailure, err
		}
		v, err := arrayValue(items)
		if err != nil {
			return source.hadOpenFailure, err
		}
		return source.hadOpenFailure, consume(v)
	}

	if raw {
		err := processRawLines(ctx, reader, consume)
		return source.hadOpenFailure, err
	}
	err := processJSON(ctx, &surrogateValidator{reader: reader}, consume)
	return source.hadOpenFailure, err
}

type sequentialInput struct {
	ctx     context.Context
	callCtx *builtins.CallContext
	stdin   io.Reader
	files   []string
	index   int
	file    string
	reader  io.Reader
	closer  io.Closer

	hadOpenFailure bool
}

func (r *sequentialInput) Read(p []byte) (int, error) {
	for {
		if err := r.ctx.Err(); err != nil {
			return 0, err
		}
		if r.reader == nil {
			if r.index >= len(r.files) {
				return 0, io.EOF
			}
			if err := r.openNext(); err != nil {
				if ctxErr := r.ctx.Err(); ctxErr != nil {
					return 0, ctxErr
				}
				r.hadOpenFailure = true
				r.callCtx.Errf("jq: %s\n", formatError(r.callCtx, err))
				continue
			}
		}
		n, err := r.reader.Read(p)
		if n > 0 {
			if errors.Is(err, io.EOF) {
				if closeErr := r.closeCurrent(); closeErr != nil {
					return n, closeErr
				}
				return n, nil
			}
			if err != nil {
				return n, &inputError{file: r.file, err: err}
			}
			return n, nil
		}
		if errors.Is(err, io.EOF) {
			if closeErr := r.closeCurrent(); closeErr != nil {
				return 0, closeErr
			}
			continue
		}
		if err != nil {
			return 0, &inputError{file: r.file, err: err}
		}
		return 0, nil
	}
}

func (r *sequentialInput) openNext() error {
	r.file = r.files[r.index]
	r.index++
	if r.file == "-" {
		if r.stdin == nil {
			r.reader = strings.NewReader("")
		} else {
			r.reader = r.stdin
		}
		r.closer = nil
		return nil
	}
	if r.callCtx.OpenRegularFile == nil {
		return &inputError{file: r.file, err: errors.New("file access is unavailable")}
	}
	handle, err := openInputFile(r.ctx, r.callCtx, r.file)
	if err != nil {
		return &inputError{file: r.file, err: err}
	}
	r.reader = handle
	r.closer = handle
	return nil
}

func openInputFile(ctx context.Context, callCtx *builtins.CallContext, file string) (io.ReadWriteCloser, error) {
	handle, err := callCtx.OpenRegularFile(ctx, file)
	if err != nil {
		return nil, err
	}
	return handle, nil
}

func (r *sequentialInput) closeCurrent() error {
	closer := r.closer
	file := r.file
	r.reader = nil
	r.closer = nil
	if closer == nil {
		return nil
	}
	if err := closer.Close(); err != nil {
		return &inputError{file: file, err: err}
	}
	return nil
}

func (r *sequentialInput) Close() error {
	return r.closeCurrent()
}

func processJSON(ctx context.Context, reader io.Reader, consume func(value) error) error {
	decoder := newJSONValueDecoder(ctx, reader)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		v, err := decoder.next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var inputErr *inputError
			if errors.As(err, &inputErr) || errors.Is(err, errInputLimit) || errors.Is(err, errValueNodes) ||
				errors.Is(err, errValueBytes) || errors.Is(err, errValueDepth) {
				return err
			}
			return &parseInputError{err: err}
		}
		if err := consume(v); err != nil {
			return err
		}
	}
}

func processRawLines(ctx context.Context, reader io.Reader, consume func(value) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4<<10), MaxRawLineBytes+2)
	scanner.Split(splitRawLine)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(scanner.Bytes()) > MaxRawLineBytes {
			return fmt.Errorf("raw input line exceeds the %d-byte limit", MaxRawLineBytes)
		}
		v, err := stringValue(scanner.Text())
		if err != nil {
			return err
		}
		if err := consume(v); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("raw input line exceeds the %d-byte limit: %w", MaxRawLineBytes, err)
	}
	return nil
}

func splitRawLine(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if i := bytes.IndexByte(data, '\n'); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func appendRawInput(ctx context.Context, reader io.Reader, output *strings.Builder) error {
	buf := make([]byte, inputReadChunk)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := reader.Read(buf)
		if n > 0 {
			if n > MaxValueBytes-output.Len()-2 {
				return errValueBytes
			}
			_, _ = output.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type inputError struct {
	file string
	err  error
}

func (e *inputError) Error() string { return e.err.Error() }
func (e *inputError) Unwrap() error { return e.err }

type parseInputError struct{ err error }

func (e *parseInputError) Error() string { return "parse error: " + e.err.Error() }
func (e *parseInputError) Unwrap() error { return e.err }

func formatError(callCtx *builtins.CallContext, err error) string {
	var inErr *inputError
	if errors.As(err, &inErr) {
		name := inErr.file
		if name == "-" {
			name = "standard input"
		}
		detailErr := inErr.err
		var pathErr *os.PathError
		if errors.As(detailErr, &pathErr) {
			detailErr = pathErr.Err
		}
		detail := builtins.SafeOperand(portableError(callCtx, detailErr))
		return fmt.Sprintf("%s: %s", builtins.SafeOperand(name), detail)
	}
	return builtins.SafeOperand(err.Error())
}

func portableError(callCtx *builtins.CallContext, err error) string {
	if callCtx.PortableErr != nil {
		return callCtx.PortableErr(err)
	}
	return err.Error()
}

type outputEmitter struct {
	ctx     context.Context
	writer  io.Writer
	compact bool
	raw     bool
	bytes   int
	wrote   bool
	last    value
}

func (e *outputEmitter) emit(v value) error {
	if err := e.ctx.Err(); err != nil {
		return err
	}
	remaining := MaxOutputBytes - e.bytes
	if remaining <= 0 {
		return errOutputLimit
	}
	var (
		text string
		err  error
	)
	if e.raw && v.kind == valueString {
		text = v.str
	} else {
		text, err = encodeValue(v, !e.compact, remaining-1)
		if err != nil {
			return err
		}
	}
	if len(text) > remaining-1 {
		return errOutputLimit
	}
	if n, err := io.WriteString(e.writer, text); err != nil {
		return err
	} else if n != len(text) {
		return io.ErrShortWrite
	}
	if err := e.ctx.Err(); err != nil {
		return err
	}
	if n, err := io.WriteString(e.writer, "\n"); err != nil {
		return err
	} else if n != 1 {
		return io.ErrShortWrite
	}
	e.bytes += len(text) + 1
	e.wrote = true
	e.last = v
	return nil
}
