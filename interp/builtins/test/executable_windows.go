//go:build windows

package test

import "os"

// isExecutable reports whether the file described by fi is considered executable on Windows.
// Windows does not have Unix permission bits, so all existing files are treated as executable.
func isExecutable(_ os.FileInfo) bool {
	return true
}
