// sse-worker.ts — the SharedWorker owning the profile's one live-update
// stream (cursor, presence tag, hold-and-drain). cmd/bundle emits it as an
// IIFE under /chunks/ and injects the hashed URL into the page.

import { createVersionMap, createWorkerHost } from "@cplieger/sse";

declare const self: SharedWorkerGlobalScope;
// Injected by cmd/bundle from the generated Go path constants.
declare const __PATH_EVENTS__: string;
declare const __PATH_EVENTS_ALIVE__: string;

const host = createWorkerHost({
  url: __PATH_EVENTS__,
  alive: { url: __PATH_EVENTS_ALIVE__ },
  versions: createVersionMap(),
  // The tabs' stamps never reach this map, so the run is fanned without a
  // verdict and each tab digests what it holds (events.ts).
  revalidate: (ctx, tabs) => tabs.run(ctx),
});

self.onconnect = (event: MessageEvent) => {
  const port = event.ports[0];
  if (port !== undefined) {
    host.attach(port);
  }
};
