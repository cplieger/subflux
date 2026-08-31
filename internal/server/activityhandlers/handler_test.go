package activityhandlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/activityhandlers"
	"github.com/cplieger/subflux/internal/server/events"
)

// The activity GET is a pure read: retention belongs to the server's prune
// ticker alone (one owner), so an entry completed longer than PruneAge ago
// is still listed — and still in the log — after a read. Under the old
// read-path prune this test fails: the handler would remove the entry.
func TestHandleGetActivity_does_not_prune(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log := activity.New(10)
		removed := 0
		log.SetOnRemove(func(activity.Entry) { removed++ })
		h := activityhandlers.New(activityhandlers.Deps{
			Activity: log,
			Alerts:   activity.NewAlertLog(10),
			Stops:    &activity.StopRegistry{},
			Events:   events.New(1),
		})
		log.End(log.Start("Scan", "d", activity.SourceManual))

		time.Sleep(activity.DefaultPruneAge + time.Minute) // fake clock: well past retention

		rec := httptest.NewRecorder()
		h.HandleGetActivity(rec, httptest.NewRequest(http.MethodGet, "/api/activity", nil))

		var got []activity.Entry
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode activity response: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("GET /api/activity listed %d entries, want 1 (the read path must not prune)", len(got))
		}
		if n := len(log.Entries()); n != 1 {
			t.Errorf("log holds %d entries after the read, want 1", n)
		}
		if removed != 0 {
			t.Errorf("the read fired %d remove events, want 0", removed)
		}
	})
}
