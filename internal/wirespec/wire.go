// Package wirespec holds subflux's wire types and their bounds. A bounded
// type declares its bound by method (Range, MaxBytes, Pattern, Count).
package wirespec

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/clientaddr"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// Hex16 is a 16-byte identity (a store epoch, a boot id, a run id) on the wire
// as 32 lowercase hex characters.
type Hex16 [16]byte

// MarshalText answers 32 lowercase hex characters.
func (h Hex16) MarshalText() ([]byte, error) { return hexText(h[:]), nil }

// UnmarshalText refuses anything but 32 lowercase hex characters.
func (h *Hex16) UnmarshalText(b []byte) error { return unhexText(h[:], "Hex16", b) }

// SHA256 is a digest on the wire as 64 lowercase hex characters.
type SHA256 [32]byte

// MarshalText answers 64 lowercase hex characters.
func (s SHA256) MarshalText() ([]byte, error) { return hexText(s[:]), nil }

// UnmarshalText refuses anything but 64 lowercase hex characters.
func (s *SHA256) UnmarshalText(b []byte) error { return unhexText(s[:], "SHA256", b) }

func hexText(b []byte) []byte { return hex.AppendEncode(nil, b) }

// unhexText fills dst from b, leaving dst unchanged on error. hex.Decode
// alone admits uppercase, which would give one value two spellings.
func unhexText(dst []byte, typ string, b []byte) error {
	if len(b) != 2*len(dst) {
		return fmt.Errorf("wirespec: %s of %d characters, want %d", typ, len(b), 2*len(dst))
	}
	for _, c := range b {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return fmt.Errorf("wirespec: %s %q is not lowercase hex", typ, b)
		}
	}
	_, err := hex.Decode(dst, b)
	return err
}

// RootParams is the {root} path parameter. In every *Params type each
// field's json name is its {wildcard}, decoded only through
// encoding.TextUnmarshaler or as a bounded string.
type RootParams struct {
	Root media.RootKey `json:"root"`
}

// ItemParams is the {key} path parameter.
type ItemParams struct {
	Key media.Key `json:"key"`
}

// LockParams names one pair of one instance: a lock is set
// for every item of the instance at once.
type LockParams struct {
	Target   language.Tag       `json:"target"`
	Instance statedb.InstanceID `json:"instance"`
	Variant  language.Variant   `json:"variant"`
}

// InstanceParams is the {instance} path parameter.
type InstanceParams struct {
	Instance statedb.InstanceID `json:"instance"`
}

// FileParams names one file beside one instance's video.
type FileParams struct {
	Name     FileName           `json:"name"`
	Instance statedb.InstanceID `json:"instance"`
}

// JobParams is the {id} path parameter.
type JobParams struct {
	ID status.JobID `json:"id"`
}

// SourceParams is the {source} path parameter.
type SourceParams struct {
	Source media.SourceID `json:"source"`
}

// ChangesQuery asks for the library changes after Since in Epoch.
type ChangesQuery struct {
	Epoch Hex16  `json:"epoch"`
	Since uint64 `json:"since"`
}

// PreviewQuery starts a preview FromMs into the video.
type PreviewQuery struct {
	FromMs FromMs `json:"from_ms,omitempty"`
}

// RescanQuery names the scanning source; with one owning source it may
// be absent.
type RescanQuery struct {
	Source *media.SourceID `json:"source,omitempty"`
}

// DeleteQuery is the expectation a file delete must match.
type DeleteQuery struct {
	Generation uint64 `json:"generation"`
	SHA256     SHA256 `json:"sha256"`
}

// LockRequest sets or clears an instance's lock.
type LockRequest struct {
	Locked bool `json:"locked"`
}

// CandidatesRequest searches for subtitles in Target.
type CandidatesRequest struct {
	Target language.Tag `json:"target"`
}

// DownloadRequest takes one held candidate as Variant.
type DownloadRequest struct {
	Handle  search.Handle    `json:"handle"`
	Variant language.Variant `json:"variant"`
}

// UploadRequest is files.upload's body: Bytes is the file or
// archive in base64, 1 byte to provider.MaxBody decoded; Name is one line
// of 1 to 255 bytes. Either refused is body_invalid at its field.
type UploadRequest struct {
	Name    UploadName       `json:"name"`
	Target  language.Tag     `json:"target"`
	Bytes   UploadBytes      `json:"bytes"`
	Item    media.Key        `json:"item"`
	Variant language.Variant `json:"variant"`
}

// TimingRequest carries exactly one of Timing (a user edit) and Proposal
// (apply a sync proposal: its timing and outcome come from the server).
type TimingRequest struct {
	Timing   *TimingWire   `json:"timing,omitempty"`
	Proposal *status.JobID `json:"proposal,omitempty"`
	Expect   ExpectDoc     `json:"expect"`
}

// RenderRequest renders a timing preview. FromMs is the overlaid
// preview's from_ms: RenderVTT renders that window shifted to start at 0, as the preview's timestamps do.
type RenderRequest struct {
	Timing TimingWire `json:"timing"`
	Expect ExpectDoc  `json:"expect"`
	FromMs FromMs     `json:"from_ms,omitempty"`
}

// SyncRequest proposes a timing for one file by Method.
type SyncRequest struct {
	Expect ExpectDoc     `json:"expect"`
	Method timing.Method `json:"method"` // reference or audio
}

// ResyncFile is one file of a resync batch with the state the caller saw.
type ResyncFile struct {
	Ref    FileRefDoc `json:"ref"`
	Expect ExpectDoc  `json:"expect"`
}

// ResyncRequest syncs a batch of files by Method.
type ResyncRequest struct {
	Files  ResyncFiles   `json:"files"`
	Method timing.Method `json:"method"`
}

// SettingsSaveRequest saves a settings document over revision Base,
// keeping the secrets Unchanged names.
type SettingsSaveRequest struct {
	Base      string          `json:"base"` // the revision the editor loaded
	Document  json.RawMessage `json:"document"`
	Unchanged []string        `json:"unchanged"`
}

// SettingsTestRequest tests one entry of Section before it is saved.
type SettingsTestRequest struct {
	Entry     json.RawMessage `json:"entry"`
	Unchanged []string        `json:"unchanged"`
	Section   TestSection     `json:"section"`
}

// TestSection is the settings section an entry under test belongs to.
type TestSection uint8

// The sections a settings test reads, on the wire sources, media_servers
// and providers.
const (
	TestSources TestSection = iota + 1
	TestMediaServers
	TestProviders
)

var testSectionNames = []string{TestSources: "sources", TestMediaServers: "media_servers", TestProviders: "providers"}

// MarshalText answers sources, media_servers or providers and refuses any
// other value.
func (s TestSection) MarshalText() ([]byte, error) {
	return marshalName(testSectionNames, "test section", uint8(s))
}

// UnmarshalText reads MarshalText's spellings and refuses every other text.
func (s *TestSection) UnmarshalText(b []byte) error {
	return unmarshalName(s, testSectionNames, "test section", b)
}

// ExternalIDsDoc is media.ExternalIDs; a zero id is absent.
type ExternalIDsDoc struct {
	TVDB uint64 `json:"tvdb,omitempty"`
	TMDB uint64 `json:"tmdb,omitempty"`
	IMDb uint64 `json:"imdb,omitempty"`
}

// NumberingDoc is media.Numbering.
type NumberingDoc struct {
	Scheme  media.Scheme `json:"scheme"`
	Season  int          `json:"season"`
	Episode int          `json:"episode"`
}

// FlagsDoc is language.Flags.
type FlagsDoc struct {
	HI     bool `json:"hi"`
	Forced bool `json:"forced"`
}

// RootDoc is one library root: a series or a movie.
type RootDoc struct {
	Source     media.SourceID     `json:"source"`
	Title      string             `json:"title"`
	Link       string             `json:"link,omitempty"` // the source's page for the root
	AltTitles  []string           `json:"alt_titles"`
	Counts     []CoverageCountDoc `json:"counts"`
	External   ExternalIDsDoc     `json:"external"`
	Key        media.RootKey      `json:"key"`
	Kind       media.Kind         `json:"kind"`
	HasHistory bool               `json:"has_history"`
}

// CoverageCountDoc is statedb.CoverageCount with the target's English
// name, as CoverageDoc carries it.
type CoverageCountDoc struct {
	TargetName string           `json:"target_name"`
	Target     language.Tag     `json:"target"`
	Items      int              `json:"items"`
	Exact      int              `json:"exact"`
	Substitute int              `json:"substitute"`
	Missing    int              `json:"missing"`
	Locked     int              `json:"locked"`
	Variant    language.Variant `json:"variant"`
}

// CoverageDoc is an item's coverage of one target and variant.
type CoverageDoc struct {
	TargetName string           `json:"target_name"`
	Target     language.Tag     `json:"target"`
	Variant    language.Variant `json:"variant"`
	Coverage   statedb.Coverage `json:"coverage"`
	Locked     bool             `json:"locked"`
	// LastRefusal is the last automatic run's refusal of a Missing pair,
	// empty after a delivery and whenever Coverage is not Missing.
	LastRefusal search.WhyNot `json:"last_refusal,omitempty"`
}

// ItemRowDoc is one episode or movie in a list.
type ItemRowDoc struct {
	Title      string             `json:"title"`
	Coverage   []CoverageDoc      `json:"coverage"`
	Key        media.Key          `json:"key"`
	Numbering  NumberingDoc       `json:"numbering"`
	Absolute   int                `json:"absolute,omitempty"`
	Instance   statedb.InstanceID `json:"instance,omitempty"` // 0: no video
	Excluded   bool               `json:"excluded"`
	HasHistory bool               `json:"has_history"`
}

// FileRefDoc names one file beside one instance's video.
type FileRefDoc struct {
	Name       FileName           `json:"name"`
	InstanceID statedb.InstanceID `json:"instance_id"`
}

// ExpectDoc is the state of a file a caller saw: generation 0 for an
// unmanaged file, and the bytes' SHA-256.
type ExpectDoc struct {
	Generation uint64 `json:"generation"`
	SHA256     SHA256 `json:"sha256"`
}

// FactsDoc is rank.Facts.
type FactsDoc struct {
	Hash       bool     `json:"hash"`
	Source     rank.Tri `json:"source"`
	Group      rank.Tri `json:"group"`
	Service    rank.Tri `json:"service"`
	Cut        rank.Tri `json:"cut"`
	Rate       rank.Tri `json:"rate"`
	PreRelease bool     `json:"pre_release"`
	SeasonPack bool     `json:"season_pack"`
	// EpisodeByNumber: a pack member chosen by its number alone, so its
	// timing ran as the guard (search design 8.11).
	EpisodeByNumber bool `json:"episode_by_number"`
}

// DecisionDoc is timing.Decision.
type DecisionDoc struct {
	Action timing.Action     `json:"action"`
	Skip   timing.SkipReason `json:"skip"`
}

// OutcomeDoc is timing.Outcome.
type OutcomeDoc struct {
	Confidence float64        `json:"confidence"`
	OffsetMs   int64          `json:"offset_ms"`
	Decision   DecisionDoc    `json:"decision"`
	Method     timing.Method  `json:"method"`
	Verdict    timing.Verdict `json:"verdict"`
}

// ProvenanceDoc is statedb.Provenance: where a downloaded file came from.
type ProvenanceDoc struct {
	Provider          string         `json:"provider"`
	Release           string         `json:"release"`
	Manual            bool           `json:"manual"`
	Uploaded          bool           `json:"uploaded"`
	MachineTranslated bool           `json:"machine_translated"`
	MatchedBy         rank.MatchedBy `json:"matched_by"`
	Facts             FactsDoc       `json:"facts"`
	Fit               rank.Fit       `json:"fit"`
	Timing            OutcomeDoc     `json:"timing"`
}

// FileDoc is one subtitle file beside a video.
type FileDoc struct {
	Provenance *ProvenanceDoc `json:"provenance"` // null: not downloaded by subflux
	Timing     *TimingWire    `json:"timing,omitempty"`
	Ref        FileRefDoc     `json:"ref"`
	TagName    string         `json:"tag_name"`
	Tag        language.Tag   `json:"tag"`
	Expect     ExpectDoc      `json:"expect"`
	Ordinal    int            `json:"ordinal,omitempty"`
	Flags      FlagsDoc       `json:"flags"`
	Managed    bool           `json:"managed"`
	Locked     bool           `json:"locked"`
}

// TrackDoc is one subtitle track embedded in a video.
type TrackDoc struct {
	Codec   string       `json:"codec"`
	Title   string       `json:"title"`
	Tag     language.Tag `json:"tag"`
	Index   int          `json:"index"`
	Flags   FlagsDoc     `json:"flags"`
	Ignored bool         `json:"ignored"`
}

// LabelDoc is one subtitle as a media server labels it.
type LabelDoc struct {
	File        string   `json:"file"`
	LanguageTag string   `json:"language_tag"`
	Display     string   `json:"display"`
	Flags       FlagsDoc `json:"flags"`
}

// ServerLabelsDoc is one media server's labels for an item, or its Error.
type ServerLabelsDoc struct {
	Server string     `json:"server"`
	Error  string     `json:"error,omitempty"`
	Labels []LabelDoc `json:"labels"`
}

// ItemDoc is the item page's one read. Files, Unparsed, Embedded and
// MediaServerLabels are null outside Active (Live false).
type ItemDoc struct {
	VideoName         string            `json:"video_name,omitempty"` // the base name only
	Root              RootDoc           `json:"root"`
	OriginalLang      language.Tag      `json:"original_lang"`
	AudioLangs        []language.Tag    `json:"audio_langs"`
	Files             []FileDoc         `json:"files"`
	Unparsed          []string          `json:"unparsed"`
	Embedded          []TrackDoc        `json:"embedded"`
	MediaServerLabels []ServerLabelsDoc `json:"media_server_labels"`
	Item              ItemRowDoc        `json:"item"`
	// Unwritable: the writer last failed to create a file in the
	// video's folder while its root accepted writes.
	Unwritable bool `json:"unwritable"`
	Live       bool `json:"live"`
}

// ProposalDoc is a finished sync job's proposed timing for one file.
type ProposalDoc struct {
	File    FileRefDoc   `json:"file"`
	Job     status.JobID `json:"job"`
	Timing  TimingWire   `json:"timing"`
	Expect  ExpectDoc    `json:"expect"`
	Outcome OutcomeDoc   `json:"outcome"`
}

// CandidateDoc is one subtitle a manual search found.
type CandidateDoc struct {
	Provider          string             `json:"provider"`
	LangName          string             `json:"lang_name"`
	Handle            search.Handle      `json:"handle"`
	Reason            string             `json:"reason"`
	Release           string             `json:"release"`
	Lang              language.Tag       `json:"lang"`
	Covers            []NumberingDoc     `json:"covers"`
	Facts             FactsDoc           `json:"facts"`
	Flags             FlagsDoc           `json:"flags"`
	Closeness         language.Closeness `json:"closeness"`
	MatchedBy         rank.MatchedBy     `json:"matched_by"`
	Class             language.Class     `json:"class"`
	Fit               rank.Fit           `json:"fit"`
	Automatic         bool               `json:"automatic"`
	WhyNot            search.WhyNot      `json:"why_not"`
	MachineTranslated bool               `json:"machine_translated"`
}

// ProviderAnswerDoc is one enabled provider's outcome in a manual search:
// count when answered, until when paused, cause when failed or
// not_asked.
type ProviderAnswerDoc struct {
	Until    *time.Time    `json:"until,omitempty"`
	Provider string        `json:"provider"`
	Cause    string        `json:"cause,omitempty"`
	Count    int           `json:"count,omitempty"`
	Answer   search.Answer `json:"answer"`
}

// JobDoc is status.Job.
type JobDoc struct {
	Ended       time.Time      `json:"ended,omitzero"`
	Started     time.Time      `json:"started"`
	Phase       string         `json:"phase"`
	Source      media.SourceID `json:"source,omitempty"`
	Scope       string         `json:"scope"`
	Cause       string         `json:"cause,omitempty"`
	ID          status.JobID   `json:"id"`
	Done        int            `json:"done"`
	Total       int            `json:"total"`
	OwnerID     int64          `json:"owner_id"` // 0: system, -1: auth disabled
	Rev         uint64         `json:"rev"`
	Cancellable bool           `json:"cancellable"`
	Outcome     status.Outcome `json:"outcome"`
	State       status.State   `json:"state"`
	Kind        status.Kind    `json:"kind"`
}

// AlertDoc is one open alert.
type AlertDoc struct {
	Since  time.Time `json:"since"`
	Source string    `json:"source"`
	Kind   string    `json:"kind"`
	Text   string    `json:"text"`
}

// ProviderDoc is one provider's health.
type ProviderDoc struct {
	Until  time.Time     `json:"until,omitzero"`
	ID     string        `json:"id"`
	Reason string        `json:"reason,omitempty"`
	Health status.Health `json:"health"`
}

// StatusDoc is the published status. Seq counts snapshots published since
// Boot; SettingsRev moves with each applied settings commit, so a client
// seeing it move re-reads session.get. Jobs is a window of 50, unfinished
// first (newest started first), then the latest ended.
type StatusDoc struct {
	Jobs        []JobDoc      `json:"jobs"`
	Alerts      []AlertDoc    `json:"alerts"`
	Providers   []ProviderDoc `json:"providers"`
	Seq         uint64        `json:"seq"`
	SettingsRev uint64        `json:"settings_rev"`
	Unfinished  int           `json:"unfinished"` // every unfinished job, every owner
	Boot        Hex16         `json:"boot"`
}

// HistoryDoc is one download attempt.
type HistoryDoc struct {
	At         time.Time          `json:"at"`
	Refusal    AttemptRefusal     `json:"refusal"`
	Target     language.Tag       `json:"target"`
	Items      []media.Key        `json:"items"`
	Provenance ProvenanceDoc      `json:"provenance"`
	Attempt    int                `json:"attempt"`
	Instance   statedb.InstanceID `json:"instance"`
	Run        Hex16              `json:"run"`
	Variant    language.Variant   `json:"variant"`
	Delivered  bool               `json:"delivered"`
}

// ChangesDoc is the polled feed in the library frame's shape: the current
// rows of roots changed after From, the deleted keys and the marks, or
// Full alone.
type ChangesDoc struct {
	Upserts []RootDoc       `json:"upserts"`
	Deletes []media.RootKey `json:"deletes"`
	Marks   []string        `json:"marks"`
	From    uint64          `json:"from"`
	Rev     uint64          `json:"rev"`
	Epoch   Hex16           `json:"epoch"`
	Full    bool            `json:"full"`
}

// UnidentifiedDoc is one item a source reports without the id that keys it.
type UnidentifiedDoc struct {
	Source media.SourceID `json:"source"`
	Root   RootDoc        `json:"root"`
	Item   ItemRowDoc     `json:"item"`
}

// CandidateList is a manual search's candidates and each enabled
// provider's outcome.
type CandidateList struct {
	Items     []CandidateDoc      `json:"items"`
	Providers []ProviderAnswerDoc `json:"providers"`
}

// AttemptRefusal is a history row's refusal: search.WhyNot's spellings or
// one of search's Refusal constants, "" for a delivered or staged attempt.
type AttemptRefusal string

// PrincipalDoc is the signed-in principal.
type PrincipalDoc struct {
	IdleDeadline     time.Time `json:"idle_deadline,omitzero"`
	AbsoluteDeadline time.Time `json:"absolute_deadline,omitzero"`
	Username         string    `json:"username"`
	DisplayName      string    `json:"display_name,omitempty"`
	Role             string    `json:"role"`
	Method           string    `json:"method"`
	UserID           int64     `json:"user_id"`
	KeyID            int64     `json:"key_id,omitempty"`
	Scope            Scope     `json:"scope"` // effective: min(key scope, role)
}

// FieldErrorDoc refuses one field by its path.
type FieldErrorDoc struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ExposureDoc is why public clients' requests count as exposure, and since
// when.
type ExposureDoc struct {
	Since    time.Time           `json:"since"`
	LastSeen time.Time           `json:"last_seen"`
	Reasons  []clientaddr.Reason `json:"reasons"` // plain_http | no_trusted_proxies
}

// SessionDoc is public. Failure and Exposure are set for admins only.
type SessionDoc struct {
	Principal *PrincipalDoc   `json:"principal"`
	Exposure  *ExposureDoc    `json:"exposure,omitempty"`
	OIDCLabel string          `json:"oidc_label"`
	Version   string          `json:"version"`
	Failure   []FieldErrorDoc `json:"failure,omitempty"`
	Boot      Hex16           `json:"boot"`
	Mode      settings.Mode   `json:"mode"`
}

// TargetDoc is one configured subtitle target and its variants.
type TargetDoc struct {
	Tag      language.Tag       `json:"tag"`
	Name     string             `json:"name"`
	Variants []language.Variant `json:"variants"`
}

// CloseNameDoc is a language close to another, and how close.
type CloseNameDoc struct {
	Name      string             `json:"name"`
	Tag       language.Tag       `json:"tag"`
	Closeness language.Closeness `json:"closeness"`
	Class     language.Class     `json:"class"`
}

// NameDoc is one language's English name and its close languages.
type NameDoc struct {
	Tag   language.Tag   `json:"tag"`
	Name  string         `json:"name"`
	Close []CloseNameDoc `json:"close"`
}

// LanguagesDoc is the language settings and names a client shows.
type LanguagesDoc struct {
	Targets        []TargetDoc `json:"targets"`
	Names          []NameDoc   `json:"names"`
	CloseVarieties bool        `json:"close_varieties"`
}

// CapabilitiesDoc is what a media source can do.
type CapabilitiesDoc struct {
	Kinds   []media.Kind `json:"kinds"`
	Changes bool         `json:"changes"`
	Rescan  bool         `json:"rescan"`
	Tags    bool         `json:"tags"`
	Posters bool         `json:"posters"`
}

// SourceDoc is one configured media source and its open alert.
type SourceDoc struct {
	Alert        *AlertDoc       `json:"alert"`
	ID           media.SourceID  `json:"id"`
	Type         string          `json:"type"`
	Capabilities CapabilitiesDoc `json:"capabilities"`
}

// SourceHealthDoc is one connection check of a media source.
type SourceHealthDoc struct {
	Cause   string `json:"cause,omitempty"`
	Message string `json:"message,omitempty"`
	OK      bool   `json:"ok"`
}

// RootFoldersDoc is the folders a media source reports.
type RootFoldersDoc struct {
	Folders []RootFolderDoc `json:"folders"`
}

// RootFolderDoc is one folder a source reports: Local from
// the source's path mappings, State from mediawrite's FolderState.
type RootFolderDoc struct {
	Remote string                 `json:"remote"`
	Local  string                 `json:"local"`
	State  mediawrite.FolderState `json:"state"`
}

// MediaServerDoc is one configured media server and its open alert.
type MediaServerDoc struct {
	Alert *AlertDoc `json:"alert"`
	ID    string    `json:"id"`
	Type  string    `json:"type"`
}

// SettingsDoc is the stored settings document and its state.
type SettingsDoc struct {
	Revision  string          `json:"revision"` // settings.Snapshot.Revision
	Document  json.RawMessage `json:"document"` // null when no file
	Unchanged []string        `json:"unchanged"`
	Failure   []FieldErrorDoc `json:"failure,omitempty"`
	Mode      settings.Mode   `json:"mode"`
	Diverged  bool            `json:"diverged"`
}

// SettingsSaved is the state a save left.
type SettingsSaved struct {
	Revision string        `json:"revision"`
	Mode     settings.Mode `json:"mode"`
}

// FieldDoc is formschema.Field.
type FieldDoc struct {
	Key      string          `json:"key"`
	Label    string          `json:"label"`
	Help     string          `json:"help,omitempty"`
	Default  string          `json:"default,omitempty"`
	Enum     []string        `json:"enum,omitempty"`
	Min      int64           `json:"min,omitempty"`
	Max      int64           `json:"max,omitempty"`
	Kind     formschema.Kind `json:"kind"`
	Required bool            `json:"required"`
	BootOnly bool            `json:"boot_only,omitempty"`
}

// SchemaDoc is one entry type's form.
type SchemaDoc struct {
	Type    string     `json:"type"`
	Summary string     `json:"summary,omitempty"` // a provider's Entry.Summary
	Fields  []FieldDoc `json:"fields"`
}

// SectionSchemaDoc is one settings.SectionSchema.
type SectionSchemaDoc struct {
	Key    string     `json:"key"`
	Fields []FieldDoc `json:"fields"`
}

// SchemasDoc is every settings form a client renders.
type SchemasDoc struct {
	Sections     []SectionSchemaDoc `json:"sections"`
	Sources      []SchemaDoc        `json:"sources"`
	MediaServers []SchemaDoc        `json:"media_servers"`
	Providers    []SchemaDoc        `json:"providers"`
}

// TestResult is one settings test's verdict.
type TestResult struct {
	Message string `json:"message,omitempty"`
	OK      bool   `json:"ok"`
}

// FieldError refuses one request field by its JSON path.
type FieldError struct {
	Path, Message string
}

func (e *FieldError) Error() string { return e.Path + " " + e.Message }
