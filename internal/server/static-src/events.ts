// Server-Sent Events connection. Receives real-time updates from the
// server and dispatches them to the store and notification system.

import * as store from './store.js';
import * as notify from './notify.js';
import { patchCoverageBadge, fetchAndMergeCoverage } from './coverage.js';
import { pollStatus, abortPoll } from './status.js';
import { coverageMediaId } from './utils.js';
import { SSE_RECONNECT_MS, SSE_MAX_RECONNECT_MS, VISIBILITY_DEBOUNCE_MS } from './constants.js';
import type { CoverageItem } from './api-types.js';

// --- Typed SSE event payloads ---

interface CoverageEvent {
  data?: { media_id?: string };
  media_id?: string;
}

interface NotifyEvent {
  data?: { level: string; text: string };
}

interface ScanStartEvent {
  data?: { action?: string; detail?: string; source?: string; succeeded?: boolean };
}

function parseSSE<T>(e: MessageEvent): T | null {
  try {
    return JSON.parse(e.data as string) as T;
  } catch {
    return null;
  }
}

let eventSource: EventSource | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectAttempt = 0;

export function connect(): void {
  if (eventSource) return;

  eventSource = new EventSource('/api/events');

  eventSource.addEventListener('open', () => {
    reconnectAttempt = 0;
  });

  eventSource.addEventListener('coverage', (e: MessageEvent) => {
    const payload = parseSSE<CoverageEvent>(e);
    const mediaId = (payload?.data?.media_id || payload?.media_id || '') as string;

    if (mediaId && store.get('currentPage') === 'library'
        && !store.get('detailCtx')) {
      fetchAndMergeCoverage().then((fresh) => {
        const item = fresh.find((i: CoverageItem) => {
          const id = coverageMediaId(i);
          // Episode events use "tvdb-{id}-s01e01" while the series row
          // uses "tvdb-{id}". Use prefix match for series.
          if (i._type === 'series') {
            return mediaId.startsWith(id);
          }
          return id === mediaId;
        });
        if (item) {
          patchCoverageBadge(coverageMediaId(item), item.targets || []);
        }
      }).catch(() => {
        store.set('needsRefresh', true);
      });
      return;
    }
    store.set('needsRefresh', true);
  });

  eventSource.addEventListener('notify', (e: MessageEvent) => {
    const payload = parseSSE<NotifyEvent>(e);
    if (!payload) return;
    const msg = payload.data;
    if (!msg) return;
    if (msg.level === 'error') notify.error(msg.text || '');
    else if (msg.level === 'success') notify.success(msg.text || '');
    else notify.info(msg.text || '');
  });

  // scan:start is an ephemeral "scan started" nudge. The status button
  // already flips to "in progress" from the caller's click handler, so
  // we only surface a short toast — useful for scheduled scans the user
  // did not initiate.
  eventSource.addEventListener('scan:start', (e: MessageEvent) => {
    const payload = parseSSE<ScanStartEvent>(e);
    if (!payload) return;
    const data = payload.data;
    if (!data) return;
    const label = data.detail || data.action || 'Scan';
    notify.info(`Scan started: ${label}`);
  });

  // scan:done triggers an immediate status refresh so the button and
  // popup update without waiting up to one poll interval. The existing
  // pollStatus pipeline already handles completion toasts via its own
  // transition detector, so we do not surface a second toast here.
  eventSource.addEventListener('scan:done', () => {
    void pollStatus();
  });

  eventSource.addEventListener('error', () => {
    if (eventSource && eventSource.readyState === EventSource.CLOSED) {
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

export function disconnect(): void {
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

document.addEventListener('visibilitychange', () => {
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
