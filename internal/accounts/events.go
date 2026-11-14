package accounts

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/cplieger/access/accesshttp"
	"github.com/cplieger/go/textsafe"
)

// auditQueue is the audit sink's bound: an event past it is counted and
// dropped.
const auditQueue = 1_024

// dropWarnEvery is the shortest interval between two WARN lines about
// dropped audit events.
const dropWarnEvery = time.Minute

// auditSink is accesshttp's Audit hook: a bounded queue Run drains into INFO
// lines. Enqueueing never blocks, so a burst of refused requests never waits
// on log output.
type auditSink struct {
	warned  time.Time
	events  chan accesshttp.AuditEvent
	metrics droppedCounter
	logger  *slog.Logger
	mu      sync.Mutex
}

func newAuditSink(m droppedCounter, l *slog.Logger) *auditSink {
	return &auditSink{events: make(chan accesshttp.AuditEvent, auditQueue), metrics: m, logger: l}
}

// enqueue queues e, or counts it dropped when the queue is full.
func (a *auditSink) enqueue(_ *http.Request, e accesshttp.AuditEvent) { //nolint:gocritic // hugeParam: accesshttp's Audit signature
	select {
	case a.events <- e:
		return
	default:
	}
	a.metrics.IncAuthEventsDropped()
	a.mu.Lock()
	now := time.Now()
	warn := a.warned.IsZero() || now.Sub(a.warned) >= dropWarnEvery
	if warn {
		a.warned = now
	}
	a.mu.Unlock()
	if warn {
		a.logger.Warn("accounts: the audit queue is full; audit events are being dropped", "queue", auditQueue)
	}
}

// run logs each event until ctx ends, then the events already queued.
func (a *auditSink) run(ctx context.Context) {
	for {
		select {
		case e := <-a.events:
			a.log(&e)
		case <-ctx.Done():
			for {
				select {
				case e := <-a.events:
					a.log(&e)
				default:
					return
				}
			}
		}
	}
}

// log writes e as one INFO line, every string made single-line safe: a
// username or reason can carry text a client chose.
func (a *auditSink) log(e *accesshttp.AuditEvent) {
	a.logger.Info("accounts: audit",
		"kind", textsafe.SanitizeSingleLine(e.Kind),
		"success", e.Success,
		"username", textsafe.SanitizeSingleLine(e.Username),
		"user", e.UserID,
		"ip", textsafe.SanitizeSingleLine(e.IP),
		"method", textsafe.SanitizeSingleLine(string(e.Method)),
		"reason", textsafe.SanitizeSingleLine(e.Reason),
		"session", textsafe.SanitizeSingleLine(e.SessionID),
		"key", e.KeyID,
	)
}
