package provider

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/formschema"
)

// maxMsg bounds a Failure's Msg once the wrapper returns it.
const maxMsg = 256

const redactMark = "[redacted]"

// Build checks cfg against e, makes e's Client, calls e.New and wraps the
// adapter. Every error the wrapped Search, Fetch or Test returns leaves it as
// a *Failure (any error that is none Transient) whose Msg is one line of at
// most 256 bytes with cfg's Secret values redacted, but a *Skipped returned
// before any request, which passes through; every Search answer is bounded
// as MaxAnswer's block says. The wrapped adapter implements Tester exactly
// when e.New's does.
func Build(e Entry, cfg Config) (Adapter, error) { //nolint:gocritic // hugeParam: the Client keeps its own copy of the row
	if cfg.ID != e.ID {
		return nil, fmt.Errorf("provider: config for %q given to entry %q", cfg.ID, e.ID)
	}
	if e.UserAgent != "" && !e.Exception {
		return nil, fmt.Errorf("provider: %s sets a User-Agent but is no Exception", e.ID)
	}
	var secrets []string
	credentialed := false
	for i := range e.Schema.Fields {
		f := &e.Schema.Fields[i]
		if _, ok := cfg.Values[f.Key]; !ok {
			return nil, fmt.Errorf("provider: %s config has no value for %q", e.ID, f.Key)
		}
		if f.Kind == formschema.Secret {
			credentialed = true
			if s := cfg.Values.Secret(f.Key); s != "" {
				secrets = append(secrets, s)
			}
		}
	}
	if len(cfg.Values) != len(e.Schema.Fields) {
		return nil, fmt.Errorf("provider: %s config has %d values for %d fields", e.ID, len(cfg.Values), len(e.Schema.Fields))
	}
	a, err := e.New(newClient(&e, credentialed), cfg.Values)
	if err != nil {
		msg, _ := textsafe.RedactSingleLineCapped(err.Error(), maxMsg, redactMark, secrets...)
		return nil, fmt.Errorf("provider: %s: %s", e.ID, msg)
	}
	w := &wrapped{inner: a, id: e.ID, secrets: secrets}
	if t, ok := a.(Tester); ok {
		return &testable{wrapped: w, tester: t}, nil
	}
	return w, nil
}

type wrapped struct {
	inner   Adapter
	id      ProviderID
	secrets []string
}

type testable struct {
	*wrapped
	tester Tester
}

func (w *testable) Test(ctx context.Context) error {
	ctx, sent := counting(ctx)
	return w.fail(ctx, w.tester.Test(ctx), sent)
}

func counting(ctx context.Context) (context.Context, *atomic.Int64) {
	n := new(atomic.Int64)
	return context.WithValue(ctx, sentKey{}, n), n
}

func (w *wrapped) Search(ctx context.Context, q Query) ([]Candidate, error) { //nolint:gocritic // hugeParam: Adapter takes the Query by value, so concurrent adapters never share one
	ctx, sent := counting(ctx)
	cands, err := w.inner.Search(ctx, q)
	return w.bound(cands), w.fail(ctx, err, sent)
}

func (w *wrapped) Fetch(ctx context.Context, ref Ref) ([]byte, error) {
	ctx, sent := counting(ctx)
	p, err := w.inner.Fetch(ctx, ref)
	if err != nil {
		return nil, w.fail(ctx, err, sent)
	}
	return p, nil
}

// fail is the one redaction point: every failure, whoever made it, leaves
// the wrapper here.
func (w *wrapped) fail(ctx context.Context, err error, sent *atomic.Int64) error {
	if err == nil {
		return nil
	}
	if s := (*Skipped)(nil); errors.As(err, &s) {
		cause, _ := textsafe.RedactSingleLineCapped(s.Cause, maxSkipCause, redactMark, w.secrets...)
		if sent.Load() == 0 {
			return &Skipped{Cause: cause}
		}
		return &Failure{Kind: Permanent, Msg: w.redact("skipped after a request: " + cause)}
	}
	var out Failure
	if f := (*Failure)(nil); errors.As(err, &f) {
		out = *f
	} else {
		out = *transportFailure(ctx, "", err)
		out.Kind = Transient
	}
	out.Msg = w.redact(out.Msg)
	return &out
}

func (w *wrapped) redact(s string) string {
	msg, _ := textsafe.RedactSingleLineCapped(s, maxMsg, redactMark, w.secrets...)
	return msg
}

// bound drops the malformed candidates, then keeps the first MaxAnswer, and
// logs one line counting what it dropped or cut.
func (w *wrapped) bound(cands []Candidate) []Candidate {
	var overMax, badToken, overCovers, cut int
	out := make([]Candidate, 0, min(len(cands), MaxAnswer))
	for i := range cands {
		c := cands[i]
		switch tok := c.Ref.Token(); {
		case tok == "" || len(tok) > MaxRefToken:
			badToken++
			continue
		case len(c.Covers) > MaxCovers:
			overCovers++
			continue
		}
		if len(out) == MaxAnswer {
			overMax++
			continue
		}
		var cr, ct bool
		c.Release, cr = BoundText(c.Release)
		c.Title, ct = BoundText(c.Title)
		if cr || ct {
			cut++
		}
		out = append(out, c)
	}
	if overMax+badToken+overCovers+cut > 0 {
		slog.Warn("provider answer bounded", "provider", w.id,
			"dropped_over_max", overMax, "dropped_ref", badToken, "dropped_covers", overCovers, "names_cut", cut)
	}
	return out
}
