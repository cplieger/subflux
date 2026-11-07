package ffmpeg

// kind is one ffmpeg configure --enable list.
type kind string

// The configure lists the runner's arguments draw on.
const (
	decoder  kind = "decoder"
	encoder  kind = "encoder"
	muxer    kind = "muxer"
	filter   kind = "filter"
	protocol kind = "protocol"
)

// ffmpeg's configure and format names for SRT and ass.
const (
	subrip = "srt"
	ass    = "ass"
)

// feature is one ffmpeg configure --enable entry.
type feature struct {
	Kind kind
	Name string
}

// features are the entries the runner's arguments need from the image's
// ffmpeg build; TestFFmpegBuildCoversArgs checks the Dockerfile against them.
var features = []feature{
	{protocol, "file"},
	{protocol, "pipe"},
	// ExtractSubtitle converts any text stream to SRT, or copies ASS and SSA.
	{decoder, "subrip"},
	{decoder, subrip},
	{decoder, ass},
	{decoder, "ssa"},
	{decoder, "movtext"},
	{decoder, "text"},
	{decoder, "webvtt"},
	{encoder, subrip},
	{muxer, subrip},
	{muxer, ass},
	{filter, "aresample"},
	{encoder, "pcm_s16le"},
	{muxer, "pcm_s16le"},
	{encoder, "libx264"},
	{encoder, "aac"},
	{muxer, "mp4"},
	{filter, "scale"},
	// ffmpeg inserts aformat to convert sample formats and anull as the
	// preview's audio graph, which names no -af.
	{filter, "aformat"},
	{filter, "anull"},
}
