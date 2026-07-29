package activity

import (
	"sync"
	"time"
)

// WarnRecorder is the narrow interface for recording warning alerts.
// Both manualops and polling consume this single-method contract.
// The concrete type AlertLog satisfies it via structural typing.
type WarnRecorder interface {
	RecordWarn(source, msg string)
}

// AlertLog tracks actionable errors for the UI.
type AlertLog struct {
	alerts []Alert
	nextID int
	max    int
	mu     sync.RWMutex
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
	al.mu.Lock()
	defer al.mu.Unlock()

	if kind == AlertPersistent {
		for i := range al.alerts {
			if al.alerts[i].Source == source &&
				al.alerts[i].Kind == AlertPersistent &&
				!al.alerts[i].Dismissed {
				al.alerts[i].Message = message
				al.alerts[i].Time = time.Now()
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
		al.alerts = al.alerts[len(al.alerts)-al.max:]
	}
}

// Dismiss marks an alert as dismissed by ID.
func (al *AlertLog) Dismiss(id int) bool {
	al.mu.Lock()
	defer al.mu.Unlock()
	for i := range al.alerts {
		if al.alerts[i].ID == id {
			al.alerts[i].Dismissed = true
			return true
		}
	}
	return false
}

// DismissBySource dismisses all undismissed persistent alerts from a source.
func (al *AlertLog) DismissBySource(source string) {
	al.mu.Lock()
	defer al.mu.Unlock()
	for i := range al.alerts {
		if al.alerts[i].Source == source &&
			al.alerts[i].Kind == AlertPersistent &&
			!al.alerts[i].Dismissed {
			al.alerts[i].Dismissed = true
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
