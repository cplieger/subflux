package schema

import (
	"strconv"

	"github.com/cplieger/subflux/internal/config/defaults"
	"github.com/cplieger/subflux/internal/subflux"
)

func searchSection() subflux.SchemaSection {
	return subflux.SchemaSection{
		Key: "search", Title: "Search", Type: fieldFields,
		Fields: []subflux.SchemaField{
			{
				Key: "scan_interval", Label: "Scan Interval", Type: fieldDuration,
				Default:     formatDuration(defaults.DefaultScanInterval),
				Placeholder: formatDuration(defaults.DefaultScanInterval),
				Min:         formatDuration(defaults.MinScanInterval),
				Help:        "Time between full library scans (minimum 1h)",
			},
			{
				Key: "scan_delay", Label: "Scan Delay", Type: fieldDuration,
				Default:     formatDuration(defaults.DefaultScanDelay),
				Placeholder: formatDuration(defaults.DefaultScanDelay),
				Min:         formatDuration(defaults.MinScanDelay),
				Help:        "Delay after each item that queried providers during a scan, so providers are not overloaded (minimum 5s). Items that need no provider work skip the delay.",
			},
			{
				Key: "provider_timeout", Label: "Provider Timeout", Type: fieldDuration,
				Default:     formatDuration(defaults.DefaultProviderTimeout),
				Placeholder: formatDuration(defaults.DefaultProviderTimeout),
				Min:         formatDuration(defaults.MinProviderTimeout),
				Help:        "Cooldown after a provider fails repeatedly (minimum 1h). Set 0 to disable.",
			},
			{
				Key: "min_score", Label: "Min Score", Type: fieldNumber,
				Default:     strconv.Itoa(defaults.MinScoreValue),
				Placeholder: strconv.Itoa(defaults.MinScoreValue),
				Min:         strconv.Itoa(defaults.MinScoreValue), Max: strconv.Itoa(defaults.MaxScoreValue),
				Help: "Global minimum score threshold. 0 accepts any match.",
			},
			{
				Key: "exclude_arr_tags", Label: "Exclude Arr Tags", Type: fieldText,
				Default:     defaults.ExcludeTag,
				Placeholder: defaults.ExcludeTag,
				Help:        "Comma-separated Sonarr/Radarr tag names. Tagged media is skipped during auto scans.",
			},
			{
				Key: "upgrade_enabled", Label: "Upgrades", Type: fieldBool,
				Default: defaultTrue,
				Group:   "upgrades",
				Help:    "Search for better-scoring subtitles during scans",
			},
			{
				Key: "upgrade_window_days", Label: "Upgrade window", Type: fieldNumber,
				Default:     "7",
				Placeholder: "7",
				Min:         "1",
				Group:       "upgrades",
				ShowWhen:    "upgrade_enabled=true",
				Help:        "Only upgrade subtitles downloaded within this many days",
			},
		},
	}
}

func adaptiveSection() subflux.SchemaSection {
	return subflux.SchemaSection{
		Key: "adaptive", Title: "Adaptive Backoff", Type: fieldFields,
		EnableKey: keyEnabled,
		Fields: []subflux.SchemaField{
			{
				Key: "initial_delay", Label: "Initial Delay", Type: fieldDuration,
				Default:     formatDuration(defaults.DefaultAdaptiveInitDelay),
				Placeholder: "7D",
				Help:        "Wait time before retrying a provider after no results",
			},
			{
				Key: "max_delay", Label: "Max Delay", Type: fieldDuration,
				Default:     formatDuration(defaults.DefaultAdaptiveMaxDelay),
				Placeholder: "3M",
				Help:        "Maximum wait between retries",
			},
			{
				Key: "backoff_multiplier", Label: "Multiplier", Type: fieldNumber,
				Default:     "2",
				Placeholder: "2",
				Min:         "1",
				Help:        "Multiply delay after each failed search",
			},
			{
				Key: "max_attempts", Label: "Max Attempts", Type: fieldNumber,
				Default:     "0",
				Placeholder: "0",
				Min:         "0",
				Help:        "Stop retrying after this many attempts. 0 retries forever.",
			},
		},
	}
}

func postProcessSection() subflux.SchemaSection {
	return subflux.SchemaSection{
		Key: "post_processing", Title: "Post-Processing", Type: fieldFields,
		Fields: []subflux.SchemaField{
			{
				Key: "sync_subtitles", Label: "Sync Subtitles", Type: fieldBool,
				Default: defaultTrue,
				Help: "Sync downloaded subtitles against embedded reference " +
					"subtitles to correct timing differences on import",
			},
			{
				Key: "audio_sync_fallback", Label: "Audio Sync Fallback", Type: fieldBool,
				Default:  defaultFalse,
				Requires: "sync_subtitles=true",
				Help: "Fall back to audio-based sync when no embedded reference " +
					"subtitle is available or reference sync fails. " +
					"Requires Sync Subtitles to be enabled",
			},
			{
				Key: "strip_hi", Label: "Strip HI", Type: fieldBool,
				Default: defaultFalse,
				Help:    "Remove hearing-impaired annotations: [sounds], (music), speaker labels",
			},
			{
				Key: "strip_tags", Label: "Strip Tags", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Remove HTML formatting tags: <i>, <b>, <u>, <font>",
			},
			{
				Key: "normalize_utf8", Label: "Normalize UTF-8", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Convert subtitle encoding to UTF-8, including from UTF-16 and Windows-1252",
			},
			{
				Key: "normalize_endings", Label: "Normalize Endings", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Convert line endings to CRLF, the SRT standard",
			},
			{
				Key: "clean_whitespace", Label: "Clean Whitespace", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Trim lines and remove empty lines",
			},
			{
				Key: "remove_empty", Label: "Remove Empty", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Drop cues with no text after processing",
			},
		},
	}
}

func scoringSection() subflux.SchemaSection {
	d := subflux.DefaultScores
	return subflux.SchemaSection{
		Key: "scoring", Title: "Scoring", Type: fieldFields,
		Help: "Weights control how subtitles are ranked. Hash match scores 100 automatically.",
		Fields: []subflux.SchemaField{
			{
				Key: "hash", Label: "Hash", Type: fieldNumber, Default: strconv.Itoa(d.Hash),
				Help: "File hash match. It is authoritative and bypasses the other weights.",
			},
			{
				Key: "source", Label: "Source", Type: fieldNumber, Default: strconv.Itoa(d.Source),
				Help: "Release source, for example BluRay, WEB-DL, HDTV or DVDRip",
			},
			{
				Key: "release_group", Label: "Release Group", Type: fieldNumber, Default: strconv.Itoa(d.ReleaseGroup),
				Help: "Scene group name, for example SPARKS or FGT",
			},
			{
				Key: "streaming_service", Label: "Streaming Service", Type: fieldNumber, Default: strconv.Itoa(d.StreamingService),
				Help: "Streaming service, for example AMZN, NF, DSNP or ATVP",
			},
			{
				Key: "video_codec", Label: "Video Codec", Type: fieldNumber, Default: strconv.Itoa(d.VideoCodec),
				Help: "Video codec, for example x264, x265 or AV1",
			},
			{
				Key: "hdr", Label: "HDR", Type: fieldNumber, Default: strconv.Itoa(d.HDR),
				Help: "HDR format, for example HDR10, Dolby Vision or HDR10+",
			},
			{
				Key: "edition", Label: "Edition", Type: fieldNumber, Default: strconv.Itoa(d.Edition),
				Help: "Movie edition, for example Director's Cut, Extended or Theatrical. Movies only.",
			},
			{
				Key: "season_pack", Label: "Season Pack", Type: fieldNumber, Default: strconv.Itoa(d.SeasonPack),
				Help: "Bonus for season packs, which keep subtitles consistent across episodes",
			},
		},
	}
}
