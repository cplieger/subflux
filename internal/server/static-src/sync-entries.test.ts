// Tests for what the sync dialog derives from its `entries` input.
//
// These are value-in/value-out: no DOM, no module state, no mocks. That is the
// whole reason the two functions were lifted out of sync.ts, where ambient
// module state made them unreachable by a test.
import { describe, it, expect } from "vitest";
import { buildSyncSubLabels, parseSeasonEpisode } from "./sync-entries.js";
import type { SubtitleEntry } from "./api-types.js";

/** A subtitle entry carrying only the fields the labeller reads. The rest of
 *  SubtitleEntry is irrelevant here and stating it would obscure the input. */
function entry(language: string, variant?: string): SubtitleEntry {
  return { language, variant } as SubtitleEntry;
}

describe("buildSyncSubLabels", () => {
  it("names a lone track without a number", () => {
    // The numbering exists to disambiguate; a single track has nothing to be
    // disambiguated from, and "English #1" would imply a sibling that is absent.
    const [label] = buildSyncSubLabels([entry("en")]);
    expect(label?.label).toBe("English");
  });

  it("numbers duplicates of the same language and variant", () => {
    const labels = buildSyncSubLabels([entry("en"), entry("en"), entry("en")]);
    expect(labels.map((l) => l.label)).toEqual(["English #1", "English #2", "English #3"]);
  });

  it("keeps variants apart, so neither is numbered", () => {
    // Same language, different variants: three distinct labels already, so the
    // duplicate counter must not fire. This is the case a language-only key
    // would get wrong.
    const labels = buildSyncSubLabels([entry("en"), entry("en", "hi"), entry("en", "forced")]);
    expect(labels.map((l) => l.label)).toEqual(["English", "English SDH", "English Forced"]);
  });

  it("numbers within a variant, not across languages", () => {
    const labels = buildSyncSubLabels([
      entry("en", "hi"),
      entry("en", "hi"),
      entry("fr"),
      entry("en"),
    ]);
    expect(labels.map((l) => l.label)).toEqual([
      "English SDH #1",
      "English SDH #2",
      "French",
      "English",
    ]);
  });

  it("keeps a language-less entry selectable rather than dropping it", () => {
    // The server sent a track the user can see. An empty label would leave it
    // in the dropdown with no way to identify it.
    const [label] = buildSyncSubLabels([entry("")]);
    expect(label?.label).toBe("??");
  });

  it("carries each entry through beside its label", () => {
    // The caller selects by index into this array and then reads `.sub`, so a
    // reordering or a dropped entry would select the wrong subtitle.
    const a = entry("en");
    const b = entry("fr");
    expect(buildSyncSubLabels([a, b]).map((l) => l.sub)).toEqual([a, b]);
  });

  it("returns nothing for no entries", () => {
    expect(buildSyncSubLabels([])).toEqual([]);
  });
});

describe("parseSeasonEpisode", () => {
  it("reads the aired season and episode off a series media_id", () => {
    expect(parseSeasonEpisode("tvdb-12345-s01e05")).toEqual({ season: 1, episode: 5 });
  });

  it("does not treat leading zeros as octal", () => {
    expect(parseSeasonEpisode("tvdb-1-s08e09")).toEqual({ season: 8, episode: 9 });
  });

  it("reads multi-digit seasons and episodes", () => {
    expect(parseSeasonEpisode("tvdb-1-s10e123")).toEqual({ season: 10, episode: 123 });
  });

  it("answers 0/0 for a movie media_id, which the server ignores", () => {
    // The zero is a real answer, not a parse failure: movies have no season or
    // episode and the caller deliberately does not branch on it.
    expect(parseSeasonEpisode("tmdb-603")).toEqual({ season: 0, episode: 0 });
  });

  it("answers 0/0 for an empty id", () => {
    expect(parseSeasonEpisode("")).toEqual({ season: 0, episode: 0 });
  });

  it("matches only at the end, so a mid-string marker does not win", () => {
    // The pattern is anchored with $ on purpose. A release name embedded in an
    // id must not be mistaken for the id's own numbering.
    expect(parseSeasonEpisode("tvdb-1-s01e02-extra")).toEqual({ season: 0, episode: 0 });
  });

  it("takes the last marker when an id carries two", () => {
    expect(parseSeasonEpisode("show-s01e02-s03e04")).toEqual({ season: 3, episode: 4 });
  });
});
