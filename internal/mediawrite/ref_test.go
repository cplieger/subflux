package mediawrite_test

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/mediawrite"
)

func TestValidNameAgreesWithDecodeRef(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"movie.en.srt", true},
		{"Movie (2020).pt-BR.sdh.forced.2.ass", true},
		{"movie.en.SSA", true},
		{"movie.en.sub", true},
		{"..", false},
		{".", false},
		{"", false},
		{"a/b.srt", false},
		{"../movie.en.srt", false},
		{"/movie.en.srt", false},
		{`a\b.srt`, false},
		{"movie\x00.srt", false},
		{"x.vtt", false},
		{"movie.en.idx", false},
		{"movie.en.srt.tmp", false},
	} {
		err := mediawrite.ValidName(tc.name)
		ref, refErr := mediawrite.DecodeRef(7, tc.name)
		switch {
		case tc.valid && (err != nil || refErr != nil):
			t.Errorf("%q: ValidName %v, DecodeRef %v; want both to accept", tc.name, err, refErr)
		case !tc.valid && (!errors.Is(err, mediawrite.ErrInvalidRef) || !errors.Is(refErr, mediawrite.ErrInvalidRef)):
			t.Errorf("%q: ValidName %v, DecodeRef %v; want ErrInvalidRef from both", tc.name, err, refErr)
		case tc.valid && (ref.Name() != tc.name || ref.Inst() != 7):
			t.Errorf("%q: DecodeRef = %q of %d, want the name of instance 7", tc.name, ref.Name(), ref.Inst())
		}
	}
	if _, err := mediawrite.DecodeRef(0, "movie.en.srt"); !errors.Is(err, mediawrite.ErrInvalidRef) {
		t.Errorf("DecodeRef of the zero instance = %v, want ErrInvalidRef", err)
	}
}
