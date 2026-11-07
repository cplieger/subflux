package subsync

import (
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

// maxPiecewiseAlloc is NFR1's bound on one alignment's memory.
const maxPiecewiseAlloc = 96 << 20

// allocated is the bytes f allocates; its caller must not run in parallel.
func allocated(f func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

func srtDoc(spans []Span) string {
	var b strings.Builder
	stamp := func(ms int64) string {
		return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3_600_000, ms/60_000%60, ms/1_000%60, ms%1_000)
	}
	for i, s := range spans {
		fmt.Fprintf(&b, "%d\n%s --> %s\nLine %d.\n\n", i+1, stamp(s.StartMs), stamp(s.EndMs), i)
	}
	return b.String()
}

func assDoc(spans []Span) string {
	var b strings.Builder
	b.WriteString("[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: Default,Arial\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	stamp := func(ms int64) string {
		return fmt.Sprintf("%d:%02d:%02d.%02d", ms/3_600_000, ms/60_000%60, ms/1_000%60, ms%1_000/10)
	}
	for i, s := range spans {
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,Line %d.\n", stamp(s.StartMs), stamp(s.EndMs), i)
	}
	return b.String()
}

func TestPiecewiseMemoryByCueCount(t *testing.T) {
	video := episode(t, 40, 300)
	for _, c := range []struct {
		name string
		doc  func([]Span) string
		far  Span
	}{
		{"SRT with a cue at 99:59:59", srtDoc, Span{359_999_000, 359_999_900}},
		{"ASS with an event at 9:59:59", assDoc, Span{35_999_000, 35_999_900}},
	} {
		t.Run(c.name, func(t *testing.T) {
			cues, err := timing.ParseCues([]byte(c.doc(append(shiftSpans(video, 2_000), c.far))))
			if err != nil {
				t.Fatal(err)
			}
			inc := DialogueSpans(cues)
			if len(inc) != len(video)+1 {
				t.Fatalf("fixture: %d dialogue spans, want %d", len(inc), len(video)+1)
			}
			var r Result
			n := allocated(func() { r, err = AlignReference(t.Context(), video, inc, ModelPiecewise) })
			t.Logf("allocated %d MiB", n>>20)
			if n > maxPiecewiseAlloc {
				t.Errorf("allocated %d MiB, want under %d", n>>20, maxPiecewiseAlloc>>20)
			}
			if err != nil {
				t.Fatal(err)
			}
			for i, s := range mapSpans(inc[:len(video)], r.Timing) {
				if d := s.StartMs - video[i].StartMs; d > 100 || d < -100 {
					t.Fatalf("cue %d placed %+d ms from the video's under %#v", i, d, r.Timing)
				}
			}
		})
	}
	t.Run("10,000 cues a side", func(t *testing.T) {
		big := episode(t, 41, maxSpans)
		inc := shiftSpans(big, 2_000)
		var err error
		n := allocated(func() { _, err = AlignReference(t.Context(), big, inc, ModelPiecewise) })
		t.Logf("allocated %d MiB", n>>20)
		if n > maxPiecewiseAlloc {
			t.Errorf("allocated %d MiB, want under %d", n>>20, maxPiecewiseAlloc>>20)
		}
		if err != nil {
			t.Fatal(err)
		}
	})
}
