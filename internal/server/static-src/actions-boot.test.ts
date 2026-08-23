// actions-boot.test.ts — the one-call wiring between @cplieger/actions and
// subflux's own layers.
//
// Nothing else asserts this seam, and both halves of it fail silently: an
// unconfigured notifier makes the framework DROP every success and error toast
// (its documented behaviour), and a missing `credentials` makes every action's
// fetch omit the session cookie, so every dispatch 401s. Asserting that the
// callbacks were merely PASSED would prove neither, so the tests capture them
// and invoke them.
import { describe, it, expect, beforeEach, vi } from "vitest";
import type { Notifier } from "@cplieger/actions";
import { initActions } from "./actions-boot.js";

// Both mocked modules are imported for their VALUES, so the doubles live in
// vi.hoisted records (a factory is hoisted above module-level consts) and the
// factories are plain functions, which mockReset cannot strip.
const framework = vi.hoisted(() => ({
  notifiers: [] as Notifier[],
  apiConfigs: [] as unknown[],
}));
vi.mock("@cplieger/actions", () => ({
  configure: (notifier: Notifier) => {
    framework.notifiers.push(notifier);
  },
  configureApi: (config: unknown) => {
    framework.apiConfigs.push(config);
  },
}));

const toasts = vi.hoisted(() => ({
  successes: [] as string[],
  errors: [] as { message: string; retry: unknown }[],
}));
vi.mock("./notify.js", () => ({
  success: (message: string) => {
    toasts.successes.push(message);
  },
  error: (message: string, retry?: unknown) => {
    toasts.errors.push({ message, retry });
  },
}));

beforeEach(() => {
  framework.notifiers.length = 0;
  framework.apiConfigs.length = 0;
  toasts.successes.length = 0;
  toasts.errors.length = 0;
});

describe("initActions", () => {
  it("installs a notifier that forwards a success message to the toast layer", () => {
    initActions();

    const notifier = framework.notifiers[0];
    notifier?.success?.("Scan started");

    expect(toasts.successes).toStrictEqual(["Scan started"]);
  });

  it("installs a notifier that forwards an error message with its retry descriptor", () => {
    initActions();
    const retry = { onClick: vi.fn() };

    framework.notifiers[0]?.error?.("Download failed", retry);

    // The retry descriptor has to survive the hop: it is what renders the
    // Retry button on a retryable action's error toast.
    expect(toasts.errors).toStrictEqual([{ message: "Download failed", retry }]);
  });

  it("forwards an error with no retry as an error with no retry", () => {
    initActions();

    framework.notifiers[0]?.error?.("Save failed");

    expect(toasts.errors).toStrictEqual([{ message: "Save failed", retry: undefined }]);
  });

  it("configures the action API to send same-origin credentials", () => {
    initActions();

    // Every subflux API call is session-cookie authenticated, so omitting this
    // would 401 each dispatch.
    expect(framework.apiConfigs).toStrictEqual([{ credentials: "same-origin" }]);
  });
});
