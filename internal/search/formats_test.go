package search

import (
	"testing"

	"subflux/internal/search/release"
)

// Local aliases for test readability.
var (
	compiledSources     = release.CompiledSources
	compiledVideoCodecs = release.CompiledVideoCodecs
	compiledHDR         = release.CompiledHDR
	compiledStreaming   = release.CompiledStreaming
)

type trashFormat = release.Format

func matchFirst(formats []trashFormat, name string) string {
	return release.MatchFirst(formats, name)
}

// --- matchFirst ---

func TestMatchFirst_returns_first_matching_format(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		formats []trashFormat
		input   string
		want    string
	}{
		{
			"video codec h264",
			compiledVideoCodecs,
			"Movie.2024.BluRay.x264-GRP",
			"h264",
		},
		{
			"video codec h265",
			compiledVideoCodecs,
			"Movie.2024.BluRay.x265-GRP",
			"h265",
		},
		{
			"source bluray",
			compiledSources,
			"Movie.2024.BluRay.1080p-GRP",
			"bluray",
		},
		{
			"source webdl",
			compiledSources,
			"Movie.2024.WEB-DL.1080p-GRP",
			"webdl",
		},
		{
			"hdr10 plus before hdr10",
			compiledHDR,
			"Movie.2024.HDR10+.x265-GRP",
			"hdr10+",
		},
		{
			"hdr10 without plus",
			compiledHDR,
			"Movie.2024.HDR10.x265-GRP",
			"hdr10",
		},
		{
			"streaming service AMZN",
			compiledStreaming,
			"Movie.2024.AMZN.WEB-DL-GRP",
			"AMZN",
		},
		{
			"no match returns empty",
			compiledVideoCodecs,
			"Movie.2024.BluRay-GRP",
			"",
		},
		{
			"empty input returns empty",
			compiledSources,
			"",
			"",
		},
		// --- Additional video codec patterns ---
		{"video codec HEVC tag", compiledVideoCodecs, "Movie.2024.BluRay.HEVC-GRP", "h265"},
		{"video codec AV1", compiledVideoCodecs, "Movie.2024.WEB-DL.AV1-GRP", "av1"},
		{"video codec h266 VVC", compiledVideoCodecs, "Movie.2025.BluRay.VVC-GRP", "h266"},
		{"video codec VC-1", compiledVideoCodecs, "Movie.2024.BluRay.VC-1-GRP", "vc1"},
		{"video codec MPEG2", compiledVideoCodecs, "Movie.2024.MPEG2-GRP", "mpeg2"},
		{"video codec VP9", compiledVideoCodecs, "Movie.2024.VP9-GRP", "vp9"},
		{"video codec DivX", compiledVideoCodecs, "Movie.2024.DivX-GRP", "divx"},
		// --- Additional HDR patterns ---
		{"hdr generic", compiledHDR, "Movie.2024.2160p.HDR.x265-GRP", "hdr"},
		{"dolby vision dovi spelling", compiledHDR, "Movie.2024.DoVi.x265-GRP", "dv"},
		{"dolby vision full name", compiledHDR, "Movie.2024.Dolby.Vision.x265-GRP", "dv"},
		{"hlg format", compiledHDR, "Movie.2024.2160p.HLG.x265-GRP", "hlg"},
		{"pq format", compiledHDR, "Movie.2024.2160p.PQ.x265-GRP", "pq"},
		{"wcg format", compiledHDR, "Movie.2024.2160p.WCG.x265-GRP", "wcg"},
		// --- Additional source patterns ---
		{"source bdrip", compiledSources, "Movie.2024.BDRip.720p-GRP", "bluray"},
		{"source brrip", compiledSources, "Movie.2024.BRRip.720p-GRP", "bluray"},
		{"source dvdrip", compiledSources, "Movie.2024.DVDRip.XviD-GRP", "dvd"},
		{"source pdtv", compiledSources, "Show.S01E01.PDTV-GRP", "sdtv"},
		{"source sdtv", compiledSources, "Show.S01E01.SDTV-GRP", "sdtv"},
		{"source tvrip", compiledSources, "Show.S01E01.TVRip-GRP", "sdtv"},
		{"source dsr", compiledSources, "Show.S01E01.DSR-GRP", "sdtv"},
		{"source cam", compiledSources, "Movie.2024.CAM-GRP", "cam"},
		{"source telesync", compiledSources, "Movie.2024.Telesync-GRP", "telesync"},
		{"source telecine", compiledSources, "Movie.2024.Telecine-GRP", "telecine"},
		{"source hdrip", compiledSources, "Movie.2024.HDRip-GRP", "hdrip"},
		{"source hd-tv", compiledSources, "Show.S01E01.HD-TV-GRP", "hdtv"},
		{"source sd-tv", compiledSources, "Show.S01E01.SD-TV-GRP", "sdtv"},
		{"source webrip via WEBMux", compiledSources, "Movie.2024.WEBMux.1080p-GRP", "webrip"},
		{"source remux with UHD prefix", compiledSources, "Movie.2024.UHD.Remux.2160p-GRP", "remux"},
		// --- Additional streaming service patterns ---
		{"streaming DSNP", compiledStreaming, "Show.S01E01.DSNP.WEB-DL-GRP", "DSNP"},
		{"streaming ATVP", compiledStreaming, "Show.S01E01.ATVP.WEB-DL-GRP", "ATVP"},
		{"streaming HMAX", compiledStreaming, "Show.S01E01.HMAX.WEB-DL-GRP", "HMAX"},
		{"streaming HULU", compiledStreaming, "Show.S01E01.HULU.WEB-DL-GRP", "HULU"},
		{"streaming PCOK", compiledStreaming, "Show.S01E01.PCOK.WEB-DL-GRP", "PCOK"},
		{"streaming PMTP", compiledStreaming, "Show.S01E01.PMTP.WEB-DL-GRP", "PMTP"},
		{"streaming CR crunchyroll", compiledStreaming, "[CR] Show S01E01 [1080p]", "CR"},
		{"streaming HIDIVE", compiledStreaming, "Show.S01E01.HIDIVE.WEB-DL-GRP", "HIDIVE"},
		{"streaming NF netflix", compiledStreaming, "Show.S01E01.NF.WEB-DL-GRP", "NF"},
		{"streaming IP iplayer", compiledStreaming, "Show.S01E01.iPlayer.WEB-DL-GRP", "IP"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := matchFirst(tt.formats, tt.input)
			if got != tt.want {
				t.Errorf("matchFirst(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}
