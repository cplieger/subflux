package opensubtitles

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
)

func TestFactory_requires_credentials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		settings map[string]any
		name     string
		wantErr  bool
	}{
		{name: "nil settings", settings: nil, wantErr: true},
		{name: "missing all", settings: map[string]any{}, wantErr: true},
		{name: "missing password", settings: map[string]any{
			"username": "user", "api_key": "key",
		}, wantErr: true},
		{name: "missing username", settings: map[string]any{
			"password": "pass", "api_key": "key",
		}, wantErr: true},
		{name: "missing api_key", settings: map[string]any{
			"username": "user", "password": "pass",
		}, wantErr: true},
		{name: "empty username", settings: map[string]any{
			"username": "", "password": "pass", "api_key": "key",
		}, wantErr: true},
		{name: "empty password", settings: map[string]any{
			"username": "user", "password": "", "api_key": "key",
		}, wantErr: true},
		{name: "empty api_key", settings: map[string]any{
			"username": "user", "password": "pass", "api_key": "",
		}, wantErr: true},
		{name: "valid credentials", settings: map[string]any{
			"username": "user", "password": "pass", "api_key": "key",
		}, wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := Factory(context.Background(), tt.settings)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Factory() expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("Factory() unexpected error: %v", err)
			}
			if p.Name() != api.ProviderNameOpenSubtitles {
				t.Errorf("Name() = %q, want %q", p.Name(), api.ProviderNameOpenSubtitles)
			}
		})
	}
}

func TestFactory_options(t *testing.T) {
	t.Parallel()

	validCreds := map[string]any{
		"username": "user", "password": "pass", "api_key": "key",
	}

	// merge returns validCreds with extra key-value pairs applied.
	merge := func(extra map[string]any) map[string]any {
		m := make(map[string]any, len(validCreds)+len(extra))
		maps.Copy(m, validCreds)
		maps.Copy(m, extra)
		return m
	}

	tests := []struct {
		extra    map[string]any
		name     string
		wantHash bool
		wantAI   bool
	}{
		// P14: use_hash's product default (true) lives ONLY in the
		// providerEntries schema declaration; the registry normalizes it in
		// before the factory runs. A bare map here bypasses normalization,
		// so absent means the accessor zero (false) — the factory must NOT
		// re-encode the default (the dual-encoding drift this prevents).
		{name: "defaults (bare map, no normalization)", extra: nil, wantHash: false, wantAI: false},
		{name: "use_hash explicit true", extra: map[string]any{"use_hash": true}, wantHash: true, wantAI: false},
		{name: "use_hash false", extra: map[string]any{"use_hash": false}, wantHash: false, wantAI: false},
		{name: "include_ai_translated true", extra: map[string]any{"include_ai_translated": true}, wantHash: false, wantAI: true},
		{name: "include_ai_translated false", extra: map[string]any{"include_ai_translated": false}, wantHash: false, wantAI: false},
		{name: "both overridden", extra: map[string]any{
			"use_hash": false, "include_ai_translated": true,
		}, wantHash: false, wantAI: true},
		{name: "string true accepted", extra: map[string]any{"use_hash": "true"}, wantHash: true, wantAI: false},
		{name: "string false accepted", extra: map[string]any{"use_hash": "false"}, wantHash: false, wantAI: false},
		// The registry path: a schema-normalized map carries the declared
		// default. The field literal here mirrors the declaration's Type and
		// Default (the real declaration is asserted by the main package's
		// registry tests; this pins that normalization reaches the factory).
		{name: "normalized map carries declared default", extra: provider.NormalizeSettings(
			[]api.ProviderSchemaField{{Key: "use_hash", Type: "bool", Default: "true"}},
			nil), wantHash: true, wantAI: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p, err := Factory(context.Background(), merge(tt.extra))
			if err != nil {
				t.Fatalf("Factory() unexpected error: %v", err)
			}
			prov := p.(*Provider)
			if prov.useHash != tt.wantHash {
				t.Errorf("useHash = %v, want %v", prov.useHash, tt.wantHash)
			}
			if prov.includeAI != tt.wantAI {
				t.Errorf("includeAI = %v, want %v", prov.includeAI, tt.wantAI)
			}
		})
	}
}

func TestCountShowSubtitles_short_circuits_on_empty_imdb(t *testing.T) {
	t.Parallel()
	// Empty-after-sanitize inputs must return (0, nil) without any HTTP
	// setup. Using a zero-value Provider (no client, no token) proves the
	// short-circuit happens before ensureToken/doGet.
	p := &Provider{}
	for _, imdb := range []string{"tt0", "tt00000", "0000", "tt"} {
		count, err := p.CountShowSubtitles(context.Background(), imdb, "en")
		if err != nil {
			t.Errorf("CountShowSubtitles(%q) error = %v, want nil", imdb, err)
		}
		if count != 0 {
			t.Errorf("CountShowSubtitles(%q) = %d, want 0", imdb, count)
		}
	}
}

func TestMergeNumberingResults(t *testing.T) {
	t.Parallel()
	errA := errors.New("scheme A failed")
	errB := errors.New("scheme B failed")
	subs := func(ids ...string) []api.Subtitle {
		out := make([]api.Subtitle, len(ids))
		for i, id := range ids {
			out[i] = api.Subtitle{ID: id}
		}
		return out
	}

	tests := []struct {
		wantErr   error
		name      string
		perScheme []numberingResult
		wantIDs   []string
	}{
		{
			name: "dedups by ID across schemes, first occurrence wins",
			perScheme: []numberingResult{
				{results: subs("1", "2")},
				{results: subs("2", "3")},
			},
			wantIDs: []string{"1", "2", "3"},
		},
		{
			name: "skips errored scheme and merges the rest, returning last error",
			perScheme: []numberingResult{
				{err: errA},
				{results: subs("5")},
			},
			wantIDs: []string{"5"},
			wantErr: errA,
		},
		{
			name: "all schemes errored yields no results and the last error",
			perScheme: []numberingResult{
				{err: errA},
				{err: errB},
			},
			wantErr: errB,
		},
		{
			name:      "no schemes yields no results and no error",
			perScheme: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			merged, err := mergeNumberingResults(tt.perScheme)
			gotIDs := make([]string, len(merged))
			for i := range merged {
				gotIDs[i] = merged[i].ID
			}
			if !slices.Equal(gotIDs, tt.wantIDs) {
				t.Errorf("merged IDs = %v, want %v", gotIDs, tt.wantIDs)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
