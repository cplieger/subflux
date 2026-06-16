package boltstore

import (
	"os"

	bolt "go.etcd.io/bbolt"
)

// StoreFileStats returns the current database file size and reclaimable freelist
// bytes. These are cheap reads: file size from os.Stat, freelist from
// bbolt's in-memory DB.Stats() (no transaction required for the freelist stat).
// The caller (typically the metrics collector on a periodic timer or at scrape
// time) uses these to set the subflux_store_file_bytes and
// subflux_store_freelist_bytes gauges.
//
// If the store is not open or a stat error occurs, it returns zeros without
// error (metrics should degrade gracefully, not block scrapes).
func (d *DB) StoreFileStats() (fileBytes int64, freelistBytes int64) {
	if d == nil || d.db == nil {
		return 0, 0
	}

	// File size via the bbolt file path (cheaper than opening a read tx).
	path := d.db.Path()
	if fi, err := os.Stat(path); err == nil {
		fileBytes = fi.Size()
	}

	// Freelist stats from bbolt's in-memory tracking. DB.Stats() is a
	// snapshot of the database's internal counters and does not require a
	// transaction. FreeAlloc is the total bytes allocated by the freelist
	// (free pages × page size), representing reclaimable space.
	stats := d.db.Stats()
	freelistBytes = int64(stats.FreeAlloc)

	return fileBytes, freelistBytes
}

// DiskFullError wraps a bbolt write error that indicates the underlying
// storage is full (ENOSPC) or otherwise unable to accept writes. The server
// uses this to decide whether to raise a persistent alert instead of
// crash-looping.
func IsDiskFullError(err error) bool {
	if err == nil {
		return false
	}
	// bbolt surfaces the underlying OS error from mmap/write syscalls.
	// Check for known disk-full and permission errors.
	if os.IsPermission(err) {
		return true
	}
	// Check for ENOSPC via the error string — Go's os package does not
	// provide a direct os.IsNoSpace helper, and errors.Is(err, syscall.ENOSPC)
	// works on Linux but the error may be wrapped by bbolt. A string match
	// is the pragmatic cross-platform fallback.
	if isENOSPC(err) {
		return true
	}
	// bbolt.ErrDatabaseNotOpen can surface when the file handle is lost
	// due to I/O errors; treat it as a potential disk issue.
	if err == bolt.ErrDatabaseNotOpen {
		return true
	}
	return false
}
