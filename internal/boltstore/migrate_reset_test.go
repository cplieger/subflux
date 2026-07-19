package boltstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
	"pgregory.net/rapid"
)

// This file holds the destructive-step proof fixtures (Requirements 3, 5.1,
// 5.2): a simulated CORE bump through resetCorePreserving proving the
// irreplaceable set (auth domain, manual rows, sync offsets) survives, a
// simulated AUTH bump through authstore.ResetPreserving proving the core
// domain is untouched and the auth rows/indexes/links/sequences are intact,
// and a rapid property pinning resetCorePreserving against random stores.
// Both bumps use injected ladders (Requirement 5.5).

// resetFixture records what seedResetFixture wrote, so assertions compare
// against the seeded truth.
type resetFixture struct {
	users      []auth.User            // alice (admin, OIDC), bob
	passkey    auth.PasskeyCredential // alice's
	apiKey     auth.Key               // bob's
	manualRows []*api.DownloadRecord  // in insertion (old-ID) order
	autoRows   []*api.DownloadRecord  // rebuilt-as-designed rows (must NOT survive)
	offsets    map[string]int64       // sync offsets by path
}

// seedResetFixture builds the v1 fixture store of Requirement 5: 2 users +
// passkey + API key, 3 manual rows (locks + manual history) interleaved with
// 2 auto rows (so surviving IDs prove the old-ID-order restore), sync
// offsets, subtitle files, an adaptive-backoff row, and a scan-state row. The
// store and auth handles are closed again so a migration open can take the
// lock.
func seedResetFixture(t *testing.T, path string) *resetFixture {
	t.Helper()
	ctx := context.Background()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer func() {
		if cerr := db.Close(ctx); cerr != nil {
			t.Fatalf("Close: %v", cerr)
		}
	}()

	fx := &resetFixture{offsets: map[string]int64{}}
	seedResetAuth(t, db, fx)

	dl := func(mt api.MediaType, mid, lang string, variant api.Variant, path string, score int, manual bool) *api.DownloadRecord {
		rec := &api.DownloadRecord{
			MediaType: mt, MediaID: mid, Language: lang, Variant: variant,
			ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "Rel." + path,
			Path: path, Score: score,
			Meta: &api.DownloadMeta{Title: "T-" + mid, VideoPath: "/m/" + mid + ".mkv", Manual: manual, Season: 1, Episode: 2},
		}
		if err := db.SaveDownload(ctx, rec); err != nil {
			t.Fatalf("SaveDownload(%s): %v", path, err)
		}
		return rec
	}

	// Interleave autos and manuals so old IDs are non-contiguous for manuals.
	fx.autoRows = append(fx.autoRows, dl(api.MediaTypeMovie, "tt1", "en", api.VariantStandard, "/m/tt1.en.srt", 70, false))
	fx.manualRows = append(fx.manualRows, dl(api.MediaTypeMovie, "tt1", "en", api.VariantStandard, "/m/tt1.en.1.srt", 88, true))
	fx.autoRows = append(fx.autoRows, dl(api.MediaTypeMovie, "tt1", "fr", api.VariantStandard, "/m/tt1.fr.srt", 60, false))
	fx.manualRows = append(fx.manualRows, dl(api.MediaTypeMovie, "tt1", "en", api.VariantStandard, "/m/tt1.en.2.srt", 92, true))
	fx.manualRows = append(fx.manualRows, dl(api.MediaTypeEpisode, "tt2", "fr", api.VariantForced, "/m/tt2.fr.forced.1.srt", 55, true))

	fx.offsets["/m/tt1.en.1.srt"] = 250
	fx.offsets["/m/tt1.fr.srt"] = -40
	for p, off := range fx.offsets {
		if err := db.SetSyncOffset(ctx, p, off); err != nil {
			t.Fatalf("SetSyncOffset(%s): %v", p, err)
		}
	}

	if _, err := db.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tt1", []api.SubtitleFile{
		{Language: "en", Variant: api.VariantStandard, Source: api.SourceExternal, Codec: "subrip", Path: "/m/tt1.en.1.srt"},
		{Language: "fr", Variant: api.VariantStandard, Source: api.SourceExternal, Codec: "subrip", Path: "/m/tt1.fr.srt"},
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles: %v", err)
	}

	// Backoff on a language no download clears (SaveDownload clears its own
	// triple), plus a scan-state row.
	if err := db.RecordNoResult(ctx, api.MediaTypeMovie, "tt1", "de", api.ProviderNameOpenSubtitles, resetBackoffParams); err != nil {
		t.Fatalf("RecordNoResult: %v", err)
	}
	if err := db.RecordScanState(ctx, &api.ScanRecord{MediaType: api.MediaTypeMovie, MediaID: "tt1", Title: "T-tt1", AudioLang: "en"}); err != nil {
		t.Fatalf("RecordScanState: %v", err)
	}
	return fx
}

// seedResetAuth writes the auth half of the fixture over the shared handle.
func seedResetAuth(t *testing.T, db *DB, fx *resetFixture) {
	t.Helper()
	ctx := context.Background()
	as := authstore.New(db.BoltDB())

	alice := &auth.User{Username: "alice", Email: "alice@example.com", Role: auth.RoleAdmin, PasswordHash: "hash-a", Enabled: true, OIDCIssuer: "https://idp", OIDCSub: "sub-alice"}
	bob := &auth.User{Username: "bob", Role: auth.RoleUser, PasswordHash: "hash-b", Enabled: true}
	for _, u := range []*auth.User{alice, bob} {
		if err := as.CreateUser(ctx, u); err != nil {
			t.Fatalf("CreateUser(%s): %v", u.Username, err)
		}
	}
	pk := &auth.PasskeyCredential{
		UserID: alice.ID, Name: "yubi", CredentialID: []byte{0x01, 0x02, 0x00, 0x03},
		PublicKey: []byte{0xAA, 0xBB}, SignCount: 7, BackupEligible: true, UserVerified: true,
	}
	if err := as.CreatePasskey(ctx, pk); err != nil {
		t.Fatalf("CreatePasskey: %v", err)
	}
	key := &auth.Key{UserID: bob.ID, KeyHash: "hash-key-1", KeyPrefix: "sfx_", KeySuffix: "beef", Label: "cli"}
	if err := as.CreateAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	fx.users = []auth.User{*alice, *bob}
	fx.passkey = *pk
	fx.apiKey = *key
}

// resetBackoffParams are fixed backoff parameters for the fixture's
// RecordNoResult rows; only the row's existence matters to the reset.
var resetBackoffParams = api.BackoffParams{
	InitialDelay: time.Minute,
	MaxDelay:     time.Hour,
	Multiplier:   2,
}

// resetLadderDomains returns the injected domain pair simulating a CORE bump
// to v2 whose step is the destructive resetCorePreserving fallback.
func resetLadderDomains() (*migrationDomain, *migrationDomain) {
	core := testCoreDomain(2, []migration{{
		from: 1, to: 2, kind: migrateInPlace, run: resetCorePreserving,
	}})
	return core, testAuthDomain(1, nil)
}

// authBucketSnapshots copies every auth bucket's raw contents for the
// byte-identity assertion.
func authBucketSnapshots(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() { _ = db.Close() }()
	err = db.View(func(tx *bolt.Tx) error {
		for _, name := range authBuckets {
			out[string(name)] = bucketMap(tx, string(name))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot auth buckets: %v", err)
	}
	return out
}

// TestMigrate_coreResetPreservesIrreplaceable is the Requirement 5.1 fixture:
// a simulated core-version bump through the export -> reset -> restore
// fallback proves auth users/passkeys/API keys survive byte-identically (auth
// stamp included), manual rows (locks + history) survive with fresh IDs in
// old-ID order, sync offsets survive and reattach after the file inventory
// rebuild, and the derived core rows are rebuilt as designed (gone until the
// next scan; counters consistent).
func TestMigrate_coreResetPreservesIrreplaceable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	fx := seedResetFixture(t, path)
	authBefore := authBucketSnapshots(t, path)
	authStampBefore := rawReadStamp(t, path, metaKeyAuthSchemaVersion)

	core, auth := resetLadderDomains()
	db, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	assertCoreResetState(t, db, fx)
	assertCoreResetOffsets(t, db, fx)
	assertCoreResetDerivedRowsGone(t, db)
	assertOffsetReattachment(t, db, fx)

	// Auth domain: stamp and every bucket byte-identical, and functionally
	// alive through the auth store.
	if got := rawStampInOpenDB(t, db, metaKeyAuthSchemaVersion); string(got) != string(authStampBefore) {
		t.Errorf("auth stamp changed across the core reset: %x -> %x", authStampBefore, got)
	}
	assertAuthBucketsIdentical(t, db, authBefore)
	as := authstore.New(db.BoltDB())
	u, err := as.GetUserByUsername(ctx, "alice")
	if err != nil || u == nil || u.ID != fx.users[0].ID || u.PasswordHash != "hash-a" {
		t.Errorf("GetUserByUsername(alice) after reset = (%+v, %v), want the seeded user", u, err)
	}
	pks, err := as.GetPasskeysByUserID(ctx, fx.users[0].ID)
	if err != nil || len(pks) != 1 || pks[0].SignCount != 7 {
		t.Errorf("passkeys after reset = (%+v, %v), want the seeded passkey", pks, err)
	}
}

// rawStampInOpenDB reads a stamp's raw bytes through an already-open store
// handle.
func rawStampInOpenDB(t *testing.T, db *DB, key []byte) []byte {
	t.Helper()
	var out []byte
	err := db.db.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket([]byte(bucketMeta)).Get(key); v != nil {
			out = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read stamp: %v", err)
	}
	return out
}

// assertAuthBucketsIdentical compares every auth bucket against the pre-reset
// snapshot byte-for-byte.
func assertAuthBucketsIdentical(t *testing.T, db *DB, before map[string]map[string]string) {
	t.Helper()
	err := db.db.View(func(tx *bolt.Tx) error {
		for _, name := range authBuckets {
			got := bucketMap(tx, string(name))
			want := before[string(name)]
			if len(got) != len(want) {
				t.Errorf("auth bucket %q: %d entries after reset, want %d", name, len(got), len(want))
				continue
			}
			for k, wv := range want {
				if gv, ok := got[k]; !ok || gv != wv {
					t.Errorf("auth bucket %q entry %x changed across the core reset", name, k)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

// assertCoreResetState proves exactly the manual rows survived, with fresh
// sequential IDs assigned in old-ID order and every field preserved, and that
// their locks still hold.
func assertCoreResetState(t *testing.T, db *DB, fx *resetFixture) {
	t.Helper()
	ctx := context.Background()
	entries, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if len(entries) != len(fx.manualRows) {
		t.Fatalf("state rows after reset = %d, want %d (manual only)", len(entries), len(fx.manualRows))
	}
	byPath := make(map[string]api.StateEntry, len(entries))
	for _, e := range entries {
		if !e.Manual {
			t.Errorf("non-manual row %q survived the reset", e.Path)
		}
		byPath[e.Path] = e
	}
	for i, rec := range fx.manualRows {
		e, ok := byPath[rec.Path]
		if !ok {
			t.Errorf("manual row %q lost in reset", rec.Path)
			continue
		}
		if e.MediaType != rec.MediaType || e.MediaID != rec.MediaID || e.Language != rec.Language ||
			e.Variant != rec.Variant || e.Provider != rec.ProviderName || e.ReleaseName != rec.ReleaseName ||
			e.Score != rec.Score || e.Season != 1 || e.Episode != 2 {
			t.Errorf("manual row %q fields changed: %+v", rec.Path, e)
		}
		// Fresh IDs 1..n in old insertion order.
		if e.ID != int64(i+1) {
			t.Errorf("manual row %q restored with id %d, want %d (fresh ids in old-ID order)", rec.Path, e.ID, i+1)
		}
	}

	for _, rec := range fx.manualRows {
		locked, err := db.IsManuallyLocked(ctx, rec.MediaType, rec.MediaID, rec.Language, rec.Variant)
		if err != nil || !locked {
			t.Errorf("IsManuallyLocked(%s/%s/%s/%s) = (%v, %v), want locked after reset", rec.MediaType, rec.MediaID, rec.Language, rec.Variant, locked, err)
		}
	}

	downloads, attempts, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads != len(fx.manualRows) || attempts != 0 {
		t.Errorf("Stats = (%d downloads, %d attempts), want (%d, 0): counters must restart consistently", downloads, attempts, len(fx.manualRows))
	}
}

// assertCoreResetOffsets proves the sync offsets survived verbatim.
func assertCoreResetOffsets(t *testing.T, db *DB, fx *resetFixture) {
	t.Helper()
	ctx := context.Background()
	for p, want := range fx.offsets {
		got, err := db.GetSyncOffset(ctx, p)
		if err != nil || got != want {
			t.Errorf("GetSyncOffset(%s) = (%d, %v), want %d", p, got, err, want)
		}
	}
}

// assertCoreResetDerivedRowsGone proves the derived buckets were emptied for
// the next scan to rebuild: no auto state, no subtitle files, no backoff, no
// scan state.
func assertCoreResetDerivedRowsGone(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	if score, _, found, err := db.CurrentScore(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard); err != nil || found {
		t.Errorf("CurrentScore after reset = (%d, found=%v, %v), want no auto row", score, found, err)
	}
	if n, err := db.TotalSubtitleFiles(ctx); err != nil || n != 0 {
		t.Errorf("TotalSubtitleFiles after reset = (%d, %v), want 0", n, err)
	}
	if items, err := db.GetBackoffItems(ctx); err != nil || len(items) != 0 {
		t.Errorf("GetBackoffItems after reset = (%v, %v), want none", items, err)
	}
	err := db.db.View(func(tx *bolt.Tx) error {
		for _, name := range []string{bucketScanState, bucketIxScanAt, bucketSubtitleFiles, bucketSearchAttempts} {
			if m := bucketMap(tx, name); len(m) != 0 {
				t.Errorf("bucket %q holds %d rows after reset, want empty", name, len(m))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

// assertOffsetReattachment proves a restored (momentarily orphaned) offset
// reattaches once the scan rebuilds the file inventory: after
// RecordSubtitleFiles re-lists the file, GetSubtitleFiles reports the offset.
func assertOffsetReattachment(t *testing.T, db *DB, fx *resetFixture) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tt1", []api.SubtitleFile{
		{Language: "en", Variant: api.VariantStandard, Source: api.SourceExternal, Codec: "subrip", Path: "/m/tt1.en.1.srt"},
	}); err != nil {
		t.Fatalf("RecordSubtitleFiles (rebuild): %v", err)
	}
	files, err := db.GetSubtitleFiles(ctx, api.MediaTypeMovie, "tt1")
	if err != nil {
		t.Fatalf("GetSubtitleFiles: %v", err)
	}
	found := false
	for _, f := range files {
		if f.Path == "/m/tt1.en.1.srt" {
			found = true
			if f.OffsetMs != fx.offsets["/m/tt1.en.1.srt"] {
				t.Errorf("rebuilt file offset = %d, want %d (offset must reattach)", f.OffsetMs, fx.offsets["/m/tt1.en.1.srt"])
			}
		}
	}
	if !found {
		t.Error("rebuilt subtitle file missing from GetSubtitleFiles")
	}
}

// TestMigrate_coreResetPreservesUnknownJSONFields proves Requirement 3.1(a)'s
// FULL raw-row preservation: a manual row carrying an additive JSON field
// this build's stateRec does not model survives the reset round-trip
// byte-for-byte, the only difference being the rewritten surrogate id — the
// restore must never launder rows through the current struct and silently
// drop what a newer (or instrumented) build wrote.
func TestMigrate_coreResetPreservesUnknownJSONFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	ctx := context.Background()

	// Seed an auto row first (id 1) so the manual row lands at id 2 and the
	// restore genuinely REWRITES the id (2 -> fresh 1), not just re-emits it.
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, rec := range []*api.DownloadRecord{
		{
			MediaType: api.MediaTypeMovie, MediaID: "tt1", Language: "en",
			ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "Rel.A",
			Path: "/m/tt1.en.srt", Score: 70,
			Meta: &api.DownloadMeta{Title: "T", VideoPath: "/m/tt1.mkv"},
		},
		{
			MediaType: api.MediaTypeMovie, MediaID: "tt1", Language: "en",
			ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "Rel.B",
			Path: "/m/tt1.en.1.srt", Score: 88,
			Meta: &api.DownloadMeta{Title: "T", VideoPath: "/m/tt1.mkv", Manual: true},
		},
	} {
		if err := db.SaveDownload(ctx, rec); err != nil {
			t.Fatalf("SaveDownload(%s): %v", rec.Path, err)
		}
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Plant an unknown additive field into the manual row's raw JSON value,
	// exactly as a newer build's encoder would have.
	var orig []byte
	rawUpdate(t, path, func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		v := sb.Get(stateKey(2))
		if v == nil {
			return errors.New("manual row id 2 missing")
		}
		planted := append([]byte(nil), v[:len(v)-1]...) // strip the trailing '}'
		planted = append(planted, []byte(`,"future_field":{"keep":[1,2,3],"s":"x\u0000y"}}`)...)
		orig = planted
		return sb.Put(stateKey(2), planted)
	})
	if n := strings.Count(string(orig), `"id":2`); n != 1 {
		t.Fatalf(`fixture invariant broken: %d occurrences of "id":2 in %s, want 1`, n, orig)
	}

	core, auth := resetLadderDomains()
	mdb, err := openWithDomains(path, core, auth)
	if err != nil {
		t.Fatalf("migration open: %v", err)
	}
	t.Cleanup(func() { _ = mdb.Close(ctx) })

	// The sole survivor sits at fresh id 1 and is the planted bytes with
	// ONLY the id value rewritten.
	var got []byte
	err = mdb.db.View(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if k, _ := sb.Cursor().First(); string(k) != string(stateKey(1)) {
			t.Errorf("first surviving key = %x, want fresh id 1", k)
		}
		if v := sb.Get(stateKey(1)); v != nil {
			got = append([]byte(nil), v...)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	want := strings.Replace(string(orig), `"id":2`, `"id":1`, 1)
	if string(got) != want {
		t.Errorf("restored raw row = %s, want the planted bytes with only the id rewritten: %s", got, want)
	}

	// The raw-preserved row still answers through the canonical read paths:
	// the lock holds and the row decodes with its known fields intact.
	locked, err := mdb.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard)
	if err != nil || !locked {
		t.Errorf("IsManuallyLocked after raw-preserving reset = (%v, %v), want locked", locked, err)
	}
	entries, err := mdb.GetState(ctx, &api.StateQuery{})
	if err != nil || len(entries) != 1 {
		t.Fatalf("GetState = (%d entries, %v), want the one manual survivor", len(entries), err)
	}
	if e := entries[0]; e.ID != 1 || !e.Manual || e.Path != "/m/tt1.en.1.srt" || e.Score != 88 {
		t.Errorf("survivor decoded as %+v, want id 1, manual, path /m/tt1.en.1.srt, score 88", e)
	}
}

// TestMigrate_authResetPreservesCore is the Requirement 5.2 fixture: a
// simulated AUTH-version bump through the authstore-owned ResetPreserving
// seam proves the core domain — including manual rows, offsets, and every
// core bucket byte-for-byte — is untouched, and the auth rows come back with
// IDs, ownership links, uniqueness indexes, and bucket sequences intact.
func TestMigrate_authResetPreservesCore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	fx := seedResetFixture(t, path)
	coreStampBefore := rawReadStamp(t, path, metaKeyCoreSchemaVersion)
	coreBefore := coreBucketSnapshots(t, path)

	authDom := testAuthDomain(2, []migration{{
		from: 1, to: 2, kind: migrateInPlace,
		run: func(tx *bolt.Tx) error {
			return authstore.ResetPreserving(tx, func(*authstore.Rowset) error { return nil })
		},
	}})
	db, err := openWithDomains(path, testCoreDomain(1, nil), authDom)
	if err != nil {
		t.Fatalf("migration open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	// Core domain byte-identical (stamp and every core bucket).
	if got := rawStampInOpenDB(t, db, metaKeyCoreSchemaVersion); string(got) != string(coreStampBefore) {
		t.Errorf("core stamp changed across the auth reset: %x -> %x", coreStampBefore, got)
	}
	assertCoreBucketsIdentical(t, db, coreBefore)

	// Auth domain functionally intact: same IDs, ownership links, uniqueness,
	// and a live sequence.
	as := authstore.New(db.BoltDB())
	for i, want := range fx.users {
		u, err := as.GetUserByUsername(ctx, want.Username)
		if err != nil || u == nil || u.ID != want.ID || u.PasswordHash != want.PasswordHash {
			t.Errorf("user %d (%s) after auth reset = (%+v, %v), want preserved", i, want.Username, u, err)
		}
	}
	if u, err := as.GetUserByOIDCSub(ctx, "https://idp", "sub-alice"); err != nil || u == nil || u.ID != fx.users[0].ID {
		t.Errorf("GetUserByOIDCSub after auth reset = (%+v, %v), want alice via ix_user_oidc", u, err)
	}
	pks, err := as.GetPasskeysByUserID(ctx, fx.users[0].ID)
	if err != nil || len(pks) != 1 || string(pks[0].CredentialID) != string(fx.passkey.CredentialID) || pks[0].ID != fx.passkey.ID {
		t.Errorf("passkeys after auth reset = (%+v, %v), want the seeded credential with its id", pks, err)
	}
	keys, err := as.ListAPIKeysByUserID(ctx, fx.users[1].ID)
	if err != nil || len(keys) != 1 || keys[0].KeyHash != fx.apiKey.KeyHash || keys[0].ID != fx.apiKey.ID {
		t.Errorf("api keys after auth reset = (%+v, %v), want the seeded key with its id", keys, err)
	}

	// Uniqueness indexes are live: a duplicate username conflicts.
	if err := as.CreateUser(ctx, &auth.User{Username: "ALICE", Role: auth.RoleUser}); err == nil {
		t.Error("duplicate username accepted after auth reset; ix_user_name must be rebuilt")
	}
	// Sequences are intact: a fresh user allocates past the preserved ids.
	fresh := &auth.User{Username: "carol", Role: auth.RoleUser}
	if err := as.CreateUser(ctx, fresh); err != nil {
		t.Fatalf("CreateUser(carol): %v", err)
	}
	if fresh.ID <= fx.users[1].ID {
		t.Errorf("fresh user id = %d, want > %d (sequence must survive the reset)", fresh.ID, fx.users[1].ID)
	}

	// Core still functionally alive: locks and offsets answer as before.
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard)
	if err != nil || !locked {
		t.Errorf("IsManuallyLocked after auth reset = (%v, %v), want locked", locked, err)
	}
	if got, err := db.GetSyncOffset(ctx, "/m/tt1.en.1.srt"); err != nil || got != fx.offsets["/m/tt1.en.1.srt"] {
		t.Errorf("GetSyncOffset after auth reset = (%d, %v), want %d", got, err, fx.offsets["/m/tt1.en.1.srt"])
	}
}

// coreBucketSnapshots copies every core bucket's raw contents (meta excluded:
// its auth stamp legitimately changes on an auth bump).
func coreBucketSnapshots(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	out := map[string]map[string]string{}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() { _ = db.Close() }()
	err = db.View(func(tx *bolt.Tx) error {
		for _, name := range coreBuckets {
			if string(name) == bucketMeta {
				continue
			}
			out[string(name)] = bucketMap(tx, string(name))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot core buckets: %v", err)
	}
	return out
}

// assertCoreBucketsIdentical compares every non-meta core bucket against the
// pre-reset snapshot byte-for-byte.
func assertCoreBucketsIdentical(t *testing.T, db *DB, before map[string]map[string]string) {
	t.Helper()
	err := db.db.View(func(tx *bolt.Tx) error {
		for name, want := range before {
			got := bucketMap(tx, name)
			if len(got) != len(want) {
				t.Errorf("core bucket %q: %d entries after auth reset, want %d", name, len(got), len(want))
				continue
			}
			for k, wv := range want {
				if gv, ok := got[k]; !ok || gv != wv {
					t.Errorf("core bucket %q entry %x changed across the auth reset", name, k)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

// resetPropQuad is the property test's (media_type, media_id, language,
// variant) draw pool element.
type resetPropQuad struct {
	mt      api.MediaType
	mid     string
	lang    string
	variant api.Variant
}

// TestResetCorePreserving_property pins the destructive-core fallback against
// random stores: after a simulated bump, exactly the manual rows survive
// (every field preserved, fresh IDs 1..n assigned in old-ID order), every
// sync offset survives, the attempts/files counters restart at zero, and all
// state indexes and counters equal a full primary re-scan.
func TestResetCorePreserving_property(t *testing.T) {
	quads := []resetPropQuad{
		{api.MediaTypeMovie, "tt1", "en", api.VariantStandard},
		{api.MediaTypeMovie, "tt1", "fr", api.VariantStandard},
		{api.MediaTypeEpisode, "tt2", "en", api.VariantForced},
	}
	offsetPaths := []string{"/m/o1.srt", "/m/o2.srt", "/m/o3.srt"}

	rapid.Check(t, func(rt *rapid.T) {
		dir, err := os.MkdirTemp("", "boltmigrate-")
		if err != nil {
			rt.Fatalf("MkdirTemp: %v", err)
		}
		rt.Cleanup(func() { _ = os.RemoveAll(dir) })
		path := filepath.Join(dir, "subflux.bolt")
		db, err := Open(path)
		if err != nil {
			rt.Fatalf("Open: %v", err)
		}
		ctx := context.Background()

		wantOffsets := seedPropertyStore(rt, db, quads, offsetPaths)
		wantManual, wantOrder := snapshotManualRows(rt, db)
		if err := db.Close(ctx); err != nil {
			rt.Fatalf("Close: %v", err)
		}

		core, auth := resetLadderDomains()
		mdb, err := openWithDomains(path, core, auth)
		if err != nil {
			rt.Fatalf("migration open: %v", err)
		}
		rt.Cleanup(func() { _ = mdb.Close(ctx) })

		assertPropertyReset(rt, mdb, wantManual, wantOrder, wantOffsets)
	})
}

// seedPropertyStore applies a random operation sequence (auto saves, manual
// saves, offset writes, backoff rows) and returns the final offsets map.
func seedPropertyStore(rt *rapid.T, db *DB, quads []resetPropQuad, offsetPaths []string) map[string]int64 {
	ctx := context.Background()
	wantOffsets := map[string]int64{}
	n := rapid.IntRange(0, 25).Draw(rt, "ops")
	for i := range n {
		switch rapid.SampledFrom([]string{"auto", "manual", "offset", "backoff"}).Draw(rt, "op") {
		case "auto", "manual":
			q := rapid.SampledFrom(quads).Draw(rt, "quad")
			manual := rapid.Bool().Draw(rt, "manual")
			rec := &api.DownloadRecord{
				MediaType: q.mt, MediaID: q.mid, Language: q.lang, Variant: q.variant,
				ProviderName: api.ProviderNameOpenSubtitles,
				ReleaseName:  rapid.SampledFrom([]string{"", "Rel.A", "Rel.B"}).Draw(rt, "release"),
				Path:         fmt.Sprintf("/m/%s.%s.%d.srt", q.mid, q.lang, i),
				Score:        rapid.IntRange(0, 100).Draw(rt, "score"),
				Meta:         &api.DownloadMeta{Title: "T", VideoPath: "/m/" + q.mid + ".mkv", Manual: manual},
			}
			if err := db.SaveDownload(ctx, rec); err != nil {
				rt.Fatalf("SaveDownload: %v", err)
			}
		case "offset":
			p := rapid.SampledFrom(offsetPaths).Draw(rt, "path")
			off := rapid.Int64Range(-500, 500).Draw(rt, "off")
			if err := db.SetSyncOffset(ctx, p, off); err != nil {
				rt.Fatalf("SetSyncOffset: %v", err)
			}
			wantOffsets[p] = off
		case "backoff":
			q := rapid.SampledFrom(quads).Draw(rt, "bq")
			if err := db.RecordNoResult(ctx, q.mt, q.mid, q.lang, api.ProviderNameGestdown, resetBackoffParams); err != nil {
				rt.Fatalf("RecordNoResult: %v", err)
			}
		}
	}
	return wantOffsets
}

// manualRowIdentity is a stateRec's full identity minus the surrogate id and
// media_imported (both legitimately regenerated by the restore).
func manualRowIdentity(sr *stateRec) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%d|%d|%d|%v",
		sr.MediaType, sr.MediaID, sr.Language, sr.Variant, sr.Provider,
		sr.ReleaseName, sr.Path, sr.Score, sr.Season, sr.Episode, sr.Manual)
}

// snapshotManualRows walks the primary in id order and returns the manual
// rows' identity multiset and their old-ID-ordered identity sequence.
func snapshotManualRows(rt *rapid.T, db *DB) (map[string]int, []string) {
	want := map[string]int{}
	var order []string
	err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketSubtitleState)).ForEach(func(_, v []byte) error {
			var sr stateRec
			if derr := kv.Decode(v, &sr); derr != nil {
				return derr
			}
			if sr.Manual {
				id := manualRowIdentity(&sr)
				want[id]++
				order = append(order, id)
			}
			return nil
		})
	})
	if err != nil {
		rt.Fatalf("snapshot manual rows: %v", err)
	}
	return want, order
}

// assertPropertyReset checks the post-migration store against the snapshots:
// survivors, ordering, fresh sequential ids, offsets, counters, and the
// index-equals-rescan invariant.
func assertPropertyReset(rt *rapid.T, db *DB, wantManual map[string]int, wantOrder []string, wantOffsets map[string]int64) {
	ctx := context.Background()
	got := map[string]int{}
	var gotOrder []string
	var ids []int64
	err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketSubtitleState)).ForEach(func(_, v []byte) error {
			var sr stateRec
			if derr := kv.Decode(v, &sr); derr != nil {
				return derr
			}
			if !sr.Manual {
				rt.Errorf("non-manual row survived the reset: %+v", sr)
			}
			id := manualRowIdentity(&sr)
			got[id]++
			gotOrder = append(gotOrder, id)
			ids = append(ids, sr.ID)
			return nil
		})
	})
	if err != nil {
		rt.Fatalf("walk migrated rows: %v", err)
	}
	if len(got) != len(wantManual) || len(gotOrder) != len(wantOrder) {
		rt.Fatalf("survivors = %d rows (%d identities), want %d rows (%d identities)", len(gotOrder), len(got), len(wantOrder), len(wantManual))
	}
	for id, n := range wantManual {
		if got[id] != n {
			rt.Errorf("manual row %q survived %d times, want %d", id, got[id], n)
		}
	}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			rt.Errorf("restore order[%d] = %q, want %q (old-ID order)", i, gotOrder[i], wantOrder[i])
		}
		if ids[i] != int64(i+1) {
			rt.Errorf("restored id[%d] = %d, want fresh sequential %d", i, ids[i], i+1)
		}
	}

	for p, want := range wantOffsets {
		if gotOff, oerr := db.GetSyncOffset(ctx, p); oerr != nil || gotOff != want {
			rt.Errorf("GetSyncOffset(%s) = (%d, %v), want %d", p, gotOff, oerr, want)
		}
	}
	downloads, attempts, err := db.Stats(ctx)
	if err != nil {
		rt.Fatalf("Stats: %v", err)
	}
	if downloads != len(wantOrder) || attempts != 0 {
		rt.Errorf("Stats = (%d, %d), want (%d, 0)", downloads, attempts, len(wantOrder))
	}

	if err := db.db.View(func(tx *bolt.Tx) error {
		verifyStateIndexesEndToEnd(rt, tx)
		verifyCounters(rt, tx)
		return nil
	}); err != nil {
		rt.Fatalf("verify view: %v", err)
	}
}
