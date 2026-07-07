// @vitest-environment happy-dom
import { describe, it, vi, beforeEach } from "vitest";

vi.mock("./api-client.js", () => ({
  apiGet: vi.fn().mockResolvedValue(null),
  apiGetTyped: vi.fn().mockResolvedValue(null),
}));
vi.mock("@cplieger/actions", () => ({
  apiAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue(null) })),
  defineAction: vi.fn(() => ({ dispatch: vi.fn().mockResolvedValue(null) })),
  retryNetwork: vi.fn((fn: unknown) => fn),
  RETRY_STANDARD: {},
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./wire/decoders.gen.js", () => ({
  decodeStats: vi.fn((v: unknown) => v),
  decodeProvidersResponse: vi.fn((v: unknown) => v),
}));

import * as store from "./store.js";

describe("status: renderPopup", () => {
  beforeEach(() => {
    document.body.innerHTML = `
      <button id="statusBtn"></button>
      <div id="statusPopup" popover></div>
    `;
    store.set("config", null);
    store.set("configChecked", true);
    store.set("isUnconfigured", false);
  });

  it.todo("renders 'All clear' when no activities, alerts, or provider issues");

  it.todo("renders activity items keyed by act-{id}");

  it.todo("filters out Manual Search and Manual Download activities");

  it.todo("renders provider error items for timed-out providers");

  it.todo("renders embedded detection error when present");

  it.todo("renders alert items with dismiss button");

  it.todo("renders scan timing when not active and last_scan exists");

  it.todo("reconcile preserves existing activity DOM nodes on re-render");

  it.todo("dismissing an activity hides it optimistically");
});

describe("status: updateStatusButton", () => {
  it.todo("shows warning icon when alerts exist");

  it.todo("shows dot icon when activities are in progress");

  it.todo("clears icon when no alerts and no activities");
});
