package mediawrite_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

func sectionNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestSectionDefaults(t *testing.T) {
	t.Parallel()
	want := mediawrite.Config{Clean: timing.CleanOptions{StripTags: true, NormalizeUTF8: true, CleanWhitespace: true, NormalizeLineEndings: true, RemoveEmpty: true}}
	if got, errs := mediawrite.ParseConfig(nil); got != want || errs != nil {
		t.Errorf("absent section: %+v %v, want %+v", got, errs, want)
	}
	if got, errs := mediawrite.ParseConfig(sectionNode(t, "{}")); got != want || errs != nil {
		t.Errorf("empty section: %+v %v, want %+v", got, errs, want)
	}
	flipped := mediawrite.Config{Clean: timing.CleanOptions{StripHI: true}}
	text := "strip_hi: true\nstrip_tags: false\nnormalize_utf8: false\nnormalize_line_endings: false\nclean_whitespace: false\nremove_empty: false\n"
	if got, errs := mediawrite.ParseConfig(sectionNode(t, text)); got != flipped || errs != nil {
		t.Errorf("every key flipped: %+v %v, want %+v", got, errs, flipped)
	}
}

// Each CleanOptions field is set by exactly one post_processing key, and
// each key sets exactly one field.
func TestConfigCarriesEveryCleanOption(t *testing.T) {
	t.Parallel()
	base, errs := mediawrite.ParseConfig(nil)
	if errs != nil {
		t.Fatal(errs)
	}
	opts := reflect.TypeFor[timing.CleanOptions]()
	reached := map[string][]string{}
	for _, f := range mediawrite.Schema.Fields {
		flip := map[string]string{"true": "false", "false": "true"}[f.Default]
		got, errs := mediawrite.ParseConfig(sectionNode(t, f.Key+": "+flip+"\n"))
		if errs != nil {
			t.Fatalf("%s: %v", f.Key, errs)
		}
		var changed []string
		for field := range opts.Fields() {
			if reflect.ValueOf(got.Clean).FieldByIndex(field.Index).Bool() != reflect.ValueOf(base.Clean).FieldByIndex(field.Index).Bool() {
				changed = append(changed, field.Name)
			}
		}
		if len(changed) != 1 {
			t.Errorf("%s flips %q, want exactly one CleanOptions field", f.Key, changed)
		}
		for _, name := range changed {
			reached[name] = append(reached[name], f.Key)
		}
	}
	for field := range opts.Fields() {
		if len(reached[field.Name]) != 1 {
			t.Errorf("CleanOptions.%s is set by %q, want exactly one post_processing key", field.Name, reached[field.Name])
		}
	}
}

func TestSectionRefusesUnknownKey(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]formschema.FieldError{
		"normalize_endings: true\n": {Path: "post_processing.normalize_endings", Message: "unknown key"},
		"strip_hi: sometimes\n":     {Path: "post_processing.strip_hi"},
	} {
		got, errs := mediawrite.ParseConfig(sectionNode(t, text))
		if got != (mediawrite.Config{}) || len(errs) != 1 || errs[0].Path != want.Path || want.Message != "" && errs[0].Message != want.Message {
			t.Errorf("%q: %+v %v, want the zero Config and %+v", text, got, errs, want)
		}
	}
	var keys []string
	for _, f := range mediawrite.Schema.Fields {
		keys = append(keys, f.Key)
	}
	want := []string{"strip_hi", "strip_tags", "normalize_utf8", "normalize_line_endings", "clean_whitespace", "remove_empty"}
	if !slices.Equal(keys, want) {
		t.Errorf("schema keys %q, want %q", keys, want)
	}
}

func TestSaveAppliesToNextDelivery(t *testing.T) {
	t.Parallel()
	l := newLibrary(t, enOnly, mediawrite.Deps{}, nil)
	first := delivery(t, l.inst, "[music] hello")
	rec, err := l.w.Deliver(t.Context(), first)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, l.path(rec.Name)); !strings.Contains(got, "[music]") {
		t.Fatalf("the first delivery reads %q; strip_hi was off", got)
	}
	l.w.SetConfig(mediawrite.Config{Clean: timing.CleanOptions{StripHI: true}})
	second := delivery(t, l.inst, "[music] hello")
	second.Numbered = true
	again, err := l.w.Deliver(t.Context(), second)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, l.path(again.Name)); strings.Contains(got, "[music]") || !strings.Contains(got, "hello") {
		t.Errorf("the delivery after the save reads %q, want the note stripped", got)
	}
	if got := readFile(t, l.path(rec.Name)); !strings.Contains(got, "[music]") {
		t.Errorf("the save rewrote the earlier delivery: %q", got)
	}
}

func TestNormalizeFalseThroughDeliverAndRetime(t *testing.T) {
	t.Parallel()
	cfg, errs := mediawrite.ParseConfig(sectionNode(t, "normalize_utf8: false\n"))
	if errs != nil {
		t.Fatal(errs)
	}
	l := newLibrary(t, "default:\n  - tag: fr\n", mediawrite.Deps{}, nil)
	l.w.SetConfig(cfg)
	// windows-1252 French; its \x92 quote is what tells it from ISO-8859-1.
	doc := "1\r\n00:00:01,000 --> 00:00:03,000\r\nO\xf9 \xeates-vous all\xe9s hier soir ?\r\n\r\n" +
		"2\r\n00:00:04,000 --> 00:00:06,000\r\n\xc0 la f\xeate, c\x92\xe9tait d\xe9j\xe0 fini.\r\n"
	cues, err := timing.ParseCues([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	d := delivery(t, l.inst, "")
	d.Cues, d.Tag, d.Target = cues, mustTag("fr"), mustTag("fr")
	rec, err := l.w.Deliver(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, l.path(rec.Name)); got != doc {
		t.Fatalf("delivered %q, want the windows-1252 bytes kept", got)
	}
	retimed, err := l.w.Retime(t.Context(), mediawrite.RefOf(rec), expectOf(rec), timing.Shift{Ms: 1000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("00:00:01,000 --> 00:00:03,000", "00:00:02,000 --> 00:00:04,000", "00:00:04,000 --> 00:00:06,000", "00:00:05,000 --> 00:00:07,000").Replace(doc)
	if got := readFile(t, l.path(retimed.Name)); got != want {
		t.Errorf("retimed %q, want only the stamps moved: %q", got, want)
	}
}

func TestRootsChangedProbesNewRoots(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, filepath.Join(a, "keep"), "x")
	writeFile(t, filepath.Join(b, "keep"), "x")
	store := openStore(t)
	synctest.Test(t, func(t *testing.T) {
		roots := rootsOf(a)
		w := newWriter(t, mediawrite.Deps{Store: store, Roots: roots}, nil)
		run(t, w)
		roots.set(a, b)
		w.SetConfig(mediawrite.Config{})
		synctest.Wait()
		before := roots.reads()
		if mediawrite.Available(w, b) {
			t.Error("SetConfig probed the roots; a section save leaves them to RootsChanged")
		}
		w.RootsChanged()
		synctest.Wait()
		if !mediawrite.Available(w, b) {
			t.Error("a root added before RootsChanged is not probed until the next interval")
		}
		if n := roots.reads() - before; n != 1 {
			t.Errorf("RootsChanged read the roots %d times, want one probe pass", n)
		}
	})
}
