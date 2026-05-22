// CLI subcommand specifications. The map below drives both per-command
// help (`subflux <cmd> --help`) and parse-time validation: unknown flags
// are rejected with a typo suggestion, required flags are enforced, and
// typed flags (int, duration) are validated for parseability.
//
// Adding a new subcommand: register it in handleCLI in main.go AND add
// an entry here. The two are intentionally separate so a missing entry
// simply falls back to "no help text" rather than blocking dispatch.

package main

import (
	"subflux/internal/cliparse"
)

// formatFlag is the shared spec entry for the --format passthrough on
// remote commands. Pretty-printed JSON is the default; --format json
// emits the server's response verbatim for jq / scripted consumers.
var formatFlag = cliparse.Flag{
	Name:    "format",
	Help:    "Output format: pretty (default, indented JSON) or json (server passthrough, single line)",
	Default: "pretty",
}

// cliSpecs lists all user-visible subcommands. The "health" subcommand
// is omitted because it is a Docker healthcheck internal, not a user
// command (it short-circuits before handleCLI in main.go).
var cliSpecs = map[string]cliparse.Spec{
	"search": {
		Name:     "search",
		Synopsis: "Search providers for a single media item",
		Help:     "Run a manual subtitle search against configured providers and optionally download the picked result.",
		Flags: []cliparse.Flag{
			{Name: "imdb", Help: "IMDb ID (e.g. tt0903747)"},
			{Name: "tmdb", Help: "TMDB ID"},
			{Name: "title", Help: "Title (free-text query)"},
			{Name: "lang", Help: "Language code", Default: "fr"},
			{Name: "pick", Help: "Number of top results to print", Type: "int", Default: "1"},
			{Name: "download", Help: "Download the picked subtitle", Type: "bool"},
		},
	},
	"status": {
		Name:     "status",
		Synopsis: "Print the running server's scan summary",
		Help:     "Fetches /api/state/stats from the running subflux server.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"state": {
		Name:     "state",
		Synopsis: "Print subtitle download history",
		Help:     "Fetches /api/state with optional filters. Defaults to limit=20.",
		Flags: []cliparse.Flag{
			{Name: "type", Help: "Filter by media type (episode|movie)"},
			{Name: "lang", Help: "Filter by language code"},
			{Name: "provider", Help: "Filter by provider name"},
			{Name: "limit", Help: "Max rows to return", Type: "int", Default: "20"},
			formatFlag,
		},
	},
	"backoff": {
		Name:     "backoff",
		Synopsis: "Inspect adaptive backoff state",
		Help:     "Lists per-provider failure counts and next-retry timestamps.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"locks": {
		Name:     "locks",
		Synopsis: "List manual download locks",
		Help:     "Manual downloads (non-top-pick) lock the item from automation.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"providers": {
		Name:     "providers",
		Synopsis: "List configured providers",
		Help:     "Shows provider names, priorities, and enabled state.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"unlock": {
		Name:     "unlock",
		Synopsis: "Remove a manual download lock",
		Help:     "Clears the lock so the next scan can re-evaluate this item.",
		Flags: []cliparse.Flag{
			{Name: "type", Help: "Media type (episode|movie)", Required: true},
			{Name: "id", Help: "Media id (e.g. tt0903747-s01e01)", Required: true},
			{Name: "lang", Help: "Language code", Required: true},
			formatFlag,
		},
	},
	"scan": {
		Name:     "scan",
		Synopsis: "Trigger a scan on the running server",
		Help:     "Returns 409 Conflict if a scan is already in progress.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"timeouts": {
		Name:     "timeouts",
		Synopsis: "Show provider circuit-breaker state",
		Help:     "Lists timed-out providers and remaining cooldown.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"timeouts-reset": {
		Name:     "timeouts-reset",
		Synopsis: "Clear all provider timeouts",
		Help:     "Re-enables every timed-out provider immediately.",
		Flags:    []cliparse.Flag{formatFlag},
	},
	"score": {
		Name:     "score",
		Synopsis: "Simulate scoring of a subtitle release",
		Help:     "Useful for debugging release-name parsing without running a full search.",
		Flags: []cliparse.Flag{
			{Name: "type", Help: "Media type (episode|movie)", Default: "episode"},
			{Name: "video", Help: "Video release name", Required: true},
			{Name: "sub", Help: "Subtitle release name", Required: true},
			{Name: "match", Help: "Identity match basis (imdb|tmdb)"},
			formatFlag,
		},
	},
	"reset-password": {
		Name:     "reset-password",
		Synopsis: "Reset a user's password (interactive)",
		Help:     "Prompts for the new password on stdin. Requires DB access (run on the host, not via the API).",
		Flags: []cliparse.Flag{
			{Name: "user", Help: "Username", Required: true},
		},
	},
	"generate-api-key": {
		Name:     "generate-api-key",
		Synopsis: "Generate a new API key for a user",
		Help:     "Prints the API key once; only the SHA-256 hash is stored.",
		Flags: []cliparse.Flag{
			{Name: "user", Help: "Username", Required: true},
			{Name: "label", Help: "Label for tracking key usage", Required: true},
		},
	},
}

// orderedSpecs returns cliSpecs sorted by name, suitable for root help.
func orderedSpecs() []cliparse.Spec {
	out := make([]cliparse.Spec, 0, len(cliSpecs))
	for _, s := range cliSpecs {
		out = append(out, s)
	}
	return cliparse.SortByName(out)
}
