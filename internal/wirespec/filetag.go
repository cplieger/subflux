package wirespec

import (
	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/mediawrite"
)

// FileTag is the If-Match tag of a subtitle file in the state e names: the
// first 8 bytes of its SHA-256 and its generation, httpwire's shared-file
// form. A handler compares it with the request's tag, then hands the writer
// the current expectation, whose own check closes the race.
func FileTag(e mediawrite.Expectation) httpwire.Tag {
	return httpwire.Tag{Open: [8]byte(e.SHA256[:8]), Rev: e.Generation}
}
