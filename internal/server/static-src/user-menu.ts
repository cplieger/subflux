// user-menu.ts — User menu popover: username, security, settings, theme, logout.

import { apiAction } from "@cplieger/actions";
import * as bus from "./bus.js";
import * as theme from "./theme.js";
import { el, icon } from "./dom.js";
import { me, PATH_LOGOUT } from "./wire/client.gen.js";
import { openConfig } from "./config.js";
import * as store from "./store.js";
import type { MeResponse } from "./api-types.js";
import { createMenuPopover, type MenuPopover } from "./popover-menu.js";
import { rovingFocus, type RovingFocusController } from "@cplieger/ui-primitives/roving-focus";

// --- Inline interfaces for API response shapes ---

let userInfo: MeResponse | null = null;

// The user menu is a @cplieger/ui-primitives popover anchored to the user
// button (replacing the native Popover API). Held here so the menu items and
// the open-rebuild hook can drive it.
let menuPopover: MenuPopover | null = null;

// The WAI-ARIA menu keyboard contract (roving focus over the role="menuitem"
// entries, Home/End, Enter/Space activation) is the library's roving-focus
// primitive — wired once on the panel; items are queried live, so the
// per-open rebuild needs only refresh() + focusFirst().
let menuNav: RovingFocusController | null = null;

export function initUserMenu(): void {
  void fetchMe();
  wireUserButton();
}

async function fetchMe(): Promise<void> {
  const data = await me();
  if (data) {
    userInfo = data;
    store.set("isAdmin", data.role === "admin");
    buildMenuContent();
  }
}

// requires @cplieger/ui-primitives >= 2.1.0 (popover stretch mode); verified
// locally via a node_modules overlay until released.
function wireUserButton(): void {
  const btn = document.getElementById("userBtn");
  const popup = document.getElementById("userMenuPopup");
  if (!btn || !popup) {
    return;
  }

  // Config and theme controls are now inside the user menu popover.
  // Remove the standalone header buttons to avoid duplicate controls.
  document.getElementById("configBtn")?.remove();
  document.getElementById("themeBtn")?.remove();

  // Rebuild menu content each time the popover opens (onOpen) so the theme
  // label/icon reflect the live data-theme. Auto-mode (matchMedia) can flip
  // data-theme while the menu is closed, leaving a stale label. haspopup:
  // "menu" matches the panel's role="menu".
  menuPopover = createMenuPopover(btn, popup, {
    haspopup: "menu",
    onOpen: buildMenuContent,
  });
  menuNav = rovingFocus(popup, ".um-item");
  btn.addEventListener("click", () => menuPopover?.toggle());
}

function buildMenuContent(): void {
  const popup = document.getElementById("userMenuPopup");
  if (!popup) {
    return;
  }

  const items: HTMLElement[] = [];

  // Username display (non-interactive).
  if (userInfo) {
    items.push(
      el(
        "div",
        { className: "um-user", role: "none" },
        el("span", { className: "um-name" }, userInfo.username),
      ),
    );
  }

  // Security link. Dedicated shield glyph: sharing the Settings gear made
  // the two adjacent items visually identical.
  items.push(
    menuItem("Security", "shield", () => {
      menuPopover?.hide();
      bus.emit(bus.BusEvent.OpenSecurity);
    }),
  );

  // Settings link (admin only).
  if (userInfo?.role === "admin") {
    items.push(
      menuItem("Settings", "settings", () => {
        menuPopover?.hide();
        openConfig();
      }),
    );
  }

  // Theme toggle.
  const themeLabel = resolveThemeLabel();
  items.push(
    menuItem(
      themeLabel,
      themeIcon(),
      () => {
        theme.cycle();
        // Update the label after cycling.
        const label = popup.querySelector(".um-theme-label");
        if (label) {
          label.textContent = resolveThemeLabel();
        }
        const ic = popup.querySelector(".um-theme-icon");
        if (ic) {
          ic.textContent = "";
          ic.appendChild(icon(themeIcon()));
        }
      },
      "um-theme-label",
      "um-theme-icon",
    ),
  );

  // Logout. Dedicated sign-out glyph: the close/X icon means
  // error/dismiss everywhere else in the app.
  items.push(
    menuItem("Logout", "logout", () => {
      void doLogout();
    }),
  );

  popup.replaceChildren(...items);

  // The panel announces itself as role="menu", so it must honor the menu
  // interaction contract — the roving-focus primitive owns it (wired once in
  // wireUserButton). After the rebuild, restore the single-Tab-stop invariant
  // on the fresh items and focus the first one (a no-op while the popover is
  // still hidden, e.g. the initial fetchMe build).
  menuNav?.refresh();
  menuNav?.focusFirst();
}

function menuItem(
  label: string,
  iconName: string,
  onclick: () => void,
  labelClass?: string,
  iconClass?: string,
): HTMLElement {
  const labelEl = el("span", labelClass ? { className: labelClass } : null, label);
  const iconEl = el(
    "span",
    { className: iconClass ? `um-icon ${iconClass}` : "um-icon" },
    icon(iconName),
  );
  return el(
    "button",
    {
      type: "button",
      className: "um-item",
      role: "menuitem",
      onclick,
    },
    iconEl,
    labelEl,
  );
}

// The theme item is labeled with what clicking it switches TO (the next stop
// in the library's light -> dark -> system cycle), keyed off the STORED
// choice so "system" is representable (the resolved data-theme attribute can
// never distinguish system-dark from pinned dark).
function resolveThemeLabel(): string {
  switch (theme.choice()) {
    case "light":
      return "Dark mode";
    case "dark":
      return "System theme";
    default:
      return "Light mode";
  }
}

function themeIcon(): string {
  switch (theme.choice()) {
    case "light":
      return "moon";
    case "dark":
      return "monitor";
    default:
      return "sun";
  }
}

/** Logout. Best-effort: the redirect below runs regardless of the server
 *  response, so there is no success toast, no error toast (error: false),
 *  and no retry. dedupe protects against a double-click firing two logouts
 *  mid-redirect (no args ⇒ constant key). */
const logoutAction = apiAction<undefined>({
  name: "auth.logout",
  request: () => ({ method: "POST", path: PATH_LOGOUT }),
  dedupe: true, // double-click protection
  error: false, // best-effort; redirect happens regardless of outcome
});

async function doLogout(): Promise<void> {
  // Best-effort; redirect regardless of server response. The apiAction
  // dispatch resolves (null on failure) rather than rejecting, so the
  // redirect always runs; identical semantics to the previous apiPost.
  await logoutAction.dispatch(undefined);
  window.location.href = "/login";
}
