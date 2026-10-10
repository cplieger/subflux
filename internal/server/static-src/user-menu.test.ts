// user-menu.test.ts — the header user menu.
//
// The theme item names the STORED choice, not the resolved data-theme, which
// cannot tell system-dark from pinned dark. The roving-focus primitive stays
// REAL so the menu's single-Tab-stop invariant is exercised. The Logout item is
// never clicked: doLogout assigns window.location.href, which cannot be stubbed
// in a real browser and would reload the runner's own iframe.
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

// panel is the module's own menu node, handed to the popover once per init and
// kept across tests, because the module creates it once at import.
const menu = vi.hoisted(() => ({
  options: null as { haspopup?: string; onOpen?: () => void } | null,
  calls: [] as string[],
  panel: null as HTMLElement | null,
}));
vi.mock("./popover-menu.js", () => ({
  createMenuPopover: (_anchor: unknown, panel: unknown, opts: unknown) => {
    menu.panel = panel as HTMLElement;
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

// doLogout's first call (see the header: never activated here); the seam
// itself is pinned in events.worker.test.ts.
vi.mock("./events.js", () => ({
  disconnectForLogout: vi.fn(),
}));

/** The header markup app.ts ships: the trigger alone. The menu panel is
 *  user-menu.ts's own node and never in the document, so the fixture empties the
 *  one the module handed its popover instead of re-authoring it. */
function mountHeader(): void {
  document.body.innerHTML = `
    <header>
      <button type="button" id="userBtn">user</button>
    </header>`;
  menu.panel?.replaceChildren();
}

function panel(): HTMLElement {
  if (!menu.panel) {
    throw new Error("initUserMenu has not handed its panel to the popover");
  }
  return menu.panel;
}

function items(): HTMLButtonElement[] {
  return [...panel().querySelectorAll<HTMLButtonElement>(".um-item")];
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

    expect(panel().querySelector(".um-name")?.textContent).toBe("cplieger");
    expect(labels()).toStrictEqual(["Security", "Settings", "System theme", "Logout"]);
  });

  it("omits Settings for a non-admin", async () => {
    wire.me = user("user");

    await boot();

    expect(labels()).toStrictEqual(["Security", "System theme", "Logout"]);
  });

  it("marks the username row non-interactive so the menu contract stays honest", async () => {
    await boot();

    // role="none" keeps a non-focusable div out of the role="menu" item set.
    expect(panel().querySelector(".um-user")?.getAttribute("role")).toBe("none");
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

  it("rebuilds on open from the stored theme choice", async () => {
    await boot();
    expect(labels()).toContain("System theme");

    themeState.choice = "light";
    menu.options?.onOpen?.();

    expect(labels()).toContain("Light theme");
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
    {
      choice: "light" as const,
      label: "Light theme",
      glyph: "icon-sun",
      name: "Theme: Light, activate to switch",
      tip: "Switch to dark theme",
    },
    {
      choice: "dark" as const,
      label: "Dark theme",
      glyph: "icon-moon",
      name: "Theme: Dark, activate to switch",
      tip: "Switch to system theme",
    },
    {
      choice: "system" as const,
      label: "System theme",
      glyph: "icon-monitor",
      name: "Theme: System, activate to switch",
      tip: "Switch to light theme",
    },
  ];

  for (const tc of cases) {
    it(`names the current ${tc.choice} choice as ${tc.label}`, async () => {
      themeState.choice = tc.choice;

      await boot();

      const item = itemNamed(tc.label);
      expect(item.querySelector(`.${tc.glyph}`)).not.toBeNull();
      expect(item.getAttribute("aria-label")).toBe(tc.name);
      expect(item.getAttribute("data-tip")).toBe(tc.tip);
    });
  }

  it("cycles the theme on click and then names the new current choice", async () => {
    themeState.choice = "light";
    await boot();

    // The click cycles; the module then re-reads the stored choice, so the
    // item must follow without a rebuild.
    themeState.choice = "dark";
    itemNamed("Light theme").click();

    const item = itemNamed("Dark theme");
    expect(themeState.cycles).toBe(1);
    expect(item.querySelector(".um-theme-icon .icon-moon")).not.toBeNull();
    expect(item.querySelectorAll(".um-theme-icon .icon")).toHaveLength(1);
    expect(item.getAttribute("aria-label")).toBe("Theme: Dark, activate to switch");
    expect(item.getAttribute("data-tip")).toBe("Switch to system theme");
  });
});
