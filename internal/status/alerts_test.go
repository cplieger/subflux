package status

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/cplieger/slogx/capture"
)

func alerts(s *Store) map[AlertSource]Alert {
	snap, _ := s.Snapshot()
	out := map[AlertSource]Alert{}
	for _, a := range snap.Alerts {
		out[a.Source] = a
	}
	return out
}

func TestAlertsAreLevels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore()
		const n = 500
		for i := range n {
			s.Reconcile(MediaRootAlert(fmt.Sprintf("/media/%d", i)), true, "unreachable")
		}
		first := alerts(s)[MediaRootAlert("/media/0")].Since
		time.Sleep(time.Hour)
		for range 3 {
			for i := range n {
				s.Reconcile(MediaRootAlert(fmt.Sprintf("/media/%d", i)), true, "still unreachable")
			}
		}
		got := alerts(s)
		if len(got) != n {
			t.Fatalf("%d alerts after re-asserts, want %d", len(got), n)
		}
		if a := got[MediaRootAlert("/media/0")]; !a.Since.Equal(first) || a.Text != "still unreachable" {
			t.Errorf("re-asserted alert %+v; want Since %v kept and the new text", a, first)
		}
		s.Reconcile(MediaRootAlert("/media/7"), false, "")
		got = alerts(s)
		if _, ok := got[MediaRootAlert("/media/7")]; ok || len(got) != n-1 {
			t.Errorf("clearing one left %d alerts, cleared present %v; want %d", len(got), ok, n-1)
		}
	})
}

func TestReconcileAcceptsEveryFixedSource(t *testing.T) {
	for f := range fixedSources {
		t.Run(string(f), func(t *testing.T) {
			rec := capture.Default(t)
			s := newStore()
			s.Reconcile(f, true, "raised")
			if a, ok := alerts(s)[f]; !ok || a.Text != "raised" {
				t.Errorf("alerts %v; want %q raised", alerts(s), f)
			}
			if n := rec.CountLevel(slog.LevelError, "status:"); n != 0 {
				t.Errorf("logged %q, want no ERROR", rec.Messages())
			}
			s.Reconcile(f, false, "")
			if _, ok := alerts(s)[f]; ok {
				t.Errorf("%q still raised after its clear", f)
			}
		})
	}
}

func TestReconcileCutsText(t *testing.T) {
	s := newStore()
	s.Reconcile(AlertStore, true, strings.Repeat("disk full\n", 100)+"\x1b[31m")
	a := alerts(s)[AlertStore]
	if len(a.Text) > maxAlertText || strings.ContainsAny(a.Text, "\n\x1b") || !utf8.ValidString(a.Text) {
		t.Errorf("text %q is not one line within %d bytes", a.Text, maxAlertText)
	}
	if !strings.HasPrefix(a.Text, "disk full disk full") {
		t.Errorf("text %q lost its start", a.Text)
	}
}

func TestRetainAlertsDropsLaterRaise(t *testing.T) {
	keep, gone := ProviderAlert("keep"), ProviderAlert("gone")
	t.Run("a retained kind drops its other sources", func(t *testing.T) {
		s := newStore()
		s.Reconcile(keep, true, "down")
		s.Reconcile(gone, true, "down")
		s.Reconcile(SourceAlert("sonarr"), true, "down")
		s.RetainAlerts("provider", []AlertSource{keep})
		got := alerts(s)
		if _, ok := got[gone]; ok {
			t.Error("a source outside the live set survived RetainAlerts")
		}
		if _, ok := got[keep]; !ok {
			t.Error("a live source was cleared")
		}
		s.Reconcile(gone, true, "late raise")
		if _, ok := alerts(s)[gone]; ok {
			t.Error("a later raise outside the live set was kept")
		}
		s.Reconcile(SourceAlert("radarr"), true, "down")
		if len(alerts(s)) != 3 {
			t.Errorf("alerts %v; a kind never retained must accept any source", alerts(s))
		}
	})
	faults := []struct {
		name string
		act  func(*Store)
	}{
		{"retaining a fixed kind", func(s *Store) { s.RetainAlerts("config", nil) }},
		{"retaining with a source of another kind", func(s *Store) { s.RetainAlerts("provider", []AlertSource{SourceAlert("x")}) }},
		{"reconciling an unknown kind", func(s *Store) { s.Reconcile("bogus:x", true, "typo") }},
	}
	for _, f := range faults {
		t.Run(f.name, func(t *testing.T) {
			rec := capture.Default(t)
			s := newStore()
			s.Reconcile(AlertConfig, true, "bad file")
			s.Reconcile(gone, true, "down")
			before, seq := s.Snapshot()
			f.act(s)
			after, seq2 := s.Snapshot()
			if seq2 != seq || len(after.Alerts) != len(before.Alerts) {
				t.Errorf("a fault changed the store: seq %d to %d, alerts %v to %v", seq, seq2, before.Alerts, after.Alerts)
			}
			if rec.CountLevel(slog.LevelError, "status:") != 1 {
				t.Errorf("logged %q, want one ERROR", rec.Messages())
			}
			s.Reconcile(ProviderAlert("other"), true, "down")
			if _, ok := alerts(s)[ProviderAlert("other")]; !ok {
				t.Error("a refused RetainAlerts still retained its kind")
			}
		})
	}
}
