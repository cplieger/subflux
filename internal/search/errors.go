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


