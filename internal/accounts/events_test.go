package accounts

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/access/account"
	"github.com/cplieger/go/slogx/capture"
)

func warnings(rec *capture.Recorder, sub string) int {
	n := 0
	for _, r := range rec.Records() {
		if r.Level == slog.LevelWarn && strings.Contains(r.Message, sub) {
			n++
		}
	}
	return n
}

func TestAuditQueueNeverBlocks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger, rec := capture.New()
		dropped := &droppedCount{}
		a := newAuditSink(dropped, logger)
		for range auditQueue + 1 {
			a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure"})
		}
		if got := dropped.n.Load(); got != 1 {
			t.Errorf("dropped = %d, want 1", got)
		}
		a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure"})
		time.Sleep(dropWarnEvery - time.Second)
		a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure"})
		if got := warnings(rec, "audit queue is full"); got != 1 {
			t.Errorf("WARN lines within a minute of drops = %d, want 1", got)
		}
		time.Sleep(time.Second)
		a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure"})
		if got := warnings(rec, "audit queue is full"); got != 2 {
			t.Errorf("WARN lines a minute after the first = %d, want 2", got)
		}
		if got := dropped.n.Load(); got != 4 {
			t.Errorf("dropped = %d, want 4", got)
		}
	})
	t.Run("the queue is logged single-line safe when Run stops", func(t *testing.T) {
		logger, rec := capture.New()
		a := newAuditSink(&droppedCount{}, logger)
		a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure", Username: "eve\x1b[31m\nforged", IP: "192.0.2.1", Reason: "invalid_credentials"})
		for range 99 {
			a.enqueue(nil, accesshttp.AuditEvent{Kind: "login.failure"})
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		a.run(ctx)
		logged := 0
		for _, r := range rec.Records() {
			if r.Message == "accounts: audit" {
				logged++
			}
		}
		if logged != 100 {
			t.Errorf("events logged when Run stopped = %d, want the 100 queued", logged)
		}
		got, ok := rec.AttrValue("accounts: audit", "username")
		if !ok {
			t.Fatal("the queued event was not logged when Run stopped")
		}
		if strings.ContainsAny(got, "\x1b\n") {
			t.Errorf("username logged as %q, want no escape or line break", got)
		}
		if ip, _ := rec.AttrValue("accounts: audit", "ip"); ip != "192.0.2.1" {
			t.Errorf("ip = %q, want 192.0.2.1", ip)
		}
	})
	t.Run("compose feeds no account event to the sink", func(t *testing.T) {
		var onEvent bool
		swap(t, &newAccount, func(ctx context.Context, c account.Config) (*account.Service, error) {
			onEvent = c.OnEvent != nil
			return account.New(ctx, c)
		})
		memService(t, newEnv())
		if onEvent {
			t.Error("account.Config.OnEvent is set; account events belong to the security log")
		}
	})
}
