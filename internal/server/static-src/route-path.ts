// The DOM-free URL vocabulary: parseRoute is the one read of a location,
// buildPath its inverse, and the library filter codec the query-string half.

/** The library's four filters as values, so the codec never touches a control. */
export interface LibraryFilters {
  readonly type: string;
  readonly q: string;
  readonly missing: boolean;
  readonly sort: string;
}

/** Every view the app has an address for. `/movies` is deliberately absent: it
 *  is a redirect alias onto the filtered library, so buildPath must not emit it. */
export type Route =
  | { readonly kind: "library"; readonly filters: LibraryFilters }
  | { readonly kind: "settings" }
  | { readonly kind: "history" }
  | { readonly kind: "series"; readonly id: number }
  | { readonly kind: "series-search"; readonly id: number; readonly lang: string }
  | { readonly kind: "series-sync"; readonly id: number }
  | { readonly kind: "series-files"; readonly id: number }
  | { readonly kind: "movie"; readonly id: number }
  | { readonly kind: "movie-search"; readonly id: number; readonly lang: string }
  | { readonly kind: "movie-sync"; readonly id: number }
  | { readonly kind: "movie-files"; readonly id: number };

export type RouteKind = Route["kind"];

/** The media kinds: every route carrying an id. */
type MediaKind = Exclude<RouteKind, "library" | "settings" | "history">;

const LIBRARY_DEFAULTS: LibraryFilters = { type: "all", q: "", missing: false, sort: "title" };

/** Decode the library filters out of a query string, supplying every default
 *  buildLibraryQuery omits. */
export function parseLibraryFilters(search: string): LibraryFilters {
  const params = new URLSearchParams(search);
  return {
    type: params.get("type") ?? LIBRARY_DEFAULTS.type,
    q: params.get("q") ?? LIBRARY_DEFAULTS.q,
    missing: params.get("missing") === "1",
    sort: params.get("sort") ?? LIBRARY_DEFAULTS.sort,
  };
}

// A member equal to its default is omitted, so a shared link carries only what
// the reader chose and parseLibraryFilters supplies the rest.
function buildLibraryQuery(f: LibraryFilters): string {
  const params = new URLSearchParams();
  if (f.type !== "" && f.type !== LIBRARY_DEFAULTS.type) {
    params.set("type", f.type);
  }
  if (f.q !== "") {
    params.set("q", f.q);
  }
  if (f.missing) {
    params.set("missing", "1");
  }
  if (f.sort !== "" && f.sort !== LIBRARY_DEFAULTS.sort) {
    params.set("sort", f.sort);
  }
  const qs = params.toString();
  return qs === "" ? "" : `?${qs}`;
}

interface RouteEntry {
  readonly kind: MediaKind;
  readonly pattern: URLPattern;
}

// The parameterless routes: one exact pathname each, so they are compared
// rather than matched. parseRoute and kindsMatching read this one list, so the
// exclusivity kindsMatching reports is the exclusivity the parse acts on.
const STATIC_ROUTES: readonly { readonly path: string; readonly route: Route }[] = [
  { path: "/settings", route: { kind: "settings" } },
  { path: "/history", route: { kind: "history" } },
];

// A :name group spans exactly one path segment, so these patterns are mutually exclusive by construction.
const ROUTE_TABLE: readonly RouteEntry[] = [
  { kind: "series", pattern: new URLPattern({ pathname: "/series/:id" }) },
  { kind: "series-sync", pattern: new URLPattern({ pathname: "/series/:id/sync" }) },
  { kind: "series-files", pattern: new URLPattern({ pathname: "/series/:id/files" }) },
  {
    kind: "series-search",
    pattern: new URLPattern({ pathname: "/series/:id/search/:lang([a-z]{2,3})" }),
  },
  { kind: "movie", pattern: new URLPattern({ pathname: "/movie/:id" }) },
  { kind: "movie-sync", pattern: new URLPattern({ pathname: "/movie/:id/sync" }) },
  { kind: "movie-files", pattern: new URLPattern({ pathname: "/movie/:id/files" }) },
  {
    kind: "movie-search",
    pattern: new URLPattern({ pathname: "/movie/:id/search/:lang([a-z]{2,3})" }),
  },
];

/** The one place a path's id segment becomes a number, and the only judge of
 *  what counts as one: decimal digits naming a value the number type can spell
 *  back. Leading zeros are read rather than refused, so `/series/007` is series
 *  7; an id past `MAX_SAFE_INTEGER` is refused, because there the digits stop
 *  round-tripping — `"9007199254740993"` reads back as `...992` and `1e21`
 *  builds as `"1e+21"` — so buildPath would emit a path this did not parse.
 *  Anything else answers null and its caller folds onto the library rather than
 *  throwing. */
function mediaID(raw: string | undefined): number | null {
  if (raw === undefined || !/^\d+$/.test(raw)) {
    return null;
  }
  const id = Number(raw);
  return Number.isSafeInteger(id) ? id : null;
}

function mediaRoute(kind: MediaKind, id: number, lang: string): Route {
  return kind === "series-search" || kind === "movie-search" ? { kind, id, lang } : { kind, id };
}

/** Read a location into a Route. Never throws: a pathname the route space
 *  cannot hold answers the library, which is the app's default view. */
export function parseRoute(pathname: string, search = ""): Route {
  for (const entry of STATIC_ROUTES) {
    if (pathname === entry.path) {
      return entry.route;
    }
  }
  for (const entry of ROUTE_TABLE) {
    const m = entry.pattern.exec({ pathname });
    if (m === null) {
      continue;
    }
    const id = mediaID(m.pathname.groups["id"]);
    if (id === null) {
      // Mutually exclusive, so no later entry can claim a pathname this one did.
      break;
    }
    return mediaRoute(entry.kind, id, m.pathname.groups["lang"] ?? "");
  }
  return { kind: "library", filters: parseLibraryFilters(search) };
}

/** buildPath is parseRoute's inverse: `parseRoute(buildPath(r))` is r. The
 *  reverse composition is a canonicalisation rather than an identity —
 *  `/series/007` builds back as `/series/7`, and default filters are dropped. */
export function buildPath(route: Route): string {
  switch (route.kind) {
    case "library":
      return `/${buildLibraryQuery(route.filters)}`;
    case "settings":
      return "/settings";
    case "history":
      return "/history";
    case "series":
      return `/series/${route.id}`;
    case "series-sync":
      return `/series/${route.id}/sync`;
    case "series-files":
      return `/series/${route.id}/files`;
    case "series-search":
      return `/series/${route.id}/search/${route.lang}`;
    case "movie":
      return `/movie/${route.id}`;
    case "movie-sync":
      return `/movie/${route.id}/sync`;
    case "movie-files":
      return `/movie/${route.id}/files`;
    case "movie-search":
      return `/movie/${route.id}/search/${route.lang}`;
  }
}

/** Which media family a route belongs to, the classification mediaParent and
 *  syncRouteFor share so the two cannot disagree about a kind. */
function mediaFamily(
  route: Route,
): { readonly family: "series" | "movie"; readonly id: number } | null {
  switch (route.kind) {
    case "series":
    case "series-search":
    case "series-sync":
    case "series-files":
      return { family: "series", id: route.id };
    case "movie":
    case "movie-search":
    case "movie-sync":
    case "movie-files":
      return { family: "movie", id: route.id };
    case "library":
    case "settings":
    case "history":
      return null;
  }
}

/** The bare detail route a media sub-route belongs to: the view a dialog-owned
 *  URL segment closes back to. Null for a route holding no media id, which is a
 *  location the dialog must leave alone. */
export function mediaParent(route: Route): Route | null {
  const m = mediaFamily(route);
  if (m === null) {
    return null;
  }
  return m.family === "series" ? { kind: "series", id: m.id } : { kind: "movie", id: m.id };
}

/** The sync route for whatever media route a location names, so the sync
 *  dialog's URL is derived rather than accumulated by concatenation. */
export function syncRouteFor(route: Route): Route | null {
  const m = mediaFamily(route);
  if (m === null) {
    return null;
  }
  return m.family === "series"
    ? { kind: "series-sync", id: m.id }
    : { kind: "movie-sync", id: m.id };
}

/** Which kinds claim this pathname: exactly one for every path the app
 *  produces and zero for anything else. Counting is the only way to assert
 *  mutual exclusivity, so this exists for router.test.ts and has no production
 *  caller. */
export function kindsMatching(pathname: string): RouteKind[] {
  const kinds: RouteKind[] = [];
  for (const entry of STATIC_ROUTES) {
    if (pathname === entry.path) {
      kinds.push(entry.route.kind);
    }
  }
  for (const entry of ROUTE_TABLE) {
    if (entry.pattern.test({ pathname })) {
      kinds.push(entry.kind);
    }
  }
  return kinds;
}
