package api

import (
	"strings"
	"testing"
)

func FuzzDetectDrift(f *testing.F) {
	f.Add("en,fr", "en", "prov1,prov2", "prov1", true, false)
	f.Add("", "", "", "", false, false)
	f.Add("en", "en", "p1", "p1", true, true)
	f.Add("en,fr,de", "", "a,b", "", true, false)

	f.Fuzz(func(t *testing.T, oldLangsRaw, newLangsRaw, oldProvsRaw, newProvsRaw string, oldAdaptive, newAdaptive bool) {
		split := func(s string) []string {
			if s == "" {
				return nil
			}
			return strings.Split(s, ",")
		}
		splitProv := func(s string) []ProviderID {
			if s == "" {
				return nil
			}
			parts := strings.Split(s, ",")
			ids := make([]ProviderID, len(parts))
			for i, p := range parts {
				ids[i] = ProviderID(p)
			}
			return ids
		}

		oldLangs := split(oldLangsRaw)
		newLangs := split(newLangsRaw)
		oldProvs := splitProv(oldProvsRaw)
		newProvs := splitProv(newProvsRaw)

		d := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, oldAdaptive, newAdaptive)

		// AdaptiveDisabled must be true only when old=true && new=false
		wantDisabled := oldAdaptive && !newAdaptive
		if d.AdaptiveDisabled != wantDisabled {
			t.Errorf("AdaptiveDisabled = %v, want %v", d.AdaptiveDisabled, wantDisabled)
		}

		// RemovedLanguages must be subset of oldLangs and not in newLangs
		newSet := make(map[string]struct{})
		for _, l := range newLangs {
			newSet[l] = struct{}{}
		}
		for _, r := range d.RemovedLanguages {
			if _, ok := newSet[r]; ok {
				t.Errorf("RemovedLanguages contains %q which is in newLangs", r)
			}
		}

		// Empty consistency
		if d.Empty() && (len(d.RemovedLanguages) > 0 || len(d.RemovedProviders) > 0 || d.AdaptiveDisabled) {
			t.Error("Empty() returned true but drift is non-empty")
		}
	})
}
