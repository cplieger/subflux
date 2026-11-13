package provider_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

// captureLog points the default logger at a buffer for the test.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestCandidateBounds(t *testing.T) {
	t.Run("answer", func(t *testing.T) {
		long := strings.Repeat("n", 10<<20) + "\rsecond line"
		cands := []provider.Candidate{
			{Ref: provider.NewRef(strings.Repeat("t", 10<<20))},
			{Ref: provider.NewRef("")},
			{Ref: provider.NewRef("covers"), Covers: make([]media.Numbering, provider.MaxCovers+1)},
			{Ref: provider.NewRef("long-name"), Release: long, Title: "Show"},
		}
		for i := range 10_000 {
			cands = append(cands, provider.Candidate{Ref: provider.NewRef(fmt.Sprintf("r%d", i))})
		}
		s := &scripted{search: func(context.Context, provider.Client, provider.Query) ([]provider.Candidate, error) {
			return cands, nil
		}}
		logs := captureLog(t)
		got, err := providertest.Build(t, entry(s, []string{host}), "").Search(t.Context(), provider.Query{})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != provider.MaxAnswer {
			t.Fatalf("%d candidates, want %d", len(got), provider.MaxAnswer)
		}
		first := got[0]
		if first.Ref.Token() != "long-name" || len(first.Release) > provider.MaxText || strings.ContainsAny(first.Release, "\r\n") || first.Title != "Show" {
			t.Errorf("first candidate %q with a %d-byte release, want long-name sanitized to one line of at most %d", first.Ref.Token(), len(first.Release), provider.MaxText)
		}
		if got[1].Ref.Token() != "r0" || got[len(got)-1].Ref.Token() != "r498" {
			t.Errorf("kept %q to %q, want r0 to r498 in order", got[1].Ref.Token(), got[len(got)-1].Ref.Token())
		}
		for _, c := range got {
			if tok := c.Ref.Token(); tok == "" || len(tok) > provider.MaxRefToken || tok == "covers" {
				t.Fatalf("kept a malformed candidate %.20q", tok)
			}
		}
		line := logs.String()
		for _, want := range []string{"dropped_over_max=9501", "dropped_ref=2", "dropped_covers=1", "names_cut=1", "provider=scripted"} {
			if !strings.Contains(line, want) {
				t.Errorf("log %q lacks %s", line, want)
			}
		}
	})
	t.Run("an answer in bounds logs nothing", func(t *testing.T) {
		s := &scripted{search: func(context.Context, provider.Client, provider.Query) ([]provider.Candidate, error) {
			return []provider.Candidate{{Ref: provider.NewRef("1"), Release: "Show.S01E01.720p"}}, nil
		}}
		logs := captureLog(t)
		got, err := providertest.Build(t, entry(s, []string{host}), "").Search(t.Context(), provider.Query{})
		if err != nil || len(got) != 1 || got[0].Release != "Show.S01E01.720p" {
			t.Fatalf("got %+v, %v", got, err)
		}
		if logs.Len() != 0 {
			t.Errorf("logged %q", logs.String())
		}
	})
}

var keyField = formschema.Field{Key: "api_key", Label: "API key", Kind: formschema.Secret, Required: true}

const key = "sekrit-KEY-0123456789"

func TestFailureRedactedAtWrapper(t *testing.T) {
	cases := []struct {
		name string
		do   func(context.Context, provider.Client) error
		ctx  func(t *testing.T) context.Context
	}{
		{"an adapter's Fail quoting its key", func(context.Context, provider.Client) error {
			return provider.Fail(provider.Auth, "the API refused key "+key)
		}, nil},
		{"a plain adapter error quoting its key", func(context.Context, provider.Client) error {
			return fmt.Errorf("decode answer for %s:\nline two", key)
		}, nil},
		{"a transport error echoing a passkey URL", func(ctx context.Context, c provider.Client) error {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/getdox.php?passkey="+key, http.NoBody)
			_, err := send(c, req)
			return err
		}, func(t *testing.T) context.Context { ctx, _ := recordingHook(t.Context()); return ctx }},
		{"a transport error echoing an api_key query", func(ctx context.Context, c provider.Client) error {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/api/v1/subtitles?api_key="+key+"&type=tv", http.NoBody)
			_, err := send(c, req)
			return err
		}, func(t *testing.T) context.Context { ctx, _ := recordingHook(t.Context()); return ctx }},
		{"an over-long failure", func(context.Context, provider.Client) error {
			return provider.Fail(provider.Permanent, strings.Repeat("x", 300)+key)
		}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				if tc.ctx != nil {
					ctx = tc.ctx(t)
				}
				s := &scripted{
					search: func(ctx context.Context, c provider.Client, _ provider.Query) ([]provider.Candidate, error) {
						return nil, tc.do(ctx, c)
					},
					fetch: func(ctx context.Context, c provider.Client, _ provider.Ref) ([]byte, error) {
						return nil, tc.do(ctx, c)
					},
				}
				a := providertest.Build(t, entry(s, []string{host}, keyField), "api_key: "+key)
				_, serr := a.Search(ctx, provider.Query{})
				_, ferr := a.Fetch(ctx, provider.NewRef("1"))
				for _, err := range []error{serr, ferr} {
					f := failure(t, err)
					if strings.Contains(f.Error(), key) || strings.Contains(f.Error(), key[:12]) {
						t.Errorf("failure keeps the key: %q", f.Error())
					}
					if len(f.Msg) > 256 || strings.ContainsAny(f.Msg, "\r\n") {
						t.Errorf("Msg is %d bytes or several lines: %q", len(f.Msg), f.Msg)
					}
					if !strings.Contains(f.Msg, "[redacted]") && !strings.HasSuffix(tc.name, "over-long failure") {
						t.Errorf("Msg %q carries no redaction mark", f.Msg)
					}
				}
			})
		})
	}
	t.Run("a plain error is Transient", func(t *testing.T) {
		s := &scripted{search: func(context.Context, provider.Client, provider.Query) ([]provider.Candidate, error) {
			return nil, errors.New("decode answer: unexpected EOF")
		}}
		_, err := providertest.Build(t, entry(s, []string{host}), "").Search(t.Context(), provider.Query{})
		if f := failure(t, err); f.Kind != provider.Transient {
			t.Errorf("%+v, want Transient", f)
		}
	})
	t.Run("a cancelled call keeps its context error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		s := &scripted{search: func(ctx context.Context, c provider.Client, _ provider.Query) ([]provider.Candidate, error) {
			_, err := get(t, ctx, c, "https://"+host+"/")
			return nil, err
		}}
		_, err := providertest.Build(t, entry(s, []string{host}), "").Search(ctx, provider.Query{})
		if !errors.Is(err, context.Canceled) || failure(t, err).Kind != provider.Transient {
			t.Errorf("err %v, want a Transient failure that is context.Canceled", err)
		}
	})
}

func TestSkippedPassesThrough(t *testing.T) {
	t.Run("before any request", func(t *testing.T) {
		s := &scripted{search: func(context.Context, provider.Client, provider.Query) ([]provider.Candidate, error) {
			return nil, &provider.Skipped{Cause: "no IMDb id for " + key}
		}}
		_, err := providertest.Build(t, entry(s, []string{host}, keyField), "api_key: "+key).Search(t.Context(), provider.Query{})
		sk, ok := errors.AsType[*provider.Skipped](err)
		if !ok || strings.Contains(sk.Cause, key) || !strings.HasPrefix(sk.Cause, "no IMDb id for ") {
			t.Errorf("err %v (%T), want a redacted *Skipped", err, err)
		}
	})
	t.Run("after a request", func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), text("{}"), host)
		s := &scripted{search: func(ctx context.Context, c provider.Client, _ provider.Query) ([]provider.Candidate, error) {
			if _, err := get(t, ctx, c, "https://"+host+"/"); err != nil {
				return nil, err
			}
			return nil, &provider.Skipped{Cause: "no IMDb id"}
		}}
		_, err := providertest.Build(t, entry(s, []string{host}), "").Search(ctx, provider.Query{})
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("%+v, want Permanent", f)
		}
	})
	t.Run("after a detached request", func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), text("{}"), host)
		s := &scripted{search: func(ctx context.Context, c provider.Client, _ provider.Query) ([]provider.Candidate, error) {
			if _, err := get(t, provider.Detached(ctx), c, "https://"+host+"/"); err != nil {
				return nil, err
			}
			return nil, &provider.Skipped{Cause: "no IMDb id"}
		}}
		_, err := providertest.Build(t, entry(s, []string{host}), "").Search(ctx, provider.Query{})
		if _, ok := errors.AsType[*provider.Skipped](err); !ok {
			t.Errorf("err %v (%T), want the *Skipped: a detached request is not the call's", err, err)
		}
	})
}

type testerAdapter struct {
	scripted
	err error
}

func (a *testerAdapter) Test(context.Context) error { return a.err }

// closerAdapter records its Close calls and answers err.
type closerAdapter struct {
	scripted
	err    error
	closes int
}

func (a *closerAdapter) Close(context.Context) error { a.closes++; return a.err }

// TestBuildCloses: every adapter Build returns is a Closer, closing the
// adapter it wraps when that one is, with its answer, and answering nil
// otherwise.
func TestBuildCloses(t *testing.T) {
	gone := errors.New("still fetching")
	for _, c := range []struct {
		name  string
		inner provider.Adapter
		want  error
	}{
		{"closer", &closerAdapter{err: gone}, gone},
		{"plain", &scripted{}, nil},
		{"tester", &testerAdapter{}, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := entry(&scripted{}, []string{host})
			e.New = func(provider.Client, formschema.Values) (provider.Adapter, error) { return c.inner, nil }
			cl, ok := providertest.Build(t, e, "").(provider.Closer)
			if !ok {
				t.Fatal("Build's adapter is no Closer")
			}
			if err := cl.Close(t.Context()); !errors.Is(err, c.want) {
				t.Errorf("Close: %v, want %v", err, c.want)
			}
			if ca, ok := c.inner.(*closerAdapter); ok && ca.closes != 1 {
				t.Errorf("the wrapped adapter closed %d times, want 1", ca.closes)
			}
		})
	}
}

func TestBuild(t *testing.T) {
	s := &scripted{}
	e := entry(s, []string{host}, keyField)
	t.Run("refuses a config for another entry", func(t *testing.T) {
		cfg := providertest.Config(t, e, "api_key: x")
		cfg.ID = "other"
		if _, err := provider.Build(e, cfg); err == nil {
			t.Error("built")
		}
	})
	t.Run("refuses values the schema does not hold", func(t *testing.T) {
		cfg := providertest.Config(t, e, "api_key: x")
		delete(cfg.Values, "api_key")
		if _, err := provider.Build(e, cfg); err == nil {
			t.Error("built with a missing value")
		}
		bare := entry(s, []string{host})
		if _, err := provider.Build(bare, providertest.Config(t, e, "api_key: x")); err == nil {
			t.Error("built with an extra value")
		}
	})
	t.Run("redacts New's error", func(t *testing.T) {
		e := e
		e.New = func(provider.Client, formschema.Values) (provider.Adapter, error) {
			return nil, errors.New("key " + key + " is malformed")
		}
		_, err := provider.Build(e, providertest.Config(t, e, "api_key: "+key))
		if err == nil || strings.Contains(err.Error(), key) {
			t.Errorf("err %v", err)
		}
	})
	t.Run("keeps Tester exactly when the adapter has it", func(t *testing.T) {
		if _, ok := providertest.Build(t, e, "api_key: x").(provider.Tester); ok {
			t.Error("a scripted adapter without Test is a Tester")
		}
		ta := &testerAdapter{err: provider.Fail(provider.Auth, "refused "+key)}
		e := e
		e.New = func(provider.Client, formschema.Values) (provider.Adapter, error) { return ta, nil }
		tt, ok := providertest.Build(t, e, "api_key: "+key).(provider.Tester)
		if !ok {
			t.Fatal("a Tester adapter lost Test")
		}
		if f := failure(t, tt.Test(t.Context())); f.Kind != provider.Auth || strings.Contains(f.Msg, key) {
			t.Errorf("Test: %+v", f)
		}
	})
}
