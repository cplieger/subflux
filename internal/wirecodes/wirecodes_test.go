package wirecodes_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/wirecodes"
)

// subfluxCodes is each code subflux answers beyond httpwire's and
// httpserver's, with its status and remediation (api design section 9's
// catalog).
var subfluxCodes = map[string]struct {
	status      int
	remediation string
}{
	"not_configured":         {http.StatusServiceUnavailable, "Finish setup in Settings, then retry."},
	"job_unrecorded":         {http.StatusServiceUnavailable, "Retry after the Retry-After delay; the job record could not be stored, so nothing started."},
	"job_capacity":           {http.StatusTooManyRequests, "Wait for running jobs to finish: every stored job record in your bound is unfinished."},
	"job_unknown":            {http.StatusNotFound, "List jobs at /api/jobs; records are kept 24 hours after a job ends."},
	"job_not_cancellable":    {http.StatusConflict, "The job has ended or cannot stop; read its outcome instead."},
	"job_not_owned":          {http.StatusForbidden, "Only the job's starter or an admin can cancel it."},
	"root_unknown":           {http.StatusNotFound, "List roots at /api/library/roots."},
	"item_unknown":           {http.StatusNotFound, "List a root's items at /api/library/roots/{root}/items."},
	"instance_unknown":       {http.StatusNotFound, "Read the item document for its current instance id."},
	"write_pending":          {http.StatusConflict, "Retry after the Retry-After delay; a write to this file is finishing."},
	"media_root_unavailable": {http.StatusServiceUnavailable, "Retry once the media root is mounted again; the media_root alert names it."},
	"outside_root":           {http.StatusUnprocessableEntity, "Map the video's folder into a media root in the source's path mappings."},
	"not_lockable":           {http.StatusConflict, "Lock a configured target and variant that a file of this item serves."},
	"timing_invalid":         {http.StatusBadRequest, "Keep offsets within six hours, scale within 0.9 to 1.1, and segments contiguous from 0."},
	"format_unsupported":     {http.StatusUnprocessableEntity, "subflux reads SRT, ASS, SSA and WebVTT only."},
	"proposal_unknown":       {http.StatusNotFound, "Propose the sync again: a proposal is kept 15 minutes after its job ends, for its starter."},
	"sync_capacity":          {http.StatusTooManyRequests, "Wait for queued sync jobs to finish, then retry."},
	"preview_capacity":       {http.StatusTooManyRequests, "Close another preview, then retry."},
	"preview_range":          {http.StatusBadRequest, "Seek within 0 to 24 hours."},
	"preview_replaced":       {http.StatusConflict, "Nothing to do: a newer preview from the same user replaced this one."},
	"target_unconfigured":    {http.StatusUnprocessableEntity, "Pick a target listed at /api/languages."},
	"variant_unserved":       {http.StatusUnprocessableEntity, "Pick a variant the target lists and the candidate's flags serve."},
	"candidate_unknown":      {http.StatusNotFound, "Search again: a candidate handle lives 15 minutes and starts one download."},
	"search_capacity":        {http.StatusTooManyRequests, "Retry after the Retry-After delay; too many candidate searches are held."},
	"source_unknown":         {http.StatusNotFound, "List sources at /api/sources."},
	"source_ambiguous":       {http.StatusConflict, "Name one of the sources fields lists in the source query parameter."},
	"source_failed":          {http.StatusBadGateway, "Check the source's URL and API key; reason names the cause."},
	"source_unsupported":     {http.StatusBadRequest, "This source does not serve that kind of root."},
	"scan_capacity":          {http.StatusTooManyRequests, "Wait for this source's queued scans to start, then retry."},
	"settings_invalid":       {http.StatusBadRequest, "Fix the settings listed in fields and save again."},
	"activation_failed":      {http.StatusInternalServerError, "The previous settings stay live; check the store alert, then save again."},
	"settings_closing":       {http.StatusServiceUnavailable, "subflux is shutting down; save again after it restarts."},
}

func TestCatalog(t *testing.T) {
	own := wirecodes.Own()
	if len(own) != len(subfluxCodes) {
		t.Errorf("subflux adds %d codes, want %d", len(own), len(subfluxCodes))
	}
	for code, want := range subfluxCodes {
		t.Run(code, func(t *testing.T) {
			if !slices.Contains(own, code) {
				t.Errorf("Own lacks %s", code)
			}
			spec, ok := wirecodes.Catalog.Lookup(code)
			switch {
			case !ok:
				t.Fatal("not in the catalog")
			case spec.Status != want.status:
				t.Errorf("status %d, want %d", spec.Status, want.status)
			case !spec.NoEffect:
				t.Error("NoEffect false: a refused request changed something")
			case spec.Remediation != want.remediation:
				t.Errorf("remediation %q, want %q", spec.Remediation, want.remediation)
			}
		})
	}
	host, ok := wirecodes.Catalog.Lookup("host_not_allowed")
	if want := "Open subflux at a host server.allowed_hosts lists, or add this host to it."; !ok || host.Remediation != want || host.Status != http.StatusForbidden {
		t.Errorf("host_not_allowed = %+v, %v; want 403 with %q", host, ok, want)
	}
}
