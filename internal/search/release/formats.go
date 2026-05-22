package release

import "regexp"

// Format holds one regex pattern for release name parsing.
type Format struct {
	Name      string   // Format name (e.g. "x265", "DTS-HD MA", "BluRay")
	Regex     string   // Regex pattern (PCRE syntax where needed)
	Compiled  *Pattern // Compiled via CompilePCRE (set by init)
	Normalize string   // Normalized label for scoring (e.g. "h265", "bluray")
}

// Normalized source labels.
const (
	NormRemux    = "remux"
	NormBluray   = "bluray"
	NormWebDL    = "webdl"
	NormWebRip   = "webrip"
	NormHDTV     = "hdtv"
	NormSDTV     = "sdtv"
	NormDVD      = "dvd"
	NormCam      = "cam"
	NormTelesync = "telesync"
	NormTelecine = "telecine"
	NormHDRip    = "hdrip"
)

// SonarrSources are source patterns ported from Sonarr QualityParser.cs.
var SonarrSources = []Format{
	{Name: "Remux", Normalize: NormRemux,
		Regex: `(?:[_. ]|\d{4}p-|\bHybrid-)(?:(BD|UHD)[-_. ]?)?Remux\b|(?:(BD|UHD)[-_. ]?)?Remux[_. ]\d{4}p`},
	{Name: "BluRay", Normalize: NormBluray,
		Regex: `\b(?:BluRay|Blu-Ray|HD-?DVD|BDMux|BD(?!$))\b`},
	{Name: "WEB-DL", Normalize: NormWebDL,
		Regex: `WEB[-_. ]DL(?:mux)?|WEBDL|AmazonHD|AmazonSD|iTunesHD|MaxdomeHD|NetflixU?HD|WebHD|HBOMaxHD|DisneyHD|[. ]WEB[. ](?:[xh][ .]?26[45]|AVC|HEVC|DDP?5[. ]1)|[. ](?-i:WEB)$|(?:720|1080|2160)p[-. ]WEB[-. ]|[-. ]WEB[-. ](?:720|1080|2160)p|\b\s/\sWEB\s/\s\b|(?:AMZN|NF|DP)[. -]WEB[. -](?!Rip)`},
	{Name: "WEBRip", Normalize: NormWebRip,
		Regex: `\b(?:WebRip|Web-Rip|WEBMux)\b`},
	{Name: "HDTV", Normalize: NormHDTV,
		Regex: `\b(?:HDTV)\b`},
	{Name: "BDRip", Normalize: NormBluray,
		Regex: `\b(?:BDRip|BDLight)\b`},
	{Name: "BRRip", Normalize: NormBluray,
		Regex: `\b(?:BRRip)\b`},
	{Name: "DVD", Normalize: NormDVD,
		Regex: `\b(?:DVD|DVDRip|NTSC|PAL|xvidvd)\b`},
	{Name: "DSR", Normalize: NormSDTV,
		Regex: `\b(?:WS[-_. ]DSR|DSR)\b`},
	{Name: "PDTV", Normalize: NormSDTV,
		Regex: `\b(?:PDTV)\b`},
	{Name: "SDTV", Normalize: NormSDTV,
		Regex: `\b(?:SDTV)\b`},
	{Name: "TVRip", Normalize: NormSDTV,
		Regex: `\b(?:TVRip)\b`},
	{Name: "HD-TV", Normalize: NormHDTV,
		Regex: `HD[-_. ]TV`},
	{Name: "SD-TV", Normalize: NormSDTV,
		Regex: `SD[-_. ]TV`},
	{Name: "Anime BD", Normalize: NormBluray,
		Regex: `bd(?:720|1080|2160)|(?<=[-_. (\[])bd(?=[-_. )\]])`},
	{Name: "Anime WEB", Normalize: NormWebDL,
		Regex: `\[WEB\]|[\[\(]WEB[ .]`},
	{Name: "CAM", Normalize: NormCam,
		Regex: `\b(?:CAM|HDCAM)\b`},
	{Name: "Telesync", Normalize: NormTelesync,
		Regex: `\b(?:Telesync|TS|PDVD)\b`},
	{Name: "Telecine", Normalize: NormTelecine,
		Regex: `\b(?:Telecine|TC)\b`},
	{Name: "HDRip", Normalize: NormHDRip,
		Regex: `\b(?:HDRip)\b`},
}

const sonarrReleaseGroupRegex = `-([a-z0-9]+(-[a-z0-9]+)?(?!.+?(?:480p|576p|720p|1080p|2160p)))` +
	`(?<!(?:WEB-DL|Blu-Ray|DTS-HD|DTS-X|DTS-MA|DTS-ES|-ES|-EN|-CAT|[ ._]\d{4}-\d{2}|-\d{2}))` +
	`(?:\b|[-._ ]|$)` +
	`|[-._ ]\[([a-z0-9]+)\]$`

const sonarrAnimeReleaseGroupRegex = `^\[(\S.+?\S)\](?:_|-|\s|\.)?`

var (
	CompiledReleaseGroup      *Pattern
	CompiledAnimeReleaseGroup *regexp.Regexp
	FileExtRe                 = regexp.MustCompile(`(?i)\.[a-z0-9]{2,4}$`)
)

// TrashVideoCodecs defines TRaSH video codec patterns.
var TrashVideoCodecs = []Format{
	{Name: "x264", Regex: `[xh][ ._-]?264|\bAVC(\b|\d)`, Normalize: "h264"},
	{Name: "x265", Regex: `[xh][ ._-]?265|\bHEVC(\b|\d)`, Normalize: "h265"},
	{Name: "x266", Regex: `[xh][ ._-]?266|\bVVC(\b|\d)`, Normalize: "h266"},
	{Name: "AV1", Regex: `\bAV1\b`, Normalize: "av1"},
	{Name: "VC-1", Regex: `\bVC[-_. ]?1\b`, Normalize: "vc1"},
	{Name: "MPEG2", Regex: `MPEG[-.]?2`, Normalize: "mpeg2"},
	{Name: "VP9", Regex: `\bVP9\b`, Normalize: "vp9"},
	{Name: "XviD", Regex: `\b[Xx][Vv][Ii][Dd]\b`, Normalize: "xvid"},
	{Name: "DivX", Regex: `\b[Dd][Ii][Vv][Xx]\b`, Normalize: "divx"},
}

// TrashHDRFormats defines TRaSH HDR patterns.
var TrashHDRFormats = []Format{
	{Name: "HDR10+", Regex: `\b(HDR10(?=[+]|P(lus)?))`, Normalize: "hdr10+"},
	{Name: "HDR10", Regex: `\b(HDR10(?![+]|P(lus)?))`, Normalize: "hdr10"},
	{Name: "HDR", Regex: `\b(HDR)\b`, Normalize: "hdr"},
	{Name: "DV", Regex: `\b(dv|dovi|dolby[ .]?v(ision)?)\b`, Normalize: "dv"},
	{Name: "HLG", Regex: `\b(HLG)\b`, Normalize: "hlg"},
	{Name: "PQ", Regex: `\b(PQ)\b`, Normalize: "pq"},
	{Name: "WCG", Regex: `\b(WCG)\b`, Normalize: "wcg"},
}

// TrashStreamingServices defines TRaSH streaming service patterns.
var TrashStreamingServices = []Format{
	{Name: "AMZN", Regex: `\b(amzn|amazon(hd)?)\b`, Normalize: "AMZN"},
	{Name: "NF", Regex: `\b(nf|netflix(u?hd)?)\b`, Normalize: "NF"},
	{Name: "DSNP", Regex: `\b(dsnp|dsny|disney|Disney\+)\b`, Normalize: "DSNP"},
	{Name: "ATVP", Regex: `\b(atvp|aptv|Apple TV\+)\b`, Normalize: "ATVP"},
	{Name: "ATV", Regex: `\b(ATV)\b`, Normalize: "ATV"},
	{Name: "HMAX", Regex: `\b(hmax|hbom|hbo[ ._-]?max)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Normalize: "HMAX"},
	{Name: "HBO", Regex: `\b(hbo)(?![ ._-]max)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Normalize: "HBO"},
	{Name: "MAX", Regex: `\b((?<!hbo[ ._-])max)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Normalize: "MAX"},
	{Name: "HULU", Regex: `\b(hulu)\b`, Normalize: "HULU"},
	{Name: "PCOK", Regex: `\b(pcok|peacock)\b`, Normalize: "PCOK"},
	{Name: "PMTP", Regex: `\b(pmtp|Paramount Plus)\b`, Normalize: "PMTP"},
	{Name: "BCORE", Regex: `\b(BCORE)\b|\b(\d{3,4}(p|i))\b.*\b(CORE)\b`, Normalize: "BCORE"},
	{Name: "CRAV", Regex: `\b(crav(e)?)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "CRAV"},
	{Name: "CRiT", Regex: `\b(CRiT)\b`, Normalize: "CRIT"},
	{Name: "ROKU", Regex: `\b(ROKU)\b`, Normalize: "ROKU"},
	{Name: "STAN", Regex: `\b(stan)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "STAN"},
	{Name: "MA", Regex: `(?<!dts[ .-]?hd[ .-]?)\b(ma|ykw)\b(?=.*\bweb[ ._-]?(dl|rip)\b)`, Normalize: "MA"},
	{Name: "iT", Regex: `\b(it|itunes)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Normalize: "IT"},
	{Name: "STRP", Regex: `\b(STRP)\b`, Normalize: "STRP"},
	{Name: "SHO", Regex: `\b(sho|showtime)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "SHO"},
	{Name: "PLAY", Regex: `\b(Play)\b(?=[ ._-]web[ ._-]?(dl|rip)\b)`, Normalize: "PLAY"},
	{Name: "NOW", Regex: `\b(now)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "NOW"},
	{Name: "IP", Regex: `\b(ip|iplayer)\b`, Normalize: "IP"},
	{Name: "ITVX", Regex: `\b(ITVX?)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "ITVX"},
	{Name: "MY5", Regex: `\b(MY5)\b`, Normalize: "MY5"},
	{Name: "ALL4", Regex: `\b(ALL4)\b`, Normalize: "ALL4"},
	{Name: "4OD", Regex: `\b(4OD)\b`, Normalize: "4OD"},
	{Name: "Pathe", Regex: `\b(Pathe)\b`, Normalize: "PATHE"},
	{Name: "OViD", Regex: `\b(ovid)\b`, Normalize: "OVID"},
	{Name: "NLZ", Regex: `\b(nlz|NLZiet)\b`, Normalize: "NLZ"},
	{Name: "VDL", Regex: `\b(vdl)\b`, Normalize: "VDL"},
	{Name: "CC", Regex: `\b(CC)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "CC"},
	{Name: "RED", Regex: `\b(red|youtube red)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "RED"},
	{Name: "SYFY", Regex: `\b(SYFY)\b`, Normalize: "SYFY"},
	{Name: "CBC", Regex: `\b(CBC)\b`, Normalize: "CBC"},
	{Name: "AUBC", Regex: `\b(AUBC)\b`, Normalize: "AUBC"},
	{Name: "HTSR", Regex: `\b(HTSR|DSNPHS|HS)\b`, Normalize: "HTSR"},
	{Name: "VIU", Regex: `\b(viu)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "VIU"},
	{Name: "TVING", Regex: `\b(tving)\b[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "TVING"},
	{Name: "TVer", Regex: `\b(tver)\b`, Normalize: "TVER"},
	{Name: "FOD", Regex: `\b(fod)\b`, Normalize: "FOD"},
	{Name: "U-NEXT", Regex: `\b(u-next)\b`, Normalize: "UNEXT"},
	{Name: "ABEMA", Regex: `\b(ABEMA[ ._-]?(TV)?)\b`, Normalize: "ABEMA"},
	{Name: "B-Global", Regex: `\b(B[ .-]?Global)\b`, Normalize: "BGLOBAL"},
	{Name: "CR", Regex: `\b(C(runchy)?[ .-]?R(oll)?)\b`, Normalize: "CR"},
	{Name: "HIDIVE", Regex: `\b(HIDI(VE)?)\b`, Normalize: "HIDIVE"},
	{Name: "FUNi", Regex: `\b(FUNi(mation)?)\b`, Normalize: "FUNI"},
	{Name: "Bilibili", Regex: `\b(Bili(bili)?)\b`, Normalize: "BILI"},
	{Name: "VRV", Regex: `\b(vrv)\b`, Normalize: "VRV"},
	{Name: "DSCP", Regex: `\b((dscp|dcp|disc)\b|dscv\+?)[ ._-]web[ ._-]?(dl|rip)?\b`, Normalize: "DSCP"},
	{Name: "DCU", Regex: `\b(dcu|DC Universe)\b`, Normalize: "DCU"},
	{Name: "QIBI", Regex: `\b(qibi|quibi)\b`, Normalize: "QIBI"},
	{Name: "NICK", Regex: `\b(nick)\b`, Normalize: "NICK"},
	{Name: "VDMO", Regex: `\b(vdmo)\b`, Normalize: "VDMO"},
	{Name: "MUBI", Regex: `\b(mubi)\b`, Normalize: "MUBI"},
}

// Compiled pattern caches — built once at init.
var (
	CompiledSources     []Format
	CompiledVideoCodecs []Format
	CompiledHDR         []Format
	CompiledStreaming   []Format
)

func init() {
	CompiledSources = compileFormats(SonarrSources)
	CompiledVideoCodecs = compileFormats(TrashVideoCodecs)
	CompiledHDR = compileFormats(TrashHDRFormats)
	CompiledStreaming = compileFormats(TrashStreamingServices)

	var err error
	CompiledReleaseGroup, err = CompilePCRE(sonarrReleaseGroupRegex)
	if err != nil {
		panic("formats: release group: " + err.Error())
	}
	CompiledAnimeReleaseGroup = regexp.MustCompile(`(?i)` + sonarrAnimeReleaseGroupRegex)
}

// compileFormats compiles each format's PCRE regex via CompilePCRE.
func compileFormats(formats []Format) []Format {
	out := make([]Format, len(formats))
	copy(out, formats)
	for i := range out {
		p, err := CompilePCRE(out[i].Regex)
		if err != nil {
			panic("trash_formats: " + out[i].Name + ": " + err.Error())
		}
		out[i].Compiled = p
	}
	return out
}

// MatchFirst returns the Normalize label of the first matching format,
// or empty string if none match.
func MatchFirst(formats []Format, name string) string {
	for i := range formats {
		if formats[i].Compiled.MatchString(name) {
			return formats[i].Normalize
		}
	}
	return ""
}
