package authstore

import (
	"testing"

	"pgregory.net/rapid"
)

// Property tests for asciiFold, the case-insensitive username/email normalizer.
// Username and (issuer, sub) uniqueness are both enforced through this fold
// (userNameIndexKey / GetUserByUsername / GetUserByEmail), so a regression that
// folded too much, too little, or corrupted a non-ASCII byte would silently
// merge or split distinct accounts. asciiFold operates byte-wise (it never
// decodes runes), so arbitrary byte strings — including invalid UTF-8 and
// multi-byte sequences — are the exact input space.

// TestAsciiFold_foldContract pins the full byte-wise contract: length is
// preserved, each ASCII A-Z byte lowercases by exactly +32, every other byte
// (including bytes >=0x80 that make up multi-byte UTF-8) passes through
// untouched, the result holds no remaining ASCII A-Z, and the fold is
// idempotent (a normalizer applied twice equals applied once).
func TestAsciiFold_foldContract(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		in := string(rapid.SliceOf(rapid.Byte()).Draw(t, "bytes"))
		got := asciiFold(in)

		if len(got) != len(in) {
			t.Fatalf("asciiFold(%q) length = %d, want %d", in, len(got), len(in))
		}
		for i := range len(in) {
			b, out := in[i], got[i]
			switch {
			case b >= 'A' && b <= 'Z':
				if want := b + ('a' - 'A'); out != want {
					t.Fatalf("byte %d (%#x): asciiFold = %#x, want %#x (lowercased)", i, b, out, want)
				}
			default:
				if out != b {
					t.Fatalf("byte %d (%#x): asciiFold changed it to %#x, want unchanged", i, b, out)
				}
			}
		}

		// No ASCII A-Z remains, and folding again is a no-op (idempotent).
		for i := range len(got) {
			if got[i] >= 'A' && got[i] <= 'Z' {
				t.Fatalf("asciiFold(%q) = %q still holds uppercase ASCII at byte %d", in, got, i)
			}
		}
		if again := asciiFold(got); again != got {
			t.Fatalf("asciiFold not idempotent: asciiFold(%q) = %q, folded again = %q", in, got, again)
		}
	})
}
