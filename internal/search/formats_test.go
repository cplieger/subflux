package search

import (
	"testing"

	"github.com/cplieger/subflux/internal/search/release"
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
		input   string
		want    string
		formats []trashFormat
	}{
		{name: "video codec h264", formats: compiledVideoCodecs, input: "Movie.2024.BluRay.x264-GRP", want: "h264"},
		{name: "video codec h265", formats: compiledVideoCodecs, input: "Movie.2024.BluRay.x265-GRP", want: "h265"},
		{name: "source bluray", formats: compiledSources, input: "Movie.2024.BluRay.1080p-GRP", want: "bluray"},
		{name: "source webdl", formats: compiledSources, input: "Movie.2024.WEB-DL.1080p-GRP", want: "webdl"},
		{name: "hdr10 plus before hdr10", formats: compiledHDR, input: "Movie.2024.HDR10+.x265-GRP", want: "hdr10+"},
		{name: "hdr10 without plus", formats: compiledHDR, input: "Movie.2024.HDR10.x265-GRP", want: "hdr10"},
		{name: "streaming service AMZN", formats: compiledStreaming, input: "Movie.2024.AMZN.WEB-DL-GRP", want: "AMZN"},
		{name: "no match returns empty", formats: compiledVideoCodecs, input: "Movie.2024.BluRay-GRP", want: ""},
		{name: "empty input returns empty", formats: compiledSources, input: "", want: ""},
		// --- Additional video codec patterns ---
		{name: "video codec HEVC tag", formats: compiledVideoCodecs, input: "Movie.2024.BluRay.HEVC-GRP", want: "h265"},
		{name: "video codec AV1", formats: compiledVideoCodecs, input: "Movie.2024.WEB-DL.AV1-GRP", want: "av1"},
		{name: "video codec h266 VVC", formats: compiledVideoCodecs, input: "Movie.2025.BluRay.VVC-GRP", want: "h266"},
		{name: "video codec VC-1", formats: compiledVideoCodecs, input: "Movie.2024.BluRay.VC-1-GRP", want: "vc1"},
		{name: "video codec MPEG2", formats: compiledVideoCodecs, input: "Movie.2024.MPEG2-GRP", want: "mpeg2"},
		{name: "video codec VP9", formats: compiledVideoCodecs, input: "Movie.2024.VP9-GRP", want: "vp9"},
		{name: "video codec DivX", formats: compiledVideoCodecs, input: "Movie.2024.DivX-GRP", want: "divx"},
		// --- Additional HDR patterns ---
		{name: "hdr generic", formats: compiledHDR, input: "Movie.2024.2160p.HDR.x265-GRP", want: "hdr"},
		{name: "dolby vision dovi spelling", formats: compiledHDR, input: "Movie.2024.DoVi.x265-GRP", want: "dv"},
		{name: "dolby vision full name", formats: compiledHDR, input: "Movie.2024.Dolby.Vision.x265-GRP", want: "dv"},
		{name: "hlg format", formats: compiledHDR, input: "Movie.2024.2160p.HLG.x265-GRP", want: "hlg"},
		{name: "pq format", formats: compiledHDR, input: "Movie.2024.2160p.PQ.x265-GRP", want: "pq"},
		{name: "wcg format", formats: compiledHDR, input: "Movie.2024.2160p.WCG.x265-GRP", want: "wcg"},
		// --- Additional source patterns ---
		{name: "source bdrip", formats: compiledSources, input: "Movie.2024.BDRip.720p-GRP", want: "bluray"},
		{name: "source brrip", formats: compiledSources, input: "Movie.2024.BRRip.720p-GRP", want: "bluray"},
		{name: "source dvdrip", formats: compiledSources, input: "Movie.2024.DVDRip.XviD-GRP", want: "dvd"},
		{name: "source pdtv", formats: compiledSources, input: "Show.S01E01.PDTV-GRP", want: "sdtv"},
		{name: "source sdtv", formats: compiledSources, input: "Show.S01E01.SDTV-GRP", want: "sdtv"},
		{name: "source tvrip", formats: compiledSources, input: "Show.S01E01.TVRip-GRP", want: "sdtv"},
		{name: "source dsr", formats: compiledSources, input: "Show.S01E01.DSR-GRP", want: "sdtv"},
		{name: "source cam", formats: compiledSources, input: "Movie.2024.CAM-GRP", want: "cam"},
		{name: "source telesync", formats: compiledSources, input: "Movie.2024.Telesync-GRP", want: "telesync"},
		{name: "source telecine", formats: compiledSources, input: "Movie.2024.Telecine-GRP", want: "telecine"},
		{name: "source hdrip", formats: compiledSources, input: "Movie.2024.HDRip-GRP", want: "hdrip"},
		{name: "source hd-tv", formats: compiledSources, input: "Show.S01E01.HD-TV-GRP", want: "hdtv"},
		{name: "source sd-tv", formats: compiledSources, input: "Show.S01E01.SD-TV-GRP", want: "sdtv"},
		{name: "source webrip via WEBMux", formats: compiledSources, input: "Movie.2024.WEBMux.1080p-GRP", want: "webrip"},
		{name: "source remux with UHD prefix", formats: compiledSources, input: "Movie.2024.UHD.Remux.2160p-GRP", want: "remux"},
		// --- Additional streaming service patterns ---
		{name: "streaming DSNP", formats: compiledStreaming, input: "Show.S01E01.DSNP.WEB-DL-GRP", want: "DSNP"},
		{name: "streaming ATVP", formats: compiledStreaming, input: "Show.S01E01.ATVP.WEB-DL-GRP", want: "ATVP"},
		{name: "streaming HMAX", formats: compiledStreaming, input: "Show.S01E01.HMAX.WEB-DL-GRP", want: "HMAX"},
		{name: "streaming HULU", formats: compiledStreaming, input: "Show.S01E01.HULU.WEB-DL-GRP", want: "HULU"},
		{name: "streaming PCOK", formats: compiledStreaming, input: "Show.S01E01.PCOK.WEB-DL-GRP", want: "PCOK"},
		{name: "streaming PMTP", formats: compiledStreaming, input: "Show.S01E01.PMTP.WEB-DL-GRP", want: "PMTP"},
		{name: "streaming CR crunchyroll", formats: compiledStreaming, input: "[CR] Show S01E01 [1080p]", want: "CR"},
		{name: "streaming HIDIVE", formats: compiledStreaming, input: "Show.S01E01.HIDIVE.WEB-DL-GRP", want: "HIDIVE"},
		{name: "streaming NF netflix", formats: compiledStreaming, input: "Show.S01E01.NF.WEB-DL-GRP", want: "NF"},
		{name: "streaming IP iplayer", formats: compiledStreaming, input: "Show.S01E01.iPlayer.WEB-DL-GRP", want: "IP"},
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
