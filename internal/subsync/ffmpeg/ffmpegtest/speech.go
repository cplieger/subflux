package ffmpegtest

import (
	"bufio"
	"encoding/binary"
	"errors"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/subsync"
)

// UseSpeech makes the PCM children t starts answer WriteSpeech's audio.
func UseSpeech(t testing.TB, spans []subsync.Span, durationMs int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "speech.pcm")
	if err := WriteSpeech(path, spans, durationMs); err != nil {
		t.Fatal(err)
	}
	UseFor(t, PCM, "cat:"+path)
}

// WriteSpeech writes 8 kHz s16le audio of durationMs to path: a voiced
// sound over spans, sorted by start, and faint noise elsewhere, which the
// voice detector tells apart.
func WriteSpeech(path string, spans []subsync.Span, durationMs int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	w := bufio.NewWriterSize(f, 1<<20)
	writeSpeech(w, spans, durationMs)
	return errors.Join(w.Flush(), f.Close())
}

func writeSpeech(w *bufio.Writer, spans []subsync.Span, durationMs int64) {
	r := rand.New(rand.NewPCG(1, 2)) //nolint:gosec // G404: a seeded fixture, the same audio every run
	v := voiced()
	var sample [2]byte
	next := 0
	for i := range 8 * durationMs {
		ms := i / 8
		for next < len(spans) && spans[next].EndMs <= ms {
			next++
		}
		s := 30 * r.NormFloat64()
		if next < len(spans) && spans[next].StartMs <= ms {
			s += v[i%int64(len(v))]
		}
		binary.LittleEndian.PutUint16(sample[:], uint16(int16(s))) //nolint:gosec // G115: s16le is the bits of the int16 sample
		_, _ = w.Write(sample[:])
	}
}

// voiced is one second of a 140 Hz harmonic series amplitude-modulated at
// 5 Hz, which repeats every second.
var voiced = sync.OnceValue(func() []float64 {
	out := make([]float64, 8_000)
	for i := range out {
		tm := float64(i) / 8_000
		var v float64
		for k := 1.0; k*140 < 3_500; k++ {
			v += math.Sin(2*math.Pi*k*140*tm) / k
		}
		out[i] = 4_000 * (0.6 + 0.4*math.Sin(2*math.Pi*5*tm)) * v
	}
	return out
})
