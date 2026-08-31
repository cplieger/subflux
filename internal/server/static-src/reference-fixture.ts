// reference-fixture.ts — the deterministic REFERENCE-LIBRARY fixture for the
// task-19 stress lane: 500 series (~10k episodes) and 4,360 file-bearing
// movies, mirrored byte-for-byte from internal/testsupport/reffixture.go.
//
// THE SYNC CONTRACT: this module and the Go generator implement the SAME
// per-item PRNG and the SAME draw order, and both test suites pin the SAME
// hardcoded aggregates (counts, total episodes, FNV checksum, sampled items)
// — reference-fixture.test.ts here, reffixture_test.go there — so a drift in
// either implementation fails its own suite. Any change to the PRNG, the draw
// order, or a derivation lands on both sides with both pins regenerated.
//
// Per-item PRNG (32-bit LCG, Math.imul keeps every step in uint32):
//
//   state0 = 0x5EED5001 ^ (kindTag*0x9E3779B9 + (index+1)*0x85EBCA6B)  (mod 2^32)
//   next():  state = state*1664525 + 1013904223                        (mod 2^32)
//   rand(n): next(); return ((state >>> 16) & 0xFFFF) % n — high bits; LCG low bits cycle
//
// kindTag: series = 1, movie = 2. Each item consumes a FIXED number of draws
// (series 7, movie 7), so fields stay aligned across implementations even
// when one side ignores a field.

import type { MovieItem, SeriesItem } from "./wire/types.gen.js";

/** Reference-library scale (design: 500 series / ~10k episodes / 4,360 movies). */
export const REF_SERIES_COUNT = 500;
export const REF_MOVIE_COUNT = 4360;

/** One deterministic reference series (the canonical per-item record). */
export interface RefSeries {
  title: string;
  tvdbId: number;
  arrId: number;
  year: number;
  episodes: number;
  episodesPerSeason: number;
  seasons: number;
  haveEN: number;
  haveFR: number;
  audioIdx: number;
  hasFR: boolean;
}

/** One deterministic reference movie (always file-bearing — the shipped
 *  collections omit file-less rows, and the reference payload is the 4,360
 *  rows that reach the wire). */
export interface RefMovie {
  title: string;
  sceneName: string;
  tmdbId: number;
  arrId: number;
  year: number;
  haveEN: number;
  haveFR: number;
  embeddedTracks: number;
  sceneIdx: number;
  audioIdx: number;
  hasFR: boolean;
}

// The audio-language CODE table audioIdx indexes into (the Go side also
// carries the index-aligned arr NAME table for its arr fakes).
const AUDIO_LANGS = ["en", "ja", "fr", "de", "es"] as const;

// The scene-name quality table sceneIdx indexes into (mirrors the Go table).
const SCENE_QUALITIES = [
  "1080p.BluRay.x264-REF",
  "2160p.WEB-DL.x265-GRP",
  "720p.HDTV.x264-OLD",
  "1080p.WEB.h264-STD",
] as const;

/** Per-item PRNG state; see the module header for the shared spec. */
function newState(kindTag: number, index: number): { state: number } {
  const mixed = (Math.imul(kindTag, 0x9e3779b9) + Math.imul(index + 1, 0x85ebca6b)) >>> 0;
  return { state: (0x5eed5001 ^ mixed) >>> 0 };
}

function rand(r: { state: number }, n: number): number {
  r.state = (Math.imul(r.state, 1664525) + 1013904223) >>> 0;
  // The 0xffff mask is a numeric no-op (a uint32 shifted right 16 IS 16
  // bits); it mirrors the Go side's provably-lossless conversion shape.
  return ((r.state >>> 16) & 0xffff) % n;
}

function pad4(i: number): string {
  return String(i).padStart(4, "0");
}

/** The 500 reference series (long-tail episode distribution, ~10k total). */
export function refSeriesItems(): RefSeries[] {
  const items: RefSeries[] = [];
  for (let i = 0; i < REF_SERIES_COUNT; i++) {
    const r = newState(1, i);
    const year = 1960 + rand(r, 66); // draw 1
    const bucket = rand(r, 100); // draw 2
    let episodes: number; // draw 3, bucketed
    if (bucket < 50) {
      episodes = 4 + rand(r, 8); // 4..11
    } else if (bucket < 80) {
      episodes = 10 + rand(r, 21); // 10..30
    } else if (bucket < 95) {
      episodes = 26 + rand(r, 41); // 26..66
    } else {
      episodes = 60 + rand(r, 81); // 60..140
    }
    const episodesPerSeason = 8 + rand(r, 6); // draw 4: 8..13
    const haveEN = rand(r, episodes + 1); // draw 5
    const hasFR = rand(r, 100) < 30; // draw 6
    const haveFR = rand(r, episodes + 1); // draw 7 (always drawn, used iff hasFR)
    items.push({
      title: `Reference Series ${pad4(i)}`,
      tvdbId: 100001 + i,
      arrId: i + 1,
      year,
      episodes,
      episodesPerSeason,
      seasons: Math.ceil(episodes / episodesPerSeason),
      haveEN,
      haveFR,
      audioIdx: i % AUDIO_LANGS.length,
      hasFR,
    });
  }
  return items;
}

/** The 4,360 reference movies. */
export function refMovieItems(): RefMovie[] {
  const items: RefMovie[] = [];
  for (let i = 0; i < REF_MOVIE_COUNT; i++) {
    const r = newState(2, i);
    const year = 1950 + rand(r, 76); // draw 1
    const audioIdx = rand(r, 5); // draw 2
    const haveEN = rand(r, 2); // draw 3
    const hasFR = rand(r, 100) < 40; // draw 4
    const haveFR = rand(r, 2); // draw 5
    const embeddedTracks = rand(r, 8); // draw 6
    const sceneIdx = rand(r, 4); // draw 7
    items.push({
      title: `Reference Movie ${pad4(i)}`,
      sceneName: `Reference.Movie.${pad4(i)}.${year}.${SCENE_QUALITIES[sceneIdx] ?? SCENE_QUALITIES[0]}`,
      tmdbId: 500001 + i,
      arrId: i + 1,
      year,
      haveEN,
      haveFR,
      embeddedTracks,
      sceneIdx,
      audioIdx,
      hasFR,
    });
  }
  return items;
}

/** The deterministic episode total (~10k by construction; exact value pinned
 *  by both sync tests). */
export function refTotalEpisodes(): number {
  return refSeriesItems().reduce((acc, s) => acc + s.episodes, 0);
}

/** FNV-1a-style fold over every drawn field of every item — the
 *  cross-language drift detector; both sync tests pin its exact value. */
export function refChecksum(): number {
  let acc = 2166136261 >>> 0;
  // Every mixed value is < 0x10000, so the mask is a numeric no-op (the Go
  // side masks identically).
  const mix = (v: number): void => {
    acc = Math.imul((acc ^ (v & 0xffff)) >>> 0, 16777619) >>> 0;
  };
  for (const s of refSeriesItems()) {
    mix(s.year);
    mix(s.episodes);
    mix(s.episodesPerSeason);
    mix(s.haveEN);
    mix(s.hasFR ? 1 : 0);
    mix(s.haveFR);
  }
  for (const m of refMovieItems()) {
    mix(m.year);
    mix(m.audioIdx);
    mix(m.haveEN);
    mix(m.hasFR ? 1 : 0);
    mix(m.haveFR);
    mix(m.embeddedTracks);
    mix(m.sceneIdx);
  }
  return acc;
}

/** The reference series COLLECTION payload: the wire rows the coverage
 *  route loader receives at the reference scale. */
export function refSeriesWire(): SeriesItem[] {
  return refSeriesItems().map((s) => {
    const targets = [
      {
        language: "en",
        variant: "standard",
        have: s.haveEN,
        total: s.episodes,
        have_ignored: 0,
      },
    ];
    if (s.hasFR) {
      targets.push({
        language: "fr",
        variant: "standard",
        have: s.haveFR,
        total: s.episodes,
        have_ignored: 0,
      });
    }
    return {
      title: s.title,
      imdb_id: `tt${String(1000000 + s.tvdbId)}`,
      first_aired: `${s.year}-01-15`,
      audio_lang: AUDIO_LANGS[s.audioIdx] ?? "en",
      rule: s.hasFR ? "en+fr" : "en",
      targets,
      tags: [],
      id: s.arrId,
      year: s.year,
      tvdb_id: s.tvdbId,
      episodes: s.episodes,
      excluded: false,
    };
  });
}

/** The reference movie COLLECTION payload (post-A3 wire: no subs inline). */
export function refMovieWire(): MovieItem[] {
  return refMovieItems().map((m) => {
    const targets = [
      { language: "en", variant: "standard", have: m.haveEN, total: 1, have_ignored: 0 },
    ];
    if (m.hasFR) {
      targets.push({
        language: "fr",
        variant: "standard",
        have: m.haveFR,
        total: 1,
        have_ignored: 0,
      });
    }
    return {
      title: m.title,
      imdb_id: `tt${String(2000000 + m.tmdbId)}`,
      scene_name: m.sceneName,
      in_cinemas: `${m.year}-03-01`,
      digital_release: `${m.year}-06-01`,
      audio_lang: AUDIO_LANGS[m.audioIdx] ?? "en",
      rule: m.hasFR ? "en+fr" : "en",
      targets,
      tags: [],
      tmdb_id: m.tmdbId,
      id: m.arrId,
      year: m.year,
      has_file: true,
      excluded: false,
    };
  });
}
