package timing_test

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/timing"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func mustParse(t *testing.T, b []byte) timing.Cues {
	t.Helper()
	c, err := timing.ParseCues(b)
	if err != nil {
		t.Fatalf("ParseCues: %v", err)
	}
	return c
}

// storedDoc is the document AppendCues stores: after the layout, format and
// encoding bytes and the uvarint length.
func storedDoc(t *testing.T, c timing.Cues) []byte {
	t.Helper()
	b := timing.AppendCues(nil, c)
	_, k := binary.Uvarint(b[3:])
	return b[3+k:]
}

func ev(startMs, endMs int64, text, style string) timing.Event {
	return timing.Event{StartMs: startMs, EndMs: endMs, Text: text, Style: style}
}

var srtEvents = []timing.Event{
	ev(1000, 2500, "Where are you going?", ""),
	ev(3000, 4000, "<i>Home, I think.</i>\nIt is late.", ""),
	ev(5250, 7000, "See you tomorrow.", ""),
}

// accepted is every AC2 fixture ParseCues reads, with its format.
var accepted = []struct {
	file   string
	format timing.Format
	events int
}{
	{"srt-crlf.srt", timing.SRT, 3},
	{"srt-utf16le.srt", timing.SRT, 3},
	{"srt-utf16be.srt", timing.SRT, 3},
	{"srt-windows-1251.srt", timing.SRT, 3},
	{"srt-windows-1252.srt", timing.SRT, 2},
	{"ass-signs-styles.ass", timing.ASS, 4},
	{"ass-no-scripttype.ass", timing.ASS, 2},
	{"ass-comments.ass", timing.ASS, 2},
	{"ssa.ssa", timing.SSA, 2},
	{"webvtt.vtt", timing.SRT, 3},
	{"ass-saved-as.srt", timing.ASS, 1},
	{"srt-saved-as.ass", timing.SRT, 3},
}

// TestFormatFromBytesNotName: the misnamed fixtures carry the other
// format's extension, so a parser keyed by extension fails them.
func TestFormatFromBytesNotName(t *testing.T) {
	t.Parallel()
	for _, tc := range accepted {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, fixture(t, tc.file))
			if got := c.Format(); got != tc.format {
				t.Errorf("Format() = %v, want %v", got, tc.format)
			}
			if got := c.Len(); got != tc.events {
				t.Errorf("Len() = %d, want %d", got, tc.events)
			}
		})
	}
}

func TestSRTEvents(t *testing.T) {
	t.Parallel()
	for _, file := range []string{"srt-crlf.srt", "srt-utf16le.srt", "srt-utf16be.srt", "srt-saved-as.ass"} {
		t.Run(file, func(t *testing.T) {
			t.Parallel()
			if got := mustParse(t, fixture(t, file)).Events(); !slices.Equal(got, srtEvents) {
				t.Errorf("Events() = %+v, want %+v", got, srtEvents)
			}
		})
	}
}

func TestSRTTimingLines(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		doc  string
		want []timing.Event
	}{
		{"one-digit fraction", "1\n0:00:01,5 --> 0:00:02,25\nA\n", []timing.Event{ev(1005, 2025, "A", "")}},
		{"dot separator", "1\n00:00:01.000 --> 00:00:02.000\nA\n", []timing.Event{ev(1000, 2000, "A", "")}},
		{"three-digit hours", "1\n100:00:00,000 --> 100:00:01,000\nA\n", []timing.Event{ev(360_000_000, 360_001_000, "A", "")}},
		{"position suffix", "1\n00:00:01,000 --> 00:00:02,000 X1:1 Y1:2\nA\n", []timing.Event{ev(1000, 2000, "A", "")}},
		{"no index line", "00:00:01,000 --> 00:00:02,000\nA\n\n00:00:03,000 --> 00:00:04,000\nB\n", []timing.Event{
			ev(1000, 2000, "A", ""), ev(3000, 4000, "B", ""),
		}},
		{"text runs to the next index and timing pair", "1\n00:00:01,000 --> 00:00:02,000\nA\n\nstill A\n7\n\n2\n00:00:03,000 --> 00:00:04,000\nB\n", []timing.Event{
			ev(1000, 2000, "A\n\nstill A\n7", ""), ev(3000, 4000, "B", ""),
		}},
		{"preamble kept as bytes", "Subtitles by nobody\n\n1\n00:00:01,000 --> 00:00:02,000\nA\n", []timing.Event{ev(1000, 2000, "A", "")}},
		{"lone CR line ends", "1\r00:00:01,000 --> 00:00:02,000\rA\rB\r", []timing.Event{ev(1000, 2000, "A\nB", "")}},
		{"four-digit fraction is no timing line", "1\n00:00:01,0000 --> 00:00:02,000\nA\n\n2\n00:00:03,000 --> 00:00:04,000\nB\n", []timing.Event{ev(3000, 4000, "B", "")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustParse(t, []byte(tc.doc)).Events(); !slices.Equal(got, tc.want) {
				t.Errorf("Events() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestASSEvents(t *testing.T) {
	t.Parallel()
	want := []timing.Event{
		ev(1000, 2500, "Where are you going?", "Default"),
		ev(1500, 4000, `{\pos(960,100)\fad(200,200)}NORTH STATION`, "Sign"),
		ev(3000, 4000, "Home, I think.\nIt is late.", "Default"),
		ev(5250, 7000, `{\i1}See you{\i0} tomorrow.`, "Default"),
	}
	if got := mustParse(t, fixture(t, "ass-signs-styles.ass")).Events(); !slices.Equal(got, want) {
		t.Errorf("Events() = %+v, want %+v", got, want)
	}
}

func TestASSFormatLine(t *testing.T) {
	t.Parallel()
	head := "[Script Info]\nScriptType: v4.00+\n\n[Events]\n"
	tests := []struct {
		name string
		body string
		want []timing.Event
	}{
		{"reordered fields", "Format: Start, Style, End, Text\nDialogue: 0:00:01.00,Sign,0:00:02.00,a, b\n", []timing.Event{ev(1000, 2000, "a, b", "Sign")}},
		{"no style field", "Format: Start, End, Text\nDialogue: 0:00:01.00,0:00:02.00,a\n", []timing.Event{ev(1000, 2000, "a", "")}},
		{"no format line", "Dialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,a\n", []timing.Event{ev(1000, 2000, "a", "Default")}},
		{"format line without text last falls back", "Format: Text, Start, End\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,a\n", []timing.Event{ev(1000, 2000, "a", "Default")}},
		{"one-digit centiseconds", "Dialogue: 0,0:00:01.5,0:00:02.05,Default,,0,0,0,,a\n", []timing.Event{ev(1050, 2050, "a", "Default")}},
		{"unreadable time is not an event", "Dialogue: 0,0:00:01.000,0:00:02.00,Default,,0,0,0,,a\nDialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,b\n", []timing.Event{ev(3000, 4000, "b", "Default")}},
		{"too few fields is not an event", "Dialogue: 0,0:00:01.00,0:00:02.00\nDialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,b\n", []timing.Event{ev(3000, 4000, "b", "Default")}},
		{"dialogue outside events is not an event", "Dialogue: 0,0:00:03.00,0:00:04.00,Default,,0,0,0,,b\n[Fonts]\nDialogue: 0,0:00:05.00,0:00:06.00,Default,,0,0,0,,c\n", []timing.Event{ev(3000, 4000, "b", "Default")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustParse(t, []byte(head+tc.body)).Events(); !slices.Equal(got, tc.want) {
				t.Errorf("Events() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCommentLinesUntimed(t *testing.T) {
	t.Parallel()
	doc := fixture(t, "ass-comments.ass")
	c := mustParse(t, doc)
	want := []timing.Event{
		ev(1000, 2500, "Where are you going?", "Default"),
		ev(3000, 4000, "Home, I think.", "Default"),
	}
	if got := c.Events(); !slices.Equal(got, want) {
		t.Errorf("Events() = %+v, want %+v", got, want)
	}
	if got := storedDoc(t, c); !bytes.Equal(got, doc) {
		t.Errorf("stored document differs from the input:\n%s", got)
	}
}

func TestASSWithoutScriptType(t *testing.T) {
	t.Parallel()
	events := "\n[Events]\nDialogue: 0,0:00:01.00,0:00:02.00,Default,,0,0,0,,a\n"
	tests := []struct {
		name string
		head string
		want timing.Format
	}{
		{"V4+ Styles is ASS", "[Script Info]\n\n[V4+ Styles]\n", timing.ASS},
		{"V4 Styles is SSA", "[Script Info]\n\n[V4 Styles]\n", timing.SSA},
		{"header case is ignored", "[script info]\n\n[v4 styles]\n", timing.SSA},
		{"ScriptType wins over the styles header", "[Script Info]\nScriptType: v4.00\n\n[V4+ Styles]\n", timing.SSA},
		{"ScriptType v4.00+ is ASS", "[Script Info]\nScriptType: V4.00+\n\n[V4 Styles]\n", timing.ASS},
		{"neither is ASS", "[Script Info]\n", timing.ASS},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustParse(t, []byte(tc.head+events)).Format(); got != tc.want {
				t.Errorf("Format() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWebVTTConvertedOnce(t *testing.T) {
	t.Parallel()
	c := mustParse(t, fixture(t, "webvtt.vtt"))
	if c.Format() != timing.SRT {
		t.Errorf("Format() = %v, want SRT", c.Format())
	}
	wantDoc := "1\n00:00:01,000 --> 00:00:02,500\nWhere are you going?\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\n<i>Home</i>, I think.\nFish & chips <3\n\n" +
		"3\n00:00:05,250 --> 00:00:07,000\n<b>See</b> you <u>tomorrow</u>.\n\n"
	doc := storedDoc(t, c)
	if string(doc) != wantDoc {
		t.Errorf("stored document = %q, want %q", doc, wantDoc)
	}
	again := mustParse(t, doc)
	if !slices.Equal(again.Events(), c.Events()) || again.Format() != timing.SRT {
		t.Errorf("the stored SRT re-parses to %+v, want %+v", again.Events(), c.Events())
	}
	if got := storedDoc(t, again); !bytes.Equal(got, doc) {
		t.Errorf("the stored SRT converted again: %q", got)
	}
}

func TestWebVTTCueRules(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		doc  string
		want []timing.Event
	}{
		{"hours present", "WEBVTT\n\n01:00:00.000 --> 01:00:01.000\na\n", []timing.Event{ev(3_600_000, 3_601_000, "a", "")}},
		{"arrow in the payload starts the next cue", "WEBVTT\n\n00:01.000 --> 00:02.000\na\n00:03.000 --> 00:04.000\nb\n", []timing.Event{
			ev(1000, 2000, "a", ""), ev(3000, 4000, "b", ""),
		}},
		{"two-digit fraction drops the cue", "WEBVTT\n\n00:01.00 --> 00:02.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"identifier without timing drops the block", "WEBVTT\n\nid\nnot a timing\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"unterminated tag runs to the line end", "WEBVTT\n\n00:01.000 --> 00:02.000\na<c.x\n", []timing.Event{ev(1000, 2000, "a", "")}},
		{"unknown reference kept", "WEBVTT\n\n00:01.000 --> 00:02.000\nR&D &copy; &nbsp;x\n", []timing.Event{ev(1000, 2000, "R&D &copy; \u00a0x", "")}},
		{"CRLF kept", "WEBVTT\r\n\r\n00:01.000 --> 00:02.000\r\na\r\n", []timing.Event{ev(1000, 2000, "a", "")}},
		{"second over 59 in MM:SS.mmm drops the cue", "WEBVTT\n\n00:60.000 --> 01:01.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"minute over 59 in MM:SS.mmm drops the cue", "WEBVTT\n\n60:00.000 --> 61:00.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"minute over 59 in H:MM:SS.mmm drops the cue", "WEBVTT\n\n01:60:00.000 --> 01:61:00.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"second over 59 in H:MM:SS.mmm drops the cue", "WEBVTT\n\n01:00:60.000 --> 01:01:00.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"one digit before the dot drops the cue", "WEBVTT\n\n0:01.000 --> 0:02.000\na\n\n00:03.000 --> 00:04.000\nb\n", []timing.Event{ev(3000, 4000, "b", "")}},
		{"four hour digits", "WEBVTT\n\n0100:00:00.000 --> 0100:00:01.000\na\n", []timing.Event{ev(360_000_000, 360_001_000, "a", "")}},
		{"the last SRT stamp", "WEBVTT\n\n999:59:59.998 --> 999:59:59.999\na\n", []timing.Event{ev(3_599_999_998, 3_599_999_999, "a", "")}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mustParse(t, []byte(tc.doc)).Events(); !slices.Equal(got, tc.want) {
				t.Errorf("Events() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// srtOf builds an SRT document of n one-letter cues.
func srtOf(n int) []byte {
	var b strings.Builder
	for range n {
		b.WriteString(oneCue)
	}
	return []byte(b.String())
}

// padded is a one-cue SRT document of exactly n bytes.
func padded(n int) []byte {
	head := "00:00:01,000 --> 00:00:02,000\n"
	return append([]byte(head), bytes.Repeat([]byte("a"), n-len(head))...)
}

const maxDocument = 16 << 20

const assHead = "[Script Info]\nScriptType: v4.00+\n\n[Events]\n"

// vttPast999 holds a stamp srtStamp would write with four hour digits,
// which parseSRT cannot read back.
const vttPast999 = "WEBVTT\n\n00:01.000 --> 00:02.000\na\n\n1000:00:00.000 --> 1000:00:01.000\nb\n"

const vttPastSRT = "WebVTT timestamp past 999:59:59.999"

// refusedDocs is every ParseCues refusal; codes_test.go drives each too.
func refusedDocs(t testing.TB) []struct {
	name string
	b    []byte
} {
	t.Helper()
	return []struct {
		name string
		b    []byte
	}{
		{"MicroDVD", fixture(t, "microdvd.sub")},
		{"VobSub", fixture(t, "vobsub.sub")},
		{"ISO-2022-JP", fixture(t, "srt-iso-2022-jp.srt")},
		{"empty", nil},
		{"16 MiB plus one byte", padded(maxDocument + 1)},
		{"100,001 events", srtOf(100_001)},
		{"WebVTT with no cue", []byte("WEBVTT\n\nNOTE nothing\n")},
		{"WebVTT hour past 999", []byte(vttPast999)},
		{"UTF-16 of odd length", append(fixture(t, "srt-utf16le.srt"), 'x')},
		{"stored zip of an SRT", storedZip(t, fixture(t, "srt-crlf.srt"))},
		{"rar4 archive", append([]byte("Rar!\x1a\x07\x00"), fixture(t, "srt-crlf.srt")...)},
		{"binary data", bytes.Repeat([]byte{0, 1, 2, 'a'}, 128)},
	}
}

// storedZip is a zip holding b uncompressed, so its timestamps are plain bytes.
func storedZip(t testing.TB, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "a.srt", Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestRefusalNamesWhatItFound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		b          []byte
	}{
		{"stored zip of an SRT", "zip archive", storedZip(t, fixture(t, "srt-crlf.srt"))},
		{"rar4", "rar4 archive", append([]byte("Rar!\x1a\x07\x00"), fixture(t, "srt-crlf.srt")...)},
		{"rar5", "rar5 archive", []byte("Rar!\x1a\x07\x01\x00")},
		{"7z", "7z archive", []byte("7z\xbc\xaf\x27\x1c")},
		{"gzip", "gzip archive", []byte("\x1f\x8b\x08")},
		{"xz", "xz archive", []byte("\xfd7zXZ\x00")},
		{"bzip2", "bzip2 archive", []byte("BZh91AY&SY")},
		{"empty zip", "empty zip archive", []byte("PK\x05\x06")},
		{"VobSub", "VobSub (MPEG program stream)", fixture(t, "vobsub.sub")},
		{"MicroDVD", "MicroDVD", fixture(t, "microdvd.sub")},
		{"MicroDVD with an open end", "MicroDVD", []byte("{10}{}Hello\n")},
		{"binary", "binary data (384 of the first 512 bytes are not text)", bytes.Repeat([]byte{0, 1, 2, 'a'}, 128)},
		{"text with no timestamp", "no timed event", []byte("{1}x}\nhello\n")},
		{"ISO-2022-JP", "ISO2022JP is not ASCII-transparent", fixture(t, "srt-iso-2022-jp.srt")},
		{"WebVTT hour past 999 after a readable cue", vttPastSRT, []byte(vttPast999)},
		{"WebVTT hour wrapping int64 to 1", vttPastSRT, []byte("WEBVTT\n\n18446744073709551617:00:00.000 --> 18446744073709551617:00:01.000\na\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := timing.ParseCues(tc.b)
			if !errors.Is(err, timing.ErrFormatUnsupported) || !strings.HasSuffix(err.Error(), ": "+tc.want) {
				t.Errorf("ParseCues error = %v, want ErrFormatUnsupported ending %q", err, tc.want)
			}
		})
	}
}

func TestRefusedFormats(t *testing.T) {
	t.Parallel()
	for _, tc := range refusedDocs(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := timing.ParseCues(tc.b); !errors.Is(err, timing.ErrFormatUnsupported) {
				t.Errorf("ParseCues error = %v, want ErrFormatUnsupported", err)
			}
		})
	}
}

func TestLimitsAccepted(t *testing.T) {
	t.Parallel()
	t.Run("exactly 16 MiB", func(t *testing.T) {
		t.Parallel()
		mustParse(t, padded(maxDocument))
	})
	t.Run("exactly 100,000 events", func(t *testing.T) {
		t.Parallel()
		if got := mustParse(t, srtOf(100_000)).Len(); got != 100_000 {
			t.Errorf("Len() = %d, want 100000", got)
		}
	})
}

func TestParseCuesKeepsNoCallerBuffer(t *testing.T) {
	t.Parallel()
	b := fixture(t, "srt-crlf.srt")
	c := mustParse(t, b)
	want := c.Events()
	clear(b)
	if got := c.Events(); !slices.Equal(got, want) {
		t.Errorf("Events() after the caller reused its buffer = %+v, want %+v", got, want)
	}
}

func TestCueCodecRoundTrip(t *testing.T) {
	t.Parallel()
	for _, tc := range accepted {
		t.Run(tc.file, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, fixture(t, tc.file))
			stored := timing.AppendCues(nil, c)
			got, err := timing.DecodeCues(stored)
			if err != nil {
				t.Fatalf("DecodeCues: %v", err)
			}
			if got.Format() != c.Format() || !slices.Equal(got.Events(), c.Events()) {
				t.Errorf("DecodeCues = %v %+v, want %v %+v", got.Format(), got.Events(), c.Format(), c.Events())
			}
			if again := timing.AppendCues(nil, got); !bytes.Equal(again, stored) {
				t.Errorf("AppendCues(DecodeCues(b)) differs from b")
			}
			clear(stored)
			if !slices.Equal(got.Events(), c.Events()) {
				t.Errorf("DecodeCues result changed when the caller reused its buffer")
			}
		})
	}
}

// TestCueCodecGolden pins the stored layout and SE-1's Encoding value for
// UTF-8 (1): a renumbered Encoding or a layout change fails here.
func TestCueCodecGolden(t *testing.T) {
	t.Parallel()
	doc := "1\n00:00:01,000 --> 00:00:02,000\nA\n"
	want := append([]byte{1, byte(timing.SRT), 1, byte(len(doc))}, doc...)
	if got := timing.AppendCues(nil, mustParse(t, []byte(doc))); !bytes.Equal(got, want) {
		t.Errorf("AppendCues = % x, want % x", got, want)
	}
}

func cueBytes(t testing.TB, layout, format, enc byte, doc string, trailing ...byte) []byte {
	t.Helper()
	b := binary.AppendUvarint([]byte{layout, format, enc}, uint64(len(doc)))
	return append(append(b, doc...), trailing...)
}

const oneCue = "00:00:01,000 --> 00:00:02,000\na\n"

// undecodableCues is every DecodeCues refusal; codes_test.go drives each too.
func undecodableCues(t testing.TB) []struct {
	name string
	b    []byte
} {
	t.Helper()
	srt := byte(timing.SRT)
	return []struct {
		name string
		b    []byte
	}{
		{"empty", nil},
		{"no length", []byte{1, srt, 1}},
		{"another layout", cueBytes(t, 2, srt, 1, oneCue)},
		{"trailing byte", cueBytes(t, 1, srt, 1, oneCue, 0)},
		{"short document", cueBytes(t, 1, srt, 1, oneCue)[:10]},
		{"format mismatch", cueBytes(t, 1, byte(timing.ASS), 1, oneCue)},
		{"unknown format", cueBytes(t, 1, 0, 1, oneCue)},
		{"document no longer parses", cueBytes(t, 1, srt, 1, "no cue here\n")},
		{"length overflows", append([]byte{1, srt, 1}, bytes.Repeat([]byte{0xff}, 11)...)},
		{"long document", append([]byte{1, srt, 1, byte(len(oneCue) + 1)}, oneCue...)},
		{"overlong length", append([]byte{1, srt, 1, byte(len(oneCue)) | 0x80, 0}, oneCue...)},
		{"undeclared encoding", cueBytes(t, 1, srt, 200, oneCue)},
		{"document over twice 16 MiB", cueBytes(t, 1, srt, 1, oneCue+strings.Repeat("a", 32<<20))},
	}
}

func TestDecodeCuesRefuses(t *testing.T) {
	t.Parallel()
	for _, tc := range undecodableCues(t) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := timing.DecodeCues(tc.b)
			if !errors.Is(err, timing.ErrFormatUnsupported) {
				t.Fatalf("DecodeCues error = %v, want ErrFormatUnsupported", err)
			}
			if n := strings.Count(err.Error(), timing.ErrFormatUnsupported.Error()); n != 1 {
				t.Errorf("DecodeCues error %q names the sentinel %d times", err, n)
			}
		})
	}
}

// storedDocLen is the length of the document AppendCues stores for c.
func storedDocLen(t testing.TB, c timing.Cues) int {
	t.Helper()
	b := timing.AppendCues(nil, c)
	n, k := binary.Uvarint(b[3:])
	if k <= 0 || int(n) != len(b)-3-k {
		t.Fatalf("AppendCues wrote a malformed length")
	}
	return int(n)
}

// TestWebVTTNearCapConverts: the 16 MiB cap is on the document ParseCues
// is given, so a WebVTT under it whose SRT conversion is longer parses,
// stores and decodes.
func TestWebVTTNearCapConverts(t *testing.T) {
	t.Parallel()
	const cues = 99_000
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	for range cues - 1 {
		b.WriteString("00:00.000-->00:00.001\n")
	}
	b.WriteString("00:00.000-->00:00.001\n")
	line := strings.Repeat("x", 999) + "\n"
	for b.Len()+len(line) <= 16<<20 {
		b.WriteString(line)
	}
	doc := []byte(b.String())
	c := mustParse(t, doc)
	if c.Len() != cues {
		t.Fatalf("Len() = %d, want %d", c.Len(), cues)
	}
	if n := storedDocLen(t, c); n <= 16<<20 {
		t.Fatalf("conversion is %d bytes; the case needs one over 16 MiB", n)
	}
	got, err := timing.DecodeCues(timing.AppendCues(nil, c))
	if err != nil {
		t.Fatalf("DecodeCues: %v", err)
	}
	if !slices.Equal(got.Events(), c.Events()) {
		t.Error("DecodeCues events differ from ParseCues'")
	}
}

// TestWebVTTConversionAtMostDoubles holds the worst cases of the bound
// maxParsedBytes rests on: minimal cues, CRLF output from an LF body, and
// one-byte payload lines.
func TestWebVTTConversionAtMostDoubles(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, doc string }{
		{"minimal cues", "WEBVTT\n\n" + strings.Repeat("00:00.000-->00:00.001\n", 1000)},
		{"CRLF output", "WEBVTT\r\n\n" + strings.Repeat("00:00.000-->00:00.001\n", 1000)},
		{"last cue unterminated", "WEBVTT\r\n\n" + strings.Repeat("00:00.000-->00:00.001\n", 9) + "00:00.000-->00:00.001"},
		{"one-byte payload lines", "WEBVTT\r\n\n00:00.000-->00:00.001\n" + strings.Repeat("x\n", 1000)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := mustParse(t, []byte(tc.doc))
			if n := storedDocLen(t, c); n > 2*len(tc.doc) {
				t.Errorf("conversion of %d bytes is %d bytes, over twice", len(tc.doc), n)
			}
		})
	}
}

// TestParseWorkLinear: a Format: line declaring a million fields costs the
// Dialogue lines after it nothing per declared field, and a WebVTT line of
// '&' with no ';' is read once. Not parallel: it reads the process's
// allocation total.
func TestParseWorkLinear(t *testing.T) {
	t.Run("ASS format fields", func(t *testing.T) {
		fields := strings.Repeat("x,", 100_000) + "Start, End, Text"
		doc := assHead + "Format: " + fields + "\n" +
			strings.Repeat("Dialogue: 0,0:00:01.00\n", 1000) + "Comment: x\n"
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if _, err := timing.ParseCues([]byte(doc)); !errors.Is(err, timing.ErrFormatUnsupported) {
			t.Fatalf("ParseCues error = %v, want ErrFormatUnsupported (no timed event)", err)
		}
		runtime.ReadMemStats(&after)
		if got := after.TotalAlloc - before.TotalAlloc; got > 16<<20 {
			t.Errorf("ParseCues allocated %d bytes for a %d-byte document", got, len(doc))
		}
	})
	t.Run("WebVTT references", func(t *testing.T) {
		doc := "WEBVTT\n\n00:01.000 --> 00:02.000\n" + strings.Repeat("&", 1<<20) + ";\n"
		start := time.Now()
		mustParse(t, []byte(doc))
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("ParseCues took %v on one line of '&'", elapsed)
		}
	})
}
