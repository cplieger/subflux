package syncsched

import (
	"errors"
	"slices"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/subsync"
)

var (
	// ErrSyncFailed wraps a tool, extraction or panic failure; Align still
	// returns a usable Identity and Unverified result beside it.
	ErrSyncFailed = errors.New("syncsched: sync failed")
	// ErrDifferentCut is Align's answer for a candidate made for another cut
	// of the video: it carries no result, and a proposal or batch file
	// ending on it applies nothing.
	ErrDifferentCut = subsync.ErrDifferentCut
	// ErrSyncCapacity is MaxQueuedPerOwner queued or running jobs for the
	// owner or MaxQueued overall, or MaxProposalsPerOwner unexpired
	// proposals for the owner or MaxProposals overall.
	ErrSyncCapacity = errors.New("syncsched: sync capacity")
	// ErrBatchTooLarge is a Resync over more than MaxBatch files.
	ErrBatchTooLarge = errors.New("syncsched: batch too large")
	// ErrPreviewCapacity is MaxPreviews previews streaming.
	ErrPreviewCapacity = errors.New("syncsched: preview capacity")
	// ErrPreviewRange is a preview start outside 0 to 24 h.
	ErrPreviewRange = errors.New("syncsched: preview start out of range")
)

// Codes is every sentinel the api-called exports return (Propose, Proposal,
// Resync, Preview), status.StartCodes' and mediawrite's passed on, with the
// code api answers for it. ErrSyncFailed and ErrDifferentCut are absent:
// only Align returns them, only search calls Align, and jobs report causes
// instead.
var Codes = slices.Concat([]struct {
	Err  error
	Code string
}{
	{ErrSyncCapacity, "sync_capacity"},
	{ErrBatchTooLarge, "body_invalid"},
	{ErrPreviewCapacity, "preview_capacity"},
	{ErrPreviewRange, "preview_range"},
}, status.StartCodes, mediawrite.Codes)

// The causes a sync job ends with besides mediawrite's codes.
const (
	causeDifferentCut      = "different_cut"
	causeFFmpegUnavailable = "ffmpeg_unavailable"
	causeNoReference       = "no_reference"
	causeNoAudio           = "no_audio"
	causeUnverified        = "unverified"
	causeWriteFailed       = "write_failed"
	causeSyncFailed        = "sync_failed"
)

// Causes is every cause a sync job ends with, in its wire spelling: its
// own, then the codes of mediawrite.Codes, which a file's read, video open
// or write passes on.
var Causes = append([]string{
	causeDifferentCut, causeFFmpegUnavailable, causeNoReference, causeNoAudio,
	causeUnverified, causeWriteFailed, causeSyncFailed,
}, codesOf(mediawrite.Codes)...)

func codesOf(codes []struct {
	Err  error
	Code string
},
) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = c.Code
	}
	return out
}
