package subtitleenc

import (
	"bytes"
	"cmp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/subtitleenc/icu"
	"golang.org/x/text/encoding/charmap"
)

const (
	// SiblingMargin is the score lead an ISO-8859 page needs over its Windows
	// sibling, and the lead the top survivor needs over one decoding the
	// sample otherwise. Czech ISO-8859-2 holding only "š" and "ž" leads its
	// 1250 reading by 3, and Polish 1250 "Mąż" ties its ISO reading.
	SiblingMargin = 3
	// SampleBytes is how much of a file the hint's pages read.
	SampleBytes = 64 << 10
)

// floorMargin is how far under its lowest true score a model's floor sits.
// Fitted on half the real set (split per hint and encoding) and scored on
// the other half, then swapped, 10 loses no held-out true file, with 10 and
// 17 wrong conversions against the fitted halves' 9 and 14; 5 loses a
// held-out Swedish file (69 against a floor of 71). Over the whole set, 192
// of 192 files read right and 31 of 5,568 mislabelled-hint trials wrong.
const floorMargin = 10

// multibyteFloor refuses ICU's 10, its answer for ten or fewer double-byte
// characters and no bad one; every true multibyte reading scores 100.
const multibyteFloor = 20

// ICU's names for the charsets most models are in.
const (
	latin1 = "ISO-8859-1"
	latin2 = "ISO-8859-2"
)

// models is each hint row's ICU model: the charset its trigrams are in, and
// the lowest score a true reading under its own hint reached over the
// committed corpus and the real set (192 subtitles transcoded from a media
// library's embedded tracks), with the files behind it. A model's floor is
// lowest less floorMargin.
var models = [...]struct {
	lang, charset string
	lowest        int
}{
	{"ar", "windows-1256", 26}, // 10 files
	{"cs", latin2, 31},         // 37 files
	{"da", latin1, 74},         // 5 files
	{"de", latin1, 73},         // 5 files
	{"el", "ISO-8859-7", 47},   // 12 files
	{"en", latin1, 59},         // 6 files
	{"es", latin1, 66},         // 5 files
	{"fr", latin1, 63},         // 7 files
	{"he", "ISO-8859-8-I", 28}, // 12 files
	{"hu", latin2, 49},         // 10 files
	{"it", latin1, 65},         // 5 files
	{"nl", latin1, 66},         // 5 files
	{"no", latin1, 72},         // 5 files
	{"pl", latin2, 50},         // 13 files
	{"pt", latin1, 66},         // 5 files
	{"ro", latin2, 52},         // 10 files
	{"ru", "windows-1251", 27}, // 55 files
	{"sv", latin1, 69},         // 5 files
	{"tr", "ISO-8859-9", 47},   // 6 files
}

// model scores a single-byte decoding: re-encoded into the model's charset,
// where a character the charset lacks is a non-letter.
type model struct {
	icu   icu.Model
	page  *charmap.Charmap
	floor int
}

func (m *model) score(text []byte) int {
	out := make([]byte, 0, len(text))
	for _, r := range string(text) {
		b, ok := m.page.EncodeRune(r)
		if !ok {
			b = '?'
		}
		out = append(out, b)
	}
	return m.icu.Confidence(out)
}

// charsets are the pages of the models' charsets. ISO-8859-8-I is ICU's
// logical-order Hebrew model, the order subtitles are stored in, over
// ISO-8859-8's bytes.
var charsets = map[string]*charmap.Charmap{
	latin1: charmap.ISO8859_1, latin2: charmap.ISO8859_2, "ISO-8859-7": charmap.ISO8859_7,
	"ISO-8859-8-I": charmap.ISO8859_8, "ISO-8859-9": charmap.ISO8859_9,
	"windows-1251": charmap.Windows1251, "windows-1256": charmap.Windows1256,
}

// multibyteCharsets are ICU's names for the multibyte pages.
var multibyteCharsets = map[Encoding]string{
	ShiftJIS: "Shift_JIS", EUCJP: "EUC-JP", GB18030: "GB18030", Big5: "Big5", EUCKR: "EUC-KR",
}

// row is a hint's pages in order and the model scoring them; a multibyte
// row has none, and ICU's recognizers score its pages' bytes.
type row struct {
	model   *model
	pages   []Encoding
	western bool
}

var (
	modelsByLang = buildModels()
	recognizers  = buildRecognizers()
	rows         = buildRows()
)

// buildModels panics on a models row ICU does not ship, which every test of
// the package then reports.
func buildModels() map[string]*model {
	out := make(map[string]*model, len(models))
	for i := range models {
		m := &models[i]
		im, ok := icu.LookupModel(m.charset, m.lang)
		page := charsets[m.charset]
		if !ok || page == nil {
			panic("subtitleenc: no ICU model " + m.charset + "/" + m.lang)
		}
		out[m.lang] = &model{icu: im, page: page, floor: m.lowest - floorMargin}
	}
	return out
}

func buildRecognizers() map[Encoding]icu.Multibyte {
	out := make(map[Encoding]icu.Multibyte, len(multibyteCharsets))
	for enc, name := range multibyteCharsets {
		r, ok := icu.LookupMultibyte(name)
		if !ok {
			panic("subtitleenc: no ICU recognizer " + name)
		}
		out[enc] = r
	}
	return out
}

func buildRows() map[string]row {
	out := map[string]row{}
	add := func(r row, langs ...string) {
		for _, l := range langs {
			out[l] = r
		}
	}
	for _, l := range []string{"en", "da", "de", "es", "fr", "it", "nl", "no", "pt", "sv"} {
		add(row{pages: []Encoding{Windows1252}, model: modelsByLang[l], western: true}, l)
	}
	out["nb"] = out["no"]
	latin2 := []Encoding{Windows1250, ISO8859_2}
	for _, l := range []string{"cs", "hu", "pl", "ro"} {
		add(row{pages: latin2, model: modelsByLang[l]}, l)
	}
	add(row{pages: latin2, model: modelsByLang["cs"]}, "sk", "hr", "sl")
	add(row{pages: []Encoding{Windows1254}, model: modelsByLang["tr"]}, "tr")
	ru := modelsByLang["ru"]
	add(row{pages: []Encoding{Windows1251, KOI8R, ISO8859_5}, model: ru}, "ru")
	add(row{pages: []Encoding{Windows1251, KOI8U, ISO8859_5}, model: ru}, "uk")
	add(row{pages: []Encoding{Windows1251, ISO8859_5}, model: ru}, "bg", "mk", "sr")
	add(row{pages: []Encoding{Windows1253, ISO8859_7}, model: modelsByLang["el"]}, "el")
	add(row{pages: []Encoding{Windows1255, ISO8859_8}, model: modelsByLang["he"]}, "he")
	add(row{pages: []Encoding{Windows1256, ISO8859_6}, model: modelsByLang["ar"]}, "ar")
	add(row{pages: []Encoding{ShiftJIS, EUCJP}}, "ja")
	add(row{pages: []Encoding{GB18030, Big5}}, "zh")
	add(row{pages: []Encoding{EUCKR}}, "ko")
	return out
}

// rowFor reads the hint's language subtag; Serbian in Latin script has no
// row, as Parse states the script of every Serbian tag but a bare "sr".
func rowFor(hint language.Tag) (row, bool) {
	lang, rest, _ := strings.Cut(hint.String(), "-")
	if script, _, _ := strings.Cut(rest, "-"); lang == "sr" && script == "Latn" {
		return row{}, false
	}
	r, ok := rows[lang]
	return r, ok
}

// Detect answers a BOM, UTF-16 by NUL interleave, 7-bit ISO-2022-JP, valid
// UTF-8, then the hint row's pages over the first SampleBytes, else Unknown.
// A form Decode refuses is Unknown; a page it refuses or holding a C1 is
// out. Of a Windows page and its ISO-8859 sibling, one reading a differing
// byte as a non-letter is out, as is a Windows page whose refused sibling
// reads one as a letter; of two left the ISO page needs SiblingMargin, where
// ICU names it unless a C1 byte shows. The top score at or above its
// model's floor wins, unless one within SiblingMargin decodes otherwise.
func Detect(data []byte, hint language.Tag) Encoding {
	if enc, ok := unicodeForm(data); ok {
		return enc
	}
	r, ok := rowFor(hint)
	if !ok {
		return Unknown
	}
	return r.detect(sampleOf(data))
}

// unicodeForm names what Detect reads before the hint, when the bytes
// announce it and read accepts them.
func unicodeForm(data []byte) (Encoding, bool) {
	enc, ok := announcedForm(data)
	if ok && enc != Unknown {
		if _, accepted := enc.read(enc.trimBOM(data)); !accepted {
			return Unknown, true
		}
	}
	return enc, ok
}

// announcedForm is a BOM, UTF-16 by the NUL pattern of ASCII-heavy text,
// ISO-2022-JP's escapes on 7-bit bytes, and valid UTF-8, ASCII included.
func announcedForm(data []byte) (Encoding, bool) {
	if enc, ok := bomForm(data); ok {
		return enc, true
	}
	if len(data) >= 4 {
		if data[0] == 0 && data[1] != 0 && data[2] == 0 && data[3] != 0 {
			return UTF16BE, true
		}
		if data[0] != 0 && data[1] == 0 && data[2] != 0 && data[3] == 0 {
			return UTF16LE, true
		}
	}
	sample := data[:min(len(data), SampleBytes)]
	if !slices.ContainsFunc(data, func(b byte) bool { return b >= 0x80 }) && icu.ISO2022JPConfidence(sample) > 0 {
		return ISO2022JP, true
	}
	if utf8.Valid(data) {
		return UTF8, true
	}
	return Unknown, false
}

// bomForm reads a UTF-32 BOM, whose first two bytes are UTF-16LE's, as
// Unknown: the Encoding set has no UTF-32.
func bomForm(data []byte) (Encoding, bool) {
	switch {
	case bytes.HasPrefix(data, bomUTF32LE), bytes.HasPrefix(data, bomUTF32BE):
		return Unknown, true
	case bytes.HasPrefix(data, bomUTF8):
		return UTF8, true
	case bytes.HasPrefix(data, bomUTF16LE):
		return UTF16LE, true
	case bytes.HasPrefix(data, bomUTF16BE):
		return UTF16BE, true
	default:
		return Unknown, false
	}
}

// sampleOf is data's first SampleBytes, cut back after its last line feed so
// no multibyte character is split: every multibyte page here encodes bytes
// below 0x30 as ASCII alone.
func sampleOf(data []byte) []byte {
	if len(data) <= SampleBytes {
		return data
	}
	s := data[:SampleBytes]
	if i := bytes.LastIndexByte(s, '\n'); i >= 0 {
		s = s[:i+1]
	}
	return s
}

// reading is one page's surviving decoding of the sample.
type reading struct {
	text  []byte
	score int
	enc   Encoding
}

func (r row) detect(sample []byte) Encoding {
	if r.western && !hasC1(sample) && slices.ContainsFunc(sample, iso8859_15Differs) {
		return Unknown
	}
	var rs []reading
	for _, enc := range r.pages {
		if text, ok := readPage(enc, sample); ok {
			rs = append(rs, reading{enc: enc, text: text})
		}
	}
	rs = settleSiblings(rs, r.pages, sample, r.model)
	for i := range rs {
		rs[i].score = r.score(rs[i], sample)
	}
	return pick(slices.DeleteFunc(rs, func(rd reading) bool { return rd.score < r.floor() }))
}

// pick is the score step over the readings at or above their floor, in the
// row's order: the top score, unless a reading within SiblingMargin of it
// decodes the sample otherwise.
func pick(rs []reading) Encoding {
	if len(rs) == 0 {
		return Unknown
	}
	slices.SortStableFunc(rs, func(a, b reading) int { return cmp.Compare(b.score, a.score) })
	for _, rd := range rs[1:] {
		if rs[0].score-rd.score < SiblingMargin && !bytes.Equal(rd.text, rs[0].text) {
			return Unknown
		}
	}
	return rs[0].enc
}

// score is the row's model on a single-byte reading, ICU's recognizer on a
// multibyte page's bytes.
func (r row) score(rd reading, sample []byte) int {
	if r.model == nil {
		return recognizers[rd.enc].Confidence(sample)
	}
	return r.model.score(rd.text)
}

func (r row) floor() int {
	if r.model == nil {
		return multibyteFloor
	}
	return r.model.floor
}

// readPage reads the sample in enc by Decode's rule, refusing a C1 control
// and KOI8-R's box characters where KOI8-U has і, ї, є and ґ.
func readPage(enc Encoding, sample []byte) ([]byte, bool) {
	if enc == KOI8R && slices.ContainsFunc(sample, koi8UOnly) {
		return nil, false
	}
	text, ok := enc.read(sample)
	if !ok || bytes.ContainsFunc(text, isC1) {
		return nil, false
	}
	return text, true
}

func koi8UOnly(b byte) bool {
	switch b {
	case 0xA4, 0xA6, 0xA7, 0xAD, 0xB4, 0xB6, 0xB7, 0xBD:
		return true
	default:
		return false
	}
}

// iso8859_15Differs is a byte where ISO-8859-15's letters sit and
// windows-1252 has common symbols ("´" as an apostrophe, "½").
func iso8859_15Differs(b byte) bool {
	switch b {
	case 0xA4, 0xA6, 0xA8, 0xB4, 0xB8, 0xBC, 0xBD, 0xBE:
		return true
	default:
		return false
	}
}

func hasC1(b []byte) bool {
	return slices.ContainsFunc(b, func(c byte) bool { return 0x80 <= c && c <= 0x9F })
}

func isC1(r rune) bool { return 0x80 <= r && r <= 0x9F }

// siblingPair is a Windows page and its ISO-8859 sibling; a page no hint row
// offers has no Encoding (Unknown).
type siblingPair struct {
	win, iso       *charmap.Charmap
	winEnc, isoEnc Encoding
}

var siblingPairs = [...]siblingPair{
	{charmap.Windows1250, charmap.ISO8859_2, Windows1250, ISO8859_2},
	{charmap.Windows1252, charmap.ISO8859_1, Windows1252, Unknown},
	{charmap.Windows1253, charmap.ISO8859_7, Windows1253, ISO8859_7},
	{charmap.Windows1254, charmap.ISO8859_9, Windows1254, Unknown},
	{charmap.Windows1255, charmap.ISO8859_8, Windows1255, ISO8859_8},
	{charmap.Windows1257, charmap.ISO8859_13, Unknown, Unknown},
}

// side is the sibling step's answer.
type side uint8

const (
	neither side = iota
	windowsPage
	isoPage
)

// settleSiblings drops the losing page of each sibling pair whose pages both
// survived and decode the sample differently, and a Windows page whose
// offered ISO sibling the refusals removed when it reads as a non-letter a
// byte the sibling reads as a letter: the sample then holds text of both.
func settleSiblings(rs []reading, pages []Encoding, sample []byte, m *model) []reading {
	for _, p := range siblingPairs {
		wi := slices.IndexFunc(rs, func(rd reading) bool { return rd.enc == p.winEnc })
		ii := slices.IndexFunc(rs, func(rd reading) bool { return rd.enc == p.isoEnc })
		if wi >= 0 && ii < 0 && p.isoEnc != Unknown && slices.Contains(pages, p.isoEnc) && !p.windowsHolds(sample) {
			rs = slices.Delete(rs, wi, wi+1)
			continue
		}
		if wi < 0 || ii < 0 || bytes.Equal(rs[wi].text, rs[ii].text) {
			continue
		}
		keep := p.choose(sample, m)
		rs = slices.DeleteFunc(rs, func(rd reading) bool {
			return (rd.enc == p.winEnc && keep != windowsPage) || (rd.enc == p.isoEnc && keep != isoPage)
		})
	}
	return rs
}

// choose is the sibling step over a sample both pages decode: a page reading
// a byte where the two differ as other than a letter or mark is out, and of
// two left the ISO page needs SiblingMargin on m; with no model, Windows.
// Decodings alike go to the Windows page.
func (p siblingPair) choose(sample []byte, m *model) side {
	winText, _ := p.win.NewDecoder().Bytes(sample)
	isoText, _ := p.iso.NewDecoder().Bytes(sample)
	if bytes.Equal(winText, isoText) {
		return windowsPage
	}
	winOK, isoOK := true, true
	p.eachDiffering(sample, func(wr, ir rune) {
		winOK = winOK && isLetter(wr)
		isoOK = isoOK && isLetter(ir)
	})
	switch {
	case winOK && isoOK:
		if m != nil && m.score(isoText) >= m.score(winText)+SiblingMargin {
			return isoPage
		}
		return windowsPage
	case winOK:
		return windowsPage
	case isoOK:
		return isoPage
	default:
		return neither
	}
}

// windowsHolds reports that the Windows page reads as a letter every sample
// byte its ISO sibling reads as a letter where the two differ.
func (p siblingPair) windowsHolds(sample []byte) bool {
	holds := true
	p.eachDiffering(sample, func(wr, ir rune) {
		holds = holds && (isLetter(wr) || !isLetter(ir))
	})
	return holds
}

// eachDiffering calls f once for each distinct sample byte the two pages
// decode differently.
func (p siblingPair) eachDiffering(sample []byte, f func(win, iso rune)) {
	var seen [256]bool
	for _, b := range sample {
		if seen[b] {
			continue
		}
		seen[b] = true
		if wr, ir := p.win.DecodeByte(b), p.iso.DecodeByte(b); wr != ir {
			f(wr, ir)
		}
	}
}

func isLetter(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) }
