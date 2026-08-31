package syncworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"time"

	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subsync"
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
	sem   chan struct{}
	spawn func(ctx context.Context, req *Request) (*Response, error)
	exe   string
	args  []string
	env   []string
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

// NewInProcess builds a Client whose jobs run inside THIS process instead of
// a child: the fallback for when the running executable's path cannot be
// resolved. Same admission slot, budget, and typed outcomes; no process
// isolation (the pre-P13 posture — an OOM takes the server with it).
func NewInProcess() *Client {
	c := &Client{sem: make(chan struct{}, 1)}
	c.spawn = func(ctx context.Context, req *Request) (*Response, error) {
		resp := &Response{Version: ProtocolVersion}
		result, err := execute(ctx, req)
		if err != nil {
			resp.Error = err.Error()
			return resp, nil
		}
		resp.Result = wireFromResult(&result)
		return resp, nil
	}
	return c
}

// Outcome discriminates how one worker job ended, each by its own NAMED
// signal: the budget timer's cause sentinel, the job context, or the process
// exit state. Nothing is inferred from error text.
type Outcome string

// Job outcomes.
const (
	// OutcomeResult: the worker delivered a versioned JSON response with
	// exit 0 and no job error.
	OutcomeResult Outcome = "result"
	// OutcomeTimeout: the analysis budget (maxWorkerRuntime) fired —
	// identified by its own cause sentinel, never inferred from ctx.Err.
	OutcomeTimeout Outcome = "timeout"
	// OutcomeCancelled: the job context was cancelled (stop request,
	// shutdown), or the admission hook refused the run.
	OutcomeCancelled Outcome = "cancelled"
	// OutcomeCrash: everything else — non-zero exit, protocol error, spawn
	// failure, or a worker-reported job error.
	OutcomeCrash Outcome = "crash"
)

// AdmissionHook is invoked exactly once, at acquisition of the single
// execution slot and before the worker spawns. Returning false REFUSES the
// admission: the slot is released, no work runs, and the job reports
// OutcomeCancelled. A nil hook admits unconditionally.
type AdmissionHook func() bool

// RunOutcome is the typed core's answer. Result is meaningful only for
// OutcomeResult; Err carries the timeout/cancel/crash detail.
type RunOutcome struct {
	Err     error
	Outcome Outcome
	Result  subsync.SyncResult
}

// errWorkerBudget is the budget timer's own signal: context.Cause returns it
// when maxWorkerRuntime fired, which is what lets run tell a budget expiry
// from a caller cancellation without reading error text.
var errWorkerBudget = errors.New("sync worker runtime budget exceeded")

// ErrAdmissionRefused reports that the admission hook refused the run (a
// cancel won the race before the worker spawned).
var ErrAdmissionRefused = errors.New("sync job admission refused")

// Reference implements syncing.SyncExec: reference-track sync in a worker.
// A thin projection of the typed core — every non-result outcome degrades to
// the documented no-change result.
func (c *Client) Reference(ctx context.Context, data []byte, videoPath, lang string, minConf float64) subsync.SyncResult {
	out := c.run(ctx, &Request{
		Version: ProtocolVersion, Op: OpReference,
		Data: data, VideoPath: videoPath, Lang: lang, MinConfidence: minConf,
	}, nil)
	return degrade(&out, OpReference, videoPath)
}

// Audio implements syncing.SyncExec: audio-based sync in a worker. A thin
// projection of the typed core, like Reference.
func (c *Client) Audio(ctx context.Context, data []byte, videoPath, subtitlePath string) subsync.SyncResult {
	out := c.RunAudio(ctx, data, videoPath, subtitlePath, nil)
	return degrade(&out, OpAudio, videoPath)
}

// RunAudio runs one audio-sync job through the typed core: admission on the
// single slot (hook at acquisition), unconditional budget, typed outcome.
func (c *Client) RunAudio(ctx context.Context, data []byte, videoPath, subtitlePath string, hook AdmissionHook) RunOutcome {
	return c.run(ctx, &Request{
		Version: ProtocolVersion, Op: OpAudio,
		Data: data, VideoPath: videoPath, SubtitlePath: subtitlePath,
	}, hook)
}

// degrade maps the typed core's outcome onto the no-change result every
// SyncExec call site already handles: failure is degradation, never
// escalation — the subtitle stays unsynced, the caller proceeds.
func degrade(out *RunOutcome, op, videoPath string) subsync.SyncResult {
	switch out.Outcome {
	case OutcomeResult:
		return out.Result
	case OutcomeCancelled:
		// A cancelled caller (shutdown, per-request context) is routine.
		slog.Debug("sync worker job cancelled; subtitle kept unsynced",
			"op", op, "video", videoPath, "error", out.Err)
	default:
		slog.Warn("sync worker failed; subtitle kept unsynced",
			"op", op, "video", videoPath,
			"outcome", string(out.Outcome), "error", out.Err)
	}
	return subsync.SyncResult{Method: subsync.MethodNone}
}

// run is the typed core: acquire the single slot (the admission hook fires at
// acquisition and may refuse), arm the runtime budget UNCONDITIONALLY, execute
// one job, and discriminate the outcome by named signals — the budget's cause
// sentinel vs the job context vs the exit state.
func (c *Client) run(ctx context.Context, req *Request, hook AdmissionHook) RunOutcome {
	// A bare pre-check so an already-dead context is deterministically
	// cancelled instead of racing the select below.
	if err := ctx.Err(); err != nil {
		return RunOutcome{Outcome: OutcomeCancelled, Err: err}
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return RunOutcome{Outcome: OutcomeCancelled, Err: ctx.Err()}
	}
	defer func() { <-c.sem }()

	if hook != nil && !hook() {
		return RunOutcome{Outcome: OutcomeCancelled, Err: ErrAdmissionRefused}
	}

	runCtx, cancel := context.WithTimeoutCause(ctx, maxWorkerRuntime, errWorkerBudget)
	defer cancel()

	start := time.Now()
	resp, err := c.spawn(runCtx, req)
	elapsed := time.Since(start).Round(time.Millisecond)
	switch {
	case err == nil && resp.Error != "":
		return RunOutcome{Outcome: OutcomeCrash, Err: errors.New(resp.Error)}
	case err == nil:
		return RunOutcome{Outcome: OutcomeResult, Result: resultFromWire(resp.Result)}
	case errors.Is(context.Cause(runCtx), errWorkerBudget):
		return RunOutcome{
			Outcome: OutcomeTimeout,
			Err:     fmt.Errorf("analysis exceeded %s (ran %s): %w", maxWorkerRuntime, elapsed, context.Cause(runCtx)),
		}
	case ctx.Err() != nil:
		return RunOutcome{Outcome: OutcomeCancelled, Err: context.Cause(ctx)}
	default:
		return RunOutcome{Outcome: OutcomeCrash, Err: err}
	}
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

	cmd := exec.CommandContext(ctx, c.exe, c.args...) //nolint:gosec // G204: c.exe is os.Executable() (this binary) and c.args is the fixed hidden subcommand, both server-owned; no user input reaches the argv
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
