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
var builtinAllowedSymbols = []string{
	// bytes.IndexByte — finds a byte in a byte slice; pure function, no I/O.
	"bytes.IndexByte",
	// bufio.NewScanner — line-by-line input reading (e.g. head, cat); no write or exec capability.
	"bufio.NewScanner",
	// bufio.Scanner — scanner type for buffered input reading; no write or exec capability.
	"bufio.Scanner",
	// bufio.SplitFunc — type for custom scanner split functions; pure type, no I/O.
	"bufio.SplitFunc",
	// context.Context — deadline/cancellation plumbing; pure interface, no side effects.
	"context.Context",
	// errors.As — error type assertion; pure function, no I/O.
	"errors.As",
	// errors.Is — error comparison; pure function, no I/O.
	"errors.Is",
	// errors.New — creates a simple error value; pure function, no I/O.
	"errors.New",
	// fmt.Sprintf — string formatting; pure function, no I/O.
	"fmt.Sprintf",
	// io/fs.FileInfo — interface type for file information; no side effects.
	"io/fs.FileInfo",
	// io/fs.ModeDir — file mode bit constant for directories; pure constant.
	"io/fs.ModeDir",
	// io/fs.ModeNamedPipe — file mode bit constant for named pipes; pure constant.
	"io/fs.ModeNamedPipe",
	// io/fs.ModeSetgid — file mode bit constant for setgid; pure constant.
	"io/fs.ModeSetgid",
	// io/fs.ModeSetuid — file mode bit constant for setuid; pure constant.
	"io/fs.ModeSetuid",
	// io/fs.ModeSocket — file mode bit constant for sockets; pure constant.
	"io/fs.ModeSocket",
	// io/fs.ModeSticky — file mode bit constant for sticky bit; pure constant.
	"io/fs.ModeSticky",
	// io/fs.ModeSymlink — file mode bit constant for symlinks; pure constant.
	"io/fs.ModeSymlink",
	// io.EOF — sentinel error value; pure constant.
	"io.EOF",
	// io.NopCloser — wraps a Reader with a no-op Close; no side effects.
	"io.NopCloser",
	// io.ReadCloser — interface type; no side effects.
	"io.ReadCloser",
	// io.Reader — interface type; no side effects.
	"io.Reader",
	// io.ReadSeeker — interface type combining Reader and Seeker; no side effects.
	"io.ReadSeeker",
	// io.SeekCurrent — whence constant for Seek(offset, SeekCurrent); pure constant.
	"io.SeekCurrent",
	// math.Inf — returns positive or negative infinity; pure function, no I/O.
	"math.Inf",
	// math.MaxInt32 — integer constant; no side effects.
	"math.MaxInt32",
	// math.MaxInt64 — integer constant; no side effects.
	"math.MaxInt64",
	// math.MaxUint64 — integer constant; no side effects.
	"math.MaxUint64",
	// math.NaN — returns IEEE 754 NaN value; pure function, no I/O.
	"math.NaN",
	// os.FileInfo — file metadata interface returned by Stat; no I/O side effects.
	"os.FileInfo",
	// os.O_RDONLY — read-only file flag constant; cannot open files by itself.
	"os.O_RDONLY",
	// regexp.Compile — compiles a regular expression; pure function, no I/O. Uses RE2 engine (linear-time, no backtracking).
	"regexp.Compile",
	// regexp.QuoteMeta — escapes all special regex characters in a string; pure function, no I/O.
	"regexp.QuoteMeta",
	// regexp.Regexp — compiled regular expression type; no I/O side effects. All matching methods are linear-time (RE2).
	"regexp.Regexp",
	// slices.Reverse — reverses a slice in-place; pure function, no I/O.
	"slices.Reverse",
	// slices.SortFunc — sorts a slice with a comparison function; pure function, no I/O.
	"slices.SortFunc",
	// strings.Builder — efficient string concatenation; pure in-memory buffer, no I/O.
	"strings.Builder",
	// strings.ContainsRune — checks if a rune is in a string; pure function, no I/O.
	"strings.ContainsRune",
	// strings.Join — concatenates a slice of strings with a separator; pure function, no I/O.
	"strings.Join",
	// strings.ReplaceAll — replaces all occurrences of a substring; pure function, no I/O.
	"strings.ReplaceAll",
	// strings.ToLower — converts string to lowercase; pure function, no I/O.
	"strings.ToLower",
	// strconv.IntSize — platform int size constant (32 or 64); pure constant, no I/O.
	"strconv.IntSize",
	// strings.Split — splits a string by separator into a slice; pure function, no I/O.
	"strings.Split",
	// strconv.Atoi — string-to-int conversion; pure function, no I/O.
	"strconv.Atoi",
	// strconv.ParseBool — string-to-bool conversion; pure function, no I/O.
	"strconv.ParseBool",
	// strconv.Itoa — int-to-string conversion; pure function, no I/O.
	"strconv.Itoa",
	// strconv.ErrRange — sentinel error value for overflow; pure constant.
	"strconv.ErrRange",
	// strconv.NumError — error type for numeric conversion failures; pure type.
	"strconv.NumError",
	// strconv.ParseFloat — string-to-float conversion; pure function, no I/O.
	"strconv.ParseFloat",
	// strconv.ParseInt — string-to-int conversion with base/bit-size; pure function, no I/O.
	"strconv.ParseInt",
	// strconv.ParseUint — string-to-unsigned-int conversion; pure function, no I/O.
	"strconv.ParseUint",
	// strconv.FormatInt — int-to-string conversion; pure function, no I/O.
	"strconv.FormatInt",
	// strings.HasPrefix — pure function for prefix matching; no I/O.
	"strings.HasPrefix",
	// strings.IndexByte — finds byte in string; pure function, no I/O.
	"strings.IndexByte",
	// strings.TrimSpace — removes leading/trailing whitespace; pure function.
	"strings.TrimSpace",
	// io.WriteString — writes a string to a writer; no filesystem access, delegates to Write.
	"io.WriteString",
	// io.Writer — interface type for writing; no side effects.
	"io.Writer",
	// unicode.Cc — control character category range table; pure data, no I/O.
	"unicode.Cc",
	// unicode.Cf — format character category range table; pure data, no I/O.
	"unicode.Cf",
	// unicode.Is — checks if rune belongs to a range table; pure function, no I/O.
	"unicode.Is",
	// unicode.Me — enclosing mark category range table; pure data, no I/O.
	"unicode.Me",
	// unicode.Mn — nonspacing mark category range table; pure data, no I/O.
	"unicode.Mn",
	// unicode.Range16 — struct type for 16-bit Unicode ranges; pure data.
	"unicode.Range16",
	// unicode.Range32 — struct type for 32-bit Unicode ranges; pure data.
	"unicode.Range32",
	// unicode.RangeTable — struct type for Unicode range tables; pure data.
	"unicode.RangeTable",
	// unicode/utf8.DecodeRune — decodes first UTF-8 rune from a byte slice; pure function, no I/O.
	"unicode/utf8.DecodeRune",
	// unicode/utf8.RuneCount — counts UTF-8 runes in a byte slice; pure function, no I/O.
	"unicode/utf8.RuneCount",
	// unicode/utf8.UTFMax — maximum number of bytes in a UTF-8 encoding; constant, no I/O.
	"unicode/utf8.UTFMax",
	// unicode/utf8.Valid — checks if a byte slice is valid UTF-8; pure function, no I/O.
	"unicode/utf8.Valid",
	// time.Time — time value type; pure data, no side effects.
	"time.Time",
}

// permanentlyBanned lists packages that may never be imported by builtin
// command implementations, regardless of what symbols they export.
var permanentlyBanned = map[string]string{
	"reflect": "reflection defeats static safety analysis",
	"unsafe":  "bypasses Go's type and memory safety guarantees",
}
