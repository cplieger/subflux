package wirespec_test

import (
	"cmp"
	"encoding/base64"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/go/httpwire"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/wirecodes"
	"github.com/cplieger/subflux/internal/wirespec"
)

// derived is the table with every row's union filled, failing t when
// Derive refuses it.
func derived(t *testing.T) *httpwire.Table {
	t.Helper()
	d, err := httpwire.Derive(wirespec.Table(), nil)
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	return d
}

func row(t *testing.T, tbl *httpwire.Table, name string) *httpwire.Endpoint {
	t.Helper()
	i := slices.IndexFunc(tbl.Endpoints, func(ep httpwire.Endpoint) bool { return ep.Name == name })
	if i < 0 {
		t.Fatalf("the table has no %s row", name)
	}
	return &tbl.Endpoints[i]
}

func TestTableDerives(t *testing.T) {
	derived(t)
}

func TestTableHasUpload(t *testing.T) {
	ep := row(t, wirespec.Table(), "files.upload")
	switch {
	case ep.Method != http.MethodPost || ep.Path != "/api/instances/{instance}/files":
		t.Errorf("files.upload is %s %s", ep.Method, ep.Path)
	case ep.Access != (httpwire.Access{Class: httpwire.Scoped, Scope: "operate"}):
		t.Errorf("files.upload access %+v, want Scoped at operate", ep.Access)
	case !ep.Keyed || ep.Status != http.StatusAccepted || ep.Response != reflect.TypeFor[wirespec.JobDoc]():
		t.Errorf("files.upload keyed %v at %d answering %v, want a Keyed 202 JobDoc", ep.Keyed, ep.Status, ep.Response)
	case !slices.Equal(ep.Owners, []*httpwire.Owner{search.Owner, status.Owner}):
		t.Errorf("files.upload owners %v, want search then status", ep.Owners)
	case ep.MaxBody != 13_398_872:
		t.Errorf("files.upload MaxBody %d, want 13,398,872", ep.MaxBody)
	}
	if !strings.Contains(ep.Description, "at most 10,000,000 bytes") {
		t.Errorf("files.upload's description does not state its decoded cap: %q", ep.Description)
	}
	if got := wirespec.UploadBytes(nil).MaxBytes(); got != 13_333_336 {
		t.Errorf("UploadBytes.MaxBytes = %d, want 13,333,336", got)
	}
	var u wirespec.UploadBytes
	if err := u.UnmarshalText(base64.StdEncoding.AppendEncode(nil, make([]byte, 10_000_000))); err != nil {
		t.Errorf("10,000,000 decoded bytes refused: %v", err)
	}
	if err := u.UnmarshalText(base64.StdEncoding.AppendEncode(nil, make([]byte, 10_000_001))); err == nil {
		t.Error("10,000,001 decoded bytes accepted")
	}
}

var jobStarts = []string{"downloads.start", "files.upload", "instances.search", "library.rescan", "sources.scan", "files.sync", "resyncs.start"}

func TestJobStarts(t *testing.T) {
	tbl := derived(t)
	if tbl.Window != 24*time.Hour {
		t.Errorf("Window %v, want 24h", tbl.Window)
	}
	for _, ep := range tbl.Endpoints {
		t.Run(ep.Name, func(t *testing.T) {
			if !slices.Contains(jobStarts, ep.Name) {
				if ep.Keyed {
					t.Error("Keyed, but not a job start")
				}
				if !slices.Contains(ep.Errors, "idempotency_key_unsupported") {
					t.Error("its union lacks idempotency_key_unsupported")
				}
				return
			}
			if !ep.Keyed || ep.Method != http.MethodPost || ep.Status != http.StatusAccepted ||
				ep.Response != reflect.TypeFor[wirespec.JobDoc]() || ep.MaxKept != 1000 {
				t.Errorf("keyed %v, %s at %d answering %v, MaxKept %d; want a Keyed POST 202 JobDoc keeping 1000",
					ep.Keyed, ep.Method, ep.Status, ep.Response, ep.MaxKept)
			}
		})
	}
}

func TestAdminSetPinned(t *testing.T) {
	var admin []string
	for _, ep := range wirespec.Table().Endpoints {
		if ep.Access.Scope == "admin" {
			admin = append(admin, ep.Name)
		}
	}
	slices.Sort(admin)
	want := []string{"settings.get", "settings.save", "settings.schemas", "settings.test", "sources.health", "sources.rootFolders", "sources.scan"}
	if !slices.Equal(admin, want) {
		t.Errorf("admin rows %v, want %v", admin, want)
	}
}

func TestOwnerCodesListed(t *testing.T) {
	tbl := derived(t)
	for name, code := range map[string]string{
		"instances.preview":    "media_root_unavailable",
		"files.delete":         "stale_version",
		"instances.search":     "instance_unknown",
		"instances.candidates": "target_unconfigured",
		"library.rescan":       "scan_capacity",
		"jobs.cancel":          "job_not_cancellable",
		"library.roots":        "cursor_invalid",
		"settings.save":        "activation_failed",
	} {
		if !slices.Contains(row(t, tbl, name).Errors, code) {
			t.Errorf("%s's union lacks %s", name, code)
		}
	}
}

func TestListedCodesOwned(t *testing.T) {
	tbl := wirespec.Table()
	for _, ep := range tbl.Endpoints {
		bare := ep
		bare.Codes = nil
		given := httpwire.Union(tbl, &bare, nil)
		for _, code := range ep.Codes {
			if slices.Contains(given, code) {
				t.Errorf("%s lists %s, which its owners or features already give", ep.Name, code)
			}
		}
	}
}

func TestFileWritesTakeIfMatch(t *testing.T) {
	tbl := wirespec.Table()
	for _, name := range []string{"files.setTiming", "files.delete"} {
		if !row(t, tbl, name).IfMatch {
			t.Errorf("%s is not an IfMatch row", name)
		}
	}
	if q := row(t, tbl, "files.delete").Query; q != nil {
		t.Errorf("files.delete takes query %v, want none", q)
	}
	var withVersion []string
	for _, ep := range tbl.Endpoints {
		for _, typ := range []reflect.Type{ep.Params, ep.Query, ep.Request, ep.Response} {
			walk(typ, map[reflect.Type]bool{}, func(f reflect.StructField) {
				if f.Name == "Expect" {
					t.Errorf("%s reaches a field Expect of type %v", ep.Name, f.Type)
				}
				if f.Type == reflect.TypeFor[httpwire.Tag]() && !slices.Contains(withVersion, ep.Name) {
					withVersion = append(withVersion, ep.Name)
				}
			})
		}
	}
	for _, name := range []string{"library.item", "files.setTiming", "files.render", "files.sync", "resyncs.start", "jobs.proposal"} {
		if !slices.Contains(withVersion, name) {
			t.Errorf("%s carries no file version", name)
		}
	}
}

func TestNoOpsOrUIRows(t *testing.T) {
	for _, ep := range wirespec.Table().Endpoints {
		if ep.Name == "meta.health" || ep.Name == "meta.metrics" {
			t.Errorf("the table holds the ops row %s", ep.Name)
		}
		switch ep.Path {
		case "/healthz", "/api/health", "/metrics", "/":
			t.Errorf("%s serves %s, an ops or UI path", ep.Name, ep.Path)
		}
	}
}

func TestBodyCaps(t *testing.T) {
	tbl := derived(t)
	for name, want := range map[string]int{
		"files.setTiming": 256 << 10,
		"files.render":    256 << 10,
		"resyncs.start":   1 << 20,
		"settings.save":   1 << 20,
		"settings.test":   1 << 20,
		"files.upload":    13_398_872,
		"downloads.start": 64 << 10,
	} {
		if got := cmp.Or(row(t, tbl, name).MaxBody, tbl.MaxBody); got != want {
			t.Errorf("%s MaxBody %d, want %d", name, got, want)
		}
	}
}

func TestCatalogCodesUsed(t *testing.T) {
	var used []string
	for _, ep := range derived(t).Endpoints {
		used = append(used, ep.Errors...)
	}
	for _, code := range wirecodes.Own() {
		if !slices.Contains(used, code) {
			t.Errorf("no row's union holds %s", code)
		}
	}
}

// walk calls f on every struct field reachable from typ.
func walk(typ reflect.Type, seen map[reflect.Type]bool, f func(reflect.StructField)) {
	if typ == nil || seen[typ] {
		return
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		walk(typ.Elem(), seen, f)
	case reflect.Struct:
		for field := range typ.Fields() {
			f(field)
			walk(field.Type, seen, f)
		}
	}
}
