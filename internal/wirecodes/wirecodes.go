// Package wirecodes holds subflux's one error catalog: every code any of its
// listeners answers, httpwire's and httpserver's included. It imports no
// subflux package, so the settings host policy and the app's listeners reach
// it without importing internal/wirespec, which imports them.
package wirecodes

import (
	"net/http"

	"github.com/cplieger/go/httpserver/v2"
	"github.com/cplieger/go/httpwire"
)

const allowedHostsRemediation = "Open subflux at a host server.allowed_hosts lists, or add this host to it."

// Catalog is the catalog every listener and the API answer through.
var Catalog = mustCatalog()

// code is one code subflux adds, with its catalog entry.
type code struct {
	name string
	spec httpserver.CodeSpec
}

func refused(name string, status int, remediation string) code {
	return code{name, httpserver.CodeSpec{Status: status, Remediation: remediation, NoEffect: true}}
}

// codes are subflux's own codes; httpwire.NewCatalog holds the rest.
var codes = []code{
	refused("not_configured", http.StatusServiceUnavailable, "Finish setup in Settings, then retry."),
	// Jobs.
	refused("job_unrecorded", http.StatusServiceUnavailable, "Retry after the Retry-After delay; the job record could not be stored, so nothing started."),
	refused("job_capacity", http.StatusTooManyRequests, "Wait for running jobs to finish: every stored job record in your bound is unfinished."),
	refused("job_unknown", http.StatusNotFound, "List jobs at /api/jobs; records are kept 24 hours after a job ends."),
	refused("job_not_cancellable", http.StatusConflict, "The job has ended or cannot stop; read its outcome instead."),
	refused("job_not_owned", http.StatusForbidden, "Only the job's starter or an admin can cancel it."),
	// Library and files.
	refused("root_unknown", http.StatusNotFound, "List roots at /api/library/roots."),
	refused("item_unknown", http.StatusNotFound, "List a root's items at /api/library/roots/{root}/items."),
	refused("instance_unknown", http.StatusNotFound, "Read the item document for its current instance id."),
	refused("write_pending", http.StatusConflict, "Retry after the Retry-After delay; a write to this file is finishing."),
	refused("media_root_unavailable", http.StatusServiceUnavailable, "Retry once the media root is mounted again; the media_root alert names it."),
	refused("outside_root", http.StatusUnprocessableEntity, "Map the video's folder into a media root in the source's path mappings."),
	refused("not_lockable", http.StatusConflict, "Lock a configured target and variant that a file of this item serves."),
	refused("timing_invalid", http.StatusBadRequest, "Keep offsets within six hours, scale within 0.9 to 1.1, and segments contiguous from 0."),
	refused("format_unsupported", http.StatusUnprocessableEntity, "subflux reads SRT, ASS, SSA and WebVTT only."),
	refused("proposal_unknown", http.StatusNotFound, "Propose the sync again: a proposal is kept 15 minutes after its job ends, for its starter."),
	refused("sync_capacity", http.StatusTooManyRequests, "Wait for queued sync jobs to finish, then retry."),
	refused("preview_capacity", http.StatusTooManyRequests, "Close another preview, then retry."),
	refused("preview_range", http.StatusBadRequest, "Seek within 0 to 24 hours."),
	refused("preview_replaced", http.StatusConflict, "Nothing to do: a newer preview from the same user replaced this one."),
	// Search.
	refused("target_unconfigured", http.StatusUnprocessableEntity, "Pick a target listed at /api/languages."),
	refused("variant_unserved", http.StatusUnprocessableEntity, "Pick a variant the target lists and the candidate's flags serve."),
	refused("candidate_unknown", http.StatusNotFound, "Search again: a candidate handle lives 15 minutes and starts one download."),
	refused("search_capacity", http.StatusTooManyRequests, "Retry after the Retry-After delay; too many candidate searches are held."),
	// Sources and settings.
	refused("source_unknown", http.StatusNotFound, "List sources at /api/sources."),
	refused("source_ambiguous", http.StatusConflict, "Name one of the sources fields lists in the source query parameter."),
	refused("source_failed", http.StatusBadGateway, "Check the source's URL and API key; reason names the cause."),
	refused("source_unsupported", http.StatusBadRequest, "This source does not serve that kind of root."),
	refused("scan_capacity", http.StatusTooManyRequests, "Wait for this source's queued scans to start, then retry."),
	refused("settings_invalid", http.StatusBadRequest, "Fix the settings listed in fields and save again."),
	refused("activation_failed", http.StatusInternalServerError, "The previous settings stay live; check the store alert, then save again."),
	refused("settings_closing", http.StatusServiceUnavailable, "subflux is shutting down; save again after it restarts."),
}

// Own answers the codes subflux adds to the catalog, in order.
//
//deadset:ignore DS1102 -- internal/wirespec's test checks every row-less code against the table; no production caller needs the list.
func Own() []string {
	out := make([]string, len(codes))
	for i, x := range codes {
		out[i] = x.name
	}
	return out
}

// mustCatalog panics on a refusal: its input is constant, so a refusal is a
// build bug every test run reports.
func mustCatalog() *httpwire.Catalog {
	c := httpwire.NewCatalog()
	for _, x := range codes {
		if err := c.Add(x.name, x.spec); err != nil {
			panic(err)
		}
	}
	if err := c.SetRemediation("host_not_allowed", allowedHostsRemediation); err != nil {
		panic(err)
	}
	return c
}
