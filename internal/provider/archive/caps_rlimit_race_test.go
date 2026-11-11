//go:build race

package archive

// limitAddressSpace sets no limit under the race detector, whose shadow
// memory reservation fails under one; the peak-heap bound is the check.
func limitAddressSpace() error { return nil }
