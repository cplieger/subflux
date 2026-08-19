// providers.go is the single registration point for all subtitle providers.
// Each provider's factory function and settings schema are registered here;
// adding a new provider requires one Register + one RegisterSchema call.
// No init(), no blank imports, no global state.
package main

import (
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/subdl"
	"github.com/cplieger/subflux/internal/provider/subsource"
	"github.com/cplieger/subflux/internal/provider/synthetic"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
	"github.com/cplieger/subflux/internal/subflux"
)

// Provider name constants are sourced from subflux.ProviderName* (single source of truth).

// Provider settings field type constants.
const (
	fieldTypeBool   = "bool"
	fieldTypeSecret = "secret"
	fieldTypeText   = "text"

	fieldDefaultTrue   = "true"
	fieldDefaultFalse  = "false"
	fieldLabelAPIKey   = "API Key"
	fieldKeyAPIKey     = "api_key"
	fieldLabelUsername = "Username"
	fieldKeyUsername   = "username"
)

// newProviderRegistry creates a registry with all built-in providers.
// Adding a new provider = one entry in the providerEntries table.
func newProviderRegistry() *provider.Registry {
	r := provider.NewRegistry()
	for _, e := range providerEntries {
		r.Register(e.name, e.factory)
		r.RegisterSchema(e.name, e.label, e.fields)
	}
	return r
}

// providerEntry describes a single provider for table-driven registration.
type providerEntry struct {
	name    subflux.ProviderID
	label   string
	factory provider.FactoryFunc
	fields  []subflux.ProviderSchemaField
}

// providerEntries is the declarative list of all built-in providers.
var providerEntries = []providerEntry{
	{
		name: subflux.ProviderNameHDBits, label: "HDBits", factory: hdbits.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: fieldKeyUsername, Label: fieldLabelUsername, Type: fieldTypeText,
				Help: "hdbits.org account",
			},
			{
				Key: "passkey", Label: "Passkey", Type: fieldTypeSecret, Secret: true,
				Help: "From hdbits.org user settings",
			},
		},
	},
	{
		name: subflux.ProviderNameOpenSubtitles, label: "OpenSubtitles", factory: opensubtitles.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: fieldKeyUsername, Label: fieldLabelUsername, Type: fieldTypeText,
				Help: "opensubtitles.com account",
			},
			{
				Key: "password", Label: "Password", Type: fieldTypeSecret, Secret: true,
				Help: "opensubtitles.com password",
			},
			{
				Key: fieldKeyAPIKey, Label: fieldLabelAPIKey, Type: fieldTypeSecret, Secret: true,
				Help: "From opensubtitles.com/consumers",
			},
			{
				Key: "use_hash", Label: "Use Hash", Type: fieldTypeBool, Default: fieldDefaultTrue,
				Help: "Match by file hash (fast, exact)",
			},
			{
				Key: "include_ai_translated", Label: "Include AI Translated", Type: fieldTypeBool,
				Default: fieldDefaultFalse,
				Help:    "Include subs from OpenSubtitles' own AI translation pipeline",
			},
			{
				Key: "include_machine_translated", Label: "Include Machine Translated",
				Type: fieldTypeBool, Default: fieldDefaultFalse,
				Help: "Include older machine-translated uploads (Google Translate era)",
			},
		},
	},
	{
		name: subflux.ProviderNameBetaSeries, label: "BetaSeries", factory: betaseries.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: "token", Label: "Token", Type: fieldTypeSecret, Secret: true,
				Help: "From betaseries.com/en/account/api",
			},
		},
	},
	{
		name: subflux.ProviderNameGestdown, label: "Gestdown", factory: gestdown.Factory,
		fields: nil,
	},
	{
		name: subflux.ProviderNameSubSource, label: "SubSource", factory: subsource.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: fieldKeyAPIKey, Label: fieldLabelAPIKey, Type: fieldTypeSecret, Secret: true,
				Help: "From subsource.net API registration",
			},
		},
	},
	{
		name: subflux.ProviderNameSubDL, label: "SubDL", factory: subdl.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: fieldKeyAPIKey, Label: fieldLabelAPIKey, Type: fieldTypeSecret, Secret: true,
				Help: "From subdl.com API registration",
			},
		},
	},
	{
		name: subflux.ProviderNameAnimeTosho, label: "AnimeTosho", factory: animetosho.Factory,
		fields: []subflux.ProviderSchemaField{
			{
				Key: "anidb_client_key", Label: "AniDB Client Key", Type: fieldTypeSecret,
				Secret: true, Help: "Optional; enables AniDB episode ID search",
			},
		},
	},
	{
		name: subflux.ProviderNameYifySubtitles, label: "YIFY Subtitles", factory: yifysubtitles.Factory,
		fields: nil,
	},
	{
		name: subflux.ProviderNameSynthetic, label: "Synthetic (Testing)", factory: synthetic.Factory,
		fields: synthetic.Schema(),
	},
}
