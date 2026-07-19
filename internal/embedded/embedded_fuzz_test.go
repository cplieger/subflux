package embedded

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzNormalizeCodecName exercises codec name normalization with arbitrary inputs
// to ensure it never panics and returns a non-empty string for non-empty input.
func FuzzNormalizeCodecName(f *testing.F) {
	f.Add("subrip")
	f.Add("ass")
	f.Add("webvtt")
	f.Add("hdmv_pgs_subtitle")
	f.Add("unknown_codec")
	f.Add("")

	f.Fuzz(func(t *testing.T, codec string) {
		result := normalizeCodecName(codec)

		// Invariant 1: never panics (implicit).

		// Invariant 2: non-empty input yields non-empty output (passthrough).
		if codec != "" && result == "" {
			t.Fatalf("normalizeCodecName(%q) returned empty", codec)
		}

		// Invariant 3: empty input returns empty (identity).
		if codec == "" && result != "" {
			t.Fatalf("normalizeCodecName(\"\") = %q, want \"\"", result)
		}
	})
}

// isASCII reports whether s contains only ASCII bytes.
func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// FuzzDetectHIFromName fuzzes hearing-impaired name classification with
// properties derived from its contract (case-insensitive substring match on
// the positive tokens "sdh", "hearing impaired", "hard of hearing"; there
// are no negating patterns):
//
//  1. Token injection: splicing a known HI token between arbitrary prefix
//     and suffix bytes MUST classify as HI. The tokens are pure ASCII, and
//     ASCII bytes can never be consumed as part of a surrounding multi-byte
//     UTF-8 sequence, so this holds even for invalid-UTF-8 surroundings.
//  2. Positive monotonicity: a detected name stays detected under any
//     prefix/suffix extension (all patterns are positive, so extra bytes
//     can only add matches, never remove one).
//  3. ASCII case-fold invariance: for all-ASCII input, classification is
//     identical across original/lower/upper casings. Deliberately not
//     asserted for full Unicode: exotic one-way case mappings (ſ→S, K→k)
//     are outside the classifier's contract.
func FuzzDetectHIFromName(f *testing.F) {
	f.Add("English SDH", "")
	f.Add("", "hearing impaired")
	f.Add("Hard of ", "Hearing")
	f.Add("Normal subtitle", " track")
	f.Add("s", "dh") // token assembled across the seam
	f.Add("", "")

	hiTokens := []string{"sdh", "SDH", "hearing impaired", "Hard of Hearing"}
	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		for _, token := range hiTokens {
			if name := prefix + token + suffix; !detectHIFromName(name) {
				t.Errorf("detectHIFromName(%q) = false, want true (contains HI token %q)",
					name, token)
			}
		}

		joined := prefix + suffix
		if detectHIFromName(prefix) && !detectHIFromName(joined) {
			t.Errorf("suffix extension lost HI detection: %q detected, %q not",
				prefix, joined)
		}
		if detectHIFromName(suffix) && !detectHIFromName(joined) {
			t.Errorf("prefix extension lost HI detection: %q detected, %q not",
				suffix, joined)
		}

		if isASCII(joined) {
			base := detectHIFromName(joined)
			if got := detectHIFromName(strings.ToLower(joined)); got != base {
				t.Errorf("detectHIFromName(%q) = %v but lowercase form = %v", joined, base, got)
			}
			if got := detectHIFromName(strings.ToUpper(joined)); got != base {
				t.Errorf("detectHIFromName(%q) = %v but uppercase form = %v", joined, base, got)
			}
		}
	})
}

// FuzzDetectForcedFromName fuzzes forced-subtitle name classification. Its
// contract is classify.IsForced: case-insensitive substring match over
// ForcedRules, whose entries are ALL positive ("forced", "foreign") by
// documented design. The same three property families as the HI target
// apply; if ForcedRules ever gains a negative (denying) pattern, the
// monotonicity property below encodes exactly the contract that changed and
// must be revisited alongside it.
func FuzzDetectForcedFromName(f *testing.F) {
	f.Add("English ", "")
	f.Add("Signs & Songs", "")
	f.Add("", " (full)")
	f.Add("for", "ced") // token assembled across the seam
	f.Add("", "")

	forcedTokens := []string{"forced", "FORCED", "Foreign"}
	f.Fuzz(func(t *testing.T, prefix, suffix string) {
		for _, token := range forcedTokens {
			if name := prefix + token + suffix; !detectForcedFromName(name) {
				t.Errorf("detectForcedFromName(%q) = false, want true (contains token %q)",
					name, token)
			}
		}

		joined := prefix + suffix
		if detectForcedFromName(prefix) && !detectForcedFromName(joined) {
			t.Errorf("suffix extension lost forced detection: %q detected, %q not",
				prefix, joined)
		}
		if detectForcedFromName(suffix) && !detectForcedFromName(joined) {
			t.Errorf("prefix extension lost forced detection: %q detected, %q not",
				suffix, joined)
		}

		if isASCII(joined) {
			base := detectForcedFromName(joined)
			if got := detectForcedFromName(strings.ToLower(joined)); got != base {
				t.Errorf("detectForcedFromName(%q) = %v but lowercase form = %v", joined, base, got)
			}
			if got := detectForcedFromName(strings.ToUpper(joined)); got != base {
				t.Errorf("detectForcedFromName(%q) = %v but uppercase form = %v", joined, base, got)
			}
		}
	})
}

// FuzzNormalizeTrack exercises the embedded subtitle track normalization
// with arbitrary codec/lang/name strings and flag combinations.
//
// Bug class: panic on empty lang causing nil dereference downstream;
// BCP 47 subtag extraction off-by-one; HI/forced detection false positives
// on adversarial track names.
func FuzzNormalizeTrack(f *testing.F) {
	f.Add(0, "subrip", "eng", "English", false, false)
	f.Add(1, "ass", "en-US", "SDH", true, false)
	f.Add(2, "hdmv_pgs_subtitle", "", "", false, false)
	f.Add(3, "subrip", "und", "Forced", false, true)
	f.Add(4, "dvd_subtitle", "fra-CA", "Commentary (Hearing Impaired)", false, false)

	f.Fuzz(func(t *testing.T, index int, codec, lang, name string, forced, hi bool) {
		track := normalizeTrack(index, codec, lang, name, forced, hi)
		if track == nil {
			// nil is valid for empty/undefined lang.
			return
		}
		if track.lang == "" {
			t.Fatal("non-nil track must have non-empty lang")
		}
	})
}
