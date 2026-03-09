//go:build windows

package test

import (
	"os"
	"path/filepath"
	"strings"
)

// windowsExeExts lists extensions that Windows considers executable.
var windowsExeExts = map[string]bool{
	".exe": true, ".cmd": true, ".bat": true, ".com": true,
}

// isExecutable reports whether the file described by fi is considered executable on Windows.
// Windows does not have Unix permission bits; instead, executability is determined by file extension.
// If the file has no recognizable executable extension, it is conservatively treated as executable
// (matching the behavior of test -x on most Windows environments).
func isExecutable(fi os.FileInfo) bool {
	ext := strings.ToLower(filepath.Ext(fi.Name()))
	if ext == "" {
		// No extension — treat as executable (consistent with Unix behavior for chmod +x files).
		return true
	}
	return windowsExeExts[ext]
}
