package release

// Source is the label the imported source patterns give a name; the last
// match counts, as in the arrs.
type Source uint8

// The sources the arrs' patterns name; SourceUnknown is a name none matched.
const (
	SourceUnknown Source = iota
	BluRay
	Remux
	BDRip
	BRRip
	WebDL
	WebRip
	HDTV
	SDTV
	PDTV
	DSR
	TVRip
	RawHD
	DVDSource
	DVDR
	Cam
	Telesync
	Telecine
	Screener
	Regional
	Workprint
)

// Family is what rank compares sources by.
type Family uint8

// The families; FamilyUnknown is SourceUnknown's.
const (
	FamilyUnknown Family = iota
	Disc
	Web
	TV
	DVD
	PreRelease
)

// Family is the one subflux-owned source mapping: a raw HD capture is TV, a
// regional release a pre-release.
func (s Source) Family() Family {
	switch s {
	case BluRay, Remux, BDRip, BRRip:
		return Disc
	case WebDL, WebRip:
		return Web
	case HDTV, SDTV, PDTV, DSR, TVRip, RawHD:
		return TV
	case DVDSource, DVDR:
		return DVD
	case Cam, Telesync, Telecine, Screener, Regional, Workprint:
		return PreRelease
	}
	return FamilyUnknown
}

// Cut is a set of cut tokens read from Radarr's EditionRegex captures; zero
// is unlabelled. Presentation words (IMAX, Open Matte, Remastered,
// Collector's, Signature and the like) never set a bit.
type Cut uint16

// The cut tokens.
const (
	CutTheatrical Cut = 1 << iota
	CutExtended
	CutDirectors
	CutUltimate
	CutFinal
	CutAssembly
	CutRecut
	CutDespecialized
	CutSpecial
	CutUnrated
	CutUncut
	CutUncensored
	CutFanEdit
)

// Parsed is one name read by the imported tables; the zero value is a name
// nothing matched.
type Parsed struct {
	group   string
	service string
	source  Source
	cut     Cut
	pack    bool
}

// Source is the last source label the name carries.
func (p Parsed) Source() Source { return p.source }

// Group is the release group after the arrs' clean, exception and
// invalid-group steps; "" when none.
func (p Parsed) Group() string { return p.group }

// Service is a TRaSH streaming custom format's label, set only when that
// format's specifications hold, its Source condition included; "" when none
// or when two formats with different labels hold.
func (p Parsed) Service() string { return p.service }

// Cut is the cut tokens of the name's edition.
func (p Parsed) Cut() Cut { return p.cut }

// SeasonPack is true for an episode name stating a season and no episode
// marker, as epmarker reads them.
func (p Parsed) SeasonPack() bool { return p.pack }

// MaxNameLen bounds what Parse reads: the name as given, cut to this many
// bytes at a rune boundary.
const MaxNameLen = 512
