package scoring

import "testing"

// FuzzNormalizeTitleIdempotent verifies that NormalizeTitle is idempotent:
// applying it twice yields the same result as applying it once.
func FuzzNormalizeTitleIdempotent(f *testing.F) {
	f.Add("Breaking Bad")
	f.Add("the.office.us")
	f.Add("Attack-on_Titan: Final Season")
	f.Add("")
	f.Add("  multiple   spaces  ")
	f.Add("UPPER.case-MiXeD")

	f.Fuzz(func(t *testing.T, s string) {
		once := NormalizeTitle(s)
		twice := NormalizeTitle(once)
		if once != twice {
			t.Fatalf("NormalizeTitle not idempotent: %q → %q → %q", s, once, twice)
		}
	})
}

// FuzzTitlesMatchSymmetric verifies TitlesMatch is symmetric: TitlesMatch(a,b) == TitlesMatch(b,a).
func FuzzTitlesMatchSymmetric(f *testing.F) {
	f.Add("Breaking Bad", "breaking bad")
	f.Add("The Office", "the.office")
	f.Add("", "anything")
	f.Add("Inception", "Inception 2010")

	f.Fuzz(func(t *testing.T, a, b string) {
		ab := TitlesMatch(a, b)
		ba := TitlesMatch(b, a)
		if ab != ba {
			t.Fatalf("TitlesMatch not symmetric: (%q,%q)=%v but (%q,%q)=%v", a, b, ab, b, a, ba)
		}
	})
}

// FuzzIsSeasonPackImpliesSeason verifies that if IsSeasonPack returns true,
// ExtractReleaseSeason returns a positive season number.
func FuzzIsSeasonPackImpliesSeason(f *testing.F) {
	f.Add("Show.S01.1080p.BluRay")
	f.Add("Show.S02E01.720p")
	f.Add("random string")
	f.Add("")
	f.Add("S99")

	f.Fuzz(func(t *testing.T, releaseName string) {
		if IsSeasonPack(releaseName) {
			season := ExtractReleaseSeason(releaseName)
			if season <= 0 {
				t.Fatalf("IsSeasonPack(%q)=true but ExtractReleaseSeason=%d", releaseName, season)
			}
		}
	})
}
