// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package analysis

// internalPerPackageSymbols maps each builtins/internal/<package> name to the
// symbols it is allowed to use. Every symbol listed here must also appear in
// internalAllowedSymbols (which acts as the global ceiling).
var internalPerPackageSymbols = map[string][]string{
	"diskstats": {
		"bufio.ErrTooLong",     // 🟢 sentinel error for scanner buffer overflow; pure constant.
		"bufio.NewScanner",     // 🟢 line-by-line reading of /proc/self/mountinfo; no write capability.
		"context.Context",      // 🟢 deadline/cancellation interface; no side effects.
		"errors.Is",            // 🟢 checks whether an error in a chain matches a target; pure function, no I/O.
		"errors.New",           // 🟢 creates a sentinel error (ErrNotSupported, ErrMaxMounts, ErrLineTooLong); pure function, no I/O.
		"fmt.Errorf",           // 🟢 error formatting; pure function, no I/O.
		"fmt.Sprintf",          // 🟢 string formatting; used to encode Statfs_t.Fsid as "major:minor"; pure function, no I/O.
		"io.Reader",            // 🟢 interface type used to feed parseMountInfo from arbitrary readers (tests use strings.NewReader); pure type, no I/O.
		"os.Open",              // 🟠 opens /proc/self/mountinfo read-only. Bypasses AllowedPaths by design — the path is hardcoded and never derived from user input, mirroring procnetsocket's documented exception.
		"strings.Builder",      // 🟢 in-memory buffer for octal-escape unescape of mountinfo paths; no I/O.
		"strings.Contains",     // 🟢 checks for ":" in a mount source string to detect host:/export remote mounts; pure function, no I/O.
		"strings.ContainsRune", // 🟢 fast-path check for backslash before unescape; pure function, no I/O.
		"strings.Cut",          // 🟢 splits a string at the first separator; pure function, no I/O.
		"strings.Fields",       // 🟢 splits whitespace-separated mountinfo fields; pure function, no I/O.
		"strings.HasPrefix",    // 🟢 checks remote-FS-type prefix and "//" UNC source prefix; pure function, no I/O.
		"golang.org/x/sys/unix.ByteSliceToString", // 🟢 converts a NUL-terminated kernel byte buffer to a Go string; pure function, no I/O.
		"golang.org/x/sys/unix.Getfsstat",         // 🟠 (darwin) read-only enumeration of mounted filesystems via getfsstat(2); no exec or write capability.
		"golang.org/x/sys/unix.MNT_LOCAL",         // 🟢 (darwin) flag constant indicating a local-only filesystem; pure constant.
		"golang.org/x/sys/unix.MNT_NOWAIT",        // 🟢 (darwin) flag constant: do not block on remote FS for getfsstat; pure constant.
		"golang.org/x/sys/unix.Statfs",            // 🟠 (linux) read-only filesystem usage syscall; no exec or write capability.
		"golang.org/x/sys/unix.Statfs_t",          // 🟢 struct type carrying filesystem usage data from statfs/getfsstat; pure data type.
	},
	"loopctl": {
		"strconv.Atoi", // 🟢 string-to-int conversion; pure function, no I/O.
	},
	"meminfo": {
		"bufio.NewScanner",  // 🟢 line-by-line reading of /proc/meminfo; no write capability.
		"context.Context",   // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",        // 🟢 creates a sentinel error (ErrNotSupported); pure function, no I/O.
		"fmt.Errorf",        // 🟢 error formatting; pure function, no I/O.
		"io.Reader",         // 🟢 interface type used to feed parseMeminfo from arbitrary readers (tests use strings.NewReader); pure type, no I/O.
		"os.Open",           // 🟠 opens /proc/meminfo read-only. Bypasses AllowedPaths by design — the path is hardcoded and never derived from user input, mirroring diskstats's documented exception.
		"strconv.ParseUint", // 🟢 parses the numeric KiB value out of each meminfo line; pure function, no I/O.
		"strings.Cut",       // 🟢 splits "Key:    Value kB" at the first colon; pure function, no I/O.
		"strings.CutSuffix", // 🟢 strips the trailing " kB" unit before parsing the number; pure function, no I/O.
		"strings.TrimSpace", // 🟢 trims whitespace between the colon and the value; pure function, no I/O.
	},
	"procinfo": {
		"bufio.NewScanner", // 🟢 line-by-line reading of /proc files; no write capability.
		"bytes.NewReader",  // 🟢 wraps a byte slice as an in-memory io.Reader; no I/O side effects.
		"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 canonical /proc filesystem root path constant; pure constant, no I/O.
		"context.Context",                       // 🟢 deadline/cancellation interface; no side effects.
		"errors.Is",                             // 🟢 checks whether an error in a chain matches a target; pure function, no I/O.
		"errors.New",                            // 🟢 creates a sentinel error (unsupported-platform stub); pure function, no I/O.
		"fmt.Errorf",                            // 🟢 error formatting; pure function, no I/O.
		"os.ErrNotExist",                        // 🟢 sentinel error value indicating a file or directory does not exist; read-only constant, no I/O.
		"fmt.Sprintf",                           // 🟢 string formatting; pure function, no I/O.
		"os.Getpid",                             // 🟠 returns the current process ID; read-only, no side effects.
		"os.Open",                               // 🟠 opens a file read-only; needed to stream /proc/stat line-by-line.
		"os.ReadDir",                            // 🟠 reads a directory listing; needed to enumerate /proc entries.
		"os.ReadFile",                           // 🟠 reads a whole file; needed to read /proc/[pid]/{stat,status}.
		"os.Stat",                               // 🟠 validates that the proc path exists before enumeration; read-only metadata, no write capability.
		"path/filepath.Join",                    // 🟢 joins path elements to construct /proc/<pid>/stat paths; pure function, no I/O.
		"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
		"strconv.Itoa",                          // 🟢 int-to-string conversion for PID directory names; pure function, no I/O.
		"strconv.ParseInt",                      // 🟢 string to int64 with base/bit-size; pure function, no I/O.
		"strings.Fields",                        // 🟢 splits a string on whitespace; pure function, no I/O.
		"strings.HasPrefix",                     // 🟢 checks string prefix; pure function, no I/O.
		"strings.Index",                         // 🟢 finds first occurrence of a substring; pure function, no I/O.
		"strings.LastIndex",                     // 🟢 finds last occurrence of a substring; pure function, no I/O.
		"strings.TrimSpace",                     // 🟢 removes leading/trailing whitespace; pure function, no I/O.
		"syscall.Getsid",                        // 🟠 returns the session ID of a process; read-only syscall, no write/exec.
		"time.Now",                              // 🟠 returns the current wall-clock time; read-only, no side effects.
		"time.Unix",                             // 🟢 constructs a Time from Unix seconds; pure function, no I/O.
		"golang.org/x/sys/unix.KinfoProc",       // 🟢 (darwin) struct type carrying per-process kinfo_proc data from sysctl; read-only data, no exec capability.
		"golang.org/x/sys/unix.SysctlKinfoProc", // 🟠 (darwin) reads a single process's kinfo_proc via kern.proc.pid sysctl; read-only, no exec or write capability.
		"golang.org/x/sys/unix.SysctlKinfoProcSlice",        // 🟠 (darwin) reads all processes' kinfo_proc via kern.proc.all sysctl; read-only, no exec or write capability.
		"golang.org/x/sys/windows.CloseHandle",              // 🟠 (windows) closes a process-snapshot handle after enumeration; no data read or exec capability.
		"golang.org/x/sys/windows.CreateToolhelp32Snapshot", // 🟠 (windows) creates a read-only snapshot of the process table; no exec or write capability.
		"golang.org/x/sys/windows.ERROR_NO_MORE_FILES",      // 🟢 (windows) sentinel error indicating end of process enumeration; pure constant.
		"golang.org/x/sys/windows.Process32First",           // 🟠 (windows) reads the first entry from a process snapshot; read-only, no exec capability.
		"golang.org/x/sys/windows.Process32Next",            // 🟠 (windows) advances to the next entry in a process snapshot; read-only, no exec capability.
		"golang.org/x/sys/windows.ProcessEntry32",           // 🟢 (windows) struct type holding process snapshot entry data; pure data type, no I/O.
		"golang.org/x/sys/windows.TH32CS_SNAPPROCESS",       // 🟢 (windows) flag constant selecting process entries for CreateToolhelp32Snapshot; pure constant.
		"golang.org/x/sys/windows.UTF16ToString",            // 🟢 (windows) converts a null-terminated UTF-16 slice to a Go string; pure function, no I/O.
	},
	"procpath": {
		// No stdlib symbols needed — this package only defines a string constant.
	},
	"sizeparse": {
		"errors.New",       // 🟢 creates sentinel parse errors; pure function, no I/O.
		"strconv.ParseInt", // 🟢 string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"procsyskernel": {
		"fmt.Errorf",          // 🟢 error formatting; pure function, no I/O.
		"io.LimitReader",      // 🟢 wraps a reader with a byte cap; pure wrapper, no I/O by itself.
		"io.ReadAll",          // 🟠 reads all data from a reader; bounded by io.LimitReader.
		"os.ModeCharDevice",   // 🟢 file mode constant; pure constant.
		"os.O_RDONLY",         // 🟢 read-only file flag; pure constant.
		"os.OpenFile",         // 🟠 opens kernel pseudo-files for reading; bypasses AllowedPaths by design.
		"path/filepath.Base",  // 🟢 returns the last element of a path; validates name is a plain basename.
		"path/filepath.Clean", // 🟢 normalises path before use; pure function, no I/O.
		"path/filepath.Join",  // 🟢 joins path elements; pure function, no I/O.
		"strings.Contains",    // 🟢 checks for ".." traversal in procPath; pure function, no I/O.
		"strings.TrimRight",   // 🟢 trims trailing characters; pure function, no I/O.
		"syscall.O_NONBLOCK",  // 🟢 non-blocking open flag; prevents FIFO hang. Pure constant.
	},
	"procnetroute": {
		"bufio.NewScanner", // 🟢 line-by-line reading of /proc/net/route; no write capability.
		"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 canonical /proc filesystem root path constant; pure constant, no I/O.
		"context.Context",          // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",               // 🟢 creates a sentinel error (non-Linux stub); pure function, no I/O.
		"fmt.Errorf",               // 🟢 error formatting for unsafe-path guard; pure function, no I/O.
		"fmt.Sprintf",              // 🟢 formats dotted-decimal IP strings; pure function, no I/O.
		"math/bits.OnesCount32",    // 🟢 counts set bits in a uint32 (popcount for prefix length); pure function, no I/O.
		"math/bits.ReverseBytes32", // 🟢 byte-swaps a uint32 to convert little-endian /proc mask to network byte order for CIDR validation; pure function, no I/O.
		"os.Open",                  // 🟠 opens /proc/net/route read-only; needed to stream the routing table.
		"path/filepath.Clean",      // 🟢 cleans procPath before ".." component check; pure function, no I/O.
		"path/filepath.Join",       // 🟢 joins procPath + "net/route"; pure function, no I/O.
		"strconv.ParseUint",        // 🟢 parses hex/decimal route fields; pure function, no I/O.
		"strings.Contains",         // 🟢 checks for ".." components in procPath safety guard; pure function, no I/O.
		"strings.Fields",           // 🟢 splits whitespace-separated route lines; pure function, no I/O.
	},
	"flagparser": {
		"errors.New",                             // 🟢 creates a sentinel error (noArgBool.Set rejects explicit values); pure function, no I/O.
		"github.com/spf13/pflag.ContinueOnError", // 🟢 pflag parsing-mode constant used to set up trial FlagSet; pure constant.
		"github.com/spf13/pflag.FlagSet",         // 🟢 pflag FlagSet type used to trial-parse argv prefixes; pure type, no I/O.
		"github.com/spf13/pflag.NewFlagSet",      // 🟢 constructs a throw-away FlagSet for trial-parsing; pure constructor, no I/O.
		"io.Discard",                             // 🟢 silences trial.SetOutput so trial-parse failures don't leak to stderr; pure no-op writer.
		"strings.Cut",                            // 🟢 splits pflag error/descriptor strings at separators; pure function, no I/O.
		"strings.CutPrefix",                      // 🟢 matches pflag error prefixes when rewriting to GNU wording; pure function, no I/O.
		"strings.HasPrefix",                      // 🟢 matches pflag error prefixes; pure function, no I/O.
		"strings.HasSuffix",                      // 🟢 matches pflag error suffixes (e.g. "flag does not allow an argument"); pure function, no I/O.
		"strings.LastIndex",                      // 🟢 finds the ", " separator in pflag flag descriptors; pure function, no I/O.
	},
	"procnetsocket": {
		"bufio.NewScanner", // 🟢 line-by-line reading of /proc/net/{tcp,udp,unix}; no write capability.
		"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 canonical /proc filesystem root path constant; pure constant, no I/O.
		"context.Context",     // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",          // 🟢 creates a sentinel error (non-Linux stub); pure function, no I/O.
		"fmt.Errorf",          // 🟢 error formatting; pure function, no I/O.
		"fmt.Sprintf",         // 🟢 formats dotted-decimal IP/port strings; pure function, no I/O.
		"os.Open",             // 🟠 opens /proc/net/tcp* and /proc/net/udp* read-only; needed to stream socket tables.
		"path/filepath.Clean", // 🟢 cleans procPath before ".." component check; pure function, no I/O.
		"path/filepath.Join",  // 🟢 joins procPath + "net/<file>"; pure function, no I/O.
		"strconv.FormatUint",  // 🟢 uint-to-string conversion for port/inode formatting; pure function, no I/O.
		"strconv.ParseUint",   // 🟢 parses hex/decimal socket fields; pure function, no I/O.
		"strings.Builder",     // 🟢 efficient string concatenation for IPv6 formatting; pure in-memory buffer, no I/O.
		"strings.Contains",    // 🟢 checks for ".." components in procPath safety guard; pure function, no I/O.
		"strings.Fields",      // 🟢 splits whitespace-separated socket lines; pure function, no I/O.
		"strings.Join",        // 🟢 reconstructs space-containing Unix socket paths from Fields tokens; pure function, no I/O.
		"strings.Split",       // 🟢 splits address:port fields on ":"; pure function, no I/O.
		"strings.ToUpper",     // 🟢 normalises hex state field to uppercase for map lookup; pure function, no I/O.
	},
	"vmstat": {
		"bufio.NewScanner",                   // 🟢 line-by-line reading of /proc/{stat,meminfo,vmstat,loadavg,uptime}; no write capability.
		"context.Context",                    // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",                         // 🟢 creates a sentinel error (ErrNotSupported, and a stub-platform error path); pure function, no I/O.
		"fmt.Errorf",                         // 🟢 error formatting; pure function, no I/O.
		"io.EOF",                             // 🟢 sentinel error value returned by bufio.Scanner at end of file; pure constant.
		"math.IsInf",                         // 🟢 rejects infinite load-average/uptime values; pure function, no I/O.
		"math.IsNaN",                         // 🟢 rejects NaN load-average/uptime values; pure function, no I/O.
		"math.MaxUint64",                     // 🟢 integer constant; bounds the /proc/meminfo KiB-to-bytes conversion against overflow; no side effects.
		"os.Getpagesize",                     // 🟢 returns the host's memory page size; read-only, no I/O.
		"os.Open",                            // 🟠 opens /proc/{stat,meminfo,vmstat,loadavg,uptime} read-only; needed to stream kernel pseudo-files.
		"path/filepath.Join",                 // 🟢 joins procPath + file name (e.g. "stat"); pure function, no I/O.
		"strconv.ParseFloat",                 // 🟢 parses load-average and uptime float fields; pure function, no I/O.
		"strconv.ParseUint",                  // 🟢 parses /proc counter fields; pure function, no I/O.
		"strings.Cut",                        // 🟢 splits a "Key: value" meminfo line at the first colon; pure function, no I/O.
		"strings.Fields",                     // 🟢 splits whitespace-separated /proc lines; pure function, no I/O.
		"strings.HasPrefix",                  // 🟢 matches /proc/stat line prefixes (cpu/intr/ctxt/procs_*); pure function, no I/O.
		"golang.org/x/sys/unix.Getpagesize",  // 🟢 (darwin) returns the host's memory page size; read-only, no I/O.
		"golang.org/x/sys/unix.SysctlRaw",    // 🟠 (darwin) reads raw sysctl byte replies (vm.swapusage, vm.loadavg); read-only, no exec or write capability.
		"golang.org/x/sys/unix.SysctlUint64", // 🟠 (darwin) reads a uint64 sysctl value (hw.memsize); read-only, no exec or write capability.
	},
	"winnet": {
		"encoding/binary.BigEndian",    // 🟢 reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
		"encoding/binary.LittleEndian", // 🟢 reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
		"errors.New",                   // 🟢 creates a sentinel error (non-Windows stub); pure function, no I/O.
		"fmt.Errorf",                   // 🟢 error formatting; pure function, no I/O.
		"fmt.Sprintf",                  // 🟢 string formatting; pure function, no I/O.
		"syscall.Errno",                // 🟢 wraps DLL return code as an error type; pure type, no I/O.
		"syscall.MustLoadDLL",          // 🔴 loads iphlpapi.dll once at program init; read-only OS loader call.
		"syscall.Proc",                 // 🟢 DLL procedure handle type used in function signature; pure type, no I/O.
		"unsafe.Pointer",               // 🔴 passes buffer/size pointers to DLL via syscall ABI. No pointer arithmetic; buffer parsed with encoding/binary after the call.
	},
	"winpoll": {
		"syscall.Errno",       // 🟢 (windows) error number type for distinguishing ERROR_BROKEN_PIPE from other PeekNamedPipe failures; pure type.
		"syscall.MustLoadDLL", // 🔴 (windows) loads kernel32.dll once at program init for PeekNamedPipe; read-only OS loader call.
		"unsafe.Pointer",      // 🔴 (windows) passes &avail to PeekNamedPipe via syscall ABI for non-consuming readability probe. Single call site; no pointer arithmetic — the returned uint32 is consumed directly.
		"golang.org/x/sys/windows.ERROR_BROKEN_PIPE",             // 🟢 (windows) sentinel error indicating the pipe's writer end has closed — used to recognize EOF-ready pipes for `read -t 0` POLLHUP-equivalent semantics; pure constant.
		"golang.org/x/sys/windows.FILE_TYPE_CHAR",                // 🟢 (windows) GetFileType result for console/character devices; pure constant.
		"golang.org/x/sys/windows.FILE_TYPE_DISK",                // 🟢 (windows) GetFileType result for regular files; pure constant.
		"golang.org/x/sys/windows.FILE_TYPE_PIPE",                // 🟢 (windows) GetFileType result for anonymous and named pipes; pure constant.
		"golang.org/x/sys/windows.FILE_TYPE_REMOTE",              // 🟢 (windows) GetFileType modifier bit for remote-mounted volumes; pure constant.
		"golang.org/x/sys/windows.GetFileType",                   // 🟠 (windows) returns the type (disk/pipe/char/remote/unknown) of an open handle; read-only metadata, no I/O.
		"golang.org/x/sys/windows.GetNumberOfConsoleInputEvents", // 🟠 (windows) reports the count of queued console input events without consuming them; read-only inspection.
		"golang.org/x/sys/windows.Handle",                        // 🟢 (windows) opaque file/handle type used to call PeekNamedPipe and GetFileType; pure type.
	},
}

// internalAllowedSymbols lists every "importpath.Symbol" permitted in
// builtins/internal/ helper packages. Each entry must be in
// "importpath.Symbol" form with a comment explaining safety.
// This is the global ceiling; each package's per-package allowlist is in
// internalPerPackageSymbols above.
//
// unsafe.Pointer is permitted here solely for winnet/winnet_windows.go, which
// must pass stack-addressed buffers to GetExtendedTcpTable/GetExtendedUdpTable
// via iphlpapi.dll. Usage is limited to two call sites; no unsafe pointer
// arithmetic occurs after the DLL call. All buffer parsing uses encoding/binary.
var internalAllowedSymbols = []string{
	"bufio.ErrTooLong", // 🟢 diskstats: sentinel error for scanner buffer overflow; pure constant.
	"bufio.NewScanner", // 🟢 procinfo/diskstats: line-by-line reading of /proc files; no write capability.
	"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 procinfo/procnet: canonical /proc filesystem root path constant; pure constant, no I/O.
	"bytes.NewReader",                            // 🟢 procinfo: wraps a byte slice as an in-memory io.Reader; no I/O side effects.
	"context.Context",                            // 🟢 procinfo: deadline/cancellation interface; no side effects.
	"encoding/binary.BigEndian",                  // 🟢 winnet: reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
	"encoding/binary.LittleEndian",               // 🟢 winnet: reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
	"errors.Is",                                  // 🟢 procinfo: checks whether an error in a chain matches a target; pure function, no I/O.
	"errors.New",                                 // 🟢 creates a sentinel error; pure function, no I/O.
	"github.com/spf13/pflag.ContinueOnError",     // 🟢 flagparser: pflag parsing-mode constant; pure constant, no I/O.
	"github.com/spf13/pflag.FlagSet",             // 🟢 flagparser: pflag FlagSet type used to trial-parse argv prefixes; pure type, no I/O.
	"github.com/spf13/pflag.NewFlagSet",          // 🟢 flagparser: constructs a throw-away FlagSet for trial-parsing; pure constructor, no I/O.
	"io.Discard",                                 // 🟢 flagparser: silences trial.SetOutput so trial-parse failures don't leak to stderr; pure no-op writer.
	"math/bits.OnesCount32",                      // 🟢 procnet: counts set bits in a uint32 (popcount for prefix length); pure function, no I/O.
	"math/bits.ReverseBytes32",                   // 🟢 procnet: byte-swaps a uint32 to convert little-endian /proc mask to network byte order for CIDR validation; pure function, no I/O.
	"fmt.Errorf",                                 // 🟢 error formatting; pure function, no I/O.
	"os.ErrNotExist",                             // 🟢 procinfo: sentinel error value indicating a file or directory does not exist; read-only constant, no I/O.
	"fmt.Sprintf",                                // 🟢 string formatting; pure function, no I/O.
	"io.LimitReader",                             // 🟢 procsyskernel: wraps a reader with a byte cap; pure wrapper, no I/O by itself.
	"io.ReadAll",                                 // 🟠 procsyskernel: reads all data from a bounded reader; used with LimitReader for 4KiB cap.
	"io.Reader",                                  // 🟢 diskstats: interface type used to feed parseMountInfo from arbitrary readers; pure type, no I/O.
	"os.Getpid",                                  // 🟠 procinfo: returns the current process ID; read-only, no side effects.
	"os.ModeCharDevice",                          // 🟢 procsyskernel: file mode constant for char device detection; pure constant.
	"os.O_RDONLY",                                // 🟢 procsyskernel: read-only open flag; pure constant.
	"os.Open",                                    // 🟠 procinfo: opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.OpenFile",                                // 🟠 procsyskernel: opens kernel pseudo-files with O_NONBLOCK; bypasses AllowedPaths by design.
	"os.ReadDir",                                 // 🟠 procinfo: reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",                                // 🟠 procinfo: reads a whole file; needed to read /proc/[pid]/{stat,status}.
	"os.Stat",                                    // 🟠 procinfo: validates that the proc path exists before enumeration; read-only metadata, no write capability.
	"path/filepath.Base",                         // 🟢 procsyskernel: returns the last element of a path; validates name is a plain basename.
	"path/filepath.Clean",                        // 🟢 procnetroute/procnetsocket: normalises procPath before ".." safety check; pure function, no I/O.
	"path/filepath.Join",                         // 🟢 procinfo: joins path elements to construct /proc/<pid>/stat paths; pure function, no I/O.
	"strconv.Atoi",                               // 🟢 string-to-int conversion; pure function, no I/O.
	"strconv.Itoa",                               // 🟢 procinfo: int-to-string conversion for PID directory names; pure function, no I/O.
	"strconv.ParseInt",                           // 🟢 procinfo: string to int64 with base/bit-size; pure function, no I/O.
	"strconv.FormatUint",                         // 🟢 procnetsocket: uint-to-string conversion for port/inode formatting; pure function, no I/O.
	"strconv.ParseUint",                          // 🟢 procnetroute/procnetsocket: parses hex/decimal route and socket fields; pure function, no I/O.
	"strings.Builder",                            // 🟢 procnetsocket/diskstats: efficient string concatenation; pure in-memory buffer, no I/O.
	"strings.Contains",                           // 🟢 procnetroute/diskstats: substring check; pure function, no I/O.
	"strings.ContainsRune",                       // 🟢 diskstats: fast-path check for backslash before unescape; pure function, no I/O.
	"strings.Cut",                                // 🟢 diskstats: splits a string at the first separator; pure function, no I/O.
	"strings.Fields",                             // 🟢 procinfo/procnetroute/procnetsocket/diskstats: splits a string on whitespace; pure function, no I/O.
	"strings.Join",                               // 🟢 procnetsocket: reconstructs space-containing Unix socket paths from Fields tokens; pure function, no I/O.
	"strings.Split",                              // 🟢 procnetsocket: splits address:port fields on ":"; pure function, no I/O.
	"strings.ToUpper",                            // 🟢 procnetsocket: normalises hex state field to uppercase for map lookup; pure function, no I/O.
	"strings.CutPrefix",                          // 🟢 flagparser: trims known pflag error prefixes before rewriting; pure function, no I/O.
	"strings.CutSuffix",                          // 🟢 meminfo: strips the trailing " kB" unit before parsing a /proc/meminfo value; pure function, no I/O.
	"strings.HasPrefix",                          // 🟢 procinfo/diskstats: checks string prefix; pure function, no I/O.
	"strings.HasSuffix",                          // 🟢 flagparser: matches pflag error suffixes (e.g. "flag does not allow an argument"); pure function, no I/O.
	"strings.Index",                              // 🟢 procinfo: finds first occurrence of a substring; pure function, no I/O.
	"strings.LastIndex",                          // 🟢 procinfo: finds last occurrence of a substring; pure function, no I/O.
	"strings.TrimRight",                          // 🟢 procinfo: trims trailing characters; pure function, no I/O.
	"strings.TrimSpace",                          // 🟢 procinfo: removes leading/trailing whitespace; pure function, no I/O.
	"syscall.Errno",                              // 🟢 winnet: wraps DLL return code as an error type; pure type, no I/O.
	"syscall.Getsid",                             // 🟠 procinfo: returns the session ID of a process; read-only syscall, no write/exec.
	"syscall.O_NONBLOCK",                         // 🟢 procsyskernel: non-blocking open flag to prevent FIFO hang; pure constant.
	"syscall.MustLoadDLL",                        // 🔴 winnet: loads iphlpapi.dll once at program init; read-only OS loader call.
	"syscall.Proc",                               // 🟢 winnet: DLL procedure handle type used in function signature; pure type, no I/O.
	"time.Now",                                   // 🟠 procinfo: returns the current wall-clock time; read-only, no side effects.
	"time.Unix",                                  // 🟢 procinfo: constructs a Time from Unix seconds; pure function, no I/O.
	"unsafe.Pointer",                             // 🔴 winnet: passes buffer/size pointers to DLL via syscall ABI. No pointer arithmetic; buffer parsed with encoding/binary after the call.
	"golang.org/x/sys/unix.ByteSliceToString",    // 🟢 diskstats (darwin): converts a NUL-terminated kernel byte buffer to a Go string; pure function, no I/O.
	"golang.org/x/sys/unix.Getfsstat",            // 🟠 diskstats (darwin): read-only enumeration of mounted filesystems via getfsstat(2); no exec or write capability.
	"golang.org/x/sys/unix.KinfoProc",            // 🟢 procinfo (darwin): struct type carrying per-process kinfo_proc data from sysctl; read-only data, no exec capability.
	"golang.org/x/sys/unix.MNT_LOCAL",            // 🟢 diskstats (darwin): flag constant indicating a local-only filesystem; pure constant.
	"golang.org/x/sys/unix.MNT_NOWAIT",           // 🟢 diskstats (darwin): flag constant: do not block on remote FS for getfsstat; pure constant.
	"golang.org/x/sys/unix.Statfs",               // 🟠 diskstats (linux): read-only filesystem usage syscall; no exec or write capability.
	"golang.org/x/sys/unix.Statfs_t",             // 🟢 diskstats: struct type carrying filesystem usage data from statfs/getfsstat; pure data type.
	"golang.org/x/sys/unix.SysctlKinfoProc",      // 🟠 procinfo (darwin): reads a single process's kinfo_proc via kern.proc.pid sysctl; read-only, no exec or write capability.
	"golang.org/x/sys/unix.SysctlKinfoProcSlice", // 🟠 procinfo (darwin): reads all processes' kinfo_proc via kern.proc.all sysctl; read-only, no exec or write capability.
	"golang.org/x/sys/windows.CloseHandle",       // 🟠 procinfo (windows): closes a process-snapshot handle after enumeration; no data read or exec capability.
	"io.EOF",                                     // 🟢 vmstat: sentinel error value returned by bufio.Scanner at end of file; pure constant.
	"math.IsInf",                                 // 🟢 vmstat: rejects infinite load-average/uptime values; pure function, no I/O.
	"math.IsNaN",                                 // 🟢 vmstat: rejects NaN load-average/uptime values; pure function, no I/O.
	"math.MaxUint64",                             // 🟢 vmstat: integer constant; bounds the /proc/meminfo KiB-to-bytes conversion against overflow; no side effects.
	"os.Getpagesize",                             // 🟢 vmstat: returns the host's memory page size; read-only, no I/O.
	"strconv.ParseFloat",                         // 🟢 vmstat: parses load-average and uptime float fields; pure function, no I/O.
	"golang.org/x/sys/unix.Getpagesize",          // 🟢 vmstat (darwin): returns the host's memory page size; read-only, no I/O.
	"golang.org/x/sys/unix.SysctlRaw",            // 🟠 vmstat (darwin): reads raw sysctl byte replies (vm.swapusage, vm.loadavg); read-only, no exec or write capability.
	"golang.org/x/sys/unix.SysctlUint64",         // 🟠 vmstat (darwin): reads a uint64 sysctl value (hw.memsize); read-only, no exec or write capability.
	"golang.org/x/sys/windows.CreateToolhelp32Snapshot",      // 🟠 procinfo (windows): creates a read-only snapshot of the process table; no exec or write capability.
	"golang.org/x/sys/windows.ERROR_BROKEN_PIPE",             // 🟢 winpoll (windows): sentinel error from PeekNamedPipe when the writer end has closed — used to recognize EOF-ready pipes; pure constant.
	"golang.org/x/sys/windows.ERROR_NO_MORE_FILES",           // 🟢 procinfo (windows): sentinel error indicating end of process enumeration; pure constant.
	"golang.org/x/sys/windows.FILE_TYPE_CHAR",                // 🟢 winpoll (windows): GetFileType result for console/character devices; pure constant.
	"golang.org/x/sys/windows.FILE_TYPE_DISK",                // 🟢 winpoll (windows): GetFileType result for regular files; pure constant.
	"golang.org/x/sys/windows.FILE_TYPE_PIPE",                // 🟢 winpoll (windows): GetFileType result for anonymous and named pipes; pure constant.
	"golang.org/x/sys/windows.FILE_TYPE_REMOTE",              // 🟢 winpoll (windows): GetFileType modifier bit for remote-mounted volumes; pure constant.
	"golang.org/x/sys/windows.GetFileType",                   // 🟠 winpoll (windows): returns the type (disk/pipe/char/remote/unknown) of an open handle; read-only metadata, no I/O.
	"golang.org/x/sys/windows.GetNumberOfConsoleInputEvents", // 🟠 winpoll (windows): reports the count of queued console input events without consuming them; read-only inspection.
	"golang.org/x/sys/windows.Handle",                        // 🟢 winpoll (windows): opaque file/handle type used to call PeekNamedPipe and GetFileType; pure type.
	"golang.org/x/sys/windows.Process32First",                // 🟠 procinfo (windows): reads the first entry from a process snapshot; read-only, no exec capability.
	"golang.org/x/sys/windows.Process32Next",                 // 🟠 procinfo (windows): advances to the next entry in a process snapshot; read-only, no exec capability.
	"golang.org/x/sys/windows.ProcessEntry32",                // 🟢 procinfo (windows): struct type holding process snapshot entry data; pure data type, no I/O.
	"golang.org/x/sys/windows.TH32CS_SNAPPROCESS",            // 🟢 procinfo (windows): flag constant selecting process entries for CreateToolhelp32Snapshot; pure constant.
	"golang.org/x/sys/windows.UTF16ToString",                 // 🟢 procinfo (windows): converts a null-terminated UTF-16 slice to a Go string; pure function, no I/O.
}
