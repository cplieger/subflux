package statedb

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// designSilent is design section 4's Silent column.
var designSilent = []string{
	"activation", "passes", "cursors", "stamps", "overlaps", "baselines",
	"ordinals", "intents", "work", "jobs", "credentials", "no_results", "root_evidence", "counts",
}

func TestSilentSetMatchesDesign(t *testing.T) {
	db, c := openCore(t)
	rev := func() uint64 {
		t.Helper()
		var r uint64
		if err := db.View(t.Context(), func(rtx *bolttable.ReadTx) (err error) { r, err = rtx.Rev(); return err }); err != nil {
			t.Fatalf("Rev: %v", err)
		}
		return r
	}
	for _, f := range coreFixtures(t) {
		t.Run(f.table, func(t *testing.T) {
			before := rev()
			if err := db.Update(t.Context(), func(tx *bolttable.Tx) error { return f.put(tx, c) }); err != nil {
				t.Fatalf("Put: %v", err)
			}
			silent := rev() == before
			if want := slices.Contains(designSilent, f.table); silent != want {
				t.Errorf("a write to %s left Rev unchanged = %v; the design's Silent column says %v", f.table, silent, want)
			}
		})
	}
}

func TestNoCrossNamespaceDeclaration(t *testing.T) {
	s := bolttable.NewSchema(schemaName)
	c, err := defineCore(s)
	if err != nil {
		t.Fatalf("defineCore: %v", err)
	}
	auth := bolttable.Must(s.Namespace("auth"))
	users := bolttable.Must(bolttable.Define(auth, 0x20, "users", instKeys, stampCodec, bolttable.TableOptions{}))
	t.Run("a cascade from core into auth is refused", func(t *testing.T) {
		err := bolttable.CascadePrefix(c.instances, users, 1, func(id InstanceID) []byte { return appendUint64(nil, uint64(id)) })
		if err == nil {
			t.Error("CascadePrefix(core.instances, auth.users) succeeded")
		}
	})
	t.Run("core holds no prefix of auth's range", func(t *testing.T) {
		if _, err := bolttable.Define(auth, 0x15, "clash", instKeys, stampCodec, bolttable.TableOptions{}); err == nil {
			t.Error("auth declared prefix 0x15, which core's credentials table holds")
		}
	})
	t.Run("core declares every table of section 4 up to 0x18", func(t *testing.T) {
		db, err := bolttable.Open(t.TempDir()+"/subflux.bolt", s, bolttable.Options{})
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer db.Close()
		st, err := db.Stats(t.Context())
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		var got []string
		for _, ns := range st.Namespaces {
			if ns.Name == "core" {
				for _, tb := range ns.Tables {
					got = append(got, tb.Name)
				}
			}
		}
		var want []string
		for _, f := range coreFixtures(t) {
			want = append(want, f.table)
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("core tables = %v; want %v", got, want)
		}
	})
}

func TestRowsRoundTrip(t *testing.T) {
	for _, f := range coreFixtures(t) {
		t.Run(f.table, func(t *testing.T) {
			if err := f.decode(); err != nil {
				t.Errorf("Decode(Append(row)) = %v", err)
			}
		})
	}
	t.Run("a decoded row re-encodes byte-equal", func(t *testing.T) {
		var dl downloadRow
		f := coreFixtures(t)[17]
		if f.table != "downloads" {
			t.Fatalf("fixture 17 is %s", f.table)
		}
		if err := downloadCodec.Decode(f.value, &dl); err != nil {
			t.Fatalf("Decode: %v", err)
		}
		b, err := downloadCodec.Append(nil, &dl)
		if err != nil || !bytes.Equal(b, f.value) {
			t.Errorf("re-encoded %s, %v; want %s", b, err, f.value)
		}
	})
}

func TestRowAppendRefusesWhatDecodeRefuses(t *testing.T) {
	cases := map[string]func() error{
		"a download with a zero variant": func() error {
			_, err := downloadCodec.Append(nil, &downloadRow{Provenance: provenanceRow{MatchedBy: uint8(rank.Hash), Fit: uint8(rank.Exact)}})
			return err
		},
		"a file record with a flag bit outside the set": func() error {
			_, err := fileCodec.Append(nil, &fileRow{Flags: 4})
			return err
		},
	}
	for name, appendRow := range cases {
		t.Run(name, func(t *testing.T) {
			if err := appendRow(); !errors.Is(err, errRowEnum) {
				t.Errorf("Append = %v, want errRowEnum", err)
			}
		})
	}
	t.Run("an upload's provenance, ranked by nothing, round-trips", func(t *testing.T) {
		in := provenanceRow{Release: "my.srt", Manual: true, Uploaded: true}
		b, err := downloadCodec.Append(nil, &downloadRow{Variant: uint8(language.Standard), Provenance: in})
		if err != nil {
			t.Fatalf("Append: %v", err)
		}
		var got downloadRow
		if err := downloadCodec.Decode(b, &got); err != nil || got.Provenance != in {
			t.Errorf("Decode = %+v, %v; want %+v", got.Provenance, err, in)
		}
	})
}

func TestOutcomeRowAcceptsEveryValue(t *testing.T) {
	last := outcomeRow{Action: uint8(timing.RigidReference), Skip: uint8(timing.Disabled), Method: uint8(timing.Audio), Verdict: uint8(timing.Unverified)}
	for name, o := range map[string]outcomeRow{"a pass that never ran": {}, "the last value of each enum": last} {
		t.Run(name, func(t *testing.T) {
			if err := o.check(); err != nil {
				t.Errorf("check(%+v) = %v", o, err)
			}
		})
	}
}

func TestRowDecodeRefuses(t *testing.T) {
	cases := []struct {
		decode func([]byte) error
		name   string
		in     string
		enum   bool
	}{
		{name: "an unknown field", decode: decodeAs(stampCodec), in: `{"pass":1,"extra":2}`},
		{name: "trailing data", decode: decodeAs(stampCodec), in: `{"pass":1}{"pass":2}`},
		{name: "a short sha256", decode: decodeAs(intentCodec), in: `{"sha256":"00","op":1}`},
		{name: "a variant outside its set", decode: decodeAs(coverageCodec), in: `{"pairs":[{"tag":"en","variant":4,"coverage":1}]}`, enum: true},
		{name: "a zero coverage", decode: decodeAs(coverageCodec), in: `{"pairs":[{"tag":"en","variant":1,"coverage":0}]}`, enum: true},
		{name: "a refusal on an exact pair", decode: decodeAs(coverageCodec), in: `{"pairs":[{"tag":"en","last_refusal":"fetch_gone","variant":1,"coverage":3}]}`},
		{name: "counts not partitioning items", decode: decodeAs(countsCodec), in: `{"pairs":[{"tag":"en","items":3,"exact":1,"substitute":1,"missing":0,"locked":0,"variant":1}]}`},
		{name: "a zero-item count", decode: decodeAs(countsCodec), in: `{"pairs":[{"tag":"en","items":0,"exact":0,"substitute":0,"missing":0,"locked":0,"variant":1}]}`},
		{name: "more locks than items", decode: decodeAs(countsCodec), in: `{"pairs":[{"tag":"en","items":1,"exact":1,"substitute":0,"missing":0,"locked":2,"variant":1}]}`},
		{name: "a count variant outside its set", decode: decodeAs(countsCodec), in: `{"pairs":[{"tag":"en","items":1,"exact":1,"substitute":0,"missing":0,"locked":0,"variant":4}]}`, enum: true},
		{name: "a flag bit outside the set", decode: decodeAs(localCodec), in: `{"files":[{"name":"a.srt","tag":"en","flags":4}]}`, enum: true},
		{name: "an op outside its set", decode: decodeAs(intentCodec), in: `{"sha256":"` + strings.Repeat("00", 32) + `","op":4}`, enum: true},
		{name: "a zero work cause", decode: decodeAs(workCodec), in: `{"not_before":"0001-01-01T00:00:00Z","first_deferral":"0001-01-01T00:00:00Z","gen":1,"seq":1,"cause":0}`, enum: true},
		{name: "a root kind outside its set", decode: decodeAs(itemCodec), in: `{"source":"s","item":"1","root":{"id":"1","kind":3},"inst":0}`, enum: true},
		{name: "a scheme outside its set", decode: decodeAs(sightingCodec), in: `{"original_lang":"","video_path":"","scene_name":"","title":"","external":{"tvdb":0,"tmdb":0,"imdb":0},"numbering":{"season":0,"episode":0,"scheme":9},"absolute":0,"excluded":false}`, enum: true},
		{name: "a matched by outside its set", decode: decodeAs(downloadCodec), in: downloadWith(`"matched_by":4,"fit":1`), enum: true},
		{name: "a zero no-result count", decode: decodeAs(noResultCodec), in: `{"next":"0001-01-01T00:00:00Z","count":0}`},
		{name: "a fit outside its set", decode: decodeAs(downloadCodec), in: downloadWith(`"matched_by":1,"fit":6`), enum: true},
		{name: "a timing action outside its set", decode: decodeAs(downloadCodec), in: downloadWith(`"timing":{"action":5},"matched_by":1,"fit":1`), enum: true},
		{name: "a timing skip reason outside its set", decode: decodeAs(fileCodec), in: `{"provenance":{"timing":{"skip":5},"matched_by":1,"fit":1},"tag":"en","timing":"AA==","identity":{"size":0,"mtime_ns":0,"sha256":"` + strings.Repeat("00", 32) + `"},"ordinal":1,"generation":1,"flags":0,"locked":false}`, enum: true},
		{name: "a timing method outside its set", decode: decodeAs(downloadCodec), in: downloadWith(`"timing":{"method":3},"matched_by":1,"fit":1`), enum: true},
		{name: "a timing verdict outside its set", decode: decodeAs(downloadCodec), in: downloadWith(`"timing":{"verdict":4},"matched_by":1,"fit":1`), enum: true},
		{name: "a series numbering scheme outside its set", decode: decodeAs(sightingCodec), in: `{"series_numbering":{"season":1,"episode":1,"scheme":9}}`, enum: true},
		{name: "evidence its Validate refuses", decode: decodeAs(evidenceCodec), in: `{"bound":{"titles":null,"catalogue":{"fetched":"2026-11-05T12:00:00Z","orders":null,"episodes":null,"tvdb":0}}}`},
		{name: "a job state outside its set", decode: decodeAs(jobCodec), in: `{"started":"0001-01-01T00:00:00Z","ended":"0001-01-01T00:00:00Z","scope":"","phase":"","cause":"","owner":0,"done":0,"total":0,"rev":1,"kind":1,"state":4,"outcome":0,"cancellable":false}`, enum: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode([]byte(tc.in))
			if err == nil {
				t.Fatal("decoded")
			}
			if tc.enum && !errors.Is(err, errRowEnum) {
				t.Errorf("err = %v; want errRowEnum", err)
			}
		})
	}
}

func decodeAs[V any, P interface {
	*V
	checker
}](c rowCodec[V, P]) func([]byte) error {
	return func(b []byte) error { var v V; return c.Decode(b, &v) }
}

func downloadWith(prov string) string {
	zero := strings.Repeat("00", 32)
	return `{"at":"2026-11-05T12:00:00Z","target":"en","refusal":"","items":[],"provenance":{"provider":"p","release":"r","facts":{"source":0,"group":0,"service":0,"cut":0,"rate":0,"hash":false,"pre_release":false,"season_pack":false,"episode_by_number":false},` +
		prov + `,"manual":false,"uploaded":false,"machine_translated":false},"attempt":1,"inst":1,"run":"` + zero[:32] + `","candidate":"` + zero + `","variant":1,"delivered":false}`
}

func TestWorkOrderServesItemFirst(t *testing.T) {
	order := func(cause WorkCause, seq uint64) []byte {
		return workOrderEntries(1, &workRow{Cause: uint8(cause), Seq: seq})[0].Key
	}
	keys := [][]byte{order(Scan, 1), order(Import, 9), order(Item, 50), order(Scan, 2), order(Import, 3)}
	slices.SortFunc(keys, bytes.Compare)
	want := [][]byte{order(Item, 50), order(Import, 3), order(Import, 9), order(Scan, 1), order(Scan, 2)}
	if !slices.EqualFunc(keys, want, bytes.Equal) {
		t.Errorf("order index sorts %x; want Item, then Import, then Scan, each oldest first", keys)
	}
	t.Run("the projection carries cause, gen and not-before", func(t *testing.T) {
		at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
		got := workOrderEntries(1, &workRow{Cause: uint8(Import), Gen: 7, NotBefore: at})[0].Value
		want := appendTime(appendUint64([]byte{byte(Import)}, 7), at)
		if !bytes.Equal(got, want) {
			t.Errorf("projection = %x; want %x", got, want)
		}
	})
}

func TestHistoryIndexesNewestFirst(t *testing.T) {
	e1, e2 := mustKey(t, "tvdb-121361-s01e01"), mustKey(t, "tvdb-121361-s01e02")
	other := mustKey(t, "tvdb-12-s01e01")
	row := &downloadRow{Items: []rowKey{{k: e1}, {k: e2}, {k: other}}}
	t.Run("one item entry per element", func(t *testing.T) {
		if n := len(itemHistoryEntries(5, row)); n != 3 {
			t.Errorf("entries = %d; want 3", n)
		}
	})
	t.Run("one root entry per distinct root", func(t *testing.T) {
		es := rootHistoryEntries(5, row)
		if len(es) != 2 {
			t.Fatalf("entries = %d; want 2", len(es))
		}
		if !bytes.HasPrefix(es[0].Key, e1.Root().Bytes()) || !bytes.HasPrefix(es[1].Key, other.Root().Bytes()) {
			t.Errorf("root entries = %x", es)
		}
	})
	t.Run("a later download sorts first", func(t *testing.T) {
		older := itemHistoryEntries(5, &downloadRow{Items: []rowKey{{k: e1}}})[0].Key
		newer := itemHistoryEntries(6, &downloadRow{Items: []rowKey{{k: e1}}})[0].Key
		if bytes.Compare(newer, older) >= 0 {
			t.Errorf("sequence 6 sorts at or after 5: %x, %x", newer, older)
		}
	})
}

func TestJobIndexesAreSparse(t *testing.T) {
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	done, running := uint8(status.Done), uint8(status.Running)
	cases := []struct {
		row                               jobRow
		name                              string
		key, ended, ownerEnded, userEnded int
		owned                             int64
	}{
		{name: "a running user job", row: jobRow{Owner: 3, State: running}, owned: 1},
		{name: "an ended keyed user job", row: jobRow{Owner: 3, State: done, Ended: at, Key: &jobKeyRow{Key: "k"}}, key: 1, ended: 1, ownerEnded: 1, userEnded: 1, owned: 1},
		{name: "an ended bypass job", row: jobRow{Owner: status.BypassOwner, State: done, Ended: at}, ended: 1, ownerEnded: 1, userEnded: 1, owned: 1},
		{name: "an ended system job", row: jobRow{Owner: status.SystemOwner, State: done, Ended: at}, ended: 1, ownerEnded: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &tc.row
			if n := len(jobKeyEntries("", r)); n != tc.key {
				t.Errorf("key entries = %d; want %d", n, tc.key)
			}
			if n := len(jobEndedEntries("", r)); n != tc.ended {
				t.Errorf("ended entries = %d; want %d", n, tc.ended)
			}
			if n := len(ownerEndedEntries("", r)); n != tc.ownerEnded {
				t.Errorf("owner_ended entries = %d; want %d", n, tc.ownerEnded)
			}
			if n := len(userEndedEntries("", r)); n != tc.userEnded {
				t.Errorf("user_ended entries = %d; want %d", n, tc.userEnded)
			}
			if w := ownedWeight("", r); w != tc.owned {
				t.Errorf("owned weight = %d; want %d", w, tc.owned)
			}
		})
	}
	t.Run("owner_ended orders by owner, then instant", func(t *testing.T) {
		k := func(owner int64, at time.Time) []byte {
			return ownerEndedEntries("", &jobRow{Owner: owner, State: done, Ended: at})[0].Key
		}
		if bytes.Compare(k(status.BypassOwner, at.Add(time.Hour)), k(status.SystemOwner, at)) >= 0 ||
			bytes.Compare(k(7, at.Add(-time.Second/2)), k(7, at)) >= 0 {
			t.Error("owner_ended keys do not order by owner, then end instant")
		}
	})
}

func TestUnidentifiedAndOrphanedAreSparse(t *testing.T) {
	k := mustKey(t, "tmdb-603")
	if n := len(unidentifiedEntries(itemRef{}, &sightingRow{Key: &rowKey{k: k}})); n != 0 {
		t.Errorf("an identified sighting has %d unidentified entries", n)
	}
	if n := len(unidentifiedEntries(itemRef{}, &sightingRow{})); n != 1 {
		t.Errorf("an unidentified sighting has %d entries; want 1", n)
	}
	if n := len(orphanedEntries(1, &instanceRow{Owner: media.SourceID("radarr")})); n != 0 {
		t.Errorf("an owned instance has %d orphaned entries", n)
	}
	if n := len(orphanedEntries(1, &instanceRow{Orphaned: time.Unix(1, 0)})); n != 1 {
		t.Errorf("an ownerless instance has %d orphaned entries; want 1", n)
	}
}
