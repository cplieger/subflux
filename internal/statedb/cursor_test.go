package statedb

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

func rawCursor(t *testing.T, c ListCursor) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(string(c))
	if err != nil || len(b) == 0 {
		t.Fatalf("cursor %q: %v", c, err)
	}
	return b
}

func cursorOfBytes(b []byte) ListCursor {
	return ListCursor(base64.RawURLEncoding.EncodeToString(b))
}

func TestCursorInvalidCases(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	passOver(t, s, "sonarr",
		report{series("1", 100), []mediasource.Item{episode("11", 1, 1, "/tv/100/E01.mkv"), episode("12", 1, 2, "/tv/100/E02.mkv")}},
		report{series("2", 200), []mediasource.Item{episode("21", 1, 1, "/tv/200/E01.mkv")}},
		report{series("3", 0), []mediasource.Item{episode("31", 1, 1, "/tv/x/E01.mkv"), episode("32", 1, 2, "/tv/x/E02.mkv")}},
	)
	k1, k2, k3 := epKey(t, 100, 1, 1), epKey(t, 100, 1, 2), epKey(t, 200, 1, 1)
	recordAttempts(t, s, attemptOf(1, false, k1), attemptOf(1, false, k1), attemptOf(1, false, k3))
	r1, r2 := k1.Root(), k3.Root()

	roots, items, unidentified := cursorLists(t, s, r1)
	itemHist, rootHist, wholeLog := historyCursors(t, s, k1)

	rootsPage := func(c ListCursor) error { _, _, err := s.Roots(t.Context(), c, 1); return err }
	itemsPage := func(r media.RootKey) func(ListCursor) error {
		return func(c ListCursor) error { _, _, err := s.Items(t.Context(), r, c, 1); return err }
	}
	unidentifiedPage := func(c ListCursor) error { _, _, err := s.Unidentified(t.Context(), c, 1); return err }
	historyPage := func(h HistoryScope) func(ListCursor) error {
		return func(c ListCursor) error { _, _, err := s.History(t.Context(), h, c, 1); return err }
	}
	edit := func(c ListCursor, f func([]byte) []byte) ListCursor { return cursorOfBytes(f(rawCursor(t, c))) }

	cases := []struct {
		page   func(ListCursor) error
		name   string
		cursor ListCursor
	}{
		{rootsPage, "bad encoding", "!!"},
		{rootsPage, "padded encoding", roots + "=="},
		{rootsPage, "root key one byte short", edit(roots, func(b []byte) []byte { return b[:len(b)-1] })},
		{rootsPage, "kind alone", edit(roots, func(b []byte) []byte { return b[:1] })},
		{itemsPage(r1), "item key one byte long", edit(items, func(b []byte) []byte { return append(b, 0) })},
		{rootsPage, "unknown list kind", edit(roots, func(b []byte) []byte { b[0] = 9; return b })},
		{rootsPage, "a root key no key text spells", edit(roots, func(b []byte) []byte { return append(b[:1], make([]byte, 10)...) })},
		{unidentifiedPage, "a sighting key that does not decode", edit(unidentified, func(b []byte) []byte { return append(b[:1], 0xff) })},
		{itemsPage(r1), "roots cursor given to Items", roots},
		{rootsPage, "items cursor given to Roots", items},
		{historyPage(ItemHistory(k1)), "unidentified cursor given to History", unidentified},
		{itemsPage(r2), "items cursor of another root", items},
		{historyPage(ItemHistory(k2)), "item history cursor of another item", itemHist},
		{historyPage(ItemHistory(k1)), "root history cursor given to item history", rootHist},
		{historyPage(RootHistory(r1)), "whole-log cursor given to root history", wholeLog},
		{historyPage(HistoryScope{}), "item history cursor given to the whole log", itemHist},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.page(tc.cursor); !errors.Is(err, ErrCursorInvalid) {
				t.Errorf("err = %v, want ErrCursorInvalid", err)
			}
		})
	}
	t.Run("each cursor pages its own list", func(t *testing.T) {
		for _, err := range []error{
			rootsPage(roots), itemsPage(r1)(items), unidentifiedPage(unidentified),
			historyPage(ItemHistory(k1))(itemHist), historyPage(RootHistory(r1))(rootHist), historyPage(HistoryScope{})(wholeLog),
		} {
			if err != nil {
				t.Error(err)
			}
		}
	})
}

// cursorLists answers the first page's cursor of Roots, Items(r) and
// Unidentified at limit 1.
func cursorLists(t *testing.T, s *Store, r media.RootKey) (roots, items, unidentified ListCursor) {
	t.Helper()
	var err error
	if _, roots, err = s.Roots(t.Context(), "", 1); err != nil || roots == "" {
		t.Fatalf("Roots: %q, %v", roots, err)
	}
	if _, items, err = s.Items(t.Context(), r, "", 1); err != nil || items == "" {
		t.Fatalf("Items: %q, %v", items, err)
	}
	if _, unidentified, err = s.Unidentified(t.Context(), "", 1); err != nil || unidentified == "" {
		t.Fatalf("Unidentified: %q, %v", unidentified, err)
	}
	return roots, items, unidentified
}

func historyCursors(t *testing.T, s *Store, k media.Key) (item, root, whole ListCursor) {
	t.Helper()
	cursor := func(h HistoryScope) ListCursor {
		_, c := history(t, s, h, "", 1)
		if c == "" {
			t.Fatal("a one-row history page answered the last page")
		}
		return c
	}
	return cursor(ItemHistory(k)), cursor(RootHistory(k.Root())), cursor(HistoryScope{})
}

func TestCursorAfterDeletePositions(t *testing.T) {
	t.Run("Items", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("1", 100)
		var eps []mediasource.Item
		for e := 1; e <= 5; e++ {
			eps = append(eps, episode(fmt.Sprint(e), 1, e, fmt.Sprintf("/tv/S/E%02d.mkv", e)))
		}
		passOver(t, s, "sonarr", report{r, eps})
		root := epKey(t, 100, 1, 1).Root()
		page, c, err := s.Items(t.Context(), root, "", 2)
		if err != nil || len(page) != 2 {
			t.Fatalf("first page %v, %v", itemKeys(page), err)
		}
		passOver(t, s, "sonarr", report{r, slices.Delete(slices.Clone(eps), 1, 2)})
		page, _, err = s.Items(t.Context(), root, c, 2)
		if want := []media.Key{epKey(t, 100, 1, 3), epKey(t, 100, 1, 4)}; err != nil || !slices.Equal(itemKeys(page), want) {
			t.Errorf("after S01E02 retired: %v, %v; want %v", itemKeys(page), err, want)
		}
	})
	t.Run("Roots", func(t *testing.T) {
		s, _ := newLibrary(t, "radarr")
		var rs []report
		for _, id := range []string{"1", "2", "3"} {
			rs = append(rs, report{movie(id, uint64(id[0]-'0')), []mediasource.Item{film(id, "/m/"+id+".mkv")}})
		}
		passOver(t, s, "radarr", rs...)
		_, c, err := s.Roots(t.Context(), "", 1)
		if err != nil {
			t.Fatal(err)
		}
		passOver(t, s, "radarr", rs[1:]...)
		page, _, err := s.Roots(t.Context(), c, 1)
		if err != nil || len(page) != 1 || page[0].Key != movieKey(t, 2).Root() {
			t.Errorf("after tmdb-1 retired: %+v, %v; want tmdb-2", page, err)
		}
	})
	t.Run("Unidentified", func(t *testing.T) {
		s, _ := newLibrary(t, "sonarr")
		r := series("5", 0)
		eps := []mediasource.Item{episode("51", 1, 1, "/tv/x/E01.mkv"), episode("52", 1, 2, "/tv/x/E02.mkv"), episode("53", 1, 3, "/tv/x/E03.mkv")}
		passOver(t, s, "sonarr", report{r, eps})
		_, c, err := s.Unidentified(t.Context(), "", 1)
		if err != nil {
			t.Fatal(err)
		}
		passOver(t, s, "sonarr", report{r, eps[1:]})
		page, _, err := s.Unidentified(t.Context(), c, 1)
		if err != nil || len(page) != 1 || page[0].Item.Native != "52" {
			t.Errorf("after 51 retired: %+v, %v; want 52", page, err)
		}
	})
}
