// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package allowedsymbols

// builtinAllowedSymbols lists every "importpath.Symbol" that may be used by
// command implementation files in builtins/. Each entry must be in
// "importpath.Symbol" form, where importpath is the full Go import path.
//
// Each symbol must have a comment explaining what it does and why it is safe
// to use inside a sandboxed builtin (e.g. pure function, constant, interface,
// no filesystem/network/exec side effects).
//
// To use a new symbol, add a single line here with its safety justification.
//
// Permanently banned (cannot be added):
//   - reflect  — reflection defeats static safety analysis
//   - unsafe   — bypasses Go's type and memory safety guarantees
//
// All packages not listed here are implicitly banned, including all
// third-party packages and other internal module packages.

// builtinPerCommandSymbols maps each builtin command name (matching its
// subdirectory under builtins/) to the symbols it is allowed to use.
// Every symbol listed here must also appear in builtinAllowedSymbols
// (which acts as the global ceiling).
var builtinPerCommandSymbols = map[string][]string{
	"break": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"cat": {
		"bufio.NewScanner", // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
	},
	"continue": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"cut": {
		"bufio.NewScanner",  // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"context.Context",   // deadline/cancellation plumbing; pure interface, no side effects.
		"io.NopCloser",      // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",     // interface type; no side effects.
		"math.MaxInt32",     // integer constant; no side effects.
		"os.O_RDONLY",       // read-only file flag constant; cannot open files by itself.
		"slices.SortFunc",   // sorts a slice with a comparison function; pure function, no I/O.
		"strconv.Atoi",      // string-to-int conversion; pure function, no I/O.
		"strings.IndexByte", // finds byte in string; pure function, no I/O.
		"strings.Split",     // splits a string by separator into a slice; pure function, no I/O.
	},
	"echo": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
		"strings.Builder", // efficient string concatenation; pure in-memory buffer, no I/O.
	},
	"exit": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
		"strconv.Atoi",    // string-to-int conversion; pure function, no I/O.
	},
	"false": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"find": {
		"context.Context",                 // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.As",                       // error type assertion; pure function, no I/O.
		"errors.Is",                       // error comparison; pure function, no I/O.
		"errors.New",                      // creates a simple error value; pure function, no I/O.
		"fmt.Errorf",                      // error formatting; pure function, no I/O.
		"io.EOF",                          // sentinel error value; pure constant.
		"io/fs.FileInfo",                  // interface type for file information; no side effects.
		"io/fs.ModeDir",                   // file mode bit constant for directories; pure constant.
		"io/fs.ModeNamedPipe",             // file mode bit constant for named pipes; pure constant.
		"io/fs.ModeSocket",                // file mode bit constant for sockets; pure constant.
		"io/fs.ModeSymlink",               // file mode bit constant for symlinks; pure constant.
		"io/fs.ReadDirFile",               // read-only directory handle interface; no write capability.
		"math.Ceil",                       // pure arithmetic; no side effects.
		"math.Floor",                      // pure arithmetic; no side effects.
		"math.MaxInt64",                   // integer constant; no side effects.
		"os.IsNotExist",                   // checks if error is "not exist"; pure function, no I/O.
		"os.PathError",                    // error type for path operations; pure type.
		"path/filepath.ToSlash",           // converts OS path separators to forward slashes; pure function, no I/O.
		"strconv.Atoi",                    // string-to-int conversion; pure function, no I/O.
		"strconv.ErrRange",                // sentinel error value for overflow; pure constant.
		"strconv.ParseInt",                // string-to-int conversion; pure function, no I/O.
		"strings.HasPrefix",               // pure function for prefix matching; no I/O.
		"strings.ToLower",                 // converts string to lowercase; pure function, no I/O.
		"time.Duration",                   // duration type; pure integer alias, no I/O.
		"time.Hour",                       // constant representing one hour; no side effects.
		"time.Minute",                     // constant representing one minute; no side effects.
		"time.Second",                     // constant representing one second; no side effects.
		"time.Time",                       // time value type; pure data, no side effects.
		"unicode/utf8.DecodeRuneInString", // decodes first UTF-8 rune from a string; pure function, no I/O.
	},
	"grep": {
		"bufio.NewScanner",  // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"bytes.IndexByte",   // finds a byte in a byte slice; pure function, no I/O.
		"bytes.NewReader",   // wraps a byte slice as an io.Reader; pure in-memory, no I/O.
		"context.Context",   // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.New",        // creates a simple error value; pure function, no I/O.
		"io.MultiReader",    // combines multiple Readers into one sequential Reader; no I/O side effects.
		"io.NopCloser",      // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",     // interface type; no side effects.
		"io.Reader",         // interface type; no side effects.
		"os.O_RDONLY",       // read-only file flag constant; cannot open files by itself.
		"regexp.Compile",    // compiles a regular expression; pure function, no I/O. Uses RE2 engine (linear-time, no backtracking).
		"regexp.QuoteMeta",  // escapes all special regex characters in a string; pure function, no I/O.
		"regexp.Regexp",     // compiled regular expression type; no I/O side effects. All matching methods are linear-time (RE2).
		"strconv.Itoa",      // int-to-string conversion; pure function, no I/O.
		"strconv.ParseBool", // string-to-bool conversion; pure function, no I/O.
		"strings.Builder",   // efficient string concatenation; pure in-memory buffer, no I/O.
		"strings.Join",      // concatenates a slice of strings with a separator; pure function, no I/O.
		"strings.Split",     // splits a string by separator into a slice; pure function, no I/O.
	},
	"help": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"head": {
		"bufio.NewScanner", // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"errors.New",       // creates a simple error value; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.ReadSeeker",    // interface type combining Reader and Seeker; no side effects.
		"io.Reader",        // interface type; no side effects.
		"io.SeekCurrent",   // whence constant for Seek(offset, SeekCurrent); pure constant.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
		"strconv.ParseInt", // string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"ls": {
		"context.Context",     // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.New",          // creates a simple error value; pure function, no I/O.
		"fmt.Sprintf",         // string formatting; pure function, no I/O.
		"io/fs.DirEntry",      // interface type for directory entries; no side effects.
		"io/fs.FileInfo",      // interface type for file information; no side effects.
		"io/fs.ModeDir",       // file mode bit constant for directories; pure constant.
		"io/fs.ModeNamedPipe", // file mode bit constant for named pipes; pure constant.
		"io/fs.ModeSetgid",    // file mode bit constant for setgid; pure constant.
		"io/fs.ModeSetuid",    // file mode bit constant for setuid; pure constant.
		"io/fs.ModeSocket",    // file mode bit constant for sockets; pure constant.
		"io/fs.ModeSticky",    // file mode bit constant for sticky bit; pure constant.
		"io/fs.ModeSymlink",   // file mode bit constant for symlinks; pure constant.
		"runtime.GOOS",        // current OS name constant; pure constant, no I/O.
		"slices.Reverse",      // reverses a slice in-place; pure function, no I/O.
		"slices.SortFunc",     // sorts a slice with a comparison function; pure function, no I/O.
		"time.Time",           // time value type; pure data, no side effects.
	},
	"ping": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
		"github.com/DataDog/datadog-traceroute/result.Results",              // result struct returned by RunTraceroute; pure data type, no side effects.
		"github.com/DataDog/datadog-traceroute/traceroute.NewTraceroute",    // creates a traceroute runner for ICMP probes; network I/O (authorized for ping).
		"github.com/DataDog/datadog-traceroute/traceroute.TracerouteParams", // parameter struct for traceroute configuration; pure data type.
		"time.Duration", // duration type; pure integer alias, no I/O.
		"time.Second",   // constant representing one second; no side effects.
	},
	"printf": {
		"context.Context",      // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.As",            // error type assertion; pure function, no I/O.
		"fmt.Sprintf",          // string formatting; pure function, no I/O.
		"math.Inf",             // returns positive or negative infinity; pure function, no I/O.
		"math.MaxUint64",       // integer constant; no side effects.
		"math.NaN",             // returns IEEE 754 NaN value; pure function, no I/O.
		"strconv.Atoi",         // string-to-int conversion; pure function, no I/O.
		"strconv.ErrRange",     // sentinel error value for overflow; pure constant.
		"strconv.IntSize",      // platform int size constant (32 or 64); pure constant, no I/O.
		"strconv.Itoa",         // int-to-string conversion; pure function, no I/O.
		"strconv.NumError",     // error type for numeric conversion failures; pure type.
		"strconv.ParseFloat",   // string-to-float conversion; pure function, no I/O.
		"strconv.ParseInt",     // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strconv.ParseUint",    // string-to-unsigned-int conversion; pure function, no I/O.
		"strings.Builder",      // efficient string concatenation; pure in-memory buffer, no I/O.
		"strings.ContainsRune", // checks if a rune is in a string; pure function, no I/O.
		"strings.ReplaceAll",   // replaces all occurrences of a substring; pure function, no I/O.
		"strings.ToLower",      // converts string to lowercase; pure function, no I/O.
	},
	"sort": {
		"bufio.NewScanner",      // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"context.Context",       // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.New",            // creates a simple error value; pure function, no I/O.
		"fmt.Errorf",            // error formatting; pure function, no I/O.
		"io.NopCloser",          // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",         // interface type; no side effects.
		"os.O_RDONLY",           // read-only file flag constant; cannot open files by itself.
		"slices.SortFunc",       // sorts a slice with a comparison function; pure function, no I/O.
		"slices.SortStableFunc", // stable sort with a comparison function; pure function, no I/O.
		"strconv.Atoi",          // string-to-int conversion; pure function, no I/O.
		"strings.Builder",       // efficient string concatenation; pure in-memory buffer, no I/O.
		"strings.IndexByte",     // finds byte in string; pure function, no I/O.
	},
	"sed": {
		"bufio.NewScanner",  // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"bufio.Scanner",     // scanner type for buffered input reading; no write or exec capability.
		"bytes.IndexByte",   // finds a byte in a byte slice; pure function, no I/O.
		"context.Context",   // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.As",         // error type assertion; pure function, no I/O.
		"errors.New",        // creates a simple error value; pure function, no I/O.
		"fmt.Sprintf",       // string formatting; pure function, no I/O.
		"io.NopCloser",      // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",     // interface type; no side effects.
		"os.FileInfo",       // file metadata interface returned by Stat; no I/O side effects.
		"os.O_RDONLY",       // read-only file flag constant; cannot open files by itself.
		"regexp.Compile",    // compiles a regular expression; pure function, no I/O. Uses RE2 engine (linear-time, no backtracking).
		"regexp.Regexp",     // compiled regular expression type; no I/O side effects. All matching methods are linear-time (RE2).
		"strconv.Atoi",      // string-to-int conversion; pure function, no I/O.
		"strconv.ParseInt",  // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strings.Builder",   // efficient string concatenation; pure in-memory buffer, no I/O.
		"strings.IndexByte", // finds byte in string; pure function, no I/O.
		"strings.Join",      // concatenates a slice of strings with a separator; pure function, no I/O.
	},
	"strings_cmd": {
		"context.Context",   // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.As",         // error type assertion; pure function, no I/O.
		"errors.Is",         // error comparison; pure function, no I/O.
		"errors.New",        // creates a simple error value; pure function, no I/O.
		"fmt.Sprintf",       // string formatting; pure function, no I/O.
		"io.EOF",            // sentinel error value; pure constant.
		"io.NopCloser",      // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",     // interface type; no side effects.
		"os.O_RDONLY",       // read-only file flag constant; cannot open files by itself.
		"os.PathError",      // error type for filesystem path errors; pure type, no I/O.
		"strconv.FormatInt", // int-to-string conversion; pure function, no I/O.
	},
	"tail": {
		"bufio.NewScanner", // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"errors.New",       // creates a simple error value; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"io.NopCloser",     // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",    // interface type; no side effects.
		"io.Reader",        // interface type; no side effects.
		"os.FileInfo",      // file metadata interface returned by Stat; no I/O side effects.
		"os.O_RDONLY",      // read-only file flag constant; cannot open files by itself.
		"strconv.ParseInt", // string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"testcmd": {
		"context.Context",     // deadline/cancellation plumbing; pure interface, no side effects.
		"io/fs.FileInfo",      // interface type for file information; no side effects.
		"io/fs.ModeNamedPipe", // file mode bit constant for named pipes; pure constant.
		"io/fs.ModeSymlink",   // file mode bit constant for symlinks; pure constant.
		"strconv.ParseInt",    // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strings.TrimSpace",   // removes leading/trailing whitespace; pure function.
	},
	"tr": {
		"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.Is",        // error comparison; pure function, no I/O.
		"fmt.Sprintf",      // string formatting; pure function, no I/O.
		"io.EOF",           // sentinel error value; pure constant.
		"strconv.ParseInt", // string-to-int conversion with base/bit-size; pure function, no I/O.
	},
	"true": {
		"context.Context", // deadline/cancellation plumbing; pure interface, no side effects.
	},
	"uniq": {
		"bufio.NewScanner",  // line-by-line input reading (e.g. head, cat); no write or exec capability.
		"bufio.SplitFunc",   // type for custom scanner split functions; pure type, no I/O.
		"bytes.Equal",       // compares two byte slices for equality; pure function, no I/O.
		"context.Context",   // deadline/cancellation plumbing; pure interface, no side effects.
		"io.NopCloser",      // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",     // interface type; no side effects.
		"io.Reader",         // interface type; no side effects.
		"io.WriteString",    // writes a string to a writer; no filesystem access, delegates to Write.
		"io.Writer",         // interface type for writing; no side effects.
		"math.MaxInt64",     // integer constant; no side effects.
		"os.O_RDONLY",       // read-only file flag constant; cannot open files by itself.
		"strconv.ErrRange",  // sentinel error value for overflow; pure constant.
		"strconv.FormatInt", // int-to-string conversion; pure function, no I/O.
		"strconv.NumError",  // error type for numeric conversion failures; pure type.
		"strconv.ParseInt",  // string-to-int conversion with base/bit-size; pure function, no I/O.
		"strings.HasPrefix", // pure function for prefix matching; no I/O.
	},
	"wc": {
		"context.Context",         // deadline/cancellation plumbing; pure interface, no side effects.
		"errors.As",               // error type assertion; pure function, no I/O.
		"errors.Is",               // error comparison; pure function, no I/O.
		"errors.New",              // creates a simple error value; pure function, no I/O.
		"io.EOF",                  // sentinel error value; pure constant.
		"io.NopCloser",            // wraps a Reader with a no-op Close; no side effects.
		"io.ReadCloser",           // interface type; no side effects.
		"io.Reader",               // interface type; no side effects.
		"os.O_RDONLY",             // read-only file flag constant; cannot open files by itself.
		"strconv.FormatInt",       // int-to-string conversion; pure function, no I/O.
		"syscall.EISDIR",          // error number constant for "is a directory"; pure constant, no I/O.
		"syscall.Errno",           // error type for system call error numbers; pure type, no I/O.
		"unicode.Cc",              // control character category range table; pure data, no I/O.
		"unicode.Cf",              // format character category range table; pure data, no I/O.
		"unicode.Co",              // private-use character category range table; pure data, no I/O.
		"unicode.Is",              // checks if rune belongs to a range table; pure function, no I/O.
		"unicode.IsGraphic",       // reports whether rune is defined as a graphic character; pure function, no I/O.
		"unicode.Me",              // enclosing mark category range table; pure data, no I/O.
		"unicode.Mn",              // nonspacing mark category range table; pure data, no I/O.
		"unicode.Range16",         // struct type for 16-bit Unicode ranges; pure data.
		"unicode.Range32",         // struct type for 32-bit Unicode ranges; pure data.
		"unicode.RangeTable",      // struct type for Unicode range tables; pure data.
		"unicode.Zs",              // Unicode space separator category range table; pure data, no I/O.
		"unicode/utf8.DecodeRune", // decodes first UTF-8 rune from a byte slice; pure function, no I/O.
		"unicode/utf8.RuneError",  // replacement character returned for invalid UTF-8; constant, no I/O.
		"unicode/utf8.UTFMax",     // maximum number of bytes in a UTF-8 encoding; constant, no I/O.
		"unicode/utf8.Valid",      // checks if a byte slice is valid UTF-8; pure function, no I/O.
	},
}

var builtinAllowedSymbols = []string{
	"bufio.NewScanner", // line-by-line input reading (e.g. head, cat); no write or exec capability.
	"bufio.Scanner",    // scanner type for buffered input reading; no write or exec capability.
	"bufio.SplitFunc",  // type for custom scanner split functions; pure type, no I/O.
	"bytes.Equal",      // compares two byte slices for equality; pure function, no I/O.
	"bytes.IndexByte",  // finds a byte in a byte slice; pure function, no I/O.
	"bytes.NewReader",  // wraps a byte slice as an io.Reader; pure in-memory, no I/O.
	"context.Context",  // deadline/cancellation plumbing; pure interface, no side effects.
	"errors.As",        // error type assertion; pure function, no I/O.
	"errors.Is",        // error comparison; pure function, no I/O.
	"errors.New",       // creates a simple error value; pure function, no I/O.
	"fmt.Errorf",       // error formatting; pure function, no I/O.
	"fmt.Sprintf",      // string formatting; pure function, no I/O.
	"github.com/DataDog/datadog-traceroute/result.Results",              // result struct returned by RunTraceroute; pure data type, no side effects.
	"github.com/DataDog/datadog-traceroute/traceroute.NewTraceroute",    // creates a traceroute runner for ICMP probes; network I/O (authorized for ping builtin only via per-command allowlist).
	"github.com/DataDog/datadog-traceroute/traceroute.TracerouteParams", // parameter struct for traceroute configuration; pure data type.
	"io.EOF",                          // sentinel error value; pure constant.
	"io.MultiReader",                  // combines multiple Readers into one sequential Reader; no I/O side effects.
	"io.NopCloser",                    // wraps a Reader with a no-op Close; no side effects.
	"io.ReadCloser",                   // interface type; no side effects.
	"io.ReadSeeker",                   // interface type combining Reader and Seeker; no side effects.
	"io.Reader",                       // interface type; no side effects.
	"io.SeekCurrent",                  // whence constant for Seek(offset, SeekCurrent); pure constant.
	"io.WriteString",                  // writes a string to a writer; no filesystem access, delegates to Write.
	"io.Writer",                       // interface type for writing; no side effects.
	"io/fs.DirEntry",                  // interface type for directory entries; no side effects.
	"io/fs.FileInfo",                  // interface type for file information; no side effects.
	"io/fs.ModeDir",                   // file mode bit constant for directories; pure constant.
	"io/fs.ModeNamedPipe",             // file mode bit constant for named pipes; pure constant.
	"io/fs.ModeSetgid",                // file mode bit constant for setgid; pure constant.
	"io/fs.ModeSetuid",                // file mode bit constant for setuid; pure constant.
	"io/fs.ModeSocket",                // file mode bit constant for sockets; pure constant.
	"io/fs.ModeSticky",                // file mode bit constant for sticky bit; pure constant.
	"io/fs.ModeSymlink",               // file mode bit constant for symlinks; pure constant.
	"io/fs.ReadDirFile",               // read-only directory handle interface; no write capability.
	"math.Ceil",                       // pure arithmetic; no side effects.
	"math.Floor",                      // pure arithmetic; no side effects.
	"math.Inf",                        // returns positive or negative infinity; pure function, no I/O.
	"math.MaxInt32",                   // integer constant; no side effects.
	"math.MaxInt64",                   // integer constant; no side effects.
	"math.MaxUint64",                  // integer constant; no side effects.
	"math.NaN",                        // returns IEEE 754 NaN value; pure function, no I/O.
	"os.FileInfo",                     // file metadata interface returned by Stat; no I/O side effects.
	"os.IsNotExist",                   // checks if error is "not exist"; pure function, no I/O.
	"os.O_RDONLY",                     // read-only file flag constant; cannot open files by itself.
	"os.PathError",                    // error type for filesystem path errors; pure type, no I/O.
	"path/filepath.ToSlash",           // converts OS path separators to forward slashes; pure function, no I/O.
	"regexp.Compile",                  // compiles a regular expression; pure function, no I/O. Uses RE2 engine (linear-time, no backtracking).
	"regexp.QuoteMeta",                // escapes all special regex characters in a string; pure function, no I/O.
	"regexp.Regexp",                   // compiled regular expression type; no I/O side effects. All matching methods are linear-time (RE2).
	"runtime.GOOS",                    // current OS name constant; pure constant, no I/O.
	"slices.Reverse",                  // reverses a slice in-place; pure function, no I/O.
	"slices.SortFunc",                 // sorts a slice with a comparison function; pure function, no I/O.
	"slices.SortStableFunc",           // stable sort with a comparison function; pure function, no I/O.
	"strconv.Atoi",                    // string-to-int conversion; pure function, no I/O.
	"strconv.ErrRange",                // sentinel error value for overflow; pure constant.
	"strconv.FormatInt",               // int-to-string conversion; pure function, no I/O.
	"strconv.IntSize",                 // platform int size constant (32 or 64); pure constant, no I/O.
	"strconv.Itoa",                    // int-to-string conversion; pure function, no I/O.
	"strconv.NumError",                // error type for numeric conversion failures; pure type.
	"strconv.ParseBool",               // string-to-bool conversion; pure function, no I/O.
	"strconv.ParseFloat",              // string-to-float conversion; pure function, no I/O.
	"strconv.ParseInt",                // string-to-int conversion with base/bit-size; pure function, no I/O.
	"strconv.ParseUint",               // string-to-unsigned-int conversion; pure function, no I/O.
	"strings.Builder",                 // efficient string concatenation; pure in-memory buffer, no I/O.
	"strings.ContainsRune",            // checks if a rune is in a string; pure function, no I/O.
	"strings.HasPrefix",               // pure function for prefix matching; no I/O.
	"strings.IndexByte",               // finds byte in string; pure function, no I/O.
	"strings.Join",                    // concatenates a slice of strings with a separator; pure function, no I/O.
	"strings.ReplaceAll",              // replaces all occurrences of a substring; pure function, no I/O.
	"strings.Split",                   // splits a string by separator into a slice; pure function, no I/O.
	"strings.ToLower",                 // converts string to lowercase; pure function, no I/O.
	"strings.TrimSpace",               // removes leading/trailing whitespace; pure function.
	"syscall.EISDIR",                  // error number constant for "is a directory"; pure constant, no I/O.
	"syscall.Errno",                   // error type for system call error numbers; pure type, no I/O.
	"time.Duration",                   // duration type; pure integer alias, no I/O.
	"time.Hour",                       // constant representing one hour; no side effects.
	"time.Minute",                     // constant representing one minute; no side effects.
	"time.Second",                     // constant representing one second; no side effects.
	"time.Time",                       // time value type; pure data, no side effects.
	"unicode.Cc",                      // control character category range table; pure data, no I/O.
	"unicode.Cf",                      // format character category range table; pure data, no I/O.
	"unicode.Co",                      // private-use character category range table; pure data, no I/O.
	"unicode.Is",                      // checks if rune belongs to a range table; pure function, no I/O.
	"unicode.IsGraphic",               // reports whether rune is defined as a graphic character; pure function, no I/O.
	"unicode.Me",                      // enclosing mark category range table; pure data, no I/O.
	"unicode.Mn",                      // nonspacing mark category range table; pure data, no I/O.
	"unicode.Range16",                 // struct type for 16-bit Unicode ranges; pure data.
	"unicode.Range32",                 // struct type for 32-bit Unicode ranges; pure data.
	"unicode.RangeTable",              // struct type for Unicode range tables; pure data.
	"unicode.Zs",                      // Unicode space separator category range table; pure data, no I/O.
	"unicode/utf8.DecodeRune",         // decodes first UTF-8 rune from a byte slice; pure function, no I/O.
	"unicode/utf8.DecodeRuneInString", // decodes first UTF-8 rune from a string; pure function, no I/O.
	"unicode/utf8.RuneError",          // replacement character returned for invalid UTF-8; constant, no I/O.
	"unicode/utf8.UTFMax",             // maximum number of bytes in a UTF-8 encoding; constant, no I/O.
	"unicode/utf8.Valid",              // checks if a byte slice is valid UTF-8; pure function, no I/O.
}
