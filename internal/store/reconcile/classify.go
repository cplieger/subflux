// Package reconcile extracts the subtitle_state reconciliation subsystem.
// It classifies stale DB entries by checking filesystem state and performs
// batch cleanup (delete or reset) in transactions.
package reconcile

import (
	"errors"
	"os"

	"github.com/cplieger/subflux/internal/api"
)

// Entry holds data for a single subtitle_state row during reconciliation.
type Entry struct {
	SubPath   string
	VideoPath string
	MediaType api.MediaType
	MediaID   string
	Language  string
	ID        int64
	Manual    bool
}

// GroupKey identifies a media+language combination for reconciliation grouping.
type GroupKey struct {
	Typ  api.MediaType
	ID   string
	Lang string
}

// Action is a typed string for reconciliation classification results.
type Action string

// Action constants returned by ClassifyEntry.
const (
	ActionSkip       Action = "skip"
	ActionDelete     Action = "delete"
	ActionSubMissing Action = "sub_missing"
	ActionSubPresent Action = "sub_present"
)

// StatFunc checks file existence.
type StatFunc func(path string) (os.FileInfo, error)

// ClassifyEntry determines the reconciliation action for a single row.
// Pure function: no logging or side-effects.
func ClassifyEntry(e *Entry, stat StatFunc) Action {
	if e.VideoPath == "" {
		return ActionSkip
	}
	if _, err := stat(e.VideoPath); errors.Is(err, os.ErrNotExist) {
		return ActionDelete
	} else if err != nil {
		return ActionSkip
	}
	if e.SubPath == "" {
		return ActionSkip
	}
	if _, err := stat(e.SubPath); errors.Is(err, os.ErrNotExist) {
		return ActionSubMissing
	} else if err != nil {
		return ActionSubPresent
	}
	return ActionSubPresent
}
