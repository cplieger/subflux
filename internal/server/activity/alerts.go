package activity

import (
	"sync"
	"time"
)

// AlertLog tracks actionable errors for the UI.
type AlertLog struct {
	onRaise   func(Alert)
	onDismiss func(Alert)
	alerts    []Alert
	nextID    int
	max       int
	mu        sync.RWMutex
}

// AlertKind controls how an alert is displayed and dismissed.
type AlertKind string

const (
	// AlertPersistent requires manual dismissal by the user.
	AlertPersistent AlertKind = "persistent"
	// AlertTransient auto-expires after TransientAlertTTL.
	AlertTransient AlertKind = "transient"
)

// TransientAlertTTL is the default TTL for transient alerts.
const TransientAlertTTL = 1 * time.Hour

// AlertLevel is a typed string for alert severity levels.
type AlertLevel string

// Alert level constants.
const (
	LevelError AlertLevel = "error"
	LevelWarn  AlertLevel = "warn"
	LevelInfo  AlertLevel = "info"
)

// Alert represents an actionable error or informational message.
type Alert struct {
	Time      time.Time     `json:"time"`
	Level     AlertLevel    `json:"level"` // "error", "warn", "info"
	Message   string        `json:"message"`
	Source    string        `json:"source"`
	Kind      AlertKind     `json:"kind"`
	TTL       time.Duration `json:"-"` // per-alert TTL override; 0 = use default
	ID        int           `json:"id"`
	Dismissed bool          `json:"dismissed"`
}

// NewAlertLog creates an AlertLog with the given max capacity.
func NewAlertLog(capacity int) *AlertLog {
	return &AlertLog{max: capacity}
}

// SetOnRaise installs the observer called with the recorded alert snapshot
// after every raise — a new alert and a refreshed persistent one alike. The
// hook is fired AFTER the log's lock is released (the AlertLog cannot import
// the events bus; the server injects the publisher here). TTL expiry and cap
// eviction fire nothing: both stay reconcile-only on the client.
func (al *AlertLog) SetOnRaise(fn func(Alert)) {
	al.mu.Lock()
	defer al.mu.Unlock()
	al.onRaise = fn
}

// SetOnDismiss installs the observer called with the dismissed alert
// snapshot for every dismissal — the by-id endpoint and DismissBySource
// alike — fired AFTER the lock is released, like SetOnRaise.
func (al *AlertLog) SetOnDismiss(fn func(Alert)) {
	al.mu.Lock()
	defer al.mu.Unlock()
	al.onDismiss = fn
}

// alertNotify collects one hook's calls decided under the AlertLog's lock
// and fires them after the lock is released (deferred before the lock, so
// the LIFO defer order runs it outside the critical section).
type alertNotify struct {
	fn     func(Alert)
	queued []Alert
}

func (n *alertNotify) fire() {
	for i := range n.queued {
		n.fn(n.queued[i])
	}
}

// queue records one alert snapshot for fn; a nil fn queues nothing.
func (n *alertNotify) queue(fn func(Alert), a *Alert) {
	if fn == nil {
		return
	}
	n.fn = fn
	n.queued = append(n.queued, *a)
}

// Record adds a transient error alert.
func (al *AlertLog) Record(source, message string) {
	al.AddAlert(source, message, AlertTransient, LevelError, 0)
}

// RecordWarn adds a transient warning alert.
func (al *AlertLog) RecordWarn(source, message string) {
	al.AddAlert(source, message, AlertTransient, LevelWarn, 0)
}

// RecordInfo adds a short-lived informational alert for scan results.
func (al *AlertLog) RecordInfo(message string) {
	al.AddAlert("scan", message, AlertTransient, LevelInfo, 10*time.Minute)
}

// RecordPersistent adds a persistent error that requires manual dismissal.
func (al *AlertLog) RecordPersistent(source, message string) {
	al.AddAlert(source, message, AlertPersistent, LevelError, 0)
}

// AddAlert appends an alert with the given level and optional TTL override.
func (al *AlertLog) AddAlert(source, message string, kind AlertKind, level AlertLevel, ttl time.Duration) {
	var n alertNotify
	defer n.fire()
	al.mu.Lock()
	defer al.mu.Unlock()

	if kind == AlertPersistent {
		for i := range al.alerts {
			if al.alerts[i].Source == source &&
				al.alerts[i].Kind == AlertPersistent &&
				!al.alerts[i].Dismissed {
				al.alerts[i].Message = message
				al.alerts[i].Time = time.Now()
				n.queue(al.onRaise, &al.alerts[i])
				return
			}
		}
	}

	al.nextID++
	al.alerts = append(al.alerts, Alert{
		ID: al.nextID, Level: level, Source: source,
		Message: message, Kind: kind, TTL: ttl, Time: time.Now(),
	})
	if len(al.alerts) > al.max {
		// Cap eviction is deliberately event-less (reconcile-only), like TTL
		// expiry: the raise below is the only delta this mutation publishes.
		al.alerts = al.alerts[len(al.alerts)-al.max:]
	}
	n.queue(al.onRaise, &al.alerts[len(al.alerts)-1])
}

// Dismiss marks an alert as dismissed by ID.
func (al *AlertLog) Dismiss(id int) bool {
	var n alertNotify
	defer n.fire()
	al.mu.Lock()
	defer al.mu.Unlock()
	for i := range al.alerts {
		if al.alerts[i].ID == id {
			al.alerts[i].Dismissed = true
			n.queue(al.onDismiss, &al.alerts[i])
			return true
		}
	}
	return false
}

// DismissBySource dismisses all undismissed persistent alerts from a source.
func (al *AlertLog) DismissBySource(source string) {
	var n alertNotify
	defer n.fire()
	al.mu.Lock()
	defer al.mu.Unlock()
	for i := range al.alerts {
		if al.alerts[i].Source == source &&
			al.alerts[i].Kind == AlertPersistent &&
			!al.alerts[i].Dismissed {
			al.alerts[i].Dismissed = true
			n.queue(al.onDismiss, &al.alerts[i])
		}
	}
}

// VisibleAlerts returns a copy of non-dismissed, non-expired alerts.
// It is the only read path: the log never hands out its internal slice, and
// its mutex stays private (tests that need the raw state live in this package).
func (al *AlertLog) VisibleAlerts() []Alert {
	al.mu.RLock()
	defer al.mu.RUnlock()

	now := time.Now()
	visible := []Alert{}
	for _, a := range al.alerts {
		if a.Dismissed {
			continue
		}
		if a.Kind == AlertPersistent {
			visible = append(visible, a)
			continue
		}
		ttl := a.TTL
		if ttl == 0 {
			ttl = TransientAlertTTL
		}
		if now.Sub(a.Time) < ttl {
			visible = append(visible, a)
		}
	}
	return visible
}
