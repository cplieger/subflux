package subsync_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
)

func FuzzParseFfprobeOutput(f *testing.F) {
	f.Add([]byte(`{"streams":[]}`))
	f.Add([]byte(`{"streams":[{"index":0,"codec_type":"subtitle","codec_name":"subrip","tags":{"language":"eng"}}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// ParseProbeOutput must not panic on arbitrary input.
		_, _ = ffmpeg.ParseProbeOutput(data)
	})
}
