// Lightweight event bus for decoupling cross-module navigation
// and actions. Breaks circular import chains between coverage,
// detail, router, and history modules.

import type { CoverageItem, SeriesItem, MovieDetail } from "./api-types.js";

// --- Typed event map: single source of truth for event names and payloads ---

export interface DetailConfig {
  title: string;
  info?: string;
  backPath?: string;
  arrLink?: string | null;
  arrName?: string;
  historyAction?: (() => void) | null;
  filesAction?: (() => void) | null;
}

export interface EventMap {
  "open:series": [item: CoverageItem | SeriesItem, skipPush?: boolean];
  "open:movie": [item: CoverageItem | MovieDetail, skipPush?: boolean];
  "panel:configure": [visible: boolean, detail?: DetailConfig];
  "nav:route": [path: string];
  "nav:history": [filter?: string];
  "load:history": [];
  "scan:series": [item: CoverageItem | SeriesItem, btn: HTMLButtonElement | null];
  "scan:movie": [item: CoverageItem | MovieDetail, btn: HTMLButtonElement | null];
  "open:security": [];
}

// Event name constants — use these instead of string literals.
export const BusEvent = {
  OpenSeries: "open:series",
  OpenMovie: "open:movie",
  PanelConfigure: "panel:configure",
  NavRoute: "nav:route",
  NavHistory: "nav:history",
  LoadHistory: "load:history",
  ScanSeries: "scan:series",
  ScanMovie: "scan:movie",
  OpenSecurity: "open:security",
} as const;

type Listener<K extends keyof EventMap> = (...args: EventMap[K]) => void;

const listeners = new Map<string, ((...args: unknown[]) => void)[]>();

// Subscribe to an event. Returns an unsubscribe function.
export function on<K extends keyof EventMap>(event: K, fn: Listener<K>): () => void {
  if (!listeners.has(event)) {
    listeners.set(event, []);
  }
  // eslint-disable-next-line @typescript-eslint/no-non-null-assertion -- guaranteed by has() check above
  listeners.get(event)!.push(fn as (...args: unknown[]) => void);
  return () => {
    const list = listeners.get(event);
    if (list) {
      listeners.set(
        event,
        list.filter((f) => f !== fn),
      );
    }
  };
}

// Emit an event to all subscribers.
export function emit<K extends keyof EventMap>(event: K, ...args: EventMap[K]): void {
  for (const fn of listeners.get(event) ?? []) {
    try {
      fn(...args);
    } catch (e) {
      console.error("bus listener error:", e);
    }
  }
}
