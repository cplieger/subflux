// Package f imports d in production.
package f

import "example.com/confinement/d"

// Name reads d.
const Name = d.Name
