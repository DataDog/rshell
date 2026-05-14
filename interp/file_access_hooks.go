// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package interp

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"mvdan.cc/sh/v3/syntax"

	"github.com/DataDog/rshell/allowedpaths"
)

// FileAccessOp identifies the kind of filesystem operation being traced.
type FileAccessOp string

const (
	FileAccessOpOpen       FileAccessOp = "open"
	FileAccessOpReadDir    FileAccessOp = "readdir"
	FileAccessOpOpenDir    FileAccessOp = "opendir"
	FileAccessOpStat       FileAccessOp = "stat"
	FileAccessOpLstat      FileAccessOp = "lstat"
	FileAccessOpReadlink   FileAccessOp = "readlink"
	FileAccessOpAccess     FileAccessOp = "access"
	FileAccessOpIsDirEmpty FileAccessOp = "is_dir_empty"
)

// FileAccessSource identifies which rshell surface initiated the access.
type FileAccessSource string

const (
	FileAccessSourceBuiltin           FileAccessSource = "builtin"
	FileAccessSourceInputRedirect     FileAccessSource = "input_redirect"
	FileAccessSourceGlob              FileAccessSource = "glob"
	FileAccessSourceCommandSubstitute FileAccessSource = "command_substitution"
)

// FileAccessResult is populated on after-events to report operation outcome.
type FileAccessResult string

const (
	FileAccessResultPending FileAccessResult = ""
	FileAccessResultSuccess FileAccessResult = "success"
	FileAccessResultError   FileAccessResult = "error"
)

// FileAccessHooks are passive callbacks invoked around rshell filesystem
// operations when configured by [WithFileAccessHooks]. Hooks are for
// observability only: they cannot authorize, deny, rewrite, or otherwise
// alter file access.
type FileAccessHooks struct {
	Before func(context.Context, FileAccessEvent)
	After  func(context.Context, FileAccessEvent)
}

// FileAccessEvent describes one filesystem operation observed by rshell.
// Before and after callbacks for the same operation share the same ID.
type FileAccessEvent struct {
	ID int64

	Command string
	Source  FileAccessSource
	Op      FileAccessOp

	RequestedPath string
	AbsPath       string
	ResolvedPath  string
	CWD           string

	Flags int
	Mode  os.FileMode
	// AccessMode is set for access checks. Bits follow the shell test
	// convention: 0x04 read, 0x02 write, 0x01 execute.
	AccessMode uint32

	Result FileAccessResult
	Err    string

	PreMetadata     *FileAccessMetadata
	PreMetadataErr  string
	PostMetadata    *FileAccessMetadata
	PostMetadataErr string
}

// FileAccessMetadata is lightweight file metadata captured for a traced access.
// It intentionally contains no file contents.
type FileAccessMetadata struct {
	Mode      fs.FileMode
	Size      int64
	ModTime   time.Time
	IsRegular bool
	IsDir     bool
	IsSymlink bool
	FileID    *FileAccessFileID
}

// FileAccessFileID identifies a file when the platform exposes stable identity.
// On Unix this is device+inode; on Windows it is volume serial+file index.
type FileAccessFileID struct {
	Dev uint64
	Ino uint64
}

// WithFileAccessHooks installs passive before/after filesystem access hooks.
// Nil callbacks are ignored. When no callbacks are configured, rshell performs
// no extra metadata collection for this feature.
func WithFileAccessHooks(hooks FileAccessHooks) RunnerOption {
	return func(r *Runner) error {
		r.fileAccessHooks = hooks
		return nil
	}
}

type fileAccessMetadataMode uint8

const (
	fileAccessMetadataNone fileAccessMetadataMode = iota
	fileAccessMetadataStat
	fileAccessMetadataLstat
)

func (r *Runner) fileAccessHooksEnabled() bool {
	return r.fileAccessHooks.Before != nil || r.fileAccessHooks.After != nil
}

func (r *Runner) beginFileAccess(
	ctx context.Context,
	command string,
	source FileAccessSource,
	op FileAccessOp,
	path string,
	cwd string,
	flags int,
	mode os.FileMode,
	accessMode uint32,
	metadataMode fileAccessMetadataMode,
) FileAccessEvent {
	if !r.fileAccessHooksEnabled() {
		return FileAccessEvent{}
	}
	if r.fileAccessSeq == nil {
		r.fileAccessSeq = &atomic.Int64{}
	}
	absPath := fileAccessAbsPath(path, cwd)
	resolvedPath := absPath
	if r.sandbox != nil {
		preserveLast := metadataMode == fileAccessMetadataLstat
		if resolved, err := r.sandbox.ResolvePath(path, cwd, preserveLast); err == nil {
			resolvedPath = resolved
		} else {
			resolvedPath = r.sandbox.CanonicalizeRootPrefix(absPath)
		}
	}

	event := FileAccessEvent{
		ID:            r.fileAccessSeq.Add(1),
		Command:       command,
		Source:        source,
		Op:            op,
		RequestedPath: path,
		AbsPath:       absPath,
		ResolvedPath:  resolvedPath,
		CWD:           cwd,
		Flags:         flags,
		Mode:          mode,
		AccessMode:    accessMode,
	}
	event.PreMetadata, event.PreMetadataErr = r.collectFileAccessMetadata(path, cwd, metadataMode)
	r.callFileAccessHook(ctx, r.fileAccessHooks.Before, event)
	return event
}

func (r *Runner) finishFileAccess(
	ctx context.Context,
	event FileAccessEvent,
	info fs.FileInfo,
	metadataErr error,
	accessErr error,
	postMetadataMode fileAccessMetadataMode,
) {
	if !r.fileAccessHooksEnabled() {
		return
	}
	if accessErr != nil {
		event.Result = FileAccessResultError
		event.Err = fileAccessErrString(accessErr)
	} else {
		event.Result = FileAccessResultSuccess
	}
	if metadataErr != nil {
		event.PostMetadataErr = fileAccessErrString(metadataErr)
	} else if info != nil {
		event.PostMetadata = r.fileAccessMetadataFromInfo(event.RequestedPath, event.CWD, info)
	} else if accessErr == nil {
		event.PostMetadata, event.PostMetadataErr = r.collectFileAccessMetadata(event.RequestedPath, event.CWD, postMetadataMode)
	}
	r.callFileAccessHook(ctx, r.fileAccessHooks.After, event)
}

func (r *Runner) observedSandboxOpen(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	flags int,
	mode os.FileMode,
	open func() (io.ReadWriteCloser, error),
) (io.ReadWriteCloser, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpOpen, path, cwd, flags, mode, 0, fileAccessMetadataStat)
	f, err := open()
	var (
		info    fs.FileInfo
		infoErr error
	)
	if err == nil {
		if st, ok := f.(interface{ Stat() (fs.FileInfo, error) }); ok {
			info, infoErr = st.Stat()
		}
	}
	r.finishFileAccess(ctx, event, info, infoErr, err, fileAccessMetadataStat)
	return f, err
}

func (r *Runner) observedReadDir(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	readDir func() ([]fs.DirEntry, error),
) ([]fs.DirEntry, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpReadDir, path, cwd, 0, 0, 0, fileAccessMetadataStat)
	entries, err := readDir()
	r.finishFileAccess(ctx, event, nil, nil, err, fileAccessMetadataStat)
	return entries, err
}

func (r *Runner) observedOpenDir(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	openDir func() (fs.ReadDirFile, error),
) (fs.ReadDirFile, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpOpenDir, path, cwd, 0, 0, 0, fileAccessMetadataStat)
	f, err := openDir()
	var (
		info    fs.FileInfo
		infoErr error
	)
	if err == nil {
		if st, ok := f.(interface{ Stat() (fs.FileInfo, error) }); ok {
			info, infoErr = st.Stat()
		}
	}
	r.finishFileAccess(ctx, event, info, infoErr, err, fileAccessMetadataStat)
	return f, err
}

func (r *Runner) observedIsDirEmpty(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	isDirEmpty func() (bool, error),
) (bool, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpIsDirEmpty, path, cwd, 0, 0, 0, fileAccessMetadataStat)
	empty, err := isDirEmpty()
	r.finishFileAccess(ctx, event, nil, nil, err, fileAccessMetadataStat)
	return empty, err
}

func (r *Runner) observedReadDirLimited(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	readDirLimited func() ([]fs.DirEntry, bool, error),
) ([]fs.DirEntry, bool, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpReadDir, path, cwd, 0, 0, 0, fileAccessMetadataStat)
	entries, truncated, err := readDirLimited()
	r.finishFileAccess(ctx, event, nil, nil, err, fileAccessMetadataStat)
	return entries, truncated, err
}

func (r *Runner) observedStat(
	ctx context.Context,
	command string,
	source FileAccessSource,
	op FileAccessOp,
	path string,
	cwd string,
	metadataMode fileAccessMetadataMode,
	stat func() (fs.FileInfo, error),
) (fs.FileInfo, error) {
	event := r.beginFileAccess(ctx, command, source, op, path, cwd, 0, 0, 0, metadataMode)
	info, err := stat()
	r.finishFileAccess(ctx, event, info, nil, err, fileAccessMetadataNone)
	return info, err
}

func (r *Runner) observedReadlink(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	readlink func() (string, error),
) (string, error) {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpReadlink, path, cwd, 0, 0, 0, fileAccessMetadataLstat)
	target, err := readlink()
	r.finishFileAccess(ctx, event, nil, nil, err, fileAccessMetadataLstat)
	return target, err
}

func (r *Runner) observedAccess(
	ctx context.Context,
	command string,
	source FileAccessSource,
	path string,
	cwd string,
	mode uint32,
	access func() error,
) error {
	event := r.beginFileAccess(ctx, command, source, FileAccessOpAccess, path, cwd, 0, 0, mode, fileAccessMetadataStat)
	err := access()
	r.finishFileAccess(ctx, event, nil, nil, err, fileAccessMetadataStat)
	return err
}

func (r *Runner) callFileAccessHook(ctx context.Context, hook func(context.Context, FileAccessEvent), event FileAccessEvent) {
	if hook == nil {
		return
	}
	defer func() {
		_ = recover()
	}()
	hook(ctx, event)
}

func (r *Runner) collectFileAccessMetadata(path string, cwd string, mode fileAccessMetadataMode) (*FileAccessMetadata, string) {
	if mode == fileAccessMetadataNone {
		return nil, ""
	}
	var (
		info fs.FileInfo
		err  error
	)
	switch mode {
	case fileAccessMetadataStat:
		info, err = r.sandbox.Stat(path, cwd)
	case fileAccessMetadataLstat:
		info, err = r.sandbox.Lstat(path, cwd)
	}
	if err != nil {
		return nil, fileAccessErrString(err)
	}
	return r.fileAccessMetadataFromInfo(path, cwd, info), ""
}

func (r *Runner) fileAccessMetadataFromInfo(path string, cwd string, info fs.FileInfo) *FileAccessMetadata {
	if info == nil {
		return nil
	}
	md := &FileAccessMetadata{
		Mode:      info.Mode(),
		Size:      info.Size(),
		ModTime:   info.ModTime(),
		IsRegular: info.Mode().IsRegular(),
		IsDir:     info.IsDir(),
		IsSymlink: info.Mode()&fs.ModeSymlink != 0,
	}
	absPath := fileAccessAbsPath(path, cwd)
	if dev, ino, ok := allowedpaths.FileIdentity(absPath, info, r.sandbox); ok {
		md.FileID = &FileAccessFileID{Dev: dev, Ino: ino}
	}
	return md
}

func fileAccessErrString(err error) string {
	if err == nil {
		return ""
	}
	return allowedpaths.PortableErrMsg(err)
}

func fileAccessAbsPath(path string, cwd string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(cwd, path))
}

// fileAccessCommandName returns a best-effort command name for file-access
// attribution without invoking shell expansion or its side effects.
func (r *Runner) fileAccessCommandName(cm syntax.Command) string {
	call, ok := cm.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return ""
	}
	if name := simpleWordLiteral(call.Args[0]); name != "" {
		return name
	}
	name, ok := r.staticCommandWordValue(call.Args[0], true)
	if !ok {
		return ""
	}
	return name
}

func (r *Runner) pushFileAccessCommand(command string) func() {
	prev := r.fileAccessCommand
	r.fileAccessCommand = command
	return func() {
		r.fileAccessCommand = prev
	}
}

func simpleWordLiteral(word *syntax.Word) string {
	if word == nil {
		return ""
	}
	var out string
	for _, part := range word.Parts {
		lit, ok := part.(*syntax.Lit)
		if !ok {
			return ""
		}
		out += lit.Value
	}
	return out
}

func (r *Runner) staticCommandWordValue(word *syntax.Word, splitAndGlob bool) (string, bool) {
	if word == nil {
		return "", false
	}
	var out string
	for _, part := range word.Parts {
		value, ok := r.staticCommandWordPartValue(part, splitAndGlob)
		if !ok {
			return "", false
		}
		out += value
	}
	if out == "" {
		return "", false
	}
	return out, true
}

func (r *Runner) staticCommandWordPartValue(part syntax.WordPart, splitAndGlob bool) (string, bool) {
	switch part := part.(type) {
	case *syntax.Lit:
		return part.Value, true
	case *syntax.SglQuoted:
		return part.Value, true
	case *syntax.DblQuoted:
		var out string
		for _, part := range part.Parts {
			value, ok := r.staticCommandWordPartValue(part, false)
			if !ok {
				return "", false
			}
			out += value
		}
		return out, true
	case *syntax.ParamExp:
		if !simpleParamExp(part) || part.Param == nil {
			return "", false
		}
		value := r.lookupVar(part.Param.Value).String()
		if splitAndGlob && r.unsafeUnquotedCommandValue(value) {
			return "", false
		}
		return value, true
	default:
		return "", false
	}
}

func simpleParamExp(param *syntax.ParamExp) bool {
	return param.Flags == nil &&
		!param.Excl && !param.Length && !param.Width && !param.IsSet &&
		param.NestedParam == nil && param.Index == nil &&
		len(param.Modifiers) == 0 && param.Slice == nil &&
		param.Repl == nil && param.Names == 0 && param.Exp == nil
}

func (r *Runner) unsafeUnquotedCommandValue(value string) bool {
	for _, r := range value {
		switch r {
		case '*', '?', '[':
			return true
		}
	}
	ifs := " \t\n"
	if vr := r.lookupVar("IFS"); vr.IsSet() {
		ifs = vr.String()
	}
	for _, r := range value {
		for _, sep := range ifs {
			if r == sep {
				return true
			}
		}
	}
	return false
}
