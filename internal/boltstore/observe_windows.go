//go:build windows

package boltstore

import (
	"errors"
	"syscall"
)

// isENOSPC reports whether the error chain contains the Windows disk-full error
// (ERROR_DISK_FULL = 112). This is the Windows equivalent of ENOSPC.
func isENOSPC(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		// ERROR_DISK_FULL = 112
		return errno == 112
	}
	return false
}
