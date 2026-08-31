package testsupport

// reffixture.go — the deterministic REFERENCE-LIBRARY fixture for the task-19
// stress lane: 500 series with realistic season/episode distributions summing
// ~10k episodes, and 4,360 file-bearing movies.
//
// THE SYNC CONTRACT: internal/server/static-src/reference-fixture.ts is the
// byte-for-byte TypeScript mirror of this generator. Both sides implement the
// SAME per-item PRNG and the SAME draw order, and both test suites pin the
// SAME hardcoded aggregate values (counts, total episodes, FNV checksum,
// sampled items) — reffixture_test.go here, reference-fixture.test.ts there —
// so a drift in either implementation fails its own suite. Any change to the
// PRNG, the draw order, or a derivation must land on both sides with both
// pins regenerated together.
//
// Per-item PRNG (32-bit LCG, portable to JS int arithmetic):
//
//	state0 = 0x5EED5001 ^ (kindTag*0x9E3779B9 + (index+1)*0x85EBCA6B)  (mod 2^32)
//	next():  state = state*1664525 + 1013904223                        (mod 2^32)
//	rand(n): next(); return ((state >> 16) & 0xFFFF) % n — high bits; LCG low bits cycle
//
// kindTag: series = 1, movie = 2. Each item consumes a FIXED number of draws
// (series 7, movie 7), so fields stay aligned across implementations even
// when one side ignores a field.

// Reference-library scale (design: 500 series / ~10k episodes / 4,360 movies).
const (
	RefSeriesCount = 500
	RefMovieCount  = 4360
)

// RefSeries is one deterministic reference-library series: the canonical
// per-item record both language sides derive their wire/DTO shapes from.
type RefSeries struct {
	Title             string
	TvdbID            int
	ArrID             int
	Year              int
	Episodes          int
	EpisodesPerSeason int
	Seasons           int
	HaveEN            int
	HaveFR            int
	AudioIdx          int
	HasFR             bool
}

// RefMovie is one deterministic reference-library movie. Every reference
// movie is file-bearing (the shipped collections omit file-less rows, and the
// reference payload is the 4,360 rows that reach the wire).
type RefMovie struct {
	Title          string
	SceneName      string
	TmdbID         int
	ArrID          int
	Year           int
	HaveEN         int
	HaveFR         int
	EmbeddedTracks int
	SceneIdx       int
	AudioIdx       int
	HasFR          bool
}

// refRand is the shared per-item PRNG state.
type refRand struct{ state uint32 }

func newRefRand(kindTag, index uint32) *refRand {
	return &refRand{state: 0x5EED5001 ^ (kindTag*0x9E3779B9 + (index+1)*0x85EBCA6B)}
}

func (r *refRand) rand(n int) int {
	r.state = r.state*1664525 + 1013904223
	// The 0xFFFF mask is a numeric no-op (a uint32 shifted right 16 IS 16
	// bits); it makes the int conversion provably lossless.
	return int(r.state>>16&0xFFFF) % n
}

// RefAudioLangs is the audio-language CODE table AudioIdx indexes into, and
// RefAudioNames the matching arr language-NAME table (same index). The TS
// mirror carries the code table; the two must stay index-aligned — the
// coveragehandlers stress test pins the correspondence through the real
// name→code mapping.
var (
	RefAudioLangs = [5]string{"en", "ja", "fr", "de", "es"}
	RefAudioNames = [5]string{"English", "Japanese", "French", "German", "Spanish"}
)

// refSceneQualities is the scene-name quality table SceneIdx indexes into
// (mirrored verbatim in the TS fixture).
var refSceneQualities = [4]string{
	"1080p.BluRay.x264-REF",
	"2160p.WEB-DL.x265-GRP",
	"720p.HDTV.x264-OLD",
	"1080p.WEB.h264-STD",
}

// refPad4 renders the zero-padded index both sides embed in titles.
func refPad4(i int) string {
	digits := "0123456789"
	return string([]byte{
		digits[i/1000%10], digits[i/100%10], digits[i/10%10], digits[i%10],
	})
}

// RefSeriesItems generates the 500 reference series. Episode counts follow a
// long-tail bucket distribution (half short single-season shows, a few
// long-runners) tuned so the deterministic total lands near 10k.
func RefSeriesItems() []RefSeries {
	items := make([]RefSeries, RefSeriesCount)
	for i := range items {
		r := newRefRand(1, uint32(i))
		year := 1960 + r.rand(66) // draw 1
		bucket := r.rand(100)     // draw 2
		var episodes int          // draw 3, bucketed
		switch {
		case bucket < 50:
			episodes = 4 + r.rand(8) // 4..11
		case bucket < 80:
			episodes = 10 + r.rand(21) // 10..30
		case bucket < 95:
			episodes = 26 + r.rand(41) // 26..66
		default:
			episodes = 60 + r.rand(81) // 60..140
		}
		perSeason := 8 + r.rand(6)     // draw 4: 8..13
		haveEN := r.rand(episodes + 1) // draw 5
		hasFR := r.rand(100) < 30      // draw 6
		haveFR := r.rand(episodes + 1) // draw 7 (always drawn, used iff hasFR)
		items[i] = RefSeries{
			Title:             "Reference Series " + refPad4(i),
			TvdbID:            100001 + i,
			ArrID:             i + 1,
			Year:              year,
			Episodes:          episodes,
			EpisodesPerSeason: perSeason,
			Seasons:           (episodes + perSeason - 1) / perSeason,
			HaveEN:            haveEN,
			HaveFR:            haveFR,
			AudioIdx:          i % len(RefAudioLangs),
			HasFR:             hasFR,
		}
	}
	return items
}

// RefMovieItems generates the 4,360 reference movies.
func RefMovieItems() []RefMovie {
	items := make([]RefMovie, RefMovieCount)
	for i := range items {
		r := newRefRand(2, uint32(i))
		year := 1950 + r.rand(76) // draw 1
		audioIdx := r.rand(5)     // draw 2
		haveEN := r.rand(2)       // draw 3
		hasFR := r.rand(100) < 40 // draw 4
		haveFR := r.rand(2)       // draw 5
		embedded := r.rand(8)     // draw 6
		sceneIdx := r.rand(4)     // draw 7
		items[i] = RefMovie{
			Title: "Reference Movie " + refPad4(i),
			SceneName: "Reference.Movie." + refPad4(i) + "." +
				itoa(year) + "." + refSceneQualities[sceneIdx],
			TmdbID:         500001 + i,
			ArrID:          i + 1,
			Year:           year,
			HaveEN:         haveEN,
			HaveFR:         haveFR,
			EmbeddedTracks: embedded,
			SceneIdx:       sceneIdx,
			AudioIdx:       audioIdx,
			HasFR:          hasFR,
		}
	}
	return items
}

// itoa avoids strconv for the one 4-digit year the scene name embeds (keeps
// the mirror's arithmetic identical).
func itoa(v int) string {
	digits := "0123456789"
	return string([]byte{
		digits[v/1000%10], digits[v/100%10], digits[v/10%10], digits[v%10],
	})
}

// RefTotalEpisodes folds the deterministic episode total (~10k by
// construction; the exact value is pinned by both sync tests).
func RefTotalEpisodes() int {
	total := 0
	for _, s := range RefSeriesItems() {
		total += s.Episodes
	}
	return total
}

// RefChecksum is the FNV-1a-style fold over every drawn field of every item,
// the cross-language drift detector: both sync tests pin its exact value.
func RefChecksum() uint32 {
	acc := uint32(2166136261)
	// Every mixed value is < 0x10000, so the mask is a numeric no-op; it
	// makes the conversion provably lossless (the TS mirror masks too).
	mix := func(v int) {
		acc = (acc ^ uint32(v&0xFFFF)) * 16777619
	}
	b2i := func(b bool) int {
		if b {
			return 1
		}
		return 0
	}
	for _, s := range RefSeriesItems() {
		mix(s.Year)
		mix(s.Episodes)
		mix(s.EpisodesPerSeason)
		mix(s.HaveEN)
		mix(b2i(s.HasFR))
		mix(s.HaveFR)
	}
	for _, m := range RefMovieItems() {
		mix(m.Year)
		mix(m.AudioIdx)
		mix(m.HaveEN)
		mix(b2i(m.HasFR))
		mix(m.HaveFR)
		mix(m.EmbeddedTracks)
		mix(m.SceneIdx)
	}
	return acc
}
