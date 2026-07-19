package syncworker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subsync"

	"log/slog"
)

// maxWorkerRuntime caps a single worker job when the caller's context has no
// earlier deadline. The slot is concurrency-one: without a ceiling, one hung
// ffmpeg inside a worker would wedge every future sync silently.
const maxWorkerRuntime = 15 * time.Minute

// workerWaitDelay is how long after context cancellation the child gets to
// exit before it is force-killed (exec.Cmd.WaitDelay).
const workerWaitDelay = 5 * time.Second

// Client runs sync jobs in supervised one-shot `subflux sync-worker` child
// processes. It implements syncing.SyncExec, so the Syncer, the engine's
// audio fallback, and the manual sync handlers all route their heavy
// computation through it transparently.
//
// Admission is concurrency-one (the P13 queue, subsuming the sync-semaphore
// side finding): a season sync of N episodes runs at most one alignment at a
// time, and manual jobs queue behind automatic ones. Failure is degradation,
// never escalation: a worker death (OOM kill, crash, timeout) is WARN-logged
// and reported as a no-change result — the subtitle stays unsynced, the
// download proceeds, the server keeps serving.
type Client struct {
	sem  chan struct{}
	exe  string
	args []string
	// env overrides the child environment; nil inherits the parent's (the
	// production default — ffmpeg discovery relies on PATH). Tests use it
	// for the helper-process re-exec pattern.
	env []string
	// spawn runs one job in a child process; a test seam that defaults to
	// the real exec implementation.
	spawn func(ctx context.Context, req *Request) (*Response, error)
}

// Compile-time assertion: the client is a drop-in SyncExec.
var _ syncing.SyncExec = (*Client)(nil)

// NewClient builds the process-isolation client. It resolves the current
// executable once; construction fails only when the running binary's path
// cannot be determined (in which case the caller should fall back to
// in-process sync).
func NewClient() (*Client, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("syncworker: resolve executable: %w", err)
	}
	c := &Client{
		sem:  make(chan struct{}, 1),
		exe:  exe,
		args: []string{"sync-worker"},
	}
	c.spawn = c.spawnProcess
	return c, nil
}

// Reference implements syncing.SyncExec: reference-track sync in a worker.
func (c *Client) Reference(ctx context.Context, data []byte, videoPath, lang string, minConf float64) subsync.SyncResult {
	return c.run(ctx, &Request{
		Version: ProtocolVersion, Op: OpReference,
		Data: data, VideoPath: videoPath, Lang: lang, MinConfidence: minConf,
	})
}

// Audio implements syncing.SyncExec: audio-based sync in a worker.
func (c *Client) Audio(ctx context.Context, data []byte, videoPath, subtitlePath string) subsync.SyncResult {
	return c.run(ctx, &Request{
		Version: ProtocolVersion, Op: OpAudio,
		Data: data, VideoPath: videoPath, SubtitlePath: subtitlePath,
	})
}

// run acquires the single slot, executes one job in a child, and converts
// failure into the no-change result every sync call site already handles.
func (c *Client) run(ctx context.Context, req *Request) subsync.SyncResult {
	noChange := subsync.SyncResult{Method: subsync.MethodNone}

	select {
	case c.sem <- struct{}{}:
		defer func() { <-c.sem }()
	case <-ctx.Done():
		return noChange
	}

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, maxWorkerRuntime)
		defer cancel()
	}

	start := time.Now()
	resp, err := c.spawn(ctx, req)
	if err != nil {
		slog.Warn("sync worker failed; subtitle kept unsynced",
			"op", req.Op, "video", req.VideoPath,
			"duration", time.Since(start).Round(time.Millisecond).String(),
			"error", err)
		return noChange
	}
	if resp.Error != "" {
		slog.Warn("sync worker job errored; subtitle kept unsynced",
			"op", req.Op, "video", req.VideoPath, "error", resp.Error)
		return noChange
	}
	return resultFromWire(resp.Result)
}

// spawnProcess is the real child-process execution: same binary, hidden
// subcommand, JSON on stdin/stdout, stderr joined to the parent's log
// stream. Context cancellation kills the child (SIGKILL after
// workerWaitDelay); an OOM kill or crash surfaces as the run error.
func (c *Client) spawnProcess(ctx context.Context, req *Request) (*Response, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	cmd := exec.CommandContext(ctx, c.exe, c.args...)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr
	cmd.Env = c.env // nil = inherit
	cmd.WaitDelay = workerWaitDelay

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("cancelled: %w", ctxErr)
		}
		return nil, fmt.Errorf("worker process: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if resp.Version != ProtocolVersion {
		return nil, fmt.Errorf("response protocol version %d, expected %d", resp.Version, ProtocolVersion)
	}
	return &resp, nil
}
