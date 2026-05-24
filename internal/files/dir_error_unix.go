//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package files

import (
	"errors"
	"syscall"
)

func rollbackParentNotEmpty(err error) bool {
	return errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST)
}
