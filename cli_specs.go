// CLI subcommand specifications. The map below drives dispatch, per-command
// help (`subflux <cmd> --help`), and parse-time validation: unknown flags
// are rejected with a typo suggestion, required flags are enforced, typed
// flags (int, bool, duration) are validated, and defaults are applied — all
// in the single cliparse.ParseAndValidate pass whose result flows into each
// entry's Run.
//
// Adding a new subcommand: add an entry here with a Run function. Hidden
// entries (sync-worker) stay dispatchable but are excluded from root help.

package main

import (
	"github.com/cplieger/subflux/internal/apipaths"
	"github.com/cplieger/subflux/internal/cliparse"
)

const (
	cmdBackoff        = "backoff"
	cmdLocks          = "locks"
	cmdProviders      = "providers"
	cmdScan           = "scan"
	cmdResetPassword  = "reset-password"
	cmdEnablePwLogin  = "enable-password-login"
	cmdGenerateAPIKey = "generate-api-key"
	cmdLang           = "lang"
	cmdSearch         = "search"
	flagProvider      = "provider"
	flagSeason        = "season"
	flagEpisode       = "episode"
	cmdStatus         = "status"
	cmdState          = "state"
	cmdSyncWorker     = "sync-worker"
	cmdUnlock         = "unlock"
	cmdTimeouts       = "timeouts"
	cmdTimeoutsReset  = "timeouts-reset"
	cmdScore          = "score"
	cmdType           = "type"
)

// formatFlag is the shared spec entry for the --format passthrough on
// remote commands. Pretty-printed JSON is the default; --format json
// emits the server's response verbatim for jq / scripted consumers.
var formatFlag = cliparse.Flag{
	Name:    "format",
	Help:    "Output format: pretty for indented JSON, or json for the server response as one line",
	Default: "pretty",
}

// cliSpecs lists all subcommands. The "health" subcommand is omitted
// because it is a Docker healthcheck internal, not a user command (it
// short-circuits before handleCLI in main.go). Every entry carries its
// Run function; handleCLI parses os.Args exactly once and hands the
// validated params to it.
var cliSpecs = map[string]cliparse.Spec{
	cmdSearch: {
		Name:     cmdSearch,
		Synopsis: "Search subtitles for a media item via the running server",
		Help: "Remote command: resolves the media in the server's Sonarr/Radarr libraries, " +
			"searches all configured providers through GET /api/search, and prints scored results. " +
			"--download saves the picked result server-side and reports the saved path. " +
			"The top pick keeps automation, and any other pick locks the item.\n\n" +
			"Requires a running subflux server. Set SUBFLUX_URL (default http://127.0.0.1:8374), " +
			"and set SUBFLUX_API_KEY when the server has auth enabled.",
		Run: runCLISearchRemote,
		Flags: []cliparse.Flag{
			{Name: "imdb", Help: "IMDb ID, for example tt0903747"},
			{Name: "tmdb", Help: "TMDB ID, movies only"},
			{Name: "title", Help: "Title to match exactly, ignoring case"},
			{Name: cmdType, Help: "Media type, series or movie. Without it, series is tried first, then movie"},
			{Name: flagSeason, Help: "Limit series expansion to a season", Type: cliparse.TypeInt},
			{Name: flagEpisode, Help: "Limit series expansion to an episode number", Type: cliparse.TypeInt},
			{Name: cmdLang, Help: "Language code", Default: "fr"},
			{Name: "pick", Help: "Result index to download with --download. 1 is the top pick", Type: cliparse.TypeInt, Default: "1"},
			{Name: "download", Help: "Download the picked subtitle", Type: cliparse.TypeBool},
		},
	},
	cmdStatus: {
		Name:     cmdStatus,
		Synopsis: "Print the running server's scan summary",
		Help:     "Fetches /api/state/stats from the running subflux server.",
		Run:      func(p cliparse.Params) int { return runCLIRemote(p, apipaths.PathStateStats) },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdState: {
		Name:     cmdState,
		Synopsis: "Print subtitle download history",
		Help:     "Fetches /api/state with optional filters. Defaults to limit=20.",
		Run:      runCLIState,
		Flags: []cliparse.Flag{
			{Name: cmdType, Help: "Filter by media type, episode or movie"},
			{Name: cmdLang, Help: "Filter by language code"},
			{Name: flagProvider, Help: "Filter by provider name"},
			{Name: "limit", Help: "Max rows to return", Type: cliparse.TypeInt, Default: "20"},
			formatFlag,
		},
	},
	cmdBackoff: {
		Name:     cmdBackoff,
		Synopsis: "Inspect adaptive backoff state",
		Help:     "Lists per-provider failure counts and next-retry timestamps.",
		Run:      func(p cliparse.Params) int { return runCLIRemote(p, apipaths.PathListBackoff) },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdLocks: {
		Name:     cmdLocks,
		Synopsis: "List manual download locks",
		Help:     "A manual download of any result other than the top pick locks the item from automation.",
		Run:      func(p cliparse.Params) int { return runCLIRemote(p, apipaths.PathListLocks) },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdProviders: {
		Name:     cmdProviders,
		Synopsis: "List configured providers",
		Help:     "Shows provider names, priorities, and enabled state.",
		Run:      func(p cliparse.Params) int { return runCLIRemote(p, apipaths.PathListProviders) },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdUnlock: {
		Name:     cmdUnlock,
		Synopsis: "Remove a manual download lock",
		Help:     "Clears the lock so the next scan can re-evaluate this item. Locks are held per variant. Omit --variant to clear all variants for the language.",
		Run:      runCLIUnlock,
		Flags: []cliparse.Flag{
			{Name: cmdType, Help: "Media type, episode or movie", Required: true},
			{Name: "id", Help: "Media ID, for example tt0903747-s01e01", Required: true},
			{Name: cmdLang, Help: "Language code", Required: true},
			{Name: "variant", Help: "Variant to unlock: standard, hi or forced. Default is all variants"},
			formatFlag,
		},
	},
	cmdScan: {
		Name:     cmdScan,
		Synopsis: "Trigger a scan on the running server",
		Help:     "Returns 409 Conflict if a scan is already in progress.",
		Run:      func(p cliparse.Params) int { return runCLIAction(p, apipaths.PathStartScan, "scan started") },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdTimeouts: {
		Name:     cmdTimeouts,
		Synopsis: "Show provider circuit-breaker state",
		Help:     "Lists timed-out providers and remaining cooldown.",
		Run:      func(p cliparse.Params) int { return runCLIRemote(p, apipaths.PathProviderTimeouts) },
		Flags:    []cliparse.Flag{formatFlag},
	},
	cmdTimeoutsReset: {
		Name:     cmdTimeoutsReset,
		Synopsis: "Clear all provider timeouts and credential disables",
		Help:     "Re-enables every timed-out, rate-limited or credential-disabled provider immediately.",
		Run: func(p cliparse.Params) int {
			return runCLIAction(p, apipaths.PathResetProviderTimeouts,
				"provider timeouts reset, all providers re-enabled")
		},
		Flags: []cliparse.Flag{formatFlag},
	},
	cmdScore: {
		Name:     cmdScore,
		Synopsis: "Simulate scoring of a subtitle release",
		Help:     "Useful for debugging release-name parsing without running a full search.",
		Run:      runCLIScore,
		Flags: []cliparse.Flag{
			{Name: cmdType, Help: "Media type, episode or movie", Default: "episode"},
			{Name: "video", Help: "Video release name", Required: true},
			{Name: "sub", Help: "Subtitle release name", Required: true},
			{Name: "match", Help: "Identity match basis, imdb or tmdb"},
			formatFlag,
		},
	},
	cmdResetPassword: {
		Name:     cmdResetPassword,
		Synopsis: "Reset an existing user's password interactively",
		Help:     "Prompts for the new password on stdin, then applies it through the running server's private admin socket at /tmp/subflux-admin/admin.sock. The store cannot be opened directly, because bbolt holds an exclusive file lock. Run it inside the server's container, for example with docker exec. The user must already exist.",
		Run:      runCLIResetPassword,
		Flags: []cliparse.Flag{
			{Name: "user", Help: "Username", Required: true},
		},
	},
	cmdGenerateAPIKey: {
		Name:     cmdGenerateAPIKey,
		Synopsis: "Generate a new API key for a user",
		Help:     "Prints the API key once. Only its SHA-256 hash is stored. Applies through the running server's private admin socket at /tmp/subflux-admin/admin.sock, so run it inside the server's container, for example with docker exec.",
		Run:      runCLIGenerateAPIKey,
		Flags: []cliparse.Flag{
			{Name: "user", Help: "Username", Required: true},
			{Name: "label", Help: "Label for tracking key usage", Required: true},
		},
	},
	cmdEnablePwLogin: {
		Name:     cmdEnablePwLogin,
		Synopsis: "Re-enable password login to recover from a lockout",
		Help:     "Sets auth.basic_enabled: true in the config file. Use when password login was disabled and OIDC is unavailable. Restart subflux to apply.",
		Run:      runCLIEnablePasswordLogin,
	},
	// Hidden: the P13 sync-worker child process the server spawns for
	// isolated sync jobs — dispatchable, but not a user command.
	cmdSyncWorker: {
		Name:     cmdSyncWorker,
		Synopsis: "(internal) run one subtitle sync job from stdin",
		Hidden:   true,
		Run:      func(cliparse.Params) int { return runSyncWorker() },
	},
}

// orderedSpecs returns the non-hidden cliSpecs sorted by name, suitable
// for root help.
func orderedSpecs() []cliparse.Spec {
	out := make([]cliparse.Spec, 0, len(cliSpecs))
	for _, s := range cliSpecs {
		if s.Hidden {
			continue
		}
		out = append(out, s)
	}
	return cliparse.SortByName(out)
}
