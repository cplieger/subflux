package skyhook

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestShowRecordedAnswers decodes SkyHook's answers as recorded on
// 2026-11-02, overviews and images stripped; each expected value is read off
// the recording.
func TestShowRecordedAnswers(t *testing.T) {
	firefly := []Order{{"official", "Aired Order"}, {"dvd", "DVD Order"}}
	thrones := firefly
	futurama := []Order{{"official", "Aired Order"}, {"dvd", "DVD Volumes"}, {"alternate", "Streaming Order"}, {"regional", "Production Order"}}
	monogatari := []Order{{"official", "Aired Order"}, {"dvd", "DVD Order"}, {"alternate", "Book Order"}}
	for _, tc := range []struct {
		lang, order string
		orders      []Order
		first       Episode
		id          int
		episodes    int
		untitled    int
		undated     int
	}{
		{"eng", "official", firefly, Episode{AirDate: "2005-09-03", Title: "Serenity", ID: 415679, Number: 1, Absolute: 15}, 78874, 20, 0, 0},
		{"eng", "dvd", firefly, Episode{AirDate: "2005-09-03", Title: "Serenity", ID: 415679, Number: 1}, 78874, 17, 0, 0},
		{"fra", "official", firefly, Episode{AirDate: "2005-09-03", Title: "Serenity", ID: 415679, Number: 1, Absolute: 15}, 78874, 20, 5, 0},
		{"fra", "dvd", firefly, Episode{AirDate: "2005-09-03", Title: "Serenity", ID: 415679, Number: 1}, 78874, 17, 2, 0},
		{"eng", "official", thrones, Episode{AirDate: "2010-12-05", Title: "Inside Game of Thrones", ID: 3226241, Number: 1}, 121361, 128, 0, 0},
		{"eng", "dvd", thrones, Episode{AirDate: "2011-04-17", Title: "Winter Is Coming", ID: 3254641, Season: 1, Number: 1}, 121361, 73, 0, 0},
		{"eng", "official", futurama, Episode{AirDate: "2007-11-27", Title: "Everybody Loves Hypnotoad", ID: 389457, Number: 1}, 73871, 170, 0, 0},
		{"eng", "dvd", futurama, Episode{AirDate: "2007-11-27", Title: "Everybody Loves Hypnotoad", ID: 389457, Number: 1}, 73871, 170, 0, 0},
		{"eng", "official", monogatari, Episode{AirDate: "2009-08-07", Title: "Episode 5.5 (Recap)", ID: 3406651, Number: 1}, 102261, 122, 0, 1},
		{"eng", "alternate", monogatari, Episode{AirDate: "2009-07-03", Title: "Hitagi Crab - Part 1", ID: 830421, Season: 1, Number: 1}, 102261, 125, 0, 1},
	} {
		name := strconv.Itoa(tc.id) + "-" + tc.lang + "-" + tc.order
		t.Run(name, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{ok(recorded(t, name+".json"))}}
			s, found, err := fakeClient(f).Show(t.Context(), tc.id, tc.lang, tc.order)
			if !found || err != nil {
				t.Fatalf("Show = %v, %v", found, err)
			}
			if len(s.Episodes) != tc.episodes {
				t.Fatalf("episodes = %d, want %d", len(s.Episodes), tc.episodes)
			}
			if !slices.Equal(s.Orders, tc.orders) {
				t.Errorf("orders = %v, want %v", s.Orders, tc.orders)
			}
			if s.Episodes[0] != tc.first {
				t.Errorf("first episode = %+v, want %+v", s.Episodes[0], tc.first)
			}
			untitled, undated := 0, 0
			for _, e := range s.Episodes {
				if e.Title == "" {
					untitled++
				}
				if e.AirDate == "" {
					undated++
				}
			}
			if untitled != tc.untitled || undated != tc.undated {
				t.Errorf("untitled %d, undated %d; want %d, %d", untitled, undated, tc.untitled, tc.undated)
			}
		})
	}
}

var secretShaped = regexp.MustCompile(`(?i)x-api-key|x-plex-token|apikey=`)

func TestRecordedAnswersCarryNoSecret(t *testing.T) {
	files, err := filepath.Glob("testdata/*.json")
	if err != nil || len(files) == 0 {
		t.Fatalf("testdata = %v, %v", files, err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if m := secretShaped.Find(b); m != nil {
			t.Errorf("%s carries %q", name, m)
		}
		if strings.Contains(string(b), `"overview"`) || strings.Contains(string(b), `"image"`) {
			t.Errorf("%s keeps an overview or an image", name)
		}
	}
}
