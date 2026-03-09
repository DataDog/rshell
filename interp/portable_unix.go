//go:build !windows

package interp

import (
	"errors"
	"syscall"
)

func isErrIsDirectory(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}
