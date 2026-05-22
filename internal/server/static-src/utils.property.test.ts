// Property-based tests for coverageMediaId and tvdbMediaId.
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { coverageMediaId, tvdbMediaId } from "./utils.js";

describe("coverageMediaId properties", () => {
  it("series items produce tvdb- prefix with tvdb_id", () => {
    fc.assert(
      fc.property(fc.nat(), (tvdbId) => {
        const result = coverageMediaId({ _type: "series", tvdb_id: tvdbId });
        expect(result).toBe(`tvdb-${tvdbId}`);
        expect(result.startsWith("tvdb-")).toBe(true);
      })
    );
  });

  it("movie items produce tmdb- prefix with tmdb_id", () => {
    fc.assert(
      fc.property(fc.nat(), (tmdbId) => {
        const result = coverageMediaId({ _type: "movie", tmdb_id: tmdbId });
        expect(result).toBe(`tmdb-${tmdbId}`);
        expect(result.startsWith("tmdb-")).toBe(true);
      })
    );
  });
});

describe("tvdbMediaId properties", () => {
  it("always starts with tvdb- and embeds the id", () => {
    fc.assert(
      fc.property(fc.nat(), fc.integer({ min: 1, max: 99 }), fc.integer({ min: 1, max: 99 }), (id, season, episode) => {
        const result = tvdbMediaId(id, season, episode);
        expect(result.startsWith(`tvdb-${id}-`)).toBe(true);
        expect(result).toContain(`s${String(season).padStart(2, "0")}`);
        expect(result).toContain(`e${String(episode).padStart(2, "0")}`);
      })
    );
  });
});
