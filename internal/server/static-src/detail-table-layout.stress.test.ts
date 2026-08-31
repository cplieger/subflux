// detail-table-layout.stress.test.ts — task 19's 1,100-row detail layout
// measurement in real Chromium (fixed 1280×720 viewport from vitest.config):
// layout cost with the shipped `content-visibility: auto` containment ON
// versus the same tree with the property STRIPPED via a style override.
// Same real-stylesheet harness as detail-table-layout.test.ts; the numbers
// are recorded as evidence, and the one gate is the SOFT gate the task
// states: the ON case must not be slower than OFF (medians, after warmups).
import { describe, it, vi, expect } from "vitest";

import tokensCSS from "./css/_shared-tokens.css?raw";
import baseCSS from "./css/02-base.css?raw";
import componentsCSS from "./css/03-components.css?raw";
import cardCSS from "./css/05-card.css?raw";
import tableCSS from "./css/06-table.css?raw";

vi.mock("./wire/client.gen.js", () => ({
  mediaEpisodes: () => Promise.resolve(null),
  coverageSeriesDetail: () => Promise.resolve([]),
  coverageMovieSubs: () => Promise.resolve([]),
  stateIDs: () => Promise.resolve(null),
}));
vi.mock("@cplieger/actions", () => ({ registerCleanup: () => undefined }));
vi.mock("./bus.js", () => ({
  on: () => () => undefined,
  emit: () => undefined,
  BusEvent: {
    PanelConfigure: "panel:configure",
    NavHistory: "nav:history",
    OpenSeries: "open:series",
    OpenMovie: "open:movie",
    ScanSeries: "scan:series",
    ScanMovie: "scan:movie",
  },
}));
vi.mock("./search.js", () => ({ openSearchPopup: () => undefined }));
vi.mock("./sync.js", () => ({
  openSyncDialog: () => undefined,
  confirmSeasonSync: () => undefined,
}));
vi.mock("./files.js", () => ({ openFileManager: () => undefined }));
vi.mock("./config.js", () => ({ openConfig: () => undefined }));
vi.mock("./detail-scan.js", () => ({
  triggerSeriesScan: () => undefined,
  triggerSeasonScan: () => undefined,
  triggerMovieScan: () => undefined,
  registerScanButton: () => undefined,
}));
vi.mock("./store.js", () => ({
  get: (k: string): unknown => (k === "ignoredCodecs" ? new Set<string>() : null),
  set: () => undefined,
}));

import { renderSeriesDetail } from "./detail.js";
import type { SeriesItem, SeasonGroup } from "./api-types.js";

// The real stylesheets, once per file (MANIFEST slice order).
const style = document.createElement("style");
style.textContent = [tokensCSS, baseCSS, componentsCSS, cardCSS, tableCSS].join("\n");
document.head.appendChild(style);

// 50 seasons × 22 file-bearing episodes = the 1,100-row detail table.
const SEASONS = 50;
const EPS_PER_SEASON = 22;

function makeSeries(): SeriesItem {
  const episodes = SEASONS * EPS_PER_SEASON;
  return {
    title: "Reference Longrunner",
    audio_lang: "en",
    rule: "en",
    id: 1,
    year: 2020,
    tvdb_id: 1,
    episodes,
    targets: [{ language: "en", variant: "standard", have: 0, total: episodes, have_ignored: 0 }],
  };
}

function makeSeasons(): SeasonGroup[] {
  const seasons: SeasonGroup[] = [];
  for (let s = 1; s <= SEASONS; s++) {
    const episodes = [];
    for (let e = 1; e <= EPS_PER_SEASON; e++) {
      episodes.push({
        id: s * 1000 + e,
        season: s,
        episode: e,
        title: `Episode ${s}x${e}`,
        has_file: true,
      });
    }
    seasons.push({ season: s, episodes });
  }
  return seasons;
}

function mountPanel(): HTMLElement {
  document.body.innerHTML =
    '<section class="card" id="coveragePanel">' +
    '<div class="card-head"><h2 id="lib-heading">Reference Longrunner</h2></div>' +
    '<div id="coverageContent"></div></section>';
  const panel = document.getElementById("coveragePanel");
  if (!panel) {
    throw new Error("panel missing");
  }
  return panel;
}

/** Two rendering opportunities: content-visibility relevancy settles at
 *  frame production, one frame after mutation. */
function settled(): Promise<void> {
  return new Promise((resolve) => {
    requestAnimationFrame(() => {
      requestAnimationFrame(() => {
        resolve();
      });
    });
  });
}

function seriesTable(): HTMLTableElement {
  const t = document.querySelector<HTMLTableElement>("table.series-detail");
  if (!t) {
    throw new Error("series table not mounted");
  }
  return t;
}

function episodeRow(): HTMLElement {
  const r = [...seriesTable().querySelectorAll<HTMLElement>("tbody tr")].find(
    (tr) => tr.className === "",
  );
  if (!r) {
    throw new Error("no episode row");
  }
  return r;
}

const WARMUPS = 5;
const SAMPLES = 15;

/** Layout cost: invalidate the table's geometry (the panel width feeds the
 *  %-based grid tracks), then force one synchronous whole-tree layout and
 *  time it. With containment ON, off-screen rows are skipped at the
 *  contain-intrinsic-size placeholder; OFF lays out all 1,100 rows. */
function measureLayoutMs(panel: HTMLElement): number[] {
  for (let i = 0; i < WARMUPS; i++) {
    panel.style.width = i % 2 === 0 ? "1000px" : "1001px";
    void seriesTable().offsetHeight;
  }
  const samples: number[] = [];
  for (let i = 0; i < SAMPLES; i++) {
    panel.style.width = i % 2 === 0 ? "1002px" : "1000px";
    const t0 = performance.now();
    void seriesTable().offsetHeight;
    samples.push(performance.now() - t0);
  }
  panel.style.width = "";
  return samples;
}

function median(values: number[]): number {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  const a = sorted[mid];
  const b = sorted.length % 2 === 0 ? sorted[mid - 1] : a;
  return ((a ?? 0) + (b ?? 0)) / 2;
}

describe("series-detail containment stress: 1,100 rows ON vs OFF", () => {
  it(
    "containment ON lays out no slower than the stripped tree (soft gate; numbers recorded)",
    { timeout: 60_000 },
    async () => {
      // --- ON: the shipped CSS as-is ---
      const panel = mountPanel();
      window.scrollTo(0, 0);
      const buildStart = performance.now();
      renderSeriesDetail(makeSeries(), makeSeasons(), [], new Set());
      const buildMs = performance.now() - buildStart;
      await settled();
      expect(
        seriesTable().querySelectorAll("tbody tr:not(.season-head):not(.season-gap)").length,
      ).toBe(1100);
      expect(getComputedStyle(episodeRow()).contentVisibility).toBe("auto");
      const onSamples = measureLayoutMs(panel);
      await settled();

      // --- OFF: the same tree with the css property stripped ---
      const override = document.createElement("style");
      override.textContent =
        "table.series-detail tbody tr { content-visibility: visible !important; contain-intrinsic-size: none !important; }";
      document.head.appendChild(override);
      await settled();
      expect(getComputedStyle(episodeRow()).contentVisibility).toBe("visible");
      const offSamples = measureLayoutMs(panel);
      override.remove();
      await settled();

      const onMedian = median(onSamples);
      const offMedian = median(offSamples);
      console.warn(
        `[stress] 1100-row detail layout (1280×720, ${String(WARMUPS)} warmups, ` +
          `${String(SAMPLES)} samples): build=${buildMs.toFixed(1)}ms ` +
          `containment ON median=${onMedian.toFixed(2)}ms ` +
          `(samples ${onSamples.map((s) => s.toFixed(1)).join("/")}) ` +
          `OFF median=${offMedian.toFixed(2)}ms ` +
          `(samples ${offSamples.map((s) => s.toFixed(1)).join("/")})`,
      );

      // THE SOFT GATE (the task's stated threshold): the ON case must not be
      // slower than OFF — fail only if ON > OFF on the medians.
      expect(onMedian).toBeLessThanOrEqual(offMedian);
    },
  );
});
