package statedb

import (
	"slices"

	"github.com/cplieger/access"
	"github.com/cplieger/access/account"
	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/status"
)

// schemaName enters every namespace's format digest.
const schemaName = "subflux"

// tables is the schema Open opens: namespaces core and auth. A test opening
// a file under core alone leaves auth nil.
type tables struct {
	schema *bolttable.Schema
	core   *coreTables
	auth   *authTables
}

// coreTables is namespace core (design section 4): tables 0x01 to 0x18 with
// their indexes, counter and cascades.
type coreTables struct {
	activation   *bolttable.Table[constKey, activationRow]
	passes       *bolttable.Table[media.SourceID, passRow]
	cursors      *bolttable.Table[media.SourceID, cursorRow]
	roots        *bolttable.Table[rootRef, rootRow]
	sightings    *bolttable.Table[itemRef, sightingRow]
	stamps       *bolttable.Table[itemRef, stampRow]
	overlaps     *bolttable.Table[itemRef, overlapRow]
	items        *bolttable.Table[media.Key, itemRow]
	instances    *bolttable.Table[InstanceID, instanceRow]
	coverage     *bolttable.Table[media.Key, coverageRow]
	locks        *bolttable.Table[lockKey, lockRow]
	files        *bolttable.Table[fileKey, fileRow]
	baselines    *bolttable.Table[fileKey, baselineRow]
	ordinals     *bolttable.Table[OrdinalKey, ordinalRow]
	intents      *bolttable.Table[fileKey, intentRow]
	local        *bolttable.Table[InstanceID, localRow]
	embedded     *bolttable.Table[InstanceID, embeddedRow]
	downloads    *bolttable.Table[uint64, downloadRow]
	work         *bolttable.Table[InstanceID, workRow]
	jobs         *bolttable.Table[status.JobID, jobRow]
	credentials  *bolttable.Table[string, credentialRow]
	noResults    *bolttable.Table[noResultKey, noResultRow]
	rootEvidence *bolttable.Table[rootRef, evidenceRow]
	counts       *bolttable.Table[media.RootKey, countsRow]

	unidentified *bolttable.Index[itemRef, sightingRow]
	byPath       *bolttable.Index[InstanceID, instanceRow]
	orphaned     *bolttable.Index[InstanceID, instanceRow]
	itemHistory  *bolttable.Index[uint64, downloadRow]
	rootHistory  *bolttable.Index[uint64, downloadRow]
	workOrder    *bolttable.Index[InstanceID, workRow]
	jobByKey     *bolttable.Index[status.JobID, jobRow]
	jobsByOwner  *bolttable.Index[status.JobID, jobRow]
	jobsEnded    *bolttable.Index[status.JobID, jobRow]
	ownerEnded   *bolttable.Index[status.JobID, jobRow]
	userEnded    *bolttable.Index[status.JobID, jobRow]
	owned        *bolttable.Counter[status.JobID, jobRow]
}

// The core table prefixes (design section 4).
const (
	pActivation   byte = 0x01
	pPasses       byte = 0x02
	pCursors      byte = 0x03
	pRoots        byte = 0x04
	pSightings    byte = 0x05
	pStamps       byte = 0x06
	pOverlaps     byte = 0x07
	pItems        byte = 0x08
	pInstances    byte = 0x09
	pCoverage     byte = 0x0A
	pLocks        byte = 0x0B
	pFiles        byte = 0x0C
	pBaselines    byte = 0x0D
	pOrdinals     byte = 0x0E
	pIntents      byte = 0x0F
	pLocal        byte = 0x10
	pEmbedded     byte = 0x11
	pDownloads    byte = 0x12
	pWork         byte = 0x13
	pJobs         byte = 0x14
	pCredentials  byte = 0x15
	pNoResults    byte = 0x16
	pRootEvidence byte = 0x17
	pCounts       byte = 0x18
)

// The value codecs' own versions; a row struct change bumps its constant.
var (
	activationCodec  = rowCodec[activationRow, *activationRow]{version: 1}
	passCodec        = rowCodec[passRow, *passRow]{version: 1}
	cursorCodec      = rowCodec[cursorRow, *cursorRow]{version: 1}
	rootCodec        = rowCodec[rootRow, *rootRow]{version: taggedVersion(1)}
	sightingCodec    = rowCodec[sightingRow, *sightingRow]{version: taggedVersion(1)}
	stampCodec       = rowCodec[stampRow, *stampRow]{version: 1}
	overlapCodec     = rowCodec[overlapRow, *overlapRow]{version: 1}
	itemCodec        = rowCodec[itemRow, *itemRow]{version: 1}
	instanceCodec    = rowCodec[instanceRow, *instanceRow]{version: 1}
	coverageCodec    = rowCodec[coverageRow, *coverageRow]{version: taggedVersion(2)}
	lockCodec        = rowCodec[lockRow, *lockRow]{version: 1}
	fileCodec        = rowCodec[fileRow, *fileRow]{version: timedVersion(1)}
	baselineCodec    = rowCodec[baselineRow, *baselineRow]{version: timedVersion(1)}
	ordinalCodec     = rowCodec[ordinalRow, *ordinalRow]{version: 1}
	intentCodec      = rowCodec[intentRow, *intentRow]{version: timedVersion(1)}
	localCodec       = rowCodec[localRow, *localRow]{version: taggedVersion(1)}
	embeddedCodec    = rowCodec[embeddedRow, *embeddedRow]{version: taggedVersion(1)}
	downloadCodec    = rowCodec[downloadRow, *downloadRow]{version: taggedVersion(1)}
	workCodec        = rowCodec[workRow, *workRow]{version: 1}
	jobCodec         = rowCodec[jobRow, *jobRow]{version: 2}
	credentialCodec  = rowCodec[credentialRow, *credentialRow]{version: 1}
	noResultCodec    = rowCodec[noResultRow, *noResultRow]{version: 1}
	evidenceCodec    = rowCodec[evidenceRow, *evidenceRow]{version: 1}
	countsCodec      = rowCodec[countsRow, *countsRow]{version: taggedVersion(1)}
	downloadSeqCodec bolttable.Uint64Key
)

// production is the schema Open opens.
var production = bolttable.Must(defineSchema(bolttable.NewSchema(schemaName)))

// defineSchema declares namespaces core and auth in s.
func defineSchema(s *bolttable.Schema) (*tables, error) {
	c, err := defineCore(s)
	if err != nil {
		return nil, err
	}
	a, err := defineAuth(s)
	if err != nil {
		return nil, err
	}
	return &tables{schema: s, core: c, auth: a}, nil
}

// defineCore declares namespace core in s.
func defineCore(s *bolttable.Schema) (*coreTables, error) {
	ns, err := s.Namespace("core")
	if err != nil {
		return nil, err
	}
	return defineCoreIn(ns)
}

// defineAuth declares namespace auth in s.
func defineAuth(s *bolttable.Schema) (*authTables, error) {
	ns, err := s.Namespace("auth")
	if err != nil {
		return nil, err
	}
	return defineAuthIn(ns)
}

// defineCoreIn declares core's tables in ns, so a test can declare one more
// table there and bump core's digest.
func defineCoreIn(ns *bolttable.Namespace) (*coreTables, error) {
	d := declarer{}
	silent := bolttable.TableOptions{Silent: true}
	live := bolttable.TableOptions{}
	c := &coreTables{
		activation:   define(&d, ns, pActivation, "activation", constKeys, activationCodec, silent),
		passes:       define(&d, ns, pPasses, "passes", sourceKeys, passCodec, silent),
		cursors:      define(&d, ns, pCursors, "cursors", sourceKeys, cursorCodec, silent),
		roots:        define(&d, ns, pRoots, "roots", rootRefKeys, rootCodec, live),
		sightings:    define(&d, ns, pSightings, "sightings", itemRefKeys, sightingCodec, live),
		stamps:       define(&d, ns, pStamps, "stamps", itemRefKeys, stampCodec, silent),
		overlaps:     define(&d, ns, pOverlaps, "overlaps", itemRefKeys, overlapCodec, silent),
		items:        define(&d, ns, pItems, "items", mediaKeys, itemCodec, live),
		instances:    define(&d, ns, pInstances, "instances", instKeys, instanceCodec, live),
		coverage:     define(&d, ns, pCoverage, "coverage", mediaKeys, coverageCodec, live),
		locks:        define(&d, ns, pLocks, "locks", lockKeys, lockCodec, live),
		files:        define(&d, ns, pFiles, "files", fileKeys, fileCodec, live),
		baselines:    define(&d, ns, pBaselines, "baselines", fileKeys, baselineCodec, silent),
		ordinals:     define(&d, ns, pOrdinals, "ordinals", ordinalKeys, ordinalCodec, silent),
		intents:      define(&d, ns, pIntents, "intents", fileKeys, intentCodec, silent),
		local:        define(&d, ns, pLocal, "local", instKeys, localCodec, live),
		embedded:     define(&d, ns, pEmbedded, "embedded", instKeys, embeddedCodec, live),
		downloads:    define(&d, ns, pDownloads, "downloads", downloadSeqCodec, downloadCodec, live),
		work:         define(&d, ns, pWork, "work", instKeys, workCodec, silent),
		jobs:         define(&d, ns, pJobs, "jobs", jobKeys, jobCodec, silent),
		credentials:  define(&d, ns, pCredentials, "credentials", textKeys, credentialCodec, silent),
		noResults:    define(&d, ns, pNoResults, "no_results", noResultKeys, noResultCodec, silent),
		rootEvidence: define(&d, ns, pRootEvidence, "root_evidence", rootRefKeys, evidenceCodec, silent),
		counts:       define(&d, ns, pCounts, "counts", rootKeys, countsCodec, silent),
	}
	plain := bolttable.IndexOptions{Version: 1}
	unique := bolttable.IndexOptions{Version: 1, Unique: true}
	c.unidentified = index(&d, c.sightings, "unidentified", plain, unidentifiedEntries)
	c.byPath = index(&d, c.instances, "path", unique, pathEntries)
	c.orphaned = index(&d, c.instances, "orphaned", plain, orphanedEntries)
	c.itemHistory = index(&d, c.downloads, "item", plain, itemHistoryEntries)
	c.rootHistory = index(&d, c.downloads, "root", plain, rootHistoryEntries)
	c.workOrder = index(&d, c.work, "order", plain, workOrderEntries)
	c.jobByKey = index(&d, c.jobs, "key", unique, jobKeyEntries)
	c.jobsByOwner = index(&d, c.jobs, "owner", plain, jobOwnerEntries)
	c.jobsEnded = index(&d, c.jobs, "ended", plain, jobEndedEntries)
	c.ownerEnded = index(&d, c.jobs, "owner_ended", plain, ownerEndedEntries)
	c.userEnded = index(&d, c.jobs, "user_ended", plain, userEndedEntries)
	if d.err == nil {
		c.owned, d.err = bolttable.DefineCounter(c.jobs, "owned", 1, ownedWeight)
	}
	instPrefix := func(id InstanceID) []byte { return appendUint64(nil, uint64(id)) }
	cascade(&d, c.instances, c.files, instPrefix)
	cascade(&d, c.instances, c.baselines, instPrefix)
	cascade(&d, c.instances, c.ordinals, instPrefix)
	cascade(&d, c.instances, c.local, instPrefix)
	cascade(&d, c.instances, c.embedded, instPrefix)
	cascade(&d, c.instances, c.work, instPrefix)
	cascade(&d, c.items, c.noResults, media.Key.Bytes)
	cascade(&d, c.roots, c.rootEvidence, func(rr rootRef) []byte { return rootRefKeys.AppendKey(nil, rr) })
	if d.err != nil {
		return nil, d.err
	}
	return c, nil
}

// declarer keeps the first declaration error, so defineCore reads as the
// table of section 4 and checks once.
type declarer struct{ err error }

func define[K, V any](d *declarer, ns *bolttable.Namespace, prefix byte, name string, k bolttable.KeyCodec[K], v bolttable.Codec[V], o bolttable.TableOptions) *bolttable.Table[K, V] {
	if d.err != nil {
		return nil
	}
	t, err := bolttable.Define(ns, prefix, name, k, v, o)
	d.err = err
	return t
}

func index[K, V any](d *declarer, t *bolttable.Table[K, V], name string, o bolttable.IndexOptions, entries func(K, *V) []bolttable.Entry) *bolttable.Index[K, V] {
	if d.err != nil {
		return nil
	}
	ix, err := bolttable.DefineIndex(t, name, o, entries)
	d.err = err
	return ix
}

func cascade[PK, PV, CK, CV any](d *declarer, parent *bolttable.Table[PK, PV], child *bolttable.Table[CK, CV], prefix func(PK) []byte) {
	if d.err == nil {
		d.err = bolttable.CascadePrefix(parent, child, 1, prefix)
	}
}

func one(key []byte) []bolttable.Entry { return []bolttable.Entry{{Key: key}} }

// unidentifiedEntries keys each entry by its sighting's key, so Unidentified
// pages resume exactly.
func unidentifiedEntries(ref itemRef, v *sightingRow) []bolttable.Entry {
	if v.Key != nil {
		return nil
	}
	return one(itemRefKeys.AppendKey(nil, ref))
}

func pathEntries(_ InstanceID, v *instanceRow) []bolttable.Entry {
	return one(appendText(nil, v.VideoPath))
}

func orphanedEntries(_ InstanceID, v *instanceRow) []bolttable.Entry {
	if v.Owner != "" {
		return nil
	}
	return one(appendTime(nil, v.Orphaned))
}

// The history indexes order a key's or root's downloads newest first, by
// the inverted sequence.
func itemHistoryEntries(seq uint64, v *downloadRow) []bolttable.Entry {
	es := make([]bolttable.Entry, 0, len(v.Items))
	for _, k := range v.Items {
		es = append(es, bolttable.Entry{Key: appendUint64(append([]byte(nil), k.k.Bytes()...), ^seq)})
	}
	return es
}

func rootHistoryEntries(seq uint64, v *downloadRow) []bolttable.Entry {
	roots := make([]media.RootKey, 0, len(v.Items))
	for _, k := range v.Items {
		if r := k.k.Root(); !slices.Contains(roots, r) {
			roots = append(roots, r)
		}
	}
	es := make([]bolttable.Entry, 0, len(roots))
	for _, r := range roots {
		es = append(es, bolttable.Entry{Key: appendUint64(append([]byte(nil), r.Bytes()...), ^seq)})
	}
	return es
}

// workOrderEntries leads with the cause class, Item rows first (CR-SE-5
// (b)), then the enqueue sequence, and projects what NextWork serves from.
func workOrderEntries(_ InstanceID, v *workRow) []bolttable.Entry {
	key := appendUint64([]byte{uint8(Item) - v.Cause}, v.Seq)
	return []bolttable.Entry{{Key: key, Value: appendWorkProjection(nil, v)}}
}

func jobKeyEntries(_ status.JobID, v *jobRow) []bolttable.Entry {
	if v.Key == nil {
		return nil
	}
	return one(appendText(ownerBytes(v.Owner), v.Key.Key))
}

func jobOwnerEntries(_ status.JobID, v *jobRow) []bolttable.Entry {
	return one(ownerBytes(v.Owner))
}

func jobEndedEntries(_ status.JobID, v *jobRow) []bolttable.Entry {
	if v.State != uint8(status.Done) {
		return nil
	}
	return one(appendTime(nil, v.Ended))
}

func ownerEndedEntries(_ status.JobID, v *jobRow) []bolttable.Entry {
	if v.State != uint8(status.Done) {
		return nil
	}
	return one(appendTime(ownerBytes(v.Owner), v.Ended))
}

func userEndedEntries(_ status.JobID, v *jobRow) []bolttable.Entry {
	if v.State != uint8(status.Done) || !userOwned(v.Owner) {
		return nil
	}
	return one(appendTime(nil, v.Ended))
}

func ownedWeight(_ status.JobID, v *jobRow) int64 {
	if userOwned(v.Owner) {
		return 1
	}
	return 0
}

// authTables is namespace auth (design sections 4 and 9): tables 0x20 to
// 0x2B, the account store v7's account.Store runs on.
type authTables struct {
	users          *bolttable.Table[int64, userRow]
	sessions       *bolttable.Table[access.SessionRef, sessionRow]
	activity       *bolttable.Table[access.SessionRef, activityRow]
	passkeys       *bolttable.Table[access.PasskeyRef, passkeyRow]
	passkeyUse     *bolttable.Table[access.PasskeyRef, passkeyUseRow]
	keys           *bolttable.Table[int64, keyRow]
	keyUse         *bolttable.Table[int64, keyUseRow]
	tokens         *bolttable.Table[string, tokenRow]
	failures       *bolttable.Table[account.FailureRef, failureRow]
	secrets        *bolttable.Table[constKey, secretsRow]
	attributions   *bolttable.Table[int64, attributionRow]
	methodState    *bolttable.Table[constKey, methodStateRow]
	byUsername     *bolttable.Index[int64, userRow]
	byOIDC         *bolttable.Index[int64, userRow]
	byHandle       *bolttable.Index[int64, userRow]
	userCount      *bolttable.Counter[int64, userRow]
	sessionByHash  *bolttable.Index[access.SessionRef, sessionRow]
	sessionByID    *bolttable.Index[access.SessionRef, sessionRow]
	byCredential   *bolttable.Index[access.PasskeyRef, passkeyRow]
	keyByHash      *bolttable.Index[int64, keyRow]
	keysByUser     *bolttable.Index[int64, keyRow]
	tokensBySub    *bolttable.Index[string, tokenRow]
	tokensByExpiry *bolttable.Index[string, tokenRow]
	tokensByIssuer *bolttable.Index[string, tokenRow]
	unreported     *bolttable.Index[account.FailureRef, failureRow]
	attrsByUser    *bolttable.Index[int64, attributionRow]
}

// The auth table prefixes (design section 4).
const (
	pUsers        byte = 0x20
	pSessions     byte = 0x21
	pActivity     byte = 0x22
	pPasskeys     byte = 0x23
	pPasskeyUse   byte = 0x24
	pKeys         byte = 0x25
	pKeyUse       byte = 0x26
	pTokens       byte = 0x27
	pFailures     byte = 0x28
	pSecrets      byte = 0x29
	pAttributions byte = 0x2A
	pMethodState  byte = 0x2B
)

// The unique indexes whose conflict answers an account sentinel (rule 4).
const (
	ixUsername   = "username"
	ixOIDC       = "oidc"
	ixCredential = "credential"
)

// The auth value codecs' own versions; auth holds no tag or timing data.
var (
	userCodec        = rowCodec[userRow, *userRow]{version: 1}
	sessionCodec     = rowCodec[sessionRow, *sessionRow]{version: 1}
	activityCodec    = rowCodec[activityRow, *activityRow]{version: 1}
	passkeyCodec     = rowCodec[passkeyRow, *passkeyRow]{version: 1}
	passkeyUseCodec  = rowCodec[passkeyUseRow, *passkeyUseRow]{version: 1}
	apiKeyCodec      = rowCodec[keyRow, *keyRow]{version: 1}
	keyUseCodec      = rowCodec[keyUseRow, *keyUseRow]{version: 1}
	tokenCodec       = rowCodec[tokenRow, *tokenRow]{version: 1}
	failureCodec     = rowCodec[failureRow, *failureRow]{version: 1}
	secretsCodec     = rowCodec[secretsRow, *secretsRow]{version: 1}
	attributionCodec = rowCodec[attributionRow, *attributionRow]{version: 1}
	methodStateCodec = rowCodec[methodStateRow, *methodStateRow]{version: 1}
)

// defineAuthIn declares auth's tables in ns, so a test can declare one more
// table there and bump auth's digest.
func defineAuthIn(ns *bolttable.Namespace) (*authTables, error) {
	d := declarer{}
	silent := bolttable.TableOptions{Silent: true}
	live := bolttable.TableOptions{}
	a := &authTables{
		users:        define(&d, ns, pUsers, "users", idKeys, userCodec, live),
		sessions:     define(&d, ns, pSessions, "sessions", sessionKeys, sessionCodec, live),
		activity:     define(&d, ns, pActivity, "session_activity", sessionKeys, activityCodec, silent),
		passkeys:     define(&d, ns, pPasskeys, "passkeys", passkeyKeys, passkeyCodec, live),
		passkeyUse:   define(&d, ns, pPasskeyUse, "passkey_use", passkeyKeys, passkeyUseCodec, silent),
		keys:         define(&d, ns, pKeys, "keys", idKeys, apiKeyCodec, live),
		keyUse:       define(&d, ns, pKeyUse, "key_use", idKeys, keyUseCodec, silent),
		tokens:       define(&d, ns, pTokens, "tokens", textKeys, tokenCodec, silent),
		failures:     define(&d, ns, pFailures, "failures", failureKeys, failureCodec, silent),
		secrets:      define(&d, ns, pSecrets, "secrets", constKeys, secretsCodec, silent),
		attributions: define(&d, ns, pAttributions, "key_attributions", idKeys, attributionCodec, live),
		methodState:  define(&d, ns, pMethodState, "auth_method_state", constKeys, methodStateCodec, silent),
	}
	plain := bolttable.IndexOptions{Version: 1}
	unique := bolttable.IndexOptions{Version: 1, Unique: true}
	a.byUsername = index(&d, a.users, ixUsername, unique, usernameEntries)
	a.byOIDC = index(&d, a.users, ixOIDC, unique, oidcEntries)
	a.byHandle = index(&d, a.users, "handle", unique, handleEntries)
	a.sessionByHash = index(&d, a.sessions, "session_hash", unique, sessionHashEntries)
	a.sessionByID = index(&d, a.sessions, "session_id", unique, sessionIDEntries)
	a.byCredential = index(&d, a.passkeys, ixCredential, unique, credentialEntries)
	a.keyByHash = index(&d, a.keys, "key_hash", unique, keyHashEntries)
	a.keysByUser = index(&d, a.keys, "key_user", plain, keyUserEntries)
	a.tokensBySub = index(&d, a.tokens, "token_subject", plain, tokenSubjectEntries)
	a.tokensByExpiry = index(&d, a.tokens, "token_expires", plain, tokenExpiryEntries)
	a.tokensByIssuer = index(&d, a.tokens, "token_issued_by", plain, tokenIssuerEntries)
	a.unreported = index(&d, a.failures, "unreported", plain, unreportedEntries)
	a.attrsByUser = index(&d, a.attributions, "attribution_user", plain, attributionUserEntries)
	if d.err == nil {
		a.userCount, d.err = bolttable.DefineCounter(a.users, "user_count", 1, func(int64, *userRow) int64 { return 1 })
	}
	byID := func(id int64) []byte { return idKeys.AppendKey(nil, id) }
	cascade(&d, a.users, a.sessions, byID)
	cascade(&d, a.users, a.passkeys, byID)
	cascadeIndex(&d, a.users, a.keysByUser, byID)
	cascadeIndex(&d, a.users, a.tokensBySub, byID)
	cascadeIndex(&d, a.users, a.attrsByUser, byID)
	cascade(&d, a.sessions, a.activity, func(r access.SessionRef) []byte { return sessionKeys.AppendKey(nil, r) })
	cascade(&d, a.passkeys, a.passkeyUse, func(r access.PasskeyRef) []byte { return passkeyKeys.AppendKey(nil, r) })
	cascade(&d, a.keys, a.keyUse, byID)
	if d.err != nil {
		return nil, d.err
	}
	return a, nil
}

func cascadeIndex[PK, PV, CK, CV any](d *declarer, parent *bolttable.Table[PK, PV], via *bolttable.Index[CK, CV], prefix func(PK) []byte) {
	if d.err == nil {
		d.err = bolttable.CascadeIndex(parent, via, 1, prefix)
	}
}

func usernameEntries(_ int64, v *userRow) []bolttable.Entry { return one(appendText(nil, v.Username)) }

// oidcEntries is sparse: an unlinked user has no entry.
func oidcEntries(_ int64, v *userRow) []bolttable.Entry {
	if v.OIDCIssuer == "" && v.OIDCSubject == "" {
		return nil
	}
	return one(appendText(appendText(nil, v.OIDCIssuer), v.OIDCSubject))
}

// handleEntries is sparse: a user with no WebAuthn handle has no entry.
func handleEntries(_ int64, v *userRow) []bolttable.Entry {
	if len(v.WebAuthnHandle) == 0 {
		return nil
	}
	return one(appendText(nil, string(v.WebAuthnHandle)))
}

func sessionHashEntries(_ access.SessionRef, v *sessionRow) []bolttable.Entry {
	return one(appendText(nil, v.TokenHash))
}

// sessionIDEntries makes a session ID unique across users (rule 4).
func sessionIDEntries(r access.SessionRef, _ *sessionRow) []bolttable.Entry {
	return one(appendText(nil, r.ID))
}

func credentialEntries(_ access.PasskeyRef, v *passkeyRow) []bolttable.Entry {
	return one(appendText(nil, string(v.CredentialID)))
}

func keyHashEntries(_ int64, v *keyRow) []bolttable.Entry { return one(appendText(nil, v.KeyHash)) }

func keyUserEntries(_ int64, v *keyRow) []bolttable.Entry {
	return one(idKeys.AppendKey(nil, v.UserID))
}

func tokenSubjectEntries(_ string, v *tokenRow) []bolttable.Entry {
	return one(appendText(idKeys.AppendKey(nil, v.Subject), v.Purpose))
}

func tokenExpiryEntries(_ string, v *tokenRow) []bolttable.Entry {
	return one(appendTime(nil, v.ExpiresAt))
}

// tokenIssuerEntries is sparse: a token no key issued has no entry, so
// DeleteOneTimeTokensIssuedBy([0]) matches nothing.
func tokenIssuerEntries(_ string, v *tokenRow) []bolttable.Entry {
	if v.IssuedByKey == 0 {
		return nil
	}
	return one(idKeys.AppendKey(nil, v.IssuedByKey))
}

func unreportedEntries(f account.FailureRef, v *failureRow) []bolttable.Entry {
	if !v.Unreported {
		return nil
	}
	return one(failureKeys.AppendKey(nil, f))
}

func attributionUserEntries(id int64, v *attributionRow) []bolttable.Entry {
	return one(idKeys.AppendKey(idKeys.AppendKey(nil, v.UserID), id))
}
