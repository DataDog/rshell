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
// allowedpathsAllowedSymbols is ordered from least safe (top) to most safe (bottom).
// See the package-level doc in symbols_common.go for the full category guide.
var allowedpathsAllowedSymbols = []string{

	// --- 1. OS / Kernel interface ---

	"os.Getgid",                          // returns the numeric group id of the caller; read-only syscall.
	"os.Getgroups",                       // returns supplementary group ids of the caller; read-only syscall.
	"os.Getuid",                          // returns the numeric user id of the caller; read-only syscall.
	"os.OpenRoot",                        // opens a directory as a sandboxed root for file access; filesystem I/O.
	"os.Stat",                            // returns file info for a path; filesystem I/O needed for sandbox path validation.
	"syscall.ByHandleFileInformation",    // Windows: file identity struct (vol serial + file index); read-only type.
	"syscall.EISDIR",                     // errno constant for "is a directory"; pure constant, no I/O.
	"syscall.Errno",                      // system call error number type; pure type, no I/O.
	"syscall.GetFileInformationByHandle", // Windows: queries file identity via handle; read-only syscall.
	"syscall.Handle",                     // Windows: file handle type; pure type alias, no I/O.
	"syscall.O_NONBLOCK",                 // non-blocking open flag; prevents blocking on FIFOs during access checks. Pure constant.
	"syscall.Stat_t",                     // Unix: file stat struct for UID/GID/inode; read-only type, no I/O.

	// --- 2. Filesystem sandbox types & sentinel errors ---

	"io/fs.DirEntry",      // interface type for directory entries; no side effects.
	"io/fs.ErrExist",      // sentinel error for "already exists"; pure constant.
	"io/fs.ErrNotExist",   // sentinel error for "does not exist"; pure constant.
	"io/fs.ErrPermission", // sentinel error for permission denied; pure constant.
	"io/fs.FileInfo",      // interface type for file metadata; no side effects.
	"io/fs.FileMode",      // file permission bits type; pure type.
	"io/fs.ReadDirFile",   // read-only directory handle interface; no write capability.
	"os.DevNull",          // platform null device path constant; pure constant.
	"os.ErrPermission",    // sentinel error for permission denied; pure constant.
	"os.FileMode",         // file permission bits type; pure type.
	"os.O_RDONLY",         // read-only file flag constant; pure constant.
	"os.PathError",        // error type wrapping path and operation; pure type.
	"os.Root",             // sandboxed directory root type; core of the filesystem sandbox.

	// --- 3. Path computation ---

	"path/filepath.Abs",       // returns absolute path; pure path computation, no I/O.
	"path/filepath.IsAbs",     // checks if path is absolute; pure function, no I/O.
	"path/filepath.Join",      // joins path elements; pure function, no I/O.
	"path/filepath.Rel",       // returns relative path between two paths; pure path computation, no I/O.
	"path/filepath.Separator", // OS path separator constant; pure constant.

	// --- 4. I/O interfaces ---

	"io.EOF",             // sentinel error value; pure constant.
	"io.ReadWriteCloser", // combined Reader/Writer/Closer interface type; no side effects.

	// --- 5. Collections ---

	"slices.SortFunc", // sorts a slice with a comparison function; pure function, no I/O.

	// --- 6. String manipulation ---

	"strings.Compare",   // compares two strings lexicographically; pure function, no I/O.
	"strings.EqualFold", // case-insensitive string comparison; pure function, no I/O.
	"strings.HasPrefix", // checks string prefix; pure function, no I/O.

	// --- 7. Error handling ---

	"errors.As",  // error type assertion; pure function, no I/O.
	"errors.Is",  // error comparison; pure function, no I/O.
	"errors.New", // creates a simple error value; pure function, no I/O.
	"fmt.Errorf", // formatted error creation; pure function, no I/O.
}
