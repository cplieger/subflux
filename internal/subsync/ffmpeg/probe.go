package ffmpeg

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
)

// Stream is one stream of a probed video.
type Stream struct {
	Type            string // subtitle, video, audio
	Codec           string // ffprobe's codec_name
	Language        string
	Title           string
	FrameRate       float64 // video streams; 0 unknown
	Index           int
	Forced          bool
	HearingImpaired bool
}

// Probe lists video's streams through ffprobe.
func (r Runner) Probe(ctx context.Context, video *os.File) ([]Stream, error) {
	c, err := start(ctx, "probe", r.ffprobe(), probeTimeout, video,
		"-v", "error", "-show_streams", "-print_format", "json", videoArg)
	if err != nil {
		return nil, err
	}
	out, err := c.readAll(maxProbeBytes)
	if err != nil {
		return nil, err
	}
	return parseProbeOutput(out)
}

// ProbeStreams opens path and returns its streams of type kind, every stream
// when kind is "".
func ProbeStreams(ctx context.Context, path, kind string) ([]Stream, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	streams, err := Runner{}.Probe(ctx, f)
	if err != nil || kind == "" {
		return streams, err
	}
	kept := streams[:0]
	for _, s := range streams {
		if s.Type == kind {
			kept = append(kept, s)
		}
	}
	return kept, nil
}

type probeOutput struct {
	Streams []probeStream `json:"streams"`
}

type probeStream struct {
	Tags        map[string]string `json:"tags"`
	Disposition map[string]int    `json:"disposition"`
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	RFrameRate  string            `json:"r_frame_rate"`
	Index       int               `json:"index"`
}

// parseProbeOutput reads ffprobe's -show_streams JSON; a frame rate that is
// not a finite positive number reads as 0.
func parseProbeOutput(data []byte) ([]Stream, error) {
	if len(data) > maxProbeBytes {
		return nil, fmt.Errorf("ffprobe output: %d bytes over %d: %w", len(data), maxProbeBytes, ErrOutputCap)
	}
	var out probeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("ffprobe output: %w", err)
	}
	streams := make([]Stream, 0, len(out.Streams))
	for _, s := range out.Streams {
		st := Stream{
			Index:           s.Index,
			Type:            s.CodecType,
			Codec:           s.CodecName,
			Language:        tag(s.Tags, "language"),
			Title:           tag(s.Tags, "title"),
			Forced:          s.Disposition["forced"] == 1,
			HearingImpaired: s.Disposition["hearing_impaired"] == 1,
		}
		if s.CodecType == "video" {
			st.FrameRate = parseFrameRate(s.RFrameRate)
		}
		streams = append(streams, st)
	}
	return streams, nil
}

// tag reads a stream tag, which Matroska muxers write in either case.
func tag(tags map[string]string, key string) string {
	if v, ok := tags[key]; ok {
		return v
	}
	return tags[strings.ToUpper(key)]
}

// parseFrameRate reads ffprobe's r_frame_rate, a fraction ("24000/1001") or
// a number; anything else, or a result that is not finite and positive, is 0.
func parseFrameRate(s string) float64 {
	num, den, isFraction := strings.Cut(s, "/")
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0
	}
	r := n
	if isFraction {
		d, err := strconv.ParseFloat(den, 64)
		if err != nil || d == 0 {
			return 0
		}
		r = n / d
	}
	if math.IsNaN(r) || math.IsInf(r, 0) || r <= 0 {
		return 0
	}
	return r
}
