package mediasource_test

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestParseScheduleBounds(t *testing.T) {
	want := mediasource.Schedule{Change: 30 * time.Second, Whole: 24 * time.Hour}
	if got, errs := mediasource.ParseSchedule(nil); got != want || errs != nil {
		t.Errorf("absent section = %+v %v, want %+v", got, errs, want)
	}
	ok := map[string]mediasource.Schedule{
		"change_interval: 10s":                    {Change: 10 * time.Second, Whole: 24 * time.Hour},
		"change_interval: 1h\nwhole_interval: 1h": {Change: time.Hour, Whole: time.Hour},
		"whole_interval: 720h":                    {Change: 30 * time.Second, Whole: 720 * time.Hour},
	}
	for doc, want := range ok {
		if got, errs := mediasource.ParseSchedule(parseYAML(t, doc)); got != want || errs != nil {
			t.Errorf("%q = %+v %v, want %+v", doc, got, errs, want)
		}
	}
	refused := map[string]string{
		"change_interval: 9s":  "scan.change_interval",
		"change_interval: 61m": "scan.change_interval",
		"whole_interval: 59m":  "scan.whole_interval",
		"whole_interval: 721h": "scan.whole_interval",
		"cadence: 1h":          "scan.cadence",
	}
	for doc, path := range refused {
		_, errs := mediasource.ParseSchedule(parseYAML(t, doc))
		if len(errs) != 1 || errs[0].Path != path {
			t.Errorf("%q = %v, want one error at %s", doc, errs, path)
		}
	}
	n := parseYAML(t, "change_interval: 20s")
	if _, errs := mediasource.ParseSchedule(n); errs != nil || n.Content[1].Tag != formschema.PlainTag {
		t.Errorf("scalar tag %q after parsing, want %q", n.Content[1].Tag, formschema.PlainTag)
	}
}
