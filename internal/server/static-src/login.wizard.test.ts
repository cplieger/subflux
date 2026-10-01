// THE CASE ORDER IS LOAD-BEARING. A vi.mock factory runs at the mocked module's
// first import and the module is cached for the whole file, whatever ?boot=N does
// to login.ts — so the case asserting ZERO chunk loads comes FIRST and a reorder
// fails loud (it would read 1). Nothing here sets sequence.shuffle.
//
// login.ts runs init() at import, so each case imports through a busted specifier:
// vi.resetModules() does not re-evaluate a module in the browser's URL-keyed
// module map, and each case needs its own module state.
import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import type * as ClientGen from "./wire/client.gen.js";
import { SETUP_PATH } from "./constants.js";

const wire = vi.hoisted(() => ({
  setup: {
    setup_required: false,
    config_valid: true,
    passkey_login_available: false,
  } as Record<string, unknown>,
  who: null as unknown,
}));
vi.mock("./wire/client.gen.js", async (importOriginal) => ({
  ...(await importOriginal<typeof ClientGen>()),
  authSetupStatus: () => Promise.resolve(wire.setup),
  me: () => Promise.resolve(wire.who),
}));

const wiz = vi.hoisted(() => ({ evaluated: 0, startConfigWizard: vi.fn() }));
vi.mock("./wizard.js", () => {
  wiz.evaluated += 1;
  return { startConfigWizard: wiz.startConfigWizard };
});

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
  const notice = document.createElement("main");
  notice.id = "setupNoticePage";
  notice.className = "auth-page";
  notice.hidden = true;
  main.append(error, form);
  document.body.replaceChildren(main, notice);
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
  wire.setup = { setup_required: false, config_valid: true, passkey_login_available: false };
  wire.who = null;
  vi.stubGlobal("fetch", () => Promise.resolve(new Response(null, { status: 404 })));
  mountLoginPage();
});

afterEach(() => {
  // The runner's own document is shared across cases, and case 2 rewrites the
  // path the wizard hand-off is keyed on.
  history.replaceState(null, "", runnerPath);
});

describe("login page: the setup wizard's chunk", () => {
  it("never loads the wizard on a configured sign-in", async () => {
    await bootLoginPage();

    await vi.waitFor(() => {
      expect(el("loginPage").hidden).toBe(false);
    });
    expect(wiz.evaluated).toBe(0);
  });

  it("loads and starts the wizard when /setup is entered on an admin session", async () => {
    history.replaceState(null, "", SETUP_PATH);
    wire.setup = { setup_required: false, config_valid: false, passkey_login_available: false };
    wire.who = { username: "root", role: "admin" };

    await bootLoginPage();

    // The chunk is a real fetch here (the mocked module is served over the
    // browser mocker's route), so the hand-off settles after the boot's ticks.
    await vi.waitFor(() => {
      expect(wiz.startConfigWizard).toHaveBeenCalledTimes(1);
    });
    expect(wiz.evaluated).toBe(1);
    expect(wiz.startConfigWizard).toHaveBeenCalledWith({ configValid: false });
  });
});
