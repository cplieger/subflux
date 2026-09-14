package events

import (
	"strconv"
	"testing"

	"github.com/cplieger/sse"
	"pgregory.net/rapid"
)

// TestVersions_resolve_reports_the_bump_count_per_key is the model check:
// over any sequence of bumps across the registry's keys, Resolve answers
// exactly the number of bumps each held key received.
func TestVersions_resolve_reports_the_bump_count_per_key(t *testing.T) {
	t.Parallel()
	keys := []sse.Subject{
		{Kind: SubjectSeries},
		{Kind: SubjectMovies},
		{Kind: SubjectHistory},
		{Kind: SubjectActivity},
		{Kind: SubjectAlerts},
		{Kind: SubjectProviders},
		{Kind: SubjectJobs},
		{Kind: SubjectDetail, Ref: "tvdb-1"},
		{Kind: SubjectDetail, Ref: "tvdb-22"},
		{Kind: SubjectDetail, Ref: "tmdb-3"},
	}
	rapid.Check(t, func(rt *rapid.T) {
		v := newVersions("0123456789abcdef")
		counts := make([]uint64, len(keys))
		n := rapid.IntRange(0, 200).Draw(rt, "bumps")
		for range n {
			i := rapid.IntRange(0, len(keys)-1).Draw(rt, "key")
			v.Bump(keys[i].Kind, keys[i].Ref)
			counts[i]++
		}
		held := make([]sse.Held, len(keys))
		for i, k := range keys {
			held[i] = sse.Held{Subject: k, Version: "0"}
		}
		states, err := v.Resolve(t.Context(), held)
		if err != nil {
			rt.Fatalf("Resolve() error = %v", err)
		}
		for i, st := range states {
			if st.Subject != keys[i] {
				rt.Fatalf("Resolve()[%d].Subject = %+v, want %+v (order preserved)", i, st.Subject, keys[i])
			}
			if want := strconv.FormatUint(counts[i], 10); st.Version != want {
				rt.Fatalf("Resolve()[%d] version = %q for %+v, want %q", i, st.Version, keys[i], want)
			}
		}
	})
}
