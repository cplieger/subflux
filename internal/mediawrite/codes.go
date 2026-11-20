package mediawrite

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel the API-called exports return (List, Current,
// Retime, Delete, OpenVideo, DecodeRef, ValidName) to the code the API answers
// for it; timing's, which they pass on, are timing.Owner's.
var Owner = &httpwire.Owner{
	Name: "mediawrite",
	Sentinels: map[string]error{
		"ErrInvalidRef":      ErrInvalidRef,
		"ErrExpectation":     ErrExpectation,
		"ErrPathPending":     ErrPathPending,
		"ErrNameContended":   ErrNameContended,
		"ErrOutsideRoot":     ErrOutsideRoot,
		"ErrRootUnavailable": ErrRootUnavailable,
	},
	Outside: map[string]string{
		"ErrNameContended": "only Deliver returns it, and only search calls Deliver",
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrInvalidRef, Code: "not_found"},
		{Err: ErrExpectation, Code: "stale_version"},
		{Err: ErrPathPending, Code: "write_pending"},
		{Err: ErrOutsideRoot, Code: "outside_root"},
		{Err: ErrRootUnavailable, Code: "media_root_unavailable"},
	},
}
