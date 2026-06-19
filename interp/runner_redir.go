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

func (r *Runner) redir(ctx context.Context, rd *syntax.Redirect) (io.Closer, error) {
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

	arg := r.literal(rd.Word)

	// Determine which fd this redirect targets (default: stdout for output ops).
	orig := &r.stdout
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
		default:
			r.errf("%s: unsupported fd\n", rd.N.Value)
			return nil, fmt.Errorf("%s: unsupported fd", rd.N.Value)
		}
	}

	switch rd.Op {
	case syntax.RdrIn:
		// done further below

	case syntax.RdrOut, syntax.ClbOut, syntax.AppOut:
		// /dev/null is short-circuited to io.Discard in all modes.
		if isDevNull(arg) {
			*orig = io.Discard
			return nil, nil
		}
		// In read-only mode, file-target redirects are blocked at validation.
		// This runtime check is defense-in-depth for any path that bypasses
		// the validator (e.g. a custom caller that skips validateNode).
		if !r.remediationMode {
			r.errf("> %s: file redirection is only supported for /dev/null\n", arg)
			return nil, fmt.Errorf("> %s: file redirection is only supported for /dev/null", arg)
		}
		return r.openWriteRedirect(ctx, rd.Op, arg, orig)

	case syntax.RdrAll, syntax.AppAll:
		// Note: these ops redirect both stdout and stderr, so they assign
		// r.stdout and r.stderr directly rather than going through *orig.
		// Bash does not allow an explicit fd prefix on &>/&>>.
		if isDevNull(arg) {
			r.stdout = io.Discard
			r.stderr = io.Discard
			return nil, nil
		}
		if !r.remediationMode {
			r.errf("&> %s: file redirection is only supported for /dev/null\n", arg)
			return nil, fmt.Errorf("&> %s: file redirection is only supported for /dev/null", arg)
		}
		return r.openWriteAllRedirect(ctx, rd.Op, arg)

	case syntax.DplOut:
		switch arg {
		case "1":
			*orig = r.stdout
		case "2":
			*orig = r.stderr
		default:
			r.errf(">&%s: unsupported fd\n", arg)
			return nil, fmt.Errorf(">&%s: unsupported fd", arg)
		}
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
