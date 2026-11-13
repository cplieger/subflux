package search_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/go/keyenc"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/statedb"
)

// tester is an idle adapter whose Test answers err.
type tester struct {
	idle
	err   error
	calls *int
}

// closingTester is a tester that counts its closes, each after a test.
type closingTester struct {
	tester
	closes int
}

func (a *closingTester) Close(context.Context) error {
	if *a.calls > a.closes {
		a.closes++
	}
	return nil
}

func (a tester) Test(context.Context) error {
	*a.calls++
	return a.err
}

// testedEntry is a credentialed entry whose adapter tests with err.
func testedEntry(err error, calls *int) provider.Entry {
	e := testEntry("tested")
	e.Languages = language.MustProviderMap(provider.WirePairs("en", "en"))
	e.Schema = formschema.Schema{Fields: []formschema.Field{{Key: "api_key", Kind: formschema.Secret, Required: true}}}
	e.New = func(provider.Client, formschema.Values) (provider.Adapter, error) {
		return tester{err: err, calls: calls}, nil
	}
	return e
}

// failingVerify fingerprints a credential as itself and cannot verify one.
type failingVerify struct{ plainHasher }

func (failingVerify) Verify(context.Context, string, string) (bool, error) {
	return false, errors.New("hasher busy")
}

func TestTestAnswers(t *testing.T) {
	type want struct {
		message string
		ok      bool
		err     bool
	}
	cases := []struct {
		arrange func(t *testing.T, r *rig, f *providercalls.Funnel, e provider.Entry, cfg provider.Config)
		testErr error
		name    string
		want    want
	}{
		{name: "passes", want: want{ok: true, message: "connected"}},
		{name: "rejected", testErr: &provider.Failure{Kind: provider.Auth, Msg: "401"}, want: want{message: "the credentials were rejected"}},
		{name: "unreachable", testErr: &provider.Failure{Kind: provider.Transient, Msg: "dial refused"}, want: want{message: "could not reach the service: dial refused"}},
		{
			name: "rate paused", testErr: &provider.Failure{Kind: provider.RateLimited, Msg: "slow", RetryAfter: time.Hour},
			arrange: func(t *testing.T, _ *rig, f *providercalls.Funnel, e provider.Entry, cfg provider.Config) {
				if _, _, err := search.Test(t.Context(), f, e, cfg); err != nil {
					t.Fatal(err)
				}
			}, want: want{message: "rate limited until "},
		},
		{name: "re-enabled", arrange: func(t *testing.T, r *rig, _ *providercalls.Funnel, _ provider.Entry, _ provider.Config) {
			disable(t, r)
		}, want: want{ok: true, message: "re-enabled"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
				calls := 0
				e := testedEntry(c.testErr, &calls)
				cfg := providertest.Config(t, e, "api_key: k")
				if c.arrange != nil {
					c.arrange(t, r, r.funnel, e, cfg)
				}
				ok, msg, err := search.Test(t.Context(), r.funnel, e, cfg)
				if ok != c.want.ok || !strings.HasPrefix(msg, c.want.message) || (err != nil) != c.want.err {
					t.Errorf("Test = %v, %q, %v; want %v, %q", ok, msg, err, c.want.ok, c.want.message)
				}
			})
		})
	}
	t.Run("closes its adapter", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			calls := 0
			a := &closingTester{}
			a.calls = &calls
			e := testedEntry(nil, &calls)
			e.New = func(provider.Client, formschema.Values) (provider.Adapter, error) { return a, nil }
			if ok, _, err := search.Test(t.Context(), r.funnel, e, providertest.Config(t, e, "api_key: k")); !ok || err != nil {
				t.Fatalf("Test = %v, %v", ok, err)
			}
			if calls != 1 || a.closes != 1 {
				t.Errorf("tested %d times, closed %d times; want once each, the close after the test", calls, a.closes)
			}
		})
	})
	t.Run("no tester", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			e := testEntry("plain")
			ok, msg, err := search.Test(t.Context(), r.funnel, e, providertest.Config(t, e, ""))
			if !ok || msg != "no credentials to check" || err != nil {
				t.Errorf("Test = %v, %q, %v; want true, no credentials to check", ok, msg, err)
			}
		})
	})
	t.Run("verify busy", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			disable(t, r)
			calls := 0
			e := testedEntry(nil, &calls)
			f := providercalls.NewFunnel(r.db, failingVerify{}, r.st)
			ok, _, err := search.Test(t.Context(), f, e, providertest.Config(t, e, "api_key: k"))
			if ok || err == nil {
				t.Errorf("Test = %v, %v; want subflux's own failure as err", ok, err)
			}
		})
	})
	t.Run("never the live engine", func(t *testing.T) {
		inRig(t, "default: [{tag: en}]", func(t *testing.T, r *rig) {
			p := r.provider("p", "en")
			r.engine()
			calls := 0
			e := testedEntry(nil, &calls)
			if _, _, err := search.Test(t.Context(), r.funnel, e, providertest.Config(t, e, "api_key: k")); err != nil {
				t.Fatal(err)
			}
			if calls != 1 || p.calls() != 0 {
				t.Errorf("Test called the tester %d times and the engine's provider %d, want 1 and 0", calls, p.calls())
			}
		})
	})
}

// disable stores a disable record for the tested entry's credential k.
func disable(t *testing.T, r *rig) {
	t.Helper()
	if err := r.db.PutCredentialDisable(t.Context(), statedb.CredentialDisable{At: time.Now(), Provider: "tested", Fingerprint: keyenc.Join("k"), Reason: "refused"}); err != nil {
		t.Fatal(err)
	}
}
