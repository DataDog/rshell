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
var internalAllowedSymbols = []string{
	"encoding/binary.BigEndian",    // Windows: reads big-endian IPv6 group values from DLL buffer; pure value, no I/O.
	"encoding/binary.LittleEndian", // Windows: reads little-endian DWORD fields from DLL buffer; pure value, no I/O.
	"errors.New",                   // creates a sentinel error; pure function, no I/O.
	"fmt.Errorf",                   // error formatting; pure function, no I/O.
	"fmt.Sprintf",                  // string formatting; pure function, no I/O.
	"strconv.Atoi",                 // string-to-int conversion; pure function, no I/O.
	"syscall.Errno",                // Windows: wraps DLL return code as an error type; pure type, no I/O.
	"syscall.MustLoadDLL",          // Windows: loads iphlpapi.dll once at program init; read-only OS loader call.
	"syscall.Proc",                 // Windows: DLL procedure handle type used in function signature; pure type, no I/O.
	"unsafe.Pointer",               // Windows: passes buffer/size pointers to DLL via syscall ABI. No pointer arithmetic; buffer parsed with encoding/binary after the call.
}
