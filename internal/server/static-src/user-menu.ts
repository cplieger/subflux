// user-menu.ts — User menu popover: username, security, settings, theme, logout.

import * as bus from "./bus.js";
import * as theme from "./theme.js";
import { el, icon } from "./dom.js";
import { apiGet, apiPost } from "./api-client.js";
import { openConfig } from "./config.js";
import type { MeResponse } from "./api-types.js";

// --- Inline interfaces for API response shapes ---

let userInfo: MeResponse | null = null;

export function initUserMenu(): void {
  void fetchMe();
  wireUserButton();
}

async function fetchMe(): Promise<void> {
  const data = await apiGet<MeResponse>("/api/auth/me");
  if (data) {
    userInfo = data;
    buildMenuContent();
  }
}

function wireUserButton(): void {
  const btn = document.getElementById("userBtn");
  if (!btn) {
    return;
  }

  // Config and theme controls are now inside the user menu popover.
  // Remove the standalone header buttons to avoid duplicate controls.
  document.getElementById("configBtn")?.remove();
  document.getElementById("themeBtn")?.remove();
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

  // Security link.
  items.push(
    menuItem("Security", "settings", () => {
      popup.hidePopover();
      bus.emit(bus.BusEvent.OpenSecurity);
    }),
  );

  // Settings link (admin only).
  if (userInfo?.role === "admin") {
    items.push(
      menuItem("Settings", "settings", () => {
        popup.hidePopover();
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

  // Logout.
  items.push(menuItem("Logout", "close", () => { void doLogout(); }));

  popup.replaceChildren(...items);
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

function resolveThemeLabel(): string {
  const active = document.documentElement.getAttribute("data-theme");
  return active === "dark" ? "Light mode" : "Dark mode";
}

function themeIcon(): string {
  const active = document.documentElement.getAttribute("data-theme");
  return active === "dark" ? "sun" : "moon";
}

async function doLogout(): Promise<void> {
  // Best-effort; redirect regardless of server response.
  await apiPost("/api/auth/logout");
  window.location.href = "/login";
}
