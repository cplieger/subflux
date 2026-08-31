// status.ts — Status surfaces: the event-fed status store with its poll
// floor (E2), buttons/chip, popup, provider health.

import * as store from "./store.js";
import * as notify from "./notify.js";
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
import {
  apiAction,
  defineAction,
  pollAction,
  registerCleanup,
  retryNetwork,
  RETRY_STANDARD,
} from "@cplieger/actions";
import type {
  ActivityEvent,
  Alert,
  AlertEvent,
  ProviderEvent,
  Stats as StatsType,
  ProvidersResponse as ProvidersResponseType,
} from "./wire/types.gen.js";
import { fmtTime } from "./utils.js";
import { SSE_DOWN_POLL_MS, STATUS_RECONCILE_MS } from "./constants.js";
import type { ActivityEntry } from "./api-types.js";
import { runningScans, type RunningScansByScope } from "./scan-scope.js";
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

/** First successful poll: whatever is already done predates this page load —
 *  seed it as historical (even when the snapshot has no done entries at all)
 *  so only LATER completions toast. Poll-only, because a FULL snapshot is the
 *  one honest baseline; event deltas never seed. */
function seedToastHistory(activities: readonly ActivityEntry[]): void {
  if (activitiesInitialized) {
    return;
  }
  activitiesInitialized = true;
  for (const a of activities) {
    if (a.done) {
      toastedActivities.add(a.id);
    }
  }
}

/** Process activity side effects: detect completions, show toasts. */
function processActivitySideEffects(activities: readonly ActivityEntry[]): void {
  if (!activitiesInitialized) {
    // No baseline yet: an entry seen before the first poll seeds cannot be
    // told from history, so completion toasts wait for the seed.
    return;
  }
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

// Status poll is a deduped action so overlapping dispatches collapse
// onto a single in-flight request. Replaces the prior `polling` re-entry
// guard + `pollAbort` AbortController. abortPoll() calls .cancel() to
// abort in-flight on disconnect / page unload. error: false because
// transient failures during background polling shouldn't toast.
const pollStatusAction = defineAction<undefined, undefined>({
  name: "status.poll",
  dedupe: true,
  run: async (_args, signal) => {
    const unconfigured = store.get("isUnconfigured");
    const popupVisible = isPopupOpen();

    // Every leg issues in ONE concurrent burst; offline detection gates what
    // is done with the RESULTS below, never their issuance. Alerts go
    // through the RAW flavor so a network-level failure (status 0, not an
    // abort) is distinguishable from "no alerts": an unreachable server must
    // show the offline state, not coalesce to a green "Healthy" — the status
    // button would otherwise be at its most reassuring exactly when the app
    // is down. stateStats stays popup-gated.
    const [alertsRes, activitiesRes, providersRes, statsRes] = await Promise.all([
      listAlertsRaw({ signal }),
      listActivity({ signal }),
      unconfigured ? null : providerTimeouts({ signal }),
      unconfigured || !popupVisible ? null : stateStats({ signal }),
    ]);

    if (!alertsRes.ok && alertsRes.status === 0) {
      if (!signal.aborted) {
        setOfflineStatus($.statusBtn, popupVisible);
      }
      return;
    }

    // The poll is the reconcile authority: it REPLACES the event-fed store
    // wholesale, which is what converges the three event-less cases (alert
    // TTL expiry, alert cap eviction, unqueried provider-cooldown expiry).
    const activities = activitiesRes ?? [];
    knownActivities = new Map(activities.map((a) => [a.id, a]));
    knownAlerts = new Map(((alertsRes.ok ? alertsRes.data : null) ?? []).map((a) => [a.id, a]));
    knownProviders = providersRes ?? { enabled: false, providers: {} };
    if (!unconfigured && popupVisible) {
      knownStats = statsRes;
    }

    seedToastHistory(activities);
    renderStatus();
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

// --- The event-fed status store (E2) ---
//
// The server's status deltas (activity upsert/remove, alert raise/dismiss,
// provider raise/clear) land here idempotently via events.ts; each full poll
// REPLACES the snapshot wholesale. Maps preserve arrival order: the poll
// writes the server's chronological list, an unseen event key appends, a
// known key updates in place.

let knownActivities = new Map<string, ActivityEntry>();
let knownAlerts = new Map<number, Alert>();
let knownProviders: ProvidersResponse = { enabled: false, providers: {} };
let knownStats: Stats | null = null;

/** Shallow equality over two decoder-shaped flat records (every compared
 *  field is a primitive, and absent optionals are omitted rather than set to
 *  undefined). What makes re-application a no-op. */
function sameRecord<T extends object>(a: T, b: T): boolean {
  const keys = Object.keys(a) as (keyof T)[];
  if (keys.length !== Object.keys(b).length) {
    return false;
  }
  return keys.every((k) => a[k] === b[k]);
}

/** Apply an activity delta (op upsert|remove, keyed entry.id). Re-applying a
 *  frame the store already reflects mutates nothing and repaints nothing. */
export function applyActivityEvent(ev: ActivityEvent): void {
  const entry = ev.entry;
  if (!entry) {
    return;
  }
  if (ev.op === "remove") {
    if (!knownActivities.delete(entry.id)) {
      return;
    }
  } else {
    const prev = knownActivities.get(entry.id);
    if (prev && sameRecord(prev, entry)) {
      return;
    }
    knownActivities.set(entry.id, entry);
  }
  renderStatus();
}

/** Apply an alert delta (op raise|dismiss, keyed alert.id), idempotently. */
export function applyAlertEvent(ev: AlertEvent): void {
  const alert = ev.alert;
  if (!alert) {
    return;
  }
  if (ev.op === "dismiss") {
    if (!knownAlerts.delete(alert.id)) {
      return;
    }
  } else {
    const prev = knownAlerts.get(alert.id);
    if (prev && sameRecord(prev, alert)) {
      return;
    }
    knownAlerts.set(alert.id, alert);
  }
  renderStatus();
}

/** Apply a provider timeout delta (op raise|clear, keyed entry.provider),
 *  idempotently. Both ops upsert the carried status snapshot — a clear is
 *  the entry with timed_out false, the same per-provider shape the poll
 *  serves — and any provider event proves the health tracker is live, so
 *  `enabled` flips true. A cooldown expiring UNQUERIED emits no event; the
 *  reconcile tick's full fetch owns that convergence. */
export function applyProviderEvent(ev: ProviderEvent): void {
  const entry = ev.entry;
  if (!entry) {
    return;
  }
  const prev = knownProviders.providers[entry.provider];
  if (knownProviders.enabled && prev && sameRecord(prev, entry.status)) {
    return;
  }
  knownProviders = {
    enabled: true,
    providers: { ...knownProviders.providers, [entry.provider]: entry.status },
  };
  renderStatus();
}

/** Repaint every status surface from the store: the running-scans map the
 *  scan buttons key off, the stopping-overlay reconcile, the nav button +
 *  label, completion toasts, the registered activity observers, and the
 *  popup when it is open. Shared by event application and the poll, so
 *  buttons and chip stay event-fresh with the poll silent. */
function renderStatus(): void {
  const btn = $.statusBtn;
  const activities = [...knownActivities.values()];
  const alerts = [...knownAlerts.values()];
  const ongoing = timedOutProviders(knownProviders);

  publishRunningScans(activities);
  reconcileStoppingOverlay(activities);

  const isActive = updateStatusButton(btn, knownProviders, alerts, activities, ongoing);
  processActivitySideEffects(activities);
  for (const fn of activityObservers) {
    fn(activities);
  }

  if (!isPopupOpen()) {
    return;
  }
  renderPopup(knownStats, knownProviders, activities, alerts, ongoing, isActive);
}

// --- Activity observers (task 12) ---
//
// Modules tracking specific activity ids (search.ts's download buttons)
// observe every status-store change — event-fed while SSE is up, poll-fed
// while it is down (the degraded 5s poll and the reconcile tick land here
// too) — through one registration, replacing per-consumer pollers.

const activityObservers = new Set<(activities: readonly ActivityEntry[]) => void>();

/** Run `fn` with the full activity snapshot after every status-store change.
 *  Returns an unregister function. */
export function observeActivities(fn: (activities: readonly ActivityEntry[]) => void): () => void {
  activityObservers.add(fn);
  return (): void => {
    activityObservers.delete(fn);
  };
}

/** Publish the running-scans-by-scope map derived from the structured scope
 *  fields. This is the load-bearing restoration/catch-up path: a fresh poll
 *  rebuilds it with zero SSE events seen, and activity events keep it live
 *  in between. Scan buttons subscribe via detail-scan.ts, so an unchanged
 *  map is NOT re-published — a re-applied event writes no signal. */
function publishRunningScans(activities: readonly ActivityEntry[]): void {
  const next = runningScans(activities);
  // Pre-boot reads (app.ts seeds the key before any poll or event) are
  // undefined at runtime even though the store type says otherwise.
  const prev = store.get("runningScansByScope") as RunningScansByScope | undefined;
  if (prev !== undefined && sameScans(prev, next)) {
    return;
  }
  store.set("runningScansByScope", next);
}

function sameScans(a: RunningScansByScope, b: RunningScansByScope): boolean {
  if (a.size !== b.size) {
    return false;
  }
  for (const [key, scan] of b) {
    const prev = a.get(key);
    if (prev === undefined) {
      return false;
    }
    if (
      prev.activityId !== scan.activityId ||
      prev.cancellable !== scan.cancellable ||
      prev.requiredRole !== scan.requiredRole
    ) {
      return false;
    }
  }
  return true;
}

/** Reconcile the optimistic "stopping…" overlay: entries that reached a
 *  terminal state (or vanished) leave the set. */
function reconcileStoppingOverlay(activities: readonly ActivityEntry[]): void {
  if (stoppingActivities.size === 0) {
    return;
  }
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

// --- The poll floor (E2) ---
//
// One fetch at connect/boot: the transaction's status leg (events.ts). While
// CONNECTED, events feed the store and the 60s reconcile tick is the drift
// belt. ONLY while the stream is DOWN does status ride the 5s poll.

let stopDownPoll: (() => void) | null = null;

/** SSE connection state, driven by events.ts. DOWN — a refused connect or
 *  the post-CLOSED reconnect ladder — puts status on the 5s poll (pausing
 *  while hidden; pollAction owns that); a live stream stops it. CONNECTING
 *  blips never enter here: the browser's own retry is not a down period. */
export function setStatusDegraded(down: boolean): void {
  if (down === (stopDownPoll !== null)) {
    return;
  }
  if (down) {
    stopDownPoll = pollAction(pollStatusAction, undefined, { interval: SSE_DOWN_POLL_MS });
  } else {
    stopDownPoll?.();
    stopDownPoll = null;
  }
}

/** Put the status poll on the shared 60s reconcile cadence: the drift belt
 *  while SSE is CONNECTED. Each tick's full fetch owns the three
 *  reconcile-only convergence cases no event carries — alert TTL expiry,
 *  alert cap eviction, and a provider cooldown expiring unqueried. The tick
 *  skips while hidden: a hidden tab issues zero status polls. */
export function initStatusReconcile(): void {
  registerReconcileTask(() => {
    void pollStatus();
  });
}

// --- Reconcile tick ---
//
// The 60s drift belt (E2), shared: the coverage dirty-set retry and the
// status poll both ride it. Started lazily on the first registration, and it
// PAUSES WHEN HIDDEN: a hidden tab converges on return via replay or
// transaction instead.

const reconcileTasks = new Set<() => void>();
let reconcileTimer: ReturnType<typeof setInterval> | null = null;

/** Run `fn` at each reconcile tick (skipped while the tab is hidden).
 *  Returns an unregister function. */
export function registerReconcileTask(fn: () => void): () => void {
  reconcileTasks.add(fn);
  if (reconcileTimer === null) {
    reconcileTimer = setInterval(() => {
      if (document.hidden) {
        return;
      }
      for (const task of reconcileTasks) {
        task();
      }
    }, STATUS_RECONCILE_MS);
    registerCleanup(() => {
      if (reconcileTimer !== null) {
        clearInterval(reconcileTimer);
        reconcileTimer = null;
      }
      reconcileTasks.clear();
    });
  }
  return (): void => {
    reconcileTasks.delete(fn);
  };
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
      // Detach the skeleton before the first reconcile: reconcile() removes
      // only children carrying its key attribute, so the unkeyed placeholder
      // rows would survive this paint and every later one, with the live rows
      // inserted below them. detail.ts clears its container in the commit for
      // the same reason; the table views get it from their shell patch.
      $.statusPopup.replaceChildren();
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
