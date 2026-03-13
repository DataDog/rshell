// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// allowedpathsAllowedSymbols lists every "importpath.Symbol" that may be used
// by non-test Go files in allowedpaths/. Each entry must be in
// "importpath.Symbol" form, where importpath is the full Go import path.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside the filesystem sandbox.
//
// Internal module imports (github.com/DataDog/rshell/*) are auto-allowed
// and do not appear here.
//
// The permanently banned packages (reflect, unsafe) apply here too.
var allowedpathsAllowedSymbols = []string{
	// errors.As — error type assertion; pure function, no I/O.
	"errors.As",
	// errors.Is — error comparison; pure function, no I/O.
	"errors.Is",
	// errors.New — creates a simple error value; pure function, no I/O.
	"errors.New",
	// fmt.Errorf — formatted error creation; pure function, no I/O.
	"fmt.Errorf",
	// io.ReadWriteCloser — combined interface type; no side effects.
	"io.ReadWriteCloser",
	// io/fs.DirEntry — interface type for directory entries; no side effects.
	"io/fs.DirEntry",
	// io/fs.ErrExist — sentinel error for "already exists"; pure constant.
	"io/fs.ErrExist",
	// io/fs.ErrNotExist — sentinel error for "does not exist"; pure constant.
	"io/fs.ErrNotExist",
	// io/fs.ErrPermission — sentinel error for permission denied; pure constant.
	"io/fs.ErrPermission",
	// io/fs.FileInfo — interface type for file metadata; no side effects.
	"io/fs.FileInfo",
	// io/fs.FileMode — file permission bits type; pure type.
	"io/fs.FileMode",
	// os.DevNull — platform null device path constant; pure constant.
	"os.DevNull",
	// os.ErrPermission — sentinel error for permission denied; pure constant.
	"os.ErrPermission",
	// os.FileMode — file permission bits type; pure type.
	"os.FileMode",
	// os.Getgid — returns the numeric group id of the caller; read-only syscall.
	"os.Getgid",
	// os.Getgroups — returns supplementary group ids; read-only syscall.
	"os.Getgroups",
	// os.Getuid — returns the numeric user id of the caller; read-only syscall.
	"os.Getuid",
	// os.O_RDONLY — read-only file flag constant; pure constant.
	"os.O_RDONLY",
	// os.OpenRoot — opens a directory as a root for sandboxed file access; needed for sandbox.
	"os.OpenRoot",
	// os.PathError — error type wrapping path and operation; pure type.
	"os.PathError",
	// os.Root — sandboxed directory root type; core of the filesystem sandbox.
	"os.Root",
	// os.Stat — returns file info for a path; needed for sandbox path validation.
	"os.Stat",
	// path/filepath.Abs — returns absolute path; pure path computation.
	"path/filepath.Abs",
	// path/filepath.IsAbs — checks if path is absolute; pure function, no I/O.
	"path/filepath.IsAbs",
	// path/filepath.Join — joins path elements; pure function, no I/O.
	"path/filepath.Join",
	// path/filepath.Rel — returns relative path; pure path computation.
	"path/filepath.Rel",
	// path/filepath.Separator — OS path separator constant; pure constant.
	"path/filepath.Separator",
	// slices.SortFunc — sorts a slice with a comparison function; pure function, no I/O.
	"slices.SortFunc",
	// strings.Compare — compares two strings lexicographically; pure function, no I/O.
	"strings.Compare",
	// strings.EqualFold — case-insensitive string comparison; pure function, no I/O.
	"strings.EqualFold",
	// strings.HasPrefix — pure function for prefix matching; no I/O.
	"strings.HasPrefix",
	// syscall.EISDIR — "is a directory" errno constant; pure constant.
	"syscall.EISDIR",
	// syscall.Errno — system call error number type; pure type.
	"syscall.Errno",
	// syscall.Stat_t — file stat structure type; pure type for Unix file metadata.
	"syscall.Stat_t",
}
