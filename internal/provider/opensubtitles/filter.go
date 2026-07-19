package opensubtitles

import "github.com/cplieger/runesafe"

// --- Language Mapping ---

// Language mapping between ISO 639-1 and OpenSubtitles.com codes.
var (
	langToOS   = map[string]string{"pt": "pt-PT", "pb": langPtBR, "zh": "zh-CN"}
	langFromOS = map[string]string{"pt-PT": "pt", langPtBR: "pb", "zh-CN": "zh", "ea": "es"}
)

func toOSLang(lang string) string {
	if v, ok := langToOS[lang]; ok {
		return v
	}
	return lang
}

func fromOSLang(lang string) string {
	if v, ok := langFromOS[lang]; ok {
		return v
	}
	return lang
}

// --- API Response Types ---

// API response types.
type loginResponse struct {
	Token string `json:"token"`
	// BaseURL is an upstream-controlled redirect host, tagged at the decode
	// boundary: validation and use read Raw(); any emit sanitizes itself.
	BaseURL runesafe.Untrusted `json:"base_url"`
	User    loginUser          `json:"user"`
}

type loginUser struct {
	VIP bool `json:"vip"`
}

type searchResponse struct {
	Data       []searchResult `json:"data"`
	TotalPages int            `json:"total_pages"`
	TotalCount int            `json:"total_count"`
}

type searchResult struct {
	Attributes searchAttributes `json:"attributes"`
}

type searchAttributes struct {
	Language          string         `json:"language"`
	Release           string         `json:"release"`
	Files             []searchFile   `json:"files"`
	FeatureDetails    featureDetails `json:"feature_details"`
	HearingImpaired   bool           `json:"hearing_impaired"`
	ForeignPartsOnly  bool           `json:"foreign_parts_only"`
	AITranslated      bool           `json:"ai_translated"`
	MachineTranslated bool           `json:"machine_translated"`
	MoviehashMatch    bool           `json:"moviehash_match"`
}

type featureDetails struct {
	Title         string `json:"movie_name"`
	Year          int    `json:"year"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
}

type searchFile struct {
	FileID int `json:"file_id"`
}

type downloadResponse struct {
	Link         string `json:"link"`
	ResetTimeUTC string `json:"reset_time_utc"`
	Requests     int    `json:"requests"`
	Remaining    int    `json:"remaining"`
}
