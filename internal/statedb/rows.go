package statedb

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// Row values (design section 4) are private structs under JSON with every
// enum a uint8 converted at the row boundary, so no other package's text
// codec or field name shapes stored bytes.

// checker is a row whose decode also checks its enums against their sets.
type checker interface{ check() error }

// rowCodec is a bolttable.Codec over encoding/json that refuses unknown
// fields, trailing data and an enum outside its set, so a stored value
// decodes to exactly one row or fails, which bolttable reports as
// ErrCorrupt. Append runs the same enum check, so a write never stores a
// row its own decode would refuse.
type rowCodec[V any, P interface {
	*V
	checker
}] struct{ version uint32 }

func (c rowCodec[V, P]) Version() uint32 { return c.version }

func (rowCodec[V, P]) Append(dst []byte, v *V) ([]byte, error) {
	if err := P(v).check(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(dst, b...), nil
}

func (rowCodec[V, P]) Decode(b []byte, v *V) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errRowTrailing
	}
	return P(v).check()
}

var (
	errRowEnum     = errors.New("statedb: stored enum outside its set")
	errRowTrailing = errors.New("statedb: stored row has trailing data")
)

func enumIn(what string, v, lo, hi uint8) error {
	if v < lo || v > hi {
		return fmt.Errorf("%w: %s %d", errRowEnum, what, v)
	}
	return nil
}

func checkVariant(v uint8) error {
	return enumIn("variant", v, uint8(language.Standard), uint8(language.Forced))
}

func checkFlags(f uint8) error {
	if f&^flagsAll != 0 {
		return fmt.Errorf("%w: flags %#x", errRowEnum, f)
	}
	return nil
}

// sum32 is a SHA-256 or digest in a row, stored as hex text rather than
// encoding/json's array of 32 numbers.
type sum32 [32]byte

func (s sum32) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, s[:]), nil }

func (s *sum32) UnmarshalText(b []byte) error { return unhexInto(s[:], b) }

// id16 is a run id in a row, stored as hex text.
type id16 [16]byte

func (s id16) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, s[:]), nil }

func (s *id16) UnmarshalText(b []byte) error { return unhexInto(s[:], b) }

var errRowHex = errors.New("statedb: stored hex has the wrong length")

func unhexInto(dst, b []byte) error {
	if hex.DecodedLen(len(b)) != len(dst) {
		return errRowHex
	}
	_, err := hex.Decode(dst, b)
	return err
}

// rowKey is a media.Key in a row value, stored as its fixed-width bytes.
type rowKey struct{ k media.Key }

func (r rowKey) MarshalText() ([]byte, error) { return hex.AppendEncode(nil, r.k.Bytes()), nil }

func (r *rowKey) UnmarshalText(b []byte) error {
	raw, err := hex.AppendDecode(nil, b)
	if err != nil {
		return err
	}
	k, err := media.KeyFromBytes(raw)
	if err != nil {
		return err
	}
	r.k = k
	return nil
}

type externalRow struct {
	TVDB uint64 `json:"tvdb"`
	TMDB uint64 `json:"tmdb"`
	IMDb uint64 `json:"imdb"`
}

type numberingRow struct {
	Season  int   `json:"season"`
	Episode int   `json:"episode"`
	Scheme  uint8 `json:"scheme"`
}

func (n numberingRow) check() error {
	return enumIn("scheme", n.Scheme, uint8(media.None), uint8(media.IMDb))
}

type nativeRefRow struct {
	ID   mediasource.NativeID `json:"id"`
	Kind uint8                `json:"kind"`
}

func (n nativeRefRow) check() error {
	return enumIn("root kind", n.Kind, uint8(media.Episode), uint8(media.Movie))
}

// 0x01 activation: the sorted sources, the policy digest and the sorted
// ignored codecs Activate last stored, and the revision of that change; a
// restart's first Activate finds removed sources here.
type activationRow struct {
	Sources   []media.SourceID `json:"sources"`
	Codecs    []string         `json:"codecs"`
	ConfigRev uint64           `json:"config_rev"`
	Policy    sum32            `json:"policy"`
}

func (*activationRow) check() error { return nil }

// 0x02 passes: the open pass; Roots null is a whole-source pass, and any
// list, [] included, a change batch.
type passRow struct {
	Roots []nativeRefRow `json:"roots"`
	Pass  uint64         `json:"pass"`
}

func (r *passRow) check() error {
	for _, n := range r.Roots {
		if err := n.check(); err != nil {
			return err
		}
	}
	return nil
}

// 0x03 cursors: mediasource.Cursor's MarshalBinary bytes.
type cursorRow struct {
	Cursor []byte `json:"cursor"`
}

func (*cursorRow) check() error { return nil }

// 0x04 roots: mediasource.Root less Native, which the key holds, and
// Evidence, which table 0x17 holds.
type rootRow struct {
	Language  rowTag      `json:"language"`
	Order     orderRow    `json:"order"`
	Title     string      `json:"title"`
	Link      string      `json:"link"`
	AltTitles []string    `json:"alt_titles,omitempty"`
	External  externalRow `json:"external"`
	Year      int         `json:"year"`
}

type orderRow struct {
	Type  string `json:"type"`
	Name  string `json:"name"`
	Aired bool   `json:"aired"`
}

func (*rootRow) check() error { return nil }

// 0x05 sightings: the claimed key (nil while unidentified), the mapped
// video path and the item's facts less Native, which the key holds.
type sightingRow struct {
	Key          *rowKey              `json:"key,omitempty"`
	OriginalLang rowTag               `json:"original_lang"`
	VideoPath    string               `json:"video_path"`
	SceneName    string               `json:"scene_name"`
	Title        string               `json:"title"`
	AirDate      string               `json:"air_date"`
	AudioLangs   []rowTag             `json:"audio_langs,omitempty"`
	Tags         []mediasource.TagRef `json:"tags,omitempty"`
	External     externalRow          `json:"external"`
	Numbering    numberingRow         `json:"numbering"`
	Series       numberingRow         `json:"series_numbering"`
	Absolute     int                  `json:"absolute"`
	Excluded     bool                 `json:"excluded"`
}

func (r *sightingRow) check() error {
	if err := r.Numbering.check(); err != nil {
		return err
	}
	return r.Series.check()
}

// 0x06 stamps.
type stampRow struct {
	Pass uint64 `json:"pass"`
}

func (*stampRow) check() error { return nil }

// 0x07 overlaps: the live source owning the path or key this sighting
// reported.
type overlapRow struct {
	Owner media.SourceID `json:"owner"`
	ByKey bool           `json:"by_key"`
}

func (*overlapRow) check() error { return nil }

// 0x08 items: the sighting providing the key's facts, and its instance, 0
// for none.
type itemRow struct {
	Source media.SourceID       `json:"source"`
	Item   mediasource.NativeID `json:"item"`
	Root   nativeRefRow         `json:"root"`
	Inst   uint64               `json:"inst"`
}

func (r *itemRow) check() error { return r.Root.check() }

// 0x09 instances. Owner "" is ownerless, with Orphaned set.
type instanceRow struct {
	Orphaned  time.Time      `json:"orphaned"`
	Owner     media.SourceID `json:"owner,omitzero"`
	VideoPath string         `json:"video_path"`
	SceneName string         `json:"scene_name"`
	Items     []rowKey       `json:"items"`
}

func (*instanceRow) check() error { return nil }

// 0x0A coverage: one entry per configured pair.
type coverageRow struct {
	Pairs []coveragePair `json:"pairs"`
}

type coveragePair struct {
	Tag      rowTag `json:"tag"`
	Variant  uint8  `json:"variant"`
	Coverage uint8  `json:"coverage"`
}

func (r *coverageRow) check() error {
	for _, p := range r.Pairs {
		if err := checkVariant(p.Variant); err != nil {
			return err
		}
		if err := enumIn("coverage", p.Coverage, uint8(Missing), uint8(Exact)); err != nil {
			return err
		}
	}
	return nil
}

// 0x0B locks.
type lockRow struct {
	At time.Time `json:"at"`
}

func (*lockRow) check() error { return nil }

// 0x0C files: a file record less Inst and Name, which the key holds. Timing
// is timing.AppendTiming's bytes.
type fileRow struct {
	Provenance *provenanceRow `json:"provenance,omitempty"`
	Tag        rowTag         `json:"tag"`
	Timing     []byte         `json:"timing"`
	Identity   identityRow    `json:"identity"`
	Ordinal    int            `json:"ordinal"`
	Generation uint64         `json:"generation"`
	Flags      uint8          `json:"flags"`
	Locked     bool           `json:"locked"`
}

type identityRow struct {
	Size    int64 `json:"size"`
	MtimeNs int64 `json:"mtime_ns"`
	SHA256  sum32 `json:"sha256"`
}

func (r *fileRow) check() error {
	if err := checkFlags(r.Flags); err != nil {
		return err
	}
	if r.Provenance != nil {
		return r.Provenance.check()
	}
	return nil
}

type provenanceRow struct {
	Provider          string     `json:"provider"`
	Release           string     `json:"release"`
	Timing            outcomeRow `json:"timing"`
	Facts             factsRow   `json:"facts"`
	MatchedBy         uint8      `json:"matched_by"`
	Fit               uint8      `json:"fit"`
	Manual            bool       `json:"manual"`
	Uploaded          bool       `json:"uploaded"`
	MachineTranslated bool       `json:"machine_translated"`
}

// outcomeRow is a timing.Outcome; its zero value is a pass that never ran.
type outcomeRow struct {
	Confidence float64 `json:"confidence"`
	OffsetMs   int64   `json:"offset_ms"`
	Action     uint8   `json:"action"`
	Skip       uint8   `json:"skip"`
	Method     uint8   `json:"method"`
	Verdict    uint8   `json:"verdict"`
}

func (o *outcomeRow) check() error {
	for _, t := range [...]struct {
		what   string
		v, max uint8
	}{
		{"timing action", o.Action, uint8(timing.RigidReference)},
		{"timing skip reason", o.Skip, uint8(timing.Disabled)},
		{"timing method", o.Method, uint8(timing.Audio)},
		{"timing verdict", o.Verdict, uint8(timing.Unverified)},
	} {
		if err := enumIn(t.what, t.v, 0, t.max); err != nil {
			return err
		}
	}
	return nil
}

type factsRow struct {
	Source          uint8 `json:"source"`
	Group           uint8 `json:"group"`
	Service         uint8 `json:"service"`
	Cut             uint8 `json:"cut"`
	Rate            uint8 `json:"rate"`
	Hash            bool  `json:"hash"`
	PreRelease      bool  `json:"pre_release"`
	SeasonPack      bool  `json:"season_pack"`
	EpisodeByNumber bool  `json:"episode_by_number"`
}

func (p *provenanceRow) check() error {
	if err := p.Timing.check(); err != nil {
		return err
	}
	// Zero MatchedBy and Fit are a file no provider ranked: a user's upload.
	if err := enumIn("matched by", p.MatchedBy, 0, uint8(rank.Hash)); err != nil {
		return err
	}
	if err := enumIn("fit", p.Fit, 0, uint8(rank.Exact)); err != nil {
		return err
	}
	f := p.Facts
	for _, t := range [...]struct {
		what string
		v    uint8
	}{{"source", f.Source}, {"group", f.Group}, {"service", f.Service}, {"cut", f.Cut}, {"rate", f.Rate}} {
		if err := enumIn(t.what, t.v, uint8(rank.Unknown), uint8(rank.Differs)); err != nil {
			return err
		}
	}
	return nil
}

// 0x0D baselines: timing.AppendCues bytes.
type baselineRow struct {
	Cues []byte `json:"cues"`
}

func (*baselineRow) check() error { return nil }

// 0x0E ordinals: the next ordinal, never below what is on disk.
type ordinalRow struct {
	Next uint64 `json:"next"`
}

func (*ordinalRow) check() error { return nil }

// 0x0F intents: the pending operation and the rows its End stages. Record
// is absent for a Delete; Baseline is a Deliver's or an adopting Retime's
// timing.AppendCues bytes.
type intentRow struct {
	Record   *fileRow      `json:"record,omitempty"`
	Baseline []byte        `json:"baseline,omitempty"`
	Attempts []downloadRow `json:"attempts,omitempty"`
	Removes  []removalRow  `json:"removes,omitempty"`
	SHA256   sum32         `json:"sha256"`
	Op       uint8         `json:"op"`
}

type removalRow struct {
	Name   string `json:"name"`
	SHA256 sum32  `json:"sha256"`
}

func (r *intentRow) check() error {
	if err := enumIn("op", r.Op, uint8(Deliver), uint8(Delete)); err != nil {
		return err
	}
	if r.Record != nil {
		if err := r.Record.check(); err != nil {
			return err
		}
	}
	for i := range r.Attempts {
		if err := r.Attempts[i].check(); err != nil {
			return err
		}
	}
	return nil
}

// 0x10 local: the instance's subtitle files subflux does not manage.
type localRow struct {
	Files []localFileRow `json:"files"`
}

type localFileRow struct {
	Name  string `json:"name"`
	Tag   rowTag `json:"tag"`
	Flags uint8  `json:"flags"`
}

func (r *localRow) check() error {
	for _, f := range r.Files {
		if err := checkFlags(f.Flags); err != nil {
			return err
		}
	}
	return nil
}

// 0x11 embedded: a probe; a failed probe has no tracks and a zero stamp.
type embeddedRow struct {
	Tracks  []trackRow `json:"tracks,omitempty"`
	Size    int64      `json:"size"`
	MtimeNs int64      `json:"mtime_ns"`
	FPS     float64    `json:"fps"`
}

type trackRow struct {
	Codec string `json:"codec"`
	Title string `json:"title"`
	Tag   rowTag `json:"tag"`
	Index int    `json:"index"`
	Flags uint8  `json:"flags"`
}

func (r *embeddedRow) check() error {
	for _, t := range r.Tracks {
		if err := checkFlags(t.Flags); err != nil {
			return err
		}
	}
	return nil
}

// 0x12 downloads: one attempt, never rewritten (DECISIONS 1).
type downloadRow struct {
	At         time.Time     `json:"at"`
	Target     rowTag        `json:"target"`
	Refusal    string        `json:"refusal"`
	Items      []rowKey      `json:"items"`
	Provenance provenanceRow `json:"provenance"`
	Attempt    int           `json:"attempt"`
	Inst       uint64        `json:"inst"`
	Run        id16          `json:"run"`
	Candidate  sum32         `json:"candidate"`
	Variant    uint8         `json:"variant"`
	Delivered  bool          `json:"delivered"`
}

func (r *downloadRow) check() error {
	if err := checkVariant(r.Variant); err != nil {
		return err
	}
	return r.Provenance.check()
}

// 0x13 work.
type workRow struct {
	NotBefore     time.Time `json:"not_before"`
	FirstDeferral time.Time `json:"first_deferral"`
	Gen           uint64    `json:"gen"`
	Seq           uint64    `json:"seq"`
	Cause         uint8     `json:"cause"`
}

func (r *workRow) check() error { return enumIn("work cause", r.Cause, uint8(Scan), uint8(Item)) }

// 0x14 jobs: status.Job less ID, which the key holds.
type jobRow struct {
	Started     time.Time  `json:"started"`
	Ended       time.Time  `json:"ended"`
	Key         *jobKeyRow `json:"key,omitempty"`
	Scope       string     `json:"scope"`
	Phase       string     `json:"phase"`
	Cause       string     `json:"cause"`
	Owner       int64      `json:"owner"`
	Done        int        `json:"done"`
	Total       int        `json:"total"`
	Rev         uint64     `json:"rev"`
	Kind        uint8      `json:"kind"`
	State       uint8      `json:"state"`
	Outcome     uint8      `json:"outcome"`
	Cancellable bool       `json:"cancellable"`
}

type jobKeyRow struct {
	Key         string `json:"key"`
	Fingerprint sum32  `json:"fingerprint"`
}

// check refuses an owner below BypassOwner and a Done record with no end,
// whose place in the ended indexes would be no instant.
func (r *jobRow) check() error {
	if r.Owner < status.BypassOwner {
		return fmt.Errorf("statedb: job owner %d is below BypassOwner", r.Owner)
	}
	if r.State == uint8(status.Done) && r.Ended.IsZero() {
		return errors.New("statedb: a done job has no end time")
	}
	if err := enumIn("job kind", r.Kind, uint8(status.Scan), uint8(status.Backup)); err != nil {
		return err
	}
	if err := enumIn("job state", r.State, uint8(status.Queued), uint8(status.Done)); err != nil {
		return err
	}
	return enumIn("job outcome", r.Outcome, uint8(status.None), uint8(status.Interrupted))
}

// userOwned is whether owner's jobs count against JobLimits.Total: a
// user's or the bypass principal's, never SystemOwner's.
func userOwned(owner int64) bool { return owner > 0 || owner == status.BypassOwner }

// 0x15 credentials: one disable record per provider, which the key holds.
type credentialRow struct {
	At          time.Time `json:"at"`
	Fingerprint string    `json:"fingerprint"`
	Reason      string    `json:"reason"`
}

func (*credentialRow) check() error { return nil }

// 0x16 no_results: how many searches of one provider for one target found
// nothing, and when the next may run.
type noResultRow struct {
	Next  time.Time `json:"next"`
	Count int       `json:"count"`
}

func (r *noResultRow) check() error {
	if r.Count < 1 {
		return fmt.Errorf("statedb: stored no-result count %d is below 1", r.Count)
	}
	return nil
}

// 0x17 root_evidence: a root's mediasource.RootEvidence, which decode
// checks with its Validate.
type evidenceRow struct {
	Held  *readingRow `json:"held,omitempty"`
	Bound readingRow  `json:"bound"`
}

type readingRow struct {
	Titles    []titleSetRow `json:"titles"`
	Catalogue catalogueRow  `json:"catalogue"`
}

type catalogueRow struct {
	Fetched  time.Time         `json:"fetched"`
	Orders   []orderPlacesRow  `json:"orders"`
	Episodes []airedEpisodeRow `json:"episodes"`
	TVDB     uint64            `json:"tvdb"`
}

type orderPlacesRow struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Places []placeRow      `json:"places"`
	Extra  []extraPlaceRow `json:"extra"`
}

type placeRow struct {
	Season  int  `json:"season"`
	Episode int  `json:"episode"`
	Listed  bool `json:"listed"`
}

type extraPlaceRow struct {
	AirDate string `json:"air_date"`
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
}

type airedEpisodeRow struct {
	AirDate  string `json:"air_date"`
	ID       uint64 `json:"id"`
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Absolute int    `json:"absolute"`
}

type titleSetRow struct {
	Language string            `json:"language"`
	Titles   []episodeTitleRow `json:"titles"`
	Found    bool              `json:"found"`
}

type episodeTitleRow struct {
	Title string `json:"title"`
	ID    uint64 `json:"id"`
}

func (r *evidenceRow) check() error { return r.evidence().Validate() }
