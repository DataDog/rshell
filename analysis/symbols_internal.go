// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package analysis

// internalPerPackageSymbols maps each builtins/internal/<package> name to the
// symbols it is allowed to use. Every symbol listed here must also appear in
// internalAllowedSymbols (which acts as the global ceiling).
var internalPerPackageSymbols = map[string][]string{
	"pyruntime": {
		"bufio.NewReader",                 // 🟢 wraps an io.Reader with buffering for readline support; no write capability.
		"bufio.Reader",                    // 🟢 type reference for buffered reader; no write capability.
		"context.Background",              // 🟢 returns the background context used for Open calls within Python open(); no side effects.
		"context.Context",                 // 🟢 deadline/cancellation interface; no side effects.
		"encoding/base64.RawStdEncoding",  // 🟢 base64 encoding/decoding without padding; pure function, no I/O.
		"encoding/base64.StdEncoding",     // 🟢 base64 encoding/decoding of byte data; pure function, no I/O.
		"encoding/hex.DecodeString",       // 🟢 hex decoding; pure function, no I/O.
		"encoding/hex.EncodeToString",     // 🟢 hex encoding; pure function, no I/O.
		"fmt.Errorf",                      // 🟢 error formatting; pure function, no I/O.
		"fmt.Fprint",                      // 🟢 writes to a writer; used only for output to stdout/stderr, no file-write capability.
		"fmt.Fprintf",                     // 🟢 formats and writes to a writer; used only for error output to stderr.
		"fmt.Fprintln",                    // 🟢 writes formatted line to a writer; used only for traceback output to stderr.
		"fmt.Sprintf",                     // 🟢 string formatting; pure function, no I/O.
		"hash/crc32.IEEETable",            // 🟢 precomputed CRC32 lookup table constant; pure constant.
		"hash/crc32.Update",               // 🟢 incremental CRC32 update; pure function, no I/O.
		"io.EOF",                          // 🟢 sentinel error value for end-of-file; read-only constant, no I/O.
		"io.LimitReader",                  // 🟢 wraps a reader with a byte cap to prevent memory exhaustion; pure wrapper, no I/O by itself.
		"io.ReadAll",                      // 🟠 reads all bytes from a reader; always bounded by io.LimitReader in this package.
		"io.ReadWriteCloser",              // 🟢 type reference for sandbox file handle; no write capability (write mode is blocked).
		"io.Reader",                       // 🟢 type reference for stdin reader; no write capability.
		"io.Writer",                       // 🟢 type reference for stdout/stderr writers; used only for output, not file writes.
		"math.Abs",                        // 🟢 absolute value; pure function, no I/O.
		"math.Acos",                       // 🟢 arc cosine for Python math module; pure function, no I/O.
		"math.Asin",                       // 🟢 arc sine for Python math module; pure function, no I/O.
		"math.Atan",                       // 🟢 arc tangent for Python math module; pure function, no I/O.
		"math.Atan2",                      // 🟢 two-argument arc tangent for Python math module; pure function, no I/O.
		"math.Ceil",                       // 🟢 ceiling function; pure function, no I/O.
		"math.Cos",                        // 🟢 cosine; pure function, no I/O.
		"math.E",                          // 🟢 Euler's number constant; pure constant.
		"math.Exp",                        // 🟢 exponential; pure function, no I/O.
		"math.Floor",                      // 🟢 floor function; pure function, no I/O.
		"math.Inf",                        // 🟢 returns infinity; pure function, no I/O.
		"math.IsInf",                      // 🟢 checks for infinity; pure function, no I/O.
		"math.IsNaN",                      // 🟢 checks for NaN; pure function, no I/O.
		"math.Log",                        // 🟢 natural logarithm; pure function, no I/O.
		"math.Log10",                      // 🟢 base-10 logarithm; pure function, no I/O.
		"math.Log2",                       // 🟢 base-2 logarithm; pure function, no I/O.
		"math.NaN",                        // 🟢 returns NaN; pure function, no I/O.
		"math.Pi",                         // 🟢 pi constant; pure constant.
		"math.Mod",                        // 🟢 floating-point modulo for Python float %; pure function, no I/O.
		"math.Pow",                        // 🟢 power function; pure function, no I/O.
		"math.Pow10",                      // 🟢 power of 10 for float formatting; pure function, no I/O.
		"math.RoundToEven",                // 🟢 banker's rounding for Python round(); pure function, no I/O.
		"math.Sin",                        // 🟢 sine; pure function, no I/O.
		"math.Sqrt",                       // 🟢 square root; pure function, no I/O.
		"math.Tan",                        // 🟢 tangent; pure function, no I/O.
		"math.Hypot",                      // 🟢 Euclidean norm for Python math.hypot(); pure function, no I/O.
		"math.Trunc",                      // 🟢 truncate to integer for Python math.trunc(); pure function, no I/O.
		"math/big.Float",                  // 🟢 arbitrary-precision float type; pure in-memory computation, no I/O.
		"math/big.Int",                    // 🟢 arbitrary-precision integer type; pure in-memory computation, no I/O.
		"math/big.NewInt",                 // 🟢 creates arbitrary-precision integer; pure function, no I/O.
		"io/fs.DirEntry",                  // 🟢 interface type for directory entries returned by ReadDir callback; no I/O by itself.
		"io/fs.FileInfo",                  // 🟢 interface type for file metadata returned by Stat callback; no I/O by itself.
		"os.DevNull",                      // 🟢 device null path constant; pure constant.
		"os.FileMode",                     // 🟢 file mode type; used only as argument type in the Open callback signature.
		"os.IsNotExist",                   // 🟢 checks whether an error indicates file-not-found; pure predicate, no I/O.
		"os.O_RDONLY",                     // 🟢 read-only file flag; pure constant.
		"path/filepath.Abs",               // 🟢 resolves a relative path to absolute; pure function, no I/O beyond cwd read.
		"path/filepath.Base",              // 🟢 returns the last element of a path for os.path.basename(); pure function, no I/O.
		"path/filepath.Clean",             // 🟢 normalises path before use; pure function, no I/O.
		"path/filepath.Dir",               // 🟢 returns directory component of path; pure function, no I/O.
		"path/filepath.Ext",               // 🟢 returns file extension; pure function, no I/O.
		"path/filepath.Join",              // 🟢 joins path elements; pure function, no I/O.
		"path/filepath.ListSeparator",     // 🟢 OS path list separator constant; pure constant.
		"path/filepath.Separator",         // 🟢 OS path separator constant; pure constant.
		"strconv.Atoi",                    // 🟢 string-to-int conversion; pure function, no I/O.
		"strconv.FormatFloat",             // 🟢 float to string conversion; pure function, no I/O.
		"strconv.FormatInt",               // 🟢 int to string conversion; pure function, no I/O.
		"strconv.ParseFloat",              // 🟢 string to float conversion; pure function, no I/O.
		"strconv.ParseInt",                // 🟢 string to int64 with base; pure function, no I/O.
		"strconv.ParseUint",               // 🟢 string to uint64 with base; pure function, no I/O.
		"strings.Builder",                 // 🟢 efficient in-memory string builder; pure in-memory buffer, no I/O.
		"strings.ContainsAny",             // 🟢 checks if string contains any of a set of runes; pure function, no I/O.
		"strings.ContainsRune",            // 🟢 checks if a rune appears in a string (used to detect binary mode 'b'); pure function, no I/O.
		"strings.Count",                   // 🟢 counts non-overlapping instances of a substring; pure function, no I/O.
		"strings.Fields",                  // 🟢 splits a string on whitespace; pure function, no I/O.
		"strings.HasPrefix",               // 🟢 checks string prefix; pure function, no I/O.
		"strings.HasSuffix",               // 🟢 checks string suffix; pure function, no I/O.
		"strings.Index",                   // 🟢 finds first occurrence of a substring; pure function, no I/O.
		"strings.IndexAny",                // 🟢 finds first occurrence of any rune in a string; pure function, no I/O.
		"strings.Join",                    // 🟢 joins strings with a separator for str.join(); pure function, no I/O.
		"strings.LastIndex",               // 🟢 finds last occurrence of a substring; pure function, no I/O.
		"strings.NewReader",               // 🟢 creates an in-memory io.Reader from a string (empty stdin fallback); pure function, no I/O.
		"strings.Repeat",                  // 🟢 repeats a string n times; pure function, no I/O.
		"strings.Replace",                 // 🟢 replaces occurrences of a substring; pure function, no I/O.
		"strings.ReplaceAll",              // 🟢 replaces all occurrences of a substring; pure function, no I/O.
		"strings.Split",                   // 🟢 splits string on a separator; pure function, no I/O.
		"strings.SplitN",                  // 🟢 splits string into at most n substrings; pure function, no I/O.
		"strings.Title",                   // 🟢 title-cases words in a string; pure function, no I/O.
		"strings.ToLower",                 // 🟢 converts string to lowercase; pure function, no I/O.
		"strings.ToUpper",                 // 🟢 converts string to uppercase; pure function, no I/O.
		"strings.Trim",                    // 🟢 trims leading and trailing characters; pure function, no I/O.
		"strings.TrimLeft",                // 🟢 trims leading characters; pure function, no I/O.
		"strings.TrimLeftFunc",            // 🟢 trims leading runes matching a predicate; pure function, no I/O.
		"strings.TrimRight",               // 🟢 trims trailing characters; pure function, no I/O.
		"strings.TrimRightFunc",           // 🟢 trims trailing runes matching a predicate; pure function, no I/O.
		"strings.TrimSpace",               // 🟢 removes leading/trailing whitespace; pure function, no I/O.
		"strings.TrimSuffix",              // 🟢 trims a suffix from a string; pure function, no I/O.
		"math.MaxInt64",                   // 🟢 maximum int64 constant; used for bounds checks in integer conversions; pure constant.
		"unicode.IsDigit",                 // 🟢 checks if a rune is a digit; pure function, no I/O.
		"unicode.IsLetter",                // 🟢 checks if a rune is a letter; pure function, no I/O.
		"unicode.MaxRune",                 // 🟢 maximum valid Unicode code point constant; used for rune range checks; pure constant.
		"unicode/utf8.DecodeRuneInString", // 🟢 decodes the first rune of a string; pure function, no I/O.
		"unicode/utf8.RuneCountInString",  // 🟢 counts runes in a string; pure function, no I/O.
		"unicode/utf8.RuneLen",            // 🟢 returns the number of bytes required to encode a rune; pure function, no I/O.
		"unicode/utf8.ValidString",        // 🟢 checks if a string is valid UTF-8; pure function, no I/O.
		"runtime.Stack",                   // 🟢 reads current goroutine stack header to extract goroutine ID for per-goroutine callObject dispatch; read-only, no exec capability.
		"sync.Map",                        // 🟢 concurrent-safe map for per-goroutine callObject registration; no I/O, no side effects.
	},
	"loopctl": {
		"strconv.Atoi", // 🟢 string-to-int conversion; pure function, no I/O.
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
		"os.ReadFile",                           // 🟠 reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
		"os.Stat",                               // 🟠 validates that the proc path exists before enumeration; read-only metadata, no write capability.
		"path/filepath.Join",                    // 🟢 joins path elements to construct /proc/<pid>/stat paths; pure function, no I/O.
		"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
		"strconv.Itoa",                          // 🟢 int-to-string conversion for PID directory names; pure function, no I/O.
		"strconv.ParseInt",                      // 🟢 string to int64 with base/bit-size; pure function, no I/O.
		"strings.Fields",                        // 🟢 splits a string on whitespace; pure function, no I/O.
		"strings.HasPrefix",                     // 🟢 checks string prefix; pure function, no I/O.
		"strings.Index",                         // 🟢 finds first occurrence of a substring; pure function, no I/O.
		"strings.LastIndex",                     // 🟢 finds last occurrence of a substring; pure function, no I/O.
		"strings.TrimRight",                     // 🟢 trims trailing characters; pure function, no I/O.
		"strings.TrimSpace",                     // 🟢 removes leading/trailing whitespace; pure function, no I/O.
		"syscall.Getsid",                        // 🟠 returns the session ID of a process; read-only syscall, no write/exec.
		"time.Now",                              // 🟠 returns the current wall-clock time; read-only, no side effects.
		"time.Unix",                             // 🟢 constructs a Time from Unix seconds; pure function, no I/O.
		"golang.org/x/sys/unix.KinfoProc",       // 🟢 (darwin) struct type carrying per-process kinfo_proc data from sysctl; read-only data, no exec capability.
		"golang.org/x/sys/unix.SysctlKinfoProc", // 🟠 (darwin) reads a single process's kinfo_proc via kern.proc.pid sysctl; read-only, no exec or write capability.
		"golang.org/x/sys/unix.SysctlKinfoProcSlice",        // 🟠 (darwin) reads all processes' kinfo_proc via kern.proc.all sysctl; read-only, no exec or write capability.
		"golang.org/x/sys/unix.SysctlRaw",                   // 🟠 (darwin) reads raw kern.procargs2 sysctl buffer per-PID to obtain argv; read-only, no exec capability.
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
	// pyruntime
	"bufio.NewReader",                 // 🟢 pyruntime: wraps an io.Reader with buffering for readline support; no write capability.
	"bufio.Reader",                    // 🟢 pyruntime: buffered reader type reference; no write capability.
	"context.Background",              // 🟢 pyruntime: returns background context for sandbox open() calls; no side effects.
	"encoding/base64.StdEncoding",     // 🟢 pyruntime: base64 encoding/decoding in the binascii module; pure function, no I/O.
	"encoding/base64.RawStdEncoding",  // 🟢 pyruntime: base64 encoding without padding in binascii module; pure function, no I/O.
	"encoding/hex.DecodeString",       // 🟢 pyruntime: hex decoding in the binascii module; pure function, no I/O.
	"encoding/hex.EncodeToString",     // 🟢 pyruntime: hex encoding in the binascii module; pure function, no I/O.
	"fmt.Fprint",                      // 🟢 pyruntime: writes to stdout/stderr in Python print(); no file-write capability.
	"fmt.Fprintf",                     // 🟢 pyruntime: writes formatted error messages to stderr; no file-write capability.
	"fmt.Fprintln",                    // 🟢 pyruntime: writes formatted traceback lines to stderr; no file-write capability.
	"hash/crc32.IEEETable",            // 🟢 pyruntime: precomputed CRC32 table constant; pure constant.
	"hash/crc32.Update",               // 🟢 pyruntime: incremental CRC32 update in binascii module; pure function, no I/O.
	"io.EOF",                          // 🟢 pyruntime: end-of-file sentinel; read-only constant.
	"io.LimitReader",                  // 🟢 pyruntime/procsyskernel: wraps a reader with a byte cap; pure wrapper, no I/O by itself.
	"io.ReadAll",                      // 🟠 pyruntime/procsyskernel: reads all bytes from a bounded reader; always used with LimitReader.
	"io.ReadWriteCloser",              // 🟢 pyruntime: sandbox file handle type; write mode is blocked at runtime.
	"io.Reader",                       // 🟢 pyruntime: stdin reader type reference; no write capability.
	"io.Writer",                       // 🟢 pyruntime: stdout/stderr writer type reference; no file-write capability.
	"io/fs.DirEntry",                  // 🟢 pyruntime: interface type for directory entries returned by the ReadDir sandbox callback; no I/O by itself.
	"io/fs.FileInfo",                  // 🟢 pyruntime: interface type for file metadata returned by the Stat sandbox callback; no I/O by itself.
	"math.Abs",                        // 🟢 pyruntime: absolute value for Python math module; pure function, no I/O.
	"math.Acos",                       // 🟢 pyruntime: arc cosine for Python math module; pure function, no I/O.
	"math.Asin",                       // 🟢 pyruntime: arc sine for Python math module; pure function, no I/O.
	"math.Atan",                       // 🟢 pyruntime: arc tangent for Python math module; pure function, no I/O.
	"math.Atan2",                      // 🟢 pyruntime: two-argument arc tangent for Python math module; pure function, no I/O.
	"math.Ceil",                       // 🟢 pyruntime: ceiling for Python math module; pure function, no I/O.
	"math.Cos",                        // 🟢 pyruntime: cosine for Python math module; pure function, no I/O.
	"math.E",                          // 🟢 pyruntime: Euler's number constant; pure constant.
	"math.Exp",                        // 🟢 pyruntime: exponential for Python math module; pure function, no I/O.
	"math.Floor",                      // 🟢 pyruntime: floor for Python math module; pure function, no I/O.
	"math.Hypot",                      // 🟢 pyruntime: Euclidean norm for Python math.hypot(); pure function, no I/O.
	"math.Inf",                        // 🟢 pyruntime: returns infinity; pure function, no I/O.
	"math.IsInf",                      // 🟢 pyruntime: checks for infinity; pure function, no I/O.
	"math.IsNaN",                      // 🟢 pyruntime: checks for NaN; pure function, no I/O.
	"math.Log",                        // 🟢 pyruntime: natural logarithm for Python math module; pure function, no I/O.
	"math.Log10",                      // 🟢 pyruntime: base-10 logarithm for Python math module; pure function, no I/O.
	"math.Log2",                       // 🟢 pyruntime: base-2 logarithm for Python math module; pure function, no I/O.
	"math.Mod",                        // 🟢 pyruntime: floating-point modulo for Python float %; pure function, no I/O.
	"math.NaN",                        // 🟢 pyruntime: returns NaN; pure function, no I/O.
	"math.Pi",                         // 🟢 pyruntime: pi constant; pure constant.
	"math.Pow",                        // 🟢 pyruntime: power function for Python math module; pure function, no I/O.
	"math.Pow10",                      // 🟢 pyruntime: power of 10 for float formatting; pure function, no I/O.
	"math.RoundToEven",                // 🟢 pyruntime: banker's rounding for Python round(); pure function, no I/O.
	"math.Sin",                        // 🟢 pyruntime: sine for Python math module; pure function, no I/O.
	"math.Sqrt",                       // 🟢 pyruntime: square root for Python math module; pure function, no I/O.
	"math.Tan",                        // 🟢 pyruntime: tangent for Python math module; pure function, no I/O.
	"math.Trunc",                      // 🟢 pyruntime: truncate to integer for Python math.trunc(); pure function, no I/O.
	"math/big.Float",                  // 🟢 pyruntime: arbitrary-precision float type for Python big int arithmetic; pure in-memory computation.
	"math/big.Int",                    // 🟢 pyruntime: arbitrary-precision integer type for Python int arithmetic; pure in-memory computation.
	"math/big.NewInt",                 // 🟢 pyruntime: creates arbitrary-precision integer; pure function, no I/O.
	"os.DevNull",                      // 🟢 pyruntime: device null path constant for os.devnull in Python os module; pure constant.
	"os.FileMode",                     // 🟢 pyruntime: file mode type used in sandbox Open callback signature; pure type.
	"os.IsNotExist",                   // 🟢 pyruntime: file-not-found predicate; pure function, no I/O.
	"path/filepath.Abs",               // 🟢 pyruntime: resolves relative path to absolute for os.path.abspath(); pure function.
	"path/filepath.Dir",               // 🟢 pyruntime: returns directory component for os.path.dirname(); pure function, no I/O.
	"path/filepath.Ext",               // 🟢 pyruntime: returns file extension for os.path.splitext(); pure function, no I/O.
	"path/filepath.ListSeparator",     // 🟢 pyruntime: OS path list separator constant for os.pathsep; pure constant.
	"path/filepath.Separator",         // 🟢 pyruntime: OS path separator constant for os.sep; pure constant.
	"strconv.FormatFloat",             // 🟢 pyruntime: float-to-string conversion for Python repr/str; pure function, no I/O.
	"strconv.FormatInt",               // 🟢 pyruntime: int-to-string conversion for Python repr/str/bin/hex/oct; pure function, no I/O.
	"strconv.ParseFloat",              // 🟢 pyruntime: string-to-float conversion for float() builtin; pure function, no I/O.
	"strings.ContainsAny",             // 🟢 pyruntime: checks if string contains any rune from a set; pure function, no I/O.
	"strings.ContainsRune",            // 🟢 pyruntime: checks mode string for binary flag; pure function, no I/O.
	"strings.Count",                   // 🟢 pyruntime: counts non-overlapping substrings for str.count(); pure function, no I/O.
	"strings.HasSuffix",               // 🟢 pyruntime: checks string suffix for str.endswith(); pure function, no I/O.
	"strings.IndexAny",                // 🟢 pyruntime: finds first occurrence of any rune for string scanning; pure function, no I/O.
	"strings.NewReader",               // 🟢 pyruntime: creates in-memory reader from string (empty stdin fallback); pure function.
	"strings.Repeat",                  // 🟢 pyruntime: repeats a string n times for str*n operator; pure function, no I/O.
	"strings.Replace",                 // 🟢 pyruntime: replaces substring occurrences for str.replace(); pure function, no I/O.
	"strings.ReplaceAll",              // 🟢 pyruntime: replaces all occurrences for str.replace(); pure function, no I/O.
	"strings.SplitN",                  // 🟢 pyruntime: splits string for str.split(sep, maxsplit); pure function, no I/O.
	"strings.Title",                   // 🟢 pyruntime: title-cases words for str.title(); pure function, no I/O.
	"strings.ToLower",                 // 🟢 pyruntime: converts string to lowercase for str.lower(); pure function, no I/O.
	"strings.Trim",                    // 🟢 pyruntime: trims characters for str.strip(); pure function, no I/O.
	"strings.TrimLeft",                // 🟢 pyruntime: trims leading characters for str.lstrip(); pure function, no I/O.
	"strings.TrimLeftFunc",            // 🟢 pyruntime: trims leading runes matching predicate for str.lstrip(); pure function, no I/O.
	"strings.TrimRightFunc",           // 🟢 pyruntime: trims trailing runes matching predicate for str.rstrip(); pure function, no I/O.
	"strings.TrimSuffix",              // 🟢 pyruntime: trims a suffix; used for augmented assignment op stripping; pure function, no I/O.
	"math.MaxInt64",                   // 🟢 pyruntime: maximum int64 constant; used for bounds checks in integer conversions; pure constant.
	"unicode.IsDigit",                 // 🟢 pyruntime: checks if rune is digit for str.isdigit(); pure function, no I/O.
	"unicode.IsLetter",                // 🟢 pyruntime: checks if rune is letter for lexer identifier scanning; pure function, no I/O.
	"unicode.MaxRune",                 // 🟢 pyruntime: maximum valid Unicode code point constant; used for rune range checks; pure constant.
	"unicode/utf8.DecodeRuneInString", // 🟢 pyruntime: decodes first rune for lexer/string ops; pure function, no I/O.
	"unicode/utf8.RuneCountInString",  // 🟢 pyruntime: counts runes for len() on strings; pure function, no I/O.
	"unicode/utf8.RuneLen",            // 🟢 pyruntime: bytes required to encode a rune; pure function, no I/O.
	"unicode/utf8.ValidString",        // 🟢 pyruntime: checks if string is valid UTF-8 for str.isascii(); pure function, no I/O.
	"runtime.Stack",                   // 🟢 pyruntime: reads current goroutine stack header to extract goroutine ID for per-goroutine callObject dispatch; read-only, no exec capability.
	"sync.Map",                        // 🟢 pyruntime: concurrent-safe map for per-goroutine callObject registration; no I/O, no side effects.
	// procinfo
	"bufio.NewScanner", // 🟢 procinfo: line-by-line reading of /proc files; no write capability.
	"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 procinfo/procnet: canonical /proc filesystem root path constant; pure constant, no I/O.
	"bytes.NewReader",                       // 🟢 procinfo: wraps a byte slice as an in-memory io.Reader; no I/O side effects.
	"context.Context",                       // 🟢 procinfo: deadline/cancellation interface; no side effects.
	"encoding/binary.BigEndian",             // 🟢 winnet: reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
	"encoding/binary.LittleEndian",          // 🟢 winnet: reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
	"errors.Is",                             // 🟢 procinfo: checks whether an error in a chain matches a target; pure function, no I/O.
	"errors.New",                            // 🟢 creates a sentinel error; pure function, no I/O.
	"math/bits.OnesCount32",                 // 🟢 procnet: counts set bits in a uint32 (popcount for prefix length); pure function, no I/O.
	"math/bits.ReverseBytes32",              // 🟢 procnet: byte-swaps a uint32 to convert little-endian /proc mask to network byte order for CIDR validation; pure function, no I/O.
	"fmt.Errorf",                            // 🟢 error formatting; pure function, no I/O.
	"os.ErrNotExist",                        // 🟢 procinfo: sentinel error value indicating a file or directory does not exist; read-only constant, no I/O.
	"fmt.Sprintf",                           // 🟢 string formatting; pure function, no I/O.
	"io.LimitReader",                        // 🟢 procsyskernel: wraps a reader with a byte cap; pure wrapper, no I/O by itself.
	"io.ReadAll",                            // 🟠 procsyskernel: reads all data from a bounded reader; used with LimitReader for 4KiB cap.
	"os.Getpid",                             // 🟠 procinfo: returns the current process ID; read-only, no side effects.
	"os.ModeCharDevice",                     // 🟢 procsyskernel: file mode constant for char device detection; pure constant.
	"os.O_RDONLY",                           // 🟢 procsyskernel: read-only open flag; pure constant.
	"os.Open",                               // 🟠 procinfo: opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.OpenFile",                           // 🟠 procsyskernel: opens kernel pseudo-files with O_NONBLOCK; bypasses AllowedPaths by design.
	"os.ReadDir",                            // 🟠 procinfo: reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",                           // 🟠 procinfo: reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
	"os.Stat",                               // 🟠 procinfo: validates that the proc path exists before enumeration; read-only metadata, no write capability.
	"path/filepath.Base",                    // 🟢 procsyskernel: returns the last element of a path; validates name is a plain basename.
	"path/filepath.Clean",                   // 🟢 procnetroute/procnetsocket: normalises procPath before ".." safety check; pure function, no I/O.
	"path/filepath.Join",                    // 🟢 procinfo: joins path elements to construct /proc/<pid>/stat paths; pure function, no I/O.
	"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
	"strconv.Itoa",                          // 🟢 procinfo: int-to-string conversion for PID directory names; pure function, no I/O.
	"strconv.ParseInt",                      // 🟢 procinfo: string to int64 with base/bit-size; pure function, no I/O.
	"strconv.FormatUint",                    // 🟢 procnetsocket: uint-to-string conversion for port/inode formatting; pure function, no I/O.
	"strconv.ParseUint",                     // 🟢 procnetroute/procnetsocket: parses hex/decimal route and socket fields; pure function, no I/O.
	"strings.Builder",                       // 🟢 procnetsocket: efficient string concatenation for IPv6 formatting; pure in-memory buffer, no I/O.
	"strings.Contains",                      // 🟢 procnetroute: checks for ".." in procPath safety guard; pure function, no I/O.
	"strings.Fields",                        // 🟢 procinfo/procnetroute/procnetsocket: splits a string on whitespace; pure function, no I/O.
	"strings.Join",                          // 🟢 procnetsocket: reconstructs space-containing Unix socket paths from Fields tokens; pure function, no I/O.
	"strings.Split",                         // 🟢 procnetsocket: splits address:port fields on ":"; pure function, no I/O.
	"strings.ToUpper",                       // 🟢 procnetsocket: normalises hex state field to uppercase for map lookup; pure function, no I/O.
	"strings.HasPrefix",                     // 🟢 procinfo: checks string prefix; pure function, no I/O.
	"strings.Index",                         // 🟢 procinfo: finds first occurrence of a substring; pure function, no I/O.
	"strings.LastIndex",                     // 🟢 procinfo: finds last occurrence of a substring; pure function, no I/O.
	"strings.TrimRight",                     // 🟢 procinfo: trims trailing characters; pure function, no I/O.
	"strings.TrimSpace",                     // 🟢 procinfo: removes leading/trailing whitespace; pure function, no I/O.
	"syscall.Errno",                         // 🟢 winnet: wraps DLL return code as an error type; pure type, no I/O.
	"syscall.Getsid",                        // 🟠 procinfo: returns the session ID of a process; read-only syscall, no write/exec.
	"syscall.O_NONBLOCK",                    // 🟢 procsyskernel: non-blocking open flag to prevent FIFO hang; pure constant.
	"syscall.MustLoadDLL",                   // 🔴 winnet: loads iphlpapi.dll once at program init; read-only OS loader call.
	"syscall.Proc",                          // 🟢 winnet: DLL procedure handle type used in function signature; pure type, no I/O.
	"time.Now",                              // 🟠 procinfo: returns the current wall-clock time; read-only, no side effects.
	"time.Unix",                             // 🟢 procinfo: constructs a Time from Unix seconds; pure function, no I/O.
	"unsafe.Pointer",                        // 🔴 winnet: passes buffer/size pointers to DLL via syscall ABI. No pointer arithmetic; buffer parsed with encoding/binary after the call.
	"golang.org/x/sys/unix.KinfoProc",       // 🟢 procinfo (darwin): struct type carrying per-process kinfo_proc data from sysctl; read-only data, no exec capability.
	"golang.org/x/sys/unix.SysctlKinfoProc", // 🟠 procinfo (darwin): reads a single process's kinfo_proc via kern.proc.pid sysctl; read-only, no exec or write capability.
	"golang.org/x/sys/unix.SysctlKinfoProcSlice",        // 🟠 procinfo (darwin): reads all processes' kinfo_proc via kern.proc.all sysctl; read-only, no exec or write capability.
	"golang.org/x/sys/unix.SysctlRaw",                   // 🟠 procinfo (darwin): reads raw kern.procargs2 sysctl buffer per-PID to obtain argv; read-only, no exec capability.
	"golang.org/x/sys/windows.CloseHandle",              // 🟠 procinfo (windows): closes a process-snapshot handle after enumeration; no data read or exec capability.
	"golang.org/x/sys/windows.CreateToolhelp32Snapshot", // 🟠 procinfo (windows): creates a read-only snapshot of the process table; no exec or write capability.
	"golang.org/x/sys/windows.ERROR_NO_MORE_FILES",      // 🟢 procinfo (windows): sentinel error indicating end of process enumeration; pure constant.
	"golang.org/x/sys/windows.Process32First",           // 🟠 procinfo (windows): reads the first entry from a process snapshot; read-only, no exec capability.
	"golang.org/x/sys/windows.Process32Next",            // 🟠 procinfo (windows): advances to the next entry in a process snapshot; read-only, no exec capability.
	"golang.org/x/sys/windows.ProcessEntry32",           // 🟢 procinfo (windows): struct type holding process snapshot entry data; pure data type, no I/O.
	"golang.org/x/sys/windows.TH32CS_SNAPPROCESS",       // 🟢 procinfo (windows): flag constant selecting process entries for CreateToolhelp32Snapshot; pure constant.
	"golang.org/x/sys/windows.UTF16ToString",            // 🟢 procinfo (windows): converts a null-terminated UTF-16 slice to a Go string; pure function, no I/O.
}
