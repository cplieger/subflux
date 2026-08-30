package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/cplieger/webhttp/v2"
)

// gzipMinBytes is the compression floor: an eligible JSON body must exceed
// this many bytes before the delayed-commit writer chooses gzip; at or under
// it the response commits identity, untouched.
const gzipMinBytes = 1024

// gzipWriterPool recycles gzip writers: a flate compressor allocates large
// hash tables, so per-response construction would dominate small responses.
var gzipWriterPool = sync.Pool{
	New: func() any { return gzip.NewWriter(io.Discard) },
}

// gzipMW returns the delayed-commit gzip middleware (A8). Placed INSIDE
// webhttp.Recoverer: a panic before commit discards the buffer and unwinds,
// so Recoverer's 500 goes out on a clean wire; a panic after a compressed
// commit seals the gzip stream best-effort and unwinds, and Recoverer skips
// its 500 because the response is committed. Exempt routes are
// short-circuited before wrapping (gzipExemptPath).
func gzipMW() webhttp.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if gzipExemptPath(r.URL.Path) || r.Method == http.MethodHead || !acceptsGzip(r) {
				next.ServeHTTP(w, r)
				return
			}
			gw := &gzipResponseWriter{rw: w}
			handlerDone := false
			// No recover here: the deferred cleanup runs during the unwind
			// and the panic continues to Recoverer untouched.
			defer func() {
				if !handlerDone {
					gw.abort()
				}
			}()
			next.ServeHTTP(gw, r)
			handlerDone = true
			gw.finish()
		})
	}
}

// gzipExemptPath reports whether the route bypasses the gzip wrapper
// entirely: the SSE hub needs the raw writer, unbuffered from the first byte.
func gzipExemptPath(path string) bool {
	return path == "/api/events"
}

// acceptsGzip reports whether Accept-Encoding offers gzip with a non-zero
// quality, by name or via "*" (an explicit gzip entry wins over the
// wildcard). A malformed q= reads as accepting — the client named the coding.
// Mirrors webhttp's static-asset negotiation so the two gzip surfaces agree.
func acceptsGzip(r *http.Request) bool {
	wildcard := false
	for part := range strings.SplitSeq(r.Header.Get("Accept-Encoding"), ",") {
		name, offered := encodingOffer(part)
		switch {
		case strings.EqualFold(name, "gzip"):
			return offered
		case name == "*":
			wildcard = offered
		}
	}
	return wildcard
}

// encodingOffer parses one Accept-Encoding list element into its coding name
// and whether that coding is offered: false only for a well-formed q=0
// refusal.
func encodingOffer(part string) (name string, offered bool) {
	token, qual := part, "1"
	if i := strings.IndexByte(part, ';'); i >= 0 {
		token = part[:i]
		if j := strings.Index(part[i:], "q="); j >= 0 {
			qual = part[i+j+2:]
			// A q-value may be followed by further parameters; cut at the
			// next ';' so a well-formed q=0 is not misread as malformed.
			if k := strings.IndexByte(qual, ';'); k >= 0 {
				qual = qual[:k]
			}
		}
	}
	q, err := strconv.ParseFloat(strings.TrimSpace(qual), 64)
	return strings.TrimSpace(token), err != nil || q != 0
}

// gzipEligible reports whether the recorded response may still choose gzip:
// a JSON content type (A8 scopes compression to the API's JSON bodies), no
// prior Content-Encoding, and a status that carries a compressible body —
// 1xx/204/304 have none, and a 206 range is a byte slice of one
// representation, so re-encoding it would corrupt client-side reassembly.
func gzipEligible(status int, h http.Header) bool {
	if status < http.StatusOK || status == http.StatusNoContent ||
		status == http.StatusNotModified || status == http.StatusPartialContent {
		return false
	}
	if h.Get("Content-Encoding") != "" {
		return false
	}
	mt, _, _ := strings.Cut(h.Get("Content-Type"), ";")
	return strings.EqualFold(strings.TrimSpace(mt), "application/json")
}

// addVaryAcceptEncoding records that the response varies by Accept-Encoding,
// skipping the add when the header already names it (webhttp.StaticHandler
// sets its own on asset responses).
func addVaryAcceptEncoding(h http.Header) {
	for _, v := range h.Values("Vary") {
		for part := range strings.SplitSeq(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "Accept-Encoding") {
				return
			}
		}
	}
	h.Add("Vary", "Accept-Encoding")
}

var (
	_ http.ResponseWriter = (*gzipResponseWriter)(nil)
	_ http.Flusher        = (*gzipResponseWriter)(nil)
)

// gzipResponseWriter is the delayed-commit writer: WriteHeader records status
// and eligibility without touching the wire, eligible bodies buffer up to
// gzipMinBytes+1, and the byte crossing the floor commits a compressed
// stream. Not safe for concurrent use, like the ResponseWriter it wraps.
type gzipResponseWriter struct {
	rw          http.ResponseWriter
	gz          *gzip.Writer // non-nil once committed compressed
	buf         []byte       // pending body while the size decision is open
	status      int
	wroteHeader bool // handler called WriteHeader (or wrote implicitly)
	committed   bool // status + headers are on the wire
}

// Header returns the underlying header map; headers stay mutable until
// commit.
func (g *gzipResponseWriter) Header() http.Header { return g.rw.Header() }

// WriteHeader records the status without committing an eligible response. An
// ineligible one (non-JSON, pre-encoded, bodyless or range status) commits
// identity immediately, so streaming handlers keep an unbuffered writer.
// First code wins; later calls are ignored.
func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		return
	}
	g.wroteHeader = true
	g.status = code
	if !gzipEligible(code, g.rw.Header()) {
		g.commitIdentity()
	}
}

// Write buffers an eligible body until the floor decision, then streams
// through the committed encoding.
func (g *gzipResponseWriter) Write(p []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if !g.committed {
		g.buf = append(g.buf, p...)
		if len(g.buf) <= gzipMinBytes {
			return len(p), nil
		}
		return len(p), g.commitCompressed()
	}
	if g.gz != nil {
		return g.gz.Write(p)
	}
	return g.rw.Write(p)
}

// commitCompressed puts the recorded status on the wire as a gzip stream.
// Content-Length no longer describes the bytes sent, so it is dropped and
// the response streams chunked.
func (g *gzipResponseWriter) commitCompressed() error {
	g.committed = true
	h := g.rw.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	addVaryAcceptEncoding(h)
	g.rw.WriteHeader(g.status)
	gz, ok := gzipWriterPool.Get().(*gzip.Writer)
	if !ok || gz == nil {
		gz = gzip.NewWriter(io.Discard)
	}
	g.gz = gz
	g.gz.Reset(g.rw)
	_, err := g.gz.Write(g.buf)
	g.buf = nil
	return err
}

// commitIdentity puts the recorded status and any buffered bytes on the wire
// unencoded; later writes pass straight through.
func (g *gzipResponseWriter) commitIdentity() {
	g.committed = true
	addVaryAcceptEncoding(g.rw.Header())
	g.rw.WriteHeader(g.status)
	if len(g.buf) > 0 {
		// A failed write surfaces on the handler's next Write.
		_, _ = g.rw.Write(g.buf)
		g.buf = nil
	}
}

// Flush forces the identity decision — a handler that streams has opted out
// of buffering — and pushes pending bytes to the client.
func (g *gzipResponseWriter) Flush() { _ = g.FlushError() }

// FlushError is Flush returning the underlying writer's error, for
// http.ResponseController callers.
func (g *gzipResponseWriter) FlushError() error {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if !g.committed {
		g.commitIdentity()
	}
	if g.gz != nil {
		if err := g.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(g.rw).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer for
// per-connection deadline management. Flush deliberately stops at this type
// (FlushError above), so a controller flush cannot bypass the buffer.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.rw }

// finish settles a normally-completed response: an undecided body at or
// under the floor commits identity, and a compressed stream is sealed.
func (g *gzipResponseWriter) finish() {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if !g.committed {
		g.commitIdentity()
	}
	g.closeGzip()
}

// abort runs during a panic unwind. Before commit it discards the buffer so
// nothing precedes Recoverer's 500 on the wire; after a compressed commit it
// seals the stream best-effort so the client holds syntactically complete
// gzip, and Recoverer skips its 500 on the committed response.
func (g *gzipResponseWriter) abort() {
	g.buf = nil
	g.closeGzip()
}

// closeGzip seals and recycles the compression stream, if one is open.
func (g *gzipResponseWriter) closeGzip() {
	if g.gz == nil {
		return
	}
	// Best-effort: the client may already be gone.
	_ = g.gz.Close()
	gzipWriterPool.Put(g.gz)
	g.gz = nil
}
