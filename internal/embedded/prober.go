// Package embedded reads the subtitle tracks inside a video file and stores
// them as its instance's probe, and owns the embedded_subtitles section. It
// inspects local media and is not a provider.
package embedded

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

// Prober probes videos with ffprobe from PATH and stores what it finds.
type Prober struct {
	store *statedb.Store
	run   ffmpeg.Runner
}

// New returns a Prober storing its probes in store.
func New(store *statedb.Store) *Prober { return &Prober{store: store} }

// Probe returns inst's probe of video, the file mediawrite.Writer.OpenVideo
// opened, which the caller closes after Probe returns. ffprobe runs only when
// video's size or modification time differs from the stored probe's stamp,
// and its result is stored with PutEmbedded. A Stat failure stores nothing;
// a failed ffprobe stores a probe with no tracks and a zero stamp, so the
// inventory completes and the next Probe runs ffprobe again, and returns the
// failure.
func (p *Prober) Probe(ctx context.Context, inst statedb.InstanceID, video *os.File) (statedb.VideoProbe, error) {
	info, err := video.Stat()
	if err != nil {
		return statedb.VideoProbe{}, fmt.Errorf("embedded: probe: %w", err)
	}
	stamp := statedb.VideoStamp{Size: info.Size(), MtimeNs: info.ModTime().UnixNano()}
	stored, found, err := p.store.Embedded(ctx, inst)
	if err != nil {
		return statedb.VideoProbe{}, fmt.Errorf("embedded: probe: %w", err)
	}
	if found && stamp != (statedb.VideoStamp{}) && stored.Stamp == stamp {
		return stored, nil
	}
	streams, err := p.run.Probe(ctx, video)
	if err != nil {
		err = fmt.Errorf("embedded: probe: %w", err)
		if perr := p.store.PutEmbedded(ctx, inst, statedb.VideoProbe{}); perr != nil {
			err = errors.Join(err, fmt.Errorf("embedded: store the failed probe: %w", perr))
		}
		return statedb.VideoProbe{}, err
	}
	probe := probeOf(streams, stamp)
	if err := p.store.PutEmbedded(ctx, inst, probe); err != nil {
		return statedb.VideoProbe{}, fmt.Errorf("embedded: probe: %w", err)
	}
	return probe, nil
}

// probeOf is the probe ffprobe's streams describe: every subtitle stream,
// and the first video stream's frame rate.
func probeOf(streams []ffmpeg.Stream, stamp statedb.VideoStamp) statedb.VideoProbe {
	p := statedb.VideoProbe{Stamp: stamp}
	for i := range streams {
		s := &streams[i]
		switch s.Type {
		case "video":
			if p.FPS == 0 {
				p.FPS = s.FrameRate
			}
		case "subtitle":
			p.Tracks = append(p.Tracks, trackOf(s))
		}
	}
	return p
}

// trackOf is one subtitle stream as stored: its codec normalized, its title
// cut to maxTitleBytes, its tag the language tag refined by the title (zero
// when the tag names no language), and its flags the dispositions' plus what
// language.ReadFlags reads in the title.
func trackOf(s *ffmpeg.Stream) statedb.EmbeddedTrack {
	title := capTitle(s.Title)
	tag, _ := language.Parse(strings.TrimSpace(s.Language))
	if tag != (language.Tag{}) {
		tag = language.RefineFromTitle(tag, title)
	}
	flags := language.ReadFlags(title)
	flags.HI = flags.HI || s.HearingImpaired
	flags.Forced = flags.Forced || s.Forced
	return statedb.EmbeddedTrack{Codec: codecName(s.Codec), Title: title, Tag: tag, Index: s.Index, Flags: flags}
}

// capTitle cuts s to at most maxTitleBytes, never inside a UTF-8 sequence.
func capTitle(s string) string {
	if len(s) <= maxTitleBytes {
		return s
	}
	cut := maxTitleBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// codecName is ffprobe's codec name as Codecs spells it; a codec Codecs
// lacks keeps ffprobe's name.
func codecName(ffprobe string) string {
	if name, ok := codecNames[ffprobe]; ok {
		return name
	}
	return ffprobe
}

var codecNames = map[string]string{
	"subrip":            codecSRT,
	codecASS:            codecASS,
	codecSSA:            codecSSA,
	codecWebVTT:         codecWebVTT,
	codecMovText:        codecMovText,
	"text":              codecMovText,
	"hdmv_pgs_subtitle": codecPGS,
	"dvd_subtitle":      codecVobSub,
	"dvb_subtitle":      codecDVBSub,
	"dvb_teletext":      codecTeletext,
	"eia_608":           codecCEA608,
	codecTTML:           codecTTML,
}
