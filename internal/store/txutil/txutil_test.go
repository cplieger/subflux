package txutil

import (
	"database/sql"
	"errors"
	"testing"
)

type mockTx struct {
	rolled bool
	err    error
}

func (m *mockTx) Rollback() error { m.rolled = true; return m.err }

func TestDeferRollback_success(t *testing.T) {
	t.Parallel()
	tx := &mockTx{}
	DeferRollback(tx)
	if !tx.rolled {
		t.Fatal("Rollback should be called")
	}
}

func TestDeferRollback_errTxDone(t *testing.T) {
	t.Parallel()
	// ErrTxDone is expected after a commit — should not panic.
	tx := &mockTx{err: sql.ErrTxDone}
	DeferRollback(tx) // should not panic or log warning
}

func TestDeferRollback_otherError(t *testing.T) {
	t.Parallel()
	// Other errors produce a warning log but don't panic.
	tx := &mockTx{err: errors.New("io error")}
	DeferRollback(tx) // should not panic
}

func TestLikeEscaper(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want string
	}{
		{"hello", "hello"},
		{"100%", `100\%`},
		{"under_score", `under\_score`},
		{`back\slash`, `back\\slash`},
		{`%_\`, `\%\_\\`},
	}
	for _, tt := range tests {
		got := LikeEscaper.Replace(tt.input)
		if got != tt.want {
			t.Errorf("LikeEscaper(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestAppendPrefixFilter_empty(t *testing.T) {
	t.Parallel()
	q, args := AppendPrefixFilter("SELECT 1", nil, "", "col")
	if q != "SELECT 1" {
		t.Fatalf("query = %q, want unchanged", q)
	}
	if len(args) != 0 {
		t.Fatalf("args = %v, want empty", args)
	}
}

func TestAppendPrefixFilter_nonempty(t *testing.T) {
	t.Parallel()
	q, args := AppendPrefixFilter("SELECT 1 WHERE 1=1", []any{"x"}, "foo%bar", "name")
	if q != `SELECT 1 WHERE 1=1 AND name LIKE ? ESCAPE '\'` {
		t.Fatalf("query = %q", q)
	}
	if len(args) != 2 {
		t.Fatalf("args len = %d, want 2", len(args))
	}
	want := `foo\%bar%`
	if args[1] != want {
		t.Fatalf("args[1] = %q, want %q", args[1], want)
	}
}
