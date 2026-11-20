package wirespec

import (
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/scan"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/settings"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/syncsched"
	"github.com/cplieger/subflux/internal/timing"
	"github.com/cplieger/subflux/internal/wirecodes"
)

// The codes several rows answer themselves.
const (
	notConfigured = "not_configured"
	rootUnknown   = "root_unknown"
)

// Body caps beyond the table's 64 KiB default.
const (
	timingBody = 256 << 10
	// 500 resync files with 255-byte names reach about 817 KB under
	// encoding/json's HTML escaping, so 512 KiB would refuse a valid batch.
	documentBody = 1 << 20
	// uploadBody holds UploadMax in base64 plus the default for the other
	// fields.
	uploadBody = (UploadMax+2)/3*4 + 64<<10
)

// The owner chains a row lists, each owner before the owners whose
// sentinels it passes on, in the order the answer mapping tries them.
func storeOwners() []*httpwire.Owner  { return []*httpwire.Owner{statedb.Owner} }
func statusOwners() []*httpwire.Owner { return []*httpwire.Owner{status.Owner} }
func writeOwners() []*httpwire.Owner  { return []*httpwire.Owner{mediawrite.Owner, timing.Owner} }
func searchOwners() []*httpwire.Owner { return []*httpwire.Owner{search.Owner, status.Owner} }
func scanOwners() []*httpwire.Owner   { return []*httpwire.Owner{scan.Owner, status.Owner} }
func syncOwners() []*httpwire.Owner {
	return []*httpwire.Owner{syncsched.Owner, status.Owner, mediawrite.Owner, timing.Owner}
}

// A *mediasource.SourceError is typed, not a sentinel: the handler answers
// it as its own *httpwire.Error, so a row reaching a source lists its codes.
var (
	sourceCodes     = []string{"source_failed", "source_unknown", "source_unsupported"}
	scanSourceCodes = []string{"source_unknown", "source_unsupported"}
)

var public = httpwire.Access{Class: httpwire.Public}

func scoped(s Scope) httpwire.Access {
	return httpwire.Access{Class: httpwire.Scoped, Scope: scopeNames[s]}
}

// jobStart is a Keyed POST answering the started job.
func jobStart(ep *httpwire.Endpoint) httpwire.Endpoint {
	ep.Method, ep.Keyed, ep.Status, ep.MaxKept = http.MethodPost, true, http.StatusAccepted, 1000
	ep.Response = reflect.TypeFor[JobDoc]()
	return *ep
}

// Table answers a fresh copy of subflux's API table. Derive fills each row's
// error union.
func Table() *httpwire.Table {
	return &httpwire.Table{
		Catalog:   wirecodes.Catalog,
		Prefix:    "/api",
		Window:    24 * time.Hour,
		Endpoints: slices.Concat(sessionRows(), libraryRows(), fileRows(), jobRows(), configRows()),
	}
}

func sessionRows() []httpwire.Endpoint {
	return []httpwire.Endpoint{
		{
			Name: "session.get", Method: http.MethodGet, Path: "/api/session", Access: public, Response: reflect.TypeFor[SessionDoc](),
			Summary: "Mode and caller", Description: "Answers every caller, anonymous included: the setup mode, the signed-in principal with its effective scope, the sign-in button label, the version and the boot id. Admins also get the failed settings fields and the exposure warning. An account store failure answers 503 rather than reporting the caller signed out.",
		},
		{
			Name: "meta.openapi", Method: http.MethodGet, Path: "/api/openapi.json", Access: public, Kind: httpwire.Raw, ContentType: "application/json",
			Summary: "This API's OpenAPI 3.1 document", Description: "The contract every client is generated from: each endpoint's parameters, bodies, scope and error codes with remediation.",
		},
	}
}

func libraryRows() []httpwire.Endpoint {
	return []httpwire.Endpoint{
		{
			Name: "library.roots", Method: http.MethodGet, Path: "/api/library/roots", Access: scoped(Read), List: true,
			Query: reflect.TypeFor[ListQuery](), Response: reflect.TypeFor[httpwire.List[RootDoc]](), Owners: storeOwners(),
			Summary: "List library roots", Description: "Series and movies, one page per call.",
		},
		{
			Name: "library.root", Method: http.MethodGet, Path: "/api/library/roots/{root}", Access: scoped(Read),
			Params: reflect.TypeFor[RootParams](), Response: reflect.TypeFor[RootDoc](), Codes: []string{rootUnknown}, Owners: storeOwners(),
			Summary: "One library root", Description: "A series or movie by key, as library.roots lists it.",
		},
		{
			Name: "library.items", Method: http.MethodGet, Path: "/api/library/roots/{root}/items", Access: scoped(Read), List: true,
			Params: reflect.TypeFor[RootParams](), Query: reflect.TypeFor[ListQuery](), Response: reflect.TypeFor[httpwire.List[ItemRowDoc]](),
			Codes: []string{rootUnknown}, Owners: storeOwners(),
			Summary: "List a root's items", Description: "The episodes or the movie of one root with coverage per configured target, one page per call.",
		},
		{
			Name: "library.item", Method: http.MethodGet, Path: "/api/library/items/{key}", Access: scoped(Read),
			Params: reflect.TypeFor[ItemParams](), Response: reflect.TypeFor[ItemDoc](), Codes: []string{"item_unknown"},
			Owners:  slices.Concat(storeOwners(), writeOwners()),
			Summary: "One item with its files", Description: "The item page's one read: the root, the item, its languages, every subtitle file with provenance, timing, lock and If-Match version, the embedded tracks and the media server labels. Outside active mode the inventory and labels are null.",
		},
		{
			Name: "library.unidentified", Method: http.MethodGet, Path: "/api/library/unidentified", Access: scoped(Read), List: true,
			Query: reflect.TypeFor[ListQuery](), Response: reflect.TypeFor[httpwire.List[UnidentifiedDoc]](), Owners: storeOwners(),
			Summary: "List unidentified items", Description: "Items a source reports without an external id, which no search can serve, one page per call.",
		},
		jobStart(&httpwire.Endpoint{
			Name: "library.rescan", Path: "/api/library/roots/{root}/scan", Access: scoped(Operate),
			Params: reflect.TypeFor[RootParams](), Query: reflect.TypeFor[RescanQuery](),
			Codes:   slices.Concat([]string{rootUnknown, "source_ambiguous", notConfigured}, scanSourceCodes),
			Owners:  slices.Concat(storeOwners(), scanOwners()),
			Summary: "Refetch this root from its source", Description: "Starts a scan job for this root. A root with one owning source needs no source parameter; one owned by several needs source, else 409 source_ambiguous lists the owners.",
		}),
		{
			Name: "instances.setLock", Method: http.MethodPut, Path: "/api/instances/{instance}/locks/{target}/{variant}", Access: scoped(Operate),
			Params: reflect.TypeFor[LockParams](), Request: reflect.TypeFor[LockRequest](), Codes: []string{"instance_unknown"}, Owners: storeOwners(),
			Summary: "Lock or unlock a target", Description: "Sets the manual lock on one target and variant of every item of a video file at once; a locked target is never searched or replaced. Setting the same value twice changes nothing.",
		},
		{
			Name: "history.list", Method: http.MethodGet, Path: "/api/history", Access: scoped(Read), List: true,
			Query: reflect.TypeFor[HistoryQuery](), Response: reflect.TypeFor[httpwire.List[HistoryDoc]](), Owners: storeOwners(),
			Summary: "Download history", Description: "Delivery and refusal attempts, newest first, for the whole library, one root or one item (root and item together are bad_query), one page per call.",
		},
	}
}

func fileRows() []httpwire.Endpoint {
	return []httpwire.Endpoint{
		jobStart(&httpwire.Endpoint{
			Name: "instances.search", Path: "/api/instances/{instance}/search", Access: scoped(Operate),
			Params: reflect.TypeFor[InstanceParams](), Codes: []string{notConfigured}, Owners: searchOwners(),
			Summary: "Search this video's missing subtitles now", Description: "Starts a search job for every configured target this video lacks.",
		}),
		{
			Name: "instances.candidates", Method: http.MethodPost, Path: "/api/instances/{instance}/candidates", Access: scoped(Operate),
			Params: reflect.TypeFor[InstanceParams](), Request: reflect.TypeFor[CandidatesRequest](), Response: reflect.TypeFor[CandidateList](),
			Codes: []string{notConfigured}, Owners: searchOwners(),
			Summary: "Manual search for one target", Description: "Asks the providers for one target and answers up to 50 ranked candidates, each with a handle that downloads.start takes within 15 minutes. A POST because it spends provider quota.",
		},
		{
			Name: "instances.preview", Method: http.MethodGet, Path: "/api/instances/{instance}/preview", Access: scoped(Read), Kind: httpwire.Raw, ContentType: "video/mp4",
			Params: reflect.TypeFor[InstanceParams](), Query: reflect.TypeFor[PreviewQuery](),
			Codes: []string{"instance_unknown", "preview_replaced", notConfigured}, Owners: syncOwners(),
			Summary: "Video preview", Description: "The video as fragmented MP4 from from_ms, its timestamps starting at 0, for the timing editor; send the same from_ms to files.render. Each user streams one preview at a time: opening another ends the previous one. A stream not read for 30 s ends: reopen it at the playback position.",
		},
		jobStart(&httpwire.Endpoint{
			Name: "downloads.start", Path: "/api/downloads", Access: scoped(Operate),
			Request: reflect.TypeFor[DownloadRequest](), Codes: []string{notConfigured}, Owners: searchOwners(),
			Summary: "Download a candidate", Description: "Starts a download job for a handle from instances.candidates; a handle starts one download. A resend with the same Idempotency-Key and body answers the same job.",
		}),
		jobStart(&httpwire.Endpoint{
			Name: "files.upload", Path: "/api/instances/{instance}/files", Access: scoped(Operate), MaxBody: uploadBody,
			Params: reflect.TypeFor[InstanceParams](), Request: reflect.TypeFor[UploadRequest](), Codes: []string{notConfigured}, Owners: searchOwners(),
			Summary: "Upload a subtitle", Description: "Starts a download job that delivers a subtitle file or archive you send for one item, target and variant, through the archive caps, parsing and the timing match, numbered and pinned, labelled Uploaded by you. bytes is the file in base64, at most 10,000,000 bytes decoded; the body is at most 13,398,872 bytes. A resend with the same Idempotency-Key and body answers the same job.",
		}),
		{
			Name: "files.setTiming", Method: http.MethodPut, Path: "/api/instances/{instance}/files/{name}/timing", Access: scoped(Operate), IfMatch: true, MaxBody: timingBody,
			Params: reflect.TypeFor[FileParams](), Request: reflect.TypeFor[TimingRequest](), Response: reflect.TypeFor[FileDoc](),
			Codes: []string{"proposal_unknown", notConfigured}, Owners: writeOwners(),
			Summary: "Save a file's timing", Description: "Retimes one subtitle file with exactly one of timing (a user edit) or proposal (the id of a finished files.sync job, whose server-held result is applied), sending the file's version as If-Match. A retry after a lost success answers 412 stale_version although the edit landed: reload the item. The body is at most 256 KiB.",
		},
		{
			Name: "files.render", Method: http.MethodPost, Path: "/api/instances/{instance}/files/{name}/render", Access: scoped(Read), Kind: httpwire.Raw, ContentType: "text/vtt", MaxBody: timingBody,
			Params: reflect.TypeFor[FileParams](), Request: reflect.TypeFor[RenderRequest](), Codes: []string{notConfigured}, Owners: writeOwners(),
			Summary: "Preview a timing as WebVTT", Description: "The file's cues under a proposed timing, as WebVTT for the 10-minute preview window starting at from_ms, shifted so the window starts at 0; send the from_ms of the instances.preview stream it overlays. Writes nothing. A POST because a piecewise timing does not fit a URL. The body is at most 256 KiB.",
		},
		jobStart(&httpwire.Endpoint{
			Name: "files.sync", Path: "/api/instances/{instance}/files/{name}/sync", Access: scoped(Operate),
			Params: reflect.TypeFor[FileParams](), Request: reflect.TypeFor[SyncRequest](), Codes: []string{notConfigured}, Owners: syncOwners(),
			Summary: "Propose a sync", Description: "Starts a sync job that proposes a timing for the file and writes nothing; read the result with jobs.proposal and apply it through files.setTiming.",
		}),
		{
			Name: "files.delete", Method: http.MethodDelete, Path: "/api/instances/{instance}/files/{name}", Access: scoped(Operate), IfMatch: true,
			Params: reflect.TypeFor[FileParams](), Codes: []string{notConfigured}, Owners: writeOwners(),
			Summary: "Delete a subtitle file", Description: "Removes the file while the version sent as If-Match still names it, else 412 stale_version. A VobSub .sub is listed but never deleted (422 format_unsupported), since its .idx would be orphaned. A retry after a lost success answers 412 stale_version although the file is gone: reload the item.",
		},
		jobStart(&httpwire.Endpoint{
			Name: "resyncs.start", Path: "/api/resyncs", Access: scoped(Operate), MaxBody: documentBody,
			Request: reflect.TypeFor[ResyncRequest](), Codes: []string{notConfigured}, Owners: syncOwners(),
			Summary: "Sync up to 500 files", Description: "Starts one job that syncs each listed file and writes only verified results. More than 500 files is bad_body at files. The body is at most 1 MiB, which holds 500 files with 255-byte names however the client escapes them.",
		}),
	}
}

func jobRows() []httpwire.Endpoint {
	return []httpwire.Endpoint{
		{
			Name: "status.get", Method: http.MethodGet, Path: "/api/status", Access: scoped(Read), Response: reflect.TypeFor[StatusDoc](),
			Summary: "Jobs, alerts and providers", Description: "The last published status: up to 50 jobs (unfinished first, newest started first, then the latest ended), the raised alerts, provider health and the settings counter settings_rev, at its boot id and seq. A changed settings_rev means settings changed and are in force: re-read session.get, languages.get, sources.list and mediaServers.list.",
		},
		{
			Name: "jobs.list", Method: http.MethodGet, Path: "/api/jobs", Access: scoped(Read), List: true,
			Query: reflect.TypeFor[ListQuery](), Response: reflect.TypeFor[JobPage](),
			Summary: "List unfinished jobs", Description: "Every unfinished job, newest started first, a page at a time, at the published boot id and seq; status.get's unfinished counts them. A job that ends between pages drops out without shifting the rest.",
		},
		{
			Name: "jobs.get", Method: http.MethodGet, Path: "/api/jobs/{id}", Access: scoped(Read),
			Params: reflect.TypeFor[JobParams](), Response: reflect.TypeFor[JobDoc](), Owners: statusOwners(),
			Summary: "One job", Description: "A job's state, progress and outcome, live or from its record, kept 24 hours after the job ends.",
		},
		{
			Name: "jobs.proposal", Method: http.MethodGet, Path: "/api/jobs/{id}/proposal", Access: scoped(Operate),
			Params: reflect.TypeFor[JobParams](), Response: reflect.TypeFor[ProposalDoc](), Codes: []string{"proposal_unknown"}, Owners: statusOwners(),
			Summary: "A sync job's proposal", Description: "The timing and outcome a finished files.sync job proposes, for its starter, kept 15 minutes.",
		},
		{
			Name: "jobs.cancel", Method: http.MethodDelete, Path: "/api/jobs/{id}", Access: scoped(Operate),
			Params: reflect.TypeFor[JobParams](), Response: reflect.TypeFor[JobDoc](), Codes: []string{"job_not_owned"}, Owners: statusOwners(),
			Summary: "Cancel a job", Description: "Asks a running job to stop; its starter or an admin may. Answers 200 with the job; status.get reports when it is Done.",
		},
	}
}

func configRows() []httpwire.Endpoint {
	return []httpwire.Endpoint{
		{
			Name: "languages.get", Method: http.MethodGet, Path: "/api/languages", Access: scoped(Read), Response: reflect.TypeFor[LanguagesDoc](),
			Summary: "Configured languages", Description: "The targets with their variants and English names, and the close-variety switch; empty outside active mode.",
		},
		{
			Name: "sources.list", Method: http.MethodGet, Path: "/api/sources", Access: scoped(Read), Response: reflect.TypeFor[[]SourceDoc](),
			Summary: "Media sources", Description: "Each configured source with its type, capabilities and alert; empty outside active mode.",
		},
		{
			Name: "sources.health", Method: http.MethodGet, Path: "/api/sources/{source}/health", Access: scoped(Admin),
			Params: reflect.TypeFor[SourceParams](), Response: reflect.TypeFor[SourceHealthDoc](), Codes: slices.Concat([]string{notConfigured}, sourceCodes),
			Summary: "Ping a source", Description: "Whether the source answers with its stored credentials, and the cause when it does not.",
		},
		{
			Name: "sources.rootFolders", Method: http.MethodGet, Path: "/api/sources/{source}/root-folders", Access: scoped(Admin),
			Params: reflect.TypeFor[SourceParams](), Response: reflect.TypeFor[RootFoldersDoc](), Codes: slices.Concat([]string{notConfigured}, sourceCodes),
			Summary: "A source's root folders", Description: "The source's root folder paths, for path mapping.",
		},
		jobStart(&httpwire.Endpoint{
			Name: "sources.scan", Path: "/api/sources/{source}/scan", Access: scoped(Admin),
			Params: reflect.TypeFor[SourceParams](), Codes: slices.Concat([]string{notConfigured}, scanSourceCodes), Owners: scanOwners(),
			Summary: "Scan a source", Description: "Starts a full scan job of one source.",
		}),
		{
			Name: "mediaServers.list", Method: http.MethodGet, Path: "/api/media-servers", Access: scoped(Read), Response: reflect.TypeFor[[]MediaServerDoc](),
			Summary: "Media servers", Description: "Each configured media server with its type and media_server alert; empty outside active mode.",
		},
		{
			Name: "settings.get", Method: http.MethodGet, Path: "/api/settings", Access: scoped(Admin), Response: reflect.TypeFor[SettingsDoc](),
			Summary: "Current settings", Description: "The settings document as JSON; every stored secret is null and its path listed in unchanged.",
		},
		{
			Name: "settings.save", Method: http.MethodPut, Path: "/api/settings", Access: scoped(Admin), MaxBody: documentBody,
			PersonActions: []string{httpwire.PersonSecretRetarget},
			Request:       reflect.TypeFor[SettingsSaveRequest](), Response: reflect.TypeFor[SettingsSaved](), Codes: []string{"settings_invalid"},
			Owners:  []*httpwire.Owner{settings.Owner},
			Summary: "Save settings", Description: "Validates, stores and activates the document; send a stored secret back as null with its path in unchanged to keep it. Sending a kept secret to a changed address needs a signed-in session. An API key cannot change the auth section. The body is at most 1 MiB.",
		},
		{
			Name: "settings.schemas", Method: http.MethodGet, Path: "/api/settings/schemas", Access: scoped(Admin), Response: reflect.TypeFor[SchemasDoc](),
			Summary: "Settings form schemas", Description: "The field schema of each source kind, media server kind and provider.",
		},
		{
			Name: "settings.test", Method: http.MethodPost, Path: "/api/settings/test", Access: scoped(Admin), MaxBody: documentBody,
			PersonActions: []string{httpwire.PersonSecretRetarget},
			Request:       reflect.TypeFor[SettingsTestRequest](), Response: reflect.TypeFor[TestResult](), Codes: []string{"settings_invalid"},
			Summary: "Test a connection", Description: "Asks the service of one unsaved source, media server or provider entry whether it accepts the credentials, taking each null secret from the stored entry; ok false carries the service's message. Sending a kept secret to a changed address needs a signed-in session. The body is at most 1 MiB.",
		},
	}
}
