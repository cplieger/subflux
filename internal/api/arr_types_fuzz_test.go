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
		if err == nil && h != 0 {
			// When successfully unmarshalled to a non-zero value,
			// it should be a valid positive integer.
			if h < 0 {
				t.Fatalf("unexpected negative event type: %d", h)
			}
		}
	})
}
