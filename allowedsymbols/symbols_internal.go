// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// internalAllowedSymbols lists every "importpath.Symbol" permitted in
// builtins/internal/ helper packages. Each entry must be in
// "importpath.Symbol" form with a comment explaining safety.
//
// unsafe.Pointer is permitted here solely for winnet/winnet_windows.go, which
// must pass stack-addressed buffers to GetExtendedTcpTable/GetExtendedUdpTable
// via iphlpapi.dll. Usage is limited to two call sites; no unsafe pointer
// arithmetic occurs after the DLL call. All buffer parsing uses encoding/binary.
//
// Third-party OS abstraction packages (golang.org/x/sys/unix,
// golang.org/x/sys/windows) are exempted entirely via the ExemptImport
// function in the test config — their symbols are not listed here.
var internalAllowedSymbols = []string{
	"bufio.NewScanner",             // procinfo: line-by-line reading of /proc files; no write capability.
	"bytes.NewReader",              // procinfo: wraps a byte slice as an in-memory io.Reader; no I/O side effects.
	"context.Context",              // procinfo: deadline/cancellation interface; no side effects.
	"encoding/binary.BigEndian",    // winnet: reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
	"encoding/binary.LittleEndian", // winnet: reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
	"errors.New",                   // creates a sentinel error; pure function, no I/O.
	"fmt.Errorf",                   // error formatting; pure function, no I/O.
	"fmt.Sprintf",                  // string formatting; pure function, no I/O.
	"os.Getpid",                    // procinfo: returns the current process ID; read-only, no side effects.
	"os.Open",                      // procinfo: opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.ReadDir",                   // procinfo: reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",                  // procinfo: reads a whole file; needed to read /proc/[pid]/{stat,cmdline,status}.
	"strconv.Atoi",                 // string-to-int conversion; pure function, no I/O.
	"strconv.ParseInt",             // procinfo: string to int64 with base/bit-size; pure function, no I/O.
	"strings.Fields",               // procinfo: splits a string on whitespace; pure function, no I/O.
	"strings.HasPrefix",            // procinfo: checks string prefix; pure function, no I/O.
	"strings.Index",                // procinfo: finds first occurrence of a substring; pure function, no I/O.
	"strings.LastIndex",            // procinfo: finds last occurrence of a substring; pure function, no I/O.
	"strings.TrimRight",            // procinfo: trims trailing characters; pure function, no I/O.
	"strings.TrimSpace",            // procinfo: removes leading/trailing whitespace; pure function, no I/O.
	"syscall.Errno",                // winnet: wraps DLL return code as an error type; pure type, no I/O.
	"syscall.Getsid",               // procinfo: returns the session ID of a process; read-only syscall, no write/exec.
	"syscall.MustLoadDLL",          // winnet: loads iphlpapi.dll once at program init; read-only OS loader call.
	"syscall.Proc",                 // winnet: DLL procedure handle type used in function signature; pure type, no I/O.
	"time.Now",                     // procinfo: returns the current wall-clock time; read-only, no side effects.
	"time.Unix",                    // procinfo: constructs a Time from Unix seconds; pure function, no I/O.
	"unsafe.Pointer",               // winnet: passes buffer/size pointers to DLL via syscall ABI. No pointer arithmetic; buffer parsed with encoding/binary after the call.
}
