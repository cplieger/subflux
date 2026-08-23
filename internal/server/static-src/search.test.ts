// @vitest-environment happy-dom
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";

// search.ts resolves #searchResultPopup at MODULE scope, so the dialog has to
// exist before the import graph loads — vi.hoisted runs early enough, and the
// element then stays attached for the whole file (runPopupSearch looks its
// results pane up with document.getElementById, which a detached dialog fails).
vi.hoisted(() => {
  document.body.innerHTML = '<dialog id="searchResultPopup"></dialog>';
});

// CRITICAL: vitest.config sets clearMocks/mockReset/restoreMocks, so every mock
// factory below is a PLAIN function closing over a vi.hoisted record.

const wire = vi.hoisted(() => ({
  searchResult: { ok: true, status: 200, data: { results: [] } } as {
    ok: boolean;
    status: number;
    data?: { results: unknown[] } | null;
    error?: string;
    code?: string;
  },
  searchQueries: [] as unknown[],
  searchSignals: [] as (AbortSignal | undefined)[],
  searchDefer: false,
  searchRelease: [] as ((v: unknown) => void)[],
  activity: [] as unknown[][],
  activityCalls: 0,
  activitySignals: [] as (AbortSignal | undefined)[],
}));
vi.mock("./wire/client.gen.js", () => ({
  manualSearchRaw: (query: unknown, opts?: { signal?: AbortSignal }) => {
    wire.searchQueries.push(query);
    wire.searchSignals.push(opts?.signal);
    if (wire.searchDefer) {
      return new Promise((resolve) => {
        wire.searchRelease.push(resolve as (v: unknown) => void);
      });
    }
    return Promise.resolve(wire.searchResult);
  },
  listActivity: (opts?: { signal?: AbortSignal }) => {
    const next = wire.activity.length > 1 ? wire.activity.shift() : wire.activity[0];
    wire.activityCalls += 1;
    wire.activitySignals.push(opts?.signal);
    return Promise.resolve(next ?? []);
  },
  PATH_DOWNLOAD_SUBTITLE: "/api/search/download",
}));

// apiAction is replaced so the download's dispatch is drivable and its
// `retryable` predicate — module-private otherwise — is reachable. pollUntil,
// registerCleanup and retryNetwork stay REAL: the poll loop under test is
// search.ts's step + until callbacks, and faking the loop around them would
// prove the fake instead.
interface ActionDef {
  name?: string;
  retryable?: (err: { code?: string; status?: number }) => boolean;
}
const actions = vi.hoisted(() => ({
  defs: new Map<string, unknown>(),
  dispatched: [] as unknown[],
  outcome: { status: "success", value: { activity_id: "act-1", status: "accepted" } } as unknown,
}));
vi.mock("@cplieger/actions", async (importOriginal) => {
  const real = (await importOriginal()) as Record<string, unknown>;
  return {
    ...real,
    apiAction: (def: { name?: string }) => {
      if (def.name !== undefined) {
        actions.defs.set(def.name, def);
      }
      return {
        dispatch: (args: unknown) => {
          actions.dispatched.push(args);
          return { outcome: Promise.resolve(actions.outcome) };
        },
      };
    },
  };
});

const storeState = vi.hoisted(() => ({ config: { languages: ["en", "fr"] } as unknown }));
vi.mock("./store.js", () => ({
  get: (key: string) => (key === "config" ? storeState.config : null),
  set: () => undefined,
}));

const toasts = vi.hoisted(() => ({ errors: [] as string[] }));
vi.mock("./notify.js", () => ({
  error: (msg: string) => {
    toasts.errors.push(msg);
  },
  success: () => undefined,
  info: () => undefined,
}));

const busState = vi.hoisted(() => ({ emitted: [] as string[] }));
vi.mock("./bus.js", () => ({
  emit: (event: string) => {
    busState.emitted.push(event);
  },
  on: () => () => undefined,
  BusEvent: { DataInvalidate: "data:invalidate" },
}));

import { openSearchPopup, closeSearchPopup } from "./search.js";
import type { SearchResult } from "./wire/types.gen.js";

// --- Fixtures (hardcoded, DAMP) ---

function media(over: Record<string, unknown> = {}): {
  id: number;
  tvdb_id?: number;
  tmdb_id?: number;
  imdb_id?: string;
  title: string;
  year?: number;
  scene_name?: string;
} {
  return {
    id: 42,
    tvdb_id: 81189,
    tmdb_id: 1396,
    title: "Breaking Bad",
    year: 2008,
    ...over,
  };
}

function episode(over: Record<string, unknown> = {}): { episode: number } {
  return { episode: 1, ...over };
}

function result(over: Partial<SearchResult> = {}): SearchResult {
  return {
    provider: "opensubtitles",
    language: "en",
    release_name: "Breaking.Bad.S01E01.1080p.WEB-DL",
    matched_by: "imdb",
    subtitle_id: "sub-1",
    tier: "excellent" as SearchResult["tier"],
    score: 92,
    hearing_impaired: false,
    forced: false,
    on_disk: false,
    ...over,
  };
}

function activityEntry(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    id: "act-1",
    action: "download",
    detail: "",
    source: "manual",
    started_at: "2026-03-04T10:00:00Z",
    done: true,
    ...over,
  };
}

// --- Harness ---

/** Flush the module's promise chains. Microtasks only, so the download tests
 *  can drive the real poll loop under fake timers. */
async function settle(): Promise<void> {
  for (let i = 0; i < 12; i += 1) {
    await Promise.resolve();
  }
}

function req<T extends Element>(selector: string): T {
  const found = document.querySelector<T>(selector);
  if (found === null) {
    throw new Error(`missing element: ${selector}`);
  }
  return found;
}

function results(): HTMLElement {
  return req<HTMLElement>("#popup-search-results");
}

function rows(): HTMLElement[] {
  return [...document.querySelectorAll<HTMLElement>(".result-row")].slice(1);
}

/** Open the popup for an episode and let the auto-run search settle. */
async function openEpisodePopup(over: Record<string, unknown> = {}): Promise<void> {
  openSearchPopup("episode", media(over), 1, episode(), "en");
  await settle();
}

async function openMoviePopup(over: Record<string, unknown> = {}): Promise<void> {
  openSearchPopup("movie", media(over), null, null, "en");
  await settle();
}

function lastQuery(): Record<string, unknown> {
  const q = wire.searchQueries.at(-1);
  if (q === undefined) {
    throw new Error("no search request was issued");
  }
  return q as Record<string, unknown>;
}

function downloadButton(): HTMLButtonElement {
  return req<HTMLButtonElement>(".result-dl button");
}

beforeEach(() => {
  req<HTMLDialogElement>("#searchResultPopup").replaceChildren();
  history.replaceState(null, "", "/");
  wire.searchResult = { ok: true, status: 200, data: { results: [] } };
  wire.searchQueries = [];
  wire.searchSignals = [];
  wire.searchDefer = false;
  wire.searchRelease = [];
  wire.activity = [[activityEntry()]];
  wire.activityCalls = 0;
  wire.activitySignals = [];
  actions.dispatched = [];
  actions.outcome = { status: "success", value: { activity_id: "act-1", status: "accepted" } };
  storeState.config = { languages: ["en", "fr"] };
  toasts.errors = [];
  busState.emitted = [];
});

afterEach(() => {
  vi.useRealTimers();
});

describe("openSearchPopup: the dialog and its URL", () => {
  it("opens the results dialog", async () => {
    await openEpisodePopup();

    expect(req<HTMLDialogElement>("#searchResultPopup").open).toBe(true);
  });

  it("pushes a series search URL carrying the tvdb id and language", async () => {
    await openEpisodePopup();

    expect(location.pathname).toBe("/series/81189/search/en");
  });

  it("pushes a movie search URL carrying the tmdb id", async () => {
    await openMoviePopup();

    expect(location.pathname).toBe("/movie/1396/search/en");
  });

  it("leaves the id segment empty when the series has no tvdb id", async () => {
    openSearchPopup("episode", { id: 42, title: "Breaking Bad" }, 1, episode(), "en");
    await settle();

    expect(location.pathname).toBe("/series//search/en");
  });

  it("titles the dialog with the show and its episode number", async () => {
    await openEpisodePopup();

    expect(req(".dlg-title").textContent).toBe("Breaking Bad S01E01");
  });

  it("titles the dialog with the bare name for a movie", async () => {
    await openMoviePopup();

    expect(req(".dlg-title").textContent).toBe("Breaking Bad");
  });

  it("still titles a movie with its bare name when an episode is handed in", async () => {
    // The media TYPE decides whether an episode number belongs in the title;
    // a stray episode argument on a movie must not add one.
    openSearchPopup("movie", media(), 1, episode(), "en");
    await settle();

    expect(req(".dlg-title").textContent).toBe("Breaking Bad");
  });

  it("puts the title inside the dialog's header", async () => {
    await openEpisodePopup();

    expect(req(".dlg-head .dlg-title").textContent).toBe("Breaking Bad S01E01");
  });

  it("groups the language picker and the close button in the header controls", async () => {
    await openEpisodePopup();

    expect([...req(".dlg-controls").children].map((c) => c.tagName)).toEqual(["SELECT", "BUTTON"]);
  });

  it("re-shows the dialog on a reopen instead of leaving the open one in place", async () => {
    await openEpisodePopup();
    const dlg = req<HTMLDialogElement>("#searchResultPopup");
    let closes = 0;
    dlg.addEventListener("close", () => {
      closes += 1;
    });

    // A modal dialog that is already open cannot be re-shown, so it has to be
    // closed first — otherwise a reopen leaves it wherever it already sat in
    // the top layer, underneath anything opened over it.
    await openEpisodePopup();

    expect(closes).toBe(1);
  });

  it("offers every configured language in the picker", async () => {
    storeState.config = { languages: ["en", "fr"] };

    await openEpisodePopup();

    expect([...req<HTMLSelectElement>("#popup-lang-sel").options].map((o) => o.value)).toEqual([
      "en",
      "fr",
    ]);
  });

  it("falls back to English when no languages are configured", async () => {
    storeState.config = null;

    openSearchPopup("episode", media(), 1, episode(), null);
    await settle();

    expect([...req<HTMLSelectElement>("#popup-lang-sel").options].map((o) => o.value)).toEqual([
      "en",
    ]);
  });

  it("preselects the requested language", async () => {
    storeState.config = { languages: ["en", "fr"] };

    openSearchPopup("episode", media(), 1, episode(), "fr");
    await settle();

    expect(req<HTMLSelectElement>("#popup-lang-sel").value).toBe("fr");
  });

  it("defaults to the first configured language when none is requested", async () => {
    storeState.config = { languages: ["fr", "en"] };

    openSearchPopup("episode", media(), 1, episode(), null);
    await settle();

    expect(location.pathname).toBe("/series/81189/search/fr");
  });

  it("re-searches in the newly picked language", async () => {
    await openEpisodePopup();
    const sel = req<HTMLSelectElement>("#popup-lang-sel");

    sel.value = "fr";
    sel.dispatchEvent(new Event("change"));
    await settle();

    expect(lastQuery()["lang"]).toBe("fr");
  });

  it("moves focus into the dialog so the keyboard lands there", async () => {
    await openEpisodePopup();

    expect(document.activeElement).toBe(req("#searchResultPopup"));
  });

  it("closes the popup from its header close button", async () => {
    await openEpisodePopup();

    req<HTMLButtonElement>('[aria-label="Close search"]').click();

    expect(req("#searchResultPopup").className).toContain("is-leaving");
  });

  it("rewrites the URL for the newly picked language without stacking history", async () => {
    await openEpisodePopup();
    const sel = req<HTMLSelectElement>("#popup-lang-sel");

    sel.value = "fr";
    sel.dispatchEvent(new Event("change"));
    await settle();

    expect(location.pathname).toBe("/series/81189/search/fr");
  });
});

describe("closeSearchPopup", () => {
  it("starts the dialog's fade-out", async () => {
    await openEpisodePopup();

    closeSearchPopup();

    expect(req("#searchResultPopup").className).toContain("is-leaving");
  });

  it("returns to the previous view when the popup pushed a history entry", async () => {
    history.replaceState(null, "", "/");
    await openEpisodePopup();

    closeSearchPopup();
    await settle();

    expect(location.pathname).toBe("/");
  });

  it("drops back to the parent view when the search URL was reached directly", async () => {
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();

    closeSearchPopup();

    expect(location.pathname).toBe("/series/81189");
  });

  it("stacks no history entry when the popup opens on the URL it already sits on", async () => {
    history.replaceState(null, "", "/");
    await openEpisodePopup();
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();

    closeSearchPopup();

    expect(location.pathname).toBe("/series/81189");
  });

  it("falls back to the root when the search URL has no parent", async () => {
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();
    history.replaceState(null, "", "/search/en");

    closeSearchPopup();

    expect(location.pathname).toBe("/");
  });

  it("leaves a non-search URL alone", async () => {
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();
    history.replaceState(null, "", "/history");

    closeSearchPopup();

    expect(location.pathname).toBe("/history");
  });

  it("touches no history entry at all when the URL is not a search URL", async () => {
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();
    history.replaceState(null, "", "/history");
    const replace = vi.spyOn(history, "replaceState");

    closeSearchPopup();

    // Rewriting the entry would discard whatever state the view that owns this
    // URL had stored on it, even though the path came out the same.
    expect(replace).not.toHaveBeenCalled();
  });

  it("does not cut a /search/<lang> segment out of the middle of a deeper URL", async () => {
    history.replaceState(null, "", "/series/81189/search/en");
    await openEpisodePopup();
    history.replaceState(null, "", "/series/81189/search/en/extra");

    closeSearchPopup();

    // Only a TRAILING language segment makes a search URL; a deeper path is
    // some other view, and inventing a parent for it navigates somewhere
    // nobody asked for.
    expect(location.pathname).toBe("/series/81189/search/en/extra");
  });

  it("stops treating the popup as history-pushing once it has gone back", async () => {
    history.replaceState(null, "", "/library");
    await openEpisodePopup();

    closeSearchPopup();
    await settle();

    // Now sitting on a search URL the popup did NOT push: the close must
    // rewrite the URL rather than pop another entry off the user's history.
    history.replaceState(null, "", "/series/81189/search/en");
    const back = vi.spyOn(history, "back");

    closeSearchPopup();

    expect(back).not.toHaveBeenCalled();
  });
});

describe("runPopupSearch: what the server is asked", () => {
  it("asks for the requested type and language", async () => {
    await openEpisodePopup();

    expect(lastQuery()).toMatchObject({ type: "episode", lang: "en" });
  });

  it("identifies an episode by tvdb id, season and episode", async () => {
    await openEpisodePopup();

    expect(lastQuery()).toMatchObject({ tvdb: "81189", season: 1, episode: 1 });
  });

  it("passes the imdb id when the series carries one", async () => {
    await openEpisodePopup({ imdb_id: "tt0903747" });

    expect(lastQuery()["imdb"]).toBe("tt0903747");
  });

  it("omits the imdb key entirely when the series has none", async () => {
    await openEpisodePopup();

    expect(Object.keys(lastQuery())).not.toContain("imdb");
  });

  it("passes the scene and absolute numbering when the episode carries it", async () => {
    openSearchPopup(
      "episode",
      media(),
      1,
      episode({ scene_season: 2, scene_episode: 5, absolute_episode: 17 }),
      "en",
    );
    await settle();

    expect(lastQuery()).toMatchObject({ scene_season: 2, scene_episode: 5, absolute_episode: 17 });
  });

  it("passes the episode title and its scene release name", async () => {
    openSearchPopup(
      "episode",
      media(),
      1,
      episode({ title: "Pilot", scene_name: "Breaking.Bad.S01E01.HDTV" }),
      "en",
    );
    await settle();

    expect(lastQuery()).toMatchObject({
      episode_title: "Pilot",
      release: "Breaking.Bad.S01E01.HDTV",
    });
  });

  it("passes the series title and year", async () => {
    await openEpisodePopup();

    expect(lastQuery()).toMatchObject({ title: "Breaking Bad", year: 2008 });
  });

  it("omits the year when the series has none", async () => {
    await openEpisodePopup({ year: undefined });

    expect(Object.keys(lastQuery())).not.toContain("year");
  });

  it("identifies a movie by tmdb id rather than tvdb", async () => {
    await openMoviePopup();

    expect(Object.keys(lastQuery())).toEqual(["type", "lang", "tmdb", "title", "year", "media_id"]);
  });

  it("passes a movie's scene release name", async () => {
    await openMoviePopup({ scene_name: "Movie.2008.1080p.BluRay" });

    expect(lastQuery()["release"]).toBe("Movie.2008.1080p.BluRay");
  });

  it("carries the arr media id so the server can resolve the video", async () => {
    await openEpisodePopup();

    expect(lastQuery()["media_id"]).toBe(42);
  });

  it("sends only the parameters the episode actually carries", async () => {
    await openEpisodePopup();

    expect(Object.keys(lastQuery())).toEqual([
      "type",
      "lang",
      "tvdb",
      "season",
      "episode",
      "title",
      "year",
      "media_id",
    ]);
  });

  it("passes a movie's imdb id when it has one", async () => {
    await openMoviePopup({ imdb_id: "tt1375666" });

    expect(lastQuery()["imdb"]).toBe("tt1375666");
  });

  it("omits a movie's year when it has none", async () => {
    await openMoviePopup({ year: undefined });

    expect(Object.keys(lastQuery())).toEqual(["type", "lang", "tmdb", "title", "media_id"]);
  });

  it("omits the media id when the item has no arr reference", async () => {
    await openEpisodePopup({ id: 0 });

    expect(Object.keys(lastQuery())).not.toContain("media_id");
  });

  it("shows a searching placeholder while the request is open", async () => {
    openSearchPopup("episode", media(), 1, episode(), "en");

    expect(results().textContent).toContain("Searching providers");
  });

  it("spins beside that placeholder so the wait reads as progress", async () => {
    openSearchPopup("episode", media(), 1, episode(), "en");

    // css/09-search.css animates `.spinner` and centres `.empty`; without both
    // classes the wait renders as a bare line of text.
    expect(results().querySelector(".empty .spinner")).not.toBeNull();
  });
});

describe("runPopupSearch: how failures are shown", () => {
  it("explains a provider-disabled failure", async () => {
    wire.searchResult = { ok: false, status: 409, code: "search_provider_disabled" };

    await openEpisodePopup();

    expect(results().textContent).toBe(
      "All search providers are disabled. Enable at least one in settings.",
    );
  });

  it("treats a no-results failure as empty rather than as an error", async () => {
    wire.searchResult = { ok: false, status: 404, code: "search_no_results" };

    await openEpisodePopup();

    expect(results().firstElementChild?.getAttribute("data-status")).toBeNull();
  });

  it("explains a provider cooldown", async () => {
    wire.searchResult = { ok: false, status: 429, code: "provider_timed_out" };

    await openEpisodePopup();

    expect(results().textContent).toBe("This provider is in cooldown; try again in a few minutes.");
  });

  it("marks an unmapped failure as an error", async () => {
    wire.searchResult = { ok: false, status: 500, error: "provider exploded" };

    await openEpisodePopup();

    expect(results().firstElementChild?.getAttribute("data-status")).toBe("err");
  });

  it("shows the server's message for an unmapped failure", async () => {
    wire.searchResult = { ok: false, status: 500, error: "provider exploded" };

    await openEpisodePopup();

    expect(results().textContent).toBe("provider exploded");
  });

  it("falls back to a generic message when the failure carries no text", async () => {
    wire.searchResult = { ok: false, status: 500 };

    await openEpisodePopup();

    expect(results().textContent).toBe("Search failed");
  });

  it("reports an ok response with no payload", async () => {
    wire.searchResult = { ok: true, status: 200, data: null };

    await openEpisodePopup();

    expect(results().textContent).toBe("Empty response");
  });

  it("says so when the providers returned nothing", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [] } };

    await openEpisodePopup();

    expect(results().textContent).toBe("No results found.");
  });

  it("reports a network failure that was not a cancellation", async () => {
    wire.searchResult = { ok: false, status: 0 };

    await openEpisodePopup();

    expect(results().textContent).toBe("Search failed");
  });

  it("leaves the pane alone when a superseded request answers after its abort", async () => {
    wire.searchDefer = true;
    await openEpisodePopup();
    wire.searchDefer = false;
    wire.searchResult = { ok: true, status: 200, data: { results: [] } };

    openSearchPopup("episode", media(), 1, episode(), "en");
    await settle();
    const stale = wire.searchRelease.shift();
    stale?.({ ok: false, status: 0 });
    await settle();

    expect(results().textContent).toBe("No results found.");
  });
});

describe("renderPopupResults", () => {
  it("renders one row per result", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result(), result({ subtitle_id: "sub-2" })] },
    };

    await openEpisodePopup();

    expect(rows()).toHaveLength(2);
  });

  it("heads each result column with its own label", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    // The header row is a grid whose columns are addressed by these class
    // names in css/09-search.css: a label without its class lands in whatever
    // column the browser puts it in, so the header stops matching the rows.
    const header = req<HTMLElement>(".result-row");
    expect(
      [".result-score", ".result-provider", ".result-match", ".result-release", ".result-dl"].map(
        (sel) => header.querySelector(sel)?.textContent,
      ),
    ).toEqual(["Score", "Provider", "Match", "Release", "Download"]);
  });

  it("counts a single result in the singular", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect(req(".result-count").textContent).toBe("1 result");
  });

  it("counts several results in the plural", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result(), result({ subtitle_id: "sub-2" })] },
    };

    await openEpisodePopup();

    expect(req(".result-count").textContent).toBe("2 results");
  });

  it("says how many of a long list are shown", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: Array.from({ length: 31 }, (_, i) => result({ subtitle_id: `s${i}` })) },
    };

    await openEpisodePopup();

    expect(req(".result-count").textContent).toBe("Showing 30 of 31 results (best matches first)");
  });

  it("renders at most thirty rows", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: Array.from({ length: 31 }, (_, i) => result({ subtitle_id: `s${i}` })) },
    };

    await openEpisodePopup();

    expect(rows()).toHaveLength(30);
  });

  it("shows the score, provider, match and release of a result", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect([
      req(".result-row:not(.muted) .result-score").textContent,
      req(".result-row:not(.muted) .result-provider").textContent,
      req(".result-row:not(.muted) .result-match").textContent,
      req(".result-row:not(.muted) .result-release").textContent,
    ]).toEqual(["92", "opensubtitles", "imdb", "Breaking.Bad.S01E01.1080p.WEB-DL "]);
  });

  it("marks the first result as the top match", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result(), result({ subtitle_id: "sub-2" })] },
    };

    await openEpisodePopup();

    expect(rows().map((r) => r.className)).toEqual(["result-row top", "result-row"]);
  });

  it("tells the user the top match downloads as an auto subtitle", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect([
      downloadButton().getAttribute("aria-label"),
      downloadButton().getAttribute("data-tip"),
    ]).toEqual(["Download (auto)", "Top match: downloads as auto subtitle"]);
  });

  it("warns that a lower match downloads as manual", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result(), result({ subtitle_id: "sub-2" })] },
    };

    await openEpisodePopup();

    const second = [...document.querySelectorAll(".result-dl button")][1];
    expect([second?.getAttribute("aria-label"), second?.getAttribute("data-tip")]).toEqual([
      "Download (manual)",
      "Not top match: downloads as manual (pauses automation)",
    ]);
  });

  it("builds the score tooltip from the match breakdown", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result({ matches: { release_group: 20, hash: 50 } })] },
    };

    await openEpisodePopup();

    expect(req(".result-row:not(.muted) .result-score").getAttribute("data-tip")).toBe(
      "Release Group: 20\nHash: 50",
    );
  });

  it("says so when a result has no attribute matches", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result({ matches: {} })] } };

    await openEpisodePopup();

    expect(req(".result-row:not(.muted) .result-score").getAttribute("data-tip")).toBe(
      "No attribute matches",
    );
  });

  it("flags a hearing-impaired release", async () => {
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result({ hearing_impaired: true })] },
    };

    await openEpisodePopup();

    expect(req(".result-hi").textContent).toBe("[HI]");
  });

  it("leaves a plain release unflagged", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect(document.querySelector(".result-hi")).toBeNull();
  });

  it("marks a release already on disk", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result({ on_disk: true })] } };

    await openEpisodePopup();

    expect(req(".result-provider .icon-check")).toBeTruthy();
  });

  it("leaves a release that is not on disk unmarked", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect(document.querySelector(".result-provider .icon-check")).toBeNull();
  });

  it("renders the column headings", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };

    await openEpisodePopup();

    expect(req(".result-row.muted").textContent).toBe("ScoreProviderMatchReleaseDownload");
  });
});

describe("downloadFromPopup", () => {
  async function clickDownload(): Promise<HTMLButtonElement> {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };
    await openEpisodePopup();
    const btn = downloadButton();
    btn.click();
    await settle();
    return btn;
  }

  it("refuses to download an item with no arr reference", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };
    await openEpisodePopup({ id: 0 });

    downloadButton().click();
    await settle();

    expect(toasts.errors).toEqual(["Media reference not available for download."]);
  });

  it("dispatches nothing for an item with no arr reference", async () => {
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };
    await openEpisodePopup({ id: 0 });

    downloadButton().click();
    await settle();

    expect(actions.dispatched).toEqual([]);
  });

  it("sends the subtitle, media and scoring details the server needs", async () => {
    vi.useFakeTimers();
    await clickDownload();

    expect(actions.dispatched).toEqual([
      {
        provider: "opensubtitles",
        subtitle_id: "sub-1",
        release_name: "Breaking.Bad.S01E01.1080p.WEB-DL",
        language: "en",
        season: 1,
        episode: 1,
        media_type: "episode",
        media_id: 42,
        top_pick: true,
        score: 92,
        hearing_impaired: false,
        forced: false,
      },
    ]);
  });

  it("marks a lower pick as a manual download", async () => {
    vi.useFakeTimers();
    wire.searchResult = {
      ok: true,
      status: 200,
      data: { results: [result(), result({ subtitle_id: "sub-2" })] },
    };
    await openEpisodePopup();

    const second = [...document.querySelectorAll<HTMLButtonElement>(".result-dl button")][1];
    second?.click();
    await settle();

    expect((actions.dispatched[0] as { top_pick: boolean }).top_pick).toBe(false);
  });

  it("reports a movie download with no season or episode", async () => {
    vi.useFakeTimers();
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };
    await openMoviePopup();

    downloadButton().click();
    await settle();

    expect(actions.dispatched[0]).toMatchObject({ season: 0, episode: 0, media_type: "movie" });
  });

  it("disables the button while the download is running", async () => {
    vi.useFakeTimers();

    const btn = await clickDownload();

    expect(btn.disabled).toBe(true);
  });

  it("shows the download as pending", async () => {
    vi.useFakeTimers();

    const btn = await clickDownload();

    expect(btn.getAttribute("data-tip")).toBe("Downloading\u2026");
  });

  it("shows a spinner while the activity is polled", async () => {
    vi.useFakeTimers();

    const btn = await clickDownload();

    expect(btn.querySelector(".spinner")).toBeTruthy();
  });

  it("marks a rejected dispatch with a close icon", async () => {
    actions.outcome = { status: "error", error: { code: "provider_error", message: "no route" } };

    const btn = await clickDownload();

    expect(btn.querySelector(".icon-close")).toBeTruthy();
  });

  it("flags a rejected dispatch on the button", async () => {
    actions.outcome = { status: "error", error: { code: "provider_error", message: "no route" } };

    const btn = await clickDownload();

    expect([btn.dataset["status"], btn.getAttribute("data-tip")]).toEqual(["err", "no route"]);
  });

  it("keeps a rejected dispatch disabled unless the download itself failed", async () => {
    actions.outcome = { status: "error", error: { code: "provider_error", message: "no route" } };

    const btn = await clickDownload();

    expect(btn.disabled).toBe(true);
  });

  it("re-enables the button after a failed download so the user can retry", async () => {
    actions.outcome = { status: "error", error: { code: "download_failed", message: "timeout" } };

    const btn = await clickDownload();

    expect(btn.disabled).toBe(false);
  });

  it("falls back to a generic tip when a cancelled dispatch carries no error", async () => {
    actions.outcome = { status: "cancelled" };

    const btn = await clickDownload();

    expect(btn.getAttribute("data-tip")).toBe("Download failed");
  });

  it("marks the button done once the activity completes", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.dataset["status"]).toBe("ok");
  });

  it("shows a check once the activity completes", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.querySelector(".icon-check")).toBeTruthy();
  });

  it("ignores activity entries belonging to other work", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ id: "other-act", done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.dataset["status"]).toBeUndefined();
  });

  it("hands the poll an abort signal so a teardown cancels the fetch", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })]];
    await clickDownload();
    await vi.advanceTimersByTimeAsync(2000);

    window.dispatchEvent(new Event("beforeunload"));

    expect(wire.activitySignals.at(-1)?.aborted).toBe(true);
  });

  it("releases the poll's signal once the download completes", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true })]];
    await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    // That signal composes a five-minute download deadline. Leaving it live
    // after the poll has finished keeps the deadline's timer armed for every
    // download the session ever runs.
    expect(wire.activitySignals.at(-1)?.aborted).toBe(true);
  });

  it("flags the button when the page unloads mid-download", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })]];
    const btn = await clickDownload();
    await vi.advanceTimersByTimeAsync(2000);

    window.dispatchEvent(new Event("beforeunload"));
    await settle();

    expect(btn.getAttribute("data-tip")).toBe("Download timed out");
  });

  it("invalidates the cached data once the download lands", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true })]];
    await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(busState.emitted).toEqual(["data:invalidate"]);
  });

  it("drops the pending tip once the download lands", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.hasAttribute("data-tip")).toBe(false);
  });

  it("flags an activity that finished in failure", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true, failed: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.dataset["status"]).toBe("err");
  });

  it("shows a close icon for an activity that finished in failure", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true, failed: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.querySelector(".icon-close")).toBeTruthy();
  });

  it("invalidates nothing when the activity failed", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: true, failed: true })]];
    await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(busState.emitted).toEqual([]);
  });

  it("keeps polling while the activity is still running", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })], [activityEntry({ done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.dataset["status"]).toBeUndefined();
  });

  it("finishes once a running activity completes", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })], [activityEntry({ done: true })]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(4000);

    expect(btn.dataset["status"]).toBe("ok");
  });

  it("treats an entry evicted after being seen as completion", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })], []];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(4000);

    expect(btn.dataset["status"]).toBe("ok");
  });

  it("keeps waiting for an entry that has not appeared yet", async () => {
    vi.useFakeTimers();
    wire.activity = [[]];
    const btn = await clickDownload();

    await vi.advanceTimersByTimeAsync(6000);

    expect(btn.dataset["status"]).toBeUndefined();
  });

  it("flags the button when the poll is torn down", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })]];
    const btn = await clickDownload();

    closeSearchPopup();
    await vi.advanceTimersByTimeAsync(2000);

    expect([btn.dataset["status"], btn.getAttribute("data-tip")]).toEqual([
      "err",
      "Download timed out",
    ]);
  });

  it("shows a close icon on a torn-down download", async () => {
    vi.useFakeTimers();
    wire.activity = [[activityEntry({ done: false })]];
    const btn = await clickDownload();

    closeSearchPopup();
    await vi.advanceTimersByTimeAsync(2000);

    expect(btn.querySelector(".icon-close")).toBeTruthy();
  });

  it("shows an hourglass the moment the download is dispatched", async () => {
    vi.useFakeTimers();
    wire.searchResult = { ok: true, status: 200, data: { results: [result()] } };
    await openEpisodePopup();
    const btn = downloadButton();

    btn.click();

    expect(btn.querySelector(".icon-hourglass")).toBeTruthy();
  });
});

describe("the download action's retry policy", () => {
  function retryable(err: { code?: string; status?: number }): boolean {
    const def = actions.defs.get("search.download") as ActionDef | undefined;
    if (def?.retryable === undefined) {
      throw new Error("search.download registered no retryable predicate");
    }
    return def.retryable(err);
  }

  it("never retries a failed download, even on a transient status", () => {
    expect(retryable({ code: "download_failed", status: 503 })).toBe(false);
  });

  it("retries a network blip", () => {
    expect(retryable({ code: "network", status: 0 })).toBe(true);
  });

  it("does not retry a plain application error", () => {
    expect(retryable({ code: "validation_error", status: 400 })).toBe(false);
  });
});

describe("page-unload cleanup", () => {
  it("aborts an in-flight search", async () => {
    await openEpisodePopup();
    const signal = wire.searchSignals.at(-1);

    window.dispatchEvent(new Event("beforeunload"));

    expect(signal?.aborted).toBe(true);
  });
});
