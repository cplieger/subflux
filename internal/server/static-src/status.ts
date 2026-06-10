// status.ts — Status bar: activity/alerts polling, provider health.

import * as store from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { el, text, icon, $ } from "./dom.js";
import { apiGet, apiGetArray, apiGetTyped } from "./api-client.js";
import { apiAction, defineAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import { decodeStats, decodeProvidersResponse, decodeActivityEntry } from "./wire/decoders.gen.js";
import type {
  Stats as StatsType,
  ProvidersResponse as ProvidersResponseType,
} from "./wire/types.gen.js";
import { fmtTime } from "./utils.js";
import { EMBEDDED_PROVIDER } from "./constants.js";
import { hideTip } from "./tooltip.js";
import type { ActivityEntry } from "./api-types.js";
import { reconcile } from "@cplieger/reactive";

interface Alert {
  id: number;
  level: string;
  message: string;
  source: string;
  kind: string;
  persistent: boolean;
  time: string;
  created_at: string;
}

// Stats and ProvidersResponse types now come from validators.ts so the
// runtime decoder + the type stay in lockstep. The local re-export keeps
// the existing references in this file working.
type Stats = StatsType;
type ProvidersResponse = ProvidersResponseType;

const toastedActivities = new Set<string>();
const dismissedActivities = new Set<string>();

// Check if the status popup is currently visible.
function isPopupOpen(): boolean {
  return $.statusPopup.matches(":popover-open");
}

// Build the stats summary fragment (media count, downloads, missing, providers).
function buildStatsSummary(stats: Stats | null, providers: ProvidersResponse): HTMLElement | null {
  const parts: string[] = [];
  if (stats) {
    if (stats.total_series > 0 || stats.total_movies > 0) {
      parts.push(`Media: ${stats.total_series + stats.total_movies}`);
    }
    parts.push(`Downloads: ${stats.downloads}`);
    if (stats.missing_subs > 0) {
      parts.push(`Missing: ${stats.missing_subs}`);
    }
  }
  if (providers.enabled) {
    const cfg = store.get("config");
    const totalEnabled = cfg
      ? Object.entries(cfg.providers ?? {}).filter(
          ([name, enabled]) => enabled && name !== EMBEDDED_PROVIDER,
        ).length
      : 0;
    const timedOut = providers.providers
      ? Object.entries(providers.providers).filter(
          ([name, p]) => p.timed_out && name !== EMBEDDED_PROVIDER,
        ).length
      : 0;
    if (totalEnabled > 0) {
      parts.push(`${totalEnabled - timedOut}/${totalEnabled} providers`);
    }
  }
  if (parts.length === 0) {
    return null;
  }
  return el("div", { className: "pop-header" }, parts.join(" \u00B7 "));
}

// --- ActivityEntry & alerts polling ---

/** Compute timed-out provider names (excluding embedded). */
function timedOutProviders(providers: ProvidersResponse): string[] {
  const ongoing: string[] = [];
  if (providers.enabled && providers.providers) {
    for (const [name, status] of Object.entries(providers.providers)) {
      if (status.timed_out && name !== EMBEDDED_PROVIDER) {
        ongoing.push(name);
      }
    }
  }
  return ongoing;
}

/** Update the status button icon, label, and severity based on current state. */
function updateStatusButton(
  btn: HTMLElement,
  providers: ProvidersResponse,
  alerts: Alert[],
  activities: ActivityEntry[],
  ongoing: string[],
): boolean {
  const hasOngoing = ongoing.length > 0;
  const hasAlerts = Array.isArray(alerts) && alerts.length > 0;
  const hasPersistent = hasAlerts && alerts.some((a: Alert) => a.kind === "persistent");
  const isActive = Array.isArray(activities) && activities.some((a: ActivityEntry) => !a.done);

  const statusEl = document.getElementById("statusIcon");
  const statusLabel = btn.querySelector(".nav-label");
  if (statusEl) {
    if (hasAlerts && (alerts.some((a: Alert) => a.level === "error") || hasPersistent)) {
      btn.dataset["status"] = "error";
      statusEl.replaceChildren(icon("warning"));
      if (statusLabel) {
        statusLabel.textContent = "Error";
      }
    } else if (hasOngoing || hasAlerts) {
      btn.dataset["status"] = "warn";
      statusEl.replaceChildren(icon("warning"));
      if (statusLabel) {
        statusLabel.textContent = "Warning";
      }
    } else if (isActive) {
      btn.dataset["status"] = "scanning";
      statusEl.replaceChildren();
      if (statusLabel) {
        statusLabel.textContent = "Searching";
      }
    } else {
      btn.dataset["status"] = "idle";
      statusEl.replaceChildren(icon("dot"));
      if (statusLabel) {
        statusLabel.textContent = "Healthy";
      }
    }
  }
  return isActive;
}

/** Process activity side effects: seed toasted set, detect scan completion, show toasts. */
function processActivitySideEffects(
  btn: HTMLElement,
  activities: ActivityEntry[],
  isActive: boolean,
): void {
  if (toastedActivities.size === 0 && Array.isArray(activities)) {
    for (const a of activities) {
      if (a.done) {
        toastedActivities.add(a.id);
      }
    }
  }

  const wasActive = btn.dataset["wasActive"] === "true";
  btn.dataset["wasActive"] = String(isActive);
  if (wasActive && !isActive) {
    emit(BusEvent.DataInvalidate);
  }

  if (Array.isArray(activities)) {
    for (const a of activities) {
      if (a.done && !toastedActivities.has(a.id)) {
        toastedActivities.add(a.id);
        if (
          a.action !== "Manual Search" &&
          a.action !== "Manual Download" &&
          a.action !== "Audio Sync"
        ) {
          notify.success(a.detail);
        }
      }
    }
  }
}

// Status poll is a deduped action so overlapping dispatches collapse
// onto a single in-flight request. Replaces the prior `polling` re-entry
// guard + `pollAbort` AbortController. abortPoll() calls .cancel() to
// abort in-flight on disconnect / page unload. error: false because
// transient failures during background polling shouldn't toast.
//
// Exported so app.ts can pass the action to pollAction() for the
// periodic background poll.
export const pollStatusAction = defineAction<undefined, undefined>({
  name: "status.poll",
  dedupe: true,
  run: async (_args, signal) => {
    const unconfigured = store.get("isUnconfigured");
    const popupVisible = isPopupOpen();

    // Always fetch alerts and activity.
    const alerts = (await apiGet<Alert[]>("/api/alerts", signal)) ?? [];
    const activities = (await apiGetArray("/api/activity", decodeActivityEntry, signal)) ?? [];

    let providers: ProvidersResponse = { enabled: false };
    let stats: Stats | null = null;
    if (!unconfigured && popupVisible) {
      const [providersRes, statsRes] = await Promise.all([
        apiGetTyped("/api/providers/timeout", decodeProvidersResponse, signal),
        apiGetTyped("/api/state/stats", decodeStats, signal),
      ]);
      if (providersRes) {
        providers = providersRes;
      }
      if (statsRes) {
        stats = statsRes;
      }
    } else if (!unconfigured) {
      const providersRes = await apiGetTyped(
        "/api/providers/timeout",
        decodeProvidersResponse,
        signal,
      );
      if (providersRes) {
        providers = providersRes;
      }
    }

    const btn = $.statusBtn;
    const ongoing = timedOutProviders(providers);
    const isActive = updateStatusButton(btn, providers, alerts, activities, ongoing);
    processActivitySideEffects(btn, activities, isActive);

    if (!popupVisible) {
      return;
    }
    hideTip();
    renderPopup(stats, providers, activities, alerts, ongoing, isActive);
  },
  error: false,
});

/** Abort any in-flight status poll (called from events.ts on disconnect). */
export function abortPoll(): void {
  pollStatusAction.cancel();
}

/** Dispatch a single status poll. Used by event handlers and direct
 *  refresh paths (config save, activity dismiss, SSE state change).
 *  pollStatusAction's dedupe coalesces with any in-flight poll. */
export async function pollStatus(): Promise<void> {
  await pollStatusAction.dispatch(undefined);
}

// buildActivityItem constructs a single activity row for the status popup.
function buildActivityItem(a: ActivityEntry): HTMLElement {
  let statusIcon: HTMLElement;
  if (a.done) {
    statusIcon = el("span", { className: "act-icon act-done" }, icon("check"));
  } else if (a.queued) {
    statusIcon = el("span", { className: "act-icon act-queued" }, icon("hourglass"));
  } else {
    statusIcon = el(
      "span",
      { className: "act-icon act-active" },
      el("span", { className: "spinner" }),
    );
  }

  const titleText = a.source === "scheduled" ? "Scheduled search" : "Manual search";

  let timerSpan: HTMLElement | null;
  if (a.done && a.ended_at) {
    timerSpan = el(
      "span",
      { className: "live-timer" },
      ` \u00B7 ${formatDuration(new Date(a.started_at), new Date(a.ended_at))}`,
    );
  } else if (a.done) {
    timerSpan = null;
  } else if (a.queued) {
    timerSpan = el("span", { className: "live-timer" }, " \u00B7 queued");
  } else {
    timerSpan = el(
      "span",
      {
        className: "live-timer",
        "data-started": a.started_at,
      },
      ` \u00B7 ${formatDuration(new Date(a.started_at), new Date())}`,
    );
  }

  const dismissBtn =
    a.done || a.queued
      ? el(
          "button",
          {
            type: "button",
            className: "close-btn ghost",
            "aria-label": a.queued ? "Cancel" : "Dismiss",
            "data-tip": a.queued ? "Cancel queued search" : null,
            onclick: (e: MouseEvent) => {
              e.stopPropagation();
              dismissActivity(a.id);
            },
          },
          icon("close"),
        )
      : null;

  const itemClass = a.done || a.queued ? "pop-item pop-act pop-done" : "pop-item pop-act";
  return el(
    "div",
    { className: itemClass, "data-act-id": a.id },
    statusIcon,
    el("span", { className: "act-title" }, titleText, timerSpan),
    el("span", { className: "act-detail" }, a.detail || ""),
    dismissBtn,
  );
}

function buildAlertItem(a: Alert): HTMLElement {
  const dismissBtn = el(
    "button",
    {
      type: "button",
      className: "pop-dismiss",
      "aria-label": "Dismiss alert",
      onclick: () => dismissAlert(a.id),
    },
    icon("close"),
  );
  if (a.kind === "persistent") {
    return el(
      "div",
      { className: "pop-item persistent" },
      el("span", { className: `level-${a.level}` }, `[${a.source}]`),
      text(` ${a.message}`),
      dismissBtn,
      el("div", { className: "pop-time" }, fmtTime(new Date(a.time))),
    );
  }
  return el(
    "div",
    { className: "pop-item" },
    el("span", { className: `level-${a.level}` }, `[${a.level}]`),
    text(` ${a.message}`),
    dismissBtn,
    el("div", { className: "pop-time" }, `${a.source} \u00B7 ${fmtTime(new Date(a.time))}`),
  );
}

// buildPopupContent constructs the status popup DOM fragment.
interface PopupItem {
  key: string;
  build: () => HTMLElement;
}

function buildPopupItems(
  stats: Stats | null,
  providers: ProvidersResponse,
  activities: ActivityEntry[],
  alerts: Alert[],
  ongoing: string[],
  isActive: boolean,
): PopupItem[] {
  const items: PopupItem[] = [];

  const statsSummary = buildStatsSummary(stats, providers);
  if (statsSummary) {
    items.push({ key: "stats", build: () => statsSummary });
  }

  const autoActivities = Array.isArray(activities)
    ? activities.filter(
        (a: ActivityEntry) =>
          a.action !== "Manual Search" &&
          a.action !== "Manual Download" &&
          !dismissedActivities.has(a.id),
      )
    : [];
  for (const a of autoActivities) {
    items.push({ key: `act-${a.id}`, build: () => buildActivityItem(a) });
  }

  if (providers.enabled && providers.providers?.[EMBEDDED_PROVIDER]) {
    const emb = providers.providers[EMBEDDED_PROVIDER];
    if (emb.last_error) {
      const errMsg = emb.last_error.replace(/detect embedded subs: /g, "");
      items.push({
        key: "emb-err",
        build: () =>
          el(
            "div",
            { className: "pop-item" },
            el("span", { className: "level-warn" }, "Embedded detection: "),
            text(errMsg),
          ),
      });
    }
  }

  if (ongoing.length > 0) {
    for (const name of ongoing) {
      const p = providers.providers?.[name];
      if (!p) {
        continue;
      }
      const err = p.last_error ?? `${p.recent_failures} failures`;
      items.push({
        key: `prov-${name}`,
        build: () =>
          el(
            "div",
            { className: "pop-item" },
            el("span", { className: "level-warn" }, `${name}: `),
            text(err),
          ),
      });
    }
  }

  if (Array.isArray(alerts) && alerts.length > 0) {
    for (const a of alerts) {
      items.push({ key: `alert-${a.id}`, build: () => buildAlertItem(a) });
    }
  }

  if (!isActive && stats?.last_scan) {
    let scanLabel = "";
    const lastDone = activities
      .filter((a: ActivityEntry) => a.done && a.action === "Full Scan")
      .pop();
    if (lastDone?.ended_at) {
      scanLabel = timeAgo(new Date(lastDone.ended_at));
    }
    if (!scanLabel) {
      scanLabel = timeAgo(new Date(stats.last_scan));
    }
    let scanText = `Last scan: ${scanLabel}`;
    if (stats.scan_interval_seconds > 0 && lastDone?.ended_at) {
      const nextMs = new Date(lastDone.ended_at).getTime() + stats.scan_interval_seconds * 1000;
      const remaining = nextMs - Date.now();
      if (remaining > 0) {
        scanText += ` \u00B7 Next scan: in ${formatDuration(
          new Date(),
          new Date(Date.now() + remaining),
        )}`;
      }
    }
    items.push({
      key: "scan-timing",
      build: () => el("div", { className: "pop-item muted" }, scanText),
    });
  }

  if (items.length === 0) {
    items.push({
      key: "empty",
      build: () => el("div", { className: "pop-item muted" }, "All clear"),
    });
  }

  return items;
}

function renderPopup(
  stats: Stats | null,
  providers: ProvidersResponse,
  activities: ActivityEntry[],
  alerts: Alert[],
  ongoing: string[],
  isActive: boolean,
): void {
  const items = buildPopupItems(stats, providers, activities, alerts, ongoing, isActive);
  reconcile($.statusPopup, items, {
    key: (item) => item.key,
    mount: (item) => item.build(),
  });
}

function dismissActivity(id: string): void {
  dismissedActivities.add(id);
  // Swap close button to spinner for immediate feedback.
  const item = document.querySelector(`[data-act-id="${CSS.escape(id)}"]`);
  if (item) {
    const btn = item.querySelector<HTMLButtonElement>(".close-btn");
    if (btn) {
      btn.disabled = true;
      btn.replaceChildren(el("span", { className: "spinner" }));
    }
  }
  // Fire and forget; next poll will filter it out. Silent error: the
  // dismissedActivities set above already hides the row optimistically;
  // a server failure surfaces only as the row reappearing on next poll.
  void dismissActivityAction.dispatch(id);
}

/** Dismiss an activity. Silent (no toast) — UI feedback is the spinner
 *  swap + dismissedActivities Set hiding the row until next poll.
 *  retryNetwork handles transient blips: a quick disconnect would
 *  otherwise leave the row hidden but never actually dismissed
 *  server-side. dedupe protects against rapid double-click. */
const dismissActivityAction = apiAction<string>({
  name: "activity.dismiss",
  request: (id) => ({ method: "DELETE", path: `/api/activity?id=${encodeURIComponent(id)}` }),
  dedupe: (id) => `activity.dismiss:${id}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: false,
});

async function dismissAlert(id: number): Promise<void> {
  const r = await dismissAlertAction.dispatch(id);
  if (r !== null) {
    void pollStatus();
  }
}

/** Dismiss an alert with retry on transient network failures. Dedupe
 *  prevents rapid-click duplicate deletes against the same alert id. */
const dismissAlertAction = apiAction<number>({
  name: "alerts.dismiss",
  request: (id) => ({ method: "DELETE", path: `/api/alerts?id=${id}` }),
  dedupe: (id) => `alerts.dismiss:${id}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: "Dismiss failed",
});

export function updateLiveTimers(): void {
  const now = new Date();
  document.querySelectorAll(".live-timer[data-started]").forEach((timer: Element) => {
    timer.textContent = ` \u00B7 ${formatDuration(
      new Date(timer.getAttribute("data-started") ?? ""),
      now,
    )}`;
  });
}

function formatDuration(start: Date, end: Date): string {
  const secs = Math.max(0, Math.floor((end.getTime() - start.getTime()) / 1000));
  if (secs < 60) {
    return `${secs}s`;
  }
  const mins = Math.floor(secs / 60);
  if (mins < 60) {
    return `${mins}m ${secs % 60}s`;
  }
  return `${Math.floor(mins / 60)}h ${mins % 60}m`;
}

function timeAgo(date: Date): string {
  const secs = Math.floor((Date.now() - date.getTime()) / 1000);
  if (secs < 60) {
    return "just now";
  }
  const mins = Math.floor(secs / 60);
  if (mins < 60) {
    return `${mins}m ago`;
  }
  const hrs = Math.floor(mins / 60);
  if (hrs < 24) {
    return `${hrs}h ago`;
  }
  const days = Math.floor(hrs / 24);
  return `${days}d ago`;
}
