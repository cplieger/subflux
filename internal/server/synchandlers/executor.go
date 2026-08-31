package synchandlers

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/cplieger/atomicfile/v3"
	"github.com/cplieger/subflux/internal/server/syncjobs"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/syncworker"
)

// AudioJobRunner is the typed sync core the executor drives: the
// hook-taking audio run with named outcomes. *syncworker.Client satisfies it
// (both the process-isolating and the in-process construction).
type AudioJobRunner interface {
	RunAudio(ctx context.Context, data []byte, videoPath, subtitlePath string, hook syncworker.AdmissionHook) syncworker.RunOutcome
}

// AudioExecutor runs one accepted sync job end to end: read and normalize
// the subtitle at RUN time (the analysis sees current content, not
// accept-time content), run the typed core — whose admission hook the
// dispatcher supplies — and on a result apply the correction (SRT write-back
// plus cumulative offset bookkeeping, exactly the synchronous handler's old
// apply path). It is constructed by the composition root and handed to the
// dispatcher as its ExecFunc.
type AudioExecutor struct {
	Store  SyncStore
	Proc   SubtitleProcessor
	Runner AudioJobRunner
}

// Execute implements syncjobs.ExecFunc.
func (e *AudioExecutor) Execute(ctx context.Context, in *syncjobs.ExecInput, hook func() bool) syncjobs.ExecResult {
	data, err := atomicfile.ReadBounded(ctx, in.SubtitlePath, MaxSyncSubSize)
	if err != nil {
		if ctx.Err() != nil {
			// A queued-DELETE race conversion cancels the job context during
			// this pre-hook read: that is a cancellation, not a crash.
			return syncjobs.ExecResult{Outcome: syncjobs.OutcomeCancelled, Err: ctx.Err()}
		}
		return syncjobs.ExecResult{
			Outcome: syncjobs.OutcomeCrash,
			Err:     fmt.Errorf("read subtitle: %w", err),
		}
	}
	data = e.Proc.NormalizeEncoding(data)

	out := e.Runner.RunAudio(ctx, data, in.VideoPath, in.SubtitlePath, hook)
	if out.Outcome != syncworker.OutcomeResult {
		return syncjobs.ExecResult{Outcome: outcomeFromWorker(out.Outcome), Err: out.Err}
	}

	result := &out.Result
	applied := result.Applied() && result.ShouldApply()
	prev, offErr := e.Store.SyncOffset(ctx, in.SubtitlePath)
	if offErr != nil {
		slog.Debug("sync job: no previous offset, starting from zero",
			"path", filepath.Base(in.SubtitlePath))
		prev = 0
	}
	// The wire offset is CUMULATIVE: the stored offset plus this run's
	// correction, for the dry-run arm too — the dialog's manual Save posts
	// an absolute cumulative value, so handing it the raw delta would
	// double-shift on save.
	cumulative := prev + result.Offset

	res := syncjobs.ExecResult{
		Outcome:    syncjobs.OutcomeResult,
		OffsetMs:   cumulative,
		Confidence: float64(result.Confidence),
		Method:     string(result.Method),
		Applied:    applied,
	}
	if !applied || result.Cues == nil || in.DryRun {
		return res
	}
	if err := e.apply(ctx, in.SubtitlePath, result, cumulative); err != nil {
		return syncjobs.ExecResult{Outcome: syncjobs.OutcomeCrash, Err: err}
	}
	slog.Info("audio sync applied",
		"offset_ms", result.Offset,
		"cumulative_offset_ms", cumulative,
		"confidence", float64(result.Confidence),
		"path", filepath.Base(in.SubtitlePath))
	return res
}

// outcomeFromWorker maps the typed core's vocabulary onto the job record's.
func outcomeFromWorker(o syncworker.Outcome) syncjobs.JobOutcome {
	switch o {
	case syncworker.OutcomeTimeout:
		return syncjobs.OutcomeTimeout
	case syncworker.OutcomeCancelled:
		return syncjobs.OutcomeCancelled
	default:
		return syncjobs.OutcomeCrash
	}
}

// apply writes the corrected cues to disk and records the cumulative offset:
// the synchronous handler's old apply path, verbatim in ordering — the file
// commits BEFORE the offset is recorded, so a failed commit leaves the DB
// holding the offset of what is actually on disk.
func (e *AudioExecutor) apply(ctx context.Context, path string, result *subsync.SyncResult, cumulative int64) error {
	cues := make([]subflux.SubtitleCue, len(result.Cues))
	for i, c := range result.Cues {
		cues[i] = subflux.SubtitleCue{Start: c.Start, End: c.End, Text: c.Text}
	}
	srtData, err := e.Proc.WriteSRT(cues)
	if err != nil {
		return fmt.Errorf("write SRT: %w", err)
	}

	// WithMaxBytes mirrors the read bound: the job's read caps at
	// MaxSyncSubSize, so the staged write must refuse to cross it.
	pf, err := atomicfile.NewPendingFile(ctx, path, atomicfile.WithMaxBytes(MaxSyncSubSize))
	if err != nil {
		return fmt.Errorf("save (prepare): %w", err)
	}
	defer func() { _ = pf.Cleanup() }()
	if _, err := pf.Write(srtData); err != nil {
		return fmt.Errorf("save (write): %w", err)
	}
	if _, err := pf.Commit(ctx); err != nil {
		return fmt.Errorf("save (commit): %w", err)
	}

	if err := e.Store.SetSyncOffset(ctx, path, cumulative); err != nil {
		return fmt.Errorf("file saved but offset tracking failed at %dms cumulative "+
			"(re-open the sync dialog to verify): %w", cumulative, err)
	}
	return nil
}
