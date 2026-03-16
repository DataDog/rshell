// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// builtinInternalAllowedSymbols lists every "importpath.Symbol" that may be
// used by helper packages under builtins/internal/. These packages provide
// OS-specific implementations (e.g. process listing) and therefore need a
// broader set of symbols than the command-level allowlist.
//
// The permanently-banned packages (unsafe, reflect, os/exec, net, plugin) are
// still forbidden here; only standard-library OS APIs that are safe for a
// read-only process-inspection helper are added.
//
// Third-party OS abstraction packages (golang.org/x/sys/unix,
// golang.org/x/sys/windows) are exempted entirely via the ExemptImport
// function in the test config — their symbols are not listed here.
var builtinInternalAllowedSymbols = []string{
	"bufio.NewScanner",   // line-by-line reading of /proc files; no write capability.
	"bytes.NewReader",    // wraps a byte slice as an in-memory io.Reader; no I/O side effects.
	"context.Context",    // deadline/cancellation interface; no side effects.
	"errors.New",         // creates a sentinel error value; pure function, no I/O.
	"fmt.Errorf",         // formats an error with context; pure function, no I/O.
	"fmt.Sprintf",        // string formatting; pure function, no I/O.
	"os.Getpid",          // returns the current process ID; read-only, no side effects.
	"os.Open",            // opens a file read-only; needed to stream /proc/stat line-by-line.
	"os.ReadDir",         // reads a directory listing; needed to enumerate /proc entries.
	"os.ReadFile",        // reads a whole file into memory; needed to read /proc/[pid]/{stat,cmdline,status}.
	"os.Readlink",        // reads a symlink target; needed to resolve /proc/[pid]/fd/0 to a tty name.
	"strconv.Atoi",       // converts a string to int; pure function, no I/O.
	"strconv.ParseInt",   // converts a string to int64 with base/bit-size; pure function, no I/O.
	"strings.Fields",     // splits a string on whitespace; pure function, no I/O.
	"strings.HasPrefix",  // checks string prefix; pure function, no I/O.
	"strings.Index",      // finds first occurrence of a substring; pure function, no I/O.
	"strings.LastIndex",  // finds last occurrence of a substring; pure function, no I/O.
	"strings.TrimPrefix", // removes a leading prefix; pure function, no I/O.
	"strings.TrimRight",  // trims trailing characters from a set; pure function, no I/O.
	"strings.TrimSpace",  // removes leading/trailing whitespace; pure function, no I/O.
	"syscall.Getsid",     // returns the session ID of a process; read-only syscall, no write/exec capability.
	"time.Now",           // returns the current wall-clock time; read-only, no side effects.
	"time.Unix",          // constructs a Time from Unix seconds; pure function, no I/O.
}
