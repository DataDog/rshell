// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// internalPerPackageSymbols maps each builtins/internal/<package> name to the
// symbols it is allowed to use. Every symbol listed here must also appear in
// internalAllowedSymbols (which acts as the global ceiling).
var internalPerPackageSymbols = map[string][]string{
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
		"path/filepath.Join",       // 🟢 joins procPath + "net/route"; pure function, no I/O.
		"strconv.ParseUint",        // 🟢 parses hex/decimal route fields; pure function, no I/O.
		"strings.Contains",         // 🟢 checks for ".." components in procPath safety guard; pure function, no I/O.
		"strings.Fields",           // 🟢 splits whitespace-separated route lines; pure function, no I/O.
	},
	"procnetsocket": {
		"bufio.NewScanner", // 🟢 line-by-line reading of /proc/net/{tcp,udp,unix}; no write capability.
		"github.com/DataDog/rshell/builtins/internal/procpath.Default", // 🟢 canonical /proc filesystem root path constant; pure constant, no I/O.
		"context.Context",    // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",         // 🟢 creates a sentinel error (non-Linux stub); pure function, no I/O.
		"fmt.Errorf",         // 🟢 error formatting; pure function, no I/O.
		"fmt.Sprintf",        // 🟢 formats dotted-decimal IP/port strings; pure function, no I/O.
		"os.Open",            // 🟠 opens /proc/net/tcp* and /proc/net/udp* read-only; needed to stream socket tables.
		"path/filepath.Join", // 🟢 joins procPath + "net/<file>"; pure function, no I/O.
		"strconv.FormatUint", // 🟢 uint-to-string conversion for port/inode formatting; pure function, no I/O.
		"strconv.ParseUint",  // 🟢 parses hex/decimal socket fields; pure function, no I/O.
		"strings.Builder",    // 🟢 efficient string concatenation for IPv6 formatting; pure in-memory buffer, no I/O.
		"strings.Contains",   // 🟢 checks for ".." components in procPath safety guard; pure function, no I/O.
		"strings.Fields",     // 🟢 splits whitespace-separated socket lines; pure function, no I/O.
		"strings.Split",      // 🟢 splits address:port fields on ":"; pure function, no I/O.
		"strings.ToUpper",    // 🟢 normalises hex state field to uppercase for map lookup; pure function, no I/O.
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
	"os.Getpid",                             // 🟠 procinfo: returns the current process ID; read-only, no side effects.
	"os.Open",                               // 🟠 procinfo: opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.ReadDir",                            // 🟠 procinfo: reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",                           // 🟠 procinfo: reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
	"os.Stat",                               // 🟠 procinfo: validates that the proc path exists before enumeration; read-only metadata, no write capability.
	"path/filepath.Join",                    // 🟢 procinfo: joins path elements to construct /proc/<pid>/stat paths; pure function, no I/O.
	"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
	"strconv.Itoa",                          // 🟢 procinfo: int-to-string conversion for PID directory names; pure function, no I/O.
	"strconv.ParseInt",                      // 🟢 procinfo: string to int64 with base/bit-size; pure function, no I/O.
	"strconv.FormatUint",                    // 🟢 procnetsocket: uint-to-string conversion for port/inode formatting; pure function, no I/O.
	"strconv.ParseUint",                     // 🟢 procnetroute/procnetsocket: parses hex/decimal route and socket fields; pure function, no I/O.
	"strings.Builder",                       // 🟢 procnetsocket: efficient string concatenation for IPv6 formatting; pure in-memory buffer, no I/O.
	"strings.Contains",                      // 🟢 procnetroute: checks for ".." in procPath safety guard; pure function, no I/O.
	"strings.Fields",                        // 🟢 procinfo/procnetroute/procnetsocket: splits a string on whitespace; pure function, no I/O.
	"strings.Split",                         // 🟢 procnetsocket: splits address:port fields on ":"; pure function, no I/O.
	"strings.ToUpper",                       // 🟢 procnetsocket: normalises hex state field to uppercase for map lookup; pure function, no I/O.
	"strings.HasPrefix",                     // 🟢 procinfo: checks string prefix; pure function, no I/O.
	"strings.Index",                         // 🟢 procinfo: finds first occurrence of a substring; pure function, no I/O.
	"strings.LastIndex",                     // 🟢 procinfo: finds last occurrence of a substring; pure function, no I/O.
	"strings.TrimRight",                     // 🟢 procinfo: trims trailing characters; pure function, no I/O.
	"strings.TrimSpace",                     // 🟢 procinfo: removes leading/trailing whitespace; pure function, no I/O.
	"syscall.Errno",                         // 🟢 winnet: wraps DLL return code as an error type; pure type, no I/O.
	"syscall.Getsid",                        // 🟠 procinfo: returns the session ID of a process; read-only syscall, no write/exec.
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
