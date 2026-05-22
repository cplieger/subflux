package api

import (
	"slices"
	"testing"

	"pgregory.net/rapid"
)

func TestConfigDrift_Empty(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		drift ConfigDrift
		want  bool
	}{
		{"zero value is empty", ConfigDrift{}, true},
		{"removed languages not empty", ConfigDrift{RemovedLanguages: []string{"fr"}}, false},
		{"removed providers not empty", ConfigDrift{RemovedProviders: []ProviderID{"opensubtitles"}}, false},
		{"adaptive disabled not empty", ConfigDrift{AdaptiveDisabled: true}, false},
		{"all fields set not empty", ConfigDrift{RemovedLanguages: []string{"fr"}, RemovedProviders: []ProviderID{"os"}, AdaptiveDisabled: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tt.drift.Empty()
			if got != tt.want {
				t.Errorf("ConfigDrift.Empty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDetectDrift consolidates all scenario-based DetectDrift tests into a
// single table-driven test.
func TestDetectDrift(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		oldLangs             []string
		newLangs             []string
		oldProvs             []ProviderID
		newProvs             []ProviderID
		oldAdaptive          bool
		newAdaptive          bool
		wantRemovedLangs     []string
		wantRemovedProvs     []ProviderID
		wantAdaptiveDisabled bool
		wantEmpty            bool
	}{
		{
			name:        "no changes",
			oldLangs:    []string{"en", "fr"},
			newLangs:    []string{"en", "fr"},
			oldProvs:    []ProviderID{"os", "bs"},
			newProvs:    []ProviderID{"os", "bs"},
			oldAdaptive: true,
			newAdaptive: true,
			wantEmpty:   true,
		},
		{
			name:             "removed language",
			oldLangs:         []string{"en", "fr", "de"},
			newLangs:         []string{"en", "fr"},
			oldProvs:         []ProviderID{"os"},
			newProvs:         []ProviderID{"os"},
			oldAdaptive:      true,
			newAdaptive:      true,
			wantRemovedLangs: []string{"de"},
		},
		{
			name:             "removed provider",
			oldLangs:         []string{"en"},
			newLangs:         []string{"en"},
			oldProvs:         []ProviderID{"os", "bs", "yify"},
			newProvs:         []ProviderID{"os"},
			oldAdaptive:      true,
			newAdaptive:      true,
			wantRemovedProvs: []ProviderID{"bs", "yify"},
		},
		{
			name:                 "adaptive disabled",
			oldLangs:             []string{"en"},
			newLangs:             []string{"en"},
			oldProvs:             []ProviderID{"os"},
			newProvs:             []ProviderID{"os"},
			oldAdaptive:          true,
			newAdaptive:          false,
			wantAdaptiveDisabled: true,
		},
		{
			name:        "adaptive enabled not flagged",
			oldLangs:    []string{"en"},
			newLangs:    []string{"en"},
			oldProvs:    []ProviderID{"os"},
			newProvs:    []ProviderID{"os"},
			oldAdaptive: false,
			newAdaptive: true,
			wantEmpty:   true,
		},
		{
			name:                 "all changes",
			oldLangs:             []string{"en", "fr"},
			newLangs:             []string{"en"},
			oldProvs:             []ProviderID{"os", "bs"},
			newProvs:             []ProviderID{"os"},
			oldAdaptive:          true,
			newAdaptive:          false,
			wantRemovedLangs:     []string{"fr"},
			wantRemovedProvs:     []ProviderID{"bs"},
			wantAdaptiveDisabled: true,
		},
		{
			name:        "empty old config",
			oldLangs:    nil,
			newLangs:    []string{"en"},
			oldProvs:    nil,
			newProvs:    []ProviderID{"os"},
			oldAdaptive: false,
			newAdaptive: true,
			wantEmpty:   true,
		},
		{
			name:             "empty new config",
			oldLangs:         []string{"en", "fr"},
			newLangs:         nil,
			oldProvs:         []ProviderID{"os"},
			newProvs:         nil,
			oldAdaptive:      false,
			newAdaptive:      false,
			wantRemovedLangs: []string{"en", "fr"},
			wantRemovedProvs: []ProviderID{"os"},
		},
		{
			name:      "both nil",
			wantEmpty: true,
		},
		{
			name:             "duplicate items in old",
			oldLangs:         []string{"en", "en", "fr"},
			newLangs:         []string{"en"},
			oldProvs:         []ProviderID{"os", "os"},
			newProvs:         []ProviderID{"bs"},
			wantRemovedLangs: []string{"fr"},
			wantRemovedProvs: []ProviderID{"os"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := DetectDrift(tt.oldLangs, tt.newLangs, tt.oldProvs, tt.newProvs, tt.oldAdaptive, tt.newAdaptive)

			if tt.wantEmpty {
				if !d.Empty() {
					t.Errorf("DetectDrift() should be empty, got %+v", d)
				}
				return
			}

			if tt.wantRemovedLangs != nil {
				gotLangs := slices.Sorted(slices.Values(d.RemovedLanguages))
				wantLangs := slices.Sorted(slices.Values(tt.wantRemovedLangs))
				if !slices.Equal(gotLangs, wantLangs) {
					t.Errorf("RemovedLanguages = %v, want %v", d.RemovedLanguages, tt.wantRemovedLangs)
				}
			} else if len(d.RemovedLanguages) != 0 {
				t.Errorf("RemovedLanguages = %v, want []", d.RemovedLanguages)
			}

			if tt.wantRemovedProvs != nil {
				gotProvs := slices.Sorted(slices.Values(d.RemovedProviders))
				wantProvs := slices.Sorted(slices.Values(tt.wantRemovedProvs))
				if !slices.Equal(gotProvs, wantProvs) {
					t.Errorf("RemovedProviders = %v, want %v", d.RemovedProviders, tt.wantRemovedProvs)
				}
			} else if len(d.RemovedProviders) != 0 {
				t.Errorf("RemovedProviders = %v, want []", d.RemovedProviders)
			}

			if d.AdaptiveDisabled != tt.wantAdaptiveDisabled {
				t.Errorf("AdaptiveDisabled = %v, want %v", d.AdaptiveDisabled, tt.wantAdaptiveDisabled)
			}
		})
	}
}

// --- DetectDrift PBT ---

func TestDetectDrift_order_independent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		oldLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 10).Draw(t, "oldLangs")
		newLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 10).Draw(t, "newLangs")
		oldProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]{3,8}`), func(s string) ProviderID { return ProviderID(s) }), 0, 10).Draw(t, "oldProvs")
		newProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]{3,8}`), func(s string) ProviderID { return ProviderID(s) }), 0, 10).Draw(t, "newProvs")
		oldAdaptive := rapid.Bool().Draw(t, "oldAdaptive")
		newAdaptive := rapid.Bool().Draw(t, "newAdaptive")

		d1 := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, oldAdaptive, newAdaptive)

		// Shuffle old slices - result should be the same (order independent).
		shuffledLangs := slices.Clone(oldLangs)
		slices.Reverse(shuffledLangs)
		shuffledProvs := slices.Clone(oldProvs)
		slices.Reverse(shuffledProvs)

		d2 := DetectDrift(shuffledLangs, newLangs, shuffledProvs, newProvs, oldAdaptive, newAdaptive)

		slices.Sort(d1.RemovedLanguages)
		slices.Sort(d2.RemovedLanguages)
		slices.Sort(d1.RemovedProviders)
		slices.Sort(d2.RemovedProviders)

		if !slices.Equal(d1.RemovedLanguages, d2.RemovedLanguages) {
			t.Fatalf("RemovedLanguages differ: %v vs %v", d1.RemovedLanguages, d2.RemovedLanguages)
		}
		if !slices.Equal(d1.RemovedProviders, d2.RemovedProviders) {
			t.Fatalf("RemovedProviders differ: %v vs %v", d1.RemovedProviders, d2.RemovedProviders)
		}
		if d1.AdaptiveDisabled != d2.AdaptiveDisabled {
			t.Fatalf("AdaptiveDisabled differ: %v vs %v", d1.AdaptiveDisabled, d2.AdaptiveDisabled)
		}
	})
}

func TestDetectDrift_removed_always_subset_of_old(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		oldLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 5).Draw(t, "old_langs")
		newLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 5).Draw(t, "new_langs")
		oldProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 5).Draw(t, "old_provs")
		newProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 5).Draw(t, "new_provs")
		d := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, rapid.Bool().Draw(t, "oa"), rapid.Bool().Draw(t, "na"))
		oldLangSet := make(map[string]bool, len(oldLangs))
		for _, l := range oldLangs {
			oldLangSet[l] = true
		}
		for _, removed := range d.RemovedLanguages {
			if !oldLangSet[removed] {
				t.Errorf("RemovedLanguages contains %q not in old %v", removed, oldLangs)
			}
		}
		oldProvSet := make(map[ProviderID]bool, len(oldProvs))
		for _, p := range oldProvs {
			oldProvSet[p] = true
		}
		for _, removed := range d.RemovedProviders {
			if !oldProvSet[removed] {
				t.Errorf("RemovedProviders contains %q not in old %v", removed, oldProvs)
			}
		}
	})
}

func TestDetectDrift_removed_never_in_new(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		oldLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 5).Draw(t, "old_langs")
		newLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 5).Draw(t, "new_langs")
		oldProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 5).Draw(t, "old_provs")
		newProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 5).Draw(t, "new_provs")
		d := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, false, false)
		newLangSet := make(map[string]bool, len(newLangs))
		for _, l := range newLangs {
			newLangSet[l] = true
		}
		for _, removed := range d.RemovedLanguages {
			if newLangSet[removed] {
				t.Errorf("RemovedLanguages contains %q still in new %v", removed, newLangs)
			}
		}
	})
}

func TestDetectDrift_adaptive_disabled_only_when_was_enabled(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		old := rapid.Bool().Draw(t, "old")
		cur := rapid.Bool().Draw(t, "new")
		d := DetectDrift(nil, nil, nil, nil, old, cur)
		if d.AdaptiveDisabled && !old {
			t.Errorf("AdaptiveDisabled=true but old was false")
		}
		if d.AdaptiveDisabled && cur {
			t.Errorf("AdaptiveDisabled=true but new is still enabled")
		}
	})
}

func TestDetectDrift_removed_never_contains_duplicates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		oldLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 8).Draw(t, "old_langs")
		newLangs := rapid.SliceOfN(rapid.StringMatching(`[a-z]{2,3}`), 0, 5).Draw(t, "new_langs")
		oldProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 8).Draw(t, "old_provs")
		newProvs := rapid.SliceOfN(rapid.Map(rapid.StringMatching(`[a-z]+`), func(s string) ProviderID { return ProviderID(s) }), 0, 5).Draw(t, "new_provs")
		d := DetectDrift(oldLangs, newLangs, oldProvs, newProvs, false, false)
		seen := make(map[string]bool)
		for _, lang := range d.RemovedLanguages {
			if seen[lang] {
				t.Errorf("RemovedLanguages contains duplicate %q", lang)
			}
			seen[lang] = true
		}
		seenProv := make(map[ProviderID]bool)
		for _, prov := range d.RemovedProviders {
			if seenProv[prov] {
				t.Errorf("RemovedProviders contains duplicate %q", prov)
			}
			seenProv[prov] = true
		}
	})
}
