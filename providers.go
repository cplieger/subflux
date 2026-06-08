// providers.go is the single registration point for all subtitle providers.
// Each provider's factory function and settings schema are registered here;
// adding a new provider requires one Register + one RegisterSchema call.
// No init(), no blank imports, no global state.
package main

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/embedded"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/hdbits"
	"github.com/cplieger/subflux/internal/provider/mock"
	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/subdl"
	"github.com/cplieger/subflux/internal/provider/subsource"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
)

// Provider name constants are sourced from api.ProviderName* (single source of truth).

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
	name    api.ProviderID
	label   string
	factory provider.FactoryFunc
	fields  []api.ProviderSchemaField
}

// providerEntries is the declarative list of all built-in providers.
var providerEntries = []providerEntry{
	{
		name: api.ProviderNameEmbedded, label: "Embedded", factory: embedded.Factory,
		fields: []api.ProviderSchemaField{
			{
				Key: "ignore_pgs", Label: "Ignore PGS", Type: fieldTypeBool, Default: fieldDefaultTrue,
				Help: "Exclude PGS bitmap subs from search results (Blu-ray). Still tracked in coverage.",
			},
			{
				Key: "ignore_vobsub", Label: "Ignore VobSub", Type: fieldTypeBool, Default: fieldDefaultTrue,
				Help: "Exclude VobSub bitmap subs from search results (DVD). Still tracked in coverage.",
			},
			{
				Key: "ignore_ass", Label: "Ignore ASS", Type: fieldTypeBool, Default: fieldDefaultFalse,
				Help: "Skip ASS/SSA styled subs (anime)",
			},
		},
	},
	{
		name: api.ProviderNameHDBits, label: "HDBits", factory: hdbits.Factory,
		fields: []api.ProviderSchemaField{
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
		name: api.ProviderNameOpenSubtitles, label: "OpenSubtitles", factory: opensubtitles.Factory,
		fields: []api.ProviderSchemaField{
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
				Default: fieldDefaultFalse, Help: "Include AI/machine-translated subs",
			},
		},
	},
	{
		name: api.ProviderNameBetaSeries, label: "BetaSeries", factory: betaseries.Factory,
		fields: []api.ProviderSchemaField{
			{
				Key: "token", Label: "Token", Type: fieldTypeSecret, Secret: true,
				Help: "From betaseries.com/en/account/api",
			},
		},
	},
	{
		name: api.ProviderNameGestdown, label: "Gestdown", factory: gestdown.Factory,
		fields: nil,
	},
	{
		name: api.ProviderNameSubSource, label: "SubSource", factory: subsource.Factory,
		fields: []api.ProviderSchemaField{
			{
				Key: fieldKeyAPIKey, Label: fieldLabelAPIKey, Type: fieldTypeSecret, Secret: true,
				Help: "From subsource.net API registration",
			},
		},
	},
	{
		name: api.ProviderNameSubDL, label: "SubDL", factory: subdl.Factory,
		fields: []api.ProviderSchemaField{
			{
				Key: fieldKeyAPIKey, Label: fieldLabelAPIKey, Type: fieldTypeSecret, Secret: true,
				Help: "From subdl.com API registration",
			},
		},
	},
	{
		name: api.ProviderNameAnimeTosho, label: "AnimeTosho", factory: animetosho.Factory,
		fields: []api.ProviderSchemaField{
			{
				Key: "anidb_client_key", Label: "AniDB Client Key", Type: fieldTypeSecret,
				Secret: true, Help: "Optional; enables AniDB episode ID search",
			},
		},
	},
	{
		name: api.ProviderNameYifySubtitles, label: "YIFY Subtitles", factory: yifysubtitles.Factory,
		fields: nil,
	},
	{
		name: api.ProviderNameMock, label: "Mock (Testing)", factory: mock.Factory,
		fields: mock.Schema(),
	},
}
