package language

import (
	"fmt"
	"testing"
)

func TestFlagsServes(t *testing.T) {
	variants := []Variant{Standard, HI, Forced, 0, Forced + 1}
	for f, want := range map[Flags][5]bool{
		{}:                       {true, false, false, false, false},
		{HI: true}:               {false, true, false, false, false},
		{Forced: true}:           {false, false, true, false, false},
		{HI: true, Forced: true}: {false, true, true, false, false},
	} {
		for i, v := range variants {
			t.Run(fmt.Sprintf("%+v serves %d", f, v), func(t *testing.T) {
				if got := f.Serves(v); got != want[i] {
					t.Errorf("%+v.Serves(%d) = %v; want %v", f, v, got, want[i])
				}
			})
		}
	}
}

func TestVariantFlags(t *testing.T) {
	for v, want := range map[Variant]Flags{
		Standard: {}, HI: {HI: true}, Forced: {Forced: true}, 0: {}, Forced + 1: {},
	} {
		t.Run(fmt.Sprint(v), func(t *testing.T) {
			if got := v.Flags(); got != want {
				t.Errorf("Variant(%d).Flags() = %+v; want %+v", v, got, want)
			}
			if v >= Standard && v <= Forced && !v.Flags().Serves(v) {
				t.Errorf("Variant(%d).Flags() does not serve it", v)
			}
		})
	}
}
