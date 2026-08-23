// user-menu.test.ts — the header user menu.
//
// Mostly wiring, with two pieces that carry real risk. The theme item is
// labelled with the stop it switches TO and keyed off the STORED choice, not
// the resolved data-theme attribute, because a resolved attribute cannot
// distinguish system-dark from pinned dark — get that wrong and the item lies
// about what it does. And the panel announces role="menu", so it owes the
// WAI-ARIA menu contract; the roving-focus primitive is left REAL here so the
// single-Tab-stop invariant is actually exercised rather than mocked away.
//
// One thing is deliberately never done: the Logout item is never clicked.
// doLogout assigns window.location.href, which cannot be stubbed in a real
// browser (window and location are non-configurable) and would reload the test
// runner's own iframe, failing the whole file. Its presence is asserted, its
// activation is not.
import { describe, it, expect, beforeEach, vi } from "vitest";
import * as bus from "./bus.js";
import * as store from "./store.js";
import { initUserMenu } from "./user-menu.js";
import type { MeResponse } from "./api-types.js";

const wire = vi.hoisted(() => ({
  me: null as MeResponse | null,
  meCalls: 0,
}));
vi.mock("./wire/client.gen.js", () => ({
  me: () => {
    wire.meCalls++;
    return Promise.resolve(wire.me);
  },
  PATH_LOGOUT: "/api/auth/logout",
}));

// logoutAction is built at module scope; the double only has to exist, because
// no test dispatches it (see the header).
vi.mock("@cplieger/actions", () => ({
  apiAction: () => ({ dispatch: () => Promise.resolve(null), cancel: () => undefined }),
}));

const menu = vi.hoisted(() => ({
  options: null as { haspopup?: string; onOpen?: () => void } | null,
  calls: [] as string[],
}));
vi.mock("./popover-menu.js", () => ({
  createMenuPopover: (_anchor: unknown, _panel: unknown, opts: unknown) => {
    menu.options = opts as { haspopup?: string; onOpen?: () => void };
    return {
      toggle: () => menu.calls.push("toggle"),
      hide: () => menu.calls.push("hide"),
      isOpen: false,
      reposition: () => menu.calls.push("reposition"),
      dispose: () => menu.calls.push("dispose"),
    };
  },
}));

const themeState = vi.hoisted(() => ({
  choice: "system" as "light" | "dark" | "system",
  cycles: 0,
}));
vi.mock("./theme.js", () => ({
  choice: () => themeState.choice,
  cycle: () => {
    themeState.cycles++;
  },
  init: () => undefined,
}));

const config = vi.hoisted(() => ({ opens: 0 }));
vi.mock("./config.js", () => ({
  openConfig: () => {
    config.opens++;
  },
}));

/** The header markup app.ts ships: the trigger, the menu panel, and the two
 *  standalone controls the menu replaces. */
function mountHeader(): void {
  document.body.innerHTML = `
    <header>
      <button type="button" id="configBtn">cfg</button>
      <button type="button" id="themeBtn">thm</button>
      <button type="button" id="userBtn">user</button>
      <div id="userMenuPopup" role="menu"></div>
    </header>`;
}

function items(): HTMLButtonElement[] {
  return [...document.querySelectorAll<HTMLButtonElement>("#userMenuPopup .um-item")];
}

function labels(): string[] {
  return items().map((i) => i.textContent ?? "");
}

function itemNamed(label: string): HTMLButtonElement {
  const found = items().find((i) => i.textContent === label);
  if (!found) {
    throw new Error(`no menu item ${label}; have ${labels().join(", ")}`);
  }
  return found;
}

function user(role: "admin" | "user"): MeResponse {
  return { username: "cplieger", role } as MeResponse;
}

/** initUserMenu kicks off an un-awaited fetch; let it land. */
async function boot(): Promise<void> {
  initUserMenu();
  await Promise.resolve();
  await Promise.resolve();
}

beforeEach(() => {
  wire.me = user("admin");
  wire.meCalls = 0;
  menu.options = null;
  menu.calls.length = 0;
  themeState.choice = "system";
  themeState.cycles = 0;
  config.opens = 0;
  store.set("isAdmin", false);
  mountHeader();
});

describe("initUserMenu", () => {
  it("publishes admin-ness from /me so the rest of the app can gate on it", async () => {
    await boot();

    expect(wire.meCalls).toBe(1);
    expect(store.get("isAdmin")).toBe(true);
  });

  it("publishes a non-admin as not admin", async () => {
    wire.me = user("user");

    await boot();

    expect(store.get("isAdmin")).toBe(false);
  });

  it("leaves the menu empty when /me fails", async () => {
    wire.me = null;

    await boot();

    expect(items()).toHaveLength(0);
  });

  it("removes the standalone config and theme buttons it replaces", async () => {
    await boot();

    expect(document.getElementById("configBtn")).toBeNull();
    expect(document.getElementById("themeBtn")).toBeNull();
  });

  it("wires the popover as a menu and rebuilds its content on open", async () => {
    await boot();

    expect(menu.options?.haspopup).toBe("menu");
    expect(menu.options?.onOpen).toBeTypeOf("function");
  });

  it("toggles the popover from the user button", async () => {
    await boot();

    document.getElementById("userBtn")?.click();

    expect(menu.calls).toStrictEqual(["toggle"]);
  });

  it("does nothing when the header has no user button", async () => {
    document.body.replaceChildren();

    await boot();

    // No popover wired, and no throw — login.html has no user menu.
    expect(menu.options).toBeNull();
  });
});

describe("user menu content", () => {
  it("lists the username, Security, Settings, the theme and Logout for an admin", async () => {
    await boot();

    expect(document.querySelector("#userMenuPopup .um-name")?.textContent).toBe("cplieger");
    expect(labels()).toStrictEqual(["Security", "Settings", "Light mode", "Logout"]);
  });

  it("omits Settings for a non-admin", async () => {
    wire.me = user("user");

    await boot();

    expect(labels()).toStrictEqual(["Security", "Light mode", "Logout"]);
  });

  it("marks the username row non-interactive so the menu contract stays honest", async () => {
    await boot();

    // role="none" keeps a non-focusable div out of the role="menu" item set.
    expect(document.querySelector("#userMenuPopup .um-user")?.getAttribute("role")).toBe("none");
  });

  it("gives every actionable row role=menuitem", async () => {
    await boot();

    expect(items().map((i) => i.getAttribute("role"))).toStrictEqual([
      "menuitem",
      "menuitem",
      "menuitem",
      "menuitem",
    ]);
  });

  it("leaves exactly one Tab stop across the items", async () => {
    await boot();

    // The roving-focus primitive owns arrow-key navigation, which is only a
    // real menu if Tab enters the panel once.
    const stops = items().filter((i) => i.tabIndex === 0);
    expect(stops).toHaveLength(1);
  });

  it("rebuilds on open so a theme flipped while closed is not stale", async () => {
    await boot();
    expect(labels()).toContain("Light mode");

    themeState.choice = "light";
    menu.options?.onOpen?.();

    expect(labels()).toContain("Dark mode");
  });

  it("renders a Logout row (never activated here — see the file header)", async () => {
    await boot();

    expect(itemNamed("Logout").querySelector(".icon-logout")).not.toBeNull();
  });
});

describe("user menu actions", () => {
  it("closes the menu and emits the security event", async () => {
    await boot();
    const seen: string[] = [];
    const off = bus.on(bus.BusEvent.OpenSecurity, () => {
      seen.push("open:security");
    });

    itemNamed("Security").click();
    off();

    expect(menu.calls).toStrictEqual(["hide"]);
    expect(seen).toStrictEqual(["open:security"]);
  });

  it("closes the menu and opens the settings drawer", async () => {
    await boot();

    itemNamed("Settings").click();

    expect(menu.calls).toStrictEqual(["hide"]);
    expect(config.opens).toBe(1);
  });

  it("uses a dedicated shield glyph for Security, not the settings gear", async () => {
    // The two rows are adjacent; sharing a glyph made them visually identical.
    await boot();

    expect(itemNamed("Security").querySelector(".icon-shield")).not.toBeNull();
    expect(itemNamed("Settings").querySelector(".icon-settings")).not.toBeNull();
  });
});

describe("theme item", () => {
  const cases = [
    { choice: "light" as const, label: "Dark mode", glyph: "icon-moon" },
    { choice: "dark" as const, label: "System theme", glyph: "icon-monitor" },
    { choice: "system" as const, label: "Light mode", glyph: "icon-sun" },
  ];

  for (const tc of cases) {
    it(`labels the ${tc.choice} choice with the next stop, ${tc.label}`, async () => {
      themeState.choice = tc.choice;

      await boot();

      const item = itemNamed(tc.label);
      expect(item.querySelector(`.${tc.glyph}`)).not.toBeNull();
    });
  }

  it("cycles the theme and relabels itself in place", async () => {
    themeState.choice = "light";
    await boot();

    // The click cycles; the module then re-reads the stored choice, so the
    // label must follow without a rebuild.
    themeState.choice = "dark";
    itemNamed("Dark mode").click();

    expect(themeState.cycles).toBe(1);
    expect(document.querySelector(".um-theme-label")?.textContent).toBe("System theme");
    expect(document.querySelector(".um-theme-icon .icon-monitor")).not.toBeNull();
  });
});
