package ffmpeg

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestParseProbeOutput(t *testing.T) {
	t.Parallel()
	data := []byte(`{"streams":[
		{"index":0,"codec_name":"h264","codec_type":"video","r_frame_rate":"24000/1001"},
		{"index":1,"codec_name":"aac","codec_type":"audio","r_frame_rate":"0/0","tags":{"LANGUAGE":"jpn"}},
		{"index":2,"codec_name":"subrip","codec_type":"subtitle","r_frame_rate":"25/1","tags":{"language":"eng","title":"English SDH"},"disposition":{"forced":0,"hearing_impaired":1}},
		{"index":3,"codec_name":"ass","codec_type":"subtitle","tags":{"TITLE":"Signs"},"disposition":{"forced":1}}]}`)
	got, err := parseProbeOutput(data)
	if err != nil {
		t.Fatal(err)
	}
	want := []Stream{
		{Index: 0, Type: "video", Codec: "h264", FrameRate: 24000.0 / 1001},
		{Index: 1, Type: "audio", Codec: "aac", Language: "jpn"},
		{Index: 2, Type: "subtitle", Codec: "subrip", Language: "eng", Title: "English SDH", HearingImpaired: true},
		{Index: 3, Type: "subtitle", Codec: "ass", Title: "Signs", Forced: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d streams, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("stream %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseProbeOutputSizeLimit(t *testing.T) {
	t.Parallel()
	data := bytes.Repeat([]byte(" "), maxProbeBytes)
	data[0], data[len(data)-1] = '{', '}'
	t.Run("at the limit", func(t *testing.T) {
		got, err := parseProbeOutput(data)
		if err != nil || len(got) != 0 {
			t.Errorf("parseProbeOutput(%d bytes) = %v, %v; want no streams, nil", len(data), got, err)
		}
	})
	t.Run("one past it", func(t *testing.T) {
		if _, err := parseProbeOutput(append(data, ' ')); !errors.Is(err, ErrOutputCap) {
			t.Errorf("parseProbeOutput(%d bytes) error = %v, want ErrOutputCap", len(data)+1, err)
		}
	})
}

func TestParseFrameRate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  float64
	}{
		{name: "NTSC film", input: "24000/1001", want: 24000.0 / 1001},
		{name: "PAL", input: "25/1", want: 25},
		{name: "plain float", input: "23.976", want: 23.976},
		{name: "plain integer", input: "25", want: 25},
		{name: "zero over zero", input: "0/0", want: 0},
		{name: "zero denominator", input: "24/0", want: 0},
		{name: "negative", input: "-24/1", want: 0},
		{name: "NaN numerator", input: "NaN/1", want: 0},
		{name: "overflowing numerator", input: "1e999/1", want: 0},
		{name: "plain Inf", input: "Inf", want: 0},
		{name: "empty", input: "", want: 0},
		{name: "bad numerator", input: "abc/1001", want: 0},
		{name: "bad denominator", input: "24000/xyz", want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseFrameRate(tt.input); got != tt.want {
				t.Errorf("parseFrameRate(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func FuzzParseProbeOutput(f *testing.F) {
	f.Add([]byte(`{"streams":[]}`))
	f.Add([]byte(`{"streams":[{"index":0,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}]}`))
	f.Add([]byte(`{"streams":[{"index":0,"codec_type":"video","r_frame_rate":"1e999/1"}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))
	f.Fuzz(func(t *testing.T, data []byte) {
		streams, err := parseProbeOutput(data)
		if err != nil {
			return
		}
		for _, s := range streams {
			if s.FrameRate < 0 || math.IsNaN(s.FrameRate) || math.IsInf(s.FrameRate, 0) {
				t.Fatalf("stream %+v: frame rate not finite and non-negative", s)
			}
			if s.FrameRate != 0 && s.Type != "video" {
				t.Fatalf("stream %+v: a frame rate on a %q stream", s, s.Type)
			}
		}
	})
}

// FuzzParseFrameRate: a rate is always finite and non-negative, so a caller
// dividing by it or comparing it never meets NaN.
func FuzzParseFrameRate(f *testing.F) {
	for _, s := range []string{"24000/1001", "30", "0/0", "", "abc/def", "1/0", "-1/1", "NaN/1"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if r := parseFrameRate(s); r < 0 || math.IsNaN(r) || math.IsInf(r, 0) {
			t.Fatalf("parseFrameRate(%q) = %v", s, r)
		}
	})
}
