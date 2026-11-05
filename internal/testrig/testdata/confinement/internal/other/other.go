// Package other is a forbidden importer of release.
package other

import "example.com/confinement/internal/release"

// Group reads a parsed name's group.
func Group(p release.Parsed) string { return p.Group }
