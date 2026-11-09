package arr

import (
	"strconv"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Native is an arr integer id's NativeID, its canonical decimal.
func Native(id int) mediasource.NativeID { return mediasource.NativeID(strconv.Itoa(id)) }

// External is an arr's TVDB or TMDB id as an external id, 0 (unknown) below 1.
func External(id int) uint64 {
	if id < 1 {
		return 0
	}
	return uint64(id)
}

// ID is the arr id ref names under kind: false for another kind or an id
// that is not a positive canonical decimal, which no arr root has.
func ID(ref mediasource.NativeRef, kind media.Kind) (int, bool) {
	id, err := strconv.Atoi(string(ref.ID))
	if ref.Kind != kind || err != nil || id < 1 || Native(id) != ref.ID {
		return 0, false
	}
	return id, true
}

// Tags keys every tag by its decimal id; the Set matches its labels.
func Tags(tags []arrapi.Tag) map[mediasource.TagRef]string {
	out := make(map[mediasource.TagRef]string, len(tags))
	for _, t := range tags {
		out[mediasource.TagRef(Native(t.ID))] = t.Label
	}
	return out
}
