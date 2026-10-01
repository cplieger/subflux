// Its own file rather than a third case beside login.wizard.test.ts: the mock
// registry is per file, and there ./wizard.js loads while here it must not.
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type * as ClientGen from "./wire/client.gen.js";
import { SETUP_PATH } from "./constants.js";

const wire = vi.hoisted(() => ({
  setup: {
    setup_required: false,
    config_valid: false,
    passkey_login_available: false,
  } as Record<string, unknown>,
  who: { username: "root", role: "admin" } as unknown,
}));
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<typeof ClientGen>()),
  authSetupStatus: () => Promise.resolve(wire.setup),
  me: () => Promise.resolve(wire.who),
}));

// The chunk that could not be delivered, staged at the browser's own module
// loader: wizard.js statically imports navigateToApp from ./nav-app.js, so a
// mock that provides no such export makes the dynamic import REJECT the way a
// missing or truncated chunk does. A factory that throws is not usable for
// this — vitest reports it as a harness error and aborts the file before any
// test runs (measured on vitest 5.0.1, browser mode).
vi.mock("./nav-app.js", () => ({}));

function mountLoginPage(): void {
  const main = document.createElement("main");
  main.id = "loginPage";
  main.className = "auth-page";
  main.hidden = true;
  const error = document.createElement("div");
  error.id = "loginError";
  error.hidden = true;
  const form = document.createElement("form");
  form.id = "loginForm";
  main.append(error, form);
  document.body.replaceChildren(main);
}

let bootCount = 0;

async function bootLoginPage(): Promise<void> {
  bootCount++;
  await import(/* @vite-ignore */ `./login.ts?boot=${bootCount}`);
  for (let i = 0; i < 4; i++) {
    await new Promise((r) => setTimeout(r, 0));
  }
}

function el(id: string): HTMLElement {
  const found = document.getElementById(id);
  if (found === null) {
    throw new Error(`missing #${id}`);
  }
  return found;
}

const runnerPath = window.location.pathname + window.location.search;

beforeEach(() => {
  vi.stubGlobal("fetch", () => Promise.resolve(new Response(null, { status: 404 })));
  mountLoginPage();
});

afterEach(() => {
  history.replaceState(null, "", runnerPath);
});

describe("login page: the wizard chunk does not load", () => {
  it("reports the failure on the sign-in page and says to reload", async () => {
    history.replaceState(null, "", SETUP_PATH);

    await bootLoginPage();

    await vi.waitFor(() => {
      expect(el("loginError").textContent).toBe(
        "The setup wizard could not be loaded. Reload the page to continue.",
      );
    });
    expect(el("loginPage").hidden).toBe(false);
  });
});
