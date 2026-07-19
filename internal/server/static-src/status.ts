// status.ts — Status bar: activity/alerts polling, provider health.

import * as store from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { el, text, icon, $ } from "./dom.js";
import { fillPath } from "./api-client.js";
import {
  listActivity,
  listAlertsRaw,
  providerTimeouts,
  stateStats,
  PATH_CANCEL_ACTIVITY,
  PATH_DISMISS_ACTIVITY,
  PATH_DISMISS_ALERT,
} from "./wire/client.gen.js";
import { apiAction, defineAction, retryNetwork, RETRY_STANDARD } from "@cplieger/actions";
import type {
  Alert,
  Stats as StatsType,
  ProvidersResponse as ProvidersResponseType,
} from "./wire/types.gen.js";
import { fmtTime } from "./utils.js";
import type { ActivityEntry } from "./api-types.js";
import { runningScans } from "./scan-scope.js";
import { createMenuPopover, type MenuPopover } from "./popover-menu.js";
import { skeletonTiming, type SkeletonTimingController } from "@cplieger/ui-primitives/skeleton";
import { patch, reconcile } from "@cplieger/reactive";

// Alert, Stats, and ProvidersResponse are the generated wire types, so the
// runtime decoder + the type stay in lockstep. The local re-export keeps
// the existing references in this file working.
type Stats = StatsType;
type ProvidersResponse = ProvidersResponseType;

const toastedActivities = new Set<string>();
// First-successful-poll marker for toast seeding: entries already terminal
// on the FIRST response are historical and seed silently; entries reaching a
// terminal state on any LATER response toast. A "toasted set is empty"
// marker would misclassify the first real completion as historical whenever
// the first response held only running activities.
let activitiesInitialized = false;
const dismissedActivities = new Set<string>();

// Optimistic "stopping…" overlay: activity ids whose stop was dispatched but
// whose terminal (cancelled) state has not arrived yet. Honesty note: a stop
// ends the scan after the item IN FLIGHT completes — for single-item scopes
// that is the whole scan, so this state may sit for minutes.
const stoppingActivities = new Set<string>();

// The status popup is a @cplieger/ui-primitives popover anchored to the status
// button (replacing the native Popover API). Held here so isPopupOpen() and the
// background poll can query/refresh it.
let statusPopover: MenuPopover | null = null;

// First-open anti-flicker controller for the popup skeleton, settled by the
// first renderPopup after opening. showDelay 0 (an empty popup at min-height
// would be worse than an instant skeleton) + the fleet's 300ms min-visible so
// a fast poll can't flash it. deferredPaint holds the newest paint while a
// min-visible commit is still pending, so a poll burst can't paint stale rows.
let popupSkeleton: SkeletonTimingController | null = null;
let deferredPaint: (() => void) | null = null;

// requires @cplieger/ui-primitives >= 2.1.0 (popover stretch mode); verified
// locally via a node_modules overlay until released.
export function initStatusPopover(): void {
  statusPopover = createMenuPopover($.statusBtn, $.statusPopup, {
    // Panel role is "group" (not a menu), so leave haspopup at its default
    // ("true") — there is no dedicated "group" aria-haspopup token.
    onOpen: () => {
      // First open: anti-flicker skeleton (see popupSkeleton above), then
      // poll. This was the native `toggle` listener in app.ts before the
      // popover migration.
      if (!$.statusPopup.children.length && popupSkeleton === null) {
        popupSkeleton = skeletonTiming(
          () => {
            const skel = document.createDocumentFragment();
            for (let i = 0; i < 2; i++) {
              skel.appendChild(
                el("div", { className: "skeleton-row" }, el("div", { className: "skeleton" })),
              );
            }
            patch($.statusPopup, skel);
          },
          { showDelayMs: 0, minVisibleMs: 300 },
        );
      }
      void pollStatus();
    },
  });
  $.statusBtn.addEventListener("click", () => statusPopover?.toggle());
}

// Check if the status popup is currently visible.
function isPopupOpen(): boolean {
  return statusPopover?.isOpen ?? false;
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
      ? Object.entries(cfg.providers).filter(([, enabled]) => enabled).length
      : 0;
    const timedOut = Object.entries(providers.providers).filter(([, p]) => p.timed_out).length;
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

/** Compute timed-out provider names. */
function timedOutProviders(providers: ProvidersResponse): string[] {
  const ongoing: string[] = [];
  if (providers.enabled) {
    for (const [name, status] of Object.entries(providers.providers)) {
      if (status.timed_out) {
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

/** Flip the status button to the offline state (server unreachable) and, when
 *  the popup is open, replace its content with a single explanatory row.
 *  The next successful poll overwrites both. */
function setOfflineStatus(btn: HTMLElement, popupVisible: boolean): void {
  btn.dataset["status"] = "offline";
  const statusEl = document.getElementById("statusIcon");
  if (statusEl) {
    statusEl.replaceChildren(icon("warning"));
  }
  const statusLabel = btn.querySelector(".nav-label");
  if (statusLabel) {
    statusLabel.textContent = "Offline";
  }
  if (popupVisible) {
    patch(
      $.statusPopup,
      el("div", { className: "pop-item muted" }, "Server unreachable \u2014 retrying\u2026"),
    );
  }
}

/** Process activity side effects: seed toasted set, detect scan completion, show toasts. */
function processActivitySideEffects(
  btn: HTMLElement,
  activities: ActivityEntry[],
  isActive: boolean,
): void {
  if (!activitiesInitialized) {
    // First successful poll: whatever is already done predates this page
    // load — seed it as historical (even when the snapshot has no done
    // entries at all) so only LATER completions toast.
    activitiesInitialized = true;
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
          // Distinct terminal signals: a cancelled or failed scan must not
          // produce a success toast.
          if (a.cancelled) {
            notify.info(`Stopped: ${a.detail}`);
          } else if (a.failed) {
            notify.error(`Failed: ${a.detail}`);
          } else {
            notify.success(a.detail);
          }
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

    // Always fetch alerts and activity. Alerts go through the RAW flavor so a
    // network-level failure (status 0, not an abort) is distinguishable from
    // "no alerts": an unreachable server must show the offline state, not
    // coalesce to a green "Healthy" — the status button would otherwise be at
    // its most reassuring exactly when the app is down.
    const alertsRes = await listAlertsRaw({ signal });
    if (!alertsRes.ok && alertsRes.status === 0) {
      if (!signal.aborted) {
        setOfflineStatus($.statusBtn, popupVisible);
      }
      return;
    }
    const alerts = (alertsRes.ok ? alertsRes.data : null) ?? [];
    const activities = (await listActivity({ signal })) ?? [];

    // Publish the running-scans-by-scope map derived from the structured
    // scope fields. This is the load-bearing restoration/catch-up path: a
    // fresh poll rebuilds it with zero SSE events seen (reconnects create a
    // fresh EventSource that sends no Last-Event-ID). Scan buttons subscribe
    // via detail-scan.ts. Also reconcile the optimistic "stopping…" overlay:
    // entries that reached a terminal state (or vanished) leave the set.
    store.set("runningScansByScope", runningScans(activities));
    if (stoppingActivities.size > 0) {
      const live = new Set<string>();
      for (const a of activities) {
        if (!a.done) {
          live.add(a.id);
        }
      }
      for (const id of stoppingActivities) {
        if (!live.has(id)) {
          stoppingActivities.delete(id);
        }
      }
    }

    let providers: ProvidersResponse = { enabled: false, providers: {} };
    let stats: Stats | null = null;
    if (!unconfigured && popupVisible) {
      const [providersRes, statsRes] = await Promise.all([
        providerTimeouts({ signal }),
        stateStats({ signal }),
      ]);
      if (providersRes) {
        providers = providersRes;
      }
      if (statsRes) {
        stats = statsRes;
      }
    } else if (!unconfigured) {
      const providersRes = await providerTimeouts({ signal });
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
// Terminal states render DISTINCTLY: completed (check), cancelled (stop
// glyph, muted), failed (warning) — a cancelled or failed scan must never
// wear the success check. Running scan entries carry the stop control.
// Exported for tests.
export function buildActivityItem(a: ActivityEntry): HTMLElement {
  const stopping = !a.done && stoppingActivities.has(a.id);

  let statusIcon: HTMLElement;
  if (a.done && a.cancelled) {
    statusIcon = el("span", { className: "act-icon act-cancelled" }, icon("stop"));
  } else if (a.done && a.failed) {
    statusIcon = el("span", { className: "act-icon act-failed" }, icon("warning"));
  } else if (a.done) {
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
  if (a.done && a.cancelled) {
    timerSpan = el("span", { className: "live-timer" }, " \u00B7 stopped");
  } else if (a.done && a.failed) {
    timerSpan = el("span", { className: "live-timer" }, " \u00B7 failed");
  } else if (a.done && a.ended_at) {
    timerSpan = el(
      "span",
      { className: "live-timer" },
      ` \u00B7 ${formatDuration(new Date(a.started_at), new Date(a.ended_at))}`,
    );
  } else if (a.done) {
    timerSpan = null;
  } else if (stopping) {
    // Optimistic post-dispatch state. End-of-current-item semantics: for a
    // single-item scope this may honestly sit here for minutes.
    timerSpan = el("span", { className: "live-timer" }, " \u00B7 stopping\u2026");
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

  // Running scan entries get the stop control (canonical home per the S12
  // ruling): confirm-less graceful stop, rendered per the object-level role
  // (full scans are admin-stoppable only). Queued entries keep the dismiss
  // cancel; done entries the dismiss button.
  let actionBtn: HTMLElement | null = null;
  if (a.done || a.queued) {
    actionBtn = el(
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
    );
  } else if (a.cancellable && (a.required_role !== "admin" || store.get("isAdmin"))) {
    actionBtn = el(
      "button",
      {
        type: "button",
        className: "close-btn ghost",
        "aria-label": "Stop scan",
        "data-tip": "Stop after the current item",
        disabled: stopping,
        onclick: (e: MouseEvent) => {
          e.stopPropagation();
          requestStopScan(a.id, e.currentTarget as HTMLButtonElement | null);
        },
      },
      icon("stop"),
    );
  }

  const itemClass = a.done || a.queued ? "pop-item pop-act pop-done" : "pop-item pop-act";
  return el(
    "div",
    { className: itemClass, "data-act-id": a.id },
    statusIcon,
    el("span", { className: "act-title" }, titleText, timerSpan),
    el("span", { className: "act-detail" }, a.detail || ""),
    actionBtn,
  );
}

/** Dispatch a graceful stop for a running scan with the optimistic
 *  "stopping…" state; the terminal cancelled entry arriving via poll (or a
 *  failed dispatch reverting the overlay) reconciles the row. */
function requestStopScan(id: string, btn: HTMLButtonElement | null): void {
  stoppingActivities.add(id);
  if (btn) {
    btn.disabled = true;
  }
  const item = document.querySelector(`[data-act-id="${CSS.escape(id)}"]`);
  const timer = item?.querySelector(".live-timer");
  if (timer) {
    timer.textContent = " \u00B7 stopping\u2026";
  }
  void cancelActivityAction.dispatch(id).then((r) => {
    if (r === null) {
      // Stop rejected (network failure, or the scan finished first): revert
      // the optimistic overlay; the poll repaints the row's true state.
      stoppingActivities.delete(id);
    }
    void pollStatus();
  });
}

/** Stop a running background scan. Silent on failure (no toast): the row
 *  reverting from "stopping…" plus the next poll's true state is the
 *  feedback. Dedupe guards rapid double-clicks; the endpoint is idempotent
 *  (204 for already-stopping) anyway. */
const cancelActivityAction = apiAction<string>({
  name: "activity.cancel",
  request: (id) => ({ method: "POST", path: fillPath(PATH_CANCEL_ACTIVITY, { id }) }),
  dedupe: (id) => `activity.cancel:${id}`,
  error: false,
});

function buildAlertItem(a: Alert): HTMLElement {
  const dismissBtn = el(
    "button",
    {
      type: "button",
      className: "pop-dismiss",
      "aria-label": "Dismiss alert",
      onclick: (e: MouseEvent) => {
        // Optimistic exit animation, matching activity dismissal; a server
        // failure surfaces as the alert reappearing on the next poll.
        const item = (e.currentTarget as HTMLElement).closest(".pop-item");
        if (item) {
          animateDismiss(item);
        }
        void dismissAlert(a.id);
      },
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

  if (ongoing.length > 0) {
    for (const name of ongoing) {
      const p = providers.providers[name];
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
  const paint = (): void => {
    const items = buildPopupItems(stats, providers, activities, alerts, ongoing, isActive);
    reconcile($.statusPopup, items, {
      key: (item) => item.key,
      mount: (item) => item.build(),
    });
    // Content changed after placement — re-clamp against the real height
    // (no-op while the popup is closed).
    statusPopover?.reposition();
  };
  if (popupSkeleton !== null) {
    // First data after open: honor the skeleton's min-visible window.
    deferredPaint = paint;
    const s = popupSkeleton;
    popupSkeleton = null;
    s.commit(() => {
      deferredPaint?.();
      deferredPaint = null;
    });
  } else if (deferredPaint !== null) {
    // A newer poll landed before the min-visible commit fired: supersede the
    // queued paint so the deferred commit renders the freshest data.
    deferredPaint = paint;
  } else {
    paint();
  }
}

/** Play the authored exit animation (fade + slide + collapse) on a popup row,
 *  removing the element when the transition ends (300ms fallback covers
 *  reduced-motion and transition-less environments). Removal outside
 *  reconcile is safe: the dismissed row is filtered from the next render. */
function animateDismiss(item: Element): void {
  item.classList.add("pop-dismissing");
  const remove = (): void => {
    item.remove();
  };
  item.addEventListener("transitionend", remove, { once: true });
  setTimeout(remove, 300);
}

function dismissActivity(id: string): void {
  dismissedActivities.add(id);
  // Play the exit animation for immediate feedback; the row is filtered from
  // subsequent renders by the dismissedActivities set.
  const item = document.querySelector(`[data-act-id="${CSS.escape(id)}"]`);
  if (item) {
    const btn = item.querySelector<HTMLButtonElement>(".close-btn");
    if (btn) {
      btn.disabled = true;
    }
    animateDismiss(item);
  }
  // Optimistic hide with rollback: on success the server prunes the entry
  // and the set entry is moot; on terminal failure (retries exhausted) the
  // id must LEAVE the client-side set — it is permanent otherwise, so the
  // row could never reappear — and a refresh poll repaints the true state.
  // The action's error notification reports the failure.
  void dismissActivityAction.dispatch(id).then((r) => {
    if (r === null) {
      dismissedActivities.delete(id);
      void pollStatus();
    }
  });
}

/** Dismiss an activity. retryNetwork + RETRY_STANDARD absorb transient
 *  blips (a quick disconnect would otherwise leave the row hidden but never
 *  actually dismissed server-side); a terminal failure surfaces the action
 *  error notification and dismissActivity rolls the optimistic hide back.
 *  dedupe protects against rapid double-click. */
const dismissActivityAction = apiAction<string>({
  name: "activity.dismiss",
  request: (id) => ({
    method: "DELETE",
    path: `${PATH_DISMISS_ACTIVITY}?id=${encodeURIComponent(id)}`,
  }),
  dedupe: (id) => `activity.dismiss:${id}`,
  retryable: retryNetwork,
  retry: RETRY_STANDARD,
  error: "Dismiss failed",
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
  request: (id) => ({ method: "DELETE", path: `${PATH_DISMISS_ALERT}?id=${id}` }),
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
