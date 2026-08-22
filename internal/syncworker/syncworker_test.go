package syncworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/subsync"
)

// tinySRT is a minimal parseable subtitle payload.
const tinySRT = "1\n00:00:01,000 --> 00:00:02,000\nhello\n\n2\n00:00:03,000 --> 00:00:04,000\nworld\n\n"

// --- protocol round-trip ---

func TestWireConversion_roundtrip(t *testing.T) {
	t.Parallel()
	in := subsync.SyncResult{
		Method:     subsync.SyncMethod("offset"),
		Offset:     1500,
		Rate:       1.001,
		Confidence: subsync.Confidence(0.83),
		Cues: []subsync.Cue{
			{Text: "a", Start: time.Second, End: 2 * time.Second},
			{Text: "b", Start: 3 * time.Second, End: 4 * time.Second},
		},
	}
	out := resultFromWire(wireFromResult(&in))
	if out.Method != in.Method || out.Offset != in.Offset ||
		out.Rate != in.Rate || out.Confidence != in.Confidence {
		t.Errorf("scalar fields did not round-trip: got %+v, want %+v", out, in)
	}
	if len(out.Cues) != len(in.Cues) {
		t.Fatalf("cue count = %d, want %d", len(out.Cues), len(in.Cues))
	}
	for i := range in.Cues {
		if out.Cues[i] != in.Cues[i] {
			t.Errorf("cue %d = %+v, want %+v", i, out.Cues[i], in.Cues[i])
		}
	}
}

// runWorkerOn feeds a marshaled request to RunWorker and decodes the response.
func runWorkerOn(t *testing.T, req any) (Response, int) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var out bytes.Buffer
	code := RunWorker(t.Context(), bytes.NewReader(payload), &out)
	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v (raw %q)", err, out.String())
	}
	return resp, code
}

func TestRunWorker_reference_no_video_returns_no_change(t *testing.T) {
	t.Parallel()
	resp, code := runWorkerOn(t, &Request{
		Version: ProtocolVersion, Op: OpReference,
		Data: []byte(tinySRT), VideoPath: "", Lang: "fr",
	})
	if code != 0 {
		t.Errorf("exit code = %d, want 0 (no-sync-found is not a protocol failure)", code)
	}
	if resp.Error != "" {
		t.Errorf("response error = %q, want empty", resp.Error)
	}
	result := resultFromWire(resp.Result)
	if result.Applied() {
		t.Errorf("result.Applied() = true for no reference, want false")
	}
}

func TestRunWorker_version_mismatch_errors(t *testing.T) {
	t.Parallel()
	resp, code := runWorkerOn(t, &Request{Version: ProtocolVersion + 7, Op: OpReference})
	if resp.Error == "" || !strings.Contains(resp.Error, "protocol version") {
		t.Errorf("response error = %q, want protocol version complaint", resp.Error)
	}
	if code == 0 {
		t.Errorf("exit code = 0, want nonzero for protocol failure")
	}
}

func TestRunWorker_unknown_op_errors(t *testing.T) {
	t.Parallel()
	resp, _ := runWorkerOn(t, &Request{Version: ProtocolVersion, Op: "transmogrify"})
	if resp.Error == "" || !strings.Contains(resp.Error, "unknown op") {
		t.Errorf("response error = %q, want unknown-op complaint", resp.Error)
	}
}

func TestRunWorker_garbage_stdin_errors(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	code := RunWorker(t.Context(), strings.NewReader("not json"), &out)
	if code == 0 {
		t.Errorf("exit code = 0, want nonzero for undecodable request")
	}
	var resp Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil || resp.Error == "" {
		t.Errorf("want decodable error response, got %q (err %v)", out.String(), err)
	}
}

// --- request size bound ---

// minimalRequestJSON is a valid request whose own length is the size bound the
// two tests below sit on, so neither hand-counts a byte total.
const minimalRequestJSON = `{"version":1,"op":"reference"}`

// A payload filling the bound exactly is a valid request: the bound is
// inclusive, and the reader must not stop one byte short of it.
func TestReadRequest_accepts_a_payload_filling_the_bound_exactly(t *testing.T) {
	t.Parallel()
	atBound := len(minimalRequestJSON)

	req, err := readRequest(strings.NewReader(minimalRequestJSON), atBound)
	if err != nil {
		t.Fatalf("readRequest(%d-byte payload, maxBytes=%d) error = %v, want nil",
			len(minimalRequestJSON), atBound, err)
	}
	if req.Version != ProtocolVersion {
		t.Errorf("readRequest(%d-byte payload, maxBytes=%d) version = %d, want %d",
			len(minimalRequestJSON), atBound, req.Version, ProtocolVersion)
	}
	if req.Op != OpReference {
		t.Errorf("readRequest(%d-byte payload, maxBytes=%d) op = %q, want %q",
			len(minimalRequestJSON), atBound, req.Op, OpReference)
	}
}

// A payload one byte past the bound is refused for its SIZE. Reading only up to
// the bound would truncate it instead, and the parent would be told its request
// was malformed rather than too large.
func TestReadRequest_refuses_a_payload_one_byte_past_the_bound(t *testing.T) {
	t.Parallel()
	pastBound := len(minimalRequestJSON) - 1

	_, err := readRequest(strings.NewReader(minimalRequestJSON), pastBound)
	if err == nil {
		t.Fatalf("readRequest(%d-byte payload, maxBytes=%d) error = nil, want an oversize refusal",
			len(minimalRequestJSON), pastBound)
	}
	if !strings.Contains(err.Error(), "request exceeds") {
		t.Errorf("readRequest(%d-byte payload, maxBytes=%d) error = %v, want an oversize refusal",
			len(minimalRequestJSON), pastBound, err)
	}
}

// --- client behavior (spawn seam) ---

func newSeamClient(spawn func(ctx context.Context, req *Request) (*Response, error)) *Client {
	c := &Client{sem: make(chan struct{}, 1), exe: "unused", args: nil}
	c.spawn = spawn
	return c
}

func TestClient_concurrency_one(t *testing.T) {
	t.Parallel()
	var inFlight, maxSeen atomic.Int32
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		cur := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		return &Response{Version: ProtocolVersion}, nil
	})

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			c.Audio(t.Context(), []byte(tinySRT), "/v.mkv", "")
		})
	}
	wg.Wait()
	if got := maxSeen.Load(); got != 1 {
		t.Errorf("max concurrent worker jobs = %d, want 1 (concurrency-one queue)", got)
	}
}

func TestClient_spawn_error_degrades_to_no_change(t *testing.T) {
	sink := capture.Default(t)
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return nil, errors.New("signal: killed") // the OOM-kill shape
	})
	result := c.Reference(t.Context(), []byte(tinySRT), "/v.mkv", "fr", 0)
	if result.Applied() || result.Method != subsync.MethodNone {
		t.Errorf("result = %+v, want no-change on worker death", result)
	}
	if sink.CountLevel(slog.LevelWarn, "sync worker failed; subtitle kept unsynced") == 0 {
		t.Errorf("want degradation WARN on worker death")
	}
}

func TestClient_response_error_degrades_to_no_change(t *testing.T) {
	sink := capture.Default(t)
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		return &Response{Version: ProtocolVersion, Error: "boom"}, nil
	})
	result := c.Audio(t.Context(), []byte(tinySRT), "/v.mkv", "")
	if result.Applied() {
		t.Errorf("result applied despite job error")
	}
	if sink.CountLevel(slog.LevelWarn, "sync worker job errored; subtitle kept unsynced") == 0 {
		t.Errorf("want degradation WARN on job error")
	}
}

func TestClient_cancelled_while_queued_returns_no_change(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	occupied := make(chan struct{})
	var once sync.Once
	c := newSeamClient(func(_ context.Context, _ *Request) (*Response, error) {
		// Signalled from INSIDE the seam, which run() reaches only after it
		// holds the single slot. sync.Once because a regression that lets the
		// cancelled call through would call the seam twice.
		once.Do(func() { close(occupied) })
		<-release
		return &Response{Version: ProtocolVersion}, nil
	})
	// Occupy the slot, then wait for proof it is held. Without the wait the
	// cancelled call below can win the free slot, and the test would exercise
	// the ordinary path instead of the queued-then-cancelled one it names.
	go c.Audio(t.Context(), nil, "/v.mkv", "")
	<-occupied

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := c.Reference(ctx, nil, "/v.mkv", "fr", 0)
	close(release)
	if result.Method != subsync.MethodNone {
		t.Errorf("cancelled-while-queued result = %+v, want no-change", result)
	}
}

// --- real child-process integration (helper re-exec pattern) ---

// TestSyncWorkerHelperProcess is not a real test: it is the child side of
// the re-exec integration tests below, activated only by the env marker.
func TestSyncWorkerHelperProcess(t *testing.T) {
	switch os.Getenv("GO_SYNCWORKER_HELPER") {
	case "":
		t.Skip("helper process entry, not a test")
	case "worker":
		os.Exit(RunWorker(t.Context(), os.Stdin, os.Stdout))
	case "hang":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
}

func helperClient(t *testing.T, mode string) *Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	c := &Client{
		sem:  make(chan struct{}, 1),
		exe:  exe,
		args: []string{"-test.run=TestSyncWorkerHelperProcess$", "--"},
		env:  append(os.Environ(), "GO_SYNCWORKER_HELPER="+mode),
	}
	c.spawn = c.spawnProcess
	return c
}

func TestClient_real_process_roundtrip(t *testing.T) {
	// Not parallel: capture.Default swaps the process-wide default logger.
	sink := capture.Default(t)
	c := helperClient(t, "worker")
	// No video file: the reference strategy finds no reference and reports
	// no-change — proving the exec + JSON plumbing end to end.
	result := c.Reference(t.Context(), []byte(tinySRT), "/nonexistent/v.mkv", "fr", 0)
	if result.Applied() || result.Method != subsync.MethodNone {
		t.Errorf("real-process result = %+v, want clean no-change", result)
	}
	// Every failure mode also reports no-change, so the result alone cannot
	// tell a completed round trip from a broken one: the degradation warnings
	// must be absent for this to be evidence the plumbing worked.
	if n := sink.Count("sync worker failed"); n != 0 {
		t.Errorf("real-process run logged %d spawn-failure warnings, want 0 (the round trip must complete)", n)
	}
	if n := sink.Count("sync worker job errored"); n != 0 {
		t.Errorf("real-process run logged %d job-error warnings, want 0", n)
	}
}

func TestClient_kill_on_cancel(t *testing.T) {
	// Not parallel: capture.Default swaps the process-wide default logger.
	sink := capture.Default(t)
	c := helperClient(t, "hang")
	ctx, cancel := context.WithTimeout(t.Context(), 150*time.Millisecond)
	defer cancel()

	start := time.Now()
	result := c.Audio(ctx, []byte(tinySRT), "/v.mkv", "")
	elapsed := time.Since(start)

	if result.Method != subsync.MethodNone {
		t.Errorf("result = %+v, want no-change after kill", result)
	}
	// 150ms deadline + 5s WaitDelay upper bound; anything near 30s means the
	// hang was not killed.
	if elapsed > 10*time.Second {
		t.Errorf("cancelled worker took %v to return; kill-on-cancel not working", elapsed)
	}
	// The diagnosis must name the cancellation: an operator reading a "worker
	// process: signal: killed" line cannot tell a timeout from a crashing child.
	const wantErr = "cancelled: context deadline exceeded"
	if got, ok := sink.AttrValue("sync worker failed", "error"); !ok || got != wantErr {
		t.Errorf("cancelled worker warning error = (%q, %v), want %q", got, ok, wantErr)
	}
}
