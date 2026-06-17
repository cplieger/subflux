package scanning

// Unit subflux-u26: tests added to kill surviving gremlins mutation-testing
// mutants in items.go, scan_item.go, tracker.go and handler_http.go. All
// package-level identifiers are prefixed with gk_subflux_u26_ to avoid
// colliding with any sibling unit sharing this package. Internal test package
// so unexported helpers (extractSegment, showLevelSkip, shouldSkipShow) and the
// existing mockShowCounter are reachable.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// Kills items.go:20:25 (ARITHMETIC_BASE `+`->`-`) on
// `total := len(episodes) + len(movies)`.
//
// With one episode and one movie the original total is 2 (non-zero), so the
// function proceeds and returns both items. The `-` mutant computes 1-1==0 and
// hits the `if total == 0 { return nil }` short-circuit.
func Test_gk_subflux_u26_SortByTitle_total_is_sum(t *testing.T) {
	t.Parallel()
	ep := ScanItem{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 1}}
	mv := ScanItem{Movie: &api.Movie{Title: "Movie"}}

	got := SortByTitle([]ScanItem{ep}, []ScanItem{mv})

	if len(got) != 2 {
		t.Errorf("SortByTitle(1 episode, 1 movie) len = %d, want 2", len(got))
	}
}

// Kills items.go:51:21 (CONDITIONALS_NEGATION `!=`->`==`) on
// `if keys[a].season != keys[b].season`.
//
// Two same-title episodes with different seasons (1 and 2) and episode numbers
// chosen so season order and episode order disagree. The original sorts by
// season so the season-1 episode is first. The `==` mutant returns 0 for
// differing seasons and falls through to episode order, putting the season-2,
// episode-1 item first.
func Test_gk_subflux_u26_SortByTitle_orders_by_season(t *testing.T) {
	t.Parallel()
	s1 := ScanItem{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 9}}
	s2 := ScanItem{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 2, EpisodeNumber: 1}}

	got := SortByTitle([]ScanItem{s2, s1}, nil)

	if len(got) != 2 {
		t.Fatalf("SortByTitle len = %d, want 2", len(got))
	}
	if got[0].Ep.SeasonNumber != 1 {
		t.Errorf("SortByTitle[0].SeasonNumber = %d, want 1 (season ordering)", got[0].Ep.SeasonNumber)
	}
}

// Kills items.go:54:26 (ARITHMETIC_BASE `-`->`+`, INVERT_NEGATIVES) on
// `return keys[a].episode - keys[b].episode`.
//
// Two same-title, same-season episodes given in descending episode order. The
// original episode comparator yields ascending order [1, 3]. Any mutation of
// the `-` makes the comparator non-negative for these positive episode numbers,
// so the descending input order [3, 1] is preserved instead of reordered.
func Test_gk_subflux_u26_SortByTitle_orders_by_episode(t *testing.T) {
	t.Parallel()
	e3 := ScanItem{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 3}}
	e1 := ScanItem{Series: &api.Series{Title: "Show"}, Ep: &api.Episode{SeasonNumber: 1, EpisodeNumber: 1}}

	got := SortByTitle([]ScanItem{e3, e1}, nil)

	if len(got) != 2 {
		t.Fatalf("SortByTitle len = %d, want 2", len(got))
	}
	if got[0].Ep.EpisodeNumber != 1 || got[1].Ep.EpisodeNumber != 3 {
		t.Errorf("SortByTitle episode order = [%d, %d], want [1, 3]",
			got[0].Ep.EpisodeNumber, got[1].Ep.EpisodeNumber)
	}
}

// Kills items.go:77:16 (CONDITIONALS_NEGATION `!=`->`==`) on
// `if item.Movie != nil` in ScanItemTitle.
//
// A movie item (Series nil, Movie non-nil) returns the movie title under the
// original. The `== nil` mutant skips the branch (Movie is non-nil) and returns
// "".
func Test_gk_subflux_u26_ScanItemTitle_movie(t *testing.T) {
	t.Parallel()
	got := ScanItemTitle(ScanItem{Movie: &api.Movie{Title: "TheMovie"}})

	if got != "TheMovie" {
		t.Errorf("ScanItemTitle(movie) = %q, want %q", got, "TheMovie")
	}
}

// Kills scan_item.go:98:15 (CONDITIONALS_NEGATION `==`->`!=`) on
// `if len(alts) == 0` in ExtractAltTitles.
//
// With one non-empty alternative title the original proceeds and returns it.
// The `!=` mutant treats any non-empty slice as the empty case and returns nil
// immediately.
func Test_gk_subflux_u26_ExtractAltTitles_nonempty(t *testing.T) {
	t.Parallel()
	got := ExtractAltTitles([]api.AlternateTitle{{Title: "Alt"}}, "Main")

	if len(got) != 1 || got[0] != "Alt" {
		t.Errorf("ExtractAltTitles([Alt], Main) = %v, want [Alt]", got)
	}
}

// Kills handler_http.go:276:7 (CONDITIONALS_NEGATION `s == ""`->`s != ""`) and
// 276:18 (CONDITIONALS_NEGATION `s == path`->`s != path`) on the guard
// `if s == "" || s == path { return "" }` in extractSegment.
//
// A valid single-segment path trims to a non-empty segment that differs from
// the full path, so the original returns the segment. Either negation flips one
// disjunct true for this input and returns "" instead.
func Test_gk_subflux_u26_extractSegment_valid(t *testing.T) {
	t.Parallel()
	got := extractSegment("/api/scan/series/123", "/api/scan/series/")

	if got != "123" {
		t.Errorf("extractSegment(valid) = %q, want %q", got, "123")
	}
}

// Kills tracker.go:99:16 (CONDITIONALS_BOUNDARY `<=`->`<`) on
// `skip := count <= threshold` in showLevelSkip.
//
// episodeCount=10 gives threshold = 10*20/100 = 2, and the counter reports
// exactly 2. The original `2 <= 2` skips the show (true); the `<` mutant makes
// `2 < 2` false.
func Test_gk_subflux_u26_showLevelSkip_count_equals_threshold(t *testing.T) {
	t.Parallel()
	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 2}}
	st := newSeasonTracker(mock, showskip.New(time.Hour))

	got := st.showLevelSkip(context.Background(), "tt1", 10, "en")

	if !got {
		t.Errorf("showLevelSkip(count==threshold) = false, want true")
	}
}

// gk_subflux_u26_warnSink is a slog source string the test asserts on.
const gk_subflux_u26_warnSink = "show skip check error"

// Kills tracker.go:73:26 (CONDITIONALS_NEGATION `err != nil`->`err == nil`) on
// `if err := g.Wait(); err != nil` in shouldSkipShow's multi-language path.
//
// The per-language goroutines never return an error, so g.Wait() is always nil.
// The original `nil != nil` is false and emits no warning. The `== nil` mutant
// makes `nil == nil` true and logs "show skip check error" on every multi-lang
// check. We capture the default slog output and assert the warning is absent.
func Test_gk_subflux_u26_shouldSkipShow_no_spurious_errgroup_warn(t *testing.T) {
	// No t.Parallel: this test swaps the global slog default logger.
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	mock := &mockShowCounter{counts: map[string]int{"tt1-en": 100, "tt1-fr": 100}}
	st := newSeasonTracker(mock, showskip.New(time.Hour))

	// Two languages forces the errgroup branch that reaches the g.Wait() guard.
	st.shouldSkipShow(context.Background(), "tt1", 10, []string{"en", "fr"})

	if strings.Contains(buf.String(), gk_subflux_u26_warnSink) {
		t.Errorf("shouldSkipShow emitted a spurious errgroup warning %q; log was:\n%s",
			gk_subflux_u26_warnSink, buf.String())
	}
}
