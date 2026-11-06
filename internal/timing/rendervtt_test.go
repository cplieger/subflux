package timing_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/subtitleenc"
	"github.com/cplieger/subflux/internal/timing"
)

func mustRenderVTT(t *testing.T, c timing.Cues, tm timing.Timing, from time.Duration) string {
	t.Helper()
	out, err := timing.RenderVTT(c, tm, from)
	if err != nil {
		t.Fatalf("RenderVTT: %v", err)
	}
	return string(out)
}

func TestRenderVTTMarkup(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, doc, want string
	}{
		{
			"SRT",
			"1\n00:00:01,000 --> 00:00:02,000\n" +
				`<i>a</i> &amp; <b>b</b> <u>c</u> <font color="red">d</font> {\an8}e <i f <script>x</script> -->` + "\n",
			"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n" +
				"<i>a</i> &amp;amp; <b>b</b> <u>c</u> d e &lt;i f &lt;script&gt;x&lt;/script&gt; --\u2060&gt;\n",
		},
		{
			"SRT hidden tags in any case",
			"1\n00:00:01,000 --> 00:00:02,000\n<I>a</I> <s>b</s> <B class=x>c</B> {literal}\n",
			"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na b c {literal}\n",
		},
		{
			"ASS",
			assHead + `Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,{\i1}a{\i0}\Nb\hc\nd <e> & {comment}f` + "\n",
			"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na\nb\u00a0c d &lt;e&gt; &amp; f\n",
		},
		{
			"ASS tags are text",
			assHead + `Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,<i>a</i> <font>x</font>` + "\n",
			"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\n&lt;i&gt;a&lt;/i&gt; &lt;font&gt;x&lt;/font&gt;\n",
		},
		{
			"blank lines dropped",
			"1\n00:00:01,000 --> 00:00:02,000\na\n\nb\n",
			"WEBVTT\n\n00:00:01.000 --> 00:00:02.000\na\nb\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustRenderVTT(t, mustParse(t, []byte(tc.doc)), timing.Identity{}, 0); got != tc.want {
				t.Errorf("RenderVTT = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRenderVTTWindow(t *testing.T) {
	t.Parallel()
	c := mustParse(t, []byte("1\n00:00:00,000 --> 00:00:05,000\nA\n\n"+
		"2\n00:01:25,000 --> 00:01:35,000\nB\n\n"+
		"3\n00:01:40,000 --> 00:01:50,000\nC\n\n"+
		"4\n00:09:55,000 --> 00:10:05,000\nD\n\n"+
		"5\n00:11:40,000 --> 00:11:50,000\nE\n"))
	tests := []struct {
		name string
		from time.Duration
		want string
	}{
		{"from 0", 0, "WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nA\n\n00:01:25.000 --> 00:01:35.000\nB\n\n" +
			"00:01:40.000 --> 00:01:50.000\nC\n\n00:09:55.000 --> 00:10:00.000\nD\n"},
		{"from 90 s", 90 * time.Second, "WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nB\n\n00:00:10.000 --> 00:00:20.000\nC\n\n" +
			"00:08:25.000 --> 00:08:35.000\nD\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustRenderVTT(t, c, timing.Identity{}, tc.from); got != tc.want {
				t.Errorf("RenderVTT = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderVTTMapsLikeRender: the preview shows the times the file gets,
// rounding, clamping and drops included.
func TestRenderVTTMapsLikeRender(t *testing.T) {
	t.Parallel()
	c := mustParse(t, fixture(t, "ass-signs-styles.ass"))
	tm := timing.Piecewise{Scale: 1, Segments: []timing.Segment{
		{FromMs: 0, ToMs: 1200, OffsetMs: -1234},
		{FromMs: 1200, ToMs: 4500, Drop: true},
		{FromMs: 4500, ToMs: 0, OffsetMs: 1234},
	}}
	want := "WEBVTT\n\n00:00:00.000 --> 00:00:01.270\nWhere are you going?\n\n00:00:06.480 --> 00:00:08.230\nSee you tomorrow.\n"
	if got := mustRenderVTT(t, c, tm, 0); got != want {
		t.Errorf("RenderVTT = %q, want %q", got, want)
	}
}

func TestWindows1251ThroughCodecRenderVTT(t *testing.T) {
	t.Parallel()
	c := mustParse(t, fixture(t, "srt-windows-1251.srt"))
	reloaded, err := timing.DecodeCues(timing.AppendCues(nil, c))
	if err != nil {
		t.Fatal(err)
	}
	before := mustRenderVTT(t, c, timing.Shift{Ms: 500}, 0)
	after := mustRenderVTT(t, reloaded, timing.Shift{Ms: 500}, 0)
	if after != before {
		t.Errorf("the stored baseline previews %q, the parsed one %q", after, before)
	}
	if !utf8.ValidString(after) {
		t.Errorf("RenderVTT wrote bytes that are not UTF-8: %q", after)
	}
	// With no hint the page is Unknown; the subtitle's language names it.
	cyrillic := mustRenderVTT(t, mustParse(t, decodeAs(t, subtitleenc.Windows1251, fixture(t, "srt-windows-1251.srt"))), timing.Shift{Ms: 500}, 0)
	for _, normalize := range []bool{false, true} {
		cleaned, err := timing.Clean(reloaded, timing.CleanOptions{NormalizeUTF8: normalize}, tag(t, "ru"))
		if err != nil {
			t.Fatalf("Clean(normalize_utf8 %v) = %v", normalize, err)
		}
		if got := mustRenderVTT(t, cleaned, timing.Shift{Ms: 500}, 0); got != cyrillic {
			t.Errorf("normalize_utf8 %v previews %q, want %q", normalize, got, cyrillic)
		}
		kept := bytes.Equal(mustRender(t, cleaned, timing.Identity{}), fixture(t, "srt-windows-1251.srt"))
		if kept == normalize {
			t.Errorf("normalize_utf8 %v: document kept its windows-1251 bytes = %v, want %v", normalize, kept, !normalize)
		}
	}
}

// renderVTTFailures is every RenderVTT refusal; codes_test.go drives each too.
func renderVTTFailures(t testing.TB) []struct {
	name string
	call func() error
	want error
} {
	t.Helper()
	var arms []struct {
		name string
		call func() error
		want error
	}
	for _, f := range renderFailures(t) {
		arms = append(arms, struct {
			name string
			call func() error
			want error
		}{f.name, func() error {
			_, err := timing.RenderVTT(f.c, f.t, 0)
			return err
		}, f.want})
	}
	srt, err := timing.ParseCues(fixture(t, "srt-crlf.srt"))
	if err != nil {
		t.Fatal(err)
	}
	return append(arms, struct {
		name string
		call func() error
		want error
	}{"negative from", func() error {
		_, err := timing.RenderVTT(srt, timing.Identity{}, -time.Millisecond)
		return err
	}, timing.ErrTimingInvalid})
}

func TestRenderVTTRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range renderVTTFailures(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.call(); !errors.Is(err, tc.want) {
				t.Errorf("RenderVTT error = %v, want %v", err, tc.want)
			}
		})
	}
}

// Not parallel: the wall-clock ceiling must not count the package's parallel tests.
func TestRenderVTTWorkLinear(t *testing.T) {
	c := mustParse(t, []byte(assHead+"Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,"+strings.Repeat("{", 1<<20)+"\n"))
	start := time.Now()
	out := mustRenderVTT(t, c, timing.Identity{}, 0)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("RenderVTT took %v on one line of unclosed '{'", elapsed)
	}
	if strings.Count(out, "{") != 1<<20 {
		t.Errorf("RenderVTT dropped unclosed braces")
	}
}
