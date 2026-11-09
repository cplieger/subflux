package seriesorder

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestLanguageSegment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		meta     string
		policy   []string
		wantLang []string
	}{
		{name: "french metadata", meta: "fr", wantLang: []string{english, "fra"}},
		{name: "no language, empty policy", wantLang: []string{english}},
		{name: "regional targets", policy: []string{"fr-CA", "pt-BR", "zh-Hant"}, wantLang: []string{english, "fra", "por", "zho"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig()
			r.sky.record(t, firefly)
			var meta language.Tag
			if tc.meta != "" {
				meta = mustTag(t, tc.meta)
			}
			if len(tc.policy) > 0 {
				r.store.setPolicy(policyOf(t, tc.policy...))
			}
			q := query("sonarr", firefly, airedSeries, meta, listed(r.sky.get(firefly, english, airedOrder), true))
			r.mustJoin(t, q)
			var langs []string
			for _, s := range r.sky.take() {
				if s.order == airedOrder {
					langs = append(langs, s.lang)
				}
			}
			slices.Sort(langs)
			if !slices.Equal(langs, tc.wantLang) {
				t.Errorf("asked the aired order in %v, want %v", langs, tc.wantLang)
			}
		})
	}
	t.Run("a target with no code", func(t *testing.T) {
		if got := titleLanguages(language.Tag{}, []language.Tag{{}}); !slices.Equal(got, []string{english}) {
			t.Errorf("titleLanguages = %v, want only %s", got, english)
		}
	})
}

func TestTitleLanguagesFromPolicy(t *testing.T) {
	r := newRig()
	r.sky.record(t, firefly)
	r.sky.record(t, got)
	roots := []*mediasource.JoinQuery{
		query("sonarr", firefly, airedSeries, language.Tag{}, listed(r.sky.get(firefly, english, airedOrder), true)),
		query("sonarr", got, airedSeries, language.Tag{}, listed(r.sky.get(got, english, airedOrder), true)),
	}
	pass := func() {
		for _, q := range roots {
			r.mustJoin(t, q)
		}
	}
	pass()
	r.sky.take()
	r.store.setPolicy(policyOf(t, "de"))
	pass()
	if sent := r.sky.take(); len(sent) != 2 || sentIn(sent, "deu") != 2 || sent[0].tvdb == sent[1].tvdb {
		t.Errorf("a policy gaining de sent %v, want one deu request per root", sent)
	}
	pass()
	if sent := r.sky.take(); len(sent) != 0 {
		t.Errorf("the next pass sent %v", sent)
	}
}
