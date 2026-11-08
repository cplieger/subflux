package statedb

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// fixture is one row of one core table with its encoded key and value.
type fixture struct {
	put          func(*bolttable.Tx, *coreTables) error
	decode       func() error
	table        string
	key, value   []byte
	keyVersion   uint32
	valueVersion uint32
}

func fix[K, V any](t *testing.T, table string, pick func(*coreTables) *bolttable.Table[K, V], kc bolttable.KeyCodec[K], vc bolttable.Codec[V], k K, v V) fixture {
	t.Helper()
	val, err := vc.Append(nil, &v)
	if err != nil {
		t.Fatalf("%s: Append: %v", table, err)
	}
	return fixture{
		table:        table,
		key:          kc.AppendKey(nil, k),
		value:        val,
		keyVersion:   kc.Version(),
		valueVersion: vc.Version(),
		put:          func(tx *bolttable.Tx, c *coreTables) error { return pick(c).Put(tx, k, &v) },
		decode:       func() error { var got V; return vc.Decode(val, &got) },
	}
}

var fixtureAt = time.Date(2026, 11, 5, 12, 0, 0, 500, time.UTC)

// coreFixtures holds one row per core table, in prefix order.
func coreFixtures(t *testing.T) []fixture {
	t.Helper()
	ep := mustKey(t, "tvdb-121361-s01e02")
	en, fr := mustTag(t, "en"), mustTag(t, "pt-BR")
	src := media.SourceID("sonarr")
	root := mediasource.NativeRef{Kind: media.Episode, ID: "73"}
	item := itemRef{Source: src, Root: root, Item: "4021"}
	file := fileKey{Inst: 7, Name: "Show S01E02.en.srt"}
	prov := provenanceRow{
		Provider: "opensubtitles", Release: "Show.S01E02.1080p.WEB-DL", MatchedBy: uint8(rank.Hash), Fit: uint8(rank.SameRelease),
		Facts:             factsRow{Source: uint8(rank.Same), Group: uint8(rank.Same), Hash: true},
		Timing:            outcomeRow{Confidence: 0.875, OffsetMs: -1250, Action: uint8(timing.ReferenceThenAudio), Method: uint8(timing.Audio), Verdict: uint8(timing.Verified)},
		MachineTranslated: true,
	}
	record := fileRow{
		Provenance: &prov, Tag: rowTag{t: en}, Timing: []byte{0}, Ordinal: 1, Generation: 2,
		Identity: identityRow{Size: 4096, MtimeNs: fixtureAt.UnixNano(), SHA256: sum32{1}}, Flags: flagHI, Locked: true,
	}
	attempt := downloadRow{
		At: fixtureAt, Target: rowTag{t: en}, Refusal: "different_cut", Items: []rowKey{{k: ep}}, Provenance: prov,
		Attempt: 1, Inst: 7, Run: id16{2}, Candidate: sum32{3}, Variant: uint8(language.HI), Delivered: true,
	}
	return []fixture{
		fix(t, "activation", func(c *coreTables) *bolttable.Table[constKey, activationRow] { return c.activation }, constKeys, activationCodec,
			constKey{}, activationRow{Sources: []media.SourceID{"radarr", src}, Codecs: []string{"hdmv_pgs_subtitle"}, ConfigRev: 9, Policy: sum32{4}}),
		fix(t, "passes", func(c *coreTables) *bolttable.Table[media.SourceID, passRow] { return c.passes }, sourceKeys, passCodec,
			src, passRow{Pass: 5, Roots: []nativeRefRow{{ID: root.ID, Kind: uint8(root.Kind)}}}),
		fix(t, "cursors", func(c *coreTables) *bolttable.Table[media.SourceID, cursorRow] { return c.cursors }, sourceKeys, cursorCodec,
			src, cursorRow{Cursor: []byte{1, 2, 3}}),
		fix(t, "roots", func(c *coreTables) *bolttable.Table[rootRef, rootRow] { return c.roots }, rootRefKeys, rootCodec,
			item.rootRef, rootRow{Language: rowTag{t: en}, Order: orderRow{Type: "dvd", Name: "DVD"}, Title: "Show", Link: "https://sonarr.example/series/show", AltTitles: []string{"Shō"}, External: externalRow{TVDB: 121361}, Year: 2011}),
		fix(t, "sightings", func(c *coreTables) *bolttable.Table[itemRef, sightingRow] { return c.sightings }, itemRefKeys, sightingCodec,
			item, sightingRow{
				Key: &rowKey{k: ep}, OriginalLang: rowTag{t: en}, VideoPath: "/tv/Show/S01E02.mkv", SceneName: "Show.S01E02.1080p.WEB-DL", Title: "Two",
				AudioLangs: []rowTag{{t: en}, {t: fr}}, Tags: []mediasource.TagRef{"3"}, External: externalRow{TVDB: 349232},
				AirDate: "2011-04-24", Numbering: numberingRow{Scheme: uint8(media.TVDB), Season: 1, Episode: 2},
				Series: numberingRow{Scheme: uint8(media.TVDB), Season: 1, Episode: 3}, Absolute: 2,
			}),
		fix(t, "stamps", func(c *coreTables) *bolttable.Table[itemRef, stampRow] { return c.stamps }, itemRefKeys, stampCodec,
			item, stampRow{Pass: 5}),
		fix(t, "overlaps", func(c *coreTables) *bolttable.Table[itemRef, overlapRow] { return c.overlaps }, itemRefKeys, overlapCodec,
			item, overlapRow{Owner: "radarr", ByKey: true}),
		fix(t, "items", func(c *coreTables) *bolttable.Table[media.Key, itemRow] { return c.items }, mediaKeys, itemCodec,
			ep, itemRow{Source: src, Item: item.Item, Root: nativeRefRow{ID: root.ID, Kind: uint8(root.Kind)}, Inst: 7}),
		fix(t, "instances", func(c *coreTables) *bolttable.Table[InstanceID, instanceRow] { return c.instances }, instKeys, instanceCodec,
			7, instanceRow{Owner: src, VideoPath: "/tv/Show/S01E02.mkv", SceneName: "Show.S01E02.1080p.WEB-DL", Items: []rowKey{{k: ep}}}),
		fix(t, "coverage", func(c *coreTables) *bolttable.Table[media.Key, coverageRow] { return c.coverage }, mediaKeys, coverageCodec,
			ep, coverageRow{Pairs: []coveragePair{
				{Tag: rowTag{t: en}, Variant: uint8(language.Standard), Coverage: uint8(Exact)},
				{Tag: rowTag{t: fr}, Variant: uint8(language.Standard), Coverage: uint8(Missing), LastRefusal: "different_cut"},
			}}),
		fix(t, "locks", func(c *coreTables) *bolttable.Table[lockKey, lockRow] { return c.locks }, lockKeys, lockCodec,
			lockKey{Key: ep, Tag: en, Variant: language.HI}, lockRow{At: fixtureAt}),
		fix(t, "files", func(c *coreTables) *bolttable.Table[fileKey, fileRow] { return c.files }, fileKeys, fileCodec,
			file, record),
		fix(t, "baselines", func(c *coreTables) *bolttable.Table[fileKey, baselineRow] { return c.baselines }, fileKeys, baselineCodec,
			file, baselineRow{Cues: []byte{1, 0}}),
		fix(t, "ordinals", func(c *coreTables) *bolttable.Table[OrdinalKey, ordinalRow] { return c.ordinals }, ordinalKeys, ordinalCodec,
			OrdinalKey{Inst: 7, Tag: en, Flags: language.Flags{HI: true}}, ordinalRow{Next: 2}),
		fix(t, "intents", func(c *coreTables) *bolttable.Table[fileKey, intentRow] { return c.intents }, fileKeys, intentCodec,
			file, intentRow{Record: &record, Baseline: []byte{1, 0}, Attempts: []downloadRow{attempt}, Removes: []removalRow{{Name: "Show S01E02.en.1.srt", SHA256: sum32{5}}}, SHA256: sum32{6}, Op: uint8(Deliver)}),
		fix(t, "local", func(c *coreTables) *bolttable.Table[InstanceID, localRow] { return c.local }, instKeys, localCodec,
			7, localRow{Files: []localFileRow{{Name: "Show S01E02.pt-BR.forced.srt", Tag: rowTag{t: fr}, Flags: flagForced}}}),
		fix(t, "embedded", func(c *coreTables) *bolttable.Table[InstanceID, embeddedRow] { return c.embedded }, instKeys, embeddedCodec,
			7, embeddedRow{Size: 1 << 30, MtimeNs: fixtureAt.UnixNano(), FPS: 23.976, Tracks: []trackRow{{Tag: rowTag{t: en}, Codec: "subrip", Title: "English", Index: 2}}}),
		fix(t, "downloads", func(c *coreTables) *bolttable.Table[uint64, downloadRow] { return c.downloads }, downloadSeqCodec, downloadCodec,
			11, attempt),
		fix(t, "work", func(c *coreTables) *bolttable.Table[InstanceID, workRow] { return c.work }, instKeys, workCodec,
			7, workRow{NotBefore: fixtureAt, FirstDeferral: fixtureAt, Gen: 3, Seq: 12, Cause: uint8(Import)}),
		fix(t, "jobs", func(c *coreTables) *bolttable.Table[status.JobID, jobRow] { return c.jobs }, jobKeys, jobCodec,
			"0123456789abcdef0123456789abcdef", jobRow{
				Started: fixtureAt, Ended: fixtureAt.Add(time.Minute), Key: &jobKeyRow{Key: "k1", Fingerprint: sum32{7}}, Scope: "library",
				Phase: "done", Cause: "manual", Owner: 1, Done: 3, Total: 3, Rev: 4,
				Kind: uint8(status.Scan), State: uint8(status.Done), Outcome: uint8(status.Succeeded),
			}),
		fix(t, "credentials", func(c *coreTables) *bolttable.Table[string, credentialRow] { return c.credentials }, textKeys, credentialCodec,
			"subdl", credentialRow{At: fixtureAt, Fingerprint: "ab12", Reason: "invalid key"}),
		fix(t, "no_results", func(c *coreTables) *bolttable.Table[noResultKey, noResultRow] { return c.noResults }, noResultKeys, noResultCodec,
			noResultKey{Key: ep, Tag: en, Provider: "subdl"}, noResultRow{Next: fixtureAt.Add(time.Hour), Count: 2}),
		fix(t, "counts", func(c *coreTables) *bolttable.Table[media.RootKey, countsRow] { return c.counts }, rootKeys, countsCodec,
			ep.Root(), countsRow{Pairs: []countPair{
				{Tag: rowTag{t: en}, Variant: uint8(language.Standard), Items: 3, Exact: 1, Substitute: 1, Missing: 1, Locked: 2},
			}}),
		fix(t, "root_evidence", func(c *coreTables) *bolttable.Table[rootRef, evidenceRow] { return c.rootEvidence }, rootRefKeys, evidenceCodec,
			item.rootRef, evidenceRow{Bound: readingRow{
				Titles: []titleSetRow{{Language: "eng", Found: true, Titles: []episodeTitleRow{{Title: "Two", ID: 349232}}}},
				Catalogue: catalogueRow{
					Fetched: fixtureAt, TVDB: 121361,
					Episodes: []airedEpisodeRow{{AirDate: "2011-04-24", ID: 349232, Season: 1, Episode: 2, Absolute: 2}},
					Orders: []orderPlacesRow{{
						Type: "dvd", Name: "DVD", Places: []placeRow{{Season: 1, Episode: 3, Listed: true}},
						Extra: []extraPlaceRow{{AirDate: "2011-05-01", Season: 1, Episode: 4}},
					}},
				},
			}}),
	}
}

// openCore opens a fresh file under a schema holding only core.
func openCore(t *testing.T) (*bolttable.DB, *coreTables) {
	t.Helper()
	c, err := defineCore(bolttable.NewSchema(schemaName))
	if err != nil {
		t.Fatalf("defineCore: %v", err)
	}
	db, err := bolttable.Open(filepath.Join(t.TempDir(), "subflux.bolt"), c.schema, bolttable.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, c
}
