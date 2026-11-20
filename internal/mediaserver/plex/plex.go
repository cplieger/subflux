// Package plex is Plex Media Server as a mediaserver adapter. Plex has no
// lookup by file path and its GUIDs depend on the agent, so the adapter keeps
// an index from each media part's server path to the items holding it.
package plex

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/cplieger/plexapi/v3"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
)

// Kind is the plex row of the media server registry.
var Kind = mediaserver.AdapterKind{Type: "plex", Schema: schema, New: newAdapter}

var schema = formschema.Schema{Fields: []formschema.Field{
	{Key: "url", Label: "Plex URL", Help: "The address subflux reaches Plex Media Server at.", Kind: formschema.String, Required: true},
	{Key: "token", Label: "Plex token", Help: "An X-Plex-Token for an account that administers the server.", Kind: formschema.Secret, Required: true},
}}

// adapter is one Plex Media Server. It is safe for concurrent use.
type adapter struct {
	c   *plexapi.Client
	log *slog.Logger
	// life is cancelled by Shutdown; every call and build runs under it.
	life   context.Context
	cancel context.CancelFunc
	// merging admits one merge at a time, so a second miss reuses the first
	// one's answer instead of walking the same items again.
	merging chan struct{}
	idx     index
	// work counts the calls and builds running; Shutdown waits for it.
	work sync.WaitGroup
}

var _ mediaserver.Adapter = (*adapter)(nil)

// newPlex builds plexapi's own client over the entry's url and token, with
// deps.Logger only. It never passes deps.HTTP: plexapi.WithHTTPClient drops
// the library's redirect refusal, so the token would follow a redirect to
// another host. It makes no request.
func newPlex(cfg mediaserver.Config, deps mediasource.Deps) (*adapter, error) {
	log := deps.Logger
	if log == nil {
		log = slog.Default()
	}
	c, err := plexapi.New(cfg.Values.String("url"), plexapi.Token(cfg.Values.Secret("token")), plexapi.WithLogger(log))
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	return &adapter{
		c: c, log: log.With("media_server", cfg.ID),
		life: life, cancel: cancel, merging: make(chan struct{}, 1),
		idx: index{gone: map[string]goneKey{}},
	}, nil
}

func newAdapter(cfg mediaserver.Config, deps mediasource.Deps) (mediaserver.Adapter, error) {
	a, err := newPlex(cfg, deps)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// errShutDown answers every call after Shutdown; transient, since the Set
// that replaced this adapter's may answer.
var errShutDown error = shutDownError{}

type shutDownError struct{}

func (shutDownError) Error() string     { return "plex: the adapter was shut down" }
func (shutDownError) IsTransient() bool { return true }

// run calls f on ctx joined to the adapter's lifetime: refused once Shutdown
// began, cancelled by it and waited for, so no request outlives Shutdown.
// A call Shutdown overtook answers errShutDown, never a partial answer.
func run[T any](ctx context.Context, a *adapter, f func(context.Context) (T, error)) (T, error) {
	var zero T
	if !a.enter() {
		return zero, errShutDown
	}
	defer a.work.Done()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(a.life, cancel)()
	v, err := f(ctx)
	if a.life.Err() != nil {
		return zero, errShutDown
	}
	return v, err
}

// enter counts one call in work unless Shutdown began; the check and the
// Add share idx.mu with Shutdown's stop, so no Add follows its Wait.
func (a *adapter) enter() bool {
	a.idx.mu.Lock()
	defer a.idx.mu.Unlock()
	if a.idx.stopped {
		return false
	}
	a.work.Add(1)
	return true
}

// Ping is one authenticated Identity read.
func (a *adapter) Ping(ctx context.Context) error {
	_, err := run(ctx, a, func(ctx context.Context) (struct{}, error) {
		_, err := a.c.Identity(ctx)
		return struct{}{}, err
	})
	return err
}

// Refresh is plexapi's RefreshMetadata of every item holding a part over
// serverPath, stopping at the first failure; a path no item holds is a
// success.
func (a *adapter) Refresh(ctx context.Context, serverPath string) error {
	_, err := run(ctx, a, func(ctx context.Context) (struct{}, error) {
		return struct{}{}, a.holders(ctx, serverPath, func(key string, _ *plexapi.Part) (bool, error) {
			if err := a.c.RefreshMetadata(ctx, plexapi.RatingKey(key)); err != nil {
				return false, fmt.Errorf("refreshing item %s: %w", key, err)
			}
			return false, nil
		})
	})
	return err
}

// Shutdown cancels every call and build running and starts no other, then
// waits until they have ended or ctx is done, answering ctx's error then.
func (a *adapter) Shutdown(ctx context.Context) error {
	a.idx.mu.Lock()
	a.idx.stopped = true
	a.idx.mu.Unlock()
	a.cancel()
	done := make(chan struct{})
	go func() {
		a.work.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
