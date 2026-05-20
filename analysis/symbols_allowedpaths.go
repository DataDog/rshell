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
// The permanently banned packages (for example reflect) apply here too.
var allowedpathsAllowedSymbols = []string{
	"bytes.Buffer",                         // 🟢 in-memory byte buffer; collects sandbox warnings for deferred output.
	"context.Context",                      // 🟢 context type used to signal cancellation; no I/O or side effects.
	"errors.As",                            // 🟢 error type assertion; pure function, no I/O.
	"errors.Is",                            // 🟢 error comparison; pure function, no I/O.
	"errors.New",                           // 🟢 creates a simple error value; pure function, no I/O.
	"fmt.Errorf",                           // 🟢 formatted error creation; pure function, no I/O.
	"fmt.Fprintf",                          // 🟠 writes warning messages to in-memory buffer during sandbox construction.
	"io.EOF",                               // 🟢 sentinel error value; pure constant.
	"io.ReadWriteCloser",                   // 🟢 combined interface type; no side effects.
	"io/fs.DirEntry",                       // 🟢 interface type for directory entries; no side effects.
	"io/fs.ModeSymlink",                    // 🟢 file mode bit for symlinks; pure constant.
	"io/fs.ErrExist",                       // 🟢 sentinel error for "already exists"; pure constant.
	"io/fs.ErrNotExist",                    // 🟢 sentinel error for "does not exist"; pure constant.
	"io/fs.ErrPermission",                  // 🟢 sentinel error for permission denied; pure constant.
	"io/fs.FileInfo",                       // 🟢 interface type for file metadata; no side effects.
	"io/fs.FileMode",                       // 🟢 file permission bits type; pure type.
	"io/fs.ReadDirFile",                    // 🟢 read-only directory handle interface; no write capability.
	"os.DevNull",                           // 🟢 platform null device path constant; pure constant.
	"os.ErrPermission",                     // 🟢 sentinel error for permission denied; pure constant.
	"os.File",                              // 🟠 file handle returned by os.Root.Open; needed for cross-root symlink fallback.
	"os.FileMode",                          // 🟢 file permission bits type; pure type.
	"os.Getgid",                            // 🟠 returns the numeric group id of the caller; read-only syscall.
	"os.Getgroups",                         // 🟠 returns supplementary group ids; read-only syscall.
	"os.Getuid",                            // 🟠 returns the numeric user id of the caller; read-only syscall.
	"os.IsPathSeparator",                   // 🟢 checks whether a byte is a platform path separator; pure function, no I/O.
	"os.O_APPEND",                          // 🟢 append file flag constant; only accepted by the dedicated redirection write-open path.
	"os.O_CREATE",                          // 🟢 create file flag constant; only accepted by the dedicated redirection write-open path.
	"os.O_EXCL",                            // 🟢 exclusive-create file flag constant; preserved when the dedicated write-open path creates files.
	"os.O_RDONLY",                          // 🟢 read-only file flag constant; pure constant.
	"os.O_RDWR",                            // 🟢 read-write file flag constant; preserved by the dedicated write-open path.
	"os.O_TRUNC",                           // 🟢 truncate file flag constant; only accepted by the dedicated redirection write-open path.
	"os.O_WRONLY",                          // 🟢 write-only file flag constant; only accepted by the dedicated redirection write-open path.
	"os.NewFile",                           // 🟠 wraps a sandbox-opened file descriptor after fd-relative openat validation; does not open paths itself.
	"os.OpenRoot",                          // 🟠 opens a directory as a root for sandboxed file access; needed for sandbox.
	"os.PathError",                         // 🟢 error type wrapping path and operation; pure type.
	"os.Root",                              // 🟠 sandboxed directory root type; core of the filesystem sandbox.
	"os.Stat",                              // 🟠 returns file info for a path; needed for sandbox path validation.
	"path/filepath.Abs",                    // 🟢 returns absolute path; pure path computation.
	"path/filepath.Clean",                  // 🟢 normalizes a path; pure function, no I/O.
	"path/filepath.Dir",                    // 🟢 returns directory portion of a path; pure function, no I/O.
	"path/filepath.EvalSymlinks",           // 🟠 resolves symlinks via os.Lstat; the sandbox uses this at setup time to record canonical root paths so builtins like `pwd -P` can reflect the symlink resolution that os.Root has implicitly followed.
	"path/filepath.IsAbs",                  // 🟢 checks if path is absolute; pure function, no I/O.
	"path/filepath.Join",                   // 🟢 joins path elements; pure function, no I/O.
	"path/filepath.Rel",                    // 🟢 returns relative path; pure path computation.
	"path/filepath.Separator",              // 🟢 OS path separator constant; pure constant.
	"slices.SortFunc",                      // 🟢 sorts a slice with a comparison function; pure function, no I/O.
	"sync.Once",                            // 🟢 ensures one-time execution; used to close file descriptors at most once.
	"strings.Compare",                      // 🟢 compares two strings lexicographically; pure function, no I/O.
	"strings.EqualFold",                    // 🟢 case-insensitive string comparison; pure function, no I/O.
	"strings.HasPrefix",                    // 🟢 pure function for prefix matching; no I/O.
	"strings.Join",                         // 🟢 joins string slices; pure function, no I/O.
	"strings.Split",                        // 🟢 splits a string by separator; pure function, no I/O.
	"unsafe.Sizeof",                        // 🔴 computes native Windows OBJECT_ATTRIBUTES struct size for NtCreateFile; no pointer arithmetic or memory dereference.
	"golang.org/x/sys/unix.Close",          // 🟠 closes intermediate directory file descriptors opened during fd-relative write-path validation.
	"golang.org/x/sys/unix.ELOOP",          // 🟢 symlink-loop errno constant; normalized to permission denied for no-follow write opens.
	"golang.org/x/sys/unix.ENOTDIR",        // 🟢 not-a-directory errno constant; normalized when no-follow parent traversal rejects a symlink directory.
	"golang.org/x/sys/unix.ENXIO",          // 🟢 no-device errno constant; normalized when non-blocking write-open races to a FIFO.
	"golang.org/x/sys/unix.O_CLOEXEC",      // 🟢 close-on-exec open flag; prevents leaking validation descriptors to child processes.
	"golang.org/x/sys/unix.O_DIRECTORY",    // 🟢 directory-only open flag for parent component traversal.
	"golang.org/x/sys/unix.O_NOFOLLOW",     // 🟢 no-follow open flag; rejects symlink parent/final components during write opens.
	"golang.org/x/sys/unix.O_NONBLOCK",     // 🟢 non-blocking open flag; prevents blocking if a final component races to a FIFO.
	"golang.org/x/sys/unix.O_RDONLY",       // 🟢 read-only open flag for parent directory traversal.
	"golang.org/x/sys/unix.Openat",         // 🟠 fd-relative open used to keep no-symlink write validation tied to the opened parent directory.
	"golang.org/x/sys/windows.CloseHandle", // 🟠 closes intermediate Windows directory handles opened during fd-relative write-path validation.
	"golang.org/x/sys/windows.ERROR_INVALID_PARAMETER",          // 🟢 Windows errno constant; used to ignore unsupported truncation on special handles.
	"golang.org/x/sys/windows.FILE_APPEND_DATA",                 // 🟢 Windows access right; preserves append-only semantics when opening sandboxed write handles.
	"golang.org/x/sys/windows.FILE_ATTRIBUTE_NORMAL",            // 🟢 Windows file attribute constant for normal file creation.
	"golang.org/x/sys/windows.FILE_ATTRIBUTE_READONLY",          // 🟢 Windows file attribute constant mirroring non-writable creation modes.
	"golang.org/x/sys/windows.FILE_CREATE",                      // 🟢 Windows NtCreateFile disposition for exclusive creation.
	"golang.org/x/sys/windows.FILE_DIRECTORY_FILE",              // 🟢 Windows NtCreateFile option requiring an intermediate component to be a directory.
	"golang.org/x/sys/windows.FILE_GENERIC_READ",                // 🟠 Windows read access right for opening intermediate directories and read-write handles.
	"golang.org/x/sys/windows.FILE_GENERIC_WRITE",               // 🟠 Windows write access right for sandboxed write handles.
	"golang.org/x/sys/windows.FILE_LIST_DIRECTORY",              // 🟢 Windows directory-list access right for intermediate directory handles.
	"golang.org/x/sys/windows.FILE_NON_DIRECTORY_FILE",          // 🟢 Windows NtCreateFile option requiring the final component not to be a directory.
	"golang.org/x/sys/windows.FILE_OPEN",                        // 🟢 Windows NtCreateFile disposition for opening existing components.
	"golang.org/x/sys/windows.FILE_OPEN_FOR_BACKUP_INTENT",      // 🟠 Windows option matching Go's root open behavior for traversing directories with ACLs.
	"golang.org/x/sys/windows.FILE_OPEN_IF",                     // 🟢 Windows NtCreateFile disposition for create-if-missing write opens.
	"golang.org/x/sys/windows.FILE_READ_ATTRIBUTES",             // 🟢 Windows access right needed so os.File.Stat works on returned handles.
	"golang.org/x/sys/windows.FILE_READ_EA",                     // 🟢 Windows access right needed so os.File.Stat works on returned handles.
	"golang.org/x/sys/windows.FILE_SHARE_DELETE",                // 🟢 Windows share mode matching Go's root open behavior for race-safe traversal.
	"golang.org/x/sys/windows.FILE_SHARE_READ",                  // 🟢 Windows share mode allowing concurrent readers of sandbox-opened handles.
	"golang.org/x/sys/windows.FILE_SHARE_WRITE",                 // 🟢 Windows share mode allowing concurrent writers of sandbox-opened handles.
	"golang.org/x/sys/windows.FILE_SYNCHRONOUS_IO_NONALERT",     // 🟢 Windows option for synchronous file handles compatible with os.File.
	"golang.org/x/sys/windows.FILE_WRITE_DATA",                  // 🟢 Windows access bit removed for append-only handles unless truncation is requested.
	"golang.org/x/sys/windows.Handle",                           // 🟢 Windows file handle type; pure type alias.
	"golang.org/x/sys/windows.IO_STATUS_BLOCK",                  // 🟢 Windows NtCreateFile status structure; pure type.
	"golang.org/x/sys/windows.InvalidHandle",                    // 🟢 Windows invalid handle sentinel; pure constant.
	"golang.org/x/sys/windows.NTStatus",                         // 🟢 Windows NT status error type; used for deterministic errno normalization.
	"golang.org/x/sys/windows.NewNTUnicodeString",               // 🟠 converts one path component to the NT string form required by NtCreateFile.
	"golang.org/x/sys/windows.NtCreateFile",                     // 🟠 fd-relative Windows open used with OBJ_DONT_REPARSE to avoid following reparse points on write paths.
	"golang.org/x/sys/windows.OBJ_CASE_INSENSITIVE",             // 🟢 Windows object attribute matching normal case-insensitive path lookup.
	"golang.org/x/sys/windows.OBJ_DONT_REPARSE",                 // 🟠 Windows object attribute that rejects reparse points during component traversal.
	"golang.org/x/sys/windows.OBJECT_ATTRIBUTES",                // 🟢 Windows NtCreateFile object attributes structure; pure type.
	"golang.org/x/sys/windows.STANDARD_RIGHTS_READ",             // 🟢 Windows access right needed so os.File.Stat works on returned handles.
	"golang.org/x/sys/windows.STATUS_FILE_IS_A_DIRECTORY",       // 🟢 Windows NT status mapped to POSIX-style is-a-directory errors.
	"golang.org/x/sys/windows.STATUS_NOT_A_DIRECTORY",           // 🟢 Windows NT status mapped to permission denial for no-follow parent traversal.
	"golang.org/x/sys/windows.STATUS_OBJECT_NAME_COLLISION",     // 🟢 Windows NT status mapped to already-exists errors for exclusive creation.
	"golang.org/x/sys/windows.STATUS_REPARSE_POINT_ENCOUNTERED", // 🟢 Windows NT status mapped to permission denial for no-follow reparse point rejection.
	"golang.org/x/sys/windows.SYNCHRONIZE",                      // 🟢 Windows access right required for synchronous file handles.
	"syscall.ByHandleFileInformation",                           // 🟢 Windows file identity structure; pure type for file metadata.
	"syscall.EEXIST",                                            // 🟢 "file exists" errno constant; used to normalize Windows exclusive-create failures.
	"syscall.EISDIR",                                            // 🟢 "is a directory" errno constant; pure constant.
	"syscall.ELOOP",                                             // 🟢 "too many levels of symbolic links" errno constant; used to normalize no-follow write-open rejections.
	"syscall.ENOTDIR",                                           // 🟢 "not a directory" errno constant; used to normalize no-follow parent traversal failures.
	"syscall.Errno",                                             // 🟢 system call error number type; pure type.
	"syscall.ERROR_FILE_NOT_FOUND",                              // 🟢 Windows errno constant returned for empty or missing path components.
	"syscall.EINVAL",                                            // 🟢 invalid argument errno constant used when os.NewFile rejects an invalid Windows handle.
	"syscall.FILE_TYPE_CHAR",                                    // 🟢 Windows file type constant; used to match Go truncation semantics for special handles.
	"syscall.FILE_TYPE_PIPE",                                    // 🟢 Windows file type constant; used to match Go truncation semantics for special handles.
	"syscall.Ftruncate",                                         // 🟠 truncates the already sandbox-opened Windows file handle when O_TRUNC is requested.
	"syscall.GetFileInformationByHandle",                        // 🟠 Windows API for file identity (vol serial + file index); read-only syscall.
	"syscall.GetFileType",                                       // 🟠 reads the type of an already-open Windows handle to preserve Go's O_TRUNC special-file behavior.
	"syscall.Handle",                                            // 🟢 Windows file handle type; pure type alias.
	"syscall.O_NONBLOCK",                                        // 🟢 non-blocking open flag; prevents blocking on FIFOs during access checks. Pure constant.
	"syscall.O_NOFOLLOW",                                        // 🟢 no-follow open flag; prevents terminal symlink writes when opening sandboxed write targets.
	"syscall.S_IWRITE",                                          // 🟢 Windows write permission bit used to translate Go create modes into file attributes.
	"syscall.Stat_t",                                            // 🟢 file stat structure type; pure type for Unix file metadata.
}
