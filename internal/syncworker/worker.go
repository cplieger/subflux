package syncworker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	"github.com/cplieger/subflux/internal/provider/classify"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subsync"
)

// maxRequestBytes bounds the stdin read: a request carries one subtitle file
// (single-digit MBs at most) plus paths; 64 MB is generous headroom, and a
// larger payload can only be a protocol bug.
const maxRequestBytes = 64 << 20

// RunWorker executes exactly one sync job read from stdin and writes the
// response to stdout. Called by the hidden `subflux sync-worker` subcommand.
// The exit code reflects PROTOCOL health only (0 even when the sync found
// nothing); the parent judges the job by the JSON response. ctx cancellation
// (parent kill, signal) aborts the underlying ffmpeg/alignment work.
func RunWorker(ctx context.Context, stdin io.Reader, stdout io.Writer) int {
	var resp Response
	resp.Version = ProtocolVersion

	req, err := readRequest(stdin)
	if err != nil {
		resp.Error = err.Error()
		return writeResponse(stdout, &resp, 2)
	}

	result, err := execute(ctx, req)
	if err != nil {
		resp.Error = err.Error()
		return writeResponse(stdout, &resp, 0)
	}
	resp.Result = wireFromResult(&result)
	return writeResponse(stdout, &resp, 0)
}

func readRequest(stdin io.Reader) (*Request, error) {
	raw, err := io.ReadAll(io.LimitReader(stdin, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read request: %w", err)
	}
	if len(raw) > maxRequestBytes {
		return nil, fmt.Errorf("request exceeds %d bytes", maxRequestBytes)
	}
	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode request: %w", err)
	}
	if req.Version != ProtocolVersion {
		return nil, fmt.Errorf("protocol version %d, worker speaks %d (binary replaced mid-flight?)", req.Version, ProtocolVersion)
	}
	return &req, nil
}

// execute runs the requested strategy in THIS process — the worker child is
// where the in-process implementations live now.
func execute(ctx context.Context, req *Request) (subsync.SyncResult, error) {
	switch req.Op {
	case OpReference:
		slog.Debug("sync worker: reference job",
			"video", req.VideoPath, "lang", req.Lang, "bytes", len(req.Data))
		// Same mapper the composition root wires into the in-process Syncer
		// (classify.Alpha2FromAlpha3): parent and child are one binary, so
		// the language table is identical by construction.
		return syncing.SyncAgainstReference(ctx, req.Data, req.VideoPath,
			req.Lang, classify.Alpha2FromAlpha3, req.MinConfidence), nil
	case OpAudio:
		slog.Debug("sync worker: audio job",
			"video", req.VideoPath, "bytes", len(req.Data))
		return syncing.SyncFromAudio(ctx, req.Data, req.VideoPath, req.SubtitlePath), nil
	default:
		return subsync.SyncResult{Method: subsync.MethodNone}, fmt.Errorf("unknown op %q", req.Op)
	}
}

func writeResponse(stdout io.Writer, resp *Response, failCode int) int {
	if err := json.NewEncoder(stdout).Encode(resp); err != nil {
		slog.Error("sync worker: write response failed", "error", err)
		return 3
	}
	if resp.Error != "" {
		slog.Warn("sync worker: job failed", "error", resp.Error)
		return failCode
	}
	return 0
}
