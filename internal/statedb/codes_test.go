package statedb

import (
	"errors"
	"testing"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/go/httpwire/httpwiretest"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestCodesCoverFailureTable(t *testing.T) {
	s, _ := newLibrary(t, "sonarr")
	passOver(t, s, "sonarr",
		report{series("1", 100), []mediasource.Item{episode("11", 1, 1, "/tv/100/E01.mkv"), episode("12", 1, 2, "/tv/100/E02.mkv")}},
		report{series("2", 200), []mediasource.Item{episode("21", 1, 1, "/tv/200/E01.mkv")}},
		report{series("3", 0), []mediasource.Item{episode("31", 1, 1, "/tv/x/E01.mkv"), episode("32", 1, 2, "/tv/x/E02.mkv")}},
	)
	k := epKey(t, 100, 1, 1)
	recordAttempts(t, s, attemptOf(1, false, k), attemptOf(1, false, k))
	roots, items, unidentified := cursorLists(t, s, k.Root())

	arms := []struct {
		call func() error
		name string
		want string
	}{
		{func() error { _, _, err := s.Roots(t.Context(), items, 1); return err }, "Roots given an Items cursor", "cursor_invalid"},
		{func() error { _, _, err := s.Items(t.Context(), k.Root(), roots, 1); return err }, "Items given a Roots cursor", "cursor_invalid"},
		{func() error { _, _, err := s.History(t.Context(), ItemHistory(k), unidentified, 1); return err }, "History given an Unidentified cursor", "cursor_invalid"},
		{func() error { _, _, err := s.Unidentified(t.Context(), roots, 1); return err }, "Unidentified given a Roots cursor", "cursor_invalid"},
		{func() error {
			return s.SetLock(t.Context(), 9999, mustTag(t, "en"), language.Standard, true)
		}, "SetLock with nothing to lock", "not_lockable"},
	}
	for _, arm := range arms {
		t.Run(arm.name, func(t *testing.T) {
			if got := codeOf(arm.call(), Owner); got != arm.want {
				t.Errorf("code %q, want %q", got, arm.want)
			}
		})
	}
}

func TestOwnerSentinels(t *testing.T) {
	httpwiretest.Sentinels(t, Owner)
}

// codeOf is the code of the first entry of owners' Codes that err matches by
// errors.Is, "" when none does: the lookup httpwire's answer mapping makes.
func codeOf(err error, owners ...*httpwire.Owner) string {
	for _, o := range owners {
		for _, c := range o.Codes {
			if errors.Is(err, c.Err) {
				return c.Code
			}
		}
	}
	return ""
}
