package syncsched_test

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
)

// TestReferenceChoice gives the chosen stream the only extractable track:
// any other choice fails the extraction.
func TestReferenceChoice(t *testing.T) {
	video := episode(8, 150)
	tests := []struct {
		name   string
		tracks []track
		want   int
	}{
		{"the subtitle's own language is never the reference", []track{
			{codec: "subrip", lang: "eng", index: 2}, {codec: "ass", lang: "jpn", index: 3},
		}, 3},
		{"a picture format is never the reference", []track{
			{codec: "hdmv_pgs_subtitle", lang: "jpn", index: 2}, {codec: "mov_text", lang: "fre", index: 4},
		}, 4},
		{"a track with no language serves", []track{{codec: "subrip", lang: "und", index: 5}}, 5},
		{"non-forced first", []track{
			{codec: "subrip", lang: "jpn", index: 2, forced: true}, {codec: "ass", lang: "fre", index: 3, hi: true},
		}, 3},
		{"then SRT", []track{
			{codec: "ass", lang: "jpn", index: 2}, {codec: "subrip", lang: "fre", index: 3, hi: true},
		}, 3},
		{"then not hearing-impaired", []track{
			{codec: "subrip", lang: "jpn", index: 2, title: "SDH"}, {codec: "subrip", lang: "fre", index: 4},
		}, 4},
		{"then the lowest index", []track{
			{codec: "subrip", lang: "jpn", index: 6}, {codec: "subrip", lang: "fre", index: 3}, {codec: "subrip", lang: "ger", index: 5},
		}, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			withReference(t, map[int][]byte{tt.want: srt(video)}, tt.tracks...)
			got, err := r.align(t, srt(shifted(video, -1_500)), byReference)
			if err != nil || got.Timing != (timing.Shift{Ms: 1_500}) {
				t.Errorf("Align = %#v, %v; want Shift{1500} from stream %d", got.Timing, err, tt.want)
			}
			if n := r.calls(ffmpegtest.Extract); n != 1 {
				t.Errorf("%d extractions, want 1", n)
			}
		})
	}
}

func TestSpanCacheSkipsRead(t *testing.T) {
	video := episode(9, 150)
	r := newRig(t)
	withReference(t, map[int][]byte{2: srt(video)}, japanese)
	payload := srt(shifted(video, -1_500))
	align := func() {
		t.Helper()
		if got, err := r.align(t, payload, byReference); err != nil || got.Timing != (timing.Shift{Ms: 1_500}) {
			t.Fatalf("Align = %#v, %v; want Shift{1500}", got.Timing, err)
		}
	}
	align()
	align()
	if n := r.calls(ffmpegtest.Extract); n != 1 {
		t.Errorf("%d extractions for two alignments of one video, want 1", n)
	}
	info, err := os.Stat(r.video)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(r.video, time.Time{}, info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	align()
	if n := r.calls(ffmpegtest.Extract); n != 2 {
		t.Errorf("%d extractions after the video changed, want 2", n)
	}
}

func TestExtractionOfUnreadableTrackFails(t *testing.T) {
	r := newRig(t)
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/2", []byte("no cues here"), 0o600); err != nil {
		t.Fatal(err)
	}
	ffmpegtest.UseFor(t, ffmpegtest.Probe, probeAnswer(japanese))
	ffmpegtest.UseFor(t, ffmpegtest.Extract, "stream-file:"+dir)
	_, err := r.align(t, srt(episode(9, 50)), byReference)
	if !errors.Is(err, syncsched.ErrSyncFailed) || !errors.Is(err, timing.ErrFormatUnsupported) {
		t.Errorf("Align over an unreadable reference track: %v, want ErrSyncFailed with timing.ErrFormatUnsupported", err)
	}
}
