package coveragedb

import (
	"testing"

	"pgregory.net/rapid"
)

func TestDiffSubtitleFiles(t *testing.T) {
	t.Parallel()
	tests := []struct {
		have    map[subFileKey]subFileVal
		want    map[subFileKey]subFileVal
		name    string
		wantDel int
		wantIns int
		wantUpd int
	}{
		{name: "both_empty", have: nil, want: nil, wantDel: 0, wantIns: 0, wantUpd: 0},
		{name: "identical", have: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"srt"}}, want: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"srt"}}, wantDel: 0, wantIns: 0, wantUpd: 0},
		{name: "additions_only", have: nil, want: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"srt"}}, wantDel: 0, wantIns: 1, wantUpd: 0},
		{name: "deletions_only", have: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"srt"}}, want: nil, wantDel: 1, wantIns: 0, wantUpd: 0},
		{name: "updates_only", have: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"srt"}}, want: map[subFileKey]subFileVal{{"eng", "full", "os", "/a.srt"}: {"ass"}}, wantDel: 0, wantIns: 0, wantUpd: 1},
		{name: "mixed", have: map[subFileKey]subFileVal{
			{"eng", "full", "os", "/a.srt"}: {"srt"},
			{"fra", "full", "os", "/b.srt"}: {"srt"},
		}, want: map[subFileKey]subFileVal{
			{"eng", "full", "os", "/a.srt"}: {"ass"},
			{"deu", "full", "os", "/c.srt"}: {"srt"},
		}, wantDel: 1, wantIns: 1, wantUpd: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			have := tt.have
			if have == nil {
				have = map[subFileKey]subFileVal{}
			}
			want := tt.want
			if want == nil {
				want = map[subFileKey]subFileVal{}
			}
			del, ins, upd := diffSubtitleFiles(have, want)
			if len(del) != tt.wantDel {
				t.Errorf("deletions = %d, want %d", len(del), tt.wantDel)
			}
			if len(ins) != tt.wantIns {
				t.Errorf("insertions = %d, want %d", len(ins), tt.wantIns)
			}
			if len(upd) != tt.wantUpd {
				t.Errorf("updates = %d, want %d", len(upd), tt.wantUpd)
			}
		})
	}
}

func TestDiffSubtitleFiles_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		genKey := func() subFileKey {
			return subFileKey{
				lang:    rapid.StringMatching(`[a-z]{3}`).Draw(t, "lang"),
				variant: rapid.SampledFrom([]string{"full", "forced", "hi"}).Draw(t, "variant"),
				source:  rapid.SampledFrom([]string{"os", "addic7ed", "embedded"}).Draw(t, "source"),
				path:    "/" + rapid.StringMatching(`[a-z]{3,8}\.srt`).Draw(t, "path"),
			}
		}
		genVal := func() subFileVal {
			return subFileVal{codec: rapid.SampledFrom([]string{"srt", "ass", "vtt"}).Draw(t, "codec")}
		}

		haveSize := rapid.IntRange(0, 10).Draw(t, "haveSize")
		wantSize := rapid.IntRange(0, 10).Draw(t, "wantSize")
		have := make(map[subFileKey]subFileVal, haveSize)
		want := make(map[subFileKey]subFileVal, wantSize)
		for range haveSize {
			have[genKey()] = genVal()
		}
		for range wantSize {
			want[genKey()] = genVal()
		}

		del, ins, upd := diffSubtitleFiles(have, want)

		// Invariant 1: all insert keys are in want but not in have.
		for _, k := range ins {
			if _, ok := have[k]; ok {
				t.Fatalf("insert key %v exists in have", k)
			}
			if _, ok := want[k]; !ok {
				t.Fatalf("insert key %v not in want", k)
			}
		}
		// Invariant 2: all delete keys are in have but not in want.
		for _, k := range del {
			if _, ok := want[k]; ok {
				t.Fatalf("delete key %v exists in want", k)
			}
			if _, ok := have[k]; !ok {
				t.Fatalf("delete key %v not in have", k)
			}
		}
		// Invariant 3: all update keys are in both with different values.
		for _, k := range upd {
			hv, hOK := have[k]
			wv, wOK := want[k]
			if !hOK || !wOK {
				t.Fatalf("update key %v not in both maps", k)
			}
			if hv == wv {
				t.Fatalf("update key %v has same value in both maps", k)
			}
		}
		// Invariant 4: inserts ∩ deletes == ∅.
		insSet := make(map[subFileKey]bool, len(ins))
		for _, k := range ins {
			insSet[k] = true
		}
		for _, k := range del {
			if insSet[k] {
				t.Fatalf("key %v in both inserts and deletes", k)
			}
		}
		// Invariant 5: idempotent.
		del2, ins2, upd2 := diffSubtitleFiles(want, want)
		if len(del2)+len(ins2)+len(upd2) != 0 {
			t.Fatal("diffSubtitleFiles(want, want) should produce empty diff")
		}
	})
}
