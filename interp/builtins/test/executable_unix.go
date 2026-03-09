//go:build !windows

package test

import "os"

// isExecutable reports whether the file described by fi has the execute permission bit set.
func isExecutable(fi os.FileInfo) bool {
	return fi.Mode().Perm()&0111 != 0
}
