// Subflux Web UI — ES module entry point.

import { initActions } from "./actions-boot.js";
import * as store from "./store.js";
import * as notify from "./notify.js";
import { on, BusEvent } from "./bus.js";

// Wire @cplieger/actions notifier + API layer before any action is created.
initActions();
import * as events from "./events.js";
import * as theme from "./theme.js";
import { pollStatus, pollStatusAction, updateLiveTimers } from "./status.js";
import { loadCoverage, filterCoverage } from "./coverage.js";
import { renderSeriesDetail, openMovieDetail } from "./detail.js";
import { closeSearchPopup } from "./search.js";
import { consumeSyncClosing } from "./sync.js";
import { navigate, navigateToHistory, applyRoute, updateLibraryFilters } from "./router.js";
import { reloadHistory } from "./history.js";
import { openConfig, closeConfig, saveConfig, initLanguages } from "./config.js";
import { initUserMenu } from "./user-menu.js";
import { initSecurity } from "./security.js";
import { el, dialog, onBackdropClose, $ } from "./dom.js";
import { patch } from "@cplieger/reactive";
import { apiGet, apiGetArray } from "./api-client.js";
import { subscribeToActions, registerCleanup, pollAction } from "@cplieger/actions";
import { viewTransition, debounce } from "./utils.js";
import { STATUS_POLL_MS } from "./constants.js";
import type { MovieItem } from "./api-types.js";
import { decodeSubtitleEntry, decodeMovieItem } from "./wire/decoders.gen.js";

// Initialize store.
store.batch(() => {
  store.set("config", null);
  store.set("configChecked", false);
  store.set("ignoredCodecs", new Set<string>());
  store.set("detailCtx", null);
  store.set("currentPage", "library");
  store.set("scanInFlight", false);
  store.set("refreshPending", false);
});

// Derived state: eliminates repeated guard checks across all modules.
// Dependencies are auto-discovered via store.get() interception.
store.computed("isUnconfigured", () => {
  const cfg = store.get("config");
  return cfg?.configured === false;
});
store.computed("isReady", () => store.get("configChecked") && !store.get("isUnconfigured"));

// Cache dialog references (typed as HTMLDialogElement).
const searchDlg = dialog("searchResultPopup");
const configDlg = dialog("configDialog");

function refreshCurrentPage(): void {
  const ctx = store.get("detailCtx");
  if (ctx && "files" in ctx && ctx.files) {
    // Files page handles its own refresh after delete; skip SSE-triggered refreshes.
    return;
  }
  // refreshCurrentPage re-fetches data and passes it to detail renderers.
  if (ctx && "tvdbId" in ctx && ctx.tvdbId) {
    void Promise.all([
      apiGetArray(`/api/coverage/series/${ctx.tvdbId}`, decodeSubtitleEntry),
      apiGet<string[]>(`/api/state/ids?type=episode&prefix=tvdb-${ctx.tvdbId}-`),
    ]).then(([subFiles, historyIDs]) => {
      renderSeriesDetail(ctx.series, ctx.seasons, subFiles ?? [], new Set(historyIDs ?? []));
    });
  } else if (ctx && "movie" in ctx && ctx.movie) {
    // Movie detail: re-fetch coverage and re-render.
    void apiGetArray("/api/coverage/movies", decodeMovieItem).then((movies) => {
      if (!movies) {
        return;
      }
      const m = movies.find((x: MovieItem) => x.tmdb_id === ctx.tmdbId);
      if (m?.id != null && m.title) {
        openMovieDetail(m, true);
      } else {
        void loadCoverage(true);
      }
    });
  } else {
    void loadCoverage(true);
  }
}

// Any module can trigger a refresh by emitting BusEvent.DataInvalidate.
// Defer refresh while a scan button is active (prevents DOM replacement
// from detaching the button reference mid-animation).
on(BusEvent.DataInvalidate, () => {
  if (store.get("scanInFlight")) {
    store.set("refreshPending", true);
  } else {
    refreshCurrentPage();
  }
});

events.connect();

// --- Init ---
theme.init();
void initLanguages();
initUserMenu();
initSecurity();

// Action-framework global: live-log every action error to the browser
// console so failures are visible in DevTools regardless of toast policy
// (suppressed-toast actions still get logged).
subscribeToActions((inst) => {
  if (inst.status !== "error" || inst.error === undefined) {
    return;
  }
  const meta: string[] = [];
  if (inst.completedAt !== undefined) {
    meta.push(`${String(inst.completedAt - inst.startedAt)}ms`);
  }
  if (inst.attempts !== undefined && inst.attempts > 1) {
    meta.push(`${String(inst.attempts)} attempts`);
  }
  if (inst.error.status !== undefined) {
    meta.push(`HTTP ${String(inst.error.status)}`);
  }
  if (inst.error.code !== undefined) {
    meta.push(inst.error.code);
  }
  console.error(
    `[action] ${inst.name} failed (${meta.join(", ")}): ${inst.error.message}`,
    inst.error,
  );
});

const footerYear = document.getElementById("footerYear");
if (footerYear) {
  footerYear.textContent = String(new Date().getFullYear());
}

// Check if the server is configured; auto-open settings if not.
void apiGet<Record<string, unknown>>("/api/config/parsed").then((pc) => {
  store.batch(() => {
    store.set("configChecked", true);
    if (pc) {
      store.set("config", pc);
      store.set("ignoredCodecs", new Set((pc["ignored_codecs"] as string[] | undefined) ?? []));
    }
  });
  if (pc?.["configured"] === false) {
    openConfig(true);
  }
});

// Route-based initialization: render the correct view for the current URL.
void applyRoute();

// Listen for browser back/forward.
window.addEventListener("popstate", () => {
  // Closing the sync dialog pops the /sync history entry; the detail
  // page is already rendered underneath, so skip the re-render.
  if (consumeSyncClosing()) {
    return;
  }
  if (searchDlg.open) {
    searchDlg.close();
  }
  // Don't close config dialog when unconfigured; user must save first.
  if (configDlg.open && !isUnconfigured()) {
    configDlg.close();
  }
  viewTransition(() => {
    void applyRoute();
  });
});

// Background polls. The status poll uses pollAction which adds:
//   - pause-when-hidden: saves battery + server load while the tab is
//     in the background. Resume on visibilitychange.
//   - refresh-on-focus: instant freshness when the user returns to the
//     tab — alerts / activities / providers are repainted immediately.
//   - auto cleanup: pollAction registers its own cleanup hook for
//     beforeunload drain (no manual clearInterval needed).
//
// updateLiveTimers is a UI tick (formats running durations on screen).
// It doesn't dispatch any action, so pollAction can't help — raw
// setInterval + manual cleanup is correct here.
pollAction(pollStatusAction, undefined, {
  interval: STATUS_POLL_MS,
});
const liveTimerId = setInterval(updateLiveTimers, 1000);
registerCleanup(() => {
  clearInterval(liveTimerId);
});

// Helper: check unconfigured state (used by dialog event handlers).
function isUnconfigured(): boolean {
  return store.get("isUnconfigured");
}

// Header buttons
const titleLink = document.querySelector("header h1 a");
if (titleLink) {
  titleLink.addEventListener("click", (e: Event) => {
    e.preventDefault();
    navigate("/");
  });
}
const themeBtn = document.getElementById("themeBtn");
if (themeBtn) {
  themeBtn.addEventListener("click", theme.cycle);
}
$.historyBtn.addEventListener("click", () => {
  if (store.get("currentPage") === "history") {
    navigate("/");
  } else {
    navigateToHistory();
  }
});
const configBtn = document.getElementById("configBtn");
if (configBtn) {
  configBtn.addEventListener("click", () => {
    openConfig();
  });
}
$.configClose.addEventListener("click", closeConfig);

// Config dialog backdrop click.
configDlg.addEventListener("click", (e: MouseEvent) => {
  if (e.target === e.currentTarget) {
    if (isUnconfigured()) {
      notify.error("Save a valid configuration before closing settings");
      return;
    }
    closeConfig();
  }
});

// Config dialog native Escape (cancel event fires before close).
configDlg.addEventListener("cancel", (e: Event) => {
  if (isUnconfigured()) {
    e.preventDefault();
    return;
  }
  if (location.pathname === "/settings") {
    navigate("/");
  }
});

// Intercept Escape at keydown level to show toast and fully prevent
// the dialog from closing when unconfigured. The cancel event alone
// is not reliable across all browsers.
configDlg.addEventListener("keydown", (e: KeyboardEvent) => {
  if (e.key === "Escape" && isUnconfigured()) {
    e.preventDefault();
    e.stopPropagation();
    notify.error("Save a valid configuration before closing settings");
  }
});

// Coverage controls — filter changes update both the view and the URL.
// Text input is debounced at 150ms to avoid janking on large libraries.
const debouncedFilter = debounce(() => {
  filterCoverage();
  updateLibraryFilters();
}, 150);

function wireFilter(id: string, event: string, handler: () => void): void {
  const filterEl = document.getElementById(id);
  if (filterEl) {
    filterEl.addEventListener(event, handler);
  }
}

wireFilter("cov-type-filter", "change", () => {
  filterCoverage();
  updateLibraryFilters();
});
wireFilter("cov-filter", "input", debouncedFilter);
wireFilter("cov-missing", "change", () => {
  filterCoverage();
  updateLibraryFilters();
});
wireFilter("cov-sort", "change", () => {
  filterCoverage();
  updateLibraryFilters();
});

// History filters — text input debounced.
const debouncedHistoryFilter = debounce(reloadHistory, 300);

const historyChange = (): void => {
  if (store.get("currentPage") === "history") {
    reloadHistory();
  }
};
wireFilter("h-type", "change", historyChange);
wireFilter("h-lang", "change", historyChange);
wireFilter("h-provider", "change", historyChange);
wireFilter("h-filter", "input", debouncedHistoryFilter);

const configForm = configDlg.querySelector("form");
if (configForm) {
  configForm.addEventListener("submit", (e: Event) => {
    e.preventDefault();
    void saveConfig();
  });
}

// Refresh popup content when it opens.
$.statusPopup.addEventListener("toggle", (e: Event) => {
  const te = e as ToggleEvent;
  if (te.newState === "open") {
    // Show skeleton if popup is empty (first open).
    if (!$.statusPopup.children.length) {
      const skel = document.createDocumentFragment();
      for (let i = 0; i < 2; i++) {
        skel.appendChild(
          el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
        );
      }
      patch($.statusPopup, skel);
    }
    void pollStatus();
  }
});

// Close popups on outside click
document.addEventListener("click", (e: MouseEvent) => {
  const t = e.target as HTMLElement;
  if (!t.closest("[popover]") && !t.closest("[popovertarget]")) {
    document.querySelectorAll("[popover]").forEach((p: Element) => {
      if (p.matches(":popover-open")) {
        (p as HTMLElement).hidePopover();
      }
    });
  }
});

// Close popups on Escape key
document.addEventListener("keydown", (e: KeyboardEvent) => {
  if (e.key !== "Escape") {
    return;
  }
  document.querySelectorAll("[popover]").forEach((p: Element) => {
    if (p.matches(":popover-open")) {
      (p as HTMLElement).hidePopover();
    }
  });
});

// Keyboard shortcut: / to focus library search (unless in an input/dialog).
document.addEventListener("keydown", (e: KeyboardEvent) => {
  if (e.key !== "/" || e.ctrlKey || e.metaKey) {
    return;
  }
  const active = document.activeElement;
  if (
    active &&
    (active.tagName === "INPUT" ||
      active.tagName === "TEXTAREA" ||
      active.tagName === "SELECT" ||
      active.closest("dialog"))
  ) {
    return;
  }
  e.preventDefault();
  const searchInput = document.getElementById("cov-filter") ?? document.getElementById("h-filter");
  if (searchInput) {
    searchInput.focus();
  }
});

// Close search dialog on backdrop click.
onBackdropClose(searchDlg, closeSearchPopup);

// Search dialog native Escape.
searchDlg.addEventListener("cancel", () => {
  if (location.pathname.includes("/search/")) {
    const parent = location.pathname.replace(/\/search\/[a-z]{2,3}$/, "");
    history.replaceState(null, "", parent || "/");
  }
});
