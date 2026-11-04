package arr_test

import (
	"testing"

	"github.com/cplieger/arrapi/v3"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource/arr"
	"go.yaml.in/yaml/v3"
)

func decodeArr(t *testing.T, doc string) formschema.Values {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	vals, errs := arr.Schema("Sonarr").Decode(n.Content[0])
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return vals
}

func TestReadValues(t *testing.T) {
	v, err := arr.ReadValues(decodeArr(t, "url: http://sonarr:8989\napi_key: k\npublic_url: https://sonarr.example.com/base"))
	if err != nil || v != (arr.Values{URL: "http://sonarr:8989", APIKey: "k", PublicURL: "https://sonarr.example.com/base"}) {
		t.Errorf("ReadValues = %+v %v", v, err)
	}
	for _, bad := range []string{"ftp://s.example", "/relative", "https://", "https://s.example/?q=1", "https://s.example/#x", "https://s.example/?"} {
		if _, err := arr.ReadValues(decodeArr(t, "url: http://s\napi_key: k\npublic_url: '"+bad+"'")); err == nil {
			t.Errorf("public_url %q accepted", bad)
		}
	}
	if _, err := arr.ReadValues(formschema.Values{}); err == nil {
		t.Error("ReadValues of nothing accepted")
	}
}

func TestSchemaSecrets(t *testing.T) {
	for _, f := range arr.Schema("Radarr").Fields {
		if (f.Key == "api_key") != (f.Kind == formschema.Secret) {
			t.Errorf("field %s kind %v: only api_key is a secret", f.Key, f.Kind)
		}
	}
}

func TestTagsAndFolders(t *testing.T) {
	if got := arr.RootFolders([]string{"/tv/", "/movies", "/", "//anime//"}); len(got) != 4 || got[0] != "/tv" || got[1] != "/movies" || got[2] != "/" || got[3] != "//anime" {
		t.Errorf("RootFolders = %q, want trailing slashes dropped, / kept", got)
	}
	if arr.Native(42) != "42" {
		t.Errorf("Native(42) = %q", arr.Native(42))
	}
	tags := arr.Tags([]arrapi.Tag{{ID: 3, Label: "No-Subflux"}, {ID: 12, Label: "anime"}})
	if len(tags) != 2 || tags["3"] != "No-Subflux" || tags["12"] != "anime" {
		t.Errorf("Tags = %v, want each label keyed by its decimal id", tags)
	}
}
