package wirespec

import (
	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/media"
)

// ListQuery pages a list.
type ListQuery struct {
	httpwire.Page
}

// HistoryQuery pages the history of one root, one item or the whole library.
type HistoryQuery struct {
	Root *media.RootKey `json:"root,omitempty"`
	Item *media.Key     `json:"item,omitempty"`
	httpwire.Page
}

// JobPage is jobs.list's answer: one page of every unfinished job, newest
// started first, at the published boot and seq.
type JobPage struct {
	httpwire.List[JobDoc]
	Seq  uint64 `json:"seq"`
	Boot Hex16  `json:"boot"`
}
