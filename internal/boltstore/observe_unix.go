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
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return errno == syscall.ENOSPC
	}
	return false
}
