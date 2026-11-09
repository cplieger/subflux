package statedb

// Codes is every sentinel the store's reads and SetLock return, with the code
// api answers for it. The pass calls' ErrPassUnknown and ErrSourceInactive
// and Open's ErrLocked stay outside, since no endpoint makes those calls. The
// element type is unnamed, so statedb never imports api's wirespec.
var Codes = []struct {
	Err  error
	Code string
}{
	{ErrCursorInvalid, "cursor_invalid"},
	{ErrNotLockable, "not_lockable"},
}
