package language

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// formatRecord is what FormatVersion pins over corpus: the sorted language
// subtags of corpus's tags for which multi holds, then each corpus entry with
// parse's text ("!" when refused, the entry alone when the text is itself).
func formatRecord(corpus []string, parse func(string) (Tag, bool), multi func(Tag) bool) []byte {
	entries := slices.Sorted(slices.Values(corpus))
	entries = slices.Compact(entries)
	var b bytes.Buffer
	b.WriteString("[multiscript]\n")
	seen := map[string]bool{}
	var subtags []string
	for _, x := range entries {
		t, ok := parse(x)
		if !ok || t == (Tag{}) {
			continue
		}
		if s := subtag(t.t); !seen[s] {
			seen[s] = true
			subtags = append(subtags, s)
		}
	}
	slices.Sort(subtags)
	for _, s := range subtags {
		if t, ok := parse(s); ok && multi(t) {
			b.WriteString(s + "\n")
		}
	}
	b.WriteString("[text]\n")
	for _, x := range entries {
		b.WriteString(x)
		switch t, ok := parse(x); {
		case !ok:
			b.WriteString("\t!")
		case t.String() != x:
			b.WriteString("\t" + t.String())
		}
		b.WriteByte('\n')
	}
	return b.Bytes()
}

var formatFoldExamples = []string{
	"ku-IQ", "sr-ME", "zh-MY", "kmr-IQ", "zsm-CC", "uzn-AF", "zh-CN", "zh-Hans-CN",
	"zh-TW", "zh-Hant-TW", "zh-HK", "cmn-Hant", "zh-SG", "cmn-MY", "sr",
}

// formatCorpus is built from langtag's public surface only: every two- and
// three-letter primary subtag Parse accepts, every region spelling Parse
// accepts joined to each that is MultiScript (so every region row is
// reached), every read exception key, every Names tag and the fold examples.
func formatCorpus() []string {
	var regions []string
	for a := byte('A'); a <= 'Z'; a++ {
		for b := byte('A'); b <= 'Z'; b++ {
			regions = append(regions, string([]byte{a, b}))
		}
	}
	for n := range 1000 {
		regions = append(regions, string([]byte{byte('0' + n/100), byte('0' + n/10%10), byte('0' + n%10)}))
	}
	var out []string
	for _, x := range primaryCandidates() {
		t, ok := Parse(x)
		if !ok {
			continue
		}
		out = append(out, x)
		if !t.t.MultiScript() {
			continue
		}
		for _, r := range regions {
			if _, ok := Parse(x + "-" + r); ok {
				out = append(out, x+"-"+r)
			}
		}
	}
	for k := range readExceptions {
		out = append(out, k)
	}
	for _, n := range Names() {
		out = append(out, n.Tag.String())
	}
	return append(out, formatFoldExamples...)
}

// primaryCandidates are every two- and three-letter lowercase string, so
// every primary subtag langtag accepts is among them.
func primaryCandidates() []string {
	out := make([]string, 0, 26*26+26*26*26)
	for a := byte('a'); a <= 'z'; a++ {
		for b := byte('a'); b <= 'z'; b++ {
			out = append(out, string([]byte{a, b}))
			for c := byte('a'); c <= 'z'; c++ {
				out = append(out, string([]byte{a, b, c}))
			}
		}
	}
	return out
}

func multiScript(t Tag) bool { return t.t.MultiScript() }

func recordHalves(t *testing.T, record []byte) (set, text string) {
	t.Helper()
	s := string(record)
	i := strings.Index(s, "[text]\n")
	if !strings.HasPrefix(s, "[multiscript]\n") || i < 0 {
		t.Fatalf("record lacks its two halves:\n%.200s", s)
	}
	return s[:i], s[i:]
}

func TestFormatComparatorCatchesRespelling(t *testing.T) {
	corpus := append(formatCorpus(), "pt-MO")
	set, text := recordHalves(t, formatRecord(corpus, Parse, multiScript))
	t.Run("pt gaining a pt_MO Hant row", func(t *testing.T) {
		ptMO := Tag{mustParse(t, "pt-Hant-MO").t}
		parse := func(raw string) (Tag, bool) {
			if raw == "pt-MO" {
				return ptMO, true
			}
			return Parse(raw)
		}
		multi := func(tag Tag) bool { return tag.t.Language() == "pt" || multiScript(tag) }
		gotSet, gotText := recordHalves(t, formatRecord(corpus, parse, multi))
		if gotSet == set {
			t.Error("the multiscript half did not change when pt became MultiScript")
		}
		if gotText == text {
			t.Error("the text half did not change when pt-MO read pt-Hant-MO")
		}
	})
	t.Run("az-IR respelled", func(t *testing.T) {
		parse := func(raw string) (Tag, bool) {
			if raw == "az-IR" {
				return mustParse(t, "az-Latn-IR"), true
			}
			return Parse(raw)
		}
		gotSet, gotText := recordHalves(t, formatRecord(corpus, parse, multiScript))
		if gotSet != set {
			t.Error("the multiscript half changed though no language's MultiScript did")
		}
		if gotText == text {
			t.Error("the text half did not change when az-IR's text did")
		}
	})
}

func formatGoldenPath(v uint16) string {
	return filepath.Join("testdata", fmt.Sprintf("format-v%d.golden", v))
}

const formatRegenerate = "bump FormatVersion, then run UPDATE_GOLDEN=1 go test -run TestFormatGolden ./internal/language/"

// formatModules lists the langtag and x/text versions this test binary links,
// written into a golden's header as information and never compared.
func formatModules() string {
	var b strings.Builder
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/cplieger/langtag/v3" || d.Path == "golang.org/x/text" {
				fmt.Fprintf(&b, "# %s %s\n", d.Path, d.Version)
			}
		}
	}
	return b.String()
}

// writeFormatGolden creates path once; a version's golden is never rewritten,
// since a changed record means stored tags changed spelling.
func writeFormatGolden(path string, record []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%s exists and is never rewritten: %s", path, formatRegenerate)
	}
	if err != nil {
		return err
	}
	_, err = f.Write(slices.Concat([]byte(formatModules()), record))
	return errors.Join(err, f.Close())
}

func TestFormatGoldenImmutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "format-v1.golden")
	if err := writeFormatGolden(path, []byte("first\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	err := writeFormatGolden(path, []byte("second\n"))
	if err == nil || !strings.Contains(err.Error(), "bump FormatVersion") {
		t.Errorf("rewriting an existing golden = %v; want a refusal telling the editor to bump FormatVersion", err)
	}
	if got, _ := os.ReadFile(path); !bytes.HasSuffix(got, []byte("first\n")) {
		t.Errorf("the refused write changed the golden to %q", got)
	}
}

func stripHeader(golden []byte) (header string, record []byte) {
	for bytes.HasPrefix(golden, []byte("#")) {
		line, rest, _ := bytes.Cut(golden, []byte("\n"))
		header += string(line) + "\n"
		golden = rest
	}
	return header, golden
}

func TestFormatGolden(t *testing.T) {
	record := formatRecord(formatCorpus(), Parse, multiScript)
	path := formatGoldenPath(FormatVersion)
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := writeFormatGolden(path, record); err != nil {
			t.Fatal(err)
		}
		return
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v: %s", err, formatRegenerate)
	}
	header, want := stripHeader(golden)
	if !bytes.Equal(record, want) {
		t.Errorf("tag spellings differ from %s, so stored tags would change meaning: %s\ngolden built with:\n%sthis build links:\n%s%s",
			path, formatRegenerate, header, formatModules(), firstDiff(want, record))
	}
}

func firstDiff(want, got []byte) string {
	w, g := strings.Split(string(want), "\n"), strings.Split(string(got), "\n")
	for i := range min(len(w), len(g)) {
		if w[i] != g[i] {
			return fmt.Sprintf("first difference at line %d: golden %q, now %q", i+1, w[i], g[i])
		}
	}
	return fmt.Sprintf("golden has %d lines, now %d", len(w), len(g))
}
