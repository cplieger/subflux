package api

import (
	"encoding/json"
	"testing"
)

func FuzzHistoryEventTypeUnmarshal(f *testing.F) {
	f.Add([]byte(`"grabbed"`))
	f.Add([]byte(`"downloadFolderImported"`))
	f.Add([]byte(`3`))
	f.Add([]byte(`null`))
	f.Add([]byte(`""`))
	f.Add([]byte(`"unknown"`))
	f.Add([]byte(`1`))
	f.Add([]byte(`{`))
	f.Add([]byte{0xff, 0xfe})

	f.Fuzz(func(t *testing.T, data []byte) {
		var h HistoryEventType
		err := json.Unmarshal(data, &h)
		if err == nil {
			// Round-trip: marshal back and re-unmarshal should produce the
			// same HistoryEventType. Any drift (e.g. a future regression in
			// the int/string dual-shape unmarshal) would surface here.
			data2, _ := json.Marshal(h)
			var h2 HistoryEventType
			if err2 := json.Unmarshal(data2, &h2); err2 != nil {
				t.Fatalf("round-trip unmarshal failed: %v", err2)
			}
			if h != h2 {
				t.Fatalf("round-trip mismatch: %d vs %d", h, h2)
			}
		}
	})
}
