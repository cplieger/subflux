package anidb

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strconv"
	"strings"
)

// decompressIfGzipped returns data unchanged if it is not gzip-compressed.
// If it starts with the gzip magic bytes (0x1F 0x8B), it decompresses and
// returns the result, capped at maxBytes. AniDB's wiki explicitly warns
// that clients may have to handle gzip manually, so this defends against
// transport-level decompression being disabled upstream.
func decompressIfGzipped(data []byte, maxBytes int64) ([]byte, error) {
	if len(data) < 2 || data[0] != 0x1f || data[1] != 0x8b {
		return data, nil
	}
	slog.Warn("anidb: response is raw gzip; transport decompression may be disabled")
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("gzip decode: %w", err)
	}
	defer gr.Close()
	out, err := io.ReadAll(io.LimitReader(gr, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gzip read: %w", err)
	}
	if int64(len(out)) > maxBytes {
		return nil, fmt.Errorf("gzip body exceeded %d bytes", maxBytes)
	}
	return out, nil
}

// XML types for anime-list.xml.
type animeList struct {
	Animes []animeEntry `xml:"anime"`
}

type animeEntry struct {
	MappingList       *mappingList `xml:"mapping-list"`
	TVDBID            string       `xml:"tvdbid,attr"`
	DefaultTVDBSeason string       `xml:"defaulttvdbseason,attr"`
	AniDBID           int          `xml:"anidbid,attr"`
	EpisodeOffset     int          `xml:"episodeoffset,attr"`
}

type mappingList struct {
	Mappings []mapping `xml:"mapping"`
}

type mapping struct {
	Text        string `xml:",chardata"`
	AniDBSeason int    `xml:"anidbseason,attr"`
	TVDBSeason  int    `xml:"tvdbseason,attr"`
	Offset      int    `xml:"offset,attr"`
}

// candidate holds a potential AniDB match with its episode offset.
type candidate struct {
	anidbID int
	offset  int
}

// findInMapping searches the parsed anime list for a TVDB series+season+episode
// and returns the corresponding AniDB series ID and episode number.
func findInMapping(list *animeList, tvdbID, season, episode int) (anidbID, epNo int) {
	tvdbStr := strconv.Itoa(tvdbID)
	seasonStr := strconv.Itoa(season)

	var candidates []candidate

	for _, a := range list.Animes {
		if a.TVDBID != tvdbStr {
			continue
		}

		if a.DefaultTVDBSeason == "a" {
			if id, ep, ok := resolveAbsoluteMapping(&a, season, episode); ok {
				return id, ep
			}
			continue
		}

		if a.DefaultTVDBSeason == seasonStr {
			candidates = append(candidates, candidate{
				anidbID: a.AniDBID, offset: a.EpisodeOffset,
			})
		}
	}

	return bestCandidate(candidates, episode)
}

// resolveAbsoluteMapping handles entries with DefaultTVDBSeason="a" and
// explicit mapping lists. Returns (anidbID, epNo, found).
//
// Anime-list.xml sometimes exposes multiple <mapping> entries for the same
// tvdbseason (e.g. one with explicit per-episode Text plus a second with a
// bulk Offset). We iterate every matching entry and prefer an exact text
// match; if none matches, we fall back to the first positive offset-based
// resolution seen. A single entry whose offset underflows (ep <= 0) no
// longer short-circuits the whole lookup — a sibling entry may still
// resolve (F10).
func resolveAbsoluteMapping(a *animeEntry, season, episode int) (anidbID, epNo int, found bool) {
	if a.MappingList == nil {
		return 0, 0, false
	}
	var offsetFallback int
	var offsetFound bool
	for _, mp := range a.MappingList.Mappings {
		if mp.TVDBSeason != season {
			continue
		}
		if mp.Text != "" {
			if epNo := findEpisodeInMapping(mp.Text, episode); epNo > 0 {
				return a.AniDBID, epNo, true
			}
		}
		if !offsetFound {
			ep := episode - mp.Offset
			if ep > 0 {
				offsetFallback = ep
				offsetFound = true
			}
		}
	}
	if offsetFound {
		return a.AniDBID, offsetFallback, true
	}
	return 0, 0, false
}

// bestCandidate returns the candidate with the highest offset still below
// the episode number, or (0, 0) if none qualifies. AniDB splits seasons
// into parts with episode offsets.
func bestCandidate(candidates []candidate, episode int) (anidbID, epNo int) {
	if len(candidates) == 0 {
		return 0, 0
	}
	slices.SortFunc(candidates, func(a, b candidate) int {
		return cmp.Compare(a.offset, b.offset)
	})
	var best candidate
	for _, c := range candidates {
		if episode > c.offset {
			best = c
		}
	}
	if best.anidbID == 0 {
		return 0, 0
	}
	return best.anidbID, episode - best.offset
}

// findEpisodeInMapping parses ";1-1;2-2;3-3;" style mapping text.
// Returns the AniDB episode number for the given TVDB episode, or 0.
func findEpisodeInMapping(text string, tvdbEpisode int) int {
	for part := range strings.SplitSeq(text, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		anidbStr, tvdbStr, ok := strings.Cut(part, "-")
		if !ok {
			continue
		}
		anidbEp, err := strconv.Atoi(strings.TrimSpace(anidbStr))
		if err != nil {
			continue
		}
		// TVDB side can be "x+y" for multi-episode mappings.
		for tp := range strings.SplitSeq(tvdbStr, "+") {
			tvdbEp, terr := strconv.Atoi(strings.TrimSpace(tp))
			if terr == nil && tvdbEp == tvdbEpisode {
				return anidbEp
			}
		}
	}
	return 0
}
