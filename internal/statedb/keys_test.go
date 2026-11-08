package statedb

import (
	"bytes"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

func mustKey(t *testing.T, s string) media.Key {
	t.Helper()
	k, err := media.ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", s, err)
	}
	return k
}

func mustTag(t testing.TB, s string) language.Tag {
	t.Helper()
	tag, ok := language.Parse(s)
	if !ok || tag == (language.Tag{}) {
		t.Fatalf("Parse(%q) names no language", s)
	}
	return tag
}

func TestTimeComponentOrder(t *testing.T) {
	at := func(s string) time.Time {
		v, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			t.Fatalf("time.Parse(%q): %v", s, err)
		}
		return v
	}
	pairs := []struct {
		name          string
		before, after time.Time
	}{
		{"half a second before noon", at("2026-11-05T11:59:59.5Z"), at("2026-11-05T12:00:00Z")},
		{"a tenth after noon", at("2026-11-05T12:00:00Z"), at("2026-11-05T12:00:00.1Z")},
		{"across the epoch", at("1969-12-31T23:59:59Z"), time.Unix(0, 0)},
		{"UnixNano's extremes", time.Unix(0, math.MinInt64), time.Unix(0, math.MaxInt64)},
	}
	for _, p := range pairs {
		t.Run(p.name, func(t *testing.T) {
			a, b := appendTime(nil, p.before), appendTime(nil, p.after)
			if bytes.Compare(a, b) >= 0 {
				t.Errorf("encoded %s = %x is not below %s = %x", p.before, a, p.after, b)
			}
		})
	}
}

func TestOwnerOrder(t *testing.T) {
	owners := []int64{math.MinInt64, status.BypassOwner, status.SystemOwner, 1, math.MaxInt64}
	for i := 1; i < len(owners); i++ {
		a, b := ownerBytes(owners[i-1]), ownerBytes(owners[i])
		if bytes.Compare(a, b) >= 0 {
			t.Errorf("owner %d = %x is not below owner %d = %x", owners[i-1], a, owners[i], b)
		}
	}
}

func TestComponentsExactPrefix(t *testing.T) {
	t.Run("a text component never prefixes a longer one", func(t *testing.T) {
		s1, s10 := appendText(nil, "s1"), appendText(nil, "s10")
		if bytes.HasPrefix(s10, s1) {
			t.Errorf("s10 = %x starts with s1 = %x", s10, s1)
		}
	})
	t.Run("a media key never prefixes a longer id's", func(t *testing.T) {
		a := mediaKeys.AppendKey(nil, mustKey(t, "tvdb-12-s01e01"))
		b := mediaKeys.AppendKey(nil, mustKey(t, "tvdb-120-s01e01"))
		root := mustKey(t, "tvdb-12-s01e01").Root().Bytes()
		if bytes.HasPrefix(b, root) || bytes.HasPrefix(b, a) {
			t.Errorf("tvdb-120's key %x starts with tvdb-12's root %x", b, root)
		}
	})
	t.Run("a root ref's last component is length-prefixed", func(t *testing.T) {
		ref := func(id string) []byte {
			return rootRefKeys.AppendKey(nil, rootRef{Source: "sonarr", Root: mediasource.NativeRef{ID: mediasource.NativeID(id), Kind: media.Episode}})
		}
		if bytes.HasPrefix(ref("10"), ref("1")) {
			t.Errorf("root 10 = %x starts with root 1 = %x", ref("10"), ref("1"))
		}
	})
}

func TestKeyCodecsRoundTrip(t *testing.T) {
	k := mustKey(t, "tvdb-12-s01e02")
	en := mustTag(t, "en")
	ref := rootRef{Source: "sonarr-4k", Root: mediasource.NativeRef{ID: "77", Kind: media.Episode}}
	t.Run("lock", func(t *testing.T) {
		want := lockKey{Key: k, Tag: en, Variant: language.HI}
		if got, err := lockKeys.ParseKey(lockKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("ordinal", func(t *testing.T) {
		want := OrdinalKey{Inst: 9, Tag: en, Flags: language.Flags{HI: true, Forced: true}}
		if got, err := ordinalKeys.ParseKey(ordinalKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("no_results", func(t *testing.T) {
		want := noResultKey{Key: k, Tag: en, Provider: "subdl"}
		if got, err := noResultKeys.ParseKey(noResultKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("item ref with an empty item", func(t *testing.T) {
		want := itemRef{rootRef: ref}
		if got, err := itemRefKeys.ParseKey(itemRefKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("file", func(t *testing.T) {
		want := fileKey{Inst: 3, Name: "Show.S01E02.en.srt"}
		if got, err := fileKeys.ParseKey(fileKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %+v, %v; want %+v", got, err, want)
		}
	})
	t.Run("job", func(t *testing.T) {
		want := status.JobID("j-1")
		if got, err := jobKeys.ParseKey(jobKeys.AppendKey(nil, want)); err != nil || got != want {
			t.Errorf("round trip = %q, %v; want %q", got, err, want)
		}
	})
	t.Run("constant", func(t *testing.T) {
		if _, err := constKeys.ParseKey(constKeys.AppendKey(nil, constKey{})); err != nil {
			t.Errorf("round trip: %v", err)
		}
		if _, err := constKeys.ParseKey([]byte{1}); !errors.Is(err, bolttable.ErrCorrupt) {
			t.Errorf("ParseKey(01) = %v; want ErrCorrupt", err)
		}
	})
}

func TestTagKeyMustBeCanonical(t *testing.T) {
	k := mustKey(t, "tmdb-603")
	withTag := func(text string) map[string][]byte {
		return map[string][]byte{
			"lock":       append(appendText(append([]byte(nil), k.Bytes()...), text), byte(language.Standard)),
			"ordinal":    appendFlags(appendText(appendUint64(nil, 1), text), language.Flags{}),
			"no_results": appendText(appendText(append([]byte(nil), k.Bytes()...), text), "subdl"),
		}
	}
	parse := map[string]func([]byte) error{
		"lock":       func(b []byte) error { _, err := lockKeys.ParseKey(b); return err },
		"ordinal":    func(b []byte) error { _, err := ordinalKeys.ParseKey(b); return err },
		"no_results": func(b []byte) error { _, err := noResultKeys.ParseKey(b); return err },
	}
	for _, text := range []string{"mo", "ro-md", "xx-notalanguage-zz"} {
		for name, b := range withTag(text) {
			t.Run(name+" "+text, func(t *testing.T) {
				if err := parse[name](b); !errors.Is(err, bolttable.ErrCorrupt) {
					t.Errorf("ParseKey with tag bytes %q = %v; want ErrCorrupt", text, err)
				}
			})
		}
	}
	for name, b := range withTag("ro-MD") {
		t.Run(name+" canonical", func(t *testing.T) {
			if err := parse[name](b); err != nil {
				t.Errorf("ParseKey with canonical ro-MD: %v", err)
			}
		})
	}
}

func TestEnumOutOfSetIsCorrupt(t *testing.T) {
	k := mustKey(t, "tmdb-603")
	en := appendTag(nil, mustTag(t, "en"))
	lock := func(v byte) []byte { return append(append(append([]byte(nil), k.Bytes()...), en...), v) }
	ordinal := func(f byte) []byte { return append(append(appendUint64(nil, 1), en...), f) }
	ref := func(kind byte) []byte { return appendText(append(appendText(nil, "sonarr"), kind), "1") }
	cases := []struct {
		name  string
		parse func() error
	}{
		{"variant 0", func() error { _, err := lockKeys.ParseKey(lock(0)); return err }},
		{"variant 4", func() error { _, err := lockKeys.ParseKey(lock(4)); return err }},
		{"flags 4", func() error { _, err := ordinalKeys.ParseKey(ordinal(4)); return err }},
		{"root kind 0", func() error { _, err := rootRefKeys.ParseKey(ref(0)); return err }},
		{"root kind 3", func() error { _, err := rootRefKeys.ParseKey(ref(3)); return err }},
		{"instance 0", func() error { _, err := instKeys.ParseKey(appendUint64(nil, 0)); return err }},
		{"bad source id", func() error { _, err := sourceKeys.ParseKey(appendText(nil, "Sonarr")); return err }},
		{"trailing byte", func() error { _, err := instKeys.ParseKey(append(appendUint64(nil, 1), 0)); return err }},
		{"short length prefix", func() error { _, err := textKeys.ParseKey([]byte{5, 'a'}); return err }},
		{"movie with an episode", func() error {
			_, err := mediaKeys.ParseKey(append(k.Root().Bytes(), 0, 1, 0, 2))
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.parse(); !errors.Is(err, bolttable.ErrCorrupt) {
				t.Errorf("ParseKey = %v; want ErrCorrupt", err)
			}
		})
	}
}
