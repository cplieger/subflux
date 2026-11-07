package providercalls

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestGestdown423PausesOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var sent atomic.Int64
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			sent.Add(1)
			w.WriteHeader(http.StatusLocked)
		}), "api.gestdown.info")
		a := providertest.Build(t, gestdown.NewEntry(), "")
		en, _ := language.Parse("en")
		q := provider.Query{
			Kind: media.Episode, External: media.ExternalIDs{TVDB: 81189},
			Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, Langs: []language.Tag{en},
		}
		search := func(ctx context.Context) error {
			_, err := a.Search(ctx, q)
			return err
		}
		b := newRig(t).f.Bind(gestdown.ID, "", 0)

		err := b.Call(ctx, Search, Interactive, search)
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.RateLimited || sent.Load() != 1 {
			t.Fatalf("first call: err %v after %d requests, want RateLimited after 1", err, sent.Load())
		}
		err = b.Call(ctx, Search, Interactive, search)
		if _, ok := errors.AsType[*BackoffError](err); !ok || sent.Load() != 1 {
			t.Errorf("second call: err %v after %d requests, want *BackoffError with none sent", err, sent.Load())
		}
	})
}
