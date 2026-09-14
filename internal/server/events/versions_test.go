package events

import (
	"errors"
	"maps"
	"testing"

	"github.com/cplieger/sse"
)

func TestVersions_current_is_zero_until_bumped_and_counts_per_key(t *testing.T) {
	t.Parallel()
	v := newVersions("0123456789abcdef")
	if got := v.Current(SubjectActivity, ""); got != "0" {
		t.Errorf("Current(activity) before any bump = %q, want %q", got, "0")
	}
	v.Bump(SubjectActivity, "")
	v.Bump(SubjectActivity, "")
	v.Bump(SubjectDetail, "tvdb-1")
	if got := v.Current(SubjectActivity, ""); got != "2" {
		t.Errorf("Current(activity) after two bumps = %q, want %q", got, "2")
	}
	if got := v.Current(SubjectDetail, "tvdb-1"); got != "1" {
		t.Errorf("Current(detail, tvdb-1) = %q, want %q", got, "1")
	}
	if got := v.Current(SubjectDetail, "tvdb-2"); got != "0" {
		t.Errorf("Current(detail, tvdb-2) = %q, want %q (keys are independent)", got, "0")
	}
}

func TestVersions_resolve_answers_one_state_per_held_key_in_order(t *testing.T) {
	t.Parallel()
	v := newVersions("0123456789abcdef")
	v.Bump(SubjectSeries, "")
	v.Bump(SubjectDetail, "tmdb-9")
	v.Bump(SubjectDetail, "tmdb-9")
	held := []sse.Held{
		{Kind: SubjectDetail, Ref: "tmdb-9", Version: "1"},
		{Kind: SubjectSeries, Version: "1"},
		{Kind: SubjectJobs, Version: "0"},
	}
	got, err := v.Resolve(t.Context(), held)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	want := []sse.State{
		{Kind: SubjectDetail, Ref: "tmdb-9", Version: "2", Status: sse.StatusCurrent},
		{Kind: SubjectSeries, Version: "1", Status: sse.StatusCurrent},
		{Kind: SubjectJobs, Version: "0", Status: sse.StatusCurrent},
	}
	if len(got) != len(want) {
		t.Fatalf("Resolve() = %d states, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Resolve()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVersions_resolve_refuses_keys_outside_the_registry(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		kind string
		ref  string
	}{
		{name: "unknown_kind", kind: "library", ref: ""},
		{name: "detail_without_ref", kind: SubjectDetail, ref: ""},
		{name: "detail_episode_id", kind: SubjectDetail, ref: "tvdb-5-s01e02"},
		{name: "detail_leading_zero", kind: SubjectDetail, ref: "tvdb-05"},
		{name: "detail_imdb", kind: SubjectDetail, ref: "tt0903747"},
		{name: "ref_on_refless_kind", kind: SubjectActivity, ref: "x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newVersions("0123456789abcdef")
			_, err := v.Resolve(t.Context(), []sse.Held{{Kind: tc.kind, Ref: tc.ref, Version: "0"}})
			if !errors.Is(err, ErrUnknownSubject) {
				t.Errorf("Resolve(%s, %q) error = %v, want ErrUnknownSubject", tc.kind, tc.ref, err)
			}
		})
	}
}

// TestPublish_mints_the_subjects_an_event_moved pins the one mint site for
// the seven event-fed kinds: which keys each event type bumps, and that
// nothing else moves.
func TestPublish_mints_the_subjects_an_event_moved(t *testing.T) {
	t.Parallel()
	all := func(v *Versions) map[string]string {
		return map[string]string{
			"series":        v.Current(SubjectSeries, ""),
			"movies":        v.Current(SubjectMovies, ""),
			"detail:tvdb-5": v.Current(SubjectDetail, "tvdb-5"),
			"detail:tmdb-9": v.Current(SubjectDetail, "tmdb-9"),
			"history":       v.Current(SubjectHistory, ""),
			"activity":      v.Current(SubjectActivity, ""),
			"alerts":        v.Current(SubjectAlerts, ""),
			"providers":     v.Current(SubjectProviders, ""),
			"jobs":          v.Current(SubjectJobs, ""),
		}
	}
	zero := all(newVersions("x"))
	cases := []struct {
		name   string
		event  Event
		bumped []string
	}{
		{
			name:   "episode_coverage",
			event:  Event{Type: CoverageUpdate, Data: CoverageEvent{MediaType: "episode", MediaID: "tvdb-5-s01e02"}},
			bumped: []string{"series", "detail:tvdb-5", "history"},
		},
		{
			name:   "movie_coverage",
			event:  Event{Type: CoverageUpdate, Data: CoverageEvent{MediaType: "movie", MediaID: "tmdb-9"}},
			bumped: []string{"movies", "detail:tmdb-9", "history"},
		},
		{
			name:   "malformed_media_id_bumps_no_detail",
			event:  Event{Type: CoverageUpdate, Data: CoverageEvent{MediaType: "episode", MediaID: "tt0903747-s01e01"}},
			bumped: []string{"series", "history"},
		},
		{
			name:   "type_mismatch_bumps_no_detail",
			event:  Event{Type: CoverageUpdate, Data: CoverageEvent{MediaType: "movie", MediaID: "tvdb-5-s01e02"}},
			bumped: []string{"movies", "history"},
		},
		{
			name:   "activity",
			event:  Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityUpsert}},
			bumped: []string{"activity"},
		},
		{
			name:   "alert",
			event:  Event{Type: AlertDelta, Data: AlertEvent{Op: AlertRaise}},
			bumped: []string{"alerts"},
		},
		{
			name:   "provider",
			event:  Event{Type: ProviderDelta, Data: ProviderEvent{Op: ProviderRaise}},
			bumped: []string{"providers"},
		},
		{
			name:  "notify_mints_nothing",
			event: Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "x"}},
		},
		{
			name:  "sync_done_mints_nothing",
			event: Event{Type: SyncDone, Data: SyncDoneEvent{JobID: 1}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			bus := New(0, nil)
			bus.Publish(tc.event)
			want := map[string]string{}
			maps.Copy(want, zero)
			for _, k := range tc.bumped {
				want[k] = "1"
			}
			got := all(bus.Versions())
			for k := range want {
				if got[k] != want[k] {
					t.Errorf("Publish(%s): version[%s] = %q, want %q", tc.event.Type, k, got[k], want[k])
				}
			}
		})
	}
}
