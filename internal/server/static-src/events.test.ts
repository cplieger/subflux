// @vitest-environment happy-dom
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

vi.mock("./store.js", () => ({
  get: vi.fn(),
  set: vi.fn(),
}));
vi.mock("./notify.js", () => ({ error: vi.fn(), success: vi.fn(), info: vi.fn() }));
vi.mock("./coverage.js", () => ({
  patchCoverageBadge: vi.fn(),
  fetchAndMergeCoverage: vi.fn().mockResolvedValue([]),
}));
vi.mock("./status.js", () => ({ pollStatus: vi.fn(), abortPoll: vi.fn() }));
vi.mock("./actions/index.js", () => ({ registerCleanup: vi.fn() }));

describe("events: SSE connection", () => {
  it.todo("connect creates EventSource to /api/events");

  it.todo("disconnect closes EventSource and clears timers");

  it.todo("reconnects with exponential backoff on error");

  it.todo("resets reconnect attempt counter on successful open");

  it.todo("disconnects on visibilitychange hidden");

  it.todo("reconnects with debounce on visibilitychange visible");
});

describe("events: SSE handlers", () => {
  it.todo("coverage event triggers fetchAndMergeCoverage when on library page");

  it.todo("coverage event sets needsRefresh when on detail page");

  it.todo("notify event calls notify.error for error level");

  it.todo("notify event calls notify.info for info level");

  it.todo("scan:start shows info toast with label");

  it.todo("scan:done triggers pollStatus");
});
