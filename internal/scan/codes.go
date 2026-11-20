package scan

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel Start returns to the code the API answers for it.
// Start's *mediasource.SourceError is typed, not a sentinel: the handler
// answers it as its own *httpwire.Error.
var Owner = &httpwire.Owner{
	Name:      "scan",
	Sentinels: map[string]error{"ErrScanCapacity": ErrScanCapacity},
	Codes:     []httpwire.OwnerCode{{Err: ErrScanCapacity, Code: "scan_capacity"}},
}
