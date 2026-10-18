package mediasource

import (
	"errors"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// JoinQuery is one series as a source lists it, for the Set's aired join.
type JoinQuery struct {
	// Episodes are in the series' order.
	Episodes []Listed
	// Source and Root name the root whose stored evidence the join reads.
	Source   media.SourceID
	Root     NativeRef
	Language language.Tag
	Order    Order
	TVDB     uint64
}

// Listed is an episode as its source lists it, in its series' order.
type Listed struct {
	AirDate, Title            string
	Season, Episode, Absolute int
}

// Joined is a listed episode's aired place; OK false leaves it unidentified.
type Joined struct {
	Season, Episode, Absolute int
	OK                        bool
}

// ErrAiredOrderUnavailable wraps a join for a series in another order whose
// list agrees with no SkyHook reading; the Set reports its root unread, so
// nothing under it is retired or written.
var ErrAiredOrderUnavailable = errors.New("aired order unavailable")
