// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"fmt"
	iofs "io/fs"
	"strconv"
)

// --- Actions (-printf) ---

func evalPrintf(ec *evalContext, format string) {
	var out []byte
	for i := 0; i < len(format); i++ {
		if format[i] == '\\' && i+1 < len(format) {
			i++
			switch format[i] {
			case 'n':
				out = append(out, '\n')
			case 't':
				out = append(out, '\t')
			case '0':
				out = append(out, 0)
			case '\\':
				out = append(out, '\\')
			default:
				out = append(out, '\\', format[i])
			}
			continue
		}
		if format[i] == '%' && i+1 < len(format) {
			i++
			switch format[i] {
			case 'p': // path
				out = append(out, ec.printPath...)
			case 'P': // path with starting-point prefix removed
				rel := ec.printPath
				if len(ec.startPath) > 0 && len(rel) > len(ec.startPath) {
					trimmed := rel[len(ec.startPath):]
					if len(trimmed) > 0 && trimmed[0] == '/' {
						trimmed = trimmed[1:]
					}
					rel = trimmed
				} else if rel == ec.startPath {
					rel = ""
				}
				out = append(out, rel...)
			case 'H': // starting-point under which file was found
				out = append(out, ec.startPath...)
			case 'f': // filename (basename)
				out = append(out, baseName(ec.printPath)...)
			case 'h': // dirname
				dir := dirName(ec.printPath)
				out = append(out, dir...)
			case 's': // size in bytes
				out = append(out, strconv.FormatInt(ec.info.Size(), 10)...)
			case 'k': // size in 1K blocks
				kblocks := (ec.info.Size() + 1023) / 1024
				if ec.info.Size() == 0 {
					kblocks = 0
				}
				out = append(out, strconv.FormatInt(kblocks, 10)...)
			case 'd': // depth
				out = append(out, strconv.Itoa(ec.depth)...)
			case 'm': // octal permissions
				out = append(out, fmt.Sprintf("%o", ec.info.Mode().Perm())...)
			case 'M': // ls-style permission string
				out = append(out, formatModeString(ec.info.Mode())...)
			case 'y': // file type character
				out = append(out, fileTypeChar(ec.info))
			case 't': // modification time
				out = append(out, ec.info.ModTime().Format("Mon Jan _2 15:04:05.0000000000 2006")...)
			case '%': // literal %
				out = append(out, '%')
			default:
				out = append(out, '%', format[i])
			}
			continue
		}
		out = append(out, format[i])
	}
	ec.callCtx.Out(string(out))
}

// formatModeString returns a ls-style permission string like "drwxr-xr-x".
func formatModeString(mode iofs.FileMode) string {
	var buf [10]byte

	// File type character.
	switch {
	case mode&iofs.ModeDir != 0:
		buf[0] = 'd'
	case mode&iofs.ModeSymlink != 0:
		buf[0] = 'l'
	case mode&iofs.ModeNamedPipe != 0:
		buf[0] = 'p'
	case mode&iofs.ModeSocket != 0:
		buf[0] = 's'
	case mode&iofs.ModeCharDevice != 0:
		buf[0] = 'c'
	case mode&iofs.ModeDevice != 0:
		buf[0] = 'b'
	default:
		buf[0] = '-'
	}

	// Permission bits.
	const rwx = "rwx"
	perm := mode.Perm()
	for i := 0; i < 9; i++ {
		if perm&(1<<uint(8-i)) != 0 {
			buf[1+i] = rwx[i%3]
		} else {
			buf[1+i] = '-'
		}
	}

	return string(buf[:])
}

// dirName returns the directory component of a path.
func dirName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' {
			if i == 0 {
				return "/"
			}
			return p[:i]
		}
	}
	return "."
}
