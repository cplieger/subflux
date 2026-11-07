package provider_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/internal/dialhook"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

const (
	host  = "api.example.test"
	other = "other.example.test"
)

// scripted is an adapter whose calls the test writes.
type scripted struct {
	search func(context.Context, provider.Client, provider.Query) ([]provider.Candidate, error)
	fetch  func(context.Context, provider.Client, provider.Ref) (provider.Payload, error)
	c      provider.Client
}

func (s *scripted) Search(ctx context.Context, q provider.Query) ([]provider.Candidate, error) {
	if s.search == nil {
		return nil, nil
	}
	return s.search(ctx, s.c, q)
}

func (s *scripted) Fetch(ctx context.Context, r provider.Ref) (provider.Payload, error) {
	if s.fetch == nil {
		return provider.Payload{}, nil
	}
	return s.fetch(ctx, s.c, r)
}

// entry is a provider whose adapter is s, reaching hosts, with fields.
func entry(s *scripted, hosts []string, fields ...formschema.Field) provider.Entry {
	return provider.Entry{
		ID:      "scripted",
		Summary: "A provider the test scripts.",
		Hosts:   hosts,
		Schema:  formschema.Schema{Fields: fields},
		New: func(c provider.Client, _ formschema.Values) (provider.Adapter, error) {
			s.c = c
			return s, nil
		},
	}
}

// clientFor is the Client Build makes for an entry reaching hosts.
func clientFor(t *testing.T, hosts ...string) provider.Client {
	t.Helper()
	s := &scripted{}
	providertest.Build(t, entry(s, hosts), "")
	return s.c
}

func request(t *testing.T, ctx context.Context, method, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, method, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	return req
}

// get sends a GET of url through c and answers its body.
func get(t *testing.T, ctx context.Context, c provider.Client, url string) (string, error) {
	t.Helper()
	return send(c, request(t, ctx, http.MethodGet, url))
}

// send sends req through c and answers its body, read through ReadAll.
func send(c provider.Client, req *http.Request) (string, error) {
	resp, err := c.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := provider.ReadAll(resp, provider.MaxBody)
	return string(b), err
}

// failure is err's *Failure, failing t when there is none.
func failure(t *testing.T, err error) *provider.Failure {
	t.Helper()
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("err = %v (%T), want a *provider.Failure", err, err)
	}
	return f
}

// recordingHook is a DialTLS that refuses and counts its calls.
func recordingHook(ctx context.Context) (context.Context, *atomic.Int64) {
	n := new(atomic.Int64)
	return dialhook.With(ctx, func(context.Context, string, string) (net.Conn, error) {
		n.Add(1)
		return nil, errors.New("recording hook dialed")
	}), n
}

func text(s string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(s)) }
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}
