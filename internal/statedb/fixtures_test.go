package statedb

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// fixture is one row of one table with its encoded key and value. name
// tells apart two fixtures of one table in the golden file; held lists the
// hash and handle texts its key or value must carry.
type fixture struct {
	put          func(*bolttable.Tx, *tables) error
	decode       func() error
	table, name  string
	held         []string
	key, value   []byte
	keyVersion   uint32
	valueVersion uint32
}

func fix[K, V any](t *testing.T, table string, pick func(*coreTables) *bolttable.Table[K, V], kc bolttable.KeyCodec[K], vc bolttable.Codec[V], k K, v V) fixture {
	t.Helper()
	return rowFixture(t, table, func(ts *tables) *bolttable.Table[K, V] { return pick(ts.core) }, kc, vc, k, v)
}

func authFix[K, V any](t *testing.T, table string, pick func(*authTables) *bolttable.Table[K, V], kc bolttable.KeyCodec[K], vc bolttable.Codec[V], k K, v V, held ...string) fixture {
	t.Helper()
	f := rowFixture(t, table, func(ts *tables) *bolttable.Table[K, V] { return pick(ts.auth) }, kc, vc, k, v)
	f.held = held
	return f
}

// rowFixture encodes k and v, and its decode checks that the value decodes
// and re-encodes to the same bytes.
func rowFixture[K, V any](t *testing.T, table string, pick func(*tables) *bolttable.Table[K, V], kc bolttable.KeyCodec[K], vc bolttable.Codec[V], k K, v V) fixture {
	t.Helper()
	val, err := vc.Append(nil, &v)
	if err != nil {
		t.Fatalf("%s: Append: %v", table, err)
	}
	return fixture{
		table:        table,
		name:         table,
		key:          kc.AppendKey(nil, k),
		value:        val,
		keyVersion:   kc.Version(),
		valueVersion: vc.Version(),
		put:          func(tx *bolttable.Tx, ts *tables) error { return pick(ts).Put(tx, k, &v) },
		decode: func() error {
			var got V
			if err := vc.Decode(val, &got); err != nil {
				return err
			}
			again, err := vc.Append(nil, &got)
			if err != nil {
				return err
			}
			if !bytes.Equal(again, val) {
				return fmt.Errorf("re-encoded %s, want %s", again, val)
			}
			return nil
		},
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
				Started: fixtureAt, Ended: fixtureAt.Add(time.Minute), Key: &jobKeyRow{Key: "k1", Fingerprint: sum32{7}}, Scope: "library", Source: "sonarr",
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

// The hash and handle texts the auth fixtures hold, as Reveal() returns
// them; none is a real credential.
const (
	fixturePasswordHash = "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHQ$aGFzaGhhc2g"
	fixtureSessionHash  = "session-hash-fixture"
	fixtureKeyHash      = "api-key-hash-fixture"
	fixtureTokenHash    = "one-time-hash-fixture"
)

// authFixtures holds one row per auth table, in prefix order, and one
// attribution per action.
func authFixtures(t *testing.T) []fixture {
	t.Helper()
	b64 := base64.StdEncoding.EncodeToString
	handle, credID, publicKey, aaguid, attestation := []byte("handle-1"), []byte("credential-1"), []byte("public-key-1"), []byte("aaguid-1"), []byte("attestation-1")
	session := access.SessionRef{UserID: 3, ID: "0123456789abcdef"}
	passkey := access.PasskeyRef{UserID: 3, ID: 4}
	discoverable := true
	fs := []fixture{
		authFix(t, "users", func(a *authTables) *bolttable.Table[int64, userRow] { return a.users }, idKeys, userCodec,
			3, userRow{
				CreatedAt: fixtureAt, UpdatedAt: fixtureAt.Add(time.Second), Username: "ada", Email: "ada@example.com", DisplayName: "Ada",
				PasswordHash: fixturePasswordHash, Role: string(access.RoleAdmin), OIDCIssuer: "https://idp.example", OIDCSubject: "sub-1",
				WebAuthnHandle: handle, Enabled: true,
			}, fixturePasswordHash, b64(handle)),
		authFix(t, "sessions", func(a *authTables) *bolttable.Table[access.SessionRef, sessionRow] { return a.sessions }, sessionKeys, sessionCodec,
			session, sessionRow{
				CreatedAt: fixtureAt, AuthTime: fixtureAt.Add(time.Second), TokenHash: fixtureSessionHash, AuthMethod: string(access.MethodPasskey),
				Kind: string(access.SessionRecovery), UserAgent: "Firefox", Label: "laptop", IssuedByKey: 5,
				IdleCap: int64(30 * time.Minute), AbsoluteCap: int64(12 * time.Hour),
			}, fixtureSessionHash),
		authFix(t, "session_activity", func(a *authTables) *bolttable.Table[access.SessionRef, activityRow] { return a.activity }, sessionKeys, activityCodec,
			session, activityRow{LastActivity: fixtureAt.Add(time.Minute), LastIP: "2001:db8::1"}),
		authFix(t, "passkeys", func(a *authTables) *bolttable.Table[access.PasskeyRef, passkeyRow] { return a.passkeys }, passkeyKeys, passkeyCodec,
			passkey, passkeyRow{
				CreatedAt: fixtureAt, Discoverable: &discoverable, AttestationType: "none", AttestationFormat: "packed", RPID: "subflux.example",
				Transport: "internal", Name: "phone", Attachment: string(access.AttachmentPlatform),
				CredentialID: credID, PublicKey: publicKey, AAGUID: aaguid, RawAttestation: attestation,
			}, b64(credID), b64(publicKey), b64(attestation)),
		authFix(t, "passkey_use", func(a *authTables) *bolttable.Table[access.PasskeyRef, passkeyUseRow] { return a.passkeyUse }, passkeyKeys, passkeyUseCodec,
			passkey, passkeyUseRow{At: fixtureAt.Add(time.Hour), SignCount: 7, RawFlags: 0x1D, CloneWarning: true}),
		authFix(t, "keys", func(a *authTables) *bolttable.Table[int64, keyRow] { return a.keys }, idKeys, apiKeyCodec,
			5, keyRow{
				CreatedAt: fixtureAt, ExpiresAt: fixtureAt.Add(24 * time.Hour), KeyHash: fixtureKeyHash, KeyPrefix: "sfx_", KeySuffix: "a1b2",
				Label: "backup", Scopes: []string{"read", "write"}, LineageIDs: []int64{1, 2, 5}, UserID: 3,
			}, fixtureKeyHash),
		authFix(t, "key_use", func(a *authTables) *bolttable.Table[int64, keyUseRow] { return a.keyUse }, idKeys, keyUseCodec,
			5, keyUseRow{LastUsedAt: fixtureAt.Add(2 * time.Hour)}),
		authFix(t, "tokens", func(a *authTables) *bolttable.Table[string, tokenRow] { return a.tokens }, textKeys, tokenCodec,
			fixtureTokenHash, tokenRow{
				CreatedAt: fixtureAt, ExpiresAt: fixtureAt.Add(time.Hour), Purpose: string(access.PurposeInvite), Role: string(access.RoleUser),
				IssuedByKey: 5,
			}, fixtureTokenHash),
		authFix(t, "failures", func(a *authTables) *bolttable.Table[account.FailureRef, failureRow] { return a.failures }, failureKeys, failureCodec,
			account.FailureRef{Name: "name-hmac", Method: access.MethodPassword, Trusted: true},
			failureRow{LockedAt: fixtureAt, Count: 10, Locked: true, Unreported: true}),
		authFix(t, "secrets", func(a *authTables) *bolttable.Table[constKey, secretsRow] { return a.secrets }, constKeys, secretsCodec,
			constKey{}, secretsRow{FailureKey: sum32{8}, DeviceKey: sum32{9}}),
	}
	for i, action := range []access.AttributionAction{
		access.AttributionCreated, access.AttributionInviteAccepted, access.AttributionRecoveryStarted,
		access.AttributionRecovered, access.AttributionEnabled, access.AttributionDisabled,
	} {
		f := authFix(t, "key_attributions", func(a *authTables) *bolttable.Table[int64, attributionRow] { return a.attributions }, idKeys, attributionCodec,
			int64(i+1), attributionRow{At: fixtureAt, Action: string(action), KeyLineage: []int64{1, 2, 5}, UserID: 3})
		f.name = "key_attributions." + string(action)
		fs = append(fs, f)
	}
	return append(fs, authFix(t, "auth_method_state", func(a *authTables) *bolttable.Table[constKey, methodStateRow] { return a.methodState }, constKeys, methodStateCodec,
		constKey{}, methodStateRow{
			PasswordLogin: true, OIDCIssuer: "https://idp.example", RPID: "subflux.example",
			SignIn: signInKeyRow{
				OIDCClientID: "subflux", OIDCRedirectURL: "https://subflux.example/auth/oidc/callback", OIDCClientSecret: sum32{10},
				OIDCAutoProvision: true, DisableAuth: true,
				OIDCAllowedGroups: setDigestRow{Count: 2, SHA256: sum32{11}}, PasskeyOrigins: setDigestRow{Count: 1, SHA256: sum32{12}},
			},
		}))
}

// openSchema opens a fresh file under a fresh copy of the production schema,
// with no statedb hooks.
func openSchema(t *testing.T) (*bolttable.DB, *tables) {
	t.Helper()
	ts, err := defineSchema(bolttable.NewSchema(schemaName))
	if err != nil {
		t.Fatalf("defineSchema: %v", err)
	}
	db, err := bolttable.Open(filepath.Join(t.TempDir(), "subflux.bolt"), ts.schema, bolttable.Options{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, ts
}
