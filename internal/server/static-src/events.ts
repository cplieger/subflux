// Server-Sent Events connection. Receives real-time updates from the
// server and dispatches them to the store and notification system.

import * as store from "./store.js";
import * as notify from "./notify.js";
import { emit, BusEvent } from "./bus.js";
import { fetchAndMergeCoverage } from "./coverage.js";
import { pollStatus, abortPoll } from "./status.js";
import { registerCleanup } from "@cplieger/actions";
import { SSE_RECONNECT_MS, SSE_MAX_RECONNECT_MS, VISIBILITY_DEBOUNCE_MS } from "./constants.js";
import { PATH_EVENTS } from "./wire/client.gen.js";
import { decodeCoverageEvent, decodeNotifyEvent, decodeScanEvent } from "./wire/decoders.gen.js";
import type { EventData } from "./wire/types.gen.js";
import type { Decoder } from "./validators.js";

// --- Typed SSE event payloads ---

// SSE frames arrive as { type, data: <event> } where data is one variant of
// the generated EventData union (the sealed events.EventData interface).
// Each named-SSE listener already knows its variant, so it decodes the inner
// payload directly through that variant's generated decoder; the T extends
// EventData bound keeps a non-union decoder out of this path. (The generated
// decodeEventDataPayload adapter covers consumers that need to dispatch on
// the discriminator at runtime; the per-event listeners here don't.)
// Returns null on a malformed frame so a bad event can't throw out of a
// listener.
function decodeSSE<T extends EventData>(e: MessageEvent, decoder: Decoder<T>): T | null {
  try {
    const env = JSON.parse(e.data as string) as { data?: unknown };
    return decoder(env.data);
  } catch {
    return null;
  }
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;
// True once any SSE connection has opened this page load; a later open is
// therefore a RECONNECT after a gap (error backoff or hidden-tab pause).
let everConnected = false;

export function connect(): void {
  if (eventSource) {
    return;
  }

  // Drain SSE state on page unload so reconnectTimer / visibilityTimer
  // can't fire into a torn-down DOM during the unload window. Browsers
  // handle most of this naturally on page close, but registerCleanup
  // guarantees deterministic teardown for tests + soft-navigation cases.
  registerCleanup(disconnect);

  eventSource = new EventSource(PATH_EVENTS);

  eventSource.addEventListener("open", () => {
    reconnectAttempt = 0;
    if (everConnected) {
      // Reconnected after a gap. Both reconnect paths (error backoff and the
      // visibilitychange pause) construct a FRESH EventSource, which never
      // sends Last-Event-ID, so any events published during the gap are
      // gone. Refetch the pull state instead of trusting the stream: the
      // current page reloads via DataInvalidate and the status button
      // re-polls, so coverage badges and activity can't silently go stale.
      emit(BusEvent.DataInvalidate);
      void pollStatus();
    }
    everConnected = true;
  });

  eventSource.addEventListener("coverage", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeCoverageEvent);
    const mediaId = payload?.media_id ?? "";

    if (mediaId && store.get("currentPage") === "library" && !store.get("detailCtx")) {
      // Refetch + setAll updates the affected row's signal reactively (bindList
      // repaints just that row); no manual per-badge DOM patching.
      void fetchAndMergeCoverage().catch(() => {
        emit(BusEvent.DataInvalidate);
      });
      return;
    }
    emit(BusEvent.DataInvalidate);
  });

  eventSource.addEventListener("notify", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeNotifyEvent);
    if (!payload) {
      return;
    }
    if (payload.level === "error") {
      notify.error(payload.text || "");
    } else if (payload.level === "success") {
      notify.success(payload.text || "");
    } else {
      notify.info(payload.text || "");
    }
  });

  // scan:start is an ephemeral "scan started" nudge. The status button
  // already flips to "in progress" from the caller's click handler, so
  // we only surface a short toast — useful for scheduled scans the user
  // did not initiate.
  eventSource.addEventListener("scan:start", (e: MessageEvent) => {
    const payload = decodeSSE(e, decodeScanEvent);
    if (!payload) {
      return;
    }
    const label = payload.detail || payload.action || "Scan";
    notify.info(`Scan started: ${label}`);
  });

  // scan:done triggers an immediate status refresh so buttons and the popup
  // update without waiting up to one poll interval (the poll republishes
  // runningScansByScope and its transition detector owns completion toasts),
  // plus a DataInvalidate so coverage counts reconcile after any terminal
  // outcome — completed, failed, or cancelled alike.
  eventSource.addEventListener("scan:done", () => {
    void pollStatus();
    emit(BusEvent.DataInvalidate);
  });

  eventSource.addEventListener("error", () => {
    if (eventSource?.readyState === EventSource.CLOSED) {
      eventSource = null;
      if (!reconnectTimer) {
        const base = SSE_RECONNECT_MS * Math.pow(2, reconnectAttempt);
        const jitter = Math.random() * SSE_RECONNECT_MS;
        const delay = Math.min(base + jitter, SSE_MAX_RECONNECT_MS);
        reconnectAttempt++;
        reconnectTimer = setTimeout(() => {
          reconnectTimer = null;
          connect();
        }, delay);
      }
    }
  });
}

function disconnect(): void {
  if (eventSource) {
    eventSource.close();
    eventSource = null;
  }
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  reconnectAttempt = 0;
  abortPoll();
}

// Pause SSE when the tab is hidden to save server resources and battery.
// Debounce reconnect on visible to avoid flapping on quick tab switches.
let visibilityTimer: ReturnType<typeof setTimeout> | null = null;

document.addEventListener("visibilitychange", () => {
  if (visibilityTimer) {
    clearTimeout(visibilityTimer);
    visibilityTimer = null;
  }
  if (document.hidden) {
    disconnect();
  } else {
    visibilityTimer = setTimeout(() => {
      visibilityTimer = null;
      connect();
    }, VISIBILITY_DEBOUNCE_MS);
  }
});
