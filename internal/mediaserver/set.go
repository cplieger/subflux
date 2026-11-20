package mediaserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/textsafe"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

const (
	labelsTimeout = 5 * time.Second
	pingBudget    = 10 * time.Second
	// maxLabelBytes bounds each string Labels returns, marker included,
	// whatever an adapter answered.
	maxLabelBytes = 512
	labelMarker   = "…"
)

// Result is one server's answer to a Refresh. Transient is true when a retry
// may change it.
type Result struct {
	Err       error
	Server    string
	Transient bool
}

// Set is the configured media servers. It is safe for concurrent use.
type Set struct {
	alerts  *status.Store
	byID    map[string]*server
	closing chan struct{} // closed by the first Shutdown
	done    chan struct{} // closed once every adapter's Shutdown returned
	shutErr error         // written before done closes
	ids     []string
	once    sync.Once
}

// server is one entry; adapter is nil when its New failed with newErr.
type server struct {
	adapter Adapter
	newErr  error
	m       mediasource.Mapper
}

// errClosed answers every call on a Set after its Shutdown; transient, since
// its successor may answer.
var errClosed error = closedError{}

type closedError struct{}

func (closedError) Error() string     { return "mediaserver: server set closed" }
func (closedError) IsTransient() bool { return true }

// NewSet builds each entry's adapter over deps. An unknown type or a
// duplicate id is a programming fault, since ParseConfigs refused both. An
// entry whose New fails is kept unavailable with its media_server:<id> alert
// raised, until a rebuilt Set retries it; an entry whose New succeeds has the
// alert cleared, since nothing has failed in this Set yet.
func NewSet(kinds []AdapterKind, cfgs []Config, deps mediasource.Deps, alerts *status.Store) (*Set, error) {
	if alerts == nil {
		return nil, errors.New("mediaserver: NewSet needs a status store")
	}
	s := &Set{alerts: alerts, byID: make(map[string]*server, len(cfgs)), closing: make(chan struct{}), done: make(chan struct{})}
	for _, cfg := range cfgs {
		k := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == cfg.Type })
		if k < 0 {
			return nil, fmt.Errorf("mediaserver: server %s: unknown type %q", cfg.ID, cfg.Type)
		}
		if _, dup := s.byID[cfg.ID]; dup {
			return nil, fmt.Errorf("mediaserver: server %s is configured twice", cfg.ID)
		}
		cfg.PathMappings = slices.Clone(cfg.PathMappings)
		srv := &server{m: serverMapper(&cfg)}
		srv.adapter, srv.newErr = kinds[k].New(cfg, deps)
		if srv.newErr != nil {
			srv.adapter = nil
			alerts.Reconcile(status.MediaServerAlert(cfg.ID), true, "media server "+cfg.ID+" could not be set up: "+srv.newErr.Error())
		} else {
			alerts.Reconcile(status.MediaServerAlert(cfg.ID), false, "")
		}
		s.byID[cfg.ID] = srv
		s.ids = append(s.ids, cfg.ID)
	}
	return s, nil
}

// Servers lists the ids of the entries whose New succeeded, in entry order.
func (s *Set) Servers() []string {
	var out []string
	for _, id := range s.ids {
		if s.byID[id].adapter != nil {
			out = append(out, id)
		}
	}
	return out
}

// lookup answers the live server id names: errClosed after Shutdown, an
// error for an unknown id or one whose New failed.
func (s *Set) lookup(id string) (*server, error) {
	select {
	case <-s.closing:
		return nil, errClosed
	default:
	}
	srv, ok := s.byID[id]
	switch {
	case !ok:
		return nil, fmt.Errorf("mediaserver: unknown server %q", id)
	case srv.newErr != nil:
		return nil, fmt.Errorf("mediaserver: server %s is unavailable: %w", id, srv.newErr)
	}
	return srv, nil
}

// Refresh asks each named server, in parallel, to refresh the items holding
// videoPath, mapped to that server's view; a path no item holds is a
// success. Results follow servers' order. A server's failure raises its
// alert and its next success clears it; the caller's cancellation changes
// neither.
func (s *Set) Refresh(ctx context.Context, videoPath string, servers []string) []Result {
	out := make([]Result, len(servers))
	var wg sync.WaitGroup
	for i, id := range servers {
		wg.Go(func() {
			out[i] = Result{Server: id}
			srv, err := s.lookup(id)
			if err == nil {
				err = srv.refresh(ctx, videoPath)
				s.report(ctx, id, err)
			}
			out[i].Err, out[i].Transient = err, transient(ctx, err)
		})
	}
	wg.Wait()
	return out
}

// Ping calls the server's Adapter.Ping, reporting its alert as Refresh does;
// it fails for an unknown server and for one whose New failed.
func (s *Set) Ping(ctx context.Context, server string) error {
	srv, err := s.lookup(server)
	if err != nil {
		return err
	}
	err = srv.adapter.Ping(ctx)
	s.report(ctx, server, err)
	return err
}

// PingServer builds cfg's adapter with its kind's New, calls its Ping under
// 10 s, then shuts the adapter down; it builds no Set, so the connection test
// needs nothing a Set holds. An unknown type or a failing New is an error.
func PingServer(ctx context.Context, kinds []AdapterKind, deps mediasource.Deps, cfg Config) error {
	k := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == cfg.Type })
	if k < 0 {
		return fmt.Errorf("mediaserver: server %s: unknown type %q", cfg.ID, cfg.Type)
	}
	ad, err := kinds[k].New(cfg, deps)
	if err != nil {
		return fmt.Errorf("mediaserver: server %s: %w", cfg.ID, err)
	}
	pctx, cancel := context.WithTimeout(ctx, pingBudget)
	defer cancel()
	return errors.Join(ad.Ping(pctx), ad.Shutdown(ctx))
}

// Labels asks every configured server in parallel, each under 5 s, how it
// labels videoPath's subtitles, in entry order. A server's failure is its
// ServerLabels.Err, so Labels fails only when ctx ends. Every string is
// sanitised to one line of at most 512 bytes, whatever the server answered.
func (s *Set) Labels(ctx context.Context, videoPath string) ([]ServerLabels, error) {
	out := make([]ServerLabels, len(s.ids))
	var wg sync.WaitGroup
	for i, id := range s.ids {
		wg.Go(func() {
			out[i] = ServerLabels{Server: id}
			labels, err := s.labelsOf(ctx, id, videoPath)
			if err != nil {
				out[i].Err = bound(err.Error())
				return
			}
			for j := range labels {
				l := &labels[j]
				l.File, l.LanguageTag, l.Display = bound(l.File), bound(l.LanguageTag), bound(l.Display)
			}
			out[i].Labels = labels
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Set) labelsOf(ctx context.Context, id, videoPath string) ([]Label, error) {
	srv, err := s.lookup(id)
	if err != nil {
		return nil, err
	}
	p, err := srv.m.Map(videoPath)
	if err != nil {
		return nil, fmt.Errorf("the video path %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, labelsTimeout)
	defer cancel()
	labels, err := srv.adapter.Labels(ctx, p)
	if err != nil {
		return nil, fmt.Errorf("reading the labels: %w", err)
	}
	return labels, nil
}

// Shutdown shuts every adapter down in parallel and returns once each
// Shutdown has returned, with their errors joined, or once ctx is done. Every
// later call on the Set answers a transient "server set closed".
func (s *Set) Shutdown(ctx context.Context) error {
	s.once.Do(func() {
		close(s.closing)
		go s.shutAdapters(context.WithoutCancel(ctx))
	})
	select {
	case <-s.done:
		return s.shutErr
	case <-ctx.Done():
		return ctx.Err()
	}
}

// shutAdapters runs detached from the first Shutdown's cancellation, so a
// caller that stops waiting still lets each adapter's own Shutdown finish.
func (s *Set) shutAdapters(ctx context.Context) {
	errs := make([]error, len(s.ids))
	var wg sync.WaitGroup
	for i, id := range s.ids {
		if ad := s.byID[id].adapter; ad != nil {
			wg.Go(func() {
				if err := ad.Shutdown(ctx); err != nil {
					errs[i] = fmt.Errorf("mediaserver: server %s: %w", id, err)
				}
			})
		}
	}
	wg.Wait()
	s.shutErr = errors.Join(errs...)
	close(s.done)
}

func (srv *server) refresh(ctx context.Context, videoPath string) error {
	p, err := srv.m.Map(videoPath)
	if err != nil {
		return fmt.Errorf("the video path %w", err)
	}
	if err := srv.adapter.Refresh(ctx, p); err != nil {
		return fmt.Errorf("refreshing the video: %w", err)
	}
	return nil
}

// report raises or clears the server's alert by err, unless the caller
// cancelled the call, which says nothing about the server.
func (s *Set) report(ctx context.Context, id string, err error) {
	if errors.Is(ctx.Err(), context.Canceled) {
		return
	}
	text := ""
	if err != nil {
		text = "media server " + id + " failed: " + err.Error()
	}
	s.alerts.Reconcile(status.MediaServerAlert(id), err != nil, text)
}

// transient is httpclient's rule, or the call's own deadline having passed:
// httpclient counts a caller's deadline as final, but here it is the
// per-call bound, so a server that sent headers and stalled is retried.
func transient(ctx context.Context, err error) bool {
	return err != nil && (httpclient.IsTransient(err) || errors.Is(ctx.Err(), context.DeadlineExceeded))
}

func bound(s string) string {
	out, _ := textsafe.SanitizeSingleLineCapped(s, maxLabelBytes, labelMarker)
	return out
}
