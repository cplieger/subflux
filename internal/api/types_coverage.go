package api

// --- Coverage tracking types (subtitle files + scan state) ---

// SubtitleSource identifies where a subtitle was found.
type SubtitleSource string

const (
	// SourceEmbedded indicates a subtitle embedded in the video container.
	SourceEmbedded SubtitleSource = "embedded"
	// SourceExternal indicates a subtitle in a separate file on disk.
	SourceExternal SubtitleSource = "external"
)

// SubtitleFile represents a discovered subtitle (embedded or external) for a media file.
type SubtitleFile struct {
	Language string         // ISO 639-1
	Variant  Variant        // "standard", "hi", "forced"
	Source   SubtitleSource // SourceEmbedded or SourceExternal
	Codec    string         // e.g. "subrip", "ass"; empty for external
	Path     string         // file path for external; empty for embedded
}

// SubtitleEntry is a subtitle-file row from coverage queries. On the wire it
// carries NO filesystem paths (S7: clients address files by typed reference,
// never by path); Path and VideoPath stay populated for in-process consumers
// (file listing size stat, deletion, reconciliation) but are json-omitted.
// Ordinal is the manual-sibling number parsed from the row's filename
// (api.ManualOrdinal) — together with (media_type, media_id, language,
// variant, source) it forms the wire FileRef the client echoes back to
// address this exact file.
type SubtitleEntry struct {
	MediaID   string `json:"media_id"`
	Language  string `json:"language"`
	Variant   string `json:"variant"`
	Source    string `json:"source"`
	Codec     string `json:"codec,omitempty"`
	Path      string `json:"-"`
	VideoPath string `json:"-"`
	Score     int    `json:"score,omitempty"`
	Ordinal   int    `json:"ordinal,omitempty"`
	OffsetMs  int64  `json:"offset_ms,omitempty"`
}

// ScanStateRow records when a media item was last scanned.
type ScanStateRow struct {
	ScannedAt string `json:"scanned_at"`
	MediaID   string `json:"media_id"`
	Title     string `json:"title"`
	AudioLang string `json:"audio_lang"`
	Season    int    `json:"season,omitempty"`
	Episode   int    `json:"episode,omitempty"`
	// Searched is false for inventory-only stamps (scan skip paths that
	// recorded coverage without provider work).
	Searched bool `json:"searched"`
}
