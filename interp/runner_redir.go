// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

// MaxHeredocBytes is the maximum size of a heredoc body in bytes.
// Heredocs exceeding this limit are rejected to prevent memory exhaustion.
const MaxHeredocBytes = 10 << 20 // 10 MiB

// isQuotedHdoc reports whether the heredoc delimiter contains any quoting.
// Per POSIX, if any part of the delimiter is quoted, the heredoc body
// must not undergo expansion or backslash processing.
func isQuotedHdoc(rd *syntax.Redirect) bool {
	for _, part := range rd.Word.Parts {
		switch p := part.(type) {
		case *syntax.SglQuoted, *syntax.DblQuoted:
			return true
		case *syntax.Lit:
			if strings.ContainsRune(p.Value, '\\') {
				return true
			}
		}
	}
	return false
}

// hdocWordRawSize returns the total byte count of literal parts in a heredoc
// word. This is used as a fast pre-check before expensive expansion — if the
// raw literals alone exceed the size limit, the expanded output will too.
func hdocWordRawSize(w *syntax.Word) int {
	var n int
	for _, part := range w.Parts {
		if lit, ok := part.(*syntax.Lit); ok {
			n += len(lit.Value)
		}
	}
	return n
}

// hdocLiteral reconstructs the literal (unexpanded) text of a heredoc body.
// This is used for quoted delimiters where no expansion should occur.
func hdocLiteral(word *syntax.Word) string {
	var buf strings.Builder
	for _, part := range word.Parts {
		hdocLiteralPart(&buf, part)
	}
	return buf.String()
}

func hdocLiteralPart(buf *strings.Builder, part syntax.WordPart) {
	switch x := part.(type) {
	case *syntax.Lit:
		buf.WriteString(x.Value)
	case *syntax.ParamExp:
		buf.WriteByte('$')
		if !x.Short {
			buf.WriteByte('{')
			buf.WriteString(x.Param.Value)
			buf.WriteByte('}')
		} else {
			buf.WriteString(x.Param.Value)
		}
	case *syntax.SglQuoted:
		buf.WriteString(x.Value)
	case *syntax.DblQuoted:
		for _, p := range x.Parts {
			hdocLiteralPart(buf, p)
		}
	}
}

func (r *Runner) hdocReader(ctx context.Context, rd *syntax.Redirect) (*os.File, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	// We write to the pipe in a new goroutine,
	// as pipe writes may block once the buffer gets full.
	// We still construct and buffer the entire heredoc first,
	// as doing it concurrently would lead to different semantics and be racy.
	quoted := isQuotedHdoc(rd)
	expandWord := func(w *syntax.Word) string {
		if quoted {
			return hdocLiteral(w)
		}
		return r.document(w)
	}
	if rd.Op != syntax.DashHdoc {
		// Fast pre-check: if the raw literal content already exceeds the
		// limit, reject before the expensive expansion pass. This avoids
		// timeouts on very large heredocs (e.g. under the race detector).
		if hdocWordRawSize(rd.Hdoc) > MaxHeredocBytes {
			pr.Close()
			pw.Close()
			r.errf("heredoc: content exceeds maximum size (%d bytes)\n", MaxHeredocBytes)
			return nil, fmt.Errorf("heredoc: content exceeds maximum size (%d bytes)", MaxHeredocBytes)
		}
		hdoc := expandWord(rd.Hdoc)
		if len(hdoc) > MaxHeredocBytes {
			pr.Close()
			pw.Close()
			r.errf("heredoc: content exceeds maximum size (%d bytes)\n", MaxHeredocBytes)
			return nil, fmt.Errorf("heredoc: content exceeds maximum size (%d bytes)", MaxHeredocBytes)
		}
		go func() {
			defer pw.Close()
			const chunkSize = 32 * 1024
			data := []byte(hdoc)
			for len(data) > 0 {
				if ctx.Err() != nil {
					return
				}
				n := chunkSize
				if n > len(data) {
					n = len(data)
				}
				if _, err := pw.Write(data[:n]); err != nil {
					return
				}
				data = data[n:]
			}
		}()
		return pr, nil
	}
	var buf bytes.Buffer
	var cur []syntax.WordPart
	var hdocErr error
	flushLine := func() {
		if hdocErr != nil {
			return
		}
		expanded := expandWord(&syntax.Word{Parts: cur})
		cur = cur[:0]
		newLen := buf.Len() + len(expanded)
		if buf.Len() > 0 {
			newLen++ // account for the '\n' separator
		}
		if newLen > MaxHeredocBytes {
			hdocErr = fmt.Errorf("heredoc: content exceeds maximum size (%d bytes)", MaxHeredocBytes)
			return
		}
		if buf.Len() > 0 {
			buf.WriteByte('\n')
		}
		buf.WriteString(expanded)
	}
	for _, wp := range rd.Hdoc.Parts {
		lit, ok := wp.(*syntax.Lit)
		if !ok {
			cur = append(cur, wp)
			continue
		}
		for i, part := range strings.Split(lit.Value, "\n") {
			if i > 0 {
				flushLine()
				cur = cur[:0]
			}
			part = strings.TrimLeft(part, "\t")
			cur = append(cur, &syntax.Lit{Value: part})
		}
	}
	flushLine()
	if hdocErr != nil {
		pr.Close()
		pw.Close()
		r.errf("%s\n", hdocErr)
		return nil, hdocErr
	}
	go func() {
		defer pw.Close()
		const chunkSize = 32 * 1024
		data := buf.Bytes()
		for len(data) > 0 {
			if ctx.Err() != nil {
				return
			}
			n := chunkSize
			if n > len(data) {
				n = len(data)
			}
			if _, err := pw.Write(data[:n]); err != nil {
				return
			}
			data = data[n:]
		}
	}()
	return pr, nil
}

func stderrFileDupToFileRedirectError(target string) error {
	return fmt.Errorf("2>&%s: stderr file redirection via fd duplication is not supported", target)
}

type writeCloser interface {
	io.Writer
	io.Closer
}

type redirectOutputLimit struct {
	mu       sync.Mutex
	limit    int64
	n        int64
	exceeded bool
}

func (l *redirectOutputLimit) write(w io.Writer, p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n >= l.limit {
		l.exceeded = true
		return len(p), nil
	}
	remaining := l.limit - l.n
	if int64(len(p)) > remaining {
		if _, err := w.Write(p[:remaining]); err != nil {
			return int(remaining), err
		}
		l.n = l.limit
		l.exceeded = true
		return len(p), nil
	}
	n, err := w.Write(p)
	l.n += int64(n)
	return n, err
}

func (l *redirectOutputLimit) isExceeded() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.exceeded
}

type cappedRedirectFile struct {
	file  writeCloser
	limit *redirectOutputLimit
}

func (f *cappedRedirectFile) Write(p []byte) (int, error) {
	if f.limit == nil {
		return f.file.Write(p)
	}
	return f.limit.write(f.file, p)
}

func (f *cappedRedirectFile) Close() error {
	return f.file.Close()
}

func (r *Runner) cappedRedirectWriter(f writeCloser) writeCloser {
	w := &cappedRedirectFile{
		file:  f,
		limit: r.redirectOutputLimit,
	}
	return w
}

type preflightFDState struct {
	known        bool
	fileRedirect bool
	source       *syntax.Redirect
}

// preflightFileBackedFdDupRedirects rejects unsupported fd duplication before
// any earlier redirect in the same statement can create or truncate a file.
func (r *Runner) preflightFileBackedFdDupRedirects(redirs []*syntax.Redirect) (map[*syntax.Redirect]string, error) {
	return r.preflightFileBackedFdDupRedirectsWithExpansion(redirs, true)
}

// preflightKnownFileBackedFdDupRedirects rejects statically known unsupported
// fd duplication before command-word expansion can run substitutions.
func (r *Runner) preflightKnownFileBackedFdDupRedirects(redirs []*syntax.Redirect) error {
	_, err := r.preflightFileBackedFdDupRedirectsWithExpansion(redirs, false)
	return err
}

func (r *Runner) preflightFileBackedFdDupRedirectsWithExpansion(redirs []*syntax.Redirect, expandUnknown bool) (map[*syntax.Redirect]string, error) {
	stdoutState := preflightFDState{known: true, fileRedirect: r.stdoutFileRedirect}
	stderrState := preflightFDState{known: true, fileRedirect: r.stderrFileRedirect}
	redirectArgs := make(map[*syntax.Redirect]string)
	for _, rd := range redirs {
		switch rd.Op {
		case syntax.RdrOut, syntax.AppOut:
			state := preflightRedirectTargetState(rd)
			if rd.N != nil && rd.N.Value == "2" {
				stderrState = state
			} else {
				stdoutState = state
			}
		case syntax.ClbOut:
			if rd.N != nil && rd.N.Value == "2" {
				stderrState = preflightFDState{known: true}
			} else {
				stdoutState = preflightFDState{known: true}
			}
		case syntax.RdrAll, syntax.AppAll:
			if redirectTargetIsDevNull(rd) {
				stdoutState = preflightFDState{known: true}
				stderrState = preflightFDState{known: true}
			}
		case syntax.DplOut:
			arg, ok := literalRedirectTargetFD(rd)
			if !ok {
				continue
			}
			var targetState preflightFDState
			switch arg {
			case "1":
				targetState = stdoutState
			case "2":
				targetState = stderrState
			default:
				continue
			}
			if !targetState.known && targetState.source != nil && expandUnknown {
				source := targetState.source
				expandedArg, ok := redirectArgs[targetState.source]
				if !ok {
					expandedArg = r.literal(targetState.source.Word)
					redirectArgs[targetState.source] = expandedArg
					if !r.exit.ok() {
						return redirectArgs, nil
					}
				}
				targetState = preflightFDState{
					known:        true,
					fileRedirect: !isDevNull(expandedArg),
				}
				if source == stdoutState.source {
					stdoutState = targetState
				}
				if source == stderrState.source {
					stderrState = targetState
				}
			}
			redirectsStderr := rd.N != nil && rd.N.Value == "2"
			if redirectsStderr && targetState.fileRedirect {
				return redirectArgs, stderrFileDupToFileRedirectError(arg)
			}
			if redirectsStderr {
				stderrState = targetState
			} else {
				stdoutState = targetState
			}
		}
	}
	return redirectArgs, nil
}

func preflightRedirectTargetState(rd *syntax.Redirect) preflightFDState {
	if rd.Word == nil || len(rd.Word.Parts) != 1 {
		return preflightFDState{source: rd}
	}
	lit, ok := rd.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return preflightFDState{source: rd}
	}
	return preflightFDState{
		known:        true,
		fileRedirect: !isDevNull(lit.Value),
	}
}

func literalRedirectTargetFD(rd *syntax.Redirect) (string, bool) {
	if rd.Word == nil || len(rd.Word.Parts) != 1 {
		return "", false
	}
	lit, ok := rd.Word.Parts[0].(*syntax.Lit)
	if !ok {
		return "", false
	}
	switch lit.Value {
	case "1", "2":
		return lit.Value, true
	default:
		return "", false
	}
}

func (r *Runner) redir(ctx context.Context, rd *syntax.Redirect, redirectArgs map[*syntax.Redirect]string) (io.Closer, error) {
	if rd.Hdoc != nil {
		pr, err := r.hdocReader(ctx, rd)
		if err != nil {
			return nil, err
		}
		r.stdin = pr
		return pr, nil
	}
	if rd.Op == syntax.Hdoc || rd.Op == syntax.DashHdoc {
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		go func() { pw.Close() }()
		r.stdin = pr
		return pr, nil
	}

	arg, ok := redirectArgs[rd]
	if !ok {
		arg = r.literal(rd.Word)
	}

	// Determine which fd this redirect targets (default: stdout for output ops).
	orig := &r.stdout
	origFileRedirect := &r.stdoutFileRedirect
	redirectsStderr := false
	if rd.N != nil {
		switch rd.N.Value {
		case "0":
			// fd 0 is stdin – only valid for input redirects.
			if rd.Op != syntax.RdrIn {
				r.errf("%s: unsupported fd\n", rd.N.Value)
				return nil, fmt.Errorf("%s: unsupported fd", rd.N.Value)
			}
		case "1":
			// default (stdout)
		case "2":
			orig = &r.stderr
			origFileRedirect = &r.stderrFileRedirect
			redirectsStderr = true
		default:
			r.errf("%s: unsupported fd\n", rd.N.Value)
			return nil, fmt.Errorf("%s: unsupported fd", rd.N.Value)
		}
	}

	switch rd.Op {
	case syntax.RdrIn:
		// done further below

	case syntax.RdrOut, syntax.AppOut:
		if !isDevNull(arg) {
			if rd.N != nil && rd.N.Value != "1" {
				r.errf("%s: unsupported fd\n", rd.N.Value)
				return nil, fmt.Errorf("%s: unsupported fd", rd.N.Value)
			}
			flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if rd.Op == syntax.AppOut {
				flag = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
			f, err := r.openForWrite(ctx, arg, flag, 0666)
			if err != nil {
				return nil, err
			}
			capped := r.cappedRedirectWriter(f)
			*orig = capped
			*origFileRedirect = true
			return capped, nil
		}
		*orig = io.Discard
		*origFileRedirect = false
		return nil, nil

	case syntax.ClbOut:
		if !isDevNull(arg) {
			r.errf(">| %s: file redirection is not supported\n", arg)
			return nil, fmt.Errorf(">| %s: file redirection is not supported", arg)
		}
		*orig = io.Discard
		*origFileRedirect = false
		return nil, nil

	case syntax.RdrAll, syntax.AppAll:
		// Note: these ops redirect both stdout and stderr, so they assign
		// r.stdout and r.stderr directly rather than going through *orig.
		// Bash does not allow an explicit fd prefix on &>/&>>.
		if !isDevNull(arg) {
			r.errf("&> %s: file redirection is only supported for /dev/null\n", arg)
			return nil, fmt.Errorf("&> %s: file redirection is only supported for /dev/null", arg)
		}
		r.stdout = io.Discard
		r.stderr = io.Discard
		r.stdoutFileRedirect = false
		r.stderrFileRedirect = false
		return nil, nil

	case syntax.DplOut:
		var (
			target             io.Writer
			targetFileRedirect bool
		)
		switch arg {
		case "1":
			target = r.stdout
			targetFileRedirect = r.stdoutFileRedirect
		case "2":
			target = r.stderr
			targetFileRedirect = r.stderrFileRedirect
		default:
			r.errf(">&%s: unsupported fd\n", arg)
			return nil, fmt.Errorf(">&%s: unsupported fd", arg)
		}
		if redirectsStderr && targetFileRedirect {
			err := stderrFileDupToFileRedirectError(arg)
			r.errf("%s\n", err)
			return nil, err
		}
		*orig = target
		*origFileRedirect = targetFileRedirect
		return nil, nil

	default:
		return nil, fmt.Errorf("unhandled redirect op: %v", rd.Op)
	}

	f, err := r.open(ctx, arg, os.O_RDONLY, 0, true)
	if err != nil {
		return nil, err
	}
	stdin, err := stdinFile(ctx, f)
	if err != nil {
		return nil, err
	}
	r.stdin = stdin
	return f, nil
}
