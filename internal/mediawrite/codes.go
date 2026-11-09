package mediawrite

import (
	"slices"

	"github.com/cplieger/subflux/internal/timing"
)

// Codes is every sentinel the api-called exports return (List, Current,
// Retime, Delete, OpenVideo, DecodeRef, ValidName), timing's passed on, with
// the code api answers for it. ErrNameContended is absent: only Deliver
// returns it, and only search calls Deliver.
var Codes = slices.Concat([]struct {
	Err  error
	Code string
}{
	{ErrInvalidRef, "param_invalid"},
	{ErrExpectation, "file_changed"},
	{ErrPathPending, "write_pending"},
	{ErrOutsideRoot, "outside_root"},
	{ErrRootUnavailable, "media_root_unavailable"},
}, timing.Codes)
