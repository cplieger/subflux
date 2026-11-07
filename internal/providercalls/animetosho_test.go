package providercalls

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/status"
)

var animeHosts = []string{"feed.animetosho.net", "storage.animetosho.net", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}

// animeQuery is a TVDB-keyed episode, so its search asks the anime map first.
func animeQuery() provider.Query {
	en, _ := language.Parse("en")
	return provider.Query{
		Kind: media.Episode, Titles: []string{"Show"}, External: media.ExternalIDs{TVDB: 100},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, Langs: []language.Tag{en},
	}
}

func searchWith(a provider.Adapter, q provider.Query) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := a.Search(ctx, q)
		return err
	}
}

func stateOf(r *rig, id provider.ProviderID) status.ProviderState {
	snap, _ := r.st.Snapshot()
	for _, p := range snap.Providers {
		if p.ID == string(id) {
			return p
		}
	}
	return status.ProviderState{}
}

func alertOf(r *rig, id provider.ProviderID) bool {
	snap, _ := r.st.Snapshot()
	for _, a := range snap.Alerts {
		if a.Source == status.ProviderAlert(string(id)) {
			return true
		}
	}
	return false
}

func TestProviderStateNamesDNSCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx := providertest.Unresolvable(t.Context(), animeHosts...)
		a := providertest.Build(t, animetosho.NewEntry(animemap.New()), "")
		b := r.f.Bind(animetosho.ID, "", 0)
		q := animeQuery()
		q.External.TVDB = 0
		for range healthThreshold {
			_ = b.Call(ctx, Search, Interactive, searchWith(a, q))
		}
		if s := stateOf(r, animetosho.ID); s.Reason != "feed.animetosho.net does not resolve" {
			t.Errorf("state %+v; want the DNS cause", s)
		}
	})
}

// TestAnimapAlertOutlivesFeedSuccess: a feed success clears the funnel's
// provider:animetosho, never the map's own condition.
func TestAnimapAlertOutlivesFeedSuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Host == "github.com" {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			_, _ = w.Write([]byte("[]"))
		}), animeHosts...)
		down := providertest.Unresolvable(ctx, "feed.animetosho.net")
		m := animemap.New()
		e := animetosho.NewEntry(m)
		var (
			mu     sync.Mutex
			active bool
		)
		e.Data(func(_ string, a bool) {
			mu.Lock()
			active = a
			mu.Unlock()
		})
		a := providertest.Build(t, e, "")
		b := r.f.Bind(animetosho.ID, "", 0)
		for range healthThreshold {
			_ = b.Call(down, Search, Interactive, searchWith(a, animeQuery()))
		}
		if !alertOf(r, animetosho.ID) {
			t.Fatal("the feed's failures raised no provider alert")
		}
		if err := b.Call(ctx, Search, Interactive, searchWith(a, animeQuery())); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		if alertOf(r, animetosho.ID) || !active {
			t.Errorf("provider alert %v, map condition active %v; want the first cleared and the second kept", alertOf(r, animetosho.ID), active)
		}
	})
}

func TestReleasedAnimeToshoFetchesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		var sent atomic.Int64
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			sent.Add(1)
			w.WriteHeader(http.StatusNotFound)
		}), animeHosts...)
		a := providertest.Build(t, animetosho.NewEntry(animemap.New()), "")
		b := r.f.Bind(animetosho.ID, "", 0)
		b.Release()
		if err := b.Call(ctx, Search, Automatic, searchWith(a, animeQuery())); !errors.Is(err, ErrReleased) || sent.Load() != 0 {
			t.Errorf("released: %v after %d requests; want ErrReleased and none", err, sent.Load())
		}
	})
}
