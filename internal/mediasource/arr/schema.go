// Package arr is what the Sonarr and Radarr adapters share: their form,
// error classifier, history cursor, tags, root folders and the poster's
// two-zone fetch. arrapi v3's sonarr and radarr clients have no common type, so arr
// takes their values and functions, never a client.
package arr

import (
	"errors"
	"net/url"

	"github.com/cplieger/subflux/internal/formschema"
)

// Values is an arr entry's decoded settings.
type Values struct{ URL, APIKey, PublicURL string }

// LinkBase is the base a root's Link is built under: public_url, else url.
func (v Values) LinkBase() string {
	if v.PublicURL != "" {
		return v.PublicURL
	}
	return v.URL
}

// Schema is the form both apps share: url and api_key required, public_url
// optional; app names the application in labels and help.
func Schema(app string) formschema.Schema {
	return formschema.Schema{Fields: []formschema.Field{
		{Key: "url", Label: app + " URL", Help: "The address subflux reaches " + app + " at.", Kind: formschema.String, Required: true},
		{Key: "api_key", Label: "API key", Help: app + " > Settings > General > API Key.", Kind: formschema.Secret, Required: true},
		{Key: "public_url", Label: "Public URL", Help: "The address your browser opens " + app + " at; enables links to its pages.", Kind: formschema.String},
	}}
}

var errBadPublicURL = errors.New("public_url must be an absolute http or https URL with a host and no query or fragment")

// ReadValues reads an entry decoded by Schema, whose Required fields refuse
// an absent or empty url and api_key. It refuses a public_url that is not
// an absolute http or https URL with a host and no query or fragment,
// naming the rule and never the value; url is the arrapi client's to check,
// whose refusals name no URL or key either.
func ReadValues(vals formschema.Values) (Values, error) {
	v := Values{URL: vals.String("url"), APIKey: vals.Secret("api_key"), PublicURL: vals.String("public_url")}
	if v.PublicURL != "" {
		u, err := url.Parse(v.PublicURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			return Values{}, errBadPublicURL
		}
	}
	return v, nil
}
