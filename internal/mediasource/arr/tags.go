package arr

import (
	"strconv"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Native is an arr integer id's NativeID, its canonical decimal.
func Native(id int) mediasource.NativeID { return mediasource.NativeID(strconv.Itoa(id)) }

// Tags keys every tag by its decimal id; the Set matches its labels.
func Tags(tags []arrapi.Tag) map[mediasource.TagRef]string {
	out := make(map[mediasource.TagRef]string, len(tags))
	for _, t := range tags {
		out[mediasource.TagRef(Native(t.ID))] = t.Label
	}
	return out
}
