package episodematch

import (
	"strings"
	"testing"
)

// FuzzAssign_bounded: any names and titles assign without a panic, never
// two title units to one episode, every member of a unit its unit's
// assignment, and a pack counts the same once every member is doubled,
// since versions of one episode count once.
func FuzzAssign_bounded(f *testing.F) {
	f.Add("S01E01.Alpha.en.srt\nS01E01.Alpha.sdh.en.srt\nS01E02.Beta.srt\nS01E03.forced.en.srt", "Alpha\nBeta\nGamma")
	f.Add("S05E07 - Old Soldiers.mkv\nS05E08 - Old Soldiers.mkv\nS05E09 - Beyond Good and Evil 1.mkv", "Old Soldiers\nBeyond Good and Evil (1)\nBeyond Good and Evil (2)")
	f.Add("01 - Winter Is Coming.srt\n02 - The Kingsroad.srt\nExtras.srt", "Winter Is Coming\nThe Kingsroad")
	f.Add("Show.S01E01-E03.Part.1.srt\n1x00 - Special.srt\nShow - 42.srt", "Part 1\n42\nSpecial")
	f.Fuzz(func(t *testing.T, names, titles string) {
		ns := bounded(names)
		ts := bounded(titles)
		if len(ns) == 0 || len(ts) == 0 {
			return
		}
		read := ReadPack(ns, 1)
		eps := make([][]string, len(ts))
		for j, title := range ts {
			eps[j] = []string{title}
		}
		got := assignTitles(read, eps)
		unitOf, _ := titleUnits(read)
		unitEp := map[int]int{}
		epUnit := map[int]int{}
		for i, e := range got {
			if e != unassigned && (e < 0 || e >= len(ts) || unitOf[i] < 0) {
				t.Fatalf("member %d assigned %d, unit %d", i, e, unitOf[i])
			}
			if u := unitOf[i]; u >= 0 {
				if prev, ok := unitEp[u]; ok && prev != e {
					t.Fatalf("unit %d's members assigned %d and %d", u, prev, e)
				}
				unitEp[u] = e
			}
			if e != unassigned {
				if u, ok := epUnit[e]; ok && u != unitOf[i] {
					t.Fatalf("units %d and %d both assigned episode %d", u, unitOf[i], e)
				}
				epUnit[e] = unitOf[i]
			}
		}
		s := greek(min(len(ts), 20))
		for j := range s.Items {
			s.Items[j].Title = ts[j]
		}
		ser := newSeries(&s, 0)
		doubled := append(append([]string(nil), ns...), ns...)
		readTwice := ReadPack(doubled, 1)
		once, twice := ser.count(0, read, ser.assign(read), 1), ser.count(0, readTwice, ser.assign(readTwice), 1)
		if once != twice || once > len(ns)*maxClaims(read) {
			t.Fatalf("count %d once, %d doubled, over %d names", once, twice, len(ns))
		}
	})
}

// bounded splits fuzz text into at most 24 lines of at most 120 bytes, the
// size of a real pack's names.
func bounded(s string) []string {
	lines := strings.Split(s, "\n")
	if len(lines) > 24 {
		lines = lines[:24]
	}
	for i, l := range lines {
		if len(l) > 120 {
			lines[i] = l[:120]
		}
	}
	return lines
}

func maxClaims(names []Name) int {
	n := 1
	for _, x := range names {
		n = max(n, len(x.Claims))
	}
	return n
}
