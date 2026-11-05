package subtitleenc

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/cplieger/subflux/internal/language"
	"golang.org/x/text/encoding/charmap"
)

func mustTag(t *testing.T, s string) language.Tag {
	t.Helper()
	tag, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q does not parse", s)
	}
	return tag
}

// subtitle is lines as a small SRT document, LF line endings.
func subtitle(lines ...string) string {
	var b strings.Builder
	for i, l := range lines {
		b.WriteString(strings.Repeat("1", i%3+1) + "\n00:00:01,000 --> 00:00:02,500\n" + l + "\n\n")
	}
	return b.String()
}

func TestDetectUnicodeForms(t *testing.T) {
	t.Parallel()
	und := language.Tag{}
	tests := []struct {
		name string
		in   []byte
		want Encoding
	}{
		{"empty", nil, UTF8},
		{"ascii", []byte("1\n00:00:01,000 --> 00:00:02,000\nHi\n"), UTF8},
		{"ascii_literal_hz", []byte("I said ~{this}~ ok\n"), UTF8},
		{"ansi_escape_in_utf8", []byte("\x1b[0mCafé\n"), UTF8},
		{"utf8_bom", []byte("\xef\xbb\xbfCafé"), UTF8},
		{"utf8_bom_on_invalid_bytes", []byte("\xef\xbb\xbfCaf\xe9"), Unknown},
		{"utf16le_bom", []byte{0xFF, 0xFE, 0xA4, 0x03}, UTF16LE},
		{"utf16be_bom", []byte{0xFE, 0xFF, 0x03, 0xA4}, UTF16BE},
		{"utf32le_bom", []byte{0xFF, 0xFE, 0, 0, 'A', 0, 0, 0, 'B', 0, 0, 0}, Unknown},
		{"utf16le_bom_odd_length", []byte{0xFF, 0xFE, 'H', 0, 'i'}, Unknown},
		{"utf16le_interleave", []byte{'1', 0, '\n', 0, 'H', 0}, UTF16LE},
		{"utf16be_interleave", []byte{0, '1', 0, '\n', 0, 'H'}, UTF16BE},
		{"utf16le_interleave_lone_surrogate", []byte{'1', 0, '\n', 0, 0x00, 0xD8, 'H', 0}, Unknown},
		{"utf16be_interleave_odd_length", []byte{0, '1', 0, '\n', 0}, Unknown},
		{"iso2022jp", []byte("1\n\x1b$B$\"$N\x1b(B\n"), ISO2022JP},
		{"iso2022jp_esc_dollar_at", []byte("1\n\x1b$@$\"$N\x1b(B\n"), ISO2022JP},
		{"iso2022jp_jis_roman", []byte("1\n\x1b$B$\"$N\x1b(J\\\n"), ISO2022JP},
		{"iso2022jp_no_closing_escape", []byte("1\n\x1b$B$\"$N"), ISO2022JP},
		{"iso2022jp_redundant_escape", []byte("1\n\x1b$B$\"\x1b$B$N\x1b(B\n"), ISO2022JP},
		{"iso2022jp_leading_ascii_escape", []byte("\x1b(B1\n\x1b$B$\"$N\x1b(B\n"), ISO2022JP},
		{"iso2022jp_line_feed_inside_jis", []byte("1\n\x1b$B$\"\n\x1b(B"), Unknown},
		{"iso2022jp_trail_outside_jis", []byte("1\n\x1b$B$\n\x1b(B"), Unknown},
		{"escapes_beside_an_8bit_byte", []byte("1\n\x1b$B$\"$N\x1b(B caf\xe9\n"), Unknown},
		{"escapes_beside_utf8_text", []byte("1\n\x1b$B$\"$N\x1b(B café\n"), UTF8},
		{"no_hint_row", []byte("caf\xe9"), Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Detect(tt.in, und); got != tt.want {
				t.Errorf("Detect = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestDetectHintRows(t *testing.T) {
	t.Parallel()
	serbian := mustEncode(t, Windows1251, subtitle("Не знам шта се десило са мојом сестром.", "Наравно да ћу поћи с тобом до станице.",
		"Зашто ми нико није рекао истину?", "Где је мој жути капут?", "Не можеш тек тако да одеш.", "Ова вест мења све."))
	norwegian := mustEncode(t, Windows1252, subtitle("Jeg vet ikke hva som skjedde med søsteren min.", "Selvfølgelig blir jeg med deg til stasjonen.",
		"Hvorfor sa ingen sannheten til meg?", "Hvor er den gule frakken min?", "Hørte du hva presten sa?", "Denne nyheten forandrer alt."))
	big5, err := os.ReadFile(filepath.Join("testdata", "corpus", "zhhant-big5.srt"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, hint string
		in         []byte
		want       Encoding
	}{
		{"serbian", "sr", serbian, Windows1251},
		{"serbian_cyrillic_region", "sr-RS", serbian, Windows1251},
		{"serbian_latin_has_no_row", "sr-Latn", serbian, Unknown},
		{"serbian_latin_region_has_no_row", "sr-ME", serbian, Unknown},
		{"norwegian", "no", norwegian, Windows1252},
		{"bokmal_reads_the_norwegian_row", "nb", norwegian, Windows1252},
		{"chinese_region_form", "zh-HK", big5, Big5},
		{"baltic_has_no_row", "lt", norwegian, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := Detect(tt.in, mustTag(t, tt.hint)); got != tt.want {
				t.Errorf("Detect(_, %s) = %s, want %s", tt.hint, got, tt.want)
			}
		})
	}
}

// TestDetectSampleEndsOnALine: a sample cut inside a double-byte character
// would refuse a valid multibyte page.
func TestDetectSampleEndsOnALine(t *testing.T) {
	t.Parallel()
	line := subtitle("妹に何があったのか分からない。", "もちろん、駅まで一緒に行くよ。", "私の黄色いコートはどこ？")
	for pad := range 64 {
		text := strings.Repeat("x", pad) + strings.Repeat(line, SampleBytes/len(mustEncode(t, ShiftJIS, line))+2)
		data := mustEncode(t, ShiftJIS, text)
		if _, err := ShiftJIS.Decode(data[:SampleBytes]); err == nil {
			continue
		}
		if got := Detect(data, mustTag(t, "ja")); got != ShiftJIS {
			t.Errorf("Detect = %s, want ShiftJIS", got)
		}
		return
	}
	t.Fatal("no padding puts the SampleBytes cut inside a character")
}

func TestPick(t *testing.T) {
	t.Parallel()
	a, b := []byte("Mąż"), []byte("Mšż")
	tests := []struct {
		name string
		rs   []reading
		want Encoding
	}{
		{"none", nil, Unknown},
		{"top_score", []reading{{enc: Windows1250, text: a, score: 40}, {enc: ISO8859_2, text: b, score: 50}}, ISO8859_2},
		{"lead_of_sibling_margin", []reading{{enc: Windows1250, text: a, score: 50}, {enc: ISO8859_2, text: b, score: 50 - SiblingMargin}}, Windows1250},
		{"lead_under_sibling_margin", []reading{{enc: Windows1250, text: a, score: 50}, {enc: ISO8859_2, text: b, score: 51 - SiblingMargin}}, Unknown},
		{"tie_decoding_alike_takes_the_row_order", []reading{{enc: Windows1255, text: a, score: 50}, {enc: ISO8859_8, text: a, score: 50}}, Windows1255},
		{"third_within_margin", []reading{{enc: Windows1251, text: a, score: 60}, {enc: KOI8R, text: a, score: 59}, {enc: ISO8859_5, text: b, score: 58}}, Unknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := pick(tt.rs); got != tt.want {
				t.Errorf("pick = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestFloorsHoldOnCorpus pins every models row against the committed corpus:
// each row's lowest is at most the score of every committed true reading
// under that model, and every row has one.
func TestFloorsHoldOnCorpus(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.srt"))
	if err != nil || len(files) == 0 {
		t.Fatalf("corpus: %d files, %v", len(files), err)
	}
	names := map[string]Encoding{}
	for e := UTF8; int(e) < len(encodingNames); e++ {
		names[e.String()] = e
	}
	low := map[*model]int{}
	for _, path := range files {
		want := readCorpusExpect(t, strings.TrimSuffix(path, ".srt")+".expect.json")
		r, ok := rowFor(mustTag(t, want.Hint))
		enc, named := names[want.Encoding]
		if !ok || !named || !slices.Contains(r.pages, enc) {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text, ok := readPage(enc, sampleOf(data))
		if !ok {
			t.Fatalf("%s: its own page refuses it", path)
		}
		s := r.score(reading{enc: enc, text: text}, sampleOf(data))
		if r.model == nil {
			if s < multibyteFloor {
				t.Errorf("%s: multibyte score %d under the floor %d", path, s, multibyteFloor)
			}
			continue
		}
		if l, seen := low[r.model]; !seen || s < l {
			low[r.model] = s
		}
	}
	for _, m := range models {
		l, seen := low[modelsByLang[m.lang]]
		switch {
		case !seen:
			t.Errorf("%s: no committed file pins this row", m.lang)
		case l < m.lowest:
			t.Errorf("%s: a committed true reading scores %d, under the row's lowest %d", m.lang, l, m.lowest)
		}
	}
}

// TestSiblingPairs drives the sibling step alone over every ruled pair, in
// both directions, from the bytes where the two pages differ.
func TestSiblingPairs(t *testing.T) {
	t.Parallel()
	pairOf := func(t *testing.T, win, iso *charmap.Charmap) siblingPair {
		t.Helper()
		for _, p := range siblingPairs {
			if p.win == win && p.iso == iso {
				return p
			}
		}
		t.Fatalf("%v and %v are no sibling pair", win, iso)
		return siblingPair{}
	}
	written := func(t *testing.T, cm *charmap.Charmap, text string) []byte {
		t.Helper()
		b, err := cm.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatalf("%v cannot write %q: %v", cm, text, err)
		}
		return b
	}
	// letterBytes classifies the no-C1 bytes where the pair decodes
	// differently, by which pages read them as a letter or mark.
	letterBytes := func(p siblingPair) (both, winOnly, isoOnly, neitherPage int) {
		for b := 0xA0; b <= 0xFF; b++ {
			wr, ir := p.win.DecodeByte(byte(b)), p.iso.DecodeByte(byte(b))
			if wr == ir {
				continue
			}
			wl := unicode.IsLetter(wr) || unicode.IsMark(wr)
			il := unicode.IsLetter(ir) || unicode.IsMark(ir)
			switch {
			case wl && il:
				both++
			case wl:
				winOnly++
			case il:
				isoOnly++
			default:
				neitherPage++
			}
		}
		return both, winOnly, isoOnly, neitherPage
	}
	type sample struct {
		name string
		in   []byte
		want side
		text string // the original, which the chosen page decodes to
	}
	check := func(t *testing.T, p siblingPair, m *model, samples []sample) {
		t.Helper()
		for _, s := range samples {
			got := p.choose(s.in, m)
			if got != s.want {
				t.Errorf("%s: side %d, want %d", s.name, got, s.want)
				continue
			}
			cm := p.win
			if got == isoPage {
				cm = p.iso
			}
			if dec, _ := cm.NewDecoder().Bytes(s.in); got != neither && string(dec) != s.text {
				t.Errorf("%s: decodes to %q, want %q", s.name, dec, s.text)
			}
		}
	}

	t.Run("windows-1250_iso-8859-2", func(t *testing.T) {
		t.Parallel()
		p := pairOf(t, charmap.Windows1250, charmap.ISO8859_2)
		czech, err := os.ReadFile(filepath.Join("testdata", "corpus", "cs-iso88592.srt"))
		if err != nil {
			t.Fatal(err)
		}
		czechText, _ := charmap.ISO8859_2.NewDecoder().Bytes(czech)
		const ws, is, tie = "Ślub się odbędzie w środę.", "Źle się czuję, muszę iść.", "Mąż przyjedzie jutro."
		check(t, p, modelsByLang["pl"], []sample{
			{"windows-written_s-acute_only_windows_reads_as_a_letter", written(t, charmap.Windows1250, ws), windowsPage, ws},
			{"iso-written_a-ogonek_only_iso_reads_as_a_letter", written(t, charmap.ISO8859_2, is), isoPage, is},
			{"windows-written_both_read_letters_tie", written(t, charmap.Windows1250, tie), windowsPage, tie},
			{"both_read_a_differing_byte_as_a_non-letter", []byte("10 \x80, \xb1 5"), neither, ""},
		})
		check(t, p, modelsByLang["cs"], []sample{
			{"iso-written_czech_leads_by_sibling_margin", czech, isoPage, string(czechText)},
		})
	})

	for _, pair := range []struct {
		name     string
		win, iso *charmap.Charmap
		text     string
	}{
		{"windows-1252_iso-8859-1", charmap.Windows1252, charmap.ISO8859_1, "Où est mon manteau, señor ?"},
		{"windows-1254_iso-8859-9", charmap.Windows1254, charmap.ISO8859_9, "Öylece çekip gidemezsin, ağabey."},
	} {
		t.Run(pair.name, func(t *testing.T) {
			t.Parallel()
			p := pairOf(t, pair.win, pair.iso)
			for b := 0xA0; b <= 0xFF; b++ {
				if wr, ir := p.win.DecodeByte(byte(b)), p.iso.DecodeByte(byte(b)); wr != ir {
					t.Errorf("byte %#x: %q and %q", b, wr, ir)
				}
			}
			check(t, p, nil, []sample{
				{"windows-written", written(t, pair.win, pair.text), windowsPage, pair.text},
				{"iso-written", written(t, pair.iso, pair.text), windowsPage, pair.text},
			})
		})
	}

	t.Run("windows-1253_iso-8859-7", func(t *testing.T) {
		t.Parallel()
		p := pairOf(t, charmap.Windows1253, charmap.ISO8859_7)
		// No byte reads as a letter on both pages, so this pair never ties.
		if both, winOnly, isoOnly, _ := letterBytes(p); both != 0 || winOnly == 0 || isoOnly == 0 {
			t.Errorf("letters on both %d, Windows only %d, ISO only %d", both, winOnly, isoOnly)
		}
		const text = "Άκου, θα έρθω αύριο."
		w, i := written(t, charmap.Windows1253, text), written(t, charmap.ISO8859_7, text)
		check(t, p, modelsByLang["el"], []sample{
			{"windows-written", w, windowsPage, text},
			{"iso-written", i, isoPage, text},
			{"both_read_a_differing_byte_as_a_non-letter", append(bytes.Clone(w), i...), neither, ""},
		})
	})

	t.Run("windows-1255_iso-8859-8", func(t *testing.T) {
		t.Parallel()
		p := pairOf(t, charmap.Windows1255, charmap.ISO8859_8)
		// ISO-8859-8 reads no differing byte as a letter: only the refusal
		// step names it (TestDetectCorpus, he-iso88598-df).
		if both, winOnly, isoOnly, _ := letterBytes(p); both != 0 || winOnly == 0 || isoOnly != 0 {
			t.Errorf("letters on both %d, Windows only %d, ISO only %d", both, winOnly, isoOnly)
		}
		const niqqud, plain = "שָׁלוֹם, אֲנִי כָּאן.", "שלום, אני כאן."
		check(t, p, modelsByLang["he"], []sample{
			{"windows-written_niqqud", written(t, charmap.Windows1255, niqqud), windowsPage, niqqud},
			{"iso-written_no_differing_byte", written(t, charmap.ISO8859_8, plain), windowsPage, plain},
			{"iso-written_double_low_line", written(t, charmap.ISO8859_8, "‗‗ "+plain), neither, ""},
		})
	})

	t.Run("windows-1257_iso-8859-13", func(t *testing.T) {
		t.Parallel()
		p := pairOf(t, charmap.Windows1257, charmap.ISO8859_13)
		// The two differ only at punctuation, which the letter test reads as
		// a non-letter on both pages.
		if both, winOnly, isoOnly, neitherPage := letterBytes(p); both+winOnly+isoOnly != 0 || neitherPage == 0 {
			t.Errorf("letters on both %d, Windows only %d, ISO only %d, neither %d", both, winOnly, isoOnly, neitherPage)
		}
		const plain = "Žinoma, eisiu su tavimi į stotį."
		check(t, p, nil, []sample{
			{"no_differing_byte", written(t, charmap.Windows1257, plain), windowsPage, plain},
			{"windows-written_acute_accent", written(t, charmap.Windows1257, "Tai jo´s paltas."), neither, ""},
			{"iso-written_quotes", written(t, charmap.ISO8859_13, "„Labas“, tarė jis."), neither, ""},
		})
	})

	t.Run("windows-1256_iso-8859-6_are_no_pair", func(t *testing.T) {
		t.Parallel()
		for _, p := range siblingPairs {
			if p.winEnc == Windows1256 || p.isoEnc == ISO8859_6 || p.win == charmap.Windows1256 {
				t.Errorf("a sibling pair holds windows-1256 or ISO-8859-6")
			}
		}
		const text = "لا أعرف ماذا حدث لأختي. هل سمعت ما قاله الكاهن؟"
		iso := written(t, charmap.ISO8859_6, strings.Repeat(subtitle(text), 8))
		var rs []reading
		for _, enc := range []Encoding{Windows1256, ISO8859_6} {
			out, ok := readPage(enc, iso)
			if !ok {
				t.Fatalf("%s refuses the sample", enc)
			}
			rs = append(rs, reading{enc: enc, text: out})
		}
		if left := settleSiblings(rs, []Encoding{Windows1256, ISO8859_6}, iso, modelsByLang["ar"]); len(left) != 2 {
			t.Errorf("the sibling step left %d of the two readings", len(left))
		}
		if got := Detect(iso, mustTag(t, "ar")); got == Windows1256 {
			t.Errorf("Detect = Windows1256")
		}
	})
}

// TestReadPage pins design 7's refusal step on single pages: a C1 control,
// KOI8-R's box characters where KOI8-U has letters, and bytes Encode would
// not write back.
func TestReadPage(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		enc  Encoding
		in   []byte
		ok   bool
	}{
		{"windows-1251_ellipsis", Windows1251, []byte("\xcf\xf0\xe8\xe2\xe5\xf2\x85"), true},
		{"iso-8859-5_reads_it_as_c1", ISO8859_5, []byte("\xcf\xf0\xe8\xe2\xe5\xf2\x85"), false},
		{"iso-8859-6_reads_windows-1256_peh_as_c1", ISO8859_6, []byte("\xc7\x81 \xc7"), false},
		{"gb18030_four-byte_c1", GB18030, []byte{0xB2, 0xBB, 0x81, 0x30, 0x81, 0x35}, false},
		{"gb18030_without_one", GB18030, []byte{0xB2, 0xBB}, true},
		{"koi8-r_on_a_koi8-u_letter", KOI8R, []byte("\xf0\xd2\xc9\xa4"), false},
		{"koi8-r_without_one", KOI8R, []byte("\xf0\xd2\xc9"), true},
		{"big5_hkscs_duplicate_code", Big5, []byte{0xFA, 0xBD}, false},
		{"big5_standard_code", Big5, []byte{0xA5, 0x5D}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, ok := readPage(tt.enc, tt.in); ok != tt.ok {
				t.Errorf("readPage(%s) ok = %v, want %v", tt.enc, ok, tt.ok)
			}
		})
	}
}

// TestDetectByteLevel holds the byte-level habits a transcoded corpus file
// cannot carry, each built here from self-written text.
func TestDetectByteLevel(t *testing.T) {
	t.Parallel()
	greek := subtitle("Άκου, θα έρθω αύριο.", "Δεν ξέρω τι έγινε με την αδελφή μου.", "Πού είναι το κίτρινο παλτό μου;")

	for _, tt := range []struct {
		name string
		bom  []byte
		enc  Encoding
	}{
		{"bom_utf8", bomUTF8, UTF8},
		{"bom_utf16le", bomUTF16LE, UTF16LE},
		{"bom_utf16be", bomUTF16BE, UTF16BE},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := mustEncode(t, tt.enc, greek)
			data := append(bytes.Clone(tt.bom), bytes.TrimPrefix(body, tt.bom)...)
			if got := Detect(data, mustTag(t, "el")); got != tt.enc {
				t.Fatalf("Detect = %s, want %s", got, tt.enc)
			}
			if out, err := tt.enc.Decode(data); err != nil || string(out) != greek {
				t.Errorf("Decode = %q, %v", out, err)
			}
		})
	}

	t.Run("crlf", func(t *testing.T) {
		t.Parallel()
		lf := subtitle("Nie wiem, co się stało z moją siostrą.", "Ściągnij buty, zanim wejdziesz do środka.", "Gdzie jest mój żółty płaszcz?")
		crlf := strings.ReplaceAll(lf, "\n", "\r\n")
		pl := mustTag(t, "pl")
		want := Detect(mustEncode(t, Windows1250, lf), pl)
		data := mustEncode(t, Windows1250, crlf)
		if got := Detect(data, pl); got != want || got != Windows1250 {
			t.Fatalf("Detect(CRLF) = %s, Detect(LF) = %s, want Windows1250 for both", got, want)
		}
		if out, err := Windows1250.Decode(data); err != nil || string(out) != crlf {
			t.Errorf("Decode does not keep every CR: %q, %v", out, err)
		}
	})

	// A Windows-1250 half, then an ISO-8859-2 half holding "ą" (0xB1, "±" in
	// 1250). With no C1 byte in the 1250 half its "ą" (0xB9, "š" in ISO) is a
	// letter on both pages, so nothing tells the halves apart: the letter
	// refusal drops 1250 and ISO8859_2 misreads that half. A 1250 half holding
	// "ś" (0x9C, a C1 control in ISO) refuses ISO, and 1250's "±" then drops
	// the Windows page too: Unknown, the bytes kept.
	isoHalf := strings.Repeat(subtitle("Źle się czuję, muszę iść.", "Ona ma długą suknię."), 4)
	for _, tt := range []struct {
		name, winText string
		want          Encoding
	}{
		{"mixed_page_no_c1", "Mąż przyjedzie jutro. Mamy mało czasu.", ISO8859_2},
		{"mixed_page_c1", "Ślub się odbędzie w środę.", Unknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			isoBytes := mustEncode(t, ISO8859_2, isoHalf)
			winHalf := subtitle(tt.winText)
			for len(winHalf) < len(isoBytes) {
				winHalf += subtitle(tt.winText)
			}
			data := append(mustEncode(t, Windows1250, winHalf), isoBytes...)
			got := Detect(data, mustTag(t, "pl"))
			if got != tt.want {
				t.Fatalf("Detect = %s, want %s", got, tt.want)
			}
			if got == Unknown {
				return
			}
			if out, err := got.Decode(data); err != nil || !strings.HasSuffix(string(out), isoHalf) {
				t.Errorf("the ISO half does not decode to its original: %v", err)
			}
		})
	}

	// The mixed-page rule's cost: a genuine Windows file holding a C1 byte
	// and a symbol its ISO sibling reads as a letter is a missed conversion.
	for _, tt := range []struct {
		name, hint string
		enc        Encoding
		text       string
	}{
		{"windows-1250_c1_and_a_symbol", "pl", Windows1250, "Ślub się odbędzie w środę… © Telewizja Polska"},
		{"windows-1253_c1_and_a_symbol", "el", Windows1253, "Δεν ξέρω τι έγινε με την αδελφή μου… Πού είναι το κίτρινο παλτό μου; ¶ Θα έρθω αύριο."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			data := mustEncode(t, tt.enc, strings.Repeat(subtitle(tt.text), 4))
			if got := Detect(data, mustTag(t, tt.hint)); got != Unknown {
				t.Errorf("Detect = %s, want Unknown", got)
			}
		})
	}

	for _, tt := range []struct {
		name, hint string
		enc        Encoding
		text       string
		broken     []byte
	}{
		{"invalid_shiftjis", "ja", ShiftJIS, "妹に何があったのか分からない。", []byte{0x82, 0x20}},
		{"invalid_eucjp", "ja", EUCJP, "妹に何があったのか分からない。", []byte{0xA4, 0x20}},
		{"invalid_gb18030", "zh-Hans", GB18030, "我不知道我妹妹发生了什么事。", []byte{0x81, 0x20}},
		{"invalid_big5", "zh-Hant", Big5, "我不知道我妹妹發生了什麼事。", []byte{0xA4, 0x20}},
		{"invalid_euckr", "ko", EUCKR, "여동생에게 무슨 일이 있었는지 모르겠어.", []byte{0xB0, 0x20}},
		{"big5_hkscs_composed_pair", "zh-Hant", Big5, "我不知道我妹妹發生了什麼事。", []byte{0x88, 0x62}},
		{"shiftjis_nec_selected_duplicate", "ja", ShiftJIS, "妹に何があったのか分からない。", []byte{0xEE, 0xE0}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			valid := mustEncode(t, tt.enc, strings.Repeat(subtitle(tt.text), 20))
			data := append(append(bytes.Clone(valid[:len(valid)/2]), tt.broken...), valid[len(valid)/2:]...)
			if got := Detect(valid, mustTag(t, tt.hint)); got != tt.enc {
				t.Fatalf("the valid file reads %s, want %s", got, tt.enc)
			}
			if got := Detect(data, mustTag(t, tt.hint)); got == tt.enc {
				t.Errorf("Detect = %s on an invalid sequence", got)
			}
		})
	}

	// A UTF-32 BOM decides before the hint row, whatever follows it.
	for _, tt := range []struct {
		name string
		bom  []byte
	}{
		{"utf32le_bom_before_a_page", bomUTF32LE},
		{"utf32be_bom_before_a_page", bomUTF32BE},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := mustEncode(t, Windows1251, subtitle("Я не знаю, что случилось с моей сестрой.", "Где моё жёлтое пальто?"))
			if got := Detect(append(bytes.Clone(tt.bom), body...), mustTag(t, "ru")); got != Unknown {
				t.Errorf("Detect = %s, want Unknown", got)
			}
		})
	}

	// Windows writes CP932's IBM extensions at 0xFA40 to 0xFC4B, as the
	// standard's Shift_JIS does: ⅰ at FA 40 and 髙 at FB FC.
	t.Run("cp932_ibm_extensions", func(t *testing.T) {
		t.Parallel()
		text := strings.Repeat(subtitle("妹に何があったのか分からない。", "もちろん、駅まで一緒に行くよ。"), 4)
		base := mustEncode(t, ShiftJIS, text)
		data := append(bytes.Clone(base), 0xFA, 0x40, 0xFB, 0xFC, '\n')
		if got := Detect(data, mustTag(t, "ja")); got != ShiftJIS {
			t.Fatalf("Detect = %s, want ShiftJIS", got)
		}
		if out, err := ShiftJIS.Decode(data); err != nil || string(out) != text+"ⅰ髙\n" {
			t.Errorf("Decode = %q, %v", out, err)
		}
	})

	t.Run("hebrew_one_cue", func(t *testing.T) {
		t.Parallel()
		text := subtitle("שלום, אני כאן.")
		data := mustEncode(t, Windows1255, text)
		if got := Detect(data, mustTag(t, "he")); got != Windows1255 {
			t.Fatalf("Detect = %s, want Windows1255", got)
		}
	})

	t.Run("invalid_utf8_behind_a_bom", func(t *testing.T) {
		t.Parallel()
		data := append(append(bytes.Clone(bomUTF8), greek...), 0xFF)
		if got := Detect(data, mustTag(t, "el")); got == UTF8 {
			t.Errorf("Detect = UTF8 on invalid UTF-8")
		}
	})
}
