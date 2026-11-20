package syncsched

import (
	"errors"
	"slices"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/timing"
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

// Owner maps every sentinel the API-called exports (Propose, Proposal,
// Resync, Preview) return to the code the API answers for it; status's and
// mediawrite's, which they pass on, are those packages' owners.
var Owner = &httpwire.Owner{
	Name: "syncsched",
	Sentinels: map[string]error{
		"ErrSyncFailed":      ErrSyncFailed,
		"ErrDifferentCut":    ErrDifferentCut,
		"ErrSyncCapacity":    ErrSyncCapacity,
		"ErrBatchTooLarge":   ErrBatchTooLarge,
		"ErrPreviewCapacity": ErrPreviewCapacity,
		"ErrPreviewRange":    ErrPreviewRange,
	},
	Outside: map[string]string{
		"ErrSyncFailed":   "only Align returns it, only search calls Align, and jobs report causes instead",
		"ErrDifferentCut": "only Align returns it; jobs report the different_cut cause",
	},
	Codes: []httpwire.OwnerCode{
		{Err: ErrSyncCapacity, Code: "sync_capacity"},
		{Err: ErrBatchTooLarge, Code: "bad_body"},
		{Err: ErrPreviewCapacity, Code: "preview_capacity"},
		{Err: ErrPreviewRange, Code: "preview_range"},
	},
}

// passedCodes are the owner codes a file's read, video open or write passes
// on, in the order a cause is matched.
var passedCodes = slices.Concat(mediawrite.Owner.Codes, timing.Owner.Codes)

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
// own, then the codes of mediawrite's and timing's owners, which a file's
// read, video open or write passes on.
var Causes = append([]string{
	causeDifferentCut, causeFFmpegUnavailable, causeNoReference, causeNoAudio,
	causeUnverified, causeWriteFailed, causeSyncFailed,
}, codesOf(passedCodes)...)

func codesOf(codes []httpwire.OwnerCode) []string {
	out := make([]string, len(codes))
	for i, c := range codes {
		out[i] = c.Code
	}
	return out
}
