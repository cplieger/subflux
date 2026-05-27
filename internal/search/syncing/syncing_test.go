package syncing_test

import (
	"testing"
)

// --- Processor ---

func TestProcessor_SyncsSubtitleToVideo(t *testing.T) {
	t.Skip("TODO: adjusts subtitle timing to match video audio track")
}

func TestProcessor_SkipsAlreadySynced(t *testing.T) {
	t.Skip("TODO: no-ops when subtitle already has matching offset")
}

func TestProcessor_HandlesFFmpegFailure(t *testing.T) {
	t.Skip("TODO: returns error when ffmpeg extraction fails")
}

// --- Strategy ---

func TestStrategy_SelectsBestReference(t *testing.T) {
	t.Skip("TODO: picks the audio track matching subtitle language")
}

func TestStrategy_FallsBackToDefaultTrack(t *testing.T) {
	t.Skip("TODO: uses default audio track when language match unavailable")
}

func TestStrategy_RespectsMaxDuration(t *testing.T) {
	t.Skip("TODO: limits sync window to configured max duration")
}

// --- Syncer ---

func TestSyncer_AppliesOffset(t *testing.T) {
	t.Skip("TODO: writes adjusted subtitle file with computed offset")
}

func TestSyncer_PreservesOriginalOnFailure(t *testing.T) {
	t.Skip("TODO: does not overwrite original when sync fails")
}

func TestSyncer_RecordsOffsetInDB(t *testing.T) {
	t.Skip("TODO: stores computed offset_ms in subtitle file row")
}
