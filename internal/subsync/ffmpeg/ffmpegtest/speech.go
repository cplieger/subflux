package ffmpegtest

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/subsync"
)

// UseSpeech makes the PCM children t starts answer WriteSpeech's audio,
// which each child synthesizes when it is called, so a test that runs no
// audio pass pays nothing for it.
func UseSpeech(t testing.TB, spans []subsync.Span, durationMs int64) {
	t.Helper()
	b, err := json.Marshal(speechSpec{Spans: spans, DurationMs: durationMs})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "speech.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	UseFor(t, PCM, "speech:"+path)
}

// speechSpec is the audio UseSpeech hands its children.
type speechSpec struct {
	Spans      []subsync.Span
	DurationMs int64
}

// speech writes the audio of the speechSpec at path to stdout.
func speech(path string, _ *os.File) int {
	b, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var s speechSpec
	if err := json.Unmarshal(b, &s); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	w := bufio.NewWriterSize(os.Stdout, 1<<20)
	writeSpeech(w, s.Spans, s.DurationMs)
	if err := w.Flush(); err != nil {
		return 1
	}
	return 0
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
	buf := make([]byte, 0, 16_000)
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
		buf = binary.LittleEndian.AppendUint16(buf, uint16(int16(s))) //nolint:gosec // G115: s16le is the bits of the int16 sample
		if len(buf) == cap(buf) {
			_, _ = w.Write(buf)
			buf = buf[:0]
		}
	}
	_, _ = w.Write(buf)
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
