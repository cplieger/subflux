package wirespec_test

import (
	"crypto/sha256"
	"testing"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/wirespec"
)

func TestFileTagIsSharedFileForm(t *testing.T) {
	sum := sha256.Sum256([]byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"))
	e := mediawrite.Expectation{Generation: 7, SHA256: sum}
	if got, want := wirespec.FileTag(e), (httpwire.Tag{Open: [8]byte(sum[:8]), Rev: 7}); got != want {
		t.Errorf("FileTag = %v, want %v", got, want)
	}
	otherContent := e
	otherContent.SHA256[0] ^= 1
	otherGeneration := e
	otherGeneration.Generation++
	for name, o := range map[string]mediawrite.Expectation{"content": otherContent, "generation": otherGeneration} {
		if wirespec.FileTag(o) == wirespec.FileTag(e) {
			t.Errorf("two expectations differing only in %s answer one tag", name)
		}
	}
}
