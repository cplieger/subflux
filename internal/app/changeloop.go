package app

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cplieger/go/slogx"
	"github.com/cplieger/subflux/internal/accounts"
	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/status"
)

// changeSource is what the loop reads; *settings.Service implements it.
type changeSource interface {
	Changed() <-chan struct{}
	Snapshot() settings.Snapshot
	Diverged() bool
}

var _ changeSource = (*settings.Service)(nil)

// exposure is what a server change resets; *clientaddr.Detector implements
// it.
type exposure interface{ Reset() }

var _ exposure = (*clientaddr.Detector)(nil)

// changeLoop is settings.Service.Changed's one reader. Per commit, and per
// Diverged change, it applies the live snapshot: the log level, the
// config_mode gauge, the config alert while the file holds settings that are
// not live, restart_pending (reconcileRestart) and an exposure reset when the
// server section differs from server, the one last applied (boot's, before
// run). Last it calls applied, which must not block; a nil applied is skipped.
type changeLoop struct {
	svc        changeSource
	level      *slog.LevelVar
	metrics    *obs.Metrics
	alerts     *status.Store
	exposure   exposure
	applied    func()
	server     settings.Server
	running    slogx.Format
	serverOK   bool
	bootBypass bool
}

func (l *changeLoop) run(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-l.svc.Changed():
			snap := l.svc.Snapshot()
			srv, ok := snap.Server()
			if ok {
				l.level.Set(srv.LogLevel())
			}
			if ok != l.serverOK || !srv.Equal(l.server) {
				l.exposure.Reset()
				l.server, l.serverOK = srv, ok
			}
			l.metrics.SetConfigMode(modeName(snap))
			l.alerts.Reconcile(status.AlertConfig, l.svc.Diverged(), "config.yaml holds settings that are not live; save the settings to replace it")
			l.reconcileRestart(snap)
			if l.applied != nil {
				l.applied()
			}
		}
	}
}

// reconcileRestart raises restart_pending, one alert naming each pending
// change, while snap asks for a log format other than the running handler's
// or config.yaml's auth section carries disable_auth: true with the booted
// bypass off. A booted bypass that is on came from a stored true, which
// BootOnly holds every Save to, so the arm has one direction. A section that
// failed to parse carries no value until a Save commits one.
func (l *changeLoop) reconcileRestart(snap settings.Snapshot) {
	var pending []string
	if logFormat(snap) != l.running {
		pending = append(pending, "the log format changes at the next restart")
	}
	if accounts.BypassEnabled(snap.FileAuth()) && !l.bootBypass {
		pending = append(pending, "config.yaml's auth.disable_auth: true turns authentication off at a restart that accepts the auth section")
	}
	l.alerts.Reconcile(status.AlertRestartPending, len(pending) > 0, strings.Join(pending, "; "))
}
