//go:build !windows

package rshell

import (
	"errors"
	"syscall"
)

func isErrIsDirectory(err error) bool {
	return errors.Is(err, syscall.EISDIR)
}
