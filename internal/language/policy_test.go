package language

import (
	"slices"
	"strings"
	"testing"
)

func tagsOf(ts []Target) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Tag.String()
	}
	return out
}

func tags(t *testing.T, raws ...string) []Tag {
	t.Helper()
	out := make([]Tag, len(raws))
	for i, r := range raws {
		out[i] = mustParse(t, r)
	}
	return out
}

const chainSection = `
languages:
  default:
    - tag: en
  rules:
    - audio: ja
      targets:
        - tag: en
          variants: [hi]
    - audio: es
      targets: []
    - audio: pt
      targets:
        - tag: pt
    - audio: pt-BR
      targets:
        - tag: pt-BR
    - audio: fr
      targets:
        - tag: fr
    - audio: fr-CA
      targets:
        - tag: fr-CA
    - audio: nb
      targets:
        - tag: nb
`

func TestTargetsForSkipRule(t *testing.T) {
	p := mustPolicy(t, chainSection)
	t.Run("on the original language", func(t *testing.T) {
		if got := p.TargetsFor(mustParse(t, "es"), tags(t, "ja")); len(got) != 0 {
			t.Errorf("TargetsFor = %v; want nothing", tagsOf(got))
		}
	})
	t.Run("on the second audio track", func(t *testing.T) {
		if got := p.TargetsFor(mustParse(t, "ko"), tags(t, "ko", "es", "ja")); len(got) != 0 {
			t.Errorf("TargetsFor = %v; want nothing", tagsOf(got))
		}
	})
}

func TestTargetsFor(t *testing.T) {
	p := mustPolicy(t, chainSection)
	for _, r := range []struct {
		name     string
		original string
		audio    []string
		want     []string
		variants []Variant
	}{
		{"original match", "ja", []string{"es"}, []string{"en"}, []Variant{HI}},
		{"audio fallback", "ko", []string{"ko", "ja"}, []string{"en"}, []Variant{HI}},
		{"defaults", "ko", []string{"ko"}, []string{"en"}, []Variant{Standard}},
		{"zero original and no audio", "", nil, []string{"en"}, []Variant{Standard}},
		{"zero audio tracks", "", []string{"", ""}, []string{"en"}, []Variant{Standard}},
		{"closest tier: pt-BR audio picks the pt-BR rule", "pt-BR", nil, []string{"pt-BR"}, []Variant{Standard}},
		{"closest tier: pt-PT audio", "pt-PT", nil, []string{"pt"}, []Variant{Standard}},
		{"tie by config order: fr-BE matches fr and fr-CA alike", "fr-BE", nil, []string{"fr"}, []Variant{Standard}},
		{"exact fr-CA wins over fr", "fr-CA", nil, []string{"fr-CA"}, []Variant{Standard}},
		{"cross-language tier is no audio match", "nn", []string{"nn"}, []string{"en"}, []Variant{Standard}},
	} {
		t.Run(r.name, func(t *testing.T) {
			var original Tag
			if r.original != "" {
				original = mustParse(t, r.original)
			}
			audio := make([]Tag, len(r.audio))
			for i, a := range r.audio {
				if a != "" {
					audio[i] = mustParse(t, a)
				}
			}
			got := p.TargetsFor(original, audio)
			if !slices.Equal(tagsOf(got), r.want) || len(got) != 1 || !slices.Equal(got[0].Variants, r.variants) {
				t.Errorf("TargetsFor = %v %v; want %v %v", tagsOf(got), got, r.want, r.variants)
			}
		})
	}
	t.Run("zero Policy", func(t *testing.T) {
		if got := (Policy{}).TargetsFor(mustParse(t, "en"), nil); len(got) != 0 {
			t.Errorf("TargetsFor = %v; want nothing", got)
		}
	})
}

func TestTargetsUnion(t *testing.T) {
	p := mustPolicy(t, `
languages:
  default:
    - tag: pt-BR
      variants: [forced]
    - tag: en
  rules:
    - audio: ja
      targets:
        - tag: en
          variants: [hi, standard]
        - tag: ja
    - audio: ko
      targets:
        - tag: pt-BR
          variants: [standard]
`)
	got := p.Targets()
	if want := []string{"pt-BR", "en", "ja"}; !slices.Equal(tagsOf(got), want) {
		t.Fatalf("Targets() = %v; want %v", tagsOf(got), want)
	}
	for i, want := range [][]Variant{{Standard, Forced}, {Standard, HI}, {Standard}} {
		if !slices.Equal(got[i].Variants, want) {
			t.Errorf("%s variants = %v; want %v", got[i].Tag, got[i].Variants, want)
		}
	}
	if len((Policy{}).Targets()) != 0 {
		t.Error("the zero Policy has targets")
	}
}

func TestTargetsForReturnsCopy(t *testing.T) {
	p := mustPolicy(t, validSection)
	for name, get := range map[string]func() []Target{
		"TargetsFor": func() []Target { return p.TargetsFor(Tag{}, nil) },
		"Targets":    p.Targets,
	} {
		t.Run(name, func(t *testing.T) {
			first := get()
			first[0].Tag = mustParse(t, "de")
			first[0].Variants[0] = HI
			again := get()
			if again[0].Tag.String() != "pt-BR" || again[0].Variants[0] != Standard {
				t.Errorf("a mutated answer changed a later one: %v", again[0])
			}
		})
	}
}

func TestDigest(t *testing.T) {
	base := mustPolicy(t, validSection).Digest()
	t.Run("equal sections", func(t *testing.T) {
		reformatted := strings.ReplaceAll(validSection, "[standard, forced]", "\n        - standard\n        - forced")
		if got := mustPolicy(t, reformatted).Digest(); got != base {
			t.Errorf("an equal section reformatted moved the digest")
		}
	})
	for name, change := range map[string][2]string{
		"switch":         {"close_varieties: true", "close_varieties: false"},
		"tag":            {"tag: pt-BR", "tag: pt-PT"},
		"variant":        {"[standard, forced]", "[standard, hi]"},
		"variant order":  {"[standard, forced]", "[forced, standard]"},
		"audio":          {"audio: ja", "audio: ko"},
		"rule targets":   {"targets: []", "targets: [{tag: es}]"},
		"default order":  {"    - tag: pt-BR\n      variants: [standard, forced]\n    - tag: en\n", "    - tag: en\n    - tag: pt-BR\n      variants: [standard, forced]\n"},
		"a target moved": {"      variants: [standard, forced]\n    - tag: en\n", "      variants: [standard, forced]\n"},
	} {
		t.Run(name, func(t *testing.T) {
			doc := strings.Replace(validSection, change[0], change[1], 1)
			if doc == validSection {
				t.Fatalf("change %q not applied", change[0])
			}
			if mustPolicy(t, doc).Digest() == base {
				t.Errorf("changing %q left the digest", change[0])
			}
		})
	}
	t.Run("zero Policy", func(t *testing.T) {
		if (Policy{}).Digest() != zeroDigest || zeroDigest == ([32]byte{}) || zeroDigest == base {
			t.Errorf("zero Policy digest %x", (Policy{}).Digest())
		}
	})
}
