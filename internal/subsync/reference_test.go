package subsync

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/timing"
)

// regular is n cues of lenMs every everyMs from 0.
func regular(n int, lenMs, everyMs int64) []Span {
	out := make([]Span, n)
	for i := range out {
		at := int64(i) * everyMs
		out[i] = Span{at, at + lenMs}
	}
	return out
}

func TestGuardSpanCount(t *testing.T) {
	t.Parallel()
	ok := episode(t, 0, 200)
	big := episode(t, 0, maxSpans+1)
	for _, c := range []struct {
		name     string
		ref, inc []Span
		want     Model
	}{
		{"10,001 reference spans", big, ok, ModelNone},
		{"10,001 candidate spans", ok, big, ModelNone},
		{"10,000 spans a side", big[:maxSpans], big[:maxSpans], ModelShift},
		{"4 reference spans", ok[:4], ok, ModelNone},
		{"4 candidate spans", ok, ok[:4], ModelNone},
		{"5 spans a side", ok[:5], ok[:5], ModelShift},
		{"reference coverage 0.04", regular(100, 1_000, 25_000), ok, ModelNone},
		{"reference coverage 0.96", regular(100, 24_000, 25_000), ok, ModelNone},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r, err := AlignReference(t.Context(), c.ref, c.inc, ModelLinear)
			if err != nil {
				t.Fatal(err)
			}
			if modelOf(r) != c.want {
				t.Errorf("model = %d, want %d", modelOf(r), c.want)
			}
			if c.want == ModelNone && (r.Timing != timing.Identity{} || r.Measured || r.Confidence != 0) {
				t.Errorf("refused input: result %+v, want Identity, unmeasured, confidence 0", r)
			}
		})
	}
}

// countdown is a context whose Err answers nil n times, then Canceled.
type countdown struct {
	context.Context
	n int
}

func (c *countdown) Err() error {
	if c.n == 0 {
		return context.Canceled
	}
	c.n--
	return nil
}

func TestCancelledContextPerStage(t *testing.T) {
	t.Parallel()
	video := episode(t, 0, 400)
	inc := unmap(video, 25/23.976, 1_500)
	for _, upTo := range []Model{ModelLinear, ModelPiecewise} {
		t.Run(fmt.Sprintf("up to model %d", upTo), func(t *testing.T) {
			t.Parallel()
			full, err := AlignReference(t.Context(), video, inc, upTo)
			if err != nil || full.Confidence < referenceGate {
				t.Fatalf("uncancelled: %+v, %v", full, err)
			}
			for n := 0; ; n++ {
				r, err := AlignReference(&countdown{Context: t.Context(), n: n}, video, inc, upTo)
				if err == nil {
					if r.Timing != full.Timing || n < 3 {
						t.Fatalf("cancelled after %d checks: result %+v with no error", n, r)
					}
					break
				}
				if !errors.Is(err, context.Canceled) || r.Timing != nil || r.Confidence != 0 {
					t.Fatalf("cancelled after %d checks: %+v, %v; want only context.Canceled", n, r, err)
				}
			}
		})
	}
}

func TestLinearKeepsOffset(t *testing.T) {
	t.Parallel()
	video := episode(t, 1, 300)
	for _, ratio := range []float64{25 / 23.976, 23.976 / 25} {
		for _, offset := range []int64{0, 3_000, -7_000} {
			t.Run(fmt.Sprintf("%.4f %+d", ratio, offset), func(t *testing.T) {
				t.Parallel()
				inc := unmap(video, ratio, offset)
				r, err := AlignReference(t.Context(), video, inc, ModelLinear)
				if err != nil {
					t.Fatal(err)
				}
				if modelOf(r) != ModelLinear || r.Confidence < referenceGate {
					t.Fatalf("model %d confidence %v, want linear at the gate", modelOf(r), r.Confidence)
				}
				assertPlaced(t, video, inc, r.Timing)
			})
		}
	}
}

func TestShiftsVerify(t *testing.T) {
	t.Parallel()
	video := episode(t, 2, 250)
	for _, shift := range []int64{3_200, -11_700, 62_000} {
		t.Run(time.Duration(shift*int64(time.Millisecond)).String(), func(t *testing.T) {
			t.Parallel()
			inc := unmap(video, 1, shift)
			r, err := AlignReference(t.Context(), video, inc, ModelLinear)
			if err != nil {
				t.Fatal(err)
			}
			if r.Timing != (timing.Shift{Ms: shift}) || modelOf(r) != ModelShift || r.Method != timing.MethodReference {
				t.Errorf("result %+v, want Shift{%d} by the reference", r, shift)
			}
			if r.Confidence < referenceGate || !r.Measured {
				t.Errorf("confidence %v measured %v, want at least %v, measured", r.Confidence, r.Measured, referenceGate)
			}
		})
	}
}

func TestIdentityIsCanonical(t *testing.T) {
	t.Parallel()
	video := episode(t, 3, 100)
	r, err := AlignReference(t.Context(), video, video, ModelLinear)
	if err != nil {
		t.Fatal(err)
	}
	if r.Timing != (timing.Identity{}) || r.Confidence != 1 {
		t.Errorf("same cues: %+v, want Identity at confidence 1", r)
	}
}

func TestUpToShiftNeverLinear(t *testing.T) {
	t.Parallel()
	video := episode(t, 1, 300)
	r, err := AlignReference(t.Context(), video, unmap(video, 25/23.976, 0), ModelShift)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Timing.(timing.Linear); ok || modelOf(r) != ModelShift {
		t.Errorf("up to ModelShift: %+v, want a shift", r)
	}
	if _, err := AlignReference(t.Context(), video, video, ModelNone); err == nil {
		t.Error("up to ModelNone: no error")
	}
}

func TestWrongEpisodeBelowHalf(t *testing.T) {
	t.Parallel()
	for k := 1; k <= 4; k++ {
		r, err := AlignReference(t.Context(), episode(t, 0, 250), episode(t, k, 250), ModelLinear)
		if err != nil {
			t.Fatal(err)
		}
		if r.Confidence >= 0.5 {
			t.Errorf("episode %d against episode 0: confidence %v, want under 0.5", k, r.Confidence)
		}
	}
}

func TestDialogueSpansKeepsDialogue(t *testing.T) {
	t.Parallel()
	srt := "1\n00:00:01,000 --> 00:00:02,000\nHello there.\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\n[door slams]\n\n" +
		"3\n00:00:05,000 --> 00:00:06,000\n♪ la la ♪\n\n" +
		"4\n00:00:07,000 --> 00:00:08,500\nGeneral Kenobi.\n"
	ass := "[Script Info]\nScriptType: v4.00+\n\n[V4+ Styles]\nFormat: Name, Fontname\nStyle: Default,Arial\nStyle: Sign,Arial\n\n" +
		"[Events]\nFormat: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n" +
		"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,Hello there.\n" +
		"Dialogue: 0,0:00:03.00,0:00:04.00,Sign,,0,0,0,,EXIT\n" +
		"Dialogue: 0,0:00:07.00,0:00:08.50,Default,,0,0,0,,{\\i1}General Kenobi.\n"
	want := []Span{{1_000, 2_000}, {7_000, 8_500}}
	for name, doc := range map[string]string{"srt": srt, "ass": ass} {
		c, err := timing.ParseCues([]byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		got := DialogueSpans(c)
		if !slices.Equal(got, want) {
			t.Errorf("%s: spans %v, want %v", name, got, want)
		}
	}
}
