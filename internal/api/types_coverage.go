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

// SubtitleFileRow is the JSON shape returned by coverage queries.
type SubtitleFileRow struct {
	MediaID   string `json:"media_id"`
	Language  string `json:"language"`
	Variant   string `json:"variant"`
	Source    string `json:"source"`
	Codec     string `json:"codec,omitempty"`
	Path      string `json:"path,omitempty"`
	VideoPath string `json:"video_path,omitempty"`
	Score     int    `json:"score,omitempty"`
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
}
