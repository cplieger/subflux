package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(%q) unexpected error: %v", path, err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })
	return db
}

func TestOpen_creates_database(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	downloads, attempts, _ := db.Stats(context.Background())
	if downloads != 0 || attempts != 0 {
		t.Errorf("Stats() = (%d, %d), want (0, 0)", downloads, attempts)
	}
}

func TestOpen_invalid_path_returns_error(t *testing.T) {
	t.Parallel()

	_, err := Open(context.Background(), "/nonexistent/\x00/bad.db")
	if err == nil {
		t.Fatal("Open(invalid path) expected error, got nil")
	}
}

func TestOpen_empty_path_returns_error(t *testing.T) {
	t.Parallel()
	_, err := Open(context.Background(), "")
	if err == nil {
		t.Fatal("Open(\"\") expected error, got nil")
	}
}

func TestIntPow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		base, want float64
		exp        int
	}{
		{2, 1, 0},
		{2, 2, 1},
		{2, 8, 3},
		{3, 9, 2},
		{1.5, 2.25, 2},
		{10, 1, 0},
		{2, 1024, 10},
	}
	for _, tc := range tests {
		got := intPow(tc.base, tc.exp)
		if got != tc.want {
			t.Errorf("intPow(%v, %v) = %v, want %v", tc.base, tc.exp, got, tc.want)
		}
	}
}

func TestIntPow_identity_properties(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		base := rapid.Float64Range(0.1, 100).Draw(t, "base")

		if got := intPow(base, 0); got != 1.0 {
			t.Errorf("intPow(%v, 0) = %v, want 1.0", base, got)
		}

		if got := intPow(base, 1); got != base {
			t.Errorf("intPow(%v, 1) = %v, want %v", base, got, base)
		}
	})
}

func TestPlaceholders(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		wantClause string
		input      []string
		wantLen    int
	}{
		{"single value", "?", []string{"a"}, 1},
		{"two values", "?,?", []string{"a", "b"}, 2},
		{"three values", "?,?,?", []string{"x", "y", "z"}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clause, args := placeholders(tt.input)
			if clause != tt.wantClause {
				t.Errorf("placeholders(%v) clause = %q, want %q",
					tt.input, clause, tt.wantClause)
			}
			if len(args) != tt.wantLen {
				t.Errorf("placeholders(%v) args len = %d, want %d",
					tt.input, len(args), tt.wantLen)
			}
			for i, v := range tt.input {
				if args[i] != v {
					t.Errorf("placeholders(%v) args[%d] = %v, want %v",
						tt.input, i, args[i], v)
				}
			}
		})
	}
}

func TestPlaceholders_empty_input(t *testing.T) {
	t.Parallel()
	clause, args := placeholders[string](nil)
	if clause != "" {
		t.Errorf("placeholders(nil) clause = %q, want empty", clause)
	}
	if len(args) != 0 {
		t.Errorf("placeholders(nil) args len = %d, want 0", len(args))
	}
}

func TestPlaceholders_count_matches_input(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "n")
		input := make([]string, n)
		for i := range n {
			input[i] = rapid.String().Draw(t, "val")
		}

		clause, args := placeholders(input)

		if len(args) != n {
			t.Errorf("placeholders(%d values) args len = %d, want %d",
				n, len(args), n)
		}

		qCount := 0
		for _, c := range clause {
			if c == '?' {
				qCount++
			}
		}
		if qCount != n {
			t.Errorf("placeholders(%d values) clause has %d '?', want %d",
				n, qCount, n)
		}

		for i, v := range input {
			if args[i] != v {
				t.Errorf("placeholders args[%d] = %v, want %v", i, args[i], v)
			}
		}
	})
}

func TestIntPow_negative_exponent_returns_one(t *testing.T) {
	t.Parallel()
	got := intPow(2, -1)
	if got != 1.0 {
		t.Errorf("intPow(2, -1) = %v, want 1.0", got)
	}
}

func TestOpen_reopen_preserves_data(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "test.db")

	db1, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(1): %v", err)
	}
	if err := db1.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/movie.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	db1.Close(context.Background())

	db2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open(2): %v", err)
	}
	defer db2.Close(context.Background())

	downloads, _, _ := db2.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads after reopen = %d, want 1", downloads)
	}
}

func TestIntPow_non_negative_for_positive_base(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(t *rapid.T) {
		base := rapid.Float64Range(0.01, 1000).Draw(t, "base")
		exp := rapid.IntRange(0, 20).Draw(t, "exp")

		got := intPow(base, exp)
		if got < 0 {
			t.Errorf("intPow(%v, %d) = %v, want non-negative", base, exp, got)
		}
	})
}

func TestAppendPrefixFilter_escapes_wildcards(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	for _, id := range []string{"tvdb-100%done", "tvdb-100_test", `tvdb-100\path`, "tvdb-100-normal"} {
		if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: id, Title: "Title", AudioLang: "en", Season: 1, Episode: 1}); err != nil {
			t.Fatalf("RecordScanState(%q): %v", id, err)
		}
	}

	tests := []struct {
		name   string
		prefix string
		want   int
	}{
		{"percent in prefix matches only literal", "tvdb-100%", 1},
		{"underscore in prefix matches only literal", "tvdb-100_", 1},
		{"backslash in prefix matches only literal", `tvdb-100\`, 1},
		{"normal prefix matches all with that prefix", "tvdb-100", 4},
		{"empty prefix returns all", "", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows, err := db.GetScanStates(context.Background(), "episode", tc.prefix)
			if err != nil {
				t.Fatalf("GetScanStates(%q) error: %v", tc.prefix, err)
			}
			if len(rows) != tc.want {
				t.Errorf("GetScanStates(%q) returned %d rows, want %d",
					tc.prefix, len(rows), tc.want)
			}
		})
	}
}

func TestAppendPrefixFilter_empty_prefix_is_noop(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		query := rapid.String().Draw(t, "query")
		args := []any{rapid.String().Draw(t, "arg")}

		gotQ, gotA := appendPrefixFilter(query, args, "")

		if gotQ != query {
			t.Errorf("appendPrefixFilter(%q, args, \"\") query = %q, want unchanged %q",
				query, gotQ, query)
		}
		if len(gotA) != len(args) {
			t.Errorf("appendPrefixFilter(%q, args, \"\") args len = %d, want %d",
				query, len(gotA), len(args))
		}
	})
}

func TestAppendPrefixFilter_nonempty_prefix_appends_clause(t *testing.T) {
	t.Parallel()

	q, a := appendPrefixFilter("SELECT * FROM t WHERE x = ?", []any{"val"}, "abc") //nolint:unqueryvet // testing the prefix filter function, not production SQL

	if !strings.Contains(q, "LIKE") {
		t.Errorf("appendPrefixFilter with prefix: query = %q, want LIKE clause", q)
	}
	if len(a) != 2 {
		t.Fatalf("appendPrefixFilter with prefix: args len = %d, want 2", len(a))
	}

	if a[1] != "abc%" {
		t.Errorf("appendPrefixFilter LIKE arg = %q, want %q", a[1], "abc%")
	}
}

func TestIntPow_property_matches_repeated_multiplication(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		base := rapid.Float64Range(0.5, 10.0).Draw(t, "base")
		exp := rapid.IntRange(0, 20).Draw(t, "exp")

		got := intPow(base, exp)

		// Mirror intPow's early-exit cap: it returns when result exceeds
		// maxDurationFloat (callers always cap the result anyway).
		want := 1.0
		for range exp {
			want *= base
			if want > maxDurationFloat {
				break
			}
		}

		if got != want {
			t.Errorf("intPow(%v, %v) = %v, want %v", base, exp, got, want)
		}
	})
}

func TestOpen_appliesFileModeAndSecurityPragmas(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "sec.db")
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close(context.Background()) })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("db file mode = %o, want 600", perm)
	}

	var secureDelete int
	if err := db.db.QueryRowContext(context.Background(), "PRAGMA secure_delete").Scan(&secureDelete); err != nil {
		t.Fatalf("PRAGMA secure_delete: %v", err)
	}
	if secureDelete != 1 {
		t.Errorf("secure_delete = %d, want 1 (ON)", secureDelete)
	}

	var synchronous int
	if err := db.db.QueryRowContext(context.Background(), "PRAGMA synchronous").Scan(&synchronous); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	if synchronous != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", synchronous)
	}
}
