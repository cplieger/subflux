package mediasource_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sourcetest"
)

// pinger is a kind whose every adapter records the entry it pings and
// answers by url: "refused" Unauthorized, "radarr" a Radarr answering, any
// other nil.
type pinger struct {
	mu     sync.Mutex
	pinged []string
}

func (p *pinger) kind(typ string, schema formschema.Schema) mediasource.AdapterKind {
	return mediasource.AdapterKind{Type: typ, Schema: schema, New: func(cfg mediasource.Config, _ mediasource.Deps) (mediasource.Adapter, error) {
		url, _ := cfg.Values.String("url")
		return &script{caps: allCaps, ping: func(context.Context) error {
			p.mu.Lock()
			p.pinged = append(p.pinged, string(cfg.ID))
			p.mu.Unlock()
			switch url {
			case "refused":
				return &mediasource.SourceError{Cause: mediasource.Unauthorized, Err: errors.New("401 Unauthorized")}
			case "radarr":
				return errors.New("arrapi: the URL answers as Radarr, not Sonarr")
			}
			return nil
		}}, nil
	}}
}

func (p *pinger) got() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := slices.Clone(p.pinged)
	slices.Sort(out)
	return out
}

func entryOf(t *testing.T, id, typ, url, key, public string) mediasource.Config {
	t.Helper()
	doc := "url: " + url + "\napi_key: " + key + "\n"
	if public != "" {
		doc += "public_url: " + public + "\n"
	}
	vals, errs := arrSchema.Decode(parseYAML(t, doc))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return mediasource.Config{ID: media.SourceID(id), Type: typ, Values: vals}
}

func TestCheckSaved(t *testing.T) {
	p := &pinger{}
	kinds := []mediasource.AdapterKind{p.kind("sonarr", arrSchema), p.kind("radarr", arrSchema), p.kind("fake", sourcetest.FakeSchema)}
	fakeEntry := func(id, url, token string) mediasource.Config {
		vals, errs := sourcetest.FakeSchema.Decode(parseYAML(t, "url: "+url+"\ntoken: "+token+"\n"))
		if len(errs) > 0 {
			t.Fatal(errs)
		}
		return mediasource.Config{ID: media.SourceID(id), Type: "fake", Values: vals}
	}
	prev := []mediasource.Config{
		entryOf(t, "keyed", "sonarr", "http://a", "k1", ""),
		entryOf(t, "same", "sonarr", "http://b", "k1", ""),
		entryOf(t, "typed", "sonarr", "http://c", "k1", ""),
		entryOf(t, "public", "sonarr", "http://d", "k1", "https://old.example"),
		fakeEntry("tok", "http://f", "t1"),
		fakeEntry("furl", "http://g", "t1"),
	}
	next := []mediasource.Config{
		entryOf(t, "keyed", "sonarr", "http://a", "k2", ""),
		entryOf(t, "same", "sonarr", "http://b", "k1", ""),
		entryOf(t, "typed", "radarr", "http://c", "k1", ""),
		entryOf(t, "public", "sonarr", "http://d", "k1", "https://new.example"),
		fakeEntry("tok", "http://f", "t2"),
		fakeEntry("furl", "http://g2", "t1"),
		entryOf(t, "added", "radarr", "http://e", "k1", ""),
	}
	if errs := mediasource.CheckSaved(t.Context(), kinds, mediasource.Deps{}, prev, next); len(errs) > 0 {
		t.Fatalf("CheckSaved = %v, want every ping answering", errs)
	}
	if got, want := p.got(), []string{"added", "keyed", "tok", "typed"}; !slices.Equal(got, want) {
		t.Errorf("pinged %q, want %q: a new entry, a changed key, a changed type and a changed fake token", got, want)
	}

	t.Run("a refused ping is a field error at the entry's index", func(t *testing.T) {
		bad := entryOf(t, "bad", "sonarr", "refused", "k", "")
		bad.Index = 3
		errs := mediasource.CheckSaved(t.Context(), kinds, mediasource.Deps{}, nil, []mediasource.Config{bad})
		if len(errs) != 1 || errs[0].Path != "sources[3]" {
			t.Fatalf("CheckSaved = %v, want one error at sources[3]", errs)
		}
		if want := "the connection check failed (unauthorized): 401 Unauthorized"; errs[0].Message != want {
			t.Errorf("message %q, want %q", errs[0].Message, want)
		}
	})
	t.Run("no prev pings every entry", func(t *testing.T) {
		p.pinged = nil
		if errs := mediasource.CheckSaved(t.Context(), kinds, mediasource.Deps{}, nil, prev); len(errs) > 0 || len(p.got()) != len(prev) {
			t.Errorf("CheckSaved from no snapshot = %v, pinged %q; want all %d", errs, p.got(), len(prev))
		}
	})
}

func TestPingEntry(t *testing.T) {
	p := &pinger{}
	kinds := []mediasource.AdapterKind{p.kind("sonarr", arrSchema)}
	ctx := t.Context()
	if err := mediasource.PingEntry(ctx, kinds, mediasource.Deps{}, entryOf(t, "s", "sonarr", "refused", "k", "")); causeOf(t, err) != mediasource.Unauthorized {
		t.Errorf("a refused key = %v, want unauthorized", err)
	}
	err := mediasource.PingEntry(ctx, kinds, mediasource.Deps{}, entryOf(t, "s", "sonarr", "radarr", "k", ""))
	if causeOf(t, err) != mediasource.Protocol {
		t.Errorf("a Radarr URL in a Sonarr entry = %v, want protocol", err)
	}
	var se *mediasource.SourceError
	if errors.As(err, &se); se.Source != "s" || se.Op != "Ping" {
		t.Errorf("error names %q %q, want the entry and Ping, as a Set's would", se.Source, se.Op)
	}
	err = mediasource.PingEntry(ctx, kinds, mediasource.Deps{}, entryOf(t, "s", "plex", "http://a", "k", ""))
	if err == nil || errors.As(err, &se) {
		t.Errorf("an unknown type = %v, want a plain error", err)
	}
	t.Run("a hung upstream is unreachable at 10s", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			hung := mediasource.AdapterKind{Type: "sonarr", New: func(mediasource.Config, mediasource.Deps) (mediasource.Adapter, error) {
				return &script{caps: allCaps, ping: func(ctx context.Context) error {
					<-ctx.Done()
					return &mediasource.SourceError{Cause: mediasource.Protocol, Err: ctx.Err()}
				}}, nil
			}}
			start := time.Now()
			err := mediasource.PingEntry(t.Context(), []mediasource.AdapterKind{hung}, mediasource.Deps{}, entryOf(t, "s", "sonarr", "http://a", "k", ""))
			if causeOf(t, err) != mediasource.Unreachable || time.Since(start) != 10*time.Second {
				t.Errorf("PingEntry = %v after %v, want unreachable at 10s", err, time.Since(start))
			}
		})
	})
}
