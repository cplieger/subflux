package syncing

import (
	"context"

	"github.com/cplieger/subflux/internal/subsync"
)

// SyncExec executes the two heavy sync strategies. The default
// implementation (InProcessExec) runs them in this process; server mode
// installs the syncworker client instead, which runs each job in a
// supervised one-shot child process so an out-of-memory sync kills the
// worker, not the server (P13). The seam sits BELOW every public interface:
// api.SubtitleSyncer, api.SubtitleProcessor, and the package-level strategy
// functions keep their exact signatures and semantics.
type SyncExec interface {
	// Reference aligns subtitle data against an embedded reference track
	// (SyncAgainstReference semantics; minConf 0 means the default threshold).
	Reference(ctx context.Context, data []byte, videoPath, lang string, minConf float64) subsync.SyncResult
	// Audio aligns subtitle data against the audio track (SyncFromAudio
	// semantics; subtitlePath is the ASS-detection hint, may be empty).
	Audio(ctx context.Context, data []byte, videoPath, subtitlePath string) subsync.SyncResult
}

// InProcessExec runs the strategies directly in the current process: the
// standalone CLI (already an isolated process), tests, and the sync-worker
// child itself.
type InProcessExec struct {
	// LangMapper maps ISO 639-2/3 to 639-1 for reference-track selection.
	LangMapper subsync.LangMapper
}

// Compile-time assertion.
var _ SyncExec = InProcessExec{}

// Reference implements SyncExec in-process.
func (e InProcessExec) Reference(ctx context.Context, data []byte, videoPath, lang string, minConf float64) subsync.SyncResult {
	return SyncAgainstReference(ctx, data, videoPath, lang, e.LangMapper, minConf)
}

// Audio implements SyncExec in-process.
func (e InProcessExec) Audio(ctx context.Context, data []byte, videoPath, subtitlePath string) subsync.SyncResult {
	return SyncFromAudio(ctx, data, videoPath, subtitlePath)
}
