package statedb

import (
	"slices"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/status"
)

// schemaName enters every namespace's format digest.
const schemaName = "subflux"

// coreTables is namespace core (design section 4): tables 0x01 to 0x18 with
// their indexes, counter and cascades.
type coreTables struct {
	schema       *bolttable.Schema
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

// core is the production schema's namespace; Open's cfg names its schema.
var core = bolttable.Must(defineCore(bolttable.NewSchema(schemaName)))

// defineCore declares namespace core in s, so a test schema can add another
// namespace beside it.
func defineCore(s *bolttable.Schema) (*coreTables, error) {
	ns, err := s.Namespace("core")
	if err != nil {
		return nil, err
	}
	return defineCoreIn(s, ns)
}

// defineCoreIn declares core's tables in ns, s's namespace core, so a test
// can declare one more table there and bump core's digest.
func defineCoreIn(s *bolttable.Schema, ns *bolttable.Namespace) (*coreTables, error) {
	d := declarer{}
	silent := bolttable.TableOptions{Silent: true}
	live := bolttable.TableOptions{}
	c := &coreTables{
		schema:       s,
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
