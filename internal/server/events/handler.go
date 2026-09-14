package events

import (
	"encoding/json"
	"net/http"

	"github.com/cplieger/sse"
)

// DefaultMaxSSEClients is the upper bound on concurrent SSE connections
// when no config is loaded or the configured value is zero.
const DefaultMaxSSEClients = 32

// Handle streams server-sent events to the browser. The SSE-Wire header
// decides the overlap: a connection without it is a pre-v3 EventSource and
// receives the legacy `epoch` frame built from the hello, after the replay
// and before live delivery, with no id. The SSE-Client header is handed to
// the hub raw, absent or not; the library owns its grammar and reads the
// empty string as no tag. Cursor parsing, the verdict, replay, admission
// (503 over the cap or after shutdown), headers, keepalives and frame
// encoding are the library's.
func Handle(bus *EventBus, w http.ResponseWriter, r *http.Request) {
	legacy := r.Header.Get("SSE-Wire") == ""
	bus.hub.Serve(w, r,
		sse.WithClientTag(r.Header.Get("SSE-Client")),
		sse.OnConnect(func(sw *sse.Writer, h sse.Hello) error {
			bus.metrics.RecordSSEConnect(string(h.Verdict), legacy)
			if !legacy {
				return nil
			}
			data, err := json.Marshal(Event{Type: Epoch, Data: EpochEvent{
				BootID: h.Epoch,
				Head:   h.Head,
				Gap:    !h.Resumed,
			}})
			if err != nil {
				return err
			}
			return sw.Event(string(Epoch), data)
		}),
	)
}
