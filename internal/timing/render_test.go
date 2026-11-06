package timing_test

import (
	"bytes"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

func mustRender(t *testing.T, c timing.Cues, tm timing.Timing) []byte {
	t.Helper()
	out, err := timing.Render(c, tm)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

func TestIdentityByteIdentical(t *testing.T) {
	t.Parallel()
	for _, tc := range accepted {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, fixture(t, tc.file))
			if got := mustRender(t, c, timing.Identity{}); !bytes.Equal(got, storedDoc(t, c)) {
				t.Errorf("Render(Identity) changed the document:\n%q", got)
			}
		})
	}
}

// TestShiftRoundTripsBytes: every fixture writes canonical stamps, so a
// shift and its inverse give back every byte, in every encoding.
func TestShiftRoundTripsBytes(t *testing.T) {
	t.Parallel()
	for _, tc := range accepted {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, fixture(t, tc.file))
			shifted := mustParse(t, mustRender(t, c, timing.Shift{Ms: 61_000}))
			if shifted.Format() != c.Format() {
				t.Fatalf("shifted Format() = %v, want %v", shifted.Format(), c.Format())
			}
			want := c.Events()
			for i := range want {
				want[i].StartMs += 61_000
				want[i].EndMs += 61_000
			}
			if got := shifted.Events(); !slices.Equal(got, want) {
				t.Errorf("shifted Events() = %+v, want %+v", got, want)
			}
			if back := mustRender(t, shifted, timing.Shift{Ms: -61_000}); !bytes.Equal(back, storedDoc(t, c)) {
				t.Errorf("shift and back = %q, want the document", back)
			}
		})
	}
}

func TestASSRoundsToTenMs(t *testing.T) {
	t.Parallel()
	t.Run("ASS shift", func(t *testing.T) {
		t.Parallel()
		out := mustRender(t, mustParse(t, fixture(t, "ass-signs-styles.ass")), timing.Shift{Ms: 1234})
		for _, stamp := range []string{",0:00:02.23,0:00:03.73,", ",0:00:02.73,0:00:05.23,", ",0:00:04.23,0:00:05.23,", ",0:00:06.48,0:00:08.23,"} {
			if !bytes.Contains(out, []byte(stamp)) {
				t.Errorf("output lacks %q:\n%s", stamp, out)
			}
		}
	})
	t.Run("SRT shift", func(t *testing.T) {
		t.Parallel()
		out := mustRender(t, mustParse(t, fixture(t, "srt-crlf.srt")), timing.Shift{Ms: 1234})
		if !bytes.Contains(out, []byte("00:00:02,234 --> 00:00:03,734")) {
			t.Errorf("output lacks 00:00:02,234 --> 00:00:03,734:\n%s", out)
		}
	})
	t.Run("ASS linear within 5 ms", func(t *testing.T) {
		t.Parallel()
		c := mustParse(t, fixture(t, "ass-signs-styles.ass"))
		lin := timing.Linear{Scale: 1.0427, OffsetMs: 3}
		got := mustParse(t, mustRender(t, c, lin)).Events()
		for i, e := range c.Events() {
			for _, p := range [...]struct{ got, base int64 }{{got[i].StartMs, e.StartMs}, {got[i].EndMs, e.EndMs}} {
				exact := float64(p.base)*lin.Scale + float64(lin.OffsetMs)
				if math.Abs(float64(p.got)-exact) > 5 || p.got%10 != 0 {
					t.Errorf("event %d: %d ms written for exact %.3f", i, p.got, exact)
				}
			}
		}
	})
}

// TestRenderAbsolute: a render reads only the baseline and the Timing, so an
// earlier render under another Timing leaves no trace (H3).
func TestRenderAbsolute(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"srt-crlf.srt", "ass-signs-styles.ass"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, fixture(t, file))
			first := mustRender(t, c, timing.Shift{Ms: 700})
			if again := mustRender(t, c, timing.Shift{Ms: 700}); !bytes.Equal(again, first) {
				t.Errorf("a second render differs:\n%s\nfirst:\n%s", again, first)
			}
			mustRender(t, c, timing.Shift{Ms: -300})
			fresh := mustRender(t, mustParse(t, fixture(t, file)), timing.Shift{Ms: 2500})
			if got := mustRender(t, c, timing.Shift{Ms: 2500}); !bytes.Equal(got, fresh) {
				t.Errorf("Shift{2500} after other renders = %s, want %s", got, fresh)
			}
		})
	}
}

func TestRenderClamps(t *testing.T) {
	t.Parallel()
	c := mustParse(t, fixture(t, "srt-crlf.srt"))
	t.Run("start before 0 starts at 0", func(t *testing.T) {
		t.Parallel()
		out := mustRender(t, c, timing.Shift{Ms: -1500})
		if !bytes.HasPrefix(out, []byte("1\r\n00:00:00,000 --> 00:00:01,000\r\n")) {
			t.Errorf("output starts %q", out[:min(len(out), 40)])
		}
	})
	t.Run("end at 0 is removed", func(t *testing.T) {
		t.Parallel()
		want := "1\r\n00:00:00,500 --> 00:00:01,500\r\n<i>Home, I think.</i>\r\nIt is late.\r\n\r\n" +
			"2\r\n00:00:02,750 --> 00:00:04,500 X1:100 X2:200 Y1:10 Y2:20\r\nSee you tomorrow.\r\n"
		if got := mustRender(t, c, timing.Shift{Ms: -2500}); string(got) != want {
			t.Errorf("Render = %q, want %q", got, want)
		}
	})
	t.Run("ASS end before 0 removes the line", func(t *testing.T) {
		t.Parallel()
		ass := mustParse(t, fixture(t, "ass-comments.ass"))
		out := mustRender(t, ass, timing.Shift{Ms: -2600})
		if bytes.Contains(out, []byte("Where are you going?")) {
			t.Errorf("the removed event's line survives:\n%s", out)
		}
		if !bytes.Contains(out, []byte("Comment: 0,0:00:00.00,0:00:00.50,Default,,0,0,0,,Translator note")) ||
			!bytes.Contains(out, []byte("Dialogue: 0,0:00:00.40,0:00:01.40,Default,,0,0,0,,Home, I think.\n")) {
			t.Errorf("output lost an untouched line or misplaced a time:\n%s", out)
		}
	})
}

func TestRenderRenumbersAfterDrop(t *testing.T) {
	t.Parallel()
	drop := timing.Piecewise{Scale: 1, Segments: []timing.Segment{
		{FromMs: 0, ToMs: 2000, OffsetMs: 0},
		{FromMs: 2000, ToMs: 4500, Drop: true},
		{FromMs: 4500, ToMs: 0, OffsetMs: 100},
	}}
	t.Run("SRT", func(t *testing.T) {
		t.Parallel()
		want := "1\r\n00:00:01,000 --> 00:00:02,500\r\nWhere are you going?\r\n\r\n" +
			"2\r\n00:00:05,350 --> 00:00:07,100 X1:100 X2:200 Y1:10 Y2:20\r\nSee you tomorrow.\r\n"
		if got := mustRender(t, mustParse(t, fixture(t, "srt-crlf.srt")), drop); string(got) != want {
			t.Errorf("Render = %q, want %q", got, want)
		}
	})
	t.Run("ASS", func(t *testing.T) {
		t.Parallel()
		c := mustParse(t, fixture(t, "ass-signs-styles.ass"))
		got := mustParse(t, mustRender(t, c, drop)).Events()
		want := []timing.Event{
			ev(1000, 2500, "Where are you going?", "Default"),
			ev(1500, 4000, `{\pos(960,100)\fad(200,200)}NORTH STATION`, "Sign"),
			ev(5350, 7100, `{\i1}See you{\i0} tomorrow.`, "Default"),
		}
		if !slices.Equal(got, want) {
			t.Errorf("Events() = %+v, want %+v", got, want)
		}
	})
	t.Run("no index lines", func(t *testing.T) {
		t.Parallel()
		doc := "00:00:01,000 --> 00:00:02,000\na\n\n00:00:03,000 --> 00:00:04,000\nb\n\n00:00:05,000 --> 00:00:06,000\nc\n"
		want := "00:00:01,000 --> 00:00:02,000\na\n\n00:00:05,100 --> 00:00:06,100\nc\n"
		if got := mustRender(t, mustParse(t, []byte(doc)), drop); string(got) != want {
			t.Errorf("Render = %q, want %q", got, want)
		}
	})
}

// renderFailures is every Render refusal; codes_test.go drives each too.
func renderFailures(t testing.TB) []struct {
	name string
	c    timing.Cues
	t    timing.Timing
	want error
} {
	t.Helper()
	late, err := timing.ParseCues([]byte(assHead + "Dialogue: 0,9:59:59.00,9:59:59.50,Default,,0,0,0,,a\n"))
	if err != nil {
		t.Fatal(err)
	}
	srt, err := timing.ParseCues(fixture(t, "srt-crlf.srt"))
	if err != nil {
		t.Fatal(err)
	}
	return []struct {
		name string
		c    timing.Cues
		t    timing.Timing
		want error
	}{
		{"invalid timing", srt, timing.Shift{Ms: 0}, timing.ErrTimingInvalid},
		{"nil timing", srt, nil, timing.ErrTimingInvalid},
		{"ASS hour overflow", late, timing.Shift{Ms: 1000}, timing.ErrTimingInvalid},
		{"zero Cues", timing.Cues{}, timing.Identity{}, timing.ErrFormatUnsupported},
	}
}

func TestRenderRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range renderFailures(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := timing.Render(tc.c, tc.t); !errors.Is(err, tc.want) {
				t.Errorf("Render error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestASSHourOverflow(t *testing.T) {
	t.Parallel()
	c := mustParse(t, []byte(assHead+"Dialogue: 0,9:59:58.00,9:59:58.99,Default,,0,0,0,,a\n"))
	out := mustRender(t, c, timing.Shift{Ms: 1000})
	if !strings.Contains(string(out), ",9:59:59.00,9:59:59.99,") {
		t.Errorf("the last ASS time is not written:\n%s", out)
	}
	if _, err := timing.Render(c, timing.Shift{Ms: 1010}); !errors.Is(err, timing.ErrTimingInvalid) {
		t.Errorf("Render past 9:59:59.99 error = %v, want ErrTimingInvalid", err)
	}
	if got := mustRender(t, mustParse(t, []byte(assHead+"Dialogue: 0,12:00:00.00,12:00:01.00,Default,,0,0,0,,a\n")), timing.Identity{}); !strings.Contains(string(got), "12:00:00.00") {
		t.Errorf("an unchanged late stamp was not kept:\n%s", got)
	}
}
