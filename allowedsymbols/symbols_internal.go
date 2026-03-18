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
		"bufio.NewScanner",                      // 🟢 line-by-line reading of /proc files; no write capability.
		"bytes.NewReader",                       // 🟢 wraps a byte slice as an in-memory io.Reader; no I/O side effects.
		"context.Context",                       // 🟢 deadline/cancellation interface; no side effects.
		"errors.New",                            // 🟢 creates a sentinel error (unsupported-platform stub); pure function, no I/O.
		"fmt.Errorf",                            // 🟢 error formatting; pure function, no I/O.
		"fmt.Sprintf",                           // 🟢 string formatting; pure function, no I/O.
		"os.Getpid",                             // 🟠 returns the current process ID; read-only, no side effects.
		"os.Open",                               // 🟠 opens a file read-only; needed to stream /proc/stat line-by-line.
		"os.ReadDir",                            // 🟠 reads a directory listing; needed to enumerate /proc entries.
		"os.ReadFile",                           // 🟠 reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
		"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
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
	"bufio.NewScanner",                      // 🟢 procinfo: line-by-line reading of /proc files; no write capability.
	"bytes.NewReader",                       // 🟢 procinfo: wraps a byte slice as an in-memory io.Reader; no I/O side effects.
	"context.Context",                       // 🟢 procinfo: deadline/cancellation interface; no side effects.
	"encoding/binary.BigEndian",             // 🟢 winnet: reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
	"encoding/binary.LittleEndian",          // 🟢 winnet: reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
	"errors.New",                            // 🟢 creates a sentinel error; pure function, no I/O.
	"fmt.Errorf",                            // 🟢 error formatting; pure function, no I/O.
	"fmt.Sprintf",                           // 🟢 string formatting; pure function, no I/O.
	"os.Getpid",                             // 🟠 procinfo: returns the current process ID; read-only, no side effects.
	"os.Open",                               // 🟠 procinfo: opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.ReadDir",                            // 🟠 procinfo: reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",                           // 🟠 procinfo: reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
	"strconv.Atoi",                          // 🟢 string-to-int conversion; pure function, no I/O.
	"strconv.ParseInt",                      // 🟢 procinfo: string to int64 with base/bit-size; pure function, no I/O.
	"strings.Fields",                        // 🟢 procinfo: splits a string on whitespace; pure function, no I/O.
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
