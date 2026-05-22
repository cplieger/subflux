package events

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// DefaultMaxSSEClients is the upper bound on concurrent SSE connections
// when no config is loaded or the configured value is zero.
const DefaultMaxSSEClients = 32

// SSEKeepaliveInterval is the time between SSE keepalive comments.
// Set below common proxy idle timeouts (nginx 60s, ALB 120s) to
// prevent connection drops behind reverse proxies.
const SSEKeepaliveInterval = 15 * time.Second

// HandleEvents streams server-sent events to the browser.
// maxClients is the configured limit (0 means use DefaultMaxSSEClients).
func HandleEvents(bus *EventBus, maxClients int, w http.ResponseWriter, r *http.Request) {
	limit := maxClients
	if limit <= 0 {
		limit = DefaultMaxSSEClients
	}
	if bus.ClientCount() >= limit {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprint(w, `{"error":"too many SSE clients"}`)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":"streaming not supported"}`)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	// no-transform per RFC 7234 §5.2.2.4 prevents intermediaries (Caddy,
	// nginx, ALB, Cloudflare) from wrapping the stream in gzip. Caddy's
	// isEncodeAllowed in modules/caddyhttp/encode/encode.go checks for it.
	// Without it a compressing proxy can buffer per-event flushes and break
	// live event delivery.
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering

	// Disable write deadline for this long-lived connection.
	rc := http.NewResponseController(w)
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("SSE: failed to clear write deadline", "error", err)
	}
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		slog.Warn("SSE: failed to clear read deadline", "error", err)
	}

	sub, unsub := bus.Subscribe()
	defer unsub()

	// Send initial keepalive so the client knows the connection is live.
	if _, err := fmt.Fprint(w, ": connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	// Keepalive ticker to prevent proxy/load balancer timeouts.
	ticker := time.NewTicker(SSEKeepaliveInterval)
	defer ticker.Stop()

	slog.Debug("SSE client connected",
		"clients", bus.ClientCount())
	defer func() {
		slog.Debug("SSE client disconnected",
			"clients", bus.ClientCount())
	}()

	// Reuse a single buffer+encoder per connection to avoid allocating
	// a new []byte on every json.Marshal call.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	for {
		// Drain any pending events from the ring buffer.
		for {
			evt, ok := sub.Next()
			if !ok {
				break
			}
			buf.Reset()
			if err := enc.Encode(evt); err != nil {
				slog.Warn("SSE: failed to marshal event", "type", evt.Type, "error", err)
				continue
			}
			// enc.Encode appends a newline; trim it for SSE data field.
			data := bytes.TrimRight(buf.Bytes(), "\n")
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", evt.Type, data); err != nil {
				return
			}
		}
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-bus.WaitCh():
			// New event(s) available — loop back to drain.
		}
	}
}
