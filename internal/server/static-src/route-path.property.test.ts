// route-path.property.test.ts — the codec's round trip.
//
// `parseRoute(buildPath(r)) === r` is the property that holds. The REVERSE
// composition is a CANONICALISATION rather than an identity, deliberately:
// `/series/007` parses to id 7 and builds back as `/series/7`, and a library
// filter equal to its default is dropped from the query string. So do not
// "fix" the asymmetry — the second property below pins the canonicalisation as
// idempotent, which is the honest statement about the non-canonical space.
import { describe, it, expect } from "vitest";
import fc from "fast-check";
import { buildPath, parseRoute, type LibraryFilters, type Route } from "./route-path.js";

const LETTERS = "abcdefghijklmnopqrstuvwxyz".split("");

// The whole id space parseRoute admits, not just the small ids the arrs mint:
// mediaID's ceiling is MAX_SAFE_INTEGER, and a generator that stopped at
// fc.nat()'s 2^31 default would never reach it.
const idArb = fc.maxSafeNat();

const langArb = fc
  .array(fc.constantFrom(...LETTERS), { minLength: 2, maxLength: 3 })
  .map((cs) => cs.join(""));

// An object LITERAL rather than fc.record: a record's value carries a null
// prototype, which toStrictEqual reads as a difference the printer cannot show
// ("Compared values have no visual difference"), so the property would fail on
// the codec's behalf.
function filtersArb(type: fc.Arbitrary<string>, sort: fc.Arbitrary<string>) {
  return fc
    .tuple(type, fc.string(), fc.boolean(), sort)
    .map(([t, q, missing, s]): LibraryFilters => ({ type: t, q, missing, sort: s }));
}

// `type` and `sort` are non-empty in the canonical space because an empty one
// is not a canonical filter set: both an empty string and the default build to
// nothing, so the parse can only answer with the default. The controls cannot
// produce one either — each is a <select> over its own option values.
const canonicalFiltersArb = filtersArb(fc.string({ minLength: 1 }), fc.string({ minLength: 1 }));

const anyFiltersArb = filtersArb(fc.string(), fc.string());

function libraryArb(filters: fc.Arbitrary<LibraryFilters>): fc.Arbitrary<Route> {
  return filters.map((f): Route => ({ kind: "library", filters: f }));
}

const plainMediaArb: fc.Arbitrary<Route> = fc
  .tuple(
    fc.constantFrom(
      "series" as const,
      "series-sync" as const,
      "series-files" as const,
      "movie" as const,
      "movie-sync" as const,
      "movie-files" as const,
    ),
    idArb,
  )
  .map(([kind, id]): Route => ({ kind, id }));

const searchMediaArb: fc.Arbitrary<Route> = fc
  .tuple(fc.constantFrom("series-search" as const, "movie-search" as const), idArb, langArb)
  .map(([kind, id, lang]): Route => ({ kind, id, lang }));

const settingsArb: fc.Arbitrary<Route> = fc.constant({ kind: "settings" });
const historyArb: fc.Arbitrary<Route> = fc.constant({ kind: "history" });

const canonicalRouteArb: fc.Arbitrary<Route> = fc.oneof(
  libraryArb(canonicalFiltersArb),
  settingsArb,
  historyArb,
  plainMediaArb,
  searchMediaArb,
);

const anyRouteArb: fc.Arbitrary<Route> = fc.oneof(
  libraryArb(anyFiltersArb),
  settingsArb,
  historyArb,
  plainMediaArb,
  searchMediaArb,
);

/** Read a built path back the way a location does: pathname and search apart. */
function reparse(path: string): Route {
  const q = path.indexOf("?");
  return q === -1 ? parseRoute(path) : parseRoute(path.slice(0, q), path.slice(q));
}

describe("the route codec", () => {
  it("reads back every route it builds", () => {
    fc.assert(
      fc.property(canonicalRouteArb, (route) => {
        expect(reparse(buildPath(route))).toStrictEqual(route);
      }),
    );
  });

  it("canonicalises idempotently, so a second pass moves nothing", () => {
    fc.assert(
      fc.property(anyRouteArb, (route) => {
        const once = reparse(buildPath(route));

        expect(reparse(buildPath(once))).toStrictEqual(once);
      }),
    );
  });
});

// The pathnames the app produces, generated rather than tabulated so the
// canonical spelling is the whole input space.
const canonicalPathArb: fc.Arbitrary<string> = fc.oneof(
  fc
    .tuple(fc.constantFrom("series", "movie"), idArb, fc.constantFrom("", "/sync", "/files"))
    .map(([family, id, tail]) => `/${family}/${String(id)}${tail}`),
  fc
    .tuple(fc.constantFrom("series", "movie"), idArb, langArb)
    .map(([family, id, lang]) => `/${family}/${String(id)}/search/${lang}`),
  fc.constantFrom("/", "/settings", "/history"),
);

describe("buildPath over a parsed pathname", () => {
  it("rebuilds a canonically spelled pathname byte for byte", () => {
    fc.assert(
      fc.property(canonicalPathArb, (path) => {
        expect(buildPath(parseRoute(path))).toBe(path);
      }),
    );
  });
});
