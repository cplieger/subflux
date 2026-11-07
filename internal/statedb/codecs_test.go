package statedb

import (
	"encoding/json"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/timing"
)

func TestCodecVersionFoldsFormatVersion(t *testing.T) {
	tagged := map[string]uint32{
		"locks key":      lockKeys.Version(),
		"ordinals key":   ordinalKeys.Version(),
		"no_results key": noResultKeys.Version(),
	}
	for name, v := range tagged {
		t.Run(name, func(t *testing.T) {
			if want := uint32(1)<<16 + uint32(language.FormatVersion); v != want {
				t.Errorf("Version = %#x; want own version 1 times 2^16 plus FormatVersion, %#x", v, want)
			}
		})
	}
	if v := taggedVersion(3); v>>16 != 3 || uint16(v) != language.FormatVersion {
		t.Errorf("taggedVersion(3) = %#x; want 3 in the high half, FormatVersion in the low", v)
	}
}

func TestCodecVersionFoldsTimingCodec(t *testing.T) {
	v := timedVersion(2)
	if v>>24 != 2 || uint8(v>>16) != uint8(timing.CodecVersion) || uint16(v) != language.FormatVersion {
		t.Errorf("timedVersion(2) = %#x; want 2, CodecVersion %d, FormatVersion %d", v, timing.CodecVersion, language.FormatVersion)
	}
}

func TestRowTagDecodesThroughParse(t *testing.T) {
	t.Run("an alias reads as its representative", func(t *testing.T) {
		var r rowTag
		if err := json.Unmarshal([]byte(`"mo"`), &r); err != nil || r.t.String() != "ro-MD" {
			t.Errorf("decode mo = %q, %v; want ro-MD", r.t, err)
		}
	})
	t.Run("text naming no language fails", func(t *testing.T) {
		var r rowTag
		if err := json.Unmarshal([]byte(`"xx-notalanguage-zz"`), &r); err == nil {
			t.Errorf("decode succeeded as %q", r.t)
		}
	})
	t.Run("empty text is the zero tag", func(t *testing.T) {
		r := rowTag{t: mustTag(t, "en")}
		if err := json.Unmarshal([]byte(`""`), &r); err != nil || r.t != (language.Tag{}) {
			t.Errorf("decode \"\" = %q, %v; want the zero Tag", r.t, err)
		}
	})
	t.Run("it writes the canonical text", func(t *testing.T) {
		b, err := json.Marshal(rowTag{t: mustTag(t, "pt-br")})
		if err != nil || string(b) != `"pt-BR"` {
			t.Errorf("encode = %s, %v; want \"pt-BR\"", b, err)
		}
	})
}
