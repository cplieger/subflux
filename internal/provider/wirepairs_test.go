package provider_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
)

func TestWirePairs(t *testing.T) {
	m := language.MustProviderMap(provider.WirePairs("English", "en", "Brazilian", "pt-BR"))
	if tag, ok := m.Tag("Brazilian"); !ok || tag.String() != "pt-BR" {
		t.Errorf("Brazilian = %v %v", tag, ok)
	}
	for _, bad := range [][]string{{"English"}, {"Nothing", "xx-not-a-tag-at-all"}, {"Empty", ""}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WirePairs(%q) did not panic", bad)
				}
			}()
			provider.WirePairs(bad...)
		}()
	}
}
