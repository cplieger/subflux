package activity

import "testing"

func FuzzActivityLogStartEnd(f *testing.F) {
	f.Add("scan", "searching library", "scheduled")
	f.Add("", "", "manual")
	f.Add("download", "opensubtitles", "scheduled")

	f.Fuzz(func(t *testing.T, action, detail, source string) {
		log := New(50)
		src := SourceScheduled
		if source == "manual" {
			src = SourceManual
		}
		id := log.Start(action, detail, src)
		if id == "" {
			t.Fatal("Start returned empty ID")
		}
		log.Progress(id, 1, 10, detail)
		log.End(id)
		entries := log.Entries()
		if len(entries) == 0 {
			t.Fatal("no entries after Start+End")
		}
	})
}

func FuzzAlertLogAddDismiss(f *testing.F) {
	f.Add("poller", "connection failed", "error")
	f.Add("", "", "warn")
	f.Add("scan", "completed", "info")

	f.Fuzz(func(t *testing.T, source, message, level string) {
		al := NewAlertLog(20)
		switch level {
		case "warn":
			al.RecordWarn(source, message)
		case "info":
			al.RecordInfo(message)
		default:
			al.Record(source, message)
		}
		visible := al.VisibleAlerts()
		if len(visible) == 0 {
			t.Fatal("no visible alerts after Record")
		}
		al.Dismiss(visible[0].ID)
	})
}
