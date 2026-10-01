// config-banner.ts — the settings form's one error banner. A permanent empty live
// region whose ONE child is the message: the region exists before any mutation that
// gets announced, and an empty region paints nothing because the box (.cfg-banner)
// belongs to the message. No `hidden`, no class on the host.

import { el } from "./dom.js";
import type { ConnTestBanner } from "./conn-test.js";

export const configBannerHost: HTMLElement = el("div", { role: "alert" });

export const configBanner: ConnTestBanner = {
  show: (msg) => {
    configBannerHost.replaceChildren(el("div", { className: "cfg-banner" }, msg));
  },
  hide: () => {
    configBannerHost.replaceChildren();
  },
  text: () => configBannerHost.firstElementChild?.textContent ?? "",
};
