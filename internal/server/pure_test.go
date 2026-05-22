package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"subflux/internal/api"
	"subflux/internal/config/schema"
	"subflux/internal/provider"

	"subflux/internal/server/activity"
	"subflux/internal/server/confighandlers"
	"subflux/internal/server/manualops"
	"subflux/internal/server/scanning"

	"pgregory.net/rapid"
)

// --- manualops.IsValidLangCode ---

func TestIsValidLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		// Valid codes.
		{"simple two char", "en", true},
		{"BCP 47 with region", "pt-BR", true},
		{"three char", "fra", true},
		{"exactly at max length", "en-US-x-custom12long", true},
		{"contains spaces", "en US", true},
		{"single char", "e", true},
		{"single dot is valid", "en.fr", true},

		// Invalid codes.
		{"empty string", "", false},
		{"one over max length", "abcdefghijklmnopqrstu", false},
		{"contains forward slash", "en/fr", false},
		{"contains backslash", "en\\fr", false},
		{"contains dot-dot traversal", "en..fr", false},
		{"contains null byte", "en\x00fr", false},
		{"contains newline", "en\nfr", false},
		{"contains tab", "en\tfr", false},
		{"contains carriage return", "en\rfr", false},
		{"only null byte", "\x00", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := manualops.IsValidLangCode(tt.input)
			if got != tt.want {
				t.Errorf("manualops.IsValidLangCode(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestIsValidLangCode_property_accepted_codes_are_path_safe(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		lang := rapid.StringMatching(`[a-zA-Z0-9\-]{1,20}`).Draw(t, "lang")
		if !manualops.IsValidLangCode(lang) {
			t.Skip("generated code rejected by manualops.IsValidLangCode")
		}
		if strings.ContainsAny(lang, "/\\") {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains path separator", lang)
		}
		if strings.Contains(lang, "..") {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains '..'", lang)
		}
		if len(lang) > manualops.MaxLangCodeLen {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but len=%d > max=%d", lang, len(lang), manualops.MaxLangCodeLen)
		}
	})
}

func TestIsValidLangCode_property_rejects_dangerous_inputs(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.String().Draw(t, "base")
		danger := rapid.SampledFrom([]string{"/", "\\", "..", "\x00", "\n", "\r"}).Draw(t, "danger")
		pos := rapid.IntRange(0, len(base)).Draw(t, "pos")
		input := base[:pos] + danger + base[pos:]
		if manualops.IsValidLangCode(input) {
			t.Errorf("manualops.IsValidLangCode(%q) = true, but contains dangerous sequence %q", input, danger)
		}
	})
}

// --- redactSecrets ---

func TestRedactSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"api_key redacted",
			"  api_key: my-secret-key",
			"  api_key: \"********\""},
		{"password redacted",
			"  password: hunter2",
			"  password: \"********\""},
		{"token redacted",
			"  token: abc123",
			"  token: \"********\""},
		{"secret redacted",
			"  secret: top-secret-value",
			"  secret: \"********\""},
		{"passkey redacted",
			"  passkey: secret123",
			"  passkey: \"********\""},
		{"non-secret preserved",
			"  url: http://example.com",
			"  url: http://example.com"},
		{"case insensitive match",
			"  API_KEY: my-key",
			"  API_KEY: \"********\""},
		{"multiline mixed",
			"  url: http://example.com\n  token: secret\n  port: 8080",
			"  url: http://example.com\n  token: \"********\"\n  port: 8080"},
		{"empty input", "", ""},
		{"partial key match not redacted",
			"  api_key_name: visible",
			"  api_key_name: visible"},
		{"empty double-quoted value not redacted",
			"  api_key: \"\"",
			"  api_key: \"\""},
		{"empty single-quoted value not redacted",
			"  api_key: ''",
			"  api_key: ''"},
		{"blank value not redacted",
			"  api_key: ",
			"  api_key: "},
		{"inline comment stripped before redaction",
			"  token: my-secret # this is a comment",
			"  token: \"********\""},
		{"client_key redacted",
			"  client_key: ck-12345",
			"  client_key: \"********\""},
		{"anidb_client_key redacted",
			"  anidb_client_key: anidb-key-123",
			"  anidb_client_key: \"********\""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(redactSecrets([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("redactSecrets(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- enabledProviders ---

func TestEnabledProviders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		providers map[api.ProviderID]api.ProviderCfg
		want      []api.ProviderID
	}{
		{"all enabled", map[api.ProviderID]api.ProviderCfg{
			"beta":  {Enabled: true},
			"alpha": {Enabled: true},
		}, []api.ProviderID{"alpha", "beta"}},
		{"mixed enabled and disabled", map[api.ProviderID]api.ProviderCfg{
			"os":   {Enabled: true},
			"yify": {Enabled: false},
			"bs":   {Enabled: true},
		}, []api.ProviderID{"bs", "os"}},
		{"none enabled", map[api.ProviderID]api.ProviderCfg{
			"os": {Enabled: false},
		}, nil},
		{"empty providers", map[api.ProviderID]api.ProviderCfg{}, nil},
		{"nil providers", nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mock := &epMock{providers: tt.providers}
			got := enabledProviders(mock)
			if !slices.Equal(got, tt.want) {
				t.Errorf("enabledProviders() = %v, want %v", got, tt.want)
			}
		})
	}
}

// epMock satisfies the interface{ ProviderConfigs() map[api.ProviderID]api.ProviderCfg }.
type epMock struct {
	providers map[api.ProviderID]api.ProviderCfg
}

func (m *epMock) ProviderConfigs() map[api.ProviderID]api.ProviderCfg { return m.providers }

// --- activity.AlertLog ---

func TestAlertLog_recordPersistent_deduplicates(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "First error")
	al.RecordPersistent("startup", "Updated error")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 1 {
		t.Fatalf("alerts count = %d, want 1 (deduplicated)", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[0].Message != "Updated error" {
		t.Errorf("alert message = %q, want %q",
			al.AlertsUnsafe()[0].Message, "Updated error")
	}
	if al.AlertsUnsafe()[0].Kind != activity.AlertPersistent {
		t.Errorf("alert kind = %q, want %q",
			al.AlertsUnsafe()[0].Kind, activity.AlertPersistent)
	}
}

func TestAlertLog_recordPersistent_different_sources_not_deduplicated(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")
	al.RecordPersistent("config", "Error B")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 2 {
		t.Fatalf("alerts count = %d, want 2", len(al.AlertsUnsafe()))
	}
}

func TestAlertLog_dismiss(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.Record("sonarr", "test error")

	al.RLock()
	id := al.AlertsUnsafe()[0].ID
	al.RUnlock()

	if !al.Dismiss(id) {
		t.Error("dismiss() returned false for existing alert")
	}

	al.RLock()
	defer al.RUnlock()

	if !al.AlertsUnsafe()[0].Dismissed {
		t.Error("alert should be dismissed after dismiss()")
	}
}

func TestAlertLog_dismiss_nonexistent_returns_false(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	if al.Dismiss(999) {
		t.Error("dismiss(999) should return false for nonexistent alert")
	}
}

func TestAlertLog_recordPersistent_dismissed_allows_new(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "First error")

	al.RLock()
	id := al.AlertsUnsafe()[0].ID
	al.RUnlock()

	al.Dismiss(id)

	// After dismissing, a new persistent alert with the same source
	// should create a new entry (not update the dismissed one).
	al.RecordPersistent("startup", "Second error")

	al.RLock()
	defer al.RUnlock()

	if len(al.AlertsUnsafe()) != 2 {
		t.Fatalf("alerts count = %d, want 2 (dismissed + new)", len(al.AlertsUnsafe()))
	}
	if al.AlertsUnsafe()[1].Message != "Second error" {
		t.Errorf("second alert message = %q, want %q",
			al.AlertsUnsafe()[1].Message, "Second error")
	}
}

// --- manualops.QueryInt ---

func TestQueryInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  string
		want int
	}{
		{"valid positive", "42", 42},
		{"zero", "0", 0},
		{"negative returns zero", "-1", 0},
		{"empty returns zero", "", 0},
		{"non-numeric returns zero", "abc", 0},
		{"float returns zero", "3.14", 0},
		{"large valid number", "2147483647", 2147483647},
		{"leading whitespace returns zero", " 42", 0},
		{"overflow returns zero", "99999999999999999999", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := &fakeQuery{val: tt.val}
			got := manualops.QueryInt(q, "key")
			if got != tt.want {
				t.Errorf("manualops.QueryInt(%q) = %d, want %d",
					tt.val, got, tt.want)
			}
		})
	}
}

// fakeQuery satisfies the interface{ Get(string) string } constraint
// used by manualops.QueryInt.
type fakeQuery struct {
	val string
}

func (f *fakeQuery) Get(_ string) string { return f.val }

func TestQueryInt_property_result_always_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		val := rapid.String().Draw(t, "val")
		q := &fakeQuery{val: val}
		got := manualops.QueryInt(q, "key")
		if got < 0 {
			t.Errorf("manualops.QueryInt(%q) = %d, want >= 0", val, got)
		}
	})
}

// --- handleDismissAlert ---

func TestHandleDismissAlert(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		query      string
		setupAlert bool
		wantCode   int
	}{
		{"missing id", "", false, http.StatusBadRequest},
		{"invalid id", "?id=abc", false, http.StatusBadRequest},
		{"zero id", "?id=0", false, http.StatusBadRequest},
		{"negative id", "?id=-1", false, http.StatusBadRequest},
		{"nonexistent id", "?id=999", false, http.StatusNotFound},
		{"success", "", true, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s := &Server{
				alerts: activity.NewAlertLog(10),
			}

			query := tt.query
			if tt.setupAlert {
				s.alerts.Record("test", "test error")

				s.alerts.RLock()
				id := s.alerts.AlertsUnsafe()[0].ID
				s.alerts.RUnlock()

				query = "?id=" + strconv.Itoa(id)
			}

			req := httptest.NewRequestWithContext(context.Background(),
				http.MethodDelete, "/api/alerts"+query, http.NoBody)
			w := httptest.NewRecorder()

			s.handleDismissAlert(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("handleDismissAlert(%s) status = %d, want %d",
					tt.name, w.Code, tt.wantCode)
			}
		})
	}
}

// --- activity.Log.progress ---

func TestActivityLog_progress_updates_entry(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "initial detail", "scheduled")
	al.Progress(id, 5, 20, "updated detail")

	al.RLock()
	defer al.RUnlock()

	if len(al.EntriesUnsafe()) != 1 {
		t.Fatalf("entries count = %d, want 1", len(al.EntriesUnsafe()))
	}
	e := al.EntriesUnsafe()[0]
	if e.Current != 5 {
		t.Errorf("entry.Current = %d, want 5", e.Current)
	}
	if e.Total != 20 {
		t.Errorf("entry.Total = %d, want 20", e.Total)
	}
	if e.Detail != "updated detail" {
		t.Errorf("entry.Detail = %q, want %q", e.Detail, "updated detail")
	}
}

func TestActivityLog_progress_empty_detail_preserves_existing(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	id := al.Start("Scan", "original detail", "scheduled")
	al.Progress(id, 3, 10, "")

	al.RLock()
	defer al.RUnlock()

	e := al.EntriesUnsafe()[0]
	if e.Detail != "original detail" {
		t.Errorf("entry.Detail = %q, want %q (empty detail should preserve original)",
			e.Detail, "original detail")
	}
	if e.Current != 3 {
		t.Errorf("entry.Current = %d, want 3", e.Current)
	}
}

func TestActivityLog_progress_nonexistent_id_is_noop(t *testing.T) {
	t.Parallel()
	al := activity.New(10)

	al.Start("Scan", "detail", "scheduled")
	al.Progress("nonexistent", 99, 100, "should not appear")

	al.RLock()
	defer al.RUnlock()

	if al.EntriesUnsafe()[0].Current != 0 {
		t.Errorf("entry.Current = %d, want 0 (nonexistent ID should not modify)",
			al.EntriesUnsafe()[0].Current)
	}
}

// --- sleepCtx ---

func TestSleepCtx_zero_duration_returns_immediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, 0)
	if err != nil {
		t.Errorf("sleepCtx(ctx, 0) = %v, want nil", err)
	}
}

func TestSleepCtx_negative_duration_returns_immediately(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, -1)
	if err != nil {
		t.Errorf("sleepCtx(ctx, -1) = %v, want nil", err)
	}
}

func TestSleepCtx_cancelled_context_returns_error(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	err := sleepCtx(ctx, 10*time.Second)
	if err == nil {
		t.Fatal("sleepCtx(cancelled, 10s) = nil, want context error")
	}
	if err != context.Canceled {
		t.Errorf("sleepCtx(cancelled, 10s) = %v, want %v", err, context.Canceled)
	}
}

func TestSleepCtx_short_duration_completes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	err := sleepCtx(ctx, 1*time.Millisecond)
	if err != nil {
		t.Errorf("sleepCtx(ctx, 1ms) = %v, want nil", err)
	}
}

// --- extractAltTitles ---

func TestExtractAltTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		alts    []api.AlternateTitle
		primary string
		want    []string
	}{
		{"nil alts", nil, "Breaking Bad", nil},
		{"empty alts", []api.AlternateTitle{}, "Breaking Bad", nil},
		{"excludes primary", []api.AlternateTitle{
			{Title: "Breaking Bad"},
		}, "Breaking Bad", nil},
		{"excludes primary case insensitive", []api.AlternateTitle{
			{Title: "breaking bad"},
		}, "Breaking Bad", nil},
		{"returns unique alts", []api.AlternateTitle{
			{Title: "Metástasis"},
			{Title: "Во все тяжкие"},
		}, "Breaking Bad", []string{"Metástasis", "Во все тяжкие"}},
		{"deduplicates case insensitive", []api.AlternateTitle{
			{Title: "Alt Title"},
			{Title: "alt title"},
			{Title: "ALT TITLE"},
		}, "Primary", []string{"Alt Title"}},
		{"skips empty titles", []api.AlternateTitle{
			{Title: ""},
			{Title: "Valid Alt"},
			{Title: ""},
		}, "Primary", []string{"Valid Alt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.ExtractAltTitles(tt.alts, tt.primary)
			if !slices.Equal(got, tt.want) {
				t.Errorf("ExtractAltTitles(%v, %q) = %v, want %v",
					tt.alts, tt.primary, got, tt.want)
			}
		})
	}
}

// --- sceneOrPath ---

func TestSceneOrPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sceneName string
		filePath  string
		want      string
	}{
		{"scene name present", "Movie.2024.BluRay-GRP", "/media/movie.mkv", "Movie.2024.BluRay-GRP"},
		{"scene name empty", "", "/media/movie.mkv", "/media/movie.mkv"},
		{"both empty", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.SceneOrPath(tt.sceneName, tt.filePath)
			if got != tt.want {
				t.Errorf("SceneOrPath(%q, %q) = %q, want %q",
					tt.sceneName, tt.filePath, got, tt.want)
			}
		})
	}
}

// --- secretContextKey ---

func TestSecretContextKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		key     string
		want    string
		lineIdx int
	}{
		{name: "top level key", yaml: "api_key: secret123", lineIdx: 0, key: "api_key", want: "api_key"},
		{name: "nested under one parent", yaml: "sonarr:\n  api_key: secret123", lineIdx: 1, key: "api_key", want: "sonarr.api_key"},
		{name: "deeply nested", yaml: "providers:\n  opensubtitles:\n    settings:\n      password: hunter2", lineIdx: 3, key: "password",
			want: "providers.opensubtitles.settings.password"},
		{name: "skips blank lines", yaml: "providers:\n\n  os:\n    api_key: abc", lineIdx: 3, key: "api_key",
			want: "providers.os.api_key"},
		{name: "skips comment lines", yaml: "providers:\n  # comment\n  os:\n    api_key: abc", lineIdx: 3, key: "api_key",
			want: "providers.os.api_key"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := splitYAMLLines(tt.yaml)
			got := secretContextKey(lines, tt.lineIdx, tt.key)
			if got != tt.want {
				t.Errorf("secretContextKey(..., %d, %q) = %q, want %q",
					tt.lineIdx, tt.key, got, tt.want)
			}
		})
	}
}

// splitYAMLLines is a test helper that splits a string into [][]byte lines.
func splitYAMLLines(s string) [][]byte {
	parts := strings.Split(s, "\n")
	lines := make([][]byte, len(parts))
	for i, p := range parts {
		lines[i] = []byte(p)
	}
	return lines
}

// --- extractSecretValues ---

func TestExtractSecretValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want map[string]string
		name string
		yaml string
	}{
		{name: "empty input", yaml: "", want: map[string]string{}},
		{name: "no secrets", yaml: "url: http://example.com\nport: 8080", want: map[string]string{}},
		{name: "simple api_key", yaml: "sonarr:\n  api_key: abc123", want: map[string]string{
			"sonarr.api_key": "abc123",
		}},
		{name: "multiple secrets", yaml: "sonarr:\n  api_key: key1\nradarr:\n  api_key: key2", want: map[string]string{
			"sonarr.api_key": "key1",
			"radarr.api_key": "key2",
		}},
		{name: "strips inline comment", yaml: "sonarr:\n  api_key: abc123 # my key", want: map[string]string{
			"sonarr.api_key": "abc123",
		}},
		{name: "skips empty values", yaml: "sonarr:\n  api_key: ", want: map[string]string{}},
		{name: "skips quoted empty", yaml: "sonarr:\n  api_key: \"\"", want: map[string]string{}},
		{name: "password key", yaml: "providers:\n  os:\n    password: hunter2", want: map[string]string{
			"providers.os.password": "hunter2",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSecretValues([]byte(tt.yaml))
			if len(got) != len(tt.want) {
				t.Fatalf("extractSecretValues() returned %d entries, want %d\ngot: %v",
					len(got), len(tt.want), got)
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("extractSecretValues() missing key %q", k)
				} else if gotV != wantV {
					t.Errorf("extractSecretValues()[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

// --- dismissBySource ---

func TestAlertLog_dismissBySource(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")
	al.RecordPersistent("config", "Error B")
	al.Record("startup", "Transient C") // transient, same source

	al.DismissBySource("startup")

	al.RLock()
	defer al.RUnlock()

	for _, a := range al.AlertsUnsafe() {
		if a.Source == "startup" && a.Kind == activity.AlertPersistent && !a.Dismissed {
			t.Error("persistent startup alert should be dismissed")
		}
		if a.Source == "config" && a.Dismissed {
			t.Error("config alert should not be dismissed")
		}
		// Transient alerts from the same source should not be dismissed.
		if a.Source == "startup" && a.Kind == activity.AlertTransient && a.Dismissed {
			t.Error("transient startup alert should not be dismissed by dismissBySource")
		}
	}
}

func TestAlertLog_dismissBySource_no_matching_source(t *testing.T) {
	t.Parallel()
	al := activity.NewAlertLog(10)

	al.RecordPersistent("startup", "Error A")

	// Should not panic or modify anything.
	al.DismissBySource("nonexistent")

	al.RLock()
	defer al.RUnlock()

	if al.AlertsUnsafe()[0].Dismissed {
		t.Error("alert should not be dismissed by non-matching source")
	}
}

// --- scanning.ScanItemSeasonEp ---

func TestScanItemSeasonEp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		item       scanning.ScanItem
		name       string
		wantSeason int
		wantEp     int
	}{
		{name: "episode", item: scanning.ScanItem{Ep: &api.Episode{SeasonNumber: 3, EpisodeNumber: 7}}, wantSeason: 3, wantEp: 7},
		{name: "movie returns zero", item: scanning.ScanItem{Movie: &api.Movie{Title: "Test"}}, wantSeason: 0, wantEp: 0},
		{name: "nil ep and movie", item: scanning.ScanItem{}, wantSeason: 0, wantEp: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, e := scanning.ScanItemSeasonEp(tt.item)
			if s != tt.wantSeason || e != tt.wantEp {
				t.Errorf("scanning.ScanItemSeasonEp() = (%d, %d), want (%d, %d)",
					s, e, tt.wantSeason, tt.wantEp)
			}
		})
	}
}

// --- requireConfigured middleware ---

func TestRequireConfigured_blocks_unconfigured(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{})
	// configured is false by default (zero value).

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := s.requireConfigured(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("requireConfigured(unconfigured) status = %d, want %d",
			rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRequireConfigured_passes_when_configured(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{})
	s.configured.Store(true)

	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	handler := s.requireConfigured(inner)

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/scan", http.NoBody)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("requireConfigured(configured) status = %d, want %d",
			rec.Code, http.StatusOK)
	}
	if !called {
		t.Error("inner handler was not called when configured")
	}
}

// --- handleResetConfig ---

func TestHandleResetConfig_rejects_when_configured(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.configured.Store(true)
	s.configH = confighandlers.New(&confighandlers.Deps{
		Configured: func() bool { return true },
		ConfigPath: func() string { return "" },
	})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleResetConfig(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("handleResetConfig(configured) status = %d, want %d",
			rec.Code, http.StatusConflict)
	}
}

func TestHandleResetConfig_no_default_config(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		Configured: func() bool { return false },
		ConfigPath: func() string { return "" },
	})
	// configured is false, defaultConfig is nil.

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleResetConfig(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("handleResetConfig(no default) status = %d, want %d",
			rec.Code, http.StatusInternalServerError)
	}
}

func TestHandleResetConfig_writes_default(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.yaml"
	cfgFilePath = configPath

	defaultCfg := []byte("# default config\nlanguages: [en]\n")
	s := &Server{
		activity:      activity.New(50),
		alerts:        activity.NewAlertLog(100),
		defaultConfig: defaultCfg,
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		DefaultConfig: defaultCfg,
		Configured:    func() bool { return false },
		ConfigPath:    func() string { return cfgFilePath },
	})
	// configured is false (unconfigured mode).

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/reset", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleResetConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleResetConfig() status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the file was written.
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config after reset: %v", err)
	}
	if !bytes.Equal(data, defaultCfg) {
		t.Errorf("config content = %q, want %q", string(data), string(defaultCfg))
	}
}

// --- handleConfigParsed unconfigured mode ---

func TestHandleConfigParsed_unconfigured_returns_defaults(t *testing.T) {
	t.Parallel()
	s := &Server{
		activity: activity.New(50),
		alerts:   activity.NewAlertLog(100),
	}
	s.live.Store(&liveState{})
	// configured is false.

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/parsed", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigParsed(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleConfigParsed(unconfigured) status = %d, want %d",
			rec.Code, http.StatusOK)
	}

	var result map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result["configured"] != false {
		t.Errorf("configured = %v, want false", result["configured"])
	}
}

// --- buildProviderSchemas ---

func TestBuildProviderSchemas_empty_registry(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	schemas := api.BuildProviderSchemas(reg)
	if len(schemas) != 0 {
		t.Errorf("BuildProviderSchemas(empty) = %d schemas, want 0", len(schemas))
	}
}

func TestBuildProviderSchemas_with_providers(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("embedded", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "embedded"}, nil
	})
	reg.Register("opensubtitles", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "opensubtitles"}, nil
	})
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", []api.ProviderSchemaField{
		{Key: "api_key", Label: "API Key", Type: "secret", Secret: true},
		{Key: "username", Label: "Username", Type: "text"},
	})
	// embedded has no schema registered; label should fall back to name.

	schemas := api.BuildProviderSchemas(reg)

	if len(schemas) != 2 {
		t.Fatalf("BuildProviderSchemas() = %d schemas, want 2", len(schemas))
	}

	// Schemas should be in sorted order (from ProviderNames).
	if schemas[0].Name != "embedded" {
		t.Errorf("schemas[0].Name = %q, want %q", schemas[0].Name, "embedded")
	}
	if schemas[1].Name != "opensubtitles" {
		t.Errorf("schemas[1].Name = %q, want %q", schemas[1].Name, "opensubtitles")
	}

	// embedded: AlwaysEnabled=true, label falls back to name.
	if !schemas[0].AlwaysEnabled {
		t.Error("embedded.AlwaysEnabled = false, want true")
	}
	if schemas[0].Label != "embedded" {
		t.Errorf("embedded.Label = %q, want %q (fallback to name)", schemas[0].Label, "embedded")
	}

	// opensubtitles: AlwaysEnabled=false, has schema fields.
	if schemas[1].AlwaysEnabled {
		t.Error("opensubtitles.AlwaysEnabled = true, want false")
	}
	if schemas[1].Label != "OpenSubtitles" {
		t.Errorf("opensubtitles.Label = %q, want %q", schemas[1].Label, "OpenSubtitles")
	}
	if len(schemas[1].Settings) != 2 {
		t.Fatalf("opensubtitles.Settings = %d fields, want 2", len(schemas[1].Settings))
	}
	if schemas[1].Settings[0].Key != "api_key" {
		t.Errorf("settings[0].Key = %q, want %q", schemas[1].Settings[0].Key, "api_key")
	}
	if !schemas[1].Settings[0].Secret {
		t.Error("settings[0].Secret = false, want true")
	}
}

// --- handleConfigSchema ---

func TestHandleConfigSchema_returns_json(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("embedded", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "embedded"}, nil
	})

	s := &Server{
		registry:   reg,
		schemaFunc: schema.Schema,
		activity:   activity.New(50),
		alerts:     activity.NewAlertLog(100),
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		SchemaFunc: schema.Schema,
		Registry:   reg,
	})
	s.live.Store(&liveState{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/config/schema", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigSchema(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("handleConfigSchema() status = %d, want %d", rec.Code, http.StatusOK)
	}
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/json")
	}

	var sections []api.SchemaSection
	if err := json.NewDecoder(rec.Body).Decode(&sections); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(sections) == 0 {
		t.Error("handleConfigSchema() returned 0 sections, want > 0")
	}
}

func TestHandleConfigSchema_rejects_non_get(t *testing.T) {
	t.Parallel()
	s := &Server{
		registry:   provider.NewRegistry(),
		schemaFunc: schema.Schema,
		activity:   activity.New(50),
		alerts:     activity.NewAlertLog(100),
	}
	s.configH = confighandlers.New(&confighandlers.Deps{
		SchemaFunc: schema.Schema,
		Registry:   provider.NewRegistry(),
	})
	s.live.Store(&liveState{})

	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/config/schema", http.NoBody)
	rec := httptest.NewRecorder()
	s.handleConfigSchema(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("handleConfigSchema(POST) status = %d, want %d",
			rec.Code, http.StatusMethodNotAllowed)
	}
}

// --- scanning.ScanItemTitle ---

func TestScanItemTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		item scanning.ScanItem
		want string
	}{
		{"series", scanning.ScanItem{Series: &api.Series{Title: "Breaking Bad"}}, "Breaking Bad"},
		{"movie", scanning.ScanItem{Movie: &api.Movie{Title: "Inception"}}, "Inception"},
		{"both nil", scanning.ScanItem{}, ""},
		{"series takes priority over movie", scanning.ScanItem{
			Series: &api.Series{Title: "Series"},
			Movie:  &api.Movie{Title: "Movie"},
		}, "Series"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := scanning.ScanItemTitle(tt.item)
			if got != tt.want {
				t.Errorf("scanning.ScanItemTitle() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- provider.ClearProviderCaches ---

// mockCacheClearer tracks whether ClearCache was called.
type mockCacheClearer struct {
	stubProvider

	cleared bool
}

func (m *mockCacheClearer) ClearCache() { m.cleared = true }

func TestClearProviderCaches_calls_cache_clearers(t *testing.T) {
	t.Parallel()
	cc := &mockCacheClearer{stubProvider: stubProvider{name: "hdbits"}}
	plain := &stubProvider{name: "os"}

	provider.ClearProviderCaches([]api.Provider{plain, cc})

	if !cc.cleared {
		t.Error("ClearCache not called on provider implementing cacheClearer")
	}
}

func TestClearProviderCaches_no_clearers(t *testing.T) {
	t.Parallel()
	plain := &stubProvider{name: "os"}
	// Should not panic with no cacheClearer providers.
	provider.ClearProviderCaches([]api.Provider{plain})
}

func TestClearProviderCaches_nil_providers(t *testing.T) {
	t.Parallel()
	// Should not panic with nil slice.
	provider.ClearProviderCaches(nil)
}

func TestBuildProviderSchemas_excludes_mock_provider(t *testing.T) {
	t.Parallel()
	reg := provider.NewRegistry()
	reg.Register("mock", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "mock"}, nil
	})
	reg.RegisterSchema("mock", "Mock Provider", nil)
	reg.Register("opensubtitles", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &stubProvider{name: "opensubtitles"}, nil
	})
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", nil)

	schemas := api.BuildProviderSchemas(reg, "mock")
	for _, s := range schemas {
		if s.Name == "mock" {
			t.Error("BuildProviderSchemas should exclude 'mock' provider")
		}
	}
	if len(schemas) != 1 {
		t.Errorf("BuildProviderSchemas len = %d, want 1 (mock excluded)", len(schemas))
	}
}

func TestEnabledProviders_output_is_sorted(t *testing.T) {
	t.Parallel()
	cfg := &epMock{providers: map[api.ProviderID]api.ProviderCfg{
		"zulu":    {Enabled: true},
		"alpha":   {Enabled: true},
		"charlie": {Enabled: true},
		"bravo":   {Enabled: false},
	}}
	got := enabledProviders(cfg)
	want := []api.ProviderID{"alpha", "charlie", "zulu"}
	if len(got) != len(want) {
		t.Fatalf("enabledProviders len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("enabledProviders[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
