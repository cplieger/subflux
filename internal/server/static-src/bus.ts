// Typed event bus for cross-module navigation and actions, backed by
// @cplieger/reactive's createBus. Single source of truth for event names and
// payloads; breaks circular import chains between coverage, detail, router,
// and history. Events, not state — durable state lives in store.ts.

import { createBus } from "@cplieger/reactive";

import type { CoverageItem, SeriesItem, MovieDetail } from "./api-types.js";

export interface DetailConfig {
  title: string;
  info?: string;
  backPath?: string;
  arrLink?: string | null;
  arrName?: string;
  historyAction?: (() => void) | null;
  filesAction?: (() => void) | null;
}

// Single-payload event map (one payload object per event). Events whose
// payload type is `undefined` are emitted with no payload argument.
interface EventMap {
  "open:series": { item: CoverageItem | SeriesItem; skipPush?: boolean };
  "open:movie": { item: CoverageItem | MovieDetail; skipPush?: boolean };
  "panel:configure": { visible: boolean; detail?: DetailConfig };
  "nav:route": string;
  "nav:history": string | undefined;
  "load:history": undefined;
  "scan:series": { item: CoverageItem | SeriesItem };
  "scan:movie": { item: CoverageItem | MovieDetail };
  "open:security": undefined;
  // Request a refresh of the current view (replaces the old needsRefresh
  // store pulse — this is an event, not state).
  "data:invalidate": undefined;
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
  DataInvalidate: "data:invalidate",
} as const;

const bus = createBus<EventMap>();

export const { on, emit } = bus;
