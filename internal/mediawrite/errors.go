package mediawrite

import "errors"

var (
	// ErrInvalidRef is a reference the writer cannot act on: an unknown
	// instance, a name that is not one local subtitle element, or a supersede
	// naming another instance.
	ErrInvalidRef = errors.New("mediawrite: invalid file reference")
	// ErrExpectation is a file whose bytes or record moved since the caller
	// listed it, or the zero Expectation of an unmanaged file listed without
	// hashing; nothing changed.
	ErrExpectation = errors.New("mediawrite: file changed since it was listed")
	// ErrPathPending is a name an unsettled write still owns.
	ErrPathPending = errors.New("mediawrite: a write to this file is pending")
	// ErrNameContended is a delivery whose name an outside writer took, or
	// whose numbered names were all taken.
	ErrNameContended = errors.New("mediawrite: no free name for the subtitle")
	// ErrOutsideRoot is a video no configured media root contains.
	ErrOutsideRoot = errors.New("mediawrite: video is outside every media root")
	// ErrRootUnavailable is a video whose root is configured but unmounted,
	// empty, read-only, hung or not yet recovered.
	ErrRootUnavailable = errors.New("mediawrite: media root unavailable")
)
