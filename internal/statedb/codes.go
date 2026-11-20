package statedb

import "github.com/cplieger/go/httpwire"

// Owner maps every sentinel the store's reads and SetLock return to the code
// the API answers for it.
var Owner = &httpwire.Owner{
	Name: "statedb",
	Sentinels: map[string]error{
		"ErrCorrupt":        ErrCorrupt,
		"ErrCursorInvalid":  ErrCursorInvalid,
		"ErrNotLockable":    ErrNotLockable,
		"ErrLocked":         ErrLocked,
		"ErrPassUnknown":    ErrPassUnknown,
		"ErrSourceInactive": ErrSourceInactive,
	},
	Outside: map[string]string{
		"ErrCorrupt":        "a store that does not decode is subflux's own failure, never a client's code",
		"ErrLocked":         "only Open returns it",
		"ErrPassUnknown":    "only BeginPass, ObserveItems and FinishPass return it, and no endpoint calls them",
		"ErrSourceInactive": "only BeginPass, ObserveItems and FinishPass return it, and no endpoint calls them",
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrCursorInvalid, Code: "cursor_invalid"},
		{Err: ErrNotLockable, Code: "not_lockable"},
	},
}
