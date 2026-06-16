//go:build !windows

package boltstore

import (
	"errors"
	"syscall"
)

// isENOSPC reports whether the error chain contains syscall.ENOSPC (no space
// left on device). On Linux this is the canonical disk-full signal from bbolt's
// mmap grow or fdatasync.
func isENOSPC(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ENOSPC
	}
	return false
}
