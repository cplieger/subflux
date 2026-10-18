package status

import (
	"log/slog"
	"slices"
	"time"

	"github.com/cplieger/runesafe/v3"
)

// maxAlertText bounds an alert's text and a provider's reason, in bytes.
const maxAlertText = 512

// Alert is a level: present while its owner reports it active.
type Alert struct {
	Since  time.Time
	Source AlertSource
	Text   string
}

// Reconcile sets source's alert when active, keeping Since across a
// re-assert, and clears it otherwise. text is cut to one line of at most 512
// bytes. A source no constructor yields is dropped with ERROR, and a raise of
// a retained kind outside its live set is dropped.
func (s *Store) Reconcile(source AlertSource, active bool, text string) {
	kind, ok := AlertKind(source)
	if !ok {
		slog.Error("status: dropped an alert of an unknown kind", "source", string(source))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, exists := s.alerts[source]
	if !active {
		if exists {
			delete(s.alerts, source)
			s.bump()
		}
		return
	}
	if live, retained := s.retained[kind]; retained && !live[source] {
		return
	}
	text = cutText(text)
	if exists && cur.Text == text {
		return
	}
	since := time.Now()
	if exists {
		since = cur.Since
	}
	s.alerts[source] = Alert{Source: source, Text: text, Since: since}
	s.bump()
}

// RetainAlerts clears every alert of kind, a per-entity kind, whose source is
// not in live, and from then on Reconcile drops a raise of that kind outside
// live; a kind never retained accepts every source. A fixed or unknown kind,
// or a live source of another kind, is a programming fault: logged at ERROR,
// and nothing changes.
func (s *Store) RetainAlerts(kind string, live []AlertSource) {
	if !slices.Contains(entityKinds, kind) {
		slog.Error("status: alerts of this kind are not retained by configuration", "kind", kind)
		return
	}
	set := make(map[AlertSource]bool, len(live))
	for _, src := range live {
		if k, ok := AlertKind(src); !ok || k != kind {
			slog.Error("status: a retained source is not of its kind", "kind", kind, "source", string(src))
			return
		}
		set[src] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retained[kind] = set
	cleared := false
	for src := range s.alerts {
		if k, _ := AlertKind(src); k == kind && !set[src] {
			delete(s.alerts, src)
			cleared = true
		}
	}
	if cleared {
		s.bump()
	}
}

func cutText(text string) string {
	out, _ := runesafe.SanitizeSingleLineCapped(text, maxAlertText, "…")
	return out
}
