// Subflux Web UI — ES module entry point.

import { initActions } from "./actions-boot.js";
import * as store from "./store.js";

// Wire @cplieger/actions notifier + API layer before any action is created.
initActions();
import * as events from "./events.js";
import * as theme from "./theme.js";
import { initStatusPopover, initStatusReconcile, updateLiveTimers } from "./status.js";
import { initScanButtons } from "./detail-scan.js";
import { filterCoverage } from "./coverage.js";
import { closeSearchPopup } from "./search.js";
import { consumeSyncClosing } from "./sync.js";
import { navigate, navigateToHistory, applyRoute, updateLibraryFilters } from "./router.js";
import { reloadHistory, reArmHistoryLatch } from "./history.js";
import { onHealReset } from "./coverage-heal.js";
import { openConfig, closeConfig, saveConfig, initLanguages } from "./config.js";
import { initUserMenu } from "./user-menu.js";
import { initSecurity } from "./security.js";
import { sendWebAuthnSignals } from "./webauthn-utils.js";
import { dialog, onBackdropClose, closeDialog, $ } from "./dom.js";
import { initTooltips } from "@cplieger/ui-primitives/tooltip";
import { configParsed } from "./wire/client.gen.js";
import { subscribeToActions, registerCleanup } from "@cplieger/actions";
import { viewTransition, debounce } from "./utils.js";

// Initialize store.
store.batch(() => {
  store.set("config", null);
  store.set("configChecked", false);
  store.set("ignoredCodecs", new Set<string>());
  store.set("detailCtx", null);
  store.set("currentPage", "library");
  store.set("runningScansByScope", new Map());
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

// The page-leg dispatcher (page-leg.ts, loaded via router.ts) owns the
// BusEvent.DataInvalidate handler and the per-route refresh enumeration.

// E4: history's pending-reload latch re-arms when a full-pair overwrite
// resets in-flight heals (composition-root wiring — history.ts must not pull
// the coverage graph into its own imports).
onHealReset(reArmHistoryLatch);

events.connect();

// --- Init ---
theme.init();
// Delegated tooltip controller (@cplieger/ui-primitives). Reads the existing
// `data-tip` attributes in place (no attribute migration). delayCold/delayWarm
// are both 300ms to reproduce subflux's prior flat 300ms show delay.
initTooltips({ attribute: "data-tip", delayCold: 300, delayWarm: 300 });
void initLanguages();
initUserMenu();
initStatusPopover();
initSecurity();
// Reconcile the user's passkey provider with what this server holds, once per
// authenticated boot. Deliberately here and not on the login page: every login
// path lands here, so no sign-in mechanism can be forgotten, and the login page
// navigates immediately, which would abandon the signal it just sent.
void sendWebAuthnSignals();
// Scan buttons key off the shared runningScansByScope store: install the
// effect that repaints every annotated button when the map changes.
initScanButtons();

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
void configParsed().then((pc) => {
  store.batch(() => {
    store.set("configChecked", true);
    if (pc) {
      store.set("config", pc);
      store.set("ignoredCodecs", new Set(pc.ignored_codecs ?? []));
    }
  });
  if (pc?.configured === false) {
    openConfig(true);
  }
});

// Route-based initialization: render the correct view for the current URL.
// Boot page loads are epoch-gated on EVERY route (E3): the apply waits for
// the first epoch or the gate's degrade (refusal/failure/deadline — refusals
// fail fast, so a 401 redirect never waits out the deadline). On a clean
// epoch the boot transaction's legs cover the loads (the route loader joins
// the collection leg); a degraded boot's ungated load is superseded later
// under the page-leg generation guard.
void events.bootGate().then(() => applyRoute());

// Listen for browser back/forward.
window.addEventListener("popstate", () => {
  // Closing the sync dialog pops the /sync history entry; the detail
  // page is already rendered underneath, so skip the re-render.
  if (consumeSyncClosing()) {
    return;
  }
  // Use the animated close path (closeDialog contract) so Back-button
  // dismissal matches every other close: a bare dlg.close() snaps shut.
  if (searchDlg.open) {
    closeDialog(searchDlg);
  }
  // Don't close config dialog when unconfigured; user must save first.
  if (configDlg.open && !isUnconfigured()) {
    closeDialog(configDlg);
  }
  viewTransition(() => {
    void applyRoute();
  });
});

// Background cadences. Status is EVENT-DRIVEN while the SSE stream is up
// (the server's status deltas feed the status store), and the poll becomes
// a floor under it (E2):
//   - ONE fetch at connect/boot — the transaction's status leg (events.ts);
//   - a 60s reconcile tick while CONNECTED (skipped while hidden) owning
//     the convergence cases no event carries;
//   - a 5s poll ONLY while the stream is DOWN (events.ts drives
//     status.setStatusDegraded; pause-when-hidden built into pollAction).
//
// updateLiveTimers is a UI tick (formats running durations on screen).
// It doesn't dispatch any action, so pollAction can't help — raw
// setInterval + manual cleanup is correct here.
initStatusReconcile();
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

// Config dialog dismissal (drag-safe backdrop + Escape, with the
// unconfigured-mode refusal) is wired by config.ts via the dialog
// primitive's createDialog + canDismiss — no hand-rolled listeners here.

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

// The status popup + user menu are now @cplieger/ui-primitives popovers
// (see status.ts initStatusPopover + user-menu.ts). createPopover owns their
// open-content refresh (onOpen), outside-click, and Escape dismissal, so the
// old native-[popover] toggle listener + document-level outside-click/Escape
// sweeps are gone.

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
