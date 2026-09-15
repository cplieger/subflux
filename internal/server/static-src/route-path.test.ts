// route-path.test.ts — the DOM-free URL vocabulary.
//
// The property worth the most is MUTUAL EXCLUSIVITY: a URLPattern `:name` group
// spans exactly one path segment, so /series/:id cannot claim /series/42/sync
// and no order over the patterns can change what a path resolves to.
// router.test.ts asserts that over the shipped list through kindsMatching; this
// file covers what each path parses to, what buildPath emits back, and the
// folds — a malformed id or an out-of-shape language segment answers the
// library rather than throwing.
import { describe, it, expect } from "vitest";
import {
  buildPath,
  kindsMatching,
  mediaParent,
  parseLibraryFilters,
  parseRoute,
  syncRouteFor,
  type Route,
} from "./route-path.js";

const LIBRARY: Route = {
  kind: "library",
  filters: { type: "all", q: "", missing: false, sort: "title" },
};

describe("parseRoute", () => {
  it("reads every path the route table holds", () => {
    expect(parseRoute("/settings")).toStrictEqual({ kind: "settings" });
    expect(parseRoute("/history")).toStrictEqual({ kind: "history" });
    expect(parseRoute("/series/42")).toStrictEqual({ kind: "series", id: 42 });
    expect(parseRoute("/series/42/sync")).toStrictEqual({ kind: "series-sync", id: 42 });
    expect(parseRoute("/series/42/files")).toStrictEqual({ kind: "series-files", id: 42 });
    expect(parseRoute("/series/42/search/fr")).toStrictEqual({
      kind: "series-search",
      id: 42,
      lang: "fr",
    });
    expect(parseRoute("/movie/7")).toStrictEqual({ kind: "movie", id: 7 });
    expect(parseRoute("/movie/7/sync")).toStrictEqual({ kind: "movie-sync", id: 7 });
    expect(parseRoute("/movie/7/files")).toStrictEqual({ kind: "movie-files", id: 7 });
    expect(parseRoute("/movie/7/search/pb")).toStrictEqual({
      kind: "movie-search",
      id: 7,
      lang: "pb",
    });
  });

  it("reads a three-letter language code, which the app really produces", () => {
    // pb (Brazilian Portuguese) is two letters, pob three: the constraint is
    // [a-z]{2,3} rather than a bare :lang for exactly this pair.
    expect(parseRoute("/series/42/search/pob")).toStrictEqual({
      kind: "series-search",
      id: 42,
      lang: "pob",
    });
  });

  it("answers the library for the legacy /movies alias", () => {
    // /movies is a REDIRECT the router leaves behind, not a Route kind: giving
    // it one would let buildPath emit a path nothing should produce.
    expect(parseRoute("/movies")).toStrictEqual(LIBRARY);
  });

  it("answers the library for the root and for an unknown path", () => {
    expect(parseRoute("/")).toStrictEqual(LIBRARY);
    expect(parseRoute("/nope")).toStrictEqual(LIBRARY);
    expect(parseRoute("/series/42/nope")).toStrictEqual(LIBRARY);
  });

  it("folds a malformed id onto the library instead of throwing", () => {
    for (const path of ["/series/abc", "/series/-1", "/series/1.5", "/series/4a", "/movie/abc"]) {
      expect(parseRoute(path)).toStrictEqual(LIBRARY);
    }
  });

  it("folds a truncated percent-escape onto the library instead of throwing", () => {
    // The one shape that would make this codec throw rather than fold: a decode
    // of `%` raises URIError, so any decoding added here has to be the
    // non-throwing kind.
    for (const path of ["/series/%", "/series/42/search/%e0%a4%a"]) {
      expect(parseRoute(path)).toStrictEqual(LIBRARY);
    }
    expect(() => parseRoute("/", "?q=%")).not.toThrow();
  });

  it("folds an id the number type cannot spell back onto the library", () => {
    // buildPath is parseRoute's inverse, and past MAX_SAFE_INTEGER the digits
    // stop round-tripping: "9007199254740993" reads back as ...992, and 1e21
    // builds as "1e+21". Either way the pair would emit a path it did not parse.
    expect(parseRoute("/series/9007199254740991")).toStrictEqual({
      kind: "series",
      id: 9007199254740991,
    });
    expect(parseRoute("/series/9007199254740992")).toStrictEqual(LIBRARY);
    expect(parseRoute("/movie/99999999999999999999/sync")).toStrictEqual(LIBRARY);
  });

  it("folds an out-of-shape language segment onto the library", () => {
    // A bare :lang would make /series/42/search/francais a search route and run
    // a search for a language that does not exist.
    for (const path of [
      "/series/42/search/FR",
      "/series/42/search/f",
      "/series/42/search/francais",
      "/series/42/search/e1",
    ]) {
      expect(parseRoute(path)).toStrictEqual(LIBRARY);
    }
  });

  it("reads an id written with leading zeros as its numeric value", () => {
    expect(parseRoute("/series/007")).toStrictEqual({ kind: "series", id: 7 });
    expect(parseRoute("/series/0")).toStrictEqual({ kind: "series", id: 0 });
    expect(parseRoute("/series/00")).toStrictEqual({ kind: "series", id: 0 });
  });

  it("folds a trailing slash and an empty segment onto the library", () => {
    for (const path of ["/series/42/", "/settings/", "/history/", "/series/", "/series//sync"]) {
      expect(parseRoute(path)).toStrictEqual(LIBRARY);
    }
  });

  it("reads the library filters off the search string", () => {
    expect(parseRoute("/", "?type=movies&q=dune&missing=1&sort=missing")).toStrictEqual({
      kind: "library",
      filters: { type: "movies", q: "dune", missing: true, sort: "missing" },
    });
  });
});

describe("buildPath", () => {
  it("emits every path the route table holds", () => {
    expect(buildPath({ kind: "settings" })).toBe("/settings");
    expect(buildPath({ kind: "history" })).toBe("/history");
    expect(buildPath({ kind: "series", id: 42 })).toBe("/series/42");
    expect(buildPath({ kind: "series-sync", id: 42 })).toBe("/series/42/sync");
    expect(buildPath({ kind: "series-files", id: 42 })).toBe("/series/42/files");
    expect(buildPath({ kind: "series-search", id: 42, lang: "fr" })).toBe("/series/42/search/fr");
    expect(buildPath({ kind: "movie", id: 7 })).toBe("/movie/7");
    expect(buildPath({ kind: "movie-sync", id: 7 })).toBe("/movie/7/sync");
    expect(buildPath({ kind: "movie-files", id: 7 })).toBe("/movie/7/files");
    expect(buildPath({ kind: "movie-search", id: 7, lang: "pb" })).toBe("/movie/7/search/pb");
  });

  it("emits a bare root for the default library filters", () => {
    expect(buildPath(LIBRARY)).toBe("/");
  });

  it("emits only the filters that differ from their defaults", () => {
    expect(
      buildPath({
        kind: "library",
        filters: { type: "movies", q: "the expanse", missing: true, sort: "missing" },
      }),
    ).toBe("/?type=movies&q=the+expanse&missing=1&sort=missing");
    expect(
      buildPath({ kind: "library", filters: { type: "all", q: "dune", missing: false, sort: "" } }),
    ).toBe("/?q=dune");
  });
});

describe("the library filter codec", () => {
  it("supplies every default the builder omitted", () => {
    expect(parseLibraryFilters("")).toStrictEqual({
      type: "all",
      q: "",
      missing: false,
      sort: "title",
    });
  });

  it("round-trips a non-default filter set through the query string", () => {
    const filters = { type: "movies", q: "the expanse", missing: true, sort: "missing" };
    const qs = buildPath({ kind: "library", filters }).slice(1);

    expect(parseLibraryFilters(qs)).toStrictEqual(filters);
  });

  it("reads missing as a flag rather than a truthy string", () => {
    expect(parseLibraryFilters("?missing=0").missing).toBe(false);
    expect(parseLibraryFilters("?missing=1").missing).toBe(true);
  });
});

describe("mediaParent", () => {
  it("maps every series sub-route onto the series detail", () => {
    const parent = { kind: "series", id: 42 };
    expect(mediaParent({ kind: "series", id: 42 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "series-sync", id: 42 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "series-files", id: 42 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "series-search", id: 42, lang: "fr" })).toStrictEqual(parent);
  });

  it("maps every movie sub-route onto the movie detail", () => {
    const parent = { kind: "movie", id: 7 };
    expect(mediaParent({ kind: "movie", id: 7 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "movie-sync", id: 7 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "movie-files", id: 7 })).toStrictEqual(parent);
    expect(mediaParent({ kind: "movie-search", id: 7, lang: "pb" })).toStrictEqual(parent);
  });

  it("answers null for a route holding no media id", () => {
    expect(mediaParent(LIBRARY)).toBeNull();
    expect(mediaParent({ kind: "settings" })).toBeNull();
    expect(mediaParent({ kind: "history" })).toBeNull();
  });
});

describe("syncRouteFor", () => {
  it("derives the sync route of each media family", () => {
    expect(syncRouteFor({ kind: "series", id: 42 })).toStrictEqual({
      kind: "series-sync",
      id: 42,
    });
    expect(syncRouteFor({ kind: "series-files", id: 42 })).toStrictEqual({
      kind: "series-sync",
      id: 42,
    });
    expect(syncRouteFor({ kind: "movie-search", id: 7, lang: "en" })).toStrictEqual({
      kind: "movie-sync",
      id: 7,
    });
  });

  it("is idempotent on a sync route, which is what stops /sync/sync", () => {
    expect(syncRouteFor({ kind: "series-sync", id: 42 })).toStrictEqual({
      kind: "series-sync",
      id: 42,
    });
  });

  it("answers null for a route holding no media id", () => {
    expect(syncRouteFor(LIBRARY)).toBeNull();
    expect(syncRouteFor({ kind: "settings" })).toBeNull();
    expect(syncRouteFor({ kind: "history" })).toBeNull();
  });
});

describe("kindsMatching", () => {
  it("claims a pathname the route space does not contain with no kind at all", () => {
    expect(kindsMatching("/")).toStrictEqual([]);
    expect(kindsMatching("/movies")).toStrictEqual([]);
    expect(kindsMatching("/nope")).toStrictEqual([]);
  });
});
