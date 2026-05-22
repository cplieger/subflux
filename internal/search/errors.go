package search

import "errors"

var (
	// ErrProviderNotFound indicates the provider name doesn't match any registered provider.
	ErrProviderNotFound = errors.New("provider not found")
	// ErrEmptyResponse indicates the provider responded with zero bytes.
	ErrEmptyResponse = errors.New("provider returned empty data")
	// ErrInvalidContent indicates the provider returned non-subtitle content.
	ErrInvalidContent = errors.New("provider returned invalid data")
)

// Error wraps a search-level sentinel error with transient classification.
// Implements the api.Transient interface so callers can distinguish permanent
// failures (don't retry) from transient ones (retry with backoff).
type Error struct {
	Err       error
	transient bool
}

// NewPermanentError wraps err as a non-transient Error.
func NewPermanentError(err error) *Error {
	return &Error{Err: err, transient: false}
}

func (e *Error) Error() string { return e.Err.Error() }
func (e *Error) Unwrap() error { return e.Err }

// IsTransient returns whether this error is transient (worth retrying).
func (e *Error) IsTransient() bool { return e.transient }
