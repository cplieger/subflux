package coverage

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// TestGk_subflux_u29_IndexSubStatus_embeddedIgnoredCodec kills coverage.go:46
// CONDITIONALS_NEGATION on "f.Source == string(api.ProviderNameEmbedded)".
// An embedded subtitle whose codec is in the ignored set must be recorded as
// IgnoredOnly (not Usable). The mutated guard "f.Source != embedded" makes
// the AND short-circuit to the else branch and marks the sub Usable instead.
func TestGk_subflux_u29_IndexSubStatus_embeddedIgnoredCodec(t *testing.T) {
	files := []api.SubtitleEntry{{
		MediaID:  "m1",
		Language: "en",
		Variant:  "",
		Source:   string(api.ProviderNameEmbedded),
		Codec:    "hdmv_pgs_subtitle",
	}}
	ignored := map[string]bool{"hdmv_pgs_subtitle": true}

	idx := IndexSubStatus(files, ignored)
	st := idx["m1"][Key{Lang: "en", Variant: ""}]
	if st == nil {
		t.Fatalf("IndexSubStatus: no status recorded for m1/en")
	}
	if st.Usable {
		t.Errorf("embedded ignored-codec sub: Usable = true, want false")
	}
	if !st.IgnoredOnly {
		t.Errorf("embedded ignored-codec sub: IgnoredOnly = false, want true")
	}
}

// TestGk_subflux_u29_ExtractSeriesPrefix kills coverage.go:74:33
// (CONDITIONALS_BOUNDARY on the loop bound "i >= 1" vs "i > 1"), :75:21
// (CONDITIONALS_NEGATION on "epMediaID[i-1] == '-'") and :75:44
// (CONDITIONALS_NEGATION on "epMediaID[i] == 's'").
//
//   - "tvdb-12345-s01e01": the only "-s" pair is at i==11. Negating the
//     first operand (75:21) makes the scan miss it → ""; negating the second
//     (75:44) matches the earlier "-1" pair at i==5 → "tvdb".
//   - "-s01e01": the matching "-s" is at index 0/1, so the loop must reach
//     i==1. The "i > 1" boundary mutant stops at i==2 and returns "".
func TestGk_subflux_u29_ExtractSeriesPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"tvdb-12345-s01e01", "tvdb-12345-"},
		{"-s01e01", "-"},
		{"nodash", ""},
	}
	for _, c := range cases {
		if got := ExtractSeriesPrefix(c.in); got != c.want {
			t.Errorf("ExtractSeriesPrefix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
