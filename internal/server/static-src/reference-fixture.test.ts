// reference-fixture.test.ts — THE SYNC PIN (TS half): these hardcoded values
// are shared verbatim with internal/testsupport/reffixture_test.go. A drift
// in either generator fails its own suite; regenerate BOTH pins together
// when the fixture spec changes (see reference-fixture.ts's sync contract).
import { describe, it, expect } from "vitest";

import {
  REF_MOVIE_COUNT,
  REF_SERIES_COUNT,
  refChecksum,
  refMovieItems,
  refMovieWire,
  refSeriesItems,
  refSeriesWire,
  refTotalEpisodes,
  type RefMovie,
  type RefSeries,
} from "./reference-fixture.js";

describe("reference fixture: cross-language sync pins", () => {
  it("pins the scale, the episode total, and the checksum", () => {
    expect(REF_SERIES_COUNT).toBe(500);
    expect(REF_MOVIE_COUNT).toBe(4360);
    expect(refSeriesItems()).toHaveLength(500);
    expect(refMovieItems()).toHaveLength(4360);
    expect(refTotalEpisodes()).toBe(10049);
    expect(refChecksum()).toBe(1400457312);
  });

  it("pins sampled series against the Go generator's values", () => {
    const series = refSeriesItems();
    const samples: [number, RefSeries][] = [
      [
        0,
        {
          title: "Reference Series 0000",
          tvdbId: 100001,
          arrId: 1,
          year: 1967,
          episodes: 9,
          episodesPerSeason: 11,
          seasons: 1,
          haveEN: 0,
          haveFR: 0,
          audioIdx: 0,
          hasFR: false,
        },
      ],
      [
        123,
        {
          title: "Reference Series 0123",
          tvdbId: 100124,
          arrId: 124,
          year: 1993,
          episodes: 8,
          episodesPerSeason: 11,
          seasons: 1,
          haveEN: 5,
          haveFR: 8,
          audioIdx: 3,
          hasFR: false,
        },
      ],
      [
        499,
        {
          title: "Reference Series 0499",
          tvdbId: 100500,
          arrId: 500,
          year: 2024,
          episodes: 11,
          episodesPerSeason: 10,
          seasons: 2,
          haveEN: 1,
          haveFR: 6,
          audioIdx: 4,
          hasFR: false,
        },
      ],
    ];
    for (const [idx, want] of samples) {
      expect(series[idx]).toStrictEqual(want);
    }
  });

  it("pins sampled movies against the Go generator's values", () => {
    const movies = refMovieItems();
    const samples: [number, RefMovie][] = [
      [
        0,
        {
          title: "Reference Movie 0000",
          sceneName: "Reference.Movie.0000.2009.2160p.WEB-DL.x265-GRP",
          tmdbId: 500001,
          arrId: 1,
          year: 2009,
          haveEN: 0,
          haveFR: 1,
          embeddedTracks: 6,
          sceneIdx: 1,
          audioIdx: 4,
          hasFR: true,
        },
      ],
      [
        2179,
        {
          title: "Reference Movie 2179",
          sceneName: "Reference.Movie.2179.1959.1080p.BluRay.x264-REF",
          tmdbId: 502180,
          arrId: 2180,
          year: 1959,
          haveEN: 1,
          haveFR: 0,
          embeddedTracks: 5,
          sceneIdx: 0,
          audioIdx: 1,
          hasFR: true,
        },
      ],
      [
        4359,
        {
          title: "Reference Movie 4359",
          sceneName: "Reference.Movie.4359.2012.1080p.WEB.h264-STD",
          tmdbId: 504360,
          arrId: 4360,
          year: 2012,
          haveEN: 1,
          haveFR: 0,
          embeddedTracks: 1,
          sceneIdx: 3,
          audioIdx: 3,
          hasFR: false,
        },
      ],
    ];
    for (const [idx, want] of samples) {
      expect(movies[idx]).toStrictEqual(want);
    }
  });

  it("maps the wire rows the collections would serve", () => {
    const series = refSeriesWire();
    const movies = refMovieWire();
    expect(series).toHaveLength(500);
    expect(movies).toHaveLength(4360);
    // The wire mapping preserves identity and the audio code table order
    // (index-aligned with the Go side's arr NAME table).
    expect(series[0]).toMatchObject({
      title: "Reference Series 0000",
      tvdb_id: 100001,
      id: 1,
      episodes: 9,
      audio_lang: "en",
      rule: "en",
    });
    expect(series[0]?.targets).toStrictEqual([
      { language: "en", variant: "standard", have: 0, total: 9, have_ignored: 0 },
    ]);
    expect(movies[0]).toMatchObject({
      title: "Reference Movie 0000",
      tmdb_id: 500001,
      id: 1,
      year: 2009,
      audio_lang: "es",
      has_file: true,
      rule: "en+fr",
    });
    expect(movies[0]?.targets).toStrictEqual([
      { language: "en", variant: "standard", have: 0, total: 1, have_ignored: 0 },
      { language: "fr", variant: "standard", have: 1, total: 1, have_ignored: 0 },
    ]);
    // Every reference movie is file-bearing: the collection serves all 4,360.
    expect(movies.every((m) => m.has_file)).toBe(true);
  });
});
