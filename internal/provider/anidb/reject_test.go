package anidb

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// countingMapper answers every episode-API request with body and counts them.
func countingMapper(body string, calls *atomic.Int32) *Mapper {
	m := NewMapper("placeholder-client")
	m.client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     make(http.Header),
		}, nil
	})}
	return m
}

func TestEpisodeID_latches_a_refused_client_key(t *testing.T) {
	var logs bytes.Buffer
	testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
	var calls atomic.Int32
	m := countingMapper(`<error code="302">client version missing or invalid</error>`, &calls)

	_, err := m.episodeID(t.Context(), 1, 1)
	if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
		t.Fatalf("episodeID() = %v, want *subflux.AuthError", err)
	}
	_, err = m.episodeID(t.Context(), 2, 1)
	if err == nil || calls.Load() != 1 {
		t.Errorf("second episodeID() = %v after %d requests, want the latched error and 1 request", err, calls.Load())
	}
	if !m.banUntil.IsZero() {
		t.Errorf("banUntil = %v, want no ban for a refused key", m.banUntil)
	}
	if answered, got := m.ClientKeyVerdict(); !answered || got == nil || !strings.Contains(got.Error(), "client version missing or invalid") {
		t.Errorf("ClientKeyVerdict() = (%v, %v), want (true, the refusal)", answered, got)
	}
	if n := strings.Count(logs.String(), "anidb client key rejected"); n != 1 {
		t.Errorf("rejection logged %d times, want once:\n%s", n, logs.String())
	}
	if strings.Contains(logs.String(), "placeholder-client") {
		t.Errorf("log carries the client key:\n%s", logs.String())
	}
}

func TestEpisodeID_other_error_codes_still_back_off(t *testing.T) {
	var calls atomic.Int32
	m := countingMapper(`<error>Banned</error>`, &calls)
	if _, err := m.episodeID(t.Context(), 1, 1); err == nil {
		t.Fatal("episodeID() on a ban = nil, want an error")
	}
	if answered, refusal := m.ClientKeyVerdict(); answered || refusal != nil || m.banUntil.IsZero() {
		t.Errorf("after a ban: ClientKeyVerdict() = (%v, %v), banUntil = %v; want no answer and a ban", answered, refusal, m.banUntil)
	}
}

func TestEpisodeID_records_an_accepted_client_key(t *testing.T) {
	var calls atomic.Int32
	m := countingMapper(`<anime id="1"><episodes><episode id="77"><epno>1</epno></episode></episodes></anime>`, &calls)
	if answered, _ := m.ClientKeyVerdict(); answered {
		t.Fatal("Setup: ClientKeyVerdict() before any request reports an answer")
	}
	if id, err := m.episodeID(t.Context(), 1, 1); err != nil || id != 77 {
		t.Fatalf("episodeID() = (%d, %v), want (77, nil)", id, err)
	}
	if answered, refusal := m.ClientKeyVerdict(); !answered || refusal != nil {
		t.Errorf("ClientKeyVerdict() after an answered lookup = (%v, %v), want (true, nil)", answered, refusal)
	}
	m.ForgetClientKeyVerdict()
	if answered, refusal := m.ClientKeyVerdict(); answered || refusal != nil {
		t.Errorf("ClientKeyVerdict() after ForgetClientKeyVerdict = (%v, %v), want (false, nil)", answered, refusal)
	}
}

func TestResolve_a_refused_key_overrides_episode_ids_cached_while_it_worked(t *testing.T) {
	// Each request after the first waits out AniDB's request interval on the bubble's clock.
	synctest.Test(t, func(t *testing.T) {
		testsupport.SwapDefaultLogger(t, slog.New(slog.DiscardHandler))
		var calls atomic.Int32
		m := NewMapper("placeholder-client")
		m.client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls.Add(1)
			body := `<error code="302">client version missing or invalid</error>`
			if r.URL.Query().Get("aid") == "1" {
				body = `<anime id="1"><episodes><episode id="77"><epno>1</epno></episode></episodes></anime>`
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     make(http.Header),
			}, nil
		})}
		m.parsedList = &animeList{Animes: []animeEntry{
			{TVDBID: "100", DefaultTVDBSeason: "1", AniDBID: 1},
			{TVDBID: "200", DefaultTVDBSeason: "1", AniDBID: 2},
		}}
		m.mappingTime = time.Now()

		if got := m.Resolve(t.Context(), 100, 1, 1); got == nil || got.AniDBEpisodeID != 77 {
			t.Fatalf("Setup: Resolve(100, 1, 1) = %+v, want AniDBEpisodeID 77", got)
		}
		if got := m.Resolve(t.Context(), 200, 1, 1); got == nil || got.AniDBEpisodeID != 0 {
			t.Fatalf("Setup: Resolve(200, 1, 1) against a refusal = %+v, want AniDBEpisodeID 0", got)
		}

		got := m.Resolve(t.Context(), 100, 1, 1)
		if got == nil || got.AniDBEpisodeID != 0 {
			t.Errorf("Resolve(100, 1, 1) after the refusal = %+v, want AniDBEpisodeID 0 so the search runs by title", got)
		}
		if n := calls.Load(); n != 2 {
			t.Errorf("AniDB requests after the refusal = %d, want 2 (the refused key answers without a request)", n)
		}

		m.ForgetClientKeyVerdict()
		got = m.Resolve(t.Context(), 100, 1, 1)
		if got == nil || got.AniDBEpisodeID != 77 {
			t.Errorf("Resolve(100, 1, 1) after ForgetClientKeyVerdict = %+v, want AniDBEpisodeID 77", got)
		}
		if n := calls.Load(); n != 3 {
			t.Errorf("AniDB requests after ForgetClientKeyVerdict = %d, want 3 (the forgotten cache is asked again)", n)
		}
	})
}

func TestForgetClientKeyVerdict_makes_the_next_lookup_ask_again(t *testing.T) {
	// The second request waits out AniDB's request interval on the bubble's clock.
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		testsupport.SwapDefaultLogger(t, slog.New(slog.NewTextHandler(&logs, nil)))
		var calls atomic.Int32
		m := countingMapper(`<error code="302">client version missing or invalid</error>`, &calls)
		if _, err := m.episodeID(t.Context(), 1, 1); err == nil {
			t.Fatal("Setup: episodeID() = nil, want the refusal")
		}

		m.ForgetClientKeyVerdict()
		if answered, got := m.ClientKeyVerdict(); answered || got != nil {
			t.Errorf("ClientKeyVerdict() after ForgetClientKeyVerdict = (%v, %v), want (false, nil)", answered, got)
		}
		if _, err := m.episodeID(t.Context(), 2, 1); err == nil {
			t.Fatal("episodeID() against a refusing AniDB = nil, want the refusal")
		}
		if got := calls.Load(); got != 2 {
			t.Errorf("AniDB requests = %d, want 2 (the forgotten refusal is asked again)", got)
		}
		if _, refusal := m.ClientKeyVerdict(); refusal == nil {
			t.Error("ClientKeyVerdict() after a second refusal reports no refusal, want it latched again")
		}
		if n := strings.Count(logs.String(), "anidb client key rejected"); n != 2 {
			t.Errorf("rejection logged %d times, want once per refusal (2):\n%s", n, logs.String())
		}
	})
}
