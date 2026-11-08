package statedb

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
)

func inspect(t *testing.T, s *Store, o InspectOptions) Inspection {
	t.Helper()
	in, err := s.Inspect(t.Context(), o)
	if err != nil {
		t.Fatalf("Inspect(%+v): %v", o, err)
	}
	return in
}

// mismatchNamed is in's Mismatch named name, the zero one when none.
func mismatchNamed(in *Inspection, name string) Mismatch {
	for _, m := range in.Mismatches {
		if m.Name == name {
			return m
		}
	}
	return Mismatch{}
}

func TestInspectVerifySkipsOwnerless(t *testing.T) {
	p := pol(t, audioRules)
	s := covLib(t, tempPath(t), p)
	activate(t, s, Activation{Sources: []media.SourceID{"sonarr", "radarr"}, Languages: p})
	k, id := showEpisode(t, s, "sonarr", epPath, "ja")
	putExternal(t, s, id, local(t, "S01E01.en.srt", "en"))
	setLock(t, s, id, "en", true)
	activate(t, s, Activation{Sources: []media.SourceID{"radarr"}, Languages: p})
	en := lockKey{Key: k, Tag: mustTag(t, "en"), Variant: language.Standard}
	cov, haveCov := getRow(t, s, s.t.coverage, k)
	lock, haveLock := getRow(t, s, s.t.locks, en)
	if !haveCov || !haveLock {
		t.Fatalf("the ownerless item holds coverage %v and lock %v, want both", haveCov, haveLock)
	}
	head := headRev(t, s)

	for _, o := range []InspectOptions{{Verify: true}, {Repair: true}} {
		if in := inspect(t, s, o); len(in.Mismatches) != 0 || !in.Verified {
			t.Errorf("Inspect(%+v) = verified %v, mismatches %+v; want verified, none", o, in.Verified, in.Mismatches)
		}
	}
	if got, _ := getRow(t, s, s.t.coverage, k); !reflect.DeepEqual(got, cov) {
		t.Errorf("Repair rewrote the ownerless coverage row: %+v, want %+v", got, cov)
	}
	if got, found := getRow(t, s, s.t.locks, en); !found || got != lock {
		t.Errorf("Repair changed the ownerless lock: %+v (found %v), want %+v", got, found, lock)
	}
	if got := headRev(t, s); got != head {
		t.Errorf("Inspect moved the head from %d to %d", head, got)
	}
}

func TestInspectRepairsCoverage(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	observe(t, s, "sonarr", wholePass(t, s, "sonarr"), show,
		episode("e1", 1, 1, "/tv/Show/S01E01.mkv"), episode("e2", 1, 2, "/tv/Show/S01E02.mkv"))
	changed, dropped, stray := epKey(t, showTVDB, 1, 1), epKey(t, showTVDB, 1, 2), epKey(t, showTVDB, 1, 3)
	want, _ := getRow(t, s, s.t.coverage, changed)
	enLock := lockKey{Key: changed, Tag: mustTag(t, "en"), Variant: language.Standard}
	wrong := coverageRow{Pairs: []coveragePair{{Tag: rowTag{t: mustTag(t, "en")}, Variant: uint8(language.Standard), Coverage: uint8(Exact)}}}
	update(t, s, func(tx *bolttable.Tx) error {
		if err := s.t.locks.Put(tx, enLock, &lockRow{At: time.Unix(1, 0).UTC()}); err != nil {
			return err
		}
		if err := s.t.coverage.Put(tx, changed, &wrong); err != nil {
			return err
		}
		if _, err := s.t.coverage.Delete(tx, dropped); err != nil {
			return err
		}
		return s.t.coverage.Put(tx, stray, &wrong)
	})

	in := inspect(t, s, InspectOptions{Verify: true})
	if got, want := mismatchNamed(&in, coverageName), (Mismatch{Name: coverageName, Missing: 2, Extra: 2}); got != want {
		t.Errorf("Verify reported %+v, want %+v", got, want)
	}
	if got, _ := getRow(t, s, s.t.coverage, changed); !reflect.DeepEqual(got, wrong) {
		t.Error("Verify without Repair rewrote a coverage row")
	}

	in = inspect(t, s, InspectOptions{Repair: true})
	if got, want := mismatchNamed(&in, coverageName), (Mismatch{Name: coverageName, Missing: 2, Extra: 2, Repaired: true}); got != want {
		t.Errorf("Repair reported %+v, want %+v", got, want)
	}
	if got, _ := getRow(t, s, s.t.coverage, changed); !reflect.DeepEqual(got, want) {
		t.Errorf("Repair left %s at %+v, want %+v", changed, got, want)
	}
	if _, found := getRow(t, s, s.t.coverage, dropped); !found {
		t.Errorf("Repair did not restore %s's row", dropped)
	}
	if _, found := getRow(t, s, s.t.coverage, stray); found {
		t.Errorf("Repair kept the row of %s, which has no item record", stray)
	}
	if _, found := getRow(t, s, s.t.locks, enLock); !found {
		t.Error("Repair deleted a lock")
	}
	if in := inspect(t, s, InspectOptions{Verify: true}); len(in.Mismatches) != 0 {
		t.Errorf("Verify after Repair = %+v, want none", in.Mismatches)
	}
}

func TestInspectBeforeActivateRefusesVerify(t *testing.T) {
	s := newStore(t)
	if _, err := s.Inspect(t.Context(), InspectOptions{Verify: true}); !errors.Is(err, errNoActivation) {
		t.Errorf("Inspect(Verify) before Activate = %v, want errNoActivation", err)
	}
	if in := inspect(t, s, InspectOptions{}); in.Verified || in.Epoch != s.db.Epoch() {
		t.Errorf("Inspect() before Activate = verified %v, epoch %x; want counts only at %x", in.Verified, in.Epoch, s.db.Epoch())
	}
}

func TestInspectReportsRevisions(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	showEpisode(t, s, "sonarr", epPath)
	in := inspect(t, s, InspectOptions{})
	if in.Rev != headRev(t, s) || in.ConfigRev == 0 || in.ConfigRev > in.Rev || in.OldestRetained == 0 || in.Commits == 0 {
		t.Errorf("Inspect() = rev %d, config rev %d, oldest %d, commits %d; want head %d, 0 < config rev <= rev, oldest and commits set",
			in.Rev, in.ConfigRev, in.OldestRetained, in.Commits, headRev(t, s))
	}
	rows := map[string]int{}
	for _, ns := range in.Namespaces {
		for _, tb := range ns.Tables {
			rows[ns.Name+"."+tb.Name] = tb.Rows
		}
	}
	if len(in.Namespaces) != 1 || in.Namespaces[0].Name != "core" || in.Namespaces[0].Format == ([32]byte{}) {
		t.Errorf("Inspect() namespaces = %+v, want core with its digest", in.Namespaces)
	}
	if rows["core.items"] != 1 || rows["core.instances"] != 1 {
		t.Errorf("table rows = %v, want one item and one instance", rows)
	}
}

// TestInspectExposesNoSecret holds Inspection to counts, sizes, digests and
// names, and checks a stored credential fingerprint reaches no rendering.
func TestInspectExposesNoSecret(t *testing.T) {
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		switch ty.Kind() {
		case reflect.Struct:
			for f := range ty.Fields() {
				walk(path+"."+f.Name, f.Type)
			}
		case reflect.Slice:
			if ty.Elem().Kind() == reflect.Uint8 {
				t.Errorf("%s is a byte slice, which can carry row bytes", path)
			}
			walk(path+"[]", ty.Elem())
		case reflect.String:
			if !strings.HasSuffix(path, ".Name") {
				t.Errorf("%s is text other than a table or namespace name", path)
			}
		case reflect.Array:
			if ty.Elem().Kind() != reflect.Uint8 {
				t.Errorf("%s is an array of %s", path, ty.Elem())
			}
		case reflect.Bool, reflect.Int, reflect.Int64, reflect.Uint64:
		default:
			t.Errorf("%s is a %s, outside counts, sizes, digests and names", path, ty.Kind())
		}
	}
	walk("Inspection", reflect.TypeFor[Inspection]())

	s := covLib(t, tempPath(t), policyOf(t, "en"))
	const fingerprint = "f1n6erpr1nt0123456789abcdef"
	if err := s.PutCredentialDisable(t.Context(), CredentialDisable{At: time.Unix(5, 0).UTC(), Provider: "opensubtitles", Fingerprint: fingerprint, Reason: "rejected"}); err != nil {
		t.Fatal(err)
	}
	in := inspect(t, s, InspectOptions{Repair: true})
	js, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{fmt.Sprintf("%+v", in), string(js)} {
		if strings.Contains(text, fingerprint) || strings.Contains(text, "opensubtitles") {
			t.Errorf("Inspect renders a stored credential: %s", text)
		}
	}
}

func TestInspectSpace(t *testing.T) {
	path := tempPath(t)
	s := covLib(t, path, policyOf(t, "en"))
	reason := strings.Repeat("r", 200)
	update(t, s, func(tx *bolttable.Tx) error {
		for i := range 1000 {
			if err := s.t.credentials.Put(tx, fmt.Sprintf("provider-%04d", i), &credentialRow{At: time.Unix(1, 0).UTC(), Reason: reason}); err != nil {
				return err
			}
		}
		return nil
	})
	full := inspect(t, s, InspectOptions{})
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if full.FileBytes != fi.Size() {
		t.Errorf("FileBytes = %d, want the file's %d bytes", full.FileBytes, fi.Size())
	}
	update(t, s, func(tx *bolttable.Tx) error {
		for i := range 1000 {
			if _, err := s.t.credentials.Delete(tx, fmt.Sprintf("provider-%04d", i)); err != nil {
				return err
			}
		}
		return nil
	})
	if emptied := inspect(t, s, InspectOptions{}); emptied.FreeBytes <= full.FreeBytes {
		t.Errorf("FreeBytes after deleting 1,000 rows = %d, want above %d", emptied.FreeBytes, full.FreeBytes)
	}
}
