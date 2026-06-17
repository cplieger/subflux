package activity

// Tests in this file kill surviving gremlins mutants in activity.go and
// alerts.go (unit subflux-u23). They are behavior-named and hardcode their
// expected values; each assertion's expected value depends on the exact
// operator at the targeted line. Helpers are prefixed gk_subflux_u23_.

import (
	"slices"
	"testing"
	"time"
)

// ---- shared helpers ----

func gk_subflux_u23_ids(es []Entry) []string {
	out := make([]string, len(es))
	for i := range es {
		out[i] = es[i].ID
	}
	return out
}

func gk_subflux_u23_has(ss []string, want string) bool {
	return slices.Contains(ss, want)
}

func gk_subflux_u23_doneEntry(id string, endedAt time.Time) Entry {
	tt := endedAt
	return Entry{ID: id, Action: "scan", Done: true, EndedAtVal: tt, EndedAt: &tt}
}

// ---- activity.go ----

// Kills activity.go:58:10 INCREMENT_DECREMENT (a.nextID++).
// nextID starts at 0; ++ yields ids "1","2"; -- would yield "-1","-2".
func Test_gk_subflux_u23_StartIncrementsID(t *testing.T) {
	a := New(10)
	id1 := a.Start("scan", "d", SourceManual)
	id2 := a.Start("scan", "d", SourceManual)
	if id1 != "1" {
		t.Errorf("Start() first id = %q, want %q", id1, "1")
	}
	if id2 != "2" {
		t.Errorf("Start() second id = %q, want %q", id2, "2")
	}
}

// Kills activity.go:69:14 CONDITIONALS_NEGATION (if a.index == nil, in Start's
// else branch). With a nil index, the original makes the map before writing;
// the mutated `!= nil` skips the make and writes to a nil map -> panic.
func Test_gk_subflux_u23_StartBuildsIndexWhenNil(t *testing.T) {
	a := &Log{maxItems: 5} // index intentionally left nil
	id := a.Start("scan", "d", SourceManual)
	got := a.Entries()
	if len(got) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(got))
	}
	// The index must have been created so findEntry (used by End) resolves the id.
	a.End(id)
	if !a.Entries()[0].Done {
		t.Errorf("after End(%q), entry Done = false, want true (index not built)", id)
	}
}

// Kills activity.go:119:13 CONDITIONALS_NEGATION (if detail != "").
// Non-empty detail must overwrite; empty detail must NOT overwrite.
func Test_gk_subflux_u23_ProgressDetailOnlyWhenNonEmpty(t *testing.T) {
	a := New(10)
	id := a.Start("scan", "orig", SourceManual)

	a.Progress(id, 1, 5, "updated")
	if got := a.Entries()[0].Detail; got != "updated" {
		t.Errorf("Progress(non-empty): Detail = %q, want %q", got, "updated")
	}

	a.Progress(id, 2, 5, "")
	e := a.Entries()[0]
	if e.Detail != "updated" {
		t.Errorf("Progress(empty) overwrote Detail = %q, want %q", e.Detail, "updated")
	}
	if e.Current != 2 || e.Total != 5 {
		t.Errorf("Progress counters = (%d,%d), want (2,5)", e.Current, e.Total)
	}
}

// Kills activity.go:171:27 ARITHMETIC_BASE and INVERT_NEGATIVES
// (cutoff := time.Now().Add(-maxAge)) and activity.go:174:48
// CONDITIONALS_NEGATION (a.entries[i].EndedAt != nil).
//   - "old" (1h old, done): pruned by the original; the != nil -> == nil
//     mutant keeps it (so asserting it is gone kills 174:48).
//   - "recent" (1m old, done): kept by the original; the cutoff-sign mutants
//     (now+maxAge instead of now-maxAge) prune it (so asserting it survives
//     kills both 171:27 mutants).
//   - "ongoing" (not done): always kept.
func Test_gk_subflux_u23_PruneKeepsRecentRemovesOld(t *testing.T) {
	a := New(50)
	now := time.Now()

	a.Lock()
	a.AppendEntry(gk_subflux_u23_doneEntry("old", now.Add(-1*time.Hour)))
	a.AppendEntry(gk_subflux_u23_doneEntry("recent", now.Add(-1*time.Minute)))
	a.AppendEntry(Entry{ID: "ongoing", Action: "scan"})
	a.Unlock()

	a.PruneCompleted(10 * time.Minute)

	ids := gk_subflux_u23_ids(a.Entries())
	if gk_subflux_u23_has(ids, "old") {
		t.Errorf("PruneCompleted kept the 1h-old completed entry; ids=%v (174:48 EndedAt!=nil)", ids)
	}
	if !gk_subflux_u23_has(ids, "recent") {
		t.Errorf("PruneCompleted removed the 1m-old completed entry; ids=%v (171:27 cutoff sign)", ids)
	}
	if !gk_subflux_u23_has(ids, "ongoing") {
		t.Errorf("PruneCompleted removed the ongoing entry; ids=%v", ids)
	}
}

// Kills activity.go:220:13 CONDITIONALS_NEGATION (if a.index == nil, in
// rebuildIndex). PruneCompleted always calls rebuildIndex; with a nil index
// the original makes the map first, the mutated `!= nil` writes to nil -> panic.
func Test_gk_subflux_u23_PruneRebuildsNilIndex(t *testing.T) {
	a := &Log{maxItems: 5}                          // nil index
	a.entries = []Entry{{ID: "k1", Action: "scan"}} // not done -> survives prune

	a.PruneCompleted(time.Minute)

	if got := a.Entries(); len(got) != 1 {
		t.Fatalf("Entries() len = %d, want 1", len(got))
	}
	a.End("k1")
	if !a.Entries()[0].Done {
		t.Errorf("End(\"k1\") via rebuilt index failed: Done = false, want true")
	}
}

// Kills activity.go:231:13 CONDITIONALS_NEGATION (if a.index != nil, in
// findEntry). Two entries share id "dup"; the index map keeps the last write
// (position 1), while a linear first-match scan would return position 0.
// The original consults the index (-> entry 1, Cancelled=true); the mutated
// `== nil` skips the index and scans (-> entry 0, Cancelled=false).
func Test_gk_subflux_u23_FindEntryPrefersIndex(t *testing.T) {
	a := New(10)
	a.Lock()
	a.AppendEntry(Entry{ID: "dup", Action: "first", Cancelled: false})
	a.AppendEntry(Entry{ID: "dup", Action: "second", Cancelled: true})
	a.Unlock()

	if !a.IsCancelled("dup") {
		t.Errorf("IsCancelled(\"dup\") = false, want true (findEntry must use the index, not a first-match scan)")
	}
}

// ---- alerts.go ----

// Kills alerts.go:75:60 ARITHMETIC_BASE (10*time.Minute in RecordInfo).
// The original TTL is 10m; the mutated `10/time.Minute` is 0, which falls back
// to the 1h transient default. Backdating the alert 30m makes it expired under
// the real 10m TTL (not visible) but still visible under the mutated 1h default.
func Test_gk_subflux_u23_RecordInfoTTLExpiresAt30m(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordInfo("scan complete")

	al.Lock()
	al.AlertsUnsafe()[0].Time = time.Now().Add(-30 * time.Minute)
	al.Unlock()

	vis := al.VisibleAlerts()
	if len(vis) != 0 {
		t.Errorf("VisibleAlerts() len = %d, want 0 (30m-old info alert is past its 10m TTL)", len(vis))
	}
}

// Kills alerts.go:88:10 CONDITIONALS_NEGATION (if kind == AlertPersistent, in
// AddAlert). Two persistent alerts from the same source must deduplicate into
// one (message updated). The mutated `!=` skips the dedup loop -> two alerts.
func Test_gk_subflux_u23_PersistentDeduplicates(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordPersistent("poller", "first message")
	al.RecordPersistent("poller", "second message")

	count := 0
	var msg string
	for _, a := range al.VisibleAlerts() {
		if a.Kind == AlertPersistent && a.Source == "poller" {
			count++
			msg = a.Message
		}
	}
	if count != 1 {
		t.Errorf("persistent alerts from \"poller\" = %d, want 1 (dedup on kind==AlertPersistent)", count)
	}
	if msg != "second message" {
		t.Errorf("deduped persistent message = %q, want %q", msg, "second message")
	}
}

// Kills alerts.go:100:11 INCREMENT_DECREMENT (al.nextID++ in AddAlert).
// nextID starts at 0; ++ yields ids 1,2; -- would yield -1,-2.
func Test_gk_subflux_u23_AlertIDIncrements(t *testing.T) {
	al := NewAlertLog(10)
	al.Record("poller", "err one")
	al.Record("poller", "err two")

	vis := al.VisibleAlerts()
	if len(vis) != 2 {
		t.Fatalf("VisibleAlerts() len = %d, want 2", len(vis))
	}
	if vis[0].ID != 1 {
		t.Errorf("first alert ID = %d, want 1", vis[0].ID)
	}
	if vis[1].ID != 2 {
		t.Errorf("second alert ID = %d, want 2", vis[1].ID)
	}
}

// Kills alerts.go:115:22 CONDITIONALS_NEGATION (if al.alerts[i].ID == id, in
// Dismiss). Dismiss(2) must mark the ID=2 alert (not ID=1). The mutated `!=`
// dismisses the first non-matching alert (ID=1) instead.
func Test_gk_subflux_u23_DismissMatchesByID(t *testing.T) {
	al := NewAlertLog(10)
	al.Record("a", "first")  // ID 1
	al.Record("b", "second") // ID 2

	if ok := al.Dismiss(2); !ok {
		t.Fatalf("Dismiss(2) = false, want true")
	}

	al.RLock()
	defer al.RUnlock()
	for _, a := range al.AlertsUnsafe() {
		switch a.ID {
		case 2:
			if !a.Dismissed {
				t.Errorf("alert ID=2 Dismissed = false, want true")
			}
		case 1:
			if a.Dismissed {
				t.Errorf("alert ID=1 Dismissed = true, want false (only ID=2 should be dismissed)")
			}
		}
	}
}

// Kills alerts.go:147:13 CONDITIONALS_NEGATION (if a.Kind == AlertPersistent,
// in VisibleAlerts). A persistent alert is always visible regardless of age.
// The mutated `!=` routes persistent alerts through the TTL check (default 1h),
// so a 2h-old persistent alert would be hidden.
func Test_gk_subflux_u23_PersistentAlwaysVisible(t *testing.T) {
	al := NewAlertLog(10)
	al.RecordPersistent("poller", "down")

	al.Lock()
	al.AlertsUnsafe()[0].Time = time.Now().Add(-2 * time.Hour)
	al.Unlock()

	found := false
	for _, a := range al.VisibleAlerts() {
		if a.Kind == AlertPersistent && a.Source == "poller" {
			found = true
		}
	}
	if !found {
		t.Errorf("VisibleAlerts() dropped a 2h-old persistent alert; persistent alerts must ignore TTL")
	}
}
