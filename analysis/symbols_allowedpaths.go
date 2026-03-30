// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package analysis

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
	"bytes.Buffer",                       // 🟢 in-memory byte buffer; collects sandbox warnings for deferred output.
	"context.Context",                    // 🟢 context type used to signal cancellation; no I/O or side effects.
	"errors.As",                          // 🟢 error type assertion; pure function, no I/O.
	"errors.Is",                          // 🟢 error comparison; pure function, no I/O.
	"errors.New",                         // 🟢 creates a simple error value; pure function, no I/O.
	"fmt.Errorf",                         // 🟢 formatted error creation; pure function, no I/O.
	"fmt.Fprintf",                        // 🟠 writes warning messages to in-memory buffer during sandbox construction.
	"io.EOF",                             // 🟢 sentinel error value; pure constant.
	"io.ReadWriteCloser",                 // 🟢 combined interface type; no side effects.
	"io/fs.DirEntry",                     // 🟢 interface type for directory entries; no side effects.
	"io/fs.ErrExist",                     // 🟢 sentinel error for "already exists"; pure constant.
	"io/fs.ErrNotExist",                  // 🟢 sentinel error for "does not exist"; pure constant.
	"io/fs.ErrPermission",                // 🟢 sentinel error for permission denied; pure constant.
	"io/fs.FileInfo",                     // 🟢 interface type for file metadata; no side effects.
	"io/fs.FileMode",                     // 🟢 file permission bits type; pure type.
	"io/fs.ReadDirFile",                  // 🟢 read-only directory handle interface; no write capability.
	"os.DevNull",                         // 🟢 platform null device path constant; pure constant.
	"os.ErrPermission",                   // 🟢 sentinel error for permission denied; pure constant.
	"os.FileMode",                        // 🟢 file permission bits type; pure type.
	"os.Getgid",                          // 🟠 returns the numeric group id of the caller; read-only syscall.
	"os.Getgroups",                       // 🟠 returns supplementary group ids; read-only syscall.
	"os.Getuid",                          // 🟠 returns the numeric user id of the caller; read-only syscall.
	"os.O_RDONLY",                        // 🟢 read-only file flag constant; pure constant.
	"os.OpenRoot",                        // 🟠 opens a directory as a root for sandboxed file access; needed for sandbox.
	"os.PathError",                       // 🟢 error type wrapping path and operation; pure type.
	"os.Root",                            // 🟠 sandboxed directory root type; core of the filesystem sandbox.
	"os.Stat",                            // 🟠 returns file info for a path; needed for sandbox path validation.
	"path/filepath.Abs",                  // 🟢 returns absolute path; pure path computation.
	"path/filepath.IsAbs",                // 🟢 checks if path is absolute; pure function, no I/O.
	"path/filepath.Join",                 // 🟢 joins path elements; pure function, no I/O.
	"path/filepath.Rel",                  // 🟢 returns relative path; pure path computation.
	"path/filepath.Separator",            // 🟢 OS path separator constant; pure constant.
	"slices.SortFunc",                    // 🟢 sorts a slice with a comparison function; pure function, no I/O.
	"sync.Once",                          // 🟢 ensures one-time execution; used to close file descriptors at most once.
	"strings.Compare",                    // 🟢 compares two strings lexicographically; pure function, no I/O.
	"strings.EqualFold",                  // 🟢 case-insensitive string comparison; pure function, no I/O.
	"strings.HasPrefix",                  // 🟢 pure function for prefix matching; no I/O.
	"syscall.ByHandleFileInformation",    // 🟢 Windows file identity structure; pure type for file metadata.
	"syscall.EISDIR",                     // 🟢 "is a directory" errno constant; pure constant.
	"syscall.Errno",                      // 🟢 system call error number type; pure type.
	"syscall.GetFileInformationByHandle", // 🟠 Windows API for file identity (vol serial + file index); read-only syscall.
	"syscall.Handle",                     // 🟢 Windows file handle type; pure type alias.
	"syscall.O_NONBLOCK",                 // 🟢 non-blocking open flag; prevents blocking on FIFOs during access checks. Pure constant.
	"syscall.Stat_t",                     // 🟢 file stat structure type; pure type for Unix file metadata.
}
