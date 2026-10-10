package search

import "errors"

var (
	// errProviderNotFound indicates the provider name doesn't match any registered provider.
	errProviderNotFound = errors.New("provider not found")
	// errInvalidContent indicates the provider returned bytes that are not a
	// subtitle. It wraps subtitlefile.Validate's reason, so a caller that needs
	// to tell an empty download from a corrupt one tests for
	// subtitlefile.ErrEmpty rather than a second sentinel here.
	errInvalidContent = errors.New("provider returned invalid data")
	// ErrProviderGated reports a call the provider gate refused, so no request
	// was made. The wrapping error carries the gate's reason.
	ErrProviderGated = errors.New("provider gated")
)
