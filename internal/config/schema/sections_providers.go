package schema

import (
	"strconv"

	"subflux/internal/api"
	"subflux/internal/config/defaults"
)

func searchSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "search", Title: "Search", Type: fieldFields,
		Fields: []api.SchemaField{
			{
				Key: "scan_interval", Label: "Scan Interval", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultScanInterval),
				Placeholder: defaults.FormatDuration(defaults.DefaultScanInterval),
				Min:         defaults.FormatDuration(defaults.MinScanInterval),
				Help:        "Time between full library scans (minimum 1h)",
			},
			{
				Key: "scan_delay", Label: "Scan Delay", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultScanDelay),
				Placeholder: defaults.FormatDuration(defaults.DefaultScanDelay),
				Min:         defaults.FormatDuration(defaults.MinScanDelay),
				Help:        "Delay between items during scans to avoid hammering providers (minimum 5s)",
			},
			{
				Key: "provider_timeout", Label: "Provider Timeout", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultProviderTimeout),
				Placeholder: defaults.FormatDuration(defaults.DefaultProviderTimeout),
				Min:         defaults.FormatDuration(defaults.MinProviderTimeout),
				Help:        "Cooldown after a provider fails repeatedly (minimum 1h, 0 to disable)",
			},
			{
				Key: "min_score", Label: "Min Score", Type: fieldNumber,
				Default:     strconv.Itoa(defaults.MinScoreValue),
				Placeholder: strconv.Itoa(defaults.MinScoreValue),
				Min:         strconv.Itoa(defaults.MinScoreValue), Max: strconv.Itoa(defaults.MaxScoreValue),
				Help: "Global minimum score threshold (0 = accept any match)",
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

func adaptiveSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "adaptive", Title: "Adaptive Backoff", Type: fieldFields,
		EnableKey: keyEnabled,
		Fields: []api.SchemaField{
			{
				Key: "initial_delay", Label: "Initial Delay", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultAdaptiveInitDelay),
				Placeholder: "7D",
				Help:        "Wait time before retrying a provider after no results",
			},
			{
				Key: "max_delay", Label: "Max Delay", Type: fieldDuration,
				Default:     defaults.FormatDuration(defaults.DefaultAdaptiveMaxDelay),
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
				Help:        "Stop retrying after this many attempts (0 = retry forever)",
			},
		},
	}
}

func postProcessSection() api.SchemaSection {
	return api.SchemaSection{
		Key: "post_processing", Title: "Post-Processing", Type: fieldFields,
		Fields: []api.SchemaField{
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
				Help:    "Convert subtitle encoding to UTF-8 (handles UTF-16, Windows-1252)",
			},
			{
				Key: "normalize_endings", Label: "Normalize Endings", Type: fieldBool,
				Default: defaultTrue,
				Help:    "Convert line endings to CRLF (SRT standard)",
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

func scoringSection() api.SchemaSection {
	d := api.DefaultScores
	return api.SchemaSection{
		Key: "scoring", Title: "Scoring", Type: fieldFields,
		Help: "Weights control how subtitles are ranked. Hash match scores 100 automatically.",
		Fields: []api.SchemaField{
			{Key: "hash", Label: "Hash", Type: fieldNumber, Default: strconv.Itoa(d.Hash),
				Help: "File hash match (authoritative, bypasses other weights)"},
			{Key: "source", Label: "Source", Type: fieldNumber, Default: strconv.Itoa(d.Source),
				Help: "BluRay, WEB-DL, HDTV, DVDRip, etc."},
			{Key: "release_group", Label: "Release Group", Type: fieldNumber, Default: strconv.Itoa(d.ReleaseGroup),
				Help: "Scene group name (e.g. SPARKS, FGT)"},
			{Key: "streaming_service", Label: "Streaming Service", Type: fieldNumber, Default: strconv.Itoa(d.StreamingService),
				Help: "AMZN, NF, DSNP, ATVP, etc."},
			{Key: "video_codec", Label: "Video Codec", Type: fieldNumber, Default: strconv.Itoa(d.VideoCodec),
				Help: "x264, x265, AV1, etc."},
			{Key: "hdr", Label: "HDR", Type: fieldNumber, Default: strconv.Itoa(d.HDR),
				Help: "HDR10, Dolby Vision, HDR10+, etc."},
			{Key: "edition", Label: "Edition", Type: fieldNumber, Default: strconv.Itoa(d.Edition),
				Help: "Director's Cut, Extended, Theatrical (movies only)"},
			{Key: "season_pack", Label: "Season Pack", Type: fieldNumber, Default: strconv.Itoa(d.SeasonPack),
				Help: "Bonus for season packs (consistent subs across episodes)"},
		},
	}
}
