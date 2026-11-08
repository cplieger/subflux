package statedb

import (
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

// doubleEpisode is a store whose commits faulty fails on demand, holding
// S01E01 and S01E02 in one file at epPath under policy, and their instance.
func doubleEpisode(t *testing.T, policy language.Policy) (*Store, InstanceID, media.Key, media.Key) {
	t.Helper()
	s := faultyStore(t, errInjected)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr"}, Languages: policy})
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show, episode("e1", 1, 1, epPath), episode("e2", 1, 2, epPath))
	k1, k2 := epKey(t, showTVDB, 1, 1), epKey(t, showTVDB, 1, 2)
	rec, _, _ := holderOf(t, s, k1)
	return s, InstanceID(rec.Inst), k1, k2
}

func TestSetLockInstanceAllOrNone(t *testing.T) {
	s, id, k1, k2 := doubleEpisode(t, policyOf(t, "en"))
	putExternal(t, s, id, local(t, "S01E01-E02.en.srt", "en"))
	en := mustTag(t, "en")
	both := func(t *testing.T, want bool) {
		t.Helper()
		if locked(t, s, k1, "en") != want || locked(t, s, k2, "en") != want {
			t.Errorf("locks on the two items = %v, %v; want both %v", locked(t, s, k1, "en"), locked(t, s, k2, "en"), want)
		}
	}

	if err := s.SetLock(markFailing(t.Context()), id, en, language.Standard, true); !errors.Is(err, errInjected) {
		t.Fatalf("SetLock(true) under a commit fault = %v, want the fault", err)
	}
	both(t, false)
	head := headRev(t, s)
	setLock(t, s, id, "en", true)
	if got := headRev(t, s); got != head+1 {
		t.Errorf("SetLock(true) took %d commits, want 1", got-head)
	}
	both(t, true)
	if err := s.SetLock(markFailing(t.Context()), id, en, language.Standard, false); !errors.Is(err, errInjected) {
		t.Fatalf("SetLock(false) under a commit fault = %v, want the fault", err)
	}
	both(t, true)
	setLock(t, s, id, "en", false)
	both(t, false)
	head = headRev(t, s)
	setLock(t, s, id, "en", false)
	if got := headRev(t, s); got != head {
		t.Errorf("clearing an unlocked pair moved the head from %d to %d", head, got)
	}

	for name, call := range map[string]func() error{
		"unknown instance":   func() error { return s.SetLock(t.Context(), id+100, en, language.Standard, true) },
		"unconfigured pair":  func() error { return s.SetLock(t.Context(), id, mustTag(t, "fr"), language.Standard, true) },
		"a pair none serves": func() error { return s.SetLock(t.Context(), id, en, language.Forced, true) },
	} {
		if err := call(); !errors.Is(err, ErrNotLockable) {
			t.Errorf("SetLock on %s = %v, want ErrNotLockable", name, err)
		}
	}
}

func refusalOf(t *testing.T, s *Store, k media.Key, tag string) string {
	t.Helper()
	rows, err := s.Coverage(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.Target == mustTag(t, tag) && r.Variant == language.Standard {
			return r.LastRefusal
		}
	}
	t.Fatalf("%s configures no %s", k, tag)
	return ""
}

func noteRefusal(t *testing.T, s *Store, id InstanceID, tag, refusal string) {
	t.Helper()
	if err := s.NoteRefusal(t.Context(), id, mustTag(t, tag), language.Standard, refusal); err != nil {
		t.Fatalf("NoteRefusal(%s, %q): %v", tag, refusal, err)
	}
}

func TestNoteRefusal(t *testing.T) {
	s, id, k1, k2 := doubleEpisode(t, pol(t, "default:\n  - tag: en\n  - tag: fr\n"))
	putExternal(t, s, id, local(t, "S01E01-E02.fr.srt", "fr"))
	head := headRev(t, s)
	noteRefusal(t, s, id, "en", "different_cut")
	if got := headRev(t, s); got != head+1 {
		t.Errorf("NoteRefusal took %d commits, want 1", got-head)
	}
	for _, k := range []media.Key{k1, k2} {
		if got := refusalOf(t, s, k, "en"); got != "different_cut" {
			t.Errorf("%s's en refusal = %q, want different_cut", k, got)
		}
	}
	head = headRev(t, s)
	noteRefusal(t, s, id, "en", "different_cut")
	if got := headRev(t, s); got != head {
		t.Errorf("a byte-equal NoteRefusal moved the head from %d to %d", head, got)
	}
	noteRefusal(t, s, id, "fr", "fetch_gone")
	if got := refusalOf(t, s, k1, "fr"); got != "" {
		t.Errorf("NoteRefusal stored %q on fr, which is Exact", got)
	}

	t.Run("a recompute still Missing keeps it", func(t *testing.T) {
		putEmbedded(t, s, id)
		if got := refusalOf(t, s, k1, "en"); got != "different_cut" {
			t.Errorf("refusal after a recompute leaving en Missing = %q", got)
		}
	})
	t.Run("a failed recompute leaving Exact changes neither", func(t *testing.T) {
		files := []LocalFile{local(t, "S01E01-E02.fr.srt", "fr"), local(t, "S01E01-E02.en.srt", "en")}
		if err := s.PutExternal(markFailing(t.Context()), id, files); !errors.Is(err, errInjected) {
			t.Fatalf("PutExternal under a commit fault = %v", err)
		}
		if c, _ := coverageOf(t, s, k1, "en", language.Standard); c != Missing || refusalOf(t, s, k1, "en") != "different_cut" {
			t.Errorf("a failed commit left en %v with refusal %q", c, refusalOf(t, s, k1, "en"))
		}
	})
	t.Run("a recompute leaving Exact clears it in its write", func(t *testing.T) {
		putExternal(t, s, id, local(t, "S01E01-E02.fr.srt", "fr"), local(t, "S01E01-E02.en.srt", "en"))
		for _, k := range []media.Key{k1, k2} {
			if got := refusalOf(t, s, k, "en"); got != "" {
				t.Errorf("%s keeps refusal %q on an Exact pair", k, got)
			}
		}
	})
	t.Run("refusals", func(t *testing.T) {
		if err := s.NoteRefusal(t.Context(), id+100, mustTag(t, "en"), language.Standard, "fetch_gone"); err == nil {
			t.Error("NoteRefusal on an unknown instance succeeded")
		}
		if err := s.NoteRefusal(t.Context(), id, mustTag(t, "en"), language.Standard, ""); err == nil {
			t.Error("NoteRefusal of an empty refusal succeeded")
		}
	})
}

func TestNoteRefusalClearedByDelivery(t *testing.T) {
	s, id, k1, k2 := doubleEpisode(t, policyOf(t, "en"))
	putExternal(t, s, id)
	noteRefusal(t, s, id, "en", "different_cut")
	d := pendingDeliver(t, id, "S01E01-E02.fr.srt", 1, k1)
	d.Record.Tag = mustTag(t, "fr")
	beginDeliver(t, s, d)
	endDeliver(t, s, id, d.Record.Name, false)
	if got := refusalOf(t, s, k1, "en"); got != "different_cut" {
		t.Errorf("a delivery that did not land cleared the refusal to %q", got)
	}
	beginDeliver(t, s, d)
	endDeliver(t, s, id, d.Record.Name, true)
	for _, k := range []media.Key{k1, k2} {
		if c, _ := coverageOf(t, s, k, "en", language.Standard); c != Missing {
			t.Fatalf("%s's en is %v after a French file landed, want Missing", k, c)
		}
		if got := refusalOf(t, s, k, "en"); got != "" {
			t.Errorf("%s keeps refusal %q after the pair's delivery landed", k, got)
		}
	}
}
