// Typed event bus for cross-module navigation and actions, backed by
// @cplieger/reactive's createBus. Single source of truth for event names and
// payloads; breaks circular import chains between coverage, detail, router,
// and history. Events, not state — durable state lives in store.ts.

import { createBus } from "@cplieger/reactive";

import type { CoverageItem, SeriesItem, MovieDetail } from "./api-types.js";

export interface DetailConfig {
  title: string;
  info?: string;
  arrLink?: string | null;
  arrName?: string;
}

// Single-payload event map (one payload object per event). Events whose
// payload type is `undefined` are emitted with no payload argument.
interface EventMap {
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "open:series": { item: CoverageItem | SeriesItem; skipPush?: boolean };
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "open:movie": { item: CoverageItem | MovieDetail; skipPush?: boolean };
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "panel:configure": { visible: boolean; detail?: DetailConfig };
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "nav:route": string;
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "nav:history": string | undefined;
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "load:history": undefined;
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "scan:series": { item: CoverageItem | SeriesItem };
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "scan:movie": { item: CoverageItem | MovieDetail };
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
  "open:security": undefined;
  // Request a refresh of the current view.
  // deadset:ignore DS1003 -- Keyed by name through emit and on, destructured from createBus<EventMap>().
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
