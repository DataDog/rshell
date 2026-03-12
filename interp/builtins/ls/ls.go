// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

// Package ls implements the ls builtin command.
//
// ls — list directory contents
//
// Usage: ls [OPTION]... [FILE]...
//
// List information about the FILEs (the current directory by default).
// Sort entries alphabetically by default.
//
// Accepted flags:
//
//	-1
//	    List one file per line. This is the default when output is not a
//	    terminal (which is always the case in this shell).
//
//	-a, --all
//	    Do not ignore entries starting with . (includes . and ..).
//
//	-A, --almost-all
//	    Do not ignore entries starting with . but omit . and ..
//
//	-d, --directory
//	    List directories themselves, not their contents.
//
//	-r, --reverse
//	    Reverse order while sorting.
//
//	-S
//	    Sort by file size, largest first.
//
//	-t
//	    Sort by modification time, newest first.
//
//	-F, --classify
//	    Append indicator (one of * / = @ |) to entries.
//
//	-p
//	    Append / indicator to directories.
//
//	-R, --recursive
//	    List subdirectories recursively.
//
//	-l
//	    Use a long listing format. Simplified: mode, size, date, name.
//	    NOTE: This is a simplified format compared to GNU ls — no
//	    owner/group/link-count columns because syscall and os/user
//	    packages are banned by the import allowlist.
//
//	-h, --human-readable
//	    With -l, print sizes in human-readable format (e.g. 1K, 234M).
//
// Exit codes:
//
//	0  All entries listed successfully.
//	1  At least one error occurred (missing file, permission denied, etc.).
//	1  Invalid usage (unrecognised flag, etc.).
package ls

import (
	"context"
	"errors"
	"fmt"
	iofs "io/fs"
	"slices"
	"time"

	"github.com/DataDog/rshell/interp/builtins"
)

// maxRecursionDepth limits how deep -R will recurse to prevent stack overflow.
const maxRecursionDepth = 256

// MaxDirEntries is the maximum number of entries ls will process per directory.
const MaxDirEntries = 10_000

// errFailed is a sentinel used to signal that at least one entry had an error.
var errFailed = errors.New("ls: one or more errors occurred")

// Cmd is the ls builtin command descriptor.
var Cmd = builtins.Command{Name: "ls", MakeFlags: registerFlags}

func registerFlags(fs *builtins.FlagSet) builtins.HandlerFunc {
	// Preserve parse order so Visit() returns flags in the order set.
	// This lets us determine which sort flag (-S or -t) was specified last.
	fs.SortFlags = false

	_ = fs.BoolP("all", "a", false, "do not ignore entries starting with .")
	_ = fs.BoolP("almost-all", "A", false, "do not ignore . and ..")
	dirOnly := fs.BoolP("directory", "d", false, "list directories themselves, not their contents")
	reverse := fs.BoolP("reverse", "r", false, "reverse order while sorting")
	_ = fs.BoolP("sort-size", "S", false, "sort by file size, largest first")
	_ = fs.BoolP("sort-time", "t", false, "sort by modification time, newest first")
	classify := fs.BoolP("classify", "F", false, "append indicator to entries")
	appendSlash := fs.BoolP("append-slash", "p", false, "append / indicator to directories")
	recursive := fs.BoolP("recursive", "R", false, "list subdirectories recursively")
	longFmt := fs.BoolP("long", "l", false, "use a long listing format")
	humanReadable := fs.BoolP("human-readable", "h", false, "with -l, print human-readable sizes")
	// -1 is the default in non-terminal (always true here), accepted for compat.
	_ = fs.Bool("one", false, "list one file per line")
	fs.Lookup("one").Shorthand = "1"

	return func(ctx context.Context, callCtx *builtins.CallContext, args []string) builtins.Result {
		now := callCtx.Now()

		// Determine the effective sort mode. When both -S and -t are given,
		// the last one specified wins, matching GNU ls behaviour.
		//
		// Determine the effective hidden-file mode. When both -a and -A are
		// given, the last one specified wins, matching GNU ls behaviour.
		//
		// NOTE: fs.Visit iterates flags in parse-order (SortFlags=false) but
		// only visits each flag once, so repeated flags like -a -A -a cannot
		// be fully resolved. This is an acceptable limitation for such a rare
		// edge case.
		var sortSize, sortTime bool
		var showAll, showAlmostAll bool
		fs.Visit(func(f *builtins.Flag) {
			switch f.Name {
			case "sort-size":
				sortSize = true
				sortTime = false
			case "sort-time":
				sortTime = true
				sortSize = false
			case "all":
				showAll = true
				showAlmostAll = false
			case "almost-all":
				showAlmostAll = true
				showAll = false
			}
		})

		opts := &options{
			all:           showAll,
			almostAll:     showAlmostAll,
			dirOnly:       *dirOnly,
			reverse:       *reverse,
			sortSize:      sortSize,
			sortTime:      sortTime,
			classify:      *classify,
			appendSlash:   *appendSlash,
			recursive:     *recursive,
			longFmt:       *longFmt,
			humanReadable: *humanReadable,
		}

		paths := args
		if len(paths) == 0 {
			paths = []string{"."}
		}

		failed := false
		multipleArgs := len(paths) > 1

		// Separate files and dirs (when not -d).
		// Use Lstat so that symlink operands retain ModeSymlink (GNU ls default).
		// The link target is only stat'd when needed (e.g. to decide dir vs file
		// for a symlink-to-directory, since -d is the only case where we'd list
		// the symlink itself rather than the target directory contents).
		var files []pathArg
		var dirs []pathArg
		for _, p := range paths {
			if ctx.Err() != nil {
				break
			}
			if p == "" {
				callCtx.Errf("ls: cannot access '': No such file or directory\n")
				failed = true
				continue
			}
			info, err := callCtx.LstatFile(ctx, p)
			if err != nil {
				callCtx.Errf("ls: cannot access '%s': %s\n", p, callCtx.PortableErr(err))
				failed = true
				continue
			}
			// For symlinks to directories: follow the link to list contents
			// (unless -d is set, which lists the entry itself).
			isDir := info.IsDir()
			if info.Mode()&iofs.ModeSymlink != 0 && !opts.dirOnly {
				if target, err := callCtx.StatFile(ctx, p); err == nil {
					isDir = target.IsDir()
				}
			}
			if !isDir || opts.dirOnly {
				files = append(files, pathArg{name: p, info: info})
			} else {
				dirs = append(dirs, pathArg{name: p, info: info})
			}
		}

		// List individual files first.
		if len(files) > 0 {
			sortEntries(files, opts, func(a pathArg) iofs.FileInfo { return a.info }, func(a pathArg) string { return a.name })
			for _, f := range files {
				printEntry(callCtx, f.name, f.info, opts, now)
			}
		}

		// Sort and list directories.
		sortEntries(dirs, opts, func(a pathArg) iofs.FileInfo { return a.info }, func(a pathArg) string { return a.name })
		showHeader := multipleArgs || len(files) > 0 || opts.recursive
		for i, d := range dirs {
			if ctx.Err() != nil {
				break
			}
			if showHeader {
				if i > 0 || len(files) > 0 {
					callCtx.Out("\n")
				}
				callCtx.Outf("%s:\n", d.name)
			}
			if err := listDir(ctx, callCtx, d.name, opts, 0, now); err != nil {
				failed = true
			}
		}

		if failed {
			return builtins.Result{Code: 1}
		}
		return builtins.Result{}
	}
}

type options struct {
	all           bool
	almostAll     bool
	dirOnly       bool
	reverse       bool
	sortSize      bool
	sortTime      bool
	classify      bool
	appendSlash   bool
	recursive     bool
	longFmt       bool
	humanReadable bool
}

type pathArg struct {
	name string
	info iofs.FileInfo
}

func listDir(ctx context.Context, callCtx *builtins.CallContext, dir string, opts *options, depth int, now time.Time) error {
	if depth > maxRecursionDepth {
		callCtx.Errf("ls: recursion depth limit exceeded at '%s'\n", dir)
		return errFailed
	}

	entries, err := callCtx.ReadDir(ctx, dir)
	if err != nil {
		callCtx.Errf("ls: cannot open directory '%s': %s\n", dir, callCtx.PortableErr(err))
		return err
	}

	// NOTE: ReadDir has already loaded all entries into memory at this point.
	// Go's os.ReadDir does not support streaming, so this check does not prevent
	// the initial allocation. It does, however, prevent expensive downstream
	// processing (sorting, stat calls, recursion) on absurdly large directories.
	if len(entries) > MaxDirEntries {
		callCtx.Errf("ls: warning: directory '%s': too many entries (%d > %d), output suppressed\n", dir, len(entries), MaxDirEntries)
		return nil
	}

	// Get FileInfo for sorting (if needed) and for long format.
	type entryInfo struct {
		name      string
		info      iofs.FileInfo
		isSymlink bool
	}

	failed := false
	var infoEntries []entryInfo

	// Synthesize . and .. for -a (os.ReadDir never includes them).
	// NOTE: ".." intentionally uses the same FileInfo as "." because the
	// parent directory may be outside the sandbox and cannot be stat'd.
	if opts.all {
		if dotInfo, err := callCtx.StatFile(ctx, dir); err == nil {
			infoEntries = append(infoEntries, entryInfo{name: ".", info: dotInfo})
			infoEntries = append(infoEntries, entryInfo{name: "..", info: dotInfo})
		}
	}

	for _, e := range entries {
		if ctx.Err() != nil {
			break
		}
		name := e.Name()
		if len(name) > 0 && name[0] == '.' && !opts.all && !opts.almostAll {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			callCtx.Errf("ls: cannot access '%s': %s\n", joinPath(dir, name), callCtx.PortableErr(infoErr))
			failed = true
			continue
		}
		infoEntries = append(infoEntries, entryInfo{
			name:      name,
			info:      info,
			isSymlink: e.Type()&iofs.ModeSymlink != 0,
		})
	}

	// Sort.
	sortEntries(infoEntries, opts, func(a entryInfo) iofs.FileInfo { return a.info }, func(a entryInfo) string { return a.name })

	// Print.
	for _, ei := range infoEntries {
		if ctx.Err() != nil {
			break
		}
		printEntry(callCtx, ei.name, ei.info, opts, now)
	}

	// Recurse into subdirectories if -R.
	// Symlinks are skipped to match GNU ls default behaviour (which does
	// not follow symlinks during -R traversal) and to prevent symlink-loop
	// denial-of-service attacks. The maxRecursionDepth limit provides an
	// additional safety bound.
	if opts.recursive {
		for _, ei := range infoEntries {
			if ctx.Err() != nil {
				break
			}
			if ei.isSymlink || !ei.info.IsDir() {
				continue
			}
			if ei.name == "." || ei.name == ".." {
				continue
			}
			subdir := joinPath(dir, ei.name)
			callCtx.Outf("\n%s:\n", subdir)
			if err := listDir(ctx, callCtx, subdir, opts, depth+1, now); err != nil {
				failed = true
			}
		}
	}

	if failed {
		return errFailed
	}
	return nil
}

func printEntry(callCtx *builtins.CallContext, name string, info iofs.FileInfo, opts *options, now time.Time) {
	if opts.longFmt {
		mode := formatMode(info)
		size := info.Size()
		modTime := info.ModTime()

		var sizeStr string
		if opts.humanReadable {
			sizeStr = humanSize(size)
		} else {
			sizeStr = fmt.Sprintf("%d", size)
		}

		timeStr := formatTime(modTime, now)
		callCtx.Outf("%s %s %s %s%s\n", mode, sizeStr, timeStr, name, indicator(info, opts))
	} else {
		callCtx.Outf("%s%s\n", name, indicator(info, opts))
	}
}

// formatMode returns a 10-character mode string matching GNU ls format.
// Go's FileMode.String() uses 'L' for symlinks (ls uses 'l') and doesn't
// render setuid/setgid/sticky in the rwx triplets.
//
// The function accepts FileInfo (rather than FileMode) to avoid referencing
// the io/fs.FileMode type directly, which is not in the import allowlist.
func formatMode(info iofs.FileInfo) string {
	var buf [10]byte
	mode := info.Mode()

	// Char 0: file type.
	// NOTE: Block device ('b') and character device ('c') types are
	// intentionally omitted because the syscall package (needed for
	// ModeDevice/ModeCharDevice) is banned by the import allowlist.
	switch {
	case mode&iofs.ModeDir != 0:
		buf[0] = 'd'
	case mode&iofs.ModeSymlink != 0:
		buf[0] = 'l'
	case mode&iofs.ModeNamedPipe != 0:
		buf[0] = 'p'
	case mode&iofs.ModeSocket != 0:
		buf[0] = 's'
	default:
		buf[0] = '-'
	}

	// Permission bits: extract the low 9 bits (rwxrwxrwx).
	const rwx = "rwx"
	perm := uint32(mode) & 0777
	for i := 0; i < 9; i++ {
		if perm&(1<<uint(8-i)) != 0 {
			buf[1+i] = rwx[i%3]
		} else {
			buf[1+i] = '-'
		}
	}

	// Setuid: affects owner execute (buf[3]).
	if mode&iofs.ModeSetuid != 0 {
		if buf[3] == 'x' {
			buf[3] = 's'
		} else {
			buf[3] = 'S'
		}
	}

	// Setgid: affects group execute (buf[6]).
	if mode&iofs.ModeSetgid != 0 {
		if buf[6] == 'x' {
			buf[6] = 's'
		} else {
			buf[6] = 'S'
		}
	}

	// Sticky: affects other execute (buf[9]).
	if mode&iofs.ModeSticky != 0 {
		if buf[9] == 'x' {
			buf[9] = 't'
		} else {
			buf[9] = 'T'
		}
	}

	return string(buf[:])
}

func indicator(info iofs.FileInfo, opts *options) string {
	mode := info.Mode()
	if opts.classify {
		if mode.IsDir() {
			return "/"
		}
		if mode&iofs.ModeSymlink != 0 {
			return "@"
		}
		if mode&iofs.ModeNamedPipe != 0 {
			return "|"
		}
		if mode&iofs.ModeSocket != 0 {
			return "="
		}
		if mode&0111 != 0 { // executable
			return "*"
		}
		return ""
	}
	if opts.appendSlash && mode.IsDir() {
		return "/"
	}
	return ""
}

func sortEntries[T any](entries []T, opts *options, getInfo func(T) iofs.FileInfo, getName func(T) string) {
	if opts.sortSize {
		slices.SortFunc(entries, func(a, b T) int {
			sa, sb := getInfo(a).Size(), getInfo(b).Size()
			if sa != sb {
				// Largest first.
				if sa > sb {
					return -1
				}
				return 1
			}
			// Break ties alphabetically by name.
			na, nb := getName(a), getName(b)
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
			return 0
		})
	} else if opts.sortTime {
		slices.SortFunc(entries, func(a, b T) int {
			ta, tb := getInfo(a).ModTime(), getInfo(b).ModTime()
			if !ta.Equal(tb) {
				// Newest first.
				if ta.After(tb) {
					return -1
				}
				return 1
			}
			// Break ties alphabetically by name.
			na, nb := getName(a), getName(b)
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
			return 0
		})
	} else {
		// Default: sort alphabetically by name.
		slices.SortFunc(entries, func(a, b T) int {
			na, nb := getName(a), getName(b)
			if na < nb {
				return -1
			}
			if na > nb {
				return 1
			}
			return 0
		})
	}

	if opts.reverse {
		slices.Reverse(entries)
	}
}

func joinPath(dir, name string) string {
	if len(dir) == 0 {
		return name
	}
	if dir[len(dir)-1] == '/' {
		return dir + name
	}
	return dir + "/" + name
}

func humanSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d", size)
	}
	units := []string{"K", "M", "G", "T", "P"}
	val := float64(size)
	for _, u := range units {
		val /= 1024
		if val < 1024 {
			if val < 10 {
				return fmt.Sprintf("%.1f%s", val, u)
			}
			return fmt.Sprintf("%.0f%s", val, u)
		}
	}
	return fmt.Sprintf("%.0fP", val)
}

func formatTime(t time.Time, now time.Time) string {
	sixMonthsAgo := now.AddDate(0, -6, 0)
	if t.Before(sixMonthsAgo) || t.After(now) {
		// Old or future: show year instead of time.
		return t.Format("Jan _2  2006")
	}
	return t.Format("Jan _2 15:04")
}
