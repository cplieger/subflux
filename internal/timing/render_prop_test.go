package timing_test

import (
	"fmt"
	"math"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/timing"
)

var (
	srtStampRe = regexp.MustCompile(`\d+:\d\d:\d\d,\d\d\d`)
	assStampRe = regexp.MustCompile(`\d+:\d\d:\d\d\.\d\d`)
)

// genDoc writes a document of random cues in format f with canonical stamps.
func genDoc(r *rand.Rand, f timing.Format) string {
	var b strings.Builder
	if f == timing.ASS {
		b.WriteString(assHead + "Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text\n")
	}
	texts := []string{"plain", "with, commas", `{\pos(1,2)}sign`, "<i>tag</i>", "12", "ünïcödé"}
	for i := range r.IntN(20) + 1 {
		start := r.Int64N(10_800_000) // with a 6 h shift, within ASS's 9:59:59.99
		end := start + r.Int64N(10_000)
		text := texts[r.IntN(len(texts))]
		if f == timing.SRT {
			fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, srtStampText(start), srtStampText(end), text)
			continue
		}
		start, end = start/10*10, end/10*10
		fmt.Fprintf(&b, "Dialogue: 0,%s,%s,Default,,0,0,0,,%s\n", assStampText(start), assStampText(end), text)
		if r.IntN(4) == 0 {
			b.WriteString("Comment: 0,0:00:00.00,0:00:01.00,Default,,0,0,0,,note\n")
		}
	}
	return b.String()
}

func srtStampText(ms int64) string {
	return fmt.Sprintf("%02d:%02d:%02d,%03d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000)
}

func assStampText(ms int64) string {
	return fmt.Sprintf("%d:%02d:%02d.%02d", ms/3_600_000, ms/60_000%60, ms/1000%60, ms%1000/10)
}

// TestShiftChangesOnlyTimestamps: under a forward shift every byte outside
// the stamps survives, and each time moves by the shift rounded to the
// format's unit, halves away from zero (design 3.2).
func TestShiftChangesOnlyTimestamps(t *testing.T) {
	t.Parallel()
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 300 {
		f := []timing.Format{timing.SRT, timing.ASS}[i%2]
		doc := genDoc(r, f)
		shift := r.Int64N(21_600_000) + 1
		c := mustParse(t, []byte(doc))
		out := string(mustRender(t, c, timing.Shift{Ms: shift}))
		re, unit := srtStampRe, int64(1)
		if f == timing.ASS {
			re, unit = assStampRe, 10
		}
		if re.ReplaceAllString(out, "T") != re.ReplaceAllString(doc, "T") {
			t.Fatalf("case %d: bytes outside the stamps changed:\n%s\nfrom:\n%s", i, out, doc)
		}
		want := c.Events()
		for k := range want {
			want[k].StartMs = int64(math.Round(float64(want[k].StartMs+shift)/float64(unit))) * unit
			want[k].EndMs = int64(math.Round(float64(want[k].EndMs+shift)/float64(unit))) * unit
		}
		if got := mustParse(t, []byte(out)).Events(); !slices.Equal(got, want) {
			t.Fatalf("case %d: shifted by %d: Events() = %+v, want %+v", i, shift, got, want)
		}
	}
}
