// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach } from "vitest";

vi.mock("./api-client.js", () => ({ apiGet: vi.fn().mockResolvedValue(null) }));
vi.mock("./actions/index.js", () => ({
  registerCleanup: vi.fn(),
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue(null) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./bus.js", () => ({
  on: vi.fn(() => () => {}),
  emit: vi.fn(),
  BusEvent: {
    PanelConfigure: "panel:configure",
    NavHistory: "nav:history",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
  },
}));
vi.mock("./search.js", () => ({ openSearchPopup: vi.fn() }));
vi.mock("./sync.js", () => ({ openSyncDialog: vi.fn() }));
vi.mock("./files.js", () => ({ openFileManager: vi.fn() }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: vi.fn(),
  triggerSeasonScan: vi.fn(),
  triggerMovieScan: vi.fn(),
}));
vi.mock("./detail-season-sync.js", () => ({ confirmSeasonSync: vi.fn() }));

import * as store from "./store.js";

describe("detail: renderSeriesDetail", () => {
  beforeEach(() => {
    document.body.innerHTML =
      '<div id="coveragePanel"><div class="card-head"><h2 id="lib-heading"></h2></div><div id="coverageContent"></div></div>';
    store.set("detailCtx", null);
  });

  it.todo("renders season headers with correct labels");

  it.todo("renders episode rows keyed by tvdbMediaId");

  it.todo("skips episodes without has_file");

  it.todo("shows season gap spacer between seasons");

  it.todo("renders coverage badges per target language");

  it.todo("season search button triggers triggerSeasonScan");

  it.todo("season sync button only appears when syncable episodes exist");

  it.todo("season history button only appears when history entries exist");

  it.todo("absolute episode numbering shown when hasAbsOrder is true");

  it.todo("reconcile preserves existing episode rows on re-render");
});

describe("detail: openMovieDetail", () => {
  it.todo("sets detailCtx with movie=true and tmdbId");

  it.todo("emits PanelConfigure with title and info");

  it.todo("renders subtitle badges for movie targets");

  it.todo("shows search button per target language");
});
