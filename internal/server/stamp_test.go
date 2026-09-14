package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/cplieger/subflux/internal/server/events"
)

func stampedGet(t *testing.T, h http.HandlerFunc, path string) (int, string) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Code, rec.Header().Get("Subject-Stamp")
}

func decodeStamp(t *testing.T, raw string) events.Stamp {
	t.Helper()
	var st events.Stamp
	if err := json.Unmarshal([]byte(raw), &st); err != nil {
		t.Fatalf("Subject-Stamp %q is not a stamp: %v", raw, err)
	}
	return st
}

func TestStamp_carries_the_subject_version_and_epoch(t *testing.T) {
	t.Parallel()
	s := &Server{events: events.New(0, nil)}
	for range 3 {
		s.events.Versions().Bump(events.SubjectActivity, "")
	}
	h := s.stamp(events.SubjectActivity, nil)(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	code, raw := stampedGet(t, h, "/api/activity")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	got := decodeStamp(t, raw)
	want := events.Stamp{Kind: events.SubjectActivity, Ref: "", Version: "3", Epoch: hubEpoch(s)}
	if got != want {
		t.Errorf("Subject-Stamp = %+v, want %+v", got, want)
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(got.Epoch) {
		t.Errorf("stamp epoch = %q, want 16 hex", got.Epoch)
	}
}

// The counter is read before the handler runs: a bump during the body must
// not move the stamp, or a payload assembled before the bump would be labelled
// with the version that follows it and the digest would answer unchanged for
// a stale read.
func TestStamp_reads_the_counter_before_the_handler(t *testing.T) {
	t.Parallel()
	s := &Server{events: events.New(0, nil)}
	h := s.stamp(events.SubjectJobs, nil)(func(w http.ResponseWriter, _ *http.Request) {
		s.events.Versions().Bump(events.SubjectJobs, "")
		w.WriteHeader(http.StatusOK)
	})

	_, raw := stampedGet(t, h, "/api/sync/jobs")
	if got := decodeStamp(t, raw); got.Version != "0" {
		t.Errorf("stamp version = %q after an in-handler bump, want the pre-bump \"0\"", got.Version)
	}
	if cur := s.events.Versions().Current(events.SubjectJobs, ""); cur != "1" {
		t.Errorf("counter after the request = %q, want 1 (the bump itself must have landed)", cur)
	}
}

func TestStamp_series_detail_ref_is_the_tvdb_root(t *testing.T) {
	t.Parallel()
	s := &Server{events: events.New(0, nil)}
	s.events.Versions().Bump(events.SubjectDetail, "tvdb-42")
	h := s.stamp(events.SubjectDetail, seriesDetailRef)(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, raw := stampedGet(t, h, "/api/coverage/series/42")
	got := decodeStamp(t, raw)
	want := events.Stamp{Kind: events.SubjectDetail, Ref: "tvdb-42", Version: "1", Epoch: hubEpoch(s)}
	if got != want {
		t.Errorf("Subject-Stamp for /42 = %+v, want %+v", got, want)
	}

	for _, path := range []string{"/api/coverage/series/abc", "/api/coverage/series/", "/api/coverage/series/042", "/api/coverage/series/42/x"} {
		if code, raw := stampedGet(t, h, path); raw != "" || code != http.StatusOK {
			t.Errorf("GET %s: Subject-Stamp = %q status %d, want no stamp and the handler still run", path, raw, code)
		}
	}
}

func TestStamp_movie_detail_ref_is_the_tmdb_root(t *testing.T) {
	t.Parallel()
	s := &Server{events: events.New(0, nil)}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/coverage/movies/{tmdbId}/subs", s.stamp(events.SubjectDetail, movieDetailRef)(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	_, raw := stampedGet(t, mux.ServeHTTP, "/api/coverage/movies/7/subs")
	got := decodeStamp(t, raw)
	want := events.Stamp{Kind: events.SubjectDetail, Ref: "tmdb-7", Version: "0", Epoch: hubEpoch(s)}
	if got != want {
		t.Errorf("Subject-Stamp for movie 7 = %+v, want %+v", got, want)
	}
	if _, raw := stampedGet(t, mux.ServeHTTP, "/api/coverage/movies/seven/subs"); raw != "" {
		t.Errorf("Subject-Stamp for a non-numeric tmdb id = %q, want none", raw)
	}
}

// hubEpoch reads the epoch the way production does, off a stamp.
func hubEpoch(s *Server) string {
	st, _ := s.events.Versions().Stamp(events.SubjectActivity, "")
	return st.Epoch
}
