// Package search is an allowed importer of release.
package search

import "example.com/confinement/internal/release"

// Group reads a parsed name's group.
func Group(p release.Parsed) string { return p.Group }
