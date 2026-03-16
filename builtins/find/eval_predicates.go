// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package find

import (
	"fmt"
	iofs "io/fs"
	"math"
	"strconv"
	"time"
)

// --- Time predicates (-atime, -amin, -ctime, -cmin) ---

// maxAtimeN is the largest N for which (N+1)*24*time.Hour does not overflow.
const maxAtimeN = int64(math.MaxInt64/(int64(24*time.Hour))) - 1

// maxAminN is the largest N for which time.Duration(N)*time.Minute does not overflow.
const maxAminN = int64(math.MaxInt64 / int64(time.Minute))

func evalAtime(ec *evalContext, n int64, cmp cmpOp) bool {
	atime := fileAtime(ec.info)
	return evalTimeInDays(ec.now, atime, n, cmp, maxAtimeN)
}

func evalAmin(ec *evalContext, n int64, cmp cmpOp) bool {
	atime := fileAtime(ec.info)
	return evalTimeInMinutes(ec.now, atime, n, cmp, maxAminN)
}

func evalCtime(ec *evalContext, n int64, cmp cmpOp) bool {
	ctime := fileCtime(ec.info)
	return evalTimeInDays(ec.now, ctime, n, cmp, maxAtimeN)
}

func evalCmin(ec *evalContext, n int64, cmp cmpOp) bool {
	ctime := fileCtime(ec.info)
	return evalTimeInMinutes(ec.now, ctime, n, cmp, maxAminN)
}

// evalTimeInDays implements the same logic as evalMtime but for an arbitrary time.
func evalTimeInDays(now, fileTime time.Time, n int64, cmp cmpOp, maxN int64) bool {
	switch cmp {
	case cmpMore:
		if n > maxN {
			return false
		}
		diff := now.Truncate(time.Second).Sub(fileTime)
		return diff >= time.Duration(n+1)*24*time.Hour
	case cmpLess:
		if n > maxN {
			return true
		}
		diff := now.Truncate(time.Second).Sub(fileTime)
		return diff < time.Duration(n)*24*time.Hour
	default:
		diff := now.Sub(fileTime)
		days := int64(math.Floor(diff.Hours() / 24))
		return days == n
	}
}

// evalTimeInMinutes implements the same logic as evalMmin but for an arbitrary time.
func evalTimeInMinutes(now, fileTime time.Time, n int64, cmp cmpOp, maxN int64) bool {
	diff := now.Sub(fileTime)
	switch cmp {
	case cmpMore:
		if n > maxN {
			return false
		}
		return diff > time.Duration(n)*time.Minute
	case cmpLess:
		if n > maxN {
			return true
		}
		return diff < time.Duration(n)*time.Minute
	default:
		mins := int64(math.Ceil(diff.Minutes()))
		return mins == n
	}
}

// --- Ownership predicates (-user, -group, -uid, -gid, -nouser, -nogroup) ---

func evalUser(ec *evalContext, name string) bool {
	// Try numeric UID first (GNU find supports both).
	if uid, err := strconv.ParseUint(name, 10, 32); err == nil {
		fileUID, ok := fileUid(ec.info)
		if !ok {
			return false
		}
		return fileUID == uint32(uid)
	}
	// Lookup by name.
	targetUID, ok := lookupUidByName(name)
	if !ok {
		ec.callCtx.Errf("find: '%s' is not the name of a known user\n", name)
		ec.failed = true
		return false
	}
	fileUID, ok := fileUid(ec.info)
	if !ok {
		return false
	}
	return fileUID == targetUID
}

func evalGroup(ec *evalContext, name string) bool {
	// Try numeric GID first.
	if gid, err := strconv.ParseUint(name, 10, 32); err == nil {
		fileGID, ok := fileGid(ec.info)
		if !ok {
			return false
		}
		return fileGID == uint32(gid)
	}
	// Lookup by name.
	targetGID, ok := lookupGidByName(name)
	if !ok {
		ec.callCtx.Errf("find: '%s' is not the name of a known group\n", name)
		ec.failed = true
		return false
	}
	fileGID, ok := fileGid(ec.info)
	if !ok {
		return false
	}
	return fileGID == targetGID
}

func evalUid(ec *evalContext, n int64, cmp cmpOp) bool {
	uid, ok := fileUid(ec.info)
	if !ok {
		return false
	}
	return compareNumeric(int64(uid), n, cmp)
}

func evalGid(ec *evalContext, n int64, cmp cmpOp) bool {
	gid, ok := fileGid(ec.info)
	if !ok {
		return false
	}
	return compareNumeric(int64(gid), n, cmp)
}

func evalNouser(ec *evalContext) bool {
	uid, ok := fileUid(ec.info)
	if !ok {
		return false
	}
	return !uidHasPasswdEntry(uid)
}

func evalNogroup(ec *evalContext) bool {
	gid, ok := fileGid(ec.info)
	if !ok {
		return false
	}
	return !gidHasGroupEntry(gid)
}

// --- Link predicates (-links, -inum, -samefile) ---

func evalLinks(ec *evalContext, n int64, cmp cmpOp) bool {
	nlink, ok := fileNlink(ec.info)
	if !ok {
		return false
	}
	return compareNumeric(int64(nlink), n, cmp)
}

func evalInum(ec *evalContext, n int64, cmp cmpOp) bool {
	ino, ok := fileIno(ec.info)
	if !ok {
		return false
	}
	return compareNumeric(int64(ino), n, cmp)
}

func evalSamefile(ec *evalContext, refPath string) bool {
	if ec.callCtx.FileIdentity == nil {
		return false
	}
	// Get identity of current file.
	curID, ok := ec.callCtx.FileIdentity(ec.printPath, ec.info)
	if !ok {
		return false
	}
	// Stat the reference file.
	statRef := ec.callCtx.LstatFile
	if ec.followLinks {
		statRef = ec.callCtx.StatFile
	}
	refInfo, err := statRef(ec.ctx, refPath)
	if err != nil {
		if ec.followLinks && isNotExist(err) {
			refInfo, err = ec.callCtx.LstatFile(ec.ctx, refPath)
		}
		if err != nil {
			return false
		}
	}
	refID, ok := ec.callCtx.FileIdentity(refPath, refInfo)
	if !ok {
		return false
	}
	return curID == refID
}

// --- Actions (-ls, -printf) ---

func evalLs(ec *evalContext) {
	info := ec.info
	mode := info.Mode()

	// Get inode and nlink.
	ino, _ := fileIno(info)
	nlink, _ := fileNlink(info)
	if nlink == 0 {
		nlink = 1
	}

	// Size in 1K blocks (ceiling).
	size := info.Size()
	blocks := (size + 1023) / 1024
	if size == 0 {
		blocks = 0
	}

	// Permission string.
	permStr := formatModeString(mode)

	// Owner and group.
	owner := "0"
	group := "0"
	if uid, ok := fileUid(info); ok {
		owner = strconv.FormatUint(uint64(uid), 10)
	}
	if gid, ok := fileGid(info); ok {
		group = strconv.FormatUint(uint64(gid), 10)
	}

	// Time formatting: recent files show time, old files show year.
	modTime := info.ModTime()
	var timeStr string
	sixMonthsAgo := ec.now.AddDate(0, -6, 0)
	if modTime.Before(sixMonthsAgo) || modTime.After(ec.now) {
		timeStr = modTime.Format("Jan _2  2006")
	} else {
		timeStr = modTime.Format("Jan _2 15:04")
	}

	ec.callCtx.Outf("%7d %4d %s %3d %-8s %-8s %8d %s %s\n",
		ino, blocks, permStr, nlink, owner, group, size, timeStr, ec.printPath)
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
			case 'f': // filename (basename)
				out = append(out, baseName(ec.printPath)...)
			case 'h': // dirname
				dir := dirName(ec.printPath)
				out = append(out, dir...)
			case 's': // size in bytes
				out = append(out, strconv.FormatInt(ec.info.Size(), 10)...)
			case 'd': // depth
				out = append(out, strconv.Itoa(ec.depth)...)
			case 'm': // octal permissions
				out = append(out, fmt.Sprintf("%o", ec.info.Mode().Perm())...)
			case 'M': // ls-style permission string
				out = append(out, formatModeString(ec.info.Mode())...)
			case 'u': // user name (or UID)
				if uid, ok := fileUid(ec.info); ok {
					out = append(out, strconv.FormatUint(uint64(uid), 10)...)
				} else {
					out = append(out, '0')
				}
			case 'g': // group name (or GID)
				if gid, ok := fileGid(ec.info); ok {
					out = append(out, strconv.FormatUint(uint64(gid), 10)...)
				} else {
					out = append(out, '0')
				}
			case 'y': // file type character
				out = append(out, fileTypeChar(ec.info))
			case 't': // modification time
				out = append(out, ec.info.ModTime().Format("Mon Jan _2 15:04:05.0000000000 2006")...)
			case 'l': // link count
				if nlink, ok := fileNlink(ec.info); ok {
					out = append(out, strconv.FormatUint(nlink, 10)...)
				} else {
					out = append(out, '1')
				}
			case 'i': // inode
				if ino, ok := fileIno(ec.info); ok {
					out = append(out, strconv.FormatUint(ino, 10)...)
				} else {
					out = append(out, '0')
				}
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
