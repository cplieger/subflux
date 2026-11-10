package embedded_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg/ffmpegtest"
	"go.yaml.in/yaml/v4"
)

// rig is a store holding one instance, its video, and a Prober over them
// whose ffprobe is the fake.
type rig struct {
	store  *statedb.Store
	prober *embedded.Prober
	path   string
	inst   statedb.InstanceID
}

func newRig(t *testing.T) *rig {
	t.Helper()
	ffmpegtest.OnPath(t, "ffprobe")
	dir := t.TempDir()
	s, err := statedb.Open(filepath.Join(dir, "subflux.bolt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("default:\n  - tag: en\n"), &doc); err != nil {
		t.Fatal(err)
	}
	policy, errs := language.ParsePolicy(doc.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	ctx := t.Context()
	if err := s.Activate(ctx, statedb.Activation{Sources: []media.SourceID{"sonarr"}, Languages: policy}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "Show S01E01.mkv")
	if err := os.WriteFile(path, []byte("video"), 0o600); err != nil {
		t.Fatal(err)
	}
	pass, err := s.BeginPass(ctx, "sonarr", statedb.PassScope{})
	if err != nil {
		t.Fatal(err)
	}
	root := mediasource.Root{
		Native: mediasource.NativeRef{ID: "73", Kind: media.Episode}, Title: "Show",
		External: media.ExternalIDs{TVDB: 121361},
	}
	item := mediasource.Item{
		Native: "e1", Title: "Episode", VideoPath: path, SceneName: filepath.Base(path),
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1},
	}
	if err := s.ObserveItems(ctx, "sonarr", pass, root, []mediasource.Item{item}); err != nil {
		t.Fatal(err)
	}
	key, ok := media.Choose(media.Episode, root.External, item.Numbering)
	if !ok {
		t.Fatal("no key")
	}
	row, found, err := s.ItemRow(ctx, key)
	if err != nil || !found || row.Inst == 0 {
		t.Fatalf("ItemRow = %v, %v, %v", row.Inst, found, err)
	}
	return &rig{store: s, prober: embedded.New(s), path: path, inst: row.Inst}
}

// probe runs Probe on a fresh open of the video, ffprobe answering as
// behaviour.
func (r *rig) probe(t *testing.T, behaviour string) (statedb.VideoProbe, error) {
	t.Helper()
	ffmpegtest.Use(t, behaviour)
	f, err := os.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	return r.prober.Probe(t.Context(), r.inst, f)
}

func (r *rig) stored(t *testing.T) (statedb.VideoProbe, bool) {
	t.Helper()
	p, found, err := r.store.Embedded(t.Context(), r.inst)
	if err != nil {
		t.Fatal(err)
	}
	return p, found
}

// answer is the fake ffprobe printing streams, each a JSON stream object.
func answer(streams ...string) string {
	return `probe:{"streams":[` + strings.Join(streams, ",") + `]}`
}

const englishSubrip = `{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}`

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("no language %q", s)
	}
	return tg
}

func TestFailedProbeStoresEmpty(t *testing.T) {
	r := newRig(t)
	if _, err := r.probe(t, answer(englishSubrip)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.path, []byte("a longer video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.probe(t, "fail"); err == nil {
		t.Fatal("Probe with ffprobe failing returned no error")
	}
	got, found := r.stored(t)
	if !found || len(got.Tracks) != 0 || got.Stamp != (statedb.VideoStamp{}) || got.FPS != 0 {
		t.Errorf("stored after a failed ffprobe = %+v, %v; want an empty probe with a zero stamp", got, found)
	}
	if p, err := r.probe(t, answer(englishSubrip)); err != nil || len(p.Tracks) != 1 {
		t.Errorf("Probe after a failed one = %+v, %v; want ffprobe run again", p, err)
	}
	t.Run("ffprobe missing", func(t *testing.T) {
		if err := os.WriteFile(r.path, []byte("a video once more"), 0o600); err != nil {
			t.Fatal(err)
		}
		ffmpegtest.OnPath(t)
		if _, err := r.probe(t, answer(englishSubrip)); !errors.Is(err, ffmpeg.ErrUnavailable) {
			t.Errorf("Probe with no ffprobe = %v, want ffmpeg.ErrUnavailable", err)
		}
		if got, found := r.stored(t); !found || len(got.Tracks) != 0 || got.Stamp != (statedb.VideoStamp{}) {
			t.Errorf("stored with no ffprobe = %+v, %v; want an empty probe with a zero stamp", got, found)
		}
	})
}

func TestStatFailureStoresNothing(t *testing.T) {
	r := newRig(t)
	ffmpegtest.Use(t, answer(englishSubrip))
	f, err := os.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.prober.Probe(t.Context(), r.inst, f); !errors.Is(err, os.ErrClosed) {
		t.Errorf("Probe of an unreadable video = %v, want its stat error", err)
	}
	if got, found := r.stored(t); found {
		t.Errorf("stored after a stat failure: %+v, want nothing", got)
	}
}

func TestProbeStoresTracks(t *testing.T) {
	r := newRig(t)
	long := "x" + strings.Repeat("é", 200)
	// 256 bytes would end inside an é, so the stored title stops a byte short.
	capped := "x" + strings.Repeat("é", 127)
	got, err := r.probe(t, answer(
		`{"index":0,"codec_type":"video","codec_name":"h264","r_frame_rate":"24000/1001"}`,
		`{"index":1,"codec_type":"audio","codec_name":"aac","tags":{"language":"jpn"}}`,
		`{"index":2,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng","title":"English SDH"}}`,
		`{"index":3,"codec_type":"subtitle","codec_name":"ass","tags":{"language":"por","title":"Brasil"},"disposition":{"forced":1}}`,
		`{"index":4,"codec_type":"subtitle","codec_name":"hdmv_pgs_subtitle","tags":{"language":"und"}}`,
		`{"index":5,"codec_type":"subtitle","codec_name":"dvd_subtitle","tags":{"LANGUAGE":"fre","TITLE":"Foreign parts"}}`,
		`{"index":6,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"chi","title":"Traditional"},"disposition":{"hearing_impaired":1}}`,
		`{"index":7,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"xx-notalanguage","title":"`+long+`"}}`,
		`{"index":8,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng","title":"English Unforced"}}`,
	))
	if err != nil {
		t.Fatal(err)
	}
	want := []statedb.EmbeddedTrack{
		{Index: 2, Codec: "srt", Title: "English SDH", Tag: tag(t, "en"), Flags: language.Flags{HI: true}},
		{Index: 3, Codec: "ass", Title: "Brasil", Tag: tag(t, "pt-BR"), Flags: language.Flags{Forced: true}},
		{Index: 4, Codec: "pgs"},
		{Index: 5, Codec: "vobsub", Title: "Foreign parts", Tag: tag(t, "fr"), Flags: language.Flags{Forced: true}},
		{Index: 6, Codec: "srt", Title: "Traditional", Tag: tag(t, "zh-Hant"), Flags: language.Flags{HI: true}},
		{Index: 7, Codec: "srt", Title: capped},
		{Index: 8, Codec: "srt", Title: "English Unforced", Tag: tag(t, "en")},
	}
	stored, _ := r.stored(t)
	for name, p := range map[string]statedb.VideoProbe{"returned": got, "stored": stored} {
		if !slices.Equal(p.Tracks, want) {
			t.Errorf("%s tracks:\n got %+v\nwant %+v", name, p.Tracks, want)
		}
	}
}

func TestProbeNormalizesCodecs(t *testing.T) {
	r := newRig(t)
	cases := []struct{ ffprobe, stored string }{
		{"subrip", "srt"},
		{"ass", "ass"},
		{"ssa", "ssa"},
		{"webvtt", "webvtt"},
		{"mov_text", "mov_text"},
		{"text", "mov_text"},
		{"hdmv_pgs_subtitle", "pgs"},
		{"dvd_subtitle", "vobsub"},
		{"dvb_subtitle", "dvbsub"},
		{"dvb_teletext", "teletext"},
		{"eia_608", "cea608"},
		{"ttml", "ttml"},
	}
	streams := make([]string, 0, len(cases)+1)
	for i, c := range cases {
		streams = append(streams, `{"index":`+strconv.Itoa(i)+`,"codec_type":"subtitle","codec_name":"`+c.ffprobe+`"}`)
	}
	streams = append(streams, `{"index":99,"codec_type":"subtitle","codec_name":"arib_caption"}`)
	p, err := r.probe(t, answer(streams...))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tracks) != len(cases)+1 {
		t.Fatalf("%d tracks, want %d", len(p.Tracks), len(cases)+1)
	}
	for i, c := range cases {
		if got := p.Tracks[i].Codec; got != c.stored || !slices.Contains(embedded.Codecs, got) {
			t.Errorf("ffprobe codec %q stored as %q, want %q, one of Codecs", c.ffprobe, got, c.stored)
		}
	}
	if got := p.Tracks[len(cases)].Codec; got != "arib_caption" {
		t.Errorf("an unknown ffprobe codec stored as %q, want ffprobe's name", got)
	}
}

func TestProbeStoresFrameRate(t *testing.T) {
	tests := []struct {
		name    string
		streams []string
		want    float64
	}{
		{"23.976 fps", []string{`{"index":0,"codec_type":"video","r_frame_rate":"24000/1001"}`}, 24000.0 / 1001},
		{"25 fps", []string{`{"index":0,"codec_type":"video","r_frame_rate":"25/1"}`}, 25},
		{"cover picture first", []string{
			`{"index":0,"codec_type":"video","codec_name":"mjpeg","r_frame_rate":"90000/1","disposition":{"attached_pic":1}}`,
			`{"index":1,"codec_type":"video","r_frame_rate":"25/1"}`,
		}, 25},
		{"two video streams", []string{
			`{"index":0,"codec_type":"video","r_frame_rate":"25/1"}`,
			`{"index":1,"codec_type":"video","r_frame_rate":"30000/1001"}`,
		}, 25},
		{"no video stream", []string{englishSubrip}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newRig(t)
			if _, err := r.probe(t, answer(tt.streams...)); err != nil {
				t.Fatal(err)
			}
			if got, _ := r.stored(t); got.FPS != tt.want {
				t.Errorf("stored FPS = %v, want %v", got.FPS, tt.want)
			}
		})
	}
}

func TestUnchangedStampRunsNoFFprobe(t *testing.T) {
	r := newRig(t)
	first, err := r.probe(t, answer(englishSubrip))
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(r.path)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stamp != (statedb.VideoStamp{Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}) {
		t.Errorf("stamp = %+v, want the video's size and modification time", first.Stamp)
	}
	again, err := r.probe(t, "fail")
	if err != nil || !slices.Equal(again.Tracks, first.Tracks) || again.Stamp != first.Stamp {
		t.Errorf("Probe of an unchanged video = %+v, %v; want the stored probe with no ffprobe run", again, err)
	}
	if err := os.Chtimes(r.path, time.Time{}, info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.probe(t, "fail"); err == nil {
		t.Error("Probe of a video touched since its probe ran no ffprobe")
	}
}
