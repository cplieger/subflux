package api

import (
	"testing"

	"pgregory.net/rapid"
)

func TestCanonicalLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		// --- The internal invention ---
		{"internal pb round-trips", "pb", "pb"},
		{"internal pb uppercase", "PB", "pb"},
		{"internal pb padded", " pb ", "pb"},
		{"internal alpha-3 spelling pob", "pob", "pb"},
		{"pt-BR is the canonical tag for pb", "pt-BR", "pb"},
		{"pt-BR lowercased", "pt-br", "pb"},
		{"pt-BR from an alpha-3 base", "por-BR", "pb"},
		{"pt-BR with an underscore separator", "pt_BR", "pb"},
		{"European Portuguese is not pb", "pt", "pt"},
		{"explicit European region is not pb", "pt-PT", "pt"},
		{"alpha-3 Portuguese is not pb", "por", "pt"},

		// --- Codes whose canonical form the space cannot hold ---
		{"tl stays tl because fil has no two-letter form", "tl", "tl"},
		{"tgl canonicalizes to fil and is unusable", "tgl", ""},
		{"bh stays bh because bho has no two-letter form", "bh", "bh"},

		// --- Standard canonicalization langtag owns ---
		{"bibliographic ger", "ger", "de"},
		{"terminological deu", "deu", "de"},
		{"bibliographic fre", "fre", "fr"},
		{"terminological fra", "fra", "fr"},
		{"deprecated iw folds to he", "iw", "he"},
		{"deprecated in folds to id", "in", "id"},
		{"deprecated ji folds to yi", "ji", "yi"},
		{"deprecated nb folds to no", "nb", "no"},
		{"macrolanguage member nob reports no", "nob", "no"},
		{"macrolanguage nor reports no", "nor", "no"},
		{"nno is its own language", "nno", "nn"},
		{"macrolanguage member cmn reports zh", "cmn", "zh"},
		{"Persian bibliographic per", "per", "fa"},
		{"Persian terminological fas", "fas", "fa"},
		{"region discarded after being read", "en-US", "en"},
		{"script discarded", "zh-Hant", "zh"},
		{"mixed case", "EnG", "en"},

		// --- Rejections ---
		{"empty", "", ""},
		{"single letter", "e", ""},
		{"unassigned two-letter xx", "xx", ""},
		{"unassigned two-letter zz", "zz", ""},
		{"unknown three-letter", "xyz", ""},
		{"digits", "00", ""},
		{"non-letter two-char", "0x", ""},
		{"malformed leading dash", "-en", ""},
		{"undetermined placeholder", "und", ""},
		{"no-linguistic-content placeholder", "zxx", ""},
		{"multiple-languages placeholder", "mul", ""},
		{"private-use subtag", "qaa", ""},
		// Real languages with no ISO 639-1 assignment: admitting one would put a
		// three-letter code in a two-letter namespace.
		{"Cantonese", "yue", ""},
		{"Filipino", "fil", ""},
		{"Hawaiian", "haw", ""},
		{"Asturian", "ast", ""},

		// --- Provider-private spellings must NOT be understood here ---
		// hdbits means Brazilian by "br" and Slovenian by "si"; subdl means
		// Brazilian by "BR_PT". The registry reads them as Breton, Sinhala, and
		// Breton-in-Portugal, and this function answers to the registry. Each
		// provider translates its own dialect before reaching this point.
		{"br is Breton", "br", "br"},
		{"si is Sinhala", "si", "si"},
		{"se is Northern Sami", "se", "se"},
		{"kr is Kanuri", "kr", "kr"},
		{"BR_PT is Breton in Portugal, which has no two-letter form here", "BR_PT", "br"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := CanonicalLangCode(tt.input); got != tt.want {
				t.Errorf("CanonicalLangCode(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestValidLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"plain ISO 639-1", "en", true},
		// pb is a documented user-facing config value and predates the library,
		// so it must keep validating or every existing config breaks.
		{"internal pb", "pb", true},
		{"tl", "tl", true},
		{"empty", "", false},
		{"typo", "xx", false},
		{"garbage", "!!", false},
		// These name real languages but are not how the internal space spells
		// them, and nothing canonicalizes a configured code at match time, so
		// accepting them would promise a match that never happens.
		{"alpha-3 is not the internal spelling", "eng", false},
		{"uppercase is not the internal spelling", "EN", false},
		{"pt-BR is spelled pb internally", "pt-BR", false},
		{"deprecated iw is spelled he internally", "iw", false},
		{"nb is spelled no internally", "nb", false},
		{"a language with no two-letter code", "yue", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidLangCode(tt.input); got != tt.want {
				t.Errorf("ValidLangCode(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// PBT: the result is always empty or exactly two lowercase ASCII letters. This
// is the property the whole internal space rests on — the code becomes a
// subtitle filename segment and part of the bbolt state key, so anything else
// cannot round-trip through a scan.
func TestCanonicalLangCode_namespaceInvariant(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.OneOf(
			rapid.StringMatching(`[a-zA-Z]{0,4}`),
			rapid.StringMatching(`[a-zA-Z]{2,3}(-[A-Za-z0-9]{2,4}){0,2}`),
			rapid.String(),
		).Draw(t, "raw")

		got := CanonicalLangCode(raw)
		if got == "" {
			return
		}
		if len(got) != 2 {
			t.Fatalf("CanonicalLangCode(%q) = %q (len %d), want len 0 or 2", raw, got, len(got))
		}
		for _, r := range got {
			if r < 'a' || r > 'z' {
				t.Fatalf("CanonicalLangCode(%q) = %q, want lowercase ASCII letters", raw, got)
			}
		}
	})
}

// PBT: canonicalizing an already-canonical code does not move it. Callers
// canonicalize defensively (LookupLangName does, and so does every provider that
// normalizes a code that may already be internal), so a second pass has to be a
// no-op or those call sites silently change the answer.
func TestCanonicalLangCode_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.OneOf(
			rapid.StringMatching(`[a-zA-Z]{0,4}`),
			rapid.StringMatching(`[a-zA-Z]{2,3}(-[A-Za-z0-9]{2,4})?`),
			rapid.SampledFrom([]string{
				"pb", "pob", "pt-BR", "por-BR", "tl", "tgl", "bh", "nob", "iw",
				"cmn", "yue", "und", "br", "BR_PT", "si",
			}),
		).Draw(t, "raw")

		once := CanonicalLangCode(raw)
		twice := CanonicalLangCode(once)
		if once != twice {
			t.Fatalf("CanonicalLangCode not idempotent: CanonicalLangCode(%q) = %q, CanonicalLangCode(%q) = %q",
				raw, once, once, twice)
		}
	})
}

// PBT: ValidLangCode agrees with CanonicalLangCode being a fixed point. The two
// are separate entry points and config validation trusts the pair to answer
// consistently — a code reported valid must be one canonicalization leaves alone.
func TestValidLangCode_agreesWithCanonical(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		raw := rapid.OneOf(
			rapid.StringMatching(`[a-zA-Z]{0,4}`),
			rapid.StringMatching(`[a-zA-Z]{2,3}(-[A-Za-z0-9]{2,4})?`),
		).Draw(t, "raw")

		canon := CanonicalLangCode(raw)
		want := canon != "" && canon == raw
		if got := ValidLangCode(raw); got != want {
			t.Fatalf("ValidLangCode(%q) = %v, want %v (CanonicalLangCode(%q) = %q)",
				raw, got, want, raw, canon)
		}
	})
}

// Every code the display-name registry and the arr name table produce must be
// valid, or a language subflux advertises cannot be configured. This is the
// check that would have caught "tl" resolving to "" during the migration.
func TestCanonicalLangCode_internalCodesAreStable(t *testing.T) {
	t.Parallel()
	for name, code := range langNameMap {
		if !ValidLangCode(code) {
			t.Errorf("langNameMap[%q] = %q, which ValidLangCode rejects; "+
				"CanonicalLangCode(%q) = %q", name, code, code, CanonicalLangCode(code))
		}
	}
}

func FuzzCanonicalLangCode(f *testing.F) {
	f.Add("en")
	f.Add("eng")
	f.Add("pb")
	f.Add("pob")
	f.Add("pt-BR")
	f.Add("BR_PT")
	f.Add("und")
	f.Add("")
	f.Add("tl")
	f.Add("yue")
	f.Add("\x00\x01")
	f.Add("abcdefghijklmnopqrstuvwxyz")
	f.Add("en-u-0a-0a-u-00-00")

	f.Fuzz(func(t *testing.T, raw string) {
		got := CanonicalLangCode(raw)
		if got == "" {
			return
		}
		if len(got) != 2 {
			t.Fatalf("CanonicalLangCode(%q) = %q (len %d), want len 0 or 2", raw, got, len(got))
		}
		for _, r := range got {
			if r < 'a' || r > 'z' {
				t.Fatalf("CanonicalLangCode(%q) = %q, want lowercase ASCII letters", raw, got)
			}
		}
		// A canonical code is valid, and canonicalizing it again is a no-op.
		if !ValidLangCode(got) {
			t.Fatalf("CanonicalLangCode(%q) = %q, which ValidLangCode rejects", raw, got)
		}
		if again := CanonicalLangCode(got); again != got {
			t.Fatalf("CanonicalLangCode not idempotent: %q -> %q -> %q", raw, got, again)
		}
	})
}
